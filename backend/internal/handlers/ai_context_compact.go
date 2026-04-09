package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/database"
)

// Universal context cache TTL — shared compacted contexts expire after 4 hours.
const universalContextCacheTTL = 4 * time.Hour

// contextCompactStores holds optional Redis store for universal context caching.
// Configured via ConfigureAIChatPersistence (shared with private AI chat).
var contextCompactRedis struct {
	store *database.RedisStore
}

// SetContextCompactRedis wires in the Redis store for universal context caching.
func SetContextCompactRedis(r *database.RedisStore) {
	contextCompactRedis.store = r
}

// CompactMessage is a single turn in a conversation to be compacted.
type CompactMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` // plain text content
}

// contextCompactRequest is the request body for POST /api/ai/context/compact.
type contextCompactRequest struct {
	Messages  []CompactMessage `json:"messages"`
	RoomID    string           `json:"roomId,omitempty"`
	Universal bool             `json:"universal"` // true = normal/generic chat, can be cached globally
}

// contextCompactResponse is the response payload.
type contextCompactResponse struct {
	Summary  string `json:"summary"`
	CacheKey string `json:"cacheKey,omitempty"` // returned for universal contexts
	Cached   bool   `json:"cached"`             // true if this summary came from cache
}

// HandleContextCompact compacts a conversation history into a dense summary.
// For universal (non-room-specific) chats it caches the result in Redis so
// other users with the same conversation content can reuse it.
func HandleContextCompact(w http.ResponseWriter, r *http.Request) {
	var req contextCompactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAIChatError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if len(req.Messages) == 0 {
		writeAIChatError(w, http.StatusBadRequest, "messages required")
		return
	}

	// Build a cache key from the conversation content.
	cacheKey := buildContextCacheKey(req.Messages, req.RoomID)

	// For universal chats, check the shared cache first.
	if req.Universal {
		if cached := loadCachedCompact(r.Context(), cacheKey); cached != "" {
			writeJSON(w, http.StatusOK, contextCompactResponse{
				Summary:  cached,
				CacheKey: cacheKey,
				Cached:   true,
			})
			return
		}
	}

	summary, err := generateCompactSummary(r.Context(), req.Messages)
	if err != nil {
		writeAIChatError(w, http.StatusBadGateway, "failed to compact context")
		return
	}

	// Cache for universal chats.
	if req.Universal {
		saveCachedCompact(r.Context(), cacheKey, summary)
	}

	writeJSON(w, http.StatusOK, contextCompactResponse{
		Summary:  summary,
		CacheKey: cacheKey,
		Cached:   false,
	})
}

// generateCompactSummary asks the AI to produce a dense summary of the messages.
func generateCompactSummary(ctx context.Context, messages []CompactMessage) (string, error) {
	var sb strings.Builder
	sb.WriteString("You are a conversation compactor. Your only job is to produce a compact, information-dense summary of the following conversation. ")
	sb.WriteString("Preserve all facts, decisions, and action items. Remove filler and social pleasantries. ")
	sb.WriteString("Output plain prose, no headers, no bullets. Be thorough but concise.\n\n")
	sb.WriteString("--- CONVERSATION ---\n")
	for _, m := range messages {
		role := strings.ToUpper(strings.TrimSpace(m.Role))
		if role == "" {
			role = "USER"
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(content)
		sb.WriteString("\n")
	}
	sb.WriteString("--- END CONVERSATION ---\n\n")
	sb.WriteString("--- USER MESSAGE ---\nProvide the compact summary now.")

	return ai.DefaultRouter.GenerateChatResponse(ctx, sb.String())
}

// buildContextCacheKey creates a deterministic key from conversation content.
func buildContextCacheKey(messages []CompactMessage, roomID string) string {
	var sb strings.Builder
	if roomID != "" {
		sb.WriteString("room:")
		sb.WriteString(strings.TrimSpace(roomID))
		sb.WriteString("|")
	}
	for _, m := range messages {
		sb.WriteString(strings.TrimSpace(m.Role))
		sb.WriteString(":")
		sb.WriteString(strings.TrimSpace(m.Content))
		sb.WriteString("|")
	}
	hash := sha256.Sum256([]byte(sb.String()))
	return fmt.Sprintf("ai:ctx:compact:%x", hash[:8])
}

func loadCachedCompact(ctx context.Context, key string) string {
	if contextCompactRedis.store == nil || contextCompactRedis.store.Client == nil {
		return ""
	}
	val, err := contextCompactRedis.store.Client.Get(ctx, key).Result()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(val)
}

func saveCachedCompact(ctx context.Context, key, summary string) {
	if contextCompactRedis.store == nil || contextCompactRedis.store.Client == nil {
		return
	}
	_ = contextCompactRedis.store.Client.Set(ctx, key, summary, universalContextCacheTTL).Err()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
