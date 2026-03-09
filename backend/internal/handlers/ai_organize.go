package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/config"
)

const aiOrganizeSystemPrompt = `You are an intelligent project manager.
Analyze the provided array of tasks, messages, and scheduled meetings (beacons).
Reorganize them into three strict categories:
- "priority": urgent tasks and beacons within 24 hours
- "pinnedItems": logically grouped by topic/context
- "expired": past events
Update or summarize the user "note" when direct connections between items are obvious.

Strict response rules:
1. Return ONLY valid JSON. No markdown, no prose.
2. Use EXACT keys: "priority", "pinnedItems", "expired".
3. Every input id must appear exactly once across all arrays.
4. Array entries must be objects with:
   - "id": string (required)
   - "note": string (optional)
   - "topic": string (optional)
5. Do not invent unknown ids.`

type aiOrganizeDashboardRequest struct {
	Items []aiOrganizeDashboardItem `json:"items"`
}

type aiOrganizeDashboardItem struct {
	ID              string                 `json:"id"`
	RoomID          string                 `json:"roomId"`
	MessageID       string                 `json:"messageId"`
	Kind            string                 `json:"kind"`
	SenderID        string                 `json:"senderId"`
	SenderName      string                 `json:"senderName"`
	PinnedByUserID  string                 `json:"pinnedByUserId"`
	PinnedByName    string                 `json:"pinnedByName"`
	OriginalCreated int64                  `json:"originalCreatedAt"`
	PinnedAt        int64                  `json:"pinnedAt"`
	MessageText     string                 `json:"messageText"`
	MediaURL        string                 `json:"mediaUrl"`
	MediaType       string                 `json:"mediaType"`
	FileName        string                 `json:"fileName"`
	Note            string                 `json:"note"`
	BeaconAt        *int64                 `json:"beaconAt"`
	BeaconLabel     string                 `json:"beaconLabel"`
	BeaconData      map[string]interface{} `json:"beaconData"`
	TaskTitle       string                 `json:"taskTitle"`
	Topic           string                 `json:"topic,omitempty"`
}

type aiOrganizeDashboardResponse struct {
	Priority    []aiOrganizeDashboardItem `json:"priority"`
	PinnedItems []aiOrganizeDashboardItem `json:"pinnedItems"`
	Expired     []aiOrganizeDashboardItem `json:"expired"`
}

type aiOrganizeLLMOutput struct {
	Priority    []aiOrganizePlacement `json:"priority"`
	PinnedItems []aiOrganizePlacement `json:"pinnedItems"`
	Expired     []aiOrganizePlacement `json:"expired"`
}

type aiOrganizePlacement struct {
	ID    string `json:"id"`
	Note  string `json:"note,omitempty"`
	Topic string `json:"topic,omitempty"`
}

type aiOrganizePromptItem struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	MessageText string  `json:"messageText"`
	TaskTitle   string  `json:"taskTitle"`
	Note        string  `json:"note"`
	BeaconAt    *int64  `json:"beaconAt,omitempty"`
	BeaconLabel string  `json:"beaconLabel"`
	MediaType   string  `json:"mediaType"`
	FileName    string  `json:"fileName"`
	Topic       string  `json:"topic,omitempty"`
	HasMedia    bool    `json:"hasMedia"`
	RecencyHrs  float64 `json:"recencyHours"`
}

type aiOrganizeLimits struct {
	MaxRequestBytes int64
	MaxItems        int
	NoteMaxLength   int
	TopicMaxLength  int
	TextMaxLength   int
	RequestTimeout  time.Duration
}

func getAIOrganizeLimits() aiOrganizeLimits {
	loaded := config.LoadAppLimits().AI
	limits := aiOrganizeLimits{
		MaxRequestBytes: loaded.OrganizeMaxRequestBytes,
		MaxItems:        loaded.OrganizeMaxItems,
		NoteMaxLength:   loaded.OrganizeNoteMaxLength,
		TopicMaxLength:  loaded.OrganizeTopicMaxLength,
		TextMaxLength:   loaded.OrganizeTextMaxLength,
		RequestTimeout:  loaded.OrganizeRequestTimeout,
	}
	if limits.MaxRequestBytes <= 0 {
		limits.MaxRequestBytes = 2 * 1024 * 1024
	}
	if limits.MaxItems <= 0 {
		limits.MaxItems = 500
	}
	if limits.NoteMaxLength <= 0 {
		limits.NoteMaxLength = 1200
	}
	if limits.TopicMaxLength <= 0 {
		limits.TopicMaxLength = 180
	}
	if limits.TextMaxLength <= 0 {
		limits.TextMaxLength = 3000
	}
	if limits.RequestTimeout <= 0 {
		limits.RequestTimeout = 30 * time.Second
	}
	return limits
}

func (h *RoomHandler) AIOrganizeDashboard(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	roomID := normalizeRoomID(chi.URLParam(r, "roomId"))
	if roomID == "" {
		writeAIOrganizeError(w, http.StatusBadRequest, "Invalid room id")
		return
	}
	if h == nil || h.redis == nil || h.redis.Client == nil {
		writeAIOrganizeError(w, http.StatusServiceUnavailable, "Room storage unavailable")
		return
	}

	userID := normalizeIdentifier(
		firstNonEmpty(
			r.URL.Query().Get("userId"),
			r.URL.Query().Get("user_id"),
			r.Header.Get("X-User-Id"),
		),
	)
	if userID == "" {
		writeAIOrganizeError(w, http.StatusUnauthorized, "User context is required")
		return
	}

	ctx := r.Context()
	isMember, memberErr := h.isRoomMember(ctx, roomID, userID)
	if memberErr != nil {
		writeAIOrganizeError(w, http.StatusInternalServerError, "Failed to verify room membership")
		return
	}
	if !isMember {
		writeAIOrganizeError(w, http.StatusForbidden, "Join the room to organize dashboard items")
		return
	}

	limits := getAIOrganizeLimits()

	r.Body = http.MaxBytesReader(w, r.Body, limits.MaxRequestBytes)
	var request aiOrganizeDashboardRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeAIOrganizeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	if len(request.Items) == 0 {
		writeAIOrganizeError(w, http.StatusBadRequest, "items is required")
		return
	}
	if len(request.Items) > limits.MaxItems {
		writeAIOrganizeError(
			w,
			http.StatusBadRequest,
			fmt.Sprintf("Too many dashboard items (%d max)", limits.MaxItems),
		)
		return
	}

	normalizedItems, itemByID := normalizeAIOrganizeItems(request.Items, roomID, limits)
	if len(normalizedItems) == 0 {
		writeAIOrganizeError(w, http.StatusBadRequest, "No valid dashboard items were provided")
		return
	}

	llmCtx, cancel := context.WithTimeout(ctx, limits.RequestTimeout)
	defer cancel()

	llmOutput, llmErr := generateAIOrganizeLLMOutput(llmCtx, roomID, normalizedItems, limits)
	if llmErr != nil {
		switch {
		case errors.Is(llmErr, context.Canceled), errors.Is(llmErr, context.DeadlineExceeded):
			writeAIOrganizeError(w, http.StatusGatewayTimeout, "AI organize request timed out")
		case errors.Is(llmErr, ai.ErrAllAIProvidersExhausted):
			writeAIOrganizeError(w, http.StatusServiceUnavailable, "AI providers are currently unavailable")
		default:
			writeAIOrganizeError(w, http.StatusBadGateway, "Failed to organize dashboard with AI")
		}
		return
	}

	organized := applyAIOrganizeLLMOutput(itemByID, llmOutput, time.Now().UTC(), limits)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(organized)
}

func normalizeAIOrganizeItems(
	input []aiOrganizeDashboardItem,
	fallbackRoomID string,
	limits aiOrganizeLimits,
) ([]aiOrganizeDashboardItem, map[string]aiOrganizeDashboardItem) {
	normalized := make([]aiOrganizeDashboardItem, 0, len(input))
	byID := make(map[string]aiOrganizeDashboardItem, len(input))
	for _, raw := range input {
		item, ok := normalizeAIOrganizeItem(raw, fallbackRoomID, limits)
		if !ok {
			continue
		}
		if _, exists := byID[item.ID]; exists {
			continue
		}
		normalized = append(normalized, item)
		byID[item.ID] = item
	}
	return normalized, byID
}

func normalizeAIOrganizeItem(
	raw aiOrganizeDashboardItem,
	fallbackRoomID string,
	limits aiOrganizeLimits,
) (aiOrganizeDashboardItem, bool) {
	itemID := normalizeMessageID(firstNonEmpty(raw.ID, raw.MessageID))
	if itemID == "" {
		return aiOrganizeDashboardItem{}, false
	}
	messageID := normalizeMessageID(firstNonEmpty(raw.MessageID, itemID))
	if messageID == "" {
		messageID = itemID
	}
	roomID := normalizeRoomID(firstNonEmpty(raw.RoomID, fallbackRoomID))
	if roomID == "" {
		return aiOrganizeDashboardItem{}, false
	}
	kind := strings.ToLower(strings.TrimSpace(raw.Kind))
	switch kind {
	case "task", "note", "message":
	default:
		kind = "message"
	}

	normalized := aiOrganizeDashboardItem{
		ID:              itemID,
		RoomID:          roomID,
		MessageID:       messageID,
		Kind:            kind,
		SenderID:        normalizeIdentifier(raw.SenderID),
		SenderName:      truncateRunes(strings.TrimSpace(raw.SenderName), 80),
		PinnedByUserID:  normalizeIdentifier(raw.PinnedByUserID),
		PinnedByName:    truncateRunes(strings.TrimSpace(raw.PinnedByName), 80),
		OriginalCreated: raw.OriginalCreated,
		PinnedAt:        raw.PinnedAt,
		MessageText:     truncateRunes(strings.TrimSpace(raw.MessageText), limits.TextMaxLength),
		MediaURL:        truncateRunes(strings.TrimSpace(raw.MediaURL), 4096),
		MediaType:       truncateRunes(strings.TrimSpace(raw.MediaType), 120),
		FileName:        truncateRunes(strings.TrimSpace(raw.FileName), 180),
		Note:            truncateRunes(strings.TrimSpace(raw.Note), limits.NoteMaxLength),
		BeaconLabel:     truncateRunes(strings.TrimSpace(raw.BeaconLabel), 160),
		BeaconData:      raw.BeaconData,
		TaskTitle:       truncateRunes(strings.TrimSpace(raw.TaskTitle), 240),
		Topic:           truncateRunes(strings.TrimSpace(raw.Topic), limits.TopicMaxLength),
	}
	if normalized.SenderName == "" {
		normalized.SenderName = "User"
	}
	if normalized.PinnedByName == "" {
		normalized.PinnedByName = "User"
	}
	if raw.BeaconAt != nil && *raw.BeaconAt > 0 {
		value := *raw.BeaconAt
		normalized.BeaconAt = &value
	}
	return normalized, true
}

func generateAIOrganizeLLMOutput(
	ctx context.Context,
	roomID string,
	items []aiOrganizeDashboardItem,
	limits aiOrganizeLimits,
) (aiOrganizeLLMOutput, error) {
	promptItems := buildAIOrganizePromptItems(items)
	encodedItems, err := json.Marshal(promptItems)
	if err != nil {
		return aiOrganizeLLMOutput{}, err
	}

	userPrompt := fmt.Sprintf(
		"Room ID: %s\nCurrent Unix time (ms): %d\nInput dashboard items JSON:\n%s",
		roomID,
		time.Now().UTC().UnixMilli(),
		string(encodedItems),
	)
	rawOutput, err := generateAIOrganizeStructuredJSON(ctx, aiOrganizeSystemPrompt, userPrompt, limits)
	if err != nil {
		return aiOrganizeLLMOutput{}, err
	}
	parsed, parseErr := parseAIOrganizeLLMOutput(rawOutput)
	if parseErr != nil {
		return aiOrganizeLLMOutput{}, parseErr
	}
	return parsed, nil
}

func buildAIOrganizePromptItems(items []aiOrganizeDashboardItem) []aiOrganizePromptItem {
	now := time.Now().UTC().UnixMilli()
	results := make([]aiOrganizePromptItem, 0, len(items))
	for _, item := range items {
		recencyHours := 0.0
		if item.BeaconAt != nil && *item.BeaconAt > 0 {
			recencyHours = float64(*item.BeaconAt-now) / float64(time.Hour/time.Millisecond)
		}
		results = append(results, aiOrganizePromptItem{
			ID:          item.ID,
			Kind:        item.Kind,
			MessageText: item.MessageText,
			TaskTitle:   item.TaskTitle,
			Note:        item.Note,
			BeaconAt:    item.BeaconAt,
			BeaconLabel: item.BeaconLabel,
			MediaType:   item.MediaType,
			FileName:    item.FileName,
			Topic:       item.Topic,
			HasMedia:    strings.TrimSpace(item.MediaURL) != "",
			RecencyHrs:  recencyHours,
		})
	}
	return results
}

func parseAIOrganizeLLMOutput(raw string) (aiOrganizeLLMOutput, error) {
	content := extractJSONObject(raw)
	if strings.TrimSpace(content) == "" {
		return aiOrganizeLLMOutput{}, fmt.Errorf("ai organize response did not contain JSON")
	}
	var output aiOrganizeLLMOutput
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return aiOrganizeLLMOutput{}, err
	}
	return output, nil
}

func applyAIOrganizeLLMOutput(
	itemByID map[string]aiOrganizeDashboardItem,
	output aiOrganizeLLMOutput,
	now time.Time,
	limits aiOrganizeLimits,
) aiOrganizeDashboardResponse {
	seen := make(map[string]struct{}, len(itemByID))
	response := aiOrganizeDashboardResponse{
		Priority:    make([]aiOrganizeDashboardItem, 0, len(output.Priority)),
		PinnedItems: make([]aiOrganizeDashboardItem, 0, len(output.PinnedItems)),
		Expired:     make([]aiOrganizeDashboardItem, 0, len(output.Expired)),
	}

	appendPlacement := func(
		target *[]aiOrganizeDashboardItem,
		placements []aiOrganizePlacement,
	) {
		for _, placement := range placements {
			id := normalizeMessageID(placement.ID)
			if id == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			item, ok := itemByID[id]
			if !ok {
				continue
			}
			if note := strings.TrimSpace(placement.Note); note != "" {
				item.Note = truncateRunes(note, limits.NoteMaxLength)
			}
			if topic := strings.TrimSpace(placement.Topic); topic != "" {
				item.Topic = truncateRunes(topic, limits.TopicMaxLength)
			}
			*target = append(*target, item)
			seen[id] = struct{}{}
		}
	}

	appendPlacement(&response.Priority, output.Priority)
	appendPlacement(&response.PinnedItems, output.PinnedItems)
	appendPlacement(&response.Expired, output.Expired)

	nowMs := now.UTC().UnixMilli()
	next24HrsMs := nowMs + int64((24*time.Hour)/time.Millisecond)
	for id, item := range itemByID {
		if _, exists := seen[id]; exists {
			continue
		}
		if item.BeaconAt != nil && *item.BeaconAt > 0 {
			if *item.BeaconAt < nowMs {
				response.Expired = append(response.Expired, item)
				continue
			}
			if *item.BeaconAt <= next24HrsMs {
				response.Priority = append(response.Priority, item)
				continue
			}
		}
		response.PinnedItems = append(response.PinnedItems, item)
	}

	return response
}

func generateAIOrganizeStructuredJSON(
	ctx context.Context,
	systemPrompt, userPrompt string,
	limits aiOrganizeLimits,
) (string, error) {
	if openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); openAIKey != "" {
		if content, err := generateAIOrganizeWithOpenAI(ctx, openAIKey, systemPrompt, userPrompt, limits); err == nil {
			return content, nil
		}
	}
	if geminiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); geminiKey != "" {
		if content, err := generateAIOrganizeWithGemini(ctx, geminiKey, systemPrompt, userPrompt, limits); err == nil {
			return content, nil
		}
	}
	return ai.DefaultRouter.GenerateChatResponse(
		ctx,
		systemPrompt+"\n\nUser request:\n"+userPrompt,
	)
}

func generateAIOrganizeWithOpenAI(
	ctx context.Context,
	apiKey, systemPrompt, userPrompt string,
	limits aiOrganizeLimits,
) (string, error) {
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	if model == "" {
		model = "gpt-4o-mini"
	}
	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": systemPrompt,
			},
			{
				"role":    "user",
				"content": userPrompt,
			},
		},
		"temperature": 0.1,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}
	status, body, err := aiOrganizePostJSON(ctx, "https://api.openai.com/v1/chat/completions", map[string]string{
		"Authorization": "Bearer " + strings.TrimSpace(apiKey),
	}, payload, limits)
	if err != nil {
		return "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", fmt.Errorf("openai ai-organize failed: status=%d msg=%s", status, aiOrganizeErrorMessageFromBody(body))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai ai-organize returned empty choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func generateAIOrganizeWithGemini(
	ctx context.Context,
	apiKey, systemPrompt, userPrompt string,
	limits aiOrganizeLimits,
) (string, error) {
	model := strings.TrimSpace(os.Getenv("GEMINI_MODEL"))
	if model == "" {
		model = "gemini-3.1-flash"
	}
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		url.PathEscape(model),
		url.QueryEscape(strings.TrimSpace(apiKey)),
	)
	payload := map[string]interface{}{
		"system_instruction": map[string]interface{}{
			"parts": []map[string]string{
				{"text": systemPrompt},
			},
		},
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": userPrompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":      0.1,
			"responseMimeType": "application/json",
		},
	}
	status, body, err := aiOrganizePostJSON(ctx, endpoint, map[string]string{}, payload, limits)
	if err != nil {
		return "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", fmt.Errorf("gemini ai-organize failed: status=%d msg=%s", status, aiOrganizeErrorMessageFromBody(body))
	}
	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini ai-organize returned empty candidates")
	}
	return strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text), nil
}

func aiOrganizePostJSON(
	ctx context.Context,
	endpoint string,
	headers map[string]string,
	payload interface{},
	limits aiOrganizeLimits,
) (int, []byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		request.Header.Set(key, value)
	}
	client := &http.Client{Timeout: limits.RequestTimeout}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, limits.MaxRequestBytes))
	if readErr != nil {
		return response.StatusCode, nil, readErr
	}
	return response.StatusCode, body, nil
}

func aiOrganizeErrorMessageFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return strings.TrimSpace(string(body))
	}
	if raw, ok := parsed["error"]; ok {
		switch typed := raw.(type) {
		case string:
			return strings.TrimSpace(typed)
		case map[string]interface{}:
			if message, ok := typed["message"].(string); ok {
				return strings.TrimSpace(message)
			}
		}
	}
	if message, ok := parsed["message"].(string); ok {
		return strings.TrimSpace(message)
	}
	return strings.TrimSpace(string(body))
}

func extractJSONObject(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimSpace(trimmed)
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < 0 || end < start {
		return trimmed
	}
	return strings.TrimSpace(trimmed[start : end+1])
}

func writeAIOrganizeError(w http.ResponseWriter, status int, message string) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": strings.TrimSpace(message),
	})
}
