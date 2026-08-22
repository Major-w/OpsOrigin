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

// CollectReportSink 把采集脚本最终输出的统一格式（collect/report：model_id-字段ID）
// 泛化 upsert 到 CMDB 实例（属性 / 关系，不存在则新增）。
//
// 设计动机：此前 cmdb 型采集脚本输出是硬编码的扁平 host 资产（hostname/os/cpu...），
// 只更新 host CI，无法按「模型 ID - 字段 ID」填充任意模型实例。
// 现统一规范：脚本最终输出符合 collect/report 格式的 JSON（单条或 {reports:[...]}），
// 经本 sink 泛化落库。内置发现插件脚本也沿用此契约，统一收口到一处落库逻辑。
//
// agentmgr 不直接依赖 cmdb 包（避免循环依赖），由 cmd/core 把 cmdb.Service
// （已实现 ApplyCollectReports）作为鸭子类型注入。
type CollectReportSink interface {
	// ApplyCollectReports 接收已解析的 report 列表（每个为单条 report 的通用 map），
	// 对每条按 model_id-字段ID upsert 到对应模型实例。返回成功落库条数与错误。
	ApplyCollectReports(ctx context.Context, tenantID string, reports []map[string]any) (applied int, err error)
}

// SetCollectReportSink 注入采集脚本输出 → CMDB 实例的泛化落库实现（cmd/core 注入 cmdb.Service）。
func (s *Service) SetCollectReportSink(sink CollectReportSink) {
	s.collectReportSink = sink
}
