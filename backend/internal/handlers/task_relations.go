package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
)

const (
	roomTaskRelationsTable  = "task_relations"
	taskRelationTypeBlocked = "blocked_by"
	taskRelationTypeSubtask = "subtask"
)

type taskRelationCreateRequest struct {
	RelationType    string `json:"relation_type"`
	RelationTypeAlt string `json:"relationType"`
	ToTaskID        string `json:"to_task_id"`
	ToTaskIDAlt     string `json:"toTaskId"`
	Content         string `json:"content"`
	Completed       *bool  `json:"completed"`
	Position        *int   `json:"position"`
}

type taskRelationUpdateRequest struct {
	RelationType    *string `json:"relation_type"`
	RelationTypeAlt *string `json:"relationType"`
	Content         *string `json:"content"`
	Completed       *bool   `json:"completed"`
	Position        *int    `json:"position"`
}

type taskRelationRow struct {
	FromTaskID   string
	ToTaskID     string
	RelationType string
	Position     int
	Content      string
	Completed    bool
}

type taskRelationSnapshot struct {
	blockedBy map[string][]string
	blocks    map[string][]string
	subtasks  map[string][]SubtaskItem
}

func (h *RoomHandler) ensureTaskRelationSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	tableName := h.scylla.Table(roomTaskRelationsTable)
	createQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id text,
		from_task_id text,
		to_task_id text,
		relation_type text,
		position int,
		content text,
		completed boolean,
		created_at timestamp,
		PRIMARY KEY (room_id, from_task_id, to_task_id)
	) WITH CLUSTERING ORDER BY (from_task_id ASC, to_task_id ASC)`, tableName)
	if err := h.scylla.Session.Query(createQuery).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		log.Printf("[task-relations] ensure schema failed: %v", err)
		return
	}

	indexQuery := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (to_task_id)`, tableName)
	if err := h.scylla.Session.Query(indexQuery).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		log.Printf("[task-relations] ensure relation target index failed: %v", err)
	}
}

func normalizeTaskRelationType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case taskRelationTypeBlocked, "depends_on", "dependency", "blocks", "blocked":
		return taskRelationTypeBlocked
	case taskRelationTypeSubtask, "subtasks", "checklist":
		return taskRelationTypeSubtask
	default:
		return ""
	}
}

func normalizeTaskRelationIdentifier(raw string) string {
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

func generateSubtaskRelationID() string {
	randomID, err := gocql.RandomUUID()
	if err == nil {
		return strings.TrimSpace(randomID.String())
	}
	return fmt.Sprintf("subtask-%d", time.Now().UTC().UnixNano())
}

func buildTaskRelationLookupCandidates(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	candidates := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	addCandidate := func(candidate string) {
		normalized := strings.TrimSpace(candidate)
		if normalized == "" {
			return
		}
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		candidates = append(candidates, normalized)
	}

	addCandidate(normalizeTaskRelationIdentifier(trimmed))
	if parsedUUID, err := parseFlexibleTaskUUID(trimmed); err == nil {
		addCandidate(strings.TrimSpace(parsedUUID.String()))
	}
	return candidates
}

func cloneSubtaskItems(items []SubtaskItem) []SubtaskItem {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]SubtaskItem, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, SubtaskItem{
			ID:        strings.TrimSpace(item.ID),
			Content:   strings.TrimSpace(item.Content),
			Completed: item.Completed,
			Position:  item.Position,
		})
	}
	return cloned
}

func sortedUniqueTaskRelationIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	next := make([]string, 0, len(ids))
	for _, id := range ids {
		normalizedID := strings.TrimSpace(id)
		if normalizedID == "" {
			continue
		}
		if _, exists := seen[normalizedID]; exists {
			continue
		}
		seen[normalizedID] = struct{}{}
		next = append(next, normalizedID)
	}
	sort.Strings(next)
	if len(next) == 0 {
		return nil
	}
	return next
}

func sortSubtasks(subtasks []SubtaskItem) []SubtaskItem {
	if len(subtasks) == 0 {
		return nil
	}
	next := cloneSubtaskItems(subtasks)
	sort.SliceStable(next, func(i, j int) bool {
		if next[i].Position != next[j].Position {
			return next[i].Position < next[j].Position
		}
		if next[i].Content != next[j].Content {
			return next[i].Content < next[j].Content
		}
		return next[i].ID < next[j].ID
	})
	return next
}

func applyTaskRelationSnapshot(task *TaskRecordResponse, snapshot taskRelationSnapshot) {
	if task == nil {
		return
	}
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		return
	}

	task.BlockedBy = append([]string(nil), snapshot.blockedBy[taskID]...)
	task.Blocks = append([]string(nil), snapshot.blocks[taskID]...)
	task.Subtasks = cloneSubtaskItems(snapshot.subtasks[taskID])
}

func (h *RoomHandler) loadTaskRelationSnapshot(
	ctx context.Context,
	roomID string,
) (taskRelationSnapshot, error) {
	query := fmt.Sprintf(
		`SELECT from_task_id, to_task_id, relation_type, position, content, completed FROM %s WHERE room_id = ?`,
		h.scylla.Table(roomTaskRelationsTable),
	)
	iter := h.scylla.Session.Query(query, roomID).WithContext(ctx).Iter()

	snapshot := taskRelationSnapshot{
		blockedBy: make(map[string][]string),
		blocks:    make(map[string][]string),
		subtasks:  make(map[string][]SubtaskItem),
	}

	var (
		fromTaskID   string
		toTaskID     string
		relationType string
		position     int
		content      string
		completed    bool
	)
	for iter.Scan(&fromTaskID, &toTaskID, &relationType, &position, &content, &completed) {
		fromID := strings.TrimSpace(fromTaskID)
		toID := strings.TrimSpace(toTaskID)
		if fromID == "" || toID == "" {
			continue
		}

		switch normalizeTaskRelationType(relationType) {
		case taskRelationTypeBlocked:
			if fromID == toID {
				continue
			}
			snapshot.blockedBy[fromID] = append(snapshot.blockedBy[fromID], toID)
			snapshot.blocks[toID] = append(snapshot.blocks[toID], fromID)
		case taskRelationTypeSubtask:
			subtaskContent := strings.TrimSpace(content)
			if subtaskContent == "" {
				subtaskContent = "Subtask"
			}
			snapshot.subtasks[fromID] = append(snapshot.subtasks[fromID], SubtaskItem{
				ID:        toID,
				Content:   subtaskContent,
				Completed: completed,
				Position:  position,
			})
		}
	}
	if err := iter.Close(); err != nil {
		return snapshot, err
	}

	for taskID, relationIDs := range snapshot.blockedBy {
		snapshot.blockedBy[taskID] = sortedUniqueTaskRelationIDs(relationIDs)
	}
	for taskID, relationIDs := range snapshot.blocks {
		snapshot.blocks[taskID] = sortedUniqueTaskRelationIDs(relationIDs)
	}
	for taskID, subtasks := range snapshot.subtasks {
		snapshot.subtasks[taskID] = sortSubtasks(subtasks)
	}

	return snapshot, nil
}

func (h *RoomHandler) enrichTaskRecordsWithRelations(
	ctx context.Context,
	roomID string,
	tasks []TaskRecordResponse,
) error {
	if len(tasks) == 0 {
		return nil
	}

	snapshot, err := h.loadTaskRelationSnapshot(ctx, roomID)
	if err != nil {
		return err
	}
	for index := range tasks {
		applyTaskRelationSnapshot(&tasks[index], snapshot)
	}
	return nil
}

func (h *RoomHandler) enrichSingleTaskRecordWithRelations(
	ctx context.Context,
	roomID string,
	task TaskRecordResponse,
) (TaskRecordResponse, error) {
	snapshot, err := h.loadTaskRelationSnapshot(ctx, roomID)
	if err != nil {
		return task, err
	}
	applyTaskRelationSnapshot(&task, snapshot)
	return task, nil
}

func (h *RoomHandler) loadSingleRoomTaskRecord(
	ctx context.Context,
	roomUUID gocql.UUID,
	normalizedRoomID string,
	taskID gocql.UUID,
) (TaskRecordResponse, error) {
	selectQuery := fmt.Sprintf(
		`SELECT id, title, description, status, custom_fields, sprint_name, assignee_id, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at, task_type, due_date, start_date, roles FROM %s WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
	)
	var (
		foundTaskID     gocql.UUID
		title           string
		description     string
		status          string
		customFieldsRaw *string
		sprintName      string
		assigneeID      *gocql.UUID
		statusActorID   string
		statusActorName string
		statusChangedAt *time.Time
		createdAt       time.Time
		updatedAt       time.Time
		taskType        string
		dueDate         *time.Time
		startDate       *time.Time
		rolesRaw        *string
	)
	if err := h.scylla.Session.Query(selectQuery, roomUUID, taskID).WithContext(ctx).Scan(
		&foundTaskID,
		&title,
		&description,
		&status,
		&customFieldsRaw,
		&sprintName,
		&assigneeID,
		&statusActorID,
		&statusActorName,
		&statusChangedAt,
		&createdAt,
		&updatedAt,
		&taskType,
		&dueDate,
		&startDate,
		&rolesRaw,
	); err != nil {
		return TaskRecordResponse{}, err
	}

	response := TaskRecordResponse{
		ID:              strings.TrimSpace(foundTaskID.String()),
		RoomID:          normalizedRoomID,
		Title:           strings.TrimSpace(title),
		Description:     strings.TrimSpace(description),
		Status:          normalizeTaskStatusValue(status),
		TaskType:        normalizeTaskTypeValue(taskType),
		CustomFields:    parseTaskCustomFieldsFromNullableString(customFieldsRaw),
		SprintName:      strings.TrimSpace(sprintName),
		StatusActorID:   strings.TrimSpace(statusActorID),
		StatusActorName: strings.TrimSpace(statusActorName),
		CreatedAt:       createdAt.UTC(),
		UpdatedAt:       updatedAt.UTC(),
		Roles:           parseTaskRoles(rolesRaw),
	}
	response.Budget = extractTaskBudget(response.Description)
	response.ActualCost = extractTaskActualCost(response.Description)
	if assigneeID != nil {
		response.AssigneeID = strings.TrimSpace(assigneeID.String())
	}
	if statusChangedAt != nil && !statusChangedAt.IsZero() {
		statusChangedAtUTC := statusChangedAt.UTC()
		response.StatusChangedAt = &statusChangedAtUTC
	}
	if dueDate != nil && !dueDate.IsZero() {
		dueDateUTC := dueDate.UTC()
		response.DueDate = &dueDateUTC
	}
	if startDate != nil && !startDate.IsZero() {
		startDateUTC := startDate.UTC()
		response.StartDate = &startDateUTC
	}
	return response, nil
}

func (h *RoomHandler) loadTaskRecordWithRelations(
	ctx context.Context,
	roomUUID gocql.UUID,
	normalizedRoomID string,
	taskID gocql.UUID,
) (TaskRecordResponse, error) {
	task, err := h.loadSingleRoomTaskRecord(ctx, roomUUID, normalizedRoomID, taskID)
	if err != nil {
		return TaskRecordResponse{}, err
	}
	return h.enrichSingleTaskRecordWithRelations(ctx, normalizedRoomID, task)
}

func (h *RoomHandler) loadTaskRelationRow(
	ctx context.Context,
	roomID string,
	fromTaskID string,
	toTaskID string,
) (taskRelationRow, error) {
	query := fmt.Sprintf(
		`SELECT relation_type, position, content, completed FROM %s WHERE room_id = ? AND from_task_id = ? AND to_task_id = ? LIMIT 1`,
		h.scylla.Table(roomTaskRelationsTable),
	)
	var (
		relationType string
		position     int
		content      string
		completed    bool
	)
	if err := h.scylla.Session.Query(query, roomID, fromTaskID, toTaskID).WithContext(ctx).Scan(
		&relationType,
		&position,
		&content,
		&completed,
	); err != nil {
		return taskRelationRow{}, err
	}
	return taskRelationRow{
		FromTaskID:   fromTaskID,
		ToTaskID:     toTaskID,
		RelationType: normalizeTaskRelationType(relationType),
		Position:     position,
		Content:      strings.TrimSpace(content),
		Completed:    completed,
	}, nil
}

func (h *RoomHandler) nextTaskSubtaskPosition(
	ctx context.Context,
	roomID string,
	fromTaskID string,
) (int, error) {
	query := fmt.Sprintf(
		`SELECT relation_type, position FROM %s WHERE room_id = ? AND from_task_id = ?`,
		h.scylla.Table(roomTaskRelationsTable),
	)
	iter := h.scylla.Session.Query(query, roomID, fromTaskID).WithContext(ctx).Iter()
	maxPosition := -1
	var (
		relationType string
		position     int
	)
	for iter.Scan(&relationType, &position) {
		if normalizeTaskRelationType(relationType) != taskRelationTypeSubtask {
			continue
		}
		if position > maxPosition {
			maxPosition = position
		}
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}
	return maxPosition + 1, nil
}

func (h *RoomHandler) deleteTaskRelationsForTask(
	ctx context.Context,
	roomID string,
	taskID string,
) error {
	if roomID == "" || taskID == "" {
		return nil
	}

	tableName := h.scylla.Table(roomTaskRelationsTable)
	deleteOutgoingQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND from_task_id = ?`, tableName)
	if err := h.scylla.Session.Query(deleteOutgoingQuery, roomID, taskID).WithContext(ctx).Exec(); err != nil {
		return err
	}

	selectQuery := fmt.Sprintf(
		`SELECT from_task_id, to_task_id, relation_type FROM %s WHERE room_id = ?`,
		tableName,
	)
	iter := h.scylla.Session.Query(selectQuery, roomID).WithContext(ctx).Iter()
	type relationKey struct {
		fromTaskID string
		toTaskID   string
	}
	toDelete := make([]relationKey, 0, 8)
	var (
		fromTaskID   string
		toTaskID     string
		relationType string
	)
	for iter.Scan(&fromTaskID, &toTaskID, &relationType) {
		if normalizeTaskRelationType(relationType) != taskRelationTypeBlocked {
			continue
		}
		if strings.TrimSpace(toTaskID) != taskID {
			continue
		}
		toDelete = append(toDelete, relationKey{
			fromTaskID: strings.TrimSpace(fromTaskID),
			toTaskID:   strings.TrimSpace(toTaskID),
		})
	}
	if err := iter.Close(); err != nil {
		return err
	}
	if len(toDelete) == 0 {
		return nil
	}

	deleteRelationQuery := fmt.Sprintf(
		`DELETE FROM %s WHERE room_id = ? AND from_task_id = ? AND to_task_id = ?`,
		tableName,
	)
	for _, key := range toDelete {
		if key.fromTaskID == "" || key.toTaskID == "" {
			continue
		}
		if err := h.scylla.Session.Query(
			deleteRelationQuery,
			roomID,
			key.fromTaskID,
			key.toTaskID,
		).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}

	return nil
}

func (h *RoomHandler) deleteRoomTaskRelations(ctx context.Context, roomID string) error {
	if roomID == "" {
		return nil
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, h.scylla.Table(roomTaskRelationsTable))
	return h.scylla.Session.Query(query, roomID).WithContext(ctx).Exec()
}

func (h *RoomHandler) broadcastTaskRelationUpdate(
	roomID string,
	action string,
	task TaskRecordResponse,
) {
	h.broadcastRoomEvent(roomID, "task_relation_update", map[string]interface{}{
		"action":  strings.ToLower(strings.TrimSpace(action)),
		"task_id": task.ID,
		"taskId":  task.ID,
		"id":      task.ID,
		"task":    task,
		"payload": map[string]interface{}{
			"task_id": task.ID,
			"taskId":  task.ID,
			"id":      task.ID,
			"task":    task,
		},
	})
}

func (h *RoomHandler) CreateRoomTaskRelation(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task relation storage unavailable"})
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
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to edit task relations"})
			return
		}
	}

	taskUUID, err := parseFlexibleTaskUUID(strings.TrimSpace(chi.URLParam(r, "taskId")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid task id"})
		return
	}
	if _, err := h.loadSingleRoomTaskRecord(r.Context(), roomUUID, normalizedRoomID, taskUUID); err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load task"})
		return
	}

	var req taskRelationCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	relationType := normalizeTaskRelationType(firstNonEmpty(req.RelationType, req.RelationTypeAlt))
	if relationType == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "relation_type is required"})
		return
	}

	fromTaskID := strings.TrimSpace(taskUUID.String())
	targetID := ""
	content := ""
	completed := false
	position := 0

	switch relationType {
	case taskRelationTypeBlocked:
		blockingTaskID := strings.TrimSpace(firstNonEmpty(req.ToTaskID, req.ToTaskIDAlt))
		blockingTaskUUID, parseErr := parseFlexibleTaskUUID(blockingTaskID)
		if parseErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "to_task_id must be a valid task id"})
			return
		}
		if blockingTaskUUID == taskUUID {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task cannot block itself"})
			return
		}
		if _, err := h.loadSingleRoomTaskRecord(
			r.Context(),
			roomUUID,
			normalizedRoomID,
			blockingTaskUUID,
		); err != nil {
			if err == gocql.ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Blocking task not found"})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to validate blocking task"})
			return
		}
		targetID = strings.TrimSpace(blockingTaskUUID.String())
	case taskRelationTypeSubtask:
		if req.Position != nil {
			if *req.Position < 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "position must be >= 0"})
				return
			}
			position = *req.Position
		} else {
			nextPosition, nextErr := h.nextTaskSubtaskPosition(r.Context(), normalizedRoomID, fromTaskID)
			if nextErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to determine subtask position"})
				return
			}
			position = nextPosition
		}
		content = strings.TrimSpace(req.Content)
		if len(content) > 400 {
			content = content[:400]
		}
		if content == "" {
			content = "Subtask"
		}
		if req.Completed != nil {
			completed = *req.Completed
		}
		targetID = normalizeTaskRelationIdentifier(firstNonEmpty(req.ToTaskID, req.ToTaskIDAlt))
		if targetID == "" {
			targetID = generateSubtaskRelationID()
		}
	}

	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, from_task_id, to_task_id, relation_type, position, content, completed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table(roomTaskRelationsTable),
	)
	if err := h.scylla.Session.Query(
		insertQuery,
		normalizedRoomID,
		fromTaskID,
		targetID,
		relationType,
		position,
		nullableTrimmedText(content),
		completed,
		time.Now().UTC(),
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create task relation"})
		return
	}

	updatedTask, err := h.loadTaskRecordWithRelations(r.Context(), roomUUID, normalizedRoomID, taskUUID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load updated task"})
		return
	}
	h.broadcastTaskRelationUpdate(normalizedRoomID, "created", updatedTask)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(updatedTask)
}

func (h *RoomHandler) UpdateRoomTaskRelation(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task relation storage unavailable"})
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
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to edit task relations"})
			return
		}
	}

	taskUUID, err := parseFlexibleTaskUUID(strings.TrimSpace(chi.URLParam(r, "taskId")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid task id"})
		return
	}
	if _, err := h.loadSingleRoomTaskRecord(r.Context(), roomUUID, normalizedRoomID, taskUUID); err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load task"})
		return
	}

	fromTaskID := strings.TrimSpace(taskUUID.String())
	relationCandidates := buildTaskRelationLookupCandidates(chi.URLParam(r, "toTaskId"))
	if len(relationCandidates) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid relation target id"})
		return
	}

	existing := taskRelationRow{}
	existingFound := false
	for _, candidate := range relationCandidates {
		row, rowErr := h.loadTaskRelationRow(r.Context(), normalizedRoomID, fromTaskID, candidate)
		if rowErr == nil {
			existing = row
			existingFound = true
			break
		}
		if rowErr != gocql.ErrNotFound {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load task relation"})
			return
		}
	}
	if !existingFound {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task relation not found"})
		return
	}

	var req taskRelationUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	nextRelationType := existing.RelationType
	if req.RelationType != nil {
		nextRelationType = normalizeTaskRelationType(*req.RelationType)
	} else if req.RelationTypeAlt != nil {
		nextRelationType = normalizeTaskRelationType(*req.RelationTypeAlt)
	}
	if nextRelationType == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unsupported relation_type"})
		return
	}

	nextPosition := existing.Position
	if req.Position != nil {
		if *req.Position < 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "position must be >= 0"})
			return
		}
		nextPosition = *req.Position
	}

	nextContent := strings.TrimSpace(existing.Content)
	if req.Content != nil {
		nextContent = strings.TrimSpace(*req.Content)
	}
	if len(nextContent) > 400 {
		nextContent = nextContent[:400]
	}

	nextCompleted := existing.Completed
	if req.Completed != nil {
		nextCompleted = *req.Completed
	}

	if nextRelationType == taskRelationTypeBlocked {
		if _, parseErr := parseFlexibleTaskUUID(existing.ToTaskID); parseErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Blocked relation target must be a valid task id"})
			return
		}
		if strings.TrimSpace(existing.ToTaskID) == fromTaskID {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task cannot block itself"})
			return
		}
		nextPosition = 0
		nextContent = ""
		nextCompleted = false
	} else if nextContent == "" {
		nextContent = "Subtask"
	}

	if nextRelationType == existing.RelationType &&
		nextPosition == existing.Position &&
		nextContent == strings.TrimSpace(existing.Content) &&
		nextCompleted == existing.Completed {
		updatedTask, loadErr := h.loadTaskRecordWithRelations(
			r.Context(),
			roomUUID,
			normalizedRoomID,
			taskUUID,
		)
		if loadErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load updated task"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updatedTask)
		return
	}

	updateQuery := fmt.Sprintf(
		`UPDATE %s SET relation_type = ?, position = ?, content = ?, completed = ? WHERE room_id = ? AND from_task_id = ? AND to_task_id = ?`,
		h.scylla.Table(roomTaskRelationsTable),
	)
	if err := h.scylla.Session.Query(
		updateQuery,
		nextRelationType,
		nextPosition,
		nullableTrimmedText(nextContent),
		nextCompleted,
		normalizedRoomID,
		existing.FromTaskID,
		existing.ToTaskID,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update task relation"})
		return
	}

	updatedTask, err := h.loadTaskRecordWithRelations(r.Context(), roomUUID, normalizedRoomID, taskUUID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load updated task"})
		return
	}
	h.broadcastTaskRelationUpdate(normalizedRoomID, "updated", updatedTask)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(updatedTask)
}

func (h *RoomHandler) DeleteRoomTaskRelation(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task relation storage unavailable"})
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
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to edit task relations"})
			return
		}
	}

	taskUUID, err := parseFlexibleTaskUUID(strings.TrimSpace(chi.URLParam(r, "taskId")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid task id"})
		return
	}
	if _, err := h.loadSingleRoomTaskRecord(r.Context(), roomUUID, normalizedRoomID, taskUUID); err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load task"})
		return
	}

	fromTaskID := strings.TrimSpace(taskUUID.String())
	relationCandidates := buildTaskRelationLookupCandidates(chi.URLParam(r, "toTaskId"))
	if len(relationCandidates) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid relation target id"})
		return
	}

	existing := taskRelationRow{}
	existingFound := false
	for _, candidate := range relationCandidates {
		row, rowErr := h.loadTaskRelationRow(r.Context(), normalizedRoomID, fromTaskID, candidate)
		if rowErr == nil {
			existing = row
			existingFound = true
			break
		}
		if rowErr != gocql.ErrNotFound {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load task relation"})
			return
		}
	}
	if !existingFound {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task relation not found"})
		return
	}

	deleteQuery := fmt.Sprintf(
		`DELETE FROM %s WHERE room_id = ? AND from_task_id = ? AND to_task_id = ?`,
		h.scylla.Table(roomTaskRelationsTable),
	)
	if err := h.scylla.Session.Query(
		deleteQuery,
		normalizedRoomID,
		existing.FromTaskID,
		existing.ToTaskID,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete task relation"})
		return
	}

	updatedTask, err := h.loadTaskRecordWithRelations(r.Context(), roomUUID, normalizedRoomID, taskUUID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load updated task"})
		return
	}
	h.broadcastTaskRelationUpdate(normalizedRoomID, "deleted", updatedTask)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(updatedTask)
}
