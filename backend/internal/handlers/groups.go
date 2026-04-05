package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/savanp08/converse/internal/projectboard"
)

type groupCreateRequest struct {
	Name        string `json:"name"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	Description string `json:"description"`
}

type groupUpdateRequest struct {
	Name         *string `json:"name,omitempty"`
	StartDate    *string `json:"start_date,omitempty"`
	EndDate      *string `json:"end_date,omitempty"`
	Description  *string `json:"description,omitempty"`
	DisplayOrder *int    `json:"display_order,omitempty"`
}

type groupDeleteRequest struct {
	Action            string `json:"action"`
	ReassignToGroupID string `json:"reassign_to_group_id"`
}

func (h *RoomHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	service := projectboard.NewService(h.scylla)
	roomID := resolveGroupRoomID(r)
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "workspace id is required"})
		return
	}

	groups, err := service.ListGroupSummaries(r.Context(), roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load groups"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(groups)
}

func (h *RoomHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	service := projectboard.NewService(h.scylla)
	roomID := resolveGroupRoomID(r)
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "workspace id is required"})
		return
	}

	var req groupCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	group, err := service.CreateGroup(r.Context(), roomID, projectboard.GroupMutation{
		Name:        req.Name,
		StartDate:   req.StartDate,
		EndDate:     req.EndDate,
		Description: req.Description,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(strings.ToLower(err.Error()), "required") || strings.Contains(strings.ToLower(err.Error()), "exists") {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(group)
}

func (h *RoomHandler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	service := projectboard.NewService(h.scylla)
	roomID := resolveGroupRoomID(r)
	groupID := strings.TrimSpace(chi.URLParam(r, "groupId"))
	if roomID == "" || groupID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "workspace id and group id are required"})
		return
	}

	var req groupUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	mutation := projectboard.GroupMutation{DisplayOrder: req.DisplayOrder}
	if req.Name != nil {
		mutation.Name = strings.TrimSpace(*req.Name)
	}
	if req.StartDate != nil {
		mutation.StartDate = strings.TrimSpace(*req.StartDate)
	}
	if req.EndDate != nil {
		mutation.EndDate = strings.TrimSpace(*req.EndDate)
	}
	if req.Description != nil {
		mutation.Description = strings.TrimSpace(*req.Description)
	}

	group, err := service.UpdateGroup(r.Context(), roomID, groupID, mutation)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(strings.ToLower(err.Error()), "exists") {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(group)
}

func (h *RoomHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	service := projectboard.NewService(h.scylla)
	roomID := resolveGroupRoomID(r)
	groupID := strings.TrimSpace(chi.URLParam(r, "groupId"))
	if roomID == "" || groupID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "workspace id and group id are required"})
		return
	}

	var req groupDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	result, err := service.DeleteGroup(r.Context(), roomID, groupID, projectboard.GroupDeleteRequest{
		Action:            req.Action,
		ReassignToGroupID: req.ReassignToGroupID,
	})
	if err != nil {
		status := http.StatusInternalServerError
		errText := strings.ToLower(err.Error())
		switch {
		case strings.Contains(errText, "required"), strings.Contains(errText, "action"):
			status = http.StatusBadRequest
		case strings.Contains(errText, "not found"):
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

func resolveGroupRoomID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return normalizeRoomID(firstNonEmpty(
		chi.URLParam(r, "workspaceId"),
		chi.URLParam(r, "roomId"),
		chi.URLParam(r, "id"),
	))
}
