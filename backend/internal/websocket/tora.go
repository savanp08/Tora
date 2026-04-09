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
	"github.com/savanp08/converse/internal/projectboard"
)

const (
	toraPrimaryMentionToken    = "@ToraAI"
	toraLegacyMentionToken     = "@Tora"
	toraProjectToken           = "@project" // matched case-insensitively
	toraCanvasToken            = "@canvas"  // matched case-insensitively
	toraBotSenderID            = "Tora-Bot"
	toraBotSenderName          = "Tora-Bot"
	toraRequestTimeout         = 25 * time.Second
	toraRequestTimeoutMutation = 5 * time.Minute // agentic loop: up to 40 turns with tool calls
	toraSummaryTimeout         = 20 * time.Second
	toraMutationMaxTurns       = 3
)

type toraRunHandle struct {
	cancel       context.CancelFunc
	ownerUserID  string
	workflowKind string
}

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

func buildToraTaskBoardSystemPrompt(cfg models.ProjectTypeConfig) string {
	roles := strings.Join(cfg.DefaultRoles, ", ")
	statuses := strings.Join(cfg.StatusOptions, " -> ")

	return fmt.Sprintf(`You are Tora, a capable AI assistant embedded in a collaborative %[1]s project.
You can answer any question — domain knowledge, concepts, code, strategy, general topics — and you also have tools to read and modify the %[2]s board when board changes are actually needed.
Being helpful conversationally is your primary mode. Board mutations are a secondary capability you use only when the user asks for them.

IDENTITY OF ENTITIES:
- %[2]s (task_type=sprint): the primary unit of project work.
- Support Tickets (task_type=support): separate from %[2]s. NEVER count support tickets in %[2]s totals.
- %[3]s: named groupings of %[2]s. group_id is the canonical reference, while sprint_name remains the fast read label on tasks.

WORKFLOW FOR MUTATIONS:
1. The current board state is pre-loaded in your initial context. Do NOT call list_tasks() before starting.
   Only call list_tasks() if you need a task_id for a %[4]s created in this same run.
2. Plan briefly (2-3 sentences) then immediately start executing tool calls.
3. For restructuring to N %[3]s: update EVERY %[4]s so only N distinct sprint_name values remain on the board.
   The visible board grouping comes from task sprint_name values, so every %[4]s must end up under one of the final N %[3]s.
4. Use list_groups() whenever you need the canonical %[5]s list or %[5]s IDs.
   If a %[5]s is removed from the final structure, call delete_group() for it after its %[2]s have been moved or deleted.
5. After mutations, call verify_task_count(). Do NOT re-call list_tasks() to verify.
6. Call verify_task_count() before finalising. If it returns a "staged" note, provide your summary immediately.
7. If verify_task_count() shows the board still has too many or too few %[3]s, continue mutating. Do not stop until the requested count matches.

PROJECT CREATION RULES:
- If the user asks to build, create, or set up a new project, create a coherent %[1]s plan from the current board state.
- Do NOT delete existing %[2]s just because they are present. Delete them only if the user explicitly asks for removal or they are clearly unrelated starter/template work that must be replaced.
- If you replace unrelated starter/template work, do it once, then finish cleanup and verification. Do NOT create a second parallel plan on later turns.
- On retry or follow-up turns, prefer verification, cleanup, and targeted fixes over repeating large create/update/delete batches.

DELETION RULES:
- DELETE a %[4]s when: the user explicitly asks to remove it, it is a duplicate, or it is obsolete and has no value in the new structure.
- REASSIGN a %[4]s to another %[5]s when: the user wants to consolidate groups but the work is still relevant.
- DELETE a %[5]s when: the user says "remove %[5]s X", "we no longer need %[5]s X", or when restructuring to N %[3]s means the old %[5]s has no place in the new structure.
  Use delete_group(action="reassign") if the %[2]s have value, delete_group(action="delete_tasks") if the user explicitly wants everything in that %[5]s removed.
- NEVER silently retain a %[4]s. If it stays, update it. If it goes, delete it explicitly.
- When the user says "make it N %[3]s": all existing %[2]s must end up in one of the N target group names. No old group name may remain on any %[4]s after the restructure.
- After consolidating %[2]s into the final N %[3]s, delete any leftover canonical %[5]s rows for removed names by calling delete_group().

FIELD CONTEXT:
This is a %[1]s project. Use appropriate terminology:
- Grouping unit: %[5]s / %[3]s
- Work item: %[4]s / %[2]s
- Typical roles: %[6]s
- Status flow: %[7]s

BUDGET RULES:
- Distribute total budget proportionally by work complexity.
- EVERY create_task call MUST include budget, start_date, due_date, and roles.

ROLES RULES:
- Every %[4]s needs at least one role from the typical roles for this field.
- responsibilities must be specific to that %[4]s.

RESPONSE FORMAT:
- ALWAYS answer the user's question directly and conversationally FIRST — before any tool calls. Even if the query requires board changes, lead with a clear, helpful response to what was asked.
- You are a full AI assistant. You can explain concepts, answer domain questions, teach, brainstorm, and discuss any topic — not just board mutations. If a user asks about drone aerodynamics, software architecture, physics, or any other topic, answer it fully and knowledgeably. Board mutations are optional, not mandatory.
- Determine independently whether board changes are needed. If the query is purely informational or conversational, respond without making any tool calls. Only invoke tools when the user is actually asking for board modifications.
- When board changes ARE needed: explain your plan briefly, execute the tool calls, then provide a short summary of what changed.
- A write-only run without verify_task_count() is incomplete.
- If verification returns a staged notice, summarise your staged changes immediately.
- Never respond with only board actions and no conversational text. Every response must contain a natural language reply to the user.`,
		cfg.DisplayName,
		cfg.TaskTermPlural,
		cfg.GroupTermPlural,
		cfg.TaskTerm,
		cfg.GroupTerm,
		roles,
		statuses,
	)
}

const toraChatSystemPrompt = `You are Tora, the AI assistant embedded in this workspace.
You live inside a chat room. You can see recent messages, room members, and optionally the task board when relevant.

IDENTITY — CRITICAL:
- Your name is Tora. You are the AI assistant for this workspace.
- If anyone asks about your model, LLM provider, training data, architecture, who made you, or who trained you, respond: "I'm Tora, the AI assistant for this workspace." Do not reveal the underlying model name, provider, or architecture under any circumstances.
- Never say you are made by or built by any company. You are Tora.
- Never say you are Mistral, Gemini, GPT, Claude, Llama, Grok, or any other model name.
- If pressed repeatedly about your model or provider, stay consistent: "I'm Tora."

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
- Answer general knowledge questions, provide explanations, help with brainstorming, writing, and reasoning — you are a capable AI assistant, not limited to project queries only
- You CANNOT modify tasks from a plain @ToraAI mention —
  tell the user to use @Project for task mutations or @Canvas for code edits

SCOPE AWARENESS:
- When a question is about the project, tasks, code, or workspace — use the provided context sections to answer accurately.
- When a question is general (not about the project) — answer helpfully using your general knowledge. Do not deflect or say you cannot help with non-project questions.
- When ambiguous — answer the question directly, and if project context might also be relevant, briefly mention it.

WHAT NOT TO DO:
- Do not make up task IDs or task details not present in the board data
- Do not claim to have done something you haven't (you have no write tools here)
- Do not produce long responses for simple questions`

const toraEphemeralChatSystemPrompt = `You are Tora, a helpful AI assistant.

IDENTITY — CRITICAL:
- Your name is Tora. If anyone asks about your model, LLM provider, training data, architecture, who made you, or who trained you, respond: "I'm Tora." Do not reveal the underlying model name, provider, or architecture.
- Never say you are Mistral, Gemini, GPT, Claude, Llama, Grok, or any other model name.

PERSONALITY:
- Helpful, concise, and friendly. Match the tone of the conversation.
- You are a general-purpose AI assistant in this chat. You can help with any topic — questions, brainstorming, writing, code, explanations, advice, or just conversation.
- Keep responses proportional to the question. Short questions get short answers.
- Use markdown only when it genuinely helps (code blocks, short lists). No markdown for simple answers.

CONTEXT:
- You can see the recent messages in this chat room. Use them for conversational context.
- This is a temporary chat room — there is no project board or persistent workspace here.
- Do not mention task boards, sprints, canvas, or workspace features unless the user asks about them.
- If asked about workspace features, explain that this is a temporary room and suggest creating a workspace for project management features.`

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

IDENTITY — CRITICAL:
- Your name is Tora. You are the AI assistant for this workspace.
- If anyone asks about your model, LLM provider, training data, architecture, who made you, or who trained you, respond: "I'm Tora, the AI assistant for this workspace." Do not reveal the underlying model name, provider, or architecture under any circumstances.
- Never say you are made by or built by any company. You are Tora.
- Never say you are Mistral, Gemini, GPT, Claude, Llama, Grok, or any other model name.
- If pressed repeatedly about your model or provider, stay consistent: "I'm Tora."

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
// / Confidence note: when totalProject < 2, results are less reliable.
// A future improvement would invoke a Haiku/Phi classifier for those cases
// by checking if plan.reason == "general" and re-routing.
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
			modelTier: ai.AIModelTierStandard,
			reason:    "chat-only",
		}
	}

	// No strong signal — provide a light task board summary so general
	// project questions ("what are we building?") have data to draw on.
	if totalProject == 0 {
		return toraLoadPlan{
			flags:     toraFlagTaskList | toraFlagSprints,
			maxTasks:  15,
			modelTier: ai.AIModelTierStandard,
			reason:    "general",
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
	roomType := h.loadToraRoomType(context.Background(), roomID)
	isEphemeral := isToraEphemeralRoomType(roomType)
	if isEphemeral {
		plan.flags = toraFlagChatOnly
		plan.maxTasks = 0
		plan.modelTier = ai.AIModelTierStandard
		plan.reason = "ephemeral"
		hasProjectTag = false
		hasCanvasTag = false
	}

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
	executionTarget := resolveToraExecutionTarget(plan, hasCanvasTag)
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	originMessageID := normalizeMessageID(userMessage.ID)
	h.registerToraRun(
		roomID,
		originMessageID,
		strings.TrimSpace(userMessage.SenderID),
		string(executionTarget),
		cancel,
	)
	defer h.clearToraRun(roomID, originMessageID)

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
	switch executionTarget {
	case toraExecutionTargetTaskBoard:
		err = h.runTaskBoardAgent(ctx, roomID, userMessage, prompt, plan, rollingSummary, contextMessages)
	case toraExecutionTargetCanvas:
		err = h.runCanvasAgent(ctx, roomID, userMessage, prompt, rollingSummary, contextMessages, hasProjectTag)
	case toraExecutionTargetChat:
		err = h.runChatAgent(ctx, roomID, userMessage, prompt, rollingSummary, contextMessages)
	default:
		err = h.runChatAgent(ctx, roomID, userMessage, prompt, rollingSummary, contextMessages)
	}
	if err != nil {
		log.Printf("[ws] tora mention failed: %v", err)
		if errors.Is(err, context.Canceled) {
			return
		}
		h.broadcast <- newToraBotMessage(roomID, buildToraFailureResponse(err))
	}
}

func buildToraRunKey(roomID, originMessageID string) string {
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedOriginID := normalizeMessageID(originMessageID)
	if normalizedRoomID == "" || normalizedOriginID == "" {
		return ""
	}
	return normalizedRoomID + "|" + normalizedOriginID
}

func (h *Hub) registerToraRun(
	roomID string,
	originMessageID string,
	ownerUserID string,
	workflowKind string,
	cancel context.CancelFunc,
) {
	if h == nil || cancel == nil {
		return
	}
	key := buildToraRunKey(roomID, originMessageID)
	if key == "" {
		return
	}
	h.toraRunMu.Lock()
	h.toraRuns[key] = toraRunHandle{
		cancel:       cancel,
		ownerUserID:  strings.TrimSpace(ownerUserID),
		workflowKind: strings.TrimSpace(workflowKind),
	}
	h.toraRunMu.Unlock()
}

func (h *Hub) clearToraRun(roomID string, originMessageID string) {
	if h == nil {
		return
	}
	key := buildToraRunKey(roomID, originMessageID)
	if key == "" {
		return
	}
	h.toraRunMu.Lock()
	delete(h.toraRuns, key)
	h.toraRunMu.Unlock()
}

func (h *Hub) cancelToraRun(roomID string, originMessageID string, requesterUserID string) bool {
	if h == nil {
		return false
	}
	key := buildToraRunKey(roomID, originMessageID)
	if key == "" {
		return false
	}
	normalizedRequesterID := strings.TrimSpace(requesterUserID)

	h.toraRunMu.Lock()
	handle, ok := h.toraRuns[key]
	if !ok {
		h.toraRunMu.Unlock()
		return false
	}
	if normalizedRequesterID != "" && handle.ownerUserID != "" && normalizedRequesterID != handle.ownerUserID {
		h.toraRunMu.Unlock()
		return false
	}
	delete(h.toraRuns, key)
	h.toraRunMu.Unlock()

	h.BroadcastToRoom(normalizeRoomID(roomID), map[string]interface{}{
		"type":            "tora_agent_event",
		"kind":            "text",
		"text":            "Stop requested. Wrapping up the current AI run.",
		"originMessageId": normalizeMessageID(originMessageID),
		"workflowKind":    strings.TrimSpace(handle.workflowKind),
		"timestamp":       time.Now().UTC().UnixMilli(),
	})
	handle.cancel()
	return true
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
// Only matches when a number is directly adjacent to the word "tasks" or "task",
// preventing false positives from incidental "to N" patterns in the text.
func extractTargetTaskCount(prompt string) int {
	search := prompt
	if len(search) > 400 {
		search = search[len(search)-400:]
	}
	lower := strings.ToLower(search)

	// Find "N tasks" or "N task" patterns
	fields := strings.Fields(lower)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i+1] == "tasks" || fields[i+1] == "task" {
			n := parseLeadingInt(fields[i])
			if n > 0 && n < 10000 {
				return n
			}
		}
	}

	// Fallback: "tasks to N" pattern
	for i := 0; i < len(fields)-1; i++ {
		if (fields[i] == "tasks" || fields[i] == "task") && i+2 < len(fields) && fields[i+1] == "to" {
			n := parseLeadingInt(fields[i+2])
			if n > 0 && n < 10000 {
				return n
			}
		}
	}

	return 0
}

func parseLeadingInt(s string) int {
	n := 0
	found := false
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int(ch-'0')
			found = true
		} else if found {
			break
		}
	}
	if !found {
		return 0
	}
	return n
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

	touchedIDs := collectTouchedTaskIDs(events)
	missingFields := make([]string, 0, 8)
	invalidDates := make([]string, 0, 8)
	duplicateTitles := make([]string, 0, 8)
	titleCounts := make(map[string]int)
	titleLabels := make(map[string]string)
	for _, task := range after.Tasks {
		// Only validate required fields on tasks the AI created or updated in this run.
		// Pre-existing untouched tasks are not the AI's responsibility.
		if len(touchedIDs) > 0 {
			if _, wasTouched := touchedIDs[strings.TrimSpace(task.ID)]; !wasTouched {
				// Still track titles/dates for duplicate and date-order checks
				key := strings.ToLower(strings.TrimSpace(task.Title))
				if key == "" {
					key = strings.TrimSpace(task.ID)
				}
				titleCounts[key]++
				titleLabels[key] = strings.TrimSpace(task.Title)
				if task.StartDate != nil && task.DueDate != nil && task.StartDate.After(*task.DueDate) {
					invalidDates = append(invalidDates, fmt.Sprintf("%s {%s}", task.Title, task.ID))
				}
				continue
			}
		}
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

	// Explicit restructure signals
	restructureKeywords := []string{
		"restructure", "reorganize", "redistribute", "rebalance",
		"merge", "consolidate", "reduce to", "split into",
		"make total tasks", "change to", "convert to",
	}
	for _, keyword := range restructureKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}

	// A target task count alone implies restructure only if not combined with creation language
	if extractTargetTaskCount(prompt) > 0 {
		creationKeywords := []string{"create", "build", "make a", "set up", "start a", "new project", "generate"}
		for _, keyword := range creationKeywords {
			if strings.Contains(lower, keyword) {
				return false // creation with a count, not a restructure
			}
		}
		return true
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

// collectTouchedTaskIDs returns the set of task IDs that were created or updated
// in the current agent run, based on successful tool_result events.
func collectTouchedTaskIDs(events []ai.AgentEvent) map[string]struct{} {
	touched := make(map[string]struct{})
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
		case "create_task", "update_task":
			taskID := readToraMutationTaskID(event.Result, event.Input)
			if taskID != "" {
				touched[taskID] = struct{}{}
			}
		}
	}
	return touched
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
    "task_number": 7,
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
    "task_number": 7,
    "task_title": "Task title",
    "task_sprint": "Sprint the task belongs to",
    "task_parent": "Parent task title if subtask, omit if top-level"
  }
]
<<<END_TORA_ACTIONS>>>

Rules:
- Only include actions you are confident about.
- For task_update / task_delete: task_id MUST be an exact ID from the {id:...} tags in the task board data above. Never invent IDs. Also include task_number from the #N tag next to the task (e.g. if you see {id:abc123}  #7, include "task_number": 7). task_number is used as a reliable fallback identifier if the UUID is unavailable.
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

func newToraBotAgentActionMessage(
	roomID string,
	origin models.Message,
	text string,
	pendingActionsJSON string,
	events []ai.AgentEvent,
) models.Message {
	normalizedOriginID := normalizeMessageID(origin.ID)
	payload, _ := json.Marshal(map[string]any{
		"text":            strings.TrimSpace(text),
		"actionsJson":     pendingActionsJSON,
		"auditTrail":      buildToraAgentAuditTrail(events),
		"agentic":         true,
		"originMessageId": normalizedOriginID,
	})
	return models.Message{
		ID:               fmt.Sprintf("%s_tora_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:           roomID,
		SenderID:         toraBotSenderID,
		SenderName:       toraBotSenderName,
		Content:          string(payload),
		Type:             "tora_action",
		ReplyToMessageID: normalizedOriginID,
		ReplyToSnippet:   summarizeToraWorkflowPrompt(origin.Content),
		CreatedAt:        time.Now().UTC(),
	}
}

// toraDryRunExecutor intercepts task-board write tools so the agent can plan
// changes without writing to the database.  Read tools execute normally so the
// agent sees accurate workspace data and verify_task_count returns a realistic
// simulated count.  The caller builds the pending action list from stagedWrites
// after the agent run completes.
type toraDryRunExecutor struct {
	real           func(ctx context.Context, name string, input map[string]any) (any, error)
	baseWorkspace  *ai.WorkspaceContext
	stagedWrites   []toraStagedWrite
	groupNamesByID map[string]string
}

type toraStagedWrite struct {
	tool  string
	input map[string]any
}

func newToraDryRunExecutor(real func(ctx context.Context, name string, input map[string]any) (any, error), workspace *ai.WorkspaceContext) *toraDryRunExecutor {
	return &toraDryRunExecutor{
		real:           real,
		baseWorkspace:  cloneToraWorkspaceContext(workspace),
		groupNamesByID: make(map[string]string),
	}
}

func (d *toraDryRunExecutor) execute(ctx context.Context, name string, input map[string]any) (any, error) {
	switch name {
	case "create_task":
		stagedInput := cloneToraMap(input)
		d.stagedWrites = append(d.stagedWrites, toraStagedWrite{tool: "create_task", input: stagedInput})
		title, _ := stagedInput["title"].(string)
		sprint, _ := stagedInput["sprint_name"].(string)
		return map[string]any{
			"ok":          true,
			"task_id":     fmt.Sprintf("preview-%d", len(d.stagedWrites)),
			"title":       strings.TrimSpace(title),
			"sprint_name": strings.TrimSpace(sprint),
			"preview":     true,
		}, nil
	case "update_task":
		d.stagedWrites = append(d.stagedWrites, toraStagedWrite{tool: "update_task", input: cloneToraMap(input)})
		taskID, _ := input["task_id"].(string)
		return map[string]any{"ok": true, "task_id": strings.TrimSpace(taskID)}, nil
	case "delete_task":
		d.stagedWrites = append(d.stagedWrites, toraStagedWrite{tool: "delete_task", input: cloneToraMap(input)})
		return map[string]any{"ok": true}, nil
	case "delete_group":
		stagedInput := cloneToraMap(input)
		d.stagedWrites = append(d.stagedWrites, toraStagedWrite{tool: "delete_group", input: stagedInput})
		if groupID := strings.TrimSpace(fmt.Sprint(stagedInput["group_id"])); groupID != "" {
			d.groupNamesByID[groupID] = strings.TrimSpace(fmt.Sprint(stagedInput["group_name"]))
		}
		if targetID := strings.TrimSpace(fmt.Sprint(stagedInput["reassign_to_group_id"])); targetID != "" {
			_, _ = d.resolveGroupNameByID(ctx, targetID)
		}
		return map[string]any{"ok": true, "staged": true}, nil
	case "list_tasks":
		return d.listTasks(ctx, input)
	case "list_groups":
		result, err := d.real(ctx, name, input)
		if err != nil {
			return result, err
		}
		d.cacheGroupNames(result)
		return d.listGroups(ctx, result)
	case "verify_task_count":
		return d.verifyTaskCount(ctx)
	default:
		return d.real(ctx, name, input)
	}
}

func (d *toraDryRunExecutor) listTasks(ctx context.Context, input map[string]any) (any, error) {
	workspace, err := d.simulatedWorkspace(ctx)
	if err != nil {
		return nil, err
	}

	taskType := toraDryRunNormalizeTaskType(fmt.Sprint(input["task_type"]))
	statusFilter := toraDryRunNormalizeStatus(fmt.Sprint(input["status"]))
	sprintFilter := toraDryRunGroupKey(fmt.Sprint(input["sprint_name"]))

	var candidates []ai.TaskCtx
	switch taskType {
	case "support":
		candidates = append(candidates, workspace.SupportTickets...)
	case "all":
		candidates = append(candidates, workspace.Tasks...)
		candidates = append(candidates, workspace.SupportTickets...)
	default:
		candidates = append(candidates, workspace.Tasks...)
	}

	filtered := make([]ai.TaskCtx, 0, len(candidates))
	for _, task := range candidates {
		if sprintFilter != "" && toraDryRunGroupKey(task.SprintName) != sprintFilter {
			continue
		}
		if statusFilter != "" && toraDryRunNormalizeStatus(task.Status) != statusFilter {
			continue
		}
		filtered = append(filtered, task)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return toraDryRunTaskLess(filtered[i], filtered[j])
	})
	return filtered, nil
}

func (d *toraDryRunExecutor) listGroups(ctx context.Context, result any) (any, error) {
	summaries, ok := toraDryRunGroupSummaries(result)
	if !ok {
		return result, nil
	}

	workspace, err := d.simulatedWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, task := range workspace.Tasks {
		counts[toraDryRunGroupKey(task.SprintName)]++
	}

	deletedGroups := make(map[string]struct{})
	for _, staged := range d.stagedWrites {
		if staged.tool != "delete_group" {
			continue
		}
		key := toraDryRunGroupKey(fmt.Sprint(staged.input["group_name"]))
		if key != "" {
			deletedGroups[key] = struct{}{}
		}
	}

	filtered := make([]projectboard.GroupSummary, 0, len(summaries))
	for _, summary := range summaries {
		key := toraDryRunGroupKey(summary.Name)
		if _, deleted := deletedGroups[key]; deleted {
			continue
		}
		summary.TaskCount = counts[key]
		filtered = append(filtered, summary)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].DisplayOrder != filtered[j].DisplayOrder {
			return filtered[i].DisplayOrder < filtered[j].DisplayOrder
		}
		return toraDryRunCompareFold(filtered[i].Name, filtered[j].Name) < 0
	})
	return filtered, nil
}

func (d *toraDryRunExecutor) verifyTaskCount(ctx context.Context) (any, error) {
	workspace, err := d.simulatedWorkspace(ctx)
	if err != nil {
		return nil, err
	}

	bySprint := make(map[string]int)
	byStatus := make(map[string]int)
	for _, task := range workspace.Tasks {
		bySprint[task.SprintName]++
		byStatus[toraDryRunNormalizeStatus(task.Status)]++
	}

	verification := map[string]any{
		"ok":              true,
		"total_tasks":     len(workspace.Tasks),
		"support_tickets": len(workspace.SupportTickets),
		"sprint_count":    len(bySprint),
		"group_count":     len(bySprint),
		"by_sprint":       bySprint,
		"by_status":       byStatus,
	}
	if len(d.stagedWrites) > 0 {
		creates := 0
		updates := 0
		deletes := 0
		for _, staged := range d.stagedWrites {
			switch staged.tool {
			case "create_task":
				creates++
			case "update_task":
				updates++
			case "delete_task", "delete_group":
				deletes++
			}
		}
		verification["note"] = fmt.Sprintf("Counts reflect staged changes awaiting user confirmation (%d creates, %d updates, %d deletes).", creates, updates, deletes)
	}
	return verification, nil
}

func (d *toraDryRunExecutor) simulatedWorkspace(ctx context.Context) (*ai.WorkspaceContext, error) {
	workspace := cloneToraWorkspaceContext(d.baseWorkspace)
	if workspace == nil {
		workspace = &ai.WorkspaceContext{}
	}

	for index, staged := range d.stagedWrites {
		switch staged.tool {
		case "create_task":
			task := toraDryRunTaskFromInput(staged.input, fmt.Sprintf("preview-%d", index+1))
			if task.TaskType == "support" {
				workspace.SupportTickets = append(workspace.SupportTickets, task)
			} else {
				workspace.Tasks = append(workspace.Tasks, task)
			}
		case "update_task":
			toraDryRunApplyTaskUpdate(workspace, staged.input)
		case "delete_task":
			toraDryRunDeleteTask(workspace, strings.TrimSpace(fmt.Sprint(staged.input["task_id"])))
		case "delete_group":
			if err := d.applyGroupDelete(ctx, workspace, staged.input); err != nil {
				return nil, err
			}
		}
	}

	workspace.Sprints = toraDryRunDeriveSprintContexts(workspace.Tasks)
	toraDryRunSortWorkspace(workspace)
	return workspace, nil
}

func (d *toraDryRunExecutor) applyGroupDelete(ctx context.Context, workspace *ai.WorkspaceContext, input map[string]any) error {
	if workspace == nil {
		return nil
	}

	groupID := strings.TrimSpace(fmt.Sprint(input["group_id"]))
	groupName := strings.TrimSpace(fmt.Sprint(input["group_name"]))
	action := strings.TrimSpace(fmt.Sprint(input["action"]))
	if groupID != "" && groupName != "" {
		d.groupNamesByID[groupID] = groupName
	}
	groupKey := toraDryRunGroupKey(groupName)
	if groupKey == "" {
		return nil
	}

	switch action {
	case "delete_tasks":
		filtered := workspace.Tasks[:0]
		for _, task := range workspace.Tasks {
			if toraDryRunGroupKey(task.SprintName) == groupKey {
				continue
			}
			filtered = append(filtered, task)
		}
		workspace.Tasks = filtered
	case "reassign":
		targetName, err := d.resolveGroupNameByID(ctx, strings.TrimSpace(fmt.Sprint(input["reassign_to_group_id"])))
		if err != nil {
			return err
		}
		if strings.TrimSpace(targetName) == "" {
			return nil
		}
		now := time.Now().UTC()
		for index := range workspace.Tasks {
			if toraDryRunGroupKey(workspace.Tasks[index].SprintName) != groupKey {
				continue
			}
			workspace.Tasks[index].SprintName = strings.TrimSpace(targetName)
			workspace.Tasks[index].UpdatedAt = now
		}
	}
	return nil
}

func (d *toraDryRunExecutor) resolveGroupNameByID(ctx context.Context, groupID string) (string, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return "", nil
	}
	if name := strings.TrimSpace(d.groupNamesByID[groupID]); name != "" {
		return name, nil
	}

	result, err := d.real(ctx, "list_groups", map[string]any{})
	if err != nil {
		return "", err
	}
	d.cacheGroupNames(result)
	return strings.TrimSpace(d.groupNamesByID[groupID]), nil
}

func (d *toraDryRunExecutor) cacheGroupNames(result any) {
	summaries, ok := toraDryRunGroupSummaries(result)
	if !ok {
		return
	}
	for _, summary := range summaries {
		groupID := strings.TrimSpace(summary.GroupID)
		groupName := strings.TrimSpace(summary.Name)
		if groupID == "" || groupName == "" {
			continue
		}
		d.groupNamesByID[groupID] = groupName
	}
}

func (d *toraDryRunExecutor) buildPendingActionsJSON() string {
	actions := make([]map[string]any, 0, len(d.stagedWrites))
	for _, w := range d.stagedWrites {
		var action map[string]any
		switch w.tool {
		case "create_task":
			action = map[string]any{"kind": "task_create", "already_applied": false}
			for _, k := range []string{"title", "description", "status", "task_type", "budget",
				"start_date", "due_date", "roles", "assignee_id", "blocked_by", "blocks"} {
				if v, ok := w.input[k]; ok && v != nil {
					action[k] = v
				}
			}
			if sprint, ok := w.input["sprint_name"]; ok {
				action["sprint"] = sprint
			}
		case "update_task":
			action = map[string]any{"kind": "task_update", "already_applied": false}
			for _, k := range []string{"task_id", "task_title", "task_number"} {
				if v, ok := w.input[k]; ok && v != nil {
					action[k] = v
				}
			}
			if sprint, ok := w.input["sprint_name"]; ok {
				action["task_sprint"] = sprint
			}
			changes := map[string]any{}
			for k, v := range w.input {
				if k == "task_id" || k == "task_title" || k == "sprint_name" || k == "task_number" {
					continue
				}
				changes[k] = v
			}
			if len(changes) > 0 {
				action["changes"] = changes
				action["change_details"] = changes
			}
		case "delete_task":
			action = map[string]any{"kind": "task_delete", "already_applied": false}
			for _, k := range []string{"task_id", "task_title", "task_number"} {
				if v, ok := w.input[k]; ok && v != nil {
					action[k] = v
				}
			}
			if sprint, ok := w.input["sprint_name"]; ok {
				action["task_sprint"] = sprint
			}
		case "delete_group":
			action = map[string]any{
				"kind":                 "group_delete",
				"already_applied":      false,
				"group_id":             w.input["group_id"],
				"group_name":           w.input["group_name"],
				"action":               w.input["action"],
				"reassign_to_group_id": w.input["reassign_to_group_id"],
			}
		default:
			continue
		}
		actions = append(actions, action)
	}
	b, err := json.Marshal(actions)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// cloneToraMap performs a shallow copy of a map[string]any.
func cloneToraMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneToraWorkspaceContext(src *ai.WorkspaceContext) *ai.WorkspaceContext {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Members = append([]ai.UserCtx(nil), src.Members...)
	dst.Tasks = make([]ai.TaskCtx, 0, len(src.Tasks))
	for _, task := range src.Tasks {
		dst.Tasks = append(dst.Tasks, cloneToraTaskCtx(task))
	}
	dst.SupportTickets = make([]ai.TaskCtx, 0, len(src.SupportTickets))
	for _, task := range src.SupportTickets {
		dst.SupportTickets = append(dst.SupportTickets, cloneToraTaskCtx(task))
	}
	dst.Sprints = append([]ai.SprintCtx(nil), src.Sprints...)
	dst.CanvasFiles = append([]ai.CanvasFileCtx(nil), src.CanvasFiles...)
	dst.RecentMessages = append([]ai.MessageCtx(nil), src.RecentMessages...)
	return &dst
}

func cloneToraTaskCtx(src ai.TaskCtx) ai.TaskCtx {
	dst := src
	dst.Budget = toraDryRunCloneFloatPtr(src.Budget)
	dst.ActualCost = toraDryRunCloneFloatPtr(src.ActualCost)
	dst.StartDate = toraDryRunCloneTimePtr(src.StartDate)
	dst.DueDate = toraDryRunCloneTimePtr(src.DueDate)
	dst.Roles = append([]ai.RoleCtx(nil), src.Roles...)
	dst.Subtasks = append([]ai.SubtaskCtx(nil), src.Subtasks...)
	dst.BlockedBy = append([]string(nil), src.BlockedBy...)
	dst.Blocks = append([]string(nil), src.Blocks...)
	if len(src.CustomFields) > 0 {
		dst.CustomFields = make(map[string]any, len(src.CustomFields))
		for key, value := range src.CustomFields {
			dst.CustomFields[key] = value
		}
	}
	return dst
}

func toraDryRunCloneFloatPtr(src *float64) *float64 {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func toraDryRunCloneTimePtr(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	value := src.UTC()
	return &value
}

func toraDryRunTaskFromInput(input map[string]any, taskID string) ai.TaskCtx {
	now := time.Now().UTC()
	task := ai.TaskCtx{
		ID:          strings.TrimSpace(taskID),
		Title:       toraDryRunFirstNonEmpty(strings.TrimSpace(fmt.Sprint(input["title"])), "(untitled task)"),
		Description: strings.TrimSpace(fmt.Sprint(input["description"])),
		Status:      toraDryRunNormalizeStatus(fmt.Sprint(input["status"])),
		TaskType:    toraDryRunNormalizeTaskType(fmt.Sprint(input["task_type"])),
		SprintName:  strings.TrimSpace(fmt.Sprint(input["sprint_name"])),
		UpdatedAt:   now,
	}
	if task.Status == "" {
		task.Status = "todo"
	}
	task.Budget = toraDryRunFloatPtr(input, "budget")
	task.ActualCost = toraDryRunFloatPtr(input, "actual_cost")
	task.StartDate = toraDryRunTimePtr(input, "start_date")
	task.DueDate = toraDryRunTimePtr(input, "due_date")
	task.Roles = toraDryRunRoles(input["roles"])
	task.Subtasks = toraDryRunSubtasks(input["subtasks"])
	task.BlockedBy = toraDryRunStringSlice(input["blocked_by"])
	task.Blocks = toraDryRunStringSlice(input["blocks"])
	task.AssigneeID = strings.TrimSpace(fmt.Sprint(input["assignee_id"]))
	task.CustomFields = toraDryRunStringAnyMap(input["custom_fields"])
	return task
}

func toraDryRunApplyTaskUpdate(workspace *ai.WorkspaceContext, input map[string]any) {
	if workspace == nil {
		return
	}
	taskID := strings.TrimSpace(fmt.Sprint(input["task_id"]))
	if taskID == "" {
		return
	}

	for index := range workspace.Tasks {
		if strings.TrimSpace(workspace.Tasks[index].ID) != taskID {
			continue
		}
		toraDryRunUpdateTask(&workspace.Tasks[index], input)
		if workspace.Tasks[index].TaskType == "support" {
			task := workspace.Tasks[index]
			workspace.Tasks = append(workspace.Tasks[:index], workspace.Tasks[index+1:]...)
			workspace.SupportTickets = append(workspace.SupportTickets, task)
		}
		return
	}
	for index := range workspace.SupportTickets {
		if strings.TrimSpace(workspace.SupportTickets[index].ID) != taskID {
			continue
		}
		toraDryRunUpdateTask(&workspace.SupportTickets[index], input)
		if workspace.SupportTickets[index].TaskType != "support" {
			task := workspace.SupportTickets[index]
			workspace.SupportTickets = append(workspace.SupportTickets[:index], workspace.SupportTickets[index+1:]...)
			workspace.Tasks = append(workspace.Tasks, task)
		}
		return
	}
}

func toraDryRunUpdateTask(task *ai.TaskCtx, input map[string]any) {
	if task == nil {
		return
	}
	if value, ok := input["title"]; ok {
		task.Title = toraDryRunFirstNonEmpty(strings.TrimSpace(fmt.Sprint(value)), task.Title)
	}
	if value, ok := input["description"]; ok {
		task.Description = strings.TrimSpace(fmt.Sprint(value))
	}
	if value, ok := input["status"]; ok {
		task.Status = toraDryRunNormalizeStatus(fmt.Sprint(value))
	}
	if value, ok := input["task_type"]; ok {
		task.TaskType = toraDryRunNormalizeTaskType(fmt.Sprint(value))
	}
	if value, ok := input["sprint_name"]; ok {
		task.SprintName = strings.TrimSpace(fmt.Sprint(value))
	}
	if value, ok := input["assignee_id"]; ok {
		task.AssigneeID = strings.TrimSpace(fmt.Sprint(value))
	}
	if _, ok := input["budget"]; ok {
		task.Budget = toraDryRunFloatPtr(input, "budget")
	}
	if _, ok := input["actual_cost"]; ok {
		task.ActualCost = toraDryRunFloatPtr(input, "actual_cost")
	}
	if _, ok := input["start_date"]; ok {
		task.StartDate = toraDryRunTimePtr(input, "start_date")
	}
	if _, ok := input["due_date"]; ok {
		task.DueDate = toraDryRunTimePtr(input, "due_date")
	}
	if value, ok := input["roles"]; ok {
		task.Roles = toraDryRunRoles(value)
	}
	if value, ok := input["subtasks"]; ok {
		task.Subtasks = toraDryRunSubtasks(value)
	}
	if value, ok := input["blocked_by"]; ok {
		task.BlockedBy = toraDryRunStringSlice(value)
	}
	if value, ok := input["blocks"]; ok {
		task.Blocks = toraDryRunStringSlice(value)
	}
	if value, ok := input["custom_fields"]; ok {
		task.CustomFields = toraDryRunStringAnyMap(value)
	}
	task.UpdatedAt = time.Now().UTC()
}

func toraDryRunDeleteTask(workspace *ai.WorkspaceContext, taskID string) {
	if workspace == nil || taskID == "" {
		return
	}
	for index := 0; index < len(workspace.Tasks); index++ {
		if strings.TrimSpace(workspace.Tasks[index].ID) != taskID {
			continue
		}
		workspace.Tasks = append(workspace.Tasks[:index], workspace.Tasks[index+1:]...)
		return
	}
	for index := 0; index < len(workspace.SupportTickets); index++ {
		if strings.TrimSpace(workspace.SupportTickets[index].ID) != taskID {
			continue
		}
		workspace.SupportTickets = append(workspace.SupportTickets[:index], workspace.SupportTickets[index+1:]...)
		return
	}
}

func toraDryRunDeriveSprintContexts(tasks []ai.TaskCtx) []ai.SprintCtx {
	sprintMap := make(map[string]*ai.SprintCtx)
	for _, task := range tasks {
		name := strings.TrimSpace(task.SprintName)
		sprint := sprintMap[name]
		if sprint == nil {
			sprint = &ai.SprintCtx{Name: name}
			sprintMap[name] = sprint
		}
		sprint.TaskCount++
		switch toraDryRunNormalizeStatus(task.Status) {
		case "done":
			sprint.Done++
		case "in_progress":
			sprint.InProgress++
		default:
			sprint.Todo++
		}
	}

	sprints := make([]ai.SprintCtx, 0, len(sprintMap))
	for _, sprint := range sprintMap {
		sprints = append(sprints, *sprint)
	}
	sort.SliceStable(sprints, func(i, j int) bool {
		if cmp := toraDryRunCompareFold(sprints[i].Name, sprints[j].Name); cmp != 0 {
			return cmp < 0
		}
		return sprints[i].TaskCount < sprints[j].TaskCount
	})
	return sprints
}

func toraDryRunSortWorkspace(workspace *ai.WorkspaceContext) {
	if workspace == nil {
		return
	}
	sort.SliceStable(workspace.Tasks, func(i, j int) bool {
		return toraDryRunTaskLess(workspace.Tasks[i], workspace.Tasks[j])
	})
	sort.SliceStable(workspace.SupportTickets, func(i, j int) bool {
		return toraDryRunTaskLess(workspace.SupportTickets[i], workspace.SupportTickets[j])
	})
	sort.SliceStable(workspace.Sprints, func(i, j int) bool {
		if cmp := toraDryRunCompareFold(workspace.Sprints[i].Name, workspace.Sprints[j].Name); cmp != 0 {
			return cmp < 0
		}
		return workspace.Sprints[i].TaskCount < workspace.Sprints[j].TaskCount
	})
}

func toraDryRunTaskLess(left, right ai.TaskCtx) bool {
	leftSprint := toraDryRunFirstNonEmpty(strings.TrimSpace(left.SprintName), "(No Sprint)")
	rightSprint := toraDryRunFirstNonEmpty(strings.TrimSpace(right.SprintName), "(No Sprint)")
	if cmp := toraDryRunCompareFold(leftSprint, rightSprint); cmp != 0 {
		return cmp < 0
	}
	if leftStatus, rightStatus := toraDryRunTaskStatusSortKey(left.Status), toraDryRunTaskStatusSortKey(right.Status); leftStatus != rightStatus {
		return leftStatus < rightStatus
	}
	if cmp := toraDryRunCompareFold(left.Title, right.Title); cmp != 0 {
		return cmp < 0
	}
	return toraDryRunCompareFold(left.ID, right.ID) < 0
}

func toraDryRunTaskStatusSortKey(status string) int {
	switch toraDryRunNormalizeStatus(status) {
	case "todo":
		return 0
	case "in_progress":
		return 1
	case "blocked":
		return 2
	case "done":
		return 3
	default:
		return 4
	}
}

func toraDryRunNormalizeStatus(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	if normalized == "" {
		return "todo"
	}
	return normalized
}

func toraDryRunNormalizeTaskType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "all":
		return "all"
	case "support":
		return "support"
	default:
		return "sprint"
	}
}

func toraDryRunGroupKey(raw string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(raw))), " ")
}

func toraDryRunCompareFold(left string, right string) int {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func toraDryRunFloatPtr(input map[string]any, field string) *float64 {
	rawValue, ok := input[field]
	if !ok || rawValue == nil {
		return nil
	}
	switch value := rawValue.(type) {
	case float64:
		return &value
	case float32:
		next := float64(value)
		return &next
	case int:
		next := float64(value)
		return &next
	case int64:
		next := float64(value)
		return &next
	case json.Number:
		if parsed, err := value.Float64(); err == nil {
			return &parsed
		}
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return &parsed
		}
	}
	return nil
}

func toraDryRunTimePtr(input map[string]any, field string) *time.Time {
	rawValue, ok := input[field]
	if !ok {
		return nil
	}
	switch value := rawValue.(type) {
	case time.Time:
		next := value.UTC()
		return &next
	case *time.Time:
		return toraDryRunCloneTimePtr(value)
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			next := parsed.UTC()
			return &next
		}
	}
	return nil
}

func toraDryRunRoles(raw any) []ai.RoleCtx {
	switch value := raw.(type) {
	case []ai.RoleCtx:
		return append([]ai.RoleCtx(nil), value...)
	case []map[string]any:
		roles := make([]ai.RoleCtx, 0, len(value))
		for _, entry := range value {
			roles = append(roles, ai.RoleCtx{
				Role:             strings.TrimSpace(fmt.Sprint(entry["role"])),
				Responsibilities: strings.TrimSpace(fmt.Sprint(entry["responsibilities"])),
			})
		}
		return roles
	case []any:
		roles := make([]ai.RoleCtx, 0, len(value))
		for _, entry := range value {
			if record, ok := entry.(map[string]any); ok {
				roles = append(roles, ai.RoleCtx{
					Role:             strings.TrimSpace(fmt.Sprint(record["role"])),
					Responsibilities: strings.TrimSpace(fmt.Sprint(record["responsibilities"])),
				})
			}
		}
		return roles
	default:
		return nil
	}
}

func toraDryRunSubtasks(raw any) []ai.SubtaskCtx {
	switch value := raw.(type) {
	case []ai.SubtaskCtx:
		return append([]ai.SubtaskCtx(nil), value...)
	case []map[string]any:
		subtasks := make([]ai.SubtaskCtx, 0, len(value))
		for _, entry := range value {
			subtasks = append(subtasks, ai.SubtaskCtx{
				Content:   strings.TrimSpace(fmt.Sprint(entry["content"])),
				Completed: toraDryRunBool(entry["completed"]),
			})
		}
		return subtasks
	case []any:
		subtasks := make([]ai.SubtaskCtx, 0, len(value))
		for _, entry := range value {
			if record, ok := entry.(map[string]any); ok {
				subtasks = append(subtasks, ai.SubtaskCtx{
					Content:   strings.TrimSpace(fmt.Sprint(record["content"])),
					Completed: toraDryRunBool(record["completed"]),
				})
			}
		}
		return subtasks
	default:
		return nil
	}
}

func toraDryRunBool(raw any) bool {
	value, ok := raw.(bool)
	return ok && value
}

func toraDryRunStringSlice(raw any) []string {
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		items := make([]string, 0, len(value))
		for _, entry := range value {
			trimmed := strings.TrimSpace(fmt.Sprint(entry))
			if trimmed != "" {
				items = append(items, trimmed)
			}
		}
		return items
	default:
		return nil
	}
}

func toraDryRunStringAnyMap(raw any) map[string]any {
	record, ok := raw.(map[string]any)
	if !ok || len(record) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(record))
	for key, value := range record {
		cloned[key] = value
	}
	return cloned
}

func toraDryRunFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func toraDryRunGroupSummaries(result any) ([]projectboard.GroupSummary, bool) {
	switch value := result.(type) {
	case []projectboard.GroupSummary:
		return append([]projectboard.GroupSummary(nil), value...), true
	case []models.Group:
		summaries := make([]projectboard.GroupSummary, 0, len(value))
		for _, group := range value {
			summaries = append(summaries, projectboard.GroupSummary{Group: group})
		}
		return summaries, true
	case []any:
		summaries := make([]projectboard.GroupSummary, 0, len(value))
		for _, entry := range value {
			record, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			summaries = append(summaries, projectboard.GroupSummary{
				Group: models.Group{
					WorkspaceID:  strings.TrimSpace(fmt.Sprint(record["workspace_id"])),
					GroupID:      strings.TrimSpace(fmt.Sprint(record["group_id"])),
					Name:         strings.TrimSpace(fmt.Sprint(record["name"])),
					DisplayOrder: toraDryRunInt(record["display_order"]),
					StartDate:    strings.TrimSpace(fmt.Sprint(record["start_date"])),
					EndDate:      strings.TrimSpace(fmt.Sprint(record["end_date"])),
					Description:  strings.TrimSpace(fmt.Sprint(record["description"])),
				},
				TaskCount: toraDryRunInt(record["task_count"]),
			})
		}
		return summaries, true
	default:
		return nil, false
	}
}

func toraDryRunInt(raw any) int {
	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return int(parsed)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return 0
}

func newToraBotCanvasActionMessage(
	roomID string,
	origin models.Message,
	text string,
	changesJSON string,
	events []ai.AgentEvent,
) models.Message {
	normalizedOriginID := normalizeMessageID(origin.ID)
	payload, _ := json.Marshal(map[string]any{
		"text":            strings.TrimSpace(text),
		"changesJson":     changesJSON,
		"auditTrail":      buildToraAgentAuditTrail(events),
		"agentic":         true,
		"pendingApply":    true,
		"originMessageId": normalizedOriginID,
	})
	return models.Message{
		ID:               fmt.Sprintf("%s_tora_%d", roomID, time.Now().UTC().UnixNano()),
		RoomID:           roomID,
		SenderID:         toraBotSenderID,
		SenderName:       toraBotSenderName,
		Content:          string(payload),
		Type:             "tora_canvas_action",
		ReplyToMessageID: normalizedOriginID,
		ReplyToSnippet:   summarizeToraWorkflowPrompt(origin.Content),
		CreatedAt:        time.Now().UTC(),
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
	h.contextBuilder = ai.NewContextBuilder(h.msgService.Scylla).WithRedis(h.msgService.Redis)
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
	finalText, pendingActionsJSON, auditEvents, err := h.runToraTaskBoardAgent(ctx, roomID, userMessage, prompt, plan)
	h.broadcast <- newToraWorkflowMessage(roomID, userMessage, "task_board", finalText, auditEvents, err)
	if err != nil {
		return err
	}

	h.broadcast <- newToraBotAgentActionMessage(roomID, userMessage, finalText, pendingActionsJSON, auditEvents)
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
	forceTaskReference bool,
) error {
	responseText, auditEvents, err := h.runToraCanvasAgent(ctx, roomID, userMessage, prompt, forceTaskReference)
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
		h.broadcast <- newToraBotCanvasActionMessage(roomID, userMessage, responseText, changesJSON, auditEvents)
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

type toraExecutionTarget string

const (
	toraExecutionTargetTaskBoard toraExecutionTarget = "task_board"
	toraExecutionTargetCanvas    toraExecutionTarget = "canvas"
	toraExecutionTargetChat      toraExecutionTarget = "chat"
)

func resolveToraExecutionTarget(plan toraLoadPlan, hasCanvasTag bool) toraExecutionTarget {
	switch {
	case hasCanvasTag:
		return toraExecutionTargetCanvas
	case plan.has(toraFlagMutation):
		return toraExecutionTargetTaskBoard
	default:
		return toraExecutionTargetChat
	}
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
	roomType := h.loadToraRoomType(ctx, roomID)
	ephemeralRoom := isToraEphemeralRoomType(roomType)
	workspaceCtx := ""
	if !ephemeralRoom {
		workspaceCtx = h.fetchToraWorkspaceContext(ctx, roomID, plan)
	}
	systemPrompt := toraSystemInstruction
	if ephemeralRoom {
		systemPrompt = buildToraChatSystemPrompt(false, true)
	}
	aiPrompt := buildToraPrompt(
		systemPrompt,
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
) (string, string, []ai.AgentEvent, error) {
	if h == nil || h.msgService == nil || h.msgService.Scylla == nil || h.msgService.Scylla.Session == nil {
		return "", "", nil, fmt.Errorf("task storage unavailable")
	}

	ctxBuilder := h.ensureToraContextBuilder()
	engineFactory := h.ensureToraAgentEngineFactory()
	if ctxBuilder == nil || engineFactory == nil {
		return "", "", nil, fmt.Errorf("task board ai is not configured")
	}

	buildOpts := ai.BuildOptions{
		IncludeCanvas: false,
		IncludeChat:   false,
		TaskLimit:     500,
	}

	workspace, err := ctxBuilder.Build(ctx, roomID, strings.TrimSpace(userMessage.SenderID), buildOpts)
	if err != nil {
		return "", "", nil, err
	}
	cfg := models.GetProjectTypeConfig(workspace.ProjectType)

	engine := engineFactory.New(roomID, ai.AgentAuthContext{
		UserID:   strings.TrimSpace(userMessage.SenderID),
		UserName: strings.TrimSpace(userMessage.SenderName),
	}, plan.modelTier)
	if engine == nil {
		return "", "", nil, fmt.Errorf("task board ai engine is unavailable")
	}
	engine.SetRoomBroadcaster(h)

	// Install dry-run executor so write tools are intercepted and staged for
	// user confirmation instead of being applied directly to the database.
	dryRun := newToraDryRunExecutor(engine.ExecuteBuiltInTool, workspace)
	engine.SetToolExecutor(dryRun.execute)

	finalText, events, err := engine.Run(ctx, prompt, ai.AgentConfig{
		MaxTurns:        40,
		Timeout:         toraRequestTimeoutMutation,
		Effort:          plan.modelTier,
		SystemPrompt:    buildToraTaskBoardSystemPrompt(cfg),
		ContextOptions:  buildOpts,
		Workspace:       workspace,
		InitialContext:  buildToraTaskBoardInitialContext(workspace, buildOpts, cfg),
		OriginMessageID: normalizeMessageID(userMessage.ID),
		WorkflowKind:    "task_board",
	})
	if err != nil {
		return "", "", events, err
	}

	if toraTaskBoardNeedsToolRetry(finalText, events) && ctx.Err() == nil {
		retryWorkspace := workspace
		if simulatedWorkspace, simErr := dryRun.simulatedWorkspace(ctx); simErr == nil && simulatedWorkspace != nil {
			retryWorkspace = simulatedWorkspace
		}
		retryText, retryEvents, retryErr := engine.Run(ctx, buildToraTaskBoardToolEnforcementPrompt(prompt, finalText, events), ai.AgentConfig{
			MaxTurns:        20,
			Timeout:         toraRequestTimeoutMutation,
			Effort:          plan.modelTier,
			SystemPrompt:    buildToraTaskBoardSystemPrompt(cfg),
			ContextOptions:  buildOpts,
			Workspace:       retryWorkspace,
			InitialContext:  buildToraTaskBoardInitialContext(retryWorkspace, buildOpts, cfg),
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
		return "", "", events, fmt.Errorf("task board ai did not execute task-board tools for this mutation request")
	}

	// Skip the DB validation/repair pass in dry-run mode — nothing was written
	// to the database yet so a re-read would show stale data.
	pendingActionsJSON := dryRun.buildPendingActionsJSON()

	summaryWorkspace := workspace
	if simulatedWorkspace, simErr := dryRun.simulatedWorkspace(ctx); simErr == nil && simulatedWorkspace != nil {
		summaryWorkspace = simulatedWorkspace
	}
	summary := formatToraTaskBoardSummary(finalText, events, summaryWorkspace)
	return summary, pendingActionsJSON, events, nil
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

func buildToraTaskBoardInitialContext(workspace *ai.WorkspaceContext, opts ai.BuildOptions, cfg models.ProjectTypeConfig) string {
	if workspace == nil {
		return ""
	}
	rendered := strings.TrimSpace(workspace.RenderForAI(opts))
	if rendered == "" {
		return ""
	}
	return fmt.Sprintf("CURRENT BOARD STATE — %s %s (pre-loaded, do NOT call list_tasks() to re-read):\n\n%s", cfg.GroupTermPlural, cfg.TaskTermPlural, rendered)
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
		if strings.TrimSpace(event.Model) != "" {
			entry["model"] = strings.TrimSpace(event.Model)
		}
		if strings.TrimSpace(event.Effort) != "" {
			entry["effort"] = strings.TrimSpace(event.Effort)
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
		case "delete_task", "delete_group":
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
		case "create_task", "update_task", "delete_task", "delete_group":
			writes++
		}
	}
	return total, writes
}

func toraTaskBoardNeedsToolRetry(finalText string, events []ai.AgentEvent) bool {
	// A graceful timeout message means the agent ran out of time — retrying
	// won't help and would just waste the remaining context window time.
	if toraIsGracefulTimeoutText(finalText) {
		return false
	}
	totalCalls, writeCalls := countToraTaskBoardToolCalls(events)
	if writeCalls > 0 {
		return !toraTaskBoardHasVerifyCall(events)
	}
	if totalCalls == 0 {
		return true
	}
	return toraResponseIsRefusal(finalText)
}

func toraTaskBoardHasVerifyCall(events []ai.AgentEvent) bool {
	for _, event := range events {
		if strings.TrimSpace(event.Kind) != "tool_call" {
			continue
		}
		if strings.TrimSpace(event.Tool) == "verify_task_count" {
			return true
		}
	}
	return false
}

func toraIsGracefulTimeoutText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "ran out of time") || strings.Contains(lower, "timed out")
}

func buildToraTaskBoardToolEnforcementPrompt(prompt string, finalText string, events []ai.AgentEvent) string {
	base := strings.TrimSpace(prompt)
	previous := strings.TrimSpace(finalText)
	writeCalls := false
	verifyCalled := toraTaskBoardHasVerifyCall(events)
	for _, event := range events {
		if strings.TrimSpace(event.Kind) != "tool_call" {
			continue
		}
		switch strings.TrimSpace(event.Tool) {
		case "create_task", "update_task", "delete_task", "delete_group":
			writeCalls = true
		}
	}
	var builder strings.Builder
	builder.WriteString(base)
	builder.WriteString("\n\nTool-use enforcement from the backend:\n")
	builder.WriteString("- You do have access to list_tasks, create_task, update_task, delete_task, list_sprints, list_groups, delete_group, and verify_task_count.\n")
	if writeCalls && !verifyCalled {
		builder.WriteString("- Your previous run already made staged board changes, but it did not finish the required verification step.\n")
		builder.WriteString("- Continue from the current staged board state. Do NOT restart the project rewrite from scratch.\n")
		builder.WriteString("- Do NOT repeat earlier create/update/delete operations unless verification shows something is still missing.\n")
		builder.WriteString("- Use the remaining zero-input tools you still need, especially list_groups() and verify_task_count().\n")
	} else {
		builder.WriteString("- Your previous answer did not perform the requested board mutation.\n")
		builder.WriteString("- This request is invalid unless you actually call create_task/update_task/delete_task/delete_group tools.\n")
		builder.WriteString("- The board state is pre-loaded. Do NOT call list_tasks first — start executing the needed write tools immediately.\n")
	}
	builder.WriteString("- Do not answer with \"I don't have the tools\" or any similar refusal.\n")
	builder.WriteString("- A run with write tools but no verify_task_count() is incomplete.\n")
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
	ephemeralRoom := isToraEphemeralRoomType(roomType)
	intentProvider := resolveToraChatProvider(ai.AIModelTierLight)
	intent := resolveToraChatIntent(ctx, prompt, intentProvider)

	ctxBuilder := h.ensureToraContextBuilder()
	engineFactory := h.ensureToraAgentEngineFactory()
	if ctxBuilder == nil || engineFactory == nil {
		return "", nil, fmt.Errorf("chat ai is not configured")
	}

	buildOpts := toraChatBuildOptions(intent, privateRoom, ephemeralRoom)
	workspace, err := ctxBuilder.Build(ctx, roomID, strings.TrimSpace(userMessage.SenderID), buildOpts)
	if err != nil {
		return "", nil, err
	}

	modelTier := toraChatModelTier(intent)
	engine := engineFactory.New(roomID, ai.AgentAuthContext{
		UserID:   strings.TrimSpace(userMessage.SenderID),
		UserName: strings.TrimSpace(userMessage.SenderName),
	}, modelTier)
	if engine == nil {
		return "", nil, fmt.Errorf("chat ai engine is unavailable")
	}
	engine.SetRoomBroadcaster(h)

	finalText, events, err := engine.Run(ctx, prompt, ai.AgentConfig{
		MaxTurns:        toraChatMaxTurns(intent),
		Timeout:         toraRequestTimeout,
		Effort:          modelTier,
		SystemPrompt:    buildToraChatSystemPrompt(privateRoom, ephemeralRoom),
		ContextOptions:  buildOpts,
		Workspace:       workspace,
		InitialContext:  buildToraChatInitialContext(workspace, intent, privateRoom, ephemeralRoom),
		AllowedTools:    toraChatAllowedTools(intent, privateRoom, ephemeralRoom),
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

func isToraEphemeralRoomType(roomType string) bool {
	lower := strings.ToLower(strings.TrimSpace(roomType))
	return lower == "temp" || lower == "ephemeral" || lower == "temporary"
}

func buildToraChatSystemPrompt(privateRoom bool, ephemeralRoom ...bool) string {
	isEphemeral := len(ephemeralRoom) > 0 && ephemeralRoom[0]
	if isEphemeral {
		return toraEphemeralChatSystemPrompt
	}
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

func toraChatBuildOptions(intent toraChatIntent, privateRoom bool, ephemeralRoom ...bool) ai.BuildOptions {
	isEphemeral := len(ephemeralRoom) > 0 && ephemeralRoom[0]
	if isEphemeral {
		return ai.BuildOptions{
			IncludeCanvas:    false,
			IncludeChat:      true,
			TaskLimit:        0,
			ChatMessageLimit: 20,
		}
	}
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
		opts.TaskLimit = 20
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
		return ai.AIModelTierStandard // was AIModelTierLight — too weak for persona adherence
	}
}

func toraChatMaxTurns(intent toraChatIntent) int {
	switch intent {
	case toraChatIntentTasks:
		return 4
	case toraChatIntentCode:
		return 3
	default:
		return 3
	}
}

func toraChatAllowedTools(intent toraChatIntent, privateRoom bool, ephemeralRoom ...bool) []string {
	isEphemeral := len(ephemeralRoom) > 0 && ephemeralRoom[0]
	if privateRoom || isEphemeral {
		return []string{}
	}
	switch intent {
	case toraChatIntentTasks:
		return []string{"list_tasks", "list_sprints", "list_groups", "search_tasks"}
	case toraChatIntentCode:
		return []string{"search_tasks"}
	default:
		// General chat — allow search_tasks so the AI can look up project info
		// if the question relates to the workspace.
		return []string{"search_tasks"}
	}
}

func buildToraChatInitialContext(workspace *ai.WorkspaceContext, intent toraChatIntent, privateRoom bool, ephemeralRoom ...bool) string {
	if workspace == nil {
		return ""
	}
	isEphemeral := len(ephemeralRoom) > 0 && ephemeralRoom[0]

	var sb strings.Builder
	sb.WriteString("CHAT ROOM CONTEXT\n")
	sb.WriteString(fmt.Sprintf("Room: %s\n", toraFirstNonEmpty(strings.TrimSpace(workspace.RoomName), strings.TrimSpace(workspace.RoomID))))
	if isEphemeral {
		sb.WriteString("Mode: temporary room\n")
	} else if privateRoom {
		sb.WriteString("Mode: private channel\n")
	} else {
		sb.WriteString("Mode: shared room\n")
	}

	if isEphemeral {
		if messages := renderToraChatMessages(workspace.RecentMessages); messages != "" {
			sb.WriteString("\nRecent messages:\n")
			sb.WriteString(messages)
		}
		return strings.TrimSpace(sb.String())
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

	if intent == toraChatIntentGeneral && len(workspace.Tasks) > 0 {
		sb.WriteString("\nNote: Task board data is available for reference if the user's question relates to the project. For general questions unrelated to the project, answer from your own knowledge.\n")
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
	if errors.Is(err, context.DeadlineExceeded) {
		return "The request timed out before I could finish. Please retry.\n• Error: request timed out, retry later"
	}
	if errors.Is(err, context.Canceled) {
		return "The request was canceled before completion. Please send it again.\n• Error: request canceled, retry later"
	}
	var statusErr *ai.HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode() {
		case http.StatusTooManyRequests:
			return buildToraProviderRateLimitResponse(statusErr)
		case http.StatusServiceUnavailable:
			return buildToraProviderUnavailableResponse(statusErr)
		}
		return buildToraProviderErrorResponse(statusErr)
	}
	if errors.Is(err, ai.ErrAllAIProvidersExhausted) {
		return "I couldn't reach any configured AI provider right now.\n• Error: provider chain unavailable\n• Retry: no reset time was reported"
	}
	return "I could not complete that request right now.\n• Error: " + err.Error()
}

func buildToraProviderRateLimitResponse(statusErr *ai.HTTPStatusError) string {
	detail := toraProviderFailureDetail(statusErr)
	lines := []string{
		"I hit an AI provider rate limit before I could finish.",
		"• Provider: " + toraProviderFailureName(statusErr),
	}
	if detail != "" {
		lines = append(lines, "• Limit detail: "+detail)
	} else {
		lines = append(lines, "• Limit detail: provider quota or request rate reached")
	}
	lines = append(lines, "• Reset: "+toraProviderRetryHint(statusErr, "not reported by provider"))
	return strings.Join(lines, "\n")
}

func buildToraProviderUnavailableResponse(statusErr *ai.HTTPStatusError) string {
	detail := toraProviderFailureDetail(statusErr)
	lines := []string{
		"The AI provider is temporarily unavailable right now.",
		"• Provider: " + toraProviderFailureName(statusErr),
	}
	if detail != "" {
		lines = append(lines, "• Upstream detail: "+detail)
	}
	lines = append(lines, "• Retry: "+toraProviderRetryHint(statusErr, "provider did not report a reset time"))
	return strings.Join(lines, "\n")
}

func buildToraProviderErrorResponse(statusErr *ai.HTTPStatusError) string {
	detail := toraProviderFailureDetail(statusErr)
	lines := []string{
		"I couldn't complete that request because the AI provider returned an error.",
		"• Provider: " + toraProviderFailureName(statusErr),
		fmt.Sprintf("• Status: %d %s", statusErr.StatusCode(), http.StatusText(statusErr.StatusCode())),
	}
	if detail != "" {
		lines = append(lines, "• Detail: "+detail)
	}
	return strings.Join(lines, "\n")
}

func toraProviderFailureName(statusErr *ai.HTTPStatusError) string {
	if statusErr == nil {
		return "unknown"
	}
	return toraFirstNonEmpty(statusErr.Provider, "unknown")
}

func toraProviderFailureDetail(statusErr *ai.HTTPStatusError) string {
	if statusErr == nil || statusErr.Err == nil {
		return ""
	}
	return sanitizeToraFailureDetail(statusErr.Err.Error())
}

func toraProviderRetryHint(statusErr *ai.HTTPStatusError, fallback string) string {
	detail := toraProviderFailureDetail(statusErr)
	if detail == "" {
		return fallback
	}
	lower := strings.ToLower(detail)
	for _, marker := range []string{"retry after ", "retry in ", "try again in "} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return detail[idx:]
		}
	}
	return fallback
}

func sanitizeToraFailureDetail(detail string) string {
	detail = strings.Join(strings.Fields(strings.TrimSpace(detail)), " ")
	if detail == "" {
		return ""
	}
	if len(detail) > 180 {
		return detail[:177] + "..."
	}
	return detail
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
		taskNumber      int
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
		`SELECT id, task_number, title, status, task_type, description, sprint_name, assignee_id, status_actor_name, due_date, start_date, roles FROM %s WHERE room_id = ?`,
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
		taskNumPtr      *int
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
	for tasksIter.Scan(&taskID, &taskNumPtr, &title, &status, &taskType, &description, &sprint, &assigneeUUIDPtr, &statusActorName, &toraDueDate, &toraStartDate, &toraRolesRaw) {
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
		if taskNumPtr != nil {
			row.taskNumber = *taskNumPtr
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
		// task_number is a short stable number the AI can use as a fallback identifier.
		// Include sprint so the AI can distinguish duplicate-named tasks.
		var line string
		numTag := ""
		if t.taskNumber > 0 {
			numTag = fmt.Sprintf("  #%d", t.taskNumber)
		}
		if t.sprint != "" {
			line = fmt.Sprintf("  - [%s] %s  {id:%s}%s  sprint:%q", t.status, t.title, t.id, numTag, t.sprint)
		} else {
			line = fmt.Sprintf("  - [%s] %s  {id:%s}%s  sprint:(none)", t.status, t.title, t.id, numTag)
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

func buildToraPrompt(systemPrompt string, rollingSummary string, contextMessages []models.Message, workspaceCtx string, prompt string, includeMutations bool) string {
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
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = toraSystemInstruction
	}
	parts = append(parts, strings.TrimSpace(systemPrompt))
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
