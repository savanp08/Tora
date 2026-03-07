package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/savanp08/converse/internal/execution"
)

// DefaultExecutionManager serves execution requests using the local Piston runtime.
var DefaultExecutionManager = execution.NewExecutionManager()

type codeExecutionRequest struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

type codeExecutionResponse struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

type pistonExecuteEnvelope struct {
	Compile struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	} `json:"compile"`
	Run struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	} `json:"run"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func HandleCodeExecution(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var req codeExecutionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	req.Language = strings.TrimSpace(req.Language)
	if req.Language == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "language is required"})
		return
	}

	decodedCodeBytes, decodeErr := base64.StdEncoding.DecodeString(req.Code)
	if decodeErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Code must be valid Base64"})
		return
	}
	decodedCode := string(decodedCodeBytes)

	executionRequest := execution.ExecutionRequest{
		Language: req.Language,
		Files: []execution.ExecutionFile{
			{
				Name:    defaultSourceFilename(req.Language),
				Content: decodedCode,
			},
		},
	}

	response, err := DefaultExecutionManager.Execute(r.Context(), executionRequest)
	if err != nil && response.Body != nil {

		statusCode := execution.HTTPStatus(err)
		message := err.Error()

		if errors.Is(err, execution.ErrServerBusy) {
			statusCode = http.StatusTooManyRequests
		}

		if strings.TrimSpace(message) == "" {
			message = "Failed to execute code"
		}

		if parsedMessage := parseExecutionErrorBody(response.Body); parsedMessage != "" {
			message = parsedMessage
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		return
	}

	result, parseErr := parseExecutionResponse(response.Body)
	if parseErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid execution engine response"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

func defaultSourceFilename(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "java":
		return "Main.java"
	case "cpp", "c++":
		return "main.cpp"
	case "c":
		return "main.c"
	case "go", "golang":
		return "main.go"
	case "rust", "rs":
		return "main.rs"
	case "python", "py":
		return "main.py"
	case "typescript", "ts":
		return "main.ts"
	case "javascript", "js", "mjs", "cjs":
		return "main.js"
	default:
		return "main.txt"
	}
}

func parseExecutionResponse(body []byte) (codeExecutionResponse, error) {
	if len(body) == 0 {
		return codeExecutionResponse{}, nil
	}

	var envelope pistonExecuteEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return codeExecutionResponse{}, err
	}

	stdout := joinExecutionPhaseOutput(envelope.Compile.Stdout, envelope.Run.Stdout)
	stderr := joinExecutionPhaseOutput(envelope.Compile.Stderr, envelope.Run.Stderr)
	if strings.TrimSpace(stdout) == "" {
		stdout = envelope.Stdout
	}
	if strings.TrimSpace(stderr) == "" {
		stderr = envelope.Stderr
	}

	return codeExecutionResponse{
		Stdout: stdout,
		Stderr: stderr,
	}, nil
}

func parseExecutionErrorBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var envelope pistonExecuteEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}

	if strings.TrimSpace(envelope.Error) != "" {
		return strings.TrimSpace(envelope.Error)
	}
	if strings.TrimSpace(envelope.Message) != "" {
		return strings.TrimSpace(envelope.Message)
	}
	if strings.TrimSpace(envelope.Compile.Stderr) != "" {
		return strings.TrimSpace(envelope.Compile.Stderr)
	}
	if strings.TrimSpace(envelope.Run.Stderr) != "" {
		return strings.TrimSpace(envelope.Run.Stderr)
	}
	if strings.TrimSpace(envelope.Stderr) != "" {
		return strings.TrimSpace(envelope.Stderr)
	}
	return ""
}

func joinExecutionPhaseOutput(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	return strings.Join(filtered, "\n")
}
