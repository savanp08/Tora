package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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
	SprintName  string    `json:"sprint_name,omitempty"`
	AssigneeID  string    `json:"assignee_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type TaskCreateRequest struct {
	Content     string `json:"content"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	SprintName  string `json:"sprint_name"`
}

type TaskStatusUpdateRequest struct {
	Status string `json:"status"`
}

func resolveTaskRequesterID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(
		firstNonEmpty(
			AuthUserIDFromContext(r.Context()),
			r.URL.Query().Get("userId"),
			r.URL.Query().Get("user_id"),
			r.Header.Get("X-User-Id"),
		),
	)
}

func resolveTaskRequesterMemberID(r *http.Request) string {
	return normalizeIdentifier(resolveTaskRequesterID(r))
}

func resolveTaskRequesterAssigneeUUID(r *http.Request) *gocql.UUID {
	rawUserID := resolveTaskRequesterID(r)
	if rawUserID == "" {
		return nil
	}
	candidates := []string{rawUserID}
	if strings.Contains(rawUserID, "_") {
		candidates = append(candidates, strings.ReplaceAll(rawUserID, "_", "-"))
	}
	for _, candidate := range candidates {
		parsed, err := parseFlexibleTaskUUID(candidate)
		if err != nil {
			continue
		}
		assigneeID := parsed
		return &assigneeID
	}
	return nil
}

func (h *RoomHandler) ensureTaskRequesterMembership(
	ctx context.Context,
	roomID string,
	requesterID string,
) (bool, error) {
	normalizedRequesterID := normalizeIdentifier(requesterID)
	if normalizedRequesterID == "" {
		return false, nil
	}
	isMember, err := h.isRoomMember(ctx, roomID, normalizedRequesterID)
	if err != nil {
		return false, err
	}
	return isMember, nil
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
		sprint_name text,
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

	alterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD sprint_name text`, tasksTable),
	}
	for _, alterQuery := range alterQueries {
		if err := h.scylla.Session.Query(alterQuery).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
			log.Printf("[task] ensure tasks schema alter failed: %v", err)
		}
	}
}

func (h *RoomHandler) GetRoomTasks(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task storage unavailable"})
		return
	}

	rawRoomID := strings.TrimSpace(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	roomUUID, normalizedRoomID, err := resolveTaskRoomUUID(rawRoomID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	query := fmt.Sprintf(
		`SELECT id, title, description, status, sprint_name, assignee_id, created_at, updated_at FROM %s WHERE room_id = ?`,
		h.scylla.Table("tasks"),
	)
	iter := h.scylla.Session.Query(query, roomUUID).WithContext(r.Context()).Iter()

	tasks := make([]TaskRecordResponse, 0, 64)
	var (
		taskID      gocql.UUID
		title       string
		description string
		status      string
		sprintName  string
		assigneeID  *gocql.UUID
		createdAt   time.Time
		updatedAt   time.Time
	)
	for iter.Scan(&taskID, &title, &description, &status, &sprintName, &assigneeID, &createdAt, &updatedAt) {
		task := TaskRecordResponse{
			ID:          strings.TrimSpace(taskID.String()),
			RoomID:      normalizedRoomID,
			Title:       strings.TrimSpace(title),
			Description: strings.TrimSpace(description),
			Status:      normalizeTaskStatusValue(status),
			SprintName:  strings.TrimSpace(sprintName),
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

func (h *RoomHandler) CreateRoomTask(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task storage unavailable"})
		return
	}

	rawRoomID := strings.TrimSpace(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	roomUUID, normalizedRoomID, err := resolveTaskRoomUUID(rawRoomID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}
	requesterMemberID := resolveTaskRequesterMemberID(r)
	if requesterMemberID != "" {
		isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), normalizedRoomID, requesterMemberID)
		if memberErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
			return
		}
		if !isMember {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to create tasks"})
			return
		}
	}

	var req TaskCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	content := strings.TrimSpace(req.Content)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = content
	}
	if title == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task content or title is required"})
		return
	}
	if len(title) > 240 {
		title = title[:240]
	}

	description := strings.TrimSpace(req.Description)
	if description == "" && content != "" && content != title {
		description = content
	}
	if len(description) > 4000 {
		description = description[:4000]
	}
	status := normalizeTaskStatusValue(req.Status)
	if status == "" {
		status = "todo"
	}
	sprintName := strings.TrimSpace(req.SprintName)
	if len(sprintName) > 160 {
		sprintName = sprintName[:160]
	}

	taskUUID, err := gocql.RandomUUID()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to generate task id"})
		return
	}
	now := time.Now().UTC()

	assigneeID := resolveTaskRequesterAssigneeUUID(r)

	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table("tasks"),
	)
	if err := h.scylla.Session.Query(
		query,
		roomUUID,
		taskUUID,
		title,
		description,
		status,
		sprintName,
		assigneeID,
		now,
		now,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create room task"})
		return
	}

	response := TaskRecordResponse{
		ID:          strings.TrimSpace(taskUUID.String()),
		RoomID:      normalizedRoomID,
		Title:       title,
		Description: description,
		Status:      status,
		SprintName:  sprintName,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if assigneeID != nil {
		response.AssigneeID = strings.TrimSpace(assigneeID.String())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *RoomHandler) UpdateRoomTaskStatus(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task storage unavailable"})
		return
	}

	rawRoomID := strings.TrimSpace(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	roomUUID, normalizedRoomID, err := resolveTaskRoomUUID(rawRoomID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}
	requesterMemberID := resolveTaskRequesterMemberID(r)
	if requesterMemberID != "" {
		isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), normalizedRoomID, requesterMemberID)
		if memberErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
			return
		}
		if !isMember {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to update tasks"})
			return
		}
	}
	taskID, err := parseFlexibleTaskUUID(strings.TrimSpace(chi.URLParam(r, "taskId")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid task id"})
		return
	}

	var req TaskStatusUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}
	status := normalizeTaskStatusValue(req.Status)
	if status == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "status is required"})
		return
	}

	now := time.Now().UTC()
	query := fmt.Sprintf(`UPDATE %s SET status = ?, updated_at = ? WHERE room_id = ? AND id = ?`, h.scylla.Table("tasks"))
	if err := h.scylla.Session.Query(query, status, now, roomUUID, taskID).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update room task status"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (h *RoomHandler) DeleteRoomTasks(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task storage unavailable"})
		return
	}

	roomID := strings.TrimSpace(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}
	requesterMemberID := resolveTaskRequesterMemberID(r)
	if requesterMemberID != "" {
		isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), normalizedRoomID, requesterMemberID)
		if memberErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
			return
		}
		if !isMember {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to clear tasks"})
			return
		}
	}

	if err := h.deleteRoomTasks(r.Context(), normalizedRoomID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete room tasks"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
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

func resolveTaskRoomUUID(raw string) (gocql.UUID, string, error) {
	normalizedRoomID := normalizeRoomID(raw)
	if normalizedRoomID == "" {
		return gocql.UUID{}, "", fmt.Errorf("room id is required")
	}

	if parsed, err := parseFlexibleTaskUUID(strings.TrimSpace(raw)); err == nil {
		return parsed, normalizedRoomID, nil
	}
	if parsed, err := parseFlexibleTaskUUID(normalizedRoomID); err == nil {
		return parsed, normalizedRoomID, nil
	}

	return deterministicTaskRoomUUID(normalizedRoomID), normalizedRoomID, nil
}

func deterministicTaskRoomUUID(normalizedRoomID string) gocql.UUID {
	// Some room IDs (ephemeral) are not UUIDs. Map them deterministically into UUID space
	// so every request for the same room hits the same Scylla partition key.
	digest := sha1.Sum([]byte("converse-task-room:" + normalizedRoomID))
	uuidBytes := make([]byte, 16)
	copy(uuidBytes, digest[:16])
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x50 // RFC 4122 version 5
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80 // RFC 4122 variant
	compact := hex.EncodeToString(uuidBytes)
	formatted := fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		compact[0:8],
		compact[8:12],
		compact[12:16],
		compact[16:20],
		compact[20:32],
	)
	parsed, err := gocql.ParseUUID(formatted)
	if err != nil {
		return gocql.UUID{}
	}
	return parsed
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
	roomUUID, _, err := resolveTaskRoomUUID(roomID)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, h.scylla.Table("tasks"))
	return h.scylla.Session.Query(query, roomUUID).WithContext(ctx).Exec()
}
