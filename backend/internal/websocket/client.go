package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
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

	maxGlobalWSConnections = int32(60000)
	maxWSConnectionsPerIP  = int32(2000)

	messageTypeCallInvite   = "call_invite"
	messageTypeWebRTCOffer  = "webrtc_offer"
	messageTypeWebRTCAnswer = "webrtc_answer"
	messageTypeWebRTCIce    = "webrtc_ice"
	messageTypeCallLog      = "call_log"
	callTypeAudio           = "audio"
	callTypeVideo           = "video"
)

var (
	wsConnectLimiter = security.NewLimiter(1000, time.Minute, 600, 15*time.Minute)

	globalWSConnections    atomic.Int32
	activeConnectionsPerIP sync.Map

	trustedProxiesMu     sync.RWMutex
	trustedProxyMatchers []trustedProxyMatcher
)

type trustedProxyMatcher struct {
	ipNet *net.IPNet
	ip    net.IP
}

type originalRemoteAddrContextKey struct{}

var originalRemoteAddrKey = originalRemoteAddrContextKey{}

var upgrader = websocket.Upgrader{
	ReadBufferSize:    1024,
	WriteBufferSize:   1024,
	EnableCompression: true,
	// dev only
	CheckOrigin: func(r *http.Request) bool { return true },
}

func CaptureOriginalRemoteAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil {
			next.ServeHTTP(w, r)
			return
		}
		rawRemoteAddr := strings.TrimSpace(r.RemoteAddr)
		if rawRemoteAddr == "" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), originalRemoteAddrKey, rawRemoteAddr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func SetTrustedProxies(entries []string) {
	nextMatchers := make([]trustedProxyMatcher, 0, len(entries))
	for _, entry := range entries {
		candidate := strings.TrimSpace(entry)
		if candidate == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(candidate); err == nil && network != nil {
			nextMatchers = append(nextMatchers, trustedProxyMatcher{ipNet: network})
			continue
		}
		if ip := net.ParseIP(candidate); ip != nil {
			nextMatchers = append(nextMatchers, trustedProxyMatcher{ip: ip})
			continue
		}
		log.Printf("[ws] ignoring invalid trusted proxy entry=%q", candidate)
	}

	trustedProxiesMu.Lock()
	trustedProxyMatchers = nextMatchers
	trustedProxiesMu.Unlock()
	log.Printf("[ws] trusted proxies configured count=%d", len(nextMatchers))
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
		return
	}
	releaseReservation, status, rejectReason := reserveWSConnection(clientIP)
	if releaseReservation == nil {
		http.Error(w, rejectReason, status)
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
		if boardEvent, isBoardEvent := parseBoardEventPayload(raw); isBoardEvent {
			if c.Hub != nil {
				if boardEvent.Type == boardCursorMoveType {
					c.forwardBoardCursorMove(boardEvent)
					continue
				}
				if boardEvent.Type == boardClearType {
					c.handleBoardClear(boardEvent)
					continue
				}
				c.Hub.boardEvent <- &ClientBoardEvent{
					Client:    c,
					Type:      boardEvent.Type,
					RoomID:    boardEvent.RoomID,
					Payload:   boardEvent.Payload,
					Element:   boardEvent.Element,
					ElementID: boardEvent.ElementID,
				}
			}
			continue
		}
		if signalingEvent, isSignalingEvent := parseSignalingPayload(raw); isSignalingEvent {
			c.forwardSignalingEvent(signalingEvent)
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
			continue
		}
		if !c.isSubscribedToRoom(msg.RoomID) {
			continue
		}
		if !c.canWriteToRoom(msg.RoomID) {
			continue
		}
		if c.Hub != nil && !c.Hub.isClientRoomMember(c.UserID, msg.RoomID) {
			c.subscribeToRoom(msg.RoomID, false)
			continue
		}
		if c.msgLimiter != nil && !c.msgLimiter.Allow() {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(msg.Type), messageTypeCallLog) && strings.TrimSpace(msg.MediaType) == "" {
			var envelopeMap map[string]interface{}
			if err := json.Unmarshal(raw, &envelopeMap); err == nil {
				msg.MediaType = strings.ToLower(strings.TrimSpace(readStringFromMap(envelopeMap, "callType", "call_type", "mediaType", "media_type")))
				if msg.MediaType == "" {
					if payloadMap, ok := envelopeMap["payload"].(map[string]interface{}); ok {
						msg.MediaType = strings.ToLower(strings.TrimSpace(readStringFromMap(payloadMap, "callType", "call_type", "mediaType", "media_type")))
					}
				}
			}
		}
		if !normalizeInboundMessage(&msg) {
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

func (c *Client) canProcessRoomBoardBroadcast(roomID string) bool {
	if c == nil || c.Hub == nil {
		return false
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return false
	}
	if !c.isSubscribedToRoom(normalizedRoomID) || !c.canWriteToRoom(normalizedRoomID) {
		return false
	}
	if !c.Hub.isClientRoomMember(c.UserID, normalizedRoomID) {
		c.subscribeToRoom(normalizedRoomID, false)
		return false
	}
	return true
}

func (c *Client) forwardBoardCursorMove(event clientBoardEventPayload) {
	if !c.canProcessRoomBoardBroadcast(event.RoomID) {
		return
	}
	payload := map[string]interface{}{
		"type": boardCursorMoveType,
	}
	for key, value := range event.Payload {
		payload[key] = value
	}
	if _, ok := payload["payload"]; !ok {
		payload["payload"] = map[string]interface{}{}
	}
	c.Hub.BroadcastToRoom(event.RoomID, payload)
}

func (c *Client) handleBoardClear(event clientBoardEventPayload) {
	if !c.canProcessRoomBoardBroadcast(event.RoomID) {
		return
	}
	normalizedRoomID := normalizeRoomID(event.RoomID)
	if normalizedRoomID == "" {
		return
	}
	isRoomAdmin, adminErr := c.Hub.isClientRoomAdmin(normalizeUsername(c.UserID), normalizedRoomID)
	if adminErr != nil {
		log.Printf("[ws] board clear admin lookup failed room=%s user=%s err=%v", normalizedRoomID, c.UserID, adminErr)
		c.Hub.sendBoardError(c, normalizedRoomID, "board_permission_check_failed", "Unable to verify board permissions. Please retry.", "")
		return
	}
	if !isRoomAdmin {
		c.Hub.sendBoardError(c, normalizedRoomID, "board_permission_denied", "Only room admin can clear the board.", "")
		return
	}
	if c.Hub.msgService == nil {
		c.Hub.sendBoardError(c, normalizedRoomID, "board_clear_failed", "Unable to clear board right now. Please retry.", "")
		return
	}
	if err := c.Hub.msgService.ClearBoardElements(context.Background(), normalizedRoomID); err != nil {
		log.Printf("[ws] board clear persist failed room=%s user=%s err=%v", normalizedRoomID, c.UserID, err)
		c.Hub.sendBoardError(c, normalizedRoomID, "board_clear_failed", "Unable to clear board. Please retry.", "")
		return
	}

	payload := map[string]interface{}{
		"type": boardClearType,
	}
	for key, value := range event.Payload {
		payload[key] = value
	}
	if _, ok := payload["payload"]; !ok {
		payload["payload"] = map[string]interface{}{}
	}
	c.Hub.BroadcastToRoom(normalizedRoomID, payload)
}

func (c *Client) forwardSignalingEvent(event clientSignalingPayload) {
	if c == nil || c.Hub == nil {
		return
	}
	if !c.canProcessRoomBoardBroadcast(event.RoomID) {
		return
	}

	payload := map[string]interface{}{
		"type": event.Type,
	}
	for key, value := range event.Payload {
		payload[key] = value
	}
	payload["fromUserId"] = c.UserID
	payload["fromUserName"] = c.Username
	if event.TargetUserID != "" {
		payload["targetUserId"] = event.TargetUserID
	}

	if nestedPayload, ok := payload["payload"].(map[string]interface{}); ok && nestedPayload != nil {
		if _, exists := nestedPayload["fromUserId"]; !exists {
			nestedPayload["fromUserId"] = c.UserID
		}
		if _, exists := nestedPayload["fromUserName"]; !exists {
			nestedPayload["fromUserName"] = c.Username
		}
		if event.TargetUserID != "" {
			if _, exists := nestedPayload["targetUserId"]; !exists {
				nestedPayload["targetUserId"] = event.TargetUserID
			}
		}
	} else if payload["payload"] == nil {
		payload["payload"] = map[string]interface{}{
			"fromUserId":   c.UserID,
			"fromUserName": c.Username,
		}
	}

	c.Hub.BroadcastToRoom(event.RoomID, payload)
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

type boardEventEnvelope struct {
	Type       string          `json:"type"`
	RoomID     string          `json:"roomId"`
	RoomID2    string          `json:"room_id"`
	ElementID  string          `json:"elementId"`
	ElementID2 string          `json:"element_id"`
	Payload    json.RawMessage `json:"payload"`
}

type signalingEnvelope struct {
	Type          string          `json:"type"`
	RoomID        string          `json:"roomId"`
	RoomID2       string          `json:"room_id"`
	TargetUserID  string          `json:"targetUserId"`
	TargetUserID2 string          `json:"target_user_id"`
	TargetUser    string          `json:"targetUser"`
	TargetUser2   string          `json:"target_user"`
	Payload       json.RawMessage `json:"payload"`
}

type clientBoardEventPayload struct {
	Type      string
	RoomID    string
	Payload   map[string]interface{}
	Element   *models.BoardElement
	ElementID string
}

type clientSignalingPayload struct {
	Type         string
	RoomID       string
	TargetUserID string
	Payload      map[string]interface{}
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

func isTransientSignalingType(eventType string) bool {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case messageTypeCallInvite, messageTypeWebRTCOffer, messageTypeWebRTCAnswer, messageTypeWebRTCIce:
		return true
	default:
		return false
	}
}

func parseSignalingPayload(raw []byte) (clientSignalingPayload, bool) {
	if len(raw) == 0 {
		return clientSignalingPayload{}, false
	}

	var envelope signalingEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return clientSignalingPayload{}, false
	}
	eventType := strings.ToLower(strings.TrimSpace(envelope.Type))
	if !isTransientSignalingType(eventType) {
		return clientSignalingPayload{}, false
	}

	var envelopeMap map[string]interface{}
	if err := json.Unmarshal(raw, &envelopeMap); err != nil {
		return clientSignalingPayload{}, false
	}

	roomID := normalizeRoomID(envelope.RoomID)
	if roomID == "" {
		roomID = normalizeRoomID(envelope.RoomID2)
	}
	if roomID == "" {
		roomID = normalizeRoomID(readStringFromMap(envelopeMap, "roomId", "room_id"))
	}
	if roomID == "" {
		if nestedPayload, ok := envelopeMap["payload"].(map[string]interface{}); ok {
			roomID = normalizeRoomID(readStringFromMap(nestedPayload, "roomId", "room_id"))
		}
	}
	if roomID == "" {
		return clientSignalingPayload{}, false
	}

	targetCandidate := ""
	for _, candidate := range []string{
		envelope.TargetUserID,
		envelope.TargetUserID2,
		envelope.TargetUser,
		envelope.TargetUser2,
	} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		targetCandidate = candidate
		break
	}
	targetUserID := normalizeUsername(targetCandidate)
	if targetUserID == "" {
		targetUserID = normalizeUsername(
			readStringFromMap(
				envelopeMap,
				"targetUserId",
				"target_user_id",
				"targetUser",
				"target_user",
			),
		)
	}
	if targetUserID == "" {
		if nestedPayload, ok := envelopeMap["payload"].(map[string]interface{}); ok {
			targetUserID = normalizeUsername(
				readStringFromMap(
					nestedPayload,
					"targetUserId",
					"target_user_id",
					"targetUser",
					"target_user",
				),
			)
		}
	}

	broadcastPayload := map[string]interface{}{}
	for key, value := range envelopeMap {
		loweredKey := strings.ToLower(strings.TrimSpace(key))
		if loweredKey == "type" || loweredKey == "roomid" || loweredKey == "room_id" {
			continue
		}
		broadcastPayload[key] = value
	}

	return clientSignalingPayload{
		Type:         eventType,
		RoomID:       roomID,
		TargetUserID: targetUserID,
		Payload:      broadcastPayload,
	}, true
}

func parseBoardEventPayload(raw []byte) (clientBoardEventPayload, bool) {
	if len(raw) == 0 {
		return clientBoardEventPayload{}, false
	}

	var envelope boardEventEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return clientBoardEventPayload{}, false
	}

	eventType := strings.ToLower(strings.TrimSpace(envelope.Type))
	if !isBoardEventType(eventType) {
		return clientBoardEventPayload{}, false
	}

	var envelopeMap map[string]interface{}
	if err := json.Unmarshal(raw, &envelopeMap); err != nil {
		return clientBoardEventPayload{}, false
	}

	roomID := normalizeRoomID(envelope.RoomID)
	if roomID == "" {
		roomID = normalizeRoomID(envelope.RoomID2)
	}
	if roomID == "" {
		roomID = normalizeRoomID(readStringFromMap(envelopeMap, "roomId", "room_id"))
	}
	if roomID == "" {
		if nestedPayload, ok := envelopeMap["payload"].(map[string]interface{}); ok {
			roomID = normalizeRoomID(readStringFromMap(nestedPayload, "roomId", "room_id"))
		}
	}
	if roomID == "" {
		return clientBoardEventPayload{}, false
	}

	broadcastPayload := map[string]interface{}{}
	for key, value := range envelopeMap {
		loweredKey := strings.ToLower(strings.TrimSpace(key))
		if loweredKey == "type" || loweredKey == "roomid" || loweredKey == "room_id" {
			continue
		}
		broadcastPayload[key] = value
	}

	elementID := normalizeMessageID(envelope.ElementID)
	if elementID == "" {
		elementID = normalizeMessageID(envelope.ElementID2)
	}
	if elementID == "" {
		elementID = parseBoardElementIDFromEnvelope(envelopeMap)
	}

	var element *models.BoardElement
	if eventType == boardElementAddType {
		element = parseBoardElementFromEnvelope(roomID, eventType, envelopeMap)
		if element != nil {
			elementID = normalizeMessageID(element.ElementID)
		}
	}

	return clientBoardEventPayload{
		Type:      eventType,
		RoomID:    roomID,
		Payload:   broadcastPayload,
		Element:   element,
		ElementID: elementID,
	}, true
}

func parseBoardElementFromEnvelope(roomID, eventType string, envelopeMap map[string]interface{}) *models.BoardElement {
	candidates := make([]map[string]interface{}, 0, 4)
	if payloadMap, ok := envelopeMap["payload"].(map[string]interface{}); ok {
		if payloadElementMap, ok := payloadMap["element"].(map[string]interface{}); ok {
			candidates = append(candidates, payloadElementMap)
		}
		candidates = append(candidates, payloadMap)
	}
	if elementMap, ok := envelopeMap["element"].(map[string]interface{}); ok {
		candidates = append(candidates, elementMap)
	}
	candidates = append(candidates, envelopeMap)

	elementID := ""
	for _, candidate := range candidates {
		elementID = normalizeMessageID(readStringFromMap(candidate, "elementId", "element_id", "id"))
		if elementID != "" {
			break
		}
	}
	if elementID == "" {
		return nil
	}

	elementType := ""
	for _, candidate := range candidates {
		rawType := strings.ToLower(strings.TrimSpace(readStringFromMap(candidate, "elementType", "element_type", "kind", "type")))
		if rawType == "" || rawType == eventType || isBoardEventType(rawType) {
			continue
		}
		elementType = rawType
		break
	}
	if elementType == "" {
		elementType = "shape"
	}

	x := float32(0)
	for _, candidate := range candidates {
		if value, ok := readFloat32FromMap(candidate, "x"); ok {
			x = value
			break
		}
	}
	y := float32(0)
	for _, candidate := range candidates {
		if value, ok := readFloat32FromMap(candidate, "y"); ok {
			y = value
			break
		}
	}
	width := float32(0)
	for _, candidate := range candidates {
		if value, ok := readFloat32FromMap(candidate, "width", "w"); ok {
			width = value
			break
		}
	}
	height := float32(0)
	for _, candidate := range candidates {
		if value, ok := readFloat32FromMap(candidate, "height", "h"); ok {
			height = value
			break
		}
	}

	content := ""
	for _, candidate := range candidates {
		if value, ok := candidate["content"]; ok {
			content = stringifyBoardContent(value)
			if content != "" {
				break
			}
		}
	}

	zIndex := 0
	for _, candidate := range candidates {
		if value, ok := readIntFromMap(candidate, "zIndex", "z_index", "z"); ok {
			zIndex = value
			break
		}
	}

	createdAt := time.Time{}
	for _, candidate := range candidates {
		if value, ok := candidate["createdAt"]; ok {
			createdAt = parseBoardTimestamp(value)
			if !createdAt.IsZero() {
				break
			}
		}
		if value, ok := candidate["created_at"]; ok {
			createdAt = parseBoardTimestamp(value)
			if !createdAt.IsZero() {
				break
			}
		}
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	return &models.BoardElement{
		RoomID:    normalizeRoomID(roomID),
		ElementID: elementID,
		Type:      elementType,
		X:         x,
		Y:         y,
		Width:     width,
		Height:    height,
		Content:   content,
		ZIndex:    zIndex,
		CreatedAt: createdAt,
	}
}

func parseBoardElementIDFromEnvelope(envelopeMap map[string]interface{}) string {
	candidates := make([]map[string]interface{}, 0, 3)
	if payloadMap, ok := envelopeMap["payload"].(map[string]interface{}); ok {
		if payloadElementMap, ok := payloadMap["element"].(map[string]interface{}); ok {
			candidates = append(candidates, payloadElementMap)
		}
		candidates = append(candidates, payloadMap)
	}
	candidates = append(candidates, envelopeMap)

	for _, candidate := range candidates {
		value := normalizeMessageID(readStringFromMap(candidate, "elementId", "element_id", "id"))
		if value != "" {
			return value
		}
	}
	return ""
}

func readStringFromMap(source map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := source[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				return trimmed
			}
		case json.Number:
			trimmed := strings.TrimSpace(typed.String())
			if trimmed != "" {
				return trimmed
			}
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		case int:
			return strconv.Itoa(typed)
		case int32:
			return strconv.FormatInt(int64(typed), 10)
		case int64:
			return strconv.FormatInt(typed, 10)
		}
	}
	return ""
}

func readFloat32FromMap(source map[string]interface{}, keys ...string) (float32, bool) {
	for _, key := range keys {
		value, ok := source[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float32:
			return typed, true
		case float64:
			return float32(typed), true
		case int:
			return float32(typed), true
		case int32:
			return float32(typed), true
		case int64:
			return float32(typed), true
		case json.Number:
			if parsed, err := typed.Float64(); err == nil {
				return float32(parsed), true
			}
		case string:
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
				return float32(parsed), true
			}
		}
	}
	return 0, false
}

func readIntFromMap(source map[string]interface{}, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := source[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed, true
		case int32:
			return int(typed), true
		case int64:
			return int(typed), true
		case float32:
			return int(typed), true
		case float64:
			return int(typed), true
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return int(parsed), true
			}
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func parseBoardTimestamp(value interface{}) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC()
	case json.Number:
		if asInt, err := typed.Int64(); err == nil {
			return parseBoardUnixTimestamp(asInt)
		}
		if asFloat, err := typed.Float64(); err == nil {
			return parseBoardUnixTimestamp(int64(asFloat))
		}
	case float64:
		return parseBoardUnixTimestamp(int64(typed))
	case float32:
		return parseBoardUnixTimestamp(int64(typed))
	case int:
		return parseBoardUnixTimestamp(int64(typed))
	case int32:
		return parseBoardUnixTimestamp(int64(typed))
	case int64:
		return parseBoardUnixTimestamp(typed)
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return time.Time{}
		}
		if asInt, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return parseBoardUnixTimestamp(asInt)
		}
		if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return parsed.UTC()
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func parseBoardUnixTimestamp(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	// Milliseconds or microseconds are common in frontend timestamps.
	switch {
	case value >= 1_000_000_000_000_000:
		return time.UnixMicro(value).UTC()
	case value >= 1_000_000_000_000:
		return time.UnixMilli(value).UTC()
	default:
		return time.Unix(value, 0).UTC()
	}
}

func stringifyBoardContent(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.RawMessage:
		return strings.TrimSpace(string(typed))
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(encoded))
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
	case messageTypeCallLog:
		if msg.Content == "" || len(msg.Content) > maxTextChars {
			return false
		}
		msg.MediaURL = ""
		msg.FileName = ""
		callType := strings.ToLower(strings.TrimSpace(msg.MediaType))
		if callType != callTypeVideo {
			callType = callTypeAudio
		}
		msg.MediaType = callType
		return true
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

	sourceIP := parseIPFromAddr(originalRemoteAddrFromRequest(r))
	if sourceIP != nil && isTrustedProxy(sourceIP) {
		if forwardedIP := parseFirstForwardedIP(r.Header.Get("X-Forwarded-For")); forwardedIP != nil {
			return forwardedIP.String()
		}
		if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
			return realIP.String()
		}
	}

	if sourceIP != nil {
		return sourceIP.String()
	}

	if fallbackIP := parseIPFromAddr(strings.TrimSpace(r.RemoteAddr)); fallbackIP != nil {
		return fallbackIP.String()
	}

	return "unknown"
}

func originalRemoteAddrFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if raw, ok := r.Context().Value(originalRemoteAddrKey).(string); ok {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			return trimmed
		}
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func parseIPFromAddr(rawAddr string) net.IP {
	trimmed := strings.TrimSpace(rawAddr)
	if trimmed == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil && host != "" {
		trimmed = host
	}
	trimmed = strings.TrimPrefix(trimmed, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return nil
	}
	return ip
}

func parseFirstForwardedIP(rawHeader string) net.IP {
	trimmed := strings.TrimSpace(rawHeader)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}
		if ip := net.ParseIP(candidate); ip != nil {
			return ip
		}
	}
	return nil
}

func isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	trustedProxiesMu.RLock()
	defer trustedProxiesMu.RUnlock()
	for _, matcher := range trustedProxyMatchers {
		if matcher.ipNet != nil && matcher.ipNet.Contains(ip) {
			return true
		}
		if matcher.ip != nil && matcher.ip.Equal(ip) {
			return true
		}
	}
	return false
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
