package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/database"
)

const privateAIChatLogsTableName = "private_ai_logs"

// DefaultAIRouter serves private chat requests using configured AI providers.
var DefaultAIRouter = ai.DefaultRouter

var privateAIChatAuditStore struct {
	mu     sync.RWMutex
	scylla *database.ScyllaStore
}

var privateAIChatSchemaState struct {
	mu      sync.Mutex
	ensured map[string]bool
}

type privateAIChatRequest struct {
	Prompt   string `json:"prompt"`
	DeviceID string `json:"deviceId"`
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

func ConfigureAIChatPersistence(scyllaStore *database.ScyllaStore) {
	privateAIChatAuditStore.mu.Lock()
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

	userID, username := extractAIChatIdentity(r)
	if userID == "" || username == "" {
		writeAIChatError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}

	ipAddress := strings.TrimSpace(extractClientIP(r))
	if ipAddress == "" {
		ipAddress = "unknown"
	}

	responseText, err := DefaultAIRouter.GenerateChatResponse(r.Context(), prompt)
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
