package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
)

const (
	privateAIChatLogsTableName = "private_ai_logs"
	privateAIRoomHistoryPrefix = "room:history:"
)

const privateAISystemInstruction = `You are "Tora, keeper of the room", this chat's AI assistant.
RULES:
1. Style: lazy + quirky, but subtle (max one short quirk line).
2. Brevity: default to 1-3 short sentences; avoid long paragraphs.
3. Accuracy: never invent facts; use room context; say when unsure.
4. Formatting: no heavy markdown (**, *, #, ---). Use - or • for lists.
5. Private mode: this response is only for this user.`

// DefaultAIRouter serves private chat requests using configured AI providers.
var DefaultAIRouter = ai.DefaultRouter

var privateAIChatAuditStore struct {
	mu     sync.RWMutex
	redis  *database.RedisStore
	scylla *database.ScyllaStore
}

var privateAIChatSchemaState struct {
	mu      sync.Mutex
	ensured map[string]bool
}

type privateAIChatRequest struct {
	Prompt   string `json:"prompt"`
	DeviceID string `json:"deviceId"`
	RoomID   string `json:"roomId"`
}

type privateAIChatResponse struct {
	Response string `json:"response"`
}

type privateAIChatAuditRecord struct {
	UserID    string
	Username  string
	IPAddress string
	DeviceID  string
	Prompt    string
	Response  string
	Timestamp time.Time
}

func privateAIContextMessageLimit() int {
	return config.LoadAppLimits().AI.ContextMessageLimit
}

func ConfigureAIChatPersistence(redisStore *database.RedisStore, scyllaStore *database.ScyllaStore) {
	privateAIChatAuditStore.mu.Lock()
	privateAIChatAuditStore.redis = redisStore
	privateAIChatAuditStore.scylla = scyllaStore
	privateAIChatAuditStore.mu.Unlock()
}

func HandlePrivateAIChat(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var req privateAIChatRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeAIChatError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeAIChatError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	roomID := resolvePrivateAIRoomID(req.RoomID, r)
	if roomID == "" {
		writeAIChatError(w, http.StatusBadRequest, "roomId is required")
		return
	}
	if deviceID == "" {
		writeAIChatError(w, http.StatusBadRequest, "deviceId is required")
		return
	}

	userID, username := extractAIChatIdentity(r)
	if userID == "" || username == "" {
		writeAIChatError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}
	roomAIEnabled, roomFeatureErr := isPrivateAIRoomEnabled(r.Context(), roomID)
	if roomFeatureErr != nil {
		writeAIChatError(w, http.StatusServiceUnavailable, "Unable to verify room AI settings")
		return
	}
	if !roomAIEnabled {
		writeAIChatError(w, http.StatusForbidden, "AI is disabled for this room")
		return
	}

	ipAddress := strings.TrimSpace(extractClientIP(r))
	if ipAddress == "" {
		ipAddress = "unknown"
	}

	if limitErr := enforcePrivateAIRequestLimits(r.Context(), userID, roomID, ipAddress, deviceID); limitErr != nil {
		var exceeded *privateAILimitExceededError
		if errors.As(limitErr, &exceeded) {
			writeAIChatError(w, http.StatusTooManyRequests, exceeded.PublicMessage())
			return
		}
		writeAIChatError(w, http.StatusServiceUnavailable, "AI limiter unavailable")
		return
	}

	aiPrompt := buildPrivateAIPromptWithRoomContext(r.Context(), roomID, prompt)
	responseText, err := DefaultAIRouter.GenerateChatResponse(r.Context(), aiPrompt)
	if err != nil {
		if errors.Is(err, ai.ErrAllAIProvidersExhausted) {
			writeAIChatError(w, http.StatusServiceUnavailable, "All AI providers exhausted")
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeAIChatError(w, http.StatusGatewayTimeout, "AI request timed out")
			return
		}
		writeAIChatError(w, http.StatusBadGateway, "Failed to generate AI response")
		return
	}

	record := privateAIChatAuditRecord{
		UserID:    userID,
		Username:  username,
		IPAddress: ipAddress,
		DeviceID:  deviceID,
		Prompt:    prompt,
		Response:  responseText,
		Timestamp: time.Now().UTC(),
	}
	if err := persistPrivateAIChatAuditRecord(r.Context(), record); err != nil {
		writeAIChatError(w, http.StatusInternalServerError, "Failed to audit AI interaction")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(privateAIChatResponse{
		Response: responseText,
	})
}

func extractAIChatIdentity(r *http.Request) (string, string) {
	if r == nil {
		return "", ""
	}
	ctx := r.Context()

	userID := normalizeIdentifier(firstNonEmpty(
		readContextString(ctx, "userId"),
		readContextString(ctx, "user_id"),
		readContextString(ctx, "uid"),
		readNestedContextUserValue(ctx, "userId"),
		readNestedContextUserValue(ctx, "user_id"),
		strings.TrimSpace(r.Header.Get("X-User-Id")),
		strings.TrimSpace(r.URL.Query().Get("userId")),
		strings.TrimSpace(r.URL.Query().Get("user_id")),
	))
	username := normalizeUsername(firstNonEmpty(
		readContextString(ctx, "username"),
		readContextString(ctx, "userName"),
		readContextString(ctx, "user_name"),
		readNestedContextUserValue(ctx, "username"),
		readNestedContextUserValue(ctx, "userName"),
		readNestedContextUserValue(ctx, "user_name"),
		strings.TrimSpace(r.Header.Get("X-Username")),
		strings.TrimSpace(r.URL.Query().Get("username")),
	))

	return userID, username
}

func readContextString(ctx context.Context, key string) string {
	if ctx == nil {
		return ""
	}
	value := ctx.Value(key)
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func readNestedContextUserValue(ctx context.Context, field string) string {
	if ctx == nil {
		return ""
	}
	for _, key := range []string{"user", "auth", "claims"} {
		raw := ctx.Value(key)
		if raw == nil {
			continue
		}
		switch typed := raw.(type) {
		case map[string]any:
			value, ok := typed[field]
			if !ok {
				continue
			}
			switch cast := value.(type) {
			case string:
				return strings.TrimSpace(cast)
			case fmt.Stringer:
				return strings.TrimSpace(cast.String())
			}
		case map[string]string:
			value, ok := typed[field]
			if ok {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func activePrivateAIChatScyllaStore() *database.ScyllaStore {
	privateAIChatAuditStore.mu.RLock()
	defer privateAIChatAuditStore.mu.RUnlock()
	return privateAIChatAuditStore.scylla
}

func activePrivateAIChatStores() (*database.RedisStore, *database.ScyllaStore) {
	privateAIChatAuditStore.mu.RLock()
	defer privateAIChatAuditStore.mu.RUnlock()
	return privateAIChatAuditStore.redis, privateAIChatAuditStore.scylla
}

func resolvePrivateAIRoomID(rawRoomID string, r *http.Request) string {
	if r == nil {
		return normalizeRoomID(rawRoomID)
	}
	return normalizeRoomID(firstNonEmpty(
		rawRoomID,
		strings.TrimSpace(r.Header.Get("X-Room-Id")),
		strings.TrimSpace(r.URL.Query().Get("roomId")),
		strings.TrimSpace(r.URL.Query().Get("room_id")),
	))
}

func isPrivateAIRoomEnabled(ctx context.Context, roomID string) (bool, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return true, nil
	}
	redisStore, _ := activePrivateAIChatStores()
	if redisStore == nil || redisStore.Client == nil {
		return true, nil
	}
	values, err := redisStore.Client.HMGet(
		ctx,
		roomKey(normalizedRoomID),
		"ai_enabled",
		"e2ee_enabled",
		"e2e_enabled",
	).Result()
	if err == redis.Nil {
		return roomDefaultAIEnabled, nil
	}
	if err != nil {
		return false, err
	}

	aiEnabled := roomDefaultAIEnabled
	e2eEnabled := roomDefaultE2EE
	if len(values) > 0 {
		aiEnabled = parseFlagString(toString(values[0]), roomDefaultAIEnabled)
	}
	if len(values) > 1 {
		rawE2E := strings.TrimSpace(toString(values[1]))
		if rawE2E == "" && len(values) > 2 {
			rawE2E = strings.TrimSpace(toString(values[2]))
		}
		e2eEnabled = parseFlagString(rawE2E, roomDefaultE2EE)
	}
	normalized := normalizeRoomFeatureFlags(aiEnabled, e2eEnabled)
	return normalized.AIEnabled, nil
}

func buildPrivateAIPromptWithRoomContext(ctx context.Context, roomID, prompt string) string {
	normalizedPrompt := strings.TrimSpace(prompt)
	normalizedRoomID := normalizeRoomID(roomID)
	rollingSummary := ""
	contextMessages := []models.Message{}
	if normalizedRoomID != "" {
		rollingSummary = loadPrivateAIRoomSummary(ctx, normalizedRoomID)
		contextMessages = loadPrivateAIRecentMessages(ctx, normalizedRoomID, privateAIContextMessageLimit())
	}

	encodedMessages := "[]"
	if len(contextMessages) > 0 {
		payload, err := json.Marshal(contextMessages)
		if err != nil {
			log.Printf("[private-ai] context marshal failed room=%s err=%v", normalizedRoomID, err)
		} else {
			encodedMessages = string(payload)
		}
	}

	if strings.TrimSpace(rollingSummary) == "" {
		rollingSummary = "No saved room summary available."
	}

	return fmt.Sprintf(
		"%s\n\nRoom ID: %s\nSystem Context: %s. Recent Chat History: %s. Respond to this new user prompt: %s",
		privateAISystemInstruction,
		normalizedRoomID,
		strings.TrimSpace(rollingSummary),
		encodedMessages,
		normalizedPrompt,
	)
}

func loadPrivateAIRoomSummary(ctx context.Context, roomID string) string {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}

	redisStore, scyllaStore := activePrivateAIChatStores()
	if redisStore != nil {
		summary, err := redisStore.GetRoomSummary(ctx, normalizedRoomID)
		if err != nil {
			log.Printf("[private-ai] redis summary lookup failed room=%s err=%v", normalizedRoomID, err)
		} else if strings.TrimSpace(summary) != "" {
			return strings.TrimSpace(summary)
		}
	}

	if scyllaStore != nil {
		summary, err := scyllaStore.GetRoomSummary(ctx, normalizedRoomID)
		if err != nil {
			log.Printf("[private-ai] scylla summary lookup failed room=%s err=%v", normalizedRoomID, err)
		} else if strings.TrimSpace(summary) != "" {
			if redisStore != nil {
				if cacheErr := redisStore.SetRoomSummary(ctx, normalizedRoomID, summary); cacheErr != nil {
					log.Printf("[private-ai] redis summary backfill failed room=%s err=%v", normalizedRoomID, cacheErr)
				}
			}
			return strings.TrimSpace(summary)
		}
	}
	return ""
}

func loadPrivateAIRecentMessages(ctx context.Context, roomID string, limit int) []models.Message {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return []models.Message{}
	}
	if limit <= 0 {
		limit = privateAIContextMessageLimit()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	redisStore, _ := activePrivateAIChatStores()
	if redisStore == nil || redisStore.Client == nil {
		return []models.Message{}
	}

	rawEntries, err := redisStore.Client.LRange(
		ctx,
		privateAIRoomHistoryPrefix+normalizedRoomID,
		int64(-limit),
		-1,
	).Result()
	if err != nil {
		log.Printf("[private-ai] redis message context lookup failed room=%s err=%v", normalizedRoomID, err)
		return []models.Message{}
	}

	messages := decodePrivateAICachedMessages(rawEntries, normalizedRoomID)
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages
}

func decodePrivateAICachedMessages(rawMessages []string, roomID string) []models.Message {
	if len(rawMessages) == 0 {
		return []models.Message{}
	}
	messages := make([]models.Message, 0, len(rawMessages))
	for _, raw := range rawMessages {
		var message models.Message
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			continue
		}

		if message.CreatedAt.IsZero() || strings.TrimSpace(message.SenderName) == "" {
			var legacy map[string]any
			if err := json.Unmarshal([]byte(raw), &legacy); err == nil {
				if strings.TrimSpace(message.SenderName) == "" {
					message.SenderName = strings.TrimSpace(firstNonEmpty(
						toString(legacy["senderName"]),
						toString(legacy["username"]),
						toString(legacy["senderId"]),
						toString(legacy["userId"]),
					))
				}
				if strings.TrimSpace(message.SenderID) == "" {
					message.SenderID = strings.TrimSpace(firstNonEmpty(
						toString(legacy["senderId"]),
						toString(legacy["userId"]),
					))
				}
				if strings.TrimSpace(message.Content) == "" {
					message.Content = strings.TrimSpace(firstNonEmpty(
						toString(legacy["content"]),
						toString(legacy["text"]),
						toString(legacy["message"]),
					))
				}
				if strings.TrimSpace(message.Type) == "" {
					message.Type = strings.TrimSpace(toString(legacy["type"]))
				}
				if message.CreatedAt.IsZero() {
					message.CreatedAt = parsePrivateAITime(
						firstNonNil(legacy["createdAt"], legacy["time"], legacy["timestamp"]),
					)
				}
			}
		}

		if strings.TrimSpace(message.RoomID) == "" {
			message.RoomID = roomID
		}
		if strings.TrimSpace(message.Type) == "" {
			message.Type = "text"
		}
		if strings.TrimSpace(message.SenderName) == "" {
			message.SenderName = "Unknown"
		}
		if strings.TrimSpace(message.Content) == "" && strings.TrimSpace(message.MediaURL) == "" {
			continue
		}
		messages = append(messages, message)
	}
	return messages
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func parsePrivateAITime(value any) time.Time {
	switch typed := value.(type) {
	case string:
		candidate := strings.TrimSpace(typed)
		if candidate == "" {
			return time.Time{}
		}
		if parsed, err := time.Parse(time.RFC3339Nano, candidate); err == nil {
			return parsed.UTC()
		}
		if parsed, err := time.Parse(time.RFC3339, candidate); err == nil {
			return parsed.UTC()
		}
	case float64:
		return time.Unix(int64(typed), 0).UTC()
	case int64:
		return time.Unix(typed, 0).UTC()
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return time.Unix(n, 0).UTC()
		}
	}
	return time.Time{}
}

func persistPrivateAIChatAuditRecord(ctx context.Context, record privateAIChatAuditRecord) error {
	store := activePrivateAIChatScyllaStore()
	if store == nil || store.Session == nil {
		return fmt.Errorf("ai audit storage unavailable")
	}
	if err := ensurePrivateAIChatAuditSchema(ctx, store); err != nil {
		return err
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (user_id, logged_at, username, ip_address, device_id, prompt, response) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		store.Table(privateAIChatLogsTableName),
	)
	return store.Session.Query(
		query,
		record.UserID,
		record.Timestamp.UTC(),
		record.Username,
		record.IPAddress,
		record.DeviceID,
		record.Prompt,
		record.Response,
	).WithContext(ctx).Exec()
}

func ensurePrivateAIChatAuditSchema(ctx context.Context, store *database.ScyllaStore) error {
	if store == nil || store.Session == nil {
		return fmt.Errorf("ai audit storage unavailable")
	}
	tableName := store.Table(privateAIChatLogsTableName)
	if tableName == "" {
		return fmt.Errorf("ai audit table is not configured")
	}

	privateAIChatSchemaState.mu.Lock()
	if privateAIChatSchemaState.ensured == nil {
		privateAIChatSchemaState.ensured = make(map[string]bool)
	}
	if privateAIChatSchemaState.ensured[tableName] {
		privateAIChatSchemaState.mu.Unlock()
		return nil
	}
	privateAIChatSchemaState.mu.Unlock()

	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			user_id text,
			logged_at timestamp,
			username text,
			ip_address text,
			device_id text,
			prompt text,
			response text,
			PRIMARY KEY (user_id, logged_at)
		) WITH CLUSTERING ORDER BY (logged_at DESC)`,
		tableName,
	)
	if err := store.Session.Query(query).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("ensure private ai log schema: %w", err)
	}

	privateAIChatSchemaState.mu.Lock()
	privateAIChatSchemaState.ensured[tableName] = true
	privateAIChatSchemaState.mu.Unlock()
	return nil
}

func writeAIChatError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": strings.TrimSpace(message),
	})
}
