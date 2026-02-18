package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/security"
	"golang.org/x/time/rate"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 65536
	maxTextChars   = 4000
	maxMediaURLLen = 4096
	maxFileNameLen = 180
)

var wsConnectLimiter = security.NewLimiter(40, time.Minute, 15, 15*time.Minute)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    1024,
	WriteBufferSize:   1024,
	EnableCompression: true,
	// dev only
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Client struct {
	Hub        *Hub
	Conn       *websocket.Conn
	Send       chan interface{}
	RoomID     string
	UserID     string
	Username   string
	JoinedAt   time.Time
	msgLimiter *rate.Limiter
}

func (c *Client) LoadHistory(ctx context.Context, service *MessageService) {
	if service == nil {
		return
	}

	history, err := service.GetRecentMessages(ctx, c.RoomID)
	if err != nil {
		log.Printf("[ws] history load error room=%s err=%v", c.RoomID, err)
		return
	}

	if len(history) == 0 {
		return
	}

	packet := map[string]interface{}{
		"type":    "history",
		"payload": history,
	}

	select {
	case c.Send <- packet:
	case <-ctx.Done():
	default:
	}
}

func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	if hub != nil && hub.tracker != nil && hub.tracker.IsSleeping() {
		http.Error(w, "Server is in safety sleep mode", http.StatusServiceUnavailable)
		return
	}

	clientIP := extractClientIP(r)
	if !wsConnectLimiter.Allow(clientIP) {
		http.Error(w, "Too many socket connection attempts", http.StatusTooManyRequests)
		log.Printf("[ws] connect rate limited ip=%s", clientIP)
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	userID := r.URL.Query().Get("userId")
	userID = normalizeUsername(userID)
	if userID == "" {
		userID = "guest_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	username := normalizeUsername(r.URL.Query().Get("username"))
	if username == "" {
		username = "Guest"
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade failed room=%s remote=%s err=%v", roomID, r.RemoteAddr, err)
		return
	}
	if hub != nil && hub.tracker != nil {
		hub.tracker.RecordWSConnection()
	}

	client := &Client{
		Hub:        hub,
		Conn:       conn,
		Send:       make(chan interface{}, 256),
		RoomID:     roomID,
		UserID:     userID,
		Username:   username,
		JoinedAt:   time.Now().UTC(),
		msgLimiter: rate.NewLimiter(rate.Every(250*time.Millisecond), 8),
	}
	client.Hub.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error { c.Conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		var msg models.Message
		err := c.Conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[ws] read unexpected close room=%s err=%v", c.RoomID, err)
			}
			break
		}
		msg.CreatedAt = time.Now().UTC()
		msg.SenderID = c.UserID
		msg.SenderName = c.Username
		msg.RoomID = c.RoomID
		if c.msgLimiter != nil && !c.msgLimiter.Allow() {
			log.Printf("[ws] message rate limited room=%s user=%s", c.RoomID, c.UserID)
			continue
		}
		if !normalizeInboundMessage(&msg) {
			log.Printf("[ws] message rejected room=%s user=%s type=%s", c.RoomID, c.UserID, msg.Type)
			continue
		}
		if msg.ID == "" {
			msg.ID = fmt.Sprintf("%s_%d", c.RoomID, msg.CreatedAt.UnixNano())
		}
		if c.Hub != nil && c.Hub.tracker != nil {
			c.Hub.tracker.RecordWSMessage(int64(estimateMessageBytes(msg)))
		}
		c.Hub.broadcast <- msg
	}
}

func normalizeRoomID(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}

	var builder strings.Builder
	prevSeparator := false
	for _, ch := range normalized {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9'):
			builder.WriteRune(ch)
			prevSeparator = false
		case ch == ' ' || ch == '-' || ch == '_':
			if builder.Len() > 0 && !prevSeparator {
				builder.WriteByte('_')
				prevSeparator = true
			}
		}
	}

	return strings.Trim(builder.String(), "_")
}

func normalizeUsername(raw string) string {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return ""
	}

	var builder strings.Builder
	prevSeparator := false
	for _, ch := range normalized {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9'):
			builder.WriteRune(ch)
			prevSeparator = false
		case ch == ' ' || ch == '-' || ch == '_':
			if builder.Len() > 0 && !prevSeparator {
				builder.WriteByte('_')
				prevSeparator = true
			}
		}
	}

	return strings.Trim(builder.String(), "_")
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case payload, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteJSON(payload); err != nil {
				log.Printf("[ws] write json failed room=%s err=%v", c.RoomID, err)
				return
			}
			if c.Hub != nil && c.Hub.tracker != nil {
				c.Hub.tracker.RecordDownload(int64(estimatePayloadBytes(payload)))
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("[ws] ping failed room=%s err=%v", c.RoomID, err)
				return
			}
		}
	}
}

func normalizeInboundMessage(msg *models.Message) bool {
	if msg == nil {
		return false
	}

	msg.Type = strings.ToLower(strings.TrimSpace(msg.Type))
	msg.Content = strings.TrimSpace(msg.Content)
	msg.MediaURL = strings.TrimSpace(msg.MediaURL)
	msg.MediaType = strings.ToLower(strings.TrimSpace(msg.MediaType))
	msg.FileName = strings.TrimSpace(msg.FileName)

	switch msg.Type {
	case "", "text":
		msg.Type = "text"
		return msg.Content != "" && len(msg.Content) <= maxTextChars
	case "image", "video", "file":
		if msg.Content == "" {
			msg.Content = msg.MediaURL
		}
		if len(msg.Content) == 0 || len(msg.Content) > maxMediaURLLen {
			return false
		}
		if msg.MediaURL == "" {
			msg.MediaURL = msg.Content
		}
		if len(msg.FileName) > maxFileNameLen {
			msg.FileName = msg.FileName[:maxFileNameLen]
		}
		return true
	default:
		return false
	}
}

func extractClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}

	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			first := strings.TrimSpace(parts[0])
			if first != "" {
				return first
			}
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}

func estimateMessageBytes(msg models.Message) int {
	estimated := len(msg.ID) + len(msg.RoomID) + len(msg.SenderID) + len(msg.SenderName) + len(msg.Content) + len(msg.Type) + len(msg.MediaURL) + len(msg.MediaType) + len(msg.FileName)
	if estimated <= 0 {
		return 0
	}
	return estimated + 80
}

func estimatePayloadBytes(payload interface{}) int {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0
	}
	return len(raw)
}
