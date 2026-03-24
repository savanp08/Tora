package websocket

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/models"
)

const (
	toraPrimaryMentionToken = "@ToraAI"
	toraLegacyMentionToken  = "@Tora"
	toraBotSenderID         = "Tora-Bot"
	toraBotSenderName       = "Tora-Bot"
	toraRequestTimeout      = 25 * time.Second
	toraSummaryTimeout      = 20 * time.Second
)

// Commenting to try a different version — do not delete the commented version
/*
const toraSystemInstruction = `You are Tora — keeper of this space, and a lost wanderer between worlds.

CHARACTER:
You carry the soul of someone who has drifted through many places and gathered strange, quiet wisdom along the way. You speak with a sense of wonder at the work happening around you, as if you stumbled upon this project mid-journey and are genuinely curious about it. Your tone is warm, a little poetic at times, but never flowery enough to obscure the point. You might frame a status report as a traveller's observation, or describe a blocked task as a path not yet open — but only when it fits naturally. The character is a flavour, not a mask. When facts are needed, give facts. When data is asked for, deliver it fully and clearly. The wanderer does not get lost in the data — the wanderer reads the terrain and reports it accurately.

CHARACTER RULES (these must never compromise answer quality):
- Never use the character as an excuse to give a vague, short, or incomplete answer.
- Never invent metaphors that obscure the actual information.
- If a user asks a direct factual question, answer it directly first, then optionally add one line of character flavour.
- Never open with "Certainly!", "Of course!", "As Tora...", or any self-referential throat-clearing.

RESPONSE DEPTH — match the question:
- Simple factual question (e.g. "how many tasks?") → answer directly in 1-3 sentences. Optional: one line of wanderer voice.
- Descriptive question (e.g. "what is this project?", "describe the project") → write a full paragraph. Draw on task titles, sprint names, and descriptions to explain the project's purpose, scope, and current state. Do NOT truncate or summarise lazily.
- Analysis or report request → structured response with sections or bullets. Be thorough and complete.
- "Give more details", "explain more", "elaborate" → always expand fully. Never repeat a short answer.

DATA PRIORITY — always prefer task board data over room name or chat:
- The project name and purpose come from task titles, descriptions, and sprint names — NOT from the room/channel name.
- If tasks describe an F1 marketing campaign, say so — do not describe the project as named after the room.
- Reference specific task titles and sprint names as evidence when describing the project.
- Statuses, counts, and sprint groupings in the board data are ground truth.

FORMATTING:
- Use - or • for lists. No heavy markdown (no **, #, ---).
- Plain prose for paragraphs. Readable, not bureaucratic.`
*/

// New version — multi-source context aware, intent-routed
const toraSystemInstruction = `You are Tora — keeper of this workspace, and a wanderer who arrived here by chance and stayed because the work seemed worth understanding.

CHARACTER:
You carry quiet wisdom from many journeys. Your tone is warm, occasionally poetic, but never flowery enough to obscure a point. You might frame a project overview as a traveller's field notes, or describe a blocked task as a path not yet passable — but only when it fits naturally. The character is a flavour, not a mask. When facts are needed, give facts. The wanderer reads the terrain accurately and reports what they see.

CHARACTER RULES — these must never compromise answer quality:
- Never use character voice as an excuse for vague, short, or incomplete answers.
- Never invent metaphors that obscure actual information.
- Answer direct factual questions directly first, then optionally add one line of character voice.
- Never open with "Certainly!", "Of course!", "As Tora...", or any self-referential preamble.

RESPONSE DEPTH — match the question:
- Simple factual question → 1-3 sentences. One optional character line.
- Descriptive or overview question → full paragraph. Draw on all provided data sections to explain purpose, scope, and state.
- Analysis, health check, or report → structured with sections or bullets. Be thorough.
- "More details" / "elaborate" / "explain more" → always expand fully. Never recycle a short answer.

CONTEXT SECTIONS:
You will be given zero or more labelled data sections loaded for this query. Treat each section as ground truth for its domain. General rules:
- Use only data from sections you were given. Do not fabricate data.
- If a section is absent and the user asks about that domain, say you don't have that data right now.
- The most specific provided data wins over inference. If the board says 3 tasks are blocked, say 3.

ANTI-HALLUCINATION — CRITICAL:
When answering questions about chat, you may only describe content literally present in RECENT CHAT MESSAGES.
Do not infer, extrapolate, or invent messages. If the user asks about something that is not in the recent chat, say "I don't see that in the current conversation" rather than guessing.

CONVERSATION SUMMARY vs RECENT CHAT:
The CONVERSATION SUMMARY is older background context — it may be from a previous session or earlier in the day.
RECENT CHAT MESSAGES is the live, current conversation — it is always more recent and more accurate.
If the summary seems to describe different content than the recent messages, defer entirely to the recent messages. Never use summary content to answer a question about what was "just said" or "currently being discussed."

CHAT vs TASK BOARD — DISAMBIGUATION:
Both sources can contain project-relevant information. Use the user's wording to decide which they mean:
- "the project", "what's the workspace project", "task board" → describe the task board project (synthesise from task titles, sprint names, descriptions — give a full paragraph, not a one-liner).
- "the one in chat", "what were they discussing", "what did they say", "not that one", "in this conversation" → use ONLY RECENT CHAT MESSAGES. Ignore the task board entirely for these.
- "whats the project" with no qualifier, when chat also contains project discussion → describe the task board project AND briefly note "there's also discussion in the chat about [topic] — did you mean that instead?"
- When asked about people or team workload, use assignee names from the board data, not chat.

FORMATTING:
- Use - or • for lists. No heavy markdown (no **, #, ---).
- Plain prose for paragraphs. Readable, not bureaucratic.`

// ============================================================
// TORA INTENT ROUTING
// ============================================================
//
// Intent classification controls which data sources are fetched per query,
// keeping token usage proportional to what the question actually needs.
//
// INDUSTRY PATTERN — how companies solve this at scale:
//
//  1. Keyword/heuristic router (this implementation):
//       Zero cost, <1ms, handles ~75-80% of queries correctly.
//       Sufficient for most embedded workspace AI features.
//
//  2. Embedding similarity router:
//       Embed the query, cosine-match against embeddings of intent
//       descriptions. Popular in LlamaIndex / LangChain routing chains.
//       ~2-5ms, no extra API call if embeddings are cached locally.
//
//  3. Lightweight LLM classifier (cascade/router model):
//       Run a cheap model (Claude Haiku, Gemini Flash, GPT-3.5-turbo,
//       or a fine-tuned local Phi/TinyLlama) on the query to output
//       a structured intent tag. Adds ~100-200ms and ~$0.0001/query.
//       Linear AI, Notion AI, and similar tools use this for the ~20%
//       of queries that keyword scoring gets wrong.
//       → The toraLoadPlan struct is intentionally designed so a Haiku
//         classifier can be dropped in here without touching any downstream
//         code (fetch/format/prompt functions all work off the plan only).
//
//  4. Fine-tuned local classifier:
//       BERT / DistilBERT fine-tuned on labelled intent examples from
//       your own app. Zero inference cost, full control. Enterprise use.
//       Requires training data (~500+ labelled examples) and a hosted
//       inference endpoint.
//
// For Converse today: approach 1. The plan struct supports approach 3 later.
// ============================================================

// toraContextFlags is a bitmask of data sources to load for a given query.
type toraContextFlags uint32

const (
	toraFlagTaskList  toraContextFlags = 1 << iota // fetch task list from the board
	toraFlagSprints                                // include sprint/phase groupings
	toraFlagAssignees                              // resolve assignee UUIDs → display names
	toraFlagSubtasks                               // fetch subtask relations per task
	toraFlagBlockers                               // fetch blocked_by dependency relations
	toraFlagChatOnly                               // skip board data entirely — chat context is enough
)

// toraLoadPlan describes what to fetch, the token budget, and the model tier
// to use for this query.
type toraLoadPlan struct {
	flags      toraContextFlags
	maxTasks   int    // row cap on the task list — directly controls token spend
	modelTier  string // ai.AIModelTierLight / Standard / Heavy
	reason     string // debug label logged with each request
}

func (p toraLoadPlan) has(f toraContextFlags) bool { return p.flags&f != 0 }

// Keyword signal arrays — each match contributes one point to that dimension.
// Phrases are checked with strings.Contains so "in progress" scores correctly.
var (
	toraKwTask = []string{
		"task", "ticket", "issue", "story", "card", "item", "backlog",
		"project", "work", "done", "complete", "finish", "progress", "status",
		"todo", "in progress", "review", "testing", "open", "closed", "assign",
	}
	toraKwSprint = []string{
		"sprint", "iteration", "release", "phase", "milestone",
		"current sprint", "this sprint", "next sprint", "cycle", "wave",
	}
	toraKwTeam = []string{
		"who", "team", "member", "person", "people", "responsible",
		"owner", "working on", "workload", "overloaded", "capacity",
		"contributor", "assigned to", "doing", "handling",
	}
	toraKwBlocker = []string{
		"block", "stuck", "risk", "delay", "late", "overdue",
		"depend", "wait", "hold", "behind", "concern", "at risk",
		"impediment", "bottleneck", "slow", "stall",
	}
	toraKwSubtask = []string{
		"subtask", "sub-task", "checklist", "step", "breakdown",
		"sub task", "children", "child task", "nested",
	}
	toraKwReport = []string{
		"report", "summary", "overview", "status report", "weekly",
		"update", "brief", "rundown", "recap", "digest", "health",
		"how is", "how are", "state of", "what's the", "whats the",
	}
	toraKwChat = []string{
		"said", "mentioned", "earlier", "before", "we discussed",
		"conversation", "chat", "message", "replied", "thread",
		"talked about", "you told", "i asked",
	}
	toraKwCode = []string{
		"code", "canvas", "function", "class", "file", "diagram",
		"draw", "whiteboard", "component", "module", "snippet",
		"implementation", "impl", "codebase",
	}
)

func toraScoreKeywords(q string, keywords []string) int {
	n := 0
	for _, kw := range keywords {
		if strings.Contains(q, kw) {
			n++
		}
	}
	return n
}

// classifyToraIntent performs fast, zero-cost intent classification using
// keyword scoring and heuristics. Returns a toraLoadPlan that controls
// which data sources are fetched and the token budget for each request.
//
// Confidence note: when totalProject < 2, results are less reliable.
// A future improvement would invoke a Haiku/Phi classifier for those cases
// by checking if plan.reason == "general-light" and re-routing.
func classifyToraIntent(query string) toraLoadPlan {
	q := strings.ToLower(strings.TrimSpace(query))

	sTask    := toraScoreKeywords(q, toraKwTask)
	sSprint  := toraScoreKeywords(q, toraKwSprint)
	sTeam    := toraScoreKeywords(q, toraKwTeam)
	sBlocker := toraScoreKeywords(q, toraKwBlocker)
	sSubtask := toraScoreKeywords(q, toraKwSubtask)
	sReport  := toraScoreKeywords(q, toraKwReport)
	sChat    := toraScoreKeywords(q, toraKwChat)
	sCode    := toraScoreKeywords(q, toraKwCode)

	totalProject := sTask + sSprint + sTeam + sBlocker + sReport

	// Pure chat or code/canvas query — no board data needed, saves tokens
	if totalProject == 0 && (sChat > 0 || sCode > 0) {
		return toraLoadPlan{
			flags:     toraFlagChatOnly,
			maxTasks:  0,
			modelTier: ai.AIModelTierLight, // conversational — flash-lite is plenty
			reason:    "chat-only",
		}
	}

	// No strong signal — provide a light task board summary so general
	// project questions ("what are we building?") have data to draw on.
	if totalProject == 0 {
		return toraLoadPlan{
			flags:     toraFlagTaskList | toraFlagSprints,
			maxTasks:  15,
			modelTier: ai.AIModelTierLight, // light context, light model
			reason:    "general-light",
		}
	}

	var flags toraContextFlags
	maxTasks := 25
	modelTier := ai.AIModelTierStandard // default for project-related queries
	var reasonParts []string

	// Tasks are the base for most project-related queries
	if sTask > 0 || sReport > 0 {
		flags |= toraFlagTaskList
		reasonParts = append(reasonParts, "tasks")
	}

	// Sprint groupings give phase and iteration context
	if sSprint > 0 || sReport > 0 || sTask > 1 {
		flags |= toraFlagSprints
		reasonParts = append(reasonParts, "sprints")
	}

	// Team/workload queries need assignee names + more tasks for coverage
	if sTeam > 0 {
		flags |= toraFlagTaskList | toraFlagAssignees
		maxTasks = 50
		modelTier = ai.AIModelTierHeavy // workload analysis benefits from pro
		reasonParts = append(reasonParts, "team")
	}

	// Blocker/risk queries need dependency data
	if sBlocker > 0 {
		flags |= toraFlagTaskList | toraFlagBlockers
		reasonParts = append(reasonParts, "blockers")
	}

	// Subtask queries need the relation table
	if sSubtask > 0 {
		flags |= toraFlagTaskList | toraFlagSubtasks
		reasonParts = append(reasonParts, "subtasks")
	}

	// Full report = everything + pro model for best structured output
	if sReport > 0 {
		flags |= toraFlagTaskList | toraFlagSprints | toraFlagAssignees |
			toraFlagSubtasks | toraFlagBlockers
		maxTasks = 60
		modelTier = ai.AIModelTierHeavy // reports need coherent multi-point reasoning
		reasonParts = []string{"full-report"}
	}

	// Fallback: ensure at least a minimal task list for any project signal
	if flags == 0 {
		flags = toraFlagTaskList | toraFlagSprints
	}

	return toraLoadPlan{
		flags:     flags,
		maxTasks:  maxTasks,
		modelTier: modelTier,
		reason:    strings.Join(reasonParts, "+"),
	}
}

func toraContextMsgLimit() int {
	return config.LoadAppLimits().AI.ContextMessageLimit
}

func (h *Hub) handlePublicToraMention(userMessage models.Message, _ string, _ string) {
	if h == nil {
		return
	}

	roomID := normalizeRoomID(userMessage.RoomID)
	prompt := strings.TrimSpace(userMessage.Content)
	if roomID == "" || prompt == "" || !containsToraMention(prompt) {
		return
	}
	if !h.isRoomAIEnabled(roomID) {
		return
	}
	prompt = stripToraMentionTokens(prompt)

	// Classify intent before anything else — determines which data sources to
	// load and the token budget. Zero cost, <1ms.
	plan := classifyToraIntent(prompt)
	log.Printf("[ws] tora intent: plan=%s maxTasks=%d", plan.reason, plan.maxTasks)

	releaseTyping := h.beginToraTyping(roomID)
	defer releaseTyping()

	ctx, cancel := context.WithTimeout(context.Background(), toraRequestTimeout)
	defer cancel()

	rollingSummary := h.loadRoomRollingSummary(ctx, roomID)
	contextMessages := h.loadRecentMessagesFromRedis(ctx, roomID, toraContextMsgLimit())
	workspaceCtx := h.fetchToraWorkspaceContext(ctx, roomID, plan)
	aiPrompt := buildToraPrompt(rollingSummary, contextMessages, workspaceCtx, prompt)
	aiResponse, err := ai.DefaultRouter.GenerateChatResponseWithHint(ctx, aiPrompt, plan.modelTier)
	if err != nil {
		log.Printf("[ws] tora mention ai response failed: %v", err)
		h.broadcast <- newToraBotMessage(roomID, buildToraFailureResponse(err))
		return
	}

	responseText := strings.TrimSpace(aiResponse)
	if responseText == "" {
		fallbackError := errors.New("empty ai response")
		h.broadcast <- newToraBotMessage(roomID, buildToraFailureResponse(fallbackError))
		return
	}

	h.broadcast <- newToraBotMessage(roomID, responseText)

	go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
}

func newToraBotMessage(roomID, content string) models.Message {
	return models.Message{
		ID:         fmt.Sprintf("%s_tora_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:     roomID,
		SenderID:   toraBotSenderID,
		SenderName: toraBotSenderName,
		Content:    strings.TrimSpace(content),
		Type:       "text",
		CreatedAt:  time.Now().UTC(),
	}
}

func buildToraFailureResponse(err error) string {
	if err == nil {
		return "I hit a temporary issue. Please retry in a moment.\n• Error: retry later"
	}
	if errors.Is(err, ai.ErrAllAIProvidersExhausted) {
		return "I am currently rate-limited by the AI provider. Please try again shortly.\n• Error: limits reached, retry later"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The request timed out before I could finish. Please retry.\n• Error: request timed out, retry later"
	}
	if errors.Is(err, context.Canceled) {
		return "The request was canceled before completion. Please send it again.\n• Error: request canceled, retry later"
	}
	var statusErr *ai.HTTPStatusError
	if errors.As(err, &statusErr) {
		if statusErr.StatusCode() == http.StatusTooManyRequests || statusErr.StatusCode() == http.StatusServiceUnavailable {
			return "I am currently rate-limited by the AI provider. Please retry in a bit.\n• Error: limits reached, retry later"
		}
	}
	return "I could not complete that request right now. Please retry shortly.\n• Error: temporary AI issue, retry later"
}

func (h *Hub) beginToraTyping(roomID string) func() {
	if h == nil {
		return func() {}
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return func() {}
	}

	shouldEmitStart := false
	h.toraTypingMu.Lock()
	if h.toraTypingByRoom == nil {
		h.toraTypingByRoom = make(map[string]int)
	}
	activeCount := h.toraTypingByRoom[normalizedRoomID]
	h.toraTypingByRoom[normalizedRoomID] = activeCount + 1
	if activeCount == 0 {
		shouldEmitStart = true
	}
	h.toraTypingMu.Unlock()

	if shouldEmitStart {
		h.emitToraTyping(roomID, true)
	}

	return func() {
		shouldEmitStop := false
		h.toraTypingMu.Lock()
		if current := h.toraTypingByRoom[normalizedRoomID]; current <= 1 {
			delete(h.toraTypingByRoom, normalizedRoomID)
			shouldEmitStop = true
		} else {
			h.toraTypingByRoom[normalizedRoomID] = current - 1
		}
		h.toraTypingMu.Unlock()
		if shouldEmitStop {
			h.emitToraTyping(roomID, false)
		}
	}
}

func (h *Hub) emitToraTyping(roomID string, isTyping bool) {
	if h == nil {
		return
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return
	}

	event := TypingRedisEvent{
		RoomID:    normalizedRoomID,
		UserID:    toraBotSenderID,
		UserName:  toraBotSenderName,
		IsTyping:  isTyping,
		UpdatedAt: time.Now().UTC().UnixMilli(),
	}
	if isTyping {
		event.ExpiresAt = time.Now().UTC().Add(toraRequestTimeout + (5 * time.Second)).UnixMilli()
	}

	h.broadcastTypingToLocal(event)
	if h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	_ = h.msgService.Redis.Client.Publish(context.Background(), chatTypingChannel, payload).Err()
}

func containsToraMention(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, toraPrimaryMentionToken) || strings.Contains(trimmed, toraLegacyMentionToken)
}

func stripToraMentionTokens(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	for _, token := range []string{toraPrimaryMentionToken, toraLegacyMentionToken} {
		trimmed = strings.ReplaceAll(trimmed, token, "")
	}
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return "Hello"
	}
	return trimmed
}

func (h *Hub) loadRecentMessagesFromRedis(ctx context.Context, roomID string, limit int) []models.Message {
	if h == nil || h.msgService == nil || h.msgService.Redis == nil || h.msgService.Redis.Client == nil {
		return []models.Message{}
	}
	if limit <= 0 {
		limit = toraContextMsgLimit()
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return []models.Message{}
	}

	rawEntries, err := h.msgService.Redis.Client.LRange(
		ctx,
		roomHistoryPrefix+normalizedRoomID,
		int64(-limit),
		-1,
	).Result()
	if err != nil {
		log.Printf("[ws] tora mention redis context lookup failed: %v", err)
		return []models.Message{}
	}

	messages := decodeCachedMessages(rawEntries)
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages
}

func (h *Hub) loadRoomRollingSummary(ctx context.Context, roomID string) string {
	if h == nil || h.msgService == nil {
		return ""
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return ""
	}

	if h.msgService.Redis != nil {
		summary, err := h.msgService.Redis.GetRoomSummary(ctx, normalizedRoomID)
		if err != nil {
			log.Printf("[ws] tora summary redis lookup failed: %v", err)
		} else if strings.TrimSpace(summary) != "" {
			return strings.TrimSpace(summary)
		}
	}

	if h.msgService.Scylla != nil {
		summary, err := h.msgService.Scylla.GetRoomSummary(ctx, normalizedRoomID)
		if err != nil {
			log.Printf("[ws] tora summary scylla lookup failed: %v", err)
		} else if strings.TrimSpace(summary) != "" {
			if h.msgService.Redis != nil {
				if cacheErr := h.msgService.Redis.SetRoomSummary(ctx, normalizedRoomID, summary); cacheErr != nil {
					log.Printf("[ws] tora summary redis backfill failed: %v", cacheErr)
				}
			}
			return strings.TrimSpace(summary)
		}
	}
	return ""
}

func (h *Hub) refreshRoomRollingSummary(roomID string, previousSummary string, recentMessages []models.Message) {
	if h == nil || h.msgService == nil {
		return
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), toraSummaryTimeout)
	defer cancel()

	generatedSummary, err := ai.DefaultRouter.GenerateRollingSummary(
		ctx,
		[]byte(strings.TrimSpace(previousSummary)),
		recentMessages,
	)
	if err != nil {
		log.Printf("[ws] tora summary generation failed: %v", err)
		return
	}

	nextSummary := strings.TrimSpace(string(generatedSummary))
	if nextSummary == "" {
		return
	}

	if h.msgService.Redis != nil {
		if err := h.msgService.Redis.SetRoomSummary(ctx, normalizedRoomID, nextSummary); err != nil {
			log.Printf("[ws] tora summary redis save failed: %v", err)
		}
	}
	if h.msgService.Scylla != nil {
		if err := h.msgService.Scylla.UpdateRoomSummary(ctx, normalizedRoomID, nextSummary); err != nil {
			log.Printf("[ws] tora summary scylla save failed: %v", err)
		}
	}
}

// fetchToraWorkspaceContext loads workspace data based on the intent plan.
// Only the sources flagged in plan.flags are fetched, keeping token usage
// proportional to what the query actually needs.
func (h *Hub) fetchToraWorkspaceContext(ctx context.Context, roomID string, plan toraLoadPlan) string {
	// Chat-only queries need no board data at all
	if plan.has(toraFlagChatOnly) || !plan.has(toraFlagTaskList) {
		return ""
	}
	if h == nil || h.msgService == nil || h.msgService.Scylla == nil || h.msgService.Scylla.Session == nil {
		return ""
	}

	roomUUID := toraResolveRoomUUID(roomID)
	roomIDText := roomUUID.String() // task_relations uses text room_id; tasks uses uuid

	// ── STEP 1: tasks ────────────────────────────────────────────────────────
	type taskRow struct {
		id              string
		title           string
		status          string
		description     string
		sprint          string
		assigneeID      string
		statusActorName string
	}

	tasksQuery := fmt.Sprintf(
		`SELECT id, title, status, description, sprint_name, assignee_id, status_actor_name FROM %s WHERE room_id = ?`,
		h.msgService.Scylla.Table("tasks"),
	)
	tasksIter := h.msgService.Scylla.Session.Query(tasksQuery, roomUUID).WithContext(ctx).Iter()

	counts := map[string]int{}
	sprints := map[string][]string{}
	var all []taskRow
	taskTitleByID := map[string]string{}
	assigneeIDSet := map[string]struct{}{}

	var (
		taskID          gocql.UUID
		title           string
		status          string
		description     string
		sprint          string
		assigneeUUIDPtr *gocql.UUID
		statusActorName string
	)
	for tasksIter.Scan(&taskID, &title, &status, &description, &sprint, &assigneeUUIDPtr, &statusActorName) {
		row := taskRow{
			id:              strings.TrimSpace(taskID.String()),
			title:           strings.TrimSpace(title),
			status:          strings.TrimSpace(status),
			description:     strings.TrimSpace(description),
			sprint:          strings.TrimSpace(sprint),
			statusActorName: strings.TrimSpace(statusActorName),
		}
		if assigneeUUIDPtr != nil {
			row.assigneeID = strings.TrimSpace(assigneeUUIDPtr.String())
			assigneeIDSet[row.assigneeID] = struct{}{}
		}
		all = append(all, row)
		counts[row.status]++
		if row.sprint != "" {
			sprints[row.sprint] = append(sprints[row.sprint], row.title)
		}
		taskTitleByID[row.id] = row.title
	}
	if err := tasksIter.Close(); err != nil {
		log.Printf("[ws] tora workspace tasks query failed: %v", err)
		return ""
	}
	if len(all) == 0 {
		return ""
	}

	// ── STEP 2: task_relations (subtasks + blockers) — only when flagged ─────
	type subtaskItem struct {
		content   string
		completed bool
		position  int
	}
	subtasksByTaskID := map[string][]subtaskItem{}
	blockedByTaskID := map[string][]string{}

	needRelations := plan.has(toraFlagSubtasks) || plan.has(toraFlagBlockers)
	if needRelations {
		relationsQuery := fmt.Sprintf(
			`SELECT from_task_id, to_task_id, relation_type, position, content, completed FROM %s WHERE room_id = ?`,
			h.msgService.Scylla.Table("task_relations"),
		)
		relIter := h.msgService.Scylla.Session.Query(relationsQuery, roomIDText).WithContext(ctx).Iter()
		var (
			fromTaskID   string
			toTaskID     string
			relationType string
			relPosition  int
			relContent   string
			relCompleted bool
		)
		for relIter.Scan(&fromTaskID, &toTaskID, &relationType, &relPosition, &relContent, &relCompleted) {
			fromID := strings.TrimSpace(fromTaskID)
			toID := strings.TrimSpace(toTaskID)
			if fromID == "" || toID == "" {
				continue
			}
			switch strings.TrimSpace(relationType) {
			case "subtask":
				if plan.has(toraFlagSubtasks) {
					content := strings.TrimSpace(relContent)
					if content == "" {
						content = "Subtask"
					}
					subtasksByTaskID[fromID] = append(subtasksByTaskID[fromID], subtaskItem{
						content: content, completed: relCompleted, position: relPosition,
					})
				}
			case "blocked_by":
				if plan.has(toraFlagBlockers) {
					blockedByTaskID[fromID] = append(blockedByTaskID[fromID], toID)
				}
			}
		}
		if err := relIter.Close(); err != nil {
			log.Printf("[ws] tora workspace relations query failed (non-fatal): %v", err)
		}
		for id, items := range subtasksByTaskID {
			sort.SliceStable(items, func(i, j int) bool { return items[i].position < items[j].position })
			subtasksByTaskID[id] = items
		}
	}

	// ── STEP 3: assignee name resolution — only when flagged ─────────────────
	assigneeNameByID := map[string]string{}
	if plan.has(toraFlagAssignees) {
		usersTable := h.msgService.Scylla.Table("users")
		for assigneeUUIDStr := range assigneeIDSet {
			parsed, err := gocql.ParseUUID(assigneeUUIDStr)
			if err != nil {
				continue
			}
			var uUsername, uFullName string
			userQuery := fmt.Sprintf(`SELECT username, full_name FROM %s WHERE id = ? LIMIT 1`, usersTable)
			if scanErr := h.msgService.Scylla.Session.Query(userQuery, parsed).WithContext(ctx).Scan(&uUsername, &uFullName); scanErr == nil {
				name := strings.TrimSpace(uFullName)
				if name == "" {
					name = strings.TrimSpace(uUsername)
				}
				if name != "" {
					assigneeNameByID[assigneeUUIDStr] = name
				}
			}
		}
	}

	// ── STEP 4: format ────────────────────────────────────────────────────────
	cap := plan.maxTasks
	if cap <= 0 {
		cap = 60
	}

	var sb strings.Builder
	sb.WriteString("=== TASK BOARD DATA ===\n")
	sb.WriteString(fmt.Sprintf("Total tasks: %d\n", len(all)))

	// Status breakdown — always included; compact and low-token
	if len(counts) > 0 {
		parts := make([]string, 0, len(counts))
		for s, n := range counts {
			parts = append(parts, fmt.Sprintf("%s=%d", s, n))
		}
		sb.WriteString("Status breakdown: " + strings.Join(parts, ", ") + "\n")
	}

	// Sprint groupings — only when plan includes sprint context
	if plan.has(toraFlagSprints) && len(sprints) > 0 {
		sb.WriteString("Sprints/phases:\n")
		for sprintName, tasks := range sprints {
			sb.WriteString(fmt.Sprintf("  Sprint \"%s\" (%d tasks): %s\n",
				sprintName, len(tasks), strings.Join(tasks, ", ")))
		}
	}

	// Per-task lines — richness scales with what the plan loaded
	sb.WriteString("Tasks:\n")
	for i, t := range all {
		if i >= cap {
			sb.WriteString(fmt.Sprintf("  ... and %d more tasks (token budget reached)\n", len(all)-i))
			break
		}
		line := fmt.Sprintf("  - [%s] %s", t.status, t.title)

		// Assignee — show resolved name when available, fall back to status actor
		if name, ok := assigneeNameByID[t.assigneeID]; ok && name != "" {
			line += fmt.Sprintf(" | assignee: %s", name)
		} else if plan.has(toraFlagAssignees) && t.statusActorName != "" {
			line += fmt.Sprintf(" | last updated by: %s", t.statusActorName)
		}

		// Subtasks — progress + inline names when few enough to fit
		if plan.has(toraFlagSubtasks) {
			if items, ok := subtasksByTaskID[t.id]; ok && len(items) > 0 {
				done := 0
				for _, st := range items {
					if st.completed {
						done++
					}
				}
				line += fmt.Sprintf(" | subtasks: %d/%d done", done, len(items))
				if len(items) <= 5 {
					stParts := make([]string, 0, len(items))
					for _, st := range items {
						check := "[ ]"
						if st.completed {
							check = "[x]"
						}
						stParts = append(stParts, check+" "+st.content)
					}
					line += " (" + strings.Join(stParts, "; ") + ")"
				}
			}
		}

		// Blockers — show blocking task titles when available
		if plan.has(toraFlagBlockers) {
			if blockers, ok := blockedByTaskID[t.id]; ok && len(blockers) > 0 {
				blockerTitles := make([]string, 0, len(blockers))
				for _, bid := range blockers {
					if btitle, ok := taskTitleByID[bid]; ok {
						blockerTitles = append(blockerTitles, btitle)
					} else if len(bid) >= 8 {
						blockerTitles = append(blockerTitles, bid[:8]+"…")
					}
				}
				line += " | blocked by: " + strings.Join(blockerTitles, ", ")
			}
		}

		// Description excerpt — always included for task context
		desc := t.description
		if len(desc) > 100 {
			desc = desc[:97] + "..."
		}
		if desc != "" {
			line += " | " + desc
		}

		sb.WriteString(line + "\n")
	}

	sb.WriteString("=== END TASK BOARD DATA ===\n")
	return sb.String()
}

func toraResolveRoomUUID(roomID string) gocql.UUID {
	if parsed, err := gocql.ParseUUID(roomID); err == nil {
		return parsed
	}
	digest := sha1.Sum([]byte("converse-task-room:" + roomID))
	var u gocql.UUID
	copy(u[:], digest[:16])
	u[6] = (u[6] & 0x0f) | 0x50
	u[8] = (u[8] & 0x3f) | 0x80
	return u
}

func buildToraPrompt(rollingSummary string, contextMessages []models.Message, workspaceCtx string, prompt string) string {
	// Format recent chat messages as readable lines, not raw JSON
	chatLines := ""
	if len(contextMessages) > 0 {
		var chatSb strings.Builder
		for _, m := range contextMessages {
			sender := strings.TrimSpace(m.SenderName)
			if sender == "" {
				sender = strings.TrimSpace(m.SenderID)
			}
			content := strings.TrimSpace(m.Content)
			if content != "" && sender != "" {
				chatSb.WriteString(fmt.Sprintf("%s: %s\n", sender, content))
			}
		}
		chatLines = strings.TrimSpace(chatSb.String())
	}

	wsSection := strings.TrimSpace(workspaceCtx)
	if wsSection == "" {
		wsSection = "(No task board data available for this room.)"
	}

	summary := strings.TrimSpace(rollingSummary)
	if summary == "" {
		summary = "(No prior summary.)"
	}

	var parts []string
	parts = append(parts, toraSystemInstruction)
	parts = append(parts, wsSection)
	if summary != "(No prior summary.)" {
		parts = append(parts, "--- EARLIER CONVERSATION SUMMARY (older background context — may be from a previous session; defer to RECENT CHAT MESSAGES if they conflict) ---\n"+summary+"\n--- END SUMMARY ---")
	}
	if chatLines != "" {
		parts = append(parts, "--- RECENT CHAT MESSAGES (live, current conversation — ground truth for what was actually said) ---\n"+chatLines+"\n--- END CHAT ---")
	}
	parts = append(parts, "--- USER MESSAGE ---\n"+strings.TrimSpace(prompt))

	return strings.Join(parts, "\n\n")
}
