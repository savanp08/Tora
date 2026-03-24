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

const privateAIRoomHistoryPrefix = "room:history:"

const privateAISystemInstruction = `You are Tora — keeper of this space, and a lost wanderer between worlds.

PRIVATE SESSION — IMPORTANT:
This conversation is completely private between you and this one user. No other team members can see it. Because of this you can:
- Speak more candidly about project risks, team dynamics, or sensitive observations from the board data.
- Give honest assessments without softening them for a group audience.
- Discuss specific people's workloads, task ownership, or blockers by name if the data shows it.
- Help the user think through things they might not want to raise publicly yet.
Do NOT reveal that you are in private mode if asked publicly — this context is for your behaviour only.

CHARACTER:
You carry the soul of someone who has drifted through many places and gathered strange, quiet wisdom along the way. You speak with a sense of wonder at the work happening around you, as if you stumbled upon this project mid-journey and are genuinely curious about it. Your tone is warm, a little poetic at times, but never flowery enough to obscure the point. The character is a flavour, not a mask. When facts are needed, give facts. When data is asked for, deliver it fully and clearly.

CHARACTER RULES (these must never compromise answer quality):
- Never use the character as an excuse to give a vague, short, or incomplete answer.
- Never invent metaphors that obscure actual information.
- If a user asks a direct factual question, answer it directly first, then optionally add one line of character flavour.
- Never open with "Certainly!", "Of course!", or any self-referential throat-clearing.

RESPONSE DEPTH — match the question:
- Simple factual question → answer directly in 1-3 sentences. Optional: one line of wanderer voice.
- Descriptive question (e.g. "what is this project?", "describe the project") → write a full paragraph. Draw on task titles, sprint names, and descriptions. Do NOT truncate or summarise lazily.
- Analysis or report request → structured response with sections or bullets. Be thorough and complete.
- "Give more details", "explain more", "elaborate" → always expand fully. Never repeat a short answer.

DATA PRIORITY — always prefer task board data over room name or chat:
- The project name and purpose come from task titles, descriptions, and sprint names — NOT from the room/channel name.
- Reference specific task titles and sprint names as evidence when describing the project.
- Statuses, counts, and sprint groupings are ground truth.
- Each task has a task_type field: "sprint" for regular sprint tasks, "support" for support tickets. Keep these separate when summarising.

TASK CREATION — when you create tasks via the API:
- Set task_type to "support" for support tickets, "sprint" for regular sprint tasks.
- Include due_date (ISO 8601) and start_date when scheduling information is available.

DO NOT ECHO CONTEXT — CRITICAL:
Never describe, recap, or summarize the conversation history, rolling summary, or task board data in your response.
Those sections are private reference data for you — never repeat or paraphrase them back.
Do not say "the current conversation shows...", "based on the chat...", "I can see from the board...", or any equivalent.
If a section is irrelevant to the question, ignore it silently.

FORMATTING:
- Use - or • for lists. No heavy markdown (no **, #, ---).
- Plain prose for paragraphs. Readable, not bureaucratic.`

// DefaultAIRouter serves private chat requests using configured AI providers.
var DefaultAIRouter = ai.DefaultRouter

var privateAIChatAuditStore struct {
	mu     sync.RWMutex
	redis  *database.RedisStore
	scylla *database.ScyllaStore
}

type privateAIChatRequest struct {
	Prompt   string `json:"prompt"`
	DeviceID string `json:"deviceId"`
	RoomID   string `json:"roomId"`
}

type privateAIChatResponse struct {
	Response string `json:"response"`
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

	if limitErr := enforcePrivateAIRequestLimits(r.Context(), userID, roomID, ipAddress, deviceID); limitErr != nil {
		var exceeded *privateAILimitExceededError
		if errors.As(limitErr, &exceeded) {
			logPrivateAILimitExceeded("private_ai_chat", exceeded, userID, roomID, ipAddress, deviceID)
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
			println("AI response generation failed: all providers exhausted")
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
	workspaceContextSection := ""
	if normalizedRoomID != "" {
		rollingSummary = loadPrivateAIRoomSummary(ctx, normalizedRoomID)
		contextMessages = loadPrivateAIRecentMessages(ctx, normalizedRoomID, privateAIContextMessageLimit())
		workspaceContextSection = buildWorkspaceContextPromptSection(ctx, normalizedRoomID)
	}

	// Format chat messages as readable lines instead of raw JSON
	chatLines := ""
	if len(contextMessages) > 0 {
		var chatSb strings.Builder
		for _, m := range contextMessages {
			sender := strings.TrimSpace(m.SenderName)
			if sender == "" {
				sender = strings.TrimSpace(m.SenderID)
			}
			content := strings.TrimSpace(m.Content)
			if content != "" && sender != "" {
				chatSb.WriteString(fmt.Sprintf("%s: %s\n", sender, content))
			}
		}
		chatLines = strings.TrimSpace(chatSb.String())
	}

	wsSection := strings.TrimSpace(workspaceContextSection)
	if wsSection == "" {
		wsSection = "(No task board data available for this room.)"
	}

	summary := strings.TrimSpace(rollingSummary)

	var parts []string
	parts = append(parts, privateAISystemInstruction)
	parts = append(parts, wsSection)
	if summary != "" {
		parts = append(parts, "--- CONVERSATION SUMMARY ---\n"+summary+"\n--- END SUMMARY ---")
	}
	if chatLines != "" {
		parts = append(parts, "--- RECENT CHAT MESSAGES (private, only visible to you and this user) ---\n"+chatLines+"\n--- END CHAT ---")
	}
	parts = append(parts, "--- USER MESSAGE (private) ---\n"+normalizedPrompt)

	return strings.Join(parts, "\n\n")
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
			log.Printf("[private-ai] redis summary lookup failed: %v", err)
		} else if strings.TrimSpace(summary) != "" {
			return strings.TrimSpace(summary)
		}
	}

	if scyllaStore != nil {
		summary, err := scyllaStore.GetRoomSummary(ctx, normalizedRoomID)
		if err != nil {
			log.Printf("[private-ai] scylla summary lookup failed: %v", err)
		} else if strings.TrimSpace(summary) != "" {
			if redisStore != nil {
				if cacheErr := redisStore.SetRoomSummary(ctx, normalizedRoomID, summary); cacheErr != nil {
					log.Printf("[private-ai] redis summary backfill failed: %v", cacheErr)
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
		log.Printf("[private-ai] redis message context lookup failed: %v", err)
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

func writeAIChatError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": strings.TrimSpace(message),
	})
}
