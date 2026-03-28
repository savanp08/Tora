package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/savanp08/converse/internal/monitor"
)

const (
	defaultVertexModel  = "gemini-3.1-flash-lite"
	defaultOpenAIModel  = "gpt-4o-mini"
	defaultCohereModel  = "command-r"
	defaultMistralModel = "codestral-latest"
)

var (
	defaultVertexModels = []string{
		defaultVertexModel, // gemini-3.1-flash-lite  $0.25/1M — default
		"gemini-3-flash",   // $0.50/1M
		"gemini-3.1-pro",   // $2.00/1M — fallback for capability
	}
	defaultGeminiModels = []string{
		"gemini-3.1-pro",
		"gemini-3.1-flash",
		"gemini-3.1-flash-lite",
	}
	defaultMistralModels = []string{
		defaultMistralModel,
		"mistral-small-latest",
	}
	defaultGroqModels = []string{
		"llama-3.3-70b-versatile",
		"llama-3.1-8b-instant",
	}
	defaultXAIModels = []string{
		"grok-2-latest",
		"grok-beta",
	}
)

// Per-tier model preference lists — models are tried in order within each
// tier, then the provider's full configured cascade catches anything missed.
//
// Vertex Gemini 3 tiers (cost-optimised, staying under 140 K input tokens):
//
//	light    → gemini-3.1-flash-lite  $0.25/1M input — conversational replies
//	standard → gemini-3-flash         $0.50/1M input — data synthesis
//	heavy    → gemini-3.1-pro         $2.00/1M input — reports / analysis
//
// Gemini Direct tiers — same cost logic, different model name set.
//
// Groq tiers (free, Llama):
//
//	light    → 8b (instant responses)
//	standard → 70b versatile
//	heavy    → 70b versatile (best available on free Groq)
var (
	vertexTierModels = map[string][]string{
		AIModelTierLight:    {"gemini-3.1-flash-lite"},
		AIModelTierStandard: {"gemini-3-flash", "gemini-3.1-flash-lite"},
		AIModelTierHeavy:    {"gemini-3.1-pro", "gemini-3-flash"},
	}
	geminiTierModels = map[string][]string{
		AIModelTierLight:    {"gemini-3.1-flash-lite", "gemini-3.1-flash"},
		AIModelTierStandard: {"gemini-3.1-flash", "gemini-3.1-flash-lite"},
		AIModelTierHeavy:    {"gemini-3.1-pro", "gemini-3.1-flash"},
	}
	groqTierModels = map[string][]string{
		AIModelTierLight:    {"llama-3.1-8b-instant", "llama-3.3-70b-versatile"},
		AIModelTierStandard: {"llama-3.3-70b-versatile", "llama-3.1-8b-instant"},
		AIModelTierHeavy:    {"llama-3.3-70b-versatile", "llama-3.1-8b-instant"},
	}
)

// buildTieredModelList returns a model list with tier-preferred models first,
// followed by the provider's full configured cascade (deduplicated).
// If the tier is unknown or empty, the original configured list is returned.
func buildTieredModelList(configured []string, tier string, tierMaps map[string][]string) []string {
	preferred, ok := tierMaps[tier]
	if !ok || len(preferred) == 0 {
		return configured
	}
	// mergeModelCascade deduplicates and preserves order: preferred → configured
	return mergeModelCascade(preferred, configured)
}

// buildDefaultProvidersFromEnv returns providers in fixed fallback order:
// Vertex Gemini -> Gemini -> OpenAI -> Mistral -> Groq -> XAI.
func buildDefaultProvidersFromEnv() []Summarizer {
	providers := make([]Summarizer, 0, 6)

	if apiKey := strings.TrimSpace(os.Getenv("GOOGLE_VERTEX_API_KEY")); apiKey != "" {
		providers = append(providers, NewVertexGeminiProvider(apiKey, parseModelCascadeFromEnv(
			os.Getenv("GOOGLE_VERTEX_MODELS"),
			os.Getenv("GOOGLE_VERTEX_MODEL"),
		)))
	}

	if apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); apiKey != "" {
		providers = append(providers, NewGeminiProvider(apiKey, parseModelCascadeFromEnv(
			os.Getenv("GEMINI_MODELS"),
			os.Getenv("GEMINI_MODEL"),
		)))
	}
	if apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); apiKey != "" {
		providers = append(providers, NewOpenAIProvider(apiKey, parseModelCascadeFromEnv(
			os.Getenv("OPENAI_MODELS"),
			os.Getenv("OPENAI_MODEL"),
		)))
	}
	if apiKey := strings.TrimSpace(os.Getenv("MISTRAL_API_KEY")); apiKey != "" {
		providers = append(providers, NewMistralProvider(apiKey, parseModelCascadeFromEnv(
			os.Getenv("MISTRAL_MODELS"),
			os.Getenv("MISTRAL_MODEL"),
		)))
	}
	if apiKey := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); apiKey != "" {
		providers = append(providers, NewGroqProvider(apiKey, parseModelCascadeFromEnv(
			os.Getenv("GROQ_MODELS"),
			os.Getenv("GROQ_MODEL"),
		)))
	}
	if apiKey := strings.TrimSpace(os.Getenv("XAI_API_KEY")); apiKey != "" {
		providers = append(providers, NewXAIProvider(apiKey, parseModelCascadeFromEnv(
			os.Getenv("XAI_MODELS"),
			os.Getenv("XAI_MODEL"),
		)))
	}

	return providers
}

type VertexGeminiProvider struct {
	apiKey string
	models []string
	client *http.Client
}

func NewVertexGeminiProvider(apiKey string, models []string) *VertexGeminiProvider {
	return &VertexGeminiProvider{
		apiKey: strings.TrimSpace(apiKey),
		models: mergeModelCascade(models, defaultVertexModels),
		client: newProviderHTTPClient(),
	}
}

// MaxInputTokens satisfies ContextLimiter.
// Capped at 140 K to keep prompt costs predictable — well below the 1 M
// hard limit but leaving 10 K headroom over the 150 K global default.
func (p *VertexGeminiProvider) MaxInputTokens() int { return 140_000 }

func (p *VertexGeminiProvider) GenerateRollingSummary(
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

func (p *VertexGeminiProvider) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	return p.generateWithModels(ctx, prompt, p.models)
}

// GenerateChatResponseWithModelHint satisfies ModelHintProvider.
// It reorders the model cascade to prefer the tier-appropriate model,
// then falls back to the full configured cascade.
func (p *VertexGeminiProvider) GenerateChatResponseWithModelHint(ctx context.Context, prompt, tier string) (string, error) {
	return p.generateWithModels(ctx, prompt, buildTieredModelList(p.models, tier, vertexTierModels))
}

func (p *VertexGeminiProvider) generateWithModels(ctx context.Context, prompt string, models []string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("vertex prompt is empty")
	}

	providerLabel := "google_vertex"
	for _, model := range models {
		endpoint := fmt.Sprintf(
			"https://aiplatform.googleapis.com/v1/publishers/google/models/%s:streamGenerateContent?key=%s",
			url.PathEscape(model),
			url.QueryEscape(strings.TrimSpace(p.apiKey)),
		)

		payload := map[string]any{
			"contents": []map[string]any{
				{
					"role": "user",
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
			recordAIRequest(providerLabel, "error")
			return "", err
		}

		text, statusMessage := extractGeminiTextFromBody(body)
		if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
			if statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable {
				log.Printf("[ai] %s model=%s temporary failure status=%d msg=%s", providerLabel, model, statusCode, statusMessage)
				recordAIRequest(providerLabel, "rate_limit")
				continue
			}
			recordAIRequest(providerLabel, "error")
			return "", toProviderStatusError(providerLabel, statusCode, statusMessage)
		}
		if strings.TrimSpace(text) == "" {
			recordAIRequest(providerLabel, "error")
			return "", fmt.Errorf("%s model=%s returned empty text", providerLabel, model)
		}
		recordAIRequest(providerLabel, "success")
		return strings.TrimSpace(text), nil
	}
	return "", newModelCascadeExhaustedError(providerLabel, models)
}

type GeminiProvider struct {
	apiKey string
	models []string
	client *http.Client
}

func NewGeminiProvider(apiKey string, models []string) *GeminiProvider {
	return &GeminiProvider{
		apiKey: strings.TrimSpace(apiKey),
		models: mergeModelCascade(models, defaultGeminiModels),
		client: newProviderHTTPClient(),
	}
}

// MaxInputTokens satisfies ContextLimiter.
// Capped at 140 K to keep prompt costs predictable — well below the 1 M
// hard limit but leaving 10 K headroom over the 150 K global default.
func (p *GeminiProvider) MaxInputTokens() int { return 140_000 }

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
	return p.generateWithModels(ctx, prompt, p.models)
}

func (p *GeminiProvider) GenerateChatResponseWithModelHint(ctx context.Context, prompt, tier string) (string, error) {
	return p.generateWithModels(ctx, prompt, buildTieredModelList(p.models, tier, geminiTierModels))
}

func (p *GeminiProvider) generateWithModels(ctx context.Context, prompt string, models []string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("gemini prompt is empty")
	}

	providerLabel := "gemini"
	for _, model := range models {
		endpoint := fmt.Sprintf(
			"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
			url.PathEscape(model),
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
			recordAIRequest(providerLabel, "error")
			return "", err
		}

		text, statusMessage := extractGeminiTextFromBody(body)

		if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
			if statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable {
				log.Printf("[ai] %s model=%s temporary failure status=%d msg=%s", providerLabel, model, statusCode, statusMessage)
				recordAIRequest(providerLabel, "rate_limit")
				continue
			}
			recordAIRequest(providerLabel, "error")
			return "", toProviderStatusError(providerLabel, statusCode, statusMessage)
		}
		if strings.TrimSpace(text) == "" {
			recordAIRequest(providerLabel, "error")
			return "", fmt.Errorf("%s model=%s returned empty text", providerLabel, model)
		}
		recordAIRequest(providerLabel, "success")
		return strings.TrimSpace(text), nil
	}
	return "", newModelCascadeExhaustedError(providerLabel, models)
}

type MistralProvider struct {
	apiKey string
	models []string
	client *http.Client
}

func NewMistralProvider(apiKey string, models []string) *MistralProvider {
	return &MistralProvider{
		apiKey: strings.TrimSpace(apiKey),
		models: mergeModelCascade(models, defaultMistralModels),
		client: newProviderHTTPClient(),
	}
}

// MaxInputTokens satisfies ContextLimiter.
// codestral-latest has a 32 K context window; we cap at 28 K to leave room
// for the completion. mistral-small-latest supports 128 K but we use the
// conservative bound for the default model.
func (p *MistralProvider) MaxInputTokens() int { return 28_000 }

func (p *MistralProvider) GenerateRollingSummary(
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

func (p *MistralProvider) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("mistral prompt is empty")
	}

	providerLabel := "mistral"
	for _, model := range p.models {
		payload := map[string]any{
			"model": model,
			"messages": []map[string]string{
				{
					"role":    "user",
					"content": prompt,
				},
			},
			"temperature": 0.2,
		}

		statusCode, body, err := postJSON(ctx, p.client, "https://codestral.mistral.ai/v1/chat/completions", map[string]string{
			"Authorization": "Bearer " + p.apiKey,
		}, payload)
		if err != nil {
			recordAIRequest(providerLabel, "error")
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
			statusMessage := firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body))
			if statusCode == http.StatusTooManyRequests {
				log.Printf("[ai] %s model=%s temporary failure status=%d msg=%s", providerLabel, model, statusCode, statusMessage)
				recordAIRequest(providerLabel, "rate_limit")
				continue
			}
			recordAIRequest(providerLabel, "error")
			return "", toProviderStatusError(providerLabel, statusCode, statusMessage)
		}
		if len(parsed.Choices) == 0 {
			recordAIRequest(providerLabel, "error")
			return "", fmt.Errorf("%s model=%s returned empty response", providerLabel, model)
		}

		text := strings.TrimSpace(parsed.Choices[0].Message.Content)
		if text == "" {
			recordAIRequest(providerLabel, "error")
			return "", fmt.Errorf("%s model=%s returned empty text", providerLabel, model)
		}
		recordAIRequest(providerLabel, "success")
		return text, nil
	}
	return "", newModelCascadeExhaustedError(providerLabel, p.models)
}

func (p *MistralProvider) GenerateToolResponse(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error) {
	return generateOpenAICompatibleToolResponse(
		ctx,
		p.client,
		"https://codestral.mistral.ai/v1/chat/completions",
		map[string]string{
			"Authorization": "Bearer " + p.apiKey,
		},
		p.models,
		"mistral",
		req,
	)
}

type OpenAIProvider struct {
	apiKey string
	models []string
	client *http.Client
}

func NewOpenAIProvider(apiKey string, models []string) *OpenAIProvider {
	cascade := models
	if len(cascade) == 0 {
		cascade = []string{defaultOpenAIModel}
	}
	return &OpenAIProvider{
		apiKey: strings.TrimSpace(apiKey),
		models: mergeModelCascade(cascade, []string{defaultOpenAIModel}),
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

	for _, model := range p.models {
		payload := map[string]any{
			"model": model,
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
			if statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable {
				log.Printf("[ai] openai model=%s temporary failure status=%d msg=%s", model, statusCode, firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body)))
				continue
			}
			return "", toProviderStatusError("openai", statusCode, firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body)))
		}
		if len(parsed.Choices) == 0 {
			return "", fmt.Errorf("openai model=%s returned empty response", model)
		}

		text := strings.TrimSpace(parsed.Choices[0].Message.Content)
		if text == "" {
			return "", fmt.Errorf("openai model=%s returned empty text", model)
		}
		return text, nil
	}
	return "", newModelCascadeExhaustedError("openai", p.models)
}

func (p *OpenAIProvider) GenerateToolResponse(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error) {
	return generateOpenAICompatibleToolResponse(
		ctx,
		p.client,
		"https://api.openai.com/v1/chat/completions",
		map[string]string{
			"Authorization": "Bearer " + p.apiKey,
		},
		p.models,
		"openai",
		req,
	)
}

type XAIProvider struct {
	apiKey string
	models []string
	client *http.Client
}

func NewXAIProvider(apiKey string, models []string) *XAIProvider {
	return &XAIProvider{
		apiKey: strings.TrimSpace(apiKey),
		models: mergeModelCascade(models, defaultXAIModels),
		client: newProviderHTTPClient(),
	}
}

func (p *XAIProvider) GenerateRollingSummary(
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

func (p *XAIProvider) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("xai prompt is empty")
	}

	providerLabel := "xai"
	for _, model := range p.models {
		payload := map[string]any{
			"model": model,
			"messages": []map[string]string{
				{
					"role":    "user",
					"content": prompt,
				},
			},
			"temperature": 0.2,
		}

		statusCode, body, err := postJSON(ctx, p.client, "https://api.x.ai/v1/chat/completions", map[string]string{
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
			statusMessage := firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body))
			if statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable {
				log.Printf("[ai] %s model=%s temporary failure status=%d msg=%s", providerLabel, model, statusCode, statusMessage)
				continue
			}
			return "", toProviderStatusError(providerLabel, statusCode, statusMessage)
		}
		if len(parsed.Choices) == 0 {
			return "", fmt.Errorf("%s model=%s returned empty response", providerLabel, model)
		}

		text := strings.TrimSpace(parsed.Choices[0].Message.Content)
		if text == "" {
			return "", fmt.Errorf("%s model=%s returned empty text", providerLabel, model)
		}
		return text, nil
	}
	return "", newModelCascadeExhaustedError(providerLabel, p.models)
}

func (p *XAIProvider) GenerateToolResponse(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error) {
	return generateOpenAICompatibleToolResponse(
		ctx,
		p.client,
		"https://api.x.ai/v1/chat/completions",
		map[string]string{
			"Authorization": "Bearer " + p.apiKey,
		},
		p.models,
		"xai",
		req,
	)
}

type GroqProvider struct {
	apiKey string
	models []string
	client *http.Client
}

func NewGroqProvider(apiKey string, models []string) *GroqProvider {
	return &GroqProvider{
		apiKey: strings.TrimSpace(apiKey),
		models: mergeModelCascade(models, defaultGroqModels),
		client: newProviderHTTPClient(),
	}
}

// MaxInputTokens satisfies ContextLimiter.
// Both llama-3.3-70b-versatile and llama-3.1-8b-instant have 128 K context
// windows on Groq; cap at 100 K to leave headroom for the completion.
func (p *GroqProvider) MaxInputTokens() int { return 100_000 }

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
	return p.generateWithModels(ctx, prompt, p.models)
}

func (p *GroqProvider) GenerateChatResponseWithModelHint(ctx context.Context, prompt, tier string) (string, error) {
	return p.generateWithModels(ctx, prompt, buildTieredModelList(p.models, tier, groqTierModels))
}

func (p *GroqProvider) generateWithModels(ctx context.Context, prompt string, models []string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("groq prompt is empty")
	}

	providerLabel := "groq"
	for _, model := range models {
		payload := map[string]any{
			"model": model,
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
			recordAIRequest(providerLabel, "error")
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
			statusMessage := firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body))
			if statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable {
				log.Printf("[ai] %s model=%s temporary failure status=%d msg=%s", providerLabel, model, statusCode, statusMessage)
				recordAIRequest(providerLabel, "rate_limit")
				continue
			}
			recordAIRequest(providerLabel, "error")
			return "", toProviderStatusError(providerLabel, statusCode, statusMessage)
		}
		if len(parsed.Choices) == 0 {
			recordAIRequest(providerLabel, "error")
			return "", fmt.Errorf("%s model=%s returned empty response", providerLabel, model)
		}

		text := strings.TrimSpace(parsed.Choices[0].Message.Content)
		if text == "" {
			recordAIRequest(providerLabel, "error")
			return "", fmt.Errorf("%s model=%s returned empty text", providerLabel, model)
		}
		recordAIRequest(providerLabel, "success")
		return text, nil
	}
	return "", newModelCascadeExhaustedError(providerLabel, models)
}

func (p *GroqProvider) GenerateToolResponse(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error) {
	return generateOpenAICompatibleToolResponse(
		ctx,
		p.client,
		"https://api.groq.com/openai/v1/chat/completions",
		map[string]string{
			"Authorization": "Bearer " + p.apiKey,
		},
		p.models,
		"groq",
		req,
	)
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

func generateOpenAICompatibleToolResponse(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	headers map[string]string,
	models []string,
	providerLabel string,
	req AgentProviderRequest,
) (AgentProviderResponse, error) {
	messages, err := buildOpenAICompatibleMessages(req)
	if err != nil {
		return AgentProviderResponse{}, err
	}
	tools := buildOpenAICompatibleTools(req.Tools)
	for _, model := range models {
		payload := map[string]any{
			"model":       strings.TrimSpace(model),
			"messages":    messages,
			"temperature": 0.2,
		}
		if len(tools) > 0 {
			payload["tools"] = tools
			payload["tool_choice"] = "auto"
		}

		statusCode, body, err := postJSON(ctx, client, endpoint, headers, payload)
		if err != nil {
			recordAIRequest(providerLabel, "error")
			return AgentProviderResponse{}, err
		}

		response, parseErr, statusMessage := parseOpenAICompatibleToolResponse(body)
		if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
			if statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable {
				log.Printf("[ai] %s model=%s temporary tool-use failure status=%d msg=%s", providerLabel, model, statusCode, statusMessage)
				recordAIRequest(providerLabel, "rate_limit")
				continue
			}
			recordAIRequest(providerLabel, "error")
			return AgentProviderResponse{}, toProviderStatusError(providerLabel, statusCode, statusMessage)
		}
		if parseErr != nil {
			recordAIRequest(providerLabel, "error")
			return AgentProviderResponse{}, parseErr
		}
		recordAIRequest(providerLabel, "success")
		return response, nil
	}
	return AgentProviderResponse{}, newModelCascadeExhaustedError(providerLabel, models)
}

func buildOpenAICompatibleMessages(req AgentProviderRequest) ([]map[string]any, error) {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if systemPrompt := strings.TrimSpace(req.SystemPrompt); systemPrompt != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	for _, message := range req.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			continue
		}
		switch role {
		case "assistant":
			textParts := make([]string, 0, len(message.Content))
			toolCalls := make([]map[string]any, 0, len(message.Content))
			for _, block := range message.Content {
				switch strings.TrimSpace(block.Type) {
				case "thinking", "text":
					if text := strings.TrimSpace(block.Text); text != "" {
						textParts = append(textParts, text)
					}
				case "tool_use":
					name := strings.TrimSpace(block.Name)
					if name == "" {
						continue
					}
					toolCalls = append(toolCalls, map[string]any{
						"id":   firstNonEmpty(strings.TrimSpace(block.ID), "tool_call"),
						"type": "function",
						"function": map[string]any{
							"name":      name,
							"arguments": marshalOpenAICompatibleString(block.Input),
						},
					})
				}
			}
			if len(toolCalls) == 0 && len(textParts) == 0 {
				continue
			}
			entry := map[string]any{
				"role": "assistant",
			}
			if len(textParts) > 0 {
				entry["content"] = strings.Join(textParts, "\n\n")
			} else {
				entry["content"] = ""
			}
			if len(toolCalls) > 0 {
				entry["tool_calls"] = toolCalls
			}
			messages = append(messages, entry)
		case "user":
			textParts := make([]string, 0, len(message.Content))
			for _, block := range message.Content {
				switch strings.TrimSpace(block.Type) {
				case "text":
					if text := strings.TrimSpace(block.Text); text != "" {
						textParts = append(textParts, text)
					}
				case "tool_result":
					toolCallID := strings.TrimSpace(block.ToolUseID)
					if toolCallID == "" {
						continue
					}
					messages = append(messages, map[string]any{
						"role":         "tool",
						"tool_call_id": toolCallID,
						"content":      marshalOpenAICompatibleString(block.Content),
					})
				}
			}
			if len(textParts) > 0 {
				messages = append(messages, map[string]any{
					"role":    "user",
					"content": strings.Join(textParts, "\n\n"),
				})
			}
		default:
			textParts := make([]string, 0, len(message.Content))
			for _, block := range message.Content {
				if text := strings.TrimSpace(block.Text); text != "" {
					textParts = append(textParts, text)
				}
			}
			if len(textParts) > 0 {
				messages = append(messages, map[string]any{
					"role":    role,
					"content": strings.Join(textParts, "\n\n"),
				})
			}
		}
	}

	return messages, nil
}

func buildOpenAICompatibleTools(tools []AnthropicTool) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	next := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		next = append(next, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": strings.TrimSpace(tool.Description),
				"parameters":  tool.InputSchema,
			},
		})
	}
	return next
}

func parseOpenAICompatibleToolResponse(body []byte) (AgentProviderResponse, error, string) {
	var parsed struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return AgentProviderResponse{}, err, extractMessageFromBody(body)
	}
	if len(parsed.Choices) == 0 {
		return AgentProviderResponse{}, fmt.Errorf("provider returned empty response"), firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body))
	}

	choice := parsed.Choices[0]
	blocks := make([]AgentProviderContentBlock, 0, len(choice.Message.ToolCalls)+1)
	if text := strings.TrimSpace(extractOpenAICompatibleMessageText(choice.Message.Content)); text != "" {
		blocks = append(blocks, AgentProviderContentBlock{
			Type: "text",
			Text: text,
		})
	}
	for _, call := range choice.Message.ToolCalls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		input := make(map[string]any)
		arguments := strings.TrimSpace(call.Function.Arguments)
		if arguments != "" && arguments != "{}" {
			decoder := json.NewDecoder(strings.NewReader(arguments))
			decoder.UseNumber()
			if err := decoder.Decode(&input); err != nil {
				return AgentProviderResponse{}, fmt.Errorf("failed to parse tool arguments for %s: %w", name, err), firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body))
			}
		}
		blocks = append(blocks, AgentProviderContentBlock{
			Type:  "tool_use",
			ID:    strings.TrimSpace(call.ID),
			Name:  name,
			Input: input,
		})
	}
	if len(blocks) == 0 {
		return AgentProviderResponse{}, fmt.Errorf("provider returned empty content"), firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body))
	}
	return AgentProviderResponse{
		Content:    blocks,
		StopReason: strings.TrimSpace(choice.FinishReason),
	}, nil, firstNonEmpty(parsed.Error.Message, extractMessageFromBody(body))
}

func extractOpenAICompatibleMessageText(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(auditStringField(record, "type")) != "text" {
				continue
			}
			if text := strings.TrimSpace(auditStringField(record, "text")); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

func marshalOpenAICompatibleString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "{}"
		}
		return string(encoded)
	}
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

type geminiResponseCandidate struct {
	Content struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"content"`
}

type geminiResponseEnvelope struct {
	Candidates []geminiResponseCandidate `json:"candidates"`
	Error      struct {
		Message string `json:"message"`
	} `json:"error"`
}

func extractGeminiTextFromBody(body []byte) (string, string) {
	var single geminiResponseEnvelope
	if err := json.Unmarshal(body, &single); err == nil {
		text := collectGeminiCandidateText(single.Candidates)
		if text != "" {
			return text, ""
		}
		if strings.TrimSpace(single.Error.Message) != "" {
			return "", strings.TrimSpace(single.Error.Message)
		}
	}

	var stream []geminiResponseEnvelope
	if err := json.Unmarshal(body, &stream); err == nil && len(stream) > 0 {
		chunks := make([]string, 0, len(stream))
		errorMessage := ""
		for _, entry := range stream {
			if errorMessage == "" && strings.TrimSpace(entry.Error.Message) != "" {
				errorMessage = strings.TrimSpace(entry.Error.Message)
			}
			if chunkText := collectGeminiCandidateText(entry.Candidates); chunkText != "" {
				chunks = append(chunks, chunkText)
			}
		}
		if merged := mergeStreamingTextChunks(chunks); merged != "" {
			return merged, ""
		}
		if errorMessage != "" {
			return "", errorMessage
		}
	}

	return "", extractMessageFromBody(body)
}

func collectGeminiCandidateText(candidates []geminiResponseCandidate) string {
	for _, candidate := range candidates {
		var builder strings.Builder
		for _, part := range candidate.Content.Parts {
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			builder.WriteString(text)
		}
		if built := strings.TrimSpace(builder.String()); built != "" {
			return built
		}
	}
	return ""
}

func mergeStreamingTextChunks(chunks []string) string {
	merged := ""
	for _, chunk := range chunks {
		normalized := strings.TrimSpace(chunk)
		if normalized == "" {
			continue
		}
		if merged == "" {
			merged = normalized
			continue
		}
		if strings.HasPrefix(normalized, merged) {
			merged = normalized
			continue
		}
		if strings.HasPrefix(merged, normalized) {
			continue
		}
		merged += normalized
	}
	return strings.TrimSpace(merged)
}

func parseModelCascadeFromEnv(values ...string) []string {
	cascade := make([]string, 0, len(values))
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(token)
			if trimmed == "" {
				continue
			}
			cascade = append(cascade, trimmed)
		}
	}
	return cascade
}

func mergeModelCascade(configured []string, defaults []string) []string {
	cascade := make([]string, 0, len(configured)+len(defaults))
	seen := make(map[string]struct{}, len(configured)+len(defaults))
	appendUnique := func(model string) {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		cascade = append(cascade, trimmed)
	}
	for _, model := range configured {
		appendUnique(model)
	}
	for _, model := range defaults {
		appendUnique(model)
	}
	return cascade
}

func newModelCascadeExhaustedError(provider string, attemptedModels []string) error {
	modelList := strings.Join(attemptedModels, ",")
	if strings.TrimSpace(modelList) == "" {
		modelList = "none"
	}
	return toProviderStatusError(
		provider,
		http.StatusTooManyRequests,
		fmt.Sprintf("%s models exhausted due to rate limit or temporary unavailability (%s)", provider, modelList),
	)
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

func recordAIRequest(provider string, status string) {
	normalizedProvider := strings.TrimSpace(strings.ToLower(provider))
	if normalizedProvider == "" {
		normalizedProvider = "unknown"
	}
	normalizedStatus := strings.TrimSpace(strings.ToLower(status))
	if normalizedStatus == "" {
		normalizedStatus = "error"
	}
	monitor.AIRequestsTotal.WithLabelValues(normalizedProvider, normalizedStatus).Inc()
}
