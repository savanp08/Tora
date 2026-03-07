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

func (r *AIRouter) RouteRequest(
	ctx context.Context,
	request func(context.Context, Summarizer) (any, error),
) (any, error) {
	if request == nil {
		return nil, ErrAllAIProvidersExhausted
	}
	if r == nil || len(r.providers) == 0 {
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
		return nil, ErrAllAIProvidersExhausted
	}
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
