package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
)

type TaskRecordResponse struct {
	ID          string    `json:"id"`
	RoomID      string    `json:"room_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	AssigneeID  string    `json:"assignee_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (h *RoomHandler) ensureTaskSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	tasksTable := h.scylla.Table("tasks")
	createQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id uuid,
		id uuid,
		title text,
		description text,
		status text,
		assignee_id uuid,
		created_at timestamp,
		updated_at timestamp,
		PRIMARY KEY ((room_id), id)
	) WITH CLUSTERING ORDER BY (id ASC)`, tasksTable)
	if err := h.scylla.Session.Query(createQuery).Exec(); err != nil {
		log.Printf("[task] ensure tasks schema failed: %v", err)
		return
	}

	indexQuery := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (assignee_id)`, tasksTable)
	if err := h.scylla.Session.Query(indexQuery).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		log.Printf("[task] ensure tasks assignee index failed: %v", err)
	}
}

func (h *RoomHandler) GetRoomTasks(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task storage unavailable"})
		return
	}

	rawRoomID := strings.TrimSpace(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	roomUUID, err := parseFlexibleTaskUUID(rawRoomID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	query := fmt.Sprintf(
		`SELECT id, title, description, status, assignee_id, created_at, updated_at FROM %s WHERE room_id = ?`,
		h.scylla.Table("tasks"),
	)
	iter := h.scylla.Session.Query(query, roomUUID).WithContext(r.Context()).Iter()

	tasks := make([]TaskRecordResponse, 0, 64)
	var (
		taskID      gocql.UUID
		title       string
		description string
		status      string
		assigneeID  *gocql.UUID
		createdAt   time.Time
		updatedAt   time.Time
	)
	for iter.Scan(&taskID, &title, &description, &status, &assigneeID, &createdAt, &updatedAt) {
		task := TaskRecordResponse{
			ID:          strings.TrimSpace(taskID.String()),
			RoomID:      strings.TrimSpace(roomUUID.String()),
			Title:       strings.TrimSpace(title),
			Description: strings.TrimSpace(description),
			Status:      normalizeTaskStatusValue(status),
			CreatedAt:   createdAt.UTC(),
			UpdatedAt:   updatedAt.UTC(),
		}
		if assigneeID != nil {
			task.AssigneeID = strings.TrimSpace(assigneeID.String())
		}
		tasks = append(tasks, task)
	}
	if err := iter.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load room tasks"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(tasks)
}

func parseFlexibleTaskUUID(raw string) (gocql.UUID, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return gocql.UUID{}, fmt.Errorf("uuid value is required")
	}
	if parsed, err := gocql.ParseUUID(normalized); err == nil {
		return parsed, nil
	}
	compact := strings.ReplaceAll(normalized, "-", "")
	if len(compact) != 32 {
		return gocql.UUID{}, fmt.Errorf("invalid uuid value")
	}
	formatted := fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		compact[0:8],
		compact[8:12],
		compact[12:16],
		compact[16:20],
		compact[20:32],
	)
	return gocql.ParseUUID(formatted)
}

func normalizeTaskStatusValue(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "":
		return "todo"
	case "todo", "in_progress", "done":
		return normalized
	default:
		return normalized
	}
}

func (h *RoomHandler) deleteRoomTasks(ctx context.Context, roomID string) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	roomUUID, err := parseFlexibleTaskUUID(roomID)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, h.scylla.Table("tasks"))
	return h.scylla.Session.Query(query, roomUUID).WithContext(ctx).Exec()
}
