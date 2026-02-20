package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/models"
)

const (
	defaultMessagePageSize = 50
	maxMessagePageSize     = 100
)

type RoomMessagesResponse struct {
	Messages []models.Message `json:"messages"`
	HasMore  bool             `json:"hasMore"`
}

func (h *RoomHandler) GetRoomMessages(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Message storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	limit := parseMessagePageSize(r.URL.Query().Get("limit"))
	beforeMessageID := normalizeMessageID(r.URL.Query().Get("before"))
	beforeCreatedAt := parseOptionalTimestampParam(
		firstNonEmpty(
			r.URL.Query().Get("beforeCreatedAt"),
			r.URL.Query().Get("before_created_at"),
		),
	)
	ctx := r.Context()

	messages, hasMore, err := h.queryRoomMessagesPage(
		ctx,
		roomID,
		beforeMessageID,
		beforeCreatedAt,
		limit,
	)
	if err != nil {
		log.Printf(
			"[room-messages] query failed room=%s before=%s beforeCreatedAt=%s limit=%d err=%v",
			roomID,
			beforeMessageID,
			beforeCreatedAt.UTC().Format(time.RFC3339Nano),
			limit,
			err,
		)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load messages"})
		return
	}

	// Return chronological order so the frontend can prepend cleanly.
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}

	response := RoomMessagesResponse{
		Messages: messages,
		HasMore:  hasMore,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (h *RoomHandler) queryRoomMessagesPage(
	ctx context.Context,
	roomID, beforeMessageID string,
	beforeCreatedAt time.Time,
	limit int,
) ([]models.Message, bool, error) {
	messagesTable := h.scylla.Table("messages")
	fetchLimit := limit + 1

	baseSelect := `SELECT room_id, message_id, sender_id, sender_name, content, type, media_url, media_type, file_name, is_edited, edited_at, has_break_room, break_room_id, break_join_count, reply_to_message_id, reply_to_snippet, created_at FROM %s`
	query := fmt.Sprintf(baseSelect+` WHERE room_id = ? ORDER BY created_at DESC LIMIT ?`, messagesTable)
	args := []interface{}{roomID, fetchLimit}

	if !beforeCreatedAt.IsZero() {
		query = fmt.Sprintf(baseSelect+` WHERE room_id = ? AND created_at < ? ORDER BY created_at DESC LIMIT ?`, messagesTable)
		args = []interface{}{roomID, beforeCreatedAt, fetchLimit}
	} else if beforeMessageID != "" {
		resolvedBeforeCreatedAt, err := h.lookupMessageCreatedAt(ctx, roomID, beforeMessageID)
		if err != nil {
			if err == gocql.ErrNotFound {
				return []models.Message{}, false, nil
			}
			return nil, false, err
		}
		query = fmt.Sprintf(baseSelect+` WHERE room_id = ? AND created_at < ? ORDER BY created_at DESC LIMIT ?`, messagesTable)
		args = []interface{}{roomID, resolvedBeforeCreatedAt, fetchLimit}
	}

	iter := h.scylla.Session.Query(query, args...).WithContext(ctx).Iter()

	messages := make([]models.Message, 0, fetchLimit)
	var (
		dbRoomID       string
		messageID      string
		senderID       string
		senderName     string
		content        string
		msgType        string
		mediaURL       string
		mediaType      string
		fileName       string
		isEdited       bool
		editedAt       time.Time
		hasBreakRoom   bool
		breakRoomID    string
		breakJoinCount int
		replyToID      string
		replySnippet   string
		createdAt      time.Time
	)

	for iter.Scan(
		&dbRoomID,
		&messageID,
		&senderID,
		&senderName,
		&content,
		&msgType,
		&mediaURL,
		&mediaType,
		&fileName,
		&isEdited,
		&editedAt,
		&hasBreakRoom,
		&breakRoomID,
		&breakJoinCount,
		&replyToID,
		&replySnippet,
		&createdAt,
	) {
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
			ReplyToMessageID: replyToID,
			ReplyToSnippet:   replySnippet,
			CreatedAt:        createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, false, err
	}

	hasMore := false
	if len(messages) > limit {
		hasMore = true
		messages = messages[:limit]
	}

	return messages, hasMore, nil
}

func (h *RoomHandler) lookupMessageCreatedAt(
	ctx context.Context,
	roomID, messageID string,
) (time.Time, error) {
	messagesTable := h.scylla.Table("messages")
	query := fmt.Sprintf(
		`SELECT created_at FROM %s WHERE room_id = ? AND message_id = ? LIMIT 1 ALLOW FILTERING`,
		messagesTable,
	)
	var createdAt time.Time
	if err := h.scylla.Session.Query(query, roomID, messageID).WithContext(ctx).Scan(&createdAt); err != nil {
		return time.Time{}, err
	}
	if createdAt.IsZero() {
		return time.Time{}, gocql.ErrNotFound
	}
	return createdAt, nil
}

func parseMessagePageSize(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultMessagePageSize
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return defaultMessagePageSize
	}
	if value > maxMessagePageSize {
		return maxMessagePageSize
	}
	return value
}

func parseOptionalTimestampParam(raw string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}
	}

	if numeric, err := strconv.ParseInt(trimmed, 10, 64); err == nil && numeric > 0 {
		if numeric < 1_000_000_000_000 {
			numeric *= 1000
		}
		return time.UnixMilli(numeric).UTC()
	}

	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.UTC()
	}

	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeMessageID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range trimmed {
		if (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '_' ||
			ch == '-' {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}
