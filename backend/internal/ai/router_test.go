package ai

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type routerTestProvider struct{}

func (routerTestProvider) GenerateRollingSummary(context.Context, []byte, []Message) ([]byte, error) {
	return nil, nil
}

func (routerTestProvider) GenerateChatResponse(context.Context, string) (string, error) {
	return "", nil
}

func TestRouteRequestWrapsLastProviderError(t *testing.T) {
	router := NewAIRouter(routerTestProvider{}, routerTestProvider{})
	firstErr := &HTTPStatusError{
		Code:     http.StatusTooManyRequests,
		Provider: "openai",
		Err:      errors.New("quota exceeded"),
	}
	lastErr := &HTTPStatusError{
		Code:     http.StatusBadRequest,
		Provider: "openai",
		Err:      errors.New("context length exceeded"),
	}

	attempt := 0
	_, err := router.RouteRequest(context.Background(), func(context.Context, Summarizer) (any, error) {
		attempt++
		if attempt == 1 {
			return nil, firstErr
		}
		return nil, lastErr
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrAllAIProvidersExhausted) {
		t.Fatalf("expected ErrAllAIProvidersExhausted, got %v", err)
	}

	var exhaustedErr *ProvidersExhaustedError
	if !errors.As(err, &exhaustedErr) {
		t.Fatalf("expected ProvidersExhaustedError, got %T", err)
	}
	if exhaustedErr.LastErr != lastErr {
		t.Fatalf("expected last error to be preserved, got %v", exhaustedErr.LastErr)
	}

	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected wrapped HTTPStatusError, got %T", err)
	}
	if statusErr.StatusCode() != http.StatusBadRequest {
		t.Fatalf("expected preserved status %d, got %d", http.StatusBadRequest, statusErr.StatusCode())
	}
}
