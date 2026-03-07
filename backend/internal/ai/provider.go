package ai

import (
	"context"
	"fmt"

	"github.com/savanp08/converse/internal/models"
)

type Message = models.Message

type Summarizer interface {
	GenerateRollingSummary(ctx context.Context, previousState []byte, newMessages []Message) ([]byte, error)
	GenerateChatResponse(ctx context.Context, prompt string) (string, error)
}

type HTTPStatusError struct {
	Code     int
	Provider string
	Err      error
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
