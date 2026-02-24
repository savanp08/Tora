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
	"github.com/savanp08/converse/internal/security"
)

const (
	messageQueueKey            = "msg_queue"
	roomHistoryPrefix          = "room:history:"
	roomHistoryTTL             = 21600
	roomHistorySize            = 50
	scyllaMessageTTL           = 15 * 24 * 60 * 60
	messageBreakMeta           = "message:break:"
	roomKeyPrefix              = "room:"
	roomMessageSoftExpiryTable = "room_message_soft_expiry"
	DeletedMessagePlaceholder  = "This message was deleted"
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
		`INSERT INTO %s (room_id, message_id, sender_id, sender_name, content, type, media_url, media_type, file_name, is_edited, edited_at, has_break_room, break_room_id, break_join_count, reply_to_message_id, reply_to_snippet, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL %d`,
		messagesTable,
		ttlSeconds,
	)
	var editedAt interface{}
	if msg.EditedAt != nil && !msg.EditedAt.IsZero() {
		editedAt = *msg.EditedAt
	}

	encryptedContent, err := security.EncryptMessage(msg.Content)
	if err != nil {
		return fmt.Errorf("encrypt message content: %w", err)
	}

	if err := safeExecScyllaQuery(
		s.Scylla.Session,
		query,
		msg.RoomID,
		msg.ID,
		msg.SenderID,
		msg.SenderName,
		encryptedContent,
		msg.Type,
		msg.MediaURL,
		msg.MediaType,
		msg.FileName,
		msg.IsEdited,
		editedAt,
		msg.HasBreakRoom,
		msg.BreakRoomID,
		msg.BreakJoinCount,
		msg.ReplyToMessageID,
		msg.ReplyToSnippet,
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

	if strings.EqualFold(strings.TrimSpace(msg.Type), "task") {
		pinsTable := s.Scylla.Table("room_pins")
		pinQuery := fmt.Sprintf(
			`INSERT INTO %s (room_id, created_at, message_id, type) VALUES (?, ?, ?, ?) USING TTL %d`,
			pinsTable,
			ttlSeconds,
		)
		if err := safeExecScyllaQuery(
			s.Scylla.Session,
			pinQuery,
			msg.RoomID,
			msg.CreatedAt,
			msg.ID,
			"task",
		); err != nil {
			return fmt.Errorf("save task pin index: %w", err)
		}
	}

	return nil
}

func (s *MessageService) CacheRecentMessage(ctx context.Context, msg models.Message) error {
	if s.Redis == nil || s.Redis.Client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	if strings.EqualFold(strings.TrimSpace(msg.Type), "task") {
		return nil
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
		s.enrichPinMetadata(ctx, roomID, redisMessages)
		return redisMessages, nil
	}

	needed := roomHistorySize - len(redisMessages)
	if s.Scylla == nil || s.Scylla.Session == nil {
		s.enrichBreakMetadata(ctx, redisMessages)
		s.enrichPinMetadata(ctx, roomID, redisMessages)
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
	s.enrichPinMetadata(ctx, roomID, combined)

	return combined, nil
}

func (s *MessageService) ensureSchema() {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return
	}

	messagesTable := s.Scylla.Table("messages")
	roomPinsTable := s.Scylla.Table("room_pins")
	roomSoftExpiryTable := s.Scylla.Table(roomMessageSoftExpiryTable)
	pinDiscussionCommentsTable := s.Scylla.Table("pin_discussion_comments")
	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text,
			created_at timestamp,
			message_id text,
			sender_id text,
			sender_name text,
			content text,
			type text,
			media_url text,
			media_type text,
			file_name text,
			is_edited boolean,
			edited_at timestamp,
			has_break_room boolean,
			break_room_id text,
			break_join_count int,
			reply_to_message_id text,
			reply_to_snippet text,
			PRIMARY KEY ((room_id), created_at, message_id)
		) WITH CLUSTERING ORDER BY (created_at DESC, message_id DESC)`,
		messagesTable,
	)
	roomPinsQuery := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text,
			created_at timestamp,
			message_id text,
			type text,
			PRIMARY KEY (room_id, created_at)
		) WITH CLUSTERING ORDER BY (created_at DESC)`,
		roomPinsTable,
	)
	pinDiscussionCommentsQuery := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text,
			pin_message_id text,
			created_at timestamp,
			comment_id text,
			parent_comment_id text,
			sender_id text,
			sender_name text,
			content text,
			is_edited boolean,
			edited_at timestamp,
			is_deleted boolean,
			is_pinned boolean,
			pinned_by text,
			pinned_by_name text,
			pinned_at timestamp,
			PRIMARY KEY ((room_id, pin_message_id), created_at, comment_id)
		) WITH CLUSTERING ORDER BY (created_at ASC, comment_id ASC)`,
		pinDiscussionCommentsTable,
	)
	roomSoftExpiryQuery := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text PRIMARY KEY,
			extended_expiry_time timestamp,
			updated_at timestamp
		)`,
		roomSoftExpiryTable,
	)

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := safeExecScyllaQuery(s.Scylla.Session, query); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
	}
	if lastErr != nil {
		log.Printf("[message-service] ensure messages schema failed: %v", lastErr)
		return
	}

	lastErr = nil
	for attempt := 1; attempt <= 3; attempt++ {
		if err := safeExecScyllaQuery(s.Scylla.Session, roomPinsQuery); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
	}
	if lastErr != nil {
		log.Printf("[message-service] ensure room_pins schema failed: %v", lastErr)
		return
	}

	lastErr = nil
	for attempt := 1; attempt <= 3; attempt++ {
		if err := safeExecScyllaQuery(s.Scylla.Session, pinDiscussionCommentsQuery); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
	}
	if lastErr != nil {
		log.Printf("[message-service] ensure pin_discussion_comments schema failed: %v", lastErr)
		return
	}
	lastErr = nil
	for attempt := 1; attempt <= 3; attempt++ {
		if err := safeExecScyllaQuery(s.Scylla.Session, roomSoftExpiryQuery); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
	}
	if lastErr != nil {
		log.Printf("[message-service] ensure room message soft-expiry schema failed: %v", lastErr)
		return
	}

	alterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD media_url text`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD media_type text`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD file_name text`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD is_edited boolean`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD edited_at timestamp`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD has_break_room boolean`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD break_room_id text`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD break_join_count int`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD reply_to_message_id text`, messagesTable),
		fmt.Sprintf(`ALTER TABLE %s ADD reply_to_snippet text`, messagesTable),
	}
	for _, alterQuery := range alterQueries {
		if err := safeExecScyllaQuery(s.Scylla.Session, alterQuery); err != nil && !isSchemaAlreadyApplied(err) {
			log.Printf("[message-service] ensure messages schema alter failed: %v", err)
		}
	}

	pinDiscussionAlterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD is_pinned boolean`, pinDiscussionCommentsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD pinned_by text`, pinDiscussionCommentsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD pinned_by_name text`, pinDiscussionCommentsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD pinned_at timestamp`, pinDiscussionCommentsTable),
	}
	for _, alterQuery := range pinDiscussionAlterQueries {
		if err := safeExecScyllaQuery(s.Scylla.Session, alterQuery); err != nil && !isSchemaAlreadyApplied(err) {
			log.Printf("[message-service] ensure pin discussion schema alter failed: %v", err)
		}
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

func (s *MessageService) enrichPinMetadata(ctx context.Context, roomID string, messages []models.Message) {
	if len(messages) == 0 || s == nil || s.Scylla == nil || s.Scylla.Session == nil || roomID == "" {
		return
	}

	first := messages[0].CreatedAt
	last := messages[0].CreatedAt
	for _, message := range messages {
		if message.CreatedAt.Before(first) {
			first = message.CreatedAt
		}
		if message.CreatedAt.After(last) {
			last = message.CreatedAt
		}
	}
	if first.IsZero() || last.IsZero() {
		return
	}

	roomPinsTable := s.Scylla.Table("room_pins")
	query := fmt.Sprintf(
		`SELECT message_id FROM %s WHERE room_id = ? AND created_at >= ? AND created_at <= ?`,
		roomPinsTable,
	)
	iter := s.Scylla.Session.Query(query, roomID, first, last).WithContext(ctx).Iter()
	pinnedByMessageID := make(map[string]bool)
	var pinnedMessageID string
	for iter.Scan(&pinnedMessageID) {
		normalizedPinnedID := normalizeMessageID(pinnedMessageID)
		if normalizedPinnedID == "" {
			continue
		}
		pinnedByMessageID[normalizedPinnedID] = true
	}
	if err := iter.Close(); err != nil {
		return
	}
	if len(pinnedByMessageID) == 0 {
		return
	}
	for index, message := range messages {
		if pinnedByMessageID[normalizeMessageID(message.ID)] {
			messages[index].IsPinned = true
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

	ctx := context.Background()
	softCutoff, cutoffErr := s.resolveSoftMessageCutoff(ctx, roomID)
	if cutoffErr != nil {
		return nil, cutoffErr
	}

	messagesTable := s.Scylla.Table("messages")
	query := fmt.Sprintf(
		`SELECT room_id, message_id, sender_id, sender_name, content, type, media_url, media_type, file_name, is_edited, edited_at, has_break_room, break_room_id, break_join_count, reply_to_message_id, reply_to_snippet, created_at FROM %s WHERE room_id = ? AND created_at >= ? ORDER BY created_at DESC LIMIT ?`,
		messagesTable,
	)
	args := []interface{}{roomID, softCutoff, limit}
	if before != nil {
		query = fmt.Sprintf(
			`SELECT room_id, message_id, sender_id, sender_name, content, type, media_url, media_type, file_name, is_edited, edited_at, has_break_room, break_room_id, break_join_count, reply_to_message_id, reply_to_snippet, created_at FROM %s WHERE room_id = ? AND created_at < ? AND created_at >= ? ORDER BY created_at DESC LIMIT ?`,
			messagesTable,
		)
		args = []interface{}{roomID, *before, softCutoff, limit}
	}

	iter := s.Scylla.Session.Query(query, args...).WithContext(ctx).Iter()

	messages = make([]models.Message, 0, limit)
	var dbRoomID string
	var messageID string
	var senderID string
	var senderName string
	var content string
	var msgType string
	var mediaURL string
	var mediaType string
	var fileName string
	var isEdited bool
	var editedAt time.Time
	var hasBreakRoom bool
	var breakRoomID string
	var breakJoinCount int
	var replyToMessageID string
	var replyToSnippet string
	var createdAt time.Time

	for iter.Scan(&dbRoomID, &messageID, &senderID, &senderName, &content, &msgType, &mediaURL, &mediaType, &fileName, &isEdited, &editedAt, &hasBreakRoom, &breakRoomID, &breakJoinCount, &replyToMessageID, &replyToSnippet, &createdAt) {
		if createdAt.Before(softCutoff) {
			continue
		}
		if decrypted, decryptErr := security.DecryptMessage(content); decryptErr == nil {
			content = decrypted
		}

		var editedAtPtr *time.Time
		if !editedAt.IsZero() {
			editedCopy := editedAt
			editedAtPtr = &editedCopy
		}
		messages = append(messages, models.Message{
			ID:               messageID,
			RoomID:           dbRoomID,
			SenderID:         senderID,
			SenderName:       senderName,
			Content:          content,
			Type:             msgType,
			MediaURL:         mediaURL,
			MediaType:        mediaType,
			FileName:         fileName,
			IsEdited:         isEdited,
			EditedAt:         editedAtPtr,
			HasBreakRoom:     hasBreakRoom,
			BreakRoomID:      breakRoomID,
			BreakJoinCount:   breakJoinCount,
			ReplyToMessageID: replyToMessageID,
			ReplyToSnippet:   replyToSnippet,
			CreatedAt:        createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("load scylla history: %w", err)
	}

	return messages, nil
}

func (s *MessageService) resolveSoftMessageCutoff(ctx context.Context, roomID string) (time.Time, error) {
	defaultCutoff := time.Now().UTC().Add(-time.Duration(roomHistoryTTL) * time.Second)
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil || roomID == "" {
		return defaultCutoff, nil
	}

	if s.Redis != nil && s.Redis.Client != nil {
		createdAtRaw, err := s.Redis.Client.HGet(ctx, roomKeyPrefix+roomID, "created_at").Result()
		if err == nil {
			if createdAtUnix, parseErr := strconv.ParseInt(strings.TrimSpace(createdAtRaw), 10, 64); parseErr == nil && createdAtUnix > 0 {
				createdAtCutoff := time.Unix(createdAtUnix, 0).UTC()
				if !createdAtCutoff.IsZero() && createdAtCutoff.Before(defaultCutoff) {
					defaultCutoff = createdAtCutoff
				}
			}
		}
	}

	softExpiryTable := s.Scylla.Table(roomMessageSoftExpiryTable)
	query := fmt.Sprintf(
		`SELECT extended_expiry_time FROM %s WHERE room_id = ? LIMIT 1`,
		softExpiryTable,
	)

	var softCutoff time.Time
	if err := s.Scylla.Session.Query(query, roomID).WithContext(ctx).Scan(&softCutoff); err != nil {
		if err == gocql.ErrNotFound {
			return defaultCutoff, nil
		}
		return time.Time{}, fmt.Errorf("resolve room soft-expiry cutoff: %w", err)
	}
	if softCutoff.IsZero() {
		return defaultCutoff, nil
	}
	return softCutoff.UTC(), nil
}

func safeExecScyllaQuery(session *gocql.Session, query string, args ...interface{}) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("scylla query panic: %v", recovered)
		}
	}()

	return session.Query(query, args...).Exec()
}

func isSchemaAlreadyApplied(err error) bool {
	if err == nil {
		return false
	}
	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "already exists") ||
		strings.Contains(lowered, "duplicate") ||
		strings.Contains(lowered, "conflicts with an existing column")
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

func (s *MessageService) UpdateMessageContent(ctx context.Context, roomID, messageID, content string, editedAt time.Time) (string, error) {
	roomID = normalizeRoomID(roomID)
	messageID = normalizeMessageID(messageID)
	content = strings.TrimSpace(content)
	if roomID == "" || messageID == "" || content == "" {
		return "", fmt.Errorf("invalid edit payload")
	}
	if len(content) > maxTextChars {
		content = content[:maxTextChars]
	}
	if editedAt.IsZero() {
		editedAt = time.Now().UTC()
	}

	createdAt, err := s.getMessageCreatedAt(ctx, roomID, messageID)
	if err != nil {
		return "", err
	}

	if s.Scylla == nil || s.Scylla.Session == nil {
		return "", fmt.Errorf("scylla session is not configured")
	}
	messagesTable := s.Scylla.Table("messages")

	selectQuery := fmt.Sprintf(
		`SELECT type, media_url, media_type, file_name FROM %s WHERE room_id = ? AND created_at = ? AND message_id = ? LIMIT 1`,
		messagesTable,
	)
	var (
		currentType      string
		currentMediaURL  string
		currentMediaType string
		currentFileName  string
	)
	if err := s.Scylla.Session.Query(selectQuery, roomID, createdAt, messageID).WithContext(ctx).Scan(
		&currentType,
		&currentMediaURL,
		&currentMediaType,
		&currentFileName,
	); err != nil {
		return "", fmt.Errorf("lookup message metadata: %w", err)
	}
	updatedType := "text"
	updatedMediaURL := ""
	updatedMediaType := ""
	updatedFileName := ""
	if strings.EqualFold(strings.TrimSpace(currentType), "task") {
		updatedType = "task"
		updatedMediaURL = currentMediaURL
		updatedMediaType = currentMediaType
		updatedFileName = currentFileName
	}

	updateQuery := fmt.Sprintf(
		`UPDATE %s SET content = ?, type = ?, media_url = ?, media_type = ?, file_name = ?, is_edited = ?, edited_at = ? WHERE room_id = ? AND created_at = ? AND message_id = ?`,
		messagesTable,
	)
	encryptedContent, encryptErr := security.EncryptMessage(content)
	if encryptErr != nil {
		return "", fmt.Errorf("encrypt message content: %w", encryptErr)
	}
	if err := safeExecScyllaQuery(
		s.Scylla.Session,
		updateQuery,
		encryptedContent,
		updatedType,
		updatedMediaURL,
		updatedMediaType,
		updatedFileName,
		true,
		editedAt,
		roomID,
		createdAt,
		messageID,
	); err != nil {
		return "", fmt.Errorf("update message content: %w", err)
	}

	if cacheErr := s.upsertCachedMessage(ctx, roomID, messageID, func(msg *models.Message) {
		msg.Content = content
		msg.Type = updatedType
		msg.MediaURL = updatedMediaURL
		msg.MediaType = updatedMediaType
		msg.FileName = updatedFileName
		msg.IsEdited = true
		editedCopy := editedAt
		msg.EditedAt = &editedCopy
	}); cacheErr != nil {
		log.Printf("[message-service] cache edit sync failed room=%s message=%s err=%v", roomID, messageID, cacheErr)
	}

	return updatedType, nil
}

func (s *MessageService) MarkMessageDeleted(ctx context.Context, roomID, messageID string, editedAt time.Time) error {
	roomID = normalizeRoomID(roomID)
	messageID = normalizeMessageID(messageID)
	if roomID == "" || messageID == "" {
		return fmt.Errorf("invalid delete payload")
	}
	if editedAt.IsZero() {
		editedAt = time.Now().UTC()
	}

	createdAt, err := s.getMessageCreatedAt(ctx, roomID, messageID)
	if err != nil {
		return err
	}

	if s.Scylla == nil || s.Scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	messagesTable := s.Scylla.Table("messages")
	updateQuery := fmt.Sprintf(
		`UPDATE %s SET content = ?, type = ?, media_url = ?, media_type = ?, file_name = ?, is_edited = ?, edited_at = ? WHERE room_id = ? AND created_at = ? AND message_id = ?`,
		messagesTable,
	)
	encryptedPlaceholder, encryptErr := security.EncryptMessage(DeletedMessagePlaceholder)
	if encryptErr != nil {
		return fmt.Errorf("encrypt deleted message placeholder: %w", encryptErr)
	}
	if err := safeExecScyllaQuery(
		s.Scylla.Session,
		updateQuery,
		encryptedPlaceholder,
		"deleted",
		"",
		"",
		"",
		false,
		editedAt,
		roomID,
		createdAt,
		messageID,
	); err != nil {
		return fmt.Errorf("mark message deleted: %w", err)
	}

	if cacheErr := s.upsertCachedMessage(ctx, roomID, messageID, func(msg *models.Message) {
		msg.Content = DeletedMessagePlaceholder
		msg.Type = "deleted"
		msg.MediaURL = ""
		msg.MediaType = ""
		msg.FileName = ""
		msg.IsEdited = false
		msg.EditedAt = nil
		msg.ReplyToMessageID = ""
		msg.ReplyToSnippet = ""
	}); cacheErr != nil {
		log.Printf("[message-service] cache delete sync failed room=%s message=%s err=%v", roomID, messageID, cacheErr)
	}

	return nil
}

func (s *MessageService) IsMessageOwnedBy(ctx context.Context, roomID, messageID, userID string) (bool, error) {
	roomID = normalizeRoomID(roomID)
	messageID = normalizeMessageID(messageID)
	userID = strings.TrimSpace(userID)
	if roomID == "" || messageID == "" || userID == "" {
		return false, fmt.Errorf("invalid ownership lookup payload")
	}
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return false, fmt.Errorf("scylla session is not configured")
	}

	messagesTable := s.Scylla.Table("messages")
	lookupQuery := fmt.Sprintf(
		`SELECT sender_id FROM %s WHERE room_id = ? AND message_id = ? LIMIT 1 ALLOW FILTERING`,
		messagesTable,
	)
	var senderID string
	if err := s.Scylla.Session.Query(lookupQuery, roomID, messageID).WithContext(ctx).Scan(&senderID); err != nil {
		return false, fmt.Errorf("lookup message owner: %w", err)
	}

	return strings.TrimSpace(senderID) == userID, nil
}

func (s *MessageService) GetMessageType(ctx context.Context, roomID, messageID string) (string, error) {
	roomID = normalizeRoomID(roomID)
	messageID = normalizeMessageID(messageID)
	if roomID == "" || messageID == "" {
		return "", fmt.Errorf("invalid message type lookup payload")
	}
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return "", fmt.Errorf("scylla session is not configured")
	}

	createdAt, err := s.getMessageCreatedAt(ctx, roomID, messageID)
	if err != nil {
		return "", err
	}

	messagesTable := s.Scylla.Table("messages")
	lookupQuery := fmt.Sprintf(
		`SELECT type FROM %s WHERE room_id = ? AND created_at = ? AND message_id = ? LIMIT 1`,
		messagesTable,
	)
	var messageType string
	if err := s.Scylla.Session.Query(lookupQuery, roomID, createdAt, messageID).WithContext(ctx).Scan(&messageType); err != nil {
		return "", fmt.Errorf("lookup message type: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(messageType)), nil
}

func (s *MessageService) getMessageCreatedAt(ctx context.Context, roomID, messageID string) (time.Time, error) {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return time.Time{}, fmt.Errorf("scylla session is not configured")
	}
	messagesTable := s.Scylla.Table("messages")
	lookupQuery := fmt.Sprintf(
		`SELECT created_at FROM %s WHERE room_id = ? AND message_id = ? LIMIT 1 ALLOW FILTERING`,
		messagesTable,
	)
	var createdAt time.Time
	if err := s.Scylla.Session.Query(lookupQuery, roomID, messageID).WithContext(ctx).Scan(&createdAt); err != nil {
		return time.Time{}, fmt.Errorf("lookup message created_at: %w", err)
	}
	if createdAt.IsZero() {
		return time.Time{}, fmt.Errorf("message not found")
	}
	return createdAt, nil
}

func (s *MessageService) CreatePinnedDiscussionComment(
	ctx context.Context,
	roomID, pinMessageID, parentCommentID, senderID, senderName, content string,
	createdAt time.Time,
) (models.Message, error) {
	roomID = normalizeRoomID(roomID)
	pinMessageID = normalizeMessageID(pinMessageID)
	parentCommentID = normalizeMessageID(parentCommentID)
	senderID = strings.TrimSpace(senderID)
	senderName = strings.TrimSpace(senderName)
	content = strings.TrimSpace(content)
	if senderName == "" {
		senderName = "Guest"
	}
	if roomID == "" || pinMessageID == "" || senderID == "" || content == "" {
		return models.Message{}, fmt.Errorf("invalid discussion comment payload")
	}
	if len(content) > maxTextChars {
		content = content[:maxTextChars]
	}
	if strings.TrimSpace(content) == "" {
		return models.Message{}, fmt.Errorf("discussion comment is empty")
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	createdAt = createdAt.UTC()

	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return models.Message{}, fmt.Errorf("scylla session is not configured")
	}
	commentID := generateDiscussionCommentID(createdAt)
	ttlSeconds := s.resolveRoomTTLSeconds(ctx, roomID)
	commentsTable := s.Scylla.Table("pin_discussion_comments")
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, pin_message_id, created_at, comment_id, parent_comment_id, sender_id, sender_name, content, is_edited, edited_at, is_deleted, is_pinned, pinned_by, pinned_by_name, pinned_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL %d`,
		commentsTable,
		ttlSeconds,
	)
	encryptedContent, encryptErr := security.EncryptMessage(content)
	if encryptErr != nil {
		return models.Message{}, fmt.Errorf("encrypt discussion comment content: %w", encryptErr)
	}
	if err := safeExecScyllaQuery(
		s.Scylla.Session,
		insertQuery,
		roomID,
		pinMessageID,
		createdAt,
		commentID,
		parentCommentID,
		senderID,
		senderName,
		encryptedContent,
		false,
		nil,
		false,
		false,
		"",
		"",
		nil,
	); err != nil {
		return models.Message{}, fmt.Errorf("save discussion comment: %w", err)
	}

	return models.Message{
		ID:               commentID,
		RoomID:           roomID,
		SenderID:         senderID,
		SenderName:       senderName,
		Content:          content,
		Type:             "text",
		ReplyToMessageID: parentCommentID,
		IsPinned:         false,
		CreatedAt:        createdAt,
		HasBreakRoom:     false,
		BreakJoinCount:   0,
	}, nil
}

func (s *MessageService) SetPinnedDiscussionComment(
	ctx context.Context,
	roomID, pinMessageID, commentID, pinnedBy, pinnedByName string,
	isPinned bool,
) (models.Message, error) {
	roomID = normalizeRoomID(roomID)
	pinMessageID = normalizeMessageID(pinMessageID)
	commentID = normalizeMessageID(commentID)
	pinnedBy = strings.TrimSpace(pinnedBy)
	pinnedByName = strings.TrimSpace(pinnedByName)
	if pinnedByName == "" {
		pinnedByName = "User"
	}
	if roomID == "" || pinMessageID == "" || commentID == "" {
		return models.Message{}, fmt.Errorf("invalid pin toggle payload")
	}
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return models.Message{}, fmt.Errorf("scylla session is not configured")
	}

	commentsTable := s.Scylla.Table("pin_discussion_comments")
	selectQuery := fmt.Sprintf(
		`SELECT created_at, parent_comment_id, sender_id, sender_name, content, is_edited, edited_at, is_deleted, is_pinned, pinned_by, pinned_by_name, pinned_at FROM %s WHERE room_id = ? AND pin_message_id = ? AND comment_id = ? LIMIT 1 ALLOW FILTERING`,
		commentsTable,
	)
	var (
		createdAt           time.Time
		parentCommentID     string
		senderID            string
		senderName          string
		content             string
		isEdited            bool
		editedAt            time.Time
		isDeleted           bool
		currentIsPinned     bool
		currentPinnedBy     string
		currentPinnedByName string
		currentPinnedAt     time.Time
	)
	if err := s.Scylla.Session.Query(selectQuery, roomID, pinMessageID, commentID).WithContext(ctx).Scan(
		&createdAt,
		&parentCommentID,
		&senderID,
		&senderName,
		&content,
		&isEdited,
		&editedAt,
		&isDeleted,
		&currentIsPinned,
		&currentPinnedBy,
		&currentPinnedByName,
		&currentPinnedAt,
	); err != nil {
		return models.Message{}, fmt.Errorf("lookup discussion comment pin state: %w", err)
	}
	if createdAt.IsZero() {
		return models.Message{}, gocql.ErrNotFound
	}

	if decrypted, decryptErr := security.DecryptMessage(content); decryptErr == nil {
		content = decrypted
	}

	nextPinnedBy := ""
	nextPinnedByName := ""
	nextPinnedAt := time.Time{}
	if isPinned {
		nextPinnedBy = pinnedBy
		nextPinnedByName = pinnedByName
		nextPinnedAt = time.Now().UTC()
	}
	updateQuery := fmt.Sprintf(
		`UPDATE %s SET is_pinned = ?, pinned_by = ?, pinned_by_name = ?, pinned_at = ? WHERE room_id = ? AND pin_message_id = ? AND created_at = ? AND comment_id = ?`,
		commentsTable,
	)
	var pinnedAtArg interface{}
	if nextPinnedAt.IsZero() {
		pinnedAtArg = nil
	} else {
		pinnedAtArg = nextPinnedAt
	}
	if err := safeExecScyllaQuery(
		s.Scylla.Session,
		updateQuery,
		isPinned,
		nextPinnedBy,
		nextPinnedByName,
		pinnedAtArg,
		roomID,
		pinMessageID,
		createdAt,
		commentID,
	); err != nil {
		return models.Message{}, fmt.Errorf("update discussion comment pin state: %w", err)
	}

	messageType := "text"
	finalContent := strings.TrimSpace(content)
	finalIsEdited := isEdited
	var editedAtPtr *time.Time
	if !editedAt.IsZero() {
		editedCopy := editedAt
		editedAtPtr = &editedCopy
	}
	if isDeleted {
		messageType = "deleted"
		finalContent = DeletedMessagePlaceholder
		finalIsEdited = false
	}

	return models.Message{
		ID:               commentID,
		RoomID:           roomID,
		SenderID:         senderID,
		SenderName:       senderName,
		Content:          finalContent,
		Type:             messageType,
		IsEdited:         finalIsEdited,
		EditedAt:         editedAtPtr,
		ReplyToMessageID: normalizeMessageID(parentCommentID),
		IsPinned:         isPinned,
		PinnedBy:         nextPinnedBy,
		PinnedByName:     nextPinnedByName,
		CreatedAt:        createdAt,
		HasBreakRoom:     false,
		BreakJoinCount:   0,
	}, nil
}

func generateDiscussionCommentID(now time.Time) string {
	nowUTC := now.UTC()
	return fmt.Sprintf("dcm_%d_%09d", nowUTC.UnixMilli(), nowUTC.UnixNano()%1_000_000_000)
}

func (s *MessageService) upsertCachedMessage(
	ctx context.Context,
	roomID, messageID string,
	mutate func(*models.Message),
) error {
	if s == nil || s.Redis == nil || s.Redis.Client == nil || roomID == "" || messageID == "" || mutate == nil {
		return nil
	}

	historyKey := roomHistoryPrefix + roomID
	entries, err := s.Redis.Client.LRange(ctx, historyKey, 0, -1).Result()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	updated := false
	serialized := make([]string, 0, len(entries))
	for _, raw := range entries {
		var msg models.Message
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			serialized = append(serialized, raw)
			continue
		}
		if !updated && normalizeMessageID(msg.ID) == messageID {
			mutate(&msg)
			updated = true
		}
		encoded, marshalErr := json.Marshal(msg)
		if marshalErr != nil {
			serialized = append(serialized, raw)
			continue
		}
		serialized = append(serialized, string(encoded))
	}

	if !updated {
		return nil
	}

	ttl := s.resolveRoomTTLSeconds(ctx, roomID)
	pipe := s.Redis.Client.TxPipeline()
	pipe.Del(ctx, historyKey)
	if len(serialized) > 0 {
		pipe.RPush(ctx, historyKey, serialized)
		pipe.LTrim(ctx, historyKey, -roomHistorySize, -1)
		pipe.Expire(ctx, historyKey, time.Duration(ttl)*time.Second)
	}
	_, execErr := pipe.Exec(ctx)
	return execErr
}
