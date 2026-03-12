package websocket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gocql/gocql"
	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/security"
)

const (
	messageQueueKey            = "msg_queue"
	roomHistoryPrefix          = "room:history:"
	roomTaskHistoryPrefix      = "room:history:task:"
	roomHistoryTTL             = 21600
	roomHistorySize            = 50
	roomTaskHistorySize        = 4
	roomHistoryBackfillLimit   = roomHistorySize * 5
	scyllaMessageTTL           = 15 * 24 * 60 * 60
	messageBreakMeta           = "message:break:"
	roomKeyPrefix              = "room:"
	roomMessageSoftExpiryTable = "room_message_soft_expiry"
	DeletedMessagePlaceholder  = "This message was deleted"
	boardDrawStartType         = "board_draw_start"
	boardCursorMoveType        = "board_cursor_move"
	boardClearType             = "board_clear"
	boardElementAddType        = "board_element_add"
	boardElementMoveType       = "board_element_move"
	boardElementDeleteType     = "board_element_delete"
	boardActivityType          = "board_activity"
	boardEventBatchType        = "board_event_batch"
	boardSizeTotalPrefix       = "board:size:total:"
	boardSizeElementsPrefix    = "board:size:elements:"
	boardSizeEntryTTL          = scyllaMessageTTL
	messageReactionsPrefix     = "message:reactions:"
	maxReactionEmojiBytes      = 32
)

var ErrBoardSizeLimitExceeded = errors.New("board storage limit exceeded")

func boardMaxStorageLimitBytes() int64 {
	return config.LoadAppLimits().Board.MaxStorageBytes
}

func wsMaxTextCharsLimit() int {
	return config.LoadAppLimits().WS.MaxTextChars
}

var supportedBoardEventTypes = map[string]struct{}{
	boardDrawStartType:     {},
	boardCursorMoveType:    {},
	boardClearType:         {},
	boardElementAddType:    {},
	boardElementMoveType:   {},
	boardElementDeleteType: {},
	boardActivityType:      {},
}

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

	ttlSeconds := scyllaMessageTTL
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
	historySize := roomHistorySize
	if isTaskHistoryMessage(msg) {
		historyKey = roomTaskHistoryPrefix + msg.RoomID
		historySize = roomTaskHistorySize
	}
	pipe := s.Redis.Client.TxPipeline()
	pipe.RPush(ctx, historyKey, payload)
	pipe.LTrim(ctx, historyKey, int64(-historySize), -1)
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

func isTaskHistoryMessage(msg models.Message) bool {
	return strings.EqualFold(strings.TrimSpace(msg.Type), "task")
}

func historyMessageKey(msg models.Message) string {
	normalizedID := normalizeMessageID(msg.ID)
	if normalizedID != "" {
		return normalizedID
	}
	return fmt.Sprintf(
		"%s|%s|%s|%d",
		strings.TrimSpace(msg.RoomID),
		strings.TrimSpace(msg.SenderID),
		strings.TrimSpace(msg.Content),
		msg.CreatedAt.UTC().UnixNano(),
	)
}

func historyMessageLess(left, right models.Message) bool {
	if left.CreatedAt.Equal(right.CreatedAt) {
		return historyMessageKey(left) < historyMessageKey(right)
	}
	return left.CreatedAt.Before(right.CreatedAt)
}

func trimHistoryBucket(messages []models.Message, limit int, includeTask bool) []models.Message {
	if limit <= 0 || len(messages) == 0 {
		return []models.Message{}
	}

	filtered := make([]models.Message, 0, len(messages))
	for _, message := range messages {
		if isTaskHistoryMessage(message) == includeTask {
			filtered = append(filtered, message)
		}
	}
	if len(filtered) == 0 {
		return []models.Message{}
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return historyMessageLess(filtered[i], filtered[j])
	})

	seen := make(map[string]struct{}, len(filtered))
	dedupedDescending := make([]models.Message, 0, len(filtered))
	for index := len(filtered) - 1; index >= 0; index-- {
		key := historyMessageKey(filtered[index])
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		dedupedDescending = append(dedupedDescending, filtered[index])
	}

	deduped := make([]models.Message, 0, len(dedupedDescending))
	for index := len(dedupedDescending) - 1; index >= 0; index-- {
		deduped = append(deduped, dedupedDescending[index])
	}
	if len(deduped) > limit {
		deduped = deduped[len(deduped)-limit:]
	}
	return deduped
}

func mergeRecentHistory(normalMessages, taskMessages []models.Message) []models.Message {
	merged := make([]models.Message, 0, len(normalMessages)+len(taskMessages))
	merged = append(merged, normalMessages...)
	merged = append(merged, taskMessages...)
	if len(merged) == 0 {
		return merged
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return historyMessageLess(merged[i], merged[j])
	})

	seen := make(map[string]struct{}, len(merged))
	deduped := make([]models.Message, 0, len(merged))
	for _, message := range merged {
		key := historyMessageKey(message)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, message)
	}
	return deduped
}

func isBoardEventType(eventType string) bool {
	normalizedType := strings.ToLower(strings.TrimSpace(eventType))
	_, ok := supportedBoardEventTypes[normalizedType]
	return ok
}

func (s *MessageService) UpsertBoardElement(ctx context.Context, element models.BoardElement) error {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	roomID := normalizeRoomID(element.RoomID)
	elementID := normalizeMessageID(element.ElementID)
	elementType := strings.TrimSpace(element.Type)
	if roomID == "" || elementID == "" || elementType == "" {
		return fmt.Errorf("invalid board element payload")
	}

	createdAt := element.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	normalizedElement := models.BoardElement{
		RoomID:          roomID,
		ElementID:       elementID,
		Type:            elementType,
		X:               element.X,
		Y:               element.Y,
		Width:           element.Width,
		Height:          element.Height,
		Content:         element.Content,
		ZIndex:          element.ZIndex,
		CreatedByUserID: normalizeUsername(element.CreatedByUserID),
		CreatedByName:   strings.TrimSpace(element.CreatedByName),
		CreatedAt:       createdAt,
	}

	existingSize, err := s.lookupBoardElementSizeBytes(ctx, roomID, elementID)
	if err != nil {
		return fmt.Errorf("resolve board element size: %w", err)
	}
	fallbackRoomTotal, err := s.resolveBoardRoomTotalFallbackBytes(ctx, roomID)
	if err != nil {
		return fmt.Errorf("resolve board room size: %w", err)
	}
	newSize := estimateBoardElementStorageBytes(normalizedElement)
	allowed, previousTrackedSize, _, err := s.reserveBoardStorageBytes(
		ctx,
		roomID,
		elementID,
		fallbackRoomTotal,
		existingSize,
		newSize,
	)
	if err != nil {
		return fmt.Errorf("reserve board storage: %w", err)
	}
	if !allowed {
		return fmt.Errorf(
			"%w: room=%s projected_bytes_exceed_limit=%d",
			ErrBoardSizeLimitExceeded,
			roomID,
			boardMaxStorageLimitBytes(),
		)
	}

	boardTable := s.Scylla.Table("board_elements")
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, element_id, type, x, y, width, height, content, z_index, created_by_user_id, created_by_name, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL %d`,
		boardTable,
		scyllaMessageTTL,
	)
	if err := s.Scylla.Session.Query(
		insertQuery,
		roomID,
		elementID,
		elementType,
		element.X,
		element.Y,
		element.Width,
		element.Height,
		element.Content,
		element.ZIndex,
		normalizedElement.CreatedByUserID,
		normalizedElement.CreatedByName,
		createdAt,
	).WithContext(ctx).Exec(); err != nil {
		if rollbackErr := s.rollbackBoardStorageBytes(
			ctx,
			roomID,
			elementID,
			newSize,
			previousTrackedSize,
		); rollbackErr != nil {
			log.Printf(
				"[message-service] board storage rollback failed room=%s element=%s err=%v",
				roomID,
				elementID,
				rollbackErr,
			)
		}
		return fmt.Errorf("save board element: %w", err)
	}

	return nil
}

func (s *MessageService) DeleteBoardElement(ctx context.Context, roomID, elementID string) error {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedElementID := normalizeMessageID(elementID)
	if normalizedRoomID == "" || normalizedElementID == "" {
		return fmt.Errorf("invalid board element identity")
	}
	existingSize, err := s.lookupBoardElementSizeBytes(ctx, normalizedRoomID, normalizedElementID)
	if err != nil {
		return fmt.Errorf("resolve board element size: %w", err)
	}

	boardTable := s.Scylla.Table("board_elements")
	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND element_id = ?`, boardTable)
	if err := s.Scylla.Session.Query(
		deleteQuery,
		normalizedRoomID,
		normalizedElementID,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("delete board element: %w", err)
	}
	if existingSize > 0 {
		fallbackRoomTotal, fallbackErr := s.resolveBoardRoomTotalFallbackBytes(ctx, normalizedRoomID)
		if fallbackErr == nil {
			if _, _, _, reserveErr := s.reserveBoardStorageBytes(
				ctx,
				normalizedRoomID,
				normalizedElementID,
				fallbackRoomTotal,
				existingSize,
				0,
			); reserveErr != nil {
				log.Printf(
					"[message-service] board storage delete reconcile failed room=%s element=%s err=%v",
					normalizedRoomID,
					normalizedElementID,
					reserveErr,
				)
			}
		} else {
			log.Printf(
				"[message-service] board storage fallback resolve failed room=%s err=%v",
				normalizedRoomID,
				fallbackErr,
			)
		}
	}

	return nil
}

func (s *MessageService) ClearBoardElements(ctx context.Context, roomID string) error {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return fmt.Errorf("invalid room id")
	}

	boardTable := s.Scylla.Table("board_elements")
	clearQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, boardTable)
	if err := s.Scylla.Session.Query(
		clearQuery,
		normalizedRoomID,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("clear board elements: %w", err)
	}

	if s.Redis != nil && s.Redis.Client != nil {
		if err := s.Redis.Client.Del(
			ctx,
			boardSizeTotalPrefix+normalizedRoomID,
			boardSizeElementsPrefix+normalizedRoomID,
		).Err(); err != nil {
			log.Printf("[message-service] board storage clear reconcile failed room=%s err=%v", normalizedRoomID, err)
		}
	}

	return nil
}

func (s *MessageService) LookupBoardElementCreator(
	ctx context.Context,
	roomID string,
	elementID string,
) (creatorUserID string, creatorName string, err error) {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return "", "", fmt.Errorf("scylla session is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedElementID := normalizeMessageID(elementID)
	if normalizedRoomID == "" || normalizedElementID == "" {
		return "", "", nil
	}
	boardTable := s.Scylla.Table("board_elements")
	query := fmt.Sprintf(
		`SELECT created_by_user_id, created_by_name FROM %s WHERE room_id = ? AND element_id = ? LIMIT 1`,
		boardTable,
	)
	var (
		rawCreatorID string
		rawCreator   string
	)
	scanErr := s.Scylla.Session.Query(
		query,
		normalizedRoomID,
		normalizedElementID,
	).WithContext(ctx).Scan(&rawCreatorID, &rawCreator)
	if scanErr != nil {
		if errors.Is(scanErr, gocql.ErrNotFound) {
			return "", "", nil
		}
		return "", "", scanErr
	}
	return normalizeUsername(rawCreatorID), strings.TrimSpace(rawCreator), nil
}

func (s *MessageService) IsRoomAdmin(ctx context.Context, roomID, userID string) (bool, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedUserID := normalizeUsername(userID)
	if normalizedRoomID == "" || normalizedUserID == "" {
		return false, nil
	}
	if s == nil || s.Redis == nil || s.Redis.Client == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	adminsKey := roomKeyPrefix + normalizedRoomID + ":admins"
	adminMembers, err := s.Redis.Client.SMembers(ctx, adminsKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}
	normalizedAdmins := make(map[string]struct{}, len(adminMembers))
	for _, rawAdmin := range adminMembers {
		adminID := normalizeUsername(rawAdmin)
		if adminID == "" {
			_ = s.Redis.Client.SRem(ctx, adminsKey, rawAdmin).Err()
			continue
		}
		normalizedAdmins[adminID] = struct{}{}
	}
	if len(normalizedAdmins) > 0 {
		_, ok := normalizedAdmins[normalizedUserID]
		return ok, nil
	}

	membersKey := roomKeyPrefix + normalizedRoomID + ":members"
	members, err := s.Redis.Client.SMembers(ctx, membersKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return false, err
	}
	if len(members) == 0 {
		return false, nil
	}

	memberJoinedKey := roomKeyPrefix + normalizedRoomID + ":member_joined_at"
	joinedAtMap, err := s.Redis.Client.HGetAll(ctx, memberJoinedKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}

	earliestUserID := ""
	earliestJoinedAt := int64(0)
	hasKnownJoinTimestamp := false
	fallbackUserID := ""
	for _, rawMember := range members {
		memberID := normalizeUsername(rawMember)
		if memberID == "" {
			continue
		}
		if fallbackUserID == "" || memberID < fallbackUserID {
			fallbackUserID = memberID
		}
		joinedAt := int64(0)
		if rawJoinedAt, ok := joinedAtMap[memberID]; ok {
			if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(rawJoinedAt), 10, 64); parseErr == nil {
				joinedAt = parsed
			}
		}
		if joinedAt <= 0 {
			// Missing join timestamps are treated as unknown. They should not outrank
			// members with a recorded join time.
			continue
		}
		if !hasKnownJoinTimestamp || earliestUserID == "" || joinedAt < earliestJoinedAt || (joinedAt == earliestJoinedAt && memberID < earliestUserID) {
			earliestUserID = memberID
			earliestJoinedAt = joinedAt
			hasKnownJoinTimestamp = true
		}
	}
	if hasKnownJoinTimestamp {
		return earliestUserID == normalizedUserID, nil
	}
	if fallbackUserID == "" {
		return false, nil
	}
	return fallbackUserID == normalizedUserID, nil
}

func estimateBoardElementStorageBytes(element models.BoardElement) int64 {
	normalized := map[string]interface{}{
		"roomId":          normalizeRoomID(element.RoomID),
		"elementId":       normalizeMessageID(element.ElementID),
		"type":            strings.TrimSpace(element.Type),
		"x":               element.X,
		"y":               element.Y,
		"width":           element.Width,
		"height":          element.Height,
		"content":         element.Content,
		"zIndex":          element.ZIndex,
		"createdByUserId": normalizeUsername(element.CreatedByUserID),
		"createdByName":   strings.TrimSpace(element.CreatedByName),
		"createdAt":       element.CreatedAt.UTC().UnixMilli(),
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return int64(len(element.Content)) + 384
	}
	estimated := int64(len(encoded)) + 192
	if estimated < 256 {
		return 256
	}
	return estimated
}

func (s *MessageService) lookupBoardElementSizeBytes(ctx context.Context, roomID, elementID string) (int64, error) {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return 0, nil
	}
	boardTable := s.Scylla.Table("board_elements")
	query := fmt.Sprintf(
		`SELECT type, x, y, width, height, content, z_index, created_by_user_id, created_by_name, created_at FROM %s WHERE room_id = ? AND element_id = ? LIMIT 1`,
		boardTable,
	)
	var (
		elementType string
		x           float32
		y           float32
		width       float32
		height      float32
		content     string
		zIndex      int
		createdByID string
		createdBy   string
		createdAt   time.Time
	)
	err := s.Scylla.Session.Query(
		query,
		roomID,
		elementID,
	).WithContext(ctx).Scan(
		&elementType,
		&x,
		&y,
		&width,
		&height,
		&content,
		&zIndex,
		&createdByID,
		&createdBy,
		&createdAt,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	element := models.BoardElement{
		RoomID:          roomID,
		ElementID:       elementID,
		Type:            elementType,
		X:               x,
		Y:               y,
		Width:           width,
		Height:          height,
		Content:         content,
		ZIndex:          zIndex,
		CreatedByUserID: createdByID,
		CreatedByName:   createdBy,
		CreatedAt:       createdAt,
	}
	return estimateBoardElementStorageBytes(element), nil
}

func (s *MessageService) resolveBoardRoomTotalFallbackBytes(ctx context.Context, roomID string) (int64, error) {
	if s == nil {
		return 0, nil
	}
	if s.Redis == nil || s.Redis.Client == nil {
		return s.calculateBoardRoomStorageBytes(ctx, roomID)
	}

	totalKey := boardSizeTotalPrefix + roomID
	exists, err := s.Redis.Client.Exists(ctx, totalKey).Result()
	if err != nil {
		return 0, err
	}
	if exists > 0 {
		return 0, nil
	}
	return s.calculateBoardRoomStorageBytes(ctx, roomID)
}

func (s *MessageService) calculateBoardRoomStorageBytes(ctx context.Context, roomID string) (int64, error) {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return 0, nil
	}
	boardTable := s.Scylla.Table("board_elements")
	query := fmt.Sprintf(
		`SELECT element_id, type, x, y, width, height, content, z_index, created_by_user_id, created_by_name, created_at FROM %s WHERE room_id = ?`,
		boardTable,
	)
	iter := s.Scylla.Session.Query(query, roomID).WithContext(ctx).Iter()
	var (
		elementID   string
		elementType string
		x           float32
		y           float32
		width       float32
		height      float32
		content     string
		zIndex      int
		createdByID string
		createdBy   string
		createdAt   time.Time
	)
	var total int64
	for iter.Scan(&elementID, &elementType, &x, &y, &width, &height, &content, &zIndex, &createdByID, &createdBy, &createdAt) {
		total += estimateBoardElementStorageBytes(models.BoardElement{
			RoomID:          roomID,
			ElementID:       elementID,
			Type:            elementType,
			X:               x,
			Y:               y,
			Width:           width,
			Height:          height,
			Content:         content,
			ZIndex:          zIndex,
			CreatedByUserID: createdByID,
			CreatedByName:   createdBy,
			CreatedAt:       createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *MessageService) reserveBoardStorageBytes(
	ctx context.Context,
	roomID string,
	elementID string,
	fallbackRoomTotal int64,
	fallbackElementBytes int64,
	newBytes int64,
) (allowed bool, previousTrackedBytes int64, projectedBytes int64, err error) {
	if newBytes < 0 {
		newBytes = 0
	}
	if fallbackRoomTotal < 0 {
		fallbackRoomTotal = 0
	}
	if fallbackElementBytes < 0 {
		fallbackElementBytes = 0
	}

	projectedWithoutRedis := fallbackRoomTotal - fallbackElementBytes + newBytes
	if projectedWithoutRedis < 0 {
		projectedWithoutRedis = 0
	}
	if s == nil || s.Redis == nil || s.Redis.Client == nil {
		if projectedWithoutRedis > boardMaxStorageLimitBytes() {
			return false, fallbackElementBytes, projectedWithoutRedis, nil
		}
		return true, fallbackElementBytes, projectedWithoutRedis, nil
	}

	totalKey := boardSizeTotalPrefix + roomID
	elementsKey := boardSizeElementsPrefix + roomID
	lua := `
local totalKey = KEYS[1]
local elementsKey = KEYS[2]
local elementId = ARGV[1]
local fallbackTotal = tonumber(ARGV[2]) or 0
local fallbackExisting = tonumber(ARGV[3]) or 0
local newSize = tonumber(ARGV[4]) or 0
local maxLimit = tonumber(ARGV[5]) or 0
local ttl = tonumber(ARGV[6]) or 0

local currentTotal = tonumber(redis.call('GET', totalKey))
if currentTotal == nil then
	currentTotal = fallbackTotal
end
if currentTotal < 0 then
	currentTotal = 0
end

local existingSize = tonumber(redis.call('HGET', elementsKey, elementId))
if existingSize == nil then
	existingSize = fallbackExisting
end
if existingSize < 0 then
	existingSize = 0
end

local projected = currentTotal - existingSize + newSize
if projected < 0 then
	projected = 0
end
if projected > maxLimit then
	return {0, existingSize, projected}
end

redis.call('SET', totalKey, projected, 'EX', ttl)
if newSize > 0 then
	redis.call('HSET', elementsKey, elementId, newSize)
else
	redis.call('HDEL', elementsKey, elementId)
end
redis.call('EXPIRE', elementsKey, ttl)
return {1, existingSize, projected}
`
	result, evalErr := s.Redis.Client.Eval(
		ctx,
		lua,
		[]string{totalKey, elementsKey},
		elementID,
		fallbackRoomTotal,
		fallbackElementBytes,
		newBytes,
		boardMaxStorageLimitBytes(),
		boardSizeEntryTTL,
	).Result()
	if evalErr != nil {
		if projectedWithoutRedis > boardMaxStorageLimitBytes() {
			return false, fallbackElementBytes, projectedWithoutRedis, nil
		}
		return true, fallbackElementBytes, projectedWithoutRedis, nil
	}

	parts, ok := result.([]interface{})
	if !ok || len(parts) < 3 {
		return false, fallbackElementBytes, projectedWithoutRedis, fmt.Errorf("invalid board storage response")
	}
	allowValue, parseErr := toInt64(parts[0])
	if parseErr != nil {
		return false, fallbackElementBytes, projectedWithoutRedis, parseErr
	}
	previousValue, parseErr := toInt64(parts[1])
	if parseErr != nil {
		return false, fallbackElementBytes, projectedWithoutRedis, parseErr
	}
	projectedValue, parseErr := toInt64(parts[2])
	if parseErr != nil {
		return false, fallbackElementBytes, projectedWithoutRedis, parseErr
	}
	return allowValue == 1, previousValue, projectedValue, nil
}

func (s *MessageService) rollbackBoardStorageBytes(
	ctx context.Context,
	roomID string,
	elementID string,
	appliedBytes int64,
	previousBytes int64,
) error {
	if s == nil || s.Redis == nil || s.Redis.Client == nil {
		return nil
	}
	fallbackRoomTotal, err := s.resolveBoardRoomTotalFallbackBytes(ctx, roomID)
	if err != nil {
		return err
	}
	_, _, _, err = s.reserveBoardStorageBytes(
		ctx,
		roomID,
		elementID,
		fallbackRoomTotal,
		appliedBytes,
		previousBytes,
	)
	return err
}

func toInt64(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int32:
		return int64(typed), nil
	case int:
		return int64(typed), nil
	case uint64:
		return int64(typed), nil
	case float64:
		return int64(typed), nil
	case []byte:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unexpected int64 value type %T", value)
	}
}

func normalizeReactionEmoji(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > maxReactionEmojiBytes {
		return ""
	}
	if utf8.RuneCountInString(trimmed) > 8 {
		return ""
	}
	return trimmed
}

func messageReactionsKey(roomID, messageID string) string {
	return messageReactionsPrefix + roomID + ":" + messageID
}

func encodeReactionHashField(userID, emoji string) string {
	return userID + "|" + base64.RawURLEncoding.EncodeToString([]byte(emoji))
}

func decodeReactionHashField(field string) (string, string, bool) {
	separator := strings.Index(field, "|")
	if separator <= 0 || separator >= len(field)-1 {
		return "", "", false
	}
	userID := normalizeUsername(field[:separator])
	emojiBytes, err := base64.RawURLEncoding.DecodeString(field[separator+1:])
	if err != nil {
		return "", "", false
	}
	emoji := normalizeReactionEmoji(string(emojiBytes))
	if userID == "" || emoji == "" {
		return "", "", false
	}
	return userID, emoji, true
}

func decodeMessageReactionUsers(raw map[string]string) map[string][]string {
	if len(raw) == 0 {
		return map[string][]string{}
	}

	result := make(map[string][]string)
	seen := make(map[string]map[string]struct{})
	for field := range raw {
		userID, emoji, ok := decodeReactionHashField(field)
		if !ok {
			continue
		}
		emojiSeen, exists := seen[emoji]
		if !exists {
			emojiSeen = make(map[string]struct{})
			seen[emoji] = emojiSeen
		}
		if _, duplicate := emojiSeen[userID]; duplicate {
			continue
		}
		emojiSeen[userID] = struct{}{}
		result[emoji] = append(result[emoji], userID)
	}

	for emoji := range result {
		sort.Strings(result[emoji])
	}
	return result
}

func (s *MessageService) GetRecentMessages(ctx context.Context, roomID string) ([]models.Message, error) {
	if roomID == "" {
		return []models.Message{}, nil
	}

	normalMessages := make([]models.Message, 0, roomHistorySize)
	taskMessages := make([]models.Message, 0, roomTaskHistorySize)
	if s.Redis != nil && s.Redis.Client != nil {
		cachedNormal, err := s.Redis.Client.LRange(ctx, roomHistoryPrefix+roomID, 0, -1).Result()
		if err != nil {
			return nil, fmt.Errorf("load cached normal history: %w", err)
		}
		cachedTasks, taskErr := s.Redis.Client.LRange(ctx, roomTaskHistoryPrefix+roomID, 0, -1).Result()
		if taskErr != nil {
			return nil, fmt.Errorf("load cached task history: %w", taskErr)
		}

		normalMessages = trimHistoryBucket(decodeCachedMessages(cachedNormal), roomHistorySize, false)
		taskMessages = trimHistoryBucket(decodeCachedMessages(cachedTasks), roomTaskHistorySize, true)
	}

	if s.Scylla == nil || s.Scylla.Session == nil {
		combined := mergeRecentHistory(normalMessages, taskMessages)
		s.enrichBreakMetadata(ctx, combined)
		s.enrichPinMetadata(ctx, roomID, combined)
		s.enrichReactionMetadata(ctx, roomID, combined)
		return combined, nil
	}

	if len(normalMessages) < roomHistorySize || len(taskMessages) < roomTaskHistorySize {
		scyllaMessagesDesc, err := s.queryScyllaMessages(roomID, roomHistoryBackfillLimit, nil)
		if err != nil {
			return nil, err
		}
		normalMessages = trimHistoryBucket(append(normalMessages, scyllaMessagesDesc...), roomHistorySize, false)
		taskMessages = trimHistoryBucket(append(taskMessages, scyllaMessagesDesc...), roomTaskHistorySize, true)
	}

	combined := mergeRecentHistory(normalMessages, taskMessages)
	s.enrichBreakMetadata(ctx, combined)
	s.enrichPinMetadata(ctx, roomID, combined)
	s.enrichReactionMetadata(ctx, roomID, combined)

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
	boardElementsTable := s.Scylla.Table("board_elements")
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
	boardElementsQuery := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text,
			element_id text,
			type text,
			x float,
			y float,
			width float,
			height float,
			content text,
			z_index int,
			created_by_user_id text,
			created_by_name text,
			created_at timestamp,
			PRIMARY KEY (room_id, element_id)
		)`,
		boardElementsTable,
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
	lastErr = nil
	for attempt := 1; attempt <= 3; attempt++ {
		if err := safeExecScyllaQuery(s.Scylla.Session, boardElementsQuery); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
	}
	if lastErr != nil {
		log.Printf("[message-service] ensure board schema failed: %v", lastErr)
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
	boardAlterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD created_by_user_id text`, boardElementsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD created_by_name text`, boardElementsTable),
	}
	for _, alterQuery := range boardAlterQueries {
		if err := safeExecScyllaQuery(s.Scylla.Session, alterQuery); err != nil && !isSchemaAlreadyApplied(err) {
			log.Printf("[message-service] ensure board schema alter failed: %v", err)
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

func (s *MessageService) enrichReactionMetadata(ctx context.Context, roomID string, messages []models.Message) {
	if len(messages) == 0 {
		return
	}
	reactionsByMessageID, err := s.GetRoomMessageReactions(ctx, roomID, collectMessageIDs(messages))
	if err != nil {
		return
	}
	if len(reactionsByMessageID) == 0 {
		return
	}
	for index, message := range messages {
		normalizedMessageID := normalizeMessageID(message.ID)
		if normalizedMessageID == "" {
			continue
		}
		reactions := reactionsByMessageID[normalizedMessageID]
		if len(reactions) == 0 {
			continue
		}
		messages[index].Reactions = reactions
	}
}

func collectMessageIDs(messages []models.Message) []string {
	if len(messages) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(messages))
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		normalizedID := normalizeMessageID(message.ID)
		if normalizedID == "" {
			continue
		}
		if _, exists := seen[normalizedID]; exists {
			continue
		}
		seen[normalizedID] = struct{}{}
		ids = append(ids, normalizedID)
	}
	return ids
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

func (s *MessageService) ToggleMessageReaction(
	ctx context.Context,
	roomID,
	messageID,
	userID,
	emoji string,
) (map[string][]string, bool, error) {
	roomID = normalizeRoomID(roomID)
	messageID = normalizeMessageID(messageID)
	userID = normalizeUsername(userID)
	emoji = normalizeReactionEmoji(emoji)
	if roomID == "" || messageID == "" || userID == "" || emoji == "" {
		return nil, false, fmt.Errorf("invalid reaction payload")
	}
	if s == nil || s.Redis == nil || s.Redis.Client == nil {
		return nil, false, fmt.Errorf("redis client is not configured")
	}

	key := messageReactionsKey(roomID, messageID)
	field := encodeReactionHashField(userID, emoji)
	exists, err := s.Redis.Client.HExists(ctx, key, field).Result()
	if err != nil {
		return nil, false, fmt.Errorf("lookup existing message reaction: %w", err)
	}

	added := !exists
	pipe := s.Redis.Client.TxPipeline()
	if exists {
		pipe.HDel(ctx, key, field)
	} else {
		pipe.HSet(ctx, key, field, "1")
	}
	pipe.Expire(ctx, key, time.Duration(s.resolveRoomTTLSeconds(ctx, roomID))*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, false, fmt.Errorf("toggle message reaction: %w", err)
	}

	reactions, err := s.GetRoomMessageReactions(ctx, roomID, []string{messageID})
	if err != nil {
		return nil, false, err
	}
	return reactions[messageID], added, nil
}

func (s *MessageService) GetRoomMessageReactions(
	ctx context.Context,
	roomID string,
	messageIDs []string,
) (map[string]map[string][]string, error) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" || len(messageIDs) == 0 {
		return map[string]map[string][]string{}, nil
	}
	if s == nil || s.Redis == nil || s.Redis.Client == nil {
		return map[string]map[string][]string{}, nil
	}

	normalizedMessageIDs := make([]string, 0, len(messageIDs))
	seen := make(map[string]struct{}, len(messageIDs))
	for _, rawMessageID := range messageIDs {
		normalizedMessageID := normalizeMessageID(rawMessageID)
		if normalizedMessageID == "" {
			continue
		}
		if _, exists := seen[normalizedMessageID]; exists {
			continue
		}
		seen[normalizedMessageID] = struct{}{}
		normalizedMessageIDs = append(normalizedMessageIDs, normalizedMessageID)
	}
	if len(normalizedMessageIDs) == 0 {
		return map[string]map[string][]string{}, nil
	}

	pipe := s.Redis.Client.Pipeline()
	cmds := make(map[string]*redis.MapStringStringCmd, len(normalizedMessageIDs))
	for _, normalizedMessageID := range normalizedMessageIDs {
		cmds[normalizedMessageID] = pipe.HGetAll(ctx, messageReactionsKey(roomID, normalizedMessageID))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("load message reactions: %w", err)
	}

	reactionsByMessageID := make(map[string]map[string][]string, len(normalizedMessageIDs))
	for messageID, cmd := range cmds {
		if cmd == nil {
			continue
		}
		entries, resultErr := cmd.Result()
		if resultErr != nil && !errors.Is(resultErr, redis.Nil) {
			return nil, fmt.Errorf("read message reactions: %w", resultErr)
		}
		reactions := decodeMessageReactionUsers(entries)
		if len(reactions) > 0 {
			reactionsByMessageID[messageID] = reactions
		}
	}
	return reactionsByMessageID, nil
}

func (s *MessageService) UpdateMessageContent(ctx context.Context, roomID, messageID, content string, editedAt time.Time) (string, error) {
	roomID = normalizeRoomID(roomID)
	messageID = normalizeMessageID(messageID)
	content = strings.TrimSpace(content)
	if roomID == "" || messageID == "" || content == "" {
		return "", fmt.Errorf("invalid edit payload")
	}
	if len(content) > wsMaxTextCharsLimit() {
		content = content[:wsMaxTextCharsLimit()]
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
		msg.Reactions = map[string][]string{}
		msg.IsEdited = false
		msg.EditedAt = nil
		msg.ReplyToMessageID = ""
		msg.ReplyToSnippet = ""
	}); cacheErr != nil {
		log.Printf("[message-service] cache delete sync failed room=%s message=%s err=%v", roomID, messageID, cacheErr)
	}
	if s.Redis != nil && s.Redis.Client != nil {
		if err := s.Redis.Client.Del(ctx, messageReactionsKey(roomID, messageID)).Err(); err != nil {
			log.Printf("[message-service] reaction cleanup failed room=%s message=%s err=%v", roomID, messageID, err)
		}
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
	if len(content) > wsMaxTextCharsLimit() {
		content = content[:wsMaxTextCharsLimit()]
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
	ttlSeconds := scyllaMessageTTL
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

	_, normalErr := s.upsertCachedMessageInKey(
		ctx,
		roomID,
		roomHistoryPrefix+roomID,
		roomHistorySize,
		messageID,
		mutate,
	)
	if normalErr != nil {
		return normalErr
	}
	_, taskErr := s.upsertCachedMessageInKey(
		ctx,
		roomID,
		roomTaskHistoryPrefix+roomID,
		roomTaskHistorySize,
		messageID,
		mutate,
	)
	return taskErr
}

func (s *MessageService) upsertCachedMessageInKey(
	ctx context.Context,
	roomID, historyKey string,
	historyLimit int,
	messageID string,
	mutate func(*models.Message),
) (bool, error) {
	entries, err := s.Redis.Client.LRange(ctx, historyKey, 0, -1).Result()
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}

	normalizedMessageID := normalizeMessageID(messageID)
	updated := false
	serialized := make([]string, 0, len(entries))
	for _, raw := range entries {
		var msg models.Message
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			serialized = append(serialized, raw)
			continue
		}
		if !updated && normalizeMessageID(msg.ID) == normalizedMessageID {
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
		return false, nil
	}

	ttl := s.resolveRoomTTLSeconds(ctx, roomID)
	pipe := s.Redis.Client.TxPipeline()
	pipe.Del(ctx, historyKey)
	if len(serialized) > 0 {
		pipe.RPush(ctx, historyKey, serialized)
		pipe.LTrim(ctx, historyKey, int64(-historyLimit), -1)
		pipe.Expire(ctx, historyKey, time.Duration(ttl)*time.Second)
	}
	_, execErr := pipe.Exec(ctx)
	if execErr != nil {
		return false, execErr
	}
	return true, nil
}
