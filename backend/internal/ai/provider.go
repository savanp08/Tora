package ai

import (
	"context"
	"fmt"

	"github.com/savanp08/converse/internal/models"
)

type Message = models.Message

// Summarizer is the base interface all AI providers must implement.
type Summarizer interface {
	GenerateRollingSummary(ctx context.Context, previousState []byte, newMessages []Message) ([]byte, error)
	GenerateChatResponse(ctx context.Context, prompt string) (string, error)
}

// ModelHintProvider is an optional capability interface for providers that
// support per-request model tier selection. The router detects this via type
// assertion and uses it when a model tier hint is available.
//
// This keeps the base Summarizer interface stable — providers that don't
// implement this just fall back to their default GenerateChatResponse.
type ModelHintProvider interface {
	GenerateChatResponseWithModelHint(ctx context.Context, prompt, tier string) (string, error)
}

// DetailedChatProvider is an optional capability interface for providers that
// can report the exact model identifier used for a chat response.
type DetailedChatProvider interface {
	GenerateChatResponseDetailed(ctx context.Context, prompt string) (response string, model string, err error)
}

// DetailedModelHintProvider is an optional capability interface for providers
// that support model-tier hints and can also report the exact model used.
type DetailedModelHintProvider interface {
	GenerateChatResponseWithModelHintDetailed(ctx context.Context, prompt, tier string) (response string, model string, err error)
}

// ContextLimiter is defined in compactor.go.
// It is an optional interface — providers that implement it report their
// maximum input token budget so the router can compact prompts before
// dispatch. Providers that don't implement it use defaultMaxInputTokens.

// Model tier constants — used by intent routing to select an appropriate
// model for the complexity of a given query.
const (
	// AIModelTierLight — fastest, cheapest model. Good for conversational
	// replies and simple lookups where reasoning depth doesn't matter.
	AIModelTierLight = "light"

	// AIModelTierStandard — balanced model. Good for task/sprint queries
	// that need accurate data synthesis but not deep analytical reasoning.
	AIModelTierStandard = "standard"

	// AIModelTierHeavy — highest capability model. Used for full reports,
	// team workload analysis, and multi-dimension project health queries
	// where coherent structured reasoning makes a measurable difference.
	AIModelTierHeavy = "heavy"
)

type HTTPStatusError struct {
	Code     int
	Provider string
	Err      error
}

type ProvidersExhaustedError struct {
	LastErr error
}

func (e *ProvidersExhaustedError) Error() string {
	if e == nil || e.LastErr == nil {
		return ErrAllAIProvidersExhausted.Error()
	}
	return fmt.Sprintf("%s: %v", ErrAllAIProvidersExhausted.Error(), e.LastErr)
}

func (e *ProvidersExhaustedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.LastErr
}

func (e *ProvidersExhaustedError) Is(target error) bool {
	return target == ErrAllAIProvidersExhausted
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		if e.Provider == "" {
			return fmt.Sprintf("http status %d", e.Code)
		}
		return fmt.Sprintf("%s http status %d", e.Provider, e.Code)
	}
	if e.Provider == "" {
		return fmt.Sprintf("http status %d: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("%s http status %d: %v", e.Provider, e.Code, e.Err)
}

func (e *HTTPStatusError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *HTTPStatusError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.Code
}
