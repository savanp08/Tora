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
	toraPrimaryMentionToken    = "@ToraAI"
	toraLegacyMentionToken     = "@Tora"
	toraProjectToken           = "@project" // matched case-insensitively
	toraCanvasToken            = "@canvas"  // matched case-insensitively
	toraBotSenderID            = "Tora-Bot"
	toraBotSenderName          = "Tora-Bot"
	toraRequestTimeout         = 25 * time.Second
	toraRequestTimeoutMutation = 75 * time.Second // 3 turns × ~20s each
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
	// @Canvas is reserved for code edits — noted in plan for future use.
	if hasCanvasTag {
		plan.reason += "+@canvas"
	}
	log.Printf("[ws] tora intent: plan=%s maxTasks=%d", plan.reason, plan.maxTasks)

	releaseTyping := h.beginToraTyping(roomID)
	defer releaseTyping()

	// Mutation requests may require multiple AI turns — use the longer timeout.
	requestTimeout := toraRequestTimeout
	if plan.has(toraFlagMutation) {
		requestTimeout = toraRequestTimeoutMutation
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	rollingSummary := h.loadRoomRollingSummary(ctx, roomID)
	contextMessages := h.loadRecentMessagesFromRedis(ctx, roomID, toraContextMsgLimit())
	workspaceCtx := h.fetchToraWorkspaceContext(ctx, roomID, plan)
	aiPrompt := buildToraPrompt(rollingSummary, contextMessages, workspaceCtx, prompt, plan.has(toraFlagMutation))
	aiResponse, err := callToraWithCompletion(ctx, aiPrompt, plan)
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

	// If the response contains a structured action block, broadcast it as a
	// tora_action message so the frontend can render accept/reject cards.
	if plan.has(toraFlagMutation) {
		textPart, actionsJSON := parseToraMutationResponse(responseText)
		if actionsJSON != "" {
			h.broadcast <- newToraBotActionMessage(roomID, textPart, actionsJSON)
			go h.refreshRoomRollingSummary(roomID, rollingSummary, contextMessages)
			return
		}
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
