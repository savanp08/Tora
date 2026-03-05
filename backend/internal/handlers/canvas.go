package handlers

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/storage"

	"github.com/go-chi/chi/v5"
)

const (
	canvasWriteWait            = 10 * time.Second
	canvasPongWait             = 60 * time.Second
	canvasPingPeriod           = (canvasPongWait * 9) / 10
	canvasMaxMessageSize       = 2 * 1024 * 1024
	canvasRoomMaxClients       = 50
	canvasSnapshotReadTimeout  = 15 * time.Second
	canvasSnapshotWriteTimeout = 15 * time.Second

	canvasMessageSync           = 0
	canvasMessageQueryAwareness = 3
	canvasSyncStep2             = 1
)

var canvasAwarenessQueryPayload = []byte{canvasMessageQueryAwareness}

var canvasUpgrader = websocket.Upgrader{
	ReadBufferSize:    1024,
	WriteBufferSize:   1024,
	EnableCompression: false,
	CheckOrigin: func(r *http.Request) bool {
		// Dev-friendly default, aligned with existing socket origin policy.
		return true
	},
}

// DefaultCanvasManager is shared by the canvas websocket endpoint.
var DefaultCanvasManager = NewCanvasManager(nil, nil, nil)

// CanvasManager tracks one isolated CanvasHub per room.
type CanvasManager struct {
	mu          sync.RWMutex
	hubs        map[string]*CanvasHub
	redisStore  *database.RedisStore
	scyllaStore *database.ScyllaStore
	r2Client    *storage.R2Client
}

func NewCanvasManager(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
) *CanvasManager {
	return &CanvasManager{
		hubs:        make(map[string]*CanvasHub),
		redisStore:  redisStore,
		scyllaStore: scyllaStore,
		r2Client:    r2Client,
	}
}

func ConfigureCanvasPersistence(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
) {
	DefaultCanvasManager.configureStores(redisStore, scyllaStore, r2Client)
}

func (m *CanvasManager) configureStores(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redisStore = redisStore
	m.scyllaStore = scyllaStore
	m.r2Client = r2Client
}

func (m *CanvasManager) activeStores() (*database.RedisStore, *database.ScyllaStore, *storage.R2Client) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.redisStore, m.scyllaStore, m.r2Client
}

func (m *CanvasManager) getHub(roomID string) *CanvasHub {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hubs[roomID]
}

func (m *CanvasManager) getOrCreateHub(roomID string) *CanvasHub {
	m.mu.RLock()
	existing := m.hubs[roomID]
	m.mu.RUnlock()
	if existing != nil {
		return existing
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing = m.hubs[roomID]
	if existing != nil {
		return existing
	}

	hub := newCanvasHub(roomID, m)
	hub.loadCurrentSnapshot()
	m.hubs[roomID] = hub
	go hub.Run()
	return hub
}

func (m *CanvasManager) removeHub(roomID string, hub *CanvasHub) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if current, ok := m.hubs[roomID]; ok && current == hub {
		delete(m.hubs, roomID)
	}
}

type canvasSnapshotLookupResult struct {
	RoomName         string
	RedisHadSnapshot bool
	R2FetchAttempted bool
	Source           string
}

func roomNameFromRedisMeta(meta []interface{}) string {
	for _, candidate := range meta {
		switch typed := candidate.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				return trimmed
			}
		case []byte:
			trimmed := strings.TrimSpace(string(typed))
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func resolveCanvasRoomName(roomID string) string {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return roomID
	}
	redisStore, _, _ := DefaultCanvasManager.activeStores()
	if redisStore == nil || redisStore.Client == nil {
		return normalizedRoomID
	}

	ctx, cancel := context.WithTimeout(context.Background(), canvasSnapshotReadTimeout)
	defer cancel()
	meta, err := redisStore.Client.HMGet(ctx, roomKey(normalizedRoomID), "name", "name_lookup").Result()
	if err != nil || len(meta) == 0 {
		return normalizedRoomID
	}
	roomName := roomNameFromRedisMeta(meta)
	if roomName == "" {
		return normalizedRoomID
	}
	return roomName
}

// CanvasHub is an isolated router for one room's binary Yjs updates.
type CanvasHub struct {
	roomID  string
	manager *CanvasManager

	clientsMu  sync.RWMutex
	clients    map[*CanvasClient]struct{}
	register   chan *CanvasClient
	unregister chan *CanvasClient

	// broadcast intentionally carries raw []byte payloads only (no JSON envelope).
	broadcast chan []byte

	// sender channel is paired with broadcast to avoid echoing updates back to origin client.
	broadcastSender chan *CanvasClient
	broadcastMu     sync.Mutex

	snapshotMu      sync.RWMutex
	CurrentSnapshot []byte

	done chan struct{}
}

func newCanvasHub(roomID string, manager *CanvasManager) *CanvasHub {
	return &CanvasHub{
		roomID:          roomID,
		manager:         manager,
		clients:         make(map[*CanvasClient]struct{}),
		register:        make(chan *CanvasClient, 64),
		unregister:      make(chan *CanvasClient, 64),
		broadcast:       make(chan []byte, 256),
		broadcastSender: make(chan *CanvasClient, 256),
		done:            make(chan struct{}),
	}
}

func appendVarUint(target []byte, value uint64) []byte {
	buffer := make([]byte, binary.MaxVarintLen64)
	written := binary.PutUvarint(buffer, value)
	return append(target, buffer[:written]...)
}

func appendVarBytes(target []byte, payload []byte) []byte {
	target = appendVarUint(target, uint64(len(payload)))
	return append(target, payload...)
}

func encodeCanvasSyncStep2Message(snapshot []byte) []byte {
	encoded := make([]byte, 0, len(snapshot)+8)
	encoded = appendVarUint(encoded, canvasMessageSync)
	encoded = appendVarUint(encoded, canvasSyncStep2)
	encoded = appendVarBytes(encoded, snapshot)
	return encoded
}

func (h *CanvasHub) setCurrentSnapshot(snapshot []byte) {
	h.snapshotMu.Lock()
	defer h.snapshotMu.Unlock()
	if len(snapshot) == 0 {
		h.CurrentSnapshot = nil
		return
	}
	h.CurrentSnapshot = append([]byte(nil), snapshot...)
}

func (h *CanvasHub) currentSnapshotCopy() []byte {
	h.snapshotMu.RLock()
	defer h.snapshotMu.RUnlock()
	if len(h.CurrentSnapshot) == 0 {
		return nil
	}
	return append([]byte(nil), h.CurrentSnapshot...)
}

func (h *CanvasHub) loadCurrentSnapshot() {
	snapshot, _, err := loadCanvasSnapshotForRoom(h.roomID)
	if err != nil {
		log.Printf("Could not preload current canvas snapshot for room %s: %v", h.roomID, err)
		return
	}
	if len(snapshot) == 0 {
		log.Printf("No existing canvas snapshot was found when room %s started.", h.roomID)
		return
	}
	h.setCurrentSnapshot(snapshot)
	log.Printf("Preloaded canvas snapshot for room %s (%d bytes).", h.roomID, len(snapshot))
}

func (h *CanvasHub) flushSnapshotOnTeardown() {
	redisStore, _, r2Client := h.manager.activeStores()
	log.Printf("Starting final canvas snapshot flush for room %s.", h.roomID)

	if redisStore == nil || redisStore.Client == nil {
		log.Printf("Skipping final flush for room %s because Redis is unavailable.", h.roomID)
		return
	}
	if r2Client == nil || r2Client.Client == nil || strings.TrimSpace(r2Client.Bucket) == "" {
		log.Printf("Skipping final flush for room %s because R2 is unavailable.", h.roomID)
		return
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), canvasSnapshotReadTimeout)
	redisSnapshot, err := database.GetCanvasSnapshotFromRedis(readCtx, redisStore.Client, h.roomID)
	readCancel()
	if err != nil {
		log.Printf("Could not read Redis snapshot during teardown for room %s: %v", h.roomID, err)
		return
	}
	if len(redisSnapshot) == 0 {
		log.Printf("Skipping final flush for room %s because Redis has no snapshot.", h.roomID)
		return
	}

	writeCtx, writeCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
	err = storage.SaveCanvasSnapshotToR2(writeCtx, r2Client.Client, r2Client.Bucket, h.roomID, redisSnapshot)
	writeCancel()
	if err != nil {
		log.Printf("Could not save final snapshot to R2 for room %s: %v", h.roomID, err)
		return
	}
	log.Printf("Uploaded final snapshot for room %s to R2 (%d bytes).", h.roomID, len(redisSnapshot))

	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
	defer deleteCancel()
	if err := database.DeleteCanvasSnapshotFromRedis(deleteCtx, redisStore.Client, h.roomID); err != nil {
		log.Printf("Could not delete Redis snapshot after R2 upload for room %s: %v", h.roomID, err)
		return
	}
	log.Printf("Completed final snapshot flush for room %s (%d bytes).", h.roomID, len(redisSnapshot))
}

func (h *CanvasHub) Run() {
	defer close(h.done)

	for {
		select {
		case client := <-h.register:
			if client == nil {
				continue
			}
			h.clientsMu.Lock()
			h.clients[client] = struct{}{}
			log.Printf("A canvas client joined room %s. Client ID: %s. Active clients: %d.", h.roomID, client.debugID(), len(h.clients))

			if snapshot := h.currentSnapshotCopy(); len(snapshot) > 0 {
				framedSnapshot := encodeCanvasSyncStep2Message(snapshot)
				log.Printf("Sending initial snapshot to client %s in room %s (%d bytes).", client.debugID(), h.roomID, len(framedSnapshot))
				select {
				case client.send <- framedSnapshot:
				default:
					delete(h.clients, client)
					close(client.send)
					log.Printf("Dropped client %s from room %s because initial snapshot delivery was blocked.", client.debugID(), h.roomID)
					h.clientsMu.Unlock()
					continue
				}
			}

			for connectedClient := range h.clients {
				select {
				case connectedClient.send <- canvasAwarenessQueryPayload:
				default:
					delete(h.clients, connectedClient)
					close(connectedClient.send)
					log.Printf("Removed client %s from room %s because awareness sync was blocked.", connectedClient.debugID(), h.roomID)
				}
			}
			h.clientsMu.Unlock()

		case client := <-h.unregister:
			if client == nil {
				continue
			}
			h.clientsMu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				log.Printf("A canvas client left room %s. Client ID: %s. Active clients: %d.", h.roomID, client.debugID(), len(h.clients))
			}
			empty := len(h.clients) == 0
			h.clientsMu.Unlock()
			if empty {
				h.manager.removeHub(h.roomID, h)
				h.flushSnapshotOnTeardown()
				return
			}

		case payload := <-h.broadcast:
			sender := <-h.broadcastSender
			h.clientsMu.Lock()
			recipientCount := 0
			for client := range h.clients {
				if client == sender {
					continue
				}
				select {
				case client.send <- payload:
					recipientCount += 1
				default:
					delete(h.clients, client)
					close(client.send)
					log.Printf("Dropped receiver %s in room %s because broadcast delivery was blocked (sender %s).", client.debugID(), h.roomID, sender.debugID())
				}
			}
			log.Printf("Broadcasted canvas update in room %s. Sender: %s. Payload size: %d bytes. Recipients: %d.", h.roomID, sender.debugID(), len(payload), recipientCount)
			empty := len(h.clients) == 0
			h.clientsMu.Unlock()
			if empty {
				h.manager.removeHub(h.roomID, h)
				h.flushSnapshotOnTeardown()
				return
			}
		}
	}
}

func (h *CanvasHub) publishFrom(sender *CanvasClient, payload []byte) bool {
	h.broadcastMu.Lock()
	defer h.broadcastMu.Unlock()
	log.Printf("Received canvas update from client %s in room %s (%d bytes).", sender.debugID(), h.roomID, len(payload))

	select {
	case <-h.done:
		return false
	case h.broadcast <- payload:
	}

	select {
	case <-h.done:
		return false
	case h.broadcastSender <- sender:
	}

	return true
}

type CanvasClient struct {
	hub  *CanvasHub
	conn *websocket.Conn
	send chan []byte

	unregisterOnce sync.Once
}

func (c *CanvasClient) debugID() string {
	return fmt.Sprintf("%p", c)
}

func (c *CanvasClient) requestUnregister() {
	c.unregisterOnce.Do(func() {
		select {
		case <-c.hub.done:
		case c.hub.unregister <- c:
		}
	})
}

func (c *CanvasClient) readPump() {
	defer func() {
		c.requestUnregister()
		_ = c.conn.Close()
	}()
	log.Printf("Started reading canvas WebSocket messages for client %s in room %s.", c.debugID(), c.hub.roomID)

	c.conn.SetReadLimit(canvasMaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(canvasPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(canvasPongWait))
	})

	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Unexpected canvas WebSocket read error in room %s: %v", c.hub.roomID, err)
			}
			log.Printf("Stopped reading canvas WebSocket messages for client %s in room %s: %v", c.debugID(), c.hub.roomID, err)
			return
		}
		if messageType != websocket.BinaryMessage {
			log.Printf("Ignored a non-binary canvas message from client %s in room %s (type %d, %d bytes).", c.debugID(), c.hub.roomID, messageType, len(payload))
			continue
		}
		if len(payload) == 0 {
			log.Printf("Ignored an empty canvas message from client %s in room %s.", c.debugID(), c.hub.roomID)
			continue
		}
		log.Printf("Read a binary canvas message from client %s in room %s (%d bytes).", c.debugID(), c.hub.roomID, len(payload))
		payloadCopy := append([]byte(nil), payload...)
		if ok := c.hub.publishFrom(c, payloadCopy); !ok {
			log.Printf("Could not publish canvas message from client %s in room %s (%d bytes).", c.debugID(), c.hub.roomID, len(payloadCopy))
			return
		}
	}
}

func (c *CanvasClient) writePump() {
	ticker := time.NewTicker(canvasPingPeriod)
	defer func() {
		ticker.Stop()
		c.requestUnregister()
		_ = c.conn.Close()
	}()
	log.Printf("Started writing canvas WebSocket messages for client %s in room %s.", c.debugID(), c.hub.roomID)

	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(canvasWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				log.Printf("Stopped writing canvas messages for client %s in room %s because the send channel closed.", c.debugID(), c.hub.roomID)
				return
			}
			log.Printf("Sent a binary canvas message to client %s in room %s (%d bytes).", c.debugID(), c.hub.roomID, len(payload))
			if err := c.conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				log.Printf("Could not send a canvas message to client %s in room %s: %v", c.debugID(), c.hub.roomID, err)
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(canvasWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("Canvas ping failed for client %s in room %s: %v", c.debugID(), c.hub.roomID, err)
				return
			}
			log.Printf("Sent canvas ping to client %s in room %s.", c.debugID(), c.hub.roomID)
		}
	}
}

// ServeCanvasWS upgrades and joins the caller into a room-isolated canvas hub.
func ServeCanvasWS(w http.ResponseWriter, r *http.Request, roomID string) {
	normalizedRoomID := normalizeRoomID(roomID)
	log.Printf("Canvas WebSocket request received. Normalized room ID: %s. Raw room ID: %s. Client: %s.", normalizedRoomID, roomID, r.RemoteAddr)
	if normalizedRoomID == "" {
		log.Printf("Rejected canvas WebSocket request with invalid room ID %q from %s.", roomID, r.RemoteAddr)
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	existingHub := DefaultCanvasManager.getHub(normalizedRoomID)
	if existingHub != nil {
		existingHub.clientsMu.RLock()
		existingClients := len(existingHub.clients)
		existingHub.clientsMu.RUnlock()
		if existingClients >= canvasRoomMaxClients {
			log.Printf("Rejected canvas WebSocket request for room %s because it already has %d clients.", normalizedRoomID, existingClients)
			http.Error(w, "Canvas is full (Max 50)", http.StatusForbidden)
			return
		}
	}

	conn, err := canvasUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Could not upgrade canvas WebSocket for room %s: %v", normalizedRoomID, err)
		return
	}
	log.Printf("Canvas WebSocket upgrade succeeded for room %s from %s.", normalizedRoomID, r.RemoteAddr)

	hub := DefaultCanvasManager.getOrCreateHub(normalizedRoomID)
	client := &CanvasClient{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
	}

	select {
	case <-hub.done:
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "canvas room unavailable"),
			time.Now().Add(canvasWriteWait),
		)
		_ = conn.Close()
		log.Printf("Canvas room %s became unavailable during registration for client %s.", normalizedRoomID, r.RemoteAddr)
		return
	case hub.register <- client:
		log.Printf("Registered canvas client %s in room %s from %s.", client.debugID(), normalizedRoomID, r.RemoteAddr)
	}

	go client.writePump()
	go client.readPump()
}

func resolveCanvasRoomIDFromRequest(r *http.Request) string {
	roomID := normalizeRoomID(strings.TrimSpace(chi.URLParam(r, "roomId")))
	if roomID != "" {
		return roomID
	}
	roomID = normalizeRoomID(strings.TrimSpace(r.URL.Query().Get("roomId")))
	if roomID != "" {
		return roomID
	}
	return normalizeRoomID(strings.TrimSpace(r.URL.Query().Get("room")))
}

func loadCanvasSnapshotForRoom(roomID string) ([]byte, canvasSnapshotLookupResult, error) {
	lookup := canvasSnapshotLookupResult{
		RoomName:         resolveCanvasRoomName(roomID),
		RedisHadSnapshot: false,
		R2FetchAttempted: false,
		Source:           "none",
	}
	if roomID == "" {
		return nil, lookup, nil
	}

	redisStore, scyllaStore, r2Client := DefaultCanvasManager.activeStores()
	if scyllaStore == nil || scyllaStore.Session == nil {
		lookup.Source = "metadata_unavailable"
		return nil, lookup, nil
	}

	metadataCtx, metadataCancel := context.WithTimeout(context.Background(), canvasSnapshotReadTimeout)
	hasCanvasData, err := database.CheckCanvasHasData(metadataCtx, scyllaStore.Session, roomID)
	metadataCancel()
	if err != nil {
		return nil, lookup, err
	}
	if !hasCanvasData {
		lookup.Source = "metadata_empty"
		return nil, lookup, nil
	}

	hub := DefaultCanvasManager.getHub(roomID)
	if hub != nil {
		hubSnapshot := hub.currentSnapshotCopy()
		if len(hubSnapshot) > 0 {
			lookup.Source = "hub"
			return hubSnapshot, lookup, nil
		}
	}

	if redisStore != nil && redisStore.Client != nil {
		redisCtx, redisCancel := context.WithTimeout(context.Background(), canvasSnapshotReadTimeout)
		redisSnapshot, redisErr := database.GetCanvasSnapshotFromRedis(redisCtx, redisStore.Client, roomID)
		redisCancel()
		if redisErr != nil {
			log.Printf("Could not load canvas snapshot from Redis for room %s: %v", roomID, redisErr)
		} else if len(redisSnapshot) > 0 {
			lookup.RedisHadSnapshot = true
			lookup.Source = "redis"
			if hub != nil {
				hub.setCurrentSnapshot(redisSnapshot)
			}
			return redisSnapshot, lookup, nil
		}
	}

	if r2Client != nil && r2Client.Client != nil && strings.TrimSpace(r2Client.Bucket) != "" {
		lookup.R2FetchAttempted = true
		r2Ctx, r2Cancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
		r2Snapshot, r2Err := storage.GetCanvasSnapshotFromR2(r2Ctx, r2Client.Client, r2Client.Bucket, roomID)
		r2Cancel()
		if r2Err != nil {
			return nil, lookup, r2Err
		}
		if len(r2Snapshot) > 0 {
			lookup.Source = "r2"
			if hub != nil {
				hub.setCurrentSnapshot(r2Snapshot)
			}
			if redisStore != nil && redisStore.Client != nil {
				snapshotCopy := append([]byte(nil), r2Snapshot...)
				go func() {
					hotCtx, hotCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
					defer hotCancel()
					if saveErr := database.SaveCanvasSnapshotToRedis(hotCtx, redisStore.Client, roomID, snapshotCopy); saveErr != nil {
						log.Printf("Could not warm Redis cache from R2 snapshot for room %s: %v", roomID, saveErr)
					}
				}()
			}
			return r2Snapshot, lookup, nil
		}
	}

	if hasCanvasData {
		return nil, lookup, fmt.Errorf("critical: scylladb indicates canvas has data, but it could not be retrieved from redis or r2")
	}

	return nil, lookup, nil
}

func HandleCanvasSnapshotLoad(w http.ResponseWriter, r *http.Request) {
	roomID := resolveCanvasRoomIDFromRequest(r)
	if roomID == "" {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	snapshot, lookup, err := loadCanvasSnapshotForRoom(roomID)
	if err != nil {
		log.Printf("[canvas] Load failed room=%s err=%v", roomID, err)
		http.Error(w, "failed to load snapshot", http.StatusInternalServerError)
		return
	}

	log.Printf("[canvas] Load success room=%s source=%s bytes=%d", roomID, lookup.Source, len(snapshot))

	if len(snapshot) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(snapshot)
}

func HandleCanvasSnapshotSave(w http.ResponseWriter, r *http.Request) {
	roomID := resolveCanvasRoomIDFromRequest(r)
	if roomID == "" {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	hub := DefaultCanvasManager.getHub(roomID)

	bodyReader := http.MaxBytesReader(w, r.Body, canvasMaxMessageSize)
	defer bodyReader.Close()
	snapshot, err := io.ReadAll(bodyReader)
	if err != nil {
		http.Error(w, "invalid snapshot payload", http.StatusBadRequest)
		return
	}
	log.Printf("[canvas] Save payload received room=%s bytes=%d hub_active=%t", roomID, len(snapshot), hub != nil)

	if hub != nil {
		hub.setCurrentSnapshot(snapshot)
	}

	redisStore, scyllaStore, r2Client := DefaultCanvasManager.activeStores()
	if redisStore != nil && redisStore.Client != nil {
		redisCtx, redisCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
		saveErr := database.SaveCanvasSnapshotToRedis(redisCtx, redisStore.Client, roomID, snapshot)
		redisCancel()
		if saveErr != nil {
			log.Printf("[canvas] Save to Redis failed room=%s err=%v", roomID, saveErr)
		}
	}

	if scyllaStore != nil && scyllaStore.Session != nil {
		go func() {
			metaCtx, metaCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
			defer metaCancel()
			if markErr := database.UpdateCanvasHasData(metaCtx, scyllaStore.Session, roomID); markErr != nil {
				log.Printf("[canvas] Update canvas_has_data failed room=%s err=%v", roomID, markErr)
			}
		}()
	}

	if hub == nil && r2Client != nil && r2Client.Client != nil {
		snapshotCopy := append([]byte(nil), snapshot...)
		go func() {
			r2Ctx, r2Cancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
			saveErr := storage.SaveCanvasSnapshotToR2(r2Ctx, r2Client.Client, r2Client.Bucket, roomID, snapshotCopy)
			r2Cancel()
			if saveErr != nil {
				log.Printf("[canvas] Rescue save to R2 failed room=%s err=%v", roomID, saveErr)
				return
			}
			log.Printf("[canvas] Rescue save to R2 succeeded room=%s bytes=%d", roomID, len(snapshotCopy))

			if redisStore != nil && redisStore.Client != nil {
				deleteCtx, deleteCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
				if deleteErr := database.DeleteCanvasSnapshotFromRedis(deleteCtx, redisStore.Client, roomID); deleteErr != nil {
					log.Printf("[canvas] Redis cleanup after rescue failed room=%s err=%v", roomID, deleteErr)
				}
				deleteCancel()
			}
		}()
	}

	w.WriteHeader(http.StatusNoContent)
}
