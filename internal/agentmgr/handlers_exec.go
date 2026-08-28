package agentmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/internal/remoteexec"
	"github.com/opsorigin/core/internal/tunnel"

	"github.com/opsorigin/core/pkg/httputil"
)

// verifyCommand 服务端命令白名单校验。whitelist 为 nil(未启用)时直接放行。
func (s *Service) verifyCommand(command string) error {
	s.whitelistMu.RLock()
	wl := s.whitelist
	s.whitelistMu.RUnlock()
	if wl == nil {
		return nil
	}
	return wl.Verify(command)
}

// handleBatchExecute 在多台主机上并发执行命令(SSH 远程执行)。
func (s *Service) handleBatchExecute(w http.ResponseWriter, r *http.Request) {
	var req remoteexec.BatchExecRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Hosts) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "hosts is required")
		return
	}
	if req.Command == "" {
		httputil.WriteError(w, http.StatusBadRequest, "command is required")
		return
	}

	result := s.remoteExec.Execute(r.Context(), req)
	httputil.WriteJSON(w, http.StatusOK, result)
}

// handleGetExecTask 查询批量执行任务状态。
func (s *Service) handleGetExecTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	if taskID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "task_id is required")
		return
	}

	result, ok := s.remoteExec.GetTask(taskID)
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, result)
}

// handleSSHExecute 在单台主机上执行命令。
// 支持三种目标：
//   - 默认：通过 SSH 直连目标主机（Core 出站）。
//   - proxy_id 指定：目标位于隔离网段，由 Proxy 代为 SSH 执行（见 proxy 契约）。
//   - sandbox=true：不指定目标主机，直接在 Server 本机沙箱环境执行（调试/冒烟）。
func (s *Service) handleSSHExecute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port,omitempty"`
		Username string `json:"username"`
		Password string `json:"password,omitempty"`
		SSHKey   string `json:"ssh_key,omitempty"`
		Command  string `json:"command"`
		Timeout  int    `json:"timeout,omitempty"`
		ProxyID  string `json:"proxy_id,omitempty"` // 指定则走 Proxy 代为执行
		Sandbox  bool   `json:"sandbox,omitempty"`  // 勾选沙箱环境执行
		// SandboxAgentID 可选:指定用哪台带沙箱标签的 Agent 执行,留空自动挑选在线的。
		SandboxAgentID string `json:"sandbox_agent_id,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Command == "" {
		httputil.WriteError(w, http.StatusBadRequest, "command is required")
		return
	}

	// §6 命令白名单:服务端下发前强制校验(直连 / 经 Proxy / 沙箱 三条路径统一拦截)。
	if err := s.verifyCommand(req.Command); err != nil {
		slog.Warn("ssh-execute denied by whitelist", "host", req.Host, "error", err)
		httputil.WriteError(w, http.StatusForbidden, "command denied by whitelist: "+err.Error())
		return
	}

	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// 沙箱执行：不选择普通目标主机，改为下发到一台带沙箱标签的真实 Agent，
	// 由其在所在主机上创建真实隔离环境执行。
	if req.Sandbox {
		tenantID := auth.TenantFromContext(r.Context())
		agent, err := s.pickSandboxAgent(r.Context(), tenantID, req.SandboxAgentID)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		start := time.Now()
		output, exitCode, execErr := s.RunScriptOnAgentWithOptions(r.Context(), agent.ID, tenantID,
			req.Command, nil, ScriptRunOptions{
				Timeout: int(timeout.Seconds()),
				Sandbox: true,
			})
		resp := map[string]any{
			"host":      agent.Name,
			"agent_id":  agent.ID,
			"output":    output,
			"exit_code": exitCode,
			"duration":  time.Since(start).Milliseconds(),
			"via":       "sandbox-agent",
		}
		if execErr != nil {
			resp["error"] = execErr.Error()
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
		return
	}

	port := req.Port
	if port == 0 {
		port = 22
	}

	// 通过 Proxy 执行：由 Proxy 代为 SSH（目标在隔离网段，Core 不可达）
	if req.ProxyID != "" && s.tunnelServer != nil {
		out, exitCode, execErr := s.execViaProxy(req.ProxyID, "ssh_exec", map[string]any{
			"host":     req.Host,
			"port":     port,
			"username": req.Username,
			"password": req.Password,
			"ssh_key":  req.SSHKey,
			"command":  req.Command,
		}, int32(timeout.Seconds()))
		if execErr == nil {
			httputil.WriteJSON(w, http.StatusOK, map[string]any{
				"host":      req.Host,
				"port":      port,
				"output":    out,
				"exit_code": exitCode,
				"via":       "proxy",
			})
			return
		}
		slog.Warn("agentless via proxy failed, fallback to direct ssh",
			"error", execErr, "proxy_id", req.ProxyID)
		// 失败降级为直连
	}

	if req.Host == "" {
		httputil.WriteError(w, http.StatusBadRequest, "host is required when not using sandbox")
		return
	}

	cred := remoteexec.Credential{
		User:     req.Username,
		Password: req.Password,
		Key:      req.SSHKey,
	}

	execReq := remoteexec.ExecRequest{
		Host:       req.Host,
		Port:       port,
		Credential: cred,
		Command:    req.Command,
		Timeout:    timeout,
	}

	result := s.remoteExec.ExecuteSingle(r.Context(), execReq)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"host":      result.Host,
		"port":      result.Port,
		"output":    result.Output,
		"exit_code": result.ExitCode,
		"error":     result.Error,
		"duration":  result.Duration.Milliseconds(),
		"via":       "direct",
	})
}

// execViaProxy 通过 Proxy 隧道代为执行命令（Agentless 目标在隔离网段时）。
// 约定：proxy 侧收到 CommandRequest 后，按 action 执行：
//   - "ssh_exec"：以 payload 中的 host/port/user/key 做 SSH 并在目标上运行 command。
//   - "ssh_test"：仅做连通性探测。
//
// 返回结构化结果经 CommandResponse.Output（JSON：{output,exit_code,error}）回传。
func (s *Service) execViaProxy(proxyID, action string, payload any, timeoutSec int32) (output string, exitCode int, err error) {
	body, mErr := json.Marshal(payload)
	if mErr != nil {
		return "", -1, fmt.Errorf("marshal proxy payload: %w", mErr)
	}
	cmd := &tunnel.CommandRequest{
		CommandID:     uuid.NewString(),
		TargetID:      proxyID,
		Action:        action,
		Payload:       body,
		TimeoutSecond: timeoutSec,
	}
	resp, e := s.tunnelServer.SendCommand(proxyID, cmd)
	if e != nil {
		return "", -1, e
	}
	if resp == nil || !resp.Success {
		msg := ""
		if resp != nil {
			msg = resp.Error
		}
		return "", -1, fmt.Errorf("proxy exec failed: %s", msg)
	}
	// 尽力解析结构化结果；失败则整体作为输出
	var structured struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
		Error    string `json:"error"`
	}
	if json.Unmarshal(resp.Output, &structured) == nil && structured.Output != "" {
		return structured.Output, structured.ExitCode, nil
	}
	return string(resp.Output), 0, nil
}

// RunScriptOnAgent 通过 Tunnel 把脚本下发到指定 Agent 所在被管主机执行，
// 并同步等待结果返回。content 为脚本正文，params 透传给 Agent 端执行环境。
// 这是 automation/script 引擎的 AgentRunner 实现，使脚本真正在主机上运行
// （而非 Core 本机 os/exec），并复用 Agent 端命令白名单校验(§6)。
// dispatchCommand 选择一个已注册该 key 会话的 tunnel server 下发命令：
// 优先 gRPC(直连 Agent 或 gRPC 代理)，回退 TCP(TCP 代理)。
// key 语义：直连 Agent 的会话 key 即其 agentID(gRPC 直连时注册 ProxyId=agentID)；
// 经代理则 key 为 host.ProxyID(代理 ID)。
func (s *Service) dispatchCommand(ctx context.Context, key string, cmd *tunnel.CommandRequest) (*tunnel.CommandResponse, error) {
	if s.grpcTunnel != nil {
		if s.grpcTunnel.HasProxy(key) {
			return s.grpcTunnel.SendCommand(ctx, key, cmd)
		}
	}
	if s.tunnelServer != nil {
		if s.tunnelServer.HasProxy(key) {
			return s.tunnelServer.SendCommand(key, cmd)
		}
	}
	return nil, fmt.Errorf("agent/proxy %s not connected on any tunnel", key)
}

func (s *Service) RunScriptOnAgent(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string) (string, int, error) {
	return s.RunScriptOnAgentWithOptions(ctx, agentID, tenantID, content, params,
		ScriptRunOptions{Timeout: timeout, RunAs: runAs})
}

// RunScriptOnAgentWithType 与 RunScriptOnAgent 相同，但额外指定脚本类型(shell/python/bat)。
func (s *Service) RunScriptOnAgentWithType(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string, scriptType string) (string, int, error) {
	return s.RunScriptOnAgentWithOptions(ctx, agentID, tenantID, content, params,
		ScriptRunOptions{Timeout: timeout, RunAs: runAs, ScriptType: scriptType})
}

// RunScriptOnAgentSandboxed 以沙箱模式把脚本下发到带沙箱标签的真实 Agent,
// 由其在目标主机上创建真实隔离环境执行。实现 script.SandboxRunner。
func (s *Service) RunScriptOnAgentSandboxed(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, scriptType string, requirements []string) (string, int, error) {
	return s.RunScriptOnAgentWithOptions(ctx, agentID, tenantID, content, params, ScriptRunOptions{
		Timeout:      timeout,
		Sandbox:      true,
		ScriptType:   scriptType,
		Requirements: requirements,
	})
}

// PickSandboxAgentID 挑选一台可用的沙箱 Agent 并返回其 ID。实现 script.SandboxRunner。
func (s *Service) PickSandboxAgentID(ctx context.Context, tenantID, preferID string) (string, error) {
	agent, err := s.pickSandboxAgent(ctx, tenantID, preferID)
	if err != nil {
		return "", err
	}
	return agent.ID, nil
}

// RunScriptOnAgentWithOptions 是 RunScriptOnAgent 的可扩展版本,支持沙箱隔离执行。
func (s *Service) RunScriptOnAgentWithOptions(ctx context.Context, agentID, tenantID, content string, params map[string]string, opts ScriptRunOptions) (string, int, error) {
	if s.tunnelServer == nil && s.grpcTunnel == nil {
		return "", -1, fmt.Errorf("no tunnel server configured: cannot dispatch script to agent %s", agentID)
	}
	host, err := s.store.Get(ctx, agentID, tenantID)
	if err != nil || host == nil {
		return "", -1, fmt.Errorf("agent %s not found (tenant=%s)", agentID, tenantID)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 300
	}
	// 请求沙箱执行时,目标 Agent 必须确实带沙箱标签,避免脚本在未隔离的生产
	// 主机上裸跑。
	if opts.Sandbox && !host.IsSandbox() {
		return "", -1, fmt.Errorf("Agent %s 未被标记为沙箱环境,拒绝以沙箱方式执行", agentID)
	}
	// 沙箱高危命令硬拦截(Core 侧防御):即便 Agent 端未强制,下发前先扫描。
	// 命中即拒绝,避免高危脚本进入隔离环境执行。
	if opts.Sandbox {
		if blocked := command.BlockedInSandbox(content); len(blocked) > 0 {
			ids := make([]string, 0, len(blocked))
			for _, m := range blocked {
				ids = append(ids, fmt.Sprintf("%s(%s)", m.RuleID, m.Desc))
			}
			return "", -1, fmt.Errorf("沙箱执行被拒绝:脚本包含高危命令 %s", strings.Join(ids, ", "))
		}
	}
	p := make(map[string]any, len(params))
	for k, v := range params {
		p[k] = v
	}
	task := &TaskRequest{
		Action:       "script",
		Script:       content,
		Params:       p,
		Timeout:      timeout,
		RunAs:        opts.RunAs,
		Sandbox:      opts.Sandbox,
		ScriptType:   opts.ScriptType,
		Requirements: opts.Requirements,
	}
	body, mErr := json.Marshal(task)
	if mErr != nil {
		return "", -1, fmt.Errorf("marshal task: %w", mErr)
	}
	cmd := &tunnel.CommandRequest{
		CommandID:     uuid.NewString(),
		TargetID:      agentID,
		Action:        "script",
		Payload:       body,
		TimeoutSecond: int32(timeout),
	}
	// 直连 Agent(ProxyID 为空)会话 key 即 agentID；经代理则 key 为 host.ProxyID。
	// 按"会话落在哪个 tunnel"智能选路——修复原实现永远只走空 TCP server、
	// 且对直连 Agent 误判为"无代理绑定"而拒绝的问题。
	key := host.ProxyID
	if key == "" {
		key = agentID
	}
	resp, e := s.dispatchCommand(ctx, key, cmd)
	if e != nil {
		return "", -1, e
	}
	if resp == nil || !resp.Success {
		msg := ""
		if resp != nil {
			msg = resp.Error
		}
		return "", -1, fmt.Errorf("agent script exec failed: %s", msg)
	}
	// Agent 端 executeScript 将 TaskResponse JSON 作为 Output 回传。
	var tr TaskResponse
	if json.Unmarshal(resp.Output, &tr) == nil {
		if !tr.Success {
			return tr.Output, tr.ExitCode, fmt.Errorf("%s", tr.Error)
		}
		return tr.Output, tr.ExitCode, nil
	}
	return string(resp.Output), 0, nil
}

// handleGetWhitelist 返回本租户的命令白名单(Agent 启动拉取,§6)。
// enabled=false 表示未启用(放行一切);enabled=true 且 rules 为空表示拒绝一切。
func (s *Service) handleGetWhitelist(w http.ResponseWriter, r *http.Request) {
	s.whitelistMu.RLock()
	wl := s.whitelist
	s.whitelistMu.RUnlock()

	if wl == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
			"version": 0,
			"rules":   []any{},
		})
		return
	}
	// 序列化 rules 为 {type,pattern} 列表。
	raw := wl.RawRules()
	rules := make([]map[string]string, 0, len(raw))
	for _, r := range raw {
		rules = append(rules, map[string]string{"type": string(r.Type), "pattern": r.Pattern})
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"version": 1,
		"rules":   rules,
	})
}

// handleExecSandbox 在"沙箱 Agent"所在的目标主机上,于真实隔离环境中执行脚本。
//
// 对应 UI 的"勾选沙箱环境执行":用户无需挑选普通目标主机,系统会选取一台被打上
// 沙箱标签的真实 Agent(通常就是部署 Server 的那几台机器),把脚本经隧道下发过去。
// Agent 端会为本次执行创建独立临时工作目录、净化环境变量、独立进程组;
// Python 脚本额外创建虚拟环境(venv)并按 requirements 安装依赖,执行后整体销毁。
func (s *Service) handleExecSandbox(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Script       string            `json:"script"`
		Timeout      int               `json:"timeout,omitempty"`
		SandboxID    string            `json:"sandbox_id,omitempty"` // 可选:指定某个沙箱 Agent
		AgentID      string            `json:"agent_id,omitempty"`   // 同义别名
		ScriptType   string            `json:"script_type,omitempty"`
		Requirements []string          `json:"requirements,omitempty"`
		Params       map[string]string `json:"params,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Script == "" {
		httputil.WriteError(w, http.StatusBadRequest, "script is required")
		return
	}
	// §6 命令白名单:沙箱执行同样受控(白名单启用时)。
	if err := s.verifyCommand(req.Script); err != nil {
		slog.Warn("sandbox-exec denied by whitelist", "error", err)
		httputil.WriteError(w, http.StatusForbidden, "script denied by whitelist: "+err.Error())
		return
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 60
	}

	tenantID := auth.TenantFromContext(r.Context())
	prefer := req.SandboxID
	if prefer == "" {
		prefer = req.AgentID
	}
	agent, err := s.pickSandboxAgent(r.Context(), tenantID, prefer)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	start := time.Now()
	output, exitCode, execErr := s.RunScriptOnAgentWithOptions(r.Context(), agent.ID, tenantID,
		req.Script, req.Params, ScriptRunOptions{
			Timeout:      timeout,
			Sandbox:      true,
			ScriptType:   req.ScriptType,
			Requirements: req.Requirements,
		})
	resp := map[string]any{
		"host":       agent.Name,
		"agent_id":   agent.ID,
		"agent_name": agent.Name,
		"output":     output,
		"exit_code":  exitCode,
		"duration":   time.Since(start).Milliseconds(),
		"via":        "sandbox-agent",
	}
	if execErr != nil {
		resp["error"] = execErr.Error()
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleGetCB(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cb := s.getOrCreateBreaker(id)
	httputil.WriteJSON(w, http.StatusOK, cb)
}

func (s *Service) handleSetCB(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		MaxHeartbeatsPerMin int  `json:"max_heartbeats_per_min"`
		MaxReqPerMin        int  `json:"max_req_per_min"`
		AutoRestart         bool `json:"auto_restart"`
		MaxConsecutiveFails int  `json:"max_consecutive_fails"`
	}
	httputil.BindJSON(r, &req)
	s.cbMu.Lock()
	cb := s.circuitBreakers[id]
	if cb == nil {
		cb = &CircuitBreakerConfig{AgentID: id}
		s.circuitBreakers[id] = cb
	}
	if req.MaxHeartbeatsPerMin > 0 {
		cb.MaxHeartbeatsPerMin = req.MaxHeartbeatsPerMin
	}
	if req.MaxReqPerMin > 0 {
		cb.MaxReqPerMin = req.MaxReqPerMin
	}
	cb.AutoRestart = req.AutoRestart
	if req.MaxConsecutiveFails > 0 {
		cb.MaxConsecutiveFails = req.MaxConsecutiveFails
	}
	s.cbMu.Unlock()
	httputil.WriteJSON(w, http.StatusOK, cb)
}

// --- Resource Guard (agent 资源自保护) ---

func (s *Service) getOrCreateRG(id string) *ResourceGuardConfig {
	s.rgMu.Lock()
	defer s.rgMu.Unlock()
	rg, ok := s.resourceGuards[id]
	if !ok {
		rg = DefaultResourceGuardConfig(id)
		s.resourceGuards[id] = rg
	}
	return rg
}

func (s *Service) handleGetRG(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rg := s.getOrCreateRG(id)
	httputil.WriteJSON(w, http.StatusOK, rg)
}

func (s *Service) handleSetRG(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Enabled          *bool `json:"enabled"`
		CPUPercent       *int  `json:"cpu_percent"`
		MemMB            *int  `json:"mem_mb"`
		CheckIntervalSec *int  `json:"check_interval_sec"`
	}
	httputil.BindJSON(r, &req)
	s.rgMu.Lock()
	rg := s.resourceGuards[id]
	if rg == nil {
		rg = DefaultResourceGuardConfig(id)
		s.resourceGuards[id] = rg
	}
	if req.Enabled != nil {
		rg.Enabled = *req.Enabled
	}
	if req.CPUPercent != nil && *req.CPUPercent >= 0 && *req.CPUPercent <= 100 {
		rg.CPUPercent = *req.CPUPercent
	}
	if req.MemMB != nil && *req.MemMB >= 0 {
		rg.MemMB = *req.MemMB
	}
	if req.CheckIntervalSec != nil && *req.CheckIntervalSec > 0 {
		rg.CheckIntervalSec = *req.CheckIntervalSec
	}
	s.rgMu.Unlock()
	httputil.WriteJSON(w, http.StatusOK, rg)
}
