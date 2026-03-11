package execution

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultPistonEndpoint    = "http://127.0.0.1:2000/api/v2/execute"
	pistonEndpointEnvVar     = "PISTON_ENDPOINT"
	defaultWorkerCount       = 10
	defaultQueueSize         = 200
	defaultQueueWaitTimeout  = 30 * time.Second
	defaultRequestTimeout    = 10 * time.Second
	maxPistonResponseBytes   = 4 * 1024 * 1024
	defaultRunTimeoutMs      = 8000
	defaultCompileTimeoutMs  = 10000
	defaultMemoryLimitBytes  = 209715200
	defaultMaxProcessCount   = 64
	defaultMaxOpenFiles      = 64
	queueBusyErrorMessage    = "execution queue is full"
	shutdownErrorMessage     = "execution service is shutting down"
	timeoutErrorMessage      = "execution timed out"
	transportErrorMessage    = "failed to reach execution engine"
	responseReadErrorMessage = "failed to read execution response"
)

var ErrExecutionTimeout = errors.New(timeoutErrorMessage)

// ManagerError carries an HTTP status so handlers can map failures directly.
type ManagerError struct {
	StatusCode int
	Message    string
	Err        error
}

func (e *ManagerError) Error() string {
	if e == nil {
		return "execution error"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "execution error"
	}
	if e.Err == nil {
		return message
	}
	return fmt.Sprintf("%s: %v", message, e.Err)
}

func (e *ManagerError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var ErrServerBusy = &ManagerError{
	StatusCode: http.StatusServiceUnavailable,
	Message:    queueBusyErrorMessage,
}

var ErrManagerShuttingDown = &ManagerError{
	StatusCode: http.StatusServiceUnavailable,
	Message:    shutdownErrorMessage,
}

// ExecutionFile mirrors a single file entry for Piston's execute API.
type ExecutionFile struct {
	Name    string `json:"name,omitempty"`
	Content string `json:"content"`
}

// ExecutionRequest is forwarded to /api/v2/execute on a local Piston instance.
type ExecutionRequest struct {
	Language           string          `json:"language"`
	Version            string          `json:"version,omitempty"`
	Files              []ExecutionFile `json:"files"`
	MainFile           string          `json:"-"`
	Stdin              string          `json:"stdin,omitempty"`
	Args               []string        `json:"args,omitempty"`
	CompileTimeout     int             `json:"compile_timeout,omitempty"`
	RunTimeout         int             `json:"run_timeout,omitempty"`
	MemoryLimit        int             `json:"memory_limit,omitempty"`
	CompileMemoryLimit int             `json:"compile_memory_limit,omitempty"`
	RunMemoryLimit     int             `json:"run_memory_limit,omitempty"`
	MaxProcessCount    int             `json:"max_process_count,omitempty"`
	MaxOpenFiles       int             `json:"max_open_files,omitempty"`
}

// ExecutionResponse is raw response payload and status from Piston.
type ExecutionResponse struct {
	StatusCode int
	Body       []byte
}

type ExecutionResult struct {
	Response ExecutionResponse
	Err      error
}

// ExecutionJob is a queue entry processed by workers.
type ExecutionJob struct {
	Ctx      context.Context
	Request  ExecutionRequest
	ResultCh chan<- ExecutionResult
}

// ExecutionManager limits concurrency and routes responses to request owners.
type ExecutionManager struct {
	pistonEndpoint string
	httpClient     *http.Client
	requestTimeout time.Duration
	queueWait      time.Duration

	jobs chan ExecutionJob

	submitMu        sync.RWMutex
	shutdownStarted bool
	shutdownOnce    sync.Once
	workersWG       sync.WaitGroup
}

type pistonRuntimeSpec struct {
	Language string
	Version  string
}

var frontendToPistonRuntime = map[string]pistonRuntimeSpec{
	"javascript": {Language: "javascript", Version: "18.15.0"},
	"js":         {Language: "javascript", Version: "18.15.0"},
	"typescript": {Language: "typescript", Version: "5.0.3"},
	"ts":         {Language: "typescript", Version: "5.0.3"},
	"python":     {Language: "python", Version: "3.10.0"},
	"py":         {Language: "python", Version: "3.10.0"},
	"cpp":        {Language: "cpp", Version: "10.2.0"},
	"c++":        {Language: "cpp", Version: "10.2.0"},
	"c":          {Language: "c", Version: "10.2.0"},
	"java":       {Language: "java", Version: "15.0.2"},
	"go":         {Language: "go", Version: "1.20.2"},
	"golang":     {Language: "go", Version: "1.20.2"},
	"rust":       {Language: "rust", Version: "1.68.2"},
	"rs":         {Language: "rust", Version: "1.68.2"},
	"shell":      {Language: "bash", Version: "5.2.0"},
	"bash":       {Language: "bash", Version: "5.2.0"},
	"sh":         {Language: "bash", Version: "5.2.0"},
}

func NewExecutionManager() *ExecutionManager {
	return NewExecutionManagerWithOptions(resolveDefaultPistonEndpoint(), nil)
}

func resolveDefaultPistonEndpoint() string {
	if endpoint := strings.TrimSpace(os.Getenv(pistonEndpointEnvVar)); endpoint != "" {
		return endpoint
	}
	return defaultPistonEndpoint
}

func NewExecutionManagerWithOptions(pistonEndpoint string, httpClient *http.Client) *ExecutionManager {
	manager := &ExecutionManager{
		pistonEndpoint: strings.TrimSpace(pistonEndpoint),
		httpClient:     httpClient,
		requestTimeout: defaultRequestTimeout,
		queueWait:      defaultQueueWaitTimeout,
		jobs:           make(chan ExecutionJob, defaultQueueSize),
	}
	if manager.pistonEndpoint == "" {
		manager.pistonEndpoint = defaultPistonEndpoint
	}
	if manager.httpClient == nil {
		manager.httpClient = &http.Client{}
	}
	manager.startWorkers()
	return manager
}

func (m *ExecutionManager) startWorkers() {
	for workerID := 0; workerID < defaultWorkerCount; workerID++ {
		m.workersWG.Add(1)
		go m.workerLoop()
	}
}

func (m *ExecutionManager) workerLoop() {
	defer m.workersWG.Done()
	for job := range m.jobs {
		response, err := m.executeAgainstPiston(job.Ctx, job.Request)
		result := ExecutionResult{
			Response: response,
			Err:      err,
		}
		// Buffered channel (size 1) allows safe reply even if caller is gone.
		select {
		case job.ResultCh <- result:
		default:
		}
	}
}

// Submit queues a job, waiting up to queueWait for capacity before failing.
func (m *ExecutionManager) Submit(ctx context.Context, request ExecutionRequest) (<-chan ExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	resultCh := make(chan ExecutionResult, 1)
	job := ExecutionJob{
		Ctx:      ctx,
		Request:  request,
		ResultCh: resultCh,
	}

	m.submitMu.RLock()
	if m.shutdownStarted {
		m.submitMu.RUnlock()
		return nil, ErrManagerShuttingDown
	}

	queueTimer := time.NewTimer(m.queueWait)
	defer queueTimer.Stop()

	select {
	case m.jobs <- job:
		m.submitMu.RUnlock()
		return resultCh, nil
	case <-queueTimer.C:
		m.submitMu.RUnlock()
		return nil, ErrServerBusy
	case <-ctx.Done():
		m.submitMu.RUnlock()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &ManagerError{
				StatusCode: http.StatusGatewayTimeout,
				Message:    timeoutErrorMessage,
				Err:        ctx.Err(),
			}
		}
		return nil, ctx.Err()
	}
}

// Execute submits a job and blocks until response or context cancellation.
func (m *ExecutionManager) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	resultCh, err := m.Submit(ctx, request)
	if err != nil {
		return ExecutionResponse{}, err
	}

	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ExecutionResponse{}, &ManagerError{
				StatusCode: http.StatusGatewayTimeout,
				Message:    timeoutErrorMessage,
				Err:        ctx.Err(),
			}
		}
		return ExecutionResponse{}, ctx.Err()
	case result := <-resultCh:
		return result.Response, result.Err
	}
}

func (m *ExecutionManager) executeAgainstPiston(
	jobCtx context.Context,
	request ExecutionRequest,
) (ExecutionResponse, error) {
	if jobCtx == nil {
		jobCtx = context.Background()
	}

	secureRequest, err := normalizePistonRequest(request)
	if err != nil {
		return ExecutionResponse{}, err
	}

	payload, err := json.Marshal(secureRequest)
	if err != nil {
		return ExecutionResponse{}, err
	}

	requestCtx, cancel := context.WithTimeout(jobCtx, m.requestTimeout)
	defer cancel()

	httpRequest, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		m.pistonEndpoint,
		bytes.NewReader(payload),
	)
	if err != nil {
		return ExecutionResponse{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := m.httpClient.Do(httpRequest)
	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return ExecutionResponse{}, &ManagerError{
				StatusCode: http.StatusGatewayTimeout,
				Message:    timeoutErrorMessage,
				Err:        ErrExecutionTimeout,
			}
		}
		if errors.Is(requestCtx.Err(), context.Canceled) {
			return ExecutionResponse{}, requestCtx.Err()
		}
		return ExecutionResponse{}, &ManagerError{
			StatusCode: http.StatusBadGateway,
			Message:    transportErrorMessage,
			Err:        err,
		}
	}
	defer httpResponse.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, maxPistonResponseBytes))
	if readErr != nil {
		return ExecutionResponse{}, &ManagerError{
			StatusCode: http.StatusBadGateway,
			Message:    responseReadErrorMessage,
			Err:        readErr,
		}
	}

	response := ExecutionResponse{
		StatusCode: httpResponse.StatusCode,
		Body:       responseBody,
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return response, &ManagerError{
			StatusCode: httpResponse.StatusCode,
			Message:    fmt.Sprintf("execution engine returned status %d", httpResponse.StatusCode),
		}
	}
	return response, nil
}

func normalizePistonRequest(request ExecutionRequest) (ExecutionRequest, error) {
	if len(request.Files) == 0 {
		return ExecutionRequest{}, &ManagerError{
			StatusCode: http.StatusBadRequest,
			Message:    "no source files were provided for execution",
		}
	}

	runtimeSpec, err := resolvePistonRuntime(request.Language)
	if err != nil {
		return ExecutionRequest{}, err
	}

	request.Language = runtimeSpec.Language
	request.Version = runtimeSpec.Version
	request.MainFile = strings.TrimSpace(request.MainFile)
	if isCFamilyRuntime(request.Language) {
		sourceFiles, dataFiles := splitCFamilyFiles(request.Files)
		if len(sourceFiles) == 0 {
			return ExecutionRequest{}, &ManagerError{
				StatusCode: http.StatusBadRequest,
				Message:    "no C/C++ source files were provided",
			}
		}
		if len(dataFiles) > 0 {
			injectorSource, injectorErr := buildCPPDataInjector(dataFiles)
			if injectorErr != nil {
				return ExecutionRequest{}, &ManagerError{
					StatusCode: http.StatusBadRequest,
					Message:    "failed to build C/C++ data injector",
					Err:        injectorErr,
				}
			}
			encodedInjector := base64.StdEncoding.EncodeToString([]byte(injectorSource))
			decodedInjector, decodeErr := base64.StdEncoding.DecodeString(encodedInjector)
			if decodeErr != nil {
				return ExecutionRequest{}, &ManagerError{
					StatusCode: http.StatusBadRequest,
					Message:    "failed to decode generated C/C++ injector",
					Err:        decodeErr,
				}
			}
			sourceFiles = append(sourceFiles, ExecutionFile{
				Name:    "_tora_injector.cpp",
				Content: string(decodedInjector),
			})
		}
		request.Files = sourceFiles
	}
	if isCompiledRuntime(request.Language) && request.MainFile != "" {
		// Keep support files (e.g. in.txt, data.json) available for runtime while ensuring
		// the declared entry file remains first for compilers that default to files[0].
		request.Files = mainFileFirst(request.Files, request.MainFile)
	}
	request.RunTimeout = defaultRunTimeoutMs
	request.CompileTimeout = defaultCompileTimeoutMs
	request.MemoryLimit = defaultMemoryLimitBytes
	request.CompileMemoryLimit = defaultMemoryLimitBytes
	request.RunMemoryLimit = defaultMemoryLimitBytes
	request.MaxProcessCount = defaultMaxProcessCount
	request.MaxOpenFiles = defaultMaxOpenFiles
	return request, nil
}

func isCompiledRuntime(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "cpp", "c", "java":
		return true
	default:
		return false
	}
}

func isCFamilyRuntime(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "cpp", "c++", "c":
		return true
	default:
		return false
	}
}

func splitCFamilyFiles(files []ExecutionFile) (sourceFiles []ExecutionFile, dataFiles []ExecutionFile) {
	sourceFiles = make([]ExecutionFile, 0, len(files))
	dataFiles = make([]ExecutionFile, 0, len(files))
	for _, file := range files {
		normalizedName := strings.TrimSpace(file.Name)
		lowerName := strings.ToLower(normalizedName)
		switch {
		case strings.HasSuffix(lowerName, ".c"),
			strings.HasSuffix(lowerName, ".cpp"),
			strings.HasSuffix(lowerName, ".h"),
			strings.HasSuffix(lowerName, ".hpp"):
			sourceFiles = append(sourceFiles, file)
		default:
			dataFiles = append(dataFiles, file)
		}
	}
	return sourceFiles, dataFiles
}

func buildCPPDataInjector(dataFiles []ExecutionFile) (string, error) {
	var builder strings.Builder
	builder.WriteString("#include <cstdio>\n")
	builder.WriteString("#include <filesystem>\n")
	builder.WriteString("#include <iostream>\n")
	builder.WriteString("#include <fstream>\n")
	builder.WriteString("#include <sstream>\n")
	builder.WriteString("#include <string>\n\n")

	// --- injector: write input files before main() runs ---
	builder.WriteString("struct ToraDataInjector {\n")
	builder.WriteString("  ToraDataInjector() {\n")
	for _, file := range dataFiles {
		decodedBytes, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(file.Content))
		if decodeErr != nil {
			decodedBytes = []byte(file.Content)
		}
		filePath := strings.TrimSpace(file.Name)
		parentDir := strings.TrimSpace(pathDir(filePath))

		builder.WriteString(fmt.Sprintf("    // Restore %s\n", filePath))
		builder.WriteString("    {\n") // own scope — prevents redeclaration across files
		if parentDir != "" && parentDir != "." {
			builder.WriteString(
				fmt.Sprintf("      std::filesystem::create_directories(\"%s\");\n",
					escapeCPPString(parentDir)),
			)
		}
		builder.WriteString(
			fmt.Sprintf("      FILE* f = fopen(\"%s\", \"wb\");\n", escapeCPPString(filePath)),
		)
		builder.WriteString("      if (f) {\n")
		if len(decodedBytes) > 0 {
			builder.WriteString(
				fmt.Sprintf("        static const unsigned char d[] = \"%s\";\n",
					bytesToCPPHexEscaped(decodedBytes)),
			)
			builder.WriteString(
				fmt.Sprintf("        fwrite(d, 1, %d, f);\n", len(decodedBytes)),
			)
		}
		builder.WriteString("        fclose(f);\n")
		builder.WriteString("      }\n")
		builder.WriteString("    }\n\n")
	}
	builder.WriteString("  }\n")
	builder.WriteString("};\n\n")
	builder.WriteString("static ToraDataInjector tora_data_injector_instance;\n\n")

	// --- extractor: read output files after main() returns ---
	builder.WriteString("struct ToraOutputExtractor {\n")
	builder.WriteString("  ~ToraOutputExtractor() {\n")
	for _, file := range dataFiles {
		filePath := strings.TrimSpace(file.Name)
		escaped := escapeCPPString(filePath)

		builder.WriteString(fmt.Sprintf("    // Extract %s\n", filePath))
		builder.WriteString("    {\n") // own scope — prevents redeclaration of 'in' across files
		builder.WriteString(fmt.Sprintf("      std::ifstream in(\"%s\", std::ios::binary);\n", escaped))
		builder.WriteString("      if (in) {\n")
		builder.WriteString("        std::ostringstream ss;\n")
		builder.WriteString("        ss << in.rdbuf();\n")
		builder.WriteString("        std::string content = ss.str();\n")
		builder.WriteString(fmt.Sprintf("        std::cout << \"===TORA_FILE_START:%s===\\n\";\n", escaped))
		builder.WriteString("        std::cout << content;\n")
		builder.WriteString(fmt.Sprintf("        std::cout << \"\\n===TORA_FILE_END:%s===\\n\";\n", escaped))
		builder.WriteString("      }\n")
		builder.WriteString("    }\n\n")
	}
	builder.WriteString("  }\n")
	builder.WriteString("};\n\n")
	builder.WriteString("static ToraOutputExtractor tora_output_extractor_instance;\n")
	return builder.String(), nil
}

// bytesToCPPHexEscaped encodes bytes as C string hex escapes (\xNN).
// A single string literal compiles orders of magnitude faster than
// a comma-separated array of thousands of integer literals.
func bytesToCPPHexEscaped(data []byte) string {
	var b strings.Builder
	b.Grow(len(data) * 4)
	for _, v := range data {
		fmt.Fprintf(&b, "\\x%02x", v)
	}
	return b.String()
}
func bytesToCPPArrayLiteral(data []byte) string {
	if len(data) == 0 {
		return "0x00"
	}
	parts := make([]string, 0, len(data))
	for _, b := range data {
		parts = append(parts, fmt.Sprintf("0x%02x", b))
	}
	return strings.Join(parts, ", ")
}

func pathDir(pathValue string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(pathValue), "\\", "/")
	if normalized == "" {
		return ""
	}
	lastSlash := strings.LastIndex(normalized, "/")
	if lastSlash <= 0 {
		return ""
	}
	return normalized[:lastSlash]
}

func escapeCPPString(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return escaped
}

func mainFileFirst(files []ExecutionFile, mainFile string) []ExecutionFile {
	mainIndex := -1
	for index := range files {
		if files[index].Name == mainFile {
			mainIndex = index
			break
		}
	}
	if mainIndex <= 0 {
		return files
	}
	reordered := make([]ExecutionFile, 0, len(files))
	reordered = append(reordered, files[mainIndex])
	reordered = append(reordered, files[:mainIndex]...)
	reordered = append(reordered, files[mainIndex+1:]...)
	return reordered
}

func resolvePistonRuntime(frontendLanguage string) (pistonRuntimeSpec, error) {
	normalizedLanguage := strings.ToLower(strings.TrimSpace(frontendLanguage))
	if normalizedLanguage == "" {
		return pistonRuntimeSpec{}, &ManagerError{
			StatusCode: http.StatusBadRequest,
			Message:    "language is required",
		}
	}

	runtimeSpec, ok := frontendToPistonRuntime[normalizedLanguage]
	if !ok {
		return pistonRuntimeSpec{}, &ManagerError{
			StatusCode: http.StatusBadRequest,
			Message:    fmt.Sprintf("unsupported execution language %q", frontendLanguage),
		}
	}
	return runtimeSpec, nil
}

// Shutdown drains workers gracefully. Do not submit jobs after shutdown.
func (m *ExecutionManager) Shutdown() {
	m.shutdownOnce.Do(func() {
		m.submitMu.Lock()
		m.shutdownStarted = true
		close(m.jobs)
		m.submitMu.Unlock()
		m.workersWG.Wait()
	})
}

func HTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var managerErr *ManagerError
	if errors.As(err, &managerErr) {
		if managerErr.StatusCode > 0 {
			return managerErr.StatusCode
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout
	default:
		return http.StatusInternalServerError
	}
}
