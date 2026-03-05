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

	"github.com/go-chi/chi/v5"
)

const (
	canvasWriteWait            = 10 * time.Second
	canvasPongWait             = 60 * time.Second
	canvasPingPeriod           = (canvasPongWait * 9) / 10
	canvasMaxMessageSize       = 2 * 1024 * 1024
	canvasRoomMaxClients       = 50
	canvasSnapshotReadTimeout  = 2 * time.Second
	canvasSnapshotWriteTimeout = 10 * time.Second

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
var DefaultCanvasManager = NewCanvasManager(nil, nil)

// CanvasManager tracks one isolated CanvasHub per room.
type CanvasManager struct {
	mu          sync.RWMutex
	hubs        map[string]*CanvasHub
	redisStore  *database.RedisStore
	scyllaStore *database.ScyllaStore
}

func NewCanvasManager(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
) *CanvasManager {
	return &CanvasManager{
		hubs:        make(map[string]*CanvasHub),
		redisStore:  redisStore,
		scyllaStore: scyllaStore,
	}
}

func ConfigureCanvasPersistence(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
) {
	DefaultCanvasManager.configureStores(redisStore, scyllaStore)
}

func (m *CanvasManager) configureStores(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redisStore = redisStore
	m.scyllaStore = scyllaStore
}

func (m *CanvasManager) activeStores() (*database.RedisStore, *database.ScyllaStore) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.redisStore, m.scyllaStore
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
	redisStore, scyllaStore := h.manager.activeStores()
	ctx, cancel := context.WithTimeout(context.Background(), canvasSnapshotReadTimeout)
	defer cancel()

	if redisStore != nil && redisStore.Client != nil {
		redisSnapshot, err := database.GetCanvasSnapshotFromRedis(ctx, redisStore.Client, h.roomID)
		if err != nil {
			log.Printf("[canvas-ws] redis snapshot load failed room=%s err=%v", h.roomID, err)
		} else if len(redisSnapshot) > 0 {
			log.Printf("[canvas-ws] event=load-current-snapshot source=redis room=%s bytes=%d", h.roomID, len(redisSnapshot))
			h.setCurrentSnapshot(redisSnapshot)
			return
		}
	}

	if scyllaStore != nil && scyllaStore.Session != nil {
		astraSnapshot, err := database.GetCanvasSnapshotFromAstra(ctx, scyllaStore.Session, h.roomID)
		if err != nil {
			log.Printf("[canvas-ws] astra snapshot load failed room=%s err=%v", h.roomID, err)
			return
		}
		if len(astraSnapshot) > 0 {
			log.Printf("[canvas-ws] event=load-current-snapshot source=astra room=%s bytes=%d", h.roomID, len(astraSnapshot))
			h.setCurrentSnapshot(astraSnapshot)
			if redisStore != nil && redisStore.Client != nil {
				snapshotCopy := append([]byte(nil), astraSnapshot...)
				go func() {
					hotCtx, hotCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
					defer hotCancel()
					if saveErr := database.SaveCanvasSnapshotToRedis(
						hotCtx,
						redisStore.Client,
						h.roomID,
						snapshotCopy,
					); saveErr != nil {
						log.Printf("[canvas-ws] redis warmup save failed room=%s err=%v", h.roomID, saveErr)
					}
				}()
			}
			return
		}
	}
	log.Printf("[canvas-ws] event=load-current-snapshot source=none room=%s bytes=0", h.roomID)
}

func (h *CanvasHub) flushSnapshotOnTeardown() {
	redisStore, scyllaStore := h.manager.activeStores()
	snapshot := []byte(nil)
	log.Printf("[canvas-ws] event=flush-snapshot-start room=%s", h.roomID)

	// Redis is the hot snapshot source and should win when available.
	if redisStore != nil && redisStore.Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), canvasSnapshotReadTimeout)
		redisSnapshot, err := database.GetCanvasSnapshotFromRedis(ctx, redisStore.Client, h.roomID)
		cancel()
		if err != nil {
			log.Printf("[canvas-ws] redis snapshot lookup on teardown failed room=%s err=%v", h.roomID, err)
		} else if len(redisSnapshot) > 0 {
			snapshot = redisSnapshot
		}
	}
	if len(snapshot) == 0 {
		snapshot = h.currentSnapshotCopy()
	}

	if len(snapshot) == 0 {
		log.Printf("[canvas-ws] event=flush-snapshot-skip room=%s reason=no-snapshot", h.roomID)
		return
	}
	if scyllaStore == nil || scyllaStore.Session == nil {
		log.Printf("[canvas-ws] event=flush-snapshot-skip room=%s reason=scylla-unavailable bytes=%d", h.roomID, len(snapshot))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
	defer cancel()

	if err := database.SaveCanvasSnapshotToAstra(
		ctx,
		scyllaStore.Session,
		h.roomID,
		snapshot,
	); err != nil {
		log.Printf("[canvas-ws] astra snapshot save failed room=%s err=%v", h.roomID, err)
		return
	}
	log.Printf("[canvas-ws] event=flush-snapshot-commit room=%s bytes=%d", h.roomID, len(snapshot))

	if redisStore != nil && redisStore.Client != nil {
		if err := database.DeleteCanvasSnapshotFromRedis(ctx, redisStore.Client, h.roomID); err != nil {
			log.Printf("[canvas-ws] redis snapshot delete failed room=%s err=%v", h.roomID, err)
		}
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
			log.Printf("[canvas-ws] event=client-register room=%s client=%s clients=%d", h.roomID, client.debugID(), len(h.clients))

			if snapshot := h.currentSnapshotCopy(); len(snapshot) > 0 {
				framedSnapshot := encodeCanvasSyncStep2Message(snapshot)
				log.Printf("[canvas-ws] event=snapshot-seed-send room=%s client=%s bytes=%d", h.roomID, client.debugID(), len(framedSnapshot))
				select {
				case client.send <- framedSnapshot:
				default:
					delete(h.clients, client)
					close(client.send)
					log.Printf("[canvas-ws] event=client-register-drop room=%s client=%s reason=seed-send-backpressure", h.roomID, client.debugID())
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
					log.Printf("[canvas-ws] event=awareness-query-drop room=%s client=%s", h.roomID, connectedClient.debugID())
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
				log.Printf("[canvas-ws] event=client-unregister room=%s client=%s clients=%d", h.roomID, client.debugID(), len(h.clients))
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
					log.Printf("[canvas-ws] event=broadcast-drop room=%s sender=%s target=%s reason=backpressure", h.roomID, sender.debugID(), client.debugID())
				}
			}
			log.Printf("[canvas-ws] event=broadcast room=%s sender=%s bytes=%d recipients=%d", h.roomID, sender.debugID(), len(payload), recipientCount)
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
	log.Printf("[canvas-ws] event=publish-from room=%s sender=%s bytes=%d", h.roomID, sender.debugID(), len(payload))

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
	log.Printf("[canvas-ws] event=read-pump-start room=%s client=%s", c.hub.roomID, c.debugID())

	c.conn.SetReadLimit(canvasMaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(canvasPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(canvasPongWait))
	})

	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[canvas-ws] read error room=%s err=%v", c.hub.roomID, err)
			}
			log.Printf("[canvas-ws] event=read-pump-stop room=%s client=%s err=%v", c.hub.roomID, c.debugID(), err)
			return
		}
		if messageType != websocket.BinaryMessage {
			log.Printf("[canvas-ws] event=read-skip-non-binary room=%s client=%s type=%d bytes=%d", c.hub.roomID, c.debugID(), messageType, len(payload))
			continue
		}
		if len(payload) == 0 {
			log.Printf("[canvas-ws] event=read-skip-empty room=%s client=%s", c.hub.roomID, c.debugID())
			continue
		}
		log.Printf("[canvas-ws] event=read-binary room=%s client=%s bytes=%d", c.hub.roomID, c.debugID(), len(payload))
		payloadCopy := append([]byte(nil), payload...)
		if ok := c.hub.publishFrom(c, payloadCopy); !ok {
			log.Printf("[canvas-ws] event=read-publish-failed room=%s client=%s bytes=%d", c.hub.roomID, c.debugID(), len(payloadCopy))
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
	log.Printf("[canvas-ws] event=write-pump-start room=%s client=%s", c.hub.roomID, c.debugID())

	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(canvasWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				log.Printf("[canvas-ws] event=write-pump-stop room=%s client=%s reason=channel-closed", c.hub.roomID, c.debugID())
				return
			}
			log.Printf("[canvas-ws] event=write-binary room=%s client=%s bytes=%d", c.hub.roomID, c.debugID(), len(payload))
			if err := c.conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				log.Printf("[canvas-ws] event=write-binary-error room=%s client=%s err=%v", c.hub.roomID, c.debugID(), err)
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(canvasWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("[canvas-ws] event=write-ping-error room=%s client=%s err=%v", c.hub.roomID, c.debugID(), err)
				return
			}
			log.Printf("[canvas-ws] event=write-ping room=%s client=%s", c.hub.roomID, c.debugID())
		}
	}
}

// ServeCanvasWS upgrades and joins the caller into a room-isolated canvas hub.
func ServeCanvasWS(w http.ResponseWriter, r *http.Request, roomID string) {
	normalizedRoomID := normalizeRoomID(roomID)
	log.Printf("[canvas-ws] event=serve-ws-request room=%s rawRoom=%s remote=%s", normalizedRoomID, roomID, r.RemoteAddr)
	if normalizedRoomID == "" {
		log.Printf("[canvas-ws] event=serve-ws-invalid-room rawRoom=%s remote=%s", roomID, r.RemoteAddr)
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	existingHub := DefaultCanvasManager.getHub(normalizedRoomID)
	if existingHub != nil {
		existingHub.clientsMu.RLock()
		existingClients := len(existingHub.clients)
		existingHub.clientsMu.RUnlock()
		if existingClients >= canvasRoomMaxClients {
			log.Printf("[canvas-ws] event=serve-ws-room-full room=%s clients=%d remote=%s", normalizedRoomID, existingClients, r.RemoteAddr)
			http.Error(w, "Canvas is full (Max 50)", http.StatusForbidden)
			return
		}
	}

	conn, err := canvasUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[canvas-ws] upgrade failed room=%s err=%v", normalizedRoomID, err)
		return
	}
	log.Printf("[canvas-ws] event=serve-ws-upgrade-ok room=%s remote=%s", normalizedRoomID, r.RemoteAddr)

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
		log.Printf("[canvas-ws] event=serve-ws-hub-unavailable room=%s remote=%s", normalizedRoomID, r.RemoteAddr)
		return
	case hub.register <- client:
		log.Printf("[canvas-ws] event=serve-ws-registered room=%s client=%s remote=%s", normalizedRoomID, client.debugID(), r.RemoteAddr)
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

func loadCanvasSnapshotForRoom(roomID string) ([]byte, error) {
	if roomID == "" {
		return nil, nil
	}
	lookupStart := time.Now()
	hub := DefaultCanvasManager.getHub(roomID)
	// If there is an active hub, it is the freshest in-memory state.
	if hub != nil {
		log.Printf("[canvas-ws] Room %s snapshot lookup: checking active hub memory.", roomID)
		hubSnapshot := hub.currentSnapshotCopy()
		if len(hubSnapshot) > 0 {
			log.Printf(
				"[canvas-ws] Room %s snapshot lookup: found %d bytes in hub memory (elapsed=%s).",
				roomID,
				len(hubSnapshot),
				time.Since(lookupStart).Round(time.Millisecond),
			)
			log.Printf("[canvas-ws] event=snapshot-load source=hub room=%s bytes=%d", roomID, len(hubSnapshot))
			return hubSnapshot, nil
		}
		log.Printf("[canvas-ws] Room %s snapshot lookup: hub is active but has no cached snapshot yet.", roomID)
	} else {
		log.Printf("[canvas-ws] Room %s snapshot lookup: no active hub, checking persisted stores.", roomID)
	}

	redisStore, scyllaStore := DefaultCanvasManager.activeStores()

	if redisStore != nil && redisStore.Client != nil {
		log.Printf("[canvas-ws] Room %s snapshot lookup: requesting Redis hot snapshot.", roomID)
		redisStarted := time.Now()
		redisCtx, redisCancel := context.WithTimeout(context.Background(), canvasSnapshotReadTimeout)
		redisSnapshot, err := database.GetCanvasSnapshotFromRedis(redisCtx, redisStore.Client, roomID)
		redisCancel()
		redisElapsed := time.Since(redisStarted).Round(time.Millisecond)
		if err != nil {
			log.Printf(
				"[canvas-ws] Room %s snapshot lookup: Redis hot snapshot read failed after %s: %v",
				roomID,
				redisElapsed,
				err,
			)
			log.Printf("[canvas-ws] redis snapshot load endpoint failed room=%s err=%v", roomID, err)
		} else if len(redisSnapshot) > 0 {
			if hub != nil {
				hub.setCurrentSnapshot(redisSnapshot)
			}
			log.Printf(
				"[canvas-ws] Room %s snapshot lookup: Redis returned %d bytes (elapsed=%s).",
				roomID,
				len(redisSnapshot),
				redisElapsed,
			)
			log.Printf("[canvas-ws] event=snapshot-load source=redis room=%s bytes=%d", roomID, len(redisSnapshot))
			return redisSnapshot, nil
		} else {
			log.Printf(
				"[canvas-ws] Room %s snapshot lookup: Redis returned no snapshot (elapsed=%s).",
				roomID,
				redisElapsed,
			)
		}
	} else {
		log.Printf("[canvas-ws] Room %s snapshot lookup: Redis store is unavailable, skipping.", roomID)
	}

	if scyllaStore != nil && scyllaStore.Session != nil {
		log.Printf("[canvas-ws] Room %s snapshot lookup: requesting Astra/Scylla persisted snapshot.", roomID)
		astraStarted := time.Now()
		astraCtx, astraCancel := context.WithTimeout(context.Background(), canvasSnapshotReadTimeout)
		astraSnapshot, err := database.GetCanvasSnapshotFromAstra(astraCtx, scyllaStore.Session, roomID)
		astraCancel()
		astraElapsed := time.Since(astraStarted).Round(time.Millisecond)
		if err != nil {
			log.Printf(
				"[canvas-ws] Room %s snapshot lookup: Astra/Scylla read failed after %s: %v",
				roomID,
				astraElapsed,
				err,
			)
			return nil, err
		}
		if len(astraSnapshot) > 0 {
			if hub != nil {
				hub.setCurrentSnapshot(astraSnapshot)
			}
			log.Printf(
				"[canvas-ws] Room %s snapshot lookup: Astra/Scylla returned %d bytes (elapsed=%s).",
				roomID,
				len(astraSnapshot),
				astraElapsed,
			)
			log.Printf("[canvas-ws] event=snapshot-load source=astra room=%s bytes=%d", roomID, len(astraSnapshot))
			if redisStore != nil && redisStore.Client != nil {
				snapshotCopy := append([]byte(nil), astraSnapshot...)
				go func() {
					hotCtx, hotCancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
					defer hotCancel()
					if saveErr := database.SaveCanvasSnapshotToRedis(
						hotCtx,
						redisStore.Client,
						roomID,
						snapshotCopy,
					); saveErr != nil {
						log.Printf("[canvas-ws] redis warmup save from astra failed room=%s err=%v", roomID, saveErr)
					}
				}()
			}
			return astraSnapshot, nil
		}
		log.Printf(
			"[canvas-ws] Room %s snapshot lookup: Astra/Scylla returned no snapshot (elapsed=%s).",
			roomID,
			astraElapsed,
		)
	} else {
		log.Printf("[canvas-ws] Room %s snapshot lookup: Astra/Scylla store is unavailable, skipping.", roomID)
	}

	log.Printf(
		"[canvas-ws] Room %s snapshot lookup complete: no snapshot found (elapsed=%s).",
		roomID,
		time.Since(lookupStart).Round(time.Millisecond),
	)
	log.Printf("[canvas-ws] event=snapshot-load source=none room=%s bytes=0", roomID)
	return nil, nil
}

func HandleCanvasSnapshotLoad(w http.ResponseWriter, r *http.Request) {
	roomID := resolveCanvasRoomIDFromRequest(r)
	requestStart := time.Now()
	log.Printf(
		"[canvas-ws] Room %s requested full canvas snapshot (remote=%s path=%s).",
		roomID,
		r.RemoteAddr,
		r.URL.Path,
	)
	log.Printf("[canvas-ws] event=snapshot-load-request room=%s remote=%s", roomID, r.RemoteAddr)
	if roomID == "" {
		log.Printf("[canvas-ws] Snapshot load rejected: missing or invalid room id (remote=%s path=%s).", r.RemoteAddr, r.URL.Path)
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	snapshot, err := loadCanvasSnapshotForRoom(roomID)
	if err != nil {
		log.Printf(
			"[canvas-ws] Room %s snapshot load failed; returning 500 (elapsed=%s): %v",
			roomID,
			time.Since(requestStart).Round(time.Millisecond),
			err,
		)
		log.Printf("[canvas-ws] snapshot load failed room=%s err=%v", roomID, err)
		http.Error(w, "failed to load snapshot", http.StatusInternalServerError)
		return
	}
	if len(snapshot) == 0 {
		log.Printf(
			"[canvas-ws] Room %s has no snapshot to send; returning %d (elapsed=%s).",
			roomID,
			http.StatusNoContent,
			time.Since(requestStart).Round(time.Millisecond),
		)
		log.Printf("[canvas-ws] event=snapshot-load-response room=%s status=%d bytes=0", roomID, http.StatusNoContent)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	if _, writeErr := w.Write(snapshot); writeErr != nil {
		log.Printf("[canvas-ws] Room %s snapshot write failed: %v", roomID, writeErr)
		return
	}
	log.Printf(
		"[canvas-ws] Room %s full snapshot sent successfully (%d bytes, elapsed=%s).",
		roomID,
		len(snapshot),
		time.Since(requestStart).Round(time.Millisecond),
	)
	log.Printf("[canvas-ws] event=snapshot-load-response room=%s status=%d bytes=%d", roomID, http.StatusOK, len(snapshot))
}

func HandleCanvasSnapshotSave(w http.ResponseWriter, r *http.Request) {
	roomID := resolveCanvasRoomIDFromRequest(r)
	requestStart := time.Now()
	log.Printf(
		"[canvas-ws] Room %s requested snapshot save (remote=%s path=%s).",
		roomID,
		r.RemoteAddr,
		r.URL.Path,
	)
	log.Printf("[canvas-ws] event=snapshot-save-request room=%s remote=%s", roomID, r.RemoteAddr)
	if roomID == "" {
		log.Printf("[canvas-ws] Snapshot save rejected: missing or invalid room id (remote=%s path=%s).", r.RemoteAddr, r.URL.Path)
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	hub := DefaultCanvasManager.getHub(roomID)

	bodyReader := http.MaxBytesReader(w, r.Body, canvasMaxMessageSize)
	defer bodyReader.Close()
	snapshot, err := io.ReadAll(bodyReader)
	if err != nil {
		log.Printf("[canvas-ws] Room %s snapshot payload read failed: %v", roomID, err)
		http.Error(w, "invalid snapshot payload", http.StatusBadRequest)
		return
	}
	log.Printf(
		"[canvas-ws] Room %s snapshot payload received (%d bytes, hubActive=%t).",
		roomID,
		len(snapshot),
		hub != nil,
	)
	log.Printf("[canvas-ws] event=snapshot-save-payload room=%s bytes=%d hubActive=%t", roomID, len(snapshot), hub != nil)
	if hub != nil {
		hub.setCurrentSnapshot(snapshot)
	}

	redisStore, scyllaStore := DefaultCanvasManager.activeStores()
	if redisStore != nil && redisStore.Client != nil {
		redisStarted := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
		saveErr := database.SaveCanvasSnapshotToRedis(ctx, redisStore.Client, roomID, snapshot)
		cancel()
		if saveErr != nil {
			log.Printf("[canvas-ws] Room %s snapshot save to Redis failed: %v", roomID, saveErr)
			log.Printf("[canvas-ws] redis snapshot save failed room=%s err=%v", roomID, saveErr)
		} else {
			log.Printf(
				"[canvas-ws] Room %s snapshot save to Redis completed (%d bytes, elapsed=%s).",
				roomID,
				len(snapshot),
				time.Since(redisStarted).Round(time.Millisecond),
			)
		}
	} else {
		log.Printf("[canvas-ws] Room %s snapshot save skipped Redis because store is unavailable.", roomID)
	}

	// Persist a cold snapshot copy as well so data survives Redis eviction/restarts.
	if scyllaStore != nil && scyllaStore.Session != nil {
		if hub == nil {
			log.Printf("[canvas-ws] Room %s has no active hub; scheduling Astra/Scylla snapshot save.", roomID)
		} else {
			log.Printf("[canvas-ws] Room %s has active hub; scheduling Astra/Scylla snapshot save for durability.", roomID)
		}
		snapshotCopy := append([]byte(nil), snapshot...)
		go func() {
			astraStarted := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), canvasSnapshotWriteTimeout)
			defer cancel()
			if saveErr := database.SaveCanvasSnapshotToAstra(ctx, scyllaStore.Session, roomID, snapshotCopy); saveErr != nil {
				log.Printf("[canvas-ws] Room %s async Astra/Scylla snapshot save failed: %v", roomID, saveErr)
				log.Printf("[canvas-ws] astra snapshot save without active hub failed room=%s err=%v", roomID, saveErr)
				return
			}
			log.Printf(
				"[canvas-ws] Room %s async Astra/Scylla snapshot save completed (%d bytes, elapsed=%s).",
				roomID,
				len(snapshotCopy),
				time.Since(astraStarted).Round(time.Millisecond),
			)
		}()
	} else {
		log.Printf("[canvas-ws] Room %s snapshot save skipped Astra/Scylla because store is unavailable.", roomID)
	}

	w.WriteHeader(http.StatusNoContent)
	log.Printf(
		"[canvas-ws] Room %s snapshot save request completed (status=%d bytes=%d elapsed=%s).",
		roomID,
		http.StatusNoContent,
		len(snapshot),
		time.Since(requestStart).Round(time.Millisecond),
	)
	log.Printf("[canvas-ws] event=snapshot-save-response room=%s status=%d bytes=%d", roomID, http.StatusNoContent, len(snapshot))
}
