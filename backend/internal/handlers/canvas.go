package handlers

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	canvasWriteWait      = 10 * time.Second
	canvasPongWait       = 60 * time.Second
	canvasPingPeriod     = (canvasPongWait * 9) / 10
	canvasMaxMessageSize = 2 * 1024 * 1024
	canvasRoomMaxClients = 50
)

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
var DefaultCanvasManager = NewCanvasManager()

// CanvasManager tracks one isolated CanvasHub per room.
type CanvasManager struct {
	mu   sync.RWMutex
	hubs map[string]*CanvasHub
}

func NewCanvasManager() *CanvasManager {
	return &CanvasManager{
		hubs: make(map[string]*CanvasHub),
	}
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
				}
			}
			empty := len(h.clients) == 0
			h.clientsMu.Unlock()
			if empty {
				h.manager.removeHub(h.roomID, h)
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
				log.Printf("[canvas-ws] read error room=%s err=%v", c.hub.roomID, err)
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
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(canvasWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ServeCanvasWS upgrades and joins the caller into a room-isolated canvas hub.
func ServeCanvasWS(w http.ResponseWriter, r *http.Request, roomID string) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	DefaultCanvasManager.mu.Lock()
	existingHub := DefaultCanvasManager.hubs[normalizedRoomID]
	if existingHub != nil {
		existingHub.clientsMu.RLock()
		existingClients := len(existingHub.clients)
		existingHub.clientsMu.RUnlock()
		if existingClients >= canvasRoomMaxClients {
			DefaultCanvasManager.mu.Unlock()
			http.Error(w, "Canvas is full (Max 50)", http.StatusForbidden)
			return
		}
	}
	DefaultCanvasManager.mu.Unlock()

	conn, err := canvasUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[canvas-ws] upgrade failed room=%s err=%v", normalizedRoomID, err)
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
		return
	case hub.register <- client:
	}

	go client.writePump()
	go client.readPump()
}
