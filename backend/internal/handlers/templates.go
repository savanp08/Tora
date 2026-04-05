package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/models"
	industrytemplates "github.com/savanp08/converse/internal/templates"
)

type applyTemplateRequest struct {
	TemplateID       string `json:"template_id"`
	TemplateIDAlt    string `json:"templateId"`
	ClearExisting    *bool  `json:"clear_existing"`
	ClearExistingAlt *bool  `json:"clearExisting"`
}

type applyTemplateResponse struct {
	Success                bool   `json:"success"`
	TemplateID             string `json:"template_id"`
	TemplateName           string `json:"template_name"`
	FieldsCreated          int    `json:"fields_created"`
	TasksCreated           int    `json:"tasks_created"`
	AutomationRulesCreated int    `json:"automation_rules_created"`
}

func (h *RoomHandler) GetIndustryTemplates(w http.ResponseWriter, r *http.Request) {
	includeTasks := parseTemplateIncludeTasks(r.URL.Query().Get("include_tasks"))
	templates := industrytemplates.ListIndustryTemplates(includeTasks)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(templates)
}

func (h *RoomHandler) ApplyRoomTemplate(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Template storage unavailable"})
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
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to apply templates"})
			return
		}
	}

	var req applyTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	templateID := strings.TrimSpace(firstNonEmpty(req.TemplateID, req.TemplateIDAlt))
	if templateID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "template_id is required"})
		return
	}

	clearExisting := false
	if req.ClearExisting != nil {
		clearExisting = *req.ClearExisting
	}
	if req.ClearExistingAlt != nil {
		clearExisting = *req.ClearExistingAlt
	}

	templateName := "Blank Workspace"
	isBlankTemplate := strings.EqualFold(templateID, "blank")
	var template industrytemplates.IndustryTemplate
	if !isBlankTemplate {
		resolvedTemplate, found := industrytemplates.FindIndustryTemplateByID(templateID)
		if !found {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unknown template"})
			return
		}
		template = resolvedTemplate
		templateName = resolvedTemplate.Name
	}
	previousProjectType := h.getRoomProjectTypeOrDefault(r.Context(), normalizedRoomID)
	targetProjectType := previousProjectType
	if isBlankTemplate {
		targetProjectType = "general"
	} else if strings.TrimSpace(template.ProjectType) != "" {
		targetProjectType = template.ProjectType
	}
	targetProjectType = models.NormalizeProjectType(targetProjectType)

	roomHasContent, err := h.roomHasTemplateContent(r.Context(), roomUUID, normalizedRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to inspect existing room content"})
		return
	}
	if roomHasContent && !clearExisting {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "This room already has tasks or fields. Re-submit with clear_existing=true to replace them.",
		})
		return
	}

	if clearExisting {
		if err := h.clearTemplateManagedRoomState(r.Context(), normalizedRoomID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to clear existing room content"})
			return
		}
	}

	fieldsCreated := 0
	tasksCreated := 0
	automationRulesCreated := 0
	rollbackRequired := false

	defer func() {
		if !rollbackRequired {
			return
		}
		_ = h.clearTemplateManagedRoomState(r.Context(), normalizedRoomID)
		if targetProjectType != previousProjectType {
			_ = h.updateRoomProjectType(r.Context(), normalizedRoomID, previousProjectType)
		}
	}()

	fieldNameToID := make(map[string]string)
	for _, field := range template.FieldSchemas {
		createdField, createErr := h.createTemplateFieldSchema(r.Context(), normalizedRoomID, field)
		if createErr != nil {
			rollbackRequired = true
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create template field schemas"})
			return
		}
		fieldNameToID[strings.ToLower(strings.TrimSpace(createdField.Name))] = createdField.FieldID
		fieldsCreated += 1
	}

	requesterAssigneeID := resolveTaskRequesterAssigneeUUID(r)
	statusActorID := strings.TrimSpace(resolveTaskRequesterID(r))
	statusActorName := resolveTaskRequesterName(r)
	for _, task := range template.SampleTasks {
		if err := h.createTemplateTask(
			r.Context(),
			roomUUID,
			normalizedRoomID,
			task,
			fieldNameToID,
			requesterAssigneeID,
			statusActorID,
			statusActorName,
		); err != nil {
			rollbackRequired = true
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create starter tasks"})
			return
		}
		tasksCreated += 1
	}

	for index, rule := range template.AutomationRules {
		if err := h.insertRoomAutomationRule(r.Context(), normalizedRoomID, requesterMemberID, index, rule); err != nil {
			rollbackRequired = true
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create template automation rules"})
			return
		}
		automationRulesCreated += 1
	}

	if targetProjectType != previousProjectType {
		if err := h.updateRoomProjectType(r.Context(), normalizedRoomID, targetProjectType); err != nil {
			rollbackRequired = true
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update workspace setup"})
			return
		}
	}

	rollbackRequired = false
	h.broadcastRoomEvent(normalizedRoomID, "template_applied", map[string]interface{}{
		"template_id":              strings.TrimSpace(templateID),
		"template_name":            templateName,
		"project_type":             targetProjectType,
		"fields_created":           fieldsCreated,
		"tasks_created":            tasksCreated,
		"automation_rules_created": automationRulesCreated,
		"applied_at":               time.Now().UTC().Format(time.RFC3339),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(applyTemplateResponse{
		Success:                true,
		TemplateID:             strings.TrimSpace(templateID),
		TemplateName:           templateName,
		FieldsCreated:          fieldsCreated,
		TasksCreated:           tasksCreated,
		AutomationRulesCreated: automationRulesCreated,
	})
}

func parseTemplateIncludeTasks(raw string) bool {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	if normalized == "" {
		return false
	}
	includeTasks, err := strconv.ParseBool(normalized)
	return err == nil && includeTasks
}

func (h *RoomHandler) roomHasTemplateContent(
	ctx context.Context,
	roomUUID gocql.UUID,
	roomID string,
) (bool, error) {
	tasksQuery := fmt.Sprintf(`SELECT id FROM %s WHERE room_id = ? LIMIT 1`, h.scylla.Table("tasks"))
	var taskID gocql.UUID
	if err := h.scylla.Session.Query(tasksQuery, roomUUID).WithContext(ctx).Scan(&taskID); err == nil {
		return true, nil
	} else if err != gocql.ErrNotFound {
		return false, err
	}

	fieldsQuery := fmt.Sprintf(
		`SELECT field_id FROM %s WHERE room_id = ? LIMIT 1`,
		h.scylla.Table(roomFieldSchemaTable),
	)
	var fieldID string
	if err := h.scylla.Session.Query(fieldsQuery, roomID).WithContext(ctx).Scan(&fieldID); err == nil {
		return true, nil
	} else if err != gocql.ErrNotFound {
		return false, err
	}

	hasRules, err := h.roomHasAutomationRules(ctx, roomID)
	if err != nil {
		return false, err
	}
	return hasRules, nil
}

func (h *RoomHandler) clearTemplateManagedRoomState(ctx context.Context, roomID string) error {
	if err := h.deleteRoomTasks(ctx, roomID); err != nil {
		return err
	}
	if err := h.deleteRoomFieldSchemas(ctx, roomID); err != nil {
		return err
	}
	if err := h.deleteRoomAutomationRules(ctx, roomID); err != nil {
		return err
	}
	return nil
}

func (h *RoomHandler) deleteRoomFieldSchemas(ctx context.Context, roomID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, h.scylla.Table(roomFieldSchemaTable))
	return h.scylla.Session.Query(query, roomID).WithContext(ctx).Exec()
}

func (h *RoomHandler) createTemplateFieldSchema(
	ctx context.Context,
	roomID string,
	field industrytemplates.TemplateField,
) (FieldSchema, error) {
	name := strings.TrimSpace(field.Name)
	if name == "" {
		return FieldSchema{}, fmt.Errorf("field name is required")
	}
	fieldType := normalizeFieldSchemaType(field.FieldType)
	if !isSupportedFieldSchemaType(fieldType) {
		return FieldSchema{}, fmt.Errorf("unsupported field type")
	}
	options, err := normalizeFieldSchemaOptionsForType(fieldType, field.Options)
	if err != nil {
		return FieldSchema{}, err
	}

	optionsJSON, err := marshalFieldSchemaOptions(options)
	if err != nil {
		return FieldSchema{}, err
	}

	fieldID := generateRoomFieldSchemaID(roomID, name)
	position := field.Position
	if position < 0 {
		position = 0
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, field_id, name, field_type, options, position, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table(roomFieldSchemaTable),
	)
	now := time.Now().UTC()
	if err := h.scylla.Session.Query(
		query,
		roomID,
		fieldID,
		name,
		fieldType,
		optionsJSON,
		position,
		now,
	).WithContext(ctx).Exec(); err != nil {
		return FieldSchema{}, err
	}

	return FieldSchema{
		FieldID:   fieldID,
		RoomID:    roomID,
		Name:      name,
		FieldType: fieldType,
		Options:   options,
		Position:  position,
	}, nil
}

func (h *RoomHandler) createTemplateTask(
	ctx context.Context,
	roomUUID gocql.UUID,
	roomID string,
	task industrytemplates.TemplateTask,
	fieldNameToID map[string]string,
	defaultAssigneeID *gocql.UUID,
	statusActorID string,
	statusActorName string,
) error {
	taskUUID, err := gocql.RandomUUID()
	if err != nil {
		return err
	}

	title := strings.TrimSpace(task.Title)
	if title == "" {
		return fmt.Errorf("task title is required")
	}
	status := normalizeTaskStatusValue(task.Status)
	if status == "" {
		status = "todo"
	}
	sprintName := strings.TrimSpace(task.SprintName)
	customFields := make(map[string]interface{})
	for fieldName, rawValue := range task.CustomFields {
		resolvedFieldID := fieldNameToID[strings.ToLower(strings.TrimSpace(fieldName))]
		if resolvedFieldID == "" {
			continue
		}
		normalizedValue := strings.TrimSpace(rawValue)
		if normalizedValue == "" {
			continue
		}
		customFields[resolvedFieldID] = normalizedValue
	}
	customFieldsJSON, err := marshalTaskCustomFields(sanitizeTaskCustomFieldsMap(customFields))
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, custom_fields, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table("tasks"),
	)
	return h.scylla.Session.Query(
		query,
		roomUUID,
		taskUUID,
		title,
		"",
		status,
		sprintName,
		defaultAssigneeID,
		nullableTaskCustomFieldsJSON(customFieldsJSON),
		nullableTrimmedText(statusActorID),
		nullableTrimmedText(statusActorName),
		now,
		now,
		now,
	).WithContext(ctx).Exec()
}
