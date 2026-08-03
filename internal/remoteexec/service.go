package remoteexec

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TaskStatus represents the execution status.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// BatchExecRequest is a request to execute a command on multiple hosts.
type BatchExecRequest struct {
	Command     string         `json:"command"`
	Hosts       []HostExecInfo `json:"hosts"`
	Timeout     int            `json:"timeout,omitempty"`     // seconds
	Concurrency int            `json:"concurrency,omitempty"` // max parallel executions
}

// HostExecInfo contains host connection details for execution.
type HostExecInfo struct {
	Host         string `json:"host"`
	Port         int    `json:"port,omitempty"`
	Username     string `json:"username"`
	Password     string `json:"password,omitempty"`
	SSHKey       string `json:"ssh_key,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
}

// BatchExecResult is the result of a batch execution.
type BatchExecResult struct {
	TaskID    string           `json:"task_id"`
	Status    TaskStatus       `json:"status"`
	Results   []HostExecResult `json:"results"`
	StartedAt time.Time        `json:"started_at"`
	EndedAt   *time.Time       `json:"ended_at,omitempty"`
	Duration  time.Duration    `json:"duration"`
}

// HostExecResult is the result for a single host.
type HostExecResult struct {
	Host     string        `json:"host"`
	Port     int           `json:"port"`
	Output   string        `json:"output"`
	ExitCode int           `json:"exit_code"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
}

// StreamEvent is a streaming output event.
type StreamEvent struct {
	TaskID string `json:"task_id"`
	Host   string `json:"host"`
	Chunk  string `json:"chunk"`
	Time   int64  `json:"time"`
}

// Service manages batch remote execution.
type Service struct {
	executor *Executor
	tasks    sync.Map // taskID -> *BatchExecResult
}

// NewService creates a new remote execution service.
func NewService() *Service {
	return &Service{
		executor: NewExecutor(),
	}
}

// Execute runs a command on multiple hosts and returns results.
func (s *Service) Execute(ctx context.Context, req BatchExecRequest) *BatchExecResult {
	taskID := uuid.New().String()
	now := time.Now()

	result := &BatchExecResult{
		TaskID:    taskID,
		Status:    TaskStatusRunning,
		StartedAt: now,
		Results:   make([]HostExecResult, len(req.Hosts)),
	}
	s.tasks.Store(taskID, result)

	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}

	go func() {
		defer func() {
			endTime := time.Now()
			result.EndedAt = &endTime
			result.Duration = endTime.Sub(result.StartedAt)
			s.tasks.Store(taskID, result)
		}()

		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup

		for i, host := range req.Hosts {
			wg.Add(1)
			go func(idx int, h HostExecInfo) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				port := h.Port
				if port == 0 {
					port = 22
				}

				cred := Credential{
					User:     h.Username,
					Password: h.Password,
					Key:      h.SSHKey,
				}

				execReq := ExecRequest{
					Host:       h.Host,
					Port:       port,
					Credential: cred,
					Command:    req.Command,
					Timeout:    timeout,
				}

				slog.Info("executing remote command",
					"task_id", taskID,
					"host", h.Host,
					"port", port)

				execResult := s.executor.Execute(ctx, execReq)

				result.Results[idx] = HostExecResult{
					Host:     h.Host,
					Port:     port,
					Output:   execResult.Output,
					ExitCode: execResult.ExitCode,
					Duration: execResult.Duration,
				}
				if execResult.Error != nil {
					result.Results[idx].Error = execResult.Error.Error()
				}

				s.tasks.Store(taskID, result)
			}(i, host)
		}

		wg.Wait()

		// determine overall status
		allSuccess := true
		anyFailed := false
		for _, r := range result.Results {
			if r.ExitCode != 0 {
				allSuccess = false
			}
			if r.Error != "" {
				anyFailed = true
			}
		}

		if anyFailed {
			result.Status = TaskStatusFailed
		} else if allSuccess {
			result.Status = TaskStatusCompleted
		} else {
			result.Status = TaskStatusCompleted
		}
	}()

	return result
}

// GetTask returns a task result by ID.
func (s *Service) GetTask(taskID string) (*BatchExecResult, bool) {
	val, ok := s.tasks.Load(taskID)
	if !ok {
		return nil, false
	}
	return val.(*BatchExecResult), true
}

// ListTasks returns all tasks.
func (s *Service) ListTasks() []*BatchExecResult {
	var tasks []*BatchExecResult
	s.tasks.Range(func(key, value interface{}) bool {
		tasks = append(tasks, value.(*BatchExecResult))
		return true
	})
	return tasks
}

// ExecuteStreaming runs a command and streams output via callback.
func (s *Service) ExecuteStreaming(ctx context.Context, req BatchExecRequest, callback func(event StreamEvent)) *BatchExecResult {
	taskID := uuid.New().String()
	now := time.Now()

	result := &BatchExecResult{
		TaskID:    taskID,
		Status:    TaskStatusRunning,
		StartedAt: now,
		Results:   make([]HostExecResult, len(req.Hosts)),
	}
	s.tasks.Store(taskID, result)

	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}

	go func() {
		defer func() {
			endTime := time.Now()
			result.EndedAt = &endTime
			result.Duration = endTime.Sub(result.StartedAt)
			s.tasks.Store(taskID, result)
		}()

		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup

		for i, host := range req.Hosts {
			wg.Add(1)
			go func(idx int, h HostExecInfo) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				port := h.Port
				if port == 0 {
					port = 22
				}

				cred := Credential{
					User:     h.Username,
					Password: h.Password,
					Key:      h.SSHKey,
				}

				execReq := ExecRequest{
					Host:       h.Host,
					Port:       port,
					Credential: cred,
					Command:    req.Command,
					Timeout:    timeout,
				}

				streamCallback := func(host string, chunk []byte) {
					if callback != nil {
						callback(StreamEvent{
							TaskID: taskID,
							Host:   host,
							Chunk:  string(chunk),
							Time:   time.Now().UnixMilli(),
						})
					}
				}

				execResult := s.executor.ExecuteStream(ctx, execReq, streamCallback)

				result.Results[idx] = HostExecResult{
					Host:     h.Host,
					Port:     port,
					Output:   execResult.Output,
					ExitCode: execResult.ExitCode,
					Duration: execResult.Duration,
				}
				if execResult.Error != nil {
					result.Results[idx].Error = execResult.Error.Error()
				}

				s.tasks.Store(taskID, result)
			}(i, host)
		}

		wg.Wait()

		anyFailed := false
		for _, r := range result.Results {
			if r.Error != "" {
				anyFailed = true
				break
			}
		}
		if anyFailed {
			result.Status = TaskStatusFailed
		} else {
			result.Status = TaskStatusCompleted
		}
	}()

	return result
}

// TestSSH tests SSH connectivity to a host.
func (s *Service) TestSSH(host string, port int, username, password, sshKey string, timeout time.Duration) (bool, string) {
	cred := Credential{
		User:     username,
		Password: password,
		Key:      sshKey,
	}
	return s.executor.TestConnectivity(host, port, cred, timeout)
}

// Close shuts down the service.
func (s *Service) Close() {
	s.executor.Close()
}

// ExecuteSingle runs a command on a single host and returns the result.
func (s *Service) ExecuteSingle(ctx context.Context, req ExecRequest) ExecResult {
	return s.executor.Execute(ctx, req)
}

// unused import guard
var _ = fmt.Sprintf
