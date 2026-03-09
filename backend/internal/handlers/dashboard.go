package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
)

type DashboardHandler struct {
	scylla *database.ScyllaStore
}

type DashboardRoomResponse struct {
	RoomID       string    `json:"room_id"`
	RoomName     string    `json:"room_name"`
	Role         string    `json:"role"`
	LastAccessed time.Time `json:"last_accessed"`
}

func NewDashboardHandler(scyllaStore *database.ScyllaStore) *DashboardHandler {
	return &DashboardHandler{scylla: scyllaStore}
}

func (h *DashboardHandler) GetRooms(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeDashboardError(w, http.StatusServiceUnavailable, "Dashboard storage unavailable")
		return
	}

	userIDRaw := AuthUserIDFromContext(r.Context())
	if userIDRaw == "" {
		writeDashboardError(w, http.StatusUnauthorized, "Authenticated user context is required")
		return
	}

	userID, err := gocql.ParseUUID(userIDRaw)
	if err != nil {
		writeDashboardError(w, http.StatusUnauthorized, "Invalid authenticated user context")
		return
	}

	query := fmt.Sprintf(
		`SELECT room_id, room_name, role, last_accessed FROM %s WHERE user_id = ?`,
		h.scylla.Table("user_rooms"),
	)
	iter := h.scylla.Session.Query(query, userID).WithContext(r.Context()).Iter()

	rooms := make([]DashboardRoomResponse, 0, 8)
	var (
		roomID       gocql.UUID
		roomName     string
		role         string
		lastAccessed time.Time
	)
	for iter.Scan(&roomID, &roomName, &role, &lastAccessed) {
		rooms = append(rooms, DashboardRoomResponse{
			RoomID:       strings.TrimSpace(roomID.String()),
			RoomName:     strings.TrimSpace(roomName),
			Role:         strings.TrimSpace(role),
			LastAccessed: lastAccessed.UTC(),
		})
	}
	if err := iter.Close(); err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "Failed to load dashboard rooms")
		return
	}

	writeDashboardJSON(w, http.StatusOK, rooms)
}

func writeDashboardError(w http.ResponseWriter, code int, message string) {
	writeDashboardJSON(w, code, map[string]string{"error": strings.TrimSpace(message)})
}

func writeDashboardJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
