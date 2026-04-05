package websocket

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/savanp08/converse/internal/ai"
)

func TestFormatToraTaskBoardSummarySkipsComputedSummaryWithoutWrites(t *testing.T) {
	refusal := "I'm sorry, but I currently don't have the tools needed to process your request."

	got := formatToraTaskBoardSummary(refusal, nil, &ai.WorkspaceContext{
		Tasks:   make([]ai.TaskCtx, 13),
		Sprints: make([]ai.SprintCtx, 3),
	})

	if got != refusal {
		t.Fatalf("expected refusal text only, got %q", got)
	}
}

func TestToraTaskBoardNeedsToolRetry(t *testing.T) {
	if !toraTaskBoardNeedsToolRetry("I don't have the tools needed.", nil) {
		t.Fatal("expected retry when there were no tool calls")
	}

	readOnlyEvents := []ai.AgentEvent{
		{Kind: "tool_call", Tool: "list_tasks"},
	}
	if !toraTaskBoardNeedsToolRetry("I don't have the tools needed.", readOnlyEvents) {
		t.Fatal("expected retry when the model only used read tools and still refused")
	}

	writeEvents := []ai.AgentEvent{
		{Kind: "tool_call", Tool: "update_task"},
	}
	if !toraTaskBoardNeedsToolRetry("Updated the board.", writeEvents) {
		t.Fatal("expected retry when writes happened without verify_task_count")
	}

	writeAndVerifyEvents := []ai.AgentEvent{
		{Kind: "tool_call", Tool: "update_task"},
		{Kind: "tool_call", Tool: "verify_task_count"},
	}
	if toraTaskBoardNeedsToolRetry("Updated the board.", writeAndVerifyEvents) {
		t.Fatal("did not expect retry once writes were followed by verify_task_count")
	}
}

func TestToraDryRunVerifyTaskCountUsesStagedSprintState(t *testing.T) {
	dryRun := newToraDryRunExecutor(func(ctx context.Context, name string, input map[string]any) (any, error) {
		switch name {
		case "list_groups":
			return []any{
				map[string]any{"group_id": "group-a", "name": "Sprint A", "task_count": 2},
				map[string]any{"group_id": "group-b", "name": "Sprint B", "task_count": 1},
			}, nil
		default:
			return nil, nil
		}
	}, &ai.WorkspaceContext{
		Tasks: []ai.TaskCtx{
			{ID: "task-1", Title: "Alpha", SprintName: "Sprint A", Status: "todo", UpdatedAt: time.Now().UTC()},
			{ID: "task-2", Title: "Beta", SprintName: "Sprint B", Status: "todo", UpdatedAt: time.Now().UTC()},
		},
		Sprints: []ai.SprintCtx{
			{Name: "Sprint A", TaskCount: 1},
			{Name: "Sprint B", TaskCount: 1},
		},
	})

	if _, err := dryRun.execute(context.Background(), "update_task", map[string]any{
		"task_id":     "task-2",
		"sprint_name": "Sprint A",
	}); err != nil {
		t.Fatalf("stage update_task: %v", err)
	}

	result, err := dryRun.execute(context.Background(), "verify_task_count", map[string]any{})
	if err != nil {
		t.Fatalf("verify_task_count: %v", err)
	}

	record, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", result)
	}
	if got := record["sprint_count"]; got != 1 {
		t.Fatalf("expected staged sprint_count=1, got %#v", got)
	}

	bySprint, ok := record["by_sprint"].(map[string]int)
	if !ok {
		t.Fatalf("expected by_sprint map[string]int, got %#v", record["by_sprint"])
	}
	if got := bySprint["Sprint A"]; got != 2 {
		t.Fatalf("expected Sprint A task count=2 after staged rename, got %d", got)
	}
	if _, exists := bySprint["Sprint B"]; exists {
		t.Fatalf("did not expect Sprint B to remain after staged rename: %#v", bySprint)
	}
}

func TestBuildToraTaskBoardToolEnforcementPrompt(t *testing.T) {
	prompt := buildToraTaskBoardToolEnforcementPrompt("reduce budget by 50%", "I don't have the tools", nil)
	for _, needle := range []string{"list_tasks", "update_task", "verify_task_count()"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected enforcement prompt to mention %q, got %q", needle, prompt)
		}
	}
}

func TestBuildToraTaskBoardToolEnforcementPromptContinuesFromStagedWrites(t *testing.T) {
	prompt := buildToraTaskBoardToolEnforcementPrompt("make total sprints 10", "Need to verify.", []ai.AgentEvent{
		{Kind: "tool_call", Tool: "update_task"},
	})
	for _, needle := range []string{
		"already made staged board changes",
		"Do NOT restart the project rewrite from scratch",
		"list_groups() and verify_task_count()",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected staged-write enforcement prompt to mention %q, got %q", needle, prompt)
		}
	}
}

func TestBuildToraChatSystemPromptUsesEphemeralPrompt(t *testing.T) {
	got := buildToraChatSystemPrompt(false, true)
	if got != toraEphemeralChatSystemPrompt {
		t.Fatalf("expected ephemeral prompt, got %q", got)
	}
}

func TestResolveToraExecutionTargetPrefersCanvasWhenCanvasTagPresent(t *testing.T) {
	plan := toraLoadPlan{flags: toraFlagMutation}
	if got := resolveToraExecutionTarget(plan, true); got != toraExecutionTargetCanvas {
		t.Fatalf("expected canvas target to win over mutation plan, got %q", got)
	}
}

func TestToraCanvasNeedsTaskReferenceWhenProjectTagPresent(t *testing.T) {
	if !toraCanvasNeedsTaskReference("create technical docs in the canvas", true) {
		t.Fatal("expected @Project-assisted canvas run to force task reference loading")
	}
}

func TestToraCanvasSystemPromptAllowsNewFilesInEmptyCanvas(t *testing.T) {
	for _, needle := range []string{
		"If the canvas is empty",
		"you MAY create a sensible new file path with write_canvas",
		"create a sensible new file path and then verify it",
	} {
		if !strings.Contains(toraCanvasSystemPrompt, needle) {
			t.Fatalf("expected canvas prompt to contain %q, got %q", needle, toraCanvasSystemPrompt)
		}
	}
}

func TestToraChatBuildOptionsForEphemeralRoomSkipsTaskBoard(t *testing.T) {
	opts := toraChatBuildOptions(toraChatIntentGeneral, false, true)
	if !opts.IncludeChat {
		t.Fatal("expected ephemeral chat rooms to include recent chat")
	}
	if opts.IncludeCanvas {
		t.Fatal("did not expect ephemeral chat rooms to include canvas")
	}
	if opts.TaskLimit != 0 {
		t.Fatalf("expected ephemeral chat rooms to skip task loading, got TaskLimit=%d", opts.TaskLimit)
	}
}

func TestToraChatAllowedToolsGeneralAllowsSearch(t *testing.T) {
	got := toraChatAllowedTools(toraChatIntentGeneral, false)
	if len(got) != 1 || got[0] != "search_tasks" {
		t.Fatalf("expected general chat to allow search_tasks, got %#v", got)
	}
}

func TestBuildToraChatInitialContextAddsGeneralTaskNote(t *testing.T) {
	workspace := &ai.WorkspaceContext{
		RoomID:   "room-1",
		RoomName: "Demo Room",
		Tasks: []ai.TaskCtx{
			{ID: "task-1", Title: "Initial planning"},
		},
		RecentMessages: []ai.MessageCtx{
			{SenderName: "Ava", Content: "What should we build first?"},
		},
	}

	got := buildToraChatInitialContext(workspace, toraChatIntentGeneral, false)
	if !strings.Contains(got, "Task board data is available for reference") {
		t.Fatalf("expected general context note, got %q", got)
	}
}

func TestBuildToraFailureResponseForProviderRateLimit(t *testing.T) {
	err := &ai.ProvidersExhaustedError{
		LastErr: &ai.HTTPStatusError{
			Code:     http.StatusTooManyRequests,
			Provider: "openai",
			Err:      errors.New("free-tier quota exceeded"),
		},
	}

	got := buildToraFailureResponse(err)
	for _, needle := range []string{
		"AI provider rate limit",
		"Provider: openai",
		"free-tier quota exceeded",
		"Reset: not reported by provider",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected response to contain %q, got %q", needle, got)
		}
	}
}

func TestBuildToraFailureResponseForProviderUnavailable(t *testing.T) {
	err := &ai.ProvidersExhaustedError{
		LastErr: &ai.HTTPStatusError{
			Code:     http.StatusServiceUnavailable,
			Provider: "groq",
			Err:      errors.New("backend overloaded"),
		},
	}

	got := buildToraFailureResponse(err)
	for _, needle := range []string{
		"temporarily unavailable",
		"Provider: groq",
		"backend overloaded",
		"provider did not report a reset time",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected response to contain %q, got %q", needle, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "rate-limited") {
		t.Fatalf("did not expect rate-limit wording, got %q", got)
	}
}

func TestBuildToraFailureResponseForGenericProviderExhaustion(t *testing.T) {
	got := buildToraFailureResponse(ai.ErrAllAIProvidersExhausted)
	if !strings.Contains(got, "provider chain unavailable") {
		t.Fatalf("expected provider-chain wording, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "rate-limit") {
		t.Fatalf("did not expect rate-limit wording, got %q", got)
	}
}
