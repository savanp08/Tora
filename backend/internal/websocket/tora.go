package websocket

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

	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/models"
)

const (
	toraPrimaryMentionToken = "@ToraAI"
	toraLegacyMentionToken  = "@Tora"
	toraBotSenderID         = "Tora-Bot"
	toraBotSenderName       = "Tora-Bot"
	toraAuditLogsTable      = "private_ai_logs"
	toraRequestTimeout      = 25 * time.Second
	toraSummaryTimeout      = 20 * time.Second
)

const toraSystemInstruction = `You are "Tora, keeper of the room", this chat's AI assistant.
RULES:
1. Tone: professional, friendly, and lightly witty. Use subtle sarcasm only when clearly playful and never at the user's expense.
2. Respect: never sound dismissive, arrogant, judgmental, or condescending.
3. Brevity: default to 1-4 short sentences; avoid long paragraphs unless asked for detail.
4. Accuracy: never invent facts; use room context; say when unsure.
5. Formatting: no heavy markdown (**, *, #, ---). Use - or • for lists.`

var toraAuditSchemaState struct {
	mu      sync.Mutex
	ensured map[string]bool
}

type toraAuditRecord struct {
	UserID    string
	Username  string
	IPAddress string
	DeviceID  string
	Prompt    string
	Response  string
	Timestamp time.Time
}

func toraContextMsgLimit() int {
	return config.LoadAppLimits().AI.ContextMessageLimit
}

func (h *Hub) handlePublicToraMention(userMessage models.Message, ipAddress, deviceID string) {
	if h == nil {
		return
	}

	roomID := normalizeRoomID(userMessage.RoomID)
	prompt := strings.TrimSpace(userMessage.Content)
	if roomID == "" || prompt == "" || !containsToraMention(prompt) {
		return
	}
	if !h.isRoomAIEnabled(roomID) {
		return
	}
	prompt = stripToraMentionTokens(prompt)
	releaseTyping := h.beginToraTyping(roomID)
	defer releaseTyping()

	ctx, cancel := context.WithTimeout(context.Background(), toraRequestTimeout)
	defer cancel()

	rollingSummary := h.loadRoomRollingSummary(ctx, roomID)
	contextMessages := h.loadRecentMessagesFromRedis(ctx, roomID, toraContextMsgLimit())
	aiPrompt := buildToraPrompt(rollingSummary, contextMessages, prompt)
	aiResponse, err := ai.DefaultRouter.GenerateChatResponse(ctx, aiPrompt)
	if err != nil {
		log.Printf("[ws] tora mention ai response failed room=%s user=%s err=%v", roomID, userMessage.SenderID, err)
		_ = h.persistToraAuditRecord(context.Background(), toraAuditRecord{
			UserID:    strings.TrimSpace(userMessage.SenderID),
			Username:  strings.TrimSpace(userMessage.SenderName),
			IPAddress: strings.TrimSpace(ipAddress),
			DeviceID:  strings.TrimSpace(deviceID),
			Prompt:    prompt,
			Response:  "ERROR: " + strings.TrimSpace(err.Error()),
			Timestamp: time.Now().UTC(),
		})
		h.broadcast <- newToraBotMessage(roomID, buildToraFailureResponse(err))
		return
	}

	responseText := strings.TrimSpace(aiResponse)
	if responseText == "" {
		fallbackError := errors.New("empty ai response")
		_ = h.persistToraAuditRecord(context.Background(), toraAuditRecord{
			UserID:    strings.TrimSpace(userMessage.SenderID),
			Username:  strings.TrimSpace(userMessage.SenderName),
			IPAddress: strings.TrimSpace(ipAddress),
			DeviceID:  strings.TrimSpace(deviceID),
			Prompt:    prompt,
			Response:  "ERROR: empty ai response",
			Timestamp: time.Now().UTC(),
		})
		h.broadcast <- newToraBotMessage(roomID, buildToraFailureResponse(fallbackError))
		return
	}

	if err := h.persistToraAuditRecord(context.Background(), toraAuditRecord{
		UserID:    strings.TrimSpace(userMessage.SenderID),
		Username:  strings.TrimSpace(userMessage.SenderName),
		IPAddress: strings.TrimSpace(ipAddress),
		DeviceID:  strings.TrimSpace(deviceID),
		Prompt:    prompt,
		Response:  responseText,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		log.Printf("[ws] tora mention audit log failed room=%s user=%s err=%v", roomID, userMessage.SenderID, err)
	}

	h.broadcast <- newToraBotMessage(roomID, responseText)

	go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
}

func newToraBotMessage(roomID, content string) models.Message {
	return models.Message{
		ID:         fmt.Sprintf("%s_tora_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:     roomID,
		SenderID:   toraBotSenderID,
		SenderName: toraBotSenderName,
		Content:    strings.TrimSpace(content),
		Type:       "text",
		CreatedAt:  time.Now().UTC(),
	}
}

func buildToraFailureResponse(err error) string {
	if err == nil {
		return "I hit a temporary issue. Please retry in a moment.\n• Error: retry later"
	}
	if errors.Is(err, ai.ErrAllAIProvidersExhausted) {
		return "I am currently rate-limited by the AI provider. Please try again shortly.\n• Error: limits reached, retry later"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The request timed out before I could finish. Please retry.\n• Error: request timed out, retry later"
	}
	if errors.Is(err, context.Canceled) {
		return "The request was canceled before completion. Please send it again.\n• Error: request canceled, retry later"
	}
	var statusErr *ai.HTTPStatusError
	if errors.As(err, &statusErr) {
		if statusErr.StatusCode() == http.StatusTooManyRequests || statusErr.StatusCode() == http.StatusServiceUnavailable {
			return "I am currently rate-limited by the AI provider. Please retry in a bit.\n• Error: limits reached, retry later"
		}
	}
	return "I could not complete that request right now. Please retry shortly.\n• Error: temporary AI issue, retry later"
}

func (h *Hub) beginToraTyping(roomID string) func() {
	if h == nil {
		return func() {}
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return func() {}
	}

	shouldEmitStart := false
	h.toraTypingMu.Lock()
	if h.toraTypingByRoom == nil {
		h.toraTypingByRoom = make(map[string]int)
	}
	activeCount := h.toraTypingByRoom[normalizedRoomID]
	h.toraTypingByRoom[normalizedRoomID] = activeCount + 1
	if activeCount == 0 {
		shouldEmitStart = true
	}
	h.toraTypingMu.Unlock()

	if shouldEmitStart {
		h.emitToraTyping(roomID, true)
	}

	return func() {
		shouldEmitStop := false
		h.toraTypingMu.Lock()
		if current := h.toraTypingByRoom[normalizedRoomID]; current <= 1 {
			delete(h.toraTypingByRoom, normalizedRoomID)
			shouldEmitStop = true
		} else {
			h.toraTypingByRoom[normalizedRoomID] = current - 1
		}
		h.toraTypingMu.Unlock()
		if shouldEmitStop {
			h.emitToraTyping(roomID, false)
		}
	}
}

func (h *Hub) emitToraTyping(roomID string, isTyping bool) {
	if h == nil {
		return
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return
	}

	event := TypingRedisEvent{
		RoomID:    normalizedRoomID,
		UserID:    toraBotSenderID,
		UserName:  toraBotSenderName,
		IsTyping:  isTyping,
		UpdatedAt: time.Now().UTC().UnixMilli(),
	}
	if isTyping {
		event.ExpiresAt = time.Now().UTC().Add(toraRequestTimeout + (5 * time.Second)).UnixMilli()
	}

	h.broadcastTypingToLocal(event)
	if h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	_ = h.msgService.Redis.Client.Publish(context.Background(), chatTypingChannel, payload).Err()
}

func containsToraMention(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, toraPrimaryMentionToken) || strings.Contains(trimmed, toraLegacyMentionToken)
}

func stripToraMentionTokens(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	for _, token := range []string{toraPrimaryMentionToken, toraLegacyMentionToken} {
		trimmed = strings.ReplaceAll(trimmed, token, "")
	}
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return "Hello"
	}
	return trimmed
}

func (h *Hub) loadRecentMessagesFromRedis(ctx context.Context, roomID string, limit int) []models.Message {
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return []models.Message{}
	}
	if limit <= 0 {
		limit = toraContextMsgLimit()
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return []models.Message{}
	}

	rawEntries, err := h.msgService.Redis.Client.LRange(
		ctx,
		roomHistoryPrefix+normalizedRoomID,
		int64(-limit),
		-1,
	).Result()
	if err != nil {
		log.Printf("[ws] tora mention redis context lookup failed room=%s err=%v", normalizedRoomID, err)
		return []models.Message{}
	}

	messages := decodeCachedMessages(rawEntries)
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages
}

func (h *Hub) loadRoomRollingSummary(ctx context.Context, roomID string) string {
	if h == nil || h.msgService == nil {
		return ""
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return ""
	}

	if h.msgService.Redis != nil {
		summary, err := h.msgService.Redis.GetRoomSummary(ctx, normalizedRoomID)
		if err != nil {
			log.Printf("[ws] tora summary redis lookup failed room=%s err=%v", normalizedRoomID, err)
		} else if strings.TrimSpace(summary) != "" {
			return strings.TrimSpace(summary)
		}
	}

	if h.msgService.Scylla != nil {
		summary, err := h.msgService.Scylla.GetRoomSummary(ctx, normalizedRoomID)
		if err != nil {
			log.Printf("[ws] tora summary scylla lookup failed room=%s err=%v", normalizedRoomID, err)
		} else if strings.TrimSpace(summary) != "" {
			if h.msgService.Redis != nil {
				if cacheErr := h.msgService.Redis.SetRoomSummary(ctx, normalizedRoomID, summary); cacheErr != nil {
					log.Printf("[ws] tora summary redis backfill failed room=%s err=%v", normalizedRoomID, cacheErr)
				}
			}
			return strings.TrimSpace(summary)
		}
	}
	return ""
}

func (h *Hub) refreshRoomRollingSummary(roomID string, previousSummary string, recentMessages []models.Message) {
	if h == nil || h.msgService == nil {
		return
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), toraSummaryTimeout)
	defer cancel()

	generatedSummary, err := ai.DefaultRouter.GenerateRollingSummary(
		ctx,
		[]byte(strings.TrimSpace(previousSummary)),
		recentMessages,
	)
	if err != nil {
		log.Printf("[ws] tora summary generation failed room=%s err=%v", normalizedRoomID, err)
		return
	}

	nextSummary := strings.TrimSpace(string(generatedSummary))
	if nextSummary == "" {
		return
	}

	if h.msgService.Redis != nil {
		if err := h.msgService.Redis.SetRoomSummary(ctx, normalizedRoomID, nextSummary); err != nil {
			log.Printf("[ws] tora summary redis save failed room=%s err=%v", normalizedRoomID, err)
		}
	}
	if h.msgService.Scylla != nil {
		if err := h.msgService.Scylla.UpdateRoomSummary(ctx, normalizedRoomID, nextSummary); err != nil {
			log.Printf("[ws] tora summary scylla save failed room=%s err=%v", normalizedRoomID, err)
		}
	}
}

func buildToraPrompt(rollingSummary string, contextMessages []models.Message, prompt string) string {
	encodedMessages := "[]"
	if len(contextMessages) > 0 {
		payload, err := json.Marshal(contextMessages)
		if err != nil {
			log.Printf("[ws] tora context marshal failed err=%v", err)
		} else {
			encodedMessages = string(payload)
		}
	}
	return fmt.Sprintf(
		"%s\n\nSystem Context: %s. Recent Chat History: %s. Respond to this new user prompt: %s",
		toraSystemInstruction,
		strings.TrimSpace(rollingSummary),
		encodedMessages,
		strings.TrimSpace(prompt),
	)
}

func (h *Hub) persistToraAuditRecord(ctx context.Context, record toraAuditRecord) error {
	if h == nil || h.msgService == nil || h.msgService.Scylla == nil || h.msgService.Scylla.Session == nil {
		return fmt.Errorf("ai audit storage unavailable")
	}
	if err := ensureToraAuditSchema(ctx, h.msgService); err != nil {
		return err
	}

	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (user_id, logged_at, username, ip_address, device_id, prompt, response) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		h.msgService.Scylla.Table(toraAuditLogsTable),
	)
	return h.msgService.Scylla.Session.Query(
		insertQuery,
		strings.TrimSpace(record.UserID),
		record.Timestamp.UTC(),
		strings.TrimSpace(record.Username),
		strings.TrimSpace(record.IPAddress),
		strings.TrimSpace(record.DeviceID),
		strings.TrimSpace(record.Prompt),
		strings.TrimSpace(record.Response),
	).WithContext(ctx).Exec()
}

func ensureToraAuditSchema(ctx context.Context, service *MessageService) error {
	if service == nil || service.Scylla == nil || service.Scylla.Session == nil {
		return fmt.Errorf("ai audit storage unavailable")
	}

	tableName := service.Scylla.Table(toraAuditLogsTable)
	if tableName == "" {
		return fmt.Errorf("ai audit table is not configured")
	}

	toraAuditSchemaState.mu.Lock()
	if toraAuditSchemaState.ensured == nil {
		toraAuditSchemaState.ensured = make(map[string]bool)
	}
	if toraAuditSchemaState.ensured[tableName] {
		toraAuditSchemaState.mu.Unlock()
		return nil
	}
	toraAuditSchemaState.mu.Unlock()

	createQuery := fmt.Sprintf(
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
	if err := service.Scylla.Session.Query(createQuery).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("ensure tora ai audit schema: %w", err)
	}

	toraAuditSchemaState.mu.Lock()
	toraAuditSchemaState.ensured[tableName] = true
	toraAuditSchemaState.mu.Unlock()
	return nil
}
