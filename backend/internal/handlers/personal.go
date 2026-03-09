package handlers

import (
	"encoding/json"
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
	req.Type = strings.TrimSpace(req.Type)
	req.Content = strings.TrimSpace(req.Content)
	req.Status = strings.TrimSpace(req.Status)
	req.CreatedAt = time.Now().UTC()

	if req.Type == "" {
		req.Type = "task"
	}
	if req.Content == "" {
		writePersonalError(w, http.StatusBadRequest, "content is required")
		return
	}
	if req.Status == "" {
		req.Status = "pending"
	}

	if err := h.repo.CreateItem(r.Context(), req); err != nil {
		writePersonalError(w, http.StatusInternalServerError, "Failed to create personal item")
		return
	}

	writePersonalJSON(w, http.StatusCreated, req)
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
