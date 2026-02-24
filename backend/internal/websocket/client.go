package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	maxGlobalWSConnections = int32(15000)
	maxWSConnectionsPerIP  = int32(5)
)

var (
	wsConnectLimiter = security.NewLimiter(40, time.Minute, 15, 15*time.Minute)

	globalWSConnections    atomic.Int32
	activeConnectionsPerIP sync.Map
)

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
	UserID     string
	Username   string
	JoinedAt   time.Time
	msgLimiter *rate.Limiter
	clientIP   string

	disconnectOnce  sync.Once
	sendCloseOnce   sync.Once
	subscriptionsMu sync.RWMutex
	subscribedRooms map[string]RoomSubscription
	onDisconnect    func()
}

type RoomSubscription struct {
	CanWrite bool
}

func (c *Client) LoadHistory(ctx context.Context, service *MessageService, roomID string) {
	if service == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return
	}

	history, err := service.GetRecentMessages(ctx, roomID)
	if err != nil {
		log.Printf("[ws] history load error room=%s user=%s err=%v", roomID, c.UserID, err)
		return
	}

	if len(history) == 0 {
		return
	}

	packet := map[string]interface{}{
		"type":    "history",
		"roomId":  roomID,
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
	releaseReservation, status, rejectReason := reserveWSConnection(clientIP)
	if releaseReservation == nil {
		http.Error(w, rejectReason, status)
		log.Printf("[ws] connection rejected ip=%s status=%d reason=%s", clientIP, status, rejectReason)
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
		releaseReservation()
		log.Printf("[ws] upgrade failed remote=%s err=%v", r.RemoteAddr, err)
		return
	}
	if hub != nil && hub.tracker != nil {
		hub.tracker.RecordWSConnection()
	}

	client := &Client{
		Hub:             hub,
		Conn:            conn,
		Send:            make(chan interface{}, 256),
		UserID:          userID,
		Username:        username,
		JoinedAt:        time.Now().UTC(),
		msgLimiter:      rate.NewLimiter(rate.Every(250*time.Millisecond), 8),
		clientIP:        clientIP,
		subscribedRooms: make(map[string]RoomSubscription),
		onDisconnect: func() {
			releaseReservation()
		},
	}
	client.Hub.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.cleanupConnectionTracking()
		c.Hub.unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error { c.Conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		_, raw, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[ws] read unexpected close user=%s err=%v", c.UserID, err)
			}
			break
		}

		if roomIDs, isSubscribe := parseSubscribeRoomIDs(raw); isSubscribe {
			if len(roomIDs) == 0 {
				continue
			}
			if c.Hub != nil {
				c.Hub.subscribe <- &ClientSubscription{
					Client:  c,
					RoomIDs: roomIDs,
				}
			}
			continue
		}
		if typing, isTyping := parseTypingPayload(raw); isTyping {
			if c.Hub != nil {
				c.Hub.typing <- &ClientTypingEvent{
					Client:   c,
					RoomID:   typing.RoomID,
					IsTyping: typing.IsTyping,
				}
			}
			continue
		}
		if discussionComment, isDiscussionComment := parseDiscussionCommentPayload(raw); isDiscussionComment {
			if c.Hub != nil {
				c.Hub.discussionComment <- &ClientDiscussionCommentEvent{
					Client:          c,
					RoomID:          discussionComment.RoomID,
					PinMessageID:    discussionComment.PinMessageID,
					ParentCommentID: discussionComment.ParentCommentID,
					Content:         discussionComment.Content,
				}
			}
			continue
		}
		if discussionCommentPin, isDiscussionCommentPin := parseDiscussionCommentPinPayload(raw); isDiscussionCommentPin {
			if c.Hub != nil {
				c.Hub.discussionCommentPin <- &ClientDiscussionCommentPinEvent{
					Client:       c,
					RoomID:       discussionCommentPin.RoomID,
					PinMessageID: discussionCommentPin.PinMessageID,
					CommentID:    discussionCommentPin.CommentID,
					IsPinned:     discussionCommentPin.IsPinned,
				}
			}
			continue
		}
		if edit, isEdit := parseMessageEditPayload(raw); isEdit {
			if c.Hub != nil {
				c.Hub.messageEdit <- &ClientMessageEditEvent{
					Client:    c,
					RoomID:    edit.RoomID,
					MessageID: edit.MessageID,
					Content:   edit.Content,
				}
			}
			continue
		}
		if deletion, isDelete := parseMessageDeletePayload(raw); isDelete {
			if c.Hub != nil {
				c.Hub.messageDelete <- &ClientMessageDeleteEvent{
					Client:    c,
					RoomID:    deletion.RoomID,
					MessageID: deletion.MessageID,
				}
			}
			continue
		}

		var msg models.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		msg.CreatedAt = time.Now().UTC()
		msg.SenderID = c.UserID
		msg.SenderName = c.Username
		msg.RoomID = normalizeRoomID(msg.RoomID)
		if msg.RoomID == "" {
			log.Printf("[ws] message rejected user=%s reason=missing_room_id", c.UserID)
			continue
		}
		if !c.isSubscribedToRoom(msg.RoomID) {
			log.Printf("[ws] message rejected user=%s room=%s reason=not_subscribed", c.UserID, msg.RoomID)
			continue
		}
		if !c.canWriteToRoom(msg.RoomID) {
			log.Printf("[ws] message rejected user=%s room=%s reason=read_only_subscription", c.UserID, msg.RoomID)
			continue
		}
		if c.Hub != nil && !c.Hub.isClientRoomMember(c.UserID, msg.RoomID) {
			c.subscribeToRoom(msg.RoomID, false)
			log.Printf("[ws] message rejected user=%s room=%s reason=membership_revoked", c.UserID, msg.RoomID)
			continue
		}
		if c.msgLimiter != nil && !c.msgLimiter.Allow() {
			log.Printf("[ws] message rate limited room=%s user=%s", msg.RoomID, c.UserID)
			continue
		}
		if !normalizeInboundMessage(&msg) {
			log.Printf("[ws] message rejected room=%s user=%s type=%s", msg.RoomID, c.UserID, msg.Type)
			continue
		}
		if msg.ID == "" {
			msg.ID = fmt.Sprintf("%s_%d", msg.RoomID, msg.CreatedAt.UnixNano())
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
	for _, ch := range normalized {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			builder.WriteRune(ch)
		}
	}

	return builder.String()
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
		c.cleanupConnectionTracking()
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
				log.Printf("[ws] write json failed user=%s err=%v", c.UserID, err)
				return
			}
			if c.Hub != nil && c.Hub.tracker != nil {
				c.Hub.tracker.RecordDownload(int64(estimatePayloadBytes(payload)))
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("[ws] ping failed user=%s err=%v", c.UserID, err)
				return
			}
		}
	}
}

type subscribeEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	RoomIDs []string        `json:"roomIds"`
	Rooms   []string        `json:"rooms"`
}

type subscribePayload struct {
	RoomIDs []string `json:"roomIds"`
	Rooms   []string `json:"rooms"`
}

type typingEnvelope struct {
	Type    string          `json:"type"`
	RoomID  string          `json:"roomId"`
	RoomID2 string          `json:"room_id"`
	Payload json.RawMessage `json:"payload"`
}

type typingPayload struct {
	RoomID  string `json:"roomId"`
	RoomID2 string `json:"room_id"`
}

type messageEditEnvelope struct {
	Type       string          `json:"type"`
	RoomID     string          `json:"roomId"`
	RoomID2    string          `json:"room_id"`
	MessageID  string          `json:"messageId"`
	MessageID2 string          `json:"message_id"`
	Content    string          `json:"content"`
	Payload    json.RawMessage `json:"payload"`
}

type messageDeleteEnvelope struct {
	Type       string          `json:"type"`
	RoomID     string          `json:"roomId"`
	RoomID2    string          `json:"room_id"`
	MessageID  string          `json:"messageId"`
	MessageID2 string          `json:"message_id"`
	Payload    json.RawMessage `json:"payload"`
}

type discussionCommentEnvelope struct {
	Type              string          `json:"type"`
	RoomID            string          `json:"roomId"`
	RoomID2           string          `json:"room_id"`
	PinMessageID      string          `json:"pinMessageId"`
	PinMessageID2     string          `json:"pin_message_id"`
	ParentCommentID   string          `json:"parentCommentId"`
	ParentCommentID2  string          `json:"parent_comment_id"`
	ReplyToMessageID  string          `json:"replyToMessageId"`
	ReplyToMessageID2 string          `json:"reply_to_message_id"`
	Content           string          `json:"content"`
	Payload           json.RawMessage `json:"payload"`
}

type discussionCommentPinEnvelope struct {
	Type          string          `json:"type"`
	RoomID        string          `json:"roomId"`
	RoomID2       string          `json:"room_id"`
	PinMessageID  string          `json:"pinMessageId"`
	PinMessageID2 string          `json:"pin_message_id"`
	CommentID     string          `json:"commentId"`
	CommentID2    string          `json:"comment_id"`
	IsPinned      bool            `json:"isPinned"`
	Payload       json.RawMessage `json:"payload"`
}

type clientTypingPayload struct {
	RoomID   string
	IsTyping bool
}

type clientMessageEditPayload struct {
	RoomID    string
	MessageID string
	Content   string
}

type clientMessageDeletePayload struct {
	RoomID    string
	MessageID string
}

type clientDiscussionCommentPayload struct {
	RoomID          string
	PinMessageID    string
	ParentCommentID string
	Content         string
}

type clientDiscussionCommentPinPayload struct {
	RoomID       string
	PinMessageID string
	CommentID    string
	IsPinned     bool
}

func parseSubscribeRoomIDs(raw []byte) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	var envelope subscribeEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false
	}
	if strings.ToLower(strings.TrimSpace(envelope.Type)) != "subscribe" {
		return nil, false
	}

	candidates := make([]string, 0, len(envelope.RoomIDs)+len(envelope.Rooms))
	candidates = append(candidates, envelope.RoomIDs...)
	candidates = append(candidates, envelope.Rooms...)

	if len(envelope.Payload) > 0 {
		var asList []string
		if err := json.Unmarshal(envelope.Payload, &asList); err == nil {
			candidates = append(candidates, asList...)
		} else {
			var payload subscribePayload
			if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
				candidates = append(candidates, payload.RoomIDs...)
				candidates = append(candidates, payload.Rooms...)
			}
		}
	}

	unique := make(map[string]struct{})
	roomIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		normalized := normalizeRoomID(candidate)
		if normalized == "" {
			continue
		}
		if _, exists := unique[normalized]; exists {
			continue
		}
		unique[normalized] = struct{}{}
		roomIDs = append(roomIDs, normalized)
	}

	return roomIDs, true
}

func parseTypingPayload(raw []byte) (clientTypingPayload, bool) {
	if len(raw) == 0 {
		return clientTypingPayload{}, false
	}
	var envelope typingEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return clientTypingPayload{}, false
	}
	eventType := strings.ToLower(strings.TrimSpace(envelope.Type))
	if eventType != "typing_start" && eventType != "typing_stop" {
		return clientTypingPayload{}, false
	}

	roomID := normalizeRoomID(envelope.RoomID)
	if roomID == "" {
		roomID = normalizeRoomID(envelope.RoomID2)
	}
	if roomID == "" && len(envelope.Payload) > 0 {
		var payload typingPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
			roomID = normalizeRoomID(payload.RoomID)
			if roomID == "" {
				roomID = normalizeRoomID(payload.RoomID2)
			}
		}
	}
	if roomID == "" {
		return clientTypingPayload{}, false
	}

	return clientTypingPayload{
		RoomID:   roomID,
		IsTyping: eventType == "typing_start",
	}, true
}

func parseMessageEditPayload(raw []byte) (clientMessageEditPayload, bool) {
	if len(raw) == 0 {
		return clientMessageEditPayload{}, false
	}
	var envelope messageEditEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return clientMessageEditPayload{}, false
	}
	if strings.ToLower(strings.TrimSpace(envelope.Type)) != "message_edit" {
		return clientMessageEditPayload{}, false
	}

	roomID := normalizeRoomID(envelope.RoomID)
	if roomID == "" {
		roomID = normalizeRoomID(envelope.RoomID2)
	}
	messageID := normalizeMessageID(envelope.MessageID)
	if messageID == "" {
		messageID = normalizeMessageID(envelope.MessageID2)
	}
	content := strings.TrimSpace(envelope.Content)

	if len(envelope.Payload) > 0 {
		var payload messageEditEnvelope
		if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
			if roomID == "" {
				roomID = normalizeRoomID(payload.RoomID)
				if roomID == "" {
					roomID = normalizeRoomID(payload.RoomID2)
				}
			}
			if messageID == "" {
				messageID = normalizeMessageID(payload.MessageID)
				if messageID == "" {
					messageID = normalizeMessageID(payload.MessageID2)
				}
			}
			if content == "" {
				content = strings.TrimSpace(payload.Content)
			}
		}
	}

	if roomID == "" || messageID == "" || content == "" {
		return clientMessageEditPayload{}, false
	}
	if len(content) > maxTextChars {
		content = content[:maxTextChars]
	}

	return clientMessageEditPayload{
		RoomID:    roomID,
		MessageID: messageID,
		Content:   content,
	}, true
}

func parseMessageDeletePayload(raw []byte) (clientMessageDeletePayload, bool) {
	if len(raw) == 0 {
		return clientMessageDeletePayload{}, false
	}
	var envelope messageDeleteEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return clientMessageDeletePayload{}, false
	}
	if strings.ToLower(strings.TrimSpace(envelope.Type)) != "message_delete" {
		return clientMessageDeletePayload{}, false
	}

	roomID := normalizeRoomID(envelope.RoomID)
	if roomID == "" {
		roomID = normalizeRoomID(envelope.RoomID2)
	}
	messageID := normalizeMessageID(envelope.MessageID)
	if messageID == "" {
		messageID = normalizeMessageID(envelope.MessageID2)
	}

	if len(envelope.Payload) > 0 && (roomID == "" || messageID == "") {
		var payload messageDeleteEnvelope
		if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
			if roomID == "" {
				roomID = normalizeRoomID(payload.RoomID)
				if roomID == "" {
					roomID = normalizeRoomID(payload.RoomID2)
				}
			}
			if messageID == "" {
				messageID = normalizeMessageID(payload.MessageID)
				if messageID == "" {
					messageID = normalizeMessageID(payload.MessageID2)
				}
			}
		}
	}

	if roomID == "" || messageID == "" {
		return clientMessageDeletePayload{}, false
	}

	return clientMessageDeletePayload{
		RoomID:    roomID,
		MessageID: messageID,
	}, true
}

func parseDiscussionCommentPayload(raw []byte) (clientDiscussionCommentPayload, bool) {
	if len(raw) == 0 {
		return clientDiscussionCommentPayload{}, false
	}

	var envelope discussionCommentEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return clientDiscussionCommentPayload{}, false
	}
	if strings.ToLower(strings.TrimSpace(envelope.Type)) != "discussion_comment" {
		return clientDiscussionCommentPayload{}, false
	}

	roomID := normalizeRoomID(envelope.RoomID)
	if roomID == "" {
		roomID = normalizeRoomID(envelope.RoomID2)
	}
	pinMessageID := normalizeMessageID(envelope.PinMessageID)
	if pinMessageID == "" {
		pinMessageID = normalizeMessageID(envelope.PinMessageID2)
	}
	parentCommentID := normalizeMessageID(envelope.ParentCommentID)
	if parentCommentID == "" {
		parentCommentID = normalizeMessageID(envelope.ParentCommentID2)
	}
	content := strings.TrimSpace(envelope.Content)

	if len(envelope.Payload) > 0 {
		var payload discussionCommentEnvelope
		if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
			if roomID == "" {
				roomID = normalizeRoomID(payload.RoomID)
				if roomID == "" {
					roomID = normalizeRoomID(payload.RoomID2)
				}
			}
			if pinMessageID == "" {
				pinMessageID = normalizeMessageID(payload.PinMessageID)
				if pinMessageID == "" {
					pinMessageID = normalizeMessageID(payload.PinMessageID2)
				}
			}
			if parentCommentID == "" {
				parentCommentID = normalizeMessageID(payload.ParentCommentID)
				if parentCommentID == "" {
					parentCommentID = normalizeMessageID(payload.ParentCommentID2)
				}
			}
			if content == "" {
				content = strings.TrimSpace(payload.Content)
			}
			if pinMessageID == "" {
				pinMessageID = normalizeMessageID(payload.ReplyToMessageID)
				if pinMessageID == "" {
					pinMessageID = normalizeMessageID(payload.ReplyToMessageID2)
				}
			}
		}
	}

	if pinMessageID == "" {
		pinMessageID = normalizeMessageID(envelope.ReplyToMessageID)
		if pinMessageID == "" {
			pinMessageID = normalizeMessageID(envelope.ReplyToMessageID2)
		}
	}
	if roomID == "" || pinMessageID == "" || content == "" {
		return clientDiscussionCommentPayload{}, false
	}
	if len(content) > maxTextChars {
		content = content[:maxTextChars]
	}

	return clientDiscussionCommentPayload{
		RoomID:          roomID,
		PinMessageID:    pinMessageID,
		ParentCommentID: parentCommentID,
		Content:         content,
	}, true
}

func parseDiscussionCommentPinPayload(raw []byte) (clientDiscussionCommentPinPayload, bool) {
	if len(raw) == 0 {
		return clientDiscussionCommentPinPayload{}, false
	}

	var envelope discussionCommentPinEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return clientDiscussionCommentPinPayload{}, false
	}
	if strings.ToLower(strings.TrimSpace(envelope.Type)) != "discussion_comment_pin" {
		return clientDiscussionCommentPinPayload{}, false
	}

	roomID := normalizeRoomID(envelope.RoomID)
	if roomID == "" {
		roomID = normalizeRoomID(envelope.RoomID2)
	}
	pinMessageID := normalizeMessageID(envelope.PinMessageID)
	if pinMessageID == "" {
		pinMessageID = normalizeMessageID(envelope.PinMessageID2)
	}
	commentID := normalizeMessageID(envelope.CommentID)
	if commentID == "" {
		commentID = normalizeMessageID(envelope.CommentID2)
	}
	isPinned := envelope.IsPinned

	if len(envelope.Payload) > 0 {
		var payload discussionCommentPinEnvelope
		if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
			if roomID == "" {
				roomID = normalizeRoomID(payload.RoomID)
				if roomID == "" {
					roomID = normalizeRoomID(payload.RoomID2)
				}
			}
			if pinMessageID == "" {
				pinMessageID = normalizeMessageID(payload.PinMessageID)
				if pinMessageID == "" {
					pinMessageID = normalizeMessageID(payload.PinMessageID2)
				}
			}
			if commentID == "" {
				commentID = normalizeMessageID(payload.CommentID)
				if commentID == "" {
					commentID = normalizeMessageID(payload.CommentID2)
				}
			}
			isPinned = payload.IsPinned
		}
	}

	if roomID == "" || pinMessageID == "" || commentID == "" {
		return clientDiscussionCommentPinPayload{}, false
	}

	return clientDiscussionCommentPinPayload{
		RoomID:       roomID,
		PinMessageID: pinMessageID,
		CommentID:    commentID,
		IsPinned:     isPinned,
	}, true
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
	msg.ReplyToMessageID = normalizeMessageID(msg.ReplyToMessageID)
	msg.ReplyToSnippet = strings.TrimSpace(msg.ReplyToSnippet)
	if len(msg.ReplyToSnippet) > 140 {
		msg.ReplyToSnippet = msg.ReplyToSnippet[:140]
	}
	if msg.ReplyToMessageID == "" {
		msg.ReplyToSnippet = ""
	}

	switch msg.Type {
	case "", "text":
		msg.Type = "text"
		return msg.Content != "" && len(msg.Content) <= maxTextChars
	case "task":
		return msg.Content != "" && len(msg.Content) <= maxTextChars
	case "image", "video", "file", "audio":
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

func normalizeMessageID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range trimmed {
		if (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '_' ||
			ch == '-' {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
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
	estimated := len(msg.ID) + len(msg.RoomID) + len(msg.SenderID) + len(msg.SenderName) + len(msg.Content) + len(msg.Type) + len(msg.MediaURL) + len(msg.MediaType) + len(msg.FileName) + len(msg.ReplyToMessageID) + len(msg.ReplyToSnippet)
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

func (c *Client) cleanupConnectionTracking() {
	if c == nil {
		return
	}
	c.disconnectOnce.Do(func() {
		if c.onDisconnect != nil {
			c.onDisconnect()
		}
	})
}

func (c *Client) closeSendChannel() {
	if c == nil {
		return
	}
	c.sendCloseOnce.Do(func() {
		close(c.Send)
	})
}

func (c *Client) subscribeToRoom(roomID string, canWrite bool) {
	if c == nil || roomID == "" {
		return
	}
	c.subscriptionsMu.Lock()
	subscription := c.subscribedRooms[roomID]
	subscription.CanWrite = subscription.CanWrite || canWrite
	c.subscribedRooms[roomID] = subscription
	c.subscriptionsMu.Unlock()
}

func (c *Client) unsubscribeFromRoom(roomID string) {
	if c == nil || roomID == "" {
		return
	}
	c.subscriptionsMu.Lock()
	delete(c.subscribedRooms, roomID)
	c.subscriptionsMu.Unlock()
}

func (c *Client) isSubscribedToRoom(roomID string) bool {
	if c == nil || roomID == "" {
		return false
	}
	c.subscriptionsMu.RLock()
	_, exists := c.subscribedRooms[roomID]
	c.subscriptionsMu.RUnlock()
	return exists
}

func (c *Client) canWriteToRoom(roomID string) bool {
	if c == nil || roomID == "" {
		return false
	}
	c.subscriptionsMu.RLock()
	subscription, exists := c.subscribedRooms[roomID]
	c.subscriptionsMu.RUnlock()
	if !exists {
		return false
	}
	return subscription.CanWrite
}

func reserveWSConnection(clientIP string) (func(), int, string) {
	for {
		currentGlobal := globalWSConnections.Load()
		if currentGlobal >= maxGlobalWSConnections {
			return nil, http.StatusServiceUnavailable, "WebSocket capacity reached"
		}
		if globalWSConnections.CompareAndSwap(currentGlobal, currentGlobal+1) {
			break
		}
	}

	ipCounter := getOrCreateIPConnectionCounter(clientIP)
	for {
		currentIP := ipCounter.Load()
		if currentIP >= maxWSConnectionsPerIP {
			decrementGlobalWSConnections()
			return nil, http.StatusTooManyRequests, "Too many active WebSocket connections for this IP"
		}
		if ipCounter.CompareAndSwap(currentIP, currentIP+1) {
			return func() {
				releaseWSConnection(clientIP)
			}, 0, ""
		}
	}
}

func getOrCreateIPConnectionCounter(clientIP string) *atomic.Int32 {
	normalizedIP := strings.TrimSpace(clientIP)
	if normalizedIP == "" {
		normalizedIP = "unknown"
	}
	counter, _ := activeConnectionsPerIP.LoadOrStore(normalizedIP, &atomic.Int32{})
	return counter.(*atomic.Int32)
}

func releaseWSConnection(clientIP string) {
	decrementGlobalWSConnections()

	normalizedIP := strings.TrimSpace(clientIP)
	if normalizedIP == "" {
		normalizedIP = "unknown"
	}

	entry, ok := activeConnectionsPerIP.Load(normalizedIP)
	if !ok {
		return
	}

	counter := entry.(*atomic.Int32)
	for {
		current := counter.Load()
		if current <= 0 {
			activeConnectionsPerIP.Delete(normalizedIP)
			return
		}
		if counter.CompareAndSwap(current, current-1) {
			if current-1 <= 0 {
				activeConnectionsPerIP.Delete(normalizedIP)
			}
			return
		}
	}
}

func decrementGlobalWSConnections() {
	for {
		current := globalWSConnections.Load()
		if current <= 0 {
			return
		}
		if globalWSConnections.CompareAndSwap(current, current-1) {
			return
		}
	}
}
