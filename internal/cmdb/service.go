package cmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/cmdb/quality"
	"github.com/opsorigin/core/internal/cmdb/registry"
	"github.com/opsorigin/core/internal/ingest"
	"github.com/opsorigin/core/pkg/config"

	"github.com/opsorigin/core/pkg/httputil"
)

type Service struct {
	cfg         *config.Config
	pg          *pg.Repository
	dg          *dgraph.Client
	mem         *memStore        // PG/Dgraph 不可用时的内存回退
	ing         *ingest.Ingestor // 异步批量落库器(F4-B)
	onCICreated func(ctx context.Context, ciID, tenantID string)

	registry  *collectorRegistry      // 采集器注册表（kind × resourceType → Collector）
	onMetric  func(point MetricPoint) // 监控指标落库回调（由 cmd/core 注入，路由到监控模块）
	sched     *Scheduler              // 采集调度器（StartScheduler 后非 nil）
	agentChan AgentChannel            // 经 Agent 通道上报的任务下发接口（方案 X：投影到 agentmgr）
	notifyCb  notifyFunc              // 平台通知中心回调（P2-5 消息订阅，cmd/core 注入）

	discoveryReg    *discovery.Registry    // 自动发现插件注册表（cmd/core 注入，Type → Discoverer）
	discoveryRunner discovery.ScriptRunner // 探针 Agent 脚本执行通道（cmd/core 注入 agentSvc）

	// monitor 是全景拓扑的监控快照提供者（cmd/core 注入 monitor.Service 适配器），nil 安全。
	monitor model.MonitorSnapshotProvider

	// typeRegistry 是 AI 模型注册表 / 本体服务（cmd/core 注入 registry.New(pgRepo)），nil 安全。
	typeRegistry registry.TypeRegistry

	// usage 是 CMDB 数据消费分析采集器（EnableUsageAnalytics 后非 nil）。
	usage *usageRecorder
}

func NewService(cfg *config.Config, pgRepo *pg.Repository, dgClient *dgraph.Client) *Service {
	s := &Service{
		cfg: cfg,
		pg:  pgRepo,
		dg:  dgClient,
		mem: newMemStore(),

		registry: newCollectorRegistry(),
	}
	// F4-B 决策 B: 批量导入异步化。提交方(如 handleBatchCreateCI)只 Submit,
	// 后台每 500ms 或缓冲满时把一批 CI 交给 flushFn 落库, 不阻塞调用方。
	flushFn := func(ctx context.Context, items []ingest.Item) {
		cis := make([]*model.CIModel, 0, len(items))
		for _, it := range items {
			if ci, ok := it.(*model.CIModel); ok {
				cis = append(cis, ci)
			}
		}
		if len(cis) == 0 {
			return
		}
		if s.pg != nil {
			if err := s.pg.BulkCreateCI(ctx, cis, "ingest-batch"); err != nil {
				slog.Error("bulk create ci failed", "error", err)
				return
			}
		} else {
			for _, ci := range cis {
				s.mem.createCI(ci)
			}
		}
		if s.onCICreated != nil {
			for _, ci := range cis {
				s.onCICreated(ctx, ci.ID, ci.TenantID)
			}
		}
	}
	s.ing = ingest.New(1000, 500*time.Millisecond, flushFn)
	go s.ing.Start(context.Background())
	return s
}

func (s *Service) OnCICreated(fn func(ctx context.Context, ciID, tenantID string)) {
	s.onCICreated = fn
}

// SetMonitorSnapshotProvider 注入全景拓扑所需的监控快照提供者（monitor.Service 适配器）。
// 未注入时全景图仅呈现 CMDB 拓扑骨架，节点健康度全部为 unknown（nil 安全）。
func (s *Service) SetMonitorSnapshotProvider(p model.MonitorSnapshotProvider) {
	s.monitor = p
}

// SetTypeRegistry 注入 AI 模型注册表 / 本体服务（设计文档阶段 1）。
// 未注入时相关端点直接返回 501，不影响其它能力。
func (s *Service) SetTypeRegistry(r registry.TypeRegistry) {
	s.typeRegistry = r
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/cmdb/ci", s.handleListCI)
	mux.HandleFunc("GET /api/v1/cmdb/ci/export", s.handleExportCI)  // P1-1 CSV 导出
	mux.HandleFunc("POST /api/v1/cmdb/ci/import", s.handleImportCI) // P1-1 CSV 导入
	mux.HandleFunc("POST /api/v1/cmdb/ci", s.handleCreateCI)
	mux.HandleFunc("POST /api/v1/cmdb/ci/batch", s.handleBatchCreateCI)        // F4-B 批量 + D2 修复
	mux.HandleFunc("POST /api/v1/cmdb/ci/batch-update", s.handleBatchUpdateCI) // 批量改属性
	mux.HandleFunc("POST /api/v1/cmdb/ci/batch-delete", s.handleBatchDeleteCI) // 批量删除
	// 自动采集上报：按 模型ID-字段ID/关系ID 定位写入(供 Agent/采集器调用, 与人工建实例解耦)
	mux.HandleFunc("POST /api/v1/cmdb/collect/report", s.handleCollectReport)
	mux.HandleFunc("GET /api/v1/cmdb/ci/{id}", s.handleGetCI)
	mux.HandleFunc("PUT /api/v1/cmdb/ci/{id}", s.handleUpdateCI)
	mux.HandleFunc("DELETE /api/v1/cmdb/ci/{id}", s.handleDeleteCI)
	mux.HandleFunc("POST /api/v1/cmdb/ci/{id}/archive", s.handleArchiveCI) // P1-2 归档
	mux.HandleFunc("POST /api/v1/cmdb/ci/{id}/restore", s.handleRestoreCI) // P1-2 恢复
	mux.HandleFunc("GET /api/v1/cmdb/ci/{id}/audit", s.handleCIAudit)
	mux.HandleFunc("GET /api/v1/cmdb/search", s.handleSearchCI)
	// 跨模型关联查询(综合查询): 沿关系链分层展开
	mux.HandleFunc("POST /api/v1/cmdb/query/chain", s.handleChainQuery)
	mux.HandleFunc("POST /api/v1/cmdb/relation/batch", s.handleBatchCreateRelation) // 批量建关系
	// 实体调解合并(IRE): 疑似重复识别 + 按渠道置信度合并
	mux.HandleFunc("GET /api/v1/cmdb/reconcile/candidates", s.handleReconcileCandidates)
	mux.HandleFunc("POST /api/v1/cmdb/reconcile/merge", s.handleReconcileMerge)
	// 行级数据权限(角色 → 模型 → 属性条件)
	mux.HandleFunc("GET /api/v1/cmdb/data-policies", s.handleListDataPolicies)
	mux.HandleFunc("POST /api/v1/cmdb/data-policies", s.handleSaveDataPolicy)
	mux.HandleFunc("DELETE /api/v1/cmdb/data-policies/{id}", s.handleDeleteDataPolicy)
	// 配置漂移: 发现值 vs CMDB 期望值
	mux.HandleFunc("GET /api/v1/cmdb/drift", s.handleListDrifts)
	mux.HandleFunc("GET /api/v1/cmdb/idc/panorama", s.handleIDCPanorama) // 机房机柜 2D 数据
	mux.HandleFunc("POST /api/v1/cmdb/drift/{id}/resolve", s.handleResolveDrift)

	// CMDB 基线快照 + 环境漂移对比。
	mux.HandleFunc("POST /api/v1/cmdb/snapshots", s.handleCaptureSnapshot)
	mux.HandleFunc("GET /api/v1/cmdb/snapshots", s.handleListSnapshots)
	mux.HandleFunc("POST /api/v1/cmdb/snapshots/compare", s.handleCompareSnapshots)
	mux.HandleFunc("GET /api/v1/cmdb/snapshots/{id}", s.handleGetSnapshot)
	mux.HandleFunc("DELETE /api/v1/cmdb/snapshots/{id}", s.handleDeleteSnapshot)

	// 数据消费分析：CMDB API 被调用次数 / 热点数据 / 调用错误频率。
	// 采集器未启用（无 PG 后端）时各端点返回 501。
	mux.HandleFunc("GET /api/v1/cmdb/usage/summary", s.handleUsageSummary)
	mux.HandleFunc("GET /api/v1/cmdb/usage/hotspots", s.handleUsageHotspots)
	mux.HandleFunc("GET /api/v1/cmdb/usage/errors", s.handleUsageErrors)
	mux.HandleFunc("GET /api/v1/cmdb/usage/status", s.handleUsageStatus)

	// 保存的查询策略（"我想查"，P1-3）
	mux.HandleFunc("GET /api/v1/cmdb/saved-queries", s.handleListSavedQueries)
	mux.HandleFunc("POST /api/v1/cmdb/saved-queries", s.handleCreateSavedQuery)
	mux.HandleFunc("GET /api/v1/cmdb/saved-queries/{id}/resolve", s.handleResolveSavedQuery)
	mux.HandleFunc("DELETE /api/v1/cmdb/saved-queries/{id}", s.handleDeleteSavedQuery)

	mux.HandleFunc("GET /api/v1/cmdb/topology/{id}", s.handleGetTopology)
	// 条件子图：按模型/环境/业务/关键字取子图，供依赖矩阵与大图场景使用（不再只有单根邻域）
	mux.HandleFunc("GET /api/v1/cmdb/subgraph", s.handleGetSubgraph)
	mux.HandleFunc("GET /api/v1/cmdb/impact/{id}", s.handleImpactAnalysis)
	// 任意两 CI 间最短依赖路径查询(无向 BFS, 沿关系链)。
	mux.HandleFunc("GET /api/v1/cmdb/path", s.handlePathQuery)
	// 全景可观测拓扑（CMDB 拓扑 + 监控状态 + 告警 rollup 融合）：
	// 纵向骨架 + 状态/告警叠加，调用链横向边按 layers 参数逐步启用。
	mux.HandleFunc("GET /api/v1/panorama", s.handleGetPanorama)

	mux.HandleFunc("GET /api/v1/cmdb/relation", s.handleListRelations)
	mux.HandleFunc("POST /api/v1/cmdb/relation", s.handleCreateRelation)
	mux.HandleFunc("DELETE /api/v1/cmdb/relation/{id}", s.handleDeleteRelation)

	// 采集器矩阵（前端露出采集能力覆盖）
	mux.HandleFunc("GET /api/v1/cmdb/collectors", s.handleListCollectors)

	mux.HandleFunc("POST /api/v1/cmdb/model-type", s.handleCreateModelType)
	mux.HandleFunc("GET /api/v1/cmdb/model-type", s.handleListModelTypes)
	mux.HandleFunc("GET /api/v1/cmdb/model-type/{id}", s.handleGetModelType)
	mux.HandleFunc("GET /api/v1/cmdb/model-type/{id}/field-usage", s.handleFieldUsage)
	mux.HandleFunc("GET /api/v1/cmdb/model-type/{id}/ci-field-usage", s.handleCIFieldUsage)
	mux.HandleFunc("PUT /api/v1/cmdb/model-type/{id}", s.handleUpdateModelType)
	mux.HandleFunc("DELETE /api/v1/cmdb/model-type/{id}", s.handleDeleteModelType)
	mux.HandleFunc("GET /api/v1/cmdb/model-type/{id}/versions", s.handleListModelTypeVersions)
	mux.HandleFunc("POST /api/v1/cmdb/model-type/{id}/rollback/{version}", s.handleRollbackModelType)
	mux.HandleFunc("GET /api/v1/cmdb/model-type/export", s.handleExportModelTypes)
	mux.HandleFunc("POST /api/v1/cmdb/model-type/import", s.handleImportModelTypes)

	// 模型分类（内置 5 类 + 用户自定义，可扩展）
	mux.HandleFunc("GET /api/v1/cmdb/model-categories", s.handleListModelCategories)
	mux.HandleFunc("POST /api/v1/cmdb/model-categories", s.handleCreateModelCategory)
	mux.HandleFunc("DELETE /api/v1/cmdb/model-categories/{id}", s.handleDeleteModelCategory)

	// P2-1 统计大屏
	mux.HandleFunc("GET /api/v1/cmdb/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/cmdb/dashboard", s.handleGetDashboard)
	mux.HandleFunc("PUT /api/v1/cmdb/dashboard", s.handleSaveDashboard)
	mux.HandleFunc("GET /api/v1/cmdb/dashboard/insights", s.handleDashboardInsights)

	// P2-2 机房机柜（IDC / Rack / U 位）
	mux.HandleFunc("GET /api/v1/cmdb/idc", s.handleListIDC)
	mux.HandleFunc("POST /api/v1/cmdb/idc", s.handleCreateIDC)
	mux.HandleFunc("DELETE /api/v1/cmdb/idc/{id}", s.handleDeleteIDC)
	mux.HandleFunc("GET /api/v1/cmdb/idc/{idcId}/racks", s.handleListRacks)
	mux.HandleFunc("POST /api/v1/cmdb/idc/{idcId}/racks", s.handleCreateRack)
	mux.HandleFunc("DELETE /api/v1/cmdb/racks/{id}", s.handleDeleteRack)
	mux.HandleFunc("GET /api/v1/cmdb/racks/{id}/units", s.handleGetRackUnits)
	mux.HandleFunc("POST /api/v1/cmdb/racks/{id}/units", s.handleAssignRackUnits)

	// P2-3 应用架构拓扑（业务/分层视图数据）
	mux.HandleFunc("GET /api/v1/cmdb/app-topology", s.handleAppTopology)

	// P2-4 字段级规则权限
	mux.HandleFunc("GET /api/v1/cmdb/field-rules", s.handleListFieldRules)
	mux.HandleFunc("POST /api/v1/cmdb/field-rules", s.handleCreateFieldRule)
	mux.HandleFunc("DELETE /api/v1/cmdb/field-rules/{id}", s.handleDeleteFieldRule)

	// P2-5 消息订阅（CI 变更推送）
	mux.HandleFunc("GET /api/v1/cmdb/subscriptions", s.handleListSubscriptions)
	mux.HandleFunc("POST /api/v1/cmdb/subscriptions", s.handleCreateSubscription)
	mux.HandleFunc("PUT /api/v1/cmdb/subscriptions/{id}", s.handleUpdateSubscription)
	mux.HandleFunc("DELETE /api/v1/cmdb/subscriptions/{id}", s.handleDeleteSubscription)
	mux.HandleFunc("POST /api/v1/cmdb/subscriptions/{id}/test", s.handleTestSubscription)
	mux.HandleFunc("GET /api/v1/cmdb/subscription-deliveries", s.handleListSubscriptionDeliveries)

	// Version History
	mux.HandleFunc("GET /api/v1/cmdb/history", s.handleListHistory)
	mux.HandleFunc("GET /api/v1/cmdb/history/{ciId}", s.handleListCIVersions)
	mux.HandleFunc("GET /api/v1/cmdb/history/detail/{id}", s.handleGetVersionDetail)

	// Discovery
	mux.HandleFunc("GET /api/v1/cmdb/discovery", s.handleListDiscoveryTasks)
	mux.HandleFunc("POST /api/v1/cmdb/discovery", s.handleCreateDiscoveryTask)
	mux.HandleFunc("PUT /api/v1/cmdb/discovery/{id}", s.handleUpdateDiscoveryTask)
	mux.HandleFunc("DELETE /api/v1/cmdb/discovery/{id}", s.handleDeleteDiscoveryTask)
	mux.HandleFunc("GET /api/v1/cmdb/discovery/types", s.handleListDiscoveryTypes)
	mux.HandleFunc("POST /api/v1/cmdb/discovery/{id}/run", s.handleRunDiscoveryTask)
	mux.HandleFunc("GET /api/v1/cmdb/discovery/{id}/results", s.handleListDiscoveryResults)
	mux.HandleFunc("POST /api/v1/cmdb/discovery/{resultId}/sync", s.handleSyncDiscoveryResult)
	mux.HandleFunc("POST /api/v1/cmdb/discovery/{taskId}/sync-all", s.handleSyncAllDiscoveryResults)

	// 采集任务框架（全局默认策略 + 统一调度）
	mux.HandleFunc("GET /api/v1/cmdb/collection-policy", s.handleGetCollectionPolicy)
	mux.HandleFunc("PUT /api/v1/cmdb/collection-policy", s.handlePutCollectionPolicy)
	mux.HandleFunc("GET /api/v1/cmdb/collection-tasks", s.handleListCollectionTasks)
	mux.HandleFunc("POST /api/v1/cmdb/collection-tasks", s.handleCreateCollectionTask)
	mux.HandleFunc("PUT /api/v1/cmdb/collection-tasks/{id}", s.handleUpdateCollectionTask)
	mux.HandleFunc("DELETE /api/v1/cmdb/collection-tasks/{id}", s.handleDeleteCollectionTask)
	mux.HandleFunc("POST /api/v1/cmdb/collection-tasks/{id}/run", s.handleRunCollectionTask)
	mux.HandleFunc("POST /api/v1/cmdb/onboard", s.handleOnboardResource)

	// 数据质量中心
	mux.HandleFunc("GET /api/v1/cmdb/quality", s.handleQualityScan)
	mux.HandleFunc("POST /api/v1/cmdb/quality/fix", s.handleQualityFix)

	// AI 模型注册表 / 本体服务（设计文档阶段 1）：类型解析、提案、晋升
	mux.HandleFunc("POST /api/v1/cmdb/registry/resolve", s.handleRegistryResolve)
	mux.HandleFunc("POST /api/v1/cmdb/registry/propose", s.handleRegistryPropose)
	mux.HandleFunc("POST /api/v1/cmdb/registry/promote", s.handleRegistryPromote)
}

// handleGetCI godoc
// applyAggregateOp 聚合计算纯函数(count/sum/avg), 便于单测。
func applyAggregateOp(op, target string, related []*model.CIModel) any {
	switch op {
	case "count":
		return len(related)
	case "sum", "avg":
		var sum float64
		for _, c := range related {
			if v, ok := c.Attributes[target].(float64); ok {
				sum += v
			}
		}
		if op == "avg" && len(related) > 0 {
			sum /= float64(len(related))
		}
		return sum
	}
	return nil
}

// computeAggregates 对单个 CI 计算其模型 schema 中声明的聚合字段
// (count/sum/avg, 沿关系链统计关联 CI)。结果写入 ci.Aggregates。
// 关系量小时直接全量 ListRelations 内存过滤; 数据量大后可改 dgraph 查询。
func (s *Service) computeAggregates(ctx context.Context, tenantID string, ci *model.CIModel) {
	if s.pg == nil || ci == nil {
		return
	}
	mt, err := s.pg.GetCIModelType(ctx, ci.TypeID, tenantID)
	if err != nil || mt == nil {
		return
	}
	var hasAgg bool
	for _, def := range mt.Schema {
		if def.Aggregate != nil {
			hasAgg = true
			break
		}
	}
	if !hasAgg {
		return
	}
	rels, err := s.pg.ListRelations(ctx, tenantID)
	if err != nil {
		return
	}

	out := map[string]any{}
	for name, def := range mt.Schema {
		agg := def.Aggregate
		if agg == nil {
			continue
		}
		// 收集关联 CI(按方向 + 关系类型过滤)
		var related []*model.CIModel
		for _, rel := range rels {
			var otherID string
			if agg.Direction == "in" {
				if rel.TargetID == ci.ID {
					otherID = rel.SourceID
				}
			} else if rel.SourceID == ci.ID {
				otherID = rel.TargetID
			}
			if otherID == "" || (agg.Relation != "" && rel.RelationType != agg.Relation) {
				continue
			}
			c, err := s.pg.GetCI(ctx, otherID, tenantID)
			if err == nil && c != nil {
				related = append(related, c)
			}
		}

		out[name] = applyAggregateOp(agg.Op, agg.Target, related)
	}
	if len(out) > 0 {
		ci.Aggregates = out
	}
}

// buildTopologyFromRelations 以 roots 为起点, 在关系切片上做双向 BFS(深度 maxDepth),
// 返回拓扑节点与边。节点名称/类型由调用方按需富化(pg 模式用按 ID 批量查询)。
//
// 两处关键约束(2026-09-12 重构):
//  1. 先建邻接表再 BFS —— 原实现逐层线性扫全表关系, 复杂度 O(depth×V×E), 大租户下每次
//     拓扑请求都要跑几百万次比较; 现改为一次性建 adj, 整体 O(V+E)。
//  2. 节点按 BFS 访问顺序输出 —— 原实现从 map 遍历产出, Go map 迭代序随机, 导致
//     handleGetTopology 的 nodes[:600] 截断砍掉的是"随机 600 个", 每次刷新保留的节点
//     都不同、边还对不上。现在截断取的是"离根最近的 N 个", 结果确定可复现。
func buildTopologyFromRelations(tenantID string, roots []string, rels []*model.CIRelation, maxDepth int) ([]model.TopologyNode, []model.TopologyEdge) {
	type neighbor struct {
		peer     string
		sourceID string
		targetID string
		relType  string
	}

	adj := make(map[string][]neighbor, len(rels)*2)
	for _, rel := range rels {
		if rel.TenantID != tenantID {
			continue
		}
		e := neighbor{peer: rel.TargetID, sourceID: rel.SourceID, targetID: rel.TargetID, relType: rel.RelationType}
		adj[rel.SourceID] = append(adj[rel.SourceID], e)
		adj[rel.TargetID] = append(adj[rel.TargetID], neighbor{
			peer: rel.SourceID, sourceID: rel.SourceID, targetID: rel.TargetID, relType: rel.RelationType,
		})
	}

	visited := make(map[string]bool, len(roots))
	order := make([]string, 0, 64) // BFS 顺序, 保证输出稳定
	var frontier []string
	for _, root := range roots {
		if root == "" || visited[root] {
			continue
		}
		visited[root] = true
		order = append(order, root)
		frontier = append(frontier, root)
	}

	seenEdge := make(map[string]bool, len(rels))
	edges := make([]model.TopologyEdge, 0, len(rels))

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, cid := range frontier {
			for _, e := range adj[cid] {
				key := e.sourceID + "|" + e.targetID + "|" + e.relType
				if !seenEdge[key] {
					seenEdge[key] = true
					edges = append(edges, model.TopologyEdge{
						SourceID: e.sourceID, TargetID: e.targetID, RelationType: e.relType,
					})
				}
				if !visited[e.peer] {
					visited[e.peer] = true
					order = append(order, e.peer)
					next = append(next, e.peer)
				}
			}
		}
		frontier = next
	}

	nodes := make([]model.TopologyNode, 0, len(order))
	for _, id := range order {
		nodes = append(nodes, model.TopologyNode{ID: id, TenantID: tenantID})
	}
	return nodes, edges
}

// validateAttributes 校验 CI 属性: 必填 / 类型 / 枚举选项 / 引用目标存在 /
// 模型唯一性约束(Unique 写入拦截)。excludeCIID 用于更新场景排除自身。
func (s *Service) validateAttributes(ctx context.Context, typeID string, attrs map[string]any, excludeCIID string) error {
	mt, err := s.ResolveModelType(ctx, typeID, "default")
	if err != nil {
		return nil
	}
	if mt == nil {
		return nil
	}

	for name, def := range mt.Schema {
		val, hasVal := attrs[name]
		if def.Required {
			if !hasVal {
				return fmt.Errorf("required attribute '%s' is missing for CI type '%s'", name, typeID)
			}
		}
		if !hasVal {
			continue
		}
		if def.Type != "" {
			if !validateAttrType(def.Type, val) {
				return fmt.Errorf("attribute '%s' expected type '%s', got %T", name, def.Type, val)
			}
		}
		// 枚举选项校验: select/enum 类型声明了 Options 时, 值必须在选项内。
		if len(def.Options) > 0 && def.Type != "ref" {
			s, ok := val.(string)
			if ok && s != "" && !containsStr(def.Options, s) {
				return fmt.Errorf("attribute '%s' value %q not in options %v", name, s, def.Options)
			}
		}
		// 引用型字段: 校验目标 CI 存在(RefModel 声明)。
		if def.Type == "ref" {
			if def.RefModel == "" {
				return fmt.Errorf("attribute '%s' is ref type but ref_model not configured", name)
			}
			if s.pg != nil {
				if sval, ok := val.(string); ok && sval != "" {
					target, err := s.pg.GetCI(ctx, sval, "default")
					if err != nil {
						return fmt.Errorf("attribute '%s' ref lookup failed: %w", name, err)
					}
					if target == nil {
						return fmt.Errorf("attribute '%s' references non-existent CI '%s'", name, sval)
					}
				}
			}
		}
		// 唯一性约束: 同租户同类型下该字段值已存在(且非自身)则拦截。
		if def.Unique && s.pg != nil {
			if sval, ok := val.(string); ok && sval != "" {
				n, err := s.pg.CountCIByAttrValue(ctx, "default", typeID, name, sval, excludeCIID)
				if err != nil {
					return fmt.Errorf("attribute '%s' uniqueness check failed: %w", name, err)
				}
				if n > 0 {
					return fmt.Errorf("attribute '%s' must be unique: value %q already exists for model '%s'", name, sval, typeID)
				}
			}
		}
	}
	return nil
}

// containsStr 判断字符串切片是否包含目标值。
func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ResolveModelType 沿 ParentID 链合并父类属性（子类覆盖同名），并默认分类为 platform。
// 带环检测，避免 ParentID 成环导致无限递归。用于属性校验与模型读取展示。
func (s *Service) ResolveModelType(ctx context.Context, typeID, tenantID string) (*model.CIModelType, error) {
	chain := []*model.CIModelType{}
	visited := map[string]bool{}
	curID := typeID
	for curID != "" && len(chain) < 20 {
		if visited[curID] {
			break
		}
		visited[curID] = true
		mt, err := s.pg.GetCIModelType(ctx, curID, tenantID)
		if err != nil {
			return nil, err
		}
		if mt == nil {
			break
		}
		chain = append(chain, mt)
		curID = mt.ParentID
	}
	if len(chain) == 0 {
		return nil, nil
	}
	// chain 当前顺序为 [当前类型, 父, 祖父...]，反转成 [根祖先, ..., 当前类型]，
	// 使合并时子类覆盖父类同名属性，且返回的 result 为请求的当前类型。
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	merged := map[string]model.AttrDef{}
	for _, m := range chain { // 从根祖先到当前类型，后者（子类）覆盖前者（父类）
		for k, v := range m.Schema {
			// 兼容旧数据：AttrDef.ID 为空时补全为字段名(稳定机器标识)。
			if v.ID == "" {
				v.ID = k
			}
			merged[k] = v
		}
	}
	result := chain[len(chain)-1]
	result.Schema = merged
	// 兼容旧数据：RelationDef.ID 为空时补全为 RelationType。
	for i := range result.Relations {
		if result.Relations[i].ID == "" {
			result.Relations[i].ID = result.Relations[i].RelationType
		}
	}
	if result.Category == "" {
		result.Category = model.ModelCategoryPlatform
	}
	return result, nil
}

// fieldUsage 模型字段消费度(保存查询/表单引用计数)。
type fieldUsage struct {
	Field        string `json:"field"`
	Type         string `json:"type"`
	Required     bool   `json:"required"`
	SavedQueries int    `json:"saved_queries"`
	ITSMForms    int    `json:"itsm_forms"`
	Total        int    `json:"total"`
	Unused       bool   `json:"unused"`
}

func countUnused(fields []fieldUsage) int {
	n := 0
	for _, f := range fields {
		if f.Unused {
			n++
		}
	}
	return n
}

// ---------- CI CSV 导入导出 (P1-1) ----------

// toCSVValue 将 CI 属性值序列化为 CSV 单元格字符串。
func toCSVValue(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(val), 'f', -1, 32)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case json.Number:
		return val.String()
	case time.Time:
		return val.Format(time.RFC3339)
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(b)
	}
}

// fromCSVValue 将 CSV 单元格字符串按属性类型反序列化为 Go 值。
func fromCSVValue(raw, typ string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	switch typ {
	case "", "string":
		return raw, nil
	case "number", "int", "integer", "float":
		return strconv.ParseFloat(raw, 64)
	case "boolean", "bool":
		return strconv.ParseBool(raw)
	case "object", "array", "json":
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		return v, nil
	default:
		return raw, nil
	}
}

func traceIDFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("trace_id").(string); ok && t != "" {
		return t
	}
	return "unknown"
}

func resolveChannel(ch int) model.ChannelWeight {
	switch {
	case ch >= 90:
		return model.WeightHuman
	case ch >= 70:
		return model.WeightITSM
	case ch >= 40:
		return model.WeightAutoScript
	default:
		return model.WeightDiscovery
	}
}

func validateAttrType(typ string, val any) bool {
	switch typ {
	case "string":
		_, ok := val.(string)
		return ok
	case "number":
		switch val.(type) {
		case float64, int, int64, json.Number:
			return true
		}
		return false
	case "boolean":
		_, ok := val.(bool)
		return ok
	}
	return true
}

// ============================================================
// AI 模型注册表 / 本体服务（设计文档阶段 1）
// ============================================================

// ============================================================
// Version History Handlers
// ============================================================

// ============================================================
// Discovery Handlers
// ============================================================

// handleListDiscoveryTypes 返回当前已注册的真实发现插件类型（含中文标签与映射的 CI 模型类型），供前端下拉。
func (s *Service) handleListDiscoveryTypes(w http.ResponseWriter, r *http.Request) {
	typeMeta := map[string]struct {
		Label   string `json:"label"`
		CIType  string `json:"ci_type"`
		Channel string `json:"channel"` // snmp=网络协议直连 / agent=探针 Agent 脚本
		Note    string `json:"note"`
	}{
		"host":        {Label: "主机（操作系统级）", CIType: "host", Channel: "agent", Note: "探针 Agent 本地采集硬件/系统信息（dmidecode 需权限），涵盖物理机/VM/云主机"},
		"server":      {Label: "物理服务器/裸金属（别名）", CIType: "host", Channel: "agent", Note: "与 host 同实现，语义指向物理机"},
		"snmp":        {Label: "网络设备（SNMP）", CIType: "switch", Channel: "snmp", Note: "交换机/路由器/防火墙，需 community"},
		"network":     {Label: "网络设备（别名）", CIType: "switch", Channel: "snmp", Note: "与 snmp 同实现"},
		"database":    {Label: "数据库实例", CIType: "database", Channel: "agent", Note: "MySQL/PostgreSQL/Oracle 探测（需客户端与账号）"},
		"middleware":  {Label: "中间件实例", CIType: "middleware", Channel: "agent", Note: "Redis/Nginx/ES/Kafka/ZK/RabbitMQ 探测"},
		"application": {Label: "应用程序/Web 服务", CIType: "app", Channel: "agent", Note: "端口+HTTP 指纹探测"},
		"app":         {Label: "应用程序（别名）", CIType: "app", Channel: "agent", Note: "与 application 同实现"},
		"relation":    {Label: "关系自动发现（内置）", CIType: "", Channel: "builtin", Note: "在已纳管 CI 之上推断外键关系"},
	}
	out := make([]map[string]any, 0, len(typeMeta))
	for t, m := range typeMeta {
		out = append(out, map[string]any{
			"type":    t,
			"label":   m.Label,
			"ci_type": m.CIType,
			"channel": m.Channel,
			"note":    m.Note,
		})
	}
	httputil.WriteJSON(w, 200, map[string]any{"types": out})
}

// buildBuiltinModelTypes 返回系统内置模型类型清单。
// 前五类(server/switch/database/middleware/app)与自动发现插件产出的
// CIType 对齐, 确保发现结果可直接落库; business_service 是服务建模层,
// 承载业务服务→应用→技术组件的依赖链与业务关键性。
// 纯函数, 便于单测; 幂等种子由 EnsureDiscoveryModelTypes 负责。
func buildBuiltinModelTypes() []model.CIModelType {
	types := []model.CIModelType{
		{
			ID:          model.ModelTypeServer,
			Name:        "服务器",
			Description: "物理服务器 / 裸金属（自动发现 server 插件产出）",
			Category:    model.ModelCategoryInfra,
			Icon:        "server",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"hostname":   {Type: "string", Description: "主机名"},
				"ip_address": {Type: "string", Description: "管理 IP"},
				"os":         {Type: "string", Description: "操作系统"},
				"cpu_cores":  {Type: "int", Description: "CPU 核数"},
				"memory_gb":  {Type: "int", Description: "内存(GB)"},
			},
			// 预置关联路径：服务器位于机柜（设施层级：数据中心→机房→机柜→设备）。
			Relations: []model.RelationDef{
				{ID: "srv_in_rack", Name: "位于机柜", RelationType: model.RelationLocatedIn, TargetModel: "rack", Cardinality: model.CardinalityOneToMany, ReverseName: "包含设备"},
			},
		},
		{
			ID:          model.ModelTypeNetwork,
			Name:        "网络设备",
			Description: "交换机 / 路由器（自动发现 network 插件产出）",
			Category:    model.ModelCategoryInfra,
			Icon:        "network",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"vendor":     {Type: "string", Description: "厂商"},
				"model":      {Type: "string", Description: "型号"},
				"ip_address": {Type: "string", Description: "管理 IP"},
			},
			// 预置关联路径：网络设备位于机柜，且可与其它网络设备互联（自关联）。
			Relations: []model.RelationDef{
				{ID: "net_in_rack", Name: "位于机柜", RelationType: model.RelationLocatedIn, TargetModel: "rack", Cardinality: model.CardinalityOneToMany, ReverseName: "包含设备"},
				{ID: "net_conn_net", Name: "互联", RelationType: model.RelationConnects, TargetModel: model.ModelTypeNetwork, Cardinality: model.CardinalityManyToMany, ReverseName: "互联"},
			},
		},
		{
			ID:          model.ModelTypeDatabase,
			Name:        "数据库",
			Description: "数据库抽象父类（mysql/oracle/postgresql 的共性承载，不可直接实例化）",
			Category:    model.ModelCategoryDatabase,
			Icon:        "database",
			ShowInMenu:  true,
			Builtin:     true,
			Abstract:    true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"ip":       {Type: "string", Description: "监听地址"},
				"port":     {Type: "int", Description: "监听端口"},
				"version":  {Type: "string", Description: "版本"},
				"engine":   {Type: "string", Description: "引擎（mysql/postgresql/oracle）"},
				"account":  {Type: "string", Description: "连接账号"},
				"password": {Type: "string", Description: "连接密码", Default: ""},
				"charset":  {Type: "string", Description: "字符集"},
			},
			// 预置关联路径：数据库实例部署于主机（子类 mysql/oracle/postgresql 继承此语义；host 涵盖物理机/VM/云主机）。
			Relations: []model.RelationDef{
				{ID: "db_runs_host", Name: "部署于", RelationType: model.RelationHosts, TargetModel: model.ModelTypeHost, Cardinality: model.CardinalityOneToMany, ReverseName: "承载数据库"},
			},
		},
		{
			ID:          model.ModelTypeMiddleware,
			Name:        "中间件",
			Description: "中间件抽象父类（redis/kafka/nginx/elasticsearch/zookeeper/rabbitmq 的共性承载，不可直接实例化）",
			Category:    model.ModelCategoryMiddleware,
			Icon:        "layers",
			ShowInMenu:  true,
			Builtin:     true,
			Abstract:    true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"ip":              {Type: "string", Description: "监听地址"},
				"port":            {Type: "int", Description: "监听端口"},
				"version":         {Type: "string", Description: "版本"},
				"middleware_type": {Type: "string", Description: "中间件类型（redis/nginx/elasticsearch/kafka/zookeeper/rabbitmq）"},
				"account":         {Type: "string", Description: "连接账号"},
				"password":        {Type: "string", Description: "连接密码", Default: ""},
				"role":            {Type: "string", Description: "角色（如 redis master/slave）"},
			},
			// 预置关联路径：中间件实例部署于主机（子类 redis/kafka/... 继承此语义；host 涵盖物理机/VM/云主机）。
			Relations: []model.RelationDef{
				{ID: "mw_runs_host", Name: "部署于", RelationType: model.RelationHosts, TargetModel: model.ModelTypeHost, Cardinality: model.CardinalityOneToMany, ReverseName: "承载中间件"},
			},
		},
		// ===== 数据库抽象父类的 leaf 子类（经 ParentID 继承 ip/port/version/engine/account/password/charset）=====
		{
			ID:          "mysql",
			Name:        "MySQL",
			Description: "MySQL 数据库实例（继承 database 父类，TypeID 下沉为具体引擎）",
			ParentID:    model.ModelTypeDatabase,
			Category:    model.ModelCategoryDatabase,
			Icon:        "database",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"binlog_format":           {Type: "enum", Options: []string{"ROW", "STATEMENT", "MIXED"}, Description: "binlog 格式"},
				"character_set_server":    {Type: "string", Description: "服务端字符集"},
				"innodb_buffer_pool_size": {Type: "int", Description: "InnoDB 缓冲池大小(MB)"},
				"replication_role":        {Type: "enum", Options: []string{"master", "slave"}, Description: "复制角色"},
				"group_replication":       {Type: "bool", Description: "是否启用组复制(MGR)"},
			},
		},
		{
			ID:          "oracle",
			Name:        "Oracle",
			Description: "Oracle 数据库实例（继承 database 父类）",
			ParentID:    model.ModelTypeDatabase,
			Category:    model.ModelCategoryDatabase,
			Icon:        "database",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"sid":             {Type: "string", Description: "Oracle SID"},
				"service_name":    {Type: "string", Description: "服务名"},
				"tablespace_list": {Type: "string", Description: "表空间列表"},
				"rac":             {Type: "bool", Description: "是否 RAC 集群"},
				"asm":             {Type: "bool", Description: "是否使用 ASM"},
			},
		},
		{
			ID:          "postgresql",
			Name:        "PostgreSQL",
			Description: "PostgreSQL 数据库实例（继承 database 父类）",
			ParentID:    model.ModelTypeDatabase,
			Category:    model.ModelCategoryDatabase,
			Icon:        "database",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"default_schema":   {Type: "string", Description: "默认 schema"},
				"wal_level":        {Type: "string", Description: "WAL 级别"},
				"max_connections":  {Type: "int", Description: "最大连接数"},
				"extension_list":   {Type: "string", Description: "已安装扩展列表"},
				"replication_slot": {Type: "string", Description: "复制槽名称"},
			},
		},
		// ===== 中间件抽象父类的 leaf 子类（经 ParentID 继承 ip/port/version/middleware_type/account/password/role）=====
		{
			ID:          "redis",
			Name:        "Redis",
			Description: "Redis 缓存/键值实例（继承 middleware 父类）",
			ParentID:    model.ModelTypeMiddleware,
			Category:    model.ModelCategoryMiddleware,
			Icon:        "layers",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"mode":            {Type: "enum", Options: []string{"standalone", "sentinel", "cluster"}, Description: "部署模式"},
				"maxmemory":       {Type: "int", Description: "最大内存(MB)"},
				"persistence":     {Type: "enum", Options: []string{"rdb", "aof"}, Description: "持久化方式"},
				"cluster_enabled": {Type: "bool", Description: "是否启用集群"},
			},
		},
		{
			ID:          "kafka",
			Name:        "Kafka",
			Description: "Kafka 消息队列实例（继承 middleware 父类）",
			ParentID:    model.ModelTypeMiddleware,
			Category:    model.ModelCategoryMiddleware,
			Icon:        "layers",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"partitions_default": {Type: "int", Description: "默认分区数"},
				"replication_factor": {Type: "int", Description: "默认副本因子"},
				"zookeeper_connect":  {Type: "string", Description: "ZK 连接串"},
			},
		},
		{
			ID:          "nginx",
			Name:        "Nginx",
			Description: "Nginx 反向代理/Web 实例（继承 middleware 父类）",
			ParentID:    model.ModelTypeMiddleware,
			Category:    model.ModelCategoryMiddleware,
			Icon:        "layers",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"worker_processes": {Type: "int", Description: "worker 进程数"},
				"listen_ports":     {Type: "string", Description: "监听端口列表"},
				"ssl_enabled":      {Type: "bool", Description: "是否启用 SSL"},
				"upstream_list":    {Type: "string", Description: "upstream 列表"},
			},
		},
		{
			ID:          "elasticsearch",
			Name:        "Elasticsearch",
			Description: "Elasticsearch 搜索/分析实例（继承 middleware 父类）",
			ParentID:    model.ModelTypeMiddleware,
			Category:    model.ModelCategoryMiddleware,
			Icon:        "layers",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"cluster_name": {Type: "string", Description: "集群名"},
				"node_role":    {Type: "string", Description: "节点角色"},
				"shard_count":  {Type: "int", Description: "分片数"},
				"heap_size":    {Type: "string", Description: "堆内存大小"},
			},
		},
		{
			ID:          "zookeeper",
			Name:        "ZooKeeper",
			Description: "ZooKeeper 协调服务实例（继承 middleware 父类）",
			ParentID:    model.ModelTypeMiddleware,
			Category:    model.ModelCategoryMiddleware,
			Icon:        "layers",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"tick_time":     {Type: "int", Description: "心跳 tick 毫秒"},
				"data_dir":      {Type: "string", Description: "数据目录"},
				"ensemble_size": {Type: "int", Description: "集群节点数"},
			},
		},
		{
			ID:          "rabbitmq",
			Name:        "RabbitMQ",
			Description: "RabbitMQ 消息队列实例（继承 middleware 父类）",
			ParentID:    model.ModelTypeMiddleware,
			Category:    model.ModelCategoryMiddleware,
			Icon:        "layers",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"vhost_list":     {Type: "string", Description: "虚拟主机列表"},
				"queue_count":    {Type: "int", Description: "队列数"},
				"erlang_version": {Type: "string", Description: "Erlang 版本"},
			},
		},
		{
			ID:          model.ModelTypeApp,
			Name:        "应用",
			Description: "应用服务实例（自动发现 application 插件产出）",
			Category:    model.ModelCategoryApp,
			Icon:        "app-window",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"tech_stack": {Type: "string", Description: "技术栈（java/go/node/php）"},
				"port":       {Type: "int", Description: "监听端口"},
			},
			// 预置关联路径：应用可调用其它应用（自关联），由多个进程服务组成，依赖数据库/中间件，部署于服务器。
			Relations: []model.RelationDef{
				{ID: "app_dep_app", Name: "调用应用", RelationType: model.RelationDependsOn, TargetModel: model.ModelTypeApp, Cardinality: model.CardinalityManyToMany, ReverseName: "被调用"},
				{ID: "app_compose_svc", Name: "包含服务", RelationType: model.RelationComposedOf, TargetModel: model.ModelTypeService, Cardinality: model.CardinalityOneToMany, ReverseName: "隶属应用"},
				{ID: "app_dep_db", Name: "依赖数据库", RelationType: model.RelationDependsOn, TargetModel: model.ModelTypeDatabase, Cardinality: model.CardinalityManyToMany, ReverseName: "被应用依赖"},
				{ID: "app_dep_mw", Name: "依赖中间件", RelationType: model.RelationDependsOn, TargetModel: model.ModelTypeMiddleware, Cardinality: model.CardinalityManyToMany, ReverseName: "被应用依赖"},
				{ID: "app_runs_host", Name: "部署于", RelationType: model.RelationHosts, TargetModel: model.ModelTypeHost, Cardinality: model.CardinalityOneToMany, ReverseName: "承载应用"},
			},
		},
		{
			ID:          model.ModelTypeService,
			Name:        "服务",
			Description: "进程级服务（应用由多个服务组成，如订单应用=API服务+Worker服务；自动发现 application 插件产出）",
			Category:    model.ModelCategoryService,
			Icon:        "cog",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"process_name": {Type: "string", Description: "进程名/服务名"},
				"listen_port":  {Type: "int", Description: "监听端口"},
				"start_cmd":    {Type: "string", Description: "启动命令"},
				"pid":          {Type: "int", Description: "进程 PID"},
				"status":       {Type: "string", Description: "运行状态（running/stopped）"},
				"language":     {Type: "string", Description: "实现语言（java/go/node/...）"},
			},
			// 预置关联路径：服务运行于主机（进程落地到资源，一台机器可承载多个服务；host 涵盖物理机/VM/云主机），
			// 并可依赖其它服务（服务间调用，自关联）。
			Relations: []model.RelationDef{
				{ID: "svc_runs_host", Name: "运行于", RelationType: model.RelationRunsOn, TargetModel: model.ModelTypeHost, Cardinality: model.CardinalityManyToMany, ReverseName: "承载服务"},
				{ID: "svc_dep_svc", Name: "依赖服务", RelationType: model.RelationDependsOn, TargetModel: model.ModelTypeService, Cardinality: model.CardinalityManyToMany, ReverseName: "被服务依赖"},
			},
		},
		{
			ID:          model.ModelTypeBusinessService,
			Name:        "业务",
			Description: "面向业务的顶层服务（服务建模：业务依赖应用/数据库/中间件）",
			Category:    model.ModelCategoryBusiness,
			Icon:        "briefcase",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				model.AttrKeyCriticality: {Type: "string", Default: "medium", Description: "业务关键性（critical/high/medium/low）"},
				"owner":                  {Type: "string", Description: "业务负责人"},
				"department":             {Type: "string", Description: "所属部门"},
			},
			// 预置关联路径：业务可依赖其它业务（自关联），也可依赖应用/数据库/中间件。
			Relations: []model.RelationDef{
				{ID: "bs_dep_bs", Name: "依赖业务", RelationType: model.RelationDependsOn, TargetModel: model.ModelTypeBusinessService, Cardinality: model.CardinalityManyToMany, ReverseName: "被依赖于"},
				{ID: "bs_dep_app", Name: "依赖应用", RelationType: model.RelationDependsOn, TargetModel: model.ModelTypeApp, Cardinality: model.CardinalityManyToMany, ReverseName: "支撑业务"},
				{ID: "bs_dep_db", Name: "依赖数据库", RelationType: model.RelationDependsOn, TargetModel: model.ModelTypeDatabase, Cardinality: model.CardinalityManyToMany, ReverseName: "被业务依赖"},
				{ID: "bs_dep_mw", Name: "依赖中间件", RelationType: model.RelationDependsOn, TargetModel: model.ModelTypeMiddleware, Cardinality: model.CardinalityManyToMany, ReverseName: "被业务依赖"},
			},
		},
		{
			ID:          "storage",
			Name:        "存储设备",
			Description: "存储阵列 / 分布式存储（SAN/NAS/Ceph 等）",
			Category:    model.ModelCategoryInfra,
			Icon:        "hard-drive",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"storage_type": {Type: "string", Description: "类型（san/nas/distributed）"},
				"capacity_tb":  {Type: "number", Description: "总容量(TB)"},
				"vendor":       {Type: "string", Description: "厂商"},
				"ip_address":   {Type: "string", Description: "管理 IP"},
			},
			// 预置关联路径：存储设备位于机柜（设施层级）。
			Relations: []model.RelationDef{
				{ID: "stor_in_rack", Name: "位于机柜", RelationType: model.RelationLocatedIn, TargetModel: "rack", Cardinality: model.CardinalityOneToMany, ReverseName: "包含设备"},
			},
		},
		{
			ID:          "host",
			Name:        "主机",
			Description: "操作系统级主机（无论底层是物理机还是虚拟机，均由 Agent / Agentless 接入；承载应用/数据库/中间件/服务）",
			Category:    model.ModelCategoryInfra,
			Icon:        "hard-drive",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"resource_id": {Type: "string", Description: "资产自然键（host:<agentID>）"},
				"hostname":    {Type: "string", Description: "主机名"},
				"os":          {Type: "string", Description: "操作系统类型"},
				"os_version":  {Type: "string", Description: "操作系统版本"},
				"cpu":         {Type: "string", Description: "CPU 信息"},
				"memory":      {Type: "string", Description: "内存信息"},
				"disk":        {Type: "string", Description: "磁盘信息"},
				"ip":          {Type: "string", Description: "管理 IP"},
				"agent_id":    {Type: "string", Description: "接入 Agent ID"},
				"labels":      {Type: "string", Description: "标签(JSON)"},
			},
			// 预置关联路径：主机位于机柜（设施层级），并可被业务资源部署/运行于（见 server/db/mw/app/svc 的关系锚点统一指向 host）。
			Relations: []model.RelationDef{
				{ID: "host_in_rack", Name: "位于机柜", RelationType: model.RelationLocatedIn, TargetModel: "rack", Cardinality: model.CardinalityOneToMany, ReverseName: "包含设备"},
			},
		},
		{
			ID:          "firewall",
			Name:        "防火墙",
			Description: "安全防护设备（硬件/云防火墙）",
			Category:    model.ModelCategoryInfra,
			Icon:        "shield",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"vendor":     {Type: "string", Description: "厂商"},
				"model":      {Type: "string", Description: "型号"},
				"ip_address": {Type: "string", Description: "管理 IP"},
			},
		},
		{
			ID:          "load_balancer",
			Name:        "负载均衡",
			Description: "负载均衡设备（硬件 LB / 云 SLB）",
			Category:    model.ModelCategoryInfra,
			Icon:        "scale",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"vendor":     {Type: "string", Description: "厂商"},
				"mode":       {Type: "string", Description: "模式（l4/l7）"},
				"ip_address": {Type: "string", Description: "管理 IP"},
			},
		},
		{
			ID:          "router",
			Name:        "路由器",
			Description: "路由器（核心/接入路由）",
			Category:    model.ModelCategoryInfra,
			Icon:        "route",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"vendor":     {Type: "string", Description: "厂商"},
				"model":      {Type: "string", Description: "型号"},
				"ip_address": {Type: "string", Description: "管理 IP"},
			},
		},
		// —— 数据中心设施（机房机柜 2D 视图的数据底座, 录入即成图）——
		{
			ID:          "data_center",
			Name:        "数据中心",
			Description: "数据中心（物理机房建筑，顶层设施节点，内含多个机房）",
			Category:    model.ModelCategoryInfra,
			Icon:        "building",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"location": {Type: "string", Description: "地理位置"},
				"owner":    {Type: "string", Description: "负责人"},
			},
			// 预置关联路径：机房位于数据中心（设施层级顶层：数据中心→机房→机柜→设备）。
			Relations: []model.RelationDef{
				{ID: "dc_has_room", Name: "包含机房", RelationType: model.RelationLocatedIn, TargetModel: "idc_room", Cardinality: model.CardinalityOneToMany, ReverseName: "位于数据中心"},
			},
		},
		{
			ID:          "idc_room",
			Name:        "机房",
			Description: "数据中心内的机房/楼层（含机柜，隶属于某数据中心）",
			Category:    model.ModelCategoryInfra,
			Icon:        "warehouse",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"data_center_id": {Type: "string", Description: "所属数据中心(data_center CI ID)"},
				"floor":          {Type: "string", Description: "楼层/区域"},
				"rows":           {Type: "int", Description: "机柜行数(平面图)"},
				"cols":           {Type: "int", Description: "机柜列数(平面图)"},
			},
			// 预置关联路径：机柜位于机房（设施层级中层）。
			Relations: []model.RelationDef{
				{ID: "room_has_rack", Name: "包含机柜", RelationType: model.RelationLocatedIn, TargetModel: "rack", Cardinality: model.CardinalityOneToMany, ReverseName: "位于机房"},
			},
		},
		{
			ID:          "rack",
			Name:        "机柜",
			Description: "标准机柜（42U），设备按 rack_id + rack_unit 属性落位，隶属于某机房",
			Category:    model.ModelCategoryInfra,
			Icon:        "rows",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"room_id":   {Type: "string", Description: "所属机房(idc_room CI ID)"},
				"row":       {Type: "int", Description: "平面图行号(从 1 起)"},
				"col":       {Type: "int", Description: "平面图列号(从 1 起)"},
				"max_units": {Type: "int", Default: "42", Description: "U 位总数"},
				"rack_type": {Type: "string", Description: "类型（标准/高密/网络）"},
			},
			// 设施层次底层：机柜是被包含节点，设备位于机柜的关系在 server/storage/switch 模型声明。
		},
		// ===== K8S 容器系列（kubernetes 抽象父类 + 7 个 leaf 子类，经 ParentID 继承）=====
		{
			ID:          model.ModelTypeKubernetes,
			Name:        "Kubernetes",
			Description: "K8S 容器平台抽象父类（集群/节点/命名空间/工作负载/Pod/Service/容器的共性承载，不可直接实例化）",
			Category:    model.ModelCategoryContainer,
			Icon:        "☸️",
			ShowInMenu:  true,
			Builtin:     true,
			Abstract:    true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"cluster_name": {Type: "string", Description: "所属集群名"},
				"namespace":    {Type: "string", Description: "命名空间"},
				"labels":       {Type: "string", Description: "标签(JSON)"},
				"annotations":  {Type: "string", Description: "注解(JSON)"},
				"k8s_version":  {Type: "string", Description: "Kubernetes 版本"},
			},
		},
		{
			ID:          "kubernetes_cluster",
			Name:        "K8S 集群",
			Description: "Kubernetes 集群（APIServer 入口，纳管 Node/Pod 等资源）",
			ParentID:    model.ModelTypeKubernetes,
			Category:    model.ModelCategoryContainer,
			Icon:        "☸️",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"api_server":    {Type: "string", Description: "API Server 地址"},
				"version":       {Type: "string", Description: "集群版本"},
				"node_count":    {Type: "int", Description: "节点数"},
				"pod_capacity":  {Type: "int", Description: "Pod 容量"},
				"cpu_total":     {Type: "int", Description: "CPU 总核数"},
				"memory_total":  {Type: "int", Description: "内存总量(GB)"},
				"network_cidr":  {Type: "string", Description: "Pod 网段"},
				"ingress_class": {Type: "string", Description: "Ingress 控制器类"},
			},
		},
		{
			ID:          "kubernetes_namespace",
			Name:        "K8S 命名空间",
			Description: "Kubernetes 命名空间（资源隔离边界）",
			ParentID:    model.ModelTypeKubernetes,
			Category:    model.ModelCategoryContainer,
			Icon:        "🔗",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"status": {Type: "enum", Options: []string{"active", "terminating"}, Description: "状态"},
				"labels": {Type: "string", Description: "标签(JSON)"},
			},
		},
		{
			ID:          "kubernetes_node",
			Name:        "K8S 节点",
			Description: "Kubernetes 节点（物理/虚拟节点，承载 Pod）",
			ParentID:    model.ModelTypeKubernetes,
			Category:    model.ModelCategoryContainer,
			Icon:        "🖥️",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"hostname":        {Type: "string", Description: "主机名"},
				"role":            {Type: "enum", Options: []string{"master", "worker"}, Description: "角色"},
				"kubelet_version": {Type: "string", Description: "Kubelet 版本"},
				"os_image":        {Type: "string", Description: "系统镜像"},
				"ip_address":      {Type: "string", Description: "内网 IP"},
				"cpu_capacity":    {Type: "int", Description: "CPU 核数"},
				"memory_capacity": {Type: "int", Description: "内存(GB)"},
				"taints":          {Type: "string", Description: "污点(JSON)"},
			},
		},
		{
			ID:          "kubernetes_workload",
			Name:        "K8S 工作负载",
			Description: "Kubernetes 工作负载（Deployment/StatefulSet/DaemonSet）",
			ParentID:    model.ModelTypeKubernetes,
			Category:    model.ModelCategoryContainer,
			Icon:        "⚙️",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"kind":               {Type: "enum", Options: []string{"deployment", "statefulset", "daemonset"}, Description: "负载类型"},
				"replicas":           {Type: "int", Description: "期望副本数"},
				"ready_replicas":     {Type: "int", Description: "就绪副本数"},
				"image":              {Type: "string", Description: "镜像"},
				"strategy":           {Type: "enum", Options: []string{"rolling", "recreate"}, Description: "更新策略"},
				"creation_timestamp": {Type: "string", Description: "创建时间"},
			},
		},
		{
			ID:          "kubernetes_pod",
			Name:        "K8S Pod",
			Description: "Kubernetes Pod（最小调度单元，可含多容器）",
			ParentID:    model.ModelTypeKubernetes,
			Category:    model.ModelCategoryContainer,
			Icon:        "📦",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"node_name": {Type: "string", Description: "所在节点(node CI ID)"},
				"phase":     {Type: "enum", Options: []string{"running", "pending", "succeeded", "failed", "unknown"}, Description: "阶段"},
				"pod_ip":    {Type: "string", Description: "Pod IP"},
				"host_ip":   {Type: "string", Description: "宿主机 IP"},
				"restarts":  {Type: "int", Description: "重启次数"},
				"qos":       {Type: "enum", Options: []string{"guaranteed", "burstable", "besteffort"}, Description: "QoS 等级"},
			},
		},
		{
			ID:          "kubernetes_service",
			Name:        "K8S Service",
			Description: "Kubernetes Service（负载均衡入口，选择一组 Pod）",
			ParentID:    model.ModelTypeKubernetes,
			Category:    model.ModelCategoryContainer,
			Icon:        "🌐",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"service_type": {Type: "enum", Options: []string{"clusterip", "nodeport", "loadbalancer", "externalname"}, Description: "类型"},
				"cluster_ip":   {Type: "string", Description: "集群内 IP"},
				"external_ip":  {Type: "string", Description: "外部 IP"},
				"ports":        {Type: "string", Description: "端口映射(JSON)"},
				"selector":     {Type: "string", Description: "标签选择器(JSON)"},
			},
		},
		{
			ID:          "container",
			Name:        "容器",
			Description: "容器实例（运行时创建，归属 Pod/节点）",
			ParentID:    model.ModelTypeKubernetes,
			Category:    model.ModelCategoryContainer,
			Icon:        "📦",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Source:      "seed",
			Schema: map[string]model.AttrDef{
				"image":         {Type: "string", Description: "镜像"},
				"image_id":      {Type: "string", Description: "镜像 ID"},
				"runtime":       {Type: "enum", Options: []string{"docker", "containerd", "cri-o"}, Description: "容器运行时"},
				"status":        {Type: "enum", Options: []string{"running", "exited", "created", "restarting", "paused"}, Description: "状态"},
				"restart_count": {Type: "int", Description: "重启次数"},
				"pod":           {Type: "string", Description: "所属 Pod(kubernetes_pod CI ID)"},
				"limits_cpu":    {Type: "string", Description: "CPU 上限(核)"},
				"limits_mem":    {Type: "string", Description: "内存上限(MB)"},
			},
		},
	}
	return append(types, cloudModelTypes()...)
}

// ===== 云资源系列（按厂商×资源类型生成，一级分类=云资源，二级分类=厂商）=====

// cloudVendors 云资源厂商与支持类型（公有云对齐 cloud 模块 types.go 全 10 类；
// 私有云聚焦 vm/vpc/disk/load_balancer）。模型 ID 形如 <vendor>_<type>（aliyun_vm），
// 与 internal/cloud/cmdb_sync.go 的 CITypeOf 厂商化映射对齐，云纳管同步结果可直接落库。
var cloudVendors = []struct {
	vendor        string
	name          string
	subCategory   string
	resourceTypes []string
}{
	{"aliyun", "阿里云", "阿里云资源", []string{"vm", "database", "load_balancer", "vpc", "bucket", "disk", "firewall", "nat_gateway", "cdn", "dns"}},
	{"tencent", "腾讯云", "腾讯云资源", []string{"vm", "database", "load_balancer", "vpc", "bucket", "disk", "firewall", "nat_gateway", "cdn", "dns"}},
	{"huawei", "华为云", "华为云资源", []string{"vm", "database", "load_balancer", "vpc", "bucket", "disk", "firewall", "nat_gateway", "cdn", "dns"}},
	{"aws", "AWS", "AWS 资源", []string{"vm", "database", "load_balancer", "vpc", "bucket", "disk", "firewall", "nat_gateway", "cdn", "dns"}},
	{"azure", "Azure", "Azure 资源", []string{"vm", "database", "load_balancer", "vpc", "bucket", "disk", "firewall", "nat_gateway", "cdn", "dns"}},
	{"volcano", "火山引擎", "火山引擎资源", []string{"vm", "database", "load_balancer", "vpc", "bucket", "disk", "firewall", "nat_gateway", "cdn", "dns"}},
	{"openstack", "OpenStack", "OpenStack 资源", []string{"vm", "vpc", "disk", "load_balancer"}},
	{"vmware", "VMware", "VMware 资源", []string{"vm", "vpc", "disk", "load_balancer"}},
	{"zstack", "ZStack", "ZStack 资源", []string{"vm", "vpc", "disk", "load_balancer"}},
	{"sangfor", "深信服超融合", "深信服超融合资源", []string{"vm", "vpc", "disk", "load_balancer"}},
	{"proxmox", "Proxmox", "Proxmox 资源", []string{"vm", "vpc", "disk", "load_balancer"}},
}

// cloudResourceTypes 资源类型显示名与类型专属字段（公共字段见 cloudBaseSchema）。
var cloudResourceTypes = map[string]struct {
	label  string
	fields map[string]model.AttrDef
}{
	"vm": {"云主机", map[string]model.AttrDef{
		"instance_type": {Type: "string", Description: "实例规格"},
		"public_ip":     {Type: "string", Description: "公网 IP"},
		"private_ip":    {Type: "string", Description: "内网 IP"},
		"vpc_id":        {Type: "string", Description: "所属 VPC"},
		"cpu_cores":     {Type: "int", Description: "CPU 核数"},
		"memory_gb":     {Type: "int", Description: "内存(GB)"},
		"disk_gb":       {Type: "int", Description: "系统盘(GB)"},
	}},
	"database": {"云数据库", map[string]model.AttrDef{
		"engine":        {Type: "string", Description: "引擎（mysql/pg/redis 等）"},
		"version":       {Type: "string", Description: "版本"},
		"instance_type": {Type: "string", Description: "规格"},
		"endpoint":      {Type: "string", Description: "连接地址"},
		"port":          {Type: "int", Description: "端口"},
		"storage_gb":    {Type: "int", Description: "存储(GB)"},
	}},
	"load_balancer": {"负载均衡", map[string]model.AttrDef{
		"scheme":    {Type: "string", Description: "类型（公网/内网）"},
		"address":   {Type: "string", Description: "服务地址"},
		"listeners": {Type: "string", Description: "监听器(JSON)"},
	}},
	"vpc": {"私有网络", map[string]model.AttrDef{
		"cidr_block": {Type: "string", Description: "网段"},
		"gateway":    {Type: "string", Description: "网关"},
	}},
	"bucket": {"对象存储", map[string]model.AttrDef{
		"storage_class": {Type: "string", Description: "存储类型"},
		"endpoint":      {Type: "string", Description: "访问端点"},
	}},
	"disk": {"云硬盘", map[string]model.AttrDef{
		"size_gb":     {Type: "int", Description: "容量(GB)"},
		"disk_type":   {Type: "string", Description: "类型（SSD/高效云盘）"},
		"attached_vm": {Type: "string", Description: "挂载的云主机"},
	}},
	"firewall": {"安全组", map[string]model.AttrDef{
		"rules": {Type: "string", Description: "规则(JSON)"},
	}},
	"nat_gateway": {"NAT 网关", map[string]model.AttrDef{
		"eip": {Type: "string", Description: "弹性公网 IP"},
	}},
	"cdn": {"CDN", map[string]model.AttrDef{
		"domain": {Type: "string", Description: "加速域名"},
		"cname":  {Type: "string", Description: "CNAME"},
	}},
	"dns": {"DNS 解析", map[string]model.AttrDef{
		"domain":       {Type: "string", Description: "域名"},
		"record_count": {Type: "int", Description: "解析记录数"},
	}},
}

// cloudBaseSchema 云资源公共字段（所有云模型共有）。
func cloudBaseSchema() map[string]model.AttrDef {
	return map[string]model.AttrDef{
		"resource_id": {Type: "string", Description: "云资源唯一 ID（provider:type:id）"},
		"provider":    {Type: "string", Description: "云厂商"},
		"region":      {Type: "string", Description: "地域"},
		"zone":        {Type: "string", Description: "可用区"},
		"status":      {Type: "string", Description: "状态"},
	}
}

// cloudModelTypes 生成云资源系列内置模型：每厂商 1 个抽象父类（云平台）+ 各资源类型 leaf。
func cloudModelTypes() []model.CIModelType {
	out := make([]model.CIModelType, 0, len(cloudVendors)*6)
	for _, v := range cloudVendors {
		// 厂商抽象父类（不可直接实例化，作厂商云平台实体）
		out = append(out, model.CIModelType{
			ID:          v.vendor,
			Name:        v.name,
			Description: v.name + "云平台（抽象父类，承载" + v.name + "各类云资源）",
			Category:    model.ModelCategoryCloud,
			SubCategory: v.subCategory,
			Icon:        "☁️",
			ShowInMenu:  true,
			Builtin:     true,
			Abstract:    true,
			Author:      model.BuiltinAuthor,
			Schema:      cloudBaseSchema(),
		})
		for _, rt := range v.resourceTypes {
			meta, ok := cloudResourceTypes[rt]
			if !ok {
				continue
			}
			schema := cloudBaseSchema()
			for k, def := range meta.fields {
				schema[k] = def
			}
			out = append(out, model.CIModelType{
				ID:          v.vendor + "_" + rt,
				Name:        v.name + " " + meta.label,
				Description: v.name + meta.label + "（" + rt + "）",
				ParentID:    v.vendor,
				Category:    model.ModelCategoryCloud,
				SubCategory: v.subCategory,
				Icon:        "☁️",
				ShowInMenu:  true,
				Builtin:     true,
				Author:      model.BuiltinAuthor,
				Source:      "seed",
				Schema:      schema,
			})
		}
	}
	return out
}

// enrichImpactWithBusiness 为影响分析结果补充业务视角：
//   - AffectedCIDetails：受影响 CI 明细（名称/类型/业务关键性）；
//   - BusinessServices：受影响的服务链顶层业务服务（按关键性降序）；
//   - ImpactLevel：综合影响等级（取受影响业务服务最高关键性；无业务服务时
//     按源 CI 类型推导，兜底 low）。
//
// 依赖 pg 查询 CI 明细；pg 不可用时仅保留底层结果（向后兼容）。
func (s *Service) enrichImpactWithBusiness(ctx context.Context, tenantID string, base *model.ImpactResult) (*model.ImpactResult, error) {
	if base == nil || s.pg == nil {
		return base, nil
	}
	if len(base.AffectedCI) == 0 {
		base.ImpactLevel = "low"
		return base, nil
	}

	ids := make([]string, 0, len(base.AffectedCI)+1)
	ids = append(ids, base.AffectedCI...)
	if base.SourceCI != "" {
		ids = append(ids, base.SourceCI)
	}

	seen := map[string]bool{}
	var crits []string
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		ci, err := s.pg.GetCI(ctx, id, tenantID)
		if err != nil || ci == nil {
			continue
		}
		detail := model.AffectedCIDetail{ID: ci.ID, Name: ci.Name, TypeID: ci.TypeID}
		if c, ok := ci.Attributes[model.AttrKeyCriticality].(string); ok {
			detail.Criticality = c
		}
		base.AffectedCIDetails = append(base.AffectedCIDetails, detail)

		if ci.TypeID == model.ModelTypeBusinessService && id != base.SourceCI {
			base.BusinessServices = append(base.BusinessServices, model.BusinessImpact{
				CIID: ci.ID, Name: ci.Name, Criticality: detail.Criticality,
			})
			if detail.Criticality != "" {
				crits = append(crits, detail.Criticality)
			}
		}
	}

	sortBusinessServices(base.BusinessServices)
	base.ImpactLevel = businessImpactLevel(crits)
	return base, nil
}

// businessImpactLevel 由受影响业务服务的关键性推导综合影响等级（最高级）。
// 无业务服务时返回 "low"（调用方可按源 CI 类型再推导）。
func businessImpactLevel(criticalities []string) string {
	rank := map[string]int{string(model.CriticalityCritical): 0, string(model.CriticalityHigh): 1, string(model.CriticalityMedium): 2, string(model.CriticalityLow): 3}
	best := ""
	for _, c := range criticalities {
		if r, ok := rank[c]; ok {
			if best == "" || r < rank[best] {
				best = c
			}
		}
	}
	if best == "" {
		return string(model.CriticalityLow)
	}
	return best
}

// sortBusinessServices 按关键性降序排列业务服务（critical < high < medium < low）。
func sortBusinessServices(services []model.BusinessImpact) {
	rank := map[string]int{string(model.CriticalityCritical): 0, string(model.CriticalityHigh): 1, string(model.CriticalityMedium): 2, string(model.CriticalityLow): 3}
	for i := 1; i < len(services); i++ {
		for j := i; j > 0 && rank[services[j].Criticality] < rank[services[j-1].Criticality]; j-- {
			services[j], services[j-1] = services[j-1], services[j]
		}
	}
}

// handleQualityFix 质量问题一键修复:
//   - consistency(孤儿关系): 删除指向不存在 CI 的关系;
//   - uniqueness(重复 IP/名称): 引导走调解合并(reconcile/merge), 此处返回指引。
func (s *Service) handleQualityFix(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)
	var req struct {
		Dimension  string `json:"dimension"`
		RelationID string `json:"relation_id"`
		CIID       string `json:"ci_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	switch req.Dimension {
	case string(quality.DimConsistency):
		if req.RelationID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "relation_id is required for consistency fix")
			return
		}
		if s.pg == nil {
			httputil.WriteError(w, http.StatusBadRequest, "requires pg backend")
			return
		}
		if err := s.pg.DeleteRelation(ctx, req.RelationID, tenantID, "quality-fix"); err != nil {
			if err.Error() == "relation not found" {
				httputil.WriteError(w, http.StatusNotFound, "relation not found")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"fixed": true, "dimension": req.Dimension, "relation_id": req.RelationID})
	case string(quality.DimUniqueness):
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"fixed": false, "hint": "重复 IP/名称请使用「实体调解合并」按渠道置信度合并"})
	default:
		httputil.WriteError(w, http.StatusBadRequest, "unsupported dimension: "+req.Dimension)
	}
}

// handleQualityScan 按需执行一次数据质量扫描（完整性/唯一性/一致性），返回质量报告。
func (s *Service) handleQualityScan(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	eng := quality.NewEngine(s.pg)
	report, err := eng.Scan(r.Context(), tenantID)
	if err != nil {
		slog.Error("quality scan failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 200, report)
}

func (s *Service) handleListDiscoveryResults(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	results, err := s.pg.ListDiscoveryResults(r.Context(), taskID, tenantID)
	if err != nil {
		slog.Error("list discovery results failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 200, results)
}

func (s *Service) handleSyncDiscoveryResult(w http.ResponseWriter, r *http.Request) {
	resultID := r.PathValue("resultId")
	tenantID := auth.TenantFromContext(r.Context())

	// 获取发现结果
	results, err := s.pg.ListDiscoveryResults(r.Context(), "", tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var target *model.DiscoveryResult
	for _, res := range results {
		if res.ID == resultID {
			target = res
			break
		}
	}
	if target == nil {
		httputil.WriteError(w, 404, "result not found")
		return
	}

	// 创建CI实例
	ci := &model.CIModel{
		TypeID:     target.CIType,
		TenantID:   tenantID,
		Name:       target.CIName,
		Status:     model.CIStatusActive,
		Attributes: target.Attributes,
		Channel:    model.WeightDiscovery,
	}
	if ci.Attributes == nil {
		ci.Attributes = make(map[string]any)
	}
	ci.Attributes["ip_address"] = target.IPAddress

	// 漂移检测: 按 IP 找已有 CI —— 有则对比差异生成漂移记录(不改 CI, 信号先行),
	// 无则按发现结果创建新 CI。
	if existing, err := s.pg.FindCIByIP(r.Context(), tenantID, target.IPAddress); err == nil && existing != nil {
		drifts := detectDrifts(existing, ci)
		for _, d := range drifts {
			if err := s.pg.SaveDrift(r.Context(), d); err != nil {
				slog.Warn("save drift failed", "error", err)
			}
		}
		// 标记已同步(指向已有 CI, 不重复创建)。
		s.pg.MarkDiscoveryResultSynced(r.Context(), resultID, existing.ID)
		httputil.WriteJSON(w, 200, map[string]any{"status": "drift_detected", "ci_id": existing.ID, "drifts": len(drifts)})
		return
	}

	if err := s.pg.CreateCI(r.Context(), ci, "discovery"); err != nil {
		slog.Error("sync discovery result failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create CI")
		return
	}

	// 标记已同步
	s.pg.MarkDiscoveryResultSynced(r.Context(), resultID, ci.ID)

	httputil.WriteJSON(w, 200, map[string]any{"status": "synced", "ci_id": ci.ID})
}

// detectDrifts 对比已有 CI(期望)与发现 CI(实际)的差异, 生成漂移记录。
// 只报告发现中出现的字段; 相同值不报。字符串化对比, 忽略类型差异。
func detectDrifts(existing, discovered *model.CIModel) []*model.CMDBDrift {
	var out []*model.CMDBDrift
	for k, v := range discovered.Attributes {
		ev, has := existing.Attributes[k]
		if !has {
			continue // 新字段不算漂移(可能是新增属性), 由采集合并处理
		}
		if fmt.Sprint(ev) != fmt.Sprint(v) {
			out = append(out, &model.CMDBDrift{
				CIID: existing.ID, TenantID: existing.TenantID,
				Field: k, Expected: fmt.Sprint(ev), Actual: fmt.Sprint(v),
			})
		}
	}
	return out
}

// handleIDCPanorama 机房机柜 2D 视图数据:
// 机柜主体以 cmdb_rack 表(管理视图录入, 含 idc_id/max_units/row/col)为数据源,
// 并兼容模型驱动录入的 rack CI(未在 cmdb_rack 登记的类型), 前端据此渲染
// 机房平面图 + 机柜正视图 —— 管理视图录入或模型驱动录入均可成图。
//
// 设备合并两条来源(历史割裂, 此处合并):
//  1. 设备 CI 自带 rack_id / rack_unit 属性(模型驱动录入);
//  2. 管理视图通过 U 位分配写入 cmdb_rack_unit 表(ci_id 占用), 设备 CI 本身
//     不含 rack_id 属性 —— 这条路径也必须被纳入, 否则 2D 视图设备永远空。
func (s *Service) handleIDCPanorama(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)
	if s.pg == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"racks": []any{}, "devices": []*model.CIModel{}})
		return
	}

	// 机柜主体: cmdb_rack 表 + cmdb_ci type=rack, 按 ID 去重合并。
	// 前端读取结构: attributes.room_id(机房分组) / attributes.max_units(U 位总数) / name / id。
	type panoRack struct {
		ID         string         `json:"id"`
		TypeID     string         `json:"type_id"`
		Name       string         `json:"name"`
		Attributes map[string]any `json:"attributes"`
	}
	rackByID := map[string]*panoRack{}
	allRacks, err := s.pg.ListAllRacks(ctx, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, rk := range allRacks {
		attrs := map[string]any{}
		if rk.IDCRoomID != "" {
			attrs["room_id"] = rk.IDCRoomID
		}
		if rk.MaxUnits > 0 {
			attrs["max_units"] = rk.MaxUnits
		}
		if rk.Row != "" {
			attrs["row"] = rk.Row
		}
		if rk.Col != "" {
			attrs["col"] = rk.Col
		}
		rackByID[rk.ID] = &panoRack{ID: rk.ID, TypeID: "rack", Name: rk.Name, Attributes: attrs}
	}
	// 兼容纯模型驱动场景: cmdb_ci type=rack 且未在 cmdb_rack 登记的。
	if rackCIs, err := s.pg.ListCI(ctx, tenantID, "rack"); err == nil {
		for _, rc := range rackCIs {
			if _, exists := rackByID[rc.ID]; exists {
				continue
			}
			attrs := map[string]any{}
			if v, ok := rc.Attributes["room_id"].(string); ok && v != "" {
				attrs["room_id"] = v
			}
			if v, ok := rc.Attributes["max_units"]; ok {
				if n, ok := v.(float64); ok && n > 0 {
					attrs["max_units"] = int(n)
				}
			}
			rackByID[rc.ID] = &panoRack{ID: rc.ID, TypeID: "rack", Name: rc.Name, Attributes: attrs}
		}
	}
	racks := make([]*panoRack, 0, len(rackByID))
	for _, rk := range rackByID {
		racks = append(racks, rk)
	}

	// 设备: 合并两条来源。
	devByID := map[string]*model.CIModel{}
	// 来源1: 各类型中带 rack_id 属性的(模型驱动录入)。
	for _, t := range []string{"server", "host", "storage", "firewall", "load_balancer", "router", "switch", "database", "middleware", "app"} {
		list, err := s.pg.ListCI(ctx, tenantID, t)
		if err != nil {
			continue
		}
		for _, ci := range list {
			if rackID, ok := ci.Attributes["rack_id"].(string); ok && rackID != "" {
				devByID[ci.ID] = ci
			}
		}
	}
	// 来源2: 管理视图 U 位占用表(cmdb_rack_unit.ci_id)。给设备 CI 注入
	// rack_id / rack_unit 属性, 使前端按相同字段渲染。
	unitCountByCI := map[string]int{} // ciID -> 该 CI 在本机柜占用的 U 位数(多 U 高度)
	for _, rk := range allRacks {
		units, err := s.pg.ListRackUnits(ctx, rk.ID, tenantID)
		if err != nil {
			continue
		}
		for _, u := range units {
			if u.CIID == "" {
				continue
			}
			unitCountByCI[u.CIID]++
			if _, exists := devByID[u.CIID]; exists {
				// 已存在: 仅补充 rack_unit(来源1 可能未带 U 位)。
				if _, hasUnit := devByID[u.CIID].Attributes["rack_unit"]; !hasUnit {
					devByID[u.CIID].Attributes["rack_unit"] = u.Unit
				}
				continue
			}
			ci, err := s.pg.GetCI(ctx, u.CIID, tenantID)
			if err != nil || ci == nil {
				continue
			}
			if ci.Attributes == nil {
				ci.Attributes = map[string]any{}
			}
			ci.Attributes["rack_id"] = rk.ID
			ci.Attributes["rack_unit"] = u.Unit
			devByID[ci.ID] = ci
		}
	}
	devices := make([]*model.CIModel, 0, len(devByID))
	for _, d := range devByID {
		// 多 U 高度: 优先 CI 属性 u_height, 其次 U 位占用表记录数, 默认 1。
		h := 1
		if v, ok := d.Attributes["u_height"]; ok {
			if n, ok := v.(float64); ok && n >= 1 {
				h = int(n)
			}
		}
		if c := unitCountByCI[d.ID]; c > h {
			h = c
		}
		if h > 1 {
			d.Attributes["u_height"] = h
		}
		devices = append(devices, d)
	}

	// 监控联动: 注入设备健康/告警状态(前端据此渲染健康色/角标)。nil 安全。
	if s.monitor != nil {
		if snap, err := s.monitor.Snapshot(ctx, tenantID); err == nil && snap != nil {
			alertsByCI := map[string]struct{ critical, warning int }{}
			for _, a := range snap.Alerts {
				k := alertsByCI[a.CIID]
				switch strings.ToLower(a.Severity) {
				case "critical":
					k.critical++
				case "warning":
					k.warning++
				}
				alertsByCI[a.CIID] = k
			}
			for _, d := range devices {
				if d.Attributes == nil {
					d.Attributes = map[string]any{}
				}
				if st, ok := snap.StatusByCI[d.ID]; ok {
					d.Attributes["health"] = st
				}
				if k, ok := alertsByCI[d.ID]; ok {
					if k.critical > 0 {
						d.Attributes["alert_critical"] = k.critical
					}
					if k.warning > 0 {
						d.Attributes["alert_warning"] = k.warning
					}
				}
			}
		}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"racks": racks, "devices": devices})
}

// handleListDrifts 漂移清单(可按 CI 过滤, 默认未解决)。
func (s *Service) handleListDrifts(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	ciID := r.URL.Query().Get("ci_id")
	includeResolved := r.URL.Query().Get("resolved") == "true"
	if s.pg == nil {
		httputil.WriteJSON(w, http.StatusOK, []*model.CMDBDrift{})
		return
	}
	drifts, err := s.pg.ListDrifts(r.Context(), tenantID, ciID, includeResolved, 500)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if drifts == nil {
		drifts = []*model.CMDBDrift{}
	}
	httputil.WriteJSON(w, http.StatusOK, drifts)
}

// handleResolveDrift 标记漂移已解决。
func (s *Service) handleResolveDrift(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.pg.ResolveDrift(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"resolved": true, "id": id})
}

func (s *Service) handleSyncAllDiscoveryResults(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskId")
	tenantID := auth.TenantFromContext(r.Context())

	affected, err := s.pg.SyncAllDiscoveryResults(r.Context(), taskID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 200, map[string]any{"synced_count": affected})
}

// discoverRelations 在已纳管 CI 之上自动推断关系（纳管后 CMDB 自动发现）。
// 不调用任何外部采集器，纯基于纳管数据派生：扫描每个 CI 属性中以 _id 结尾的外键，
// 匹配其他 CI 的 resource_id（按裸 id + provider 作用域），生成 relation 并去重后落库。
// 这样云/主机/容器由纳管层拉取生成基础 CI，CMDB 只负责在已有 CI 上补全拓扑关系，不重复拉资源。
func (s *Service) discoverRelations(ctx context.Context, tenantID string) (int, error) {
	cis, err := s.loadAllCIs(ctx, tenantID)
	if err != nil {
		return 0, fmt.Errorf("load CIs: %w", err)
	}

	byFull := make(map[string]*model.CIModel, len(cis))
	byBare := make(map[string][]*model.CIModel, len(cis))
	for _, ci := range cis {
		if ci.Attributes == nil {
			continue
		}
		if rid, ok := ci.Attributes["resource_id"].(string); ok && rid != "" {
			byFull[rid] = ci
			b := bareID(rid)
			byBare[b] = append(byBare[b], ci)
		}
	}

	// 已有关系去重
	existing := make(map[string]bool)
	if rels, e := s.pg.ListRelations(ctx, tenantID); e == nil {
		for _, r := range rels {
			existing[r.SourceID+"|"+r.TargetID+"|"+r.RelationType] = true
		}
	}

	created := 0
	for _, ci := range cis {
		if ci.Attributes == nil {
			continue
		}
		for key, val := range ci.Attributes {
			if !strings.HasSuffix(key, "_id") {
				continue
			}
			ref, ok := val.(string)
			if !ok || ref == "" {
				continue
			}
			for _, t := range matchTargets(ref, ci, byFull, byBare) {
				rt := relationTypeFor(key)
				relKey := ci.ID + "|" + t.ID + "|" + rt
				if existing[relKey] {
					continue
				}
				rel := &model.CIRelation{
					TenantID:     tenantID,
					SourceID:     ci.ID,
					TargetID:     t.ID,
					RelationType: rt,
					Properties: map[string]any{
						"discovered_by": "cmdb-relation-discovery",
						"ref_attr":      key,
					},
				}
				if err := s.persistRelation(ctx, rel); err != nil {
					slog.Warn("skip discovered relation", "src", ci.ID, "dst", t.ID, "err", err)
					continue
				}
				existing[relKey] = true
				created++
			}
		}
	}
	return created, nil
}

// loadAllCIs 分页拉取租户下全部 CI（typeID 留空表示不限类型）。
func (s *Service) loadAllCIs(ctx context.Context, tenantID string) ([]*model.CIModel, error) {
	var all []*model.CIModel
	const batch = 100
	for offset := 0; ; offset += batch {
		page, err := s.pg.ListCIPaginated(ctx, tenantID, "", "", "ASC", batch, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < batch {
			break
		}
	}
	return all, nil
}

// bareID 取 resource_id 的最后一段（自然键 "<provider>:<type>:<id>" 的裸 id）。
func bareID(resourceID string) string {
	if i := strings.LastIndex(resourceID, ":"); i >= 0 {
		return resourceID[i+1:]
	}
	return resourceID
}

// relationTypeFor 把外键属性名映射为语义化关系类型。
func relationTypeFor(refKey string) string {
	switch refKey {
	case "vpc_id":
		return "belongs_to_vpc"
	case "subnet_id":
		return "in_subnet"
	case "instance_id", "host_id":
		return "runs_on"
	case "cluster_id":
		return "member_of_cluster"
	case "node":
		return "runs_on_node"
	case "security_group_id", "security_group_ids":
		return "protected_by_sg"
	case "zone_id", "availability_zone":
		return "located_in_zone"
	default:
		return "references"
	}
}

// matchTargets 把外键值解析为候选目标 CI：先按完整 resource_id 精确匹配，
// 再按裸 id 匹配（子资源的 vpc_id="vpc-123" 对应父资源 resource_id="aliyun:vpc:vpc-123"），
// 最后按 provider 作用域收敛，避免跨云误连。
func matchTargets(ref string, src *model.CIModel, byFull map[string]*model.CIModel, byBare map[string][]*model.CIModel) []*model.CIModel {
	var raw []*model.CIModel
	if ci, ok := byFull[ref]; ok {
		raw = append(raw, ci)
	}
	if ci, ok := byBare[bareID(ref)]; ok {
		raw = append(raw, ci...)
	}

	seen := make(map[string]bool)
	srcProvider, _ := src.Attributes["provider"].(string)
	var out []*model.CIModel
	for _, t := range raw {
		if t.ID == src.ID || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		// provider 作用域：双方都有 provider 时要求一致，降低跨云误连概率
		if srcProvider != "" {
			if tp, ok := t.Attributes["provider"].(string); ok && tp != "" && tp != srcProvider {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// persistRelation 落库一条关系，先做环检测（Dgraph 或内存回退），避免引入拓扑环。
func (s *Service) persistRelation(ctx context.Context, rel *model.CIRelation) error {
	hasCycle := false
	if s.dg != nil {
		c, err := s.dg.DetectCycle(ctx, rel.SourceID, rel.TargetID, rel.TenantID)
		if err != nil {
			return fmt.Errorf("cycle detection failed: %w", err)
		}
		hasCycle = c
	} else if s.mem != nil {
		hasCycle = s.mem.detectCycle(rel.TenantID, rel.SourceID, rel.TargetID)
	}
	if hasCycle {
		return fmt.Errorf("would create a topological cycle")
	}
	if s.pg != nil {
		if err := s.pg.CreateRelation(ctx, rel, "discovery-relation"); err != nil {
			return err
		}
	} else if s.mem != nil {
		s.mem.createRelation(rel)
	}
	return nil
}

// CreateCI 创建一条 CI,复用与 HTTP handler 相同的落库与事件联动逻辑
// (pg.CreateCI + outbox 事件 + onCICreated 回调 → monitorTopo.RecordTenant / dgraph)。
// 供云资源同步等内部调用方使用,无需经过 HTTP 层。
func (s *Service) CreateCI(ctx context.Context, ci *model.CIModel, traceID string) error {
	if ci.Status == "" {
		ci.Status = model.CIStatusActive
	}
	if s.pg != nil {
		// 抽象父类（如 database/middleware）仅作继承基类，禁止直接实例化。
		if mt, err := s.pg.GetCIModelType(ctx, ci.TypeID, ci.TenantID); err == nil && mt != nil && !mt.IsInstantiable() {
			return fmt.Errorf("model type %q is abstract and cannot be instantiated directly", ci.TypeID)
		}
		if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes, ""); err != nil {
			return err
		}
		if err := s.pg.CreateCI(ctx, ci, traceID); err != nil {
			return err
		}
	} else {
		s.mem.createCI(ci)
	}
	if s.onCICreated != nil {
		s.onCICreated(ctx, ci.ID, ci.TenantID)
	}
	return nil
}

// UpdateCI 更新一条 CI,复用与 HTTP handler 相同的 anti-flapping 校验与事件联动逻辑。
// ch 表示本次更新的来源权重(云资源同步统一使用 WeightDiscovery)。
func (s *Service) UpdateCI(ctx context.Context, ci *model.CIModel, ch model.ChannelWeight, traceID string) error {
	if s.pg != nil {
		if err := s.pg.UpdateCI(ctx, ci, ch, traceID); err != nil {
			return err
		}
	} else {
		s.mem.updateCI(ci)
	}
	return nil
}

// findCIByAttribute 按 类型 + 字段 + 值 定位单个 CI(采集上报 identify 使用)。
// 字段键支持 field_id 或字段名(经 FieldByNameOrID 解析为实际字段名再比对)。
// 返回 nil 表示未找到(允许新建)。
func (s *Service) findCIByAttribute(ctx context.Context, tenantID, typeID, fieldKey string, value any) (*model.CIModel, error) {
	mt, err := s.ResolveModelType(ctx, typeID, tenantID)
	if err != nil {
		return nil, err
	}
	if mt == nil {
		return nil, nil
	}
	// 解析为实际字段名
	realName := fieldKey
	if def, ok := mt.FieldByNameOrID(fieldKey); ok {
		// 找到定义, 用 map 的键(字段名)作为比较基准; 需再定位 name。
		for name := range mt.Schema {
			if mt.Schema[name].ID == fieldKey || name == fieldKey {
				realName = name
				break
			}
		}
		_ = def
	}
	var list []*model.CIModel
	if s.pg != nil {
		list, err = s.pg.ListCI(ctx, tenantID, typeID)
		if err != nil {
			return nil, err
		}
	} else if s.mem != nil {
		list = s.mem.listCIs(tenantID, typeID)
	}
	for _, ci := range list {
		if ci.Attributes == nil {
			continue
		}
		if fmt.Sprintf("%v", ci.Attributes[realName]) == fmt.Sprintf("%v", value) {
			return ci, nil
		}
	}
	return nil, nil
}

// UpdateCIAttributes 按 ID 读取 CI 并合并更新其 Attributes 字段(供流程动作库 cmdb_update 调用)。
func (s *Service) UpdateCIAttributes(ctx context.Context, id, tenantID string, attrs map[string]any) error {
	var existing *model.CIModel
	if s.pg != nil {
		var err error
		existing, err = s.pg.GetCI(ctx, id, tenantID)
		if err != nil {
			return err
		}
	} else {
		existing = s.mem.getCI(id, tenantID)
	}
	if existing == nil {
		return fmt.Errorf("ci %s not found", id)
	}
	if existing.Attributes == nil {
		existing.Attributes = map[string]any{}
	}
	for k, v := range attrs {
		existing.Attributes[k] = v
	}
	return s.UpdateCI(ctx, existing, model.WeightITSM, "")
}
