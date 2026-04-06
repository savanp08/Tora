package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsLongRunningAIAPIPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		want bool
	}{
		{path: "/api/rooms/abc/ai-organize", want: true},
		{path: "/api/rooms/abc/ai-timeline", want: true},
		{path: "/api/rooms/abc/ai-timeline/stream", want: true},
		{path: "/api/rooms/abc/ai-edit", want: true},
		{path: "/api/rooms/abc/ai-edit/stream", want: true},
		{path: "/api/ai/chat", want: false},
		{path: "/api/rooms/abc/tasks", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := isLongRunningAIAPIPath(tc.path); got != tc.want {
				t.Fatalf("isLongRunningAIAPIPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestAPITimeoutMiddlewareUsesLongAIRequestBudget(t *testing.T) {
	t.Parallel()

	var remaining time.Duration
	handler := apiTimeoutMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("expected request deadline to be set")
		}
		remaining = time.Until(deadline)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/rooms/test-room/ai-timeline/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusNoContent)
	}
	if remaining < 19*time.Minute || remaining > apiLongAIRequestTimeout {
		t.Fatalf("expected long AI timeout near %s, got %s remaining", apiLongAIRequestTimeout, remaining)
	}
}

func TestAPITimeoutMiddlewareKeepsDefaultBudgetForRegularAPIPaths(t *testing.T) {
	t.Parallel()

	var remaining time.Duration
	handler := apiTimeoutMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("expected request deadline to be set")
		}
		remaining = time.Until(deadline)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/rooms/test-room/tasks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusNoContent)
	}
	if remaining < 55*time.Second || remaining > apiDefaultRequestTimeout {
		t.Fatalf("expected default API timeout near %s, got %s remaining", apiDefaultRequestTimeout, remaining)
	}
}
