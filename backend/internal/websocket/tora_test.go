package websocket

import (
	"strings"
	"testing"

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
	if toraTaskBoardNeedsToolRetry("Updated the board.", writeEvents) {
		t.Fatal("did not expect retry once a write tool was used")
	}
}

func TestBuildToraTaskBoardToolEnforcementPrompt(t *testing.T) {
	prompt := buildToraTaskBoardToolEnforcementPrompt("reduce budget by 50%", "I don't have the tools")
	for _, needle := range []string{"list_tasks()", "update_task", "verify_task_count()"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected enforcement prompt to mention %q, got %q", needle, prompt)
		}
	}
}
