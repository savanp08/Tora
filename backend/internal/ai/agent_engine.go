package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/execution"
	"github.com/savanp08/converse/internal/projectboard"
)

const (
	defaultAgentMaxTurns = 8
	defaultAgentTimeout  = 120 * time.Second
	defaultAgentModel    = "claude-opus-4-6"

	agentCanvasFilesTable = "canvas_files"

	defaultAgentSystemPrompt = `You are Converse's agentic workspace AI.

Use tools whenever the request depends on current workspace state or requires a mutation.
Never invent task IDs, sprint assignments, or file paths when tools can verify them.
Prefer small, correct steps over large speculative plans.
When you mutate tasks or canvas state, verify the result with follow-up tool calls before you finish.
If a tool reports an error, reason about it and recover when possible.`
)

var (
	agentExecutionManagerOnce sync.Once
	agentExecutionManager     *execution.ExecutionManager
)

// Provider is the shared baseline AI interface used across the package today.
// Tool-enabled providers can opt into AgentEngine by also implementing
// ToolUseProvider.
type Provider interface {
	Summarizer
}

// ToolUseProvider is the capability AgentEngine needs from a model backend.
// The shape mirrors Anthropic-style conversations: messages contain content
// blocks, and assistant responses may include tool_use blocks.
type ToolUseProvider interface {
	Provider
	GenerateToolResponse(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error)
}

// AnthropicTool matches the schema shape expected by Anthropic-style tool use.
type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type AgentProviderRequest struct {
	Model        string                 `json:"model,omitempty"`
	SystemPrompt string                 `json:"system_prompt,omitempty"`
	Messages     []AgentProviderMessage `json:"messages,omitempty"`
	Tools        []AnthropicTool        `json:"tools,omitempty"`
}

type AgentProviderResponse struct {
	Content    []AgentProviderContentBlock `json:"content,omitempty"`
	StopReason string                      `json:"stop_reason,omitempty"`
}

type AgentProviderMessage struct {
	Role    string                      `json:"role"`
	Content []AgentProviderContentBlock `json:"content,omitempty"`
}

type AgentProviderContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

// AgentRoomBroadcaster keeps the engine decoupled from websocket.Hub while
// still matching its BroadcastToRoom surface.
type AgentRoomBroadcaster interface {
	BroadcastToRoom(roomID string, payload map[string]interface{})
}

type AgentEngine struct {
	provider    Provider
	ctxBuilder  *ContextBuilder
	roomID      string
	authContext AgentAuthContext

	broadcaster  AgentRoomBroadcaster
	toolExecutor func(ctx context.Context, name string, input map[string]any) (any, error)
}

type AgentAuthContext struct {
	UserID   string
	UserName string
	Token    string
}

type AgentConfig struct {
	MaxTurns        int
	Timeout         time.Duration
	Model           string
	SystemPrompt    string
	ContextOptions  BuildOptions
	Workspace       *WorkspaceContext
	InitialContext  string
	AllowedTools    []string
	OriginMessageID string
	WorkflowKind    string
	StreamCallback  func(event AgentEvent)
}

type AgentEvent struct {
	Kind            string
	Tool            string
	Input           map[string]any
	Result          any
	Text            string
	Error           string
	Turn            int
	TotalTurns      int
	Timestamp       int64
	OriginMessageID string
	WorkflowKind    string
}

type agentToolInputError struct {
	Field   string
	Message string
}

type agentModelHintGenerator interface {
	GenerateChatResponseWithHint(ctx context.Context, prompt, modelTier string) (string, error)
}

type PromptToolUseProvider struct {
	base      Provider
	modelHint string
}

func (e *agentToolInputError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return strings.TrimSpace(e.Message)
	}
	if strings.TrimSpace(e.Field) == "" {
		return "invalid tool input"
	}
	return "missing required field: " + strings.TrimSpace(e.Field)
}

func NewAgentEngine(provider Provider, ctxBuilder *ContextBuilder, roomID string, authContext AgentAuthContext) *AgentEngine {
	return &AgentEngine{
		provider:    provider,
		ctxBuilder:  ctxBuilder,
		roomID:      strings.TrimSpace(roomID),
		authContext: authContext,
	}
}

func NewPromptToolUseProvider(base Provider, modelHint string) *PromptToolUseProvider {
	return &PromptToolUseProvider{
		base:      base,
		modelHint: strings.TrimSpace(modelHint),
	}
}

func (p *PromptToolUseProvider) GenerateRollingSummary(ctx context.Context, previousState []byte, newMessages []Message) ([]byte, error) {
	if p == nil || p.base == nil {
		return nil, fmt.Errorf("base provider is not configured")
	}
	return p.base.GenerateRollingSummary(ctx, previousState, newMessages)
}

func (p *PromptToolUseProvider) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	if p == nil || p.base == nil {
		return "", fmt.Errorf("base provider is not configured")
	}
	return p.base.GenerateChatResponse(ctx, prompt)
}

func (p *PromptToolUseProvider) GenerateToolResponse(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error) {
	if p == nil || p.base == nil {
		return AgentProviderResponse{}, fmt.Errorf("base provider is not configured")
	}

	prompt := buildPromptToolUseRequest(req)
	raw, err := p.generateToolLoopText(ctx, prompt)
	if err != nil {
		return AgentProviderResponse{}, err
	}
	return parsePromptToolUseResponse(raw)
}

func (p *PromptToolUseProvider) generateToolLoopText(ctx context.Context, prompt string) (string, error) {
	if p == nil || p.base == nil {
		return "", fmt.Errorf("base provider is not configured")
	}
	if strings.TrimSpace(p.modelHint) != "" {
		if hinted, ok := p.base.(agentModelHintGenerator); ok {
			return hinted.GenerateChatResponseWithHint(ctx, prompt, p.modelHint)
		}
		if hinted, ok := any(p.base).(agentModelHintGenerator); ok {
			return hinted.GenerateChatResponseWithHint(ctx, prompt, p.modelHint)
		}
	}
	return p.base.GenerateChatResponse(ctx, prompt)
}

func (e *AgentEngine) SetRoomBroadcaster(broadcaster AgentRoomBroadcaster) {
	if e == nil {
		return
	}
	e.broadcaster = broadcaster
}

func (e *AgentEngine) SetToolExecutor(executor func(ctx context.Context, name string, input map[string]any) (any, error)) {
	if e == nil {
		return
	}
	e.toolExecutor = executor
}

func (e *AgentEngine) ExecuteBuiltInTool(ctx context.Context, name string, input map[string]any) (any, error) {
	switch strings.TrimSpace(name) {
	case "list_tasks":
		return e.agentListTasks(ctx, input)
	case "create_task":
		return e.agentCreateTask(ctx, input)
	case "update_task":
		return e.agentUpdateTask(ctx, input)
	case "delete_task":
		return e.agentDeleteTask(ctx, input)
	case "list_sprints":
		return e.agentListSprints(ctx)
	case "list_groups":
		return e.agentListGroups(ctx)
	case "delete_group":
		return e.agentDeleteGroup(ctx, input)
	case "read_canvas":
		return e.agentReadCanvas(ctx, input)
	case "write_canvas":
		return e.agentWriteCanvas(ctx, input)
	case "execute_canvas":
		return e.agentExecuteCanvas(ctx, input)
	case "search_tasks":
		return e.agentSearchTasks(ctx, input)
	case "verify_task_count":
		return e.agentVerifyTaskCount(ctx)
	default:
		return nil, &agentToolInputError{Message: "unknown tool: " + strings.TrimSpace(name)}
	}
}

func BuiltInAnthropicTools() []AnthropicTool {
	roleArraySchema := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"role": map[string]any{
					"type":        "string",
					"description": "Team role responsible for this task area.",
				},
				"responsibilities": map[string]any{
					"type":        "string",
					"description": "What this role is expected to do on the task.",
				},
			},
			"required": []string{"role"},
		},
	}
	subtaskArraySchema := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "Subtask checklist text.",
				},
				"completed": map[string]any{
					"type":        "boolean",
					"description": "Whether the subtask is already completed.",
				},
			},
			"required": []string{"content"},
		},
	}

	dateField := map[string]any{
		"type":        "string",
		"format":      "date-time",
		"description": "ISO 8601 timestamp.",
	}

	taskMutationProperties := map[string]any{
		"title": map[string]any{
			"type":        "string",
			"description": "Task title.",
		},
		"description": map[string]any{
			"type":        "string",
			"description": "Task description.",
		},
		"status": map[string]any{
			"type":        "string",
			"description": "todo, in_progress, done, or blocked.",
		},
		"task_type": map[string]any{
			"type":        "string",
			"description": "sprint or support. Defaults to sprint.",
		},
		"sprint_name": map[string]any{
			"type":        "string",
			"description": "Sprint name for the task.",
		},
		"assignee_id": map[string]any{
			"type":        "string",
			"description": "User UUID to assign to the task. Use empty string to clear.",
		},
		"budget": map[string]any{
			"type":        "number",
			"description": "Estimated budget in USD.",
		},
		"actual_cost": map[string]any{
			"type":        "number",
			"description": "Actual spent/cost value in USD.",
		},
		"start_date": dateField,
		"due_date":   dateField,
		"roles":      roleArraySchema,
		"custom_fields": map[string]any{
			"type":                 "object",
			"description":          "Patch object for task custom fields. Null values remove keys.",
			"additionalProperties": true,
		},
		"blocked_by": map[string]any{
			"type":        "array",
			"description": "Array of task IDs this task depends on.",
			"items":       map[string]any{"type": "string"},
		},
		"blocks": map[string]any{
			"type":        "array",
			"description": "Array of task IDs blocked by this task.",
			"items":       map[string]any{"type": "string"},
		},
		"subtasks": subtaskArraySchema,
	}

	return []AnthropicTool{
		{
			Name:        "list_tasks",
			Description: "List current tasks or support tickets for the room, optionally filtered by sprint, status, or type.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sprint_name": map[string]any{"type": "string"},
					"status":      map[string]any{"type": "string"},
					"task_type": map[string]any{
						"type":        "string",
						"description": "Defaults to sprint. Use support for support tickets or all for both.",
					},
				},
			},
		},
		{
			Name:        "create_task",
			Description: "Create a new task in the current room.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": taskMutationProperties,
				"required":   []string{"title", "sprint_name", "budget", "start_date", "due_date", "roles"},
			},
		},
		{
			Name:        "update_task",
			Description: "Update an existing task by task_id.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": mergeSchemaProperties(
					map[string]any{
						"task_id": map[string]any{
							"type":        "string",
							"description": "Exact task UUID to update.",
						},
					},
					taskMutationProperties,
				),
				"required": []string{"task_id"},
			},
		},
		{
			Name:        "delete_task",
			Description: "Delete a task by task_id. 404 should be treated as success.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "Exact task UUID to delete.",
					},
					"task_title": map[string]any{
						"type":        "string",
						"description": "Task title for user-facing progress display.",
					},
				},
				"required": []string{"task_id", "task_title"},
			},
		},
		{
			Name:        "list_sprints",
			Description: "List derived sprint summaries from current non-support tasks.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "list_groups",
			Description: "List all named groups (sprints/phases/campaigns) in the workspace. Returns group IDs, names, dates, and task counts.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
		},
		{
			Name:        "delete_group",
			Description: "Delete a group (sprint/phase/campaign). You must specify what to do with its tasks: reassign them to another group or delete them.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"group_id": map[string]any{
						"type":        "string",
						"description": "UUID of the group to delete",
					},
					"group_name": map[string]any{
						"type":        "string",
						"description": "Name of the group (for confirmation display)",
					},
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"reassign", "delete_tasks"},
						"description": "What to do with tasks in this group",
					},
					"reassign_to_group_id": map[string]any{
						"type":        "string",
						"description": "Required when action=reassign. UUID of the group to move tasks into.",
					},
				},
				"required": []string{"group_id", "group_name", "action"},
			},
		},
		{
			Name:        "read_canvas",
			Description: "Read a single canvas file by path, or list available files when no path is provided.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "write_canvas",
			Description: "Write full content to a canvas file path.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
					"content":   map[string]any{"type": "string"},
					"description": map[string]any{
						"type":        "string",
						"description": "What changed and why.",
					},
				},
				"required": []string{"file_path", "content"},
			},
		},
		{
			Name:        "search_tasks",
			Description: "Find the most relevant tasks by fuzzy title and description search.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Defaults to 10.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "execute_canvas",
			Description: "Compile or run the mirrored canvas workspace for verification using the requested language and main file.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"language": map[string]any{
						"type":        "string",
						"description": "Execution language such as python, javascript, typescript, go, rust, java, c, or cpp.",
					},
					"main_file": map[string]any{
						"type":        "string",
						"description": "Workspace-relative file path to treat as the main entrypoint.",
					},
					"stdin": map[string]any{
						"type":        "string",
						"description": "Optional stdin payload passed to the program.",
					},
				},
				"required": []string{"language", "main_file"},
			},
		},
		{
			Name:        "verify_task_count",
			Description: "Return authoritative task, support ticket, sprint, and status counts for the current board.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (e *AgentEngine) Run(ctx context.Context, userMessage string, cfg AgentConfig) (finalText string, events []AgentEvent, err error) {
	if e == nil {
		return "", nil, fmt.Errorf("agent engine is nil")
	}
	if e.provider == nil {
		return "", nil, fmt.Errorf("agent provider is not configured")
	}
	if e.ctxBuilder == nil {
		return "", nil, fmt.Errorf("context builder is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cfg = normalizeAgentConfig(cfg)
	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	toolProvider, ok := e.provider.(ToolUseProvider)
	if !ok {
		finalText = "The configured AI provider is not tool-enabled yet, so I can't run the agentic loop."
		events = e.recordEvent(events, cfg, cfg.MaxTurns, 1, AgentEvent{
			Kind:  "done",
			Text:  finalText,
			Error: "configured provider does not implement tool use",
		})
		return finalText, events, fmt.Errorf("configured provider does not support tool use")
	}

	buildOpts := normalizeBuildOptions(cfg.ContextOptions)
	if buildOpts.TaskLimit <= 0 {
		buildOpts.TaskLimit = defaultTaskLimit
	}
	allowedToolSet, tools := resolveAgentTools(cfg.AllowedTools)
	restrictTools := cfg.AllowedTools != nil

	workspace := cfg.Workspace
	if workspace == nil {
		var buildErr error
		workspace, buildErr = e.ctxBuilder.Build(runCtx, e.roomID, e.authContext.UserID, buildOpts)
		if buildErr != nil {
			return "", nil, buildErr
		}
	}

	messages := make([]AgentProviderMessage, 0, 2)
	if strings.TrimSpace(cfg.InitialContext) != "" {
		messages = append(messages, AgentProviderMessage{
			Role: "assistant",
			Content: []AgentProviderContentBlock{
				{
					Type: "text",
					Text: strings.TrimSpace(cfg.InitialContext),
				},
			},
		})
		messages = append(messages, AgentProviderMessage{
			Role: "user",
			Content: []AgentProviderContentBlock{
				{
					Type: "text",
					Text: strings.TrimSpace(userMessage),
				},
			},
		})
	} else {
		messages = append(messages, AgentProviderMessage{
			Role: "user",
			Content: []AgentProviderContentBlock{
				{
					Type: "text",
					Text: buildAgentWorkspacePrompt(workspace, userMessage, buildOpts),
				},
			},
		})
	}

	var lastResponseText string
	for turn := 1; turn <= cfg.MaxTurns; turn++ {
		if runErr := runCtx.Err(); runErr != nil {
			finalText = gracefulAgentTimeoutMessage(runErr)
			events = e.recordEvent(events, cfg, cfg.MaxTurns, turn, AgentEvent{
				Kind:  "done",
				Text:  finalText,
				Error: runErr.Error(),
			})
			return finalText, events, nil
		}

		// Emit a pre-call event so the frontend knows the agent is active and
		// the workflow button appears immediately on turn 1.
		events = e.recordEvent(events, cfg, cfg.MaxTurns, turn, AgentEvent{
			Kind: "thinking",
			Text: "",
		})

		response, responseErr := toolProvider.GenerateToolResponse(runCtx, AgentProviderRequest{
			Model:        cfg.Model,
			SystemPrompt: cfg.SystemPrompt,
			Messages:     messages,
			Tools:        tools,
		})
		if responseErr != nil {
			if isAgentContextTimeout(runCtx, responseErr) {
				finalText = gracefulAgentTimeoutMessage(responseErr)
				events = e.recordEvent(events, cfg, cfg.MaxTurns, turn, AgentEvent{
					Kind:  "done",
					Text:  finalText,
					Error: responseErr.Error(),
				})
				return finalText, events, nil
			}
			return lastResponseText, events, responseErr
		}

		assistantBlocks := cloneAgentContentBlocks(response.Content)
		toolResults := make([]AgentProviderContentBlock, 0, len(assistantBlocks))
		turnTextParts := make([]string, 0, 2)
		sawToolUse := false

		for index := range assistantBlocks {
			block := &assistantBlocks[index]
			if strings.TrimSpace(block.Type) == "tool_use" && strings.TrimSpace(block.ID) == "" {
				block.ID = fmt.Sprintf("tool-%d-%d", turn, index+1)
			}
		}
		messages = append(messages, AgentProviderMessage{
			Role:    "assistant",
			Content: assistantBlocks,
		})

		for _, block := range assistantBlocks {
			switch strings.TrimSpace(block.Type) {
			case "thinking":
				text := strings.TrimSpace(block.Text)
				if text == "" {
					continue
				}
				events = e.recordEvent(events, cfg, cfg.MaxTurns, turn, AgentEvent{
					Kind: "thinking",
					Text: text,
				})

			case "text":
				text := strings.TrimSpace(block.Text)
				if text == "" {
					continue
				}
				turnTextParts = append(turnTextParts, text)
				events = e.recordEvent(events, cfg, cfg.MaxTurns, turn, AgentEvent{
					Kind: "text",
					Text: text,
				})

			case "tool_use":
				sawToolUse = true
				toolInput := cloneStringAnyMap(block.Input)
				events = e.recordEvent(events, cfg, cfg.MaxTurns, turn, AgentEvent{
					Kind:  "tool_call",
					Tool:  strings.TrimSpace(block.Name),
					Input: toolInput,
				})

				toolName := strings.TrimSpace(block.Name)
				var (
					result  any
					toolErr error
				)
				if restrictTools {
					if _, ok := allowedToolSet[toolName]; !ok {
						toolErr = &agentToolInputError{Message: "tool not allowed: " + toolName}
					}
				}
				if toolErr == nil {
					result, toolErr = e.executeTool(runCtx, toolName, toolInput)
				}
				if toolErr != nil {
					result = serializeToolError(toolErr)
				}
				events = e.recordEvent(events, cfg, cfg.MaxTurns, turn, AgentEvent{
					Kind:   "tool_result",
					Tool:   strings.TrimSpace(block.Name),
					Input:  toolInput,
					Result: result,
				})

				toolResults = append(toolResults, AgentProviderContentBlock{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   result,
					IsError:   isSerializedToolError(result),
				})
			}
		}

		if sawToolUse {
			if len(toolResults) > 0 {
				messages = append(messages, AgentProviderMessage{
					Role:    "user",
					Content: toolResults,
				})
			}
			if len(turnTextParts) > 0 {
				lastResponseText = strings.Join(turnTextParts, "\n\n")
			}
			continue
		}

		if len(turnTextParts) > 0 {
			finalText = strings.Join(turnTextParts, "\n\n")
		} else if strings.TrimSpace(lastResponseText) != "" {
			finalText = strings.TrimSpace(lastResponseText)
		} else if turn < cfg.MaxTurns {
			// Model returned an empty turn (no text, no tools) after finishing tool work.
			// Nudge it to produce a summary rather than returning a cryptic error.
			messages = append(messages, AgentProviderMessage{
				Role:    "user",
				Content: []AgentProviderContentBlock{{Type: "text", Text: "Please summarize what you've done."}},
			})
			continue
		} else {
			finalText = "I finished the tool loop but did not receive a final text response."
		}

		events = e.recordEvent(events, cfg, cfg.MaxTurns, turn, AgentEvent{
			Kind: "done",
			Text: finalText,
		})
		return finalText, events, nil
	}

	if strings.TrimSpace(lastResponseText) == "" {
		lastResponseText = "I reached the maximum number of agent turns before I could finish."
	}
	events = e.recordEvent(events, cfg, cfg.MaxTurns, cfg.MaxTurns, AgentEvent{
		Kind: "done",
		Text: lastResponseText,
	})
	return lastResponseText, events, nil
}

// executeTool dispatches a single tool call to the appropriate handler.
func (e *AgentEngine) executeTool(ctx context.Context, name string, input map[string]any) (any, error) {
	if e != nil && e.toolExecutor != nil {
		return e.toolExecutor(ctx, strings.TrimSpace(name), cloneStringAnyMap(input))
	}
	return e.ExecuteBuiltInTool(ctx, name, input)
}

func (e *AgentEngine) agentListTasks(ctx context.Context, input map[string]any) (any, error) {
	workspace, err := e.buildWorkspaceContext(ctx, BuildOptions{TaskLimit: defaultTaskLimit})
	if err != nil {
		return nil, err
	}

	taskType, _, err := optionalStringField(input, "task_type")
	if err != nil {
		return nil, err
	}
	taskType = strings.TrimSpace(taskType)
	if taskType == "" {
		taskType = "sprint"
	}

	sprintName, _, err := optionalStringField(input, "sprint_name")
	if err != nil {
		return nil, err
	}
	status, _, err := optionalStringField(input, "status")
	if err != nil {
		return nil, err
	}

	status = strings.TrimSpace(status)
	if status != "" {
		status = normalizeContextTaskStatus(status)
	}
	sprintKey := agentSprintNameKey(sprintName)

	var candidates []TaskCtx
	switch normalizeAgentTaskType(taskType) {
	case "support":
		candidates = append(candidates, workspace.SupportTickets...)
	case "all":
		candidates = append(candidates, workspace.Tasks...)
		candidates = append(candidates, workspace.SupportTickets...)
	default:
		candidates = append(candidates, workspace.Tasks...)
	}

	filtered := make([]TaskCtx, 0, len(candidates))
	for _, task := range candidates {
		if sprintKey != "" && agentSprintNameKey(task.SprintName) != sprintKey {
			continue
		}
		if strings.TrimSpace(status) != "" && normalizeContextTaskStatus(task.Status) != status {
			continue
		}
		filtered = append(filtered, task)
	}

	sort.SliceStable(filtered, func(i, j int) bool { return taskCtxLess(filtered[i], filtered[j]) })
	return filtered, nil
}

func (e *AgentEngine) agentCreateTask(ctx context.Context, input map[string]any) (any, error) {
	title, err := requiredStringField(input, "title")
	if err != nil {
		return nil, err
	}
	sprintName, err := requiredStringField(input, "sprint_name")
	if err != nil {
		return nil, err
	}

	description, _, err := optionalStringField(input, "description")
	if err != nil {
		return nil, err
	}
	status, _, err := optionalStringField(input, "status")
	if err != nil {
		return nil, err
	}
	taskType, _, err := optionalStringField(input, "task_type")
	if err != nil {
		return nil, err
	}
	budget, _, err := optionalFloatField(input, "budget")
	if err != nil {
		return nil, err
	}
	actualCost, _, err := optionalFloatField(input, "actual_cost")
	if err != nil {
		return nil, err
	}
	startDate, _, err := optionalTimeField(input, "start_date")
	if err != nil {
		return nil, err
	}
	dueDate, _, err := optionalTimeField(input, "due_date")
	if err != nil {
		return nil, err
	}
	roles, _, err := optionalRolesField(input, "roles")
	if err != nil {
		return nil, err
	}
	assigneeID, assigneePresent, err := optionalStringField(input, "assignee_id")
	if err != nil {
		return nil, err
	}
	customFields, _, err := optionalObjectField(input, "custom_fields")
	if err != nil {
		return nil, err
	}
	blockedBy, _, err := optionalStringSliceField(input, "blocked_by")
	if err != nil {
		return nil, err
	}
	blocks, _, err := optionalStringSliceField(input, "blocks")
	if err != nil {
		return nil, err
	}
	subtasks, _, err := optionalSubtasksField(input, "subtasks")
	if err != nil {
		return nil, err
	}

	store, roomUUID, err := e.taskStoreAndRoomUUID()
	if err != nil {
		return nil, err
	}

	taskUUID, err := gocql.RandomUUID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate task id: %w", err)
	}

	title = truncatePlainText(title, 240)
	description = applyAgentFinancialsToDescription(truncatePlainText(description, 4000), budget, actualCost)
	status = normalizeAgentTaskStatus(status)
	taskType = normalizeAgentTaskType(taskType)
	sprintName = truncatePlainText(e.canonicalizeSprintName(ctx, roomUUID, sprintName), 160)
	customFieldsJSON, err := marshalAgentCustomFields(customFields)
	if err != nil {
		return nil, err
	}
	assigneeUUID, err := e.resolveTaskAssigneeUUID(assigneeID, assigneePresent)
	if err != nil {
		return nil, err
	}
	if !assigneePresent {
		assigneeUUID = e.resolveAuthAssigneeUUID()
	}

	now := time.Now().UTC()
	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, custom_fields, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at, task_type, due_date, start_date, roles) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		store.Table("tasks"),
	)
	if err := store.Session.Query(
		query,
		roomUUID,
		taskUUID,
		title,
		description,
		status,
		sprintName,
		assigneeUUID,
		customFieldsJSON,
		nullableText(e.authContext.UserID),
		nullableText(e.authContext.UserName),
		now,
		now,
		now,
		nullableText(taskType),
		startDateOrNil(dueDate),
		startDateOrNil(startDate),
		marshalAgentRoles(roles),
	).WithContext(ctx).Exec(); err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}
	if err := e.ensureAgentTaskRelationSchema(ctx); err != nil {
		return nil, err
	}
	if err := e.replaceTaskBlockedByRelations(ctx, roomUUID, taskUUID, blockedBy); err != nil {
		return nil, err
	}
	if err := e.replaceTaskBlocksRelations(ctx, roomUUID, taskUUID, blocks); err != nil {
		return nil, err
	}
	if err := e.replaceTaskSubtasks(ctx, roomUUID, taskUUID, subtasks); err != nil {
		return nil, err
	}

	task, err := e.loadTaskByID(ctx, roomUUID, taskUUID)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (e *AgentEngine) agentUpdateTask(ctx context.Context, input map[string]any) (any, error) {
	taskIDRaw, err := requiredStringField(input, "task_id")
	if err != nil {
		return nil, err
	}
	taskUUID, err := parseFlexibleUUID(taskIDRaw)
	if err != nil {
		return nil, &agentToolInputError{
			Field:   "task_id",
			Message: "invalid field: task_id must be a valid UUID",
		}
	}

	store, roomUUID, err := e.taskStoreAndRoomUUID()
	if err != nil {
		return nil, err
	}

	existing, err := e.loadRawTaskRow(ctx, roomUUID, taskUUID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, fmt.Errorf("task not found")
		}
		return nil, err
	}

	title, titlePresent, err := optionalStringField(input, "title")
	if err != nil {
		return nil, err
	}
	description, descriptionPresent, err := optionalStringField(input, "description")
	if err != nil {
		return nil, err
	}
	status, statusPresent, err := optionalStringField(input, "status")
	if err != nil {
		return nil, err
	}
	taskType, taskTypePresent, err := optionalStringField(input, "task_type")
	if err != nil {
		return nil, err
	}
	sprintName, sprintPresent, err := optionalStringField(input, "sprint_name")
	if err != nil {
		return nil, err
	}
	budget, budgetPresent, err := optionalFloatField(input, "budget")
	if err != nil {
		return nil, err
	}
	actualCost, actualCostPresent, err := optionalFloatField(input, "actual_cost")
	if err != nil {
		return nil, err
	}
	startDate, startDatePresent, err := optionalTimeField(input, "start_date")
	if err != nil {
		return nil, err
	}
	dueDate, dueDatePresent, err := optionalTimeField(input, "due_date")
	if err != nil {
		return nil, err
	}
	roles, rolesPresent, err := optionalRolesField(input, "roles")
	if err != nil {
		return nil, err
	}
	assigneeID, assigneePresent, err := optionalStringField(input, "assignee_id")
	if err != nil {
		return nil, err
	}
	customFields, customFieldsPresent, err := optionalObjectField(input, "custom_fields")
	if err != nil {
		return nil, err
	}
	blockedBy, blockedByPresent, err := optionalStringSliceField(input, "blocked_by")
	if err != nil {
		return nil, err
	}
	blocks, blocksPresent, err := optionalStringSliceField(input, "blocks")
	if err != nil {
		return nil, err
	}
	subtasks, subtasksPresent, err := optionalSubtasksField(input, "subtasks")
	if err != nil {
		return nil, err
	}

	setClauses := make([]string, 0, 11)
	args := make([]any, 0, 16)

	if titlePresent {
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, &agentToolInputError{
				Field:   "title",
				Message: "invalid field: title cannot be empty",
			}
		}
		setClauses = append(setClauses, "title = ?")
		args = append(args, truncatePlainText(title, 240))
	}

	if descriptionPresent || budgetPresent || actualCostPresent {
		baseDescription := existing.Description
		if descriptionPresent {
			baseDescription = truncatePlainText(description, 4000)
		}
		existingCustomFields := parseJSONMap(existing.CustomFields)
		nextBudget := extractTaskBudget(existing.Description, existingCustomFields)
		if budgetPresent {
			nextBudget = budget
		}
		nextActualCost := extractTaskActualCost(existing.Description, existingCustomFields)
		if actualCostPresent {
			nextActualCost = actualCost
		}
		nextDescription := applyAgentFinancialsToDescription(baseDescription, nextBudget, nextActualCost)
		setClauses = append(setClauses, "description = ?")
		args = append(args, truncatePlainText(nextDescription, 4000))
	}

	if statusPresent {
		setClauses = append(setClauses, "status = ?")
		args = append(args, normalizeAgentTaskStatus(status))
	}
	if taskTypePresent {
		setClauses = append(setClauses, "task_type = ?")
		args = append(args, nullableText(normalizeAgentTaskType(taskType)))
	}
	if sprintPresent {
		setClauses = append(setClauses, "sprint_name = ?")
		args = append(args, nullableText(truncatePlainText(e.canonicalizeSprintName(ctx, roomUUID, sprintName), 160)))
	}
	if dueDatePresent {
		setClauses = append(setClauses, "due_date = ?")
		args = append(args, startDateOrNil(dueDate))
	}
	if startDatePresent {
		setClauses = append(setClauses, "start_date = ?")
		args = append(args, startDateOrNil(startDate))
	}
	if rolesPresent {
		setClauses = append(setClauses, "roles = ?")
		args = append(args, marshalAgentRoles(roles))
	}
	if assigneePresent {
		assigneeUUID, err := e.resolveTaskAssigneeUUID(assigneeID, true)
		if err != nil {
			return nil, err
		}
		setClauses = append(setClauses, "assignee_id = ?")
		args = append(args, assigneeUUID)
	}
	if customFieldsPresent {
		mergedCustomFields := mergeAgentCustomFields(parseJSONMap(existing.CustomFields), customFields)
		customFieldsJSON, marshalErr := marshalAgentCustomFields(mergedCustomFields)
		if marshalErr != nil {
			return nil, marshalErr
		}
		setClauses = append(setClauses, "custom_fields = ?")
		args = append(args, customFieldsJSON)
	}

	if len(setClauses) == 0 && !blockedByPresent && !blocksPresent && !subtasksPresent {
		return nil, &agentToolInputError{
			Message: "no editable fields provided",
		}
	}

	now := time.Now().UTC()
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, now, roomUUID, taskUUID)

	query := fmt.Sprintf(`UPDATE %s SET %s WHERE room_id = ? AND id = ?`, store.Table("tasks"), strings.Join(setClauses, ", "))
	if len(setClauses) > 0 {
		if err := store.Session.Query(query, args...).WithContext(ctx).Exec(); err != nil {
			return nil, fmt.Errorf("failed to update task: %w", err)
		}
	}
	if err := e.ensureAgentTaskRelationSchema(ctx); err != nil {
		return nil, err
	}
	if blockedByPresent {
		if err := e.replaceTaskBlockedByRelations(ctx, roomUUID, taskUUID, blockedBy); err != nil {
			return nil, err
		}
	}
	if blocksPresent {
		if err := e.replaceTaskBlocksRelations(ctx, roomUUID, taskUUID, blocks); err != nil {
			return nil, err
		}
	}
	if subtasksPresent {
		if err := e.replaceTaskSubtasks(ctx, roomUUID, taskUUID, subtasks); err != nil {
			return nil, err
		}
	}

	task, err := e.loadTaskByID(ctx, roomUUID, taskUUID)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (e *AgentEngine) agentDeleteTask(ctx context.Context, input map[string]any) (any, error) {
	taskIDRaw, err := requiredStringField(input, "task_id")
	if err != nil {
		return nil, err
	}
	taskTitle, err := requiredStringField(input, "task_title")
	if err != nil {
		return nil, err
	}
	taskUUID, err := parseFlexibleUUID(taskIDRaw)
	if err != nil {
		return nil, &agentToolInputError{
			Field:   "task_id",
			Message: "invalid field: task_id must be a valid UUID",
		}
	}

	store, roomUUID, err := e.taskStoreAndRoomUUID()
	if err != nil {
		return nil, err
	}

	_, loadErr := e.loadRawTaskRow(ctx, roomUUID, taskUUID)
	if loadErr != nil {
		if errors.Is(loadErr, gocql.ErrNotFound) {
			return map[string]any{
				"deleted":    true,
				"task_id":    strings.TrimSpace(taskUUID.String()),
				"task_title": taskTitle,
			}, nil
		}
		return nil, loadErr
	}

	if err := e.deleteTaskRelationsForTask(ctx, strings.TrimSpace(taskUUID.String())); err != nil {
		return nil, err
	}

	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND id = ?`, store.Table("tasks"))
	if err := store.Session.Query(deleteQuery, roomUUID, taskUUID).WithContext(ctx).Exec(); err != nil {
		return nil, fmt.Errorf("failed to delete task: %w", err)
	}

	return map[string]any{
		"deleted":    true,
		"task_id":    strings.TrimSpace(taskUUID.String()),
		"task_title": taskTitle,
	}, nil
}

func (e *AgentEngine) agentListSprints(ctx context.Context) (any, error) {
	workspace, err := e.buildWorkspaceContext(ctx, BuildOptions{TaskLimit: defaultTaskLimit})
	if err != nil {
		return nil, err
	}
	sprints := append([]SprintCtx(nil), workspace.Sprints...)
	sort.SliceStable(sprints, func(i, j int) bool { return sprintCtxLess(sprints[i], sprints[j]) })
	return sprints, nil
}

func (e *AgentEngine) agentListGroups(ctx context.Context) (any, error) {
	service := projectboard.NewService(e.scyllaStore())
	return service.ListGroupSummaries(ctx, e.roomID)
}

func (e *AgentEngine) agentDeleteGroup(ctx context.Context, input map[string]any) (any, error) {
	groupID, err := requiredStringField(input, "group_id")
	if err != nil {
		return nil, err
	}
	groupName, err := requiredStringField(input, "group_name")
	if err != nil {
		return nil, err
	}
	action, err := requiredStringField(input, "action")
	if err != nil {
		return nil, err
	}
	reassignToGroupID, _, err := optionalStringField(input, "reassign_to_group_id")
	if err != nil {
		return nil, err
	}

	service := projectboard.NewService(e.scyllaStore())
	summaries, err := service.ListGroupSummaries(ctx, e.roomID)
	if err != nil {
		return nil, err
	}

	taskCount := 0
	for _, summary := range summaries {
		if strings.EqualFold(strings.TrimSpace(summary.GroupID), strings.TrimSpace(groupID)) {
			taskCount = summary.TaskCount
			break
		}
	}

	if strings.EqualFold(strings.TrimSpace(action), "delete_tasks") && taskCount > 20 {
		return map[string]any{
			"error": fmt.Sprintf("Deleting this group would remove %d tasks. Break this into smaller steps or use action=reassign to move tasks first.", taskCount),
		}, nil
	}
	if strings.EqualFold(strings.TrimSpace(action), "reassign") && strings.TrimSpace(reassignToGroupID) == "" {
		return nil, &agentToolInputError{
			Field:   "reassign_to_group_id",
			Message: "reassign_to_group_id is required when action=reassign",
		}
	}

	result, err := service.DeleteGroup(ctx, e.roomID, groupID, projectboard.GroupDeleteRequest{
		Action:            action,
		ReassignToGroupID: reassignToGroupID,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"deleted":               true,
		"group_id":              strings.TrimSpace(groupID),
		"group_name":            strings.TrimSpace(groupName),
		"action":                strings.TrimSpace(action),
		"task_count":            result.TaskCount,
		"deleted_task_count":    result.DeletedTaskCount,
		"reassigned_task_count": result.ReassignedCount,
		"reassign_to_group_id":  strings.TrimSpace(reassignToGroupID),
	}, nil
}

func (e *AgentEngine) agentReadCanvas(ctx context.Context, input map[string]any) (any, error) {
	filePath, present, err := optionalStringField(input, "file_path")
	if err != nil {
		return nil, err
	}
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return nil, fmt.Errorf("canvas storage is not configured")
	}

	roomKeys := e.canvasRoomKeys()
	if !present || strings.TrimSpace(filePath) == "" {
		files, err := e.loadCanvasFilesForKeys(ctx, roomKeys)
		if err != nil {
			return nil, err
		}
		return files, nil
	}

	query := fmt.Sprintf(`SELECT path, language, content FROM %s WHERE room_id = ? AND path = ? LIMIT 1`, store.Table(agentCanvasFilesTable))
	for _, roomKey := range roomKeys {
		var pathValue, language, content string
		err := store.Session.Query(query, roomKey, strings.TrimSpace(filePath)).WithContext(ctx).Scan(&pathValue, &language, &content)
		if err == nil {
			_, lines := excerptFirstLines(content, 0)
			return map[string]any{
				"path":     strings.TrimSpace(pathValue),
				"language": strings.TrimSpace(language),
				"content":  content,
				"lines":    lines,
			}, nil
		}
		if errors.Is(err, gocql.ErrNotFound) {
			continue
		}
		if isMissingTableError(err) {
			return []CanvasFileCtx{}, nil
		}
		return nil, err
	}

	return nil, fmt.Errorf("canvas file not found")
}

func (e *AgentEngine) agentWriteCanvas(ctx context.Context, input map[string]any) (any, error) {
	filePath, err := requiredStringField(input, "file_path")
	if err != nil {
		return nil, err
	}
	content, contentPresent, err := optionalStringField(input, "content")
	if err != nil {
		return nil, err
	}
	if !contentPresent {
		return nil, &agentToolInputError{
			Field:   "content",
			Message: "missing required field: content",
		}
	}
	_, _, err = optionalStringField(input, "description")
	if err != nil {
		return nil, err
	}

	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return nil, fmt.Errorf("canvas storage is not configured")
	}
	if err := ensureAgentCanvasSchema(ctx, store); err != nil {
		return nil, err
	}

	roomKey := normalizeContextRoomID(e.roomID)
	if roomKey == "" {
		roomKey = strings.TrimSpace(e.roomID)
	}
	now := time.Now().UTC()
	language := inferCanvasLanguage(filePath)

	query := fmt.Sprintf(`INSERT INTO %s (room_id, path, language, content, updated_at) VALUES (?, ?, ?, ?, ?)`, store.Table(agentCanvasFilesTable))
	if err := store.Session.Query(
		query,
		roomKey,
		strings.TrimSpace(filePath),
		nullableText(language),
		content,
		now,
	).WithContext(ctx).Exec(); err != nil {
		return nil, fmt.Errorf("failed to write canvas file: %w", err)
	}

	_, lines := excerptFirstLines(content, 0)
	return map[string]any{
		"written": true,
		"path":    strings.TrimSpace(filePath),
		"lines":   lines,
	}, nil
}

func (e *AgentEngine) agentSearchTasks(ctx context.Context, input map[string]any) (any, error) {
	query, err := requiredStringField(input, "query")
	if err != nil {
		return nil, err
	}
	limit, present, err := optionalIntField(input, "limit")
	if err != nil {
		return nil, err
	}
	if !present || limit <= 0 {
		limit = 10
	}

	workspace, err := e.buildWorkspaceContext(ctx, BuildOptions{TaskLimit: defaultTaskLimit})
	if err != nil {
		return nil, err
	}

	allTasks := make([]TaskCtx, 0, len(workspace.Tasks)+len(workspace.SupportTickets))
	allTasks = append(allTasks, workspace.Tasks...)
	allTasks = append(allTasks, workspace.SupportTickets...)

	results := searchTaskContexts(allTasks, query, limit)
	return results, nil
}

func (e *AgentEngine) agentExecuteCanvas(ctx context.Context, input map[string]any) (any, error) {
	language, err := requiredStringField(input, "language")
	if err != nil {
		return nil, err
	}
	mainFile, err := requiredStringField(input, "main_file")
	if err != nil {
		return nil, err
	}
	stdin, _, err := optionalStringField(input, "stdin")
	if err != nil {
		return nil, err
	}

	files, err := e.loadCanvasWorkspaceFiles(ctx)
	if err != nil {
		return nil, err
	}
	return executeCanvasWorkspace(ctx, language, mainFile, stdin, files)
}

func (e *AgentEngine) agentVerifyTaskCount(ctx context.Context) (any, error) {
	workspace, err := e.buildWorkspaceContext(ctx, BuildOptions{TaskLimit: defaultTaskLimit})
	if err != nil {
		return nil, err
	}

	bySprint := make(map[string]int)
	byStatus := make(map[string]int)
	for _, task := range workspace.Tasks {
		sprintName := strings.TrimSpace(task.SprintName)
		if sprintName == "" {
			sprintName = "(No Sprint)"
		}
		bySprint[sprintName]++

		status := normalizeAgentTaskStatus(task.Status)
		if status == "" {
			status = "todo"
		}
		byStatus[status]++
	}

	return map[string]any{
		"total_tasks":     len(workspace.Tasks),
		"support_tickets": len(workspace.SupportTickets),
		"sprint_count":    len(bySprint),
		"group_count":     len(bySprint),
		"by_sprint":       bySprint,
		"by_status":       byStatus,
	}, nil
}

func (e *AgentEngine) buildWorkspaceContext(ctx context.Context, opts BuildOptions) (*WorkspaceContext, error) {
	if e == nil || e.ctxBuilder == nil {
		return nil, fmt.Errorf("context builder is not configured")
	}
	return e.ctxBuilder.Build(ctx, e.roomID, e.authContext.UserID, normalizeBuildOptions(opts))
}

func (e *AgentEngine) taskStoreAndRoomUUID() (*database.ScyllaStore, gocql.UUID, error) {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return nil, gocql.UUID{}, fmt.Errorf("task storage is not configured")
	}
	roomUUID, _, err := resolveContextTaskRoomUUID(e.roomID)
	if err != nil {
		return nil, gocql.UUID{}, err
	}
	return store, roomUUID, nil
}

func (e *AgentEngine) scyllaStore() *database.ScyllaStore {
	if e == nil || e.ctxBuilder == nil {
		return nil
	}
	return e.ctxBuilder.scylla
}

func (e *AgentEngine) canonicalizeSprintName(ctx context.Context, roomUUID gocql.UUID, candidate string) string {
	store := e.scyllaStore()
	trimmed := strings.TrimSpace(candidate)
	if store == nil || store.Session == nil || trimmed == "" {
		return trimmed
	}

	query := fmt.Sprintf(`SELECT sprint_name FROM %s WHERE room_id = ?`, store.Table("tasks"))
	iter := store.Session.Query(query, roomUUID).WithContext(ctx).Iter()
	key := agentSprintNameKey(trimmed)
	var stored string
	for iter.Scan(&stored) {
		stored = strings.TrimSpace(stored)
		if stored != "" && agentSprintNameKey(stored) == key {
			_ = iter.Close()
			return stored
		}
	}
	_ = iter.Close()
	return trimmed
}

func (e *AgentEngine) resolveAuthAssigneeUUID() *gocql.UUID {
	userID := strings.TrimSpace(e.authContext.UserID)
	if userID == "" {
		return nil
	}
	parsed, err := parseFlexibleUUID(userID)
	if err != nil {
		return nil
	}
	return &parsed
}

type agentRawTaskRow struct {
	ID           gocql.UUID
	Title        string
	Description  string
	Status       string
	TaskType     string
	SprintName   string
	AssigneeID   *gocql.UUID
	CustomFields *string
	DueDate      *time.Time
	StartDate    *time.Time
	RolesRaw     *string
	UpdatedAt    time.Time
}

type agentSubtaskInput struct {
	Content   string
	Completed bool
}

func (e *AgentEngine) loadRawTaskRow(ctx context.Context, roomUUID gocql.UUID, taskUUID gocql.UUID) (*agentRawTaskRow, error) {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return nil, fmt.Errorf("task storage is not configured")
	}

	query := fmt.Sprintf(
		`SELECT id, title, description, status, task_type, sprint_name, assignee_id, custom_fields, due_date, start_date, roles, updated_at FROM %s WHERE room_id = ? AND id = ? LIMIT 1`,
		store.Table("tasks"),
	)

	row := &agentRawTaskRow{}
	if err := store.Session.Query(query, roomUUID, taskUUID).WithContext(ctx).Scan(
		&row.ID,
		&row.Title,
		&row.Description,
		&row.Status,
		&row.TaskType,
		&row.SprintName,
		&row.AssigneeID,
		&row.CustomFields,
		&row.DueDate,
		&row.StartDate,
		&row.RolesRaw,
		&row.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return row, nil
}

func (e *AgentEngine) loadTaskByID(ctx context.Context, roomUUID gocql.UUID, taskUUID gocql.UUID) (*TaskCtx, error) {
	row, err := e.loadRawTaskRow(ctx, roomUUID, taskUUID)
	if err != nil {
		return nil, err
	}

	assigneeName := strings.TrimSpace(e.authContext.UserName)
	if row.AssigneeID != nil && e.ctxBuilder != nil {
		names, nameErr := e.ctxBuilder.loadUserDisplayNames(ctx, []string{strings.TrimSpace(row.AssigneeID.String())})
		if nameErr == nil {
			assigneeName = strings.TrimSpace(names[strings.TrimSpace(row.AssigneeID.String())])
		}
	}

	customFields := parseJSONMap(row.CustomFields)
	task := &TaskCtx{
		ID:           strings.TrimSpace(row.ID.String()),
		Title:        strings.TrimSpace(row.Title),
		Description:  strings.TrimSpace(row.Description),
		Status:       normalizeAgentTaskStatus(row.Status),
		TaskType:     normalizeAgentTaskType(row.TaskType),
		SprintName:   strings.TrimSpace(row.SprintName),
		AssigneeName: assigneeName,
		Budget:       extractTaskBudget(row.Description, customFields),
		ActualCost:   extractTaskActualCost(row.Description, customFields),
		StartDate:    cloneTimePtr(row.StartDate),
		DueDate:      cloneTimePtr(row.DueDate),
		Roles:        parseRoleContexts(row.RolesRaw),
		CustomFields: cloneStringAnyMap(customFields),
		UpdatedAt:    row.UpdatedAt.UTC(),
	}
	if row.AssigneeID != nil {
		task.AssigneeID = strings.TrimSpace(row.AssigneeID.String())
	}
	relations, relationErr := e.loadTaskRelationSnapshot(ctx, roomUUID)
	if relationErr == nil {
		task.Subtasks = cloneSubtasks(relations.Subtasks[task.ID])
		task.BlockedBy = cloneStringSlice(relations.BlockedBy[task.ID])
		task.Blocks = cloneStringSlice(relations.Blocks[task.ID])
	}
	return task, nil
}

func (e *AgentEngine) ensureAgentTaskRelationSchema(ctx context.Context) error {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return fmt.Errorf("task relation storage is not configured")
	}

	tableName := store.Table("task_relations")
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
	if err := store.Session.Query(createQuery).WithContext(ctx).Exec(); err != nil && !isAgentSchemaAlreadyAppliedError(err) {
		return err
	}
	indexQuery := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (to_task_id)`, tableName)
	if err := store.Session.Query(indexQuery).WithContext(ctx).Exec(); err != nil && !isAgentSchemaAlreadyAppliedError(err) {
		return err
	}
	return nil
}

func (e *AgentEngine) loadTaskRelationSnapshot(ctx context.Context, roomUUID gocql.UUID) (contextTaskRelationSnapshot, error) {
	if e == nil || e.ctxBuilder == nil {
		return contextTaskRelationSnapshot{}, fmt.Errorf("context builder is not configured")
	}
	return e.ctxBuilder.loadTaskRelations(ctx, roomUUID)
}

func (e *AgentEngine) replaceTaskBlockedByRelations(ctx context.Context, roomUUID gocql.UUID, taskUUID gocql.UUID, blockedBy []string) error {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return fmt.Errorf("task relation storage is not configured")
	}
	roomKey := strings.TrimSpace(roomUUID.String())
	taskID := strings.TrimSpace(taskUUID.String())
	if roomKey == "" || taskID == "" {
		return nil
	}

	if err := e.deleteTaskRelationsByType(ctx, roomKey, taskID, "blocked_by"); err != nil {
		return err
	}
	normalized, err := normalizeAgentRelationTaskIDs(blockedBy, "blocked_by")
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return nil
	}
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, from_task_id, to_task_id, relation_type, position, content, completed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		store.Table("task_relations"),
	)
	now := time.Now().UTC()
	for _, blockedTaskID := range normalized {
		if blockedTaskID == taskID {
			continue
		}
		if err := store.Session.Query(insertQuery, roomKey, blockedTaskID, taskID, "blocked_by", 0, nil, false, now).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (e *AgentEngine) replaceTaskBlocksRelations(ctx context.Context, roomUUID gocql.UUID, taskUUID gocql.UUID, blocks []string) error {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return fmt.Errorf("task relation storage is not configured")
	}
	roomKey := strings.TrimSpace(roomUUID.String())
	taskID := strings.TrimSpace(taskUUID.String())
	if roomKey == "" || taskID == "" {
		return nil
	}
	normalized, err := normalizeAgentRelationTaskIDs(blocks, "blocks")
	if err != nil {
		return err
	}
	if err := e.deleteIncomingBlockedRelations(ctx, roomKey, taskID); err != nil {
		return err
	}
	if len(normalized) == 0 {
		return nil
	}
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, from_task_id, to_task_id, relation_type, position, content, completed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		store.Table("task_relations"),
	)
	now := time.Now().UTC()
	for _, blockedTaskID := range normalized {
		if blockedTaskID == taskID {
			continue
		}
		if err := store.Session.Query(insertQuery, roomKey, taskID, blockedTaskID, "blocked_by", 0, nil, false, now).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (e *AgentEngine) replaceTaskSubtasks(ctx context.Context, roomUUID gocql.UUID, taskUUID gocql.UUID, subtasks []agentSubtaskInput) error {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return fmt.Errorf("task relation storage is not configured")
	}
	roomKey := strings.TrimSpace(roomUUID.String())
	taskID := strings.TrimSpace(taskUUID.String())
	if roomKey == "" || taskID == "" {
		return nil
	}
	if err := e.deleteTaskRelationsByType(ctx, roomKey, taskID, "subtask"); err != nil {
		return err
	}
	subtasks = sanitizeAgentSubtasks(subtasks)
	if len(subtasks) == 0 {
		return nil
	}
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, from_task_id, to_task_id, relation_type, position, content, completed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		store.Table("task_relations"),
	)
	now := time.Now().UTC()
	for index, subtask := range subtasks {
		subtaskID, err := gocql.RandomUUID()
		if err != nil {
			return fmt.Errorf("failed to generate subtask id: %w", err)
		}
		if err := store.Session.Query(
			insertQuery,
			roomKey,
			taskID,
			strings.TrimSpace(subtaskID.String()),
			"subtask",
			index,
			subtask.Content,
			subtask.Completed,
			now,
		).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (e *AgentEngine) deleteTaskRelationsByType(ctx context.Context, roomKey string, fromTaskID string, relationType string) error {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return fmt.Errorf("task relation storage is not configured")
	}
	selectQuery := fmt.Sprintf(`SELECT to_task_id, relation_type FROM %s WHERE room_id = ? AND from_task_id = ?`, store.Table("task_relations"))
	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND from_task_id = ? AND to_task_id = ?`, store.Table("task_relations"))
	iter := store.Session.Query(selectQuery, roomKey, fromTaskID).WithContext(ctx).Iter()
	toDelete := make([]string, 0, 8)
	var toTaskID, storedType string
	for iter.Scan(&toTaskID, &storedType) {
		if strings.TrimSpace(storedType) != relationType {
			continue
		}
		toDelete = append(toDelete, strings.TrimSpace(toTaskID))
	}
	if err := iter.Close(); err != nil {
		if isMissingTableError(err) {
			return nil
		}
		return err
	}
	for _, targetTaskID := range toDelete {
		if targetTaskID == "" {
			continue
		}
		if err := store.Session.Query(deleteQuery, roomKey, fromTaskID, targetTaskID).WithContext(ctx).Exec(); err != nil && !isMissingTableError(err) {
			return err
		}
	}
	return nil
}

func (e *AgentEngine) deleteIncomingBlockedRelations(ctx context.Context, roomKey string, toTaskID string) error {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return fmt.Errorf("task relation storage is not configured")
	}
	selectQuery := fmt.Sprintf(`SELECT from_task_id, to_task_id, relation_type FROM %s WHERE room_id = ?`, store.Table("task_relations"))
	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND from_task_id = ? AND to_task_id = ?`, store.Table("task_relations"))
	iter := store.Session.Query(selectQuery, roomKey).WithContext(ctx).Iter()
	type relationKey struct {
		FromTaskID string
		ToTaskID   string
	}
	toDelete := make([]relationKey, 0, 8)
	var fromTaskID, targetTaskID, relationType string
	for iter.Scan(&fromTaskID, &targetTaskID, &relationType) {
		if strings.TrimSpace(relationType) != "blocked_by" {
			continue
		}
		if strings.TrimSpace(targetTaskID) != toTaskID {
			continue
		}
		toDelete = append(toDelete, relationKey{
			FromTaskID: strings.TrimSpace(fromTaskID),
			ToTaskID:   strings.TrimSpace(targetTaskID),
		})
	}
	if err := iter.Close(); err != nil {
		if isMissingTableError(err) {
			return nil
		}
		return err
	}
	for _, relation := range toDelete {
		if relation.FromTaskID == "" || relation.ToTaskID == "" {
			continue
		}
		if err := store.Session.Query(deleteQuery, roomKey, relation.FromTaskID, relation.ToTaskID).WithContext(ctx).Exec(); err != nil && !isMissingTableError(err) {
			return err
		}
	}
	return nil
}

func (e *AgentEngine) deleteTaskRelationsForTask(ctx context.Context, taskID string) error {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return fmt.Errorf("task relation storage is not configured")
	}

	roomKeys := e.taskRelationRoomKeys()
	tableName := store.Table("task_relations")
	deleteOutgoingQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND from_task_id = ?`, tableName)
	selectQuery := fmt.Sprintf(`SELECT from_task_id, to_task_id, relation_type FROM %s WHERE room_id = ?`, tableName)
	deleteRelationQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND from_task_id = ? AND to_task_id = ?`, tableName)

	for _, roomKey := range roomKeys {
		if err := store.Session.Query(deleteOutgoingQuery, roomKey, taskID).WithContext(ctx).Exec(); err != nil && !isMissingTableError(err) {
			return err
		}

		iter := store.Session.Query(selectQuery, roomKey).WithContext(ctx).Iter()
		type relationKey struct {
			fromTaskID string
			toTaskID   string
		}
		toDelete := make([]relationKey, 0, 8)
		var fromTaskID, toTaskID, relationType string
		for iter.Scan(&fromTaskID, &toTaskID, &relationType) {
			if strings.TrimSpace(relationType) != "blocked_by" {
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
			if isMissingTableError(err) {
				return nil
			}
			return err
		}

		for _, key := range toDelete {
			if key.fromTaskID == "" || key.toTaskID == "" {
				continue
			}
			if err := store.Session.Query(deleteRelationQuery, roomKey, key.fromTaskID, key.toTaskID).WithContext(ctx).Exec(); err != nil {
				return err
			}
		}
	}

	return nil
}

func (e *AgentEngine) taskRelationRoomKeys() []string {
	roomUUID, normalizedRoomID, err := resolveContextTaskRoomUUID(e.roomID)
	keys := []string{
		strings.TrimSpace(e.roomID),
		normalizeContextRoomID(e.roomID),
	}
	if err == nil {
		if roomUUID != (gocql.UUID{}) {
			keys = append(keys, strings.TrimSpace(roomUUID.String()))
		}
		keys = append(keys, strings.TrimSpace(normalizedRoomID))
	}
	return uniqueNonEmptyStrings(keys)
}

func (e *AgentEngine) canvasRoomKeys() []string {
	return uniqueNonEmptyStrings([]string{
		normalizeContextRoomID(e.roomID),
		strings.TrimSpace(e.roomID),
	})
}

func (e *AgentEngine) loadCanvasFilesForKeys(ctx context.Context, roomKeys []string) ([]CanvasFileCtx, error) {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return nil, fmt.Errorf("canvas storage is not configured")
	}

	allFiles := make([]CanvasFileCtx, 0, 16)
	seen := make(map[string]struct{}, 16)
	query := fmt.Sprintf(`SELECT path, language, content FROM %s WHERE room_id = ?`, store.Table(agentCanvasFilesTable))

	for _, roomKey := range roomKeys {
		iter := store.Session.Query(query, roomKey).WithContext(ctx).Iter()
		var pathValue, language, content string
		for iter.Scan(&pathValue, &language, &content) {
			pathValue = strings.TrimSpace(pathValue)
			if pathValue == "" {
				continue
			}
			if _, exists := seen[pathValue]; exists {
				continue
			}
			seen[pathValue] = struct{}{}
			excerpt, lines := excerptFirstLines(content, defaultCanvasExcerptLines)
			allFiles = append(allFiles, CanvasFileCtx{
				Path:     pathValue,
				Language: strings.TrimSpace(language),
				Lines:    lines,
				Excerpt:  excerpt,
			})
		}
		if err := iter.Close(); err != nil {
			if isMissingTableError(err) {
				return nil, nil
			}
			return nil, err
		}
	}

	sort.SliceStable(allFiles, func(i, j int) bool {
		return compareFold(allFiles[i].Path, allFiles[j].Path) < 0
	})
	return allFiles, nil
}

func (e *AgentEngine) loadCanvasWorkspaceFiles(ctx context.Context) ([]execution.ExecutionFile, error) {
	store := e.scyllaStore()
	if store == nil || store.Session == nil {
		return nil, fmt.Errorf("canvas storage is not configured")
	}

	files := make([]execution.ExecutionFile, 0, 16)
	seen := make(map[string]struct{}, 16)
	query := fmt.Sprintf(`SELECT path, content FROM %s WHERE room_id = ?`, store.Table(agentCanvasFilesTable))

	for _, roomKey := range e.canvasRoomKeys() {
		iter := store.Session.Query(query, roomKey).WithContext(ctx).Iter()
		var pathValue string
		var content string
		for iter.Scan(&pathValue, &content) {
			pathValue = strings.TrimSpace(pathValue)
			if pathValue == "" {
				continue
			}
			if _, ok := seen[pathValue]; ok {
				continue
			}
			seen[pathValue] = struct{}{}
			files = append(files, execution.ExecutionFile{
				Name:    pathValue,
				Content: content,
			})
		}
		if err := iter.Close(); err != nil {
			if isMissingTableError(err) {
				return nil, nil
			}
			return nil, err
		}
	}

	sort.SliceStable(files, func(i, j int) bool {
		return compareFold(files[i].Name, files[j].Name) < 0
	})
	return files, nil
}

func executeCanvasWorkspace(
	ctx context.Context,
	language string,
	mainFile string,
	stdin string,
	files []execution.ExecutionFile,
) (map[string]any, error) {
	language = strings.TrimSpace(language)
	mainFile = strings.TrimSpace(mainFile)
	if language == "" {
		return nil, &agentToolInputError{Field: "language", Message: "missing required field: language"}
	}
	if mainFile == "" {
		return nil, &agentToolInputError{Field: "main_file", Message: "missing required field: main_file"}
	}
	if len(files) == 0 {
		return map[string]any{
			"ok":        false,
			"language":  language,
			"main_file": mainFile,
			"error":     "canvas workspace is empty",
		}, nil
	}

	normalizedMainFile := strings.Trim(strings.ReplaceAll(mainFile, "\\", "/"), "/")
	fileList := make([]execution.ExecutionFile, 0, len(files))
	hasMainFile := false
	for _, file := range files {
		name := strings.Trim(strings.ReplaceAll(strings.TrimSpace(file.Name), "\\", "/"), "/")
		if name == "" {
			continue
		}
		fileList = append(fileList, execution.ExecutionFile{
			Name:    name,
			Content: file.Content,
		})
		if name == normalizedMainFile {
			hasMainFile = true
		}
	}
	if len(fileList) == 0 {
		return map[string]any{
			"ok":        false,
			"language":  language,
			"main_file": normalizedMainFile,
			"error":     "canvas workspace is empty",
		}, nil
	}
	if !hasMainFile {
		return map[string]any{
			"ok":        false,
			"language":  language,
			"main_file": normalizedMainFile,
			"error":     "main_file was not found in the mirrored canvas workspace",
		}, nil
	}

	manager := getAgentExecutionManager()
	response, err := manager.Execute(ctx, execution.ExecutionRequest{
		Language: language,
		MainFile: normalizedMainFile,
		Files:    fileList,
		Stdin:    stdin,
	})
	if err != nil {
		result := map[string]any{
			"ok":        false,
			"language":  language,
			"main_file": normalizedMainFile,
			"error":     strings.TrimSpace(err.Error()),
		}
		if len(response.Body) > 0 {
			result["raw_response"] = string(response.Body)
		}
		return result, nil
	}

	parsed := map[string]any{
		"ok":          true,
		"language":    language,
		"main_file":   normalizedMainFile,
		"status_code": response.StatusCode,
	}
	if len(response.Body) == 0 {
		return parsed, nil
	}

	var envelope map[string]any
	if err := json.Unmarshal(response.Body, &envelope); err == nil && len(envelope) > 0 {
		for _, key := range []string{"stdout", "stderr"} {
			if value := strings.TrimSpace(auditStringField(envelope, key)); value != "" {
				parsed[key] = value
			}
		}
		if compileMap, ok := envelope["compile"].(map[string]any); ok {
			if value := strings.TrimSpace(auditStringField(compileMap, "stdout")); value != "" {
				parsed["compile_stdout"] = value
			}
			if value := strings.TrimSpace(auditStringField(compileMap, "stderr")); value != "" {
				parsed["compile_stderr"] = value
			}
		}
		if runMap, ok := envelope["run"].(map[string]any); ok {
			if value := strings.TrimSpace(auditStringField(runMap, "stdout")); value != "" {
				parsed["run_stdout"] = value
			}
			if value := strings.TrimSpace(auditStringField(runMap, "stderr")); value != "" {
				parsed["run_stderr"] = value
			}
		}
		if len(parsed) > 0 {
			return parsed, nil
		}
	}

	parsed["raw_response"] = string(response.Body)
	return parsed, nil
}

func ExecuteCanvasWorkspace(
	ctx context.Context,
	language string,
	mainFile string,
	stdin string,
	files []execution.ExecutionFile,
) (map[string]any, error) {
	return executeCanvasWorkspace(ctx, language, mainFile, stdin, files)
}

func (e *AgentEngine) LoadCanvasExecutionFiles(ctx context.Context) ([]execution.ExecutionFile, error) {
	return e.loadCanvasWorkspaceFiles(ctx)
}

func ensureAgentCanvasSchema(ctx context.Context, store *database.ScyllaStore) error {
	if store == nil || store.Session == nil {
		return fmt.Errorf("canvas storage is not configured")
	}

	createQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id text,
		path text,
		language text,
		content text,
		updated_at timestamp,
		PRIMARY KEY (room_id, path)
	) WITH CLUSTERING ORDER BY (path ASC)`, store.Table(agentCanvasFilesTable))
	if err := store.Session.Query(createQuery).WithContext(ctx).Exec(); err != nil && !isAgentSchemaAlreadyAppliedError(err) {
		return err
	}
	return nil
}

func isAgentSchemaAlreadyAppliedError(err error) bool {
	if err == nil {
		return false
	}
	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "already exists") ||
		strings.Contains(lowered, "conflicts with an existing column") ||
		strings.Contains(lowered, "duplicate")
}

func normalizeAgentConfig(cfg AgentConfig) AgentConfig {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = defaultAgentMaxTurns
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultAgentTimeout
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = defaultAgentModel
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = defaultAgentSystemPrompt
	}
	cfg.ContextOptions = normalizeBuildOptions(cfg.ContextOptions)
	return cfg
}

type promptToolUseCall struct {
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type promptToolUseEnvelope struct {
	Thinking  string              `json:"thinking,omitempty"`
	Text      string              `json:"text,omitempty"`
	FinalText string              `json:"final_text,omitempty"`
	ToolCalls []promptToolUseCall `json:"tool_calls,omitempty"`
}

func buildPromptToolUseRequest(req AgentProviderRequest) string {
	var builder strings.Builder
	builder.WriteString("You are running an agentic tool loop for Converse.\n")
	builder.WriteString("Return only JSON. Do not use markdown fences.\n")
	builder.WriteString("If you need tools, return tool_calls. If you are done, return final_text.\n")

	if strings.TrimSpace(req.SystemPrompt) != "" {
		builder.WriteString("\nSYSTEM PROMPT\n")
		builder.WriteString(strings.TrimSpace(req.SystemPrompt))
		builder.WriteByte('\n')
	}

	if len(req.Tools) > 0 {
		encodedTools, _ := json.MarshalIndent(req.Tools, "", "  ")
		builder.WriteString("\nAVAILABLE TOOLS\n")
		builder.Write(encodedTools)
		builder.WriteByte('\n')
	}

	builder.WriteString("\nCONVERSATION\n")
	for index, message := range req.Messages {
		builder.WriteString(fmt.Sprintf("%d. %s\n", index+1, strings.ToUpper(strings.TrimSpace(message.Role))))
		for _, block := range message.Content {
			switch strings.TrimSpace(block.Type) {
			case "thinking":
				if text := strings.TrimSpace(block.Text); text != "" {
					builder.WriteString("THINKING: ")
					builder.WriteString(text)
					builder.WriteByte('\n')
				}
			case "text":
				if text := strings.TrimSpace(block.Text); text != "" {
					builder.WriteString("TEXT: ")
					builder.WriteString(text)
					builder.WriteByte('\n')
				}
			case "tool_use":
				encodedInput, _ := json.Marshal(block.Input)
				builder.WriteString("TOOL_CALL: ")
				builder.WriteString(strings.TrimSpace(block.Name))
				if strings.TrimSpace(block.ID) != "" {
					builder.WriteString(" id=")
					builder.WriteString(strings.TrimSpace(block.ID))
				}
				builder.WriteString(" input=")
				builder.Write(encodedInput)
				builder.WriteByte('\n')
			case "tool_result":
				encodedResult, _ := json.Marshal(block.Content)
				builder.WriteString("TOOL_RESULT")
				if strings.TrimSpace(block.ToolUseID) != "" {
					builder.WriteString(" for=")
					builder.WriteString(strings.TrimSpace(block.ToolUseID))
				}
				builder.WriteString(": ")
				builder.Write(encodedResult)
				builder.WriteByte('\n')
			}
		}
		builder.WriteByte('\n')
	}

	builder.WriteString("RESPONSE JSON SCHEMA\n")
	builder.WriteString(`{
  "thinking": "short planning note",
  "text": "optional short user-visible status before tool calls",
  "tool_calls": [
    {
      "id": "call_1",
      "name": "list_tasks",
      "input": {}
    }
  ],
  "final_text": "only when you are completely done"
}` + "\n")
	builder.WriteString("Rules:\n")
	builder.WriteString("- Return valid JSON only. No markdown fences, no extra text.\n")
	builder.WriteString("- Use exact tool names from AVAILABLE TOOLS.\n")
	builder.WriteString("- Every tool call input MUST include ALL required fields from the tool's input_schema. Do not skip any required field.\n")
	builder.WriteString("- Zero-input tools must still be emitted with \"input\": {}.\n")
	builder.WriteString("- If tool_calls is non-empty, final_text must be empty.\n")
	builder.WriteString("- If you are done, tool_calls must be empty and final_text must be set.\n")
	builder.WriteString("- When creating tasks: always include title, sprint_name, budget, start_date, due_date, and roles in the input.\n")
	return builder.String()
}

func parsePromptToolUseResponse(raw string) (AgentProviderResponse, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return AgentProviderResponse{}, errors.New("empty tool-use response")
	}

	jsonPayload := extractPromptToolUseJSON(trimmed)
	var envelope promptToolUseEnvelope
	decoder := json.NewDecoder(strings.NewReader(jsonPayload))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return AgentProviderResponse{
			Content: []AgentProviderContentBlock{
				{Type: "text", Text: trimmed},
			},
		}, nil
	}

	blocks := make([]AgentProviderContentBlock, 0, len(envelope.ToolCalls)+2)
	if thinking := strings.TrimSpace(envelope.Thinking); thinking != "" {
		blocks = append(blocks, AgentProviderContentBlock{
			Type: "thinking",
			Text: thinking,
		})
	}
	if len(envelope.ToolCalls) > 0 {
		if text := strings.TrimSpace(envelope.Text); text != "" {
			blocks = append(blocks, AgentProviderContentBlock{
				Type: "text",
				Text: text,
			})
		}
	}
	for index, call := range envelope.ToolCalls {
		toolName := strings.TrimSpace(call.Name)
		if toolName == "" {
			continue
		}
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = fmt.Sprintf("prompt_call_%d", index+1)
		}
		toolInput := cloneStringAnyMap(call.Input)
		if toolInput == nil {
			toolInput = map[string]any{}
		}
		blocks = append(blocks, AgentProviderContentBlock{
			Type:  "tool_use",
			ID:    callID,
			Name:  toolName,
			Input: toolInput,
		})
	}

	if len(envelope.ToolCalls) == 0 {
		finalText := strings.TrimSpace(envelope.FinalText)
		if finalText == "" {
			finalText = strings.TrimSpace(envelope.Text)
		}
		if finalText == "" {
			finalText = trimmed
		}
		blocks = append(blocks, AgentProviderContentBlock{
			Type: "text",
			Text: finalText,
		})
	} else if len(blocks) == 0 {
		finalText := strings.TrimSpace(envelope.FinalText)
		if finalText == "" {
			finalText = strings.TrimSpace(envelope.Text)
		}
		if finalText == "" {
			finalText = trimmed
		}
		blocks = append(blocks, AgentProviderContentBlock{
			Type: "text",
			Text: finalText,
		})
	}

	return AgentProviderResponse{Content: blocks}, nil
}

func extractPromptToolUseJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		if end := strings.LastIndex(trimmed, "```"); end >= 0 {
			trimmed = trimmed[:end]
		}
		trimmed = strings.TrimSpace(trimmed)
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(trimmed[start : end+1])
	}
	return trimmed
}

func buildAgentWorkspacePrompt(workspace *WorkspaceContext, userMessage string, opts BuildOptions) string {
	var builder strings.Builder
	builder.WriteString("WORKSPACE CONTEXT\n")
	if workspace != nil {
		builder.WriteString("Room ID: ")
		builder.WriteString(strings.TrimSpace(workspace.RoomID))
		builder.WriteByte('\n')
		if strings.TrimSpace(workspace.RoomName) != "" {
			builder.WriteString("Room Name: ")
			builder.WriteString(strings.TrimSpace(workspace.RoomName))
			builder.WriteByte('\n')
		}
		if requester := formatUserLabel(workspace.RequestedBy); requester != "" {
			builder.WriteString("Requested By: ")
			builder.WriteString(requester)
			builder.WriteByte('\n')
		}
		rendered := strings.TrimSpace(workspace.RenderForAI(opts))
		if rendered != "" {
			builder.WriteByte('\n')
			builder.WriteString(rendered)
		}
	}
	builder.WriteString("\n\nUSER REQUEST\n")
	builder.WriteString(strings.TrimSpace(userMessage))
	return strings.TrimSpace(builder.String())
}

func (e *AgentEngine) recordEvent(events []AgentEvent, cfg AgentConfig, totalTurns int, turn int, event AgentEvent) []AgentEvent {
	event.Turn = turn
	event.TotalTurns = totalTurns
	if event.Timestamp <= 0 {
		event.Timestamp = time.Now().UTC().UnixMilli()
	}
	if strings.TrimSpace(event.OriginMessageID) == "" {
		event.OriginMessageID = strings.TrimSpace(cfg.OriginMessageID)
	}
	if strings.TrimSpace(event.WorkflowKind) == "" {
		event.WorkflowKind = strings.TrimSpace(cfg.WorkflowKind)
	}
	events = append(events, event)
	if e != nil && e.broadcaster != nil {
		payload := map[string]interface{}{
			"type":            "tora_agent_event",
			"kind":            event.Kind,
			"tool":            event.Tool,
			"input":           event.Input,
			"result":          event.Result,
			"text":            event.Text,
			"turn":            event.Turn,
			"totalTurns":      event.TotalTurns,
			"timestamp":       event.Timestamp,
			"originMessageId": event.OriginMessageID,
			"workflowKind":    event.WorkflowKind,
		}
		if strings.TrimSpace(event.Error) != "" {
			payload["error"] = event.Error
		}
		e.broadcaster.BroadcastToRoom(strings.TrimSpace(e.roomID), payload)
	}
	if cfg.StreamCallback != nil {
		cfg.StreamCallback(event)
	}
	return events
}

func cloneAgentContentBlocks(blocks []AgentProviderContentBlock) []AgentProviderContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	cloned := make([]AgentProviderContentBlock, len(blocks))
	copy(cloned, blocks)
	for index := range cloned {
		cloned[index].Input = cloneStringAnyMap(cloned[index].Input)
	}
	return cloned
}

func cloneStringAnyMap(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		cloned[trimmed] = value
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func getAgentExecutionManager() *execution.ExecutionManager {
	agentExecutionManagerOnce.Do(func() {
		agentExecutionManager = execution.NewExecutionManager()
	})
	return agentExecutionManager
}

func gracefulAgentTimeoutMessage(err error) string {
	if err == nil {
		return "I ran out of time before I could finish."
	}
	return "I ran out of time before I could finish, but I kept the partial progress so I can continue from there."
}

func isAgentContextTimeout(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ctx == nil {
		return false
	}
	return errors.Is(ctx.Err(), context.DeadlineExceeded)
}

func serializeToolError(err error) any {
	if err == nil {
		return nil
	}
	var inputErr *agentToolInputError
	if errors.As(err, &inputErr) {
		payload := map[string]any{
			"error": inputErr.Error(),
		}
		if strings.TrimSpace(inputErr.Field) != "" {
			payload["field"] = strings.TrimSpace(inputErr.Field)
		}
		return payload
	}
	return map[string]any{"error": err.Error()}
}

func isSerializedToolError(value any) bool {
	payload, ok := value.(map[string]any)
	if !ok {
		payloadInterface, ok := value.(map[string]interface{})
		if !ok {
			return false
		}
		payload = map[string]any(payloadInterface)
	}
	_, exists := payload["error"]
	return exists
}

func mergeSchemaProperties(left map[string]any, right map[string]any) map[string]any {
	merged := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		merged[key] = value
	}
	for key, value := range right {
		merged[key] = value
	}
	return merged
}

func resolveAgentTools(allowed []string) (map[string]struct{}, []AnthropicTool) {
	allTools := BuiltInAnthropicTools()
	if allowed == nil {
		return nil, allTools
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		allowedSet[name] = struct{}{}
	}

	filtered := make([]AnthropicTool, 0, len(allTools))
	for _, tool := range allTools {
		if _, ok := allowedSet[strings.TrimSpace(tool.Name)]; ok {
			filtered = append(filtered, tool)
		}
	}
	return allowedSet, filtered
}

func requiredStringField(input map[string]any, field string) (string, error) {
	value, present, err := optionalStringField(input, field)
	if err != nil {
		return "", err
	}
	if !present || strings.TrimSpace(value) == "" {
		return "", &agentToolInputError{
			Field:   field,
			Message: "missing required field: " + field,
		}
	}
	return strings.TrimSpace(value), nil
}

func optionalStringField(input map[string]any, field string) (string, bool, error) {
	if len(input) == 0 {
		return "", false, nil
	}
	rawValue, present := input[field]
	if !present {
		return "", false, nil
	}
	if rawValue == nil {
		return "", true, nil
	}
	switch value := rawValue.(type) {
	case string:
		return strings.TrimSpace(value), true, nil
	case json.Number:
		return strings.TrimSpace(value.String()), true, nil
	default:
		return "", true, &agentToolInputError{
			Field:   field,
			Message: "invalid field: " + field + " must be a string",
		}
	}
}

func optionalFloatField(input map[string]any, field string) (*float64, bool, error) {
	if len(input) == 0 {
		return nil, false, nil
	}
	rawValue, present := input[field]
	if !present {
		return nil, false, nil
	}
	if rawValue == nil {
		return nil, true, nil
	}

	switch value := rawValue.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			break
		}
		return &value, true, nil
	case float32:
		next := float64(value)
		return &next, true, nil
	case int:
		next := float64(value)
		return &next, true, nil
	case int32:
		next := float64(value)
		return &next, true, nil
	case int64:
		next := float64(value)
		return &next, true, nil
	case json.Number:
		next, err := value.Float64()
		if err == nil {
			return &next, true, nil
		}
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, true, nil
		}
		next, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil {
			return &next, true, nil
		}
	}

	return nil, true, &agentToolInputError{
		Field:   field,
		Message: "invalid field: " + field + " must be a number",
	}
}

func optionalTimeField(input map[string]any, field string) (*time.Time, bool, error) {
	if len(input) == 0 {
		return nil, false, nil
	}
	rawValue, present := input[field]
	if !present {
		return nil, false, nil
	}
	if rawValue == nil {
		return nil, true, nil
	}

	switch value := rawValue.(type) {
	case time.Time:
		next := value.UTC()
		return &next, true, nil
	case *time.Time:
		if value == nil {
			return nil, true, nil
		}
		next := value.UTC()
		return &next, true, nil
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, true, nil
		}
		parsed, err := time.Parse(time.RFC3339, trimmed)
		if err != nil {
			return nil, true, &agentToolInputError{
				Field:   field,
				Message: "invalid field: " + field + " must be an ISO8601 timestamp",
			}
		}
		next := parsed.UTC()
		return &next, true, nil
	default:
		return nil, true, &agentToolInputError{
			Field:   field,
			Message: "invalid field: " + field + " must be an ISO8601 timestamp",
		}
	}
}

func optionalIntField(input map[string]any, field string) (int, bool, error) {
	if len(input) == 0 {
		return 0, false, nil
	}
	rawValue, present := input[field]
	if !present {
		return 0, false, nil
	}
	if rawValue == nil {
		return 0, true, nil
	}

	switch value := rawValue.(type) {
	case int:
		return value, true, nil
	case int32:
		return int(value), true, nil
	case int64:
		return int(value), true, nil
	case float64:
		return int(value), true, nil
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed), true, nil
		}
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return 0, true, nil
		}
		parsed, err := strconv.Atoi(trimmed)
		if err == nil {
			return parsed, true, nil
		}
	}

	return 0, true, &agentToolInputError{
		Field:   field,
		Message: "invalid field: " + field + " must be an integer",
	}
}

func optionalRolesField(input map[string]any, field string) ([]RoleCtx, bool, error) {
	if len(input) == 0 {
		return nil, false, nil
	}
	rawValue, present := input[field]
	if !present {
		return nil, false, nil
	}
	if rawValue == nil {
		return nil, true, nil
	}

	switch value := rawValue.(type) {
	case []RoleCtx:
		return sanitizeRoleContexts(value), true, nil
	case []map[string]any:
		roles := make([]RoleCtx, 0, len(value))
		for _, item := range value {
			role, err := roleContextFromMap(item)
			if err != nil {
				return nil, true, &agentToolInputError{Field: field, Message: err.Error()}
			}
			if strings.TrimSpace(role.Role) != "" {
				roles = append(roles, role)
			}
		}
		return roles, true, nil
	case []any:
		roles := make([]RoleCtx, 0, len(value))
		for _, item := range value {
			switch typed := item.(type) {
			case map[string]any:
				role, err := roleContextFromMap(typed)
				if err != nil {
					return nil, true, &agentToolInputError{Field: field, Message: err.Error()}
				}
				if strings.TrimSpace(role.Role) != "" {
					roles = append(roles, role)
				}
			default:
				return nil, true, &agentToolInputError{
					Field:   field,
					Message: "invalid field: roles must contain objects with role and responsibilities",
				}
			}
		}
		return roles, true, nil
	default:
		return nil, true, &agentToolInputError{
			Field:   field,
			Message: "invalid field: " + field + " must be an array",
		}
	}
}

func optionalObjectField(input map[string]any, field string) (map[string]any, bool, error) {
	if len(input) == 0 {
		return nil, false, nil
	}
	rawValue, present := input[field]
	if !present {
		return nil, false, nil
	}
	if rawValue == nil {
		return nil, true, nil
	}
	switch value := rawValue.(type) {
	case map[string]any:
		return cloneStringAnyMap(value), true, nil
	default:
		return nil, true, &agentToolInputError{
			Field:   field,
			Message: "invalid field: " + field + " must be an object",
		}
	}
}

func optionalStringSliceField(input map[string]any, field string) ([]string, bool, error) {
	if len(input) == 0 {
		return nil, false, nil
	}
	rawValue, present := input[field]
	if !present {
		return nil, false, nil
	}
	if rawValue == nil {
		return nil, true, nil
	}
	switch value := rawValue.(type) {
	case []string:
		return cloneStringSlice(value), true, nil
	case []any:
		values := make([]string, 0, len(value))
		for _, item := range value {
			switch typed := item.(type) {
			case string:
				values = append(values, typed)
			case json.Number:
				values = append(values, typed.String())
			default:
				return nil, true, &agentToolInputError{
					Field:   field,
					Message: "invalid field: " + field + " must contain only strings",
				}
			}
		}
		return cloneStringSlice(values), true, nil
	default:
		return nil, true, &agentToolInputError{
			Field:   field,
			Message: "invalid field: " + field + " must be an array",
		}
	}
}

func optionalSubtasksField(input map[string]any, field string) ([]agentSubtaskInput, bool, error) {
	if len(input) == 0 {
		return nil, false, nil
	}
	rawValue, present := input[field]
	if !present {
		return nil, false, nil
	}
	if rawValue == nil {
		return nil, true, nil
	}
	switch value := rawValue.(type) {
	case []agentSubtaskInput:
		return sanitizeAgentSubtasks(value), true, nil
	case []SubtaskCtx:
		subtasks := make([]agentSubtaskInput, 0, len(value))
		for _, item := range value {
			subtasks = append(subtasks, agentSubtaskInput{
				Content:   item.Content,
				Completed: item.Completed,
			})
		}
		return sanitizeAgentSubtasks(subtasks), true, nil
	case []any:
		subtasks := make([]agentSubtaskInput, 0, len(value))
		for _, item := range value {
			switch typed := item.(type) {
			case string:
				subtasks = append(subtasks, agentSubtaskInput{Content: typed})
			case map[string]any:
				subtask, err := agentSubtaskFromMap(typed)
				if err != nil {
					return nil, true, &agentToolInputError{Field: field, Message: err.Error()}
				}
				subtasks = append(subtasks, subtask)
			default:
				return nil, true, &agentToolInputError{
					Field:   field,
					Message: "invalid field: " + field + " must contain strings or objects",
				}
			}
		}
		return sanitizeAgentSubtasks(subtasks), true, nil
	default:
		return nil, true, &agentToolInputError{
			Field:   field,
			Message: "invalid field: " + field + " must be an array",
		}
	}
}

func agentSubtaskFromMap(input map[string]any) (agentSubtaskInput, error) {
	contentValue, ok := input["content"].(string)
	if !ok || strings.TrimSpace(contentValue) == "" {
		return agentSubtaskInput{}, fmt.Errorf("invalid field: subtasks items must include non-empty content")
	}
	completed := false
	if rawCompleted, exists := input["completed"]; exists {
		if typed, ok := rawCompleted.(bool); ok {
			completed = typed
		}
	}
	return agentSubtaskInput{
		Content:   strings.TrimSpace(contentValue),
		Completed: completed,
	}, nil
}

func sanitizeAgentSubtasks(subtasks []agentSubtaskInput) []agentSubtaskInput {
	if len(subtasks) == 0 {
		return nil
	}
	sanitized := make([]agentSubtaskInput, 0, len(subtasks))
	for _, subtask := range subtasks {
		subtask.Content = truncatePlainText(subtask.Content, 300)
		if strings.TrimSpace(subtask.Content) == "" {
			continue
		}
		sanitized = append(sanitized, subtask)
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func roleContextFromMap(input map[string]any) (RoleCtx, error) {
	roleRaw, _ := input["role"]
	roleValue, ok := roleRaw.(string)
	if !ok || strings.TrimSpace(roleValue) == "" {
		return RoleCtx{}, fmt.Errorf("invalid field: roles items must include a non-empty role")
	}
	responsibilities := ""
	if raw, ok := input["responsibilities"].(string); ok {
		responsibilities = strings.TrimSpace(raw)
	}
	return RoleCtx{
		Role:             strings.TrimSpace(roleValue),
		Responsibilities: responsibilities,
	}, nil
}

func sanitizeRoleContexts(roles []RoleCtx) []RoleCtx {
	if len(roles) == 0 {
		return nil
	}
	sanitized := make([]RoleCtx, 0, len(roles))
	for _, role := range roles {
		role.Role = strings.TrimSpace(role.Role)
		role.Responsibilities = strings.TrimSpace(role.Responsibilities)
		if role.Role == "" {
			continue
		}
		sanitized = append(sanitized, role)
	}
	return sanitized
}

func marshalAgentCustomFields(fields map[string]any) (interface{}, error) {
	fields = sanitizeAgentCustomFields(fields)
	if len(fields) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, &agentToolInputError{
			Field:   "custom_fields",
			Message: "invalid field: custom_fields must be JSON-serializable",
		}
	}
	return string(encoded), nil
}

func sanitizeAgentCustomFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	sanitized := make(map[string]any, len(fields))
	for key, value := range fields {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		sanitized[trimmed] = value
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func mergeAgentCustomFields(existing map[string]any, patch map[string]any) map[string]any {
	if patch == nil {
		return nil
	}
	merged := cloneStringAnyMap(existing)
	if merged == nil {
		merged = make(map[string]any, len(patch))
	}
	for key, value := range sanitizeAgentCustomFields(patch) {
		if value == nil {
			delete(merged, key)
			continue
		}
		merged[key] = value
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func marshalAgentRoles(roles []RoleCtx) *string {
	roles = sanitizeRoleContexts(roles)
	if len(roles) == 0 {
		return nil
	}
	encoded, err := json.Marshal(roles)
	if err != nil {
		return nil
	}
	value := string(encoded)
	return &value
}

func normalizeAgentTaskType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "all":
		return "all"
	case "support":
		return "support"
	case "", "sprint", "general":
		return "sprint"
	default:
		return normalized
	}
}

func agentSprintNameKey(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	fields := strings.Fields(lower)
	return strings.Join(fields, " ")
}

func normalizeAgentTaskStatus(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	if normalized == "" {
		return "todo"
	}
	return normalized
}

func truncatePlainText(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return strings.TrimSpace(value[:maxLen])
}

func nullableText(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func startDateOrNil(value *time.Time) interface{} {
	if value == nil || value.IsZero() {
		return nil
	}
	next := value.UTC()
	return &next
}

func applyAgentFinancialsToDescription(description string, budget *float64, actualCost *float64) string {
	base, entries := parseTaskMetadataEntries(description)
	metadataParts := make([]string, 0, len(entries)+2)
	for _, entry := range entries {
		switch entry.Key {
		case "budget", "actual cost", "actual_cost", "spent", "cost":
			continue
		default:
			metadataParts = append(metadataParts, entry.Raw)
		}
	}
	if budget != nil && !math.IsNaN(*budget) && !math.IsInf(*budget, 0) && *budget >= 0 {
		metadataParts = append(metadataParts, fmt.Sprintf("Budget: $%s", formatBudget(*budget)))
	}
	if actualCost != nil && !math.IsNaN(*actualCost) && !math.IsInf(*actualCost, 0) && *actualCost >= 0 {
		metadataParts = append(metadataParts, fmt.Sprintf("Spent: $%s", formatBudget(*actualCost)))
	}
	if len(metadataParts) == 0 {
		return strings.TrimSpace(base)
	}
	block := "[" + strings.Join(metadataParts, " | ") + "]"
	if strings.TrimSpace(base) == "" {
		return block
	}
	return strings.TrimSpace(base) + "\n\n" + block
}

func (e *AgentEngine) resolveTaskAssigneeUUID(raw string, present bool) (*gocql.UUID, error) {
	if !present {
		return nil, nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := parseFlexibleUUID(trimmed)
	if err != nil {
		return nil, &agentToolInputError{
			Field:   "assignee_id",
			Message: "invalid field: assignee_id must be a valid UUID",
		}
	}
	return &parsed, nil
}

func normalizeAgentRelationTaskIDs(values []string, field string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		parsed, err := parseFlexibleUUID(trimmed)
		if err != nil {
			return nil, &agentToolInputError{
				Field:   field,
				Message: "invalid field: " + field + " must contain valid task UUIDs",
			}
		}
		normalized = append(normalized, strings.TrimSpace(parsed.String()))
	}
	return uniqueNonEmptyStrings(normalized), nil
}

func searchTaskContexts(tasks []TaskCtx, query string, limit int) []TaskCtx {
	query = strings.ToLower(collapseWhitespace(query))
	if query == "" || len(tasks) == 0 || limit <= 0 {
		return nil
	}

	tokens := strings.Fields(query)
	type scoredTask struct {
		task  TaskCtx
		score int
	}

	scored := make([]scoredTask, 0, len(tasks))
	for _, task := range tasks {
		title := strings.ToLower(collapseWhitespace(task.Title))
		description := strings.ToLower(collapseWhitespace(task.Description))
		score := 0
		if strings.Contains(title, query) {
			score += 20
		}
		if strings.Contains(description, query) {
			score += 8
		}
		for _, token := range tokens {
			if strings.Contains(title, token) {
				score += 5
			}
			if strings.Contains(description, token) {
				score += 2
			}
		}
		if score == 0 {
			continue
		}
		scored = append(scored, scoredTask{task: task, score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return taskCtxLess(scored[i].task, scored[j].task)
	})

	if limit > len(scored) {
		limit = len(scored)
	}
	results := make([]TaskCtx, 0, limit)
	for _, item := range scored[:limit] {
		results = append(results, item.task)
	}
	return results
}

func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func inferCanvasLanguage(filePath string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filePath))) {
	case ".go":
		return "go"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".svelte":
		return "svelte"
	case ".py":
		return "python"
	case ".json":
		return "json"
	case ".md":
		return "markdown"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".yaml", ".yml":
		return "yaml"
	default:
		return ""
	}
}
