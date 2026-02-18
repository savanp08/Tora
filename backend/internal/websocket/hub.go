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

type Hub struct {
	rooms      map[string]map[*Client]bool
	broadcast  chan models.Message
	redisInbox chan models.Message
	register   chan *Client
	unregister chan *Client

	msgService *MessageService
	tracker    *monitor.UsageTracker
}

func NewHub(service *MessageService, tracker *monitor.UsageTracker) *Hub {
	hub := &Hub{
		broadcast:  make(chan models.Message),
		redisInbox: make(chan models.Message, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		rooms:      make(map[string]map[*Client]bool),
		msgService: service,
		tracker:    tracker,
	}

	if service != nil && service.CanPersistToDisk() {
		go hub.persistenceWorker()
	}
	if service != nil && service.Redis != nil && service.Redis.Client != nil {
		go hub.Subscribe()
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
			if _, ok := h.rooms[client.RoomID]; !ok {
				h.rooms[client.RoomID] = make(map[*Client]bool)
			}
			if client.JoinedAt.IsZero() {
				client.JoinedAt = time.Now().UTC()
			}
			h.rooms[client.RoomID][client] = true

			onlineMembers := make([]map[string]interface{}, 0, len(h.rooms[client.RoomID]))
			for roomClient := range h.rooms[client.RoomID] {
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

			select {
			case client.Send <- map[string]interface{}{
				"type":    "online_list",
				"payload": onlineMembers,
			}:
			default:
				close(client.Send)
				delete(h.rooms[client.RoomID], client)
				continue
			}

			joinedPayload := map[string]interface{}{
				"type": "user_joined",
				"payload": map[string]interface{}{
					"id":       client.UserID,
					"name":     client.Username,
					"joinedAt": client.JoinedAt.UnixMilli(),
				},
			}
			for roomClient := range h.rooms[client.RoomID] {
				if roomClient == client {
					continue
				}
				select {
				case roomClient.Send <- joinedPayload:
				default:
					close(roomClient.Send)
					delete(h.rooms[client.RoomID], roomClient)
				}
			}

			if h.msgService != nil {
				go client.LoadHistory(context.Background(), h.msgService)
			}

		case client := <-h.unregister:
			if _, ok := h.rooms[client.RoomID]; ok {
				if _, ok := h.rooms[client.RoomID][client]; ok {
					delete(h.rooms[client.RoomID], client)
					close(client.Send)

					userLeftPayload := map[string]interface{}{
						"type": "user_left",
						"payload": map[string]interface{}{
							"id": client.UserID,
						},
					}
					for roomClient := range h.rooms[client.RoomID] {
						select {
						case roomClient.Send <- userLeftPayload:
						default:
							close(roomClient.Send)
							delete(h.rooms[client.RoomID], roomClient)
						}
					}
				}

				if len(h.rooms[client.RoomID]) == 0 {
					delete(h.rooms, client.RoomID)
				}
			}

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
		}
	}
}

func (h *Hub) broadcastToLocal(msg models.Message) {
	clients, ok := h.rooms[msg.RoomID]
	if !ok {
		return
	}

	for client := range clients {
		select {
		case client.Send <- msg:
		default:
			close(client.Send)
			delete(clients, client)
		}
	}
}
