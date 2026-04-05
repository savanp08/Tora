package websocket

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/execution"
	"github.com/savanp08/converse/internal/models"
)

const toraCanvasSystemPrompt = `You are Tora, an AI code assistant embedded in a collaborative coding canvas.
You have read and write access to the canvas files in this room.

CAPABILITIES:
- Read any file with read_canvas(file_path)
- Write/update any file with write_canvas(file_path, content, description). These writes are staged for user review until applied.
- List all files with read_canvas() (no args)
- Verify code by compiling/running the mirrored workspace with execute_canvas(language, main_file, stdin)
- Explain code, suggest improvements, add features, fix bugs

WORKFLOW FOR CODE CHANGES:
1. Always read_canvas() first to see what files exist
2. Read the specific file(s) you plan to edit before writing
3. If the canvas is empty, or the user explicitly asks you to create something new, you MAY create a sensible new file path with write_canvas(...).
   Prefer conventional names that match the request (for example, README.md, technical_implementation.md, main.go, App.tsx).
4. When writing, produce the COMPLETE file content — never partial diffs,
   never "..." placeholders. The write_canvas tool replaces the entire file.
5. After writing, call read_canvas(file_path) to verify the staged write succeeded
6. When the change is risky or touches executable code, call execute_canvas(...) to verify it before finishing
7. Explain what you changed and why in plain text after the tool calls

CODE QUALITY RULES:
- Match the existing code style (indentation, naming, patterns)
- Do not add unnecessary dependencies
- Add comments only where logic is non-obvious
- Preserve all existing functionality unless explicitly asked to remove it

MULTI-FILE CHANGES:
- Handle related files in one loop (e.g. if adding an API endpoint,
  also update the route registration and the type definitions)
- Declare your plan: "I will edit 3 files: X, Y, Z" before starting

TASK CONTEXT:
- You may receive read-only task board context and read-only task lookup tools when the user references project tasks.
- Never modify the task board from @Canvas. Task data is reference-only here.

WHAT NOT TO DO:
- Do not pretend an existing file/path already exists when it does not. For new work, create a sensible new file path and then verify it.
- Do not write partial content with "rest unchanged" — always full content
- Do not modify files outside the canvas (task board, database schema, etc.)`

type toraCanvasDraftChange struct {
	Path        string
	Content     string
	Description string
	Operation   string
}

func (h *Hub) runToraCanvasAgent(
	ctx context.Context,
	roomID string,
	userMessage models.Message,
	prompt string,
	forceTaskReference bool,
) (string, []ai.AgentEvent, error) {
	if h == nil || h.msgService == nil || h.msgService.Scylla == nil || h.msgService.Scylla.Session == nil {
		return "", nil, fmt.Errorf("canvas ai storage unavailable")
	}

	ctxBuilder := h.ensureToraContextBuilder()
	engineFactory := h.ensureToraAgentEngineFactory()
	if ctxBuilder == nil || engineFactory == nil {
		return "", nil, fmt.Errorf("canvas ai is not configured")
	}

	taskReferenceRequested := toraCanvasNeedsTaskReference(prompt, forceTaskReference)
	buildOpts := toraCanvasBuildOptions(taskReferenceRequested)
	workspace, err := ctxBuilder.Build(ctx, roomID, strings.TrimSpace(userMessage.SenderID), buildOpts)
	if err != nil {
		return "", nil, err
	}

	includeTaskTools := taskReferenceRequested && toraCanvasWorkspaceHasTaskBoard(workspace)
	engine := engineFactory.New(roomID, ai.AgentAuthContext{
		UserID:   strings.TrimSpace(userMessage.SenderID),
		UserName: strings.TrimSpace(userMessage.SenderName),
	}, toraCanvasModelTier(includeTaskTools))
	if engine == nil {
		return "", nil, fmt.Errorf("canvas ai engine is unavailable")
	}
	engine.SetRoomBroadcaster(h)
	draftWrites := make(map[string]toraCanvasDraftChange)
	engine.SetToolExecutor(func(toolCtx context.Context, name string, input map[string]any) (any, error) {
		switch strings.TrimSpace(name) {
		case "read_canvas":
			return executeToraCanvasDraftRead(toolCtx, engine, draftWrites, input)
		case "write_canvas":
			return executeToraCanvasDraftWrite(toolCtx, engine, draftWrites, input)
		case "execute_canvas":
			return executeToraCanvasDraftExecute(toolCtx, engine, draftWrites, input)
		default:
			return engine.ExecuteBuiltInTool(toolCtx, name, input)
		}
	})

	finalText, events, err := engine.Run(ctx, prompt, ai.AgentConfig{
		MaxTurns:        toraCanvasMaxTurns(includeTaskTools),
		Timeout:         toraRequestTimeoutMutation,
		SystemPrompt:    toraCanvasSystemPrompt,
		ContextOptions:  buildOpts,
		Workspace:       workspace,
		InitialContext:  buildToraCanvasInitialContext(workspace, includeTaskTools),
		AllowedTools:    toraCanvasAllowedTools(includeTaskTools),
		OriginMessageID: normalizeMessageID(userMessage.ID),
		WorkflowKind:    "canvas",
		StreamCallback: func(event ai.AgentEvent) {
			h.emitToraCanvasUpdated(roomID, event)
		},
	})
	if err != nil {
		return "", events, err
	}

	return strings.TrimSpace(finalText), events, nil
}

func resolveToraCanvasProvider(modelTier string) ai.Provider {
	return resolveToraTaskBoardProvider(modelTier)
}

func toraCanvasBuildOptions(taskReferenceRequested bool) ai.BuildOptions {
	opts := ai.BuildOptions{
		IncludeCanvas:      true,
		IncludeChat:        false,
		TaskLimit:          80,
		CanvasExcerptLines: 20,
	}
	if taskReferenceRequested {
		opts.TaskLimit = 200
	}
	return opts
}

func toraCanvasAllowedTools(includeTaskTools bool) []string {
	tools := []string{"read_canvas", "write_canvas", "execute_canvas"}
	if includeTaskTools {
		tools = append(tools, "list_tasks", "list_sprints", "search_tasks")
	}
	return tools
}

func toraCanvasModelTier(includeTaskTools bool) string {
	if includeTaskTools {
		return ai.AIModelTierStandard
	}
	return ai.AIModelTierStandard
}

func toraCanvasMaxTurns(includeTaskTools bool) int {
	if includeTaskTools {
		return 8
	}
	return 6
}

func toraCanvasPromptReferencesTasks(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return false
	}

	keywords := []string{
		"task", "tasks", "ticket", "tickets", "sprint", "sprints",
		"budget", "deadline", "due date", "assignee", "project board",
		"task board", "backlog", "milestone", "roadmap",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func toraCanvasNeedsTaskReference(prompt string, forceTaskReference bool) bool {
	return forceTaskReference || toraCanvasPromptReferencesTasks(prompt)
}

func toraCanvasWorkspaceHasTaskBoard(workspace *ai.WorkspaceContext) bool {
	if workspace == nil {
		return false
	}
	return len(workspace.Tasks) > 0 || len(workspace.SupportTickets) > 0 || len(workspace.Sprints) > 0
}

func buildToraCanvasInitialContext(workspace *ai.WorkspaceContext, includeTaskContext bool) string {
	if workspace == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("CANVAS ROOM CONTEXT\n")
	sb.WriteString(fmt.Sprintf("Room: %s\n", toraFirstNonEmpty(strings.TrimSpace(workspace.RoomName), strings.TrimSpace(workspace.RoomID))))

	if members := renderToraChatMembers(workspace.Members); members != "" {
		sb.WriteString("\nCollaborators:\n")
		sb.WriteString(members)
	}

	sb.WriteString("\nCanvas files:\n")
	if fileIndex := renderToraCanvasFileIndex(workspace.CanvasFiles); fileIndex != "" {
		sb.WriteString(fileIndex)
	} else {
		sb.WriteString("  (no canvas files found)\n")
	}
	sb.WriteString("\nTreat the listed canvas file paths as authoritative for this room.\n")

	if includeTaskContext {
		if summary := renderToraCanvasTaskReference(workspace); summary != "" {
			sb.WriteString("\nRead-only task board reference:\n")
			sb.WriteString(summary)
		}
	}

	return strings.TrimSpace(sb.String())
}

func renderToraCanvasFileIndex(files []ai.CanvasFileCtx) string {
	if len(files) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf(
			"  - %s [%s] %d lines\n",
			path,
			toraFirstNonEmpty(strings.TrimSpace(file.Language), "plaintext"),
			file.Lines,
		))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderToraCanvasTaskReference(workspace *ai.WorkspaceContext) string {
	if workspace == nil || !toraCanvasWorkspaceHasTaskBoard(workspace) {
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
	return strings.TrimRight(sb.String(), "\n")
}

func (h *Hub) emitToraCanvasUpdated(roomID string, event ai.AgentEvent) {
	if h == nil || strings.TrimSpace(event.Kind) != "tool_result" || strings.TrimSpace(event.Tool) != "write_canvas" {
		return
	}

	result, ok := event.Result.(map[string]any)
	if !ok {
		return
	}

	written, _ := result["written"].(bool)
	if !written {
		return
	}
	draft, _ := result["draft"].(bool)
	if draft {
		return
	}

	path := toraCanvasEventString(result["path"])
	if path == "" {
		path = toraCanvasEventString(event.Input["file_path"])
	}
	if path == "" {
		return
	}

	h.BroadcastToRoom(roomID, map[string]interface{}{
		"type":        "canvas_updated",
		"path":        path,
		"lines":       toraCanvasEventInt(result["lines"]),
		"description": toraCanvasEventString(event.Input["description"]),
	})
}

func toraCanvasEventString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func toraCanvasEventInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint:
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		return int(typed)
	case uint64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func executeToraCanvasDraftRead(
	ctx context.Context,
	engine *ai.AgentEngine,
	drafts map[string]toraCanvasDraftChange,
	input map[string]any,
) (any, error) {
	filePath := strings.TrimSpace(toraCanvasDraftInputString(input, "file_path"))
	if filePath != "" {
		if draft, ok := drafts[filePath]; ok {
			return map[string]any{
				"path":     draft.Path,
				"language": toraCanvasDraftLanguage(draft.Path),
				"content":  draft.Content,
				"lines":    toraCanvasDraftLineCount(draft.Content),
				"draft":    true,
			}, nil
		}
		return engine.ExecuteBuiltInTool(ctx, "read_canvas", input)
	}

	baseResult, err := engine.ExecuteBuiltInTool(ctx, "read_canvas", input)
	if err != nil {
		return nil, err
	}
	filesByPath := make(map[string]ai.CanvasFileCtx, len(drafts))
	switch typed := baseResult.(type) {
	case []ai.CanvasFileCtx:
		for _, file := range typed {
			path := strings.TrimSpace(file.Path)
			if path == "" {
				continue
			}
			filesByPath[path] = file
		}
	case []map[string]any:
		for _, file := range typed {
			path := strings.TrimSpace(toraCanvasEventString(file["path"]))
			if path == "" {
				continue
			}
			filesByPath[path] = ai.CanvasFileCtx{
				Path:     path,
				Language: strings.TrimSpace(toraCanvasEventString(file["language"])),
				Lines:    toraCanvasEventInt(file["lines"]),
				Excerpt:  strings.TrimSpace(toraCanvasEventString(file["excerpt"])),
			}
		}
	}

	for path, draft := range drafts {
		filesByPath[path] = ai.CanvasFileCtx{
			Path:     path,
			Language: toraCanvasDraftLanguage(path),
			Lines:    toraCanvasDraftLineCount(draft.Content),
			Excerpt:  toraCanvasDraftExcerpt(draft.Content, 50),
		}
	}

	files := make([]ai.CanvasFileCtx, 0, len(filesByPath))
	for _, file := range filesByPath {
		files = append(files, file)
	}
	sort.SliceStable(files, func(i, j int) bool {
		return strings.ToLower(files[i].Path) < strings.ToLower(files[j].Path)
	})
	return files, nil
}

func executeToraCanvasDraftWrite(
	ctx context.Context,
	engine *ai.AgentEngine,
	drafts map[string]toraCanvasDraftChange,
	input map[string]any,
) (any, error) {
	filePath := strings.TrimSpace(toraCanvasDraftInputString(input, "file_path"))
	if filePath == "" {
		return nil, fmt.Errorf("missing required field: file_path")
	}
	content, ok := input["content"].(string)
	if !ok {
		return nil, fmt.Errorf("missing required field: content")
	}
	description := strings.TrimSpace(toraCanvasDraftInputString(input, "description"))
	operation := "update"
	if existingDraft, ok := drafts[filePath]; ok {
		if strings.TrimSpace(existingDraft.Operation) != "" {
			operation = strings.TrimSpace(existingDraft.Operation)
		}
	} else if engine != nil {
		if _, err := engine.ExecuteBuiltInTool(ctx, "read_canvas", map[string]any{"file_path": filePath}); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				operation = "create"
			}
		}
	}
	drafts[filePath] = toraCanvasDraftChange{
		Path:        filePath,
		Content:     content,
		Description: description,
		Operation:   operation,
	}
	return map[string]any{
		"written":     true,
		"draft":       true,
		"path":        filePath,
		"lines":       toraCanvasDraftLineCount(content),
		"description": description,
		"operation":   operation,
	}, nil
}

func executeToraCanvasDraftExecute(
	ctx context.Context,
	engine *ai.AgentEngine,
	drafts map[string]toraCanvasDraftChange,
	input map[string]any,
) (any, error) {
	language := strings.TrimSpace(toraCanvasDraftInputString(input, "language"))
	if language == "" {
		return nil, fmt.Errorf("missing required field: language")
	}
	mainFile := strings.TrimSpace(toraCanvasDraftInputString(input, "main_file"))
	if mainFile == "" {
		return nil, fmt.Errorf("missing required field: main_file")
	}
	stdin := toraCanvasDraftInputString(input, "stdin")

	baseFiles, err := engine.LoadCanvasExecutionFiles(ctx)
	if err != nil {
		return nil, err
	}
	fileByPath := make(map[string]string, len(baseFiles)+len(drafts))
	for _, file := range baseFiles {
		path := strings.TrimSpace(file.Name)
		if path == "" {
			continue
		}
		fileByPath[path] = file.Content
	}
	for path, draft := range drafts {
		fileByPath[path] = draft.Content
	}
	files := make([]execution.ExecutionFile, 0, len(fileByPath))
	for path, content := range fileByPath {
		files = append(files, execution.ExecutionFile{Name: path, Content: content})
	}
	sort.SliceStable(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	return ai.ExecuteCanvasWorkspace(ctx, language, mainFile, stdin, files)
}

func toraCanvasDraftInputString(input map[string]any, key string) string {
	if len(input) == 0 {
		return ""
	}
	switch value := input[key].(type) {
	case string:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func toraCanvasDraftLineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func toraCanvasDraftLanguage(path string) string {
	lower := strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.HasSuffix(lower, ".py"):
		return "python"
	case strings.HasSuffix(lower, ".ts"), strings.HasSuffix(lower, ".tsx"):
		return "typescript"
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".jsx"):
		return "javascript"
	case strings.HasSuffix(lower, ".go"):
		return "go"
	case strings.HasSuffix(lower, ".rs"):
		return "rust"
	case strings.HasSuffix(lower, ".java"):
		return "java"
	case strings.HasSuffix(lower, ".c"):
		return "c"
	case strings.HasSuffix(lower, ".cc"), strings.HasSuffix(lower, ".cpp"), strings.HasSuffix(lower, ".cxx"):
		return "cpp"
	case strings.HasSuffix(lower, ".json"):
		return "json"
	case strings.HasSuffix(lower, ".html"):
		return "html"
	case strings.HasSuffix(lower, ".css"):
		return "css"
	case strings.HasSuffix(lower, ".md"):
		return "markdown"
	case strings.HasSuffix(lower, ".sh"):
		return "shell"
	default:
		return "plaintext"
	}
}

func toraCanvasDraftExcerpt(content string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
