package ai

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestBuildDefaultProvidersFromEnvOnlyUsesVertex(t *testing.T) {
	t.Setenv("GOOGLE_VERTEX_API_KEY", "vertex-key")
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("MISTRAL_API_KEY", "mistral-key")
	t.Setenv("GROQ_API_KEY", "groq-key")
	t.Setenv("XAI_API_KEY", "xai-key")

	providers := buildDefaultProvidersFromEnv()
	if len(providers) != 1 {
		t.Fatalf("expected only the Vertex provider, got %d providers", len(providers))
	}

	if _, ok := providers[0].(*VertexGeminiProvider); !ok {
		t.Fatalf("expected first provider to be VertexGeminiProvider, got %T", providers[0])
	}
}

func TestBuildDefaultProvidersFromEnvIgnoresNonVertexProviders(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("MISTRAL_API_KEY", "mistral-key")
	t.Setenv("GROQ_API_KEY", "groq-key")
	t.Setenv("XAI_API_KEY", "xai-key")

	providers := buildDefaultProvidersFromEnv()
	if len(providers) != 0 {
		t.Fatalf("expected no providers without Vertex configured, got %d", len(providers))
	}
}

func TestBuildDefaultProvidersFromEnvIncludesConfiguredVertexModelFirst(t *testing.T) {
	t.Setenv("GOOGLE_VERTEX_API_KEY", "vertex-key")
	t.Setenv("GOOGLE_VERTEX_MODEL", "gemini-2.5-flash-lite-preview")

	providers := buildDefaultProvidersFromEnv()
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}

	vertexProvider, ok := providers[0].(*VertexGeminiProvider)
	if !ok {
		t.Fatalf("expected first provider to be VertexGeminiProvider, got %T", providers[0])
	}
	if len(vertexProvider.models) == 0 {
		t.Fatal("expected vertex models to be populated")
	}
	if vertexProvider.models[0] != "gemini-2.5-flash-lite-preview" {
		t.Fatalf("expected first model to be configured override, got %q", vertexProvider.models[0])
	}
}

func TestToProviderStatusErrorDoesNotTreatContextLengthExceededAsRateLimit(t *testing.T) {
	err := toProviderStatusError("openai", http.StatusBadRequest, "context length exceeded")

	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError, got %T", err)
	}
	if statusErr.StatusCode() != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, statusErr.StatusCode())
	}
}

func TestNewModelCascadeExhaustedErrorPreservesServiceUnavailable(t *testing.T) {
	err := newModelCascadeExhaustedError("openai", []string{"gpt-4o-mini", "gpt-4.1-mini"}, http.StatusServiceUnavailable, "backend overloaded")

	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError, got %T", err)
	}
	if statusErr.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, statusErr.StatusCode())
	}
	if strings.Contains(strings.ToLower(err.Error()), "rate limit") {
		t.Fatalf("did not expect rate-limit wording in %q", err.Error())
	}
}
