package script

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/opsorigin/core/internal/command"
	"fmt"
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
	versions   map[string][]*ScriptVersion    // scriptID -> versions (ascending by Version)
	executions map[string][]*ExecutionResult  // scriptID -> executions (ascending by StartedAt)
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
		ID:          uuid.New().String(),
		ScriptID:    sc.ID,
		TenantID:    sc.TenantID,
		Version:     sc.Version,
		Name:        sc.Name,
		Type:        sc.Type,
		Content:     sc.Content,
		InputParams: sc.InputParams,
		OutputParams: sc.OutputParams,
		Timeout:     sc.Timeout,
		RunAs:       sc.RunAs,
		ChangeNote:  changeNote,
		CreatedBy:   createdBy,
		CreatedAt:   at,
	}
}

// AgentRunner 把脚本下发到被管主机上的 Agent 执行。
// 由 agentmgr.Service 通过 Tunnel 实现，使脚本真正在主机运行（而非 Core 本机）。
type AgentRunner interface {
	RunScriptOnAgent(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string) (output string, exitCode int, err error)
}

// Service exposes the script management HTTP API.
type Service struct {
	store       Store
	agentRunner AgentRunner
	cmdWhitelist *command.Whitelist // 本地兜底执行路径的命令白名单(§6)
}

// SetAgentRunner 注入 Agent 派发器。未注入（或脚本未指定 AgentID）时，
// 脚本回退到 Core 本机 os/exec 执行（调试/沙箱场景）。
func (s *Service) SetAgentRunner(r AgentRunner) {
	s.agentRunner = r
}

// SetCommandWhitelist 注入本地执行路径的命令白名单。
// 当脚本在 Core 本机兜底运行(无 Agent)时,若白名单已设且含 sudo/runAs, 则校验基础命令。
// nil 表示未启用(不拦截, dev 期可用)。
func (s *Service) SetCommandWhitelist(wl *command.Whitelist) {
	s.cmdWhitelist = wl
}

// NewService creates a Service backed by an in-memory store.
func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = NewPGStore(db)
	}
	return &Service{store: st}
}

// context keys for out-of-band request data.
type ctxKey string

const (
	changeNoteCtxKey ctxKey = "change_note"
	userCtxKey      ctxKey = "user_id"
)

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

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

// RegisterRoutes wires all script API routes onto the given mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	// 原有 CRUD
	mux.HandleFunc("POST /api/v1/scripts", s.handleCreate)
	mux.HandleFunc("GET /api/v1/scripts", s.handleList)
	mux.HandleFunc("GET /api/v1/scripts/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/scripts/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/scripts/{id}", s.handleDelete)
	// 新增:执行
	mux.HandleFunc("POST /api/v1/scripts/{id}/execute", s.handleExecute)
	// 新增:执行历史
	mux.HandleFunc("GET /api/v1/scripts/{id}/executions", s.handleListExecutions)
	// 新增:版本管理
	mux.HandleFunc("GET /api/v1/scripts/{id}/versions", s.handleListVersions)
	mux.HandleFunc("GET /api/v1/scripts/{id}/versions/{version}", s.handleGetVersion)
	// 新增:版本比对(用 query 参数 from/to,末尾 diff 字面量与 {version} 通配符不冲突)
	mux.HandleFunc("GET /api/v1/scripts/{id}/diff", s.handleDiff)
	// 新增:语法检查（POST /api/v1/scripts/check）
	mux.HandleFunc("POST /api/v1/scripts/check", s.handleCheck)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var sc Script
	if err := json.NewDecoder(r.Body).Decode(&sc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sc.TenantID = tenantFromCtx(r.Context())
	if sc.Timeout == 0 {
		sc.Timeout = 300
	}
	if err := s.store.Create(r.Context(), &sc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	scripts, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if scripts == nil {
		scripts = []*Script{}
	}
	writeJSON(w, http.StatusOK, scripts)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	sc, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || sc == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	// 解析脚本字段 + 可选的 change_note(单独解码)
	var req struct {
		Script
		ChangeNote string `json:"change_note,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sc := req.Script
	sc.ID = existing.ID
	sc.TenantID = existing.TenantID
	sc.CreatedAt = existing.CreatedAt
	// Version 递增在 store.Update 内完成
	ctx := context.WithValue(r.Context(), changeNoteCtxKey, req.ChangeNote)
	if err := s.store.Update(ctx, &sc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.Delete(r.Context(), id, tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleExecute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	sc, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || sc == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.TenantID = tenantID

	// 校验必填输入参数
	if sc.InputParams != nil {
		for _, p := range sc.InputParams {
			if p.Required {
				if _, ok := req.Params[p.Name]; !ok {
					writeError(w, http.StatusBadRequest, "missing required param: "+p.Name)
					return
				}
			}
		}
	}

	result := s.executeScript(sc, &req)
	if err := s.store.SaveExecution(r.Context(), result); err != nil {
		slog.Error("failed to save execution", "script_id", sc.ID, "error", err)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	results, err := s.store.ListExecutions(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if results == nil {
		results = []*ExecutionResult{}
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Service) handleListVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	versions, err := s.store.ListVersions(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []*ScriptVersion{}
	}
	writeJSON(w, http.StatusOK, versions)
}

func (s *Service) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var version int
	if _, err := fmt.Sscanf(r.PathValue("version"), "%d", &version); err != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	v, err := s.store.GetVersion(r.Context(), id, tenantID, version)
	if err != nil || v == nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Service) handleDiff(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	var fromVer, toVer int
	if _, err := fmt.Sscanf(fromStr, "%d", &fromVer); err != nil {
		writeError(w, http.StatusBadRequest, "invalid from version")
		return
	}
	if _, err := fmt.Sscanf(toStr, "%d", &toVer); err != nil {
		writeError(w, http.StatusBadRequest, "invalid to version")
		return
	}
	from, err := s.store.GetVersion(r.Context(), id, tenantID, fromVer)
	if err != nil || from == nil {
		writeError(w, http.StatusNotFound, "from version not found")
		return
	}
	to, err := s.store.GetVersion(r.Context(), id, tenantID, toVer)
	if err != nil || to == nil {
		writeError(w, http.StatusNotFound, "to version not found")
		return
	}
	writeJSON(w, http.StatusOK, diffVersions(from, to))
}

// Execute 为 workflow 引擎提供的公开接口,执行指定脚本并返回结果。
// agentID 非空时通过 AgentRunner 派发到主机执行；tenantID 用于多租户隔离
// （修复原硬编码 "default" 导致非 default 租户脚本 not found 的 bug）。
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

// executeScript runs the script in a subprocess and returns the captured result.
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

	// 目标 Agent 派发：指定 AgentID 且已注入 AgentRunner 时，
	// 把脚本下发到该 Agent 所在被管主机执行（复用 Agent 端白名单校验）。
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
			tid = tenantFromCtx(context.Background())
		}
		output, exitCode, rerr := s.agentRunner.RunScriptOnAgent(context.Background(), req.AgentID, tid, sc.Content, req.Params, to, ra)
		if rerr != nil {
			result.finish("failed", exitCode, rerr.Error(), started)
			return result
		}
		result.Output = output
		result.finish("success", exitCode, "", started)
		result.parseOutput(sc.OutputParams)
		return result
	}

	// 确定超时:请求优先,否则脚本,否则默认 300
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = sc.Timeout
	}
	if timeout <= 0 {
		timeout = 300
	}

	// 写临时文件(带扩展名)
	pattern := "opsorigin-script-*.sh"
	switch sc.Type {
	case ScriptTypePython:
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

	// RunAs:请求优先,否则脚本
	runAs := req.RunAs
	if runAs == "" {
		runAs = sc.RunAs
	}

	// 本地执行路径白名单校验(与 Agent 端校验对齐 §6)
	if s.cmdWhitelist != nil {
		var checkCmd string
		switch sc.Type {
		case ScriptTypeShell:
			checkCmd = "bash"
		case ScriptTypePython:
			checkCmd = "python3"
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
	case ScriptTypePython:
		if runAs != "" {
			cmd = exec.CommandContext(ctx, "sudo", "-u", runAs, "python3", tmpPath)
		} else {
			cmd = exec.CommandContext(ctx, "python3", tmpPath)
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

	// 注入环境变量 PARAM_<NAME>=<value>
	cmd.Env = append(os.Environ(), buildParamEnv(req.Params)...)
	// 将脚本进程放入独立进程组,便于超时时清理子进程
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

// finish finalizes an ExecutionResult with the given terminal state.
func (r *ExecutionResult) finish(status string, exitCode int, errMsg string, started time.Time) {
	r.Status = status
	r.ExitCode = exitCode
	r.Error = errMsg
	r.Duration = time.Since(started).Milliseconds()
	now := time.Now()
	r.FinishedAt = &now
}

// buildParamEnv converts params into PARAM_<NAME>=<value> environment entries.
func buildParamEnv(params map[string]string) []string {
	var env []string
	for k, v := range params {
		env = append(env, fmt.Sprintf("PARAM_%s=%s", strings.ToUpper(k), v))
	}
	return env
}

// parseOutput 从 stdout 中提取出参：优先整段JSON → 末行JSON → KEY=VALUE行。
func (r *ExecutionResult) parseOutput(outputParams []ParamDef) {
	if len(outputParams) == 0 || r.Output == "" { return }
	out := make(map[string]any)

	// 策略1: 整段 stdout 就是 JSON
	var full map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(r.Output)), &full) == nil {
		for _, p := range outputParams {
			if v, ok := full[p.Name]; ok { out[p.Name] = v }
		}
		if len(out) > 0 { r.ParsedOutput = out; return }
	}

	// 策略2: 取最后一行 JSON
	lines := strings.Split(strings.TrimSpace(r.Output), "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	var last map[string]any
	if json.Unmarshal([]byte(lastLine), &last) == nil {
		for _, p := range outputParams {
			if v, ok := last[p.Name]; ok { out[p.Name] = v }
		}
		if len(out) > 0 { r.ParsedOutput = out; return }
	}

	// 策略3: KV行 — 匹配 OutputParams 中定义的名字, 也支持 OUT_<NAME>=<VALUE> 前缀
	for _, line := range lines {
		line = strings.TrimSpace(line)
		eq := strings.Index(line, "=")
		if eq <= 0 { continue }
		k, v := line[:eq], line[eq+1:]
		// 去掉 OUT_ 前缀 (约定: 脚本中用 OUT_HOSTNAME=value 输出)
		if strings.HasPrefix(k, "OUT_") { k = k[4:] }
		for _, p := range outputParams {
			if p.Name == k { out[k] = v }
		}
	}
	if len(out) > 0 { r.ParsedOutput = out }
}

// diffVersions compares two script versions field by field.
func diffVersions(from, to *ScriptVersion) *DiffResult {
	result := &DiffResult{FromVersion: from.Version, ToVersion: to.Version}

	if from.Name != to.Name {
		result.Changes = append(result.Changes, DiffEntry{Field: "name", OldValue: from.Name, NewValue: to.Name})
	}
	if from.Timeout != to.Timeout {
		result.Changes = append(result.Changes, DiffEntry{
			Field:    "timeout",
			OldValue: fmt.Sprintf("%d", from.Timeout),
			NewValue: fmt.Sprintf("%d", to.Timeout),
		})
	}
	if from.RunAs != to.RunAs {
		result.Changes = append(result.Changes, DiffEntry{Field: "run_as", OldValue: from.RunAs, NewValue: to.RunAs})
	}
	if fromJSON, toJSON := toJSONString(from.InputParams), toJSONString(to.InputParams); fromJSON != toJSON {
		result.Changes = append(result.Changes, DiffEntry{Field: "input_params", OldValue: fromJSON, NewValue: toJSON})
	}
	if fromJSON, toJSON := toJSONString(from.OutputParams), toJSONString(to.OutputParams); fromJSON != toJSON {
		result.Changes = append(result.Changes, DiffEntry{Field: "output_params", OldValue: fromJSON, NewValue: toJSON})
	}
	if from.Content != to.Content {
		// content 字段标记为 old_value/new_value 的行级 diff
		result.Changes = append(result.Changes, diffContent(from.Content, to.Content)...)
	}
	return result
}

// diffContent performs a simple line-level diff (added/removed lines).
// Removed lines are recorded with OldValue, added lines with NewValue, both
// under the "content" field.
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
	// removed lines (only in old)
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
	// added lines (only in new)
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

func toJSONString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleCheck 语法检查：POST /api/v1/scripts/check
// 请求体 {type, content}，返回 {valid, errors[{line, column, message}]}。
func (s *Service) handleCheck(w http.ResponseWriter, r *http.Request) {
	var req CheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "type and content are required")
		return
	}
	result := checkSyntax(string(req.Type), req.Content)
	writeJSON(w, http.StatusOK, result)
}

// ---- 语法检查实现 ----

// bashErrRE 匹配 bash -n 的错误输出格式：
//
//	sh: line 3: syntax error near unexpected token `fi'
//
// bash: line 5: fi: unexpected end of file
var bashErrRE = regexp.MustCompile(`^.*:\s*line\s+(\d+):\s+(.+)`)

// pythonErrRE 匹配 python3 -m py_compile 的错误输出格式：
//
//	File "<stdin>", line 3
//	    x =
//	    ^
//
// SyntaxError: invalid syntax
var pythonErrRE = regexp.MustCompile(`line\s+(\d+)`)

func checkSyntax(scriptType, content string) *CheckResult {
	switch ScriptType(scriptType) {
	case ScriptTypeShell:
		return checkShell(content)
	case ScriptTypePython:
		return checkPython(content)
	case ScriptTypeBat:
		// Windows batch 文件无官方语法检查器，直接通过
		return &CheckResult{Valid: true}
	default:
		return &CheckResult{Valid: false, Errors: []CheckError{{Message: fmt.Sprintf("unsupported script type: %s", scriptType)}}}
	}
}

func checkShell(content string) *CheckResult {
	// 写临时文件，bash -n 检查语法（不执行）
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

	// 解析错误行
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
	// python3 -m py_compile 编译检查（不执行）
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

	// 解析错误行号
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
			// 非结构化错误消息
			errors = append(errors, CheckError{Message: line})
		}
	}
	if len(errors) == 0 {
		errors = append(errors, CheckError{Message: string(stderr)})
	}
	return &CheckResult{Valid: false, Errors: errors}
}
