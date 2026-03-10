package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/savanp08/converse/internal/execution"
	"github.com/savanp08/converse/internal/monitor"
)

// DefaultExecutionManager serves execution requests using the local Piston runtime.
var DefaultExecutionManager = execution.NewExecutionManager()

type codeExecutionRequest struct {
	Language string `json:"language"`
	Files    []struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	} `json:"files"`
	MainFile string `json:"main_file"`
	Stdin    string `json:"stdin"`
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
		recordExecutionStatus("", "error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	req.Language = strings.TrimSpace(req.Language)
	if req.Language == "" {
		recordExecutionStatus(req.Language, "error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "language is required"})
		return
	}

	req.MainFile = strings.TrimSpace(req.MainFile)
	if req.MainFile == "" {
		recordExecutionStatus(req.Language, "error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "main_file is required"})
		return
	}

	if len(req.Files) == 0 {
		recordExecutionStatus(req.Language, "error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "files are required"})
		return
	}

	decodedFiles := make([]execution.ExecutionFile, 0, len(req.Files))
	mainFileIndex := -1
	for _, file := range req.Files {
		fileName := strings.TrimSpace(file.Name)
		if fileName == "" {
			recordExecutionStatus(req.Language, "error")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "each file must include a name"})
			return
		}

		decodedContent, decodeErr := base64.StdEncoding.DecodeString(file.Content)
		if decodeErr != nil {
			recordExecutionStatus(req.Language, "error")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "each file content must be valid Base64"})
			return
		}

		if fileName == req.MainFile && mainFileIndex < 0 {
			mainFileIndex = len(decodedFiles)
		}
		decodedFiles = append(decodedFiles, execution.ExecutionFile{
			Name:    fileName,
			Content: string(decodedContent),
		})
	}

	if mainFileIndex < 0 {
		recordExecutionStatus(req.Language, "error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "main_file must match one of files[].name"})
		return
	}

	if mainFileIndex > 0 {
		mainFile := decodedFiles[mainFileIndex]
		copy(decodedFiles[1:mainFileIndex+1], decodedFiles[0:mainFileIndex])
		decodedFiles[0] = mainFile
	}

	executionRequest := execution.ExecutionRequest{
		Language: req.Language,
		Stdin:    req.Stdin,
		Files:    decodedFiles,
	}

	response, err := DefaultExecutionManager.Execute(r.Context(), executionRequest)
	if err != nil {

		statusCode := execution.HTTPStatus(err)
		message := err.Error()
		metricStatus := "error"

		if errors.Is(err, execution.ErrServerBusy) {
			metricStatus = "rate_limit"
		}

		if strings.TrimSpace(message) == "" {
			message = "Failed to execute code"
		}

		if len(response.Body) > 0 {
			if parsedMessage := parseExecutionErrorBody(response.Body); parsedMessage != "" {
				message = parsedMessage
			}
		}
		if errors.Is(err, execution.ErrManagerShuttingDown) {
			metricStatus = "rate_limit"
		}
		recordExecutionStatus(req.Language, metricStatus)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		return
	}

	result, parseErr := parseExecutionResponse(response.Body)
	if parseErr != nil {
		recordExecutionStatus(req.Language, "error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid execution engine response"})
		return
	}
	recordExecutionStatus(req.Language, "success")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
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

func recordExecutionStatus(language string, status string) {
	normalizedLanguage := strings.ToLower(strings.TrimSpace(language))
	if normalizedLanguage == "" {
		normalizedLanguage = "unknown"
	}
	normalizedStatus := strings.ToLower(strings.TrimSpace(status))
	if normalizedStatus == "" {
		normalizedStatus = "error"
	}
	monitor.CodeExecutionsTotal.WithLabelValues(normalizedLanguage, normalizedStatus).Inc()
}
