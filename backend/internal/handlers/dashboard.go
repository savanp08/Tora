package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
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

type DashboardOverviewResponse struct {
	RecentRooms     []DashboardRoomResponse `json:"recent_rooms"`
	PendingRequests []models.UserConnection `json:"pending_requests"`
	UpcomingItems   []models.PersonalItem   `json:"upcoming_items"`
	AssignedTasks   []models.Task           `json:"assigned_tasks"`
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

func (h *DashboardHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
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

	response := DashboardOverviewResponse{
		RecentRooms:     make([]DashboardRoomResponse, 0),
		PendingRequests: make([]models.UserConnection, 0),
		UpcomingItems:   make([]models.PersonalItem, 0),
		AssignedTasks:   make([]models.Task, 0),
	}

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		loadErrors []error
	)
	recordError := func(step string, loadErr error) {
		if loadErr == nil {
			return
		}
		mu.Lock()
		loadErrors = append(loadErrors, fmt.Errorf("%s: %w", step, loadErr))
		mu.Unlock()
	}
	recordOptionalError := func(step string, loadErr error) {
		if loadErr == nil {
			return
		}
		log.Printf("[dashboard-overview] optional section failed step=%s err=%v", strings.TrimSpace(step), loadErr)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

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
			recordError("load recent rooms", err)
			return
		}

		sort.Slice(rooms, func(i, j int) bool {
			return rooms[i].LastAccessed.After(rooms[j].LastAccessed)
		})
		if len(rooms) > 5 {
			rooms = rooms[:5]
		}

		mu.Lock()
		response.RecentRooms = rooms
		mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		query := fmt.Sprintf(
			`SELECT user_id, target_id, status, created_at FROM %s WHERE target_id = ? AND status = ? ALLOW FILTERING`,
			h.scylla.Table("user_connections"),
		)
		iter := h.scylla.Session.Query(query, userID, "pending").WithContext(r.Context()).Iter()

		requests := make([]models.UserConnection, 0)
		for {
			var connection models.UserConnection
			if !iter.Scan(&connection.UserID, &connection.TargetID, &connection.Status, &connection.CreatedAt) {
				break
			}
			connection.Status = strings.TrimSpace(connection.Status)
			connection.CreatedAt = connection.CreatedAt.UTC()
			requests = append(requests, connection)
		}
		if err := iter.Close(); err != nil {
			recordOptionalError("load pending requests", err)
			return
		}

		mu.Lock()
		response.PendingRequests = requests
		mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		query := fmt.Sprintf(
			`SELECT user_id, item_id, type, content, status, due_at, created_at FROM %s WHERE user_id = ? AND status = ? ALLOW FILTERING`,
			h.scylla.Table("personal_items"),
		)
		iter := h.scylla.Session.Query(query, userID, "pending").WithContext(r.Context()).Iter()

		items := make([]models.PersonalItem, 0)
		for {
			var (
				item      models.PersonalItem
				dueAtRaw  *time.Time
				createdAt time.Time
			)
			if !iter.Scan(&item.UserID, &item.ItemID, &item.Type, &item.Content, &item.Status, &dueAtRaw, &createdAt) {
				break
			}
			item.Type = strings.TrimSpace(item.Type)
			item.Content = strings.TrimSpace(item.Content)
			item.Status = strings.TrimSpace(item.Status)
			item.CreatedAt = createdAt.UTC()
			if dueAtRaw != nil {
				dueAt := dueAtRaw.UTC()
				item.DueAt = &dueAt
			}
			items = append(items, item)
		}
		if err := iter.Close(); err != nil {
			recordOptionalError("load upcoming items", err)
			return
		}

		mu.Lock()
		response.UpcomingItems = items
		mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		query := fmt.Sprintf(
			`SELECT room_id, id, title, description, status, assignee_id, created_at, updated_at FROM %s WHERE assignee_id = ? ALLOW FILTERING`,
			h.scylla.Table("tasks"),
		)
		iter := h.scylla.Session.Query(query, userID).WithContext(r.Context()).Iter()

		tasks := make([]models.Task, 0)
		for {
			var task models.Task
			if !iter.Scan(
				&task.RoomID,
				&task.ID,
				&task.Title,
				&task.Description,
				&task.Status,
				&task.AssigneeID,
				&task.CreatedAt,
				&task.UpdatedAt,
			) {
				break
			}
			task.Title = strings.TrimSpace(task.Title)
			task.Description = strings.TrimSpace(task.Description)
			task.Status = strings.TrimSpace(task.Status)
			task.CreatedAt = task.CreatedAt.UTC()
			task.UpdatedAt = task.UpdatedAt.UTC()
			normalizedStatus := strings.ToLower(task.Status)
			if normalizedStatus == "completed" || normalizedStatus == "done" {
				continue
			}
			tasks = append(tasks, task)
		}
		if err := iter.Close(); err != nil {
			recordOptionalError("load assigned tasks", err)
			return
		}

		mu.Lock()
		response.AssignedTasks = tasks
		mu.Unlock()
	}()

	wg.Wait()
	if len(loadErrors) > 0 {
		for _, loadErr := range loadErrors {
			log.Printf("[dashboard-overview] critical section failed err=%v", loadErr)
		}
	}

	writeDashboardJSON(w, http.StatusOK, response)
}

func writeDashboardError(w http.ResponseWriter, code int, message string) {
	writeDashboardJSON(w, code, map[string]string{"error": strings.TrimSpace(message)})
}

func writeDashboardJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
