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
	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/security"
)

const (
	defaultMessagePageSize          = 50
	maxMessagePageSize              = 100
	defaultDiscussionCommentsLimit  = 250
	maxDiscussionCommentsLimit      = 500
	maxDiscussionCommentContentRune = 2000
	deletedDiscussionPlaceholder    = "This message was deleted"
	hardScyllaTTLSeconds            = 15 * 24 * 60 * 60
)

type RoomMessagesResponse struct {
	Messages []models.Message `json:"messages"`
	HasMore  bool             `json:"hasMore"`
}

type RoomPinUpsertRequest struct {
	UserID    string `json:"userId"`
	MessageID string `json:"messageId"`
}

type RoomPinUpsertResponse struct {
	RoomID    string `json:"roomId"`
	MessageID string `json:"messageId"`
	CreatedAt int64  `json:"createdAt"`
	Type      string `json:"type"`
}

type RoomPinNavigateResponse struct {
	Message *models.Message `json:"message"`
}

type RoomDiscussionCommentsResponse struct {
	Comments []models.Message `json:"comments"`
}

type RoomDiscussionCommentResponse struct {
	Comment *models.Message `json:"comment"`
}

type DiscussionCommentMutationRequest struct {
	UserID          string `json:"userId"`
	Username        string `json:"username,omitempty"`
	Content         string `json:"content,omitempty"`
	ParentCommentID string `json:"parentCommentId,omitempty"`
	CreatedAt       int64  `json:"createdAt,omitempty"`
}

type discussionCommentRow struct {
	CreatedAt       time.Time
	CommentID       string
	ParentCommentID string
	SenderID        string
	SenderName      string
	Content         string
	IsEdited        bool
	EditedAt        time.Time
	IsDeleted       bool
	IsPinned        bool
	PinnedBy        string
	PinnedByName    string
	PinnedAt        time.Time
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
	roomAlive, aliveErr := h.isRoomAliveInRedis(ctx, roomID)
	if aliveErr != nil {
		log.Printf("[room-messages] redis gatekeeper failed room=%s err=%v", roomID, aliveErr)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room status"})
		return
	}
	if !roomAlive {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RoomMessagesResponse{
			Messages: []models.Message{},
			HasMore:  false,
		})
		return
	}

	requiresPassword, passwordErr := h.isRoomPasswordProtected(ctx, roomID)
	if passwordErr != nil {
		log.Printf("[room-messages] room password lookup failed room=%s err=%v", roomID, passwordErr)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room access settings"})
		return
	}
	if requiresPassword {
		userID := normalizeIdentifier(
			firstNonEmpty(
				r.URL.Query().Get("userId"),
				r.URL.Query().Get("user_id"),
				r.Header.Get("X-User-Id"),
			),
		)
		if userID == "" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":            "Join the room to view messages",
				"requiresPassword": true,
			})
			return
		}
		isMember, memberErr := h.isRoomMember(ctx, roomID, userID)
		if memberErr != nil {
			log.Printf("[room-messages] member check failed room=%s user=%s err=%v", roomID, userID, memberErr)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify membership"})
			return
		}
		if !isMember {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":            "Join the room to view messages",
				"requiresPassword": true,
			})
			return
		}
	}

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

func (h *RoomHandler) UpsertRoomPin(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Message storage unavailable"})
		return
	}
	if h.redis == nil || h.redis.Client == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Membership storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	var req RoomPinUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	userID := normalizeIdentifier(req.UserID)
	messageID := normalizeMessageID(req.MessageID)
	if userID == "" || messageID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "userId and messageId are required"})
		return
	}

	ctx := r.Context()
	isMember, err := h.isRoomMember(ctx, roomID, userID)
	if err != nil {
		log.Printf("[room-pins] membership check failed room=%s user=%s err=%v", roomID, userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "User is not a member of this room"})
		return
	}

	createdAt, err := h.lookupMessageCreatedAt(ctx, roomID, messageID)
	if err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Message not found"})
			return
		}
		log.Printf("[room-pins] lookup message failed room=%s message=%s err=%v", roomID, messageID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load message"})
		return
	}

	lookupMessage, err := h.lookupMessageByPrimaryKey(ctx, roomID, createdAt, messageID)
	if err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Message not found"})
			return
		}
		log.Printf("[room-pins] lookup primary message failed room=%s message=%s err=%v", roomID, messageID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load message"})
		return
	}

	messageType := strings.ToLower(strings.TrimSpace(lookupMessage.Type))
	if messageType == "" {
		messageType = "message"
	}

	roomPinsTable := h.scylla.Table("room_pins")
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, created_at, message_id, type) VALUES (?, ?, ?, ?) USING TTL %d`,
		roomPinsTable,
		hardScyllaTTLSeconds,
	)
	if err := h.scylla.Session.Query(
		insertQuery,
		roomID,
		createdAt,
		messageID,
		messageType,
	).WithContext(ctx).Exec(); err != nil {
		log.Printf("[room-pins] upsert failed room=%s message=%s err=%v", roomID, messageID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to pin message"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RoomPinUpsertResponse{
		RoomID:    roomID,
		MessageID: messageID,
		CreatedAt: createdAt.UTC().UnixMilli(),
		Type:      messageType,
	})
}

func (h *RoomHandler) NavigateRoomPins(w http.ResponseWriter, r *http.Request) {
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

	beforeCursor := parseOptionalTimestampParam(r.URL.Query().Get("before"))
	afterCursor := parseOptionalTimestampParam(r.URL.Query().Get("after"))
	if beforeCursor.IsZero() == afterCursor.IsZero() {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(
			map[string]string{"error": "Provide exactly one query parameter: before or after"},
		)
		return
	}

	ctx := r.Context()
	messageID, createdAt, err := h.lookupAdjacentPinnedMessage(ctx, roomID, beforeCursor, afterCursor)
	if err != nil {
		if err == gocql.ErrNotFound {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(RoomPinNavigateResponse{Message: nil})
			return
		}
		log.Printf(
			"[room-pins] pin lookup failed room=%s before=%s after=%s err=%v",
			roomID,
			beforeCursor.UTC().Format(time.RFC3339Nano),
			afterCursor.UTC().Format(time.RFC3339Nano),
			err,
		)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to navigate room pins"})
		return
	}

	message, err := h.lookupMessageByPrimaryKey(ctx, roomID, createdAt, messageID)
	if err != nil {
		if err == gocql.ErrNotFound {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(RoomPinNavigateResponse{Message: nil})
			return
		}
		log.Printf(
			"[room-pins] message lookup failed room=%s message=%s created_at=%s err=%v",
			roomID,
			messageID,
			createdAt.UTC().Format(time.RFC3339Nano),
			err,
		)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load pinned message"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RoomPinNavigateResponse{Message: &message})
}

func (h *RoomHandler) GetPinnedDiscussionComments(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Message storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	pinMessageID := normalizeMessageID(chi.URLParam(r, "pinMessageId"))
	userID := normalizeIdentifier(r.URL.Query().Get("userId"))
	if roomID == "" || pinMessageID == "" || userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room, pin, or user id"})
		return
	}

	ctx := r.Context()
	roomAlive, aliveErr := h.isRoomAliveInRedis(ctx, roomID)
	if aliveErr != nil {
		log.Printf("[pin-discussion] redis gatekeeper failed room=%s err=%v", roomID, aliveErr)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room status"})
		return
	}
	if !roomAlive {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RoomDiscussionCommentsResponse{Comments: []models.Message{}})
		return
	}

	isMember, err := h.isRoomMember(ctx, roomID, userID)
	if err != nil {
		log.Printf("[pin-discussion] membership check failed room=%s user=%s err=%v", roomID, userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "User is not a member of this room"})
		return
	}

	limit := parseDiscussionCommentsLimit(r.URL.Query().Get("limit"))
	comments, err := h.queryPinnedDiscussionComments(ctx, roomID, pinMessageID, limit)
	if err != nil {
		log.Printf("[pin-discussion] query failed room=%s pin=%s err=%v", roomID, pinMessageID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load discussion comments"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RoomDiscussionCommentsResponse{Comments: comments})
}

func (h *RoomHandler) CreatePinnedDiscussionComment(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Message storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	pinMessageID := normalizeMessageID(chi.URLParam(r, "pinMessageId"))
	if roomID == "" || pinMessageID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room or pin id"})
		return
	}

	var req DiscussionCommentMutationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	userID := normalizeIdentifier(req.UserID)
	senderName := normalizeUsername(req.Username)
	content := strings.TrimSpace(req.Content)
	parentCommentID := normalizeMessageID(req.ParentCommentID)
	if userID == "" || content == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "userId and content are required"})
		return
	}
	if senderName == "" {
		senderName = "Guest"
	}
	if len([]rune(content)) > maxDiscussionCommentContentRune {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Comment is too long"})
		return
	}

	ctx := r.Context()
	isMember, err := h.isRoomMember(ctx, roomID, userID)
	if err != nil {
		log.Printf("[pin-discussion] membership check failed room=%s user=%s err=%v", roomID, userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "User is not a member of this room"})
		return
	}

	createdAt := time.Now().UTC()
	commentID := generateDiscussionCommentID(createdAt)
	encryptedContent, encryptErr := security.EncryptMessage(content)
	if encryptErr != nil {
		log.Printf("[pin-discussion] encrypt failed room=%s pin=%s err=%v", roomID, pinMessageID, encryptErr)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save discussion comment"})
		return
	}

	commentsTable := h.scylla.Table("pin_discussion_comments")
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, pin_message_id, created_at, comment_id, parent_comment_id, sender_id, sender_name, content, is_edited, edited_at, is_deleted, is_pinned, pinned_by, pinned_by_name, pinned_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL %d`,
		commentsTable,
		hardScyllaTTLSeconds,
	)
	if err := h.scylla.Session.Query(
		insertQuery,
		roomID,
		pinMessageID,
		createdAt,
		commentID,
		parentCommentID,
		userID,
		senderName,
		encryptedContent,
		false,
		nil,
		false,
		false,
		"",
		"",
		nil,
	).WithContext(ctx).Exec(); err != nil {
		log.Printf("[pin-discussion] insert failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save discussion comment"})
		return
	}

	responseComment := discussionRowToModelMessage(roomID, discussionCommentRow{
		CreatedAt:       createdAt,
		CommentID:       commentID,
		ParentCommentID: parentCommentID,
		SenderID:        userID,
		SenderName:      senderName,
		Content:         content,
		IsEdited:        false,
		EditedAt:        time.Time{},
		IsDeleted:       false,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RoomDiscussionCommentResponse{Comment: &responseComment})
}

func (h *RoomHandler) EditPinnedDiscussionComment(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Message storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	pinMessageID := normalizeMessageID(chi.URLParam(r, "pinMessageId"))
	commentID := normalizeMessageID(chi.URLParam(r, "commentId"))
	if roomID == "" || pinMessageID == "" || commentID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room, pin, or comment id"})
		return
	}

	var req DiscussionCommentMutationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}
	userID := normalizeIdentifier(req.UserID)
	content := strings.TrimSpace(req.Content)
	createdAt := parseClientTimestamp(req.CreatedAt)
	if userID == "" || content == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "userId and content are required"})
		return
	}
	if len([]rune(content)) > maxDiscussionCommentContentRune {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Comment is too long"})
		return
	}

	ctx := r.Context()
	isMember, err := h.isRoomMember(ctx, roomID, userID)
	if err != nil {
		log.Printf("[pin-discussion] membership check failed room=%s user=%s err=%v", roomID, userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "User is not a member of this room"})
		return
	}

	if createdAt.IsZero() {
		resolvedCreatedAt, resolveErr := h.lookupPinnedDiscussionCommentCreatedAt(ctx, roomID, pinMessageID, commentID)
		if resolveErr != nil {
			if resolveErr == gocql.ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Discussion comment not found"})
				return
			}
			log.Printf("[pin-discussion] lookup created_at failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, resolveErr)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to edit discussion comment"})
			return
		}
		createdAt = resolvedCreatedAt
	}

	currentRow, err := h.lookupPinnedDiscussionCommentByPrimaryKey(ctx, roomID, pinMessageID, createdAt, commentID)
	if err != nil {
		if err == gocql.ErrNotFound {
			resolvedCreatedAt, resolveErr := h.lookupPinnedDiscussionCommentCreatedAt(ctx, roomID, pinMessageID, commentID)
			if resolveErr != nil {
				if resolveErr == gocql.ErrNotFound {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "Discussion comment not found"})
					return
				}
				log.Printf("[pin-discussion] fallback lookup created_at failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, resolveErr)
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to edit discussion comment"})
				return
			}
			createdAt = resolvedCreatedAt
			currentRow, err = h.lookupPinnedDiscussionCommentByPrimaryKey(ctx, roomID, pinMessageID, createdAt, commentID)
			if err == gocql.ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Discussion comment not found"})
				return
			}
			if err != nil {
				log.Printf("[pin-discussion] fallback lookup failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, err)
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to edit discussion comment"})
				return
			}
		} else {
			log.Printf("[pin-discussion] lookup failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, err)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to edit discussion comment"})
			return
		}
	}
	if normalizeIdentifier(currentRow.SenderID) != userID {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Only the comment author can edit this comment"})
		return
	}
	if currentRow.IsDeleted {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Deleted comments cannot be edited"})
		return
	}

	editedAt := time.Now().UTC()
	encryptedContent, encryptErr := security.EncryptMessage(content)
	if encryptErr != nil {
		log.Printf("[pin-discussion] encrypt failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, encryptErr)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to edit discussion comment"})
		return
	}

	commentsTable := h.scylla.Table("pin_discussion_comments")
	updateQuery := fmt.Sprintf(
		`UPDATE %s SET content = ?, is_edited = ?, edited_at = ?, is_deleted = ? WHERE room_id = ? AND pin_message_id = ? AND created_at = ? AND comment_id = ?`,
		commentsTable,
	)
	if err := h.scylla.Session.Query(
		updateQuery,
		encryptedContent,
		true,
		editedAt,
		false,
		roomID,
		pinMessageID,
		createdAt,
		commentID,
	).WithContext(ctx).Exec(); err != nil {
		log.Printf("[pin-discussion] update failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to edit discussion comment"})
		return
	}

	currentRow.Content = content
	currentRow.IsEdited = true
	currentRow.EditedAt = editedAt
	currentRow.IsDeleted = false
	responseComment := discussionRowToModelMessage(roomID, currentRow)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RoomDiscussionCommentResponse{Comment: &responseComment})
}

func (h *RoomHandler) DeletePinnedDiscussionComment(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Message storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	pinMessageID := normalizeMessageID(chi.URLParam(r, "pinMessageId"))
	commentID := normalizeMessageID(chi.URLParam(r, "commentId"))
	if roomID == "" || pinMessageID == "" || commentID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room, pin, or comment id"})
		return
	}

	var req DiscussionCommentMutationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}
	userID := normalizeIdentifier(req.UserID)
	createdAt := parseClientTimestamp(req.CreatedAt)
	if userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "userId is required"})
		return
	}

	ctx := r.Context()
	isMember, err := h.isRoomMember(ctx, roomID, userID)
	if err != nil {
		log.Printf("[pin-discussion] membership check failed room=%s user=%s err=%v", roomID, userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "User is not a member of this room"})
		return
	}

	if createdAt.IsZero() {
		resolvedCreatedAt, resolveErr := h.lookupPinnedDiscussionCommentCreatedAt(ctx, roomID, pinMessageID, commentID)
		if resolveErr != nil {
			if resolveErr == gocql.ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Discussion comment not found"})
				return
			}
			log.Printf("[pin-discussion] lookup created_at failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, resolveErr)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete discussion comment"})
			return
		}
		createdAt = resolvedCreatedAt
	}

	currentRow, err := h.lookupPinnedDiscussionCommentByPrimaryKey(ctx, roomID, pinMessageID, createdAt, commentID)
	if err != nil {
		if err == gocql.ErrNotFound {
			resolvedCreatedAt, resolveErr := h.lookupPinnedDiscussionCommentCreatedAt(ctx, roomID, pinMessageID, commentID)
			if resolveErr != nil {
				if resolveErr == gocql.ErrNotFound {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "Discussion comment not found"})
					return
				}
				log.Printf("[pin-discussion] fallback lookup created_at failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, resolveErr)
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete discussion comment"})
				return
			}
			createdAt = resolvedCreatedAt
			currentRow, err = h.lookupPinnedDiscussionCommentByPrimaryKey(ctx, roomID, pinMessageID, createdAt, commentID)
			if err == gocql.ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Discussion comment not found"})
				return
			}
			if err != nil {
				log.Printf("[pin-discussion] fallback lookup failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, err)
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete discussion comment"})
				return
			}
		} else {
			log.Printf("[pin-discussion] lookup failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, err)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete discussion comment"})
			return
		}
	}
	if normalizeIdentifier(currentRow.SenderID) != userID {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Only the comment author can delete this comment"})
		return
	}

	editedAt := time.Now().UTC()
	encryptedContent, encryptErr := security.EncryptMessage(deletedDiscussionPlaceholder)
	if encryptErr != nil {
		log.Printf("[pin-discussion] encrypt delete placeholder failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, encryptErr)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete discussion comment"})
		return
	}

	commentsTable := h.scylla.Table("pin_discussion_comments")
	updateQuery := fmt.Sprintf(
		`UPDATE %s SET content = ?, is_edited = ?, edited_at = ?, is_deleted = ? WHERE room_id = ? AND pin_message_id = ? AND created_at = ? AND comment_id = ?`,
		commentsTable,
	)
	if err := h.scylla.Session.Query(
		updateQuery,
		encryptedContent,
		false,
		editedAt,
		true,
		roomID,
		pinMessageID,
		createdAt,
		commentID,
	).WithContext(ctx).Exec(); err != nil {
		log.Printf("[pin-discussion] delete failed room=%s pin=%s comment=%s err=%v", roomID, pinMessageID, commentID, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete discussion comment"})
		return
	}

	currentRow.Content = deletedDiscussionPlaceholder
	currentRow.IsEdited = false
	currentRow.EditedAt = editedAt
	currentRow.IsDeleted = true
	responseComment := discussionRowToModelMessage(roomID, currentRow)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RoomDiscussionCommentResponse{Comment: &responseComment})
}

func (h *RoomHandler) queryRoomMessagesPage(
	ctx context.Context,
	roomID, beforeMessageID string,
	beforeCreatedAt time.Time,
	limit int,
) ([]models.Message, bool, error) {
	softCutoff, err := h.resolveRoomMessageSoftCutoff(ctx, roomID)
	if err != nil {
		return nil, false, err
	}

	messagesTable := h.scylla.Table("messages")
	fetchLimit := limit + 1

	baseSelect := `SELECT room_id, message_id, sender_id, sender_name, content, type, media_url, media_type, file_name, is_edited, edited_at, has_break_room, break_room_id, break_join_count, reply_to_message_id, reply_to_snippet, created_at FROM %s`
	query := fmt.Sprintf(baseSelect+` WHERE room_id = ? AND created_at >= ? ORDER BY created_at DESC LIMIT ?`, messagesTable)
	args := []interface{}{roomID, softCutoff, fetchLimit}

	if !beforeCreatedAt.IsZero() {
		query = fmt.Sprintf(baseSelect+` WHERE room_id = ? AND created_at < ? AND created_at >= ? ORDER BY created_at DESC LIMIT ?`, messagesTable)
		args = []interface{}{roomID, beforeCreatedAt, softCutoff, fetchLimit}
	} else if beforeMessageID != "" {
		resolvedBeforeCreatedAt, lookupErr := h.lookupMessageCreatedAt(ctx, roomID, beforeMessageID)
		if lookupErr != nil {
			if lookupErr == gocql.ErrNotFound {
				return []models.Message{}, false, nil
			}
			return nil, false, lookupErr
		}
		query = fmt.Sprintf(baseSelect+` WHERE room_id = ? AND created_at < ? AND created_at >= ? ORDER BY created_at DESC LIMIT ?`, messagesTable)
		args = []interface{}{roomID, resolvedBeforeCreatedAt, softCutoff, fetchLimit}
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
			ReplyToMessageID: replyToID,
			ReplyToSnippet:   replySnippet,
			CreatedAt:        createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, false, err
	}

	if err := h.applyPinnedState(ctx, roomID, messages); err != nil {
		log.Printf("[room-messages] pin-state enrich failed room=%s err=%v", roomID, err)
	}

	hasMore := false
	if len(messages) > limit {
		hasMore = true
		messages = messages[:limit]
	}

	return messages, hasMore, nil
}

func (h *RoomHandler) isRoomAliveInRedis(ctx context.Context, roomID string) (bool, error) {
	if h == nil || h.redis == nil || h.redis.Client == nil {
		return true, nil
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return false, nil
	}
	exists, err := h.redis.Client.Exists(ctx, roomKey(normalizedRoomID)).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (h *RoomHandler) resolveRoomMessageSoftCutoff(ctx context.Context, roomID string) (time.Time, error) {
	defaultCutoff := time.Now().UTC().Add(-roomDefaultTTL)
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return defaultCutoff, nil
	}

	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return defaultCutoff, nil
	}

	if h.redis != nil && h.redis.Client != nil {
		createdAtRaw, err := h.redis.Client.HGet(ctx, roomKey(roomID), "created_at").Result()
		if err == nil {
			if createdAtUnix, parseErr := strconv.ParseInt(strings.TrimSpace(createdAtRaw), 10, 64); parseErr == nil && createdAtUnix > 0 {
				createdAtCutoff := time.Unix(createdAtUnix, 0).UTC()
				if !createdAtCutoff.IsZero() && createdAtCutoff.Before(defaultCutoff) {
					defaultCutoff = createdAtCutoff
				}
			}
		}
	}

	softExpiryTable := h.scylla.Table(roomSoftExpiryTable)
	query := fmt.Sprintf(
		`SELECT extended_expiry_time FROM %s WHERE room_id = ? LIMIT 1`,
		softExpiryTable,
	)
	var softCutoff time.Time
	if err := h.scylla.Session.Query(query, roomID).WithContext(ctx).Scan(&softCutoff); err != nil {
		if err == gocql.ErrNotFound {
			return defaultCutoff, nil
		}
		return time.Time{}, err
	}
	if softCutoff.IsZero() {
		return defaultCutoff, nil
	}
	return softCutoff.UTC(), nil
}

func (h *RoomHandler) lookupAdjacentPinnedMessage(
	ctx context.Context,
	roomID string,
	beforeCursor, afterCursor time.Time,
) (string, time.Time, error) {
	softCutoff, err := h.resolveRoomMessageSoftCutoff(ctx, roomID)
	if err != nil {
		return "", time.Time{}, err
	}

	roomPinsTable := h.scylla.Table("room_pins")
	query := ""
	args := []interface{}{roomID, softCutoff}
	if !beforeCursor.IsZero() {
		query = fmt.Sprintf(
			`SELECT message_id, created_at FROM %s WHERE room_id = ? AND created_at >= ? AND created_at < ? LIMIT 1`,
			roomPinsTable,
		)
		args = append(args, beforeCursor)
	} else {
		query = fmt.Sprintf(
			`SELECT message_id, created_at FROM %s WHERE room_id = ? AND created_at >= ? AND created_at > ? ORDER BY created_at ASC LIMIT 1`,
			roomPinsTable,
		)
		args = append(args, afterCursor)
	}

	var (
		messageID string
		createdAt time.Time
	)
	if err := h.scylla.Session.Query(query, args...).WithContext(ctx).Scan(&messageID, &createdAt); err != nil {
		return "", time.Time{}, err
	}
	if strings.TrimSpace(messageID) == "" || createdAt.IsZero() {
		return "", time.Time{}, gocql.ErrNotFound
	}
	return messageID, createdAt, nil
}

func (h *RoomHandler) lookupMessageByPrimaryKey(
	ctx context.Context,
	roomID string,
	createdAt time.Time,
	messageID string,
) (models.Message, error) {
	softCutoff, err := h.resolveRoomMessageSoftCutoff(ctx, roomID)
	if err != nil {
		return models.Message{}, err
	}
	if createdAt.Before(softCutoff) {
		return models.Message{}, gocql.ErrNotFound
	}

	messagesTable := h.scylla.Table("messages")
	query := fmt.Sprintf(
		`SELECT room_id, message_id, sender_id, sender_name, content, type, media_url, media_type, file_name, is_edited, edited_at, has_break_room, break_room_id, break_join_count, reply_to_message_id, reply_to_snippet, created_at FROM %s WHERE room_id = ? AND created_at = ? AND message_id = ? LIMIT 1`,
		messagesTable,
	)
	var (
		dbRoomID       string
		dbMessageID    string
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
		dbCreatedAt    time.Time
	)
	if err := h.scylla.Session.Query(query, roomID, createdAt, messageID).WithContext(ctx).Scan(
		&dbRoomID,
		&dbMessageID,
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
		&dbCreatedAt,
	); err != nil {
		return models.Message{}, err
	}
	if dbCreatedAt.Before(softCutoff) {
		return models.Message{}, gocql.ErrNotFound
	}

	if decrypted, decryptErr := security.DecryptMessage(content); decryptErr == nil {
		content = decrypted
	}

	var editedAtPtr *time.Time
	if !editedAt.IsZero() {
		editedCopy := editedAt
		editedAtPtr = &editedCopy
	}

	isPinned, err := h.isPinnedMessage(ctx, roomID, dbMessageID, dbCreatedAt)
	if err != nil {
		log.Printf(
			"[room-pins] lookup pinned state failed room=%s message=%s created_at=%s err=%v",
			roomID,
			dbMessageID,
			dbCreatedAt.UTC().Format(time.RFC3339Nano),
			err,
		)
	}

	return models.Message{
		ID:               dbMessageID,
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
		IsPinned:         isPinned,
		CreatedAt:        dbCreatedAt,
	}, nil
}

func (h *RoomHandler) lookupMessageCreatedAt(
	ctx context.Context,
	roomID, messageID string,
) (time.Time, error) {
	softCutoff, err := h.resolveRoomMessageSoftCutoff(ctx, roomID)
	if err != nil {
		return time.Time{}, err
	}

	messagesTable := h.scylla.Table("messages")
	query := fmt.Sprintf(
		`SELECT created_at FROM %s WHERE room_id = ? AND message_id = ? LIMIT 1 ALLOW FILTERING`,
		messagesTable,
	)
	var createdAt time.Time
	if err := h.scylla.Session.Query(query, roomID, messageID).WithContext(ctx).Scan(&createdAt); err != nil {
		return time.Time{}, err
	}
	if createdAt.IsZero() || createdAt.Before(softCutoff) {
		return time.Time{}, gocql.ErrNotFound
	}
	return createdAt, nil
}

func (h *RoomHandler) applyPinnedState(ctx context.Context, roomID string, messages []models.Message) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil || len(messages) == 0 {
		return nil
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
		return nil
	}

	roomPinsTable := h.scylla.Table("room_pins")
	query := fmt.Sprintf(
		`SELECT message_id FROM %s WHERE room_id = ? AND created_at >= ? AND created_at <= ?`,
		roomPinsTable,
	)
	iter := h.scylla.Session.Query(query, roomID, first, last).WithContext(ctx).Iter()
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
		return err
	}
	if len(pinnedByMessageID) == 0 {
		return nil
	}

	for index, message := range messages {
		if pinnedByMessageID[normalizeMessageID(message.ID)] {
			messages[index].IsPinned = true
		}
	}
	return nil
}

func (h *RoomHandler) isPinnedMessage(
	ctx context.Context,
	roomID, messageID string,
	createdAt time.Time,
) (bool, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil || createdAt.IsZero() {
		return false, nil
	}

	roomPinsTable := h.scylla.Table("room_pins")
	query := fmt.Sprintf(
		`SELECT message_id FROM %s WHERE room_id = ? AND created_at = ? LIMIT 1`,
		roomPinsTable,
	)
	var pinnedMessageID string
	if err := h.scylla.Session.Query(query, roomID, createdAt).WithContext(ctx).Scan(&pinnedMessageID); err != nil {
		if err == gocql.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return normalizeMessageID(pinnedMessageID) == normalizeMessageID(messageID), nil
}

func (h *RoomHandler) ensurePinnedDiscussionSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	commentsTable := h.scylla.Table("pin_discussion_comments")
	createQuery := fmt.Sprintf(
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
		commentsTable,
	)
	if err := h.scylla.Session.Query(createQuery).Exec(); err != nil {
		log.Printf("[pin-discussion] ensure schema failed: %v", err)
	}

	alterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD is_pinned boolean`, commentsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD pinned_by text`, commentsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD pinned_by_name text`, commentsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD pinned_at timestamp`, commentsTable),
	}
	for _, alterQuery := range alterQueries {
		if err := h.scylla.Session.Query(alterQuery).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
			log.Printf("[pin-discussion] ensure schema alter failed: %v", err)
		}
	}
}

func (h *RoomHandler) isRoomMember(ctx context.Context, roomID, userID string) (bool, error) {
	if h == nil || h.redis == nil || h.redis.Client == nil {
		return false, fmt.Errorf("membership storage unavailable")
	}
	isMember, err := h.redis.Client.SIsMember(ctx, roomMembersKey(roomID), userID).Result()
	if err != nil && err != redis.Nil {
		return false, err
	}
	return isMember, nil
}

func (h *RoomHandler) queryPinnedDiscussionComments(
	ctx context.Context,
	roomID, pinMessageID string,
	limit int,
) ([]models.Message, error) {
	commentsTable := h.scylla.Table("pin_discussion_comments")
	query := fmt.Sprintf(
		`SELECT created_at, comment_id, parent_comment_id, sender_id, sender_name, content, is_edited, edited_at, is_deleted, is_pinned, pinned_by, pinned_by_name, pinned_at FROM %s WHERE room_id = ? AND pin_message_id = ? LIMIT ?`,
		commentsTable,
	)
	iter := h.scylla.Session.Query(query, roomID, pinMessageID, limit).WithContext(ctx).Iter()

	comments := make([]models.Message, 0, limit)
	var (
		createdAt       time.Time
		commentID       string
		parentCommentID string
		senderID        string
		senderName      string
		content         string
		isEdited        bool
		editedAt        time.Time
		isDeleted       bool
		isPinned        bool
		pinnedBy        string
		pinnedByName    string
		pinnedAt        time.Time
	)
	for iter.Scan(
		&createdAt,
		&commentID,
		&parentCommentID,
		&senderID,
		&senderName,
		&content,
		&isEdited,
		&editedAt,
		&isDeleted,
		&isPinned,
		&pinnedBy,
		&pinnedByName,
		&pinnedAt,
	) {
		if decrypted, decryptErr := security.DecryptMessage(content); decryptErr == nil {
			content = decrypted
		}
		row := discussionCommentRow{
			CreatedAt:       createdAt,
			CommentID:       commentID,
			ParentCommentID: parentCommentID,
			SenderID:        senderID,
			SenderName:      senderName,
			Content:         content,
			IsEdited:        isEdited,
			EditedAt:        editedAt,
			IsDeleted:       isDeleted,
			IsPinned:        isPinned,
			PinnedBy:        pinnedBy,
			PinnedByName:    pinnedByName,
			PinnedAt:        pinnedAt,
		}
		comments = append(comments, discussionRowToModelMessage(roomID, row))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	return comments, nil
}

func (h *RoomHandler) lookupPinnedDiscussionCommentByPrimaryKey(
	ctx context.Context,
	roomID, pinMessageID string,
	createdAt time.Time,
	commentID string,
) (discussionCommentRow, error) {
	commentsTable := h.scylla.Table("pin_discussion_comments")
	query := fmt.Sprintf(
		`SELECT created_at, comment_id, parent_comment_id, sender_id, sender_name, content, is_edited, edited_at, is_deleted, is_pinned, pinned_by, pinned_by_name, pinned_at FROM %s WHERE room_id = ? AND pin_message_id = ? AND created_at = ? AND comment_id = ? LIMIT 1`,
		commentsTable,
	)

	var row discussionCommentRow
	if err := h.scylla.Session.Query(
		query,
		roomID,
		pinMessageID,
		createdAt,
		commentID,
	).WithContext(ctx).Scan(
		&row.CreatedAt,
		&row.CommentID,
		&row.ParentCommentID,
		&row.SenderID,
		&row.SenderName,
		&row.Content,
		&row.IsEdited,
		&row.EditedAt,
		&row.IsDeleted,
		&row.IsPinned,
		&row.PinnedBy,
		&row.PinnedByName,
		&row.PinnedAt,
	); err != nil {
		return discussionCommentRow{}, err
	}

	if decrypted, decryptErr := security.DecryptMessage(row.Content); decryptErr == nil {
		row.Content = decrypted
	}
	return row, nil
}

func (h *RoomHandler) lookupPinnedDiscussionCommentCreatedAt(
	ctx context.Context,
	roomID, pinMessageID, commentID string,
) (time.Time, error) {
	commentsTable := h.scylla.Table("pin_discussion_comments")
	query := fmt.Sprintf(
		`SELECT created_at FROM %s WHERE room_id = ? AND pin_message_id = ? AND comment_id = ? LIMIT 1 ALLOW FILTERING`,
		commentsTable,
	)
	var createdAt time.Time
	if err := h.scylla.Session.Query(query, roomID, pinMessageID, commentID).WithContext(ctx).Scan(&createdAt); err != nil {
		return time.Time{}, err
	}
	if createdAt.IsZero() {
		return time.Time{}, gocql.ErrNotFound
	}
	return createdAt, nil
}

func discussionRowToModelMessage(roomID string, row discussionCommentRow) models.Message {
	content := strings.TrimSpace(row.Content)
	messageType := "text"
	isEdited := row.IsEdited
	editedAt := row.EditedAt
	if row.IsDeleted {
		messageType = "deleted"
		content = deletedDiscussionPlaceholder
		isEdited = false
	}
	var editedAtPtr *time.Time
	if !editedAt.IsZero() {
		editedCopy := editedAt
		editedAtPtr = &editedCopy
	}

	return models.Message{
		ID:               strings.TrimSpace(row.CommentID),
		RoomID:           roomID,
		SenderID:         strings.TrimSpace(row.SenderID),
		SenderName:       strings.TrimSpace(row.SenderName),
		Content:          content,
		Type:             messageType,
		IsEdited:         isEdited,
		EditedAt:         editedAtPtr,
		ReplyToMessageID: strings.TrimSpace(row.ParentCommentID),
		IsPinned:         row.IsPinned,
		PinnedBy:         strings.TrimSpace(row.PinnedBy),
		PinnedByName:     strings.TrimSpace(row.PinnedByName),
		CreatedAt:        row.CreatedAt.UTC(),
		HasBreakRoom:     false,
		BreakJoinCount:   0,
	}
}

func parseDiscussionCommentsLimit(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultDiscussionCommentsLimit
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return defaultDiscussionCommentsLimit
	}
	if value > maxDiscussionCommentsLimit {
		return maxDiscussionCommentsLimit
	}
	return value
}

func parseClientTimestamp(raw int64) time.Time {
	if raw <= 0 {
		return time.Time{}
	}
	if raw < 1_000_000_000_000 {
		raw *= 1000
	}
	return time.UnixMilli(raw).UTC()
}

func generateDiscussionCommentID(now time.Time) string {
	nowUTC := now.UTC()
	return fmt.Sprintf("dcm_%d_%09d", nowUTC.UnixMilli(), nowUTC.UnixNano()%1_000_000_000)
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
