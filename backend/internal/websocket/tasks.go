package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gocql/gocql"
)

type TaskPayload struct {
	Type    string                 `json:"type"`
	RoomID  string                 `json:"roomId"`
	Task    TaskPayloadTask        `json:"task"`
	Payload map[string]interface{} `json:"payload,omitempty"`
	Raw     map[string]interface{} `json:"-"`
}

type TaskPayloadTask struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	AssigneeID  string    `json:"assignee_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func isTaskEventType(eventType string) bool {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "task_create", "task_update", "task_delete", "task_move":
		return true
	default:
		return false
	}
}

func parseTaskPayload(raw []byte) (TaskPayload, bool) {
	if len(raw) == 0 {
		return TaskPayload{}, false
	}

	var envelopeMap map[string]interface{}
	if err := json.Unmarshal(raw, &envelopeMap); err != nil {
		return TaskPayload{}, false
	}

	taskType := strings.ToLower(strings.TrimSpace(readStringFromMap(envelopeMap, "type")))
	if !isTaskEventType(taskType) {
		return TaskPayload{}, false
	}

	roomID := normalizeRoomID(readStringFromMap(envelopeMap, "roomId", "room_id"))
	if roomID == "" {
		if payloadMap, ok := envelopeMap["payload"].(map[string]interface{}); ok {
			roomID = normalizeRoomID(readStringFromMap(payloadMap, "roomId", "room_id"))
		}
	}
	if roomID == "" {
		return TaskPayload{}, false
	}

	taskMap := resolveTaskMap(envelopeMap)
	taskID := normalizeTaskIdentifier(readStringFromMap(taskMap, "id", "taskId", "task_id"))
	if taskID == "" {
		taskID = normalizeTaskIdentifier(readStringFromMap(envelopeMap, "id", "taskId", "task_id"))
	}
	if taskID == "" {
		return TaskPayload{}, false
	}

	title := strings.TrimSpace(readStringFromMap(taskMap, "title"))
	description := strings.TrimSpace(readStringFromMap(taskMap, "description"))
	status := normalizeTaskStatus(readStringFromMap(taskMap, "status"))
	if status == "" {
		status = normalizeTaskStatus(readStringFromMap(envelopeMap, "status"))
	}
	assigneeID := normalizeTaskIdentifier(readStringFromMap(taskMap, "assigneeId", "assignee_id"))
	if assigneeID == "" {
		assigneeID = normalizeTaskIdentifier(readStringFromMap(envelopeMap, "assigneeId", "assignee_id"))
	}
	createdAt := parseTimestampFromMap(taskMap, "createdAt", "created_at")
	if createdAt.IsZero() {
		createdAt = parseTimestampFromMap(envelopeMap, "createdAt", "created_at")
	}
	updatedAt := parseTimestampFromMap(taskMap, "updatedAt", "updated_at")
	if updatedAt.IsZero() {
		updatedAt = parseTimestampFromMap(envelopeMap, "updatedAt", "updated_at")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if createdAt.IsZero() {
		createdAt = updatedAt
	}

	if taskType == "task_create" && title == "" {
		title = "Untitled Task"
	}

	payloadCopy := map[string]interface{}{}
	if payloadMap, ok := envelopeMap["payload"].(map[string]interface{}); ok && payloadMap != nil {
		for key, value := range payloadMap {
			payloadCopy[key] = value
		}
	}

	return TaskPayload{
		Type:   taskType,
		RoomID: roomID,
		Task: TaskPayloadTask{
			ID:          taskID,
			Title:       title,
			Description: description,
			Status:      status,
			AssigneeID:  assigneeID,
			CreatedAt:   createdAt.UTC(),
			UpdatedAt:   updatedAt.UTC(),
		},
		Payload: payloadCopy,
		Raw:     envelopeMap,
	}, true
}

func resolveTaskMap(envelopeMap map[string]interface{}) map[string]interface{} {
	if taskMap, ok := envelopeMap["task"].(map[string]interface{}); ok && taskMap != nil {
		return taskMap
	}
	if payloadMap, ok := envelopeMap["payload"].(map[string]interface{}); ok && payloadMap != nil {
		if taskMap, ok := payloadMap["task"].(map[string]interface{}); ok && taskMap != nil {
			return taskMap
		}
		return payloadMap
	}
	return envelopeMap
}

func normalizeTaskStatus(raw string) string {
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

func normalizeTaskIdentifier(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range trimmed {
		if (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' ||
			ch == '_' {
			builder.WriteRune(ch)
		}
	}
	return strings.TrimSpace(builder.String())
}

func parseTimestampFromMap(source map[string]interface{}, keys ...string) time.Time {
	for _, key := range keys {
		value, ok := source[key]
		if !ok {
			continue
		}
		parsed := parseBoardTimestamp(value)
		if !parsed.IsZero() {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func parseFlexibleUUID(raw string) (gocql.UUID, error) {
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

func (s *MessageService) UpsertTaskPayload(ctx context.Context, payload TaskPayload) error {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	roomUUID, err := parseFlexibleUUID(payload.RoomID)
	if err != nil {
		return fmt.Errorf("invalid room id: %w", err)
	}
	taskUUID, err := parseFlexibleUUID(payload.Task.ID)
	if err != nil {
		return fmt.Errorf("invalid task id: %w", err)
	}

	now := time.Now().UTC()
	createdAt := payload.Task.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := payload.Task.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = now
	}

	var assigneeUUID interface{}
	if assignee := strings.TrimSpace(payload.Task.AssigneeID); assignee != "" {
		parsedAssigneeUUID, parseErr := parseFlexibleUUID(assignee)
		if parseErr != nil {
			return fmt.Errorf("invalid assignee id: %w", parseErr)
		}
		assigneeUUID = parsedAssigneeUUID
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, assignee_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Scylla.Table("tasks"),
	)
	return s.Scylla.Session.Query(
		query,
		roomUUID,
		taskUUID,
		strings.TrimSpace(payload.Task.Title),
		strings.TrimSpace(payload.Task.Description),
		normalizeTaskStatus(payload.Task.Status),
		assigneeUUID,
		createdAt,
		updatedAt,
	).WithContext(ctx).Exec()
}

func (s *MessageService) DeleteTaskPayload(ctx context.Context, payload TaskPayload) error {
	if s == nil || s.Scylla == nil || s.Scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	roomUUID, err := parseFlexibleUUID(payload.RoomID)
	if err != nil {
		return fmt.Errorf("invalid room id: %w", err)
	}
	taskUUID, err := parseFlexibleUUID(payload.Task.ID)
	if err != nil {
		return fmt.Errorf("invalid task id: %w", err)
	}

	query := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND id = ?`, s.Scylla.Table("tasks"))
	return s.Scylla.Session.Query(query, roomUUID, taskUUID).WithContext(ctx).Exec()
}
