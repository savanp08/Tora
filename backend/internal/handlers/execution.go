package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
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
	Stdout string              `json:"stdout"`
	Stderr string              `json:"stderr"`
	Files  []codeExecutionFile `json:"files,omitempty"`
}

type codeExecutionFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
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

	normalizedMainFile, mainFileErr := normalizeExecutionWorkspacePath(req.MainFile)
	if mainFileErr != nil {
		recordExecutionStatus(req.Language, "error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": mainFileErr.Error()})
		return
	}
	req.MainFile = normalizedMainFile

	decodedFiles := make([]execution.ExecutionFile, 0, len(req.Files))
	mainFileIndex := -1
	for _, file := range req.Files {
		fileName, fileNameErr := normalizeExecutionWorkspacePath(file.Name)
		if fileNameErr != nil {
			recordExecutionStatus(req.Language, "error")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fileNameErr.Error()})
			return
		}
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

	organizedFiles, organizedMainFile, organizeErr := organizeFilesForMainFile(decodedFiles, req.MainFile)
	if organizeErr != nil {
		recordExecutionStatus(req.Language, "error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": organizeErr.Error()})
		return
	}
	decodedFiles = organizedFiles
	req.MainFile = organizedMainFile

	executionRequest := execution.ExecutionRequest{
		Language: req.Language,
		MainFile: req.MainFile,
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

func normalizeExecutionWorkspacePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	for strings.HasPrefix(trimmed, "./") {
		trimmed = strings.TrimPrefix(trimmed, "./")
	}
	trimmed = strings.TrimLeft(trimmed, "/")
	if trimmed == "" {
		return "", nil
	}

	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "" {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("invalid file path %q: parent directory traversal is not allowed", raw)
	}
	return cleaned, nil
}

func organizeFilesForMainFile(
	files []execution.ExecutionFile,
	mainFile string,
) ([]execution.ExecutionFile, string, error) {
	if len(files) == 0 {
		return nil, mainFile, nil
	}

	organized := make([]execution.ExecutionFile, 0, len(files))
	mainDir := path.Dir(mainFile)
	if mainDir == "." {
		mainDir = ""
	}
	rebasedMainFile := mainFile
	if mainDir != "" {
		rebasedMainFile = strings.TrimPrefix(mainFile, mainDir+"/")
	}

	seenNames := make(map[string]struct{}, len(files))
	for _, file := range files {
		nextName := file.Name
		if mainDir != "" && strings.HasPrefix(nextName, mainDir+"/") {
			nextName = strings.TrimPrefix(nextName, mainDir+"/")
		}
		if strings.TrimSpace(nextName) == "" {
			return nil, "", errors.New("file path cannot be empty after path normalization")
		}
		if _, exists := seenNames[nextName]; exists {
			return nil, "", fmt.Errorf(
				"duplicate file path %q after organizing workspace relative to main_file %q",
				nextName,
				mainFile,
			)
		}
		seenNames[nextName] = struct{}{}
		organized = append(organized, execution.ExecutionFile{
			Name:    nextName,
			Content: file.Content,
		})
	}

	mainFileIndex := -1
	for index := range organized {
		if organized[index].Name == rebasedMainFile {
			mainFileIndex = index
			break
		}
	}
	if mainFileIndex < 0 {
		return nil, "", errors.New("main_file could not be resolved after workspace organization")
	}
	if mainFileIndex > 0 {
		mainEntry := organized[mainFileIndex]
		copy(organized[1:mainFileIndex+1], organized[0:mainFileIndex])
		organized[0] = mainEntry
	}

	return organized, rebasedMainFile, nil
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
	cleanStdout, extractedFiles := extractExecutionFilesFromStdout(stdout)

	return codeExecutionResponse{
		Stdout: cleanStdout,
		Stderr: stderr,
		Files:  extractedFiles,
	}, nil
}

const (
	toraFileStartMarkerPrefix = "===TORA_FILE_START:"
	toraFileEndMarkerPrefix   = "===TORA_FILE_END:"
	toraFileMarkerSuffix      = "==="
)

func extractExecutionFilesFromStdout(stdout string) (string, []codeExecutionFile) {
	if strings.TrimSpace(stdout) == "" {
		return stdout, nil
	}

	files := make([]codeExecutionFile, 0, 8)
	var cleaned strings.Builder
	searchOffset := 0

	for {
		startRelative := strings.Index(stdout[searchOffset:], toraFileStartMarkerPrefix)
		if startRelative < 0 {
			cleaned.WriteString(stdout[searchOffset:])
			break
		}

		startIndex := searchOffset + startRelative
		cleaned.WriteString(stdout[searchOffset:startIndex])

		fileNameStart := startIndex + len(toraFileStartMarkerPrefix)
		markerSuffixRelative := strings.Index(stdout[fileNameStart:], toraFileMarkerSuffix)
		if markerSuffixRelative < 0 {
			cleaned.WriteString(stdout[startIndex:])
			break
		}
		fileNameEnd := fileNameStart + markerSuffixRelative
		fileName := strings.TrimSpace(stdout[fileNameStart:fileNameEnd])
		if fileName == "" {
			cleaned.WriteString(stdout[startIndex:])
			break
		}

		contentStart := fileNameEnd + len(toraFileMarkerSuffix)
		if strings.HasPrefix(stdout[contentStart:], "\r\n") {
			contentStart += 2
		} else if strings.HasPrefix(stdout[contentStart:], "\n") {
			contentStart += 1
		}

		endMarker := toraFileEndMarkerPrefix + fileName + toraFileMarkerSuffix
		endRelative := strings.Index(stdout[contentStart:], endMarker)
		if endRelative < 0 {
			cleaned.WriteString(stdout[startIndex:])
			break
		}
		contentEnd := contentStart + endRelative
		fileContent := stdout[contentStart:contentEnd]
		files = append(files, codeExecutionFile{
			Name:    fileName,
			Content: fileContent,
		})

		searchOffset = contentEnd + len(endMarker)
		if strings.HasPrefix(stdout[searchOffset:], "\r\n") {
			searchOffset += 2
		} else if strings.HasPrefix(stdout[searchOffset:], "\n") {
			searchOffset += 1
		}
	}

	if len(files) == 0 {
		return stdout, nil
	}
	return cleaned.String(), files
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
