package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/ai"
)

const aiTimelineSystemPrompt = `You are a senior Agile planning assistant.
Generate a project timeline plan in STRICT JSON format.

Strict response rules:
1. Return ONLY valid JSON. No markdown, no prose.
2. Root object keys: "project_name" (string), "sprints" (array).
3. Each sprint object keys:
   - "name" (string, required)
   - "start_date" (YYYY-MM-DD, required)
   - "end_date" (YYYY-MM-DD, required)
   - "tasks" (array, required)
4. Each task object keys:
   - "title" (string, required)
   - "status" (string: "todo", "in_progress", or "done"; default "todo")
   - "effort_score" (integer 1-10)
   - "type" (string)
   - "description" (string, optional)
5. Do not add unknown keys outside this schema.`

type aiTimelineGenerateRequest struct {
	Prompt string `json:"prompt"`
	UserID string `json:"userId,omitempty"`
}

type aiTimelineGenerateResponse struct {
	ProjectName   string             `json:"project_name"`
	TotalProgress float64            `json:"total_progress,omitempty"`
	Sprints       []aiTimelineSprint `json:"sprints"`
	PersistedTask int                `json:"persisted_task_count"`
}

type aiTimelineProject struct {
	ProjectName   string             `json:"project_name"`
	TotalProgress float64            `json:"total_progress,omitempty"`
	Sprints       []aiTimelineSprint `json:"sprints"`
}

type aiTimelineSprint struct {
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name"`
	StartDate string           `json:"start_date"`
	EndDate   string           `json:"end_date"`
	Tasks     []aiTimelineTask `json:"tasks"`
}

type aiTimelineTask struct {
	TaskID      string `json:"task_id,omitempty"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	EffortScore int    `json:"effort_score"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

func (h *RoomHandler) HandleAIGenerateTimeline(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		writeAITimelineError(w, http.StatusServiceUnavailable, "Task storage unavailable")
		return
	}
	if h.redis == nil || h.redis.Client == nil {
		writeAITimelineError(w, http.StatusServiceUnavailable, "Room storage unavailable")
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid room id")
		return
	}

	var req aiTimelineGenerateRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1*1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeAITimelineError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	userID := normalizeIdentifier(
		firstNonEmpty(
			AuthUserIDFromContext(r.Context()),
			req.UserID,
			r.URL.Query().Get("userId"),
			r.URL.Query().Get("user_id"),
			r.Header.Get("X-User-Id"),
		),
	)
	if userID == "" {
		writeAITimelineError(w, http.StatusUnauthorized, "User context is required")
		return
	}

	isMember, memberErr := h.isRoomMember(r.Context(), roomID, userID)
	if memberErr != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to verify room membership")
		return
	}
	if !isMember {
		writeAITimelineError(w, http.StatusForbidden, "Join the room to generate a timeline")
		return
	}

	limits := getAIOrganizeLimits()
	generateCtx, cancel := context.WithTimeout(r.Context(), limits.RequestTimeout)
	defer cancel()

	generated, generateErr := generateAITimelineProject(generateCtx, roomID, prompt, limits)
	if generateErr != nil {
		switch {
		case errors.Is(generateErr, context.Canceled), errors.Is(generateErr, context.DeadlineExceeded):
			writeAITimelineError(w, http.StatusGatewayTimeout, "AI timeline request timed out")
		case errors.Is(generateErr, ai.ErrAllAIProvidersExhausted):
			writeAITimelineError(w, http.StatusServiceUnavailable, "AI providers are currently unavailable")
		default:
			writeAITimelineError(w, http.StatusBadGateway, "Failed to generate timeline from AI")
		}
		return
	}

	roomUUID, _, parseRoomErr := resolveTaskRoomUUID(roomID)
	if parseRoomErr != nil {
		writeAITimelineError(w, http.StatusBadRequest, "Invalid room id")
		return
	}
	assigneeID := resolveAuthAssigneeUUID(r.Context())
	persistedCount, persistErr := h.persistAITimelineTasks(r.Context(), roomUUID, assigneeID, &generated)
	if persistErr != nil {
		writeAITimelineError(w, http.StatusInternalServerError, "Failed to persist generated timeline tasks")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(aiTimelineGenerateResponse{
		ProjectName:   generated.ProjectName,
		TotalProgress: generated.TotalProgress,
		Sprints:       generated.Sprints,
		PersistedTask: persistedCount,
	})
}

func resolveAuthAssigneeUUID(ctx context.Context) *gocql.UUID {
	raw := strings.TrimSpace(AuthUserIDFromContext(ctx))
	if raw == "" {
		return nil
	}
	parsed, err := gocql.ParseUUID(raw)
	if err != nil {
		return nil
	}
	return &parsed
}

func generateAITimelineProject(
	ctx context.Context,
	roomID string,
	prompt string,
	limits aiOrganizeLimits,
) (aiTimelineProject, error) {
	userPrompt := fmt.Sprintf(
		"Room ID: %s\nUser request: %s\nGenerate a sprint plan now.",
		roomID,
		strings.TrimSpace(prompt),
	)
	raw, err := generateAIOrganizeStructuredJSON(ctx, aiTimelineSystemPrompt, userPrompt, limits)
	if err != nil {
		return aiTimelineProject{}, err
	}
	return parseAITimelineProject(raw)
}

func parseAITimelineProject(raw string) (aiTimelineProject, error) {
	content := extractJSONObject(raw)
	if strings.TrimSpace(content) == "" {
		return aiTimelineProject{}, fmt.Errorf("ai timeline response did not contain JSON")
	}

	var parsed aiTimelineProject
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return aiTimelineProject{}, err
	}

	normalized := normalizeAITimelineProject(parsed)
	if len(normalized.Sprints) == 0 {
		return aiTimelineProject{}, fmt.Errorf("ai timeline returned no valid sprints")
	}

	taskCount := 0
	for _, sprint := range normalized.Sprints {
		taskCount += len(sprint.Tasks)
	}
	if taskCount == 0 {
		return aiTimelineProject{}, fmt.Errorf("ai timeline returned no valid tasks")
	}
	return normalized, nil
}

func normalizeAITimelineProject(input aiTimelineProject) aiTimelineProject {
	projectName := truncateRunes(strings.TrimSpace(input.ProjectName), 180)
	if projectName == "" {
		projectName = "AI Project Timeline"
	}

	now := time.Now().UTC()
	normalizedSprints := make([]aiTimelineSprint, 0, len(input.Sprints))
	for sprintIndex, sprint := range input.Sprints {
		sprintName := truncateRunes(strings.TrimSpace(sprint.Name), 160)
		if sprintName == "" {
			sprintName = fmt.Sprintf("Sprint %d", sprintIndex+1)
		}

		startDate := normalizeTimelineDate(sprint.StartDate, now)
		endDate := normalizeTimelineDate(sprint.EndDate, startDate.AddDate(0, 0, 7))
		if endDate.Before(startDate) {
			endDate = startDate.AddDate(0, 0, 7)
		}

		normalizedTasks := make([]aiTimelineTask, 0, len(sprint.Tasks))
		for _, task := range sprint.Tasks {
			title := truncateRunes(strings.TrimSpace(task.Title), 240)
			if title == "" {
				continue
			}
			status := normalizeTaskStatusValue(task.Status)
			if status == "" {
				status = "todo"
			}
			effort := task.EffortScore
			if effort < 1 || effort > 10 {
				effort = 3
			}
			taskType := truncateRunes(strings.ToLower(strings.TrimSpace(task.Type)), 48)
			if taskType == "" {
				taskType = "general"
			}
			description := truncateRunes(strings.TrimSpace(task.Description), 4000)
			normalizedTasks = append(normalizedTasks, aiTimelineTask{
				Title:       title,
				Status:      status,
				EffortScore: effort,
				Type:        taskType,
				Description: description,
			})
		}
		if len(normalizedTasks) == 0 {
			continue
		}

		normalizedSprints = append(normalizedSprints, aiTimelineSprint{
			ID:        fmt.Sprintf("sprint-%d", sprintIndex+1),
			Name:      sprintName,
			StartDate: startDate.Format("2006-01-02"),
			EndDate:   endDate.Format("2006-01-02"),
			Tasks:     normalizedTasks,
		})
	}

	return aiTimelineProject{
		ProjectName:   projectName,
		TotalProgress: 0,
		Sprints:       normalizedSprints,
	}
}

func normalizeTimelineDate(raw string, fallback time.Time) time.Time {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback.UTC()
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return fallback.UTC()
	}
	return parsed.UTC()
}

func (h *RoomHandler) persistAITimelineTasks(
	ctx context.Context,
	roomUUID gocql.UUID,
	assigneeID *gocql.UUID,
	project *aiTimelineProject,
) (int, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil || project == nil {
		return 0, fmt.Errorf("task storage unavailable")
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table("tasks"),
	)

	inserted := 0
	now := time.Now().UTC()
	for sprintIndex := range project.Sprints {
		sprint := &project.Sprints[sprintIndex]
		sprintName := truncateRunes(strings.TrimSpace(sprint.Name), 160)
		for taskIndex := range sprint.Tasks {
			task := &sprint.Tasks[taskIndex]
			taskID, taskIDErr := gocql.RandomUUID()
			if taskIDErr != nil {
				return inserted, taskIDErr
			}

			taskIDString := strings.TrimSpace(taskID.String())
			title := truncateRunes(strings.TrimSpace(task.Title), 240)
			if title == "" {
				continue
			}
			status := normalizeTaskStatusValue(task.Status)
			if status == "" {
				status = "todo"
			}

			description := truncateRunes(strings.TrimSpace(task.Description), 3600)
			metadataParts := make([]string, 0, 3)
			if task.Type != "" {
				metadataParts = append(metadataParts, fmt.Sprintf("Type: %s", strings.TrimSpace(task.Type)))
			}
			if task.EffortScore > 0 {
				metadataParts = append(metadataParts, fmt.Sprintf("Effort: %d", task.EffortScore))
			}
			if sprint.StartDate != "" || sprint.EndDate != "" {
				metadataParts = append(
					metadataParts,
					fmt.Sprintf("Sprint Window: %s -> %s", strings.TrimSpace(sprint.StartDate), strings.TrimSpace(sprint.EndDate)),
				)
			}
			if len(metadataParts) > 0 {
				meta := "[" + strings.Join(metadataParts, " | ") + "]"
				if description == "" {
					description = meta
				} else {
					description = truncateRunes(description+"\n\n"+meta, 4000)
				}
			}

			if err := h.scylla.Session.Query(
				query,
				roomUUID,
				taskID,
				title,
				description,
				status,
				sprintName,
				assigneeID,
				now,
				now,
			).WithContext(ctx).Exec(); err != nil {
				return inserted, err
			}

			task.TaskID = taskIDString
			task.Title = title
			task.Description = description
			task.Status = status
			inserted++
		}
	}

	return inserted, nil
}

func writeAITimelineError(w http.ResponseWriter, status int, message string) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": strings.TrimSpace(message),
	})
}
