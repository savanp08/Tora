package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type mockToolUseProvider struct {
	generateToolResponse func(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error)
}

type mockSummarizerOnly struct{}

func (m *mockSummarizerOnly) GenerateRollingSummary(_ context.Context, _ []byte, _ []Message) ([]byte, error) {
	return nil, nil
}

func (m *mockSummarizerOnly) GenerateChatResponse(_ context.Context, _ string) (string, error) {
	return "ok", nil
}

func (m *mockToolUseProvider) GenerateRollingSummary(_ context.Context, _ []byte, _ []Message) ([]byte, error) {
	return nil, nil
}

func (m *mockToolUseProvider) GenerateChatResponse(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockToolUseProvider) GenerateToolResponse(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error) {
	if m == nil || m.generateToolResponse == nil {
		return AgentProviderResponse{}, errors.New("mock tool response not configured")
	}
	return m.generateToolResponse(ctx, req)
}

func TestAgentEngineToolDispatch(t *testing.T) {
	var (
		callCount    int
		dispatchedTo string
	)

	provider := &mockToolUseProvider{
		generateToolResponse: func(_ context.Context, _ AgentProviderRequest) (AgentProviderResponse, error) {
			callCount++
			if callCount == 1 {
				return AgentProviderResponse{
					Content: []AgentProviderContentBlock{
						{
							Type:  "tool_use",
							ID:    "tool-1",
							Name:  "list_tasks",
							Input: map[string]any{"status": "todo"},
						},
					},
				}, nil
			}
			return AgentProviderResponse{
				Content: []AgentProviderContentBlock{
					{Type: "text", Text: "Finished after checking the board."},
				},
			}, nil
		},
	}

	engine := NewAgentEngine(provider, &ContextBuilder{}, "room-1", AgentAuthContext{UserID: "user-1"})
	engine.SetToolExecutor(func(_ context.Context, name string, input map[string]any) (any, error) {
		dispatchedTo = name
		if input["status"] != "todo" {
			t.Fatalf("unexpected tool input: %#v", input)
		}
		return []TaskCtx{{ID: "task-1", Title: "Task A"}}, nil
	})

	finalText, _, err := engine.Run(context.Background(), "show me tasks", AgentConfig{
		MaxTurns: 2,
		Timeout:  time.Second,
		Workspace: &WorkspaceContext{
			RoomID: "room-1",
		},
		InitialContext: "Workspace context",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if dispatchedTo != "list_tasks" {
		t.Fatalf("expected list_tasks to be dispatched, got %q", dispatchedTo)
	}
	if !strings.Contains(finalText, "Finished after checking the board.") {
		t.Fatalf("unexpected final text: %q", finalText)
	}
}

func TestAgentEngineMaxTurns(t *testing.T) {
	provider := &mockToolUseProvider{
		generateToolResponse: func(_ context.Context, _ AgentProviderRequest) (AgentProviderResponse, error) {
			return AgentProviderResponse{
				Content: []AgentProviderContentBlock{
					{
						Type:  "tool_use",
						ID:    "tool-1",
						Name:  "list_tasks",
						Input: map[string]any{},
					},
				},
			}, nil
		},
	}

	engine := NewAgentEngine(provider, &ContextBuilder{}, "room-1", AgentAuthContext{UserID: "user-1"})
	engine.SetToolExecutor(func(_ context.Context, _ string, _ map[string]any) (any, error) {
		return []TaskCtx{}, nil
	})

	finalText, _, err := engine.Run(context.Background(), "keep going", AgentConfig{
		MaxTurns: 2,
		Timeout:  time.Second,
		Workspace: &WorkspaceContext{
			RoomID: "room-1",
		},
		InitialContext: "Workspace context",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(finalText, "maximum number of agent turns") {
		t.Fatalf("expected max-turns message, got %q", finalText)
	}
}

func TestAgentEngineContextCancel(t *testing.T) {
	provider := &mockToolUseProvider{
		generateToolResponse: func(ctx context.Context, _ AgentProviderRequest) (AgentProviderResponse, error) {
			<-ctx.Done()
			return AgentProviderResponse{}, ctx.Err()
		},
	}

	engine := NewAgentEngine(provider, &ContextBuilder{}, "room-1", AgentAuthContext{UserID: "user-1"})

	finalText, events, err := engine.Run(context.Background(), "timeout please", AgentConfig{
		MaxTurns: 3,
		Timeout:  20 * time.Millisecond,
		Workspace: &WorkspaceContext{
			RoomID: "room-1",
		},
		InitialContext: "Workspace context",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(strings.ToLower(finalText), "ran out of time") {
		t.Fatalf("expected graceful timeout message, got %q", finalText)
	}
	if len(events) == 0 || events[len(events)-1].Kind != "done" {
		t.Fatalf("expected final done event, got %#v", events)
	}
}

func TestBuildActionsJsonFromAudit(t *testing.T) {
	start := time.Date(2026, time.March, 24, 0, 0, 0, 0, time.UTC)
	due := start.Add(48 * time.Hour)

	events := []AgentEvent{
		{
			Kind: "tool_result",
			Tool: "create_task",
			Result: TaskCtx{
				ID:          "task-create-1",
				Title:       "API Foundation",
				Description: "Build the base API surface",
				Status:      "todo",
				TaskType:    "sprint",
				SprintName:  "Sprint 1: Core Infrastructure",
				Budget:      floatPtr(3200),
				StartDate:   &start,
				DueDate:     &due,
				Roles: []RoleCtx{
					{Role: "Backend Developer", Responsibilities: "Build API handlers"},
				},
			},
		},
		{
			Kind:  "tool_result",
			Tool:  "update_task",
			Input: map[string]any{"task_id": "task-update-1", "status": "in_progress", "sprint_name": "Sprint 2: Delivery"},
			Result: TaskCtx{
				ID:         "task-update-1",
				Title:      "Integration Layer",
				Status:     "in_progress",
				TaskType:   "sprint",
				SprintName: "Sprint 2: Delivery",
			},
		},
		{
			Kind:   "tool_result",
			Tool:   "delete_task",
			Input:  map[string]any{"task_id": "task-delete-1", "task_title": "Legacy QA", "task_sprint": "Sprint 1: Core Infrastructure"},
			Result: map[string]any{"deleted": true, "task_id": "task-delete-1", "task_title": "Legacy QA"},
		},
		{
			Kind:   "tool_result",
			Tool:   "create_task",
			Result: map[string]any{"error": "validation failed"},
		},
	}

	actionsJSON, err := BuildActionsJSONFromAudit(events)
	if err != nil {
		t.Fatalf("BuildActionsJSONFromAudit returned error: %v", err)
	}

	var actions []map[string]any
	if err := json.Unmarshal([]byte(actionsJSON), &actions); err != nil {
		t.Fatalf("failed to parse actions json: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("expected 3 synthesized actions, got %d", len(actions))
	}

	if actions[0]["kind"] != "task_create" || actions[0]["already_applied"] != true {
		t.Fatalf("unexpected create action: %#v", actions[0])
	}
	if actions[1]["kind"] != "task_update" || actions[1]["task_id"] != "task-update-1" {
		t.Fatalf("unexpected update action: %#v", actions[1])
	}
	if actions[2]["kind"] != "task_delete" || actions[2]["task_id"] != "task-delete-1" {
		t.Fatalf("unexpected delete action: %#v", actions[2])
	}
}

func TestBuildCanvasActionsJsonFromAudit(t *testing.T) {
	events := []AgentEvent{
		{
			Kind:  "tool_result",
			Tool:  "write_canvas",
			Input: map[string]any{"file_path": "app/main.py", "content": "print('hello')\n", "description": "Create main entrypoint"},
			Result: map[string]any{
				"written": true,
				"draft":   true,
				"path":    "app/main.py",
				"lines":   1,
			},
		},
		{
			Kind:  "tool_result",
			Tool:  "write_canvas",
			Input: map[string]any{"file_path": "app/main.py", "content": "print('updated')\n", "description": "Refine main entrypoint"},
			Result: map[string]any{
				"written": true,
				"draft":   true,
				"path":    "app/main.py",
				"lines":   1,
			},
		},
		{
			Kind:   "tool_result",
			Tool:   "write_canvas",
			Input:  map[string]any{"file_path": "app/bad.py", "content": "oops"},
			Result: map[string]any{"error": "failed"},
		},
	}

	actionsJSON, err := BuildCanvasActionsJSONFromAudit(events)
	if err != nil {
		t.Fatalf("BuildCanvasActionsJSONFromAudit returned error: %v", err)
	}

	var actions []map[string]any
	if err := json.Unmarshal([]byte(actionsJSON), &actions); err != nil {
		t.Fatalf("failed to parse canvas actions json: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 synthesized canvas action, got %d", len(actions))
	}
	if actions[0]["file_path"] != "app/main.py" {
		t.Fatalf("unexpected canvas action path: %#v", actions[0])
	}
	if actions[0]["description"] != "Refine main entrypoint" {
		t.Fatalf("expected latest description to win, got %#v", actions[0])
	}
}

func TestAIRouterGenerateToolResponseUsesToolCapableProvider(t *testing.T) {
	router := NewAIRouter(
		&mockSummarizerOnly{},
		&mockToolUseProvider{
			generateToolResponse: func(_ context.Context, _ AgentProviderRequest) (AgentProviderResponse, error) {
				return AgentProviderResponse{
					Content: []AgentProviderContentBlock{
						{Type: "text", Text: "native tool response"},
					},
				}, nil
			},
		},
	)

	if !router.SupportsToolUse() {
		t.Fatal("expected router to report native tool support")
	}

	response, err := router.GenerateToolResponse(context.Background(), AgentProviderRequest{
		SystemPrompt: "test",
		Messages: []AgentProviderMessage{
			{
				Role:    "user",
				Content: []AgentProviderContentBlock{{Type: "text", Text: "hello"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateToolResponse returned error: %v", err)
	}
	if len(response.Content) == 0 || response.Content[0].Text != "native tool response" {
		t.Fatalf("unexpected tool response: %#v", response.Content)
	}
}

func floatPtr(value float64) *float64 {
	return &value
}
