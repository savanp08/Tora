package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/ai"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const aiBlueprintSystemPrompt = "You are an Expert Project Architect. Return ONLY valid JSON with keys: 'assistant_reply' and 'timeline'. 'assistant_reply' must be a short plain-language update in a professional, friendly, lightly witty tone (never dismissive or arrogant). 'timeline' must include 'project_name', 'tech_stack' (array), 'target_audience', 'estimated_cost', 'roles_needed', and 'sprints' (array of objects with 'name', 'duration_days'). DO NOT generate tasks in this step."

const aiTaskFillSystemPrompt = "You are an Expert Agile Manager. Given a project blueprint and a Sprint name, generate strict JSON containing an array of 'tasks'. Each task needs 'title', 'duration_unit' (hours/days), 'duration_value' (number), 'status', 'type', and 'budget' (numeric task budget allocation). Keep it realistic."

const aiTimelineEditSystemPrompt = "You are an Expert Project Program Manager acting as a JSON patcher. You receive the current project state (each task has a database task_id) and an edit request. Return ONLY valid JSON with keys: 'assistant_reply' and 'operations'. 'operations' must contain only task deltas, never the full project. Each operation must be one of: {\"action\":\"update_task\",\"task_id\":\"uuid\",\"changes\":{\"title\":\"...\",\"status\":\"todo|in_progress|done\",\"task_type\":\"...\",\"budget\":123,\"actual_cost\":45,\"duration_unit\":\"hours|days\",\"duration_value\":2,\"description\":\"...\",\"sprint_name\":\"...\"}}, {\"action\":\"add_task\",\"sprint_name\":\"Sprint 1\",\"task\":{\"title\":\"...\",\"status\":\"todo|in_progress|done\",\"task_type\":\"...\",\"budget\":123,\"actual_cost\":45,\"duration_unit\":\"hours|days\",\"duration_value\":2,\"description\":\"...\"}}, {\"action\":\"delete_task\",\"task_id\":\"uuid\"}. Keep assistant_reply concise, professional, friendly, and never dismissive."

const aiTimelineIntentSystemPrompt = "You are an AI Project Manager. The user will ask a question or request an edit to their project board. Decide whether the request requires modifying the project tasks, or if it only needs a conversational reply. Return ONLY valid JSON with keys: 'intent' and 'assistant_reply'. 'intent' must be either 'chat' or 'modify_project'. 'assistant_reply' must be concise, professional, friendly, and never dismissive."

type aiTimelineGenerateRequest struct {
	Prompt   string `json:"prompt"`
	UserID   string `json:"userId,omitempty"`
	DeviceID string `json:"deviceId,omitempty"`
}

type AIIntentResponse struct {
	Intent         string `json:"intent"`
	AssistantReply string `json:"assistant_reply"`
}

type aiTimelineEditRequest struct {
	Prompt       string          `json:"prompt"`
	CurrentState json.RawMessage `json:"current_state"`
	UserID       string          `json:"userId,omitempty"`
	DeviceID     string          `json:"deviceId,omitempty"`
}

type aiTimelineGenerateResponse struct {
	AssistantReply string             `json:"assistant_reply,omitempty"`
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
	AssistantReply string             `json:"assistant_reply,omitempty"`
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
	ID            string  `json:"id,omitempty"`
	Title         string  `json:"title"`
	Status        string  `json:"status"`
	Type          string  `json:"type"`
	Budget        float64 `json:"budget,omitempty"`
	ActualCost    float64 `json:"actual_cost,omitempty"`
	DurationUnit  string  `json:"duration_unit,omitempty"`
	DurationValue float64 `json:"duration_value,omitempty"`
	EffortScore   int     `json:"effort_score,omitempty"`
	Description   string  `json:"description,omitempty"`
}

type aiTimelineProjectPatch struct {
	ProjectName    string   `json:"project_name,omitempty"`
	TechStack      []string `json:"tech_stack,omitempty"`
	TargetAudience string   `json:"target_audience,omitempty"`
	EstimatedCost  string   `json:"estimated_cost,omitempty"`
	RolesNeeded    []string `json:"roles_needed,omitempty"`
}

type aiTimelineEditOperation struct {
	Op            string                       `json:"op,omitempty"`
	Action        string                       `json:"action,omitempty"`
	TaskID        string                       `json:"task_id,omitempty"`
	ID            string                       `json:"id,omitempty"`
	SprintName    string                       `json:"sprint_name,omitempty"`
	Sprint        string                       `json:"sprint,omitempty"`
	Title         string                       `json:"title,omitempty"`
	Status        string                       `json:"status,omitempty"`
	Type          string                       `json:"type,omitempty"`
	TaskType      string                       `json:"task_type,omitempty"`
	Budget        *float64                     `json:"budget,omitempty"`
	ActualCost    *float64                     `json:"actual_cost,omitempty"`
	DurationUnit  string                       `json:"duration_unit,omitempty"`
	DurationValue *float64                     `json:"duration_value,omitempty"`
	Description   string                       `json:"description,omitempty"`
	Changes       map[string]any               `json:"changes,omitempty"`
	Task          *aiTimelineEditOperationTask `json:"task,omitempty"`
}

type aiTimelineEditOperationTask struct {
	Title         string   `json:"title,omitempty"`
	Status        string   `json:"status,omitempty"`
	Type          string   `json:"type,omitempty"`
	TaskType      string   `json:"task_type,omitempty"`
	Budget        *float64 `json:"budget,omitempty"`
	ActualCost    *float64 `json:"actual_cost,omitempty"`
	DurationUnit  string   `json:"duration_unit,omitempty"`
	DurationValue *float64 `json:"duration_value,omitempty"`
	Description   string   `json:"description,omitempty"`
	SprintName    string   `json:"sprint_name,omitempty"`
}

type aiTimelineEditOperationsResponse struct {
	AssistantReply string                    `json:"assistant_reply,omitempty"`
	ProjectPatch   aiTimelineProjectPatch    `json:"project_patch,omitempty"`
	Operations     []aiTimelineEditOperation `json:"operations"`
}

type aiTimelineEditTaskSummary struct {
	TaskID        string  `json:"task_id"`
	Title         string  `json:"title"`
	Status        string  `json:"status,omitempty"`
	Type          string  `json:"type,omitempty"`
	Budget        float64 `json:"budget,omitempty"`
	ActualCost    float64 `json:"actual_cost,omitempty"`
	DurationUnit  string  `json:"duration_unit,omitempty"`
	DurationValue float64 `json:"duration_value,omitempty"`
}

type aiTimelineEditSprintSummary struct {
	Name         string                      `json:"name"`
	DurationDays int                         `json:"duration_days,omitempty"`
	StartDate    string                      `json:"start_date,omitempty"`
	EndDate      string                      `json:"end_date,omitempty"`
	Tasks        []aiTimelineEditTaskSummary `json:"tasks"`
}

type aiTimelineEditSummary struct {
	ProjectName    string                        `json:"project_name"`
	TechStack      []string                      `json:"tech_stack,omitempty"`
	TargetAudience string                        `json:"target_audience,omitempty"`
	EstimatedCost  string                        `json:"estimated_cost,omitempty"`
	RolesNeeded    []string                      `json:"roles_needed,omitempty"`
	SprintCount    int                           `json:"sprint_count"`
	TaskCount      int                           `json:"task_count"`
	Sprints        []aiTimelineEditSprintSummary `json:"sprints"`
}

type aiTimelineFlatTask struct {
	SprintIndex int
	TaskIndex   int
	SprintName  string
	StartDate   string
	EndDate     string
	Task        aiTimelineTask
}

type aiTimelineIntentTaskSummary struct {
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
	Type   string `json:"type,omitempty"`
}

type aiTimelineIntentSprintSummary struct {
	Name         string                        `json:"name"`
	DurationDays int                           `json:"duration_days,omitempty"`
	TaskCount    int                           `json:"task_count"`
	Tasks        []aiTimelineIntentTaskSummary `json:"tasks,omitempty"`
}

type aiTimelineIntentSummary struct {
	ProjectName    string                          `json:"project_name"`
	TechStack      []string                        `json:"tech_stack,omitempty"`
	TargetAudience string                          `json:"target_audience,omitempty"`
	EstimatedCost  string                          `json:"estimated_cost,omitempty"`
	RolesNeeded    []string                        `json:"roles_needed,omitempty"`
	SprintCount    int                             `json:"sprint_count"`
	TaskCount      int                             `json:"task_count"`
	Sprints        []aiTimelineIntentSprintSummary `json:"sprints"`
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
			var exceeded *privateAILimitExceededError
			if errors.As(limitErr, &exceeded) {
				logPrivateAILimitExceeded("ai_timeline_generate_sprint", exceeded, userID, roomID, clientIP, deviceID)
			} else {
				log.Printf("[ai-limit] timeline_generate_sprint limiter error user_id=%q room_id=%q err=%v", normalizeIdentifier(userID), normalizeRoomID(roomID), limitErr)
			}
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
		AssistantReply: generated.AssistantReply,
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
			logPrivateAILimitExceeded("ai_timeline_edit", exceeded, userID, roomID, clientIP, deviceID)
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

	limits := getAIOrganizeLimits()

	intentSummaryJSON, summaryErr := buildAITimelineIntentSummaryJSON(normalizedCurrent)
	if summaryErr != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Failed to summarize current_state")
		return
	}
	intentCtx, cancelIntent := context.WithTimeout(r.Context(), limits.RequestTimeout)
	intentResult, intentErr := classifyAITimelineEditIntent(
		intentCtx,
		roomID,
		prompt,
		intentSummaryJSON,
		limits,
	)
	cancelIntent()
	if intentErr != nil {
		log.Printf("[ai_timeline] intent classification failed room_id=%q user_id=%q err=%v", roomID, userID, intentErr)
	} else if intentResult.Intent == "chat" {
		assistantReply := strings.TrimSpace(intentResult.AssistantReply)
		if assistantReply == "" {
			assistantReply = "I can explain the current board and answer questions without editing it."
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(AIIntentResponse{
			Intent:         "chat",
			AssistantReply: assistantReply,
		})
		return
	}

	editSummaryJSON, editSummaryErr := buildAITimelineEditSummaryJSON(normalizedCurrent)
	if editSummaryErr != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Failed to summarize current_state for edits")
		return
	}
	editCtx, cancelEdit := context.WithTimeout(r.Context(), limits.RequestTimeout)
	editOps, editErr := generateAITimelineEditOperations(
		editCtx,
		roomID,
		prompt,
		editSummaryJSON,
		limits,
	)
	cancelEdit()
	if editErr != nil {
		switch {
		case errors.Is(editErr, context.Canceled), errors.Is(editErr, context.DeadlineExceeded):
			writeAITimelineError(w, http.StatusGatewayTimeout, "AI timeline edit request timed out")
		case errors.Is(editErr, ai.ErrAllAIProvidersExhausted):
			writeAITimelineError(w, http.StatusServiceUnavailable, "AI providers are currently unavailable")
		default:
			writeAITimelineError(w, http.StatusBadGateway, "Failed to generate timeline edit operations from AI")
		}
		return
	}
	persistedCount, persistErr := h.applyAIOperations(r.Context(), roomID, editOps.Operations)
	if persistErr != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to apply AI timeline operations")
		return
	}

	edited := applyAITimelineEditOperations(normalizedCurrent, editOps)
	if strings.TrimSpace(edited.AssistantReply) == "" {
		edited.AssistantReply = strings.TrimSpace(editOps.AssistantReply)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(aiTimelineGenerateResponse{
		AssistantReply: edited.AssistantReply,
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

func generateAITimelineEditOperations(
	ctx context.Context,
	roomID string,
	prompt string,
	currentSummaryJSON string,
	limits aiOrganizeLimits,
) (aiTimelineEditOperationsResponse, error) {
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nCurrent project summary JSON:\n%s\n\nUser edit request:\n%s\n\nReturn ONLY the operations JSON.",
		roomID,
		strings.TrimSpace(currentSummaryJSON),
		strings.TrimSpace(prompt),
	)
	raw, err := generateAIOrganizeStructuredJSON(ctx, aiTimelineEditSystemPrompt, userPrompt, limits)
	if err != nil {
		return aiTimelineEditOperationsResponse{}, err
	}
	return parseAITimelineEditOperations(raw)
}

func parseAITimelineEditOperations(raw string) (aiTimelineEditOperationsResponse, error) {
	candidates := extractJSONObjectsCandidates(raw)
	if len(candidates) == 0 {
		return aiTimelineEditOperationsResponse{}, fmt.Errorf("ai edit operations response did not contain JSON")
	}

	var lastErr error
	for _, content := range candidates {
		parsed, err := parseAITimelineEditOperationsCandidate(content)
		if err != nil {
			lastErr = err
			continue
		}
		return parsed, nil
	}
	if lastErr != nil {
		return aiTimelineEditOperationsResponse{}, lastErr
	}
	return aiTimelineEditOperationsResponse{}, fmt.Errorf("ai edit operations response was not parseable")
}

func parseAITimelineEditOperationsCandidate(content string) (aiTimelineEditOperationsResponse, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return aiTimelineEditOperationsResponse{}, err
	}

	assistantReply := decodeJSONString(
		pickFirstNonEmptyJSONRaw(
			envelope["assistant_reply"],
			envelope["assistantReply"],
			envelope["response"],
			envelope["message"],
		),
	)
	patch := parseAITimelineProjectPatch(
		pickFirstNonEmptyJSONRaw(
			envelope["project_patch"],
			envelope["projectPatch"],
		),
	)

	opsRaw := pickFirstNonEmptyJSONRaw(
		envelope["operations"],
		envelope["ops"],
		envelope["edits"],
		envelope["changes"],
	)
	var rawOperations []aiTimelineEditOperation
	if len(opsRaw) > 0 {
		if err := json.Unmarshal(opsRaw, &rawOperations); err != nil {
			return aiTimelineEditOperationsResponse{}, err
		}
	}

	operations := normalizeAITimelineEditOperations(rawOperations)
	if len(operations) == 0 && !hasAITimelineProjectPatch(patch) {
		return aiTimelineEditOperationsResponse{}, fmt.Errorf("ai edit operations response returned no valid edits")
	}

	return aiTimelineEditOperationsResponse{
		AssistantReply: truncateRunes(strings.TrimSpace(assistantReply), 2000),
		ProjectPatch:   patch,
		Operations:     operations,
	}, nil
}

func parseAITimelineProjectPatch(raw json.RawMessage) aiTimelineProjectPatch {
	if len(raw) == 0 {
		return aiTimelineProjectPatch{}
	}
	var patch aiTimelineProjectPatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return aiTimelineProjectPatch{}
	}
	patch.ProjectName = truncateRunes(strings.TrimSpace(patch.ProjectName), 180)
	patch.TargetAudience = truncateRunes(strings.TrimSpace(patch.TargetAudience), 180)
	patch.EstimatedCost = truncateRunes(strings.TrimSpace(patch.EstimatedCost), 120)
	patch.TechStack = normalizeTimelineStringSlice(patch.TechStack, nil)
	patch.RolesNeeded = normalizeTimelineStringSlice(patch.RolesNeeded, nil)
	return patch
}

func hasAITimelineProjectPatch(patch aiTimelineProjectPatch) bool {
	return patch.ProjectName != "" ||
		patch.TargetAudience != "" ||
		patch.EstimatedCost != "" ||
		len(patch.TechStack) > 0 ||
		len(patch.RolesNeeded) > 0
}

func normalizeAITimelineEditOperations(input []aiTimelineEditOperation) []aiTimelineEditOperation {
	readChangeValue := func(changes map[string]any, keys ...string) any {
		if len(changes) == 0 {
			return nil
		}
		for _, key := range keys {
			if value, ok := changes[key]; ok {
				return value
			}
		}
		for existingKey, value := range changes {
			normalizedExisting := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(existingKey), "-", "_"))
			for _, key := range keys {
				normalizedKey := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
				if normalizedExisting == normalizedKey {
					return value
				}
			}
		}
		return nil
	}

	normalized := make([]aiTimelineEditOperation, 0, len(input))
	for _, operation := range input {
		opName := strings.ToLower(strings.TrimSpace(firstNonEmpty(operation.Op, operation.Action)))
		if opName == "" {
			typeCandidate := strings.ToLower(strings.TrimSpace(operation.Type))
			if typeCandidate == "add_task" || typeCandidate == "update_task" || typeCandidate == "delete_task" {
				opName = typeCandidate
			}
		}
		if opName == "" {
			continue
		}

		changes := operation.Changes
		nestedTask := operation.Task

		taskType := strings.ToLower(strings.TrimSpace(operation.TaskType))
		if taskType == "" {
			typeCandidate := strings.ToLower(strings.TrimSpace(operation.Type))
			if typeCandidate != "add_task" && typeCandidate != "update_task" && typeCandidate != "delete_task" {
				taskType = typeCandidate
			}
		}
		if taskType == "" {
			taskType = strings.ToLower(strings.TrimSpace(asStringValue(readChangeValue(changes, "task_type", "type"))))
		}
		if taskType == "" && nestedTask != nil {
			taskType = strings.ToLower(strings.TrimSpace(firstNonEmpty(nestedTask.TaskType, nestedTask.Type)))
		}

		sprintName := truncateRunes(strings.TrimSpace(firstNonEmpty(operation.SprintName, operation.Sprint)), 160)
		if sprintName == "" {
			sprintName = truncateRunes(strings.TrimSpace(asStringValue(readChangeValue(changes, "sprint_name", "sprint"))), 160)
		}
		if sprintName == "" && nestedTask != nil {
			sprintName = truncateRunes(strings.TrimSpace(nestedTask.SprintName), 160)
		}

		taskID := normalizeAITimelineTaskIdentifier(firstNonEmpty(operation.TaskID, operation.ID))
		if taskID == "" {
			taskID = normalizeAITimelineTaskIdentifier(asStringValue(readChangeValue(changes, "task_id", "id")))
		}

		title := truncateRunes(strings.TrimSpace(operation.Title), 240)
		if title == "" {
			title = truncateRunes(strings.TrimSpace(asStringValue(readChangeValue(changes, "title"))), 240)
		}
		if title == "" && nestedTask != nil {
			title = truncateRunes(strings.TrimSpace(nestedTask.Title), 240)
		}

		description := truncateRunes(strings.TrimSpace(operation.Description), 4000)
		if description == "" {
			description = truncateRunes(strings.TrimSpace(asStringValue(readChangeValue(changes, "description"))), 4000)
		}
		if description == "" && nestedTask != nil {
			description = truncateRunes(strings.TrimSpace(nestedTask.Description), 4000)
		}

		status := ""
		if strings.TrimSpace(operation.Status) != "" {
			status = normalizeTaskStatusValue(operation.Status)
			if status == "" {
				status = "todo"
			}
		}
		if status == "" {
			statusCandidate := asStringValue(readChangeValue(changes, "status"))
			if statusCandidate != "" {
				status = normalizeTaskStatusValue(statusCandidate)
				if status == "" {
					status = "todo"
				}
			}
		}
		if status == "" && nestedTask != nil && strings.TrimSpace(nestedTask.Status) != "" {
			status = normalizeTaskStatusValue(nestedTask.Status)
			if status == "" {
				status = "todo"
			}
		}

		durationUnit := ""
		if strings.TrimSpace(operation.DurationUnit) != "" {
			durationUnit = normalizeTimelineDurationUnit(operation.DurationUnit)
		}
		if durationUnit == "" {
			durationCandidate := asStringValue(readChangeValue(changes, "duration_unit"))
			if durationCandidate != "" {
				durationUnit = normalizeTimelineDurationUnit(durationCandidate)
			}
		}
		if durationUnit == "" && nestedTask != nil && strings.TrimSpace(nestedTask.DurationUnit) != "" {
			durationUnit = normalizeTimelineDurationUnit(nestedTask.DurationUnit)
		}

		var durationValue *float64
		if operation.DurationValue != nil {
			baseUnit := durationUnit
			if baseUnit == "" {
				baseUnit = "days"
			}
			normalizedValue := normalizeTimelineDurationValue(*operation.DurationValue, baseUnit)
			durationValue = &normalizedValue
		}
		if durationValue == nil {
			parsedDuration := asFloatPointer(readChangeValue(changes, "duration_value"))
			if parsedDuration != nil {
				baseUnit := durationUnit
				if baseUnit == "" {
					baseUnit = "days"
				}
				normalizedValue := normalizeTimelineDurationValue(*parsedDuration, baseUnit)
				durationValue = &normalizedValue
			}
		}
		if durationValue == nil && nestedTask != nil && nestedTask.DurationValue != nil {
			baseUnit := durationUnit
			if baseUnit == "" {
				baseUnit = "days"
			}
			normalizedValue := normalizeTimelineDurationValue(*nestedTask.DurationValue, baseUnit)
			durationValue = &normalizedValue
		}

		var budget *float64
		if operation.Budget != nil {
			normalizedBudget := normalizeTimelineBudgetValue(*operation.Budget)
			budget = &normalizedBudget
		}
		if budget == nil {
			parsedBudget := asFloatPointer(readChangeValue(changes, "budget"))
			if parsedBudget != nil {
				normalizedBudget := normalizeTimelineBudgetValue(*parsedBudget)
				budget = &normalizedBudget
			}
		}
		if budget == nil && nestedTask != nil && nestedTask.Budget != nil {
			normalizedBudget := normalizeTimelineBudgetValue(*nestedTask.Budget)
			budget = &normalizedBudget
		}

		var actualCost *float64
		if operation.ActualCost != nil {
			normalizedCost := normalizeTimelineBudgetValue(*operation.ActualCost)
			actualCost = &normalizedCost
		}
		if actualCost == nil {
			parsedCost := asFloatPointer(readChangeValue(changes, "actual_cost", "spent", "actualCost"))
			if parsedCost != nil {
				normalizedCost := normalizeTimelineBudgetValue(*parsedCost)
				actualCost = &normalizedCost
			}
		}
		if actualCost == nil && nestedTask != nil && nestedTask.ActualCost != nil {
			normalizedCost := normalizeTimelineBudgetValue(*nestedTask.ActualCost)
			actualCost = &normalizedCost
		}

		taskType = truncateRunes(taskType, 48)

		switch opName {
		case "delete_task":
			if taskID == "" {
				continue
			}
			normalized = append(normalized, aiTimelineEditOperation{
				Op:     opName,
				TaskID: taskID,
			})
		case "update_task":
			if taskID == "" {
				continue
			}
			if title == "" &&
				status == "" &&
				taskType == "" &&
				sprintName == "" &&
				description == "" &&
				budget == nil &&
				actualCost == nil &&
				durationUnit == "" &&
				durationValue == nil {
				continue
			}
			normalized = append(normalized, aiTimelineEditOperation{
				Op:            opName,
				TaskID:        taskID,
				SprintName:    sprintName,
				Title:         title,
				Status:        status,
				TaskType:      taskType,
				Budget:        budget,
				ActualCost:    actualCost,
				DurationUnit:  durationUnit,
				DurationValue: durationValue,
				Description:   description,
			})
		case "add_task":
			if title == "" {
				continue
			}
			normalized = append(normalized, aiTimelineEditOperation{
				Op:            opName,
				SprintName:    sprintName,
				Title:         title,
				Status:        status,
				TaskType:      taskType,
				Budget:        budget,
				ActualCost:    actualCost,
				DurationUnit:  durationUnit,
				DurationValue: durationValue,
				Description:   description,
			})
		}
	}
	return normalized
}

func asStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		return strings.TrimSpace(strconv.FormatFloat(typed, 'f', -1, 64))
	case float32:
		return strings.TrimSpace(strconv.FormatFloat(float64(typed), 'f', -1, 64))
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	default:
		return ""
	}
}

func asFloatPointer(value any) *float64 {
	switch typed := value.(type) {
	case float64:
		if !math.IsNaN(typed) && !math.IsInf(typed, 0) {
			copy := typed
			return &copy
		}
	case float32:
		parsed := float64(typed)
		if !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
			copy := parsed
			return &copy
		}
	case int:
		parsed := float64(typed)
		copy := parsed
		return &copy
	case int64:
		parsed := float64(typed)
		copy := parsed
		return &copy
	case int32:
		parsed := float64(typed)
		copy := parsed
		return &copy
	case uint:
		parsed := float64(typed)
		copy := parsed
		return &copy
	case uint64:
		parsed := float64(typed)
		copy := parsed
		return &copy
	case uint32:
		parsed := float64(typed)
		copy := parsed
		return &copy
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil
		}
		copy := parsed
		return &copy
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil
		}
		copy := parsed
		return &copy
	}
	return nil
}

func decodeJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var decoded string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(decoded)
}

func classifyAITimelineEditIntent(
	ctx context.Context,
	roomID string,
	prompt string,
	projectSummaryJSON string,
	limits aiOrganizeLimits,
) (AIIntentResponse, error) {
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nCurrent project summary JSON:\n%s\n\nUser request:\n%s\n\nReturn only the intent classification JSON.",
		roomID,
		strings.TrimSpace(projectSummaryJSON),
		strings.TrimSpace(prompt),
	)
	raw, err := generateAIOrganizeStructuredJSON(ctx, aiTimelineIntentSystemPrompt, userPrompt, limits)
	if err != nil {
		return AIIntentResponse{}, err
	}
	return parseAIIntentResponse(raw)
}

func parseAIIntentResponse(raw string) (AIIntentResponse, error) {
	candidates := extractJSONObjectsCandidates(raw)
	if len(candidates) == 0 {
		return AIIntentResponse{}, fmt.Errorf("ai intent response did not contain JSON")
	}

	var lastErr error
	for _, content := range candidates {
		var parsed AIIntentResponse
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			lastErr = err
			continue
		}
		intent := normalizeAIIntent(parsed.Intent)
		if intent == "" {
			lastErr = fmt.Errorf("invalid intent value")
			continue
		}
		return AIIntentResponse{
			Intent:         intent,
			AssistantReply: truncateRunes(strings.TrimSpace(parsed.AssistantReply), 2000),
		}, nil
	}
	if lastErr != nil {
		return AIIntentResponse{}, lastErr
	}
	return AIIntentResponse{}, fmt.Errorf("ai intent response was not parseable")
}

func normalizeAIIntent(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "chat":
		return "chat"
	case "modify_project":
		return "modify_project"
	default:
		return ""
	}
}

func buildAITimelineIntentSummaryJSON(project aiTimelineProject) (string, error) {
	const maxSummarySprints = 12
	const maxSummaryTasksPerSprint = 10

	summary := aiTimelineIntentSummary{
		ProjectName:    truncateRunes(strings.TrimSpace(project.ProjectName), 180),
		TechStack:      normalizeTimelineStringSlice(project.TechStack, nil),
		TargetAudience: truncateRunes(strings.TrimSpace(project.TargetAudience), 180),
		EstimatedCost:  truncateRunes(strings.TrimSpace(project.EstimatedCost), 120),
		RolesNeeded:    normalizeTimelineStringSlice(project.RolesNeeded, nil),
		SprintCount:    len(project.Sprints),
		TaskCount:      0,
		Sprints:        make([]aiTimelineIntentSprintSummary, 0, len(project.Sprints)),
	}
	if summary.ProjectName == "" {
		summary.ProjectName = "AI Project Timeline"
	}

	for sprintIndex, sprint := range project.Sprints {
		summary.TaskCount += len(sprint.Tasks)
		if sprintIndex >= maxSummarySprints {
			continue
		}
		sprintSummary := aiTimelineIntentSprintSummary{
			Name:         truncateRunes(strings.TrimSpace(sprint.Name), 160),
			DurationDays: sprint.DurationDays,
			TaskCount:    len(sprint.Tasks),
			Tasks:        make([]aiTimelineIntentTaskSummary, 0, len(sprint.Tasks)),
		}
		if sprintSummary.Name == "" {
			sprintSummary.Name = fmt.Sprintf("Sprint %d", sprintIndex+1)
		}
		for taskIndex, task := range sprint.Tasks {
			if taskIndex >= maxSummaryTasksPerSprint {
				break
			}
			title := truncateRunes(strings.TrimSpace(task.Title), 180)
			if title == "" {
				continue
			}
			sprintSummary.Tasks = append(sprintSummary.Tasks, aiTimelineIntentTaskSummary{
				Title:  title,
				Status: normalizeTaskStatusValue(task.Status),
				Type:   truncateRunes(strings.ToLower(strings.TrimSpace(task.Type)), 48),
			})
		}
		summary.Sprints = append(summary.Sprints, sprintSummary)
	}

	encoded, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func buildAITimelineEditSummaryJSON(project aiTimelineProject) (string, error) {
	summary := aiTimelineEditSummary{
		ProjectName:    truncateRunes(strings.TrimSpace(project.ProjectName), 180),
		TechStack:      normalizeTimelineStringSlice(project.TechStack, nil),
		TargetAudience: truncateRunes(strings.TrimSpace(project.TargetAudience), 180),
		EstimatedCost:  truncateRunes(strings.TrimSpace(project.EstimatedCost), 120),
		RolesNeeded:    normalizeTimelineStringSlice(project.RolesNeeded, nil),
		SprintCount:    len(project.Sprints),
		TaskCount:      0,
		Sprints:        make([]aiTimelineEditSprintSummary, 0, len(project.Sprints)),
	}
	if summary.ProjectName == "" {
		summary.ProjectName = "AI Project Timeline"
	}

	for sprintIndex, sprint := range project.Sprints {
		sprintSummary := aiTimelineEditSprintSummary{
			Name:         truncateRunes(strings.TrimSpace(sprint.Name), 160),
			DurationDays: sprint.DurationDays,
			StartDate:    strings.TrimSpace(sprint.StartDate),
			EndDate:      strings.TrimSpace(sprint.EndDate),
			Tasks:        make([]aiTimelineEditTaskSummary, 0, len(sprint.Tasks)),
		}
		if sprintSummary.Name == "" {
			sprintSummary.Name = fmt.Sprintf("Sprint %d", sprintIndex+1)
		}

		for _, task := range sprint.Tasks {
			taskID := normalizeAITimelineTaskIdentifier(firstNonEmpty(task.TaskID, task.ID))
			if taskID == "" {
				taskID = fmt.Sprintf("missing-id-%d", summary.TaskCount+1)
			}
			title := truncateRunes(strings.TrimSpace(task.Title), 240)
			if title == "" {
				continue
			}
			status := normalizeTaskStatusValue(task.Status)
			if status == "" {
				status = "todo"
			}
			durationUnit := normalizeTimelineDurationUnit(task.DurationUnit)
			durationValue := normalizeTimelineDurationValue(task.DurationValue, durationUnit)
			sprintSummary.Tasks = append(sprintSummary.Tasks, aiTimelineEditTaskSummary{
				TaskID:        taskID,
				Title:         title,
				Status:        status,
				Type:          truncateRunes(strings.ToLower(strings.TrimSpace(task.Type)), 48),
				Budget:        normalizeTimelineBudgetValue(task.Budget),
				ActualCost:    normalizeTimelineBudgetValue(task.ActualCost),
				DurationUnit:  durationUnit,
				DurationValue: durationValue,
			})
			summary.TaskCount++
		}
		summary.Sprints = append(summary.Sprints, sprintSummary)
	}

	encoded, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func applyAITimelineEditOperations(
	current aiTimelineProject,
	operations aiTimelineEditOperationsResponse,
) aiTimelineProject {
	edited := current
	edited.AssistantReply = truncateRunes(strings.TrimSpace(operations.AssistantReply), 2000)

	if patch := operations.ProjectPatch; hasAITimelineProjectPatch(patch) {
		if patch.ProjectName != "" {
			edited.ProjectName = patch.ProjectName
		}
		if len(patch.TechStack) > 0 {
			edited.TechStack = patch.TechStack
		}
		if patch.TargetAudience != "" {
			edited.TargetAudience = patch.TargetAudience
		}
		if patch.EstimatedCost != "" {
			edited.EstimatedCost = patch.EstimatedCost
		}
		if len(patch.RolesNeeded) > 0 {
			edited.RolesNeeded = patch.RolesNeeded
		}
	}

	for _, operation := range operations.Operations {
		switch operation.Op {
		case "delete_task":
			sprintIndex, taskIndex, found := findAITimelineTaskPosition(&edited, operation.TaskID)
			if !found {
				continue
			}
			tasks := edited.Sprints[sprintIndex].Tasks
			edited.Sprints[sprintIndex].Tasks = append(tasks[:taskIndex], tasks[taskIndex+1:]...)
		case "update_task":
			sprintIndex, taskIndex, found := findAITimelineTaskPosition(&edited, operation.TaskID)
			if !found {
				continue
			}
			targetTask := edited.Sprints[sprintIndex].Tasks[taskIndex]
			if operation.Title != "" {
				targetTask.Title = operation.Title
			}
			if operation.Status != "" {
				targetTask.Status = operation.Status
			}
			if operation.TaskType != "" {
				targetTask.Type = operation.TaskType
			}
			if operation.Budget != nil {
				targetTask.Budget = *operation.Budget
			}
			if operation.ActualCost != nil {
				targetTask.ActualCost = *operation.ActualCost
			}
			if operation.DurationUnit != "" {
				targetTask.DurationUnit = operation.DurationUnit
			}
			if operation.DurationValue != nil {
				durationUnit := targetTask.DurationUnit
				if durationUnit == "" {
					durationUnit = "days"
				}
				targetTask.DurationValue = normalizeTimelineDurationValue(*operation.DurationValue, durationUnit)
			}
			if operation.Description != "" {
				targetTask.Description = operation.Description
			}
			edited.Sprints[sprintIndex].Tasks[taskIndex] = targetTask

			if operation.SprintName != "" {
				targetSprintIndex := ensureAITimelineSprint(&edited, operation.SprintName)
				if targetSprintIndex >= 0 && targetSprintIndex != sprintIndex {
					targetTaskCopy := edited.Sprints[sprintIndex].Tasks[taskIndex]
					oldTasks := edited.Sprints[sprintIndex].Tasks
					edited.Sprints[sprintIndex].Tasks = append(oldTasks[:taskIndex], oldTasks[taskIndex+1:]...)
					edited.Sprints[targetSprintIndex].Tasks = append(
						edited.Sprints[targetSprintIndex].Tasks,
						targetTaskCopy,
					)
				}
			}
		case "add_task":
			targetSprintIndex := ensureAITimelineSprint(&edited, operation.SprintName)
			if targetSprintIndex < 0 {
				continue
			}
			status := operation.Status
			if status == "" {
				status = "todo"
			}
			taskType := operation.TaskType
			if taskType == "" {
				taskType = "general"
			}
			durationUnit := operation.DurationUnit
			if durationUnit == "" {
				durationUnit = "days"
			}
			durationValue := 1.0
			if operation.DurationValue != nil {
				durationValue = normalizeTimelineDurationValue(*operation.DurationValue, durationUnit)
			}
			budget := 0.0
			if operation.Budget != nil {
				budget = normalizeTimelineBudgetValue(*operation.Budget)
			}
			actualCost := 0.0
			if operation.ActualCost != nil {
				actualCost = normalizeTimelineBudgetValue(*operation.ActualCost)
			}
			effort := estimateEffortScoreFromDuration(durationUnit, durationValue)
			taskID := normalizeAITimelineTaskIdentifier(firstNonEmpty(operation.TaskID, operation.ID))
			edited.Sprints[targetSprintIndex].Tasks = append(
				edited.Sprints[targetSprintIndex].Tasks,
				aiTimelineTask{
					TaskID:        taskID,
					ID:            taskID,
					Title:         operation.Title,
					Status:        status,
					Type:          taskType,
					Budget:        budget,
					ActualCost:    actualCost,
					DurationUnit:  durationUnit,
					DurationValue: durationValue,
					EffortScore:   effort,
					Description:   operation.Description,
				},
			)
		}
	}

	return normalizeAITimelineProject(edited)
}

func ensureAITimelineSprint(project *aiTimelineProject, sprintName string) int {
	if project == nil {
		return -1
	}
	normalizedTarget := strings.TrimSpace(sprintName)
	if normalizedTarget == "" {
		if len(project.Sprints) == 0 {
			project.Sprints = []aiTimelineSprint{{
				ID:           "sprint-1",
				Name:         "Sprint 1",
				DurationDays: 7,
				Tasks:        []aiTimelineTask{},
			}}
		}
		return 0
	}
	for index, sprint := range project.Sprints {
		if strings.EqualFold(strings.TrimSpace(sprint.Name), normalizedTarget) {
			return index
		}
	}
	project.Sprints = append(project.Sprints, aiTimelineSprint{
		ID:           fmt.Sprintf("sprint-%d", len(project.Sprints)+1),
		Name:         truncateRunes(normalizedTarget, 160),
		DurationDays: 7,
		Tasks:        []aiTimelineTask{},
	})
	return len(project.Sprints) - 1
}

func findAITimelineTaskPosition(project *aiTimelineProject, taskID string) (int, int, bool) {
	if project == nil {
		return -1, -1, false
	}
	normalizedTarget := normalizeAITimelineTaskIdentifier(taskID)
	if normalizedTarget == "" {
		return -1, -1, false
	}
	for sprintIndex := range project.Sprints {
		for taskIndex := range project.Sprints[sprintIndex].Tasks {
			candidate := normalizeAITimelineTaskIdentifier(
				firstNonEmpty(
					project.Sprints[sprintIndex].Tasks[taskIndex].TaskID,
					project.Sprints[sprintIndex].Tasks[taskIndex].ID,
				),
			)
			if candidate == normalizedTarget {
				return sprintIndex, taskIndex, true
			}
		}
	}
	return -1, -1, false
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
	candidates := extractJSONObjectsCandidates(raw)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("ai sprint task response did not contain JSON")
	}

	var lastErr error
	for _, content := range candidates {
		var parsed struct {
			Tasks []aiTimelineTask `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			lastErr = err
			continue
		}
		normalizedTasks := normalizeAITimelineTasks(parsed.Tasks)
		if len(normalizedTasks) == 0 {
			lastErr = fmt.Errorf("ai sprint task generation returned no valid tasks")
			continue
		}
		return normalizedTasks, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("ai sprint task response did not contain parsable tasks JSON")
}

func parseAITimelineProject(raw string) (aiTimelineProject, error) {
	candidates := extractJSONObjectsCandidates(raw)
	if len(candidates) == 0 {
		return aiTimelineProject{}, fmt.Errorf("ai timeline response did not contain JSON")
	}

	var lastErr error
	for _, content := range candidates {
		parsed, err := parseAITimelineProjectCandidate(content)
		if err != nil {
			lastErr = err
			continue
		}

		normalized := normalizeAITimelineProject(parsed)
		if len(normalized.Sprints) == 0 {
			lastErr = fmt.Errorf("ai timeline returned no valid sprints")
			continue
		}
		return normalized, nil
	}
	if lastErr != nil {
		return aiTimelineProject{}, lastErr
	}
	return aiTimelineProject{}, fmt.Errorf("ai timeline response did not contain parsable project JSON")
}

func parseAITimelineProjectCandidate(content string) (aiTimelineProject, error) {
	// Backward-compatible direct schema:
	// { project_name, ..., sprints: [...] }
	var direct aiTimelineProject
	if err := json.Unmarshal([]byte(content), &direct); err == nil && len(direct.Sprints) > 0 {
		return direct, nil
	}

	// Preferred schema:
	// { assistant_reply: "...", timeline: { ...project... } }
	var envelope struct {
		AssistantReply  string          `json:"assistant_reply"`
		Timeline        json.RawMessage `json:"timeline"`
		Project         json.RawMessage `json:"project"`
		ProjectTimeline json.RawMessage `json:"project_timeline"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return aiTimelineProject{}, err
	}

	nestedPayload := pickFirstNonEmptyJSONRaw(
		envelope.Timeline,
		envelope.ProjectTimeline,
		envelope.Project,
	)
	if len(nestedPayload) == 0 {
		return aiTimelineProject{}, fmt.Errorf("missing 'timeline' object in AI response")
	}

	var nested aiTimelineProject
	if err := json.Unmarshal(nestedPayload, &nested); err != nil {
		return aiTimelineProject{}, err
	}
	if strings.TrimSpace(envelope.AssistantReply) != "" {
		nested.AssistantReply = strings.TrimSpace(envelope.AssistantReply)
	}
	return nested, nil
}

func pickFirstNonEmptyJSONRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		trimmed := strings.TrimSpace(string(value))
		if trimmed == "" || trimmed == "null" {
			continue
		}
		return value
	}
	return nil
}

func trimAIResponseCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```JSON")
	trimmed = strings.TrimPrefix(trimmed, "```")
	if endFence := strings.LastIndex(trimmed, "```"); endFence >= 0 {
		trimmed = trimmed[:endFence]
	}
	return strings.TrimSpace(trimmed)
}

func extractJSONObjectsCandidates(raw string) []string {
	text := trimAIResponseCodeFence(raw)
	if text == "" {
		return nil
	}

	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") && json.Valid([]byte(text)) {
		return []string{text}
	}

	candidates := make([]string, 0, 2)
	start := -1
	depth := 0
	inString := false
	escaped := false

	for idx := 0; idx < len(text); idx++ {
		char := text[idx]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}

		if char == '"' {
			inString = true
			continue
		}
		if char == '{' {
			if depth == 0 {
				start = idx
			}
			depth++
			continue
		}
		if char == '}' && depth > 0 {
			depth--
			if depth == 0 && start >= 0 {
				candidate := strings.TrimSpace(text[start : idx+1])
				if candidate != "" {
					candidates = append(candidates, candidate)
				}
				start = -1
			}
		}
	}

	if len(candidates) == 0 {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			return []string{trimmed}
		}
	}
	return candidates
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

func normalizeTimelineBudgetValue(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1_000_000_000 {
		return 1_000_000_000
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
		taskID := normalizeAITimelineTaskIdentifier(firstNonEmpty(task.TaskID, task.ID))
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
		budget := normalizeTimelineBudgetValue(task.Budget)
		actualCost := normalizeTimelineBudgetValue(task.ActualCost)
		durationUnit := normalizeTimelineDurationUnit(task.DurationUnit)
		durationValue := normalizeTimelineDurationValue(task.DurationValue, durationUnit)
		effort := task.EffortScore
		if effort < 1 || effort > 10 {
			effort = estimateEffortScoreFromDuration(durationUnit, durationValue)
		}
		description := truncateRunes(strings.TrimSpace(task.Description), 4000)
		normalized = append(normalized, aiTimelineTask{
			TaskID:        taskID,
			ID:            taskID,
			Title:         title,
			Status:        status,
			Type:          taskType,
			Budget:        budget,
			ActualCost:    actualCost,
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
	assistantReply := truncateRunes(strings.TrimSpace(input.AssistantReply), 2000)
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
		sprintID := truncateRunes(strings.TrimSpace(sprint.ID), 80)
		if sprintID == "" {
			sprintID = fmt.Sprintf("sprint-%d", sprintIndex+1)
		}

		normalizedSprints = append(normalizedSprints, aiTimelineSprint{
			ID:             sprintID,
			Name:           sprintName,
			StartDate:      startDate.Format("2006-01-02"),
			EndDate:        endDate.Format("2006-01-02"),
			DurationDays:   durationDays,
			TasksGenerated: len(normalizedTasks) > 0,
			Tasks:          normalizedTasks,
		})
	}

	return aiTimelineProject{
		AssistantReply: assistantReply,
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

func formatBudgetValue(value float64) string {
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

func normalizeAITimelineTaskIdentifier(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := parseFlexibleTaskUUID(trimmed)
	if err != nil {
		return trimmed
	}
	return strings.TrimSpace(parsed.String())
}

func parseAITimelineTaskUUID(raw string) (gocql.UUID, bool) {
	parsed, err := parseFlexibleTaskUUID(strings.TrimSpace(raw))
	if err != nil {
		return gocql.UUID{}, false
	}
	return parsed, true
}

func flattenAITimelineProjectTasks(project *aiTimelineProject) []aiTimelineFlatTask {
	if project == nil {
		return nil
	}
	flat := make([]aiTimelineFlatTask, 0, 32)
	for sprintIndex := range project.Sprints {
		sprint := project.Sprints[sprintIndex]
		sprintName := truncateRunes(strings.TrimSpace(sprint.Name), 160)
		for taskIndex := range sprint.Tasks {
			flat = append(flat, aiTimelineFlatTask{
				SprintIndex: sprintIndex,
				TaskIndex:   taskIndex,
				SprintName:  sprintName,
				StartDate:   strings.TrimSpace(sprint.StartDate),
				EndDate:     strings.TrimSpace(sprint.EndDate),
				Task:        sprint.Tasks[taskIndex],
			})
		}
	}
	return flat
}

func setAITimelineTaskID(project *aiTimelineProject, sprintIndex, taskIndex int, taskID string) {
	if project == nil {
		return
	}
	if sprintIndex < 0 || sprintIndex >= len(project.Sprints) {
		return
	}
	if taskIndex < 0 || taskIndex >= len(project.Sprints[sprintIndex].Tasks) {
		return
	}
	normalizedTaskID := normalizeAITimelineTaskIdentifier(taskID)
	project.Sprints[sprintIndex].Tasks[taskIndex].TaskID = normalizedTaskID
	project.Sprints[sprintIndex].Tasks[taskIndex].ID = normalizedTaskID
}

func buildAITimelineTaskDescription(task aiTimelineTask, startDate, endDate string) string {
	baseDescription, existingEntries := parseTaskMetadataEntries(task.Description)
	managedKeys := map[string]struct{}{
		"type":          {},
		"budget":        {},
		"duration":      {},
		"effort":        {},
		"sprint window": {},
		"spent":         {},
		"actual cost":   {},
		"actual_cost":   {},
		"cost":          {},
	}
	metadataParts := make([]string, 0, len(existingEntries)+6)
	for _, entry := range existingEntries {
		if _, managed := managedKeys[entry.key]; managed {
			continue
		}
		metadataParts = append(metadataParts, entry.raw)
	}

	taskType := truncateRunes(strings.TrimSpace(task.Type), 48)
	if taskType != "" {
		metadataParts = append(metadataParts, fmt.Sprintf("Type: %s", taskType))
	}
	if task.Budget > 0 {
		metadataParts = append(metadataParts, fmt.Sprintf("Budget: $%s", formatBudgetValue(task.Budget)))
	}
	if task.ActualCost > 0 {
		metadataParts = append(metadataParts, fmt.Sprintf("Spent: $%s", formatBudgetValue(task.ActualCost)))
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
	if strings.TrimSpace(startDate) != "" || strings.TrimSpace(endDate) != "" {
		metadataParts = append(
			metadataParts,
			fmt.Sprintf("Sprint Window: %s -> %s", strings.TrimSpace(startDate), strings.TrimSpace(endDate)),
		)
	}

	baseDescription = truncateRunes(strings.TrimSpace(baseDescription), 3600)
	if len(metadataParts) == 0 {
		return baseDescription
	}
	metadataBlock := "[" + strings.Join(metadataParts, " | ") + "]"
	if baseDescription == "" {
		return truncateRunes(metadataBlock, 4000)
	}
	return truncateRunes(baseDescription+"\n\n"+metadataBlock, 4000)
}

func extractTaskMetadataValue(description string, key string) string {
	_, entries := parseTaskMetadataEntries(description)
	for _, entry := range entries {
		if entry.key != strings.ToLower(strings.TrimSpace(key)) {
			continue
		}
		raw := entry.raw
		if idx := strings.Index(raw, ":"); idx >= 0 {
			return strings.TrimSpace(raw[idx+1:])
		}
		return strings.TrimSpace(raw)
	}
	return ""
}

func extractTaskDurationMetadata(description string) (string, float64, bool) {
	value := extractTaskMetadataValue(description, "duration")
	if value == "" {
		return "", 0, false
	}
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return "", 0, false
	}
	parsedValue, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || math.IsNaN(parsedValue) || math.IsInf(parsedValue, 0) || parsedValue <= 0 {
		return "", 0, false
	}
	unit := "days"
	if len(parts) > 1 {
		unit = normalizeTimelineDurationUnit(parts[1])
	}
	return unit, normalizeTimelineDurationValue(parsedValue, unit), true
}

func extractTaskEffortMetadata(description string) (int, bool) {
	value := extractTaskMetadataValue(description, "effort")
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 || parsed > 10 {
		return 0, false
	}
	return parsed, true
}

func extractTaskSprintWindowMetadata(description string) (string, string) {
	value := extractTaskMetadataValue(description, "sprint window")
	if value == "" {
		return "", ""
	}
	parts := strings.SplitN(value, "->", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func buildAITaskFromOperation(currentDescription string, operation aiTimelineEditOperation) (aiTimelineTask, string, string) {
	nextTask := aiTimelineTask{
		Type:          strings.TrimSpace(extractTaskMetadataValue(currentDescription, "type")),
		Budget:        0,
		ActualCost:    0,
		DurationUnit:  "days",
		DurationValue: 1,
		EffortScore:   0,
		Description:   currentDescription,
	}
	if currentBudget := extractTaskBudget(currentDescription); currentBudget != nil {
		nextTask.Budget = *currentBudget
	}
	if currentActualCost := extractTaskActualCost(currentDescription); currentActualCost != nil {
		nextTask.ActualCost = *currentActualCost
	}
	if unit, value, ok := extractTaskDurationMetadata(currentDescription); ok {
		nextTask.DurationUnit = unit
		nextTask.DurationValue = value
	}
	if effort, ok := extractTaskEffortMetadata(currentDescription); ok {
		nextTask.EffortScore = effort
	}
	startDate, endDate := extractTaskSprintWindowMetadata(currentDescription)

	if operation.TaskType != "" {
		nextTask.Type = operation.TaskType
	}
	if operation.Budget != nil {
		nextTask.Budget = *operation.Budget
	}
	if operation.ActualCost != nil {
		nextTask.ActualCost = *operation.ActualCost
	}
	if operation.DurationUnit != "" {
		nextTask.DurationUnit = operation.DurationUnit
	}
	if operation.DurationValue != nil {
		baseUnit := nextTask.DurationUnit
		if baseUnit == "" {
			baseUnit = "days"
		}
		nextTask.DurationValue = normalizeTimelineDurationValue(*operation.DurationValue, baseUnit)
	}
	if operation.Description != "" {
		nextTask.Description = operation.Description
	}
	if nextTask.Type == "" {
		nextTask.Type = "general"
	}
	if nextTask.DurationUnit == "" {
		nextTask.DurationUnit = "days"
	}
	if nextTask.DurationValue <= 0 {
		nextTask.DurationValue = 1
	}
	if nextTask.EffortScore < 1 || nextTask.EffortScore > 10 {
		nextTask.EffortScore = estimateEffortScoreFromDuration(nextTask.DurationUnit, nextTask.DurationValue)
	}
	return nextTask, startDate, endDate
}

func (h *RoomHandler) applyAIOperations(
	ctx context.Context,
	roomID string,
	operations []aiTimelineEditOperation,
) (int, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return 0, fmt.Errorf("task storage unavailable")
	}
	roomUUID, _, err := resolveTaskRoomUUID(roomID)
	if err != nil {
		return 0, err
	}

	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table("tasks"),
	)
	deleteQuery := fmt.Sprintf(
		`DELETE FROM %s WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
	)
	selectDescriptionQuery := fmt.Sprintf(
		`SELECT description FROM %s WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
	)

	now := time.Now().UTC()
	changedRows := 0

	for index := range operations {
		operation := &operations[index]
		switch operation.Op {
		case "update_task":
			taskUUID, ok := parseAITimelineTaskUUID(operation.TaskID)
			if !ok {
				continue
			}

			setClauses := make([]string, 0, 8)
			args := make([]interface{}, 0, 12)

			if operation.Title != "" {
				setClauses = append(setClauses, "title = ?")
				args = append(args, truncateRunes(strings.TrimSpace(operation.Title), 240))
			}
			if operation.Status != "" {
				setClauses = append(setClauses, "status = ?")
				args = append(args, normalizeTaskStatusValue(operation.Status))
			}
			if operation.SprintName != "" {
				setClauses = append(setClauses, "sprint_name = ?")
				args = append(args, nullableTrimmedText(truncateRunes(strings.TrimSpace(operation.SprintName), 160)))
			}

			if operation.Description != "" ||
				operation.Budget != nil ||
				operation.ActualCost != nil ||
				operation.TaskType != "" ||
				operation.DurationUnit != "" ||
				operation.DurationValue != nil {
				currentDescription := ""
				if err := h.scylla.Session.Query(selectDescriptionQuery, roomUUID, taskUUID).WithContext(ctx).Scan(&currentDescription); err != nil {
					if err == gocql.ErrNotFound {
						continue
					}
					return changedRows, err
				}
				taskForDescription, startDate, endDate := buildAITaskFromOperation(currentDescription, *operation)
				nextDescription := buildAITimelineTaskDescription(taskForDescription, startDate, endDate)
				setClauses = append(setClauses, "description = ?")
				args = append(args, nextDescription)
			}

			if len(setClauses) == 0 {
				continue
			}
			setClauses = append(setClauses, "updated_at = ?")
			args = append(args, now)
			setClauses = append(setClauses, "status_actor_id = ?")
			args = append(args, nullableTrimmedText("tora_ai"))
			setClauses = append(setClauses, "status_actor_name = ?")
			args = append(args, nullableTrimmedText("Tora AI"))
			if operation.Status != "" {
				setClauses = append(setClauses, "status_changed_at = ?")
				args = append(args, now)
			}
			args = append(args, roomUUID, taskUUID)
			updateQuery := fmt.Sprintf(
				`UPDATE %s SET %s WHERE room_id = ? AND id = ?`,
				h.scylla.Table("tasks"),
				strings.Join(setClauses, ", "),
			)
			if err := h.scylla.Session.Query(updateQuery, args...).WithContext(ctx).Exec(); err != nil {
				return changedRows, err
			}
			changedRows++
		case "add_task":
			title := truncateRunes(strings.TrimSpace(operation.Title), 240)
			if title == "" {
				continue
			}
			status := normalizeTaskStatusValue(operation.Status)
			if status == "" {
				status = "todo"
			}
			sprintName := truncateRunes(strings.TrimSpace(operation.SprintName), 160)
			taskType := truncateRunes(strings.TrimSpace(operation.TaskType), 48)
			if taskType == "" {
				taskType = "general"
			}
			durationUnit := operation.DurationUnit
			if durationUnit == "" {
				durationUnit = "days"
			}
			durationValue := 1.0
			if operation.DurationValue != nil {
				durationValue = normalizeTimelineDurationValue(*operation.DurationValue, durationUnit)
			}
			budget := 0.0
			if operation.Budget != nil {
				budget = normalizeTimelineBudgetValue(*operation.Budget)
			}
			actualCost := 0.0
			if operation.ActualCost != nil {
				actualCost = normalizeTimelineBudgetValue(*operation.ActualCost)
			}
			taskDescription := buildAITimelineTaskDescription(
				aiTimelineTask{
					Title:         title,
					Status:        status,
					Type:          taskType,
					Budget:        budget,
					ActualCost:    actualCost,
					DurationUnit:  durationUnit,
					DurationValue: durationValue,
					EffortScore:   estimateEffortScoreFromDuration(durationUnit, durationValue),
					Description:   operation.Description,
				},
				"",
				"",
			)

			newTaskID, taskIDErr := gocql.RandomUUID()
			if taskIDErr != nil {
				return changedRows, taskIDErr
			}
			operation.TaskID = strings.TrimSpace(newTaskID.String())
			operation.ID = operation.TaskID
			if err := h.scylla.Session.Query(
				insertQuery,
				roomUUID,
				newTaskID,
				title,
				taskDescription,
				status,
				sprintName,
				nil,
				nullableTrimmedText("tora_ai"),
				nullableTrimmedText("Tora AI"),
				now,
				now,
				now,
			).WithContext(ctx).Exec(); err != nil {
				return changedRows, err
			}
			changedRows++
		case "delete_task":
			taskUUID, ok := parseAITimelineTaskUUID(operation.TaskID)
			if !ok {
				continue
			}
			if err := h.scylla.Session.Query(deleteQuery, roomUUID, taskUUID).WithContext(ctx).Exec(); err != nil {
				return changedRows, err
			}
			changedRows++
		}
	}

	return changedRows, nil
}

func (h *RoomHandler) persistAITimelineTaskDiff(
	ctx context.Context,
	roomUUID gocql.UUID,
	assigneeID *gocql.UUID,
	current *aiTimelineProject,
	edited *aiTimelineProject,
) (int, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil || edited == nil {
		return 0, fmt.Errorf("task storage unavailable")
	}

	currentByID := make(map[gocql.UUID]aiTimelineFlatTask, 64)
	for _, entry := range flattenAITimelineProjectTasks(current) {
		taskID := normalizeAITimelineTaskIdentifier(firstNonEmpty(entry.Task.TaskID, entry.Task.ID))
		parsedTaskID, ok := parseAITimelineTaskUUID(taskID)
		if !ok {
			continue
		}
		currentByID[parsedTaskID] = entry
	}

	updateQuery := fmt.Sprintf(
		`UPDATE %s SET title = ?, description = ?, status = ?, sprint_name = ?, updated_at = ?, status_actor_id = ?, status_actor_name = ?, status_changed_at = ? WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
	)
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table("tasks"),
	)
	deleteQuery := fmt.Sprintf(
		`DELETE FROM %s WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
	)

	now := time.Now().UTC()
	seenExistingIDs := make(map[gocql.UUID]struct{}, len(currentByID))
	changedRows := 0

	for _, entry := range flattenAITimelineProjectTasks(edited) {
		title := truncateRunes(strings.TrimSpace(entry.Task.Title), 240)
		if title == "" {
			continue
		}
		status := normalizeTaskStatusValue(entry.Task.Status)
		if status == "" {
			status = "todo"
		}
		description := buildAITimelineTaskDescription(entry.Task, entry.StartDate, entry.EndDate)
		sprintName := truncateRunes(strings.TrimSpace(entry.SprintName), 160)

		taskID := normalizeAITimelineTaskIdentifier(firstNonEmpty(entry.Task.TaskID, entry.Task.ID))
		parsedTaskID, taskIDExists := parseAITimelineTaskUUID(taskID)
		if taskIDExists {
			if currentEntry, existsInCurrent := currentByID[parsedTaskID]; existsInCurrent {
				currentTitle := truncateRunes(strings.TrimSpace(currentEntry.Task.Title), 240)
				currentStatus := normalizeTaskStatusValue(currentEntry.Task.Status)
				if currentStatus == "" {
					currentStatus = "todo"
				}
				currentSprintName := truncateRunes(strings.TrimSpace(currentEntry.SprintName), 160)
				currentDescription := buildAITimelineTaskDescription(
					currentEntry.Task,
					currentEntry.StartDate,
					currentEntry.EndDate,
				)
				if title == currentTitle &&
					status == currentStatus &&
					sprintName == currentSprintName &&
					description == currentDescription {
					seenExistingIDs[parsedTaskID] = struct{}{}
					setAITimelineTaskID(edited, entry.SprintIndex, entry.TaskIndex, parsedTaskID.String())
					continue
				}
				if err := h.scylla.Session.Query(
					updateQuery,
					title,
					description,
					status,
					sprintName,
					now,
					nullableTrimmedText("tora_ai"),
					nullableTrimmedText("Tora AI"),
					now,
					roomUUID,
					parsedTaskID,
				).WithContext(ctx).Exec(); err != nil {
					return changedRows, err
				}
				seenExistingIDs[parsedTaskID] = struct{}{}
				setAITimelineTaskID(edited, entry.SprintIndex, entry.TaskIndex, parsedTaskID.String())
				changedRows++
				continue
			}
		}

		newTaskID, taskIDErr := gocql.RandomUUID()
		if taskIDErr != nil {
			return changedRows, taskIDErr
		}
		if err := h.scylla.Session.Query(
			insertQuery,
			roomUUID,
			newTaskID,
			title,
			description,
			status,
			sprintName,
			assigneeID,
			nullableTrimmedText("tora_ai"),
			nullableTrimmedText("Tora AI"),
			now,
			now,
			now,
		).WithContext(ctx).Exec(); err != nil {
			return changedRows, err
		}
		setAITimelineTaskID(edited, entry.SprintIndex, entry.TaskIndex, newTaskID.String())
		changedRows++
	}

	for existingTaskID := range currentByID {
		if _, keep := seenExistingIDs[existingTaskID]; keep {
			continue
		}
		if err := h.scylla.Session.Query(deleteQuery, roomUUID, existingTaskID).WithContext(ctx).Exec(); err != nil {
			return changedRows, err
		}
		changedRows++
	}

	return changedRows, nil
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
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table("tasks"),
	)

	inserted := 0
	now := time.Now().UTC()
	for sprintIndex := range project.Sprints {
		sprint := &project.Sprints[sprintIndex]
		sprintName := truncateRunes(strings.TrimSpace(sprint.Name), 160)
		for taskIndex := range sprint.Tasks {
			task := &sprint.Tasks[taskIndex]
			normalizedTaskID := normalizeAITimelineTaskIdentifier(firstNonEmpty(task.TaskID, task.ID))
			taskID, taskIDParsed := parseAITimelineTaskUUID(normalizedTaskID)
			if !taskIDParsed {
				taskIDGenerated, taskIDErr := gocql.RandomUUID()
				if taskIDErr != nil {
					return inserted, taskIDErr
				}
				taskID = taskIDGenerated
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
			metadataParts := make([]string, 0, 5)
			if task.Type != "" {
				metadataParts = append(metadataParts, fmt.Sprintf("Type: %s", strings.TrimSpace(task.Type)))
			}
			if task.Budget > 0 {
				metadataParts = append(
					metadataParts,
					fmt.Sprintf("Budget: $%s", formatBudgetValue(task.Budget)),
				)
			}
			if task.ActualCost > 0 {
				metadataParts = append(
					metadataParts,
					fmt.Sprintf("Spent: $%s", formatBudgetValue(task.ActualCost)),
				)
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
				nullableTrimmedText("tora_ai"),
				nullableTrimmedText("Tora AI"),
				now,
				now,
				now,
			).WithContext(ctx).Exec(); err != nil {
				return inserted, err
			}

			task.TaskID = taskIDString
			task.ID = taskIDString
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
