package ai

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

var ErrAllAIProvidersExhausted = errors.New("All AI providers exhausted")
var DefaultRouter = NewAIRouter(buildDefaultProvidersFromEnv()...)

type AIRouter struct {
	providers []Summarizer
}

func NewAIRouter(providers ...Summarizer) *AIRouter {
	filtered := make([]Summarizer, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	return &AIRouter{providers: filtered}
}

func (r *AIRouter) replaceProviders(providers ...Summarizer) {
	if r == nil {
		return
	}
	filtered := make([]Summarizer, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	r.providers = filtered
}

// RefreshDefaultProvidersFromEnv rebuilds the default provider chain using
// current process environment variables.
func RefreshDefaultProvidersFromEnv() {
	if DefaultRouter == nil {
		DefaultRouter = NewAIRouter()
	}
	DefaultRouter.replaceProviders(buildDefaultProvidersFromEnv()...)
}

func (r *AIRouter) RouteRequest(
	ctx context.Context,
	request func(context.Context, Summarizer) (any, error),
) (any, error) {
	if request == nil {
		// println("AIRouter: nil request provided")
		println("nil req provided")
		return nil, ErrAllAIProvidersExhausted
	}
	if r == nil || len(r.providers) == 0 {
		// println("AIRouter: no providers provided")
		println("no providers provided")
		return nil, ErrAllAIProvidersExhausted
	}

	var lastErr error
	for _, provider := range r.providers {
		result, err := request(ctx, provider)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if isRateLimitError(err) {
			continue
		}
		// Keep iterating so the next provider can serve as fallback even for
		// non-429 transient errors.
		continue
	}

	if lastErr != nil {
		println("All AI providers exhausted. Last error: " + lastErr.Error())
		return nil, &ProvidersExhaustedError{LastErr: lastErr}
	}
	println("All AI providers exhausted with no specific error.")
	return nil, ErrAllAIProvidersExhausted
}

func (r *AIRouter) GenerateRollingSummary(
	ctx context.Context,
	previousState []byte,
	newMessages []Message,
) ([]byte, error) {
	result, err := r.RouteRequest(ctx, func(callCtx context.Context, provider Summarizer) (any, error) {
		return provider.GenerateRollingSummary(callCtx, previousState, newMessages)
	})
	if err != nil {
		return nil, err
	}
	summary, ok := result.([]byte)
	if !ok {
		println("AIRouter: unexpected result type from provider")
		return nil, ErrAllAIProvidersExhausted
	}
	return summary, nil
}

func (r *AIRouter) GenerateChatResponse(ctx context.Context, prompt string) (string, error) {
	result, err := r.RouteRequest(ctx, func(callCtx context.Context, provider Summarizer) (any, error) {
		return provider.GenerateChatResponse(callCtx, compactForProvider(prompt, provider))
	})
	if err != nil {
		return "", err
	}
	response, ok := result.(string)
	if !ok {
		println("AIRouter: unexpected result type from provider")
		return "", ErrAllAIProvidersExhausted
	}
	return response, nil
}

func (r *AIRouter) GenerateChatResponseDetailed(ctx context.Context, prompt string) (string, string, error) {
	type detailedChatResult struct {
		Response string
		Model    string
	}

	result, err := r.RouteRequest(ctx, func(callCtx context.Context, provider Summarizer) (any, error) {
		compacted := compactForProvider(prompt, provider)
		if detailedProvider, ok := provider.(DetailedChatProvider); ok {
			response, model, detailedErr := detailedProvider.GenerateChatResponseDetailed(callCtx, compacted)
			return detailedChatResult{Response: response, Model: model}, detailedErr
		}
		response, basicErr := provider.GenerateChatResponse(callCtx, compacted)
		return detailedChatResult{Response: response}, basicErr
	})
	if err != nil {
		return "", "", err
	}
	detailed, ok := result.(detailedChatResult)
	if !ok {
		println("AIRouter: unexpected detailed result type from provider")
		return "", "", ErrAllAIProvidersExhausted
	}
	return detailed.Response, detailed.Model, nil
}

// GenerateChatResponseWithHint routes the request using the model tier hint
// when the provider supports it (ModelHintProvider), falling back to the
// default GenerateChatResponse for providers that don't.
//
// This lets Vertex/Gemini/Groq use tier-appropriate models (pro for heavy
// reports, flash-lite for conversational replies) while other providers in
// the fallback chain continue to work unchanged.
func (r *AIRouter) GenerateChatResponseWithHint(ctx context.Context, prompt, modelTier string) (string, error) {
	result, err := r.RouteRequest(ctx, func(callCtx context.Context, provider Summarizer) (any, error) {
		compacted := compactForProvider(prompt, provider)
		if modelTier != "" {
			if hintProvider, ok := provider.(ModelHintProvider); ok {
				return hintProvider.GenerateChatResponseWithModelHint(callCtx, compacted, modelTier)
			}
		}
		return provider.GenerateChatResponse(callCtx, compacted)
	})
	if err != nil {
		return "", err
	}
	response, ok := result.(string)
	if !ok {
		println("AIRouter: unexpected result type from provider")
		return "", ErrAllAIProvidersExhausted
	}
	return response, nil
}

func (r *AIRouter) GenerateChatResponseWithHintDetailed(ctx context.Context, prompt, modelTier string) (string, string, error) {
	type detailedChatResult struct {
		Response string
		Model    string
	}

	result, err := r.RouteRequest(ctx, func(callCtx context.Context, provider Summarizer) (any, error) {
		compacted := compactForProvider(prompt, provider)
		if modelTier != "" {
			if detailedHintProvider, ok := provider.(DetailedModelHintProvider); ok {
				response, model, detailedErr := detailedHintProvider.GenerateChatResponseWithModelHintDetailed(callCtx, compacted, modelTier)
				return detailedChatResult{Response: response, Model: model}, detailedErr
			}
			if hintProvider, ok := provider.(ModelHintProvider); ok {
				response, hintedErr := hintProvider.GenerateChatResponseWithModelHint(callCtx, compacted, modelTier)
				return detailedChatResult{Response: response}, hintedErr
			}
		}
		if detailedProvider, ok := provider.(DetailedChatProvider); ok {
			response, model, detailedErr := detailedProvider.GenerateChatResponseDetailed(callCtx, compacted)
			return detailedChatResult{Response: response, Model: model}, detailedErr
		}
		response, basicErr := provider.GenerateChatResponse(callCtx, compacted)
		return detailedChatResult{Response: response}, basicErr
	})
	if err != nil {
		return "", "", err
	}
	detailed, ok := result.(detailedChatResult)
	if !ok {
		println("AIRouter: unexpected hinted detailed result type from provider")
		return "", "", ErrAllAIProvidersExhausted
	}
	return detailed.Response, detailed.Model, nil
}

func (r *AIRouter) SupportsToolUse() bool {
	if r == nil {
		return false
	}
	for _, provider := range r.providers {
		if _, ok := provider.(ToolUseProvider); ok {
			return true
		}
	}
	return false
}

func (r *AIRouter) GenerateToolResponse(ctx context.Context, req AgentProviderRequest) (AgentProviderResponse, error) {
	if r == nil || len(r.providers) == 0 {
		return AgentProviderResponse{}, ErrAllAIProvidersExhausted
	}

	var (
		lastErr        error
		checkedAnyTool bool
	)
	for _, provider := range r.providers {
		toolProvider, ok := provider.(ToolUseProvider)
		if !ok {
			continue
		}
		checkedAnyTool = true
		response, err := toolProvider.GenerateToolResponse(ctx, req)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return AgentProviderResponse{}, err
		}
		if isRateLimitError(err) {
			continue
		}
		continue
	}

	if !checkedAnyTool {
		return AgentProviderResponse{}, ErrAllAIProvidersExhausted
	}
	if lastErr != nil {
		return AgentProviderResponse{}, lastErr
	}
	return AgentProviderResponse{}, ErrAllAIProvidersExhausted
}

type statusCodeError interface {
	StatusCode() int
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	var withStatusCode statusCodeError
	if errors.As(err, &withStatusCode) && withStatusCode.StatusCode() == http.StatusTooManyRequests {
		return true
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "429") || isProviderRateLimitMessage(lower)
}
