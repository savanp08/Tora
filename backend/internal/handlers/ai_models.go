package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// AIModelInfo describes a single AI model exposed to the frontend.
type AIModelInfo struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
	// Tier maps to the backend AIModelTier constants: light, standard, heavy.
	Tier string `json:"tier"`
	// Icon is a short token the frontend uses to pick a provider icon/color.
	Icon string `json:"icon"`
}

// AIEffortLevel describes a user-facing effort/speed preset.
type AIEffortLevel struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Tier is the backend AIModelTier this effort level maps to.
	Tier string `json:"tier"`
}

// AIModelsResponse is the response payload for GET /api/ai/models.
type AIModelsResponse struct {
	Models  []AIModelInfo   `json:"models"`
	Efforts []AIEffortLevel `json:"efforts"`
}

// HandleGetAIModels returns available AI models and effort levels.
// Models are filtered to only those whose provider API key is configured.
// Efforts are ordered by speed ascending: fast → extended → max.
func HandleGetAIModels(w http.ResponseWriter, r *http.Request) {
	models := buildAvailableAIModels()
	efforts := buildAIEffortLevels()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(AIModelsResponse{
		Models:  models,
		Efforts: efforts,
	})
}

// buildAvailableAIModels returns models for all providers whose API keys are set.
func buildAvailableAIModels() []AIModelInfo {
	var models []AIModelInfo

	if strings.TrimSpace(os.Getenv("GOOGLE_VERTEX_API_KEY")) != "" {
		models = append(models,
			AIModelInfo{ID: "gemini-3.1-flash-lite-preview", Label: "Gemini 3.1 Flash Lite", Provider: "vertex", Tier: "light", Icon: "gemini"},
			AIModelInfo{ID: "gemini-3-flash-preview", Label: "Gemini 3 Flash", Provider: "vertex", Tier: "standard", Icon: "gemini"},
			AIModelInfo{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", Provider: "vertex", Tier: "standard", Icon: "gemini"},
			AIModelInfo{ID: "gemini-3.1-pro-preview", Label: "Gemini 3.1 Pro", Provider: "vertex", Tier: "heavy", Icon: "gemini"},
			AIModelInfo{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", Provider: "vertex", Tier: "heavy", Icon: "gemini"},
		)
	}

	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" {
		// Only add if Vertex is not configured (avoid duplicates)
		if strings.TrimSpace(os.Getenv("GOOGLE_VERTEX_API_KEY")) == "" {
			models = append(models,
				AIModelInfo{ID: "gemini-3.1-flash-lite-preview", Label: "Gemini 3.1 Flash Lite", Provider: "gemini", Tier: "light", Icon: "gemini"},
				AIModelInfo{ID: "gemini-3-flash-preview", Label: "Gemini 3 Flash", Provider: "gemini", Tier: "standard", Icon: "gemini"},
				AIModelInfo{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", Provider: "gemini", Tier: "standard", Icon: "gemini"},
				AIModelInfo{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", Provider: "gemini", Tier: "heavy", Icon: "gemini"},
			)
		}
	}

	if strings.TrimSpace(os.Getenv("GROQ_API_KEY")) != "" {
		models = append(models,
			AIModelInfo{ID: "llama-3.1-8b-instant", Label: "Llama 3.1 8B", Provider: "groq", Tier: "light", Icon: "llama"},
			AIModelInfo{ID: "llama-3.3-70b-versatile", Label: "Llama 3.3 70B", Provider: "groq", Tier: "standard", Icon: "llama"},
		)
	}

	if strings.TrimSpace(os.Getenv("XAI_API_KEY")) != "" {
		models = append(models,
			AIModelInfo{ID: "grok-2-latest", Label: "Grok 2", Provider: "xai", Tier: "standard", Icon: "grok"},
			AIModelInfo{ID: "grok-beta", Label: "Grok Beta", Provider: "xai", Tier: "heavy", Icon: "grok"},
		)
	}

	if strings.TrimSpace(os.Getenv("MISTRAL_API_KEY")) != "" {
		models = append(models,
			AIModelInfo{ID: "mistral-small-latest", Label: "Mistral Small", Provider: "mistral", Tier: "light", Icon: "mistral"},
			AIModelInfo{ID: "mistral-medium-latest", Label: "Mistral Medium", Provider: "mistral", Tier: "standard", Icon: "mistral"},
		)
	}

	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		models = append(models,
			AIModelInfo{ID: "gpt-4o-mini", Label: "GPT-4o Mini", Provider: "openai", Tier: "light", Icon: "openai"},
			AIModelInfo{ID: "gpt-4o", Label: "GPT-4o", Provider: "openai", Tier: "standard", Icon: "openai"},
		)
	}

	return models
}

// buildAIEffortLevels returns effort presets ordered by speed ascending (fast → max).
func buildAIEffortLevels() []AIEffortLevel {
	return []AIEffortLevel{
		{ID: "fast", Label: "Fast", Description: "Quickest responses, light reasoning", Tier: "light"},
		{ID: "extended", Label: "Extended", Description: "Balanced quality and speed", Tier: "standard"},
		{ID: "max", Label: "Max", Description: "Deepest reasoning, best quality", Tier: "heavy"},
	}
}
