package agentmgr

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/tunnel"

	"github.com/opsorigin/core/pkg/httputil"
)

// handleRestartAgent 通过下发 action=restart 任务来重启 agent,而非触发熔断。
func (s *Service) handleRestartAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := &TaskRequest{
		Action:  "restart",
		Timeout: 300,
	}
	if err := s.store.CreateTask(r.Context(), id, task); err != nil {
		slog.Warn("queue restart task failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("agent restart task queued", "agent_id", id, "task_id", task.TaskID)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":            "restart_command_queued",
		"agent_id":          id,
		"restart_requested": true,
		"task_id":           task.TaskID,
	})
}

// handleUpgradeAgent 通过下发 action=upgrade 任务来升级 agent 二进制。
func (s *Service) handleUpgradeAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 可选:从 body 读取 target_version,不指定则升级到最新
	var req struct {
		TargetVersion string `json:"target_version,omitempty"`
	}
	httputil.BindJSON(r, &req) // 忽略错误,body 可空

	task := &TaskRequest{
		Action:  "upgrade",
		Params:  map[string]any{"target_version": req.TargetVersion},
		Timeout: 600,
	}
	if err := s.store.CreateTask(r.Context(), id, task); err != nil {
		slog.Warn("queue upgrade task failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 如果 Agent 通过 Proxy 连接,尝试通过 tunnel 立即下发
	if s.tunnelServer != nil {
		if host, err := s.store.Get(r.Context(), id, auth.TenantFromContext(r.Context())); err == nil && host != nil && host.ProxyID != "" {
			payloadBytes, _ := json.Marshal(task)
			cmd := &tunnel.CommandRequest{
				CommandID:     task.TaskID,
				TargetID:      id,
				Action:        "upgrade",
				Payload:       payloadBytes,
				TimeoutSecond: 600,
			}
			s.tunnelServer.SendCommand(host.ProxyID, cmd)
		}
	}

	slog.Info("agent upgrade task queued", "agent_id", id, "task_id", task.TaskID)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "upgrade_command_queued",
		"agent_id": id,
		"task_id":  task.TaskID,
	})
}

// handleUninstallAgent 通过下发 action=uninstall 任务来卸载 agent。
// 卸载成功后,Agent 会停止服务、删除二进制和配置,然后从主机列表移除。
func (s *Service) handleUninstallAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := &TaskRequest{
		Action:  "uninstall",
		Timeout: 120,
	}
	if err := s.store.CreateTask(r.Context(), id, task); err != nil {
		slog.Warn("queue uninstall task failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 通过 Proxy 或 WS 立即下发
	if s.tunnelServer != nil {
		if host, err := s.store.Get(r.Context(), id, auth.TenantFromContext(r.Context())); err == nil && host != nil && host.ProxyID != "" {
			payloadBytes, _ := json.Marshal(task)
			cmd := &tunnel.CommandRequest{
				CommandID:     task.TaskID,
				TargetID:      id,
				Action:        "uninstall",
				Payload:       payloadBytes,
				TimeoutSecond: 120,
			}
			s.tunnelServer.SendCommand(host.ProxyID, cmd)
		}
	}

	slog.Info("agent uninstall task queued", "agent_id", id, "task_id", task.TaskID)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "uninstall_command_queued",
		"agent_id": id,
		"task_id":  task.TaskID,
	})
}

func (s *Service) getOrCreateBreaker(id string) *CircuitBreakerConfig {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()
	cb, ok := s.circuitBreakers[id]
	if !ok {
		cb = &CircuitBreakerConfig{
			AgentID:             id,
			MaxHeartbeatsPerMin: 120,
			MaxReqPerMin:        60,
			AutoRestart:         false,
			MaxConsecutiveFails: 5,
		}
		s.circuitBreakers[id] = cb
	}
	return cb
}

func (s *Service) handleCMDBReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	host, err := s.store.Get(r.Context(), id, auth.TenantFromContext(r.Context()))
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}

	var report cmdbReportPayload
	if err := httputil.BindJSON(r, &report); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Update host with CMDB data
	applyCMDBReport(host, &report)

	s.store.Update(r.Context(), host)
	// 方案 X 闭环：Agent 上报 CMDB 资产后立即同步为 CMDB host CI。
	s.syncHostCI(r.Context(), auth.TenantFromContext(r.Context()), host)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "cmdb data received"})
}

// handleGetVersionPolicy 返回全局目标版本及 Agent 合规统计。
func (s *Service) handleGetVersionPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	agents, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.versionPolicyMu.Lock()
	target := s.versionPolicy
	s.versionPolicyMu.Unlock()

	total := len(agents)
	onTarget := 0
	for _, a := range agents {
		if a.Type != "agent" {
			continue
		}
		if target != "" && a.Version == target {
			onTarget++
		}
	}
	pending := 0
	if target != "" {
		pending = total - onTarget
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"target_version": target,
		"agents_total":   total,
		"on_target":      onTarget,
		"pending":        pending,
	})
}

// handleSetVersionPolicy 设置全局目标版本。
func (s *Service) handleSetVersionPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetVersion string `json:"target_version"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s.versionPolicyMu.Lock()
	s.versionPolicy = req.TargetVersion
	s.versionPolicyMu.Unlock()

	slog.Info("agent version policy updated", "target_version", req.TargetVersion)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"status":         "ok",
		"target_version": req.TargetVersion,
	})
}

// handleBatchUpgrade 批量升级所有在线 Agent 到目标版本。
func (s *Service) handleBatchUpgrade(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	s.versionPolicyMu.Lock()
	target := s.versionPolicy
	s.versionPolicyMu.Unlock()

	if target == "" {
		httputil.WriteError(w, http.StatusBadRequest, "no target version set; use PUT /api/v1/agents/version-policy first")
		return
	}

	agents, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	upgraded := 0
	skipped := 0
	for _, a := range agents {
		if a.Type != "agent" || a.Status != "online" {
			continue
		}
		if a.Version == target {
			skipped++
			continue
		}
		task := &TaskRequest{
			Action:  "upgrade",
			Params:  map[string]any{"target_version": target},
			Timeout: 600,
		}
		if err := s.store.CreateTask(r.Context(), a.ID, task); err != nil {
			slog.Warn("batch upgrade: failed to create task", "agent_id", a.ID, "error", err)
			continue
		}
		// 尝试经 tunnel 立即下发
		if s.tunnelServer != nil && a.ProxyID != "" {
			payloadBytes, _ := json.Marshal(task)
			cmd := &tunnel.CommandRequest{
				CommandID:     uuid.NewString(),
				TargetID:      a.ID,
				Action:        "upgrade",
				Payload:       payloadBytes,
				TimeoutSecond: 600,
			}
			s.tunnelServer.SendCommand(a.ProxyID, cmd)
		}
		upgraded++
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"target_version": target,
		"upgrade_queued": upgraded,
		"skipped":        skipped,
	})
}
