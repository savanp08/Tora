package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
)

const roomFieldSchemaTable = "room_field_schemas"

var supportedFieldSchemaTypes = map[string]struct{}{
	"text":         {},
	"number":       {},
	"date":         {},
	"select":       {},
	"multi_select": {},
	"checkbox":     {},
	"person":       {},
	"url":          {},
}

type FieldSchema struct {
	FieldID   string   `json:"field_id"`
	RoomID    string   `json:"room_id"`
	Name      string   `json:"name"`
	FieldType string   `json:"field_type"`
	Options   []string `json:"options,omitempty"`
	Position  int      `json:"position"`
}

type createFieldSchemaRequest struct {
	Name         string   `json:"name"`
	FieldType    string   `json:"field_type"`
	FieldTypeAlt string   `json:"fieldType"`
	Options      []string `json:"options"`
	Position     *int     `json:"position"`
}

type updateFieldSchemaRequest struct {
	Name         *string   `json:"name"`
	FieldType    *string   `json:"field_type"`
	FieldTypeAlt *string   `json:"fieldType"`
	Options      *[]string `json:"options"`
	Position     *int      `json:"position"`
}

func (h *RoomHandler) ensureFieldSchemaSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id text,
		field_id text,
		name text,
		field_type text,
		options text,
		position int,
		created_at timestamp,
		PRIMARY KEY (room_id, field_id)
	) WITH CLUSTERING ORDER BY (field_id ASC)`, h.scylla.Table(roomFieldSchemaTable))
	if err := h.scylla.Session.Query(query).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		log.Printf("[field-schema] ensure schema failed: %v", err)
	}
}

func (h *RoomHandler) GetRoomFieldSchemas(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field schema storage unavailable"})
		return
	}

	roomID := normalizeRoomID(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	query := fmt.Sprintf(
		`SELECT field_id, name, field_type, options, position FROM %s WHERE room_id = ?`,
		h.scylla.Table(roomFieldSchemaTable),
	)
	iter := h.scylla.Session.Query(query, roomID).WithContext(r.Context()).Iter()
	schemas := make([]FieldSchema, 0, 24)
	var (
		fieldID    string
		name       string
		fieldType  string
		optionsRaw *string
		position   int
	)
	for iter.Scan(&fieldID, &name, &fieldType, &optionsRaw, &position) {
		schemas = append(schemas, FieldSchema{
			FieldID:   strings.TrimSpace(fieldID),
			RoomID:    roomID,
			Name:      strings.TrimSpace(name),
			FieldType: normalizeFieldSchemaType(fieldType),
			Options:   parseFieldSchemaOptions(optionsRaw),
			Position:  position,
		})
	}
	if err := iter.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load field schemas"})
		return
	}

	sort.SliceStable(schemas, func(i, j int) bool {
		if schemas[i].Position == schemas[j].Position {
			if schemas[i].Name == schemas[j].Name {
				return schemas[i].FieldID < schemas[j].FieldID
			}
			return schemas[i].Name < schemas[j].Name
		}
		return schemas[i].Position < schemas[j].Position
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(schemas)
}

func (h *RoomHandler) CreateRoomFieldSchema(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field schema storage unavailable"})
		return
	}

	roomID := normalizeRoomID(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	var req createFieldSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field name is required"})
		return
	}
	if len(name) > 120 {
		name = name[:120]
	}

	fieldType := normalizeFieldSchemaType(firstNonEmpty(req.FieldType, req.FieldTypeAlt))
	if !isSupportedFieldSchemaType(fieldType) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unsupported field_type"})
		return
	}

	options, optionsErr := normalizeFieldSchemaOptionsForType(fieldType, req.Options)
	if optionsErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": optionsErr.Error()})
		return
	}

	position := 0
	if req.Position != nil {
		if *req.Position < 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "position must be >= 0"})
			return
		}
		position = *req.Position
	} else {
		nextPosition, err := h.nextRoomFieldSchemaPosition(r.Context(), roomID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to determine field position"})
			return
		}
		position = nextPosition
	}

	fieldID := generateRoomFieldSchemaID(roomID, name)
	var existingID string
	existsQuery := fmt.Sprintf(
		`SELECT field_id FROM %s WHERE room_id = ? AND field_id = ? LIMIT 1`,
		h.scylla.Table(roomFieldSchemaTable),
	)
	if err := h.scylla.Session.Query(existsQuery, roomID, fieldID).WithContext(r.Context()).Scan(&existingID); err == nil {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field schema already exists"})
		return
	} else if err != gocql.ErrNotFound {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to check existing field schema"})
		return
	}

	optionsJSON, marshalErr := marshalFieldSchemaOptions(options)
	if marshalErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid options payload"})
		return
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
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create field schema"})
		return
	}

	response := FieldSchema{
		FieldID:   fieldID,
		RoomID:    roomID,
		Name:      name,
		FieldType: fieldType,
		Options:   options,
		Position:  position,
	}
	h.broadcastFieldSchemaUpdate(roomID, "created", response)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *RoomHandler) UpdateRoomFieldSchema(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field schema storage unavailable"})
		return
	}

	roomID := normalizeRoomID(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}
	fieldID := normalizeFieldSchemaID(chi.URLParam(r, "fieldId"))
	if fieldID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid field id"})
		return
	}

	current, err := h.loadRoomFieldSchema(r.Context(), roomID, fieldID)
	if err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field schema not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load field schema"})
		return
	}

	var req updateFieldSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	next := current
	if req.Name != nil {
		nextName := strings.TrimSpace(*req.Name)
		if nextName == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field name cannot be empty"})
			return
		}
		if len(nextName) > 120 {
			nextName = nextName[:120]
		}
		next.Name = nextName
	}

	fieldTypeProvided := false
	nextFieldTypeRaw := ""
	if req.FieldType != nil {
		fieldTypeProvided = true
		nextFieldTypeRaw = *req.FieldType
	}
	if req.FieldTypeAlt != nil {
		fieldTypeProvided = true
		nextFieldTypeRaw = *req.FieldTypeAlt
	}
	if fieldTypeProvided {
		nextFieldType := normalizeFieldSchemaType(nextFieldTypeRaw)
		if !isSupportedFieldSchemaType(nextFieldType) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unsupported field_type"})
			return
		}
		next.FieldType = nextFieldType
	}

	if req.Position != nil {
		if *req.Position < 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "position must be >= 0"})
			return
		}
		next.Position = *req.Position
	}

	if req.Options != nil {
		next.Options = sanitizeFieldSchemaOptions(*req.Options)
	}
	normalizedOptions, optionsErr := normalizeFieldSchemaOptionsForType(next.FieldType, next.Options)
	if optionsErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": optionsErr.Error()})
		return
	}
	next.Options = normalizedOptions

	if next.Name == current.Name &&
		next.FieldType == current.FieldType &&
		next.Position == current.Position &&
		equalStringSlices(next.Options, current.Options) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(current)
		return
	}

	optionsJSON, marshalErr := marshalFieldSchemaOptions(next.Options)
	if marshalErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid options payload"})
		return
	}
	updateQuery := fmt.Sprintf(
		`UPDATE %s SET name = ?, field_type = ?, options = ?, position = ? WHERE room_id = ? AND field_id = ?`,
		h.scylla.Table(roomFieldSchemaTable),
	)
	if err := h.scylla.Session.Query(
		updateQuery,
		next.Name,
		next.FieldType,
		optionsJSON,
		next.Position,
		roomID,
		fieldID,
	).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update field schema"})
		return
	}

	h.broadcastFieldSchemaUpdate(roomID, "updated", next)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(next)
}

func (h *RoomHandler) DeleteRoomFieldSchema(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field schema storage unavailable"})
		return
	}

	roomID := normalizeRoomID(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}
	fieldID := normalizeFieldSchemaID(chi.URLParam(r, "fieldId"))
	if fieldID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid field id"})
		return
	}

	if _, err := h.loadRoomFieldSchema(r.Context(), roomID, fieldID); err != nil {
		if err == gocql.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field schema not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load field schema"})
		return
	}

	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND field_id = ?`, h.scylla.Table(roomFieldSchemaTable))
	if err := h.scylla.Session.Query(deleteQuery, roomID, fieldID).WithContext(r.Context()).Exec(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete field schema"})
		return
	}

	h.broadcastRoomEvent(roomID, "field_schema_update", map[string]interface{}{
		"action":   "deleted",
		"field_id": fieldID,
	})

	w.WriteHeader(http.StatusNoContent)
}

func (h *RoomHandler) loadRoomFieldSchema(ctx context.Context, roomID string, fieldID string) (FieldSchema, error) {
	query := fmt.Sprintf(
		`SELECT field_id, name, field_type, options, position FROM %s WHERE room_id = ? AND field_id = ? LIMIT 1`,
		h.scylla.Table(roomFieldSchemaTable),
	)
	var (
		storedFieldID string
		name          string
		fieldType     string
		optionsRaw    *string
		position      int
	)
	if err := h.scylla.Session.Query(query, roomID, fieldID).WithContext(ctx).Scan(
		&storedFieldID,
		&name,
		&fieldType,
		&optionsRaw,
		&position,
	); err != nil {
		return FieldSchema{}, err
	}
	return FieldSchema{
		FieldID:   normalizeFieldSchemaID(storedFieldID),
		RoomID:    roomID,
		Name:      strings.TrimSpace(name),
		FieldType: normalizeFieldSchemaType(fieldType),
		Options:   parseFieldSchemaOptions(optionsRaw),
		Position:  position,
	}, nil
}

func (h *RoomHandler) nextRoomFieldSchemaPosition(ctx context.Context, roomID string) (int, error) {
	query := fmt.Sprintf(`SELECT position FROM %s WHERE room_id = ?`, h.scylla.Table(roomFieldSchemaTable))
	iter := h.scylla.Session.Query(query, roomID).WithContext(ctx).Iter()
	maxPosition := -1
	var position int
	for iter.Scan(&position) {
		if position > maxPosition {
			maxPosition = position
		}
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}
	return maxPosition + 1, nil
}

func (h *RoomHandler) broadcastFieldSchemaUpdate(roomID string, action string, schema FieldSchema) {
	h.broadcastRoomEvent(roomID, "field_schema_update", map[string]interface{}{
		"action":       strings.ToLower(strings.TrimSpace(action)),
		"field_schema": schema,
		"field_id":     schema.FieldID,
	})
}

func normalizeFieldSchemaType(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func isSupportedFieldSchemaType(raw string) bool {
	_, ok := supportedFieldSchemaTypes[normalizeFieldSchemaType(raw)]
	return ok
}

func normalizeFieldSchemaID(raw string) string {
	trimmed := strings.TrimSpace(raw)
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

func generateRoomFieldSchemaID(roomID string, name string) string {
	digest := sha1.Sum([]byte(strings.TrimSpace(roomID) + strings.TrimSpace(name)))
	encoded := hex.EncodeToString(digest[:])
	if len(encoded) < 12 {
		return encoded
	}
	return encoded[:12]
}

func sanitizeFieldSchemaOptions(options []string) []string {
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
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		sanitized = append(sanitized, trimmed)
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func parseFieldSchemaOptions(raw *string) []string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	var parsed []string
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil
	}
	return sanitizeFieldSchemaOptions(parsed)
}

func marshalFieldSchemaOptions(options []string) (interface{}, error) {
	sanitized := sanitizeFieldSchemaOptions(options)
	if len(sanitized) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

func normalizeFieldSchemaOptionsForType(fieldType string, options []string) ([]string, error) {
	normalizedType := normalizeFieldSchemaType(fieldType)
	sanitized := sanitizeFieldSchemaOptions(options)
	if normalizedType == "select" || normalizedType == "multi_select" {
		if len(sanitized) == 0 {
			return nil, fmt.Errorf("select and multi_select fields require at least one option")
		}
		return sanitized, nil
	}
	return nil, nil
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
