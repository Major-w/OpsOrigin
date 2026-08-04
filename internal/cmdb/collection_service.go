package cmdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

// RegisterCollector 注册一个采集器（kind × resourceType → Collector）。重复注册覆盖。
func (s *Service) RegisterCollector(kind, resourceType string, c Collector) {
	s.registry.Register(kind, resourceType, c)
}

// AgentChannel 是「经 Agent 通道上报」任务的下发接口（方案 X）。
// cmdb 作为统一调度者持有 channel=agent 的采集任务，但采集动作由 Agent 本地执行并 push 上报；
// 因此 CMDB 需要把这类任务以「下发投影」的形式交给 Agent 传输层（agentmgr）推送到目标 Agent。
// 为避免 cmdb ↔ agentmgr 循环依赖，此接口由 cmd/core 用适配器实现。
type AgentChannel interface {
	// SyncAgentCollectTask 把一条 cmdb 采集任务同步为 Agent 侧采集任务（新增/更新 + 推送）。
	SyncAgentCollectTask(ctx context.Context, spec AgentCollectTaskSpec) error
	// RemoveAgentCollectTask 删除对应 Agent 侧采集任务（并推送空清单移除）。
	RemoveAgentCollectTask(ctx context.Context, cmdbTaskID string) error
}

// AgentCollectTaskSpec 是同步给 Agent 通道的任务描述（cmdb 任务 → agentmgr 投影的入参）。
type AgentCollectTaskSpec struct {
	CmdbTaskID  string // cmdb 采集任务 ID（作为投影键）
	TenantID    string
	Kind        string // cmdb | monitor（映射到 agentmgr 的 cmdb | metric）
	Target      string // 指向纳管资源，如 "host:<agentID>"
	IntervalSec int    // 采集周期（秒）
	Enabled     bool
}

// SetAgentChannel 注入 Agent 通道下发实现（由 cmd/core 用 agentmgr 适配器注入）。
func (s *Service) SetAgentChannel(ch AgentChannel) {
	s.agentChan = ch
}

// SetDiscoveryRegistry 注入自动发现插件注册表（cmd/core 启动时由 discovery/plugins.NewRegistry() 构建）。
func (s *Service) SetDiscoveryRegistry(reg *discovery.Registry) {
	s.discoveryReg = reg
}

// SetDiscoveryScriptRunner 注入探针 Agent 脚本执行通道（cmd/core 把 agentSvc 注入，鸭子实现 ScriptRunner 接口）。
func (s *Service) SetDiscoveryScriptRunner(runner discovery.ScriptRunner) {
	s.discoveryRunner = runner
}

// toAgentSpec 把 cmdb 采集任务转换为 Agent 通道同步描述。
func (s *Service) toAgentSpec(t *model.CollectionTask) AgentCollectTaskSpec {
	return AgentCollectTaskSpec{
		CmdbTaskID:  t.ID,
		TenantID:    t.TenantID,
		Kind:        t.Kind,
		Target:      t.Target,
		IntervalSec: int(parseFrequency(t.Frequency).Seconds()),
		Enabled:     t.Enabled,
	}
}

// SetMetricSink 注入监控指标落库回调（由 cmd/core 把采集到的指标路由到监控模块）。
func (s *Service) SetMetricSink(fn func(point MetricPoint)) {
	s.onMetric = fn
}

// StartScheduler 启动采集调度器（注册完采集器后调用）。
func (s *Service) StartScheduler(ctx context.Context) {
	s.sched = NewScheduler(s, 30*time.Second)
	s.sched.Start(ctx)
}

// StopScheduler 停止调度器并等待在途任务结束。
func (s *Service) StopScheduler() {
	if s.sched != nil {
		s.sched.Stop()
	}
}

// listAllEnabledTasks 返回全部启用的采集任务（供调度器扫描）。
// PG 不可用时（stub 模式）返回空，避免调度器空指针 panic，与代码库其余
// `if s.pg != nil` 守护风格保持一致。
func (s *Service) listAllEnabledTasks(ctx context.Context) ([]*model.CollectionTask, error) {
	if s.pg == nil {
		return nil, nil
	}
	return s.pg.ListAllEnabledCollectionTasks(ctx)
}

// OnboardResource 纳管资源时调用：依据平台默认策略与逐资源覆盖开关，
// 创建对应的 cmdb / monitor 采集任务（已存在则跳过）。
//   - enableCMDB / enableMonitor 为 nil 时回退到平台全局默认；非 nil 表示纳管界面显式覆盖。
func (s *Service) OnboardResource(ctx context.Context, tenantID, resourceType, target string, enableCMDB, enableMonitor *bool) error {
	policy, err := s.GetPlatformPolicy(ctx, tenantID)
	if err != nil {
		return err
	}
	// 方案 X：CMDB 是统一调度者。
	//   - 云资源由 CMDB 服务端直接拉取(channel=server，复用 cloud 采集器)；
	//   - 主机/容器经 Agent 通道推送上报(channel=agent，由 Agent 本地采集后 push)。
	channel := "server"
	if resourceType != "cloud" {
		channel = "agent"
	}
	if resolveSwitch(enableCMDB, policy.DefaultCMDBEnabled) {
		if err := s.ensureCollectionTask(ctx, tenantID, resourceType, target, "cmdb", policy.CMDBDefaultFrequency, channel); err != nil {
			return err
		}
	}
	if resolveSwitch(enableMonitor, policy.DefaultMonitorEnabled) {
		if err := s.ensureCollectionTask(ctx, tenantID, resourceType, target, "monitor", policy.MonitorDefaultFrequency, channel); err != nil {
			return err
		}
	}
	return nil
}

func resolveSwitch(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

// ensureCollectionTask 若同 target+kind 任务不存在则创建。
func (s *Service) ensureCollectionTask(ctx context.Context, tenantID, resourceType, target, kind, frequency, channel string) error {
	tasks, err := s.pg.ListCollectionTasks(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Target == target && t.Kind == kind {
			return nil
		}
	}
	t := &model.CollectionTask{
		TenantID:     tenantID,
		Name:         fmt.Sprintf("%s %s collection", resourceType, kind),
		ResourceType: resourceType,
		Target:       target,
		Kind:         kind,
		Channel:      channel,
		Frequency:    frequency,
		Enabled:      true,
	}
	// 走 Service 层 CreateCollectionTask(而非直接 pg),确保 channel=agent 的任务
	// 在创建时经 AgentChannel 同步为 Agent 侧投影(方案 X:主机/容器纳管即建采集任务)。
	return s.CreateCollectionTask(ctx, t)
}

// GetPlatformPolicy 读取平台默认策略（不存在返回内置默认，不写库）。
func (s *Service) GetPlatformPolicy(ctx context.Context, tenantID string) (*model.PlatformPolicy, error) {
	return s.pg.GetPlatformPolicy(ctx, tenantID)
}

// SavePlatformPolicy 写入（upsert）平台默认策略。
func (s *Service) SavePlatformPolicy(ctx context.Context, p *model.PlatformPolicy) error {
	return s.pg.SavePlatformPolicy(ctx, p)
}

// CreateCollectionTask 新建采集任务。
// channel=agent 的任务（主机/容器）会同步为 Agent 侧采集任务（投影到 agentmgr 下发）。
func (s *Service) CreateCollectionTask(ctx context.Context, t *model.CollectionTask) error {
	if t.Channel == "" {
		t.Channel = "server"
	}
	if err := s.pg.CreateCollectionTask(ctx, t); err != nil {
		return err
	}
	if t.Channel == "agent" && s.agentChan != nil {
		if err := s.agentChan.SyncAgentCollectTask(ctx, s.toAgentSpec(t)); err != nil {
			slog.Warn("sync agent collect task failed", "task", t.ID, "err", err)
		}
	}
	return nil
}

// GetCollectionTask 读取采集任务。
func (s *Service) GetCollectionTask(ctx context.Context, id, tenantID string) (*model.CollectionTask, error) {
	return s.pg.GetCollectionTask(ctx, id, tenantID)
}

// ListCollectionTasks 列出某租户采集任务。
func (s *Service) ListCollectionTasks(ctx context.Context, tenantID string) ([]*model.CollectionTask, error) {
	return s.pg.ListCollectionTasks(ctx, tenantID)
}

// UpdateCollectionTask 更新采集任务（启停 / 频率 / 通道切换）。
// channel 由 server 变为 agent 时同步投影；agent→server 时移除投影。
func (s *Service) UpdateCollectionTask(ctx context.Context, t *model.CollectionTask) error {
	if err := s.pg.UpdateCollectionTask(ctx, t); err != nil {
		return err
	}
	if s.agentChan != nil {
		if t.Channel == "agent" {
			if err := s.agentChan.SyncAgentCollectTask(ctx, s.toAgentSpec(t)); err != nil {
				slog.Warn("sync agent collect task failed", "task", t.ID, "err", err)
			}
		} else {
			if err := s.agentChan.RemoveAgentCollectTask(ctx, t.ID); err != nil {
				slog.Warn("remove agent collect task failed", "task", t.ID, "err", err)
			}
		}
	}
	return nil
}

// DeleteCollectionTask 删除采集任务（如有 Agent 投影一并移除）。
func (s *Service) DeleteCollectionTask(ctx context.Context, id, tenantID string) error {
	if s.agentChan != nil {
		if err := s.agentChan.RemoveAgentCollectTask(ctx, id); err != nil {
			slog.Warn("remove agent collect task failed", "task", id, "err", err)
		}
	}
	return s.pg.DeleteCollectionTask(ctx, id, tenantID)
}

// markTaskSuccess / markTaskFailed / markTaskPending 更新任务调度状态。
func (s *Service) markTaskSuccess(ctx context.Context, t *model.CollectionTask) {
	now := time.Now()
	t.LastRunAt = &now
	t.Status = "success"
	t.Error = ""
	_ = s.pg.TouchCollectionTask(ctx, t.ID, t.LastRunAt, t.Status, "")
}

func (s *Service) markTaskFailed(ctx context.Context, t *model.CollectionTask, msg string) {
	now := time.Now()
	t.LastRunAt = &now
	t.Status = "failed"
	t.Error = msg
	_ = s.pg.TouchCollectionTask(ctx, t.ID, t.LastRunAt, t.Status, msg)
}

func (s *Service) markTaskPending(ctx context.Context, t *model.CollectionTask, msg string) {
	t.Status = "pending"
	t.Error = msg
	_ = s.pg.TouchCollectionTask(ctx, t.ID, t.LastRunAt, t.Status, msg)
}

// markTaskDelegated 标记任务为「已委派 Agent」：采集动作由 Agent 本地执行并 push 上报，
// server 侧不主动拉取。区别于 pending（采集器缺失/配置错误），delegated 是预期的正常状态。
func (s *Service) markTaskDelegated(ctx context.Context, t *model.CollectionTask) {
	now := time.Now()
	t.LastRunAt = &now
	t.Status = "delegated"
	t.Error = ""
	_ = s.pg.TouchCollectionTask(ctx, t.ID, t.LastRunAt, t.Status, "")
}

// runCollectionTaskResolved 执行采集任务，对动态目标（query 模式）按保存查询实时解析
// 命中 CI 集合后逐资源下发采集；静态目标直接执行。返回命中目标数 / 落库 CI 数 / 指标数。
// 供手动触发（handleRunCollectionTask）与调度器（scheduler.runOne）共用，避免逻辑分叉。
func (s *Service) runCollectionTaskResolved(ctx context.Context, t *model.CollectionTask, tenantID string) (resolved, cis, metrics int, err error) {
	if t.TargetMode != "query" || t.TargetQuery == "" {
		c, ok := s.registry.Lookup(t.Kind, t.ResourceType)
		if !ok {
			return 0, 0, 0, fmt.Errorf("no collector registered for %s", collectorKey(t.Kind, t.ResourceType))
		}
		out, e := c.Run(ctx, t)
		if e != nil {
			return 0, 0, 0, e
		}
		if e := s.ingestCollectionOutput(ctx, t, out); e != nil {
			return 0, 0, 0, e
		}
		return 1, len(out.CIs), len(out.Metrics), nil
	}

	targets, rerr := s.ResolveSavedQueryTargetIDs(ctx, tenantID, t.TargetQuery)
	if rerr != nil {
		return 0, 0, 0, rerr
	}
	for _, res := range targets {
		clone := *t
		clone.Target = res
		clone.TargetMode = "static"
		cc, cok := s.registry.Lookup(clone.Kind, clone.ResourceType)
		if !cok {
			slog.Warn("skip resolved target: no collector", "target", res, "kind", clone.Kind)
			continue
		}
		out, e := cc.Run(ctx, &clone)
		if e != nil {
			slog.Warn("collection failed for resolved target", "target", res, "err", e)
			continue
		}
		if e := s.ingestCollectionOutput(ctx, &clone, out); e != nil {
			slog.Warn("ingest failed for resolved target", "target", res, "err", e)
			continue
		}
		cis += len(out.CIs)
		metrics += len(out.Metrics)
	}
	return len(targets), cis, metrics, nil
}

// ingestCollectionOutput 按 Kind 路由落库：cmdb→CI upsert，monitor→指标回调。
func (s *Service) ingestCollectionOutput(ctx context.Context, t *model.CollectionTask, out *CollectionOutput) error {
	switch t.Kind {
	case "cmdb":
		if len(out.CIs) > 0 {
			applyCollectionMeta(out.CIs, t) // 按采集任务推导 source，逐字段写入来源/置信度
			if err := s.upsertCIsByResourceID(ctx, t.TenantID, out.CIs); err != nil {
				return err
			}
		}
	case "monitor":
		if s.onMetric != nil {
			for _, m := range out.Metrics {
				s.onMetric(m)
			}
		}
	default:
		slog.Warn("unknown collection kind, skipped", "kind", t.Kind, "task", t.ID)
	}
	return nil
}

// applyCollectionMeta 为采集产出的 CI 逐字段写入来源/置信度元数据（字段级，平行存储）。
// source 由采集任务推导：经 Agent 通道上报 → agent；服务端拉取（当前仅云） → cloud_api。
// 若采集器已显式在 AttributeMeta 中上报（未来扩展点），则跳过兜底，允许其覆盖默认权重。
// 人工编辑路径不走这里，由 repo 按 ChannelWeight 兜底标记为 manual。
func applyCollectionMeta(cis []*model.CIModel, t *model.CollectionTask) {
	src := model.SourceCloudAPI
	if t.Channel == "agent" {
		src = model.SourceAgent
	}
	now := time.Now()
	for _, ci := range cis {
		if ci.AttributeMeta == nil {
			ci.AttributeMeta = make(map[string]model.AttrMeta)
		}
		for k := range ci.Attributes {
			if _, ok := ci.AttributeMeta[k]; ok {
				continue // 采集器已显式上报，跳过兜底
			}
			ci.AttributeMeta[k] = model.AttrMeta{
				Source:     src,
				Confidence: model.DefaultConfidence(src),
				LastSeenAt: now,
			}
		}
	}
}

// upsertCIsByResourceID 按 resource_id 幂等 upsert CI（复用 CreateCI/UpdateCI 渠道权重）。
func (s *Service) upsertCIsByResourceID(ctx context.Context, tenantID string, cis []*model.CIModel) error {
	byType := make(map[string][]*model.CIModel, 8)
	for _, ci := range cis {
		byType[ci.TypeID] = append(byType[ci.TypeID], ci)
	}
	const traceID = "collection"
	for typeID, list := range byType {
		existing := make(map[string]*model.CIModel)
		if s.pg != nil {
			cur, err := s.pg.ListCI(ctx, tenantID, typeID)
			if err != nil {
				return fmt.Errorf("list CI for %s: %w", typeID, err)
			}
			for _, c := range cur {
				if c.Attributes == nil {
					continue
				}
				if rid, ok := c.Attributes["resource_id"].(string); ok && rid != "" {
					existing[rid] = c
				}
			}
		}
		for _, ci := range list {
			rid, _ := ci.Attributes["resource_id"].(string)
			if prev, ok := existing[rid]; ok && rid != "" {
				ci.ID = prev.ID
				if err := s.UpdateCI(ctx, ci, model.WeightDiscovery, traceID); err != nil {
					if errors.Is(err, pg.ErrAntiFlapping) {
						// 人工改过的 CI 不被自动采集覆盖 → 跳过。
						continue
					}
					return fmt.Errorf("update CI %s: %w", rid, err)
				}
			} else {
				if err := s.CreateCI(ctx, ci, traceID); err != nil {
					return fmt.Errorf("create CI %s: %w", rid, err)
				}
			}
		}
	}
	return nil
}
