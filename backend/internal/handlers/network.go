package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/repository"
)

type NetworkHandler struct {
	repo   *repository.NetworkRepo
	scylla *database.ScyllaStore
}

type sendConnectionRequestBody struct {
	TargetUsername string `json:"target_username"`
}

type acceptConnectionRequestBody struct {
	TargetID string `json:"target_id"`
}

func NewNetworkHandler(repo *repository.NetworkRepo, scyllaStore *database.ScyllaStore) *NetworkHandler {
	return &NetworkHandler{
		repo:   repo,
		scylla: scyllaStore,
	}
}

func (h *NetworkHandler) SendConnectionRequest(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil || h.scylla == nil || h.scylla.Session == nil {
		writeNetworkError(w, http.StatusServiceUnavailable, "Network storage unavailable")
		return
	}

	fromID, ok := parseAuthenticatedNetworkUserID(r)
	if !ok {
		writeNetworkError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}

	var req sendConnectionRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeNetworkError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	targetUsername := normalizeNetworkUsername(req.TargetUsername)
	if targetUsername == "" {
		writeNetworkError(w, http.StatusBadRequest, "target_username is required")
		return
	}

	targetID, found, err := h.lookupUserIDByUsername(r, targetUsername)
	if err != nil {
		writeNetworkError(w, http.StatusInternalServerError, "Failed to resolve target user")
		return
	}
	if !found {
		writeNetworkError(w, http.StatusNotFound, "Target user not found")
		return
	}
	if targetID == fromID {
		writeNetworkError(w, http.StatusBadRequest, "Cannot send connection request to self")
		return
	}

	if err := h.repo.SendRequest(r.Context(), fromID, targetID); err != nil {
		writeNetworkError(w, http.StatusInternalServerError, "Failed to send connection request")
		return
	}

	writeNetworkJSON(w, http.StatusCreated, map[string]string{
		"message":   "Connection request sent",
		"target_id": targetID.String(),
	})
}

func (h *NetworkHandler) AcceptConnectionRequest(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writeNetworkError(w, http.StatusServiceUnavailable, "Network storage unavailable")
		return
	}

	userID, ok := parseAuthenticatedNetworkUserID(r)
	if !ok {
		writeNetworkError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}

	var req acceptConnectionRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeNetworkError(w, http.StatusBadRequest, "Invalid JSON format")
		return
	}
	targetID, err := gocql.ParseUUID(strings.TrimSpace(req.TargetID))
	if err != nil {
		writeNetworkError(w, http.StatusBadRequest, "Invalid target_id")
		return
	}
	if targetID == userID {
		writeNetworkError(w, http.StatusBadRequest, "Cannot accept a request from self")
		return
	}

	if err := h.repo.AcceptRequest(r.Context(), userID, targetID); err != nil {
		writeNetworkError(w, http.StatusInternalServerError, "Failed to accept connection request")
		return
	}

	writeNetworkJSON(w, http.StatusOK, map[string]string{
		"message":   "Connection request accepted",
		"target_id": targetID.String(),
	})
}

func (h *NetworkHandler) ListPendingRequests(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writeNetworkError(w, http.StatusServiceUnavailable, "Network storage unavailable")
		return
	}

	targetID, ok := parseAuthenticatedNetworkUserID(r)
	if !ok {
		writeNetworkError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}

	requests, err := h.repo.GetPendingRequests(r.Context(), targetID)
	if err != nil {
		writeNetworkError(w, http.StatusInternalServerError, "Failed to load pending requests")
		return
	}
	writeNetworkJSON(w, http.StatusOK, requests)
}

func (h *NetworkHandler) ListConnections(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repo == nil {
		writeNetworkError(w, http.StatusServiceUnavailable, "Network storage unavailable")
		return
	}

	userID, ok := parseAuthenticatedNetworkUserID(r)
	if !ok {
		writeNetworkError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}

	connections, err := h.repo.GetConnections(r.Context(), userID)
	if err != nil {
		writeNetworkError(w, http.StatusInternalServerError, "Failed to load connections")
		return
	}
	writeNetworkJSON(w, http.StatusOK, connections)
}

func parseAuthenticatedNetworkUserID(r *http.Request) (gocql.UUID, bool) {
	userIDRaw := AuthUserIDFromContext(r.Context())
	if userIDRaw == "" {
		return gocql.UUID{}, false
	}
	userID, err := gocql.ParseUUID(strings.TrimSpace(userIDRaw))
	if err != nil {
		return gocql.UUID{}, false
	}
	return userID, true
}

func (h *NetworkHandler) lookupUserIDByUsername(r *http.Request, normalizedUsername string) (gocql.UUID, bool, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return gocql.UUID{}, false, fmt.Errorf("scylla session is not configured")
	}

	query := fmt.Sprintf(`SELECT user_id FROM %s WHERE username = ? LIMIT 1`, h.scylla.Table("users_by_username"))
	var userID gocql.UUID
	err := h.scylla.Session.Query(query, normalizedUsername).WithContext(r.Context()).Scan(&userID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return gocql.UUID{}, false, nil
		}
		return gocql.UUID{}, false, err
	}
	return userID, true, nil
}

func normalizeNetworkUsername(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	var normalized strings.Builder
	normalized.Grow(len(trimmed))
	lastUnderscore := false

	for _, ch := range trimmed {
		switch {
		case ch >= 'a' && ch <= 'z':
			normalized.WriteRune(ch)
			lastUnderscore = false
		case ch >= 'A' && ch <= 'Z':
			normalized.WriteRune(ch + ('a' - 'A'))
			lastUnderscore = false
		case ch >= '0' && ch <= '9':
			normalized.WriteRune(ch)
			lastUnderscore = false
		case ch == '_' || ch == '-' || ch == ' ':
			if normalized.Len() == 0 || lastUnderscore {
				continue
			}
			normalized.WriteByte('_')
			lastUnderscore = true
		}

		if normalized.Len() >= 32 {
			break
		}
	}

	return strings.Trim(normalized.String(), "_")
}

func writeNetworkError(w http.ResponseWriter, code int, message string) {
	writeNetworkJSON(w, code, map[string]string{"error": strings.TrimSpace(message)})
}

func writeNetworkJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
