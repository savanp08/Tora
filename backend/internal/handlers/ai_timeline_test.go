package handlers

import (
	"context"
	"testing"
	"time"
)

func TestCalculateAITimelineExecutionTimeoutScalesAndCaps(t *testing.T) {
	cases := []struct {
		name        string
		baseSecs    int
		sprintCount int
		wantSecs    int
	}{
		{name: "single sprint uses one call window", baseSecs: 30, sprintCount: 1, wantSecs: 300},
		{name: "five sprints cap at prompt window", baseSecs: 300, sprintCount: 5, wantSecs: 900},
		{name: "many sprints stay capped at prompt window", baseSecs: 300, sprintCount: 12, wantSecs: 900},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := calculateAITimelineExecutionTimeout(time.Duration(tc.baseSecs)*time.Second, tc.sprintCount)
			if got != time.Duration(tc.wantSecs)*time.Second {
				t.Fatalf("expected %ds, got %s", tc.wantSecs, got)
			}
		})
	}
}

func TestCalculateAITimelineBlueprintTimeoutScalesAndCaps(t *testing.T) {
	got := calculateAITimelineBlueprintTimeout(120*time.Second, "Build a detailed drone program.")
	if got != 10*time.Minute {
		t.Fatalf("expected staged blueprint budget of 10m, got %s", got)
	}
}

func TestCalculateAITimelineEditTimeoutIncludesSummaryContext(t *testing.T) {
	got := calculateAITimelineEditTimeout(120*time.Second, "Restructure the board.", `{"tasks":[]}`)
	if got != 5*time.Minute {
		t.Fatalf("expected fixed per-call timeout of 5m, got %s", got)
	}
}

func TestBuildAITimelineErrorPayloadDistinguishesDeadlineAndCancellation(t *testing.T) {
	deadlineStatus, deadlinePayload := buildAITimelineErrorPayload(
		"blueprint",
		context.DeadlineExceeded,
		5*time.Minute,
		15*time.Minute,
	)
	if deadlineStatus != 504 {
		t.Fatalf("expected deadline status 504, got %d", deadlineStatus)
	}
	if deadlinePayload.Code != "deadline_exceeded" {
		t.Fatalf("expected deadline code, got %q", deadlinePayload.Code)
	}

	canceledStatus, canceledPayload := buildAITimelineErrorPayload(
		"blueprint",
		context.Canceled,
		5*time.Minute,
		15*time.Minute,
	)
	if canceledStatus != aiHTTPStatusClientClosed {
		t.Fatalf("expected canceled status %d, got %d", aiHTTPStatusClientClosed, canceledStatus)
	}
	if canceledPayload.Code != "request_canceled" {
		t.Fatalf("expected cancellation code, got %q", canceledPayload.Code)
	}
	if canceledPayload.Error == deadlinePayload.Error {
		t.Fatalf("expected distinct error messages for canceled vs deadline")
	}
}

func TestBuildAITimelineErrorPayloadUsesStageOverrideFromWrappedStageError(t *testing.T) {
	status, payload := buildAITimelineErrorPayload(
		"blueprint",
		&aiTimelineStageError{
			Stage:   "blueprint_foundation",
			Timeout: 5 * time.Minute,
			Err:     context.DeadlineExceeded,
		},
		10*time.Minute,
		15*time.Minute,
	)
	if status != 504 {
		t.Fatalf("expected deadline status 504, got %d", status)
	}
	if payload.Stage != "blueprint_foundation" {
		t.Fatalf("expected wrapped stage override, got %q", payload.Stage)
	}
	if payload.TimeoutMs != int64((5*time.Minute)/time.Millisecond) {
		t.Fatalf("expected wrapped timeout override, got %d", payload.TimeoutMs)
	}
	if payload.Error != "Project foundation generation exceeded its time budget." {
		t.Fatalf("unexpected stage-specific error message: %q", payload.Error)
	}
}

func TestIsAITimelineAgentTimedOutIgnoresPlainCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if isAITimelineAgentTimedOut(ctx, "", nil) {
		t.Fatalf("expected plain cancellation to not be treated as timeout")
	}
}

func TestUpsertAITimelineAgentTaskAppendsAndReplacesByTaskID(t *testing.T) {
	project := &aiTimelineProject{
		Sprints: []aiTimelineSprint{
			{
				ID:           "sprint-1",
				Name:         "Sprint 1",
				StartDate:    "2026-04-01",
				EndDate:      "2026-04-07",
				DurationDays: 7,
			},
		},
	}

	first := aiTimelineTask{
		TaskID: "task-1",
		ID:     "task-1",
		Title:  "Research architecture",
		Status: "todo",
		Type:   "general",
	}
	index := upsertAITimelineAgentTask(project, "Sprint 1", "2026-04-01", "2026-04-07", first)
	if index != 0 {
		t.Fatalf("expected sprint index 0, got %d", index)
	}
	if len(project.Sprints[0].Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(project.Sprints[0].Tasks))
	}
	if !project.Sprints[0].TasksGenerated {
		t.Fatalf("expected sprint to be marked as generated")
	}

	replacement := first
	replacement.Title = "Research final architecture"
	index = upsertAITimelineAgentTask(project, "Sprint 1", "2026-04-01", "2026-04-07", replacement)
	if index != 0 {
		t.Fatalf("expected sprint index 0 on replacement, got %d", index)
	}
	if len(project.Sprints[0].Tasks) != 1 {
		t.Fatalf("expected replacement to keep task count at 1, got %d", len(project.Sprints[0].Tasks))
	}
	if project.Sprints[0].Tasks[0].Title != "Research final architecture" {
		t.Fatalf("expected replacement title, got %q", project.Sprints[0].Tasks[0].Title)
	}
}

func TestCollectAITimelineMissingSprintsOnlyIncludesEmptyOnes(t *testing.T) {
	project := aiTimelineProject{
		Sprints: []aiTimelineSprint{
			{Name: "Sprint 1", Tasks: []aiTimelineTask{{Title: "Done", Status: "todo", Type: "general"}}},
			{Name: "Sprint 2"},
			{Name: "", Tasks: nil},
		},
	}

	got := collectAITimelineMissingSprints(project)
	if len(got) != 2 {
		t.Fatalf("expected 2 missing sprints, got %d (%v)", len(got), got)
	}
	if got[0] != "Sprint 2" {
		t.Fatalf("expected Sprint 2 first, got %q", got[0])
	}
	if got[1] != "Sprint 3" {
		t.Fatalf("expected fallback Sprint 3 label, got %q", got[1])
	}
}
