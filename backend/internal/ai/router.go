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
		println("nil req provided");
		return nil, ErrAllAIProvidersExhausted
	}
	if r == nil || len(r.providers) == 0 {
		// println("AIRouter: no providers provided")
		println("no providers provided");
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
		return nil, ErrAllAIProvidersExhausted
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
		return provider.GenerateChatResponse(callCtx, prompt)
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

	return strings.Contains(strings.ToLower(err.Error()), "429")
}
