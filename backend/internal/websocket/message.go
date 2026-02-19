package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gocql/gocql"
	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
)

const (
	messageQueueKey   = "msg_queue"
	roomHistoryPrefix = "room:history:"
	roomHistoryTTL    = 21600
	roomHistorySize   = 50
	scyllaMessageTTL  = 21600
	messageBreakMeta  = "message:break:"
	roomKeyPrefix     = "room:"
)

type MessageService struct {
	Redis          *database.RedisStore
	Scylla         *database.ScyllaStore
	scyllaDisabled atomic.Bool
	panicFailures  atomic.Int32
}

func NewMessageService(redisStore *database.RedisStore, scyllaStore *database.ScyllaStore) *MessageService {
	service := &MessageService{Redis: redisStore, Scylla: scyllaStore}
	service.ensureSchema()
	return service
}

func (s *MessageService) CanPersistToDisk() bool {
	return s != nil &&
		s.Redis != nil &&
		s.Redis.Client != nil &&
		s.Scylla != nil &&
		s.Scylla.Session != nil &&
		!s.scyllaDisabled.Load()
}

func (s *MessageService) EnqueueMessage(ctx context.Context, msg models.Message) error {
	if s.Redis == nil || s.Redis.Client == nil {
		return fmt.Errorf("redis client is not configured")
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	if err := s.Redis.Client.RPush(ctx, messageQueueKey, payload).Err(); err != nil {
		return fmt.Errorf("enqueue message: %w", err)
	}

	return nil
}

func (s *MessageService) SaveToScylla(msg models.Message) error {
	if s.Scylla == nil || s.Scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if s.scyllaDisabled.Load() {
		return fmt.Errorf("scylla persistence disabled after repeated panics")
	}

	ttlSeconds := s.resolveRoomTTLSeconds(context.Background(), msg.RoomID)
	messagesTable := s.Scylla.Table("messages")
	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, message_id, sender_id, sender_name, content, type, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) USING TTL %d`,
		messagesTable,
		ttlSeconds,
	)
	if err := safeExecScyllaQuery(
		s.Scylla.Session,
		query,
		msg.RoomID,
		msg.ID,
		msg.SenderID,
		msg.SenderName,
		msg.Content,
		msg.Type,
		msg.CreatedAt,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "panic") {
			failures := s.panicFailures.Add(1)
			if failures >= 3 && s.scyllaDisabled.CompareAndSwap(false, true) {
				log.Printf("[message-service] disabling scylla persistence after repeated panics")
			}
		}
		return fmt.Errorf("save to scylla: %w", err)
	}
	s.panicFailures.Store(0)

	return nil
}

func (s *MessageService) CacheRecentMessage(ctx context.Context, msg models.Message) error {
	if s.Redis == nil || s.Redis.Client == nil {
		return fmt.Errorf("redis client is not configured")
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	historyTTLSeconds := s.resolveRoomTTLSeconds(ctx, msg.RoomID)
	historyTTL := time.Duration(historyTTLSeconds) * time.Second
	historyKey := roomHistoryPrefix + msg.RoomID
	pipe := s.Redis.Client.TxPipeline()
	pipe.RPush(ctx, historyKey, payload)
	pipe.LTrim(ctx, historyKey, -roomHistorySize, -1)
	pipe.Expire(ctx, historyKey, historyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("cache recent message: %w", err)
	}

	return nil
}

func (s *MessageService) resolveRoomTTLSeconds(ctx context.Context, roomID string) int {
	if s == nil || s.Redis == nil || s.Redis.Client == nil || roomID == "" {
		return scyllaMessageTTL
	}

	ttl, err := s.Redis.Client.TTL(ctx, roomKeyPrefix+roomID).Result()
	if err != nil || ttl <= 0 {
		return scyllaMessageTTL
	}

	seconds := int(ttl / time.Second)
	if seconds <= 0 {
		return 1
	}
	return seconds
}

func (s *MessageService) GetRecentMessages(ctx context.Context, roomID string) ([]models.Message, error) {
	if roomID == "" {
		return []models.Message{}, nil
	}

	redisMessages := make([]models.Message, 0, roomHistorySize)
	if s.Redis != nil && s.Redis.Client != nil {
		historyKey := roomHistoryPrefix + roomID
		cached, err := s.Redis.Client.LRange(ctx, historyKey, 0, -1).Result()
		if err != nil {
			return nil, fmt.Errorf("load cached history: %w", err)
		}

		redisMessages = decodeCachedMessages(cached)
		if len(redisMessages) > roomHistorySize {
			redisMessages = redisMessages[len(redisMessages)-roomHistorySize:]
		}
	}

	if len(redisMessages) >= roomHistorySize {
		s.enrichBreakMetadata(ctx, redisMessages)
		return redisMessages, nil
	}

	needed := roomHistorySize - len(redisMessages)
	if s.Scylla == nil || s.Scylla.Session == nil {
		s.enrichBreakMetadata(ctx, redisMessages)
		return redisMessages, nil
	}

	var before *time.Time
	if len(redisMessages) > 0 && !redisMessages[0].CreatedAt.IsZero() {
		oldestCached := redisMessages[0].CreatedAt
		before = &oldestCached
	}

	scyllaMessagesDesc, err := s.queryScyllaMessages(roomID, needed, before)
	if err != nil && before != nil {
		log.Printf("[message-service] scoped scylla query failed room=%s err=%v", roomID, err)
		scyllaMessagesDesc, err = s.queryScyllaMessages(roomID, needed, nil)
	}
	if err != nil {
		return nil, err
	}

	for left, right := 0, len(scyllaMessagesDesc)-1; left < right; left, right = left+1, right-1 {
		scyllaMessagesDesc[left], scyllaMessagesDesc[right] = scyllaMessagesDesc[right], scyllaMessagesDesc[left]
	}

	combined := append(scyllaMessagesDesc, redisMessages...)
	combined = dedupeChronological(combined)
	if len(combined) > roomHistorySize {
		combined = combined[len(combined)-roomHistorySize:]
	}
	s.enrichBreakMetadata(ctx, combined)

	return combined, nil
}

func (s *MessageService) ensureSchema() {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return
	}

	messagesTable := s.Scylla.Table("messages")
	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text,
			created_at timestamp,
			message_id text,
			sender_id text,
			sender_name text,
			content text,
			type text,
			PRIMARY KEY ((room_id), created_at, message_id)
		) WITH CLUSTERING ORDER BY (created_at DESC, message_id DESC)`,
		messagesTable,
	)

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := safeExecScyllaQuery(s.Scylla.Session, query); err == nil {
			return
		} else {
			lastErr = err
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
	}
	if lastErr != nil {
		log.Printf("[message-service] ensure messages schema failed: %v", lastErr)
	}
}

func (s *MessageService) enrichBreakMetadata(ctx context.Context, messages []models.Message) {
	if len(messages) == 0 || s.Redis == nil || s.Redis.Client == nil {
		return
	}

	pipe := s.Redis.Client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(messages))
	for index, msg := range messages {
		if msg.ID == "" {
			continue
		}
		cmds[index] = pipe.HGetAll(ctx, messageBreakMeta+msg.ID)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return
	}

	for index, cmd := range cmds {
		if cmd == nil {
			continue
		}
		fields, err := cmd.Result()
		if err != nil || len(fields) == 0 {
			continue
		}

		hasBreak := fields["has_break_room"] == "1" || strings.EqualFold(fields["has_break_room"], "true")
		breakRoomID := fields["break_room_id"]
		joinCount, _ := strconv.Atoi(fields["break_join_count"])

		if hasBreak || breakRoomID != "" {
			messages[index].HasBreakRoom = true
			messages[index].BreakRoomID = breakRoomID
			messages[index].BreakJoinCount = joinCount
		}
	}
}

func decodeCachedMessages(rawMessages []string) []models.Message {
	messages := make([]models.Message, 0, len(rawMessages))
	for _, raw := range rawMessages {
		var msg models.Message
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}

		if msg.Content == "" || msg.SenderID == "" || msg.SenderName == "" || msg.CreatedAt.IsZero() {
			var legacy map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &legacy); err == nil {
				if msg.Content == "" {
					msg.Content = toString(legacy["content"])
					if msg.Content == "" {
						msg.Content = toString(legacy["text"])
					}
				}
				if msg.SenderID == "" {
					msg.SenderID = toString(legacy["senderId"])
					if msg.SenderID == "" {
						msg.SenderID = toString(legacy["userId"])
					}
				}
				if msg.SenderName == "" {
					msg.SenderName = toString(legacy["senderName"])
					if msg.SenderName == "" {
						msg.SenderName = toString(legacy["username"])
					}
				}
				if msg.CreatedAt.IsZero() {
					msg.CreatedAt = toTime(legacy["createdAt"])
					if msg.CreatedAt.IsZero() {
						msg.CreatedAt = toTime(legacy["time"])
					}
				}
			}
		}
		messages = append(messages, msg)
	}
	return messages
}

func dedupeChronological(messages []models.Message) []models.Message {
	seen := make(map[string]struct{}, len(messages))
	result := make([]models.Message, 0, len(messages))
	for _, msg := range messages {
		key := msg.ID
		if key == "" {
			key = fmt.Sprintf("%s|%s|%s", msg.RoomID, msg.SenderID, msg.Content)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, msg)
	}
	return result
}

func (s *MessageService) queryScyllaMessages(roomID string, limit int, before *time.Time) (messages []models.Message, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("load scylla history panic: %v", recovered)
		}
	}()

	if limit <= 0 {
		return []models.Message{}, nil
	}

	messagesTable := s.Scylla.Table("messages")
	query := fmt.Sprintf(
		`SELECT room_id, message_id, sender_id, sender_name, content, type, created_at FROM %s WHERE room_id = ? ORDER BY created_at DESC LIMIT ?`,
		messagesTable,
	)
	args := []interface{}{roomID, limit}
	if before != nil {
		query = fmt.Sprintf(
			`SELECT room_id, message_id, sender_id, sender_name, content, type, created_at FROM %s WHERE room_id = ? AND created_at < ? ORDER BY created_at DESC LIMIT ?`,
			messagesTable,
		)
		args = []interface{}{roomID, *before, limit}
	}

	iter := s.Scylla.Session.Query(query, args...).Iter()

	messages = make([]models.Message, 0, limit)
	var dbRoomID string
	var messageID string
	var senderID string
	var senderName string
	var content string
	var msgType string
	var createdAt time.Time

	for iter.Scan(&dbRoomID, &messageID, &senderID, &senderName, &content, &msgType, &createdAt) {
		messages = append(messages, models.Message{
			ID:         messageID,
			RoomID:     dbRoomID,
			SenderID:   senderID,
			SenderName: senderName,
			Content:    content,
			Type:       msgType,
			CreatedAt:  createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("load scylla history: %w", err)
	}

	return messages, nil
}

func safeExecScyllaQuery(session *gocql.Session, query string, args ...interface{}) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("scylla query panic: %v", recovered)
		}
	}()

	return session.Query(query, args...).Exec()
}

func toString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case int:
		return fmt.Sprintf("%d", v)
	default:
		return ""
	}
}

func toTime(value interface{}) time.Time {
	switch v := value.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return parsed
		}
		if parsed, err := time.Parse(time.RFC3339, v); err == nil {
			return parsed
		}
	case float64:
		return time.Unix(int64(v), 0).UTC()
	case int64:
		return time.Unix(v, 0).UTC()
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return time.Unix(n, 0).UTC()
		}
	}
	return time.Time{}
}
