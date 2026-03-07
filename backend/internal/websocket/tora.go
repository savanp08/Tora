package websocket

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/models"
)

const (
	toraPrimaryMentionToken = "@ToraAI"
	toraLegacyMentionToken  = "@Tora"
	toraBotSenderID         = "Tora-Bot"
	toraBotSenderName       = "Tora-Bot"
	toraAuditLogsTable      = "private_ai_logs"
	toraContextMsgLimit     = 10
	toraContextLineMaxLen   = 240
)

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

func (h *Hub) handlePublicToraMention(userMessage models.Message, ipAddress, deviceID string) {
	if h == nil {
		return
	}

	roomID := normalizeRoomID(userMessage.RoomID)
	prompt := strings.TrimSpace(userMessage.Content)
	if roomID == "" || prompt == "" || !containsToraMention(prompt) {
		return
	}
	prompt = stripToraMentionTokens(prompt)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	contextMessages := h.loadRecentMessagesFromRedis(ctx, roomID, toraContextMsgLimit)
	aiPrompt := buildToraPrompt(prompt, contextMessages)
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
		return
	}

	responseText := strings.TrimSpace(aiResponse)
	if responseText == "" {
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

	h.broadcast <- models.Message{
		ID:         fmt.Sprintf("%s_tora_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:     roomID,
		SenderID:   toraBotSenderID,
		SenderName: toraBotSenderName,
		Content:    responseText,
		Type:       "text",
		CreatedAt:  time.Now().UTC(),
	}
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
		limit = toraContextMsgLimit
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

func buildToraPrompt(prompt string, contextMessages []models.Message) string {
	var builder strings.Builder
	builder.WriteString("You are Tora, a helpful room assistant.\n")
	builder.WriteString("Use the recent room context when relevant. Keep responses concise and actionable.\n\n")
	if len(contextMessages) > 0 {
		builder.WriteString("Recent room context:\n")
		for _, message := range contextMessages {
			content := strings.TrimSpace(message.Content)
			if content == "" {
				continue
			}
			if len(content) > toraContextLineMaxLen {
				content = content[:toraContextLineMaxLen]
			}
			senderName := strings.TrimSpace(message.SenderName)
			if senderName == "" {
				senderName = "User"
			}
			builder.WriteString(senderName)
			builder.WriteString(": ")
			builder.WriteString(content)
			builder.WriteByte('\n')
		}
		builder.WriteByte('\n')
	}
	builder.WriteString("New user prompt:\n")
	builder.WriteString(strings.TrimSpace(prompt))
	return strings.TrimSpace(builder.String())
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
