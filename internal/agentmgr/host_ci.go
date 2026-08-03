package agentmgr

import (
	"context"
	"log/slog"
)

// HostCIWriter 把 Agent 纳管的主机资产同步为 CMDB 的 host CI（方案 X 闭环：agent_hosts → CMDB 最后一跳）。
//
// 为何需要：此前主机/容器经 Agent 采集的资产只落在 agent_hosts 表，从没进 CMDB。
// 这与云路径（cloudCMDBAdapter 把云资源 SyncResources 进 CMDB）不对称——本接口补齐 agent 侧的等价落库，
// 使「纳管 → 采集 → CMDB 落库」对主机/容器同样闭环。
//
// agentmgr 不直接依赖 cmdb 包（避免循环依赖 / 保持依赖单向），由 cmd/core 用 agentCMDBAdapter 实现本接口。
type HostCIWriter interface {
	// SyncHost 把一条 AgentHost 资产幂等同步为 CMDB 的 host 类型 CI。
	// 返回 (created, updated, err)。err 非 nil 表示落库失败；anti-flapping 保护的跳过计为成功。
	SyncHost(ctx context.Context, tenantID string, host *AgentHost) (created, updated int, err error)
}

// SetHostCIWriter 注入 agent_hosts → CMDB CI 的同步实现（cmd/core 用 agentCMDBAdapter 注入）。
func (s *Service) SetHostCIWriter(w HostCIWriter) {
	s.hostCIWriter = w
}

// syncHostCI 在主机资产变化后（注册 / CMDB 上报 / 采集完成）触发 CMDB CI 同步。
// hostCIWriter 未注入时静默跳过，不阻塞主流程。
func (s *Service) syncHostCI(ctx context.Context, tenantID string, host *AgentHost) {
	if s.hostCIWriter == nil {
		return
	}
	if _, _, err := s.hostCIWriter.SyncHost(ctx, tenantID, host); err != nil {
		slog.Warn("agent: sync host ci to cmdb failed", "agent", host.ID, "tenant", tenantID, "err", err)
	}
}
