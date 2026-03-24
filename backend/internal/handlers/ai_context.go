package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/database"
)

const (
	aiContextCachePrefix            = "ai_ctx:"
	aiContextCacheTTL               = 60 * time.Second
	aiContextRecentHistoryLimit     = 20
	aiContextRecentActivityLimit    = 5
	aiContextPromptMaxApproxTokens  = 3000
	aiContextPromptMaxChars         = aiContextPromptMaxApproxTokens * 4
	aiContextTaskLineCharBudget     = 4200
	aiContextTopOverBudgetTaskLimit = 8
)

type WorkspaceSnapshot struct {
	RoomID         string           `json:"room_id"`
	ProjectName    string           `json:"project_name,omitempty"`
	GeneratedAt    time.Time        `json:"generated_at"`
	TaskSummary    TaskBoardSummary `json:"task_summary"`
	TimeSummary    *TimeSummary     `json:"time_summary,omitempty"`
	CostSummary    *CostSummary     `json:"cost_summary,omitempty"`
	RecentActivity []string         `json:"recent_activity,omitempty"`
}

type TaskBoardSummary struct {
	TotalTasks       int            `json:"total_tasks"`
	ByStatus         map[string]int `json:"by_status"`
	BlockedTasks     []TaskBrief    `json:"blocked_tasks"`
	OverdueTasks     []TaskBrief    `json:"overdue_tasks"`
	UnassignedTasks  []TaskBrief    `json:"unassigned_tasks"`
	AllTasks         []TaskBrief    `json:"all_tasks"`
}

type TaskBrief struct {
	ID           string                 `json:"id"`
	Title        string                 `json:"title"`
	Status       string                 `json:"status"`
	TaskType     string                 `json:"task_type,omitempty"`
	Assignee     string                 `json:"assignee,omitempty"`
	DueDate      string                 `json:"due_date,omitempty"`
	Sprint       string                 `json:"sprint,omitempty"`
	IsBlocked    bool                   `json:"is_blocked,omitempty"`
	CustomFields map[string]interface{} `json:"custom_fields,omitempty"`
}

type TimeSummary struct {
	TotalHours float64            `json:"total_hours"`
	ByUser     map[string]float64 `json:"by_user"`
	ByTask     map[string]float64 `json:"by_task"`
}

type CostSummary struct {
	TotalBudget     float64    `json:"total_budget"`
	TotalSpent      float64    `json:"total_spent"`
	VariancePercent float64    `json:"variance_percent"`
	TopOverBudget   []TaskBrief `json:"top_over_budget"`
}

type aiCostDeltaTask struct {
	brief TaskBrief
	delta float64
}

func (h *RoomHandler) GetRoomAIContext(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "AI context storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	requesterID := resolveTaskRequesterMemberID(r)
	if requesterID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
		return
	}
	isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), roomID, requesterID)
	if memberErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to access AI context"})
		return
	}

	snapshot, err := BuildWorkspaceSnapshot(r.Context(), h.redis, h.scylla, roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to build workspace context"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snapshot)
}

func BuildWorkspaceSnapshot(
	ctx context.Context,
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	roomID string,
) (*WorkspaceSnapshot, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return nil, fmt.Errorf("room id is required")
	}
	if scyllaStore == nil || scyllaStore.Session == nil {
		return nil, fmt.Errorf("scylla store is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cached := loadCachedWorkspaceSnapshot(ctx, redisStore, normalizedRoomID)
	if cached != nil {
		return cached, nil
	}

	snapshot, err := buildWorkspaceSnapshotFresh(ctx, redisStore, scyllaStore, normalizedRoomID)
	if err != nil {
		return nil, err
	}
	cacheWorkspaceSnapshot(ctx, redisStore, normalizedRoomID, snapshot)
	return snapshot, nil
}

func buildWorkspaceSnapshotFresh(
	ctx context.Context,
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	roomID string,
) (*WorkspaceSnapshot, error) {
	tasks, err := loadAIContextTaskRecords(ctx, scyllaStore, roomID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	taskSummary := buildAIContextTaskSummary(tasks, now)
	timeSummary, _ := buildAIContextTimeSummary(ctx, scyllaStore, roomID, tasks)
	costSummary := buildAIContextCostSummary(tasks, now)

	snapshot := &WorkspaceSnapshot{
		RoomID:         roomID,
		ProjectName:    loadAIContextProjectName(ctx, redisStore, scyllaStore, roomID),
		GeneratedAt:    now,
		TaskSummary:    taskSummary,
		TimeSummary:    timeSummary,
		CostSummary:    costSummary,
		RecentActivity: loadAIContextRecentActivity(ctx, redisStore, roomID),
	}
	return snapshot, nil
}

func loadCachedWorkspaceSnapshot(
	ctx context.Context,
	redisStore *database.RedisStore,
	roomID string,
) *WorkspaceSnapshot {
	if redisStore == nil || redisStore.Client == nil || roomID == "" {
		return nil
	}
	cachedJSON, err := redisStore.Client.Get(ctx, aiContextCachePrefix+roomID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return nil
	}
	if strings.TrimSpace(cachedJSON) == "" {
		return nil
	}
	var snapshot WorkspaceSnapshot
	if err := json.Unmarshal([]byte(cachedJSON), &snapshot); err != nil {
		return nil
	}
	if snapshot.GeneratedAt.IsZero() {
		snapshot.GeneratedAt = time.Now().UTC()
	} else {
		snapshot.GeneratedAt = snapshot.GeneratedAt.UTC()
	}
	return &snapshot
}

func cacheWorkspaceSnapshot(
	ctx context.Context,
	redisStore *database.RedisStore,
	roomID string,
	snapshot *WorkspaceSnapshot,
) {
	if redisStore == nil || redisStore.Client == nil || roomID == "" || snapshot == nil {
		return
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	_ = redisStore.Client.Set(ctx, aiContextCachePrefix+roomID, string(encoded), aiContextCacheTTL).Err()
}

func loadAIContextProjectName(
	ctx context.Context,
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	roomID string,
) string {
	if redisStore != nil && redisStore.Client != nil {
		if name, err := redisStore.Client.HGet(ctx, roomKey(roomID), "name").Result(); err == nil {
			trimmed := strings.TrimSpace(name)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	if scyllaStore != nil && scyllaStore.Session != nil {
		var name string
		query := fmt.Sprintf(`SELECT name FROM %s WHERE room_id = ? LIMIT 1`, scyllaStore.Table("rooms"))
		if err := scyllaStore.Session.Query(query, roomID).WithContext(ctx).Scan(&name); err == nil {
			trimmed := strings.TrimSpace(name)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return "Workspace"
}

func loadAIContextTaskRecords(
	ctx context.Context,
	scyllaStore *database.ScyllaStore,
	roomID string,
) ([]TaskRecordResponse, error) {
	roomUUID, normalizedRoomID, err := resolveTaskRoomUUID(roomID)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		`SELECT id, title, description, status, custom_fields, sprint_name, assignee_id, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at, task_type, due_date, start_date FROM %s WHERE room_id = ?`,
		scyllaStore.Table("tasks"),
	)
	iter := scyllaStore.Session.Query(query, roomUUID).WithContext(ctx).Iter()

	tasks := make([]TaskRecordResponse, 0, 96)
	var (
		taskID          gocql.UUID
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
	)
	for iter.Scan(
		&taskID,
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
	) {
		task := TaskRecordResponse{
			ID:              strings.TrimSpace(taskID.String()),
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
		}
		task.Budget = extractTaskBudget(task.Description)
		task.ActualCost = extractTaskActualCost(task.Description)
		if assigneeID != nil {
			task.AssigneeID = strings.TrimSpace(assigneeID.String())
		}
		if statusChangedAt != nil && !statusChangedAt.IsZero() {
			statusChanged := statusChangedAt.UTC()
			task.StatusChangedAt = &statusChanged
		}
		if dueDate != nil && !dueDate.IsZero() {
			dueDateUTC := dueDate.UTC()
			task.DueDate = &dueDateUTC
		}
		if startDate != nil && !startDate.IsZero() {
			startDateUTC := startDate.UTC()
			task.StartDate = &startDateUTC
		}
		tasks = append(tasks, task)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	helper := &RoomHandler{scylla: scyllaStore}
	if err := helper.enrichTaskRecordsWithRelations(ctx, normalizedRoomID, tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func buildAIContextTaskSummary(tasks []TaskRecordResponse, now time.Time) TaskBoardSummary {
	statusCounts := make(map[string]int)
	blockedTasks := make([]TaskBrief, 0, 16)
	overdueTasks := make([]TaskBrief, 0, 16)
	unassignedTasks := make([]TaskBrief, 0, 16)
	allTasks := make([]TaskBrief, 0, len(tasks))

	for _, task := range tasks {
		status := normalizeTaskStatusValue(task.Status)
		if status == "" {
			status = "todo"
		}
		statusCounts[status] += 1

		dueAt, dueDate, hasDueDate := resolveTaskDueDate(task)
		isBlocked := len(task.BlockedBy) > 0
		brief := TaskBrief{
			ID:           strings.TrimSpace(task.ID),
			Title:        strings.TrimSpace(task.Title),
			Status:       status,
			TaskType:     strings.TrimSpace(task.TaskType),
			Assignee:     strings.TrimSpace(task.AssigneeID),
			DueDate:      dueDate,
			Sprint:       strings.TrimSpace(task.SprintName),
			IsBlocked:    isBlocked,
			CustomFields: cloneTaskCustomFields(task.CustomFields),
		}

		if brief.Title == "" {
			brief.Title = "(untitled task)"
		}

		allTasks = append(allTasks, brief)
		if isBlocked {
			blockedTasks = append(blockedTasks, brief)
		}
		if hasDueDate && dueAt.Before(now) && status != "done" {
			overdueTasks = append(overdueTasks, brief)
		}
		if strings.TrimSpace(brief.Assignee) == "" {
			unassignedTasks = append(unassignedTasks, brief)
		}
	}

	sortTaskBriefList(blockedTasks)
	sortTaskBriefList(overdueTasks)
	sortTaskBriefList(unassignedTasks)
	sortTaskBriefList(allTasks)

	return TaskBoardSummary{
		TotalTasks:      len(allTasks),
		ByStatus:        statusCounts,
		BlockedTasks:    blockedTasks,
		OverdueTasks:    overdueTasks,
		UnassignedTasks: unassignedTasks,
		AllTasks:        allTasks,
	}
}

func buildAIContextCostSummary(tasks []TaskRecordResponse, now time.Time) *CostSummary {
	totalBudget := 0.0
	totalSpent := 0.0
	overBudget := make([]aiCostDeltaTask, 0, 12)

	for _, task := range tasks {
		taskBudget := normalizeFinancialPointer(task.Budget)
		taskSpent := normalizeFinancialPointer(task.ActualCost)
		if taskBudget <= 0 {
			if parsedBudget, ok := parseNumericCustomField(task.CustomFields, []string{"budget", "task_budget"}); ok {
				taskBudget = parsedBudget
			}
		}
		if taskSpent <= 0 {
			if parsedSpent, ok := parseNumericCustomField(task.CustomFields, []string{"spent", "actual_cost", "cost"}); ok {
				taskSpent = parsedSpent
			}
		}
		if taskBudget > 0 {
			totalBudget += taskBudget
		}
		if taskSpent > 0 {
			totalSpent += taskSpent
		}

		if taskBudget > 0 && taskSpent > taskBudget {
			_, dueDate, _ := resolveTaskDueDate(task)
			brief := TaskBrief{
				ID:           strings.TrimSpace(task.ID),
				Title:        strings.TrimSpace(task.Title),
				Status:       normalizeTaskStatusValue(task.Status),
				Assignee:     strings.TrimSpace(task.AssigneeID),
				DueDate:      dueDate,
				Sprint:       strings.TrimSpace(task.SprintName),
				IsBlocked:    len(task.BlockedBy) > 0,
				CustomFields: cloneTaskCustomFields(task.CustomFields),
			}
			overBudget = append(overBudget, aiCostDeltaTask{
				brief: brief,
				delta: taskSpent - taskBudget,
			})
		}
	}

	if totalBudget <= 0 && totalSpent <= 0 && len(overBudget) == 0 {
		return nil
	}

	sort.SliceStable(overBudget, func(i, j int) bool {
		if overBudget[i].delta == overBudget[j].delta {
			return overBudget[i].brief.Title < overBudget[j].brief.Title
		}
		return overBudget[i].delta > overBudget[j].delta
	})
	topOverBudget := make([]TaskBrief, 0, aiContextTopOverBudgetTaskLimit)
	for _, entry := range overBudget {
		topOverBudget = append(topOverBudget, entry.brief)
		if len(topOverBudget) >= aiContextTopOverBudgetTaskLimit {
			break
		}
	}

	variancePercent := 0.0
	if totalBudget > 0 {
		variancePercent = roundToTwoDecimals(((totalSpent - totalBudget) / totalBudget) * 100)
	}
	return &CostSummary{
		TotalBudget:     roundToTwoDecimals(totalBudget),
		TotalSpent:      roundToTwoDecimals(totalSpent),
		VariancePercent: variancePercent,
		TopOverBudget:   topOverBudget,
	}
}

func buildAIContextTimeSummary(
	ctx context.Context,
	scyllaStore *database.ScyllaStore,
	roomID string,
	tasks []TaskRecordResponse,
) (*TimeSummary, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	totalSeconds := 0.0
	byUserSeconds := make(map[string]float64)
	byTaskSeconds := make(map[string]float64)

	query := fmt.Sprintf(
		`SELECT user_id, duration_seconds FROM %s WHERE room_id = ? AND task_id = ?`,
		scyllaStore.Table("time_entries"),
	)
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			continue
		}
		iter := scyllaStore.Session.Query(query, roomID, taskID).WithContext(ctx).Iter()
		var (
			userID          string
			durationSeconds int
		)
		for iter.Scan(&userID, &durationSeconds) {
			if durationSeconds <= 0 {
				continue
			}
			userKey := strings.TrimSpace(userID)
			if userKey == "" {
				userKey = "unknown"
			}
			taskKey := taskID
			secondsFloat := float64(durationSeconds)
			totalSeconds += secondsFloat
			byUserSeconds[userKey] += secondsFloat
			byTaskSeconds[taskKey] += secondsFloat
		}
		if err := iter.Close(); err != nil {
			if isMissingTimeEntryTableError(err) {
				return nil, nil
			}
			return nil, err
		}
	}

	if totalSeconds <= 0 {
		return nil, nil
	}

	byUserHours := make(map[string]float64, len(byUserSeconds))
	for userID, seconds := range byUserSeconds {
		byUserHours[userID] = roundToTwoDecimals(seconds / 3600)
	}
	byTaskHours := make(map[string]float64, len(byTaskSeconds))
	for taskID, seconds := range byTaskSeconds {
		byTaskHours[taskID] = roundToTwoDecimals(seconds / 3600)
	}

	return &TimeSummary{
		TotalHours: roundToTwoDecimals(totalSeconds / 3600),
		ByUser:     byUserHours,
		ByTask:     byTaskHours,
	}, nil
}

func loadAIContextRecentActivity(
	ctx context.Context,
	redisStore *database.RedisStore,
	roomID string,
) []string {
	if redisStore == nil || redisStore.Client == nil {
		return nil
	}
	rawEntries, err := redisStore.Client.LRange(
		ctx,
		privateAIRoomHistoryPrefix+roomID,
		int64(-aiContextRecentHistoryLimit),
		-1,
	).Result()
	if err != nil {
		return nil
	}

	messages := decodePrivateAICachedMessages(rawEntries, roomID)
	if len(messages) == 0 {
		return nil
	}
	activity := make([]string, 0, aiContextRecentActivityLimit)
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		content = strings.Join(strings.Fields(content), " ")
		if len(content) > 120 {
			content = strings.TrimSpace(content[:117]) + "..."
		}
		actor := strings.TrimSpace(firstNonEmpty(message.SenderName, message.SenderID, "Unknown"))
		activity = append(activity, fmt.Sprintf("%s: %s", actor, content))
		if len(activity) >= aiContextRecentActivityLimit {
			break
		}
	}
	return activity
}

func resolveTaskDueDate(task TaskRecordResponse) (time.Time, string, bool) {
	if task.CustomFields != nil {
		for key, value := range task.CustomFields {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			if !isDueDateKey(normalizedKey) {
				continue
			}
			if dueAt, ok := parseDueDateValue(value); ok {
				return dueAt, dueAt.Format("2006-01-02"), true
			}
		}
	}

	_, metadataEntries := parseTaskMetadataEntries(task.Description)
	for _, entry := range metadataEntries {
		normalizedKey := strings.ToLower(strings.TrimSpace(entry.key))
		if !isDueDateKey(normalizedKey) {
			continue
		}
		valuePortion := entry.raw
		if index := strings.Index(valuePortion, ":"); index >= 0 {
			valuePortion = valuePortion[index+1:]
		}
		if dueAt, ok := parseDueDateValue(strings.TrimSpace(valuePortion)); ok {
			return dueAt, dueAt.Format("2006-01-02"), true
		}
	}
	return time.Time{}, "", false
}

func isDueDateKey(normalizedKey string) bool {
	if normalizedKey == "" {
		return false
	}
	return normalizedKey == "due" ||
		normalizedKey == "due_date" ||
		normalizedKey == "due date" ||
		normalizedKey == "deadline" ||
		normalizedKey == "target_date" ||
		normalizedKey == "target date" ||
		normalizedKey == "end_date" ||
		normalizedKey == "end date"
}

func parseDueDateValue(value interface{}) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		return parseDueDateString(typed)
	case float64:
		return parseDueDateEpoch(typed)
	case int:
		return parseDueDateEpoch(float64(typed))
	case int64:
		return parseDueDateEpoch(float64(typed))
	case json.Number:
		if asInt, err := typed.Int64(); err == nil {
			return parseDueDateEpoch(float64(asInt))
		}
		if asFloat, err := typed.Float64(); err == nil {
			return parseDueDateEpoch(asFloat)
		}
	}
	return time.Time{}, false
}

func parseDueDateString(raw string) (time.Time, bool) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02",
		"2006/01/02",
		"01/02/2006",
		"01-02-2006",
		"02 Jan 2006",
		"2 Jan 2006",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, normalized); err == nil {
			return parsed.UTC(), true
		}
	}
	if asNumber, err := strconv.ParseFloat(normalized, 64); err == nil {
		return parseDueDateEpoch(asNumber)
	}
	return time.Time{}, false
}

func parseDueDateEpoch(value float64) (time.Time, bool) {
	if !isFinitePositive(value) {
		return time.Time{}, false
	}
	if value > 1_000_000_000_000 {
		return time.UnixMilli(int64(value)).UTC(), true
	}
	return time.Unix(int64(value), 0).UTC(), true
}

func sortTaskBriefList(tasks []TaskBrief) {
	sort.SliceStable(tasks, func(i, j int) bool {
		left := tasks[i]
		right := tasks[j]
		if left.Status != right.Status {
			return left.Status < right.Status
		}
		if left.Sprint != right.Sprint {
			return left.Sprint < right.Sprint
		}
		return left.Title < right.Title
	})
}

func normalizeFinancialPointer(value *float64) float64 {
	if value == nil {
		return 0
	}
	if !isFinitePositive(*value) {
		return 0
	}
	return *value
}

func parseNumericCustomField(fields map[string]interface{}, keys []string) (float64, bool) {
	if len(fields) == 0 {
		return 0, false
	}
	for _, key := range keys {
		for fieldKey, rawValue := range fields {
			normalizedFieldKey := strings.ToLower(strings.TrimSpace(fieldKey))
			if normalizedFieldKey != strings.ToLower(strings.TrimSpace(key)) {
				continue
			}
			switch typed := rawValue.(type) {
			case float64:
				if isFinitePositive(typed) {
					return typed, true
				}
			case int:
				if typed > 0 {
					return float64(typed), true
				}
			case int64:
				if typed > 0 {
					return float64(typed), true
				}
			case string:
				parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(typed, ",", "")), 64)
				if err == nil && isFinitePositive(parsed) {
					return parsed, true
				}
			case json.Number:
				if parsed, err := typed.Float64(); err == nil && isFinitePositive(parsed) {
					return parsed, true
				}
			}
		}
	}
	return 0, false
}

func isFinitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}

func roundToTwoDecimals(value float64) float64 {
	return math.Round(value*100) / 100
}

func isMissingTimeEntryTableError(err error) bool {
	if err == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(normalized, "unconfigured table") ||
		strings.Contains(normalized, "undefined name") ||
		strings.Contains(normalized, "cannot be found") ||
		strings.Contains(normalized, "not found")
}

func formatWorkspaceContextPromptSection(snapshot *WorkspaceSnapshot, maxChars int) string {
	if snapshot == nil {
		return ""
	}
	if maxChars <= 0 {
		maxChars = aiContextPromptMaxChars
	}

	var builder strings.Builder
	builder.WriteString("=== WORKSPACE CONTEXT ===\n")
	builder.WriteString(fmt.Sprintf("Project: %s\n", strings.TrimSpace(firstNonEmpty(snapshot.ProjectName, "Workspace"))))
	builder.WriteString(
		fmt.Sprintf(
			"Tasks: %d total - %s\n",
			snapshot.TaskSummary.TotalTasks,
			formatStatusCountMap(snapshot.TaskSummary.ByStatus),
		),
	)

	builder.WriteString("Blocked tasks: ")
	if len(snapshot.TaskSummary.BlockedTasks) == 0 {
		builder.WriteString("none\n")
	} else {
		builder.WriteString(formatTaskTitleList(snapshot.TaskSummary.BlockedTasks, 8))
		builder.WriteString("\n")
	}

	builder.WriteString("Overdue tasks: ")
	if len(snapshot.TaskSummary.OverdueTasks) == 0 {
		builder.WriteString("none\n")
	} else {
		builder.WriteString(formatTaskTitleList(snapshot.TaskSummary.OverdueTasks, 8))
		builder.WriteString("\n")
	}

	if snapshot.TimeSummary != nil {
		builder.WriteString(
			fmt.Sprintf(
				"Time logged (last 30d): %.2fh total; by user: %s\n",
				snapshot.TimeSummary.TotalHours,
				formatFloatMap(snapshot.TimeSummary.ByUser),
			),
		)
	}
	if snapshot.CostSummary != nil {
		builder.WriteString(
			fmt.Sprintf(
				"Budget: %.2f allocated, %.2f spent (%.2f%% variance)\n",
				snapshot.CostSummary.TotalBudget,
				snapshot.CostSummary.TotalSpent,
				snapshot.CostSummary.VariancePercent,
			),
		)
	}
	if len(snapshot.RecentActivity) > 0 {
		builder.WriteString("Recent board activity: ")
		builder.WriteString(strings.Join(snapshot.RecentActivity, " | "))
		builder.WriteString("\n")
	}

	if len(snapshot.TaskSummary.AllTasks) > 0 {
		builder.WriteString("Task details:\n")
		taskLines := prioritizedTaskLines(snapshot.TaskSummary, aiContextTaskLineCharBudget)
		for _, line := range taskLines {
			builder.WriteString("- ")
			builder.WriteString(line)
			builder.WriteString("\n")
			if builder.Len() >= maxChars {
				break
			}
		}
	}

	builder.WriteString("=========================\n")
	builder.WriteString(
		"You have full visibility of this project. Answer status, risk, blocker, and workload questions using this context.\n",
	)

	contextText := builder.String()
	if len(contextText) <= maxChars {
		return contextText
	}
	trimmed := strings.TrimSpace(contextText[:maxChars])
	if strings.HasSuffix(trimmed, ".") {
		return trimmed
	}
	return trimmed + " ..."
}

func buildWorkspaceContextPromptSection(ctx context.Context, roomID string) string {
	redisStore, scyllaStore := activePrivateAIChatStores()
	snapshot, err := BuildWorkspaceSnapshot(ctx, redisStore, scyllaStore, roomID)
	if err != nil || snapshot == nil {
		return ""
	}
	return formatWorkspaceContextPromptSection(snapshot, aiContextPromptMaxChars)
}

func formatStatusCountMap(statuses map[string]int) string {
	if len(statuses) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(statuses))
	for key := range statuses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, statuses[key]))
	}
	return strings.Join(parts, ", ")
}

func formatTaskTitleList(tasks []TaskBrief, maxCount int) string {
	if len(tasks) == 0 {
		return "none"
	}
	if maxCount <= 0 {
		maxCount = len(tasks)
	}
	parts := make([]string, 0, minInt(len(tasks), maxCount))
	for _, task := range tasks {
		title := strings.TrimSpace(task.Title)
		if title == "" {
			continue
		}
		if len(title) > 72 {
			title = strings.TrimSpace(title[:69]) + "..."
		}
		parts = append(parts, title)
		if len(parts) >= maxCount {
			break
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "; ")
}

func formatFloatMap(values map[string]float64) string {
	if len(values) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%.2f", key, values[key]))
	}
	return strings.Join(parts, ", ")
}

func prioritizedTaskLines(summary TaskBoardSummary, charBudget int) []string {
	if len(summary.AllTasks) == 0 {
		return nil
	}
	if charBudget <= 0 {
		charBudget = aiContextTaskLineCharBudget
	}
	blockedSet := make(map[string]struct{}, len(summary.BlockedTasks))
	overdueSet := make(map[string]struct{}, len(summary.OverdueTasks))
	for _, task := range summary.BlockedTasks {
		blockedSet[task.ID] = struct{}{}
	}
	for _, task := range summary.OverdueTasks {
		overdueSet[task.ID] = struct{}{}
	}

	highPriority := make([]TaskBrief, 0, len(summary.AllTasks))
	lowPriority := make([]TaskBrief, 0, len(summary.AllTasks))
	for _, task := range summary.AllTasks {
		_, isBlocked := blockedSet[task.ID]
		_, isOverdue := overdueSet[task.ID]
		if isBlocked || isOverdue || normalizeTaskStatusValue(task.Status) != "done" {
			highPriority = append(highPriority, task)
			continue
		}
		lowPriority = append(lowPriority, task)
	}

	lines := make([]string, 0, len(summary.AllTasks))
	totalChars := 0
	appendTask := func(task TaskBrief) bool {
		line := fmt.Sprintf("[%s] %s", normalizeTaskStatusValue(task.Status), strings.TrimSpace(task.Title))
		if line == "" || strings.HasSuffix(line, "] ") {
			line = fmt.Sprintf("[%s] %s", normalizeTaskStatusValue(task.Status), task.ID)
		}
		details := make([]string, 0, 4)
		if task.Sprint != "" {
			details = append(details, "sprint="+task.Sprint)
		}
		if task.Assignee != "" {
			details = append(details, "assignee="+task.Assignee)
		}
		if task.DueDate != "" {
			details = append(details, "due="+task.DueDate)
		}
		if task.IsBlocked {
			details = append(details, "blocked=true")
		}
		if len(details) > 0 {
			line += " (" + strings.Join(details, ", ") + ")"
		}
		lineLength := len(line) + 1
		if len(lines) > 0 && totalChars+lineLength > charBudget {
			return false
		}
		lines = append(lines, line)
		totalChars += lineLength
		return true
	}

	for _, task := range highPriority {
		if !appendTask(task) {
			return lines
		}
	}
	for _, task := range lowPriority {
		if !appendTask(task) {
			break
		}
	}
	return lines
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
