package agentmgr

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/httputil"
)

// CleanupLegacySandboxHosts 清理历史遗留的"伪沙箱 Agent"记录。
//
// 旧实现(已废弃)会在 Core 启动时向 agent_hosts 注册一条 type=sandbox 的伪记录
// (名称形如 "xxx (Sandbox)"),它与 Core 同进程、不发心跳,却被列表接口特判为恒
// 在线,在 UI 上表现为一条无心跳的"幽灵主机"。
//
// 新设计:沙箱是打在**真实 Agent**上的一个标签(SandboxLabelKey),脚本在该 Agent
// 所在的目标主机上于真实隔离环境中执行。因此这里在启动时把历史伪记录删除,
// 避免继续污染主机列表。
func (s *Service) CleanupLegacySandboxHosts() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hosts, err := s.store.List(ctx, "default")
	if err != nil {
		slog.Warn("cleanup legacy sandbox hosts: list failed", "error", err)
		return
	}
	removed := 0
	for _, h := range hosts {
		if h.Type != AgentTypeSandbox {
			continue
		}
		if err := s.store.Delete(ctx, h.ID, h.TenantID); err != nil {
			slog.Warn("cleanup legacy sandbox host failed", "id", h.ID, "error", err)
			continue
		}
		removed++
		slog.Info("removed legacy pseudo sandbox agent", "id", h.ID, "name", h.Name)
	}
	if removed > 0 {
		slog.Info("legacy sandbox hosts cleaned up", "count", removed)
	}
}

// listSandboxAgents 返回本租户内所有被打上沙箱标签且在线的真实 Agent。
func (s *Service) listSandboxAgents(ctx context.Context, tenantID string) ([]*AgentHost, error) {
	hosts, err := s.store.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var onlineSet map[string]bool
	if s.hbWriter != nil && s.hbWriter.rdb != nil {
		ids := make([]string, 0, len(hosts))
		for _, h := range hosts {
			ids = append(ids, h.ID)
		}
		onlineSet = s.hbWriter.BulkOnlineStatus(ctx, ids)
	}
	now := time.Now()
	out := make([]*AgentHost, 0, 4)
	for _, h := range hosts {
		if !h.IsSandbox() {
			continue
		}
		copyHost := *h
		if onlineSet != nil {
			if onlineSet[h.ID] {
				copyHost.Status = AgentStatusOnline
			} else {
				copyHost.Status = AgentStatusOffline
			}
		} else if copyHost.LastHeartbeat != nil && now.Sub(*copyHost.LastHeartbeat) > OfflineThreshold {
			copyHost.Status = AgentStatusOffline
		}
		out = append(out, &copyHost)
	}
	return out, nil
}

// pickSandboxAgent 选择一个可用的沙箱 Agent。
// preferID 非空时校验其确实带沙箱标签且在线;否则取第一个在线的沙箱 Agent。
func (s *Service) pickSandboxAgent(ctx context.Context, tenantID, preferID string) (*AgentHost, error) {
	agents, err := s.listSandboxAgents(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if preferID != "" {
		for _, a := range agents {
			if a.ID == preferID {
				if a.Status != AgentStatusOnline {
					return nil, fmt.Errorf("指定的沙箱 Agent %s 当前离线", preferID)
				}
				return a, nil
			}
		}
		return nil, fmt.Errorf("Agent %s 未被标记为沙箱环境,请先在主机列表中为其开启沙箱标签", preferID)
	}
	for _, a := range agents {
		if a.Status == AgentStatusOnline {
			return a, nil
		}
	}
	return nil, fmt.Errorf("没有可用的沙箱 Agent:请先在主机列表中为部署 Server 的主机 Agent 开启沙箱标签,并确保其在线")
}

// handleListSandboxAgents 返回所有被打上沙箱标签的真实 Agent。
// 前端"沙箱环境执行"下拉框据此渲染可选环境。
func (s *Service) handleListSandboxAgents(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	agents, err := s.listSandboxAgents(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	online := 0
	for _, a := range agents {
		if a.Status == AgentStatusOnline {
			online++
		}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"exists": online > 0,
		"count":  len(agents),
		"online": online,
		"agents": agents,
	})
}

// handleSetSandboxLabel 给指定的真实 Agent 打上/摘掉"沙箱环境"标签。
// 这取代了旧的"Core 进程内注册伪沙箱 Agent"做法:沙箱不是一台新主机,
// 而是既有 Agent 上的一个能力标记。
// handleSetSandboxLabel 设置/取消 Agent 的沙箱标签(PUT /agents/{id}/sandbox)。
func (s *Service) handleSetSandboxLabel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	if host.Type == AgentTypeAgentless {
		httputil.WriteError(w, http.StatusBadRequest, "Agentless 主机无本地 Agent,无法承载隔离沙箱环境")
		return
	}
	host.SetSandbox(req.Enabled)
	host.UpdatedAt = time.Now()
	if err := s.store.Update(r.Context(), host); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("sandbox label updated", "agent_id", id, "enabled", req.Enabled)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "sandbox": req.Enabled})
}

// ApplySandboxPool 把"Server 所在主机"的 Agent 自动标记为沙箱环境(默认沙箱池)。
// 匹配规则:
//   - 主机名(hostname)等于本机主机名的 Agent;
//   - Agent ID 命中环境变量 OPSORIGIN_SANDBOX_AGENTS(逗号分隔)列表。
//
// 可通过 OPSORIGIN_SANDBOX_AUTO=0 关闭自动标记。
func (s *Service) ApplySandboxPool(ctx context.Context) {
	if os.Getenv("OPSORIGIN_SANDBOX_AUTO") == "0" {
		return
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = ""
	}
	cfgSet := map[string]bool{}
	if v := os.Getenv("OPSORIGIN_SANDBOX_AGENTS"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfgSet[p] = true
			}
		}
	}
	agents, err := s.store.List(ctx, "default")
	if err != nil {
		slog.Warn("sandbox pool: list agents failed", "err", err)
		return
	}
	marked := 0
	for _, a := range agents {
		if a.Type == AgentTypeAgentless {
			continue
		}
		match := cfgSet[a.ID]
		if hostname != "" && (a.Hostname == hostname || a.Name == hostname) {
			match = true
		}
		if match && !a.IsSandbox() {
			a.SetSandbox(true)
			a.UpdatedAt = time.Now()
			if err := s.store.Update(ctx, a); err == nil {
				marked++
				slog.Info("sandbox pool: marked agent as sandbox", "agent_id", a.ID, "hostname", a.Hostname)
			}
		}
	}
	if marked > 0 {
		slog.Info("sandbox pool applied", "marked", marked)
	}
}
