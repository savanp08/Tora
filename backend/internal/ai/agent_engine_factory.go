package ai

import (
	"encoding/json"
	"strings"
	"time"
)

type AgentProviderResolver func(modelTier string) Provider

type AgentEngineFactory struct {
	ctxBuilder       *ContextBuilder
	providerResolver AgentProviderResolver
}

func NewAgentEngineFactory(ctxBuilder *ContextBuilder, providerResolver AgentProviderResolver) *AgentEngineFactory {
	return &AgentEngineFactory{
		ctxBuilder:       ctxBuilder,
		providerResolver: providerResolver,
	}
}

func (f *AgentEngineFactory) ContextBuilder() *ContextBuilder {
	if f == nil {
		return nil
	}
	return f.ctxBuilder
}

func (f *AgentEngineFactory) New(roomID string, authContext AgentAuthContext, modelTier string) *AgentEngine {
	if f == nil {
		return nil
	}
	var provider Provider
	if f.providerResolver != nil {
		provider = f.providerResolver(strings.TrimSpace(modelTier))
	}
	return NewAgentEngine(provider, f.ctxBuilder, roomID, authContext)
}

func BuildActionsJSONFromAudit(events []AgentEvent) (string, error) {
	actions := buildAppliedTaskActionsFromAudit(events)
	if len(actions) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(actions)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func BuildCanvasActionsJSONFromAudit(events []AgentEvent) (string, error) {
	actions := buildCanvasActionsFromAudit(events)
	if len(actions) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(actions)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func buildAppliedTaskActionsFromAudit(events []AgentEvent) []map[string]any {
	if len(events) == 0 {
		return nil
	}

	actions := make([]map[string]any, 0, len(events))
	for _, event := range events {
		if strings.TrimSpace(event.Kind) != "tool_result" {
			continue
		}
		if isSerializedToolError(event.Result) {
			continue
		}

		switch strings.TrimSpace(event.Tool) {
		case "create_task":
			if action := buildAppliedCreateAction(event); len(action) > 0 {
				actions = append(actions, action)
			}
		case "update_task":
			if action := buildAppliedUpdateAction(event); len(action) > 0 {
				actions = append(actions, action)
			}
		case "delete_task":
			if action := buildAppliedDeleteAction(event); len(action) > 0 {
				actions = append(actions, action)
			}
		}
	}
	return actions
}

func buildCanvasActionsFromAudit(events []AgentEvent) []map[string]any {
	if len(events) == 0 {
		return nil
	}

	latestByPath := make(map[string]map[string]any)
	order := make([]string, 0, len(events))
	for _, event := range events {
		if strings.TrimSpace(event.Kind) != "tool_result" || strings.TrimSpace(event.Tool) != "write_canvas" {
			continue
		}
		if isSerializedToolError(event.Result) {
			continue
		}

		filePath := auditStringField(event.Result, "path", "Path")
		if filePath == "" {
			filePath = auditStringField(event.Input, "file_path", "path")
		}
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			continue
		}

		change := map[string]any{
			"kind":            "canvas_write",
			"file_path":       filePath,
			"content":         auditStringField(event.Input, "content"),
			"description":     auditStringField(event.Input, "description"),
			"already_applied": false,
		}
		if lines, ok := auditIntField(event.Result, "lines", "Lines"); ok {
			change["lines"] = lines
		}
		if _, seen := latestByPath[filePath]; !seen {
			order = append(order, filePath)
		}
		latestByPath[filePath] = change
	}

	if len(latestByPath) == 0 {
		return nil
	}
	actions := make([]map[string]any, 0, len(latestByPath))
	for _, path := range order {
		change, ok := latestByPath[path]
		if !ok {
			continue
		}
		actions = append(actions, change)
	}
	return actions
}

func buildAppliedCreateAction(event AgentEvent) map[string]any {
	task, ok := coerceAuditTaskCtx(event.Result)
	if !ok {
		return nil
	}

	action := map[string]any{
		"kind":            "task_create",
		"title":           strings.TrimSpace(task.Title),
		"status":          strings.TrimSpace(task.Status),
		"sprint":          strings.TrimSpace(task.SprintName),
		"task_type":       strings.TrimSpace(task.TaskType),
		"already_applied": true,
	}
	if strings.TrimSpace(task.Description) != "" {
		action["description"] = strings.TrimSpace(task.Description)
	}
	if task.Budget != nil {
		action["budget"] = *task.Budget
	}
	if task.StartDate != nil && !task.StartDate.IsZero() {
		action["start_date"] = task.StartDate.UTC().Format(time.RFC3339)
	}
	if task.DueDate != nil && !task.DueDate.IsZero() {
		action["due_date"] = task.DueDate.UTC().Format(time.RFC3339)
	}
	if roles := marshalAuditRoles(task.Roles); len(roles) > 0 {
		action["roles"] = roles
	}
	return action
}

func buildAppliedUpdateAction(event AgentEvent) map[string]any {
	task, ok := coerceAuditTaskCtx(event.Result)
	if !ok {
		return nil
	}

	changes := make(map[string]any)
	for key, value := range event.Input {
		normalizedKey := strings.TrimSpace(key)
		switch normalizedKey {
		case "", "task_id", "task_title", "task_sprint", "task_parent":
			continue
		default:
			changes[normalizedKey] = value
		}
	}

	action := map[string]any{
		"kind":            "task_update",
		"task_id":         firstNonEmpty(strings.TrimSpace(task.ID), auditStringField(event.Input, "task_id")),
		"task_title":      firstNonEmpty(strings.TrimSpace(task.Title), auditStringField(event.Input, "task_title")),
		"task_sprint":     firstNonEmpty(strings.TrimSpace(task.SprintName), auditStringField(event.Input, "task_sprint")),
		"already_applied": true,
	}
	if taskParent := auditStringField(event.Input, "task_parent", "taskParent"); taskParent != "" {
		action["task_parent"] = taskParent
	}
	if len(changes) > 0 {
		action["changes"] = changes
	}
	return action
}

func buildAppliedDeleteAction(event AgentEvent) map[string]any {
	taskID := auditStringField(event.Result, "task_id", "taskId", "ID", "id")
	if taskID == "" {
		taskID = auditStringField(event.Input, "task_id", "taskId", "id")
	}
	taskTitle := auditStringField(event.Result, "task_title", "taskTitle", "title", "Title")
	if taskTitle == "" {
		taskTitle = auditStringField(event.Input, "task_title", "taskTitle", "title")
	}

	action := map[string]any{
		"kind":            "task_delete",
		"task_id":         strings.TrimSpace(taskID),
		"task_title":      strings.TrimSpace(taskTitle),
		"already_applied": true,
	}
	if taskSprint := auditStringField(event.Input, "task_sprint", "taskSprint"); taskSprint != "" {
		action["task_sprint"] = taskSprint
	}
	if taskParent := auditStringField(event.Input, "task_parent", "taskParent"); taskParent != "" {
		action["task_parent"] = taskParent
	}
	return action
}

func auditIntField(source any, keys ...string) (int, bool) {
	record, ok := source.(map[string]any)
	if !ok {
		return 0, false
	}
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed, true
		case int8:
			return int(typed), true
		case int16:
			return int(typed), true
		case int32:
			return int(typed), true
		case int64:
			return int(typed), true
		case float32:
			return int(typed), true
		case float64:
			return int(typed), true
		case json.Number:
			parsed, err := typed.Int64()
			if err == nil {
				return int(parsed), true
			}
		}
	}
	return 0, false
}

func coerceAuditTaskCtx(result any) (TaskCtx, bool) {
	switch typed := result.(type) {
	case TaskCtx:
		return typed, true
	case *TaskCtx:
		if typed == nil {
			return TaskCtx{}, false
		}
		return *typed, true
	case map[string]any:
		return taskCtxFromAuditMap(typed)
	default:
		return TaskCtx{}, false
	}
}

func taskCtxFromAuditMap(source map[string]any) (TaskCtx, bool) {
	if len(source) == 0 {
		return TaskCtx{}, false
	}

	task := TaskCtx{
		ID:          auditStringField(source, "ID", "id"),
		Title:       auditStringField(source, "Title", "title"),
		Description: auditStringField(source, "Description", "description"),
		Status:      auditStringField(source, "Status", "status"),
		TaskType:    auditStringField(source, "TaskType", "task_type", "taskType"),
		SprintName:  auditStringField(source, "SprintName", "sprint_name", "sprintName", "sprint"),
	}
	if budget, ok := auditFloatField(source, "Budget", "budget"); ok {
		task.Budget = &budget
	}
	if startDate, ok := auditTimeField(source, "StartDate", "start_date", "startDate"); ok {
		task.StartDate = &startDate
	}
	if dueDate, ok := auditTimeField(source, "DueDate", "due_date", "dueDate"); ok {
		task.DueDate = &dueDate
	}
	if rolesRaw, ok := source["Roles"]; ok {
		task.Roles = coerceAuditRoleSlice(rolesRaw)
	} else if rolesRaw, ok := source["roles"]; ok {
		task.Roles = coerceAuditRoleSlice(rolesRaw)
	}
	if strings.TrimSpace(task.ID) == "" && strings.TrimSpace(task.Title) == "" {
		return TaskCtx{}, false
	}
	return task, true
}

func coerceAuditRoleSlice(value any) []RoleCtx {
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]RoleCtx); ok {
			return append([]RoleCtx(nil), typed...)
		}
		if typed, ok := value.([]map[string]any); ok {
			items = make([]any, 0, len(typed))
			for _, entry := range typed {
				items = append(items, entry)
			}
		} else {
			return nil
		}
	}

	roles := make([]RoleCtx, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case RoleCtx:
			roles = append(roles, typed)
		case *RoleCtx:
			if typed != nil {
				roles = append(roles, *typed)
			}
		case map[string]any:
			role := RoleCtx{
				Role:             auditStringField(typed, "role", "Role"),
				Responsibilities: auditStringField(typed, "responsibilities", "Responsibilities"),
			}
			if strings.TrimSpace(role.Role) != "" || strings.TrimSpace(role.Responsibilities) != "" {
				roles = append(roles, role)
			}
		}
	}
	return roles
}

func marshalAuditRoles(roles []RoleCtx) []map[string]any {
	if len(roles) == 0 {
		return nil
	}

	encoded := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		item := map[string]any{
			"role": strings.TrimSpace(role.Role),
		}
		if strings.TrimSpace(role.Responsibilities) != "" {
			item["responsibilities"] = strings.TrimSpace(role.Responsibilities)
		}
		encoded = append(encoded, item)
	}
	return encoded
}

func auditStringField(source any, keys ...string) string {
	record, ok := source.(map[string]any)
	if !ok {
		if alt, ok := source.(map[string]interface{}); ok {
			record = map[string]any(alt)
		}
	}
	if !ok || len(record) == 0 {
		return ""
	}
	for _, key := range keys {
		if value, exists := record[key]; exists {
			if text := strings.TrimSpace(toAuditString(value)); text != "" {
				return text
			}
		}
	}
	return ""
}

func auditFloatField(source map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, exists := source[key]
		if !exists || value == nil {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed, true
		case float32:
			return float64(typed), true
		case int:
			return float64(typed), true
		case int64:
			return float64(typed), true
		case json.Number:
			parsed, err := typed.Float64()
			if err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func auditTimeField(source map[string]any, keys ...string) (time.Time, bool) {
	for _, key := range keys {
		value, exists := source[key]
		if !exists || value == nil {
			continue
		}
		switch typed := value.(type) {
		case time.Time:
			if !typed.IsZero() {
				return typed.UTC(), true
			}
		case *time.Time:
			if typed != nil && !typed.IsZero() {
				return typed.UTC(), true
			}
		case string:
			if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed)); err == nil {
				return parsed.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func toAuditString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case interface{ String() string }:
		return typed.String()
	default:
		return ""
	}
}
