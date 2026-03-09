package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/ai"
)

const aiBlueprintSystemPrompt = "You are an Expert Project Architect. Given a prompt, output strict JSON with 'project_name', 'tech_stack' (array), 'target_audience', 'estimated_cost', 'roles_needed', and 'sprints' (array of objects with 'name', 'duration_days'). DO NOT generate tasks."

const aiTaskFillSystemPrompt = "You are an Expert Agile Manager. Given a project blueprint and a Sprint name, generate strict JSON containing an array of 'tasks'. Each task needs 'title', 'duration_unit' (hours/days), 'duration_value' (number), 'status', and 'type'. Keep it realistic."

const aiTimelineEditSystemPrompt = "You are an Expert Project Program Manager. You receive a current project JSON and an edit prompt. Return strict JSON with keys: 'project_name', 'tech_stack', 'target_audience', 'estimated_cost', 'roles_needed', and 'sprints'. Each sprint must include 'name', 'duration_days', and 'tasks'. Each task must include 'title', 'duration_unit', 'duration_value', 'status', and 'type'."

type aiTimelineGenerateRequest struct {
	Prompt   string `json:"prompt"`
	UserID   string `json:"userId,omitempty"`
	DeviceID string `json:"deviceId,omitempty"`
}

type aiTimelineEditRequest struct {
	Prompt       string          `json:"prompt"`
	CurrentState json.RawMessage `json:"current_state"`
	UserID       string          `json:"userId,omitempty"`
	DeviceID     string          `json:"deviceId,omitempty"`
}

type aiTimelineGenerateResponse struct {
	ProjectName    string             `json:"project_name"`
	TechStack      []string           `json:"tech_stack,omitempty"`
	TargetAudience string             `json:"target_audience,omitempty"`
	EstimatedCost  string             `json:"estimated_cost,omitempty"`
	RolesNeeded    []string           `json:"roles_needed,omitempty"`
	TotalProgress  float64            `json:"total_progress,omitempty"`
	Sprints        []aiTimelineSprint `json:"sprints"`
	IsPartial      bool               `json:"is_partial,omitempty"`
	MissingSprints []string           `json:"missing_sprints,omitempty"`
	PersistedTask  int                `json:"persisted_task_count"`
}

type aiTimelineProject struct {
	ProjectName    string             `json:"project_name"`
	TechStack      []string           `json:"tech_stack,omitempty"`
	TargetAudience string             `json:"target_audience,omitempty"`
	EstimatedCost  string             `json:"estimated_cost,omitempty"`
	RolesNeeded    []string           `json:"roles_needed,omitempty"`
	TotalProgress  float64            `json:"total_progress,omitempty"`
	Sprints        []aiTimelineSprint `json:"sprints"`
	IsPartial      bool               `json:"is_partial,omitempty"`
	MissingSprints []string           `json:"missing_sprints,omitempty"`
}

type aiTimelineSprint struct {
	ID             string           `json:"id,omitempty"`
	Name           string           `json:"name"`
	StartDate      string           `json:"start_date,omitempty"`
	EndDate        string           `json:"end_date,omitempty"`
	DurationDays   int              `json:"duration_days"`
	TasksGenerated bool             `json:"tasks_generated"`
	Tasks          []aiTimelineTask `json:"tasks"`
}

type aiTimelineTask struct {
	TaskID        string  `json:"task_id,omitempty"`
	Title         string  `json:"title"`
	Status        string  `json:"status"`
	Type          string  `json:"type"`
	DurationUnit  string  `json:"duration_unit,omitempty"`
	DurationValue float64 `json:"duration_value,omitempty"`
	EffortScore   int     `json:"effort_score,omitempty"`
	Description   string  `json:"description,omitempty"`
}

func (h *RoomHandler) HandleAIGenerateTimeline(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeAITimelineError(w, http.StatusServiceUnavailable, "Task storage unavailable")
		return
	}
	if h.redis == nil || h.redis.Client == nil {
		writeAITimelineError(w, http.StatusServiceUnavailable, "Room storage unavailable")
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid room id")
		return
	}

	var req aiTimelineGenerateRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1*1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeAITimelineError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	userID := normalizeIdentifier(
		firstNonEmpty(
			AuthUserIDFromContext(r.Context()),
			req.UserID,
			r.URL.Query().Get("userId"),
			r.URL.Query().Get("user_id"),
			r.Header.Get("X-User-Id"),
		),
	)
	if userID == "" {
		writeAITimelineError(w, http.StatusUnauthorized, "User context is required")
		return
	}
	deviceID := strings.TrimSpace(
		firstNonEmpty(
			req.DeviceID,
			r.URL.Query().Get("deviceId"),
			r.URL.Query().Get("device_id"),
			r.Header.Get("X-Device-Id"),
		),
	)
	clientIP := strings.TrimSpace(extractClientIP(r))
	if clientIP == "" {
		clientIP = "unknown"
	}

	isMember, memberErr := h.isRoomMember(r.Context(), roomID, userID)
	if memberErr != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to verify room membership")
		return
	}
	if !isMember {
		writeAITimelineError(w, http.StatusForbidden, "Join the room to generate a timeline")
		return
	}

	limits := getAIOrganizeLimits()
	blueprintCtx, cancelBlueprint := context.WithTimeout(r.Context(), limits.RequestTimeout)
	defer cancelBlueprint()

	generated, generateErr := generateAIProjectBlueprint(blueprintCtx, roomID, prompt, limits)
	if generateErr != nil {
		switch {
		case errors.Is(generateErr, context.Canceled), errors.Is(generateErr, context.DeadlineExceeded):
			writeAITimelineError(w, http.StatusGatewayTimeout, "AI timeline request timed out")
		case errors.Is(generateErr, ai.ErrAllAIProvidersExhausted):
			writeAITimelineError(w, http.StatusServiceUnavailable, "AI providers are currently unavailable")
		default:
			writeAITimelineError(w, http.StatusBadGateway, "Failed to generate timeline from AI")
		}
		return
	}
	blueprintRaw, marshalErr := json.Marshal(generated)
	if marshalErr != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to prepare project blueprint")
		return
	}
	blueprintJSON := string(blueprintRaw)

	for sprintIndex := range generated.Sprints {
		if limitErr := enforcePrivateAIRequestLimits(r.Context(), userID, roomID, clientIP, deviceID); limitErr != nil {
			generated.IsPartial = true
			generated.MissingSprints = append(generated.MissingSprints, remainingSprintNames(generated.Sprints, sprintIndex)...)
			break
		}

		sprintName := strings.TrimSpace(generated.Sprints[sprintIndex].Name)
		sprintCtx, cancelSprint := context.WithTimeout(r.Context(), limits.RequestTimeout)
		tasks, taskErr := generateTasksForSprint(sprintCtx, blueprintJSON, sprintName, limits)
		cancelSprint()
		if taskErr != nil {
			switch {
			case errors.Is(taskErr, context.Canceled), errors.Is(taskErr, context.DeadlineExceeded):
				writeAITimelineError(w, http.StatusGatewayTimeout, "AI timeline task generation timed out")
			case errors.Is(taskErr, ai.ErrAllAIProvidersExhausted):
				writeAITimelineError(w, http.StatusServiceUnavailable, "AI providers are currently unavailable")
			default:
				writeAITimelineError(w, http.StatusBadGateway, "Failed to generate sprint tasks from AI")
			}
			return
		}
		generated.Sprints[sprintIndex].Tasks = tasks
		generated.Sprints[sprintIndex].TasksGenerated = true
	}

	roomUUID, _, parseRoomErr := resolveTaskRoomUUID(roomID)
	if parseRoomErr != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid room id")
		return
	}
	assigneeID := resolveAuthAssigneeUUID(r.Context())
	persistedCount, persistErr := h.persistAITimelineTasks(r.Context(), roomUUID, assigneeID, &generated)
	if persistErr != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to persist generated timeline tasks")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(aiTimelineGenerateResponse{
		ProjectName:    generated.ProjectName,
		TechStack:      generated.TechStack,
		TargetAudience: generated.TargetAudience,
		EstimatedCost:  generated.EstimatedCost,
		RolesNeeded:    generated.RolesNeeded,
		TotalProgress:  generated.TotalProgress,
		Sprints:        generated.Sprints,
		IsPartial:      generated.IsPartial,
		MissingSprints: generated.MissingSprints,
		PersistedTask:  persistedCount,
	})
}

func (h *RoomHandler) HandleAIEditTimeline(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeAITimelineError(w, http.StatusServiceUnavailable, "Task storage unavailable")
		return
	}
	if h.redis == nil || h.redis.Client == nil {
		writeAITimelineError(w, http.StatusServiceUnavailable, "Room storage unavailable")
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid room id")
		return
	}

	var req aiTimelineEditRequest
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeAITimelineError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	if len(req.CurrentState) == 0 || strings.TrimSpace(string(req.CurrentState)) == "" || strings.TrimSpace(string(req.CurrentState)) == "null" {
		writeAITimelineError(w, http.StatusBadRequest, "current_state is required")
		return
	}

	userID := normalizeIdentifier(
		firstNonEmpty(
			AuthUserIDFromContext(r.Context()),
			req.UserID,
			r.URL.Query().Get("userId"),
			r.URL.Query().Get("user_id"),
			r.Header.Get("X-User-Id"),
		),
	)
	if userID == "" {
		writeAITimelineError(w, http.StatusUnauthorized, "User context is required")
		return
	}
	deviceID := strings.TrimSpace(
		firstNonEmpty(
			req.DeviceID,
			r.URL.Query().Get("deviceId"),
			r.URL.Query().Get("device_id"),
			r.Header.Get("X-Device-Id"),
		),
	)
	clientIP := strings.TrimSpace(extractClientIP(r))
	if clientIP == "" {
		clientIP = "unknown"
	}

	isMember, memberErr := h.isRoomMember(r.Context(), roomID, userID)
	if memberErr != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to verify room membership")
		return
	}
	if !isMember {
		writeAITimelineError(w, http.StatusForbidden, "Join the room to edit the timeline")
		return
	}

	if limitErr := enforcePrivateAIRequestLimits(r.Context(), userID, roomID, clientIP, deviceID); limitErr != nil {
		var exceeded *privateAILimitExceededError
		if errors.As(limitErr, &exceeded) {
			writeAITimelineError(w, http.StatusTooManyRequests, exceeded.PublicMessage())
			return
		}
		writeAITimelineError(w, http.StatusServiceUnavailable, "AI limiter unavailable")
		return
	}

	var currentState aiTimelineProject
	if err := json.Unmarshal(req.CurrentState, &currentState); err != nil {
		writeAITimelineError(w, http.StatusBadRequest, "current_state must be valid project JSON")
		return
	}
	normalizedCurrent := normalizeAITimelineProject(currentState)
	currentStateBytes, marshalErr := json.Marshal(normalizedCurrent)
	if marshalErr != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Failed to normalize current_state")
		return
	}

	limits := getAIOrganizeLimits()
	editCtx, cancelEdit := context.WithTimeout(r.Context(), limits.RequestTimeout)
	defer cancelEdit()

	edited, editErr := generateAIEditedTimelineProject(
		editCtx,
		roomID,
		prompt,
		string(currentStateBytes),
		limits,
	)
	if editErr != nil {
		switch {
		case errors.Is(editErr, context.Canceled), errors.Is(editErr, context.DeadlineExceeded):
			writeAITimelineError(w, http.StatusGatewayTimeout, "AI timeline edit request timed out")
		case errors.Is(editErr, ai.ErrAllAIProvidersExhausted):
			writeAITimelineError(w, http.StatusServiceUnavailable, "AI providers are currently unavailable")
		default:
			writeAITimelineError(w, http.StatusBadGateway, "Failed to edit timeline from AI")
		}
		return
	}

	if err := h.deleteRoomTasks(r.Context(), roomID); err != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to reset room tasks before AI edit")
		return
	}

	roomUUID, _, parseRoomErr := resolveTaskRoomUUID(roomID)
	if parseRoomErr != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid room id")
		return
	}
	assigneeID := resolveAuthAssigneeUUID(r.Context())
	persistedCount, persistErr := h.persistAITimelineTasks(r.Context(), roomUUID, assigneeID, &edited)
	if persistErr != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to persist AI-edited timeline tasks")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(aiTimelineGenerateResponse{
		ProjectName:    edited.ProjectName,
		TechStack:      edited.TechStack,
		TargetAudience: edited.TargetAudience,
		EstimatedCost:  edited.EstimatedCost,
		RolesNeeded:    edited.RolesNeeded,
		TotalProgress:  edited.TotalProgress,
		Sprints:        edited.Sprints,
		IsPartial:      edited.IsPartial,
		MissingSprints: edited.MissingSprints,
		PersistedTask:  persistedCount,
	})
}

func resolveAuthAssigneeUUID(ctx context.Context) *gocql.UUID {
	raw := strings.TrimSpace(AuthUserIDFromContext(ctx))
	if raw == "" {
		return nil
	}
	parsed, err := gocql.ParseUUID(raw)
	if err != nil {
		return nil
	}
	return &parsed
}

func generateAIProjectBlueprint(
	ctx context.Context,
	roomID string,
	prompt string,
	limits aiOrganizeLimits,
) (aiTimelineProject, error) {
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nUser request: %s\nGenerate a detailed project blueprint now.",
		roomID,
		strings.TrimSpace(prompt),
	)
	raw, err := generateAIOrganizeStructuredJSON(ctx, aiBlueprintSystemPrompt, userPrompt, limits)
	if err != nil {
		return aiTimelineProject{}, err
	}
	return parseAITimelineProject(raw)
}

func generateAIEditedTimelineProject(
	ctx context.Context,
	roomID string,
	prompt string,
	currentStateJSON string,
	limits aiOrganizeLimits,
) (aiTimelineProject, error) {
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nCurrent project JSON:\n%s\n\nRequested edits:\n%s\n\nReturn the full updated project JSON.",
		roomID,
		strings.TrimSpace(currentStateJSON),
		strings.TrimSpace(prompt),
	)
	raw, err := generateAIOrganizeStructuredJSON(ctx, aiTimelineEditSystemPrompt, userPrompt, limits)
	if err != nil {
		return aiTimelineProject{}, err
	}
	return parseAITimelineProject(raw)
}

func generateTasksForSprint(
	ctx context.Context,
	blueprintJSON string,
	sprintName string,
	limits aiOrganizeLimits,
) ([]aiTimelineTask, error) {
	normalizedSprintName := strings.TrimSpace(sprintName)
	if normalizedSprintName == "" {
		return nil, fmt.Errorf("sprint name is required")
	}

	userPrompt := fmt.Sprintf(
		"Project blueprint JSON:\n%s\n\nSprint name: %s\nGenerate tasks only for this sprint.",
		strings.TrimSpace(blueprintJSON),
		normalizedSprintName,
	)
	raw, err := generateAIOrganizeStructuredJSON(ctx, aiTaskFillSystemPrompt, userPrompt, limits)
	if err != nil {
		return nil, err
	}
	return parseAISprintTasks(raw)
}

func parseAISprintTasks(raw string) ([]aiTimelineTask, error) {
	content := extractJSONObject(raw)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("ai sprint task response did not contain JSON")
	}

	var parsed struct {
		Tasks []aiTimelineTask `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, err
	}
	normalizedTasks := normalizeAITimelineTasks(parsed.Tasks)
	if len(normalizedTasks) == 0 {
		return nil, fmt.Errorf("ai sprint task generation returned no valid tasks")
	}
	return normalizedTasks, nil
}

func parseAITimelineProject(raw string) (aiTimelineProject, error) {
	content := extractJSONObject(raw)
	if strings.TrimSpace(content) == "" {
		return aiTimelineProject{}, fmt.Errorf("ai timeline response did not contain JSON")
	}

	var parsed aiTimelineProject
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return aiTimelineProject{}, err
	}

	normalized := normalizeAITimelineProject(parsed)
	if len(normalized.Sprints) == 0 {
		return aiTimelineProject{}, fmt.Errorf("ai timeline returned no valid sprints")
	}
	return normalized, nil
}

func normalizeTimelineStringSlice(values []string, fallback []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		entry := truncateRunes(strings.TrimSpace(value), 80)
		if entry == "" {
			continue
		}
		key := strings.ToLower(entry)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, entry)
	}
	if len(normalized) == 0 && len(fallback) > 0 {
		return fallback
	}
	return normalized
}

func normalizeTimelineDurationUnit(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "hour", "hours":
		return "hours"
	case "day", "days":
		return "days"
	default:
		return "days"
	}
}

func normalizeTimelineDurationValue(value float64, unit string) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		if unit == "hours" {
			return 4
		}
		return 1
	}
	if value > 10_000 {
		return 10_000
	}
	return value
}

func estimateEffortScoreFromDuration(unit string, value float64) int {
	hours := value
	if unit == "days" {
		hours = value * 8
	}
	switch {
	case hours <= 2:
		return 1
	case hours <= 4:
		return 2
	case hours <= 8:
		return 3
	case hours <= 16:
		return 4
	case hours <= 24:
		return 5
	case hours <= 40:
		return 6
	case hours <= 60:
		return 7
	case hours <= 80:
		return 8
	case hours <= 120:
		return 9
	default:
		return 10
	}
}

func normalizeAITimelineTasks(tasks []aiTimelineTask) []aiTimelineTask {
	normalized := make([]aiTimelineTask, 0, len(tasks))
	for _, task := range tasks {
		title := truncateRunes(strings.TrimSpace(task.Title), 240)
		if title == "" {
			continue
		}
		status := normalizeTaskStatusValue(task.Status)
		if status == "" {
			status = "todo"
		}
		taskType := truncateRunes(strings.ToLower(strings.TrimSpace(task.Type)), 48)
		if taskType == "" {
			taskType = "general"
		}
		durationUnit := normalizeTimelineDurationUnit(task.DurationUnit)
		durationValue := normalizeTimelineDurationValue(task.DurationValue, durationUnit)
		effort := task.EffortScore
		if effort < 1 || effort > 10 {
			effort = estimateEffortScoreFromDuration(durationUnit, durationValue)
		}
		description := truncateRunes(strings.TrimSpace(task.Description), 4000)
		normalized = append(normalized, aiTimelineTask{
			Title:         title,
			Status:        status,
			Type:          taskType,
			DurationUnit:  durationUnit,
			DurationValue: durationValue,
			EffortScore:   effort,
			Description:   description,
		})
	}
	return normalized
}

func remainingSprintNames(sprints []aiTimelineSprint, startIndex int) []string {
	if startIndex < 0 || startIndex >= len(sprints) {
		return nil
	}
	names := make([]string, 0, len(sprints)-startIndex)
	for idx := startIndex; idx < len(sprints); idx++ {
		name := strings.TrimSpace(sprints[idx].Name)
		if name == "" {
			name = fmt.Sprintf("Sprint %d", idx+1)
		}
		names = append(names, name)
	}
	return names
}

func normalizeAITimelineProject(input aiTimelineProject) aiTimelineProject {
	projectName := truncateRunes(strings.TrimSpace(input.ProjectName), 180)
	if projectName == "" {
		projectName = "AI Project Timeline"
	}
	normalizedTechStack := normalizeTimelineStringSlice(input.TechStack, nil)
	normalizedRoles := normalizeTimelineStringSlice(input.RolesNeeded, nil)
	targetAudience := truncateRunes(strings.TrimSpace(input.TargetAudience), 180)
	if targetAudience == "" {
		targetAudience = "General users"
	}
	estimatedCost := truncateRunes(strings.TrimSpace(input.EstimatedCost), 120)
	if estimatedCost == "" {
		estimatedCost = "TBD"
	}

	currentStartDate := time.Now().UTC()
	normalizedSprints := make([]aiTimelineSprint, 0, len(input.Sprints))
	for sprintIndex, sprint := range input.Sprints {
		sprintName := truncateRunes(strings.TrimSpace(sprint.Name), 160)
		if sprintName == "" {
			sprintName = fmt.Sprintf("Sprint %d", sprintIndex+1)
		}
		durationDays := sprint.DurationDays
		if durationDays <= 0 {
			durationDays = 7
		}
		if durationDays > 180 {
			durationDays = 180
		}

		startDate := normalizeTimelineDate(sprint.StartDate, currentStartDate)
		defaultEndDate := startDate.AddDate(0, 0, durationDays-1)
		endDate := normalizeTimelineDate(sprint.EndDate, defaultEndDate)
		if endDate.Before(startDate) {
			endDate = defaultEndDate
		}
		currentStartDate = endDate.AddDate(0, 0, 1)

		normalizedTasks := normalizeAITimelineTasks(sprint.Tasks)

		normalizedSprints = append(normalizedSprints, aiTimelineSprint{
			ID:             fmt.Sprintf("sprint-%d", sprintIndex+1),
			Name:           sprintName,
			StartDate:      startDate.Format("2006-01-02"),
			EndDate:        endDate.Format("2006-01-02"),
			DurationDays:   durationDays,
			TasksGenerated: len(normalizedTasks) > 0,
			Tasks:          normalizedTasks,
		})
	}

	return aiTimelineProject{
		ProjectName:    projectName,
		TechStack:      normalizedTechStack,
		TargetAudience: targetAudience,
		EstimatedCost:  estimatedCost,
		RolesNeeded:    normalizedRoles,
		TotalProgress:  0,
		Sprints:        normalizedSprints,
		IsPartial:      false,
		MissingSprints: nil,
	}
}

func formatDurationValue(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "0"
	}
	if math.Mod(value, 1) == 0 {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', 1, 64)
}

func normalizeTimelineDate(raw string, fallback time.Time) time.Time {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback.UTC()
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return fallback.UTC()
	}
	return parsed.UTC()
}

func (h *RoomHandler) persistAITimelineTasks(
	ctx context.Context,
	roomUUID gocql.UUID,
	assigneeID *gocql.UUID,
	project *aiTimelineProject,
) (int, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil || project == nil {
		return 0, fmt.Errorf("task storage unavailable")
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table("tasks"),
	)

	inserted := 0
	now := time.Now().UTC()
	for sprintIndex := range project.Sprints {
		sprint := &project.Sprints[sprintIndex]
		sprintName := truncateRunes(strings.TrimSpace(sprint.Name), 160)
		for taskIndex := range sprint.Tasks {
			task := &sprint.Tasks[taskIndex]
			taskID, taskIDErr := gocql.RandomUUID()
			if taskIDErr != nil {
				return inserted, taskIDErr
			}

			taskIDString := strings.TrimSpace(taskID.String())
			title := truncateRunes(strings.TrimSpace(task.Title), 240)
			if title == "" {
				continue
			}
			status := normalizeTaskStatusValue(task.Status)
			if status == "" {
				status = "todo"
			}

			description := truncateRunes(strings.TrimSpace(task.Description), 3600)
			metadataParts := make([]string, 0, 3)
			if task.Type != "" {
				metadataParts = append(metadataParts, fmt.Sprintf("Type: %s", strings.TrimSpace(task.Type)))
			}
			if task.DurationUnit != "" && task.DurationValue > 0 {
				metadataParts = append(
					metadataParts,
					fmt.Sprintf("Duration: %s %s", formatDurationValue(task.DurationValue), strings.TrimSpace(task.DurationUnit)),
				)
			}
			if task.EffortScore > 0 {
				metadataParts = append(metadataParts, fmt.Sprintf("Effort: %d", task.EffortScore))
			}
			if sprint.StartDate != "" || sprint.EndDate != "" {
				metadataParts = append(
					metadataParts,
					fmt.Sprintf("Sprint Window: %s -> %s", strings.TrimSpace(sprint.StartDate), strings.TrimSpace(sprint.EndDate)),
				)
			}
			if len(metadataParts) > 0 {
				meta := "[" + strings.Join(metadataParts, " | ") + "]"
				if description == "" {
					description = meta
				} else {
					description = truncateRunes(description+"\n\n"+meta, 4000)
				}
			}

			if err := h.scylla.Session.Query(
				query,
				roomUUID,
				taskID,
				title,
				description,
				status,
				sprintName,
				assigneeID,
				now,
				now,
			).WithContext(ctx).Exec(); err != nil {
				return inserted, err
			}

			task.TaskID = taskIDString
			task.Title = title
			task.Description = description
			task.Status = status
			inserted++
		}
	}

	return inserted, nil
}

func writeAITimelineError(w http.ResponseWriter, status int, message string) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": strings.TrimSpace(message),
	})
}
