package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/monitor"
)

const chatBroadcastChannel = "chat:broadcast"
const chatTypingChannel = "chat:typing"
const chatMutationChannel = "chat:message_mutation"
const chatDiscussionChannel = "chat:discussion_comment"
const chatRoomEventChannel = "chat:room_event"

type Hub struct {
	rooms                map[string]map[*Client]bool
	broadcast            chan models.Message
	redisInbox           chan models.Message
	typing               chan *ClientTypingEvent
	typingInbox          chan TypingRedisEvent
	boardEvent           chan *ClientBoardEvent
	taskEvent            chan *ClientTaskEvent
	discussionComment    chan *ClientDiscussionCommentEvent
	discussionCommentPin chan *ClientDiscussionCommentPinEvent
	messageReaction      chan *ClientMessageReactionEvent
	discussionInbox      chan DiscussionCommentEvent
	messageEdit          chan *ClientMessageEditEvent
	messageDelete        chan *ClientMessageDeleteEvent
	roomEvent            chan RoomEvent
	roomEventInbox       chan RoomEvent
	mutationInbox        chan MessageMutationEvent
	register             chan *Client
	unregister           chan *Client
	subscribe            chan *ClientSubscription

	msgService *MessageService
	tracker    *monitor.UsageTracker

	contextBuilder *ai.ContextBuilder
	agentEngine    *ai.AgentEngineFactory

	toraTypingMu     sync.Mutex
	toraTypingByRoom map[string]int
	toraRunMu        sync.Mutex
	toraRuns         map[string]toraRunHandle
}

type ClientTypingEvent struct {
	Client   *Client
	RoomID   string
	IsTyping bool
}

type TypingRedisEvent struct {
	RoomID    string `json:"roomId"`
	UserID    string `json:"userId"`
	UserName  string `json:"userName"`
	IsTyping  bool   `json:"isTyping"`
	UpdatedAt int64  `json:"updatedAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

type ClientBoardEvent struct {
	Client    *Client
	Type      string
	RoomID    string
	Payload   map[string]interface{}
	Element   *models.BoardElement
	ElementID string
}

type ClientTaskEvent struct {
	Client  *Client
	Payload TaskPayload
}

type ClientDiscussionCommentEvent struct {
	Client          *Client
	RoomID          string
	PinMessageID    string
	ParentCommentID string
	Content         string
}

type ClientDiscussionCommentPinEvent struct {
	Client       *Client
	RoomID       string
	PinMessageID string
	CommentID    string
	IsPinned     bool
}

type DiscussionCommentEvent struct {
	Type         string         `json:"type"`
	RoomID       string         `json:"roomId"`
	PinMessageID string         `json:"pinMessageId"`
	Payload      models.Message `json:"payload"`
}

type ClientMessageEditEvent struct {
	Client    *Client
	RoomID    string
	MessageID string
	Content   string
}

type ClientMessageDeleteEvent struct {
	Client    *Client
	RoomID    string
	MessageID string
}

type ClientMessageReactionEvent struct {
	Client    *Client
	RoomID    string
	MessageID string
	Emoji     string
}

type MessageMutationEvent struct {
	Type        string `json:"type"`
	RoomID      string `json:"roomId"`
	MessageID   string `json:"messageId"`
	Content     string `json:"content,omitempty"`
	MessageType string `json:"messageType,omitempty"`
	IsEdited    bool   `json:"isEdited"`
	EditedAt    int64  `json:"editedAt,omitempty"`
}

type RoomEvent struct {
	Type         string                 `json:"type"`
	RoomID       string                 `json:"roomId"`
	Payload      map[string]interface{} `json:"payload,omitempty"`
	OriginUserID string                 `json:"originUserId,omitempty"`
}

type ClientSubscription struct {
	Client  *Client
	RoomIDs []string
}

func boardMaxStorageBytes() int64 {
	return config.LoadAppLimits().Board.MaxStorageBytes
}

func boardStorageLimitLabel() string {
	limitBytes := boardMaxStorageBytes()
	if limitBytes <= 0 {
		return "0MB"
	}
	mb := float64(limitBytes) / (1024 * 1024)
	if mb == float64(int64(mb)) {
		return strconv.FormatInt(int64(mb), 10) + "MB"
	}
	return strconv.FormatFloat(mb, 'f', 1, 64) + "MB"
}

func NewHub(service *MessageService, tracker *monitor.UsageTracker) *Hub {
	hub := &Hub{
		broadcast:            make(chan models.Message, 4096),
		redisInbox:           make(chan models.Message, 4096),
		typing:               make(chan *ClientTypingEvent, 256),
		typingInbox:          make(chan TypingRedisEvent, 4096),
		boardEvent:           make(chan *ClientBoardEvent, 4096),
		taskEvent:            make(chan *ClientTaskEvent, 1024),
		discussionComment:    make(chan *ClientDiscussionCommentEvent, 256),
		discussionCommentPin: make(chan *ClientDiscussionCommentPinEvent, 256),
		messageReaction:      make(chan *ClientMessageReactionEvent, 256),
		discussionInbox:      make(chan DiscussionCommentEvent, 512),
		messageEdit:          make(chan *ClientMessageEditEvent, 256),
		messageDelete:        make(chan *ClientMessageDeleteEvent, 256),
		roomEvent:            make(chan RoomEvent, 256),
		roomEventInbox:       make(chan RoomEvent, 512),
		mutationInbox:        make(chan MessageMutationEvent, 512),
		register:             make(chan *Client, 4096),
		unregister:           make(chan *Client, 4096),
		subscribe:            make(chan *ClientSubscription, 256),
		rooms:                make(map[string]map[*Client]bool),
		msgService:           service,
		tracker:              tracker,
		toraTypingByRoom:     make(map[string]int),
		toraRuns:             make(map[string]toraRunHandle),
	}

	if service != nil && service.Scylla != nil {
		hub.contextBuilder = ai.NewContextBuilder(service.Scylla).WithRedis(service.Redis)
		hub.agentEngine = ai.NewAgentEngineFactory(hub.contextBuilder, resolveToraAgentProvider)
	}

	if service != nil && service.CanPersistToDisk() {
		for i := 0; i < 30; i++ {
			go hub.persistenceWorker()
		}
	}
	if service != nil && service.Redis != nil && service.Redis.Client != nil {
		go hub.Subscribe()
		go hub.SubscribeTyping()
		go hub.SubscribeDiscussionComments()
		go hub.SubscribeMessageMutations()
		go hub.SubscribeRoomEvents()
	}

	return hub
}

func (h *Hub) Subscribe() {
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return
	}

	ctx := context.Background()
	for {
		pubsub := h.msgService.Redis.Client.Subscribe(ctx, chatBroadcastChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			log.Printf("redis subscribe receive error: %v", err)
			_ = pubsub.Close()
			time.Sleep(time.Second)
			continue
		}

		channel := pubsub.Channel()
		for incoming := range channel {
			if incoming == nil || strings.TrimSpace(incoming.Payload) == "" {
				continue
			}

			var msg models.Message
			if err := json.Unmarshal([]byte(incoming.Payload), &msg); err != nil {
				log.Printf("redis subscribe unmarshal error: %v", err)
				continue
			}

			select {
			case h.redisInbox <- msg:
			default:
			}
		}

		_ = pubsub.Close()
		time.Sleep(time.Second)
	}
}

func (h *Hub) SubscribeTyping() {
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return
	}

	ctx := context.Background()
	for {
		pubsub := h.msgService.Redis.Client.Subscribe(ctx, chatTypingChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			log.Printf("redis typing subscribe receive error: %v", err)
			_ = pubsub.Close()
			time.Sleep(time.Second)
			continue
		}

		channel := pubsub.Channel()
		for incoming := range channel {
			if incoming == nil || strings.TrimSpace(incoming.Payload) == "" {
				continue
			}
			var event TypingRedisEvent
			if err := json.Unmarshal([]byte(incoming.Payload), &event); err != nil {
				log.Printf("redis typing unmarshal error: %v", err)
				continue
			}
			event.RoomID = normalizeRoomID(event.RoomID)
			event.UserID = strings.TrimSpace(event.UserID)
			event.UserName = strings.TrimSpace(event.UserName)
			if event.RoomID == "" || event.UserID == "" {
				continue
			}
			select {
			case h.typingInbox <- event:
			default:
			}
		}

		_ = pubsub.Close()
		time.Sleep(time.Second)
	}
}

func (h *Hub) SubscribeDiscussionComments() {
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return
	}

	ctx := context.Background()
	for {
		pubsub := h.msgService.Redis.Client.Subscribe(ctx, chatDiscussionChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			log.Printf("redis discussion subscribe receive error: %v", err)
			_ = pubsub.Close()
			time.Sleep(time.Second)
			continue
		}

		channel := pubsub.Channel()
		for incoming := range channel {
			if incoming == nil || strings.TrimSpace(incoming.Payload) == "" {
				continue
			}
			var event DiscussionCommentEvent
			if err := json.Unmarshal([]byte(incoming.Payload), &event); err != nil {
				log.Printf("redis discussion unmarshal error: %v", err)
				continue
			}
			event.Type = strings.ToLower(strings.TrimSpace(event.Type))
			event.RoomID = normalizeRoomID(event.RoomID)
			event.PinMessageID = normalizeMessageID(event.PinMessageID)
			event.Payload.RoomID = normalizeRoomID(event.Payload.RoomID)
			event.Payload.ID = normalizeMessageID(event.Payload.ID)
			event.Payload.ReplyToMessageID = normalizeMessageID(event.Payload.ReplyToMessageID)
			if event.Type == "" {
				event.Type = "discussion_comment"
			}
			if event.Type != "discussion_comment" ||
				event.RoomID == "" ||
				event.PinMessageID == "" ||
				event.Payload.ID == "" {
				continue
			}
			if event.Payload.CreatedAt.IsZero() {
				event.Payload.CreatedAt = time.Now().UTC()
			}
			select {
			case h.discussionInbox <- event:
			default:
			}
		}

		_ = pubsub.Close()
		time.Sleep(time.Second)
	}
}

func (h *Hub) SubscribeMessageMutations() {
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return
	}

	ctx := context.Background()
	for {
		pubsub := h.msgService.Redis.Client.Subscribe(ctx, chatMutationChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			log.Printf("redis mutation subscribe receive error: %v", err)
			_ = pubsub.Close()
			time.Sleep(time.Second)
			continue
		}

		channel := pubsub.Channel()
		for incoming := range channel {
			if incoming == nil || strings.TrimSpace(incoming.Payload) == "" {
				continue
			}
			var event MessageMutationEvent
			if err := json.Unmarshal([]byte(incoming.Payload), &event); err != nil {
				log.Printf("redis mutation unmarshal error: %v", err)
				continue
			}
			event.Type = strings.ToLower(strings.TrimSpace(event.Type))
			event.RoomID = normalizeRoomID(event.RoomID)
			event.MessageID = normalizeMessageID(event.MessageID)
			if event.Type == "" || event.RoomID == "" || event.MessageID == "" {
				continue
			}
			select {
			case h.mutationInbox <- event:
			default:
			}
		}

		_ = pubsub.Close()
		time.Sleep(time.Second)
	}
}

func (h *Hub) SubscribeRoomEvents() {
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return
	}

	ctx := context.Background()
	for {
		pubsub := h.msgService.Redis.Client.Subscribe(ctx, chatRoomEventChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			log.Printf("redis room-event subscribe receive error: %v", err)
			_ = pubsub.Close()
			time.Sleep(time.Second)
			continue
		}

		channel := pubsub.Channel()
		for incoming := range channel {
			if incoming == nil || strings.TrimSpace(incoming.Payload) == "" {
				continue
			}
			var event RoomEvent
			if err := json.Unmarshal([]byte(incoming.Payload), &event); err != nil {
				log.Printf("redis room-event unmarshal error: %v", err)
				continue
			}
			event.Type = strings.ToLower(strings.TrimSpace(event.Type))
			event.RoomID = normalizeRoomID(event.RoomID)
			event.OriginUserID = strings.TrimSpace(event.OriginUserID)
			if event.Type == "" || event.RoomID == "" {
				continue
			}
			if event.Payload == nil {
				event.Payload = map[string]interface{}{}
			}
			select {
			case h.roomEventInbox <- event:
			default:
			}
		}

		_ = pubsub.Close()
		time.Sleep(time.Second)
	}
}

func (h *Hub) persistenceWorker() {
	ctx := context.Background()

	for {
		if h.msgService == nil || !h.msgService.CanPersistToDisk() {
			time.Sleep(time.Second)
			continue
		}

		// Use a 2-second timeout
		result, err := h.msgService.Redis.Client.BLPop(ctx, 2*time.Second, messageQueueKey).Result()
		if err != nil {
			if err.Error() == "redis: nil" {
				// YIELD THE CPU: Give the main HTTP router 10 milliseconds
				// to grab a connection for user requests before looping again!
				time.Sleep(10 * time.Millisecond)
				continue
			}
			log.Printf("persistence worker pop error: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if len(result) < 2 {
			continue
		}

		var msg models.Message
		if err := json.Unmarshal([]byte(result[1]), &msg); err != nil {
			log.Printf("persistence worker unmarshal error: %v", err)
			continue
		}
		if !h.shouldPersistRoomMessagesToCloud(msg.RoomID) {
			continue
		}

		if err := h.msgService.SaveToScylla(msg); err != nil {
			log.Printf("persistence worker save error: %v", err)
			if requeueErr := h.msgService.EnqueueMessage(ctx, msg); requeueErr != nil {
				log.Printf("persistence worker requeue error: %v", requeueErr)
			}
			time.Sleep(time.Second)
			continue
		}
	}
}

func (h *Hub) Run() {
	roomExpiryTicker := time.NewTicker(15 * time.Second)
	defer roomExpiryTicker.Stop()

	for {
		select {
		case client := <-h.register:
			if client == nil {
				continue
			}
			if client.JoinedAt.IsZero() {
				client.JoinedAt = time.Now().UTC()
			}

		case subscription := <-h.subscribe:
			h.handleSubscription(subscription)

		case client := <-h.unregister:
			h.removeClientFromAllRooms(client, true)
			if client != nil {
				client.closeSendChannel()
			}

		case typingEvent := <-h.typing:
			h.handleClientTypingEvent(typingEvent)

		case boardEvent := <-h.boardEvent:
			h.handleClientBoardEvent(boardEvent)

		case taskEvent := <-h.taskEvent:
			h.handleClientTaskEvent(taskEvent)

		case discussionEvent := <-h.discussionComment:
			h.handleClientDiscussionCommentEvent(discussionEvent)

		case discussionPinEvent := <-h.discussionCommentPin:
			h.handleClientDiscussionCommentPinEvent(discussionPinEvent)

		case editEvent := <-h.messageEdit:
			h.handleClientMessageEditEvent(editEvent)

		case deleteEvent := <-h.messageDelete:
			h.handleClientMessageDeleteEvent(deleteEvent)

		case reactionEvent := <-h.messageReaction:
			h.handleClientMessageReactionEvent(reactionEvent)

		case roomEvent := <-h.roomEvent:
			h.publishRoomEvent(roomEvent)

		case msg := <-h.broadcast:
			if msg.CreatedAt.IsZero() {
				msg.CreatedAt = time.Now().UTC()
			}
			shouldPersistToCloud := h.shouldPersistRoomMessagesToCloud(msg.RoomID)

			if h.msgService != nil {
				if shouldPersistToCloud {
					go func(m models.Message) {
						if err := h.msgService.EnqueueMessage(context.Background(), m); err != nil {
							log.Printf("enqueue message error: %v", err)
						}
					}(msg)
				}

				go func(m models.Message) {
					if err := h.msgService.CacheRecentMessage(context.Background(), m); err != nil {
						log.Printf("cache message error: %v", err)
					}
				}(msg)
			}

			if h.msgService != nil && h.msgService.Redis != nil && h.msgService.Redis.Client != nil {
				payload, err := json.Marshal(msg)
				if err != nil {
					log.Printf("redis publish marshal error: %v", err)
					h.broadcastToLocal(msg)
					continue
				}
				if err := h.msgService.Redis.Client.Publish(context.Background(), chatBroadcastChannel, payload).Err(); err != nil {
					log.Printf("redis publish error: %v", err)
					h.broadcastToLocal(msg)
				}
			} else {
				h.broadcastToLocal(msg)
			}

		case msg := <-h.redisInbox:
			if msg.CreatedAt.IsZero() {
				msg.CreatedAt = time.Now().UTC()
			}
			h.broadcastToLocal(msg)

		case typingEvent := <-h.typingInbox:
			h.broadcastTypingToLocal(typingEvent)

		case discussionEvent := <-h.discussionInbox:
			h.broadcastDiscussionCommentToLocal(discussionEvent)

		case mutationEvent := <-h.mutationInbox:
			h.broadcastMutationToLocal(mutationEvent)

		case roomEvent := <-h.roomEventInbox:
			h.broadcastRoomEventToLocal(roomEvent)

		case <-roomExpiryTicker.C:
			h.broadcastExpiredRooms()
		}
	}
}

func (h *Hub) broadcastExpiredRooms() {
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return
	}

	ctx := context.Background()
	for roomID, clients := range h.rooms {
		normalizedRoomID := normalizeRoomID(roomID)
		if normalizedRoomID == "" {
			continue
		}

		exists, err := h.msgService.Redis.Client.Exists(ctx, roomKeyPrefix+normalizedRoomID).Result()
		if err != nil {
			log.Printf("[ws] room expiry check failed room=%s err=%v", normalizedRoomID, err)
			continue
		}
		if exists > 0 {
			continue
		}

		expiredPayload := map[string]interface{}{
			"type":   "room_expired",
			"roomId": normalizedRoomID,
			"payload": map[string]interface{}{
				"roomId":    normalizedRoomID,
				"expiredAt": time.Now().UTC().UnixMilli(),
			},
		}

		for client := range clients {
			client.unsubscribeFromRoom(normalizedRoomID)
			select {
			case client.Send <- expiredPayload:
			default:
				h.removeClientFromAllRooms(client, true)
				client.closeSendChannel()
			}
		}

		h.removeRoom(normalizedRoomID)
	}
}

func (h *Hub) broadcastToLocal(msg models.Message) {
	clients, ok := h.rooms[msg.RoomID]
	if !ok {
		return
	}

	for client := range clients {
		if !client.canWriteToRoom(msg.RoomID) && h.isRoomPasswordProtected(msg.RoomID) {
			delete(clients, client)
			client.unsubscribeFromRoom(msg.RoomID)
			continue
		}
		if client.canWriteToRoom(msg.RoomID) && !h.isClientRoomMember(client.UserID, msg.RoomID) {
			client.subscribeToRoom(msg.RoomID, false)
			continue
		}
		select {
		case client.Send <- msg:
		default:
			h.removeClientFromAllRooms(client, true)
			client.closeSendChannel()
		}
	}
}

func (h *Hub) handleSubscription(subscription *ClientSubscription) {
	if subscription == nil || subscription.Client == nil {
		return
	}

	client := subscription.Client
	roomConnectionLimit := config.LoadAppLimits().WS.MaxConnectionsPerRoom
	if client.JoinedAt.IsZero() {
		client.JoinedAt = time.Now().UTC()
	}

	seen := make(map[string]struct{})
	for _, rawRoomID := range subscription.RoomIDs {
		roomID := normalizeRoomID(rawRoomID)
		if roomID == "" {
			continue
		}
		if _, exists := seen[roomID]; exists {
			continue
		}
		seen[roomID] = struct{}{}

		canWrite := h.isClientRoomMember(client.UserID, roomID)
		if h.isRoomPasswordProtected(roomID) && !canWrite {
			select {
			case client.Send <- map[string]interface{}{
				"type":   "room_access_required",
				"roomId": roomID,
				"payload": map[string]interface{}{
					"requiresPassword": true,
				},
			}:
			default:
				h.removeClientFromAllRooms(client, true)
				client.closeSendChannel()
				return
			}
			continue
		}

		roomClients, roomExists := h.rooms[roomID]
		if !roomExists {
			roomClients = make(map[*Client]bool)
			h.rooms[roomID] = roomClients
			monitor.ActiveRooms.Inc()
		}

		alreadySubscribed := roomClients[client]
		if !alreadySubscribed && len(roomClients) >= roomConnectionLimit {
			select {
			case client.Send <- map[string]interface{}{
				"type":   "room_limit_exceeded",
				"roomId": roomID,
				"payload": map[string]interface{}{
					"code":    "room_full",
					"limit":   roomConnectionLimit,
					"message": "Room is full. Maximum " + strconv.Itoa(roomConnectionLimit) + " users are allowed in this room.",
				},
			}:
			default:
			}
			// Skip only the saturated room so one crowded room does not terminate
			// the entire websocket session and other room subscriptions.
			continue
		}

		alreadyWritable := client.canWriteToRoom(roomID)
		if alreadySubscribed && alreadyWritable == canWrite {
			continue
		}

		roomClients[client] = true
		client.subscribeToRoom(roomID, canWrite)

		if canWrite {
			onlineMembers := h.collectWritableOnlineMembers(roomID)
			select {
			case client.Send <- map[string]interface{}{
				"type":    "online_list",
				"roomId":  roomID,
				"payload": onlineMembers,
			}:
			default:
				h.removeClientFromAllRooms(client, true)
				client.closeSendChannel()
				return
			}
		}

		if canWrite && (!alreadySubscribed || !alreadyWritable) {
			joinedIsAdmin, _ := h.isClientRoomAdmin(client.UserID, roomID)
			joinedPayload := map[string]interface{}{
				"type":   "user_joined",
				"roomId": roomID,
				"payload": map[string]interface{}{
					"id":       client.UserID,
					"name":     client.Username,
					"joinedAt": client.JoinedAt.UnixMilli(),
					"isAdmin":  joinedIsAdmin,
				},
			}
			for roomClient := range roomClients {
				if roomClient == client || !roomClient.canWriteToRoom(roomID) {
					continue
				}
				select {
				case roomClient.Send <- joinedPayload:
				default:
					h.removeClientFromAllRooms(roomClient, true)
					roomClient.closeSendChannel()
				}
			}
		}

		if h.msgService != nil && !alreadySubscribed {
			go client.LoadHistory(context.Background(), h.msgService, roomID)
		}
	}
}

func (h *Hub) handleClientTypingEvent(event *ClientTypingEvent) {
	if event == nil || event.Client == nil {
		return
	}
	roomID := normalizeRoomID(event.RoomID)
	client := event.Client
	isSubscribed := client.isSubscribedToRoom(roomID)
	canWrite := client.canWriteToRoom(roomID)
	if roomID == "" || !isSubscribed || !canWrite {
		return
	}
	if !h.isClientRoomMember(client.UserID, roomID) {
		client.subscribeToRoom(roomID, false)
		return
	}

	typingEvent := TypingRedisEvent{
		RoomID:    roomID,
		UserID:    client.UserID,
		UserName:  client.Username,
		IsTyping:  event.IsTyping,
		UpdatedAt: time.Now().UTC().UnixMilli(),
	}

	// Broadcast immediately to this process so typing indicators stay responsive
	// even if Redis pub/sub delivery is delayed.
	h.broadcastTypingToLocal(typingEvent)

	if h.msgService != nil && h.msgService.Redis != nil && h.msgService.Redis.Client != nil {
		payload, err := json.Marshal(typingEvent)
		if err != nil {
			log.Printf("redis typing marshal error: %v", err)
			return
		}
		if err := h.msgService.Redis.Client.Publish(context.Background(), chatTypingChannel, payload).Err(); err != nil {
			log.Printf("redis typing publish error: %v", err)
		}
		return
	}
}

func (h *Hub) handleClientBoardEvent(event *ClientBoardEvent) {
	if event == nil || event.Client == nil {
		return
	}
	client := event.Client
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	roomID := normalizeRoomID(event.RoomID)
	if roomID == "" || !isBoardEventType(eventType) {
		return
	}
	if !client.isSubscribedToRoom(roomID) || !client.canWriteToRoom(roomID) {
		return
	}
	if !h.isClientRoomMember(client.UserID, roomID) {
		client.subscribeToRoom(roomID, false)
		return
	}

	payload := map[string]interface{}{}
	for key, value := range event.Payload {
		payload[key] = value
	}
	payload["type"] = eventType
	payload["roomId"] = roomID
	payloadBody := payload
	if nestedPayload, ok := payload["payload"].(map[string]interface{}); ok && nestedPayload != nil {
		payloadBody = nestedPayload
	}

	if eventType == boardActivityType {
		h.publishRoomEvent(RoomEvent{
			Type:         eventType,
			RoomID:       roomID,
			Payload:      payload,
			OriginUserID: client.UserID,
		})
		return
	}

	normalizedActorUserID := normalizeUsername(client.UserID)
	isRoomAdmin, adminErr := h.isClientRoomAdmin(normalizedActorUserID, roomID)
	if adminErr != nil {
		log.Printf("[ws] board admin lookup failed room=%s user=%s err=%v", roomID, normalizedActorUserID, adminErr)
		h.sendBoardError(client, roomID, "board_permission_check_failed", "Unable to verify board permissions. Please retry.", "")
		return
	}

	switch eventType {
	case boardElementAddType:
		if event.Element == nil {
			h.sendBoardError(client, roomID, "board_payload_invalid", "Invalid board element payload.", "")
			return
		}
		element := *event.Element
		element.RoomID = roomID
		element.CreatedByUserID = normalizedActorUserID
		element.CreatedByName = strings.TrimSpace(client.Username)
		payloadBody["elementId"] = element.ElementID
		payloadBody["elementType"] = element.Type
		payloadBody["x"] = element.X
		payloadBody["y"] = element.Y
		payloadBody["width"] = element.Width
		payloadBody["height"] = element.Height
		payloadBody["content"] = element.Content
		payloadBody["zIndex"] = element.ZIndex
		payloadBody["createdByUserId"] = element.CreatedByUserID
		payloadBody["createdByName"] = element.CreatedByName
		payloadBody["createdAt"] = element.CreatedAt.UnixMilli()
		if h.msgService == nil {
			break
		}
		if err := h.msgService.UpsertBoardElement(context.Background(), element); err != nil {
			log.Printf(
				"[ws] board element add persist failed room=%s element=%s user=%s err=%v",
				roomID,
				element.ElementID,
				client.UserID,
				err,
			)
			if errors.Is(err, ErrBoardSizeLimitExceeded) {
				h.sendBoardError(
					client,
					roomID,
					"board_size_limit",
					"Board is full ("+boardStorageLimitLabel()+" max). Remove some elements and try again.",
					element.ElementID,
				)
			} else {
				h.sendBoardError(client, roomID, "board_add_failed", "Unable to save board element. Please retry.", element.ElementID)
			}
			return
		}
	case boardElementMoveType:
		elementID := normalizeMessageID(event.ElementID)
		if elementID == "" && event.Element != nil {
			elementID = normalizeMessageID(event.Element.ElementID)
		}
		if elementID == "" {
			return
		}
		if !isRoomAdmin {
			if h.msgService == nil {
				h.sendBoardError(client, roomID, "board_permission_denied", "Only element owner can move this item.", elementID)
				return
			}
			creatorUserID, _, err := h.msgService.LookupBoardElementCreator(context.Background(), roomID, elementID)
			if err != nil {
				log.Printf(
					"[ws] board move creator lookup failed room=%s element=%s user=%s err=%v",
					roomID,
					elementID,
					normalizedActorUserID,
					err,
				)
				h.sendBoardError(client, roomID, "board_permission_check_failed", "Unable to verify board permissions. Please retry.", elementID)
				return
			}
			if creatorUserID == "" || creatorUserID != normalizedActorUserID {
				h.sendBoardError(client, roomID, "board_permission_denied", "Only element owner can move this item.", elementID)
				return
			}
		}
		payloadBody["elementId"] = elementID
	case boardElementDeleteType:
		elementID := normalizeMessageID(event.ElementID)
		if elementID == "" && event.Element != nil {
			elementID = normalizeMessageID(event.Element.ElementID)
		}
		if elementID == "" {
			return
		}
		if !isRoomAdmin {
			if h.msgService == nil {
				h.sendBoardError(client, roomID, "board_permission_denied", "Only element owner can delete this item.", elementID)
				return
			}
			creatorUserID, _, err := h.msgService.LookupBoardElementCreator(context.Background(), roomID, elementID)
			if err != nil {
				log.Printf(
					"[ws] board delete creator lookup failed room=%s element=%s user=%s err=%v",
					roomID,
					elementID,
					normalizedActorUserID,
					err,
				)
				h.sendBoardError(client, roomID, "board_permission_check_failed", "Unable to verify board permissions. Please retry.", elementID)
				return
			}
			if creatorUserID == "" || creatorUserID != normalizedActorUserID {
				h.sendBoardError(client, roomID, "board_permission_denied", "Only element owner can delete this item.", elementID)
				return
			}
		}
		if h.msgService == nil {
			payloadBody["elementId"] = elementID
			break
		}
		if err := h.msgService.DeleteBoardElement(context.Background(), roomID, elementID); err != nil {
			log.Printf(
				"[ws] board element delete persist failed room=%s element=%s user=%s err=%v",
				roomID,
				elementID,
				client.UserID,
				err,
			)
			h.sendBoardError(client, roomID, "board_delete_failed", "Unable to delete board element. Please retry.", elementID)
			return
		}
		payloadBody["elementId"] = elementID
	}

	h.publishRoomEvent(RoomEvent{
		Type:         eventType,
		RoomID:       roomID,
		Payload:      payload,
		OriginUserID: client.UserID,
	})
}

func (h *Hub) isClientRoomAdmin(userID, roomID string) (bool, error) {
	normalizedUserID := normalizeUsername(userID)
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedUserID == "" || normalizedRoomID == "" {
		return false, nil
	}
	if h == nil || h.msgService == nil {
		return false, nil
	}
	return h.msgService.IsRoomAdmin(context.Background(), normalizedRoomID, normalizedUserID)
}

func (h *Hub) sendBoardError(client *Client, roomID, code, message, elementID string) {
	if client == nil {
		return
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return
	}
	normalizedElementID := normalizeMessageID(elementID)
	payload := map[string]interface{}{
		"type":   "board_error",
		"roomId": normalizedRoomID,
		"payload": map[string]interface{}{
			"code":      strings.TrimSpace(code),
			"message":   strings.TrimSpace(message),
			"elementId": normalizedElementID,
			"maxBytes":  boardMaxStorageBytes(),
		},
	}
	select {
	case client.Send <- payload:
	default:
		h.removeClientFromAllRooms(client, true)
		client.closeSendChannel()
	}
}

func (h *Hub) handleClientDiscussionCommentEvent(event *ClientDiscussionCommentEvent) {
	if event == nil || event.Client == nil || h.msgService == nil {
		return
	}

	client := event.Client
	roomID := normalizeRoomID(event.RoomID)
	pinMessageID := normalizeMessageID(event.PinMessageID)
	parentCommentID := normalizeMessageID(event.ParentCommentID)
	content := strings.TrimSpace(event.Content)
	if roomID == "" || pinMessageID == "" || content == "" {
		return
	}
	if !client.isSubscribedToRoom(roomID) || !client.canWriteToRoom(roomID) {
		return
	}
	if !h.isClientRoomMember(client.UserID, roomID) {
		client.subscribeToRoom(roomID, false)
		return
	}

	comment, err := h.msgService.CreatePinnedDiscussionComment(
		context.Background(),
		roomID,
		pinMessageID,
		parentCommentID,
		client.UserID,
		client.Username,
		content,
		time.Now().UTC(),
	)
	if err != nil {
		log.Printf(
			"[ws] discussion comment create failed room=%s pin=%s user=%s err=%v",
			roomID,
			pinMessageID,
			client.UserID,
			err,
		)
		return
	}

	discussionEvent := DiscussionCommentEvent{
		Type:         "discussion_comment",
		RoomID:       roomID,
		PinMessageID: pinMessageID,
		Payload:      comment,
	}
	h.publishDiscussionCommentEvent(discussionEvent)
}

func (h *Hub) handleClientTaskEvent(event *ClientTaskEvent) {
	if event == nil || event.Client == nil || h.msgService == nil {
		return
	}

	client := event.Client
	taskPayload := event.Payload
	taskPayload.Type = strings.ToLower(strings.TrimSpace(taskPayload.Type))
	taskPayload.RoomID = normalizeRoomID(taskPayload.RoomID)
	taskPayload.Task.ID = normalizeTaskIdentifier(taskPayload.Task.ID)

	if !isTaskEventType(taskPayload.Type) || taskPayload.RoomID == "" || taskPayload.Task.ID == "" {
		return
	}
	if !client.isSubscribedToRoom(taskPayload.RoomID) || !client.canWriteToRoom(taskPayload.RoomID) {
		return
	}
	if !h.isClientRoomMember(client.UserID, taskPayload.RoomID) {
		client.subscribeToRoom(taskPayload.RoomID, false)
		return
	}

	if taskPayload.Raw == nil {
		taskPayload.Raw = map[string]interface{}{}
	}
	taskPayload.Raw["type"] = taskPayload.Type
	if _, hasRoomID := taskPayload.Raw["roomId"]; !hasRoomID {
		taskPayload.Raw["roomId"] = taskPayload.RoomID
	}
	if _, hasRoomID2 := taskPayload.Raw["room_id"]; !hasRoomID2 {
		taskPayload.Raw["room_id"] = taskPayload.RoomID
	}

	if taskPayload.Payload == nil {
		taskPayload.Payload = map[string]interface{}{}
	}
	taskPayload.Payload["id"] = taskPayload.Task.ID
	taskPayload.Payload["taskId"] = taskPayload.Task.ID
	taskPayload.Payload["task_id"] = taskPayload.Task.ID
	if _, hasTitle := taskPayload.Payload["title"]; !hasTitle && strings.TrimSpace(taskPayload.Task.Title) != "" {
		taskPayload.Payload["title"] = taskPayload.Task.Title
	}
	if _, hasDescription := taskPayload.Payload["description"]; !hasDescription && strings.TrimSpace(taskPayload.Task.Description) != "" {
		taskPayload.Payload["description"] = taskPayload.Task.Description
	}
	if _, hasStatus := taskPayload.Payload["status"]; !hasStatus && strings.TrimSpace(taskPayload.Task.Status) != "" {
		taskPayload.Payload["status"] = normalizeTaskStatus(taskPayload.Task.Status)
	}
	if _, hasAssignee := taskPayload.Payload["assignee_id"]; !hasAssignee && strings.TrimSpace(taskPayload.Task.AssigneeID) != "" {
		taskPayload.Payload["assignee_id"] = taskPayload.Task.AssigneeID
		taskPayload.Payload["assigneeId"] = taskPayload.Task.AssigneeID
	}
	if _, hasPayload := taskPayload.Raw["payload"]; !hasPayload {
		taskPayload.Raw["payload"] = taskPayload.Payload
	}

	go func(payload TaskPayload, originUserID string) {
		ctx := context.Background()
		var err error
		switch payload.Type {
		case "task_create", "task_update", "task_move":
			err = h.msgService.UpsertTaskPayload(ctx, payload)
		case "task_delete":
			err = h.msgService.DeleteTaskPayload(ctx, payload)
		}
		if err != nil {
			log.Printf(
				"[ws] task persistence failed type=%s room=%s task=%s user=%s err=%v",
				payload.Type,
				payload.RoomID,
				payload.Task.ID,
				originUserID,
				err,
			)
			return
		}

		h.publishRoomEvent(RoomEvent{
			Type:         payload.Type,
			RoomID:       payload.RoomID,
			Payload:      payload.Raw,
			OriginUserID: strings.TrimSpace(originUserID),
		})
	}(taskPayload, client.UserID)
}

func (h *Hub) handleClientDiscussionCommentPinEvent(event *ClientDiscussionCommentPinEvent) {
	if event == nil || event.Client == nil || h.msgService == nil {
		return
	}

	client := event.Client
	roomID := normalizeRoomID(event.RoomID)
	pinMessageID := normalizeMessageID(event.PinMessageID)
	commentID := normalizeMessageID(event.CommentID)
	if roomID == "" || pinMessageID == "" || commentID == "" {
		return
	}
	if !client.isSubscribedToRoom(roomID) || !client.canWriteToRoom(roomID) {
		return
	}
	if !h.isClientRoomMember(client.UserID, roomID) {
		client.subscribeToRoom(roomID, false)
		return
	}

	updatedComment, err := h.msgService.SetPinnedDiscussionComment(
		context.Background(),
		roomID,
		pinMessageID,
		commentID,
		client.UserID,
		client.Username,
		event.IsPinned,
	)
	if err != nil {
		log.Printf(
			"[ws] discussion comment pin failed room=%s pin=%s comment=%s user=%s err=%v",
			roomID,
			pinMessageID,
			commentID,
			client.UserID,
			err,
		)
		return
	}

	discussionEvent := DiscussionCommentEvent{
		Type:         "discussion_comment",
		RoomID:       roomID,
		PinMessageID: pinMessageID,
		Payload:      updatedComment,
	}
	h.publishDiscussionCommentEvent(discussionEvent)
}

func (h *Hub) handleClientMessageEditEvent(event *ClientMessageEditEvent) {
	if event == nil || event.Client == nil || h.msgService == nil {
		return
	}
	roomID := normalizeRoomID(event.RoomID)
	messageID := normalizeMessageID(event.MessageID)
	content := strings.TrimSpace(event.Content)
	client := event.Client
	if roomID == "" || messageID == "" || content == "" {
		return
	}
	if !client.isSubscribedToRoom(roomID) || !client.canWriteToRoom(roomID) {
		return
	}
	if !h.isClientRoomMember(client.UserID, roomID) {
		client.subscribeToRoom(roomID, false)
		return
	}
	messageType, typeErr := h.msgService.GetMessageType(context.Background(), roomID, messageID)
	if typeErr != nil {
		log.Printf("[ws] message edit type lookup failed room=%s message=%s user=%s err=%v", roomID, messageID, client.UserID, typeErr)
		return
	}
	if messageType != "task" {
		ownsMessage, err := h.msgService.IsMessageOwnedBy(context.Background(), roomID, messageID, client.UserID)
		if err != nil {
			log.Printf("[ws] message edit ownership check failed room=%s message=%s user=%s err=%v", roomID, messageID, client.UserID, err)
			return
		}
		if !ownsMessage {
			return
		}
	}
	updatedMessageType, err := h.msgService.UpdateMessageContent(
		context.Background(),
		roomID,
		messageID,
		content,
		time.Now().UTC(),
	)
	if err != nil {
		log.Printf("[ws] message edit failed room=%s message=%s user=%s err=%v", roomID, messageID, client.UserID, err)
		return
	}

	mutation := MessageMutationEvent{
		Type:        "message_edit",
		RoomID:      roomID,
		MessageID:   messageID,
		Content:     content,
		MessageType: updatedMessageType,
		IsEdited:    true,
		EditedAt:    time.Now().UTC().UnixMilli(),
	}
	h.publishMutationEvent(mutation)
}

func (h *Hub) handleClientMessageDeleteEvent(event *ClientMessageDeleteEvent) {
	if event == nil || event.Client == nil || h.msgService == nil {
		return
	}
	roomID := normalizeRoomID(event.RoomID)
	messageID := normalizeMessageID(event.MessageID)
	client := event.Client
	if roomID == "" || messageID == "" {
		return
	}
	if !client.isSubscribedToRoom(roomID) || !client.canWriteToRoom(roomID) {
		return
	}
	if !h.isClientRoomMember(client.UserID, roomID) {
		client.subscribeToRoom(roomID, false)
		return
	}
	ownsMessage, err := h.msgService.IsMessageOwnedBy(context.Background(), roomID, messageID, client.UserID)
	if err != nil {
		log.Printf("[ws] message delete ownership check failed room=%s message=%s user=%s err=%v", roomID, messageID, client.UserID, err)
		return
	}
	if !ownsMessage {
		return
	}
	if err := h.msgService.MarkMessageDeleted(context.Background(), roomID, messageID, time.Now().UTC()); err != nil {
		log.Printf("[ws] message delete failed room=%s message=%s user=%s err=%v", roomID, messageID, client.UserID, err)
		return
	}

	mutation := MessageMutationEvent{
		Type:      "message_delete",
		RoomID:    roomID,
		MessageID: messageID,
		Content:   DeletedMessagePlaceholder,
		IsEdited:  false,
		EditedAt:  time.Now().UTC().UnixMilli(),
	}
	h.publishMutationEvent(mutation)
}

func (h *Hub) handleClientMessageReactionEvent(event *ClientMessageReactionEvent) {
	if event == nil || event.Client == nil || h.msgService == nil {
		return
	}
	roomID := normalizeRoomID(event.RoomID)
	messageID := normalizeMessageID(event.MessageID)
	emoji := normalizeReactionEmoji(event.Emoji)
	client := event.Client
	if roomID == "" || messageID == "" || emoji == "" {
		return
	}
	if !client.isSubscribedToRoom(roomID) || !client.canWriteToRoom(roomID) {
		return
	}
	if !h.isClientRoomMember(client.UserID, roomID) {
		client.subscribeToRoom(roomID, false)
		return
	}

	reactions, _, err := h.msgService.ToggleMessageReaction(
		context.Background(),
		roomID,
		messageID,
		client.UserID,
		emoji,
	)
	if err != nil {
		log.Printf(
			"[ws] message reaction toggle failed room=%s message=%s user=%s err=%v",
			roomID,
			messageID,
			client.UserID,
			err,
		)
		return
	}

	h.publishRoomEvent(RoomEvent{
		Type:   "message_reaction",
		RoomID: roomID,
		Payload: map[string]interface{}{
			"messageId":  messageID,
			"message_id": messageID,
			"emoji":      emoji,
			"userId":     strings.TrimSpace(client.UserID),
			"user_id":    strings.TrimSpace(client.UserID),
			"reactions":  reactions,
		},
	})
}

func (h *Hub) publishMutationEvent(event MessageMutationEvent) {
	if h.msgService != nil && h.msgService.Redis != nil && h.msgService.Redis.Client != nil {
		payload, err := json.Marshal(event)
		if err != nil {
			log.Printf("redis mutation marshal error: %v", err)
			h.broadcastMutationToLocal(event)
			return
		}
		if err := h.msgService.Redis.Client.Publish(context.Background(), chatMutationChannel, payload).Err(); err != nil {
			log.Printf("redis mutation publish error: %v", err)
			h.broadcastMutationToLocal(event)
		}
		return
	}
	h.broadcastMutationToLocal(event)
}

func (h *Hub) publishDiscussionCommentEvent(event DiscussionCommentEvent) {
	if h.msgService != nil && h.msgService.Redis != nil && h.msgService.Redis.Client != nil {
		payload, err := json.Marshal(event)
		if err != nil {
			log.Printf("redis discussion marshal error: %v", err)
			h.broadcastDiscussionCommentToLocal(event)
			return
		}
		if err := h.msgService.Redis.Client.Publish(context.Background(), chatDiscussionChannel, payload).Err(); err != nil {
			log.Printf("redis discussion publish error: %v", err)
			h.broadcastDiscussionCommentToLocal(event)
		}
		return
	}
	h.broadcastDiscussionCommentToLocal(event)
}

func (h *Hub) BroadcastToRoom(roomID string, payload map[string]interface{}) {
	if h == nil {
		return
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return
	}
	rawType, _ := payload["type"].(string)
	eventType := strings.ToLower(strings.TrimSpace(rawType))
	if eventType == "" {
		return
	}
	body := map[string]interface{}{}
	for key, value := range payload {
		lowered := strings.ToLower(strings.TrimSpace(key))
		if lowered == "type" || lowered == "roomid" || lowered == "room_id" {
			continue
		}
		body[key] = value
	}
	event := RoomEvent{
		Type:    eventType,
		RoomID:  normalizedRoomID,
		Payload: body,
	}
	// Route through the shared room-event publisher so every hub instance
	// (not just this process) receives and broadcasts the update.
	h.publishRoomEvent(event)
}

func (h *Hub) publishRoomEvent(event RoomEvent) {
	event.Type = strings.ToLower(strings.TrimSpace(event.Type))
	event.RoomID = normalizeRoomID(event.RoomID)
	event.OriginUserID = strings.TrimSpace(event.OriginUserID)
	if event.Type == "" || event.RoomID == "" {
		return
	}
	if event.Payload == nil {
		event.Payload = map[string]interface{}{}
	}

	if h.msgService != nil && h.msgService.Redis != nil && h.msgService.Redis.Client != nil {
		payload, err := json.Marshal(event)
		if err != nil {
			log.Printf("redis room-event marshal error: %v", err)
			h.broadcastRoomEventToLocal(event)
			return
		}
		if err := h.msgService.Redis.Client.Publish(context.Background(), chatRoomEventChannel, payload).Err(); err != nil {
			log.Printf("redis room-event publish error: %v", err)
			h.broadcastRoomEventToLocal(event)
		}
		return
	}
	h.broadcastRoomEventToLocal(event)
}

func (h *Hub) broadcastTypingToLocal(event TypingRedisEvent) {
	roomID := normalizeRoomID(event.RoomID)
	userID := strings.TrimSpace(event.UserID)
	if roomID == "" || userID == "" {
		return
	}
	clients, ok := h.rooms[roomID]
	if !ok {
		return
	}
	envelopeType := "typing_stop"
	if event.IsTyping {
		envelopeType = "typing_start"
	}
	payload := map[string]interface{}{
		"type":    envelopeType,
		"roomId":  roomID,
		"room_id": roomID,
		"payload": map[string]interface{}{
			"id":        userID,
			"userId":    userID,
			"user_id":   userID,
			"name":      event.UserName,
			"username":  event.UserName,
			"userName":  event.UserName,
			"user_name": event.UserName,
			"roomId":    roomID,
			"room_id":   roomID,
			"isTyping":  event.IsTyping,
			"is_typing": event.IsTyping,
		},
	}
	if event.ExpiresAt > 0 {
		if nestedPayload, ok := payload["payload"].(map[string]interface{}); ok {
			nestedPayload["expiresAt"] = event.ExpiresAt
			nestedPayload["expires_at"] = event.ExpiresAt
		}
	}
	for roomClient := range clients {
		if roomClient.UserID == userID {
			continue
		}
		select {
		case roomClient.Send <- payload:
		default:
			h.removeClientFromAllRooms(roomClient, true)
			roomClient.closeSendChannel()
		}
	}
}

func (h *Hub) broadcastDiscussionCommentToLocal(event DiscussionCommentEvent) {
	roomID := normalizeRoomID(event.RoomID)
	pinMessageID := normalizeMessageID(event.PinMessageID)
	commentID := normalizeMessageID(event.Payload.ID)
	if roomID == "" || pinMessageID == "" || commentID == "" {
		return
	}
	clients, ok := h.rooms[roomID]
	if !ok {
		return
	}
	if event.Payload.CreatedAt.IsZero() {
		event.Payload.CreatedAt = time.Now().UTC()
	}
	event.Payload.RoomID = roomID
	event.Payload.ID = commentID
	event.Payload.ReplyToMessageID = normalizeMessageID(event.Payload.ReplyToMessageID)

	payload := map[string]interface{}{
		"type":         "discussion_comment",
		"roomId":       roomID,
		"pinMessageId": pinMessageID,
		"payload":      event.Payload,
	}

	for roomClient := range clients {
		if roomClient.canWriteToRoom(roomID) && !h.isClientRoomMember(roomClient.UserID, roomID) {
			roomClient.subscribeToRoom(roomID, false)
			continue
		}
		if !roomClient.canWriteToRoom(roomID) {
			continue
		}
		select {
		case roomClient.Send <- payload:
		default:
			h.removeClientFromAllRooms(roomClient, true)
			roomClient.closeSendChannel()
		}
	}
}

func (h *Hub) broadcastMutationToLocal(event MessageMutationEvent) {
	roomID := normalizeRoomID(event.RoomID)
	messageID := normalizeMessageID(event.MessageID)
	if roomID == "" || messageID == "" {
		return
	}
	clients, ok := h.rooms[roomID]
	if !ok {
		return
	}
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	if eventType != "message_edit" && eventType != "message_delete" {
		return
	}

	payload := map[string]interface{}{
		"type":   eventType,
		"roomId": roomID,
		"payload": map[string]interface{}{
			"id":        messageID,
			"messageId": messageID,
			"content":   event.Content,
			"isEdited":  event.IsEdited,
			"editedAt":  event.EditedAt,
		},
	}
	messageType := "deleted"
	if eventType == "message_edit" {
		messageType = strings.ToLower(strings.TrimSpace(event.MessageType))
		if messageType == "" {
			messageType = "text"
		}
	}
	payloadBody := payload["payload"].(map[string]interface{})
	payloadBody["type"] = messageType
	payloadBody["messageType"] = messageType

	for roomClient := range clients {
		if roomClient.canWriteToRoom(roomID) && !h.isClientRoomMember(roomClient.UserID, roomID) {
			roomClient.subscribeToRoom(roomID, false)
			continue
		}
		select {
		case roomClient.Send <- payload:
		default:
			h.removeClientFromAllRooms(roomClient, true)
			roomClient.closeSendChannel()
		}
	}
}

func (h *Hub) broadcastRoomEventToLocal(event RoomEvent) {
	roomID := normalizeRoomID(event.RoomID)
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	if roomID == "" || eventType == "" {
		return
	}
	if isTransientSignalingType(eventType) {
		h.broadcastSignalingEventToLocal(eventType, roomID, event.Payload)
		return
	}
	if isTaskEventType(eventType) {
		h.broadcastTaskEventToLocal(RoomEvent{
			Type:         eventType,
			RoomID:       roomID,
			Payload:      event.Payload,
			OriginUserID: strings.TrimSpace(event.OriginUserID),
		})
		return
	}
	if isBoardEventType(eventType) {
		h.broadcastBoardEventToLocal(RoomEvent{
			Type:         eventType,
			RoomID:       roomID,
			Payload:      event.Payload,
			OriginUserID: strings.TrimSpace(event.OriginUserID),
		})
		return
	}
	clients, ok := h.rooms[roomID]
	if !ok {
		return
	}

	payload := map[string]interface{}{
		"type":   eventType,
		"roomId": roomID,
		"payload": func() map[string]interface{} {
			if event.Payload == nil {
				return map[string]interface{}{}
			}
			return event.Payload
		}(),
	}

	for roomClient := range clients {
		select {
		case roomClient.Send <- payload:
		default:
			h.removeClientFromAllRooms(roomClient, true)
			roomClient.closeSendChannel()
		}
	}
}

func (h *Hub) broadcastTaskEventToLocal(event RoomEvent) {
	roomID := normalizeRoomID(event.RoomID)
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	if roomID == "" || !isTaskEventType(eventType) {
		return
	}
	clients, ok := h.rooms[roomID]
	if !ok {
		return
	}

	payload := map[string]interface{}{}
	for key, value := range event.Payload {
		payload[key] = value
	}
	payload["type"] = eventType
	payload["roomId"] = roomID
	payload["room_id"] = roomID

	originUserID := strings.TrimSpace(event.OriginUserID)
	for roomClient := range clients {
		if roomClient.canWriteToRoom(roomID) && !h.isClientRoomMember(roomClient.UserID, roomID) {
			roomClient.subscribeToRoom(roomID, false)
			continue
		}
		if !roomClient.canWriteToRoom(roomID) {
			continue
		}
		if originUserID != "" && roomClient.UserID == originUserID {
			continue
		}
		select {
		case roomClient.Send <- payload:
		default:
			h.removeClientFromAllRooms(roomClient, true)
			roomClient.closeSendChannel()
		}
	}
}

func resolveSignalingTargetUserID(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}
	targetUserID := normalizeUsername(
		readStringFromMap(
			payload,
			"targetUserId",
			"target_user_id",
			"targetUser",
			"target_user",
		),
	)
	if targetUserID != "" {
		return targetUserID
	}
	if nestedPayload, ok := payload["payload"].(map[string]interface{}); ok && nestedPayload != nil {
		return normalizeUsername(
			readStringFromMap(
				nestedPayload,
				"targetUserId",
				"target_user_id",
				"targetUser",
				"target_user",
			),
		)
	}
	return ""
}

func resolveSignalingOriginUserID(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}
	originUserID := normalizeUsername(
		readStringFromMap(
			payload,
			"fromUserId",
			"from_user_id",
			"userId",
			"user_id",
			"senderId",
			"sender_id",
		),
	)
	if originUserID != "" {
		return originUserID
	}
	if nestedPayload, ok := payload["payload"].(map[string]interface{}); ok && nestedPayload != nil {
		return normalizeUsername(
			readStringFromMap(
				nestedPayload,
				"fromUserId",
				"from_user_id",
				"userId",
				"user_id",
				"senderId",
				"sender_id",
			),
		)
	}
	return ""
}

func (h *Hub) broadcastSignalingEventToLocal(eventType, roomID string, body map[string]interface{}) {
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedEventType := strings.ToLower(strings.TrimSpace(eventType))
	if normalizedRoomID == "" || !isTransientSignalingType(normalizedEventType) {
		return
	}

	clients, ok := h.rooms[normalizedRoomID]
	if !ok {
		return
	}

	payload := map[string]interface{}{
		"type":   normalizedEventType,
		"roomId": normalizedRoomID,
	}
	for key, value := range body {
		loweredKey := strings.ToLower(strings.TrimSpace(key))
		if loweredKey == "type" || loweredKey == "roomid" || loweredKey == "room_id" {
			continue
		}
		payload[key] = value
	}
	if _, hasPayload := payload["payload"]; !hasPayload {
		payload["payload"] = map[string]interface{}{}
	}

	targetUserID := resolveSignalingTargetUserID(payload)
	originUserID := resolveSignalingOriginUserID(payload)

	for roomClient := range clients {
		if roomClient.canWriteToRoom(normalizedRoomID) && !h.isClientRoomMember(roomClient.UserID, normalizedRoomID) {
			roomClient.subscribeToRoom(normalizedRoomID, false)
			continue
		}
		if !roomClient.canWriteToRoom(normalizedRoomID) {
			continue
		}

		normalizedClientUserID := normalizeUsername(roomClient.UserID)
		if targetUserID != "" && normalizedClientUserID != targetUserID {
			continue
		}
		if originUserID != "" && normalizedClientUserID == originUserID {
			continue
		}

		select {
		case roomClient.Send <- payload:
		default:
			h.removeClientFromAllRooms(roomClient, true)
			roomClient.closeSendChannel()
		}
	}
}

func (h *Hub) broadcastBoardEventToLocal(event RoomEvent) {
	roomID := normalizeRoomID(event.RoomID)
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	if roomID == "" || !isBoardEventType(eventType) {
		return
	}
	clients, ok := h.rooms[roomID]
	if !ok {
		return
	}

	payload := map[string]interface{}{
		"type":   eventType,
		"roomId": roomID,
	}
	for key, value := range event.Payload {
		loweredKey := strings.ToLower(strings.TrimSpace(key))
		if loweredKey == "type" || loweredKey == "roomid" || loweredKey == "room_id" {
			continue
		}
		payload[key] = value
	}

	originUserID := strings.TrimSpace(event.OriginUserID)
	for roomClient := range clients {
		if roomClient.canWriteToRoom(roomID) && !h.isClientRoomMember(roomClient.UserID, roomID) {
			roomClient.subscribeToRoom(roomID, false)
			continue
		}
		if !roomClient.canWriteToRoom(roomID) {
			continue
		}
		if originUserID != "" && roomClient.UserID == originUserID {
			continue
		}
		select {
		case roomClient.Send <- payload:
		default:
			h.removeClientFromAllRooms(roomClient, true)
			roomClient.closeSendChannel()
		}
	}
}

func (h *Hub) removeClientFromAllRooms(client *Client, broadcastUserLeft bool) {
	if client == nil {
		return
	}

	for roomID, clients := range h.rooms {
		if _, ok := clients[client]; !ok {
			continue
		}
		wasWritable := client.canWriteToRoom(roomID)
		delete(clients, client)
		client.unsubscribeFromRoom(roomID)

		if broadcastUserLeft && wasWritable {
			userLeftPayload := map[string]interface{}{
				"type":   "user_left",
				"roomId": roomID,
				"payload": map[string]interface{}{
					"id": client.UserID,
				},
			}
			for roomClient := range clients {
				if !roomClient.canWriteToRoom(roomID) {
					continue
				}
				select {
				case roomClient.Send <- userLeftPayload:
				default:
					delete(clients, roomClient)
					roomClient.unsubscribeFromRoom(roomID)
					roomClient.closeSendChannel()
				}
			}
		}

		if len(clients) == 0 {
			h.removeRoom(roomID)
		}
	}
}

func (h *Hub) removeRoom(roomID string) {
	if h == nil {
		return
	}
	if _, exists := h.rooms[roomID]; !exists {
		return
	}
	delete(h.rooms, roomID)
	monitor.ActiveRooms.Dec()
}

func (h *Hub) collectWritableOnlineMembers(roomID string) []map[string]interface{} {
	roomClients := h.rooms[roomID]
	onlineMembers := make([]map[string]interface{}, 0, len(roomClients))

	// Fetch the full admin set once so we can tag each member without N Redis calls.
	adminSet := map[string]struct{}{}
	if h.msgService != nil && h.msgService.Redis != nil && h.msgService.Redis.Client != nil {
		adminsKey := "room:" + normalizeRoomID(roomID) + ":admins"
		adminMembers, err := h.msgService.Redis.Client.SMembers(context.Background(), adminsKey).Result()
		if err == nil {
			for _, a := range adminMembers {
				if id := normalizeUsername(a); id != "" {
					adminSet[id] = struct{}{}
				}
			}
		}
	}

	for roomClient := range roomClients {
		if roomClient.canWriteToRoom(roomID) && !h.isClientRoomMember(roomClient.UserID, roomID) {
			roomClient.subscribeToRoom(roomID, false)
			continue
		}
		if !roomClient.canWriteToRoom(roomID) {
			continue
		}
		joinedAt := roomClient.JoinedAt
		if joinedAt.IsZero() {
			joinedAt = time.Now().UTC()
		}
		_, isAdmin := adminSet[normalizeUsername(roomClient.UserID)]
		onlineMembers = append(onlineMembers, map[string]interface{}{
			"id":       roomClient.UserID,
			"name":     roomClient.Username,
			"joinedAt": joinedAt.UnixMilli(),
			"isAdmin":  isAdmin,
		})
	}
	sort.SliceStable(onlineMembers, func(i, j int) bool {
		left := onlineMembers[i]["joinedAt"].(int64)
		right := onlineMembers[j]["joinedAt"].(int64)
		if left == right {
			return onlineMembers[i]["id"].(string) < onlineMembers[j]["id"].(string)
		}
		return left < right
	})
	return onlineMembers
}

func (h *Hub) isClientRoomMember(userID, roomID string) bool {
	userID = strings.TrimSpace(userID)
	roomID = normalizeRoomID(roomID)
	if userID == "" || roomID == "" {
		return false
	}
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return true
	}
	membersKey := "room:" + roomID + ":members"
	isMember, err := h.msgService.Redis.Client.SIsMember(context.Background(), membersKey, userID).Result()
	if err != nil {
		log.Printf("[ws] membership lookup failed room=%s user=%s err=%v", roomID, userID, err)
		return false
	}
	return isMember
}

func (h *Hub) isRoomPasswordProtected(roomID string) bool {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return false
	}
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return false
	}
	passwordHash, err := h.msgService.Redis.Client.HGet(
		context.Background(),
		roomKeyPrefix+roomID,
		"room_password_hash",
	).Result()
	if err == redis.Nil {
		return false
	}
	if err != nil {
		log.Printf("[ws] room password lookup failed room=%s err=%v", roomID, err)
		return false
	}
	return strings.TrimSpace(passwordHash) != ""
}

func parseBoolFlag(value string, defaultValue bool) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return defaultValue
	}
	switch normalized {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return defaultValue
	}
}

func (h *Hub) getRoomFeatureFlags(roomID string) (bool, bool) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return true, false
	}
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return true, false
	}

	values, err := h.msgService.Redis.Client.HMGet(
		context.Background(),
		roomKeyPrefix+roomID,
		"ai_enabled",
		"e2ee_enabled",
		"e2e_enabled",
	).Result()
	if err != nil && err != redis.Nil {
		log.Printf("[ws] room feature lookup failed room=%s err=%v", roomID, err)
		return true, false
	}
	aiEnabled := true
	e2eEnabled := false
	if len(values) > 0 {
		aiEnabled = parseBoolFlag(toString(values[0]), true)
	}
	if len(values) > 1 {
		rawE2E := strings.TrimSpace(toString(values[1]))
		if rawE2E == "" && len(values) > 2 {
			rawE2E = strings.TrimSpace(toString(values[2]))
		}
		e2eEnabled = parseBoolFlag(rawE2E, false)
	}
	if e2eEnabled {
		aiEnabled = false
	}
	return aiEnabled, e2eEnabled
}

func (h *Hub) isRoomAIEnabled(roomID string) bool {
	aiEnabled, _ := h.getRoomFeatureFlags(roomID)
	return aiEnabled
}

func (h *Hub) isRoomE2EEEnabled(roomID string) bool {
	_, e2eEnabled := h.getRoomFeatureFlags(roomID)
	return e2eEnabled
}

func (h *Hub) shouldPersistRoomMessagesToCloud(roomID string) bool {
	return !h.isRoomE2EEEnabled(roomID)
}

func (h *Hub) getRoomMemberJoinedAt(userID, roomID string) time.Time {
	normalizedUserID := strings.TrimSpace(userID)
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedUserID == "" || normalizedRoomID == "" {
		return time.Time{}
	}
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return time.Time{}
	}
	rawJoinedAt, err := h.msgService.Redis.Client.HGet(
		context.Background(),
		roomKeyPrefix+normalizedRoomID+":member_joined_at",
		normalizedUserID,
	).Result()
	if err != nil {
		if err != redis.Nil {
			log.Printf("[ws] room member joined-at lookup failed room=%s user=%s err=%v", normalizedRoomID, normalizedUserID, err)
		}
		return time.Time{}
	}
	joinedAtUnix, parseErr := strconv.ParseInt(strings.TrimSpace(rawJoinedAt), 10, 64)
	if parseErr != nil || joinedAtUnix <= 0 {
		return time.Time{}
	}
	return time.Unix(joinedAtUnix, 0).UTC()
}
