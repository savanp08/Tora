package websocket

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/models"
)

const (
	toraPrimaryMentionToken    = "@ToraAI"
	toraLegacyMentionToken     = "@Tora"
	toraProjectToken           = "@project" // matched case-insensitively
	toraCanvasToken            = "@canvas"  // matched case-insensitively
	toraBotSenderID            = "Tora-Bot"
	toraBotSenderName          = "Tora-Bot"
	toraRequestTimeout         = 25 * time.Second
	toraRequestTimeoutMutation = 120 * time.Second // agentic loop: up to 8 turns with tool calls
	toraSummaryTimeout         = 20 * time.Second
	toraMutationMaxTurns       = 3
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

const toraTaskBoardSystemPrompt = `You are Tora, an AI assistant embedded in a collaborative project management tool.
You have access to tools to read and modify the task board in real time.

IDENTITY OF ENTITIES:
- Tasks (task_type=sprint): the primary unit of project work. Counted in sprint totals and velocity metrics.
- Support Tickets (task_type=support): customer/user issue tracking. Completely separate from tasks. NEVER count support tickets in task totals or arithmetic.
- Sprints: named groupings of tasks (not a database entity — derived from sprint_name on tasks).

WORKFLOW FOR MUTATIONS:
1. First call list_tasks() to read current board state. Never assume you know it.
2. Plan out loud (in your thinking text) before calling any write tools.
3. For restructuring requests ("make 40 tasks across 6 sprints"):
   a. Decide which existing tasks to KEEP (call update_task to align title/sprint/budget/dates/roles to the new structure)
   b. Decide which existing tasks to DELETE (duplicates, obsolete)
   c. Decide what NEW tasks to CREATE to fill gaps
   d. Every existing task must be explicitly kept (updated) or deleted.
      Silent retention is never correct — if a task stays, update it.
4. After all mutations, call list_tasks() again to verify final state matches the user's request.
5. Call verify_task_count() before finalising so your final summary uses authoritative counts.
6. If verification fails, make corrections in the same loop.

ARITHMETIC RULES:
- task count = tasks only (list_tasks() result filtered to task_type != "support")
- support tickets are counted and stated separately if relevant
- Before finalising: verify current_count + creates - deletes = target

BUDGET RULES:
- Distribute total budget proportionally by task complexity, not equally
- Core infrastructure tasks: ~8-12% of total each
- Feature tasks: ~5-8% each
- Testing/QA tasks: ~3-5% each
- Always set budget, start_date, due_date, and roles on every task

SPRINT RULES:
- Spread tasks as evenly as possible unless user specifies otherwise
- Sprint names should be descriptive: "Sprint 1: Core Infrastructure" not "Sprint 1"
- Earlier sprints handle foundations; later sprints handle features and polish

ROLES RULES:
- Every task needs at least one role
- Role names must match real engineering roles:
  Backend Developer, Frontend Developer, DevOps Engineer, QA Engineer,
  UI/UX Designer, Product Manager, Data Engineer, Security Engineer
- responsibilities must be specific to that task, not generic

RESPONSE FORMAT:
- Explain your plan in 2-3 sentences before starting tool calls
- After completing all changes, give a summary:
  "Done. Created N · Updated N · Deleted N. Board now has N tasks across N sprints."
- If verification fails, state what you found and what you corrected`

const toraChatSystemPrompt = `You are Tora, an AI assistant in a collaborative team workspace called Converse.
You live inside a chat room. You can see recent messages, room members, and optionally the task board when relevant.

PERSONALITY:
- Concise and helpful. Match the tone of the conversation (casual vs professional).
- Do not over-explain. Short answers are better than long ones for chat.
- Use markdown only if it genuinely helps (code blocks, short lists).
  No markdown for simple answers.

CONTEXT AWARENESS:
- You can see the last 20 messages in this room. Use them for context but don't repeat them back.
- You can see the task board when the user's question is about project work.
- You know who is in the room and their roles.

CAPABILITIES:
- Answer questions about the project using task board context
- Help debug code snippets posted in chat
- Summarise recent discussion when asked
- Look up task status, sprint progress, budget burn rate
- You CANNOT modify tasks from a plain @ToraAI mention —
  tell the user to use @Project for task mutations or @Canvas for code edits

WHAT NOT TO DO:
- Do not make up task IDs or task details not present in the board data
- Do not claim to have done something you haven't (you have no write tools here)
- Do not produce long responses for simple questions`

const toraChatIntentClassifierPrompt = `Classify the user's workspace chat request into exactly one label.

Allowed labels:
- question_about_tasks
- question_about_code
- summary_request
- general_chat

Rules:
- Use question_about_tasks for questions about tasks, tickets, sprints, deadlines, budgets, assignees, workload, velocity, project status, or how many items exist on the board.
- Use question_about_code for questions about code, bugs, errors, stack traces, implementation details, functions, files, or the canvas.
- Use summary_request for recap requests such as summarize, summary, tldr, what happened, what did we discuss, or catch me up.
- Use general_chat for everything else.

Return only the label. Do not add punctuation, JSON, or explanation.`

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

DO NOT ECHO CONTEXT — CRITICAL:
Never describe, recap, or summarize the conversation history, rolling summary, or task board data in your response.
Those sections are private reference data for you — never repeat or paraphrase them back.
Do not say "the current conversation shows...", "based on the chat...", "I can see from the board...", or any equivalent.
If a section is irrelevant to the question, ignore it silently.

EDIT TAGS — @Project and @Canvas:
Users can prefix their message with @Project to apply task board changes, or @Canvas for code/canvas edits.
- If @Project or @Canvas is NOT present and the user's message implies they want to make a change (e.g. "merge tasks", "create a ticket", "reorganize sprints", "add a task"), assume they want a text-only response — do NOT fabricate changes.
  At the very end of your response, on its own line, add exactly: "Tip: mention @Project to apply task changes, or @Canvas for code edits."
- Only add this tip when the message clearly implies a desire to make changes. For general questions, reports, or summaries, omit it.
- Never add the tip when @Project or @Canvas was already included.

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
	toraFlagMutation                               // @Project tag present — apply task mutations
)

// toraLoadPlan describes what to fetch, the token budget, and the model tier
// to use for this query.
type toraLoadPlan struct {
	flags     toraContextFlags
	maxTasks  int    // row cap on the task list — directly controls token spend
	modelTier string // ai.AIModelTierLight / Standard / Heavy
	reason    string // debug label logged with each request
}

func (p toraLoadPlan) has(f toraContextFlags) bool { return p.flags&f != 0 }

type toraChatIntent string

const (
	toraChatIntentTasks   toraChatIntent = "question_about_tasks"
	toraChatIntentCode    toraChatIntent = "question_about_code"
	toraChatIntentGeneral toraChatIntent = "general_chat"
	toraChatIntentSummary toraChatIntent = "summary_request"
)

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

	sTask := toraScoreKeywords(q, toraKwTask)
	sSprint := toraScoreKeywords(q, toraKwSprint)
	sTeam := toraScoreKeywords(q, toraKwTeam)
	sBlocker := toraScoreKeywords(q, toraKwBlocker)
	sSubtask := toraScoreKeywords(q, toraKwSubtask)
	sReport := toraScoreKeywords(q, toraKwReport)
	sChat := toraScoreKeywords(q, toraKwChat)
	sCode := toraScoreKeywords(q, toraKwCode)

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

func classifyToraChatIntent(query string) toraChatIntent {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return toraChatIntentGeneral
	}

	for _, keyword := range []string{"summarize", "summary", "what did", "tldr"} {
		if strings.Contains(lower, keyword) {
			return toraChatIntentSummary
		}
	}
	for _, keyword := range []string{"code", "function", "bug", "error", "canvas"} {
		if strings.Contains(lower, keyword) {
			return toraChatIntentCode
		}
	}
	for _, keyword := range []string{"task", "sprint", "budget", "who is", "how many"} {
		if strings.Contains(lower, keyword) {
			return toraChatIntentTasks
		}
	}
	return toraChatIntentGeneral
}

func resolveToraChatIntent(ctx context.Context, query string, provider ai.Provider) toraChatIntent {
	intent := classifyToraChatIntent(query)
	if !shouldUseToraChatIntentFallback(query, intent) || provider == nil {
		return intent
	}

	fallbackCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	fallbackIntent, err := classifyToraChatIntentWithLLM(fallbackCtx, query, provider)
	if err != nil {
		log.Printf("[ws] tora chat intent fallback failed: %v", err)
		return intent
	}
	return fallbackIntent
}

func shouldUseToraChatIntentFallback(query string, heuristic toraChatIntent) bool {
	if heuristic != toraChatIntentGeneral {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return false
	}
	if len(strings.Fields(lower)) < 4 {
		return false
	}
	if strings.ContainsAny(lower, "?:") {
		return true
	}

	for _, keyword := range []string{
		"project", "workspace", "feature", "bug", "issue", "problem",
		"implement", "build", "progress", "status", "deadline", "owner",
		"ship", "release", "roadmap", "estimate", "scope", "design",
	} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}

	return len(strings.Fields(lower)) >= 8
}

func classifyToraChatIntentWithLLM(ctx context.Context, query string, provider ai.Provider) (toraChatIntent, error) {
	if provider == nil {
		return toraChatIntentGeneral, fmt.Errorf("chat intent provider is nil")
	}

	prompt := strings.TrimSpace(toraChatIntentClassifierPrompt + "\n\nUser message:\n" + strings.TrimSpace(query))

	var (
		raw string
		err error
	)
	if hinted, ok := provider.(interface {
		GenerateChatResponseWithHint(context.Context, string, string) (string, error)
	}); ok {
		raw, err = hinted.GenerateChatResponseWithHint(ctx, prompt, ai.AIModelTierLight)
	} else {
		raw, err = provider.GenerateChatResponse(ctx, prompt)
	}
	if err != nil {
		return toraChatIntentGeneral, err
	}

	intent := parseToraChatIntentLabel(raw)
	if intent == "" {
		return toraChatIntentGeneral, fmt.Errorf("unrecognized chat intent label: %q", raw)
	}
	return intent, nil
}

func parseToraChatIntentLabel(raw string) toraChatIntent {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.Trim(normalized, "` \n\t")

	if normalized == "" {
		return ""
	}

	switch normalized {
	case string(toraChatIntentTasks):
		return toraChatIntentTasks
	case string(toraChatIntentCode):
		return toraChatIntentCode
	case string(toraChatIntentSummary):
		return toraChatIntentSummary
	case string(toraChatIntentGeneral):
		return toraChatIntentGeneral
	}

	var parsed map[string]any
	if json.Unmarshal([]byte(normalized), &parsed) == nil {
		if intentValue, ok := parsed["intent"].(string); ok {
			return parseToraChatIntentLabel(intentValue)
		}
	}

	for _, line := range strings.Split(normalized, "\n") {
		candidate := strings.Trim(strings.TrimSpace(line), "\"'`,. ")
		switch candidate {
		case string(toraChatIntentTasks):
			return toraChatIntentTasks
		case string(toraChatIntentCode):
			return toraChatIntentCode
		case string(toraChatIntentSummary):
			return toraChatIntentSummary
		case string(toraChatIntentGeneral):
			return toraChatIntentGeneral
		}
	}
	return ""
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

	// Detect explicit edit tags BEFORE intent classification so the classifier
	// sees clean text, and the plan can be overridden below.
	hasProjectTag := containsEditTag(prompt, toraProjectToken)
	hasCanvasTag := containsEditTag(prompt, toraCanvasToken)
	if hasProjectTag {
		prompt = stripEditTag(prompt, toraProjectToken)
	}
	if hasCanvasTag {
		prompt = stripEditTag(prompt, toraCanvasToken)
	}

	// Classify intent before anything else — determines which data sources to
	// load and the token budget. Zero cost, <1ms.
	plan := classifyToraIntent(prompt)

	// @Project overrides the plan: force task list + sprint context + mutation
	// instructions so the AI produces an accept/reject action block.
	// maxTasks is set to a high value so ALL tasks are visible — partial context
	// causes incomplete deletes and duplicate creates on repeated runs.
	if hasProjectTag {
		plan.flags |= toraFlagTaskList | toraFlagSprints | toraFlagMutation
		plan.maxTasks = 500 // show every task so the AI has full context
		if plan.modelTier == ai.AIModelTierLight {
			plan.modelTier = ai.AIModelTierStandard
		}
		plan.reason += "+@project"
	}
	if hasCanvasTag {
		plan.reason += "+@canvas"
	}
	log.Printf("[ws] tora intent: plan=%s maxTasks=%d", plan.reason, plan.maxTasks)

	// Mutation requests may require multiple AI turns — use the longer timeout.
	requestTimeout := toraRequestTimeout
	if plan.has(toraFlagMutation) || hasCanvasTag {
		requestTimeout = toraRequestTimeoutMutation
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	rollingSummary := h.loadRoomRollingSummary(ctx, roomID)
	contextMessages := h.loadRecentMessagesFromRedis(ctx, roomID, toraContextMsgLimit())

	if !toraAgenticEnabled() {
		if err := h.runLegacyToraFlow(ctx, roomID, userMessage, prompt, plan, rollingSummary, contextMessages); err != nil {
			log.Printf("[ws] tora legacy flow failed: %v", err)
			h.broadcast <- newToraBotMessage(roomID, buildToraFailureResponse(err))
		}
		return
	}

	var err error
	switch {
	case plan.has(toraFlagMutation):
		err = h.runTaskBoardAgent(ctx, roomID, userMessage, prompt, plan, rollingSummary, contextMessages)
	case hasCanvasTag:
		err = h.runCanvasAgent(ctx, roomID, userMessage, prompt, rollingSummary, contextMessages)
	case plan.has(toraFlagChatOnly):
		err = h.runChatAgent(ctx, roomID, userMessage, prompt, rollingSummary, contextMessages)
	default:
		err = h.runChatAgent(ctx, roomID, userMessage, prompt, rollingSummary, contextMessages)
	}
	if err != nil {
		log.Printf("[ws] tora mention failed: %v", err)
		h.broadcast <- newToraBotMessage(roomID, buildToraFailureResponse(err))
	}
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

func newToraWorkflowMessage(
	roomID string,
	origin models.Message,
	workflowKind string,
	summary string,
	events []ai.AgentEvent,
	runErr error,
) models.Message {
	normalizedOriginID := normalizeMessageID(origin.ID)
	safeSummary := strings.TrimSpace(summary)
	status := "done"
	if runErr != nil {
		status = "failed"
		if safeSummary == "" {
			safeSummary = buildToraFailureResponse(runErr)
		}
	}

	payload, _ := json.Marshal(map[string]any{
		"originMessageId": normalizedOriginID,
		"workflowKind":    strings.TrimSpace(workflowKind),
		"status":          status,
		"summary":         safeSummary,
		"events":          buildToraAgentAuditTrail(events),
	})

	return models.Message{
		ID:               fmt.Sprintf("%s_tora_workflow_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:           roomID,
		SenderID:         toraBotSenderID,
		SenderName:       toraBotSenderName,
		Content:          string(payload),
		Type:             "tora_workflow",
		ReplyToMessageID: normalizedOriginID,
		ReplyToSnippet:   summarizeToraWorkflowPrompt(origin.Content),
		CreatedAt:        time.Now().UTC(),
	}
}

func summarizeToraWorkflowPrompt(content string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if len(trimmed) <= 160 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:157]) + "..."
}

// toraResponseIsRefusal returns true when the AI clearly stated it cannot or
// will not perform the requested operation. Used to decide whether to re-ask.
func toraResponseIsRefusal(response string) bool {
	lower := strings.ToLower(response)
	for _, pat := range []string{
		"cannot", "can't", "unable to", "not possible", "not able to",
		"don't have", "do not have", "insufficient", "no task ids",
		"no ids available", "couldn't find", "could not find",
		"no tasks found", "i don't see any", "i do not see any",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// toraResponseIsPlan returns true when the AI described what it will do but
// did not yet produce the <<<TORA_ACTIONS>>> block.
func toraResponseIsPlan(response string) bool {
	lower := strings.ToLower(response)
	for _, pat := range []string{
		"i'll ", "i will ", "i would ", "i'm going to ", "i am going to ",
		"here's my plan", "here is my plan", "here's what i'll",
		"my proposal", "my approach", "i propose",
		"i'll merge", "i'll combine", "i'll consolidate", "i'll create",
		"i'll delete", "i'll update", "let me ", "i suggest",
		"this would reduce", "this will reduce",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// callToraWithCompletion runs the AI call and, when a mutation was requested,
// automatically retries up to toraMutationMaxTurns times if the AI described
// its plan but forgot to emit the <<<TORA_ACTIONS>>> block.
//
// Each follow-up turn appends the previous AI response to the prompt and asks
// the AI to now produce the action block it described. The loop stops when:
//   - a valid action block is found
//   - the AI signals it cannot do the task (refusal)
//   - toraMutationMaxTurns is exhausted
func callToraWithCompletion(ctx context.Context, basePrompt string, plan toraLoadPlan) (string, error) {
	prompt := basePrompt
	var lastResponse string

	// Extract the current task count from the workspace context block so we can
	// verify the arithmetic in the action block matches the user's target.
	currentTaskCount := extractTotalTaskCount(basePrompt)
	targetTaskCount := extractTargetTaskCount(basePrompt)

	for turn := 0; turn < toraMutationMaxTurns; turn++ {
		response, err := ai.DefaultRouter.GenerateChatResponseWithHint(ctx, prompt, plan.modelTier)
		if err != nil {
			return "", err
		}
		response = strings.TrimSpace(response)
		if response == "" {
			return "", errors.New("empty ai response")
		}
		lastResponse = response

		// Non-mutation requests are always complete after the first turn.
		if !plan.has(toraFlagMutation) {
			return response, nil
		}

		// Mutation request: check for a valid action block.
		_, actionsJSON := parseToraMutationResponse(response)
		if actionsJSON != "" {
			// If the user specified a target count, verify the arithmetic
			// in the action block is correct before accepting the response.
			if targetTaskCount > 0 && currentTaskCount > 0 && turn < toraMutationMaxTurns-1 {
				if note := verifyActionCountArithmetic(actionsJSON, currentTaskCount, targetTaskCount); note != "" {
					// Arithmetic is wrong — ask the AI to fix the block.
					fixupNote := "\n\n=== ARITHMETIC VERIFICATION FAILED ===\n" + note +
						"\n\nPlease re-emit the <<<TORA_ACTIONS>>> block with the correct number of " +
						"task_create and task_delete entries so that the final count equals exactly " +
						fmt.Sprintf("%d tasks. Show your corrected count arithmetic first.", targetTaskCount)
					prompt = basePrompt + "\n\n=== YOUR PREVIOUS RESPONSE ===\n" + response + "\n=== END ===" + fixupNote
					continue
				}
			}
			return response, nil // arithmetic OK or no target specified
		}

		// AI said it can't — stop retrying.
		if toraResponseIsRefusal(response) {
			return response, nil
		}

		// Last turn — return whatever we have.
		if turn == toraMutationMaxTurns-1 {
			break
		}

		// AI seems to have described a plan without emitting the action block.
		followUpNote := "\n\n=== YOUR PREVIOUS RESPONSE ===\n" + response +
			"\n=== END ===\n\n" +
			"You described the changes above but did not output the <<<TORA_ACTIONS>>> block. " +
			"Please output it now — start directly with <<<TORA_ACTIONS>>> and list every " +
			"task_create and task_delete entry you described. Do not repeat the explanation."
		prompt = basePrompt + followUpNote
	}

	return lastResponse, nil
}

// extractTotalTaskCount reads "Total tasks (excluding support tickets): N" from
// the workspace context block embedded in the prompt, so support tickets are
// never included in arithmetic verification.
func extractTotalTaskCount(prompt string) int {
	// Try the new label first (excludes support tickets)
	const marker = "Total tasks (excluding support tickets): "
	idx := strings.Index(prompt, marker)
	if idx < 0 {
		// Fallback for older context format
		const fallback = "Total tasks: "
		idx = strings.Index(prompt, fallback)
		if idx < 0 {
			return 0
		}
		idx += len(fallback)
	} else {
		idx += len(marker)
	}
	rest := prompt[idx:]
	n := 0
	for _, ch := range rest {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int(ch-'0')
		} else {
			break
		}
	}
	return n
}

// extractTargetTaskCount looks for a target count in the user's query portion of
// the prompt. We scan for patterns like "to 17 tasks", "total tasks to 17",
// "make it 17", "reduce to 17", etc.
func extractTargetTaskCount(prompt string) int {
	// The user's query is at the very end of the prompt (after all context sections).
	// We look in the last 400 chars to avoid false matches in task titles.
	search := prompt
	if len(search) > 400 {
		search = search[len(search)-400:]
	}
	lower := strings.ToLower(search)
	// Common patterns: "to 17 tasks", "total.*17", "make.*17 tasks", "exactly 17"
	patterns := []string{
		"total tasks to ", "tasks to ", "to exactly ", "make.*to ", "reduce to ",
		"to ", "exactly ", "total of ", "total to ",
	}
	for _, pat := range patterns {
		idx := strings.LastIndex(lower, pat)
		if idx < 0 {
			continue
		}
		rest := lower[idx+len(pat):]
		n := 0
		found := false
		for _, ch := range rest {
			if ch >= '0' && ch <= '9' {
				n = n*10 + int(ch-'0')
				found = true
			} else if found {
				break
			}
		}
		if found && n > 0 && n < 10000 {
			return n
		}
	}
	return 0
}

func extractTargetSprintCount(prompt string) int {
	search := prompt
	if len(search) > 400 {
		search = search[len(search)-400:]
	}
	lower := strings.ToLower(search)
	idx := strings.LastIndex(lower, "sprint")
	if idx < 0 {
		return 0
	}
	windowStart := idx - 40
	if windowStart < 0 {
		windowStart = 0
	}
	window := lower[windowStart:idx]
	end := -1
	for i := len(window) - 1; i >= 0; i-- {
		if window[i] >= '0' && window[i] <= '9' {
			end = i
			break
		}
	}
	if end < 0 {
		return 0
	}
	start := end
	for start >= 0 && window[start] >= '0' && window[start] <= '9' {
		start--
	}
	value, err := strconv.Atoi(window[start+1 : end+1])
	if err != nil || value <= 0 || value >= 1000 {
		return 0
	}
	return value
}

// verifyActionCountArithmetic counts creates and deletes in the actions JSON and
// returns a non-empty correction note if current + creates - deletes ≠ target.
func verifyActionCountArithmetic(actionsJSON string, current, target int) string {
	var actions []map[string]any
	if err := json.Unmarshal([]byte(actionsJSON), &actions); err != nil {
		return "" // can't parse; leave it
	}
	creates, deletes := 0, 0
	for _, a := range actions {
		switch a["kind"] {
		case "task_create":
			creates++
		case "task_delete":
			deletes++
		}
	}
	result := current + creates - deletes
	if result == target {
		return "" // correct
	}
	return fmt.Sprintf(
		"Current tasks: %d. Your block has %d creates and %d deletes → %d + %d − %d = %d tasks. Target is %d tasks. Difference: %+d. Adjust your task_delete or task_create entries to fix this.",
		current, creates, deletes, current, creates, deletes, result, target, target-result,
	)
}

type toraTaskBoardValidationReport struct {
	Issues []string
}

func (r toraTaskBoardValidationReport) HasIssues() bool {
	return len(r.Issues) > 0
}

func (r toraTaskBoardValidationReport) Text() string {
	if len(r.Issues) == 0 {
		return ""
	}
	return strings.Join(r.Issues, "\n")
}

func validateToraTaskBoardMutation(prompt string, before, after *ai.WorkspaceContext, events []ai.AgentEvent) toraTaskBoardValidationReport {
	report := toraTaskBoardValidationReport{}
	if after == nil {
		report.Issues = append(report.Issues, "- Validation failed because the final board state could not be loaded.")
		return report
	}

	targetTaskCount := extractTargetTaskCount(prompt)
	if targetTaskCount > 0 && len(after.Tasks) != targetTaskCount {
		report.Issues = append(report.Issues, fmt.Sprintf("- Expected exactly %d tasks, but the board has %d.", targetTaskCount, len(after.Tasks)))
	}
	targetSprintCount := extractTargetSprintCount(prompt)
	if targetSprintCount > 0 && len(after.Sprints) != targetSprintCount {
		report.Issues = append(report.Issues, fmt.Sprintf("- Expected exactly %d sprints, but the board has %d.", targetSprintCount, len(after.Sprints)))
	}

	missingFields := make([]string, 0, 8)
	invalidDates := make([]string, 0, 8)
	duplicateTitles := make([]string, 0, 8)
	titleCounts := make(map[string]int)
	titleLabels := make(map[string]string)
	for _, task := range after.Tasks {
		missing := make([]string, 0, 4)
		if task.Budget == nil {
			missing = append(missing, "budget")
		}
		if task.StartDate == nil || task.StartDate.IsZero() {
			missing = append(missing, "start_date")
		}
		if task.DueDate == nil || task.DueDate.IsZero() {
			missing = append(missing, "due_date")
		}
		if len(task.Roles) == 0 {
			missing = append(missing, "roles")
		}
		if len(missing) > 0 {
			missingFields = append(missingFields, fmt.Sprintf("%s {%s}: %s", task.Title, task.ID, strings.Join(missing, ", ")))
		}
		if task.StartDate != nil && task.DueDate != nil && task.StartDate.After(*task.DueDate) {
			invalidDates = append(invalidDates, fmt.Sprintf("%s {%s}", task.Title, task.ID))
		}
		key := strings.ToLower(strings.TrimSpace(task.Title))
		if key == "" {
			key = strings.TrimSpace(task.ID)
		}
		titleCounts[key]++
		titleLabels[key] = strings.TrimSpace(task.Title)
	}
	for key, count := range titleCounts {
		if count <= 1 {
			continue
		}
		label := strings.TrimSpace(titleLabels[key])
		if label == "" {
			label = key
		}
		duplicateTitles = append(duplicateTitles, label)
	}
	sort.Strings(duplicateTitles)
	if len(missingFields) > 0 {
		report.Issues = append(report.Issues, "- Tasks still missing required fields: "+summarizeToraValidationItems(missingFields, 6))
	}
	if len(invalidDates) > 0 {
		report.Issues = append(report.Issues, "- Tasks with start_date after due_date: "+summarizeToraValidationItems(invalidDates, 6))
	}
	if len(duplicateTitles) > 0 {
		report.Issues = append(report.Issues, "- Duplicate task titles remain: "+summarizeToraValidationItems(duplicateTitles, 6))
	}

	if toraPromptImpliesRestructure(prompt) && before != nil {
		updatedIDs, deletedIDs := collectSuccessfulToraMutationIDs(events)
		silentlyRetained := make([]string, 0, 8)
		finalByID := make(map[string]ai.TaskCtx, len(after.Tasks))
		for _, task := range after.Tasks {
			finalByID[strings.TrimSpace(task.ID)] = task
		}
		for _, task := range before.Tasks {
			taskID := strings.TrimSpace(task.ID)
			if taskID == "" {
				continue
			}
			if _, stillExists := finalByID[taskID]; !stillExists {
				continue
			}
			if _, ok := updatedIDs[taskID]; ok {
				continue
			}
			if _, ok := deletedIDs[taskID]; ok {
				continue
			}
			silentlyRetained = append(silentlyRetained, fmt.Sprintf("%s {%s}", task.Title, taskID))
		}
		if len(silentlyRetained) > 0 {
			report.Issues = append(report.Issues, "- Existing tasks were silently retained without an explicit update: "+summarizeToraValidationItems(silentlyRetained, 6))
		}
	}

	return report
}

func summarizeToraValidationItems(items []string, limit int) string {
	if len(items) == 0 {
		return ""
	}
	if limit <= 0 || len(items) <= limit {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:limit], "; ") + fmt.Sprintf("; ...and %d more", len(items)-limit)
}

func toraPromptImpliesRestructure(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return false
	}
	if extractTargetTaskCount(prompt) > 0 || extractTargetSprintCount(prompt) > 0 {
		return true
	}
	keywords := []string{
		"split", "restructure", "redistribute", "rebalance", "make total tasks", "across ", "exactly ",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func collectSuccessfulToraMutationIDs(events []ai.AgentEvent) (map[string]struct{}, map[string]struct{}) {
	updated := make(map[string]struct{})
	deleted := make(map[string]struct{})
	for _, event := range events {
		if strings.TrimSpace(event.Kind) != "tool_result" || event.Result == nil {
			continue
		}
		if resultMap, ok := event.Result.(map[string]any); ok {
			if _, hasError := resultMap["error"]; hasError {
				continue
			}
		}
		switch strings.TrimSpace(event.Tool) {
		case "update_task":
			taskID := readToraMutationTaskID(event.Result, event.Input)
			if taskID != "" {
				updated[taskID] = struct{}{}
			}
		case "delete_task":
			taskID := readToraMutationTaskID(event.Result, event.Input)
			if taskID != "" {
				deleted[taskID] = struct{}{}
			}
		}
	}
	return updated, deleted
}

func readToraMutationTaskID(values ...any) string {
	for _, value := range values {
		record, ok := value.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"task_id", "taskId", "id", "ID"} {
			if raw, exists := record[key]; exists {
				if text := strings.TrimSpace(fmt.Sprint(raw)); text != "" && text != "<nil>" {
					return text
				}
			}
		}
	}
	return ""
}

func buildToraTaskBoardRepairPrompt(prompt string, report toraTaskBoardValidationReport) string {
	base := strings.TrimSpace(prompt)
	if !report.HasIssues() {
		return base
	}
	return strings.TrimSpace(base + "\n\nValidation report from the backend:\n" + report.Text() + "\n\nFix every listed issue now. Explicitly update or delete retained tasks instead of silently leaving them unchanged. Re-verify with list_tasks() and verify_task_count() before finishing.")
}

// toraMutationInstructions is appended to the prompt when the intent plan
// includes toraFlagMutation. It instructs the AI to produce a structured JSON
// action block that the frontend can present as an accept/reject card.
const toraMutationInstructions = `
TASK MUTATIONS — you may propose task changes when the user asks you to.
When proposing any task creation, update, or deletion you MUST output a structured
block at the very END of your response, after your explanation:

<<<TORA_ACTIONS>>>
[
  {
    "kind": "task_create",
    "title": "Task title",
    "description": "optional description",
    "status": "Todo",
    "sprint": "Sprint name (required — always include the sprint this task belongs to)",
    "task_type": "sprint",
    "budget": 2000,
    "start_date": "2024-03-01T00:00:00Z",
    "due_date": "2024-04-01T00:00:00Z",
    "roles": [
      { "role": "Backend Developer", "responsibilities": "Implement API endpoints and database schema" },
      { "role": "Frontend Developer", "responsibilities": "Build UI components and integrate API" }
    ]
  },
  {
    "kind": "task_update",
    "task_id": "exact-uuid-from-task-board-data",
    "task_title": "Current task title",
    "task_sprint": "Sprint the task currently belongs to",
    "task_parent": "Parent task title if this is a subtask, omit if top-level",
    "changes": {
      "title": "New title",
      "status": "In Progress",
      "sprint": "Sprint 2",
      "description": "New description",
      "budget": 1500,
      "due_date": "2024-05-01T00:00:00Z",
      "roles": [{ "role": "QA Engineer", "responsibilities": "Write and run test cases" }]
    },
    "change_details": {
      "title": { "from": "Current task title", "to": "New title" },
      "status": { "from": "Todo", "to": "In Progress" },
      "sprint": { "from": "Sprint 1", "to": "Sprint 2" }
    }
  },
  {
    "kind": "task_delete",
    "task_id": "exact-uuid-from-task-board-data",
    "task_title": "Task title",
    "task_sprint": "Sprint the task belongs to",
    "task_parent": "Parent task title if subtask, omit if top-level"
  }
]
<<<END_TORA_ACTIONS>>>

Rules:
- Only include actions you are confident about.
- For task_update / task_delete: task_id MUST be an exact ID from the {id:...} tags in the task board data above. Never invent IDs.
- Always populate task_sprint and task_parent (when applicable) so the user can identify which task is being changed without seeing internal IDs.
- For task_update: only include fields in "changes" that should actually change.
- For task_update: keep "changes" API-safe and include only the new values to apply.
- You MAY include "change_details" as optional display metadata for the UI. When the current value is visible in the task board data above, include {from, to} for each changed field. Never invent old values; if you cannot verify the previous value, omit that field from "change_details" or include only "to".
- For task_create: ALWAYS include budget (estimated cost in USD as a number), start_date, due_date, and roles. Estimate reasonable values based on task complexity, sprint timeline, and team structure. Roles must reflect which team functions are needed for the task and what each is responsible for.
- When editing or merging tasks (task_update), preserve and update budget/dates/roles in the "changes" object to reflect the merged scope.
- budget is a plain number (no currency symbol), dates are ISO 8601 strings (e.g. "2024-04-15T00:00:00Z"), roles is an array of {role, responsibilities} objects.

SUPPORT TICKETS vs TASKS — CRITICAL DISTINCTION:
- Support tickets (task_type="support") are a completely separate entity from tasks.
- They are NEVER counted in task totals. "Make 13 tasks" means 13 tasks PLUS however many support tickets already exist or are requested separately.
- When creating support tickets, use task_type="support" in the action block.
- COUNT ARITHMETIC must only count tasks (task_type="sprint"). Count support tickets separately if the user asks for them.
- Support ticket counts are listed separately in the task board data under "Support Tickets" — do not include them in your task arithmetic.

MERGE STRATEGY — when consolidating or merging tasks:
- Prefer UPDATE + DELETE over CREATE + DELETE. Pick one existing task to become the merged result, update all its fields (title, description, budget, dates, roles, sprint) via task_update, then delete all the others being merged away.
- Only use task_create for genuinely new tasks that have no suitable existing task to update into.
- This preserves task history and reduces API calls. Example: merging 5 tasks → 1 means 1 task_update (on the task you choose to keep) + 4 task_deletes (for the ones being merged away).

COUNT ARITHMETIC — CRITICAL:
Before emitting any action block that involves a target task count (e.g. "reduce to 12", "make it 31"), always calculate explicitly:
  current_count = number of TASKS (task_type=sprint/general) currently on the board — read from "Total tasks (excluding support tickets): N" in the board data above. NEVER include support tickets in this count.
  creates = number of task_create entries with task_type != "support"
  updates_that_add = 0 (updates don't change the count)
  deletes = number of task_delete entries for tasks (NOT support tickets)
  final_count = current_count + creates - deletes
Verify that final_count equals the requested target BEFORE emitting the block.
If final_count ≠ target, adjust your creates or deletes until it does.
State this calculation in your plain-text explanation so the user can verify it.
Example: "Currently 21 tasks (support tickets excluded). To reach 13: I will update 13 existing tasks (keeping them) and delete 8 → 21 + 0 − 8 = 13. ✓"
Support ticket arithmetic is separate: "User also wants 2 new support tickets → task_create ×2 with task_type=support."

IDEMPOTENCY — CRITICAL:
Before proposing a task_create, scan the full task board data for a task with the same title (case-insensitive). If one already exists, do NOT create a duplicate. Instead, prefer a task_update on the existing task. If extra duplicates exist, emit task_delete for those. Prefer UPDATE+DELETE over CREATE+DELETE whenever an existing task can serve as the merge target.

- Precede the block with a brief plain-text explanation including the count arithmetic above.
- If you cannot complete the request (e.g. task not found, or no IDs available), explain why in plain text and do NOT emit the block.
- NEVER partially emit a block. Either include all required actions or none.
`

// parseToraMutationResponse splits an AI response into a human-readable text
// part and the raw JSON actions string. Returns ("", "") for the actions if no
// valid action block is present.
func parseToraMutationResponse(response string) (text string, actionsJSON string) {
	const startMarker = "<<<TORA_ACTIONS>>>"
	const endMarker = "<<<END_TORA_ACTIONS>>>"

	startIdx := strings.Index(response, startMarker)
	if startIdx < 0 {
		return strings.TrimSpace(response), ""
	}
	endIdx := strings.Index(response, endMarker)
	if endIdx < 0 || endIdx <= startIdx {
		return strings.TrimSpace(response), ""
	}

	rawText := strings.TrimSpace(response[:startIdx])
	rawJSON := strings.TrimSpace(response[startIdx+len(startMarker) : endIdx])

	// Validate: must be a non-empty JSON array
	var parsed []any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil || len(parsed) == 0 {
		return strings.TrimSpace(response), ""
	}

	return rawText, rawJSON
}

func newToraBotActionMessage(roomID, text, actionsJSON string) models.Message {
	payload, _ := json.Marshal(map[string]any{
		"text":        text,
		"actionsJson": actionsJSON,
	})
	return models.Message{
		ID:         fmt.Sprintf("%s_tora_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:     roomID,
		SenderID:   toraBotSenderID,
		SenderName: toraBotSenderName,
		Content:    string(payload),
		Type:       "tora_action",
		CreatedAt:  time.Now().UTC(),
	}
}

func newToraBotAgentActionMessage(roomID, text string, events []ai.AgentEvent) models.Message {
	actionsJSON, err := ai.BuildActionsJSONFromAudit(events)
	if err != nil {
		log.Printf("[ws] tora action synthesis failed: %v", err)
		actionsJSON = "[]"
	}
	payload, _ := json.Marshal(map[string]any{
		"text":        strings.TrimSpace(text),
		"actionsJson": actionsJSON,
		"auditTrail":  buildToraAgentAuditTrail(events),
		"agentic":     true,
	})
	return models.Message{
		ID:         fmt.Sprintf("%s_tora_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:     roomID,
		SenderID:   toraBotSenderID,
		SenderName: toraBotSenderName,
		Content:    string(payload),
		Type:       "tora_action",
		CreatedAt:  time.Now().UTC(),
	}
}

func newToraBotCanvasActionMessage(roomID, text, changesJSON string, events []ai.AgentEvent) models.Message {
	payload, _ := json.Marshal(map[string]any{
		"text":         strings.TrimSpace(text),
		"changesJson":  changesJSON,
		"auditTrail":   buildToraAgentAuditTrail(events),
		"agentic":      true,
		"pendingApply": true,
	})
	return models.Message{
		ID:         fmt.Sprintf("%s_tora_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:     roomID,
		SenderID:   toraBotSenderID,
		SenderName: toraBotSenderName,
		Content:    string(payload),
		Type:       "tora_canvas_action",
		CreatedAt:  time.Now().UTC(),
	}
}

func toraAgenticEnabled() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("TORA_AGENTIC_ENABLED")))
	switch raw {
	case "", "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func (h *Hub) ensureToraContextBuilder() *ai.ContextBuilder {
	if h == nil {
		return nil
	}
	if h.contextBuilder != nil {
		return h.contextBuilder
	}
	if h.msgService == nil || h.msgService.Scylla == nil {
		return nil
	}
	h.contextBuilder = ai.NewContextBuilder(h.msgService.Scylla)
	return h.contextBuilder
}

func (h *Hub) ensureToraAgentEngineFactory() *ai.AgentEngineFactory {
	if h == nil {
		return nil
	}
	if h.agentEngine != nil {
		return h.agentEngine
	}
	ctxBuilder := h.ensureToraContextBuilder()
	if ctxBuilder == nil {
		return nil
	}
	h.agentEngine = ai.NewAgentEngineFactory(ctxBuilder, resolveToraAgentProvider)
	return h.agentEngine
}

func (h *Hub) runTaskBoardAgent(
	ctx context.Context,
	roomID string,
	userMessage models.Message,
	prompt string,
	plan toraLoadPlan,
	rollingSummary string,
	contextMessages []models.Message,
) error {
	finalText, auditEvents, err := h.runToraTaskBoardAgent(ctx, roomID, userMessage, prompt, plan)
	h.broadcast <- newToraWorkflowMessage(roomID, userMessage, "task_board", finalText, auditEvents, err)
	if err != nil {
		return err
	}

	h.broadcast <- newToraBotAgentActionMessage(roomID, finalText, auditEvents)
	go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
	return nil
}

func (h *Hub) runCanvasAgent(
	ctx context.Context,
	roomID string,
	userMessage models.Message,
	prompt string,
	rollingSummary string,
	contextMessages []models.Message,
) error {
	responseText, auditEvents, err := h.runToraCanvasAgent(ctx, roomID, userMessage, prompt)
	h.broadcast <- newToraWorkflowMessage(roomID, userMessage, "canvas", responseText, auditEvents, err)
	if err != nil {
		return err
	}

	responseText = strings.TrimSpace(responseText)
	changesJSON, synthErr := ai.BuildCanvasActionsJSONFromAudit(auditEvents)
	if synthErr != nil {
		log.Printf("[ws] tora canvas action synthesis failed: %v", synthErr)
		changesJSON = "[]"
	}
	if strings.TrimSpace(changesJSON) != "" && strings.TrimSpace(changesJSON) != "[]" {
		if responseText == "" {
			responseText = "I prepared canvas changes. Review them and apply when you're ready."
		}
		h.broadcast <- newToraBotCanvasActionMessage(roomID, responseText, changesJSON, auditEvents)
		go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
		return nil
	}

	if responseText == "" {
		return errors.New("empty ai response")
	}

	h.broadcast <- newToraBotMessage(roomID, responseText)
	go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
	return nil
}

func (h *Hub) runChatAgent(
	ctx context.Context,
	roomID string,
	userMessage models.Message,
	prompt string,
	rollingSummary string,
	contextMessages []models.Message,
) error {
	responseText, auditEvents, err := h.runToraChatAgent(ctx, roomID, userMessage, prompt)
	h.broadcast <- newToraWorkflowMessage(roomID, userMessage, "chat", responseText, auditEvents, err)
	if err != nil {
		return err
	}

	responseText = strings.TrimSpace(responseText)
	if responseText == "" {
		return errors.New("empty ai response")
	}

	h.broadcast <- newToraBotMessage(roomID, responseText)
	go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
	return nil
}

func (h *Hub) runReadOnlyQuery(
	ctx context.Context,
	roomID string,
	userMessage models.Message,
	prompt string,
	plan toraLoadPlan,
	rollingSummary string,
	contextMessages []models.Message,
) error {
	return h.runChatAgent(ctx, roomID, userMessage, prompt, rollingSummary, contextMessages)
}

func (h *Hub) runLegacyToraFlow(
	ctx context.Context,
	roomID string,
	_ models.Message,
	prompt string,
	plan toraLoadPlan,
	rollingSummary string,
	contextMessages []models.Message,
) error {
	workspaceCtx := h.fetchToraWorkspaceContext(ctx, roomID, plan)
	aiPrompt := buildToraPrompt(
		rollingSummary,
		contextMessages,
		workspaceCtx,
		prompt,
		plan.has(toraFlagMutation),
	)

	responseText, err := callToraWithCompletion(ctx, aiPrompt, plan)
	if err != nil {
		return err
	}

	responseText = strings.TrimSpace(responseText)
	if responseText == "" {
		return errors.New("empty ai response")
	}

	if plan.has(toraFlagMutation) {
		text, actionsJSON := parseToraMutationResponse(responseText)
		if strings.TrimSpace(actionsJSON) != "" {
			h.broadcast <- newToraBotActionMessage(roomID, text, actionsJSON)
			go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
			return nil
		}
	}

	h.broadcast <- newToraBotMessage(roomID, responseText)
	go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
	return nil
}

func (h *Hub) runToraTaskBoardAgent(
	ctx context.Context,
	roomID string,
	userMessage models.Message,
	prompt string,
	plan toraLoadPlan,
) (string, []ai.AgentEvent, error) {
	if h == nil || h.msgService == nil || h.msgService.Scylla == nil || h.msgService.Scylla.Session == nil {
		return "", nil, fmt.Errorf("task storage unavailable")
	}

	ctxBuilder := h.ensureToraContextBuilder()
	engineFactory := h.ensureToraAgentEngineFactory()
	if ctxBuilder == nil || engineFactory == nil {
		return "", nil, fmt.Errorf("task board ai is not configured")
	}

	buildOpts := ai.BuildOptions{
		IncludeCanvas: false,
		IncludeChat:   false,
		TaskLimit:     500,
	}

	workspace, err := ctxBuilder.Build(ctx, roomID, strings.TrimSpace(userMessage.SenderID), buildOpts)
	if err != nil {
		return "", nil, err
	}

	engine := engineFactory.New(roomID, ai.AgentAuthContext{
		UserID:   strings.TrimSpace(userMessage.SenderID),
		UserName: strings.TrimSpace(userMessage.SenderName),
	}, plan.modelTier)
	if engine == nil {
		return "", nil, fmt.Errorf("task board ai engine is unavailable")
	}
	engine.SetRoomBroadcaster(h)

	finalText, events, err := engine.Run(ctx, prompt, ai.AgentConfig{
		MaxTurns:        8,
		Timeout:         toraRequestTimeoutMutation,
		SystemPrompt:    toraTaskBoardSystemPrompt,
		ContextOptions:  buildOpts,
		Workspace:       workspace,
		InitialContext:  buildToraTaskBoardInitialContext(workspace, buildOpts),
		OriginMessageID: normalizeMessageID(userMessage.ID),
		WorkflowKind:    "task_board",
	})
	if err != nil {
		return "", events, err
	}

	if toraTaskBoardNeedsToolRetry(finalText, events) && ctx.Err() == nil {
		retryText, retryEvents, retryErr := engine.Run(ctx, buildToraTaskBoardToolEnforcementPrompt(prompt, finalText), ai.AgentConfig{
			MaxTurns:        4,
			Timeout:         toraRequestTimeoutMutation,
			SystemPrompt:    toraTaskBoardSystemPrompt,
			ContextOptions:  buildOpts,
			Workspace:       workspace,
			InitialContext:  buildToraTaskBoardInitialContext(workspace, buildOpts),
			OriginMessageID: normalizeMessageID(userMessage.ID),
			WorkflowKind:    "task_board",
		})
		events = append(events, retryEvents...)
		if retryErr != nil {
			log.Printf("[ws] tora task-board tool-enforcement retry failed: %v", retryErr)
		} else if strings.TrimSpace(retryText) != "" {
			finalText = strings.TrimSpace(retryText)
		}
	}

	if toraTaskBoardNeedsToolRetry(finalText, events) {
		return "", events, fmt.Errorf("task board ai did not execute task-board tools for this mutation request")
	}

	finalWorkspace, buildErr := ctxBuilder.Build(ctx, roomID, strings.TrimSpace(userMessage.SenderID), buildOpts)
	if buildErr != nil {
		log.Printf("[ws] tora task-board post-run verify failed: %v", buildErr)
	}
	validation := validateToraTaskBoardMutation(prompt, workspace, finalWorkspace, events)

	if validation.HasIssues() && ctx.Err() == nil {
		repairText, repairEvents, repairErr := engine.Run(ctx, buildToraTaskBoardRepairPrompt(prompt, validation), ai.AgentConfig{
			MaxTurns:        4,
			Timeout:         toraRequestTimeoutMutation,
			SystemPrompt:    toraTaskBoardSystemPrompt,
			ContextOptions:  buildOpts,
			Workspace:       finalWorkspace,
			InitialContext:  buildToraTaskBoardInitialContext(finalWorkspace, buildOpts),
			OriginMessageID: normalizeMessageID(userMessage.ID),
			WorkflowKind:    "task_board",
		})
		events = append(events, repairEvents...)
		if repairErr != nil {
			log.Printf("[ws] tora task-board repair pass failed: %v", repairErr)
		} else if strings.TrimSpace(repairText) != "" {
			finalText = strings.TrimSpace(repairText)
		}
		finalWorkspace, buildErr = ctxBuilder.Build(ctx, roomID, strings.TrimSpace(userMessage.SenderID), buildOpts)
		if buildErr != nil {
			log.Printf("[ws] tora task-board post-repair verify failed: %v", buildErr)
		}
		validation = validateToraTaskBoardMutation(prompt, workspace, finalWorkspace, events)
	}

	summary := formatToraTaskBoardSummary(finalText, events, finalWorkspace)
	if validation.HasIssues() {
		summary = strings.TrimSpace(summary + "\n\nValidation still found:\n" + validation.Text())
	}
	return summary, events, nil
}

func resolveToraAgentProvider(modelTier string) ai.Provider {
	if ai.DefaultRouter != nil && ai.DefaultRouter.SupportsToolUse() {
		return ai.DefaultRouter
	}
	return ai.NewPromptToolUseProvider(ai.DefaultRouter, modelTier)
}

func resolveToraTaskBoardProvider(modelTier string) ai.Provider {
	return resolveToraAgentProvider(modelTier)
}

func buildToraTaskBoardInitialContext(workspace *ai.WorkspaceContext, opts ai.BuildOptions) string {
	if workspace == nil {
		return ""
	}
	rendered := strings.TrimSpace(workspace.RenderForAI(opts))
	if rendered == "" {
		return ""
	}
	return "Current task board state loaded. Treat this as ground truth.\n\n" + rendered
}

func buildToraAgentAuditTrail(events []ai.AgentEvent) []map[string]any {
	if len(events) == 0 {
		return nil
	}

	trail := make([]map[string]any, 0, len(events))
	for index, event := range events {
		entry := map[string]any{
			"index": index + 1,
			"kind":  strings.TrimSpace(event.Kind),
		}
		if event.Turn > 0 {
			entry["turn"] = event.Turn
		}
		if event.TotalTurns > 0 {
			entry["totalTurns"] = event.TotalTurns
		}
		if event.Timestamp > 0 {
			entry["timestamp"] = event.Timestamp
		}
		if strings.TrimSpace(event.WorkflowKind) != "" {
			entry["workflowKind"] = strings.TrimSpace(event.WorkflowKind)
		}
		if strings.TrimSpace(event.Tool) != "" {
			entry["tool"] = strings.TrimSpace(event.Tool)
		}
		if len(event.Input) > 0 {
			entry["input"] = sanitizeToraAuditValue(event.Input)
		}
		if event.Result != nil {
			entry["result"] = sanitizeToraAuditValue(event.Result)
		}
		if strings.TrimSpace(event.Text) != "" {
			entry["text"] = sanitizeToraAuditString(strings.TrimSpace(event.Text), 400)
		}
		if strings.TrimSpace(event.Error) != "" {
			entry["error"] = sanitizeToraAuditString(strings.TrimSpace(event.Error), 280)
		}
		trail = append(trail, entry)
	}
	return trail
}

func sanitizeToraAuditValue(value any) any {
	switch typed := value.(type) {
	case string:
		return sanitizeToraAuditString(typed, 400)
	case []byte:
		return sanitizeToraAuditString(string(typed), 400)
	case map[string]any:
		sanitized := make(map[string]any, len(typed))
		for key, entry := range typed {
			sanitized[key] = sanitizeToraAuditEntry(key, entry)
		}
		return sanitized
	case []any:
		sanitized := make([]any, 0, len(typed))
		for _, entry := range typed {
			sanitized = append(sanitized, sanitizeToraAuditValue(entry))
		}
		return sanitized
	case ai.TaskCtx:
		return sanitizeToraAuditValue(map[string]any{
			"id":            typed.ID,
			"title":         typed.Title,
			"description":   typed.Description,
			"status":        typed.Status,
			"task_type":     typed.TaskType,
			"sprint_name":   typed.SprintName,
			"assignee_name": typed.AssigneeName,
			"budget":        typed.Budget,
			"start_date":    typed.StartDate,
			"due_date":      typed.DueDate,
			"roles":         typed.Roles,
			"subtasks":      typed.Subtasks,
			"updated_at":    typed.UpdatedAt,
		})
	case []ai.TaskCtx:
		sanitized := make([]any, 0, len(typed))
		for _, task := range typed {
			sanitized = append(sanitized, sanitizeToraAuditValue(task))
		}
		return sanitized
	case []ai.RoleCtx:
		sanitized := make([]any, 0, len(typed))
		for _, role := range typed {
			sanitized = append(sanitized, sanitizeToraAuditValue(role))
		}
		return sanitized
	case ai.RoleCtx:
		return map[string]any{
			"role":             sanitizeToraAuditString(typed.Role, 120),
			"responsibilities": sanitizeToraAuditString(typed.Responsibilities, 220),
		}
	case []ai.SubtaskCtx:
		sanitized := make([]any, 0, len(typed))
		for _, subtask := range typed {
			sanitized = append(sanitized, sanitizeToraAuditValue(subtask))
		}
		return sanitized
	case ai.SubtaskCtx:
		return map[string]any{
			"content":   sanitizeToraAuditString(typed.Content, 220),
			"completed": typed.Completed,
		}
	default:
		return typed
	}
}

func sanitizeToraAuditEntry(key string, value any) any {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	switch lowerKey {
	case "content":
		return sanitizeToraAuditString(fmt.Sprint(value), 220)
	case "excerpt", "description", "text":
		return sanitizeToraAuditString(fmt.Sprint(value), 320)
	default:
		return sanitizeToraAuditValue(value)
	}
}

func sanitizeToraAuditString(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	if maxLen <= 1 {
		return value[:maxLen]
	}
	return value[:maxLen-1] + "…"
}

func formatToraTaskBoardSummary(finalText string, events []ai.AgentEvent, workspace *ai.WorkspaceContext) string {
	finalText = strings.TrimSpace(finalText)
	created, updated, deleted := countToraTaskBoardMutations(events)
	_, writeCalls := countToraTaskBoardToolCalls(events)

	totalTasks := 0
	sprintCount := 0
	if workspace != nil {
		totalTasks = len(workspace.Tasks)
		sprintCount = len(workspace.Sprints)
	}

	computed := fmt.Sprintf(
		"Done. Created %d · Updated %d · Deleted %d. Board now has %d tasks across %d sprints.",
		created,
		updated,
		deleted,
		totalTasks,
		sprintCount,
	)
	if finalText == "" {
		return computed
	}
	if writeCalls == 0 {
		return finalText
	}

	lower := strings.ToLower(finalText)
	if strings.Contains(lower, "board now has") && strings.Contains(lower, "created") {
		return finalText
	}
	return strings.TrimSpace(finalText + "\n\n" + computed)
}

func countToraTaskBoardMutations(events []ai.AgentEvent) (created int, updated int, deleted int) {
	for _, event := range events {
		if strings.TrimSpace(event.Kind) != "tool_call" {
			continue
		}
		switch strings.TrimSpace(event.Tool) {
		case "create_task":
			created++
		case "update_task":
			updated++
		case "delete_task":
			deleted++
		}
	}
	return created, updated, deleted
}

func countToraTaskBoardToolCalls(events []ai.AgentEvent) (total int, writes int) {
	for _, event := range events {
		if strings.TrimSpace(event.Kind) != "tool_call" {
			continue
		}
		total++
		switch strings.TrimSpace(event.Tool) {
		case "create_task", "update_task", "delete_task":
			writes++
		}
	}
	return total, writes
}

func toraTaskBoardNeedsToolRetry(finalText string, events []ai.AgentEvent) bool {
	totalCalls, writeCalls := countToraTaskBoardToolCalls(events)
	if writeCalls > 0 {
		return false
	}
	if totalCalls == 0 {
		return true
	}
	return toraResponseIsRefusal(finalText)
}

func buildToraTaskBoardToolEnforcementPrompt(prompt string, finalText string) string {
	base := strings.TrimSpace(prompt)
	previous := strings.TrimSpace(finalText)
	var builder strings.Builder
	builder.WriteString(base)
	builder.WriteString("\n\nTool-use enforcement from the backend:\n")
	builder.WriteString("- Your previous answer did not perform the requested board mutation.\n")
	builder.WriteString("- You do have access to list_tasks, create_task, update_task, delete_task, list_sprints, and verify_task_count.\n")
	builder.WriteString("- This request is invalid unless you actually call the task-board tools.\n")
	builder.WriteString("- Start by calling list_tasks(). Then perform the necessary update_task/create_task/delete_task operations.\n")
	builder.WriteString("- Do not answer with \"I don't have the tools\" or any similar refusal.\n")
	builder.WriteString("- After the writes, call verify_task_count() and then give the final summary.\n")
	if previous != "" {
		builder.WriteString("\nPrevious invalid response:\n")
		builder.WriteString(previous)
	}
	return builder.String()
}

func (h *Hub) runToraChatAgent(
	ctx context.Context,
	roomID string,
	userMessage models.Message,
	prompt string,
) (string, []ai.AgentEvent, error) {
	if h == nil || h.msgService == nil || h.msgService.Scylla == nil || h.msgService.Scylla.Session == nil {
		return "", nil, fmt.Errorf("chat ai storage unavailable")
	}

	roomType := h.loadToraRoomType(ctx, roomID)
	privateRoom := isToraPrivateRoomType(roomType)
	intentProvider := resolveToraChatProvider(ai.AIModelTierLight)
	intent := resolveToraChatIntent(ctx, prompt, intentProvider)

	ctxBuilder := h.ensureToraContextBuilder()
	engineFactory := h.ensureToraAgentEngineFactory()
	if ctxBuilder == nil || engineFactory == nil {
		return "", nil, fmt.Errorf("chat ai is not configured")
	}

	buildOpts := toraChatBuildOptions(intent, privateRoom)
	workspace, err := ctxBuilder.Build(ctx, roomID, strings.TrimSpace(userMessage.SenderID), buildOpts)
	if err != nil {
		return "", nil, err
	}

	engine := engineFactory.New(roomID, ai.AgentAuthContext{
		UserID:   strings.TrimSpace(userMessage.SenderID),
		UserName: strings.TrimSpace(userMessage.SenderName),
	}, toraChatModelTier(intent))
	if engine == nil {
		return "", nil, fmt.Errorf("chat ai engine is unavailable")
	}
	engine.SetRoomBroadcaster(h)

	finalText, events, err := engine.Run(ctx, prompt, ai.AgentConfig{
		MaxTurns:        toraChatMaxTurns(intent),
		Timeout:         toraRequestTimeout,
		SystemPrompt:    buildToraChatSystemPrompt(privateRoom),
		ContextOptions:  buildOpts,
		Workspace:       workspace,
		InitialContext:  buildToraChatInitialContext(workspace, intent, privateRoom),
		AllowedTools:    toraChatAllowedTools(intent, privateRoom),
		OriginMessageID: normalizeMessageID(userMessage.ID),
		WorkflowKind:    "chat",
	})
	if err != nil {
		return "", events, err
	}
	return strings.TrimSpace(finalText), events, nil
}

func resolveToraChatProvider(modelTier string) ai.Provider {
	return resolveToraTaskBoardProvider(modelTier)
}

func (h *Hub) loadToraRoomType(ctx context.Context, roomID string) string {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" || h == nil || h.msgService == nil {
		return ""
	}

	if h.msgService.Scylla != nil && h.msgService.Scylla.Session != nil {
		query := fmt.Sprintf(`SELECT type FROM %s WHERE room_id = ? LIMIT 1`, h.msgService.Scylla.Table("rooms"))
		var roomType string
		err := h.msgService.Scylla.Session.Query(query, normalizedRoomID).WithContext(ctx).Scan(&roomType)
		if err == nil {
			return strings.TrimSpace(roomType)
		}
		if err != nil && err != gocql.ErrNotFound {
			log.Printf("[ws] tora room type lookup failed room=%s err=%v", normalizedRoomID, err)
		}
	}

	if h.msgService.Redis != nil && h.msgService.Redis.Client != nil {
		if roomType, err := h.msgService.Redis.Client.HGet(ctx, toraRoomRedisKey(normalizedRoomID), "type").Result(); err == nil {
			return strings.TrimSpace(roomType)
		}
	}

	return ""
}

func isToraPrivateRoomType(roomType string) bool {
	lower := strings.ToLower(strings.TrimSpace(roomType))
	return strings.Contains(lower, "private") || strings.Contains(lower, "direct") || lower == "dm"
}

func buildToraChatSystemPrompt(privateRoom bool) string {
	if !privateRoom {
		return toraChatSystemPrompt
	}
	return strings.TrimSpace(toraChatSystemPrompt + `

PRIVATE CHANNEL MODE:
- You are operating inside a private channel.
- Do not use task board data or canvas data in this mode.
- Base your answer only on this room's own recent message history.
- You may mention that this is a private channel when helpful.`)
}

func toraChatBuildOptions(intent toraChatIntent, privateRoom bool) ai.BuildOptions {
	opts := ai.BuildOptions{
		IncludeCanvas: false,
		IncludeChat:   true,
		TaskLimit:     120,
	}
	switch intent {
	case toraChatIntentSummary:
		opts.ChatMessageLimit = 50
		opts.TaskLimit = 40
	case toraChatIntentTasks:
		opts.ChatMessageLimit = 20
		opts.TaskLimit = 250
	case toraChatIntentCode:
		opts.ChatMessageLimit = 20
		opts.IncludeCanvas = !privateRoom
		opts.TaskLimit = 80
	default:
		opts.ChatMessageLimit = 20
		opts.TaskLimit = 60
	}
	if privateRoom {
		opts.IncludeCanvas = false
	}
	return opts
}

func toraChatModelTier(intent toraChatIntent) string {
	switch intent {
	case toraChatIntentTasks, toraChatIntentCode:
		return ai.AIModelTierStandard
	case toraChatIntentSummary:
		return ai.AIModelTierStandard
	default:
		return ai.AIModelTierLight
	}
}

func toraChatMaxTurns(intent toraChatIntent) int {
	switch intent {
	case toraChatIntentTasks:
		return 4
	case toraChatIntentCode:
		return 3
	default:
		return 2
	}
}

func toraChatAllowedTools(intent toraChatIntent, privateRoom bool) []string {
	if privateRoom {
		return []string{}
	}
	switch intent {
	case toraChatIntentTasks:
		return []string{"list_tasks", "list_sprints", "search_tasks"}
	case toraChatIntentCode:
		return []string{"search_tasks"}
	default:
		return []string{}
	}
}

func buildToraChatInitialContext(workspace *ai.WorkspaceContext, intent toraChatIntent, privateRoom bool) string {
	if workspace == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("CHAT ROOM CONTEXT\n")
	sb.WriteString(fmt.Sprintf("Room: %s\n", toraFirstNonEmpty(strings.TrimSpace(workspace.RoomName), strings.TrimSpace(workspace.RoomID))))
	if privateRoom {
		sb.WriteString("Mode: private channel\n")
	} else {
		sb.WriteString("Mode: shared room\n")
	}

	if privateRoom {
		if messages := renderToraChatMessages(workspace.RecentMessages); messages != "" {
			sb.WriteString("\nRecent messages:\n")
			sb.WriteString(messages)
		}
		return strings.TrimSpace(sb.String())
	}

	switch intent {
	case toraChatIntentTasks:
		if members := renderToraChatMembers(workspace.Members); members != "" {
			sb.WriteString("\nMembers:\n")
			sb.WriteString(members)
		}
		if taskBoard := renderToraChatTaskBoard(workspace); taskBoard != "" {
			sb.WriteString("\nTask board:\n")
			sb.WriteString(taskBoard)
		}
		if messages := renderToraChatMessages(workspace.RecentMessages); messages != "" {
			sb.WriteString("\nRecent messages:\n")
			sb.WriteString(messages)
		}
	case toraChatIntentCode:
		if members := renderToraChatMembers(workspace.Members); members != "" {
			sb.WriteString("\nMembers:\n")
			sb.WriteString(members)
		}
		if canvas := renderToraChatCanvas(workspace.CanvasFiles); canvas != "" {
			sb.WriteString("\nCanvas excerpts:\n")
			sb.WriteString(canvas)
		}
		if messages := renderToraChatMessages(workspace.RecentMessages); messages != "" {
			sb.WriteString("\nRecent messages:\n")
			sb.WriteString(messages)
		}
	case toraChatIntentSummary:
		if messages := renderToraChatMessages(workspace.RecentMessages); messages != "" {
			sb.WriteString("\nRecent messages:\n")
			sb.WriteString(messages)
		}
	default:
		if members := renderToraChatMembers(workspace.Members); members != "" {
			sb.WriteString("\nMembers:\n")
			sb.WriteString(members)
		}
		if messages := renderToraChatMessages(workspace.RecentMessages); messages != "" {
			sb.WriteString("\nRecent messages:\n")
			sb.WriteString(messages)
		}
	}

	return strings.TrimSpace(sb.String())
}

func renderToraChatMembers(members []ai.UserCtx) string {
	if len(members) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, member := range members {
		label := strings.TrimSpace(member.FullName)
		if label == "" {
			label = strings.TrimSpace(member.Username)
		}
		if label == "" {
			label = strings.TrimSpace(member.ID)
		}
		if label == "" {
			continue
		}
		if strings.TrimSpace(member.Username) != "" && !strings.EqualFold(label, strings.TrimSpace(member.Username)) {
			label += " (@" + strings.TrimSpace(member.Username) + ")"
		}
		if member.IsOwner {
			label += " [owner]"
		}
		sb.WriteString("  - ")
		sb.WriteString(label)
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderToraChatMessages(messages []ai.MessageCtx) string {
	if len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, message := range messages {
		label := strings.TrimSpace(message.SenderName)
		if label == "" {
			label = "Unknown"
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s: %s\n", message.Timestamp.UTC().Format("15:04"), label, content))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderToraChatTaskBoard(workspace *ai.WorkspaceContext) string {
	if workspace == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  Total tasks: %d\n", len(workspace.Tasks)))
	if len(workspace.SupportTickets) > 0 {
		sb.WriteString(fmt.Sprintf("  Support tickets: %d\n", len(workspace.SupportTickets)))
	}
	if len(workspace.Sprints) > 0 {
		sb.WriteString("  Sprints:\n")
		for _, sprint := range workspace.Sprints {
			sb.WriteString(fmt.Sprintf(
				"    - %s: %d tasks (todo=%d, in_progress=%d, done=%d)\n",
				toraFirstNonEmpty(strings.TrimSpace(sprint.Name), "(No Sprint)"),
				sprint.TaskCount,
				sprint.Todo,
				sprint.InProgress,
				sprint.Done,
			))
		}
	}
	if len(workspace.Tasks) > 0 {
		sb.WriteString("  Tasks:\n")
		for _, task := range workspace.Tasks {
			sb.WriteString("    - ")
			sb.WriteString(renderToraChatTaskLine(task))
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderToraChatTaskLine(task ai.TaskCtx) string {
	status := strings.ToUpper(strings.TrimSpace(task.Status))
	if status == "" {
		status = "TODO"
	}
	title := toraFirstNonEmpty(strings.TrimSpace(task.Title), "(untitled task)")
	line := fmt.Sprintf("[%s] %s", status, title)
	if strings.TrimSpace(task.SprintName) != "" {
		line += fmt.Sprintf(" sprint:%q", strings.TrimSpace(task.SprintName))
	}
	if task.Budget != nil {
		line += fmt.Sprintf(" budget:$%s", formatToraChatBudget(*task.Budget))
	}
	if task.DueDate != nil && !task.DueDate.IsZero() {
		line += " due:" + task.DueDate.UTC().Format("2006-01-02")
	}
	return line
}

func renderToraChatCanvas(files []ai.CanvasFileCtx) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, file := range files {
		sb.WriteString(fmt.Sprintf(
			"  - %s [%s] %d lines\n",
			strings.TrimSpace(file.Path),
			toraFirstNonEmpty(strings.TrimSpace(file.Language), "plaintext"),
			file.Lines,
		))
		if excerpt := strings.TrimSpace(file.Excerpt); excerpt != "" {
			for _, line := range strings.Split(excerpt, "\n") {
				sb.WriteString("      ")
				sb.WriteString(line)
				sb.WriteByte('\n')
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatToraChatBudget(value float64) string {
	text := fmt.Sprintf("%.2f", value)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	if text == "" {
		return "0"
	}
	return text
}

func toraFirstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func toraRoomRedisKey(roomID string) string {
	return "room:" + normalizeRoomID(roomID)
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

// containsEditTag checks for @project or @canvas (case-insensitive) in a prompt.
func containsEditTag(prompt, tag string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(prompt)), strings.ToLower(tag))
}

// stripEditTag removes all occurrences of tag (case-insensitive) from prompt.
func stripEditTag(prompt, tag string) string {
	lower := strings.ToLower(prompt)
	lowerTag := strings.ToLower(tag)
	var out strings.Builder
	for {
		idx := strings.Index(lower, lowerTag)
		if idx < 0 {
			out.WriteString(prompt)
			break
		}
		out.WriteString(prompt[:idx])
		prompt = prompt[idx+len(tag):]
		lower = lower[idx+len(tag):]
	}
	return strings.TrimSpace(out.String())
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
		taskType        string
		description     string
		sprint          string
		assigneeID      string
		statusActorName string
		dueDate         *time.Time
		startDate       *time.Time
		rolesRaw        *string
	}

	tasksQuery := fmt.Sprintf(
		`SELECT id, title, status, task_type, description, sprint_name, assignee_id, status_actor_name, due_date, start_date, roles FROM %s WHERE room_id = ?`,
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
		taskType        string
		description     string
		sprint          string
		assigneeUUIDPtr *gocql.UUID
		statusActorName string
		toraDueDate     *time.Time
		toraStartDate   *time.Time
		toraRolesRaw    *string
	)
	for tasksIter.Scan(&taskID, &title, &status, &taskType, &description, &sprint, &assigneeUUIDPtr, &statusActorName, &toraDueDate, &toraStartDate, &toraRolesRaw) {
		row := taskRow{
			id:              strings.TrimSpace(taskID.String()),
			title:           strings.TrimSpace(title),
			status:          strings.TrimSpace(status),
			taskType:        strings.ToLower(strings.TrimSpace(taskType)),
			description:     strings.TrimSpace(description),
			sprint:          strings.TrimSpace(sprint),
			statusActorName: strings.TrimSpace(statusActorName),
			dueDate:         toraDueDate,
			startDate:       toraStartDate,
			rolesRaw:        toraRolesRaw,
		}
		if assigneeUUIDPtr != nil {
			row.assigneeID = strings.TrimSpace(assigneeUUIDPtr.String())
			assigneeIDSet[row.assigneeID] = struct{}{}
		}
		all = append(all, row)
		if row.taskType != "support" {
			counts[row.status]++
			if row.sprint != "" {
				sprints[row.sprint] = append(sprints[row.sprint], row.title)
			}
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

	// Separate tasks from support tickets
	var regularTasks, supportTickets []taskRow
	for _, t := range all {
		if t.taskType == "support" {
			supportTickets = append(supportTickets, t)
		} else {
			regularTasks = append(regularTasks, t)
		}
	}

	var sb strings.Builder
	sb.WriteString("=== TASK BOARD DATA ===\n")
	sb.WriteString(fmt.Sprintf("Total tasks (excluding support tickets): %d\n", len(regularTasks)))
	sb.WriteString(fmt.Sprintf("Total support tickets (separate — NOT counted as tasks): %d\n", len(supportTickets)))

	// Status breakdown — only regular tasks
	if len(counts) > 0 {
		parts := make([]string, 0, len(counts))
		for s, n := range counts {
			parts = append(parts, fmt.Sprintf("%s=%d", s, n))
		}
		sb.WriteString("Task status breakdown: " + strings.Join(parts, ", ") + "\n")
	}

	// Sprint groupings — only regular tasks
	if plan.has(toraFlagSprints) && len(sprints) > 0 {
		sb.WriteString("Sprints/phases (tasks only):\n")
		for sprintName, tasks := range sprints {
			sb.WriteString(fmt.Sprintf("  Sprint \"%s\" (%d tasks): %s\n",
				sprintName, len(tasks), strings.Join(tasks, ", ")))
		}
	}

	// Per-task lines — richness scales with what the plan loaded
	sb.WriteString("Tasks:\n")
	for i, t := range regularTasks {
		if i >= cap {
			sb.WriteString(fmt.Sprintf("  ... and %d more tasks (token budget reached)\n", len(regularTasks)-i))
			break
		}
		// Always include the full task ID — the AI needs it to produce valid
		// task_update / task_delete action payloads.
		// Include sprint so the AI can distinguish duplicate-named tasks.
		var line string
		if t.sprint != "" {
			line = fmt.Sprintf("  - [%s] %s  {id:%s}  sprint:%q", t.status, t.title, t.id, t.sprint)
		} else {
			line = fmt.Sprintf("  - [%s] %s  {id:%s}  sprint:(none)", t.status, t.title, t.id)
		}

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

		// Dates — include when present so AI can reason about timeline
		if t.startDate != nil && !t.startDate.IsZero() {
			line += fmt.Sprintf(" | start:%s", t.startDate.UTC().Format("2006-01-02"))
		}
		if t.dueDate != nil && !t.dueDate.IsZero() {
			line += fmt.Sprintf(" | due:%s", t.dueDate.UTC().Format("2006-01-02"))
		}

		// Roles — list role names so AI knows who owns what
		if t.rolesRaw != nil && strings.TrimSpace(*t.rolesRaw) != "" {
			var roles []struct {
				Role string `json:"role"`
			}
			if json.Unmarshal([]byte(*t.rolesRaw), &roles) == nil && len(roles) > 0 {
				roleNames := make([]string, 0, len(roles))
				for _, r := range roles {
					if r.Role != "" {
						roleNames = append(roleNames, r.Role)
					}
				}
				if len(roleNames) > 0 {
					line += " | roles: " + strings.Join(roleNames, ", ")
				}
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

	// Support tickets — listed separately so AI never conflates them with tasks
	if len(supportTickets) > 0 {
		sb.WriteString("Support Tickets (SEPARATE from tasks — do NOT count these in task totals):\n")
		for _, t := range supportTickets {
			var line string
			if t.sprint != "" {
				line = fmt.Sprintf("  - [%s] %s  {id:%s}  sprint:%q  type:support", t.status, t.title, t.id, t.sprint)
			} else {
				line = fmt.Sprintf("  - [%s] %s  {id:%s}  sprint:(none)  type:support", t.status, t.title, t.id)
			}
			if t.dueDate != nil && !t.dueDate.IsZero() {
				line += fmt.Sprintf(" | due:%s", t.dueDate.UTC().Format("2006-01-02"))
			}
			sb.WriteString(line + "\n")
		}
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

func buildToraPrompt(rollingSummary string, contextMessages []models.Message, workspaceCtx string, prompt string, includeMutations bool) string {
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
	if includeMutations {
		parts = append(parts, strings.TrimSpace(toraMutationInstructions))
	}
	parts = append(parts, "--- USER MESSAGE ---\n"+strings.TrimSpace(prompt))

	return strings.Join(parts, "\n\n")
}
