package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
)

const (
	intakeFormsTableName      = "intake_forms"
	formSubmissionsTableName  = "form_submissions"
	defaultIntakeTargetStatus = "todo"
)

var supportedIntakeFieldTypes = map[string]struct{}{
	"text":     {},
	"textarea": {},
	"number":   {},
	"email":    {},
	"select":   {},
	"checkbox": {},
}

type IntakeFormField struct {
	FieldID   string   `json:"field_id"`
	Label     string   `json:"label"`
	FieldType string   `json:"field_type"`
	Required  bool     `json:"required"`
	Options   []string `json:"options,omitempty"`
}

type IntakeFormRecord struct {
	FormID          string            `json:"form_id"`
	RoomID          string            `json:"room_id"`
	Title           string            `json:"title"`
	Description     string            `json:"description,omitempty"`
	Fields          []IntakeFormField `json:"fields"`
	TargetStatus    string            `json:"target_status"`
	TargetSprint    string            `json:"target_sprint,omitempty"`
	Enabled         bool              `json:"enabled"`
	SubmissionCount int               `json:"submission_count,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

type IntakeFormSubmissionRecord struct {
	FormID         string                 `json:"form_id"`
	SubmissionID   string                 `json:"submission_id"`
	RoomID         string                 `json:"room_id"`
	TaskID         string                 `json:"task_id,omitempty"`
	Data           map[string]interface{} `json:"data,omitempty"`
	SubmitterEmail string                 `json:"submitter_email,omitempty"`
	SubmittedAt    time.Time              `json:"submitted_at"`
}

type createIntakeFormRequest struct {
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Fields       []IntakeFormField `json:"fields"`
	TargetStatus string            `json:"target_status"`
	TargetSprint string            `json:"target_sprint"`
	Enabled      *bool             `json:"enabled"`
}

type updateIntakeFormRequest struct {
	Title           *string            `json:"title"`
	Description     *string            `json:"description"`
	Fields          *[]IntakeFormField `json:"fields"`
	TargetStatus    *string            `json:"target_status"`
	TargetStatusAlt *string            `json:"targetStatus"`
	TargetSprint    *string            `json:"target_sprint"`
	TargetSprintAlt *string            `json:"targetSprint"`
	Enabled         *bool              `json:"enabled"`
}

type submitPublicIntakeFormRequest struct {
	Fields         map[string]interface{} `json:"fields"`
	SubmitterEmail string                 `json:"submitter_email"`
}

func (h *RoomHandler) ensureIntakeFormsSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	intakeFormsTable := h.scylla.Table(intakeFormsTableName)
	formSubmissionsTable := h.scylla.Table(formSubmissionsTableName)

	createFormsQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id text,
		form_id text,
		title text,
		description text,
		fields text,
		target_status text,
		target_sprint text,
		enabled boolean,
		created_at timestamp,
		PRIMARY KEY (room_id, form_id)
	) WITH CLUSTERING ORDER BY (form_id ASC)`, intakeFormsTable)
	if err := h.scylla.Session.Query(createFormsQuery).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		log.Printf("[intake-forms] ensure forms schema failed: %v", err)
	}

	createSubmissionsQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		form_id text,
		submission_id text,
		room_id text,
		task_id text,
		data text,
		submitter_email text,
		submitted_at timestamp,
		PRIMARY KEY (form_id, submission_id)
	) WITH CLUSTERING ORDER BY (submission_id DESC)`, formSubmissionsTable)
	if err := h.scylla.Session.Query(createSubmissionsQuery).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		log.Printf("[intake-forms] ensure submissions schema failed: %v", err)
	}

	indexQuery := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (form_id)`, intakeFormsTable)
	if err := h.scylla.Session.Query(indexQuery).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		log.Printf("[intake-forms] ensure forms form_id index failed: %v", err)
	}
}

func normalizeIntakeFormID(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}
	var normalized strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' ||
			r == '-' {
			normalized.WriteRune(r)
		}
	}
	return strings.TrimSpace(normalized.String())
}

func generateIntakeFormID(roomID, title string) string {
	digest := sha1.Sum([]byte(strings.TrimSpace(roomID) + "|" + strings.TrimSpace(title) + "|" + time.Now().UTC().Format(time.RFC3339Nano)))
	encoded := hex.EncodeToString(digest[:])
	if len(encoded) < 12 {
		return encoded
	}
	return encoded[:12]
}

func generateIntakeSubmissionID(formID, taskID string) string {
	digest := sha1.Sum([]byte(strings.TrimSpace(formID) + "|" + strings.TrimSpace(taskID) + "|" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)))
	encoded := hex.EncodeToString(digest[:])
	if len(encoded) < 16 {
		return encoded
	}
	return encoded[:16]
}

func normalizeIntakeFieldType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "text", "textarea", "number", "email", "select", "checkbox":
		return normalized
	default:
		return ""
	}
}

func sanitizeIntakeFieldOptions(options []string) []string {
	if len(options) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(options))
	sanitized := make([]string, 0, len(options))
	for _, option := range options {
		trimmed := strings.TrimSpace(option)
		if trimmed == "" {
			continue
		}
		lowered := strings.ToLower(trimmed)
		if _, exists := seen[lowered]; exists {
			continue
		}
		seen[lowered] = struct{}{}
		sanitized = append(sanitized, trimmed)
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func marshalIntakeFormFields(fields []IntakeFormField) (string, error) {
	if len(fields) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func parseIntakeFormFields(raw *string) []IntakeFormField {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	var fields []IntakeFormField
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return nil
	}
	return fields
}

func marshalIntakeSubmissionData(data map[string]interface{}) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func parseIntakeSubmissionData(raw *string) map[string]interface{} {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &data); err != nil {
		return nil
	}
	return data
}

func (h *RoomHandler) roomFieldSchemaIDSet(ctx context.Context, roomID string) (map[string]struct{}, error) {
	query := fmt.Sprintf(`SELECT field_id FROM %s WHERE room_id = ?`, h.scylla.Table(roomFieldSchemaTable))
	iter := h.scylla.Session.Query(query, roomID).WithContext(ctx).Iter()
	fieldIDs := make(map[string]struct{}, 16)
	var fieldID string
	for iter.Scan(&fieldID) {
		normalized := normalizeFieldSchemaID(fieldID)
		if normalized == "" {
			continue
		}
		fieldIDs[normalized] = struct{}{}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return fieldIDs, nil
}

func normalizeIntakeFormFieldsForRoom(fields []IntakeFormField, roomFieldIDs map[string]struct{}) ([]IntakeFormField, error) {
	if len(fields) == 0 {
		return nil, fmt.Errorf("at least one form field is required")
	}

	sanitized := make([]IntakeFormField, 0, len(fields))
	seenFieldIDs := make(map[string]struct{}, len(fields))
	for index, field := range fields {
		fieldID := normalizeFieldSchemaID(field.FieldID)
		if fieldID == "" {
			return nil, fmt.Errorf("fields[%d].field_id is required", index)
		}
		if _, exists := seenFieldIDs[fieldID]; exists {
			return nil, fmt.Errorf("fields[%d].field_id is duplicated", index)
		}
		seenFieldIDs[fieldID] = struct{}{}
		if _, exists := roomFieldIDs[fieldID]; !exists {
			return nil, fmt.Errorf("fields[%d].field_id does not exist in room field schemas", index)
		}

		label := strings.TrimSpace(field.Label)
		if label == "" {
			return nil, fmt.Errorf("fields[%d].label is required", index)
		}
		if len(label) > 120 {
			label = label[:120]
		}

		fieldType := normalizeIntakeFieldType(field.FieldType)
		if _, ok := supportedIntakeFieldTypes[fieldType]; !ok {
			return nil, fmt.Errorf("fields[%d].field_type is invalid", index)
		}

		options := sanitizeIntakeFieldOptions(field.Options)
		if fieldType == "select" && len(options) == 0 {
			return nil, fmt.Errorf("fields[%d].options are required for select fields", index)
		}
		if fieldType != "select" {
			options = nil
		}

		sanitized = append(sanitized, IntakeFormField{
			FieldID:   fieldID,
			Label:     label,
			FieldType: fieldType,
			Required:  field.Required,
			Options:   options,
		})
	}
	return sanitized, nil
}

func isValidEmailAddress(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	_, err := mail.ParseAddress(trimmed)
	return err == nil
}

func normalizeIntakeFieldValue(field IntakeFormField, value interface{}) (interface{}, bool, string) {
	switch field.FieldType {
	case "checkbox":
		switch typed := value.(type) {
		case bool:
			if field.Required && !typed {
				return nil, false, "must be checked"
			}
			return typed, true, ""
		case string:
			trimmed := strings.ToLower(strings.TrimSpace(typed))
			if trimmed == "" {
				if field.Required {
					return nil, false, "must be checked"
				}
				return nil, false, ""
			}
			if trimmed == "true" || trimmed == "1" || trimmed == "yes" || trimmed == "on" {
				return true, true, ""
			}
			if trimmed == "false" || trimmed == "0" || trimmed == "no" || trimmed == "off" {
				if field.Required {
					return nil, false, "must be checked"
				}
				return false, true, ""
			}
			return nil, false, "must be true or false"
		case float64:
			if typed == 1 {
				return true, true, ""
			}
			if typed == 0 {
				if field.Required {
					return nil, false, "must be checked"
				}
				return false, true, ""
			}
			return nil, false, "must be true or false"
		default:
			return nil, false, "must be true or false"
		}
	case "number":
		switch typed := value.(type) {
		case float64:
			return typed, true, ""
		case int:
			return float64(typed), true, ""
		case int64:
			return float64(typed), true, ""
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed == "" {
				if field.Required {
					return nil, false, "is required"
				}
				return nil, false, ""
			}
			parsed, err := strconv.ParseFloat(trimmed, 64)
			if err != nil {
				return nil, false, "must be a number"
			}
			return parsed, true, ""
		default:
			return nil, false, "must be a number"
		}
	default:
		stringValue := strings.TrimSpace(fmt.Sprintf("%v", value))
		if stringValue == "" || stringValue == "<nil>" {
			if field.Required {
				return nil, false, "is required"
			}
			return nil, false, ""
		}
		if len(stringValue) > 1024 {
			stringValue = stringValue[:1024]
		}
		if field.FieldType == "email" && !isValidEmailAddress(stringValue) {
			return nil, false, "must be a valid email"
		}
		if field.FieldType == "select" {
			validOption := false
			for _, option := range field.Options {
				if strings.EqualFold(strings.TrimSpace(option), stringValue) {
					stringValue = option
					validOption = true
					break
				}
			}
			if !validOption {
				return nil, false, "must match one of the configured options"
			}
		}
		return stringValue, true, ""
	}
}

func normalizeSubmissionValues(
	fields []IntakeFormField,
	raw map[string]interface{},
) (map[string]interface{}, map[string]string) {
	sanitized := make(map[string]interface{}, len(fields))
	fieldErrors := make(map[string]string)
	for _, field := range fields {
		rawValue, hasValue := raw[field.FieldID]
		if !hasValue {
			if field.Required {
				if field.FieldType == "checkbox" {
					fieldErrors[field.FieldID] = "must be checked"
				} else {
					fieldErrors[field.FieldID] = "is required"
				}
			}
			continue
		}
		normalizedValue, ok, valueErr := normalizeIntakeFieldValue(field, rawValue)
		if valueErr != "" {
			fieldErrors[field.FieldID] = valueErr
			continue
		}
		if !ok {
			continue
		}
		sanitized[field.FieldID] = normalizedValue
	}
	return sanitized, fieldErrors
}

func (h *RoomHandler) loadIntakeFormByRoomAndID(
	ctx context.Context,
	roomID string,
	formID string,
) (IntakeFormRecord, error) {
	query := fmt.Sprintf(
		`SELECT title, description, fields, target_status, target_sprint, enabled, created_at
		 FROM %s WHERE room_id = ? AND form_id = ? LIMIT 1`,
		h.scylla.Table(intakeFormsTableName),
	)
	var (
		title        string
		description  string
		fieldsRaw    *string
		targetStatus string
		targetSprint string
		enabled      *bool
		createdAt    time.Time
	)
	if err := h.scylla.Session.Query(query, roomID, formID).WithContext(ctx).Scan(
		&title,
		&description,
		&fieldsRaw,
		&targetStatus,
		&targetSprint,
		&enabled,
		&createdAt,
	); err != nil {
		return IntakeFormRecord{}, err
	}
	return IntakeFormRecord{
		FormID:       formID,
		RoomID:       roomID,
		Title:        strings.TrimSpace(title),
		Description:  strings.TrimSpace(description),
		Fields:       parseIntakeFormFields(fieldsRaw),
		TargetStatus: normalizeTaskStatusValue(firstNonEmpty(strings.TrimSpace(targetStatus), defaultIntakeTargetStatus)),
		TargetSprint: strings.TrimSpace(targetSprint),
		Enabled:      enabled == nil || *enabled,
		CreatedAt:    createdAt.UTC(),
	}, nil
}

func (h *RoomHandler) loadIntakeFormByID(ctx context.Context, formID string) (IntakeFormRecord, error) {
	query := fmt.Sprintf(
		`SELECT room_id, title, description, fields, target_status, target_sprint, enabled, created_at
		 FROM %s WHERE form_id = ? LIMIT 1`,
		h.scylla.Table(intakeFormsTableName),
	)
	var (
		roomID       string
		title        string
		description  string
		fieldsRaw    *string
		targetStatus string
		targetSprint string
		enabled      *bool
		createdAt    time.Time
	)
	if err := h.scylla.Session.Query(query, formID).WithContext(ctx).Scan(
		&roomID,
		&title,
		&description,
		&fieldsRaw,
		&targetStatus,
		&targetSprint,
		&enabled,
		&createdAt,
	); err != nil {
		return IntakeFormRecord{}, err
	}
	return IntakeFormRecord{
		FormID:       formID,
		RoomID:       normalizeRoomID(roomID),
		Title:        strings.TrimSpace(title),
		Description:  strings.TrimSpace(description),
		Fields:       parseIntakeFormFields(fieldsRaw),
		TargetStatus: normalizeTaskStatusValue(firstNonEmpty(strings.TrimSpace(targetStatus), defaultIntakeTargetStatus)),
		TargetSprint: strings.TrimSpace(targetSprint),
		Enabled:      enabled == nil || *enabled,
		CreatedAt:    createdAt.UTC(),
	}, nil
}

func (h *RoomHandler) countIntakeFormSubmissions(ctx context.Context, formID string) (int, error) {
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE form_id = ?`, h.scylla.Table(formSubmissionsTableName))
	var count int64
	if err := h.scylla.Session.Query(query, formID).WithContext(ctx).Scan(&count); err != nil {
		return 0, err
	}
	if count < 0 {
		return 0, nil
	}
	return int(count), nil
}

func (h *RoomHandler) buildTaskCreateBroadcastPayload(roomID string, task TaskRecordResponse) map[string]interface{} {
	normalizedRoomID := normalizeRoomID(roomID)
	status := normalizeTaskStatusValue(task.Status)
	createdAt := task.CreatedAt.UTC().Format(time.RFC3339Nano)
	updatedAt := task.UpdatedAt.UTC().Format(time.RFC3339Nano)
	return map[string]interface{}{
		"task": map[string]interface{}{
			"id":                task.ID,
			"title":             task.Title,
			"description":       task.Description,
			"status":            status,
			"custom_fields":     task.CustomFields,
			"sprint_name":       task.SprintName,
			"assignee_id":       task.AssigneeID,
			"status_actor_id":   task.StatusActorID,
			"status_actor_name": task.StatusActorName,
			"status_changed_at": task.StatusChangedAt,
			"created_at":        createdAt,
			"updated_at":        updatedAt,
		},
		"payload": map[string]interface{}{
			"id":                task.ID,
			"task_id":           task.ID,
			"taskId":            task.ID,
			"title":             task.Title,
			"description":       task.Description,
			"status":            status,
			"custom_fields":     task.CustomFields,
			"customFields":      task.CustomFields,
			"sprint_name":       task.SprintName,
			"sprintName":        task.SprintName,
			"assignee_id":       task.AssigneeID,
			"assigneeId":        task.AssigneeID,
			"status_actor_id":   task.StatusActorID,
			"statusActorId":     task.StatusActorID,
			"status_actor_name": task.StatusActorName,
			"statusActorName":   task.StatusActorName,
			"created_at":        createdAt,
			"createdAt":         createdAt,
			"updated_at":        updatedAt,
			"updatedAt":         updatedAt,
			"room_id":           normalizedRoomID,
			"roomId":            normalizedRoomID,
		},
	}
}

func (h *RoomHandler) createTaskFromIntakeSubmission(
	ctx context.Context,
	form IntakeFormRecord,
	title string,
	description string,
	customFields map[string]interface{},
) (TaskRecordResponse, error) {
	roomUUID, normalizedRoomID, err := resolveTaskRoomUUID(form.RoomID)
	if err != nil {
		return TaskRecordResponse{}, err
	}
	taskUUID, err := gocql.RandomUUID()
	if err != nil {
		return TaskRecordResponse{}, err
	}

	status := normalizeTaskStatusValue(firstNonEmpty(form.TargetStatus, defaultIntakeTargetStatus))
	if status == "" {
		status = defaultIntakeTargetStatus
	}
	sprintName := strings.TrimSpace(form.TargetSprint)
	if len(sprintName) > 160 {
		sprintName = sprintName[:160]
	}
	if len(title) > 240 {
		title = title[:240]
	}
	if len(description) > 4000 {
		description = description[:4000]
	}

	customFieldsJSON, err := marshalTaskCustomFields(customFields)
	if err != nil {
		return TaskRecordResponse{}, err
	}
	now := time.Now().UTC()
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, id, title, description, status, sprint_name, assignee_id, custom_fields, status_actor_id, status_actor_name, status_changed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table("tasks"),
	)
	if err := h.scylla.Session.Query(
		insertQuery,
		roomUUID,
		taskUUID,
		title,
		description,
		status,
		sprintName,
		nil,
		nullableTaskCustomFieldsJSON(customFieldsJSON),
		nil,
		nil,
		now,
		now,
		now,
	).WithContext(ctx).Exec(); err != nil {
		return TaskRecordResponse{}, err
	}

	response := TaskRecordResponse{
		ID:           strings.TrimSpace(taskUUID.String()),
		RoomID:       normalizedRoomID,
		Title:        title,
		Description:  description,
		Status:       status,
		CustomFields: cloneTaskCustomFields(customFields),
		SprintName:   sprintName,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	response.Budget = extractTaskBudget(response.Description)
	response.ActualCost = extractTaskActualCost(response.Description)
	return response, nil
}

func (h *RoomHandler) GetRoomIntakeForms(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	requesterID := resolveTaskRequesterMemberID(r)
	if requesterID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
		return
	}
	isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), roomID, requesterID)
	if memberErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to access intake forms"})
		return
	}

	query := fmt.Sprintf(
		`SELECT form_id, title, description, fields, target_status, target_sprint, enabled, created_at
		 FROM %s WHERE room_id = ?`,
		h.scylla.Table(intakeFormsTableName),
	)
	iter := h.scylla.Session.Query(query, roomID).WithContext(r.Context()).Iter()
	forms := make([]IntakeFormRecord, 0, 24)
	var (
		formID       string
		title        string
		description  string
		fieldsRaw    *string
		targetStatus string
		targetSprint string
		enabled      *bool
		createdAt    time.Time
	)
	for iter.Scan(&formID, &title, &description, &fieldsRaw, &targetStatus, &targetSprint, &enabled, &createdAt) {
		normalizedFormID := normalizeIntakeFormID(formID)
		if normalizedFormID == "" {
			continue
		}
		submissionCount, countErr := h.countIntakeFormSubmissions(r.Context(), normalizedFormID)
		if countErr != nil {
			submissionCount = 0
		}
		forms = append(forms, IntakeFormRecord{
			FormID:          normalizedFormID,
			RoomID:          roomID,
			Title:           strings.TrimSpace(title),
			Description:     strings.TrimSpace(description),
			Fields:          parseIntakeFormFields(fieldsRaw),
			TargetStatus:    normalizeTaskStatusValue(firstNonEmpty(strings.TrimSpace(targetStatus), defaultIntakeTargetStatus)),
			TargetSprint:    strings.TrimSpace(targetSprint),
			Enabled:         enabled == nil || *enabled,
			SubmissionCount: submissionCount,
			CreatedAt:       createdAt.UTC(),
		})
	}
	if err := iter.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load intake forms"})
		return
	}

	sort.SliceStable(forms, func(i, j int) bool {
		if forms[i].CreatedAt.Equal(forms[j].CreatedAt) {
			return forms[i].FormID < forms[j].FormID
		}
		return forms[i].CreatedAt.After(forms[j].CreatedAt)
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(forms)
}

func (h *RoomHandler) CreateRoomIntakeForm(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	requesterID := resolveTaskRequesterMemberID(r)
	if requesterID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
		return
	}
	isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), roomID, requesterID)
	if memberErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to create intake forms"})
		return
	}

	var req createIntakeFormRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "title is required"})
		return
	}
	if len(title) > 180 {
		title = title[:180]
	}
	description := strings.TrimSpace(req.Description)
	if len(description) > 1000 {
		description = description[:1000]
	}
	targetStatus := normalizeTaskStatusValue(firstNonEmpty(req.TargetStatus, defaultIntakeTargetStatus))
	if targetStatus == "" {
		targetStatus = defaultIntakeTargetStatus
	}
	targetSprint := strings.TrimSpace(req.TargetSprint)
	if len(targetSprint) > 160 {
		targetSprint = targetSprint[:160]
	}

	fieldSchemaIDs, schemaErr := h.roomFieldSchemaIDSet(r.Context(), roomID)
	if schemaErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load room field schemas"})
		return
	}
	fields, fieldErr := normalizeIntakeFormFieldsForRoom(req.Fields, fieldSchemaIDs)
	if fieldErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fieldErr.Error()})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	formID := generateIntakeFormID(roomID, title)
	fieldsJSON, marshalErr := marshalIntakeFormFields(fields)
	if marshalErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid fields payload"})
		return
	}

	now := time.Now().UTC()
	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, form_id, title, description, fields, target_status, target_sprint, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table(intakeFormsTableName),
	)
	if err := h.scylla.Session.Query(
		insertQuery,
		roomID,
		formID,
		title,
		description,
		fieldsJSON,
		targetStatus,
		targetSprint,
		enabled,
		now,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create intake form"})
		return
	}

	response := IntakeFormRecord{
		FormID:          formID,
		RoomID:          roomID,
		Title:           title,
		Description:     description,
		Fields:          fields,
		TargetStatus:    targetStatus,
		TargetSprint:    targetSprint,
		Enabled:         enabled,
		SubmissionCount: 0,
		CreatedAt:       now,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *RoomHandler) UpdateRoomIntakeForm(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	formID := normalizeIntakeFormID(chi.URLParam(r, "formId"))
	if roomID == "" || formID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id or form id"})
		return
	}

	requesterID := resolveTaskRequesterMemberID(r)
	if requesterID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
		return
	}
	isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), roomID, requesterID)
	if memberErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to update intake forms"})
		return
	}

	current, loadErr := h.loadIntakeFormByRoomAndID(r.Context(), roomID, formID)
	if loadErr != nil {
		if loadErr == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load intake form"})
		return
	}

	var req updateIntakeFormRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	next := current
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "title cannot be empty"})
			return
		}
		if len(title) > 180 {
			title = title[:180]
		}
		next.Title = title
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		if len(description) > 1000 {
			description = description[:1000]
		}
		next.Description = description
	}
	if req.TargetStatus != nil || req.TargetStatusAlt != nil {
		targetStatus := normalizeTaskStatusValue(firstNonEmpty(
			strings.TrimSpace(firstNonEmptyValue(req.TargetStatus)),
			strings.TrimSpace(firstNonEmptyValue(req.TargetStatusAlt)),
			defaultIntakeTargetStatus,
		))
		if targetStatus == "" {
			targetStatus = defaultIntakeTargetStatus
		}
		next.TargetStatus = targetStatus
	}
	if req.TargetSprint != nil || req.TargetSprintAlt != nil {
		targetSprint := strings.TrimSpace(firstNonEmpty(
			firstNonEmptyValue(req.TargetSprint),
			firstNonEmptyValue(req.TargetSprintAlt),
		))
		if len(targetSprint) > 160 {
			targetSprint = targetSprint[:160]
		}
		next.TargetSprint = targetSprint
	}
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	}
	if req.Fields != nil {
		fieldSchemaIDs, schemaErr := h.roomFieldSchemaIDSet(r.Context(), roomID)
		if schemaErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load room field schemas"})
			return
		}
		normalizedFields, fieldErr := normalizeIntakeFormFieldsForRoom(*req.Fields, fieldSchemaIDs)
		if fieldErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fieldErr.Error()})
			return
		}
		next.Fields = normalizedFields
	}

	fieldsJSON, marshalErr := marshalIntakeFormFields(next.Fields)
	if marshalErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid fields payload"})
		return
	}

	updateQuery := fmt.Sprintf(
		`UPDATE %s SET title = ?, description = ?, fields = ?, target_status = ?, target_sprint = ?, enabled = ? WHERE room_id = ? AND form_id = ?`,
		h.scylla.Table(intakeFormsTableName),
	)
	if err := h.scylla.Session.Query(
		updateQuery,
		next.Title,
		next.Description,
		fieldsJSON,
		next.TargetStatus,
		next.TargetSprint,
		next.Enabled,
		roomID,
		formID,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update intake form"})
		return
	}

	submissionCount, countErr := h.countIntakeFormSubmissions(r.Context(), formID)
	if countErr == nil {
		next.SubmissionCount = submissionCount
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(next)
}

func (h *RoomHandler) DeleteRoomIntakeForm(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	formID := normalizeIntakeFormID(chi.URLParam(r, "formId"))
	if roomID == "" || formID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id or form id"})
		return
	}

	requesterID := resolveTaskRequesterMemberID(r)
	if requesterID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
		return
	}
	isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), roomID, requesterID)
	if memberErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to delete intake forms"})
		return
	}

	deleteFormQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND form_id = ?`, h.scylla.Table(intakeFormsTableName))
	if err := h.scylla.Session.Query(deleteFormQuery, roomID, formID).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete intake form"})
		return
	}

	deleteSubmissionsQuery := fmt.Sprintf(`DELETE FROM %s WHERE form_id = ?`, h.scylla.Table(formSubmissionsTableName))
	if err := h.scylla.Session.Query(deleteSubmissionsQuery, formID).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete intake form submissions"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *RoomHandler) GetRoomIntakeFormSubmissions(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form storage unavailable"})
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	formID := normalizeIntakeFormID(chi.URLParam(r, "formId"))
	if roomID == "" || formID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id or form id"})
		return
	}

	requesterID := resolveTaskRequesterMemberID(r)
	if requesterID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
		return
	}
	isMember, memberErr := h.ensureTaskRequesterMembership(r.Context(), roomID, requesterID)
	if memberErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room to view intake submissions"})
		return
	}

	if _, loadErr := h.loadIntakeFormByRoomAndID(r.Context(), roomID, formID); loadErr != nil {
		if loadErr == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load intake form"})
		return
	}

	query := fmt.Sprintf(
		`SELECT submission_id, room_id, task_id, data, submitter_email, submitted_at
		 FROM %s WHERE form_id = ?`,
		h.scylla.Table(formSubmissionsTableName),
	)
	iter := h.scylla.Session.Query(query, formID).WithContext(r.Context()).Iter()
	submissions := make([]IntakeFormSubmissionRecord, 0, 64)
	var (
		submissionID   string
		submissionRoom string
		taskID         string
		dataRaw        *string
		submitterEmail string
		submittedAt    time.Time
	)
	for iter.Scan(&submissionID, &submissionRoom, &taskID, &dataRaw, &submitterEmail, &submittedAt) {
		normalizedSubmissionID := normalizeIntakeFormID(submissionID)
		if normalizedSubmissionID == "" {
			continue
		}
		submissions = append(submissions, IntakeFormSubmissionRecord{
			FormID:         formID,
			SubmissionID:   normalizedSubmissionID,
			RoomID:         normalizeRoomID(submissionRoom),
			TaskID:         strings.TrimSpace(taskID),
			Data:           parseIntakeSubmissionData(dataRaw),
			SubmitterEmail: strings.TrimSpace(submitterEmail),
			SubmittedAt:    submittedAt.UTC(),
		})
	}
	if err := iter.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load intake submissions"})
		return
	}

	sort.SliceStable(submissions, func(i, j int) bool {
		if submissions[i].SubmittedAt.Equal(submissions[j].SubmittedAt) {
			return submissions[i].SubmissionID > submissions[j].SubmissionID
		}
		return submissions[i].SubmittedAt.After(submissions[j].SubmittedAt)
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(submissions)
}

func (h *RoomHandler) GetPublicIntakeForm(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form storage unavailable"})
		return
	}

	formID := normalizeIntakeFormID(chi.URLParam(r, "formId"))
	if formID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid form id"})
		return
	}

	form, err := h.loadIntakeFormByID(r.Context(), formID)
	if err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Form not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load form"})
		return
	}
	if !form.Enabled {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Form is not available"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"form_id":     form.FormID,
		"title":       form.Title,
		"description": form.Description,
		"fields":      form.Fields,
	})
}

func (h *RoomHandler) SubmitPublicIntakeForm(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Intake form storage unavailable"})
		return
	}

	formID := normalizeIntakeFormID(chi.URLParam(r, "formId"))
	if formID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid form id"})
		return
	}

	form, err := h.loadIntakeFormByID(r.Context(), formID)
	if err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Form not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load form"})
		return
	}
	if !form.Enabled {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Form is not available"})
		return
	}

	var req submitPublicIntakeFormRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}
	if req.Fields == nil {
		req.Fields = make(map[string]interface{})
	}

	submitterEmail := strings.TrimSpace(req.SubmitterEmail)
	fieldErrors := make(map[string]string)
	if submitterEmail != "" && !isValidEmailAddress(submitterEmail) {
		fieldErrors["submitter_email"] = "must be a valid email"
	}

	sanitizedValues, valueErrors := normalizeSubmissionValues(form.Fields, req.Fields)
	for key, value := range valueErrors {
		fieldErrors[key] = value
	}

	if len(fieldErrors) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        "Validation failed",
			"field_errors": fieldErrors,
		})
		return
	}

	title := ""
	for _, field := range form.Fields {
		if field.FieldType != "text" {
			continue
		}
		value, exists := sanitizedValues[field.FieldID]
		if !exists {
			continue
		}
		titleCandidate := strings.TrimSpace(fmt.Sprintf("%v", value))
		if titleCandidate == "" {
			continue
		}
		title = titleCandidate
		break
	}
	if title == "" {
		title = fmt.Sprintf("Form submission %s", time.Now().UTC().Format("2006-01-02 15:04"))
	}

	descriptionParts := []string{
		fmt.Sprintf("Submitted via intake form: %s", strings.TrimSpace(form.Title)),
	}
	if submitterEmail != "" {
		descriptionParts = append(descriptionParts, fmt.Sprintf("Email: %s", submitterEmail))
	}
	description := strings.Join(descriptionParts, "\n")

	task, taskErr := h.createTaskFromIntakeSubmission(r.Context(), form, title, description, sanitizedValues)
	if taskErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create task from form submission"})
		return
	}

	submissionID := generateIntakeSubmissionID(form.FormID, task.ID)
	dataJSON, marshalErr := marshalIntakeSubmissionData(sanitizedValues)
	if marshalErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save submission data"})
		return
	}

	insertSubmissionQuery := fmt.Sprintf(
		`INSERT INTO %s (form_id, submission_id, room_id, task_id, data, submitter_email, submitted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table(formSubmissionsTableName),
	)
	now := time.Now().UTC()
	if err := h.scylla.Session.Query(
		insertSubmissionQuery,
		form.FormID,
		submissionID,
		form.RoomID,
		task.ID,
		dataJSON,
		firstNonEmpty(submitterEmail, ""),
		now,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save form submission"})
		return
	}

	h.broadcastRoomEvent(form.RoomID, "task_create", h.buildTaskCreateBroadcastPayload(form.RoomID, task))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"submission_id": submissionID,
		"task_id":       task.ID,
	})
}

func firstNonEmptyValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
