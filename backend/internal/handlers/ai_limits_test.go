package handlers

import (
	"strings"
	"testing"
	"time"
)

func TestPrivateAILimitExceededErrorPublicMessageIncludesWindowAndReset(t *testing.T) {
	resetAt := time.Date(2026, time.March, 29, 23, 15, 0, 0, time.UTC)
	message := (&privateAILimitExceededError{
		Scope:   aiLimitScopeDeviceID,
		Window:  aiLimitWindowHour,
		Limit:   35,
		Current: 35,
		ResetAt: resetAt,
		ResetIn: 42 * time.Minute,
	}).PublicMessage()

	for _, needle := range []string{
		"this device",
		"hourly window",
		"35/35 requests",
		"Resets in 42m",
		"2026-03-29T23:15:00Z",
	} {
		if !strings.Contains(message, needle) {
			t.Fatalf("expected message to contain %q, got %q", needle, message)
		}
	}
}
