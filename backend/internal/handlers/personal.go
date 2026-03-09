package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/repository"
)

type PersonalHandler struct {
	repo *repository.PersonalRepo
}

func NewPersonalHandler(repo *repository.PersonalRepo) *PersonalHandler {
	return &PersonalHandler{repo: repo}
}

type personalStatusUpdateRequest struct {
	Status string `json:"status"`
}

type personalBulkCreateRequest struct {
	Items []models.PersonalItem `json:"items"`
}

const (
	personalTypeTask     = "task"
	personalTypeNote     = "note"
	personalTypeReminder = "reminder"
)

func (h *PersonalHandler) CreateItem(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writePersonalError(w, http.StatusServiceUnavailable, "Personal item storage unavailable")
		return
	}

	userIDRaw := AuthUserIDFromContext(r.Context())
	if userIDRaw == "" {
		writePersonalError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}
	userID, err := gocql.ParseUUID(userIDRaw)
	if err != nil {
		writePersonalError(w, http.StatusUnauthorized, "Invalid authenticated user context")
		return
	}

	var req models.PersonalItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePersonalError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}

	itemID, err := gocql.RandomUUID()
	if err != nil {
		writePersonalError(w, http.StatusInternalServerError, "Failed to generate item id")
		return
	}

	req.UserID = userID
	req.ItemID = itemID
	req.CreatedAt = time.Now().UTC()
	normalized, err := sanitizePersonalItem(req, personalTypeTask)
	if err != nil {
		writePersonalError(w, http.StatusBadRequest, err.Error())
		return
	}
	req = normalized

	if err := h.repo.CreateItem(r.Context(), req); err != nil {
		writePersonalError(w, http.StatusInternalServerError, "Failed to create personal item")
		return
	}

	writePersonalJSON(w, http.StatusCreated, req)
}

func (h *PersonalHandler) CreateItemsBulk(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writePersonalError(w, http.StatusServiceUnavailable, "Personal item storage unavailable")
		return
	}

	userIDRaw := AuthUserIDFromContext(r.Context())
	if userIDRaw == "" {
		writePersonalError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}
	userID, err := gocql.ParseUUID(userIDRaw)
	if err != nil {
		writePersonalError(w, http.StatusUnauthorized, "Invalid authenticated user context")
		return
	}

	var req personalBulkCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePersonalError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	if len(req.Items) == 0 {
		writePersonalError(w, http.StatusBadRequest, "items is required")
		return
	}
	if len(req.Items) > 40 {
		writePersonalError(w, http.StatusBadRequest, "A maximum of 40 items can be created per request")
		return
	}

	created := make([]models.PersonalItem, 0, len(req.Items))
	for index, item := range req.Items {
		itemID, err := gocql.RandomUUID()
		if err != nil {
			writePersonalError(w, http.StatusInternalServerError, "Failed to generate item id")
			return
		}
		item.UserID = userID
		item.ItemID = itemID
		item.CreatedAt = time.Now().UTC()

		normalized, err := sanitizePersonalItem(item, personalTypeTask)
		if err != nil {
			writePersonalError(w, http.StatusBadRequest, fmt.Sprintf("items[%d]: %s", index, err.Error()))
			return
		}
		created = append(created, normalized)
	}

	if err := h.repo.CreateItems(r.Context(), created); err != nil {
		writePersonalError(w, http.StatusInternalServerError, "Failed to create personal items")
		return
	}

	writePersonalJSON(w, http.StatusCreated, created)
}

func (h *PersonalHandler) GetItems(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writePersonalError(w, http.StatusServiceUnavailable, "Personal item storage unavailable")
		return
	}

	userIDRaw := AuthUserIDFromContext(r.Context())
	if userIDRaw == "" {
		writePersonalError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}
	userID, err := gocql.ParseUUID(userIDRaw)
	if err != nil {
		writePersonalError(w, http.StatusUnauthorized, "Invalid authenticated user context")
		return
	}

	items, err := h.repo.GetItemsByUserID(r.Context(), userID)
	if err != nil {
		writePersonalError(w, http.StatusInternalServerError, "Failed to load personal items")
		return
	}

	writePersonalJSON(w, http.StatusOK, items)
}

func (h *PersonalHandler) UpdateItemStatus(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writePersonalError(w, http.StatusServiceUnavailable, "Personal item storage unavailable")
		return
	}

	userIDRaw := AuthUserIDFromContext(r.Context())
	if userIDRaw == "" {
		writePersonalError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}
	userID, err := gocql.ParseUUID(userIDRaw)
	if err != nil {
		writePersonalError(w, http.StatusUnauthorized, "Invalid authenticated user context")
		return
	}

	itemIDRaw := strings.TrimSpace(chi.URLParam(r, "itemId"))
	itemID, err := gocql.ParseUUID(itemIDRaw)
	if err != nil {
		writePersonalError(w, http.StatusBadRequest, "Invalid item id")
		return
	}

	var req personalStatusUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePersonalError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	req.Status = strings.TrimSpace(req.Status)
	if req.Status == "" {
		writePersonalError(w, http.StatusBadRequest, "status is required")
		return
	}
	req.Status = normalizePersonalStatus(req.Status)
	if req.Status == "" {
		writePersonalError(w, http.StatusBadRequest, "Unsupported status value")
		return
	}

	if err := h.repo.UpdateItemStatus(r.Context(), userID, itemID, req.Status); err != nil {
		writePersonalError(w, http.StatusInternalServerError, "Failed to update personal item status")
		return
	}

	writePersonalJSON(w, http.StatusOK, map[string]string{"message": "Status updated"})
}

func (h *PersonalHandler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writePersonalError(w, http.StatusServiceUnavailable, "Personal item storage unavailable")
		return
	}

	userIDRaw := AuthUserIDFromContext(r.Context())
	if userIDRaw == "" {
		writePersonalError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}
	userID, err := gocql.ParseUUID(userIDRaw)
	if err != nil {
		writePersonalError(w, http.StatusUnauthorized, "Invalid authenticated user context")
		return
	}

	itemIDRaw := strings.TrimSpace(chi.URLParam(r, "itemId"))
	itemID, err := gocql.ParseUUID(itemIDRaw)
	if err != nil {
		writePersonalError(w, http.StatusBadRequest, "Invalid item id")
		return
	}

	if err := h.repo.DeleteItem(r.Context(), userID, itemID); err != nil {
		writePersonalError(w, http.StatusInternalServerError, "Failed to delete personal item")
		return
	}

	writePersonalJSON(w, http.StatusOK, map[string]string{"message": "Item deleted"})
}

func writePersonalError(w http.ResponseWriter, code int, message string) {
	writePersonalJSON(w, code, map[string]string{"error": strings.TrimSpace(message)})
}

func writePersonalJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func sanitizePersonalItem(input models.PersonalItem, defaultType string) (models.PersonalItem, error) {
	input.Type = normalizePersonalType(input.Type, defaultType)
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	input.Description = strings.TrimSpace(input.Description)
	input.RepeatRule = normalizePersonalRepeatRule(input.RepeatRule)
	input.Status = normalizePersonalStatus(input.Status)
	if input.Status == "" {
		input.Status = "pending"
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}

	if input.Type == personalTypeTask && input.Title == "" && input.Content != "" {
		input.Title = input.Content
	}
	if input.Content == "" && input.Title != "" {
		input.Content = input.Title
	}
	if input.Content == "" {
		return models.PersonalItem{}, fmt.Errorf("content is required")
	}

	switch input.Type {
	case personalTypeNote:
		input.RepeatRule = ""
		if input.DueAt == nil && input.RemindAt != nil {
			input.DueAt = copyTimePointer(input.RemindAt)
		}
	case personalTypeReminder:
		if input.RemindAt == nil {
			if input.DueAt != nil {
				input.RemindAt = copyTimePointer(input.DueAt)
			} else if input.EndAt != nil {
				input.RemindAt = copyTimePointer(input.EndAt)
			}
		}
		if input.RemindAt == nil {
			return models.PersonalItem{}, fmt.Errorf("remind_at is required for reminders")
		}
		if input.StartAt == nil {
			now := time.Now().UTC()
			input.StartAt = &now
		}
		input.DueAt = copyTimePointer(input.RemindAt)
	case personalTypeTask:
		if input.EndAt == nil && input.DueAt != nil {
			input.EndAt = copyTimePointer(input.DueAt)
		}
		if input.DueAt == nil && input.EndAt != nil {
			input.DueAt = copyTimePointer(input.EndAt)
		}
		if input.DueAt == nil && input.RemindAt != nil {
			input.DueAt = copyTimePointer(input.RemindAt)
		}
	default:
		return models.PersonalItem{}, fmt.Errorf("unsupported item type")
	}

	return input, nil
}

func normalizePersonalType(raw string, fallback string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case personalTypeTask, personalTypeNote, personalTypeReminder:
		return value
	}
	switch strings.ToLower(strings.TrimSpace(fallback)) {
	case personalTypeTask, personalTypeNote, personalTypeReminder:
		return strings.ToLower(strings.TrimSpace(fallback))
	default:
		return personalTypeTask
	}
}

func normalizePersonalStatus(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "pending":
		return "pending"
	case "todo":
		return "pending"
	case "in_progress", "in-progress", "active":
		return "in_progress"
	case "completed", "done":
		return "completed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return ""
	}
}

func normalizePersonalRepeatRule(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "none":
		return ""
	case "daily", "weekly", "monthly", "weekdays":
		return value
	default:
		return ""
	}
}

func copyTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := value.UTC()
	return &copied
}
