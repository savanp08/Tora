package ai

import "testing"

func TestBuildDefaultProvidersFromEnvPrioritizesVertex(t *testing.T) {
	t.Setenv("GOOGLE_VERTEX_API_KEY", "vertex-key")
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("MISTRAL_API_KEY", "mistral-key")
	t.Setenv("GROQ_API_KEY", "groq-key")
	t.Setenv("XAI_API_KEY", "xai-key")

	providers := buildDefaultProvidersFromEnv()
	if len(providers) != 6 {
		t.Fatalf("expected 6 providers, got %d", len(providers))
	}

	if _, ok := providers[0].(*VertexGeminiProvider); !ok {
		t.Fatalf("expected first provider to be VertexGeminiProvider, got %T", providers[0])
	}
	if _, ok := providers[1].(*GeminiProvider); !ok {
		t.Fatalf("expected second provider to be GeminiProvider, got %T", providers[1])
	}
	if _, ok := providers[2].(*OpenAIProvider); !ok {
		t.Fatalf("expected third provider to be OpenAIProvider, got %T", providers[2])
	}
	if _, ok := providers[3].(*MistralProvider); !ok {
		t.Fatalf("expected fourth provider to be MistralProvider, got %T", providers[3])
	}
	if _, ok := providers[4].(*GroqProvider); !ok {
		t.Fatalf("expected fifth provider to be GroqProvider, got %T", providers[4])
	}
	if _, ok := providers[5].(*XAIProvider); !ok {
		t.Fatalf("expected sixth provider to be XAIProvider, got %T", providers[5])
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
