package ai

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
)

const (
	defaultGeminiModel = "gemini-2.0-flash"
	defaultGroqModel   = "llama-3.3-70b-versatile"
	defaultOpenAIModel = "gpt-4o-mini"
	defaultCohereModel = "command-r"
)

// buildDefaultProvidersFromEnv returns providers in fixed fallback order:
// Gemini -> Groq -> OpenAI -> Cohere.
func buildDefaultProvidersFromEnv() []Summarizer {
	providers := make([]Summarizer, 0, 4)

	if apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); apiKey != "" {
		providers = append(providers, NewGeminiProvider(apiKey, strings.TrimSpace(os.Getenv("GEMINI_MODEL"))))
	}
	if apiKey := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); apiKey != "" {
		providers = append(providers, NewGroqProvider(apiKey, strings.TrimSpace(os.Getenv("GROQ_MODEL"))))
	}
	if apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); apiKey != "" {
		providers = append(providers, NewOpenAIProvider(apiKey, strings.TrimSpace(os.Getenv("OPENAI_MODEL"))))
	}
	if apiKey := strings.TrimSpace(os.Getenv("COHERE_API_KEY")); apiKey != "" {
		providers = append(providers, NewCohereProvider(apiKey, strings.TrimSpace(os.Getenv("COHERE_MODEL"))))
	}

	return providers
}

type GeminiProvider struct {
	apiKey string
	model  string
	client *http.Client
}

func NewGeminiProvider(apiKey, model string) *GeminiProvider {
	return &GeminiProvider{
		apiKey: strings.TrimSpace(apiKey),
		model:  trimOrDefault(model, defaultGeminiModel),
		client: newProviderHTTPClient(),
	}
}

func (p *GeminiProvider) GenerateRollingSummary(
	ctx context.Context,
	previousState []byte,
	newMessages []Message,
) ([]byte, error) {
	response, err := p.GenerateChatResponse(ctx, buildRollingSummaryPrompt(previousState, newMessages))
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(response)), nil
}

func (p *GeminiProvider) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("gemini prompt is empty")
	}

	modelPath := url.PathEscape(strings.TrimSpace(p.model))
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		modelPath,
		url.QueryEscape(strings.TrimSpace(p.apiKey)),
	)

	payload := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]string{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature": 0.2,
		},
	}

	statusCode, body, err := postJSON(ctx, p.client, endpoint, map[string]string{}, payload)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return "", toProviderStatusError("gemini", statusCode, firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body)))
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned empty response")
	}

	text := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
	if text == "" {
		return "", fmt.Errorf("gemini returned empty text")
	}
	return text, nil
}

type GroqProvider struct {
	apiKey string
	model  string
	client *http.Client
}

func NewGroqProvider(apiKey, model string) *GroqProvider {
	return &GroqProvider{
		apiKey: strings.TrimSpace(apiKey),
		model:  trimOrDefault(model, defaultGroqModel),
		client: newProviderHTTPClient(),
	}
}

func (p *GroqProvider) GenerateRollingSummary(
	ctx context.Context,
	previousState []byte,
	newMessages []Message,
) ([]byte, error) {
	response, err := p.GenerateChatResponse(ctx, buildRollingSummaryPrompt(previousState, newMessages))
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(response)), nil
}

func (p *GroqProvider) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("groq prompt is empty")
	}

	payload := map[string]any{
		"model": p.model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.2,
	}

	statusCode, body, err := postJSON(ctx, p.client, "https://api.groq.com/openai/v1/chat/completions", map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}, payload)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return "", toProviderStatusError("groq", statusCode, firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body)))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("groq returned empty response")
	}

	text := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("groq returned empty text")
	}
	return text, nil
}

type OpenAIProvider struct {
	apiKey string
	model  string
	client *http.Client
}

func NewOpenAIProvider(apiKey, model string) *OpenAIProvider {
	return &OpenAIProvider{
		apiKey: strings.TrimSpace(apiKey),
		model:  trimOrDefault(model, defaultOpenAIModel),
		client: newProviderHTTPClient(),
	}
}

func (p *OpenAIProvider) GenerateRollingSummary(
	ctx context.Context,
	previousState []byte,
	newMessages []Message,
) ([]byte, error) {
	response, err := p.GenerateChatResponse(ctx, buildRollingSummaryPrompt(previousState, newMessages))
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(response)), nil
}

func (p *OpenAIProvider) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("openai prompt is empty")
	}

	payload := map[string]any{
		"model": p.model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.2,
	}

	statusCode, body, err := postJSON(ctx, p.client, "https://api.openai.com/v1/chat/completions", map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}, payload)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return "", toProviderStatusError("openai", statusCode, firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body)))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai returned empty response")
	}

	text := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("openai returned empty text")
	}
	return text, nil
}

type CohereProvider struct {
	apiKey string
	model  string
	client *http.Client
}

func NewCohereProvider(apiKey, model string) *CohereProvider {
	return &CohereProvider{
		apiKey: strings.TrimSpace(apiKey),
		model:  trimOrDefault(model, defaultCohereModel),
		client: newProviderHTTPClient(),
	}
}

func (p *CohereProvider) GenerateRollingSummary(
	ctx context.Context,
	previousState []byte,
	newMessages []Message,
) ([]byte, error) {
	response, err := p.GenerateChatResponse(ctx, buildRollingSummaryPrompt(previousState, newMessages))
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(response)), nil
}

func (p *CohereProvider) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("cohere prompt is empty")
	}

	payload := map[string]any{
		"model":       p.model,
		"message":     prompt,
		"temperature": 0.2,
	}

	statusCode, body, err := postJSON(ctx, p.client, "https://api.cohere.com/v1/chat", map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}, payload)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Text    string `json:"text"`
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(body, &parsed)

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return "", toProviderStatusError("cohere", statusCode, extractMessageFromBody(body))
	}

	text := strings.TrimSpace(parsed.Text)
	if text == "" && len(parsed.Message.Content) > 0 {
		text = strings.TrimSpace(parsed.Message.Content[0].Text)
	}
	if text == "" {
		return "", fmt.Errorf("cohere returned empty text")
	}
	return text, nil
}

func buildRollingSummaryPrompt(previousState []byte, newMessages []Message) string {
	var builder strings.Builder
	builder.WriteString("Update the rolling summary of this conversation.\n")
	builder.WriteString("Keep it concise and preserve important decisions, blockers, and action items.\n\n")

	previous := strings.TrimSpace(string(previousState))
	if previous != "" {
		builder.WriteString("Previous summary:\n")
		builder.WriteString(previous)
		builder.WriteString("\n\n")
	}

	builder.WriteString("New messages:\n")
	for _, message := range newMessages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		sender := strings.TrimSpace(message.SenderName)
		if sender == "" {
			sender = "User"
		}
		builder.WriteString(sender)
		builder.WriteString(": ")
		builder.WriteString(content)
		builder.WriteString("\n")
	}
	builder.WriteString("\nReturn only the updated summary text.")
	return strings.TrimSpace(builder.String())
}

func newProviderHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 45 * time.Second,
	}
}

func postJSON(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	headers map[string]string,
	payload any,
) (int, []byte, error) {
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encodedPayload))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if readErr != nil {
		return response.StatusCode, nil, readErr
	}
	return response.StatusCode, body, nil
}

func toProviderStatusError(provider string, statusCode int, message string) error {
	normalizedMessage := strings.TrimSpace(message)
	if normalizedMessage == "" {
		normalizedMessage = http.StatusText(statusCode)
	}
	lower := strings.ToLower(normalizedMessage)
	isRateLimit := statusCode == http.StatusTooManyRequests ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "exceeded")
	if isRateLimit {
		return &HTTPStatusError{
			Code:     http.StatusTooManyRequests,
			Provider: provider,
			Err:      errors.New(normalizedMessage),
		}
	}
	return &HTTPStatusError{
		Code:     statusCode,
		Provider: provider,
		Err:      errors.New(normalizedMessage),
	}
}

func extractMessageFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return strings.TrimSpace(string(body))
	}

	if rawError, ok := payload["error"]; ok {
		switch typed := rawError.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case map[string]any:
			if message, ok := typed["message"].(string); ok && strings.TrimSpace(message) != "" {
				return strings.TrimSpace(message)
			}
		}
	}

	if message, ok := payload["message"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	return strings.TrimSpace(string(body))
}

func trimOrDefault(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
