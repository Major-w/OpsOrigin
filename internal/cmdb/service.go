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

	// 保存的查询策略（"我想查"，P1-3）
	mux.HandleFunc("GET /api/v1/cmdb/saved-queries", s.handleListSavedQueries)
	mux.HandleFunc("POST /api/v1/cmdb/saved-queries", s.handleCreateSavedQuery)
	mux.HandleFunc("GET /api/v1/cmdb/saved-queries/{id}/resolve", s.handleResolveSavedQuery)
	mux.HandleFunc("DELETE /api/v1/cmdb/saved-queries/{id}", s.handleDeleteSavedQuery)

	mux.HandleFunc("GET /api/v1/cmdb/topology/{id}", s.handleGetTopology)
	mux.HandleFunc("GET /api/v1/cmdb/impact/{id}", s.handleImpactAnalysis)
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

// buildTopologyFromRelations 以 rootID 为起点, 在关系切片上做双向 BFS(深度 maxDepth),
// 返回拓扑节点与边。节点名称/类型由调用方按需富化(pg 模式用 ListCI 映射)。
func buildTopologyFromRelations(tenantID, rootID string, rels []*model.CIRelation, maxDepth int) ([]model.TopologyNode, []model.TopologyEdge) {
	visited := map[string]bool{rootID: true}
	var nodes []model.TopologyNode
	var edges []model.TopologyEdge

	frontier := []string{rootID}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, cid := range frontier {
			for _, rel := range rels {
				if rel.TenantID != tenantID {
					continue
				}
				var neighbor string
				if rel.SourceID == cid {
					neighbor = rel.TargetID
				} else if rel.TargetID == cid {
					neighbor = rel.SourceID
				} else {
					continue
				}
				if !edgeExists(edges, rel.SourceID, rel.TargetID, rel.RelationType) {
					edges = append(edges, model.TopologyEdge{
						SourceID: rel.SourceID, TargetID: rel.TargetID, RelationType: rel.RelationType,
					})
				}
				if !visited[neighbor] {
					visited[neighbor] = true
					next = append(next, neighbor)
				}
			}
		}
		frontier = next
	}

	for id := range visited {
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
	merged := map[string]model.AttrDef{}
	for _, m := range chain { // 从根祖先到当前类型，后者覆盖前者
		for k, v := range m.Schema {
			merged[k] = v
		}
	}
	result := chain[len(chain)-1]
	result.Schema = merged
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
		"server":      {Label: "物理服务器/裸金属", CIType: "server", Channel: "agent", Note: "探针 Agent 本地采集硬件/系统信息（dmidecode 需权限）"},
		"host":        {Label: "物理服务器（别名）", CIType: "server", Channel: "agent", Note: "与 server 同实现"},
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
	return []model.CIModelType{
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
		},
		{
			ID:          model.ModelTypeBusinessService,
			Name:        "业务服务",
			Description: "面向业务的顶层服务（服务建模：业务服务依赖应用/数据库/中间件）",
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
		},
		{
			ID:          "virtual_machine",
			Name:        "虚拟机",
			Description: "虚拟机实例（VMware/KVM/OpenStack 等虚拟化平台）",
			Category:    model.ModelCategoryInfra,
			Icon:        "cpu",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"hypervisor": {Type: "string", Description: "虚拟化平台（vmware/kvm/openstack）"},
				"vcpu":       {Type: "int", Description: "vCPU 数"},
				"memory_gb":  {Type: "int", Description: "内存(GB)"},
				"host_name":  {Type: "string", Description: "宿主机"},
				"ip_address": {Type: "string", Description: "IP 地址"},
				"rack_id":    {Type: "string", Description: "所在机柜(rack 模型 CI ID)"},
				"rack_unit":  {Type: "int", Description: "占用 U 位"},
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
			Description: "数据中心（物理机房建筑，含多个机房）",
			Category:    model.ModelCategoryInfra,
			Icon:        "building",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"location": {Type: "string", Description: "地理位置"},
				"owner":    {Type: "string", Description: "负责人"},
			},
		},
		{
			ID:          "idc_room",
			Name:        "机房",
			Description: "数据中心内的机房/楼层（含机柜）",
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
		},
		{
			ID:          "rack",
			Name:        "机柜",
			Description: "标准机柜（42U），设备按 rack_id + rack_unit 属性落位",
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
		},
	}
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

// handleIDCPanorama 机房机柜 2D 视图数据(模型驱动):
// 返回 rack 机柜 CI 与所有带 rack_id 属性的设备 CI, 前端据此渲染
// 机房平面图 + 机柜正视图 —— 录入模型数据即成图。
func (s *Service) handleIDCPanorama(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)
	if s.pg == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"racks": []*model.CIModel{}, "devices": []*model.CIModel{}})
		return
	}
	racks, err := s.pg.ListCI(ctx, tenantID, "rack")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 设备: 各类型中带 rack_id 属性的(占用机柜 U 位)。
	var devices []*model.CIModel
	for _, t := range []string{"server", "virtual_machine", "storage", "firewall", "load_balancer", "router", "switch", "database", "middleware", "app"} {
		list, err := s.pg.ListCI(ctx, tenantID, t)
		if err != nil {
			continue
		}
		for _, ci := range list {
			if rackID, ok := ci.Attributes["rack_id"].(string); ok && rackID != "" {
				devices = append(devices, ci)
			}
		}
	}
	if racks == nil {
		racks = []*model.CIModel{}
	}
	if devices == nil {
		devices = []*model.CIModel{}
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
