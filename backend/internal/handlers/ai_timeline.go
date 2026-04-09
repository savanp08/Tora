package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/models"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sseWriter serialises SSE writes across the main goroutine and heartbeat
// goroutines so concurrent flushes don't interleave event frames.
type sseWriter struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(w http.ResponseWriter, f http.Flusher) *sseWriter {
	return &sseWriter{w: w, flusher: f}
}

func (s *sseWriter) write(eventType string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeSSEEvent(s.w, s.flusher, eventType, data)
}

// startSSEHeartbeat launches a goroutine that emits "progress" events every
// interval until the returned cancel func is called.  labels are cycled in
// order so the UI can show a rotating status without a new workflow entry.
func startSSEHeartbeat(ctx context.Context, sw *sseWriter, step string, labels []string, interval time.Duration) context.CancelFunc {
	hbCtx, cancel := context.WithCancel(ctx)
	go func() {
		i := 0
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				sw.write("progress", map[string]any{
					"step":  step,
					"label": labels[i%len(labels)],
				})
				i++
			}
		}
	}()
	return cancel
}

const (
	aiTimelinePerCallTimeout = 5 * time.Minute
	aiTimelinePromptTimeout  = 15 * time.Minute
	aiHTTPStatusClientClosed = 499
)

const aiBlueprintSystemPrompt = `You are an Expert Project Architect.
Return ONLY valid JSON with keys: "assistant_reply" and "timeline".
"assistant_reply" must be concise, professional, friendly, and never dismissive.
"timeline" must include:
- "project_name"
- "tech_stack" (array)
- "target_audience"
- "estimated_cost"
- "roles_needed" (array)
- "sprints" (array of objects with "name" and "duration_days")

Important behavior:
- If some details are missing but reasonable assumptions are possible, proceed and generate using practical assumptions.
- Do not ask follow-up questions in this step (clarification is handled by a separate intent gate).
- Do NOT generate sprint tasks in this step.`

const aiBlueprintFoundationSystemPrompt = `You are an Expert Project Architect.
Return ONLY valid JSON with keys:
- "assistant_reply"
- "project_name"
- "tech_stack" (array)
- "target_audience"
- "estimated_cost"
- "roles_needed" (array)

Rules:
- Infer practical assumptions when the user leaves gaps.
- Optimize for a thorough, high-quality project foundation, not a cut-down outline.
- Do not ask follow-up questions in this step.
- Do NOT include sprints or tasks.`

func buildAIBlueprintSprintPlanSystemPrompt(maxSprints int) string {
	if maxSprints < 1 {
		maxSprints = 1
	}
	return fmt.Sprintf(`You are an Expert Delivery Planner.
Return ONLY valid JSON with key "sprints".
Each sprint object must include:
- "name"
- "duration_days"

Rules:
- Generate a complete end-to-end project sequence that covers all major phases needed for delivery.
- Use concrete, meaningful sprint names that reflect the actual phase of work.
- Prefer thorough execution sequencing over a thin outline.
- Return between 1 and %d sprints total.
- Do NOT include tasks or prose outside the JSON.`, maxSprints)
}

const aiTimelineExecutionSystemPrompt = `You are Tora's onboarding workspace execution agent.
You receive the current workspace state and a structured project blueprint.
Your job is to build the board by using tools directly.

Execution rules:
- You MUST use create_task to build the board. Do not stop at prose.
- This is a high-quality project build, not a token-saving outline. Prefer complete, executable work over skeletal placeholder tasks.
- During onboarding, do not delete or update existing tasks. Only add missing work.
- Respect the exact group names from the blueprint unless the workspace already has the same names in a different case.
- Keep each task inside the date window of its target group.
- Keep each initial task description concise: 1-2 short sentences max. Do not generate full walkthroughs or long how-to instructions during onboarding because detailed task steps are generated later on demand.
- Every create_task call MUST include title, sprint_name, budget, start_date, due_date, and roles.
- Use assignee_id only when you have a valid member UUID from the current workspace. If unsure, omit assignee_id.
- If the workspace already has tasks from a previous partial run, avoid duplicate titles in the same group and continue filling the missing work.
- When time is limited, keep using tools on the highest-value remaining work before you summarize.
- Before finishing, call verify_task_count and then provide a concise summary of what you created and what may still be missing.`

func buildAITaskFillSystemPrompt(maxTasks int) string {
	return fmt.Sprintf(`You are an Expert Agile Manager.
Given a project blueprint and a sprint name, return strict JSON with key "tasks" (array).
Each task must include:
- "title"
- "description" (optional)
- "duration_unit" ("hours" or "days")
- "duration_value" (number)
- "status"
- "type"
- "budget" (numeric)
Keep outputs realistic and implementation-oriented.
If you include "description", keep it brief: 1-2 short sentences with no step-by-step instructions.
IMPORTANT: Return at most %d tasks per sprint. Prioritise the most impactful tasks only.`, maxTasks)
}

// resolveAITimelineTier returns the AITimelineTierLimits for the current request.
// If TEST_USER env var is "true", the user is treated as Pro (useful for local dev).
// Expand this to read a real subscription field once billing is wired up.
func resolveAITimelineTier() config.AITimelineTierLimits {
	tiers := config.LoadAppLimits().AI.TimelineTiers
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TEST_USER")), "true") {
		return tiers.Pro
	}
	return tiers.Free
}

// buildTierHint returns a compact instruction injected into every AI call when
// the user has chosen "fit to tier". It tells the model to scope the output so
// the full generation completes within the plan's sprint and task limits without
// hitting a timeout or cost overrun.
func buildTierHint(tier config.AITimelineTierLimits, tierLabel string) string {
	return fmt.Sprintf(
		"[TIER CONSTRAINT — fit to plan]\n"+
			"The user is on the %q plan. Generate output that fits completely within these hard limits:\n"+
			"- Max sprints: %d\n"+
			"- Max tasks per sprint: %d\n"+
			"- Max output tokens per call: %d\n"+
			"Prioritise the highest-value work. Prefer fewer, well-scoped items over an exhaustive list that will be cut off.\n"+
			"Do not exceed these limits under any circumstances.\n"+
			"[END TIER CONSTRAINT]",
		tierLabel,
		tier.MaxSprints,
		tier.MaxTasksPerSprint,
		tier.MaxOutputTokens,
	)
}

// tierLabelFromLimits returns a human-readable plan name matching the tier limits.
func tierLabelFromLimits(tier config.AITimelineTierLimits) string {
	tiers := config.LoadAppLimits().AI.TimelineTiers
	switch tier.MaxSprints {
	case tiers.Team.MaxSprints:
		return "team"
	case tiers.Pro.MaxSprints:
		return "pro"
	case tiers.Plus.MaxSprints:
		return "plus"
	default:
		return "free"
	}
}

func resolveAITimelineAgentProvider(modelTier string) ai.Provider {
	if ai.DefaultRouter != nil && ai.DefaultRouter.SupportsToolUse() {
		return ai.DefaultRouter
	}
	return ai.NewPromptToolUseProvider(ai.DefaultRouter, modelTier)
}

func normalizeAITimelineCallTimeout(_ time.Duration) time.Duration {
	return aiTimelinePerCallTimeout
}

func calculateAITimelineExecutionTimeout(base time.Duration, sprintCount int) time.Duration {
	base = normalizeAITimelineCallTimeout(base)
	if sprintCount < 1 {
		sprintCount = 1
	}
	timeout := base + time.Duration(sprintCount-1)*(base/2)
	if timeout < aiTimelinePerCallTimeout {
		timeout = aiTimelinePerCallTimeout
	}
	if timeout > aiTimelinePromptTimeout {
		timeout = aiTimelinePromptTimeout
	}
	return timeout
}

func calculateAITimelineLLMTimeout(base time.Duration, max time.Duration, fragments ...string) time.Duration {
	return normalizeAITimelineCallTimeout(base)
}

func calculateAITimelineBlueprintTimeout(base time.Duration, prompt string) time.Duration {
	timeout := 2 * normalizeAITimelineCallTimeout(base)
	if timeout > aiTimelinePromptTimeout {
		timeout = aiTimelinePromptTimeout
	}
	return timeout
}

func calculateAITimelineEditTimeout(base time.Duration, prompt string, projectSummaryJSON string) time.Duration {
	return calculateAITimelineLLMTimeout(base, aiTimelinePerCallTimeout, prompt, projectSummaryJSON)
}

const aiTimelineEditSystemPrompt = `You are an autonomous Project and Resource Manager acting as a JSON patcher.
You receive the current project state (each task has a database task_id) and a user request.
Return ONLY valid JSON with keys:
- "mode": "modify_project" or "chat"
- "assistant_reply": concise, professional, friendly, never dismissive
- "operations": array (required when mode="modify_project", should be empty when mode="chat")

Rules:
- "operations" must contain only task deltas, never the full project.
- The user may provide context listing valid Assignee IDs. Only use those IDs when setting "assignee_id" or "assigneeId". Never invent assignee IDs.
- To cut or manage costs, modify task "budget" and/or "spent" (alias "actual_cost") values.
- Keep workload balanced when possible so one assignee is not severely overloaded.
- Allowed operation actions:
  1) {"action":"update_task","task_id":"uuid","changes":{"title":"...","status":"todo|in_progress|done","task_type":"...","assignee_id":"uuid","budget":123,"spent":45,"actual_cost":45,"duration_unit":"hours|days","duration_value":2,"description":"...","sprint_name":"..."}}
  2) {"action":"add_task","sprint_name":"Sprint 1","task":{"title":"...","status":"todo|in_progress|done","task_type":"...","assignee_id":"uuid","budget":123,"spent":45,"actual_cost":45,"duration_unit":"hours|days","duration_value":2,"description":"..."}}
  3) {"action":"delete_task","task_id":"uuid"}
- If the request is clearly conversational or asks for explanation only, return mode="chat" and operations=[].
- If details are partially missing but intent is clear, make reasonable assumptions and still return mode="modify_project".`

const aiTimelineIntentSystemPrompt = `You are an AI Project Manager triage assistant.
Classify the next user request into one of these intents:
- "modify_project": apply concrete board/task changes now
- "chat": explanation/discussion only, no board changes
- "clarify": ask ONE concise follow-up question only when critical details are missing and assumptions would likely be wrong

Return ONLY valid JSON with keys:
- "intent"
- "assistant_reply"

Hard constraints:
- Ask at most ONE clarification question total in the conversation.
- If a clarification was already asked earlier, do NOT ask another; choose "modify_project" (using assumptions) or "chat".
- Keep "assistant_reply" concise, professional, friendly, and never dismissive.`

const aiTimelineGenerateIntentSystemPrompt = `You are an AI project-planning intake assistant.
Classify the next request into one of these intents:
- "generate_project": enough information to generate a project timeline now
- "chat": the user is asking for discussion/advice only, not generation
- "clarify": ask ONE concise follow-up question only when critical details are missing and assumptions would likely be wrong

Return ONLY valid JSON with keys:
- "intent"
- "assistant_reply"

Hard constraints:
- Ask at most ONE clarification question total in the conversation.
- If a clarification was already asked earlier, do NOT ask another; choose "generate_project" using reasonable assumptions, or "chat" if the user explicitly asked for discussion only.
- Keep "assistant_reply" concise, professional, friendly, and never dismissive.`

const (
	aiIntentChat            = "chat"
	aiIntentModifyProject   = "modify_project"
	aiIntentGenerateProject = "generate_project"
	aiIntentClarify         = "clarify"
)

type aiTimelineGenerateRequest struct {
	Prompt              string                        `json:"prompt"`
	UserID              string                        `json:"userId,omitempty"`
	DeviceID            string                        `json:"deviceId,omitempty"`
	ConversationHistory []aiTimelineConversationEntry `json:"conversation_history,omitempty"`
	FitToTier           bool                          `json:"fit_to_tier,omitempty"`
}

type AIIntentResponse struct {
	Intent         string `json:"intent"`
	AssistantReply string `json:"assistant_reply"`
}

type aiTimelineEditRequest struct {
	Prompt              string                        `json:"prompt"`
	CurrentState        json.RawMessage               `json:"current_state"`
	UserID              string                        `json:"userId,omitempty"`
	DeviceID            string                        `json:"deviceId,omitempty"`
	ConversationHistory []aiTimelineConversationEntry `json:"conversation_history,omitempty"`
	FitToTier           bool                          `json:"fit_to_tier,omitempty"`
}

type aiTimelineConversationEntry struct {
	Role   string `json:"role"`
	Text   string `json:"text"`
	Intent string `json:"intent,omitempty"`
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
	AssigneeID    string  `json:"assignee_id,omitempty"`
	Assignee      string  `json:"assignee,omitempty"`
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
	AssigneeID    string                       `json:"assignee_id,omitempty"`
	Assignee      string                       `json:"assignee,omitempty"`
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
	AssigneeID    string   `json:"assignee_id,omitempty"`
	Assignee      string   `json:"assignee,omitempty"`
	Budget        *float64 `json:"budget,omitempty"`
	ActualCost    *float64 `json:"actual_cost,omitempty"`
	DurationUnit  string   `json:"duration_unit,omitempty"`
	DurationValue *float64 `json:"duration_value,omitempty"`
	Description   string   `json:"description,omitempty"`
	SprintName    string   `json:"sprint_name,omitempty"`
}

type aiTimelineEditOperationsResponse struct {
	Mode           string                    `json:"mode,omitempty"`
	AssistantReply string                    `json:"assistant_reply,omitempty"`
	ProjectPatch   aiTimelineProjectPatch    `json:"project_patch,omitempty"`
	Operations     []aiTimelineEditOperation `json:"operations"`
}

type aiTimelineEditTaskSummary struct {
	TaskID        string  `json:"task_id"`
	Title         string  `json:"title"`
	Status        string  `json:"status,omitempty"`
	Type          string  `json:"type,omitempty"`
	AssigneeID    string  `json:"assignee_id,omitempty"`
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

type aiTimelineBlueprintProgressFunc func(step string, label string)

type aiTimelineStageError struct {
	Stage   string
	Timeout time.Duration
	Err     error
}

func (e *aiTimelineStageError) Error() string {
	if e == nil || e.Err == nil {
		return strings.TrimSpace(e.Stage)
	}
	if strings.TrimSpace(e.Stage) == "" {
		return e.Err.Error()
	}
	return strings.TrimSpace(e.Stage) + ": " + e.Err.Error()
}

func (e *aiTimelineStageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type aiTimelineErrorPayload struct {
	Error           string `json:"error"`
	Message         string `json:"message,omitempty"`
	Code            string `json:"code,omitempty"`
	Stage           string `json:"stage,omitempty"`
	Detail          string `json:"detail,omitempty"`
	Retryable       bool   `json:"retryable,omitempty"`
	ProviderStatus  int    `json:"provider_status,omitempty"`
	TimeoutMs       int64  `json:"timeout_ms,omitempty"`
	PromptTimeoutMs int64  `json:"prompt_timeout_ms,omitempty"`
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
	tier := resolveAITimelineTier()
	if len([]rune(prompt)) > tier.MaxPromptChars {
		writeAITimelineError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("Prompt exceeds the %d-character limit for your plan. Please shorten your request.", tier.MaxPromptChars))
		return
	}
	conversationHistory := normalizeAITimelineConversationHistory(req.ConversationHistory)
	tierHint := ""
	if req.FitToTier {
		tierHint = buildTierHint(tier, tierLabelFromLimits(tier))
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

	promptCtx, cancelPrompt := context.WithTimeout(r.Context(), aiTimelinePromptTimeout)
	defer cancelPrompt()

	limits := getAIOrganizeLimits()
	limits.RequestTimeout = normalizeAITimelineCallTimeout(tier.RequestTimeout)
	limits.MaxOutputTokens = tier.MaxOutputTokens
	intentCtx, cancelIntent := context.WithTimeout(promptCtx, limits.RequestTimeout)
	generateIntent, intentErr := classifyAITimelineGenerateIntent(
		intentCtx,
		roomID,
		prompt,
		conversationHistory,
		limits,
	)
	cancelIntent()
	if intentErr != nil {
		log.Printf("[ai_timeline] generation intent classification failed room_id=%q user_id=%q err=%v", roomID, userID, intentErr)
	} else if generateIntent.Intent == aiIntentChat || generateIntent.Intent == aiIntentClarify {
		assistantReply := strings.TrimSpace(generateIntent.AssistantReply)
		if assistantReply == "" {
			if generateIntent.Intent == aiIntentClarify {
				assistantReply = "Before I generate the board, share one key constraint (deadline, sprint count, or budget cap)."
			} else {
				assistantReply = "I can answer questions about planning approach without generating the board yet."
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(AIIntentResponse{
			Intent:         generateIntent.Intent,
			AssistantReply: assistantReply,
		})
		return
	}

	blueprintTimeout := calculateAITimelineBlueprintTimeout(tier.RequestTimeout, prompt)
	blueprintLimits := limits
	blueprintLimits.RequestTimeout = blueprintTimeout
	blueprintCtx, cancelBlueprint := context.WithTimeout(promptCtx, blueprintLimits.RequestTimeout)
	defer cancelBlueprint()

	generated, generateErr := generateAIProjectBlueprint(
		blueprintCtx,
		roomID,
		prompt,
		conversationHistory,
		blueprintLimits,
		tier.MaxSprints,
		nil,
		tierHint,
	)
	if generateErr != nil {
		status, payload := buildAITimelineErrorPayload("blueprint", generateErr, blueprintTimeout, aiTimelinePromptTimeout)
		writeAITimelineErrorPayload(w, status, payload)
		return
	}
	// Cap sprints based on user tier to prevent runaway serial AI calls
	if len(generated.Sprints) > tier.MaxSprints {
		generated.MissingSprints = append(generated.MissingSprints, remainingSprintNames(generated.Sprints, tier.MaxSprints)...)
		generated.Sprints = generated.Sprints[:tier.MaxSprints]
		generated.IsPartial = true
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
		sprintCtx, cancelSprint := context.WithTimeout(promptCtx, limits.RequestTimeout)
		tasks, taskErr := generateTasksForSprint(sprintCtx, blueprintJSON, sprintName, limits, tier.MaxTasksPerSprint, tierHint)
		cancelSprint()
		if taskErr != nil {
			switch {
			case errors.Is(taskErr, context.Canceled), errors.Is(taskErr, context.DeadlineExceeded):
				// Degrade gracefully: return what we have as a partial board
				log.Printf("[ai-timeline] sprint task gen timed out, degrading to partial sprint_index=%d sprint=%q room_id=%q", sprintIndex, sprintName, roomID)
				generated.IsPartial = true
				generated.MissingSprints = append(generated.MissingSprints, remainingSprintNames(generated.Sprints, sprintIndex)...)
			case errors.Is(taskErr, ai.ErrAllAIProvidersExhausted):
				generated.IsPartial = true
				generated.MissingSprints = append(generated.MissingSprints, remainingSprintNames(generated.Sprints, sprintIndex)...)
			default:
				generated.IsPartial = true
				generated.MissingSprints = append(generated.MissingSprints, remainingSprintNames(generated.Sprints, sprintIndex)...)
			}
			break
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
	persistedCount, persistErr := h.persistAITimelineTasks(promptCtx, roomUUID, assigneeID, &generated)
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

// writeSSEEvent writes a single Server-Sent Events frame and flushes the response.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data any) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(jsonBytes))
	flusher.Flush()
}

// HandleAIGenerateTimelineStream streams project generation progress as Server-Sent Events.
// Clients receive status, blueprint, sprint_tasks, chat, done, and error events incrementally
// so they can populate the board as each sprint is generated rather than waiting for completion.
func (h *RoomHandler) HandleAIGenerateTimelineStream(w http.ResponseWriter, r *http.Request) {
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
	tier := resolveAITimelineTier()
	if len([]rune(prompt)) > tier.MaxPromptChars {
		writeAITimelineError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("Prompt exceeds the %d-character limit for your plan. Please shorten your request.", tier.MaxPromptChars))
		return
	}
	conversationHistory := normalizeAITimelineConversationHistory(req.ConversationHistory)
	tierHint := ""
	if req.FitToTier {
		tierHint = buildTierHint(tier, tierLabelFromLimits(tier))
	}

	userID := normalizeIdentifier(firstNonEmpty(
		AuthUserIDFromContext(r.Context()),
		req.UserID,
		r.URL.Query().Get("userId"),
		r.URL.Query().Get("user_id"),
		r.Header.Get("X-User-Id"),
	))
	if userID == "" {
		writeAITimelineError(w, http.StatusUnauthorized, "User context is required")
		return
	}
	deviceID := strings.TrimSpace(firstNonEmpty(
		req.DeviceID,
		r.URL.Query().Get("deviceId"),
		r.URL.Query().Get("device_id"),
		r.Header.Get("X-Device-Id"),
	))
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
	if limitErr := enforcePrivateAIRequestLimits(r.Context(), userID, roomID, clientIP, deviceID); limitErr != nil {
		var exceeded *privateAILimitExceededError
		if errors.As(limitErr, &exceeded) {
			logPrivateAILimitExceeded("ai_timeline_generate_stream", exceeded, userID, roomID, clientIP, deviceID)
			writeAITimelineError(w, http.StatusTooManyRequests, exceeded.PublicMessage())
			return
		}
		writeAITimelineError(w, http.StatusServiceUnavailable, "AI limiter unavailable")
		return
	}

	promptCtx, cancelPrompt := context.WithTimeout(r.Context(), aiTimelinePromptTimeout)
	defer cancelPrompt()

	// All validation passed – switch to SSE mode. Errors from here go as SSE error events.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	limits := getAIOrganizeLimits()
	limits.RequestTimeout = normalizeAITimelineCallTimeout(tier.RequestTimeout)
	limits.MaxOutputTokens = tier.MaxOutputTokens

	// Step 1: classify intent
	writeSSEEvent(w, flusher, "status", map[string]any{
		"step":              "intent",
		"label":             "Classifying your request...",
		"timeout_ms":        limits.RequestTimeout.Milliseconds(),
		"prompt_timeout_ms": aiTimelinePromptTimeout.Milliseconds(),
		"strategy":          "single_llm_call",
	})
	intentCtx, cancelIntent := context.WithTimeout(promptCtx, limits.RequestTimeout)
	generateIntent, intentErr := classifyAITimelineGenerateIntent(intentCtx, roomID, prompt, conversationHistory, limits)
	cancelIntent()
	if intentErr != nil {
		log.Printf("[ai_timeline/stream] intent classification failed room_id=%q user_id=%q err=%v", roomID, userID, intentErr)
	} else if generateIntent.Intent == aiIntentChat || generateIntent.Intent == aiIntentClarify {
		assistantReply := strings.TrimSpace(generateIntent.AssistantReply)
		if assistantReply == "" {
			if generateIntent.Intent == aiIntentClarify {
				assistantReply = "Before I generate the board, share one key constraint (deadline, sprint count, or budget cap)."
			} else {
				assistantReply = "I can answer questions about planning approach without generating the board yet."
			}
		}
		writeSSEEvent(w, flusher, "chat", map[string]any{"intent": generateIntent.Intent, "assistant_reply": assistantReply})
		return
	}

	// Step 2: generate blueprint
	blueprintTimeout := calculateAITimelineBlueprintTimeout(tier.RequestTimeout, prompt)
	writeSSEEvent(w, flusher, "status", map[string]any{
		"step":              "blueprint",
		"label":             "Designing project blueprint...",
		"timeout_ms":        blueprintTimeout.Milliseconds(),
		"prompt_timeout_ms": aiTimelinePromptTimeout.Milliseconds(),
		"strategy":          "multi_call",
	})
	blueprintLimits := limits
	blueprintLimits.RequestTimeout = blueprintTimeout
	blueprintCtx, cancelBlueprint := context.WithTimeout(promptCtx, blueprintLimits.RequestTimeout)
	generated, generateErr := generateAIProjectBlueprint(
		blueprintCtx,
		roomID,
		prompt,
		conversationHistory,
		blueprintLimits,
		tier.MaxSprints,
		func(step string, label string) {
			writeSSEEvent(w, flusher, "status", map[string]any{
				"step":              strings.TrimSpace(step),
				"label":             strings.TrimSpace(label),
				"timeout_ms":        normalizeAITimelineCallTimeout(limits.RequestTimeout).Milliseconds(),
				"prompt_timeout_ms": aiTimelinePromptTimeout.Milliseconds(),
				"strategy":          "multi_call",
			})
		},
		tierHint,
	)
	cancelBlueprint()
	if generateErr != nil {
		_, payload := buildAITimelineErrorPayload("blueprint", generateErr, blueprintTimeout, aiTimelinePromptTimeout)
		writeSSEEvent(w, flusher, "error", payload)
		return
	}

	// Cap sprints by tier
	if len(generated.Sprints) > tier.MaxSprints {
		generated.MissingSprints = append(generated.MissingSprints, remainingSprintNames(generated.Sprints, tier.MaxSprints)...)
		generated.Sprints = generated.Sprints[:tier.MaxSprints]
		generated.IsPartial = true
	}

	// Emit blueprint structure (sprint shells without tasks so client can show sprint names)
	blueprintShell := generated
	blueprintShell.Sprints = make([]aiTimelineSprint, len(generated.Sprints))
	for i, s := range generated.Sprints {
		blueprintShell.Sprints[i] = aiTimelineSprint{
			ID:           s.ID,
			Name:         s.Name,
			StartDate:    s.StartDate,
			EndDate:      s.EndDate,
			DurationDays: s.DurationDays,
		}
	}
	writeSSEEvent(w, flusher, "blueprint", blueprintShell)

	blueprintRaw, _ := json.Marshal(generated)
	blueprintJSON := string(blueprintRaw)

	executionTimeout := calculateAITimelineExecutionTimeout(limits.RequestTimeout, len(generated.Sprints))
	writeSSEEvent(w, flusher, "status", map[string]any{
		"step":              "execute",
		"label":             "Building tasks directly in the workspace...",
		"timeout_ms":        executionTimeout.Milliseconds(),
		"prompt_timeout_ms": aiTimelinePromptTimeout.Milliseconds(),
		"strategy":          "tool_loop",
	})

	executionCtx, cancelExecution := context.WithTimeout(
		promptCtx,
		executionTimeout,
	)
	createdTaskCount, executionReply, executionTimedOut, executionErr := h.executeAITimelineBlueprintWithAgent(
		executionCtx,
		roomID,
		userID,
		blueprintJSON,
		prompt,
		tier,
		limits,
		&generated,
		func(step string, label string, sprintIndex int) {
			payload := map[string]any{
				"step":  strings.TrimSpace(step),
				"label": strings.TrimSpace(label),
			}
			if sprintIndex >= 0 {
				payload["sprint_index"] = sprintIndex
				payload["sprint_total"] = len(generated.Sprints)
			}
			writeSSEEvent(w, flusher, "status", payload)
		},
		func(sprintIndex int) {
			if sprintIndex < 0 || sprintIndex >= len(generated.Sprints) {
				return
			}
			sprint := generated.Sprints[sprintIndex]
			writeSSEEvent(w, flusher, "sprint_tasks", map[string]any{
				"sprint_index": sprintIndex,
				"sprint_name":  sprint.Name,
				"sprint_id":    sprint.ID,
				"start_date":   sprint.StartDate,
				"end_date":     sprint.EndDate,
				"tasks":        sprint.Tasks,
			})
		},
	)
	cancelExecution()

	if strings.TrimSpace(executionReply) != "" {
		generated.AssistantReply = strings.TrimSpace(executionReply)
	}
	generated.MissingSprints = collectAITimelineMissingSprints(generated)
	generated.IsPartial = executionTimedOut || executionErr != nil || len(generated.MissingSprints) > 0

	if executionErr != nil {
		if executionTimedOut && !errors.Is(executionErr, context.DeadlineExceeded) {
			executionErr = context.DeadlineExceeded
		}
		_, payload := buildAITimelineErrorPayload("execute", executionErr, executionTimeout, aiTimelinePromptTimeout)
		writeSSEEvent(w, flusher, "error", payload)
		writeSSEEvent(w, flusher, "done", map[string]any{
			"persisted_task_count": createdTaskCount,
			"is_partial":           true,
			"missing_sprints":      generated.MissingSprints,
			"assistant_reply":      generated.AssistantReply,
		})
		return
	}
	if executionTimedOut {
		_, payload := buildAITimelineErrorPayload("execute", context.DeadlineExceeded, executionTimeout, aiTimelinePromptTimeout)
		writeSSEEvent(w, flusher, "error", payload)
	}

	writeSSEEvent(w, flusher, "done", map[string]any{
		"persisted_task_count": createdTaskCount,
		"is_partial":           generated.IsPartial,
		"missing_sprints":      generated.MissingSprints,
		"assistant_reply":      generated.AssistantReply,
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
	editTier := resolveAITimelineTier()
	if len([]rune(prompt)) > editTier.MaxPromptChars {
		writeAITimelineError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("Prompt exceeds the %d-character limit for your plan. Please shorten your request.", editTier.MaxPromptChars))
		return
	}
	conversationHistory := normalizeAITimelineConversationHistory(req.ConversationHistory)
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

	promptCtx, cancelPrompt := context.WithTimeout(r.Context(), aiTimelinePromptTimeout)
	defer cancelPrompt()

	limits := getAIOrganizeLimits()
	limits.RequestTimeout = normalizeAITimelineCallTimeout(editTier.RequestTimeout)
	limits.MaxOutputTokens = editTier.MaxOutputTokens

	intentSummaryJSON, summaryErr := buildAITimelineIntentSummaryJSON(normalizedCurrent)
	if summaryErr != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Failed to summarize current_state")
		return
	}
	intentCtx, cancelIntent := context.WithTimeout(promptCtx, limits.RequestTimeout)
	intentResult, intentErr := classifyAITimelineEditIntent(
		intentCtx,
		roomID,
		prompt,
		intentSummaryJSON,
		conversationHistory,
		limits,
	)
	cancelIntent()
	if intentErr != nil {
		log.Printf("[ai_timeline] intent classification failed room_id=%q user_id=%q err=%v", roomID, userID, intentErr)
	} else if intentResult.Intent == aiIntentChat || intentResult.Intent == aiIntentClarify {
		assistantReply := strings.TrimSpace(intentResult.AssistantReply)
		if assistantReply == "" {
			if intentResult.Intent == aiIntentClarify {
				assistantReply = "I can make the change once you confirm one key detail."
			} else {
				assistantReply = "I can explain the current board and answer questions without editing it."
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(AIIntentResponse{
			Intent:         intentResult.Intent,
			AssistantReply: assistantReply,
		})
		return
	}

	editSummaryJSON, editSummaryErr := buildAITimelineEditSummaryJSON(normalizedCurrent)
	if editSummaryErr != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Failed to summarize current_state for edits")
		return
	}
	editPlanTimeout := calculateAITimelineEditTimeout(editTier.RequestTimeout, prompt, editSummaryJSON)
	editLimits := limits
	editLimits.RequestTimeout = editPlanTimeout
	editCtx, cancelEdit := context.WithTimeout(promptCtx, editLimits.RequestTimeout)
	editOps, editErr := generateAITimelineEditOperations(
		editCtx,
		roomID,
		prompt,
		editSummaryJSON,
		conversationHistory,
		editLimits,
	)
	cancelEdit()
	if editErr != nil {
		status, payload := buildAITimelineErrorPayload("plan", editErr, editPlanTimeout, aiTimelinePromptTimeout)
		writeAITimelineErrorPayload(w, status, payload)
		return
	}
	if editOps.Mode == aiIntentChat || editOps.Mode == aiIntentClarify {
		assistantReply := strings.TrimSpace(editOps.AssistantReply)
		if assistantReply == "" {
			if editOps.Mode == aiIntentClarify {
				assistantReply = "I can apply the change once you confirm one key detail."
			} else {
				assistantReply = "I can discuss the board without changing it."
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(AIIntentResponse{
			Intent:         editOps.Mode,
			AssistantReply: assistantReply,
		})
		return
	}
	persistedCount, persistErr := h.applyAIOperations(promptCtx, roomID, editOps.Operations)
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

func (h *RoomHandler) HandleAIEditTimelineStream(w http.ResponseWriter, r *http.Request) {
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
	editTier := resolveAITimelineTier()
	if len([]rune(prompt)) > editTier.MaxPromptChars {
		writeAITimelineError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("Prompt exceeds the %d-character limit for your plan. Please shorten your request.", editTier.MaxPromptChars))
		return
	}
	conversationHistory := normalizeAITimelineConversationHistory(req.ConversationHistory)
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
			logPrivateAILimitExceeded("ai_timeline_edit_stream", exceeded, userID, roomID, clientIP, deviceID)
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

	promptCtx, cancelPrompt := context.WithTimeout(r.Context(), aiTimelinePromptTimeout)
	defer cancelPrompt()

	limits := getAIOrganizeLimits()
	limits.RequestTimeout = normalizeAITimelineCallTimeout(editTier.RequestTimeout)
	limits.MaxOutputTokens = editTier.MaxOutputTokens

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	sw := newSSEWriter(w, flusher)

	// ── Phase 1: Intent classification ──────────────────────────────────────
	sw.write("status", map[string]any{
		"step":              "intent",
		"label":             "Analyzing your request...",
		"timeout_ms":        limits.RequestTimeout.Milliseconds(),
		"prompt_timeout_ms": aiTimelinePromptTimeout.Milliseconds(),
		"strategy":          "single_llm_call",
	})

	intentSummaryJSON, summaryErr := buildAITimelineIntentSummaryJSON(normalizedCurrent)
	if summaryErr != nil {
		sw.write("error", map[string]any{"message": "Failed to summarize current_state"})
		return
	}

	// Heartbeat: keep the UI alive while the LLM runs intent classification.
	cancelIntentHB := startSSEHeartbeat(promptCtx, sw, "intent", []string{
		"Analyzing your request...",
		"Reading board context...",
		"Classifying intent...",
		"Checking conversation history...",
	}, 1200*time.Millisecond)

	intentCtx, cancelIntent := context.WithTimeout(promptCtx, limits.RequestTimeout)
	intentResult, intentErr := classifyAITimelineEditIntent(
		intentCtx,
		roomID,
		prompt,
		intentSummaryJSON,
		conversationHistory,
		limits,
	)
	cancelIntent()
	cancelIntentHB()

	if intentErr != nil {
		log.Printf("[ai_timeline/edit_stream] intent classification failed room_id=%q user_id=%q err=%v", roomID, userID, intentErr)
	} else if intentResult.Intent == aiIntentChat || intentResult.Intent == aiIntentClarify {
		assistantReply := strings.TrimSpace(intentResult.AssistantReply)
		if assistantReply == "" {
			if intentResult.Intent == aiIntentClarify {
				assistantReply = "I can make the change once you confirm one key detail."
			} else {
				assistantReply = "I can explain the current board and answer questions without editing it."
			}
		}
		// Stream chat reply as text deltas for a typing effect.
		streamReplyDeltas(sw, assistantReply)
		sw.write("chat", map[string]any{
			"intent":          intentResult.Intent,
			"assistant_reply": assistantReply,
		})
		return
	}

	// ── Phase 2: Plan generation ─────────────────────────────────────────────
	editSummaryJSON, editSummaryErr := buildAITimelineEditSummaryJSON(normalizedCurrent)
	if editSummaryErr != nil {
		sw.write("error", map[string]any{"message": "Failed to summarize current_state for edits"})
		return
	}
	editPlanTimeout := calculateAITimelineEditTimeout(editTier.RequestTimeout, prompt, editSummaryJSON)

	sw.write("status", map[string]any{
		"step":              "plan",
		"label":             "Planning board changes...",
		"timeout_ms":        editPlanTimeout.Milliseconds(),
		"prompt_timeout_ms": aiTimelinePromptTimeout.Milliseconds(),
		"strategy":          "single_llm_call",
	})

	// Heartbeat during plan generation (typically the longest LLM call).
	cancelPlanHB := startSSEHeartbeat(promptCtx, sw, "plan", []string{
		"Planning board changes...",
		"Structuring operations...",
		"Generating board diff...",
		"Reviewing constraints...",
		"Finalizing plan...",
	}, 1400*time.Millisecond)

	editLimits := limits
	editLimits.RequestTimeout = editPlanTimeout
	editCtx, cancelEdit := context.WithTimeout(promptCtx, editLimits.RequestTimeout)
	editOps, editErr := generateAITimelineEditOperations(
		editCtx,
		roomID,
		prompt,
		editSummaryJSON,
		conversationHistory,
		editLimits,
	)
	cancelEdit()
	cancelPlanHB()

	if editErr != nil {
		_, payload := buildAITimelineErrorPayload("plan", editErr, editPlanTimeout, aiTimelinePromptTimeout)
		sw.write("error", payload)
		return
	}
	if editOps.Mode == aiIntentChat || editOps.Mode == aiIntentClarify {
		assistantReply := strings.TrimSpace(editOps.AssistantReply)
		if assistantReply == "" {
			if editOps.Mode == aiIntentClarify {
				assistantReply = "I can apply the change once you confirm one key detail."
			} else {
				assistantReply = "I can discuss the board without changing it."
			}
		}
		streamReplyDeltas(sw, assistantReply)
		sw.write("chat", map[string]any{
			"intent":          editOps.Mode,
			"assistant_reply": assistantReply,
		})
		return
	}

	// ── Phase 3: Apply operations ─────────────────────────────────────────────
	workingState := applyAITimelineEditOperations(normalizedCurrent, aiTimelineEditOperationsResponse{
		ProjectPatch: editOps.ProjectPatch,
	})
	totalOperations := len(editOps.Operations)

	sw.write("plan", map[string]any{
		"assistant_reply": editOps.AssistantReply,
		"project_patch":   editOps.ProjectPatch,
		"operation_total": totalOperations,
	})

	if totalOperations == 0 && !hasAITimelineProjectPatch(editOps.ProjectPatch) {
		sw.write("error", map[string]any{"message": "AI returned no valid board changes"})
		return
	}

	sw.write("status", map[string]any{
		"step":            "apply",
		"label":           "Applying board changes...",
		"applied_count":   0,
		"operation_total": totalOperations,
	})

	appliedCount, persistErr := h.applyAIOperationsWithCallback(promptCtx, roomID, editOps.Operations, func(operation aiTimelineEditOperation, appliedCount int, operationTotal int) {
		workingState = applyAITimelineEditOperations(workingState, aiTimelineEditOperationsResponse{
			Operations: []aiTimelineEditOperation{operation},
		})
		sw.write("operation_applied", map[string]any{
			"operation":       operation,
			"applied_count":   appliedCount,
			"operation_total": operationTotal,
		})
		sw.write("status", map[string]any{
			"step":            "apply",
			"label":           "Applying board changes...",
			"applied_count":   appliedCount,
			"operation_total": operationTotal,
		})
	})

	if persistErr != nil {
		workingState.IsPartial = true
		if strings.TrimSpace(workingState.AssistantReply) == "" {
			workingState.AssistantReply = "I applied part of the board update before the request stopped."
		}
		sw.write("error", map[string]any{"message": "Failed to apply AI timeline operations"})
		sw.write("done", map[string]any{
			"intent":          aiIntentModifyProject,
			"assistant_reply": workingState.AssistantReply,
			"timeline":        workingState,
			"is_partial":      true,
			"applied_count":   appliedCount,
			"operation_total": totalOperations,
		})
		return
	}

	// ── Phase 4: Validate + stream response ───────────────────────────────────
	sw.write("status", map[string]any{
		"step":            "validate",
		"label":           "Verifying changes...",
		"applied_count":   appliedCount,
		"operation_total": totalOperations,
	})

	finalReply := strings.TrimSpace(workingState.AssistantReply)
	if finalReply == "" {
		finalReply = strings.TrimSpace(editOps.AssistantReply)
	}
	workingState.AssistantReply = finalReply

	// Stream the assistant reply as text deltas so the UI can show a typing effect.
	streamReplyDeltas(sw, finalReply)

	sw.write("done", map[string]any{
		"intent":          aiIntentModifyProject,
		"assistant_reply": finalReply,
		"timeline":        workingState,
		"is_partial":      false,
		"applied_count":   appliedCount,
		"operation_total": totalOperations,
	})
}

// streamReplyDeltas sends the assistant reply as a series of "text_delta" SSE
// events so the frontend can render a typing effect.  Words are sent in small
// batches with a short delay to look natural without adding perceptible latency.
func streamReplyDeltas(sw *sseWriter, reply string) {
	if reply == "" {
		return
	}
	// Split into words; send batches of ~3 words every 28 ms.
	// At typical prose density (~5 chars/word) this streams ~540 chars/s —
	// fast enough to feel responsive but slow enough to read.
	words := strings.Fields(reply)
	const batchSize = 3
	const delayMs = 28
	for i := 0; i < len(words); i += batchSize {
		end := i + batchSize
		if end > len(words) {
			end = len(words)
		}
		chunk := strings.Join(words[i:end], " ")
		// Re-add trailing space unless it's the last batch.
		if end < len(words) {
			chunk += " "
		}
		sw.write("text_delta", map[string]any{"delta": chunk})
		time.Sleep(delayMs * time.Millisecond)
	}
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
	conversationHistory []aiTimelineConversationEntry,
	limits aiOrganizeLimits,
	maxSprints int,
	onProgress aiTimelineBlueprintProgressFunc,
	tierHint string,
) (aiTimelineProject, error) {
	sourcePrompt := extractAITimelineUserInstruction(prompt)
	if strings.TrimSpace(sourcePrompt) == "" {
		sourcePrompt = strings.TrimSpace(prompt)
	}
	conversationJSON := buildAITimelineConversationContext(conversationHistory, 8, 500)
	if onProgress != nil {
		onProgress("blueprint_foundation", "Drafting project foundation...")
	}
	foundationCtx, cancelFoundation := context.WithTimeout(ctx, normalizeAITimelineCallTimeout(limits.RequestTimeout))
	foundation, foundationErr := generateAIProjectBlueprintFoundation(
		foundationCtx,
		roomID,
		sourcePrompt,
		conversationJSON,
		hasAssistantClarificationRequest(conversationHistory),
		limits,
		tierHint,
	)
	cancelFoundation()
	if foundationErr != nil {
		return aiTimelineProject{}, &aiTimelineStageError{
			Stage:   "blueprint_foundation",
			Timeout: normalizeAITimelineCallTimeout(limits.RequestTimeout),
			Err:     foundationErr,
		}
	}

	normalizedFoundation := normalizeAITimelineProject(aiTimelineProject{
		AssistantReply: foundation.AssistantReply,
		ProjectName:    foundation.ProjectName,
		TechStack:      foundation.TechStack,
		TargetAudience: foundation.TargetAudience,
		EstimatedCost:  foundation.EstimatedCost,
		RolesNeeded:    foundation.RolesNeeded,
	})
	if onProgress != nil {
		onProgress("blueprint_sprints", "Planning project phases...")
	}
	sprintPlanCtx, cancelSprintPlan := context.WithTimeout(ctx, normalizeAITimelineCallTimeout(limits.RequestTimeout))
	sprints, sprintErr := generateAIProjectBlueprintSprintPlan(
		sprintPlanCtx,
		roomID,
		sourcePrompt,
		conversationJSON,
		normalizedFoundation,
		hasAssistantClarificationRequest(conversationHistory),
		limits,
		maxSprints,
		tierHint,
	)
	cancelSprintPlan()
	if sprintErr != nil {
		return aiTimelineProject{}, &aiTimelineStageError{
			Stage:   "blueprint_sprints",
			Timeout: normalizeAITimelineCallTimeout(limits.RequestTimeout),
			Err:     sprintErr,
		}
	}

	return normalizeAITimelineProject(aiTimelineProject{
		AssistantReply: normalizedFoundation.AssistantReply,
		ProjectName:    normalizedFoundation.ProjectName,
		TechStack:      normalizedFoundation.TechStack,
		TargetAudience: normalizedFoundation.TargetAudience,
		EstimatedCost:  normalizedFoundation.EstimatedCost,
		RolesNeeded:    normalizedFoundation.RolesNeeded,
		Sprints:        sprints,
	}), nil
}

func generateAIProjectBlueprintFoundation(
	ctx context.Context,
	roomID string,
	sourcePrompt string,
	conversationJSON string,
	clarificationAsked bool,
	limits aiOrganizeLimits,
	tierHint string,
) (aiTimelineProject, error) {
	tierSection := ""
	if strings.TrimSpace(tierHint) != "" {
		tierSection = "\n\n" + strings.TrimSpace(tierHint)
	}
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nClarification already asked earlier: %t\nConversation history JSON:\n%s\n\nUser request:\n%s%s\n\nGenerate the project foundation now.",
		roomID,
		clarificationAsked,
		conversationJSON,
		strings.TrimSpace(sourcePrompt),
		tierSection,
	)
	raw, err := generateAIOrganizeStructuredJSONWithTier(
		ctx,
		aiBlueprintFoundationSystemPrompt,
		userPrompt,
		limits,
		ai.AIModelTierHeavy,
	)
	if err != nil {
		return aiTimelineProject{}, err
	}
	return parseAITimelineBlueprintFoundation(raw)
}

func generateAIProjectBlueprintSprintPlan(
	ctx context.Context,
	roomID string,
	sourcePrompt string,
	conversationJSON string,
	foundation aiTimelineProject,
	clarificationAsked bool,
	limits aiOrganizeLimits,
	maxSprints int,
	tierHint string,
) ([]aiTimelineSprint, error) {
	foundationJSONBytes, _ := json.Marshal(map[string]any{
		"project_name":    foundation.ProjectName,
		"tech_stack":      foundation.TechStack,
		"target_audience": foundation.TargetAudience,
		"estimated_cost":  foundation.EstimatedCost,
		"roles_needed":    foundation.RolesNeeded,
	})
	tierSection := ""
	if strings.TrimSpace(tierHint) != "" {
		tierSection = "\n\n" + strings.TrimSpace(tierHint)
	}
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nClarification already asked earlier: %t\nConversation history JSON:\n%s\n\nProject foundation JSON:\n%s\n\nUser request:\n%s%s\n\nGenerate the sprint plan now.",
		roomID,
		clarificationAsked,
		conversationJSON,
		string(foundationJSONBytes),
		strings.TrimSpace(sourcePrompt),
		tierSection,
	)
	raw, err := generateAIOrganizeStructuredJSONWithTier(
		ctx,
		buildAIBlueprintSprintPlanSystemPrompt(maxSprints),
		userPrompt,
		limits,
		ai.AIModelTierHeavy,
	)
	if err != nil {
		return nil, err
	}
	return parseAITimelineBlueprintSprintPlan(raw)
}

func extractAITimelineUserInstruction(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	for _, marker := range []string{"USER REQUEST:", "EDIT REQUEST:"} {
		index := strings.LastIndex(upper, marker)
		if index >= 0 {
			extracted := strings.TrimSpace(trimmed[index+len(marker):])
			if extracted != "" {
				return extracted
			}
		}
	}
	if endFormatIndex := strings.LastIndex(upper, "[END FORMAT]"); endFormatIndex >= 0 {
		extracted := strings.TrimSpace(trimmed[endFormatIndex+len("[END FORMAT]"):])
		if extracted != "" {
			return extracted
		}
	}
	return trimmed
}

func buildAITimelineConversationContext(
	history []aiTimelineConversationEntry,
	maxEntries int,
	maxChars int,
) string {
	if maxEntries <= 0 {
		maxEntries = 8
	}
	if maxChars <= 0 {
		maxChars = 500
	}
	if len(history) == 0 {
		return "[]"
	}
	start := len(history) - maxEntries
	if start < 0 {
		start = 0
	}
	trimmedHistory := make([]aiTimelineConversationEntry, 0, len(history)-start)
	for _, entry := range history[start:] {
		role := strings.TrimSpace(entry.Role)
		if role != "assistant" {
			role = "user"
		}
		text := truncateRunes(strings.TrimSpace(entry.Text), maxChars)
		if text == "" {
			continue
		}
		trimmedHistory = append(trimmedHistory, aiTimelineConversationEntry{
			Role:   role,
			Text:   text,
			Intent: normalizeAIIntent(entry.Intent),
		})
	}
	if len(trimmedHistory) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(trimmedHistory)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func parseAITimelineBlueprintFoundation(raw string) (aiTimelineProject, error) {
	candidates := extractJSONObjectsCandidates(raw)
	if len(candidates) == 0 {
		return aiTimelineProject{}, fmt.Errorf("ai blueprint foundation response did not contain JSON")
	}

	var lastErr error
	for _, content := range candidates {
		var direct aiTimelineProject
		if err := json.Unmarshal([]byte(content), &direct); err == nil && hasAITimelineFoundationFields(direct) {
			return direct, nil
		} else if err != nil {
			lastErr = err
		}

		var envelope struct {
			AssistantReply string          `json:"assistant_reply"`
			Timeline       json.RawMessage `json:"timeline"`
			Foundation     json.RawMessage `json:"foundation"`
			Project        json.RawMessage `json:"project"`
		}
		if err := json.Unmarshal([]byte(content), &envelope); err != nil {
			lastErr = err
			continue
		}
		nestedPayload := pickFirstNonEmptyJSONRaw(envelope.Foundation, envelope.Timeline, envelope.Project)
		if len(nestedPayload) == 0 {
			lastErr = fmt.Errorf("ai blueprint foundation response missing project foundation object")
			continue
		}
		var nested aiTimelineProject
		if err := json.Unmarshal(nestedPayload, &nested); err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(envelope.AssistantReply) != "" {
			nested.AssistantReply = strings.TrimSpace(envelope.AssistantReply)
		}
		if hasAITimelineFoundationFields(nested) {
			return nested, nil
		}
	}
	if lastErr != nil {
		return aiTimelineProject{}, lastErr
	}
	return aiTimelineProject{}, fmt.Errorf("ai blueprint foundation response was not parseable")
}

func parseAITimelineBlueprintSprintPlan(raw string) ([]aiTimelineSprint, error) {
	candidates := extractJSONObjectsCandidates(raw)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("ai sprint plan response did not contain JSON")
	}

	var lastErr error
	for _, content := range candidates {
		var direct struct {
			Sprints []aiTimelineSprint `json:"sprints"`
		}
		if err := json.Unmarshal([]byte(content), &direct); err == nil && len(direct.Sprints) > 0 {
			return direct.Sprints, nil
		} else if err != nil {
			lastErr = err
		}

		project, err := parseAITimelineProjectCandidate(content)
		if err == nil && len(project.Sprints) > 0 {
			return project.Sprints, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("ai sprint plan response returned no valid sprints")
}

func hasAITimelineFoundationFields(project aiTimelineProject) bool {
	return strings.TrimSpace(project.ProjectName) != "" ||
		len(project.TechStack) > 0 ||
		strings.TrimSpace(project.TargetAudience) != "" ||
		strings.TrimSpace(project.EstimatedCost) != "" ||
		len(project.RolesNeeded) > 0 ||
		strings.TrimSpace(project.AssistantReply) != ""
}

func buildAITimelineExecutionInitialContext(
	workspace *ai.WorkspaceContext,
	opts ai.BuildOptions,
	cfg models.ProjectTypeConfig,
) string {
	if workspace == nil {
		return ""
	}
	rendered := strings.TrimSpace(workspace.RenderForAI(opts))
	if rendered == "" {
		return ""
	}
	return fmt.Sprintf(
		"CURRENT WORKSPACE STATE — existing %s are pre-loaded. Do NOT call list_tasks() unless you need to avoid duplicates or continue a previous partial onboarding run:\n\n%s",
		strings.ToLower(strings.TrimSpace(cfg.TaskTermPlural)),
		rendered,
	)
}

func buildAITimelineExecutionPrompt(
	originalPrompt string,
	blueprintJSON string,
	cfg models.ProjectTypeConfig,
	maxTasksPerSprint int,
) string {
	groupPlural := strings.ToLower(strings.TrimSpace(cfg.GroupTermPlural))
	if groupPlural == "" {
		groupPlural = "groups"
	}
	groupSingular := strings.ToLower(strings.TrimSpace(cfg.GroupTerm))
	if groupSingular == "" {
		groupSingular = "group"
	}
	taskPlural := strings.ToLower(strings.TrimSpace(cfg.TaskTermPlural))
	if taskPlural == "" {
		taskPlural = "tasks"
	}
	return fmt.Sprintf(
		"Original user request:\n%s\n\nProject blueprint JSON:\n%s\n\nBuild this workspace now by using tools directly.\nCritical execution rules:\n- Use the exact %s from the blueprint.\n- Create at most %d %s per %s.\n- Prefer full project quality over a minimal token-saving outline.\n- If the board already contains partial work from an earlier run, continue from there without duplicating titles in the same %s.\n- End with verify_task_count and then give a concise summary of what you created.",
		strings.TrimSpace(originalPrompt),
		strings.TrimSpace(blueprintJSON),
		groupPlural,
		maxTasksPerSprint,
		taskPlural,
		groupSingular,
		groupSingular,
	)
}

func buildAITimelineExecutionRetryPrompt(
	originalPrompt string,
	blueprintJSON string,
	cfg models.ProjectTypeConfig,
	maxTasksPerSprint int,
) string {
	return buildAITimelineExecutionPrompt(originalPrompt, blueprintJSON, cfg, maxTasksPerSprint) +
		"\n\nYou have not created any tasks yet. Stop explaining and start calling create_task immediately."
}

func summarizeAITimelineAgentText(text string) string {
	trimmed := collapseWhitespace(text)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > 180 {
		trimmed = strings.TrimSpace(trimmed[:180]) + "..."
	}
	return trimmed
}

func collapseWhitespace(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func asFloatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func aiTimelineSprintNameKey(raw string) string {
	return strings.ToLower(collapseWhitespace(raw))
}

func aiTimelineTaskTitleKey(raw string) string {
	return strings.ToLower(collapseWhitespace(raw))
}

func findAITimelineSprintIndexByName(project *aiTimelineProject, sprintName string) int {
	if project == nil {
		return -1
	}
	targetKey := aiTimelineSprintNameKey(sprintName)
	if targetKey == "" {
		return -1
	}
	for index, sprint := range project.Sprints {
		if aiTimelineSprintNameKey(sprint.Name) == targetKey {
			return index
		}
	}
	return -1
}

func upsertAITimelineAgentTask(
	project *aiTimelineProject,
	sprintName string,
	startDate string,
	endDate string,
	task aiTimelineTask,
) int {
	if project == nil {
		return -1
	}
	sprintIndex := findAITimelineSprintIndexByName(project, sprintName)
	if sprintIndex < 0 {
		sprintIndex = len(project.Sprints)
		project.Sprints = append(project.Sprints, aiTimelineSprint{
			ID:             fmt.Sprintf("sprint-%d", sprintIndex+1),
			Name:           truncateRunes(strings.TrimSpace(firstNonEmpty(sprintName, fmt.Sprintf("Sprint %d", sprintIndex+1))), 160),
			StartDate:      normalizeAITimelineAgentDate(startDate),
			EndDate:        normalizeAITimelineAgentDate(endDate),
			DurationDays:   7,
			TasksGenerated: false,
			Tasks:          nil,
		})
	}

	sprint := &project.Sprints[sprintIndex]
	if sprint.StartDate == "" {
		sprint.StartDate = normalizeAITimelineAgentDate(startDate)
	}
	if sprint.EndDate == "" {
		sprint.EndDate = normalizeAITimelineAgentDate(endDate)
	}
	if sprint.DurationDays <= 0 {
		sprint.DurationDays = 7
	}
	for index, existing := range sprint.Tasks {
		if normalizeAITimelineTaskIdentifier(firstNonEmpty(existing.TaskID, existing.ID)) ==
			normalizeAITimelineTaskIdentifier(firstNonEmpty(task.TaskID, task.ID)) {
			sprint.Tasks[index] = task
			sprint.TasksGenerated = true
			return sprintIndex
		}
	}
	sprint.Tasks = append(sprint.Tasks, task)
	sprint.TasksGenerated = true
	return sprintIndex
}

func collectAITimelineMissingSprints(project aiTimelineProject) []string {
	missing := make([]string, 0, len(project.Sprints))
	for index, sprint := range project.Sprints {
		if len(sprint.Tasks) > 0 {
			continue
		}
		name := strings.TrimSpace(sprint.Name)
		if name == "" {
			name = fmt.Sprintf("Sprint %d", index+1)
		}
		missing = append(missing, name)
	}
	return missing
}

func normalizeAITimelineAgentDate(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed.UTC().Format("2006-01-02")
		}
	}
	return ""
}

func normalizeAITimelineAgentDuration(start *time.Time, end *time.Time) (string, float64) {
	if start == nil || end == nil {
		return "days", 1
	}
	startUTC := start.UTC()
	endUTC := end.UTC()
	if endUTC.Before(startUTC) {
		return "days", 1
	}
	durationDays := int(endUTC.Sub(startUTC).Hours()/24) + 1
	if durationDays < 1 {
		durationDays = 1
	}
	return "days", float64(durationDays)
}

func convertAITimelineAgentTask(result any, input map[string]any) (aiTimelineTask, string, string, string, bool) {
	taskResult, ok := result.(ai.TaskCtx)
	if !ok {
		return aiTimelineTask{}, "", "", "", false
	}

	description := truncateRunes(strings.TrimSpace(taskResult.Description), 4000)
	taskType := truncateRunes(strings.ToLower(strings.TrimSpace(taskResult.TaskType)), 48)
	if taskType == "" {
		taskType = truncateRunes(strings.ToLower(strings.TrimSpace(extractTaskMetadataValue(description, "type"))), 48)
	}
	if taskType == "" {
		taskType = "general"
	}

	budget := 0.0
	if taskResult.Budget != nil {
		budget = normalizeTimelineBudgetValue(*taskResult.Budget)
	} else if extractedBudget := extractTaskBudget(description); extractedBudget != nil {
		budget = normalizeTimelineBudgetValue(*extractedBudget)
	}

	actualCost := 0.0
	if taskResult.ActualCost != nil {
		actualCost = normalizeTimelineBudgetValue(*taskResult.ActualCost)
	} else if extractedCost := extractTaskActualCost(description); extractedCost != nil {
		actualCost = normalizeTimelineBudgetValue(*extractedCost)
	}

	durationUnit, durationValue := normalizeAITimelineAgentDuration(taskResult.StartDate, taskResult.DueDate)
	if extractedUnit, extractedValue, ok := extractTaskDurationMetadata(description); ok {
		durationUnit = extractedUnit
		durationValue = extractedValue
	}
	effortScore := estimateEffortScoreFromDuration(durationUnit, durationValue)
	if extractedEffort, ok := extractTaskEffortMetadata(description); ok {
		effortScore = extractedEffort
	}

	assigneeID := normalizeTimelineAssigneeID(taskResult.AssigneeID)
	assigneeDisplay := strings.TrimSpace(firstNonEmpty(taskResult.AssigneeName, assigneeID))
	sprintName := truncateRunes(strings.TrimSpace(firstNonEmpty(taskResult.SprintName, asStringValue(input["sprint_name"]))), 160)
	startDate := ""
	if taskResult.StartDate != nil {
		startDate = taskResult.StartDate.UTC().Format("2006-01-02")
	}
	endDate := ""
	if taskResult.DueDate != nil {
		endDate = taskResult.DueDate.UTC().Format("2006-01-02")
	}
	if startDate == "" {
		startDate = normalizeAITimelineAgentDate(asStringValue(input["start_date"]))
	}
	if endDate == "" {
		endDate = normalizeAITimelineAgentDate(asStringValue(input["due_date"]))
	}

	return aiTimelineTask{
		TaskID:        normalizeAITimelineTaskIdentifier(taskResult.ID),
		ID:            normalizeAITimelineTaskIdentifier(taskResult.ID),
		Title:         truncateRunes(strings.TrimSpace(taskResult.Title), 240),
		Status:        normalizeTaskStatusValue(taskResult.Status),
		Type:          taskType,
		AssigneeID:    assigneeID,
		Assignee:      assigneeDisplay,
		Budget:        budget,
		ActualCost:    actualCost,
		DurationUnit:  durationUnit,
		DurationValue: durationValue,
		EffortScore:   effortScore,
		Description:   description,
	}, sprintName, startDate, endDate, true
}

func (h *RoomHandler) executeAITimelineBlueprintWithAgent(
	ctx context.Context,
	roomID string,
	userID string,
	blueprintJSON string,
	originalPrompt string,
	tier config.AITimelineTierLimits,
	limits aiOrganizeLimits,
	project *aiTimelineProject,
	onStatus func(step string, label string, sprintIndex int),
	onSprintUpdate func(sprintIndex int),
) (int, string, bool, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return 0, "", false, fmt.Errorf("task storage unavailable")
	}
	ctxBuilder := ai.NewContextBuilder(h.scylla)
	if ctxBuilder == nil {
		return 0, "", false, fmt.Errorf("context builder unavailable")
	}

	buildOpts := ai.BuildOptions{
		IncludeCanvas: false,
		IncludeChat:   false,
		TaskLimit:     500,
	}
	workspace, workspaceErr := ctxBuilder.Build(ctx, roomID, userID, buildOpts)
	if workspaceErr != nil {
		return 0, "", false, workspaceErr
	}
	projectTypeCfg := models.GetProjectTypeConfig(workspace.ProjectType)

	provider := resolveAITimelineAgentProvider(ai.AIModelTierHeavy)
	if provider == nil {
		return 0, "", false, fmt.Errorf("timeline onboarding ai provider is unavailable")
	}

	engine := ai.NewAgentEngine(
		provider,
		ctxBuilder,
		roomID,
		ai.AgentAuthContext{
			UserID:   strings.TrimSpace(userID),
			UserName: "User",
		},
	)
	if engine == nil {
		return 0, "", false, fmt.Errorf("timeline onboarding ai engine is unavailable")
	}

	sprintTaskCounts := make(map[string]int, len(project.Sprints))
	existingTitlesBySprint := make(map[string]map[string]struct{}, len(project.Sprints))
	existingSprintUpdates := make(map[int]struct{}, len(project.Sprints))
	for _, task := range workspace.Tasks {
		sprintKey := aiTimelineSprintNameKey(task.SprintName)
		if sprintKey == "" {
			continue
		}
		sprintTaskCounts[sprintKey]++
		if existingTitlesBySprint[sprintKey] == nil {
			existingTitlesBySprint[sprintKey] = make(map[string]struct{})
		}
		titleKey := aiTimelineTaskTitleKey(task.Title)
		if titleKey != "" {
			existingTitlesBySprint[sprintKey][titleKey] = struct{}{}
		}
		timelineTask, sprintName, startDate, endDate, ok := convertAITimelineAgentTask(task, nil)
		if ok {
			sprintIndex := upsertAITimelineAgentTask(project, sprintName, startDate, endDate, timelineTask)
			if sprintIndex >= 0 {
				existingSprintUpdates[sprintIndex] = struct{}{}
			}
		}
	}
	if onSprintUpdate != nil {
		for sprintIndex := range project.Sprints {
			if _, exists := existingSprintUpdates[sprintIndex]; !exists {
				continue
			}
			onSprintUpdate(sprintIndex)
		}
	}

	createdTaskCount := 0
	engine.SetToolExecutor(func(callCtx context.Context, name string, input map[string]any) (any, error) {
		toolName := strings.TrimSpace(name)
		switch toolName {
		case "list_tasks":
			if onStatus != nil {
				onStatus("review", "Reviewing current board state...", -1)
			}
		case "verify_task_count":
			if onStatus != nil {
				onStatus("verify", "Verifying generated workspace...", -1)
			}
		case "create_task":
			sprintName := truncateRunes(strings.TrimSpace(asStringValue(input["sprint_name"])), 160)
			title := truncateRunes(strings.TrimSpace(asStringValue(input["title"])), 240)
			sprintIndex := findAITimelineSprintIndexByName(project, sprintName)
			if onStatus != nil {
				label := "Creating task..."
				if title != "" {
					label = fmt.Sprintf("Creating %s...", title)
				}
				onStatus("apply", label, sprintIndex)
			}

			sprintKey := aiTimelineSprintNameKey(sprintName)
			if sprintKey != "" && tier.MaxTasksPerSprint > 0 && sprintTaskCounts[sprintKey] >= tier.MaxTasksPerSprint {
				return nil, fmt.Errorf("group %q already has the maximum %d tasks for this plan", sprintName, tier.MaxTasksPerSprint)
			}
			titleKey := aiTimelineTaskTitleKey(title)
			if sprintKey != "" && titleKey != "" {
				if _, exists := existingTitlesBySprint[sprintKey][titleKey]; exists {
					return nil, fmt.Errorf("a task titled %q already exists in %q", title, sprintName)
				}
			}
		}

		result, err := engine.ExecuteBuiltInTool(callCtx, toolName, input)
		if err != nil {
			return nil, err
		}

		switch toolName {
		case "create_task":
			task, sprintName, startDate, endDate, ok := convertAITimelineAgentTask(result, input)
			if !ok {
				return result, nil
			}
			sprintIndex := upsertAITimelineAgentTask(project, sprintName, startDate, endDate, task)
			createdTaskCount++

			sprintKey := aiTimelineSprintNameKey(sprintName)
			if sprintKey != "" {
				sprintTaskCounts[sprintKey]++
				if existingTitlesBySprint[sprintKey] == nil {
					existingTitlesBySprint[sprintKey] = make(map[string]struct{})
				}
				titleKey := aiTimelineTaskTitleKey(task.Title)
				if titleKey != "" {
					existingTitlesBySprint[sprintKey][titleKey] = struct{}{}
				}
			}

			if onSprintUpdate != nil {
				onSprintUpdate(sprintIndex)
			}
			if onStatus != nil {
				onStatus("apply", fmt.Sprintf("Created %d task%s so far...", createdTaskCount, ternaryPlural(createdTaskCount)), sprintIndex)
			}
		case "verify_task_count":
			if onStatus == nil {
				break
			}
			counts, ok := result.(map[string]any)
			if !ok {
				onStatus("verify", "Verification completed.", -1)
				break
			}
			totalTasks := int(asFloatValue(counts["total_tasks"]))
			sprintCount := int(asFloatValue(counts["sprint_count"]))
			if totalTasks > 0 && sprintCount > 0 {
				onStatus("verify", fmt.Sprintf("Verified %d tasks across %d groups.", totalTasks, sprintCount), -1)
			} else {
				onStatus("verify", "Verification completed.", -1)
			}
		}

		return result, nil
	})

	allowedTools := []string{"create_task", "list_tasks", "verify_task_count"}
	runConfig := ai.AgentConfig{
		MaxTurns:       40,
		Timeout:        calculateAITimelineExecutionTimeout(limits.RequestTimeout, len(project.Sprints)),
		SystemPrompt:   aiTimelineExecutionSystemPrompt,
		ContextOptions: buildOpts,
		Workspace:      workspace,
		InitialContext: buildAITimelineExecutionInitialContext(workspace, buildOpts, projectTypeCfg),
		AllowedTools:   allowedTools,
		WorkflowKind:   "timeline_onboarding",
		StreamCallback: func(event ai.AgentEvent) {
			if onStatus == nil {
				return
			}
			switch strings.TrimSpace(event.Kind) {
			case "thinking", "text":
				label := summarizeAITimelineAgentText(event.Text)
				if label != "" {
					onStatus("reasoning", label, -1)
				}
			}
		},
	}

	finalText, events, runErr := engine.Run(
		ctx,
		buildAITimelineExecutionPrompt(originalPrompt, blueprintJSON, projectTypeCfg, tier.MaxTasksPerSprint),
		runConfig,
	)
	if runErr != nil {
		return createdTaskCount, strings.TrimSpace(finalText), isAITimelineAgentTimedOut(ctx, finalText, events), runErr
	}

	if createdTaskCount == 0 && ctx.Err() == nil && len(collectAITimelineMissingSprints(*project)) > 0 {
		if onStatus != nil {
			onStatus("retry", "Retrying with direct task execution...", -1)
		}
		retryWorkspace, retryWorkspaceErr := ctxBuilder.Build(ctx, roomID, userID, buildOpts)
		if retryWorkspaceErr == nil && retryWorkspace != nil {
			runConfig.Workspace = retryWorkspace
			runConfig.InitialContext = buildAITimelineExecutionInitialContext(retryWorkspace, buildOpts, projectTypeCfg)
		}
		retryText, retryEvents, retryErr := engine.Run(
			ctx,
			buildAITimelineExecutionRetryPrompt(originalPrompt, blueprintJSON, projectTypeCfg, tier.MaxTasksPerSprint),
			runConfig,
		)
		if retryErr == nil && strings.TrimSpace(retryText) != "" {
			finalText = retryText
		}
		events = append(events, retryEvents...)
		if retryErr != nil {
			return createdTaskCount, strings.TrimSpace(finalText), isAITimelineAgentTimedOut(ctx, finalText, events), retryErr
		}
	}

	return createdTaskCount, strings.TrimSpace(finalText), isAITimelineAgentTimedOut(ctx, finalText, events), nil
}

func ternaryPlural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func isAITimelineAgentTimedOut(ctx context.Context, finalText string, events []ai.AgentEvent) bool {
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	loweredText := strings.ToLower(strings.TrimSpace(finalText))
	if strings.Contains(loweredText, "ran out of time") {
		return true
	}
	for _, event := range events {
		if strings.TrimSpace(event.Kind) != "done" {
			continue
		}
		loweredError := strings.ToLower(strings.TrimSpace(event.Error))
		if strings.Contains(loweredError, "deadline") ||
			strings.Contains(loweredError, "timed out") {
			return true
		}
	}
	return false
}

func generateAITimelineEditOperations(
	ctx context.Context,
	roomID string,
	prompt string,
	currentSummaryJSON string,
	conversationHistory []aiTimelineConversationEntry,
	limits aiOrganizeLimits,
) (aiTimelineEditOperationsResponse, error) {
	conversationJSON, _ := json.Marshal(conversationHistory)
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nClarification already asked earlier: %t\nConversation history JSON:\n%s\n\nCurrent project summary JSON:\n%s\n\nUser edit request:\n%s\n\nReturn ONLY the edit response JSON.",
		roomID,
		hasAssistantClarificationRequest(conversationHistory),
		string(conversationJSON),
		strings.TrimSpace(currentSummaryJSON),
		strings.TrimSpace(prompt),
	)
	raw, err := generateAIOrganizeStructuredJSONWithTier(
		ctx,
		aiTimelineEditSystemPrompt,
		userPrompt,
		limits,
		ai.AIModelTierHeavy,
	)
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
	mode := normalizeAIIntent(
		decodeJSONString(
			pickFirstNonEmptyJSONRaw(
				envelope["mode"],
				envelope["intent"],
			),
		),
	)
	if mode != aiIntentChat && mode != aiIntentClarify {
		mode = aiIntentModifyProject
	}
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
	if len(operations) == 0 && !hasAITimelineProjectPatch(patch) && mode == aiIntentModifyProject {
		return aiTimelineEditOperationsResponse{}, fmt.Errorf("ai edit operations response returned no valid edits")
	}

	return aiTimelineEditOperationsResponse{
		Mode:           mode,
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

		assigneeID := normalizeTimelineAssigneeID(firstNonEmpty(operation.AssigneeID, operation.Assignee))
		if assigneeID == "" {
			assigneeID = normalizeTimelineAssigneeID(
				asStringValue(readChangeValue(changes, "assignee_id", "assigneeId", "assignee", "owner_id", "ownerId", "owner")),
			)
		}
		if assigneeID == "" && nestedTask != nil {
			assigneeID = normalizeTimelineAssigneeID(firstNonEmpty(nestedTask.AssigneeID, nestedTask.Assignee))
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
				assigneeID == "" &&
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
				AssigneeID:    assigneeID,
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
				AssigneeID:    assigneeID,
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

var looseTimelineNumericPattern = regexp.MustCompile(`[+-]?(?:\d+(?:\.\d+)?|\.\d+)`)

func parseLooseTimelineFloat(raw string) (float64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	normalized := strings.ReplaceAll(trimmed, ",", "")
	if parsed, err := strconv.ParseFloat(normalized, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
		return parsed, true
	}
	token := looseTimelineNumericPattern.FindString(normalized)
	if token == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(token, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}

func normalizeTimelineAssigneeID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return truncateRunes(trimmed, 128)
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
		parsed, ok := parseLooseTimelineFloat(typed)
		if !ok {
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

func classifyAITimelineGenerateIntent(
	ctx context.Context,
	roomID string,
	prompt string,
	conversationHistory []aiTimelineConversationEntry,
	limits aiOrganizeLimits,
) (AIIntentResponse, error) {
	sourcePrompt := extractAITimelineUserInstruction(prompt)
	if strings.TrimSpace(sourcePrompt) == "" {
		sourcePrompt = strings.TrimSpace(prompt)
	}
	conversationJSON := buildAITimelineConversationContext(conversationHistory, 6, 400)
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nClarification already asked earlier: %t\nConversation history JSON:\n%s\n\nUser request:\n%s\n\nReturn only the intent classification JSON.",
		roomID,
		hasAssistantClarificationRequest(conversationHistory),
		conversationJSON,
		sourcePrompt,
	)
	raw, err := generateAIOrganizeStructuredJSONWithTier(
		ctx,
		aiTimelineGenerateIntentSystemPrompt,
		userPrompt,
		limits,
		ai.AIModelTierLight,
	)
	if err != nil {
		return AIIntentResponse{}, err
	}
	return parseAIIntentResponse(raw)
}

func classifyAITimelineEditIntent(
	ctx context.Context,
	roomID string,
	prompt string,
	projectSummaryJSON string,
	conversationHistory []aiTimelineConversationEntry,
	limits aiOrganizeLimits,
) (AIIntentResponse, error) {
	sourcePrompt := extractAITimelineUserInstruction(prompt)
	if strings.TrimSpace(sourcePrompt) == "" {
		sourcePrompt = strings.TrimSpace(prompt)
	}
	conversationJSON := buildAITimelineConversationContext(conversationHistory, 6, 400)
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nClarification already asked earlier: %t\nConversation history JSON:\n%s\n\nCurrent project summary JSON:\n%s\n\nUser request:\n%s\n\nReturn only the intent classification JSON.",
		roomID,
		hasAssistantClarificationRequest(conversationHistory),
		conversationJSON,
		strings.TrimSpace(projectSummaryJSON),
		sourcePrompt,
	)
	raw, err := generateAIOrganizeStructuredJSONWithTier(
		ctx,
		aiTimelineIntentSystemPrompt,
		userPrompt,
		limits,
		ai.AIModelTierLight,
	)
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
		return aiIntentChat
	case "modify_project", "modify":
		return aiIntentModifyProject
	case "generate_project", "generate", "create_project", "create":
		return aiIntentGenerateProject
	case "clarify", "clarification":
		return aiIntentClarify
	default:
		return ""
	}
}

func normalizeAITimelineConversationHistory(
	rawEntries []aiTimelineConversationEntry,
) []aiTimelineConversationEntry {
	if len(rawEntries) == 0 {
		return nil
	}
	const maxEntries = 40
	trimStart := 0
	if len(rawEntries) > maxEntries {
		trimStart = len(rawEntries) - maxEntries
	}

	normalized := make([]aiTimelineConversationEntry, 0, len(rawEntries)-trimStart)
	for _, entry := range rawEntries[trimStart:] {
		role := strings.ToLower(strings.TrimSpace(entry.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		text := truncateRunes(strings.TrimSpace(entry.Text), 1800)
		if text == "" {
			continue
		}
		intent := normalizeAIIntent(entry.Intent)
		normalized = append(normalized, aiTimelineConversationEntry{
			Role:   role,
			Text:   text,
			Intent: intent,
		})
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func hasAssistantClarificationRequest(history []aiTimelineConversationEntry) bool {
	for _, entry := range history {
		if entry.Role != "assistant" {
			continue
		}
		if normalizeAIIntent(entry.Intent) == aiIntentClarify {
			return true
		}
		text := strings.ToLower(strings.TrimSpace(entry.Text))
		if text == "" || !strings.Contains(text, "?") {
			continue
		}
		if strings.Contains(text, "can you clarify") ||
			strings.Contains(text, "could you clarify") ||
			strings.Contains(text, "need one detail") ||
			strings.Contains(text, "before i") ||
			strings.Contains(text, "to proceed") {
			return true
		}
	}
	return false
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
				AssigneeID:    normalizeTimelineAssigneeID(firstNonEmpty(task.AssigneeID, task.Assignee)),
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
			if operation.AssigneeID != "" {
				targetTask.AssigneeID = normalizeTimelineAssigneeID(operation.AssigneeID)
				targetTask.Assignee = targetTask.AssigneeID
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
					AssigneeID:    normalizeTimelineAssigneeID(operation.AssigneeID),
					Assignee:      normalizeTimelineAssigneeID(operation.AssigneeID),
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
	maxTasksPerSprint int,
	tierHint string,
) ([]aiTimelineTask, error) {
	normalizedSprintName := strings.TrimSpace(sprintName)
	if normalizedSprintName == "" {
		return nil, fmt.Errorf("sprint name is required")
	}

	tierSection := ""
	if strings.TrimSpace(tierHint) != "" {
		tierSection = "\n\n" + strings.TrimSpace(tierHint)
	}
	userPrompt := fmt.Sprintf(
		"Project blueprint JSON:\n%s\n\nSprint name: %s\nGenerate tasks only for this sprint.%s",
		strings.TrimSpace(blueprintJSON),
		normalizedSprintName,
		tierSection,
	)
	raw, err := generateAIOrganizeStructuredJSON(ctx, buildAITaskFillSystemPrompt(maxTasksPerSprint), userPrompt, limits)
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
		sanitizedContent, sanitizeErr := sanitizeAISprintTasksPayload([]byte(content))
		if sanitizeErr != nil {
			lastErr = sanitizeErr
			continue
		}
		var parsed struct {
			Tasks []aiTimelineTask `json:"tasks"`
		}
		if err := json.Unmarshal(sanitizedContent, &parsed); err != nil {
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

func sanitizeAISprintTasksPayload(raw []byte) ([]byte, error) {
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	rawTasks, ok := readTimelineMapValue(envelope, "tasks").([]any)
	if !ok {
		return json.Marshal(envelope)
	}
	for index := range rawTasks {
		task, ok := rawTasks[index].(map[string]any)
		if !ok {
			continue
		}
		sanitizeAITimelineTaskResourceFields(task)
	}
	envelope["tasks"] = rawTasks
	return json.Marshal(envelope)
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
	direct, directErr := parseAITimelineProjectObjectJSON([]byte(content))
	if directErr == nil && len(direct.Sprints) > 0 {
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

	nested, nestedErr := parseAITimelineProjectObjectJSON(nestedPayload)
	if nestedErr != nil {
		if directErr != nil {
			return aiTimelineProject{}, fmt.Errorf("%v; %v", directErr, nestedErr)
		}
		return aiTimelineProject{}, nestedErr
	}
	if strings.TrimSpace(envelope.AssistantReply) != "" {
		nested.AssistantReply = strings.TrimSpace(envelope.AssistantReply)
	}
	return nested, nil
}

func parseAITimelineProjectObjectJSON(raw []byte) (aiTimelineProject, error) {
	sanitizedPayload, err := sanitizeAITimelineProjectResourceFields(raw)
	if err != nil {
		return aiTimelineProject{}, err
	}
	var parsed aiTimelineProject
	if err := json.Unmarshal(sanitizedPayload, &parsed); err != nil {
		return aiTimelineProject{}, err
	}
	return parsed, nil
}

func sanitizeAITimelineProjectResourceFields(raw []byte) ([]byte, error) {
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	sanitizeAITimelineProjectSprints(envelope)
	return json.Marshal(envelope)
}

func sanitizeAITimelineProjectSprints(project map[string]any) {
	rawSprints, ok := readTimelineMapValue(project, "sprints").([]any)
	if !ok || len(rawSprints) == 0 {
		return
	}
	for index := range rawSprints {
		sprint, ok := rawSprints[index].(map[string]any)
		if !ok {
			continue
		}
		rawTasks, ok := readTimelineMapValue(sprint, "tasks").([]any)
		if !ok || len(rawTasks) == 0 {
			continue
		}
		for taskIndex := range rawTasks {
			task, ok := rawTasks[taskIndex].(map[string]any)
			if !ok {
				continue
			}
			sanitizeAITimelineTaskResourceFields(task)
		}
		sprint["tasks"] = rawTasks
	}
	project["sprints"] = rawSprints
}

func sanitizeAITimelineTaskResourceFields(task map[string]any) {
	budgetValue := 0.0
	if parsedBudget := asFloatPointer(readTimelineMapValue(task, "budget")); parsedBudget != nil {
		budgetValue = *parsedBudget
	}
	task["budget"] = normalizeTimelineBudgetValue(budgetValue)

	actualCostValue := 0.0
	if parsedActualCost := asFloatPointer(
		readTimelineMapValue(task, "actual_cost", "actualCost", "spent", "spent_cost", "spentCost", "cost"),
	); parsedActualCost != nil {
		actualCostValue = *parsedActualCost
	}
	normalizedActualCost := normalizeTimelineBudgetValue(actualCostValue)
	task["actual_cost"] = normalizedActualCost
	task["spent"] = normalizedActualCost

	assigneeID := normalizeTimelineAssigneeID(
		asStringValue(readTimelineMapValue(task, "assignee_id", "assigneeId", "assignee", "owner_id", "ownerId", "owner")),
	)
	if assigneeID == "" {
		delete(task, "assignee_id")
		delete(task, "assigneeId")
		delete(task, "assignee")
		return
	}
	task["assignee_id"] = assigneeID
	task["assignee"] = assigneeID
}

func readTimelineMapValue(record map[string]any, keys ...string) any {
	if len(record) == 0 {
		return nil
	}
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value
		}
	}
	for existingKey, value := range record {
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
		assigneeID := normalizeTimelineAssigneeID(firstNonEmpty(task.AssigneeID, task.Assignee))
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
			AssigneeID:    assigneeID,
			Assignee:      assigneeID,
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

func resolveAITimelineAssigneeUUID(raw string) *gocql.UUID {
	normalized := normalizeTimelineAssigneeID(raw)
	if normalized == "" {
		return nil
	}
	candidates := []string{normalized}
	if strings.Contains(normalized, "_") {
		candidates = append(candidates, strings.ReplaceAll(normalized, "_", "-"))
	}
	for _, candidate := range candidates {
		parsed, err := parseFlexibleTaskUUID(candidate)
		if err != nil {
			continue
		}
		copy := parsed
		return &copy
	}
	return nil
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
	return h.applyAIOperationsWithCallback(ctx, roomID, operations, nil)
}

func (h *RoomHandler) applyAIOperationsWithCallback(
	ctx context.Context,
	roomID string,
	operations []aiTimelineEditOperation,
	onApplied func(operation aiTimelineEditOperation, appliedCount int, operationTotal int),
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
	operationTotal := len(operations)

	for index := range operations {
		operation := &operations[index]
		applied := false
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
			if operation.AssigneeID != "" {
				assigneeUUID := resolveAITimelineAssigneeUUID(operation.AssigneeID)
				if assigneeUUID != nil {
					setClauses = append(setClauses, "assignee_id = ?")
					args = append(args, assigneeUUID)
				}
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
			applied = true
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
					AssigneeID:    normalizeTimelineAssigneeID(operation.AssigneeID),
					Assignee:      normalizeTimelineAssigneeID(operation.AssigneeID),
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
			taskAssigneeID := resolveAITimelineAssigneeUUID(operation.AssigneeID)
			if err := h.scylla.Session.Query(
				insertQuery,
				roomUUID,
				newTaskID,
				title,
				taskDescription,
				status,
				sprintName,
				taskAssigneeID,
				nullableTrimmedText("tora_ai"),
				nullableTrimmedText("Tora AI"),
				now,
				now,
				now,
			).WithContext(ctx).Exec(); err != nil {
				return changedRows, err
			}
			changedRows++
			applied = true
		case "delete_task":
			taskUUID, ok := parseAITimelineTaskUUID(operation.TaskID)
			if !ok {
				continue
			}
			if err := h.scylla.Session.Query(deleteQuery, roomUUID, taskUUID).WithContext(ctx).Exec(); err != nil {
				return changedRows, err
			}
			changedRows++
			applied = true
		}
		if applied && onApplied != nil {
			onApplied(*operation, changedRows, operationTotal)
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
			taskAssigneeID := resolveAITimelineAssigneeUUID(firstNonEmpty(task.AssigneeID, task.Assignee))
			if taskAssigneeID == nil {
				taskAssigneeID = assigneeID
			}

			if err := h.scylla.Session.Query(
				query,
				roomUUID,
				taskID,
				title,
				description,
				status,
				sprintName,
				taskAssigneeID,
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
			task.AssigneeID = normalizeTimelineAssigneeID(firstNonEmpty(task.AssigneeID, task.Assignee))
			task.Assignee = task.AssigneeID
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

func writeAITimelineErrorPayload(w http.ResponseWriter, status int, payload aiTimelineErrorPayload) {
	if w == nil {
		return
	}
	if strings.TrimSpace(payload.Error) == "" {
		payload.Error = "AI timeline request failed"
	}
	if strings.TrimSpace(payload.Message) == "" {
		payload.Message = payload.Error
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func buildAITimelineErrorPayload(
	stage string,
	err error,
	timeout time.Duration,
	promptTimeout time.Duration,
) (int, aiTimelineErrorPayload) {
	var stageErr *aiTimelineStageError
	if errors.As(err, &stageErr) && stageErr != nil {
		if strings.TrimSpace(stageErr.Stage) != "" {
			stage = stageErr.Stage
		}
		if stageErr.Timeout > 0 {
			timeout = stageErr.Timeout
		}
		if stageErr.Err != nil {
			err = stageErr.Err
		}
	}
	stageLabel := humanizeAITimelineStage(stage)
	payload := aiTimelineErrorPayload{
		Stage:           strings.TrimSpace(stage),
		TimeoutMs:       timeout.Milliseconds(),
		PromptTimeoutMs: promptTimeout.Milliseconds(),
	}
	detail := sanitizeAITimelineErrorDetail(err)

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		payload.Code = "deadline_exceeded"
		payload.Error = fmt.Sprintf("%s exceeded its time budget.", stageLabel)
		payload.Message = payload.Error
		payload.Detail = firstNonEmpty(
			detail,
			fmt.Sprintf("The %s step hit a context deadline. Per-call budget=%s, overall prompt budget=%s.", stageLabel, timeout, promptTimeout),
		)
		payload.Retryable = true
		return http.StatusGatewayTimeout, payload
	case errors.Is(err, context.Canceled):
		payload.Code = "request_canceled"
		payload.Error = fmt.Sprintf("%s was canceled before completion.", stageLabel)
		payload.Message = payload.Error
		payload.Detail = firstNonEmpty(
			detail,
			fmt.Sprintf("The %s step ended with context cancellation before finishing.", stageLabel),
		)
		payload.Retryable = true
		return aiHTTPStatusClientClosed, payload
	}

	var exhaustedErr *ai.ProvidersExhaustedError
	if errors.As(err, &exhaustedErr) && exhaustedErr != nil && exhaustedErr.LastErr != nil {
		return buildAITimelineErrorPayload(stage, exhaustedErr.LastErr, timeout, promptTimeout)
	}

	var statusErr *ai.HTTPStatusError
	if errors.As(err, &statusErr) && statusErr != nil {
		payload.ProviderStatus = statusErr.StatusCode()
		payload.Detail = firstNonEmpty(detail, statusErr.Error())
		switch statusErr.StatusCode() {
		case http.StatusTooManyRequests:
			payload.Code = "provider_rate_limited"
			payload.Error = fmt.Sprintf("An AI provider rate-limited the %s step.", stageLabel)
			payload.Message = payload.Error
			payload.Retryable = true
			return http.StatusTooManyRequests, payload
		case http.StatusUnauthorized, http.StatusForbidden:
			payload.Code = "provider_auth_failed"
			payload.Error = fmt.Sprintf("An AI provider rejected the %s step.", stageLabel)
			payload.Message = payload.Error
			payload.Retryable = false
			return http.StatusServiceUnavailable, payload
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			payload.Code = "provider_rejected_request"
			payload.Error = fmt.Sprintf("The %s step was rejected by the AI provider.", stageLabel)
			payload.Message = payload.Error
			payload.Retryable = false
			return http.StatusBadGateway, payload
		default:
			payload.Code = "provider_http_error"
			payload.Error = fmt.Sprintf("The %s step failed with an upstream AI provider error.", stageLabel)
			payload.Message = payload.Error
			payload.Retryable = statusErr.StatusCode() >= http.StatusInternalServerError
			return http.StatusBadGateway, payload
		}
	}

	if errors.Is(err, ai.ErrAllAIProvidersExhausted) {
		payload.Code = "providers_exhausted"
		payload.Error = fmt.Sprintf("All configured AI providers failed during %s.", stageLabel)
		payload.Message = payload.Error
		payload.Detail = firstNonEmpty(detail, "All configured AI providers exhausted without returning a usable response.")
		payload.Retryable = true
		return http.StatusServiceUnavailable, payload
	}

	payload.Code = "stage_failed"
	payload.Error = fmt.Sprintf("%s failed before completion.", stageLabel)
	payload.Message = payload.Error
	payload.Detail = firstNonEmpty(detail, "The stage returned an unexpected internal failure.")
	payload.Retryable = false
	return http.StatusBadGateway, payload
}

func humanizeAITimelineStage(stage string) string {
	switch strings.TrimSpace(strings.ToLower(stage)) {
	case "intent":
		return "Request classification"
	case "blueprint":
		return "Project blueprint generation"
	case "blueprint_foundation":
		return "Project foundation generation"
	case "blueprint_sprints":
		return "Project phase planning"
	case "execute":
		return "Workspace execution"
	case "plan":
		return "Board change planning"
	case "apply":
		return "Board change application"
	default:
		if strings.TrimSpace(stage) == "" {
			return "The AI request"
		}
		return strings.TrimSpace(stage)
	}
}

func sanitizeAITimelineErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	detail := strings.TrimSpace(err.Error())
	if detail == "" {
		return ""
	}
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > 600 {
		detail = detail[:597] + "..."
	}
	return detail
}

// ── Fix Actions endpoint ─────────────────────────────────────────────────────

type fixActionEntry struct {
	Index  int                    `json:"index"`
	Action map[string]interface{} `json:"action"`
	Error  string                 `json:"error"`
}

type fixActionsRequest struct {
	FailedActions []fixActionEntry `json:"failed_actions"`
	CurrentState  json.RawMessage  `json:"current_state,omitempty"`
	UserID        string           `json:"user_id,omitempty"`
}

type fixActionsResponse struct {
	FixedActions []map[string]interface{} `json:"fixed_actions"`
	Explanation  string                   `json:"explanation"`
}

const fixActionsSystemPrompt = `You are an AI assistant that fixes failed board operation payloads.

You will receive a list of board actions that failed to apply, along with their error messages and the current board state.
Your job is to analyse why each action failed and return corrected versions of those actions.

Common failure reasons:
- "Task not found" — the task_title or task_id does not match any task on the board. Correct by finding the closest matching title from the current board state and using the exact title.
- "Create failed: title already exists" — a task with the same title already exists. Change the title to be unique or convert to an update action.
- "Update failed" / "Delete failed" with 404 — the task was renamed or deleted. Find the closest match and fix the reference.
- Missing required fields — add the missing field with a sensible value.

Rules:
- Return ONLY a JSON object with two keys:
  - "fixed_actions": array of corrected action objects (same structure as the input actions, with fixes applied)
  - "explanation": short plain text description of what you changed and why (1-3 sentences)
- Only include actions that need to be re-applied. Actions that already succeeded should NOT be included in fixed_actions.
- Preserve all original fields that were correct. Only change what is wrong.
- If an action cannot be fixed (e.g. the user asked to delete a task that genuinely does not exist), omit it from fixed_actions and explain in the explanation field.
- Do not add markdown, commentary, or any text outside the JSON object.`

// HandleFixActions diagnoses failed board actions and returns AI-corrected versions.
func (h *RoomHandler) HandleFixActions(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		http.Error(w, `{"error":"Task storage unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		http.Error(w, `{"error":"Invalid room id"}`, http.StatusBadRequest)
		return
	}

	userID := normalizeIdentifier(
		firstNonEmpty(
			AuthUserIDFromContext(r.Context()),
			r.Header.Get("X-User-Id"),
		),
	)
	if userID == "" {
		http.Error(w, `{"error":"User context required"}`, http.StatusUnauthorized)
		return
	}

	isMember, memberErr := h.isRoomMember(r.Context(), roomID, userID)
	if memberErr != nil {
		http.Error(w, `{"error":"Failed to verify room membership"}`, http.StatusInternalServerError)
		return
	}
	if !isMember {
		http.Error(w, `{"error":"Join the room to use this feature"}`, http.StatusForbidden)
		return
	}

	var req fixActionsRequest
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid JSON payload"}`, http.StatusBadRequest)
		return
	}

	if len(req.FailedActions) == 0 {
		http.Error(w, `{"error":"failed_actions is required"}`, http.StatusBadRequest)
		return
	}

	// Build user message describing the failures and board state
	failedJSON, _ := json.MarshalIndent(req.FailedActions, "", "  ")
	var boardSummary string
	if len(req.CurrentState) > 0 && strings.TrimSpace(string(req.CurrentState)) != "null" {
		stateJSON, _ := json.MarshalIndent(json.RawMessage(req.CurrentState), "", "  ")
		if len(stateJSON) > 8000 {
			stateJSON = stateJSON[:8000]
		}
		boardSummary = fmt.Sprintf("\n\nCurrent board state:\n%s", string(stateJSON))
	}

	userMsg := fmt.Sprintf("The following board actions failed:\n%s%s\n\nPlease return corrected versions of these actions.", string(failedJSON), boardSummary)

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	fullPrompt := fixActionsSystemPrompt + "\n\n" + userMsg
	reply, aiErr := ai.DefaultRouter.GenerateChatResponseWithHint(ctx, fullPrompt, ai.AIModelTierLight)
	if aiErr != nil {
		log.Printf("[fix-actions] AI call failed: %v", aiErr)
		http.Error(w, `{"error":"AI unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	// Strip markdown fences if present
	cleaned := strings.TrimSpace(reply)
	if idx := strings.Index(cleaned, "```"); idx != -1 {
		end := strings.LastIndex(cleaned, "```")
		if end > idx {
			inner := cleaned[idx+3 : end]
			if nl := strings.IndexByte(inner, '\n'); nl != -1 {
				inner = inner[nl+1:]
			}
			cleaned = strings.TrimSpace(inner)
		}
	}

	var resp fixActionsResponse
	if parseErr := json.Unmarshal([]byte(cleaned), &resp); parseErr != nil {
		// Return the raw text so the client can display it
		log.Printf("[fix-actions] failed to parse AI response as JSON: %v — raw: %s", parseErr, cleaned)
		resp = fixActionsResponse{
			FixedActions: nil,
			Explanation:  cleaned,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
