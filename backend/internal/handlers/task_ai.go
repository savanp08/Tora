package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/ai"
)

const (
	taskDetailNotesFieldKey       = "task_notes"
	taskDetailSummaryFieldKey     = "task_detail_summary"
	taskDetailStepsFieldKey       = "task_detail_steps"
	taskDetailGeneratedAtFieldKey = "task_detail_generated_at"
	taskDetailRequestTimeout      = 45 * time.Second
	taskDetailMaxSummaryRunes     = 1200
	taskDetailMaxNotesRunes       = 3000
)

const taskDetailGenerationSystemPrompt = `You are Tora's task detail generator.
Return ONLY valid JSON with keys:
- "summary": string
- "steps": array of strings

Rules:
- Write for a teammate who may not already know how to complete the task.
- Keep "summary" concise and practical: 2-4 short sentences.
- "steps" must be actionable, specific, and ordered.
- Return between 4 and 10 steps when possible.
- Do not write a long essay, markdown headings, or code fences.
- Reuse the project's existing direction and avoid inventing unrelated scope.`

type taskDetailGenerateRequest struct {
	Description *string `json:"description,omitempty"`
	Notes       *string `json:"notes,omitempty"`
}

type taskDetailGenerateAIResponse struct {
	Summary string   `json:"summary"`
	Steps   []string `json:"steps"`
}

func (h *RoomHandler) GenerateRoomTaskDetails(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task storage unavailable"})
		return
	}

	rawRoomID := strings.TrimSpace(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	roomUUID, normalizedRoomID, err := resolveTaskRoomUUID(rawRoomID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}
	requesterMemberID := resolveTaskRequesterMemberID(r)
	if requesterMemberID != "" {
		isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), normalizedRoomID, requesterMemberID)
		if memberErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
			return
		}
		if !isMember {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to generate task details"})
			return
		}
	}
	taskID, err := parseFlexibleTaskUUID(strings.TrimSpace(chi.URLParam(r, "taskId")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid task id"})
		return
	}

	var req taskDetailGenerateRequest
	if r.Body != nil {
		if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
			return
		}
	}

	currentTask, err := h.loadTaskRecordWithRelations(r.Context(), roomUUID, normalizedRoomID, taskID)
	if err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Task not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load task"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), taskDetailRequestTimeout)
	defer cancel()

	workspaceContext := ""
	if snapshot, snapshotErr := BuildWorkspaceSnapshot(ctx, h.redis, h.scylla, normalizedRoomID); snapshotErr == nil && snapshot != nil {
		workspaceContext = formatWorkspaceContextPromptSection(snapshot, 4200)
	}

	limits := getAIOrganizeLimits()
	limits.RequestTimeout = taskDetailRequestTimeout
	if limits.MaxOutputTokens <= 0 || limits.MaxOutputTokens > 1400 {
		limits.MaxOutputTokens = 1400
	}

	rawResponse, err := generateAIOrganizeStructuredJSONWithTier(
		ctx,
		taskDetailGenerationSystemPrompt,
		buildTaskDetailGenerationPrompt(currentTask, workspaceContext, req),
		limits,
		ai.AIModelTierHeavy,
	)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "Failed to generate task details",
			"detail": strings.TrimSpace(err.Error()),
		})
		return
	}

	var generated taskDetailGenerateAIResponse
	if err := json.Unmarshal([]byte(rawResponse), &generated); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "AI returned invalid task detail JSON"})
		return
	}

	summary := truncateRunes(strings.TrimSpace(generated.Summary), taskDetailMaxSummaryRunes)
	steps := normalizeGeneratedTaskSteps(generated.Steps)
	if summary == "" && len(steps) == 0 {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "AI returned empty task details"})
		return
	}

	nextDescription := currentTask.Description
	if req.Description != nil {
		nextDescription = replaceTaskDescriptionBase(currentTask.Description, truncateRunes(strings.TrimSpace(*req.Description), 3200))
	}
	if len(nextDescription) > 4000 {
		nextDescription = truncateRunes(nextDescription, 4000)
	}

	patch := map[string]interface{}{
		taskDetailSummaryFieldKey:     summary,
		taskDetailStepsFieldKey:       steps,
		taskDetailGeneratedAtFieldKey: time.Now().UTC().Format(time.RFC3339),
	}
	if req.Notes != nil {
		normalizedNotes := truncateRunes(strings.TrimSpace(*req.Notes), taskDetailMaxNotesRunes)
		if normalizedNotes == "" {
			patch[taskDetailNotesFieldKey] = nil
		} else {
			patch[taskDetailNotesFieldKey] = normalizedNotes
		}
	}

	mergedCustomFields := mergeTaskCustomFieldsMaps(currentTask.CustomFields, patch)
	customFieldsJSON, err := marshalTaskCustomFields(mergedCustomFields)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid custom_fields payload"})
		return
	}

	now := time.Now().UTC()
	updateQuery := fmt.Sprintf(
		`UPDATE %s SET description = ?, custom_fields = ?, updated_at = ? WHERE room_id = ? AND id = ?`,
		h.scylla.Table("tasks"),
	)
	if err := h.scylla.Session.Query(
		updateQuery,
		nextDescription,
		nullableTaskCustomFieldsJSON(customFieldsJSON),
		now,
		roomUUID,
		taskID,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save generated task details"})
		return
	}

	if h.redis != nil && h.redis.Client != nil {
		_ = h.redis.Client.Del(r.Context(), aiContextCachePrefix+normalizedRoomID).Err()
	}

	updatedTask, err := h.loadTaskRecordWithRelations(r.Context(), roomUUID, normalizedRoomID, taskID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load updated task"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(updatedTask)
}

func buildTaskDetailGenerationPrompt(
	task TaskRecordResponse,
	workspaceContext string,
	req taskDetailGenerateRequest,
) string {
	description := taskDescriptionBaseWithOverride(task.Description, req.Description)
	notes := taskDetailNotesWithOverride(task.CustomFields, req.Notes)

	var builder strings.Builder
	if trimmedContext := strings.TrimSpace(workspaceContext); trimmedContext != "" {
		builder.WriteString("Workspace context:\n")
		builder.WriteString(trimmedContext)
		builder.WriteString("\n\n")
	}

	builder.WriteString("Task to expand:\n")
	builder.WriteString("Title: ")
	builder.WriteString(strings.TrimSpace(task.Title))
	builder.WriteString("\nStatus: ")
	builder.WriteString(strings.TrimSpace(task.Status))
	builder.WriteString("\nSprint: ")
	builder.WriteString(strings.TrimSpace(task.SprintName))
	if task.TaskType != "" {
		builder.WriteString("\nType: ")
		builder.WriteString(strings.TrimSpace(task.TaskType))
	}
	if description != "" {
		builder.WriteString("\nCurrent description: ")
		builder.WriteString(description)
	}
	if notes != "" {
		builder.WriteString("\nTeam notes: ")
		builder.WriteString(notes)
	}
	if task.Budget != nil && *task.Budget > 0 {
		builder.WriteString(fmt.Sprintf("\nBudget: $%.2f", *task.Budget))
	}
	if task.DueDate != nil && !task.DueDate.IsZero() {
		builder.WriteString("\nDue date: ")
		builder.WriteString(task.DueDate.UTC().Format("2006-01-02"))
	}
	if len(task.Roles) > 0 {
		builder.WriteString("\nRoles:\n")
		for _, role := range task.Roles {
			roleName := strings.TrimSpace(role.Role)
			roleResponsibilities := strings.TrimSpace(role.Responsibilities)
			if roleName == "" && roleResponsibilities == "" {
				continue
			}
			builder.WriteString("- ")
			if roleName != "" {
				builder.WriteString(roleName)
			}
			if roleResponsibilities != "" {
				if roleName != "" {
					builder.WriteString(": ")
				}
				builder.WriteString(roleResponsibilities)
			}
			builder.WriteString("\n")
		}
	}
	if len(task.BlockedBy) > 0 {
		builder.WriteString(fmt.Sprintf("Blocked by %d linked task(s).\n", len(task.BlockedBy)))
	}
	if len(task.Subtasks) > 0 {
		builder.WriteString("Existing checklist items:\n")
		for _, subtask := range task.Subtasks {
			content := strings.TrimSpace(subtask.Content)
			if content == "" {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(content)
			if subtask.Completed {
				builder.WriteString(" (already done)")
			}
			builder.WriteString("\n")
		}
	}

	builder.WriteString("\nGenerate a concise summary plus concrete completion steps for this specific task.")
	return builder.String()
}

func taskDescriptionBaseWithOverride(description string, override *string) string {
	if override != nil {
		return strings.TrimSpace(*override)
	}
	base, _ := parseTaskMetadataEntries(description)
	return strings.TrimSpace(base)
}

func taskDetailNotesWithOverride(customFields map[string]interface{}, override *string) string {
	if override != nil {
		return strings.TrimSpace(*override)
	}
	if len(customFields) == 0 {
		return ""
	}
	value, exists := customFields[taskDetailNotesFieldKey]
	if !exists || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func replaceTaskDescriptionBase(description, nextBase string) string {
	trimmedBase := strings.TrimSpace(nextBase)
	_, entries := parseTaskMetadataEntries(description)
	if len(entries) == 0 {
		return trimmedBase
	}
	metadataParts := make([]string, 0, len(entries))
	for _, entry := range entries {
		raw := strings.TrimSpace(entry.raw)
		if raw == "" {
			continue
		}
		metadataParts = append(metadataParts, raw)
	}
	if len(metadataParts) == 0 {
		return trimmedBase
	}
	metadataBlock := "[" + strings.Join(metadataParts, " | ") + "]"
	if trimmedBase == "" {
		return metadataBlock
	}
	return trimmedBase + "\n\n" + metadataBlock
}

func normalizeGeneratedTaskSteps(steps []string) []string {
	if len(steps) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(steps))
	normalized := make([]string, 0, len(steps))
	for _, step := range steps {
		trimmed := truncateRunes(strings.TrimSpace(step), 280)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, trimmed)
		if len(normalized) >= 10 {
			break
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
