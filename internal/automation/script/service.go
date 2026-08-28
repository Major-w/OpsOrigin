package script

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/opsorigin/core/pkg/auth"

	"fmt"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/pkg/validate"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

// Store defines the persistence interface for scripts, their version history
// and execution history.
type Store interface {
	// 脚本 CRUD
	Create(ctx context.Context, s *Script) error
	Get(ctx context.Context, id, tenantID string) (*Script, error)
	List(ctx context.Context, tenantID string) ([]*Script, error)
	Update(ctx context.Context, s *Script) error
	Delete(ctx context.Context, id, tenantID string) error
	// 版本管理
	SaveVersion(ctx context.Context, v *ScriptVersion) error
	ListVersions(ctx context.Context, scriptID, tenantID string) ([]*ScriptVersion, error)
	GetVersion(ctx context.Context, scriptID, tenantID string, version int) (*ScriptVersion, error)
	// 执行历史
	SaveExecution(ctx context.Context, result *ExecutionResult) error
	ListExecutions(ctx context.Context, scriptID, tenantID string) ([]*ExecutionResult, error)
}

// MemoryStore is an in-memory, concurrency-safe implementation of Store.
type MemoryStore struct {
	mu         sync.RWMutex
	scripts    map[string]*Script
	versions   map[string][]*ScriptVersion   // scriptID -> versions (ascending by Version)
	executions map[string][]*ExecutionResult // scriptID -> executions (ascending by StartedAt)
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		scripts:    make(map[string]*Script),
		versions:   make(map[string][]*ScriptVersion),
		executions: make(map[string][]*ExecutionResult),
	}
}

func (s *MemoryStore) Create(ctx context.Context, sc *Script) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc.ID = uuid.New().String()
	sc.Version = 1
	now := time.Now()
	sc.CreatedAt = now
	sc.UpdatedAt = now
	s.scripts[sc.ID] = sc
	// 保存初始版本快照
	s.versions[sc.ID] = append(s.versions[sc.ID], snapshot(sc, now, changeNoteFromCtx(ctx), userFromCtx(ctx)))
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*Script, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.scripts[id]
	if !ok || sc.TenantID != tenantID {
		return nil, nil
	}
	return sc, nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID string) ([]*Script, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Script
	for _, sc := range s.scripts {
		if sc.TenantID == tenantID {
			result = append(result, sc)
		}
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, sc *Script) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.scripts[sc.ID]
	if !ok || existing.TenantID != sc.TenantID {
		return nil
	}
	sc.Version = existing.Version + 1
	sc.CreatedAt = existing.CreatedAt
	sc.UpdatedAt = time.Now()
	s.scripts[sc.ID] = sc
	// 保存新版本快照
	s.versions[sc.ID] = append(s.versions[sc.ID], snapshot(sc, sc.UpdatedAt, changeNoteFromCtx(ctx), userFromCtx(ctx)))
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.scripts[id]
	if !ok || sc.TenantID != tenantID {
		return nil
	}
	delete(s.scripts, sc.ID)
	delete(s.versions, sc.ID)
	delete(s.executions, sc.ID)
	return nil
}

func (s *MemoryStore) SaveVersion(ctx context.Context, v *ScriptVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.ID == "" {
		v.ID = uuid.New().String()
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	s.versions[v.ScriptID] = append(s.versions[v.ScriptID], v)
	return nil
}

// ListVersions returns the versions for a script in descending order by Version.
func (s *MemoryStore) ListVersions(ctx context.Context, scriptID, tenantID string) ([]*ScriptVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.versions[scriptID]
	result := make([]*ScriptVersion, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].TenantID == tenantID {
			result = append(result, all[i])
		}
	}
	return result, nil
}

func (s *MemoryStore) GetVersion(ctx context.Context, scriptID, tenantID string, version int) (*ScriptVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.versions[scriptID] {
		if v.TenantID == tenantID && v.Version == version {
			return v, nil
		}
	}
	return nil, nil
}

func (s *MemoryStore) SaveExecution(ctx context.Context, result *ExecutionResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if result.ID == "" {
		result.ID = uuid.New().String()
	}
	if result.StartedAt.IsZero() {
		result.StartedAt = time.Now()
	}
	s.executions[result.ScriptID] = append(s.executions[result.ScriptID], result)
	return nil
}

// ListExecutions returns the execution history for a script in descending order by StartedAt.
func (s *MemoryStore) ListExecutions(ctx context.Context, scriptID, tenantID string) ([]*ExecutionResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.executions[scriptID]
	result := make([]*ExecutionResult, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].TenantID == tenantID {
			result = append(result, all[i])
		}
	}
	return result, nil
}

// snapshot builds an immutable ScriptVersion from a Script.
func snapshot(sc *Script, at time.Time, changeNote, createdBy string) *ScriptVersion {
	return &ScriptVersion{
		ID:             uuid.New().String(),
		ScriptID:       sc.ID,
		TenantID:       sc.TenantID,
		Version:        sc.Version,
		Name:           sc.Name,
		Type:           sc.Type,
		Content:        sc.Content,
		InputParams:    sc.InputParams,
		OutputParams:   sc.OutputParams,
		Timeout:        sc.Timeout,
		RunAs:          sc.RunAs,
		ChangeNote:     changeNote,
		CreatedBy:      createdBy,
		CreatedAt:      at,
		Public:         sc.Public,
		ApprovalStatus: sc.ApprovalStatus,
	}
}

// AgentRunner 把脚本下发到被管主机上的 Agent 执行。
type AgentRunner interface {
	RunScriptOnAgent(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string) (output string, exitCode int, err error)
	RunScriptOnAgentWithType(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string, scriptType string) (output string, exitCode int, err error)
}

// SandboxRunner 是 AgentRunner 的可选扩展:把脚本以"沙箱模式"下发到带沙箱标签
// 的真实 Agent,由其在目标主机上创建真实隔离环境(临时工作目录/净化环境变量/
// Python venv)执行。agentmgr.Service 实现了该接口。
type SandboxRunner interface {
	RunScriptOnAgentSandboxed(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, scriptType string, requirements []string) (output string, exitCode int, err error)
	// PickSandboxAgentID 在未显式指定 Agent 时,挑选一台在线的沙箱 Agent。
	PickSandboxAgentID(ctx context.Context, tenantID, preferID string) (string, error)
}

// Service exposes the script management HTTP API.
type Service struct {
	store        Store
	agentRunner  AgentRunner
	cmdWhitelist *command.Whitelist
	dangerous    *command.DangerousPolicy
}

func (s *Service) SetDangerousPolicy(dp *command.DangerousPolicy) {
	s.dangerous = dp
}

func (s *Service) SetAgentRunner(r AgentRunner) {
	s.agentRunner = r
}

func (s *Service) SetCommandWhitelist(wl *command.Whitelist) {
	s.cmdWhitelist = wl
}

func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = NewPGStore(db)
	}
	return &Service{store: st}
}

type ctxKey string

const (
	changeNoteCtxKey ctxKey = "change_note"
	userCtxKey       ctxKey = "user_id"
)

func changeNoteFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(changeNoteCtxKey).(string); ok {
		return v
	}
	return ""
}

func userFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(userCtxKey).(string); ok {
		return v
	}
	return ""
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/scripts", s.handleCreate)
	mux.HandleFunc("GET /api/v1/scripts", s.handleList)
	mux.HandleFunc("GET /api/v1/scripts/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/scripts/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/scripts/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/v1/scripts/{id}/execute", s.handleExecute)
	mux.HandleFunc("GET /api/v1/scripts/{id}/executions", s.handleListExecutions)
	mux.HandleFunc("GET /api/v1/scripts/{id}/versions", s.handleListVersions)
	mux.HandleFunc("GET /api/v1/scripts/{id}/versions/{version}", s.handleGetVersion)
	mux.HandleFunc("GET /api/v1/scripts/{id}/diff", s.handleDiff)
	mux.HandleFunc("POST /api/v1/scripts/check", s.handleCheck)
	mux.HandleFunc("POST /api/v1/scripts/{id}/publish", s.handlePublish)
	mux.HandleFunc("POST /api/v1/scripts/{id}/approve", s.handleApprove)
	mux.HandleFunc("POST /api/v1/scripts/{id}/reject", s.handleReject)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var sc Script
	if err := httputil.BindJSON(r, &sc); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.Name("脚本名称", sc.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	sc.TenantID = auth.TenantFromContext(r.Context())
	if sc.Timeout == 0 {
		sc.Timeout = 300
	}
	sc.ApprovalStatus = s.determineApproval(&sc)
	if err := s.store.Create(r.Context(), &sc); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, sc)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	scripts, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if scripts == nil {
		scripts = []*Script{}
	}
	httputil.WriteJSON(w, http.StatusOK, scripts)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	sc, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || sc == nil {
		httputil.WriteError(w, http.StatusNotFound, "script not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, sc)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	existing, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "script not found")
		return
	}
	var req struct {
		Script
		ChangeNote string `json:"change_note,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sc := req.Script
	if err := validate.Name("脚本名称", sc.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	sc.ID = existing.ID
	sc.TenantID = existing.TenantID
	sc.CreatedAt = existing.CreatedAt
	if sc.Public != existing.Public || sc.Content != existing.Content {
		sc.ApprovalStatus = s.determineApproval(&sc)
	} else {
		sc.ApprovalStatus = existing.ApprovalStatus
	}
	ctx := context.WithValue(r.Context(), changeNoteCtxKey, req.ChangeNote)
	if err := s.store.Update(ctx, &sc); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, sc)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.Delete(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleExecute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	sc, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || sc == nil {
		httputil.WriteError(w, http.StatusNotFound, "script not found")
		return
	}
	var req ExecuteRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.TenantID = tenantID
	if sc.InputParams != nil {
		for _, p := range sc.InputParams {
			if p.Required {
				if _, ok := req.Params[p.Name]; !ok {
					httputil.WriteError(w, http.StatusBadRequest, "missing required param: "+p.Name)
					return
				}
			}
		}
	}
	result := s.executeScript(sc, &req)
	if err := s.store.SaveExecution(r.Context(), result); err != nil {
		slog.Error("failed to save execution", "script_id", sc.ID, "error", err)
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	results, err := s.store.ListExecutions(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if results == nil {
		results = []*ExecutionResult{}
	}
	httputil.WriteJSON(w, http.StatusOK, results)
}

func (s *Service) handleListVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	versions, err := s.store.ListVersions(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []*ScriptVersion{}
	}
	httputil.WriteJSON(w, http.StatusOK, versions)
}

func (s *Service) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var version int
	if _, err := fmt.Sscanf(r.PathValue("version"), "%d", &version); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid version")
		return
	}
	v, err := s.store.GetVersion(r.Context(), id, tenantID, version)
	if err != nil || v == nil {
		httputil.WriteError(w, http.StatusNotFound, "version not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, v)
}

func (s *Service) handleDiff(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	var fromVer, toVer int
	if _, err := fmt.Sscanf(fromStr, "%d", &fromVer); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid from version")
		return
	}
	if _, err := fmt.Sscanf(toStr, "%d", &toVer); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid to version")
		return
	}
	from, err := s.store.GetVersion(r.Context(), id, tenantID, fromVer)
	if err != nil || from == nil {
		httputil.WriteError(w, http.StatusNotFound, "from version not found")
		return
	}
	to, err := s.store.GetVersion(r.Context(), id, tenantID, toVer)
	if err != nil || to == nil {
		httputil.WriteError(w, http.StatusNotFound, "to version not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, diffVersions(from, to))
}

func (s *Service) Execute(scriptID string, params map[string]string, timeout int, runAs string, agentID string, tenantID string) (string, int, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	sc, err := s.store.Get(context.Background(), scriptID, tenantID)
	if err != nil || sc == nil {
		return "", -1, fmt.Errorf("script not found: %s", scriptID)
	}
	req := &ExecuteRequest{Params: params, Timeout: timeout, RunAs: runAs, AgentID: agentID, TenantID: tenantID}
	result := s.executeScript(sc, req)
	s.store.SaveExecution(context.Background(), result)
	if result.Error != "" {
		return result.Output, result.ExitCode, fmt.Errorf("%s", result.Error)
	}
	return result.Output, result.ExitCode, nil
}

func (s *Service) executeScript(sc *Script, req *ExecuteRequest) *ExecutionResult {
	started := time.Now()
	result := &ExecutionResult{
		ID:        uuid.New().String(),
		ScriptID:  sc.ID,
		TenantID:  sc.TenantID,
		Status:    "running",
		Params:    req.Params,
		RunAs:     req.RunAs,
		StartedAt: started,
	}
	// 沙箱执行:下发到带沙箱标签的真实 Agent,在目标主机的隔离环境中运行。
	if req.Sandbox {
		sr, ok := s.agentRunner.(SandboxRunner)
		if !ok || s.agentRunner == nil {
			result.finish("failed", -1, "当前部署未启用沙箱执行能力(Agent 通道不可用)", started)
			return result
		}
		to := req.Timeout
		if to <= 0 {
			to = sc.Timeout
		}
		if to <= 0 {
			to = 300
		}
		tid := req.TenantID
		if tid == "" {
			tid = auth.TenantFromContext(context.Background())
		}
		agentID, perr := sr.PickSandboxAgentID(context.Background(), tid, req.AgentID)
		if perr != nil {
			result.finish("failed", -1, perr.Error(), started)
			return result
		}
		output, exitCode, rerr := sr.RunScriptOnAgentSandboxed(context.Background(), agentID, tid,
			sc.Content, req.Params, to, string(sc.Type), req.Requirements)
		if rerr != nil {
			result.Output = output
			result.finish("failed", exitCode, rerr.Error(), started)
			return result
		}
		result.Output = output
		result.finish("success", exitCode, "", started)
		result.parseOutput(sc.OutputParams)
		return result
	}
	if req.AgentID != "" && s.agentRunner != nil {
		to := req.Timeout
		if to <= 0 {
			to = sc.Timeout
		}
		if to <= 0 {
			to = 300
		}
		ra := req.RunAs
		if ra == "" {
			ra = sc.RunAs
		}
		tid := req.TenantID
		if tid == "" {
			tid = auth.TenantFromContext(context.Background())
		}
		output, exitCode, rerr := s.agentRunner.RunScriptOnAgentWithType(context.Background(), req.AgentID, tid, sc.Content, req.Params, to, ra, string(sc.Type))
		if rerr != nil {
			result.finish("failed", exitCode, rerr.Error(), started)
			return result
		}
		result.Output = output
		result.finish("success", exitCode, "", started)
		result.parseOutput(sc.OutputParams)
		return result
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = sc.Timeout
	}
	if timeout <= 0 {
		timeout = 300
	}
	pattern := "opsorigin-script-*.sh"
	switch sc.Type {
	case ScriptTypePython, ScriptTypePython2, ScriptTypePython3:
		pattern = "opsorigin-script-*.py"
	case ScriptTypeBat:
		pattern = "opsorigin-script-*.bat"
	}
	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		result.finish("failed", -1, fmt.Sprintf("create temp file: %v", err), started)
		return result
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.WriteString(sc.Content); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		result.finish("failed", -1, fmt.Sprintf("write temp file: %v", err), started)
		return result
	}
	tmpFile.Close()
	defer os.Remove(tmpPath)
	runAs := req.RunAs
	if runAs == "" {
		runAs = sc.RunAs
	}
	if s.cmdWhitelist != nil {
		var checkCmd string
		switch sc.Type {
		case ScriptTypeShell:
			checkCmd = "bash"
		case ScriptTypePython, ScriptTypePython3:
			checkCmd = "python3"
		case ScriptTypePython2:
			checkCmd = "python2"
		case ScriptTypeBat:
			checkCmd = "cmd"
		}
		if checkCmd != "" {
			if err := s.cmdWhitelist.Verify(checkCmd); err != nil {
				result.finish("failed", -1, "local exec denied by whitelist: "+err.Error(), started)
				return result
			}
		}
		if runAs != "" {
			if err := s.cmdWhitelist.Verify("sudo"); err != nil {
				result.finish("failed", -1, "local exec with runAs denied by whitelist: "+err.Error(), started)
				return result
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch sc.Type {
	case ScriptTypeShell:
		if runAs != "" {
			cmd = exec.CommandContext(ctx, "sudo", "-u", runAs, "bash", tmpPath)
		} else {
			cmd = exec.CommandContext(ctx, "bash", tmpPath)
		}
	case ScriptTypePython, ScriptTypePython3:
		if runAs != "" {
			cmd = exec.CommandContext(ctx, "sudo", "-u", runAs, "python3", tmpPath)
		} else {
			cmd = exec.CommandContext(ctx, "python3", tmpPath)
		}
	case ScriptTypePython2:
		if runAs != "" {
			cmd = exec.CommandContext(ctx, "sudo", "-u", runAs, "python2", tmpPath)
		} else {
			cmd = exec.CommandContext(ctx, "python2", tmpPath)
		}
	case ScriptTypeBat:
		if runAs != "" {
			cmd = exec.CommandContext(ctx, "sudo", "-u", runAs, "cmd", "/c", tmpPath)
		} else {
			cmd = exec.CommandContext(ctx, "cmd", "/c", tmpPath)
		}
	default:
		result.finish("failed", -1, fmt.Sprintf("unsupported script type: %s", sc.Type), started)
		return result
	}
	cmd.Env = append(os.Environ(), buildParamEnv(req.Params)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.CombinedOutput()
	result.Output = string(output)
	result.Duration = time.Since(started).Milliseconds()
	now := time.Now()
	result.FinishedAt = &now
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		result.Status = "timeout"
		result.ExitCode = -1
		result.Error = "execution timed out"
	case err != nil:
		result.Status = "failed"
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		result.Error = err.Error()
	default:
		result.Status = "success"
		result.ExitCode = 0
	}
	result.parseOutput(sc.OutputParams)
	return result
}

func (r *ExecutionResult) finish(status string, exitCode int, errMsg string, started time.Time) {
	r.Status = status
	r.ExitCode = exitCode
	r.Error = errMsg
	r.Duration = time.Since(started).Milliseconds()
	now := time.Now()
	r.FinishedAt = &now
}

func buildParamEnv(params map[string]string) []string {
	var env []string
	for k, v := range params {
		env = append(env, fmt.Sprintf("PARAM_%s=%s", strings.ToUpper(k), v))
	}
	return env
}

func (r *ExecutionResult) parseOutput(outputParams []ParamDef) {
	if len(outputParams) == 0 || r.Output == "" {
		return
	}
	out := make(map[string]any)
	var full map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(r.Output)), &full) == nil {
		for _, p := range outputParams {
			if v, ok := full[p.Name]; ok {
				out[p.Name] = v
			}
		}
		if len(out) > 0 {
			r.ParsedOutput = out
			return
		}
	}
	lines := strings.Split(strings.TrimSpace(r.Output), "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	var last map[string]any
	if json.Unmarshal([]byte(lastLine), &last) == nil {
		for _, p := range outputParams {
			if v, ok := last[p.Name]; ok {
				out[p.Name] = v
			}
		}
		if len(out) > 0 {
			r.ParsedOutput = out
			return
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		k, v := line[:eq], line[eq+1:]
		if strings.HasPrefix(k, "OUT_") {
			k = k[4:]
		}
		for _, p := range outputParams {
			if p.Name == k {
				out[k] = v
			}
		}
	}
	if len(out) > 0 {
		r.ParsedOutput = out
	}
}

func diffVersions(from, to *ScriptVersion) *DiffResult {
	result := &DiffResult{FromVersion: from.Version, ToVersion: to.Version}
	if from.Name != to.Name {
		result.Changes = append(result.Changes, DiffEntry{Field: "name", OldValue: from.Name, NewValue: to.Name})
	}
	if from.Timeout != to.Timeout {
		result.Changes = append(result.Changes, DiffEntry{Field: "timeout", OldValue: fmt.Sprintf("%d", from.Timeout), NewValue: fmt.Sprintf("%d", to.Timeout)})
	}
	if from.RunAs != to.RunAs {
		result.Changes = append(result.Changes, DiffEntry{Field: "run_as", OldValue: from.RunAs, NewValue: to.RunAs})
	}
	if fromJ, toJ := toJSONString(from.InputParams), toJSONString(to.InputParams); fromJ != toJ {
		result.Changes = append(result.Changes, DiffEntry{Field: "input_params", OldValue: fromJ, NewValue: toJ})
	}
	if fromJ, toJ := toJSONString(from.OutputParams), toJSONString(to.OutputParams); fromJ != toJ {
		result.Changes = append(result.Changes, DiffEntry{Field: "output_params", OldValue: fromJ, NewValue: toJ})
	}
	if from.Content != to.Content {
		result.Changes = append(result.Changes, diffContent(from.Content, to.Content)...)
	}
	return result
}

func diffContent(old, new string) []DiffEntry {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")
	oldCounts := make(map[string]int)
	for _, l := range oldLines {
		oldCounts[l]++
	}
	newCounts := make(map[string]int)
	for _, l := range newLines {
		newCounts[l]++
	}
	var entries []DiffEntry
	remCounts := make(map[string]int)
	for k, v := range oldCounts {
		if v > newCounts[k] {
			remCounts[k] = v - newCounts[k]
		}
	}
	for _, l := range oldLines {
		if remCounts[l] > 0 {
			entries = append(entries, DiffEntry{Field: "content", OldValue: l})
			remCounts[l]--
		}
	}
	addCounts := make(map[string]int)
	for k, v := range newCounts {
		if v > oldCounts[k] {
			addCounts[k] = v - oldCounts[k]
		}
	}
	for _, l := range newLines {
		if addCounts[l] > 0 {
			entries = append(entries, DiffEntry{Field: "content", NewValue: l})
			addCounts[l]--
		}
	}
	return entries
}

func toJSONString(v any) string { b, _ := json.Marshal(v); return string(b) }

func (s *Service) handleCheck(w http.ResponseWriter, r *http.Request) {
	var req CheckRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" || req.Content == "" {
		httputil.WriteError(w, http.StatusBadRequest, "type and content are required")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, checkSyntax(string(req.Type), req.Content))
}

var bashErrRE = regexp.MustCompile(`^.*:\s*line\s+(\d+):\s+(.+)`)
var pythonErrRE = regexp.MustCompile(`line\s+(\d+)`)

func checkSyntax(scriptType, content string) *CheckResult {
	switch ScriptType(scriptType) {
	case ScriptTypeShell:
		return checkShell(content)
	case ScriptTypePython:
		return checkPython(content)
	case ScriptTypeBat:
		return &CheckResult{Valid: true}
	default:
		return &CheckResult{Valid: false, Errors: []CheckError{{Message: fmt.Sprintf("unsupported script type: %s", scriptType)}}}
	}
}

func checkShell(content string) *CheckResult {
	tmp, err := os.CreateTemp("", "opsorigin-script-check-*.sh")
	if err != nil {
		return &CheckResult{Valid: false, Errors: []CheckError{{Message: fmt.Sprintf("failed to create temp file: %v", err)}}}
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		return &CheckResult{Valid: false, Errors: []CheckError{{Message: fmt.Sprintf("failed to write temp file: %v", err)}}}
	}
	tmp.Close()
	cmd := exec.Command("bash", "-n", tmp.Name())
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		return &CheckResult{Valid: true}
	}
	lines := strings.Split(string(stderr), "\n")
	var errors []CheckError
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := bashErrRE.FindStringSubmatch(line)
		if m != nil {
			ln, _ := strconv.Atoi(m[1])
			errors = append(errors, CheckError{Line: ln, Message: strings.TrimSpace(m[2])})
		} else {
			errors = append(errors, CheckError{Message: line})
		}
	}
	if len(errors) == 0 {
		errors = append(errors, CheckError{Message: string(stderr)})
	}
	return &CheckResult{Valid: false, Errors: errors}
}

func checkPython(content string) *CheckResult {
	tmp, err := os.CreateTemp("", "opsorigin-script-check-*.py")
	if err != nil {
		return &CheckResult{Valid: false, Errors: []CheckError{{Message: fmt.Sprintf("failed to create temp file: %v", err)}}}
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		return &CheckResult{Valid: false, Errors: []CheckError{{Message: fmt.Sprintf("failed to write temp file: %v", err)}}}
	}
	tmp.Close()
	cmd := exec.Command("python3", "-m", "py_compile", tmp.Name())
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		return &CheckResult{Valid: true}
	}
	lines := strings.Split(string(stderr), "\n")
	var errors []CheckError
	current := CheckError{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := pythonErrRE.FindStringSubmatch(line)
		if m != nil {
			current.Line, _ = strconv.Atoi(m[1])
		} else if strings.HasPrefix(line, "SyntaxError:") || strings.HasPrefix(line, "IndentationError:") || strings.HasPrefix(line, "TabError:") {
			current.Message = line
			errors = append(errors, current)
			current = CheckError{}
		} else if !strings.HasPrefix(line, "File ") && !strings.HasPrefix(line, "^") {
			errors = append(errors, CheckError{Message: line})
		}
	}
	if len(errors) == 0 {
		errors = append(errors, CheckError{Message: string(stderr)})
	}
	return &CheckResult{Valid: false, Errors: errors}
}

// ------- 公共脚本审批 -------

func (s *Service) determineApproval(sc *Script) string {
	if !sc.Public {
		return "draft"
	}
	if s.dangerous != nil {
		matches := s.dangerous.ScanScript(sc.Content)
		if len(matches) > 0 {
			slog.Warn("public script contains dangerous commands, requires approval", "script_name", sc.Name, "match_count", len(matches))
			return "pending"
		}
	}
	return "approved"
}

func (s *Service) handlePublish(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	sc, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || sc == nil {
		httputil.WriteError(w, http.StatusNotFound, "script not found")
		return
	}
	if !sc.Public {
		httputil.WriteError(w, http.StatusBadRequest, "only public scripts can be published")
		return
	}
	sc.ApprovalStatus = s.determineApproval(sc)
	if err := s.store.Update(r.Context(), sc); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, sc)
}

func (s *Service) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	sc, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || sc == nil {
		httputil.WriteError(w, http.StatusNotFound, "script not found")
		return
	}
	// 接受 pending(公共脚本高危需审批)与 draft(AI 生成/私有脚本待批准) → approved
	if sc.ApprovalStatus != "pending" && sc.ApprovalStatus != "draft" {
		httputil.WriteError(w, http.StatusBadRequest, "script is not in pending/draft status")
		return
	}
	sc.ApprovalStatus = "approved"
	ctx := context.WithValue(r.Context(), changeNoteCtxKey, "approved")
	if err := s.store.Update(ctx, sc); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, sc)
}

func (s *Service) handleReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	sc, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || sc == nil {
		httputil.WriteError(w, http.StatusNotFound, "script not found")
		return
	}
	if sc.ApprovalStatus != "pending" {
		httputil.WriteError(w, http.StatusBadRequest, "script is not in pending status")
		return
	}
	sc.ApprovalStatus = "rejected"
	ctx := context.WithValue(r.Context(), changeNoteCtxKey, "rejected")
	if err := s.store.Update(ctx, sc); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, sc)
}
