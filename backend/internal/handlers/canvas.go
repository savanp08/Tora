package handlers

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/security"
	"github.com/savanp08/converse/internal/storage"

	"github.com/go-chi/chi/v5"
)

const (
	canvasWriteWait               = 10 * time.Second
	canvasPongWait                = 60 * time.Second
	canvasPingPeriod              = (canvasPongWait * 9) / 10
	canvasMaxMessageSize          = 2 * 1024 * 1024
	canvasRoomMaxClients          = 50
	canvasSnapshotReadTimeout     = 15 * time.Second
	canvasSnapshotWriteTimeout    = 15 * time.Second
	canvasSnapshotMetadataTimeout = 4 * time.Second
	canvasSnapshotRedisTimeout    = 1200 * time.Millisecond
	canvasSnapshotR2Timeout       = 8 * time.Second
	canvasRoomNameLookupTimeout   = 300 * time.Millisecond
	canvasSnapshotStageSlack      = 100 * time.Millisecond

	canvasMessageSync           = 0
	canvasMessageQueryAwareness = 3
	canvasSyncStep2             = 1
)

var canvasAwarenessQueryPayload = []byte{canvasMessageQueryAwareness}

var canvasWriteLimiter = security.NewLimiter(30, time.Minute, 10, 15*time.Minute)

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
var DefaultCanvasManager = NewCanvasManager(nil, nil, nil, nil)

// CanvasManager tracks one isolated CanvasHub per room.
type CanvasManager struct {
	mu          sync.RWMutex
	hubs        map[string]*CanvasHub
	redisStore  *database.RedisStore
	scyllaStore *database.ScyllaStore
	r2Client    *storage.R2Client
	tracker     *monitor.UsageTracker
}

func NewCanvasManager(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
	tracker *monitor.UsageTracker,
) *CanvasManager {
	return &CanvasManager{
		hubs:        make(map[string]*CanvasHub),
		redisStore:  redisStore,
		scyllaStore: scyllaStore,
		r2Client:    r2Client,
		tracker:     tracker,
	}
}

func ConfigureCanvasPersistence(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
	tracker *monitor.UsageTracker,
) {
	DefaultCanvasManager.configureStores(redisStore, scyllaStore, r2Client, tracker)
}

func (m *CanvasManager) configureStores(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
	tracker *monitor.UsageTracker,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redisStore = redisStore
	m.scyllaStore = scyllaStore
	m.r2Client = r2Client
	m.tracker = tracker
}

func (m *CanvasManager) activeStores() (*database.RedisStore, *database.ScyllaStore, *storage.R2Client, *monitor.UsageTracker) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.redisStore, m.scyllaStore, m.r2Client, m.tracker
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
	existing = m.hubs[roomID]
	if existing != nil {
		m.mu.Unlock()
		return existing
	}

	hub := newCanvasHub(roomID, m)
	m.hubs[roomID] = hub
	m.mu.Unlock()

	// Load after unlocking to avoid manager lock re-entry deadlocks.
	hub.loadCurrentSnapshot()
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

func resolveCanvasRoomName(roomID string, redisStore *database.RedisStore) string {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return roomID
	}
	if redisStore == nil || redisStore.Client == nil {
		return normalizedRoomID
	}

	ctx, cancel := context.WithTimeout(context.Background(), canvasRoomNameLookupTimeout)
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
	snapshot, _, err := loadCanvasSnapshotForRoom(context.Background(), h.roomID)
	if err != nil {
		log.Printf("Could not preload current canvas snapshot for room %s: %v", h.roomID, err)
		return
	}
	if len(snapshot) == 0 {
		return
	}
	h.setCurrentSnapshot(snapshot)
}

func (h *CanvasHub) flushSnapshotOnTeardown() {
	redisStore, _, r2Client, _ := h.manager.activeStores()

	if redisStore == nil || redisStore.Client == nil {
		return
	}
	if r2Client == nil || r2Client.Client == nil || strings.TrimSpace(r2Client.Bucket) == "" {
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
		return
	}

	writeCtx, writeCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
	if quotaErr := storage.EnsureR2WriteAllowed(writeCtx, redisStore, storage.R2HardCapBytes); quotaErr != nil {
		writeCancel()
		if errors.Is(quotaErr, storage.ErrR2StorageFull) {
			log.Printf("Skipping final R2 snapshot save for room %s because storage hard cap was reached.", h.roomID)
			return
		}
		log.Printf("Could not verify R2 storage quota during teardown for room %s: %v", h.roomID, quotaErr)
		return
	}
	err = storage.SaveCanvasSnapshotToR2(writeCtx, r2Client.Client, r2Client.Bucket, h.roomID, redisSnapshot)
	writeCancel()
	if err != nil {
		log.Printf("Could not save final snapshot to R2 for room %s: %v", h.roomID, err)
		return
	}
	if _, usageErr := storage.IncrementR2UsageBytes(context.Background(), redisStore, int64(len(redisSnapshot))); usageErr != nil {
		log.Printf("Could not increment R2 usage bytes after final snapshot save for room %s: %v", h.roomID, usageErr)
	}

	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
	defer deleteCancel()
	if err := database.DeleteCanvasSnapshotFromRedis(deleteCtx, redisStore.Client, h.roomID); err != nil {
		log.Printf("Could not delete Redis snapshot after R2 upload for room %s: %v", h.roomID, err)
		return
	}
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

			if snapshot := h.currentSnapshotCopy(); len(snapshot) > 0 {
				framedSnapshot := encodeCanvasSyncStep2Message(snapshot)
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
			for client := range h.clients {
				if client == sender {
					continue
				}
				select {
				case client.send <- payload:
				default:
					delete(h.clients, client)
					close(client.send)
					log.Printf("Dropped receiver %s in room %s because broadcast delivery was blocked (sender %s).", client.debugID(), h.roomID, sender.debugID())
				}
			}
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
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		if len(payload) == 0 {
			continue
		}
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
	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(canvasWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
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
		}
	}
}

// ServeCanvasWS upgrades and joins the caller into a room-isolated canvas hub.
func ServeCanvasWS(w http.ResponseWriter, r *http.Request, roomID string) {
	normalizedRoomID := normalizeRoomID(roomID)
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

func canvasSnapshotStageContext(parent context.Context, maxStageTimeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithTimeout(context.Background(), maxStageTimeout)
	}
	if deadline, hasDeadline := parent.Deadline(); hasDeadline {
		remaining := time.Until(deadline) - canvasSnapshotStageSlack
		if remaining <= 0 {
			return context.WithTimeout(parent, time.Millisecond)
		}
		if remaining < maxStageTimeout {
			return context.WithTimeout(parent, remaining)
		}
	}
	return context.WithTimeout(parent, maxStageTimeout)
}

func loadCanvasSnapshotForRoom(ctx context.Context, roomID string) ([]byte, canvasSnapshotLookupResult, error) {
	lookup := canvasSnapshotLookupResult{RoomName: roomID, RedisHadSnapshot: false, R2FetchAttempted: false, Source: "none"}
	if roomID == "" {
		return nil, lookup, nil
	}

	redisStore, scyllaStore, r2Client, _ := DefaultCanvasManager.activeStores()
	lookup.RoomName = resolveCanvasRoomName(roomID, redisStore)

	hub := DefaultCanvasManager.getHub(roomID)
	if hub != nil {
		hubSnapshot := hub.currentSnapshotCopy()
		if len(hubSnapshot) > 0 {
			lookup.Source = "hub"
			return hubSnapshot, lookup, nil
		}
	}

	if redisStore != nil && redisStore.Client != nil {
		redisCtx, redisCancel := canvasSnapshotStageContext(ctx, canvasSnapshotRedisTimeout)
		redisSnapshot, redisErr := database.GetCanvasSnapshotFromRedis(redisCtx, redisStore.Client, roomID)
		redisCancel()
		if redisErr != nil {
			log.Printf("[canvas] Redis snapshot lookup skipped room=%s err=%v", roomID, redisErr)
		} else if len(redisSnapshot) > 0 {
			lookup.RedisHadSnapshot = true
			lookup.Source = "redis"
			if hub != nil {
				hub.setCurrentSnapshot(redisSnapshot)
			}
			return redisSnapshot, lookup, nil
		}
	}

	metadataKnown := false
	metadataHasData := false
	if scyllaStore == nil || scyllaStore.Session == nil {
		lookup.Source = "metadata_unavailable"
	} else {
		metadataCtx, metadataCancel := canvasSnapshotStageContext(ctx, canvasSnapshotMetadataTimeout)
		hasCanvasData, metadataErr := database.CheckCanvasHasData(metadataCtx, scyllaStore.Session, roomID)
		metadataCancel()
		if metadataErr != nil {
			log.Printf("[canvas] Metadata check failed room=%s err=%v", roomID, metadataErr)
			lookup.Source = "metadata_check_failed"
		} else {
			metadataKnown = true
			metadataHasData = hasCanvasData
			if !hasCanvasData {
				lookup.Source = "metadata_empty"
				return nil, lookup, nil
			}
		}
	}

	if r2Client != nil && r2Client.Client != nil && strings.TrimSpace(r2Client.Bucket) != "" {
		lookup.R2FetchAttempted = true
		r2Ctx, r2Cancel := canvasSnapshotStageContext(ctx, canvasSnapshotR2Timeout)
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

	if metadataKnown && metadataHasData {
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
	startedAt := time.Now()
	requestCtx, requestCancel := context.WithTimeout(r.Context(), canvasSnapshotReadTimeout-canvasSnapshotStageSlack)
	defer requestCancel()

	snapshot, lookup, err := loadCanvasSnapshotForRoom(requestCtx, roomID)
	if err != nil {
		log.Printf(
			"[canvas] Load failed room=%s room_name=%q elapsed_ms=%d err=%v",
			roomID,
			lookup.RoomName,
			time.Since(startedAt).Milliseconds(),
			err,
		)
		http.Error(w, "failed to load snapshot", http.StatusInternalServerError)
		return
	}

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
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		clientIP := extractClientIP(r)
		if !canvasWriteLimiter.Allow(clientIP) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Canvas write rate limit exceeded"})
			return
		}
	}

	hub := DefaultCanvasManager.getHub(roomID)

	bodyReader := http.MaxBytesReader(w, r.Body, canvasMaxMessageSize)
	defer bodyReader.Close()
	snapshot, err := io.ReadAll(bodyReader)
	if err != nil {
		http.Error(w, "invalid snapshot payload", http.StatusBadRequest)
		return
	}

	if hub != nil {
		hub.setCurrentSnapshot(snapshot)
	}

	redisStore, scyllaStore, r2Client, tracker := DefaultCanvasManager.activeStores()
	if tracker != nil {
		tracker.RecordRequest(int64(len(snapshot)), 0)
	}
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
			if quotaErr := storage.EnsureR2WriteAllowed(r2Ctx, redisStore, storage.R2HardCapBytes); quotaErr != nil {
				r2Cancel()
				if errors.Is(quotaErr, storage.ErrR2StorageFull) {
					log.Printf("[canvas] Rescue save skipped room=%s reason=storage_hard_cap", roomID)
					return
				}
				log.Printf("[canvas] Rescue save quota check failed room=%s err=%v", roomID, quotaErr)
				return
			}
			saveErr := storage.SaveCanvasSnapshotToR2(r2Ctx, r2Client.Client, r2Client.Bucket, roomID, snapshotCopy)
			r2Cancel()
			if saveErr != nil {
				log.Printf("[canvas] Rescue save to R2 failed room=%s err=%v", roomID, saveErr)
				return
			}
			if _, usageErr := storage.IncrementR2UsageBytes(context.Background(), redisStore, int64(len(snapshotCopy))); usageErr != nil {
				log.Printf("[canvas] Failed to increment r2 usage bytes after rescue save room=%s err=%v", roomID, usageErr)
			}

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

type canvasMirrorSyncFile struct {
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
	Content  string `json:"content"`
}

type canvasMirrorSyncRequest struct {
	Files []canvasMirrorSyncFile `json:"files"`
}

func ensureCanvasFilesTable(ctx context.Context, scyllaStore *database.ScyllaStore) error {
	if scyllaStore == nil || scyllaStore.Session == nil {
		return fmt.Errorf("canvas storage unavailable")
	}
	tableName := scyllaStore.Table("canvas_files")
	query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id text,
		path text,
		language text,
		content text,
		updated_at timestamp,
		PRIMARY KEY (room_id, path)
	) WITH CLUSTERING ORDER BY (path ASC)`, tableName)
	return scyllaStore.Session.Query(query).WithContext(ctx).Exec()
}

func HandleCanvasFileMirrorSync(w http.ResponseWriter, r *http.Request) {
	roomID := strings.TrimSpace(chi.URLParam(r, "roomId"))
	if roomID == "" && r != nil {
		roomID = strings.TrimSpace(r.URL.Query().Get("roomId"))
		if roomID == "" {
			roomID = strings.TrimSpace(r.URL.Query().Get("room"))
		}
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		http.Error(w, "missing room id", http.StatusBadRequest)
		return
	}

	_, scyllaStore, _, _ := DefaultCanvasManager.activeStores()
	if scyllaStore == nil || scyllaStore.Session == nil {
		http.Error(w, "canvas storage unavailable", http.StatusServiceUnavailable)
		return
	}

	var req canvasMirrorSyncRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid canvas mirror payload", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), canvasSnapshotWriteTimeout)
	defer cancel()

	if err := ensureCanvasFilesTable(ctx, scyllaStore); err != nil {
		http.Error(w, "failed to ensure canvas mirror storage", http.StatusInternalServerError)
		return
	}

	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, scyllaStore.Table("canvas_files"))
	if err := scyllaStore.Session.Query(deleteQuery, normalizedRoomID).WithContext(ctx).Exec(); err != nil {
		http.Error(w, "failed to clear previous canvas mirror", http.StatusInternalServerError)
		return
	}

	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, path, language, content, updated_at) VALUES (?, ?, ?, ?, ?)`,
		scyllaStore.Table("canvas_files"),
	)
	now := time.Now().UTC()
	writtenCount := 0
	for _, file := range req.Files {
		normalizedPath, err := normalizeExecutionWorkspacePath(file.Path)
		if err != nil || normalizedPath == "" {
			continue
		}
		if err := scyllaStore.Session.Query(
			insertQuery,
			normalizedRoomID,
			normalizedPath,
			nullableTrimmedText(file.Language),
			file.Content,
			now,
		).WithContext(ctx).Exec(); err != nil {
			http.Error(w, "failed to write canvas mirror", http.StatusInternalServerError)
			return
		}
		writtenCount++
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"room_id": normalizedRoomID,
		"files":   writtenCount,
	})
}
