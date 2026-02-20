package websocket

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/monitor"
)

const chatBroadcastChannel = "chat:broadcast"
const chatTypingChannel = "chat:typing"
const chatMutationChannel = "chat:message_mutation"

type Hub struct {
	rooms         map[string]map[*Client]bool
	broadcast     chan models.Message
	redisInbox    chan models.Message
	typing        chan *ClientTypingEvent
	typingInbox   chan TypingRedisEvent
	messageEdit   chan *ClientMessageEditEvent
	messageDelete chan *ClientMessageDeleteEvent
	mutationInbox chan MessageMutationEvent
	register      chan *Client
	unregister    chan *Client
	subscribe     chan *ClientSubscription

	msgService *MessageService
	tracker    *monitor.UsageTracker
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

type MessageMutationEvent struct {
	Type      string `json:"type"`
	RoomID    string `json:"roomId"`
	MessageID string `json:"messageId"`
	Content   string `json:"content,omitempty"`
	IsEdited  bool   `json:"isEdited"`
	EditedAt  int64  `json:"editedAt,omitempty"`
}

type ClientSubscription struct {
	Client  *Client
	RoomIDs []string
}

func NewHub(service *MessageService, tracker *monitor.UsageTracker) *Hub {
	hub := &Hub{
		broadcast:     make(chan models.Message),
		redisInbox:    make(chan models.Message, 256),
		typing:        make(chan *ClientTypingEvent, 256),
		typingInbox:   make(chan TypingRedisEvent, 512),
		messageEdit:   make(chan *ClientMessageEditEvent, 256),
		messageDelete: make(chan *ClientMessageDeleteEvent, 256),
		mutationInbox: make(chan MessageMutationEvent, 512),
		register:      make(chan *Client),
		unregister:    make(chan *Client),
		subscribe:     make(chan *ClientSubscription, 256),
		rooms:         make(map[string]map[*Client]bool),
		msgService:    service,
		tracker:       tracker,
	}

	if service != nil && service.CanPersistToDisk() {
		go hub.persistenceWorker()
	}
	if service != nil && service.Redis != nil && service.Redis.Client != nil {
		go hub.Subscribe()
		go hub.SubscribeTyping()
		go hub.SubscribeMessageMutations()
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
				log.Printf("redis subscribe drop room=%s reason=inbox_full", msg.RoomID)
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
				log.Printf("redis typing drop room=%s user=%s reason=inbox_full", event.RoomID, event.UserID)
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
				log.Printf("redis mutation drop room=%s message=%s reason=inbox_full", event.RoomID, event.MessageID)
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

		result, err := h.msgService.Redis.Client.BLPop(ctx, 0, messageQueueKey).Result()
		if err != nil {
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

		case editEvent := <-h.messageEdit:
			h.handleClientMessageEditEvent(editEvent)

		case deleteEvent := <-h.messageDelete:
			h.handleClientMessageDeleteEvent(deleteEvent)

		case msg := <-h.broadcast:
			if msg.CreatedAt.IsZero() {
				msg.CreatedAt = time.Now().UTC()
			}

			if h.msgService != nil {
				go func(m models.Message) {
					if err := h.msgService.EnqueueMessage(context.Background(), m); err != nil {
						log.Printf("enqueue message error: %v", err)
					}
				}(msg)

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

		case mutationEvent := <-h.mutationInbox:
			h.broadcastMutationToLocal(mutationEvent)
		}
	}
}

func (h *Hub) broadcastToLocal(msg models.Message) {
	clients, ok := h.rooms[msg.RoomID]
	if !ok {
		return
	}

	for client := range clients {
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

		if _, ok := h.rooms[roomID]; !ok {
			h.rooms[roomID] = make(map[*Client]bool)
		}
		canWrite := h.isClientRoomMember(client.UserID, roomID)
		alreadySubscribed := h.rooms[roomID][client]
		alreadyWritable := client.canWriteToRoom(roomID)
		if alreadySubscribed && alreadyWritable == canWrite {
			continue
		}

		h.rooms[roomID][client] = true
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
			joinedPayload := map[string]interface{}{
				"type":   "user_joined",
				"roomId": roomID,
				"payload": map[string]interface{}{
					"id":       client.UserID,
					"name":     client.Username,
					"joinedAt": client.JoinedAt.UnixMilli(),
				},
			}
			for roomClient := range h.rooms[roomID] {
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
	if roomID == "" || !client.isSubscribedToRoom(roomID) || !client.canWriteToRoom(roomID) {
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

	if h.msgService != nil && h.msgService.Redis != nil && h.msgService.Redis.Client != nil {
		payload, err := json.Marshal(typingEvent)
		if err != nil {
			log.Printf("redis typing marshal error: %v", err)
			h.broadcastTypingToLocal(typingEvent)
			return
		}
		if err := h.msgService.Redis.Client.Publish(context.Background(), chatTypingChannel, payload).Err(); err != nil {
			log.Printf("redis typing publish error: %v", err)
			h.broadcastTypingToLocal(typingEvent)
		}
		return
	}

	h.broadcastTypingToLocal(typingEvent)
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
	ownsMessage, err := h.msgService.IsMessageOwnedBy(context.Background(), roomID, messageID, client.UserID)
	if err != nil {
		log.Printf("[ws] message edit ownership check failed room=%s message=%s user=%s err=%v", roomID, messageID, client.UserID, err)
		return
	}
	if !ownsMessage {
		log.Printf("[ws] message edit denied room=%s message=%s user=%s reason=not_owner", roomID, messageID, client.UserID)
		return
	}
	if err := h.msgService.UpdateMessageContent(context.Background(), roomID, messageID, content, time.Now().UTC()); err != nil {
		log.Printf("[ws] message edit failed room=%s message=%s user=%s err=%v", roomID, messageID, client.UserID, err)
		return
	}

	mutation := MessageMutationEvent{
		Type:      "message_edit",
		RoomID:    roomID,
		MessageID: messageID,
		Content:   content,
		IsEdited:  true,
		EditedAt:  time.Now().UTC().UnixMilli(),
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
		log.Printf("[ws] message delete denied room=%s message=%s user=%s reason=not_owner", roomID, messageID, client.UserID)
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
		"type":   envelopeType,
		"roomId": roomID,
		"payload": map[string]interface{}{
			"id":   userID,
			"name": event.UserName,
		},
	}
	for roomClient := range clients {
		if roomClient.canWriteToRoom(roomID) && !h.isClientRoomMember(roomClient.UserID, roomID) {
			roomClient.subscribeToRoom(roomID, false)
			continue
		}
		if !roomClient.canWriteToRoom(roomID) {
			continue
		}
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
			"type":      map[string]string{"message_delete": "deleted", "message_edit": "text"}[eventType],
		},
	}

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
			delete(h.rooms, roomID)
		}
	}
}

func (h *Hub) collectWritableOnlineMembers(roomID string) []map[string]interface{} {
	roomClients := h.rooms[roomID]
	onlineMembers := make([]map[string]interface{}, 0, len(roomClients))
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
		onlineMembers = append(onlineMembers, map[string]interface{}{
			"id":       roomClient.UserID,
			"name":     roomClient.Username,
			"joinedAt": joinedAt.UnixMilli(),
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
