package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ScriptExecutor executes a script by ID and returns its output, exit code and error.
// It may be nil, in which case script nodes degrade gracefully with an error message.
type ScriptExecutor interface {
	Execute(scriptID string, params map[string]string, timeout int, runAs string, agentID string, tenantID string) (output string, exitCode int, err error)
}

type Store interface {
	Create(ctx context.Context, w *Workflow) error
	Get(ctx context.Context, id, tenantID string) (*Workflow, error)
	List(ctx context.Context, tenantID string) ([]*Workflow, error)
	Update(ctx context.Context, w *Workflow) error
	Delete(ctx context.Context, id, tenantID string) error
	CreateExecution(ctx context.Context, exec *WorkflowExecution) error
	ListExecutions(ctx context.Context, workflowID, tenantID string) ([]*WorkflowExecution, error)
	GetExecution(ctx context.Context, id, tenantID string) (*WorkflowExecution, error)
	UpdateExecution(ctx context.Context, exec *WorkflowExecution) error
	CancelExecution(ctx context.Context, id, tenantID string) error
	CreateApproval(ctx context.Context, a *StepApproval) error
	ListApprovals(ctx context.Context, tenantID string) ([]*StepApproval, error)
	GetApproval(ctx context.Context, id, tenantID string) (*StepApproval, error)
	UpdateApproval(ctx context.Context, a *StepApproval) error
}

type MemoryStore struct {
	mu         sync.RWMutex
	workflows  map[string]*Workflow
	executions map[string]*WorkflowExecution
	approvals  map[string]*StepApproval
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workflows:  make(map[string]*Workflow),
		executions: make(map[string]*WorkflowExecution),
		approvals:  make(map[string]*StepApproval),
	}
}

func (s *MemoryStore) Create(ctx context.Context, w *Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.ID = uuid.New().String()
	now := time.Now()
	w.CreatedAt = now
	w.UpdatedAt = now
	s.workflows[w.ID] = w
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.workflows[id]
	if !ok || w.TenantID != tenantID {
		return nil, nil
	}
	return w, nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID string) ([]*Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Workflow
	for _, w := range s.workflows {
		if w.TenantID == tenantID {
			result = append(result, w)
		}
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, w *Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.workflows[w.ID]
	if !ok || existing.TenantID != w.TenantID {
		return nil
	}
	w.UpdatedAt = time.Now()
	s.workflows[w.ID] = w
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workflows[id]
	if !ok || w.TenantID != tenantID {
		return nil
	}
	delete(s.workflows, w.ID)
	return nil
}

func (s *MemoryStore) CreateExecution(ctx context.Context, exec *WorkflowExecution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	exec.ID = uuid.New().String()
	exec.CreatedAt = time.Now()
	if exec.Status == "" {
		exec.Status = ExecStatusPending
	}
	s.executions[exec.ID] = exec
	return nil
}

func (s *MemoryStore) ListExecutions(ctx context.Context, workflowID, tenantID string) ([]*WorkflowExecution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*WorkflowExecution
	for _, e := range s.executions {
		if e.TenantID == tenantID && (workflowID == "" || e.WorkflowID == workflowID) {
			result = append(result, e)
		}
	}
	return result, nil
}

func (s *MemoryStore) GetExecution(ctx context.Context, id, tenantID string) (*WorkflowExecution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.executions[id]
	if !ok || e.TenantID != tenantID {
		return nil, nil
	}
	return e, nil
}

func (s *MemoryStore) UpdateExecution(ctx context.Context, exec *WorkflowExecution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.executions[exec.ID]
	if !ok || existing.TenantID != exec.TenantID {
		return errors.New("execution not found")
	}
	s.executions[exec.ID] = exec
	return nil
}

func (s *MemoryStore) CreateApproval(ctx context.Context, a *StepApproval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = uuid.New().String()
	a.CreatedAt = time.Now()
	if a.Status == "" {
		a.Status = ApprovalPending
	}
	s.approvals[a.ID] = a
	return nil
}

func (s *MemoryStore) ListApprovals(ctx context.Context, tenantID string) ([]*StepApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*StepApproval
	for _, a := range s.approvals {
		if a.TenantID == tenantID {
			result = append(result, a)
		}
	}
	return result, nil
}

func (s *MemoryStore) GetApproval(ctx context.Context, id, tenantID string) (*StepApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.approvals[id]
	if !ok || a.TenantID != tenantID {
		return nil, nil
	}
	return a, nil
}

func (s *MemoryStore) UpdateApproval(ctx context.Context, a *StepApproval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.approvals[a.ID]
	if !ok || existing.TenantID != a.TenantID {
		return errors.New("approval not found")
	}
	s.approvals[a.ID] = a
	return nil
}

func (s *MemoryStore) CancelExecution(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	exec, ok := s.executions[id]
	if !ok || exec.TenantID != tenantID {
		return nil
	}
	exec.Status = ExecStatusCancelled
	now := time.Now()
	exec.FinishedAt = &now
	return nil
}

type Service struct {
	store      Store
	scriptExec ScriptExecutor
}

func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = NewPGStore(db)
	}
	return &Service{store: st}
}

// SetScriptExecutor injects a ScriptExecutor used by script/parallel steps.
// If never called, scriptExec is nil and script nodes return a degraded error.
func (s *Service) SetScriptExecutor(exec ScriptExecutor) {
	s.scriptExec = exec
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/workflows", s.handleCreate)
	mux.HandleFunc("GET /api/v1/workflows", s.handleList)
	mux.HandleFunc("GET /api/v1/workflows/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/workflows/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/workflows/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/v1/workflows/{id}/execute", s.handleExecute)
	mux.HandleFunc("GET /api/v1/workflows/{id}/executions", s.handleListExecutions)
	// "approvals" is a literal segment; Go 1.22+ serves it before {id} wildcards.
	mux.HandleFunc("GET /api/v1/workflows/approvals", s.handleListApprovals)
	mux.HandleFunc("POST /api/v1/workflows/approvals/{id}/approve", s.handleApprove)
	mux.HandleFunc("POST /api/v1/workflows/approvals/{id}/reject", s.handleReject)
	mux.HandleFunc("POST /api/v1/workflows/executions/{exec_id}/cancel", s.handleCancelExecution)
	// 模板
	mux.HandleFunc("GET /api/v1/workflows/templates/list", s.handleListTemplates)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var wf Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	wf.TenantID = tenantFromCtx(r.Context())
	if err := s.store.Create(r.Context(), &wf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, wf)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	workflows, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if workflows == nil {
		workflows = []*Workflow{}
	}
	writeJSON(w, http.StatusOK, workflows)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	wf, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || wf == nil {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}
	var wf Workflow
	json.NewDecoder(r.Body).Decode(&wf)
	wf.ID = existing.ID
	wf.TenantID = existing.TenantID
	wf.CreatedAt = existing.CreatedAt
	s.store.Update(r.Context(), &wf)
	writeJSON(w, http.StatusOK, wf)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	s.store.Delete(r.Context(), id, tenantID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleExecute creates a running execution and drives it through the steps.
// On an approval node it pauses (status=waiting) and returns the execution.
func (s *Service) handleExecute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	wf, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || wf == nil {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}
	var req struct {
		AgentIDs []string       `json:"agent_ids"`
		Inputs   map[string]any `json:"inputs,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	now := time.Now()
	exec := &WorkflowExecution{
		WorkflowID:  wf.ID,
		TenantID:    tenantID,
		AgentIDs:    req.AgentIDs,
		Inputs:      req.Inputs,
		Status:      ExecStatusRunning,
		StartedAt:   &now,
		StepResults: make(map[string]StepResult),
		Context:     make(map[string]any),
	}
	if req.Inputs != nil {
		exec.Context["inputs"] = req.Inputs
	}
	if err := s.store.CreateExecution(r.Context(), exec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	exec.Status = ExecStatusRunning

	startStepID := ""
	if len(wf.Steps) > 0 {
		startStepID = wf.Steps[0].ID
	}
	// 异步驱动执行：HTTP 立即返回 202，执行在后台 goroutine 进行，
	// 前端通过 GET /workflows/{id}/executions 轮询状态。
	// 使用 WithoutCancel 派生，避免客户端断开导致后台执行被取消。
	go s.runFromStep(context.WithoutCancel(r.Context()), exec, wf, startStepID)
	writeJSON(w, http.StatusAccepted, exec)
}

// runFromStep drives execution starting at startStepID, following OnSuccess/OnFailure.
// It pauses (returning nil) when an approval node is hit, persisting the pending state.
func (s *Service) runFromStep(ctx context.Context, exec *WorkflowExecution, wf *Workflow, startStepID string) error {
	const maxSteps = 1000
	// 工作流全局超时
	if wf.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(wf.Timeout)*time.Second)
		defer cancel()
	}
	visited := make(map[string]int)
	currentStepID := startStepID
	for currentStepID != "" {
		if len(visited) >= maxSteps {
			exec.Status = ExecStatusFailed
			finished := time.Now()
			exec.FinishedAt = &finished
			exec.PendingStepID = ""
			s.store.UpdateExecution(ctx, exec)
			return errors.New("workflow exceeded max steps (possible cycle)")
		}
		step := findStep(wf, currentStepID)
		if step == nil {
			break
		}
		visited[currentStepID]++
		if visited[currentStepID] > 1 {
			exec.Status = ExecStatusFailed
			finished := time.Now()
			exec.FinishedAt = &finished
			exec.PendingStepID = ""
			s.store.UpdateExecution(ctx, exec)
			return fmt.Errorf("workflow cycle detected at step %s", currentStepID)
		}
		start := time.Now()
		next, pending, err := s.executeStep(ctx, exec, wf, step)
		duration := time.Since(start).Milliseconds()

		result := StepResult{
			StepID:   step.ID,
			StepName: step.Name,
			Duration: duration,
		}
		if out, ok := exec.Context[step.ID].(map[string]any); ok {
			result.Output = out
		}
		switch {
		case pending:
			result.Status = ExecStatusWaiting
		case err != nil:
			result.Status = ExecStatusFailed
			result.Error = err.Error()
		default:
			result.Status = ExecStatusSucceeded
		}
		exec.StepResults[step.ID] = result
		s.store.UpdateExecution(ctx, exec)

		if pending {
			exec.PendingStepID = step.ID
			exec.Status = ExecStatusWaiting
			s.store.UpdateExecution(ctx, exec)
			return nil
		}
		if err != nil {
			exec.Status = ExecStatusFailed
			finished := time.Now()
			exec.FinishedAt = &finished
			s.store.UpdateExecution(ctx, exec)
			return err
		}
		currentStepID = next
	}
	exec.Status = ExecStatusSucceeded
	finished := time.Now()
	exec.FinishedAt = &finished
	exec.PendingStepID = ""
	s.store.UpdateExecution(ctx, exec)
	return nil
}

// executeStep runs a single step and returns the next step ID, whether the
// execution is paused pending approval, and any error.
func (s *Service) executeStep(ctx context.Context, exec *WorkflowExecution, wf *Workflow, step *WorkflowStep) (nextStepID string, pending bool, err error) {
	// 若本次执行指定了目标 Agent，脚本步骤派发到该 Agent 所在主机执行。
	agentID := ""
	if len(exec.AgentIDs) > 0 {
		agentID = exec.AgentIDs[0]
	}
	switch step.Type {
	case StepTypeScript:
		if e := s.runScriptStep(step, exec.Context, agentID, exec.TenantID); e != nil {
			if step.OnFailure != "" {
				return step.OnFailure, false, nil
			}
			return "", false, e
		}
		return step.OnSuccess, false, nil

	case StepTypeCondition:
		if evalCondition(step.Condition, exec.Context) {
			return step.OnSuccess, false, nil
		}
		return step.OnFailure, false, nil

	case StepTypeParallel:
		if e := s.runParallelSteps(step, exec.Context, agentID, exec.TenantID); e != nil {
			if step.OnFailure != "" {
				return step.OnFailure, false, nil
			}
			return "", false, e
		}
		return step.OnSuccess, false, nil

	case StepTypeWait:
		if step.WaitSeconds > 0 {
			timer := time.NewTimer(time.Duration(step.WaitSeconds) * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return "", false, ctx.Err()
			}
		}
		return step.OnSuccess, false, nil

	case StepTypeApproval:
		a := &StepApproval{
			TenantID:    exec.TenantID,
			ExecutionID: exec.ID,
			StepID:      step.ID,
			StepName:    step.Name,
			Approvers:   step.Approvers,
			Message:     step.ApprovalMsg,
			Status:      ApprovalPending,
		}
		if e := s.store.CreateApproval(ctx, a); e != nil {
			return "", false, e
		}
		// surface the approval to the caller via context so it appears in the response
		exec.Context[step.ID] = map[string]any{"approval_id": a.ID, "status": string(a.Status)}
		return "", true, nil
	}
	return step.OnSuccess, false, nil
}

// runScriptStep invokes the ScriptExecutor and stores parsed output under the step ID.
func (s *Service) runScriptStep(step *WorkflowStep, ctx map[string]any, agentID string, tenantID string) error {
	out, err := s.executeScript(*step, ctx, agentID, tenantID)
	if err != nil {
		return err
	}
	ctx[step.ID] = out
	return nil
}

// executeScript runs a single script node against the given context, returning
// its parsed output. The context is only read (template resolution), never written.
func (s *Service) executeScript(step WorkflowStep, ctx map[string]any, agentID string, tenantID string) (map[string]any, error) {
	if s.scriptExec == nil {
		return nil, errors.New("script executor not configured")
	}
	params := resolveParams(step.Inputs, ctx)
	runAs := ""
	if v, ok := step.Inputs["run_as"].(string); ok {
		runAs = v
	}
	output, exitCode, err := s.scriptExec.Execute(step.ScriptID, params, step.Timeout, runAs, agentID, tenantID)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("script %s exited with code %d: %s", step.ScriptID, exitCode, output)
	}
	return parseOutput(output), nil
}

// runParallelSteps executes a step's ParallelSteps concurrently and merges their
// outputs into the context after all complete. Per-step results are collected
// locally to avoid concurrent writes to the shared context.
func (s *Service) runParallelSteps(step *WorkflowStep, ctx map[string]any, agentID string, tenantID string) error {
	type presult struct {
		id  string
		out map[string]any
		err error
	}
	results := make([]presult, len(step.ParallelSteps))
	var wg sync.WaitGroup
	for i := range step.ParallelSteps {
		wg.Add(1)
		go func(idx int, ps WorkflowStep) {
			defer wg.Done()
			out, err := s.executeScript(ps, ctx, agentID, tenantID)
			results[idx] = presult{id: ps.ID, out: out, err: err}
		}(i, step.ParallelSteps[i])
	}
	wg.Wait()
	for _, r := range results {
		if r.err != nil {
			return r.err
		}
		if r.id != "" {
			ctx[r.id] = r.out
		}
	}
	return nil
}

func (s *Service) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	approvals, err := s.store.ListApprovals(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if approvals == nil {
		approvals = []*StepApproval{}
	}
	writeJSON(w, http.StatusOK, approvals)
}

func (s *Service) handleApprove(w http.ResponseWriter, r *http.Request) {
	s.decideApproval(w, r, ApprovalApproved)
}

func (s *Service) handleReject(w http.ResponseWriter, r *http.Request) {
	s.decideApproval(w, r, ApprovalRejected)
}

// decideApproval records the decision and, on approval, resumes the execution;
// on rejection it fails the execution.
func (s *Service) decideApproval(w http.ResponseWriter, r *http.Request, status ApprovalStatus) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	approval, err := s.store.GetApproval(r.Context(), id, tenantID)
	if err != nil || approval == nil {
		writeError(w, http.StatusNotFound, "approval not found")
		return
	}
	if approval.Status != ApprovalPending {
		writeError(w, http.StatusBadRequest, "approval already decided")
		return
	}
	var req struct {
		DecidedBy string `json:"decided_by"`
		Comment    string `json:"comment"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	now := time.Now()
	approval.Status = status
	approval.DecidedBy = req.DecidedBy
	approval.Comment = req.Comment
	approval.DecidedAt = &now
	if err := s.store.UpdateApproval(r.Context(), approval); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	exec, err := s.store.GetExecution(r.Context(), approval.ExecutionID, tenantID)
	if err != nil || exec == nil {
		writeJSON(w, http.StatusOK, approval)
		return
	}
	wf, _ := s.store.Get(r.Context(), exec.WorkflowID, tenantID)
	if wf == nil {
		writeJSON(w, http.StatusOK, approval)
		return
	}

	if status == ApprovalApproved {
		exec.StepResults[approval.StepID] = StepResult{
			StepID:   approval.StepID,
			StepName: approval.StepName,
			Status:   ExecStatusSucceeded,
		}
		nextStepID := ""
		if step := findStep(wf, approval.StepID); step != nil {
			nextStepID = step.OnSuccess
		}
		exec.Status = ExecStatusRunning
		exec.PendingStepID = ""
		s.store.UpdateExecution(r.Context(), exec)
		// 异步续跑：审批后的步骤在后台 goroutine 执行，避免长流程阻塞审批请求。
		go s.runFromStep(context.WithoutCancel(r.Context()), exec, wf, nextStepID)
	} else {
		exec.Status = ExecStatusFailed
		finished := time.Now()
		exec.FinishedAt = &finished
		exec.PendingStepID = ""
		exec.StepResults[approval.StepID] = StepResult{
			StepID:   approval.StepID,
			StepName: approval.StepName,
			Status:   ExecStatusFailed,
			Error:    "approval rejected",
		}
		s.store.UpdateExecution(r.Context(), exec)
	}
	writeJSON(w, http.StatusOK, approval)
}

func (s *Service) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	execs, err := s.store.ListExecutions(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if execs == nil {
		execs = []*WorkflowExecution{}
	}
	writeJSON(w, http.StatusOK, execs)
}

// findStep returns a pointer to the top-level step with the given ID.
func findStep(wf *Workflow, stepID string) *WorkflowStep {
	for i := range wf.Steps {
		if wf.Steps[i].ID == stepID {
			return &wf.Steps[i]
		}
	}
	return nil
}

var templateVarRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

// resolveValue replaces ${step_id.field} references in s using values from ctx.
func resolveValue(s string, ctx map[string]any) string {
	return templateVarRegex.ReplaceAllStringFunc(s, func(match string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			return match
		}
		v, ok := lookupField(ctx[parts[0]], parts[1])
		if !ok {
			return match
		}
		return fmt.Sprintf("%v", v)
	})
}

// lookupField fetches a (possibly dotted-nested) field from a context value.
func lookupField(v any, field string) (any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	if strings.Contains(field, ".") {
		parts := strings.SplitN(field, ".", 2)
		nested, ok := m[parts[0]]
		if !ok {
			return nil, false
		}
		return lookupField(nested, parts[1])
	}
	val, ok := m[field]
	return val, ok
}

// resolveParams converts step inputs to string params, applying ${...} templates
// against the execution context so later steps can reference earlier outputs.
func resolveParams(inputs map[string]any, ctx map[string]any) map[string]string {
	params := make(map[string]string)
	for k, v := range inputs {
		if k == "run_as" {
			continue
		}
		switch val := v.(type) {
		case string:
			params[k] = resolveValue(val, ctx)
		default:
			params[k] = resolveValue(fmt.Sprintf("%v", v), ctx)
		}
	}
	return params
}

// parseOutput parses a script's stdout as JSON; if it isn't JSON the raw output
// is wrapped under an "output" key so downstream steps can still reference it.
func parseOutput(output string) map[string]any {
	output = strings.TrimSpace(output)
	if output == "" {
		return map[string]any{"output": ""}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(output), &m); err == nil {
		return m
	}
	return map[string]any{"output": output}
}

// evalCondition evaluates a simplified condition expression against the context.
// Supported forms:
//   - `${step.field} == "value"`
//   - `${step.field} != "value"`
//   - `${step.field}` (truthy if resolves to a non-empty, changed value)
func evalCondition(cond string, ctx map[string]any) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return true
	}
	// 解析 && / || 组合条件 (优先级: && 高于 ||)
	if strings.Contains(cond, "||") {
		parts := strings.SplitN(cond, "||", 2)
		if evalCondition(parts[0], ctx) {
			return true
		}
		return evalCondition(parts[1], ctx)
	}
	if strings.Contains(cond, "&&") {
		parts := strings.SplitN(cond, "&&", 2)
		return evalCondition(parts[0], ctx) && evalCondition(parts[1], ctx)
	}
	// 单条件匹配
	for _, op := range []string{">=", "<=", "!=", "=~", ">", "<", "=="} {
		if strings.Contains(cond, op) {
			parts := strings.SplitN(cond, op, 2)
			lhs := resolveValue(strings.TrimSpace(parts[0]), ctx)
			rhs := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			switch op {
			case "==": return lhs == rhs
			case "!=": return lhs != rhs
			case "=~":
				matched, _ := regexp.MatchString(rhs, lhs)
				return matched
			case ">", ">=", "<", "<=":
				return compareNumbers(lhs, rhs, op)
			}
		}
	}
	resolved := resolveValue(cond, ctx)
	return resolved != "" && resolved != cond
}

func compareNumbers(a, b, op string) bool {
	fa, errA := strconv.ParseFloat(a, 64)
	fb, errB := strconv.ParseFloat(b, 64)
	if errA != nil || errB != nil {
		return false
	}
	switch op {
	case ">": return fa > fb
	case ">=": return fa >= fb
	case "<": return fa < fb
	case "<=": return fa <= fb
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Trigger 同步执行指定工作流，返回最终 execution。供 scheduler 定时触发复用，
// 调用方（API handler / scheduler）自行决定是否异步驱动。
func (s *Service) Trigger(ctx context.Context, tenantID, workflowID string, agentIDs []string, inputs map[string]any) (*WorkflowExecution, error) {
	wf, err := s.store.Get(ctx, workflowID, tenantID)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}
	now := time.Now()
	exec := &WorkflowExecution{
		WorkflowID:  wf.ID,
		TenantID:    tenantID,
		AgentIDs:    agentIDs,
		Inputs:      inputs,
		Status:      ExecStatusRunning,
		StartedAt:   &now,
		StepResults: make(map[string]StepResult),
		Context:     make(map[string]any),
	}
	if inputs != nil {
		exec.Context["inputs"] = inputs
	}
	if err := s.store.CreateExecution(ctx, exec); err != nil {
		return nil, err
	}
	exec.Status = ExecStatusRunning
	startStepID := ""
	if len(wf.Steps) > 0 {
		startStepID = wf.Steps[0].ID
	}
	s.runFromStep(ctx, exec, wf, startStepID)
	return exec, nil
}

func (s *Service) handleCancelExecution(w http.ResponseWriter, r *http.Request) {
	execID := r.PathValue("exec_id")
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" { tenantID = "default" }
	exec, err := s.store.GetExecution(r.Context(), execID, tenantID)
	if err != nil || exec == nil {
		writeError(w, http.StatusNotFound, "execution not found"); return
	}
	if exec.Status != "running" && exec.Status != "waiting" {
		writeError(w, http.StatusBadRequest, "cannot cancel execution in status: "+string(exec.Status)); return
	}
	if err := s.store.CancelExecution(r.Context(), execID, tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error()); return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Service) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	templates := []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Steps       []WorkflowStep `json:"steps"`
	}{
		{ID: "tmpl-ci", Name: "CI 部署流水线", Description: "拉取代码→构建→测试→部署",
			Steps: []WorkflowStep{
				{ID: "git", Name: "拉取代码", Type: "script", ScriptID: "", Inputs: map[string]any{"branch": "main"}},
				{ID: "build", Name: "构建镜像", Type: "script", ScriptID: "", OnSuccess: "test"},
				{ID: "test", Name: "运行测试", Type: "script", ScriptID: "", OnSuccess: "deploy", OnFailure: "rollback"},
				{ID: "deploy", Name: "部署上线", Type: "script", ScriptID: ""},
				{ID: "rollback", Name: "回滚", Type: "script", ScriptID: ""},
			},
		},
		{ID: "tmpl-approval", Name: "带审批的变更发布",
			Description: "提交变更→审批→执行→验证",
			Steps: []WorkflowStep{
				{ID: "submit", Name: "提交变更", Type: "script", ScriptID: "", OnSuccess: "approve"},
				{ID: "approve", Name: "人工审批", Type: "approval", Approvers: []string{}, ApprovalMsg: "请审批本次变更"},
				{ID: "execute", Name: "执行变更", Type: "script", ScriptID: "", OnSuccess: "verify"},
				{ID: "verify", Name: "验证结果", Type: "script", ScriptID: ""},
			},
		},
		{ID: "tmpl-batch", Name: "批量主机巡检", Description: "并行检查多台主机→汇总报告",
			Steps: []WorkflowStep{
				{ID: "parallel", Name: "并行巡检", Type: "parallel", ParallelSteps: []WorkflowStep{
					{ID: "check1", Name: "目标主机-1", Type: "script", ScriptID: ""},
					{ID: "check2", Name: "目标主机-2", Type: "script", ScriptID: ""},
				}},
			},
		},
		{ID: "tmpl-notify", Name: "故障自愈流程", Description: "检测故障→自动修复→通知",
			Steps: []WorkflowStep{
				{ID: "detect", Name: "故障检测", Type: "script", ScriptID: "", OnSuccess: "fix", OnFailure: "notify"},
				{ID: "fix", Name: "自动修复", Type: "script", ScriptID: "", OnSuccess: "verify", OnFailure: "notify"},
				{ID: "verify", Name: "修复验证", Type: "wait", WaitSeconds: 30, OnSuccess: "notify"},
				{ID: "notify", Name: "通知", Type: "script", ScriptID: ""},
			},
		},
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}
