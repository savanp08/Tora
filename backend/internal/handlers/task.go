package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
)

type TaskRecordResponse struct {
	ID              string     `json:"id"`
	RoomID          string     `json:"room_id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	Budget          *float64   `json:"budget,omitempty"`
	ActualCost      *float64   `json:"actual_cost,omitempty"`
	SprintName      string     `json:"sprint_name,omitempty"`
	AssigneeID      string     `json:"assignee_id,omitempty"`
	StatusActorID   string     `json:"status_actor_id,omitempty"`
	StatusActorName string     `json:"status_actor_name,omitempty"`
	StatusChangedAt *time.Time `json:"status_changed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type TaskCreateRequest struct {
	Content       string   `json:"content"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Status        string   `json:"status"`
	SprintName    string   `json:"sprint_name"`
	Budget        *float64 `json:"budget,omitempty"`
	ActualCost    *float64 `json:"actual_cost,omitempty"`
	ActualCostAlt *float64 `json:"actualCost,omitempty"`
	Spent         *float64 `json:"spent,omitempty"`
	SpentCost     *float64 `json:"spent_cost,omitempty"`
	SpentCostAlt  *float64 `json:"spentCost,omitempty"`
}

type TaskStatusUpdateRequest struct {
	Status string `json:"status"`
}

type TaskUpdateRequest struct {
	Title         *string  `json:"title"`
	Description   *string  `json:"description"`
	Budget        *float64 `json:"budget,omitempty"`
	ActualCost    *float64 `json:"actual_cost,omitempty"`
	ActualCostAlt *float64 `json:"actualCost,omitempty"`
	Spent         *float64 `json:"spent,omitempty"`
	SpentCost     *float64 `json:"spent_cost,omitempty"`
	SpentCostAlt  *float64 `json:"spentCost,omitempty"`
	SprintName    *string  `json:"sprint_name"`
	SprintNameAlt *string  `json:"sprintName"`
	AssigneeID    *string  `json:"assignee_id"`
	AssigneeIDAlt *string  `json:"assigneeId"`
}

type taskMetadataEntry struct {
	key string
	raw string
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

func resolveTaskRequesterName(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(
		firstNonEmpty(
			r.URL.Query().Get("username"),
			r.URL.Query().Get("userName"),
			r.URL.Query().Get("user_name"),
			r.Header.Get("X-User-Name"),
			r.Header.Get("X-Username"),
		),
	)
}

func nullableTrimmedText(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func parseTaskMetadataEntries(description string) (string, []taskMetadataEntry) {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return "", nil
	}

	lastOpen := strings.LastIndex(trimmed, "[")
	lastClose := strings.LastIndex(trimmed, "]")
	if lastOpen < 0 || lastClose < lastOpen || strings.TrimSpace(trimmed[lastClose+1:]) != "" {
		return trimmed, nil
	}

	baseDescription := strings.TrimSpace(trimmed[:lastOpen])
	metadataBody := strings.TrimSpace(trimmed[lastOpen+1 : lastClose])
	if metadataBody == "" {
		return baseDescription, nil
	}
	if !strings.Contains(metadataBody, ":") {
		return trimmed, nil
	}

	sections := strings.Split(metadataBody, "|")
	entries := make([]taskMetadataEntry, 0, len(sections))
	for _, section := range sections {
		raw := strings.TrimSpace(section)
		if raw == "" {
			continue
		}
		key := raw
		if idx := strings.Index(raw, ":"); idx >= 0 {
			key = raw[:idx]
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		entries = append(entries, taskMetadataEntry{
			key: key,
			raw: raw,
		})
	}
	return baseDescription, entries
}

func parseTaskBudgetValue(raw string) (float64, bool) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return 0, false
	}
	normalized = strings.ReplaceAll(normalized, ",", "")
	normalized = strings.ReplaceAll(normalized, "$", "")
	normalized = strings.ReplaceAll(normalized, "USD", "")
	parts := strings.Fields(normalized)
	if len(parts) > 0 {
		normalized = parts[0]
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(normalized), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func formatTaskBudgetValue(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return "0"
	}
	formatted := strconv.FormatFloat(value, 'f', 2, 64)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "" {
		return "0"
	}
	return formatted
}

func isTaskCostMetadataKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return normalized == "actual cost" || normalized == "actual_cost" || normalized == "spent" || normalized == "cost"
}

func firstTaskFinancialValue(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func applyTaskFinancialsToDescription(description string, budget *float64, actualCost *float64) string {
	baseDescription, entries := parseTaskMetadataEntries(description)
	metadataParts := make([]string, 0, len(entries)+2)
	for _, entry := range entries {
		if entry.key == "budget" || isTaskCostMetadataKey(entry.key) {
			continue
		}
		metadataParts = append(metadataParts, entry.raw)
	}

	if budget != nil && !math.IsNaN(*budget) && !math.IsInf(*budget, 0) && *budget > 0 {
		metadataParts = append(metadataParts, fmt.Sprintf("Budget: $%s", formatTaskBudgetValue(*budget)))
	}
	if actualCost != nil && !math.IsNaN(*actualCost) && !math.IsInf(*actualCost, 0) && *actualCost >= 0 {
		metadataParts = append(metadataParts, fmt.Sprintf("Spent: $%s", formatTaskBudgetValue(*actualCost)))
	}

	if len(metadataParts) == 0 {
		return strings.TrimSpace(baseDescription)
	}

	metadataBlock := "[" + strings.Join(metadataParts, " | ") + "]"
	if strings.TrimSpace(baseDescription) == "" {
		return metadataBlock
	}
	return strings.TrimSpace(baseDescription) + "\n\n" + metadataBlock
}

func applyTaskBudgetToDescription(description string, budget *float64) string {
	return applyTaskFinancialsToDescription(description, budget, nil)
}

func extractTaskBudget(description string) *float64 {
	_, entries := parseTaskMetadataEntries(description)
	for _, entry := range entries {
		if entry.key != "budget" {
			continue
		}
		valuePortion := entry.raw
		if idx := strings.Index(valuePortion, ":"); idx >= 0 {
			valuePortion = valuePortion[idx+1:]
		}
		parsed, ok := parseTaskBudgetValue(valuePortion)
		if !ok {
			continue
		}
		budget := parsed
		return &budget
	}
	return nil
}

func extractTaskActualCost(description string) *float64 {
	_, entries := parseTaskMetadataEntries(description)
	for _, entry := range entries {
		if !isTaskCostMetadataKey(entry.key) {
			continue
		}
		valuePortion := entry.raw
		if idx := strings.Index(valuePortion, ":"); idx >= 0 {
			valuePortion = valuePortion[idx+1:]
		}
		parsed, ok := parseTaskBudgetValue(valuePortion)
		if !ok {
			continue
		}
		actualCost := parsed
		return &actualCost
	}
	return nil
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
		status_actor_id text,
		status_actor_name text,
		status_changed_at timestamp,
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
		fmt.Sprintf(`ALTER TABLE %s ADD status_actor_id text`, tasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD status_actor_name text`, tasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD status_changed_at timestamp`, tasksTable),
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
		`SELECT id, title, description, status, sprint_name, assignee_id, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at FROM %s WHERE room_id = ?`,
		h.scylla.Table("tasks"),
	)
	iter := h.scylla.Session.Query(query, roomUUID).WithContext(r.Context()).Iter()

	tasks := make([]TaskRecordResponse, 0, 64)
	var (
		taskID          gocql.UUID
		title           string
		description     string
		status          string
		sprintName      string
		assigneeID      *gocql.UUID
		statusActorID   string
		statusActorName string
		statusChangedAt *time.Time
		createdAt       time.Time
		updatedAt       time.Time
	)
	for iter.Scan(
		&taskID,
		&title,
		&description,
		&status,
		&sprintName,
		&assigneeID,
		&statusActorID,
		&statusActorName,
		&statusChangedAt,
		&createdAt,
		&updatedAt,
	) {
		task := TaskRecordResponse{
			ID:              strings.TrimSpace(taskID.String()),
			RoomID:          normalizedRoomID,
			Title:           strings.TrimSpace(title),
			Description:     strings.TrimSpace(description),
			Status:          normalizeTaskStatusValue(status),
			SprintName:      strings.TrimSpace(sprintName),
			StatusActorID:   strings.TrimSpace(statusActorID),
			StatusActorName: strings.TrimSpace(statusActorName),
			CreatedAt:       createdAt.UTC(),
			UpdatedAt:       updatedAt.UTC(),
		}
		task.Budget = extractTaskBudget(task.Description)
		task.ActualCost = extractTaskActualCost(task.Description)
		if assigneeID != nil {
			task.AssigneeID = strings.TrimSpace(assigneeID.String())
		}
		if statusChangedAt != nil && !statusChangedAt.IsZero() {
			statusChangedAtUTC := statusChangedAt.UTC()
			task.StatusChangedAt = &statusChangedAtUTC
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
	requestedActualCost := firstTaskFinancialValue(
		req.ActualCost,
		req.ActualCostAlt,
		req.SpentCost,
		req.SpentCostAlt,
		req.Spent,
	)
	description = applyTaskFinancialsToDescription(description, req.Budget, requestedActualCost)
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
	statusActorID := strings.TrimSpace(resolveTaskRequesterID(r))
	statusActorName := resolveTaskRequesterName(r)

	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		nullableTrimmedText(statusActorID),
		nullableTrimmedText(statusActorName),
		now,
		now,
		now,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create room task"})
		return
	}

	response := TaskRecordResponse{
		ID:              strings.TrimSpace(taskUUID.String()),
		RoomID:          normalizedRoomID,
		Title:           title,
		Description:     description,
		Status:          status,
		SprintName:      sprintName,
		StatusActorID:   statusActorID,
		StatusActorName: statusActorName,
		StatusChangedAt: &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	response.Budget = extractTaskBudget(response.Description)
	response.ActualCost = extractTaskActualCost(response.Description)
	if assigneeID != nil {
		response.AssigneeID = strings.TrimSpace(assigneeID.String())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *RoomHandler) UpdateRoomTask(w http.ResponseWriter, r *http.Request) {
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

	var req TaskUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	setClauses := make([]string, 0, 6)
	args := make([]interface{}, 0, 8)

	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "title cannot be empty"})
			return
		}
		if len(title) > 240 {
			title = title[:240]
		}
		setClauses = append(setClauses, "title = ?")
		args = append(args, title)
	}

	descriptionValue := req.Description
	requestedActualCost := firstTaskFinancialValue(
		req.ActualCost,
		req.ActualCostAlt,
		req.SpentCost,
		req.SpentCostAlt,
		req.Spent,
	)
	if req.Budget != nil || requestedActualCost != nil {
		baseDescription := ""
		if descriptionValue != nil {
			baseDescription = strings.TrimSpace(*descriptionValue)
		} else {
			descriptionQuery := fmt.Sprintf(
				`SELECT description FROM %s WHERE room_id = ? AND id = ?`,
				h.scylla.Table("tasks"),
			)
			if err := h.scylla.Session.Query(descriptionQuery, roomUUID, taskID).
				WithContext(r.Context()).
				Scan(&baseDescription); err != nil {
				if err == gocql.ErrNotFound {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task not found"})
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load existing task"})
				return
			}
		}
		nextBudget := extractTaskBudget(baseDescription)
		if req.Budget != nil {
			nextBudget = req.Budget
		}
		nextActualCost := extractTaskActualCost(baseDescription)
		if requestedActualCost != nil {
			nextActualCost = requestedActualCost
		}
		updatedDescription := applyTaskFinancialsToDescription(baseDescription, nextBudget, nextActualCost)
		descriptionValue = &updatedDescription
	}
	if descriptionValue != nil {
		description := strings.TrimSpace(*descriptionValue)
		if len(description) > 4000 {
			description = description[:4000]
		}
		setClauses = append(setClauses, "description = ?")
		args = append(args, description)
	}

	sprintNameValue := req.SprintName
	if sprintNameValue == nil {
		sprintNameValue = req.SprintNameAlt
	}
	if sprintNameValue != nil {
		sprintName := strings.TrimSpace(*sprintNameValue)
		if len(sprintName) > 160 {
			sprintName = sprintName[:160]
		}
		setClauses = append(setClauses, "sprint_name = ?")
		args = append(args, nullableTrimmedText(sprintName))
	}

	assigneeIDValue := req.AssigneeID
	if assigneeIDValue == nil {
		assigneeIDValue = req.AssigneeIDAlt
	}
	if assigneeIDValue != nil {
		assigneeRaw := strings.TrimSpace(*assigneeIDValue)
		if assigneeRaw == "" {
			setClauses = append(setClauses, "assignee_id = ?")
			args = append(args, nil)
		} else {
			candidates := []string{assigneeRaw}
			if strings.Contains(assigneeRaw, "_") {
				candidates = append(candidates, strings.ReplaceAll(assigneeRaw, "_", "-"))
			}
			var assigneeUUID *gocql.UUID
			for _, candidate := range candidates {
				parsedAssignee, parseErr := parseFlexibleTaskUUID(candidate)
				if parseErr != nil {
					continue
				}
				assigneeCopy := parsedAssignee
				assigneeUUID = &assigneeCopy
				break
			}
			if assigneeUUID == nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid assignee id"})
				return
			}
			setClauses = append(setClauses, "assignee_id = ?")
			args = append(args, assigneeUUID)
		}
	}

	if len(setClauses) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "No editable fields provided"})
		return
	}

	now := time.Now().UTC()
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, now)
	args = append(args, roomUUID, taskID)

	updateQuery := fmt.Sprintf(
		`UPDATE %s SET %s WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
		strings.Join(setClauses, ", "),
	)
	if err := h.scylla.Session.Query(updateQuery, args...).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update room task"})
		return
	}

	selectQuery := fmt.Sprintf(
		`SELECT id, title, description, status, sprint_name, assignee_id, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at FROM %s WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
	)
	var (
		foundTaskID     gocql.UUID
		title           string
		description     string
		status          string
		sprintName      string
		assigneeID      *gocql.UUID
		statusActorID   string
		statusActorName string
		statusChangedAt *time.Time
		createdAt       time.Time
		updatedAt       time.Time
	)
	if err := h.scylla.Session.Query(selectQuery, roomUUID, taskID).WithContext(r.Context()).Scan(
		&foundTaskID,
		&title,
		&description,
		&status,
		&sprintName,
		&assigneeID,
		&statusActorID,
		&statusActorName,
		&statusChangedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load updated task"})
		return
	}

	response := TaskRecordResponse{
		ID:              strings.TrimSpace(foundTaskID.String()),
		RoomID:          normalizedRoomID,
		Title:           strings.TrimSpace(title),
		Description:     strings.TrimSpace(description),
		Status:          normalizeTaskStatusValue(status),
		SprintName:      strings.TrimSpace(sprintName),
		StatusActorID:   strings.TrimSpace(statusActorID),
		StatusActorName: strings.TrimSpace(statusActorName),
		CreatedAt:       createdAt.UTC(),
		UpdatedAt:       updatedAt.UTC(),
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
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
	statusActorID := strings.TrimSpace(resolveTaskRequesterID(r))
	statusActorName := resolveTaskRequesterName(r)
	query := fmt.Sprintf(
		`UPDATE %s SET status = ?, updated_at = ?, status_actor_id = ?, status_actor_name = ?, status_changed_at = ? WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
	)
	if err := h.scylla.Session.Query(
		query,
		status,
		now,
		nullableTrimmedText(statusActorID),
		nullableTrimmedText(statusActorName),
		now,
		roomUUID,
		taskID,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update room task status"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]interface{}{
		"status":            status,
		"updated_at":        now,
		"status_changed_at": now,
	}
	if statusActorID != "" {
		response["status_actor_id"] = statusActorID
	}
	if statusActorName != "" {
		response["status_actor_name"] = statusActorName
	}
	_ = json.NewEncoder(w).Encode(response)
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

func (h *RoomHandler) DeleteRoomTask(w http.ResponseWriter, r *http.Request) {
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
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to delete tasks"})
			return
		}
	}

	taskID, err := parseFlexibleTaskUUID(strings.TrimSpace(chi.URLParam(r, "taskId")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid task id"})
		return
	}

	query := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND id = ?`, h.scylla.Table("tasks"))
	if err := h.scylla.Session.Query(query, roomUUID, taskID).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete room task"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "deleted",
		"task_id": strings.TrimSpace(taskID.String()),
	})
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
