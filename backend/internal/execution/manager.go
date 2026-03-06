package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultPistonEndpoint    = "http://127.0.0.1:2000/api/v2/execute"
	defaultWorkerCount       = 20
	defaultQueueSize         = 20
	defaultRequestTimeout    = 10 * time.Second
	maxPistonResponseBytes   = 4 * 1024 * 1024
	defaultRunTimeoutMs      = 3000
	defaultCompileTimeoutMs  = 5000
	defaultMemoryLimitBytes  = 209715200
	defaultMaxProcessCount   = 64
	defaultMaxOpenFiles      = 64
	queueBusyErrorMessage    = "server busy"
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
	StatusCode: http.StatusTooManyRequests,
	Message:    queueBusyErrorMessage,
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

	jobs chan ExecutionJob

	shutdownOnce sync.Once
	workersWG    sync.WaitGroup
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
	return NewExecutionManagerWithOptions(defaultPistonEndpoint, nil)
}

func NewExecutionManagerWithOptions(pistonEndpoint string, httpClient *http.Client) *ExecutionManager {
	manager := &ExecutionManager{
		pistonEndpoint: strings.TrimSpace(pistonEndpoint),
		httpClient:     httpClient,
		requestTimeout: defaultRequestTimeout,
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

// Submit queues a job immediately or returns ErrServerBusy if the queue is full.
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

	select {
	case m.jobs <- job:
		return resultCh, nil
	default:
		return nil, ErrServerBusy
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
	request.RunTimeout = defaultRunTimeoutMs
	request.CompileTimeout = defaultCompileTimeoutMs
	request.MemoryLimit = defaultMemoryLimitBytes
	request.CompileMemoryLimit = defaultMemoryLimitBytes
	request.RunMemoryLimit = defaultMemoryLimitBytes
	request.MaxProcessCount = defaultMaxProcessCount
	request.MaxOpenFiles = defaultMaxOpenFiles
	return request, nil
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
		close(m.jobs)
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
