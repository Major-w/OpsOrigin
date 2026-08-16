package cmdb

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/cmdb/quality"
	"github.com/opsorigin/core/internal/cmdb/registry"
	"github.com/opsorigin/core/internal/ingest"
	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/validate"

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
	mux.HandleFunc("POST /api/v1/cmdb/relation/batch", s.handleBatchCreateRelation) // 批量建关系
	// 实体调解合并(IRE): 疑似重复识别 + 按渠道置信度合并
	mux.HandleFunc("GET /api/v1/cmdb/reconcile/candidates", s.handleReconcileCandidates)
	mux.HandleFunc("POST /api/v1/cmdb/reconcile/merge", s.handleReconcileMerge)
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

// handleCreateCI godoc
// @Summary 创建 CI
// @Description 创建一个新的配置项
// @Tags CMDB
// @Accept json
// @Produce json
// @Param ci body object true "CI 信息"
// @Success 201 {object} model.CIModel
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/cmdb/ci [post]
// @Security BearerAuth
func (s *Service) handleCreateCI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var req struct {
		TypeID      string            `json:"type_id"`
		Name        string            `json:"name"`
		Description string            `json:"description,omitempty"`
		Attributes  map[string]any    `json:"attributes"`
		Labels      map[string]string `json:"labels,omitempty"`
		Channel     int               `json:"channel"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ci := &model.CIModel{
		TypeID:      req.TypeID,
		TenantID:    tenantID,
		Name:        req.Name,
		Description: req.Description,
		Status:      model.CIStatusActive,
		Attributes:  req.Attributes,
		Labels:      req.Labels,
		Channel:     resolveChannel(req.Channel),
	}

	traceID := traceIDFromCtx(ctx)
	if s.pg != nil {
		if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes, ""); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.pg.CreateCI(ctx, ci, traceID); err != nil {
			slog.Error("create ci failed", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		// 内存回退:跳过 schema 校验(无 modelType 表)
		s.mem.createCI(ci)
	}

	if s.onCICreated != nil {
		s.onCICreated(ctx, ci.ID, tenantID)
	}

	// P2-5 订阅分发：CI 创建事件
	s.dispatchCIEvent(ctx, tenantID, "ci_created", ci)

	writeJSON(w, http.StatusCreated, ci)
}

// handleBatchCreateCI 批量创建 CI(F4-B 决策 B: 异步化)。
// 请求体为 CI 数组; 每条经最小校验后提交到 ingest 缓冲(202 立即返回),
// 由后台 flush 批量落库, 绝不阻塞调用方。缓冲满时该条计入 ingest_dropped_total。
func (s *Service) handleBatchCreateCI(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())

	var reqs []struct {
		TypeID      string            `json:"type_id"`
		Name        string            `json:"name"`
		Description string            `json:"description,omitempty"`
		Attributes  map[string]any    `json:"attributes"`
		Labels      map[string]string `json:"labels,omitempty"`
		Channel     int               `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(reqs) == 0 {
		writeError(w, http.StatusBadRequest, "empty batch")
		return
	}

	accepted := 0
	for i := range reqs {
		req := &reqs[i]
		if req.Name == "" {
			continue
		}
		ci := &model.CIModel{
			TypeID:      req.TypeID,
			TenantID:    tenantID,
			Name:        req.Name,
			Description: req.Description,
			Status:      model.CIStatusActive,
			Attributes:  req.Attributes,
			Labels:      req.Labels,
			Channel:     resolveChannel(req.Channel),
		}
		if !s.ing.Submit(ci) {
			// 缓冲满: 丢弃(计入 dropped 指标), 不阻塞
			continue
		}
		accepted++
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": accepted,
		"total":    len(reqs),
	})
}

// handleGetCI godoc
// @Summary 获取 CI 详情
// @Description 根据 ID 获取配置项详情
// @Tags CMDB
// @Accept json
// @Produce json
// @Param id path string true "CI ID"
// @Success 200 {object} model.CIModel
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/cmdb/ci/{id} [get]
// @Security BearerAuth
func (s *Service) handleGetCI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	if s.pg == nil {
		ci := s.mem.getCI(id, tenantID)
		if ci == nil {
			writeError(w, http.StatusNotFound, "ci not found")
			return
		}
		writeJSON(w, http.StatusOK, ci)
		return
	}
	ci, err := s.pg.GetCI(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ci == nil {
		writeError(w, http.StatusNotFound, "ci not found")
		return
	}
	// P2-4 字段级规则权限
	if rules, rerr := s.pg.ListFieldRules(r.Context(), tenantID, ci.TypeID); rerr == nil {
		applyFieldRules([]*model.CIModel{ci}, rules, subjectFromRequest(r))
	}
	writeJSON(w, http.StatusOK, ci)
}

func (s *Service) handleUpdateCI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Status      string            `json:"status"`
		Attributes  map[string]any    `json:"attributes"`
		Labels      map[string]string `json:"labels,omitempty"`
		Channel     int               `json:"channel"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var existing *model.CIModel
	if s.pg != nil {
		var err error
		existing, err = s.pg.GetCI(ctx, id, tenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		// PG 不可用时才回退到内存存储,避免每次都多查一次
		existing = s.mem.getCI(id, tenantID)
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "ci not found")
		return
	}

	if req.Attributes == nil {
		req.Attributes = existing.Attributes
	}
	if req.Labels == nil {
		req.Labels = existing.Labels
	}
	if req.Name == "" {
		req.Name = existing.Name
	}
	if req.Status == "" {
		req.Status = string(existing.Status)
	}

	ci := &model.CIModel{
		ID:          id,
		TypeID:      existing.TypeID,
		TenantID:    tenantID,
		Name:        req.Name,
		Description: req.Description,
		Status:      model.CIStatus(req.Status),
		Attributes:  req.Attributes,
		Labels:      req.Labels,
	}

	channel := resolveChannel(req.Channel)
	traceID := traceIDFromCtx(ctx)

	if s.pg != nil {
		// 更新路径同样校验(必填/类型/唯一/引用), exclude 自身避免自撞。
		if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes, id); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.pg.UpdateCI(ctx, ci, channel, traceID); err != nil {
			if errors.Is(err, pg.ErrAntiFlapping) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			slog.Error("update ci failed", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		s.mem.updateCI(ci)
	}

	// P2-5 订阅分发：CI 更新事件
	s.dispatchCIEvent(ctx, tenantID, "ci_updated", ci)

	writeJSON(w, http.StatusOK, ci)
}

func (s *Service) handleDeleteCI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	traceID := traceIDFromCtx(r.Context())

	if s.pg == nil {
		if !s.mem.deleteCI(id, tenantID) {
			writeError(w, http.StatusNotFound, "ci not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// 删除前取快照，用于订阅分发（P2-5）
	before, _ := s.pg.GetCI(r.Context(), id, tenantID)
	if err := s.pg.DeleteCI(r.Context(), id, tenantID, traceID); err != nil {
		if err.Error() == "ci not found" {
			writeError(w, http.StatusNotFound, "ci not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if before != nil {
		s.dispatchCIEvent(r.Context(), tenantID, "ci_deleted", before)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListCI godoc
// @Summary 获取 CI 列表
// @Description 获取配置项列表，支持分页、排序、过滤
// @Tags CMDB
// @Accept json
// @Produce json
// @Param type_id query string false "CI 类型 ID"
// @Param sort_by query string false "排序字段"
// @Param sort_order query string false "排序方向 (asc/desc)"
// @Param limit query int false "每页数量"
// @Param offset query int false "偏移量"
// @Param archived query string false "归档过滤 (true/all/空)"
// @Success 200 {array} model.CIModel
// @Failure 500 {object} map[string]string
// @Router /api/v1/cmdb/ci [get]
// @Security BearerAuth
func (s *Service) handleListCI(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	typeID := r.URL.Query().Get("type_id")
	sortBy := r.URL.Query().Get("sort_by")
	sortOrder := r.URL.Query().Get("sort_order")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	// archived 过滤：true=仅归档 / all=含归档 / 其它(含空)=排除归档（默认）
	archivedMode := r.URL.Query().Get("archived")
	if archivedMode == "true" {
		archivedMode = "archived"
	}

	if s.pg == nil {
		cis := s.mem.listCIs(tenantID, typeID)
		if cis == nil {
			cis = []*model.CIModel{}
		}
		// 内存回退：按 archivedMode 过滤（status 字段）
		if archivedMode == "archived" {
			cis = filterByStatus(cis, model.CIStatusArchived)
		} else if archivedMode != "all" {
			cis = filterExcludeStatus(cis, model.CIStatusArchived)
		}
		writeJSON(w, http.StatusOK, cis)
		return
	}
	cis, err := s.pg.ListCIFiltered(r.Context(), tenantID, typeID, archivedMode, sortBy, sortOrder, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}
	// P2-4 字段级规则权限
	if rules, rerr := s.pg.ListFieldRules(r.Context(), tenantID, typeID); rerr == nil {
		applyFieldRules(cis, rules, subjectFromRequest(r))
	}
	writeJSON(w, http.StatusOK, cis)
}

func (s *Service) handleGetTopology(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	if s.dg == nil {
		nodes, edges := s.mem.queryTopology(tenantID, id, 3)
		writeJSON(w, http.StatusOK, map[string]any{
			"nodes": nodes,
			"edges": edges,
		})
		return
	}
	nodes, edges, err := s.dg.QueryTopology(r.Context(), tenantID, id, 3)
	if err != nil {
		slog.Error("query topology failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"edges": edges,
	})
}

func (s *Service) handleImpactAnalysis(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var result *model.ImpactResult
	if s.dg == nil {
		result, _ = s.mem.impactAnalysis(tenantID, id, 3)
	} else {
		var err error
		result, err = s.dg.ImpactAnalysis(r.Context(), tenantID, id, 3)
		if err != nil {
			slog.Error("impact analysis failed", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// 业务视角富化：受影响 CI 明细 + 业务服务 + 综合影响等级。
	enriched, err := s.enrichImpactWithBusiness(r.Context(), tenantID, result)
	if err != nil {
		slog.Warn("impact enrich failed, fallback to raw result", "error", err)
	}
	writeJSON(w, http.StatusOK, enriched)
}

func (s *Service) handleCIAudit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	if s.pg == nil {
		writeJSON(w, http.StatusOK, []*model.AttributeChange{})
		return
	}
	changes, err := s.pg.GetCIAudit(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if changes == nil {
		changes = []*model.AttributeChange{}
	}
	writeJSON(w, http.StatusOK, changes)
}

func (s *Service) handleSearchCI(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	q := r.URL.Query().Get("q")
	typeID := r.URL.Query().Get("type_id")
	// archived 过滤：true=仅归档 / all=含归档 / 其它(含空)=排除归档（与列表保持一致）
	archivedMode := r.URL.Query().Get("archived")
	if archivedMode == "true" {
		archivedMode = "archived"
	}

	if s.pg == nil {
		cis := s.mem.listCIs(tenantID, typeID)
		if q != "" {
			filtered := make([]*model.CIModel, 0)
			for _, ci := range cis {
				if strings.Contains(ci.Name, q) || strings.Contains(ci.Description, q) || strings.Contains(ci.TypeID, q) {
					filtered = append(filtered, ci)
				}
			}
			cis = filtered
		}
		if archivedMode == "archived" {
			cis = filterByStatus(cis, model.CIStatusArchived)
		} else if archivedMode != "all" {
			cis = filterExcludeStatus(cis, model.CIStatusArchived)
		}
		if cis == nil {
			cis = []*model.CIModel{}
		}
		writeJSON(w, http.StatusOK, cis)
		return
	}
	if q != "" {
		// Push the free-text filter down to SQL instead of loading the
		// entire CI table and filtering in Go — required for acceptable
		// performance as the dataset grows (see report issue 3.3).
		cis, err := s.pg.ListCIByQuery(r.Context(), tenantID, typeID, q)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if archivedMode == "archived" {
			cis = filterByStatus(cis, model.CIStatusArchived)
		} else if archivedMode != "all" {
			cis = filterExcludeStatus(cis, model.CIStatusArchived)
		}
		if cis == nil {
			cis = []*model.CIModel{}
		}
		// P2-4 字段级规则权限
		if rules, rerr := s.pg.ListFieldRules(r.Context(), tenantID, typeID); rerr == nil {
			applyFieldRules(cis, rules, subjectFromRequest(r))
		}
		writeJSON(w, http.StatusOK, cis)
		return
	}
	cis, err := s.pg.ListCI(r.Context(), tenantID, typeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if archivedMode == "archived" {
		cis = filterByStatus(cis, model.CIStatusArchived)
	} else if archivedMode != "all" {
		cis = filterExcludeStatus(cis, model.CIStatusArchived)
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}
	// P2-4 字段级规则权限
	if rules, rerr := s.pg.ListFieldRules(r.Context(), tenantID, typeID); rerr == nil {
		applyFieldRules(cis, rules, subjectFromRequest(r))
	}
	writeJSON(w, http.StatusOK, cis)
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

func (s *Service) handleListRelations(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())

	if s.pg == nil {
		rels := s.mem.listRelations(tenantID)
		if rels == nil {
			rels = []*model.CIRelation{}
		}
		writeJSON(w, http.StatusOK, rels)
		return
	}
	rels, err := s.pg.ListRelations(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rels == nil {
		rels = []*model.CIRelation{}
	}
	writeJSON(w, http.StatusOK, rels)
}

func (s *Service) handleCreateRelation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var req struct {
		SourceID     string         `json:"source_id"`
		TargetID     string         `json:"target_id"`
		RelationType string         `json:"relation_type"`
		Properties   map[string]any `json:"properties,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	hasCycle := false
	if s.dg != nil {
		var err error
		hasCycle, err = s.dg.DetectCycle(ctx, req.SourceID, req.TargetID, tenantID)
		if err != nil {
			slog.Error("cycle detection failed", "error", err)
		}
	} else {
		hasCycle = s.mem.detectCycle(tenantID, req.SourceID, req.TargetID)
	}
	if hasCycle {
		writeError(w, http.StatusConflict, "adding this relation would create a topological cycle [ASSERT-CMDB-02]")
		return
	}

	rel := &model.CIRelation{
		TenantID:     tenantID,
		SourceID:     req.SourceID,
		TargetID:     req.TargetID,
		RelationType: req.RelationType,
		Properties:   req.Properties,
	}

	traceID := traceIDFromCtx(ctx)
	if s.pg != nil {
		if err := s.pg.CreateRelation(ctx, rel, traceID); err != nil {
			slog.Error("create relation failed", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		s.mem.createRelation(rel)
	}

	writeJSON(w, http.StatusCreated, rel)
}

// handleBatchUpdateCI 批量更新多个 CI 的属性(合并写), 逐个过 validateAttributes
// (唯一/引用/类型校验)。返回成功数与逐条失败明细, 不因单条失败中断整体。
func (s *Service) handleBatchUpdateCI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var req struct {
		IDs   []string       `json:"ids"`
		Attrs map[string]any `json:"attrs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 || len(req.Attrs) == 0 {
		writeError(w, http.StatusBadRequest, "ids and attrs are required")
		return
	}

	traceID := traceIDFromCtx(ctx)
	type failure struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	var failed []failure
	updated := 0

	for _, id := range req.IDs {
		var ci *model.CIModel
		if s.pg != nil {
			var err error
			ci, err = s.pg.GetCI(ctx, id, tenantID)
			if err != nil {
				failed = append(failed, failure{id, err.Error()})
				continue
			}
		} else {
			ci = s.mem.getCI(id, tenantID)
		}
		if ci == nil {
			failed = append(failed, failure{id, "ci not found"})
			continue
		}
		// 合并属性(批量只改给定字段, 不动其余)
		merged := map[string]any{}
		for k, v := range ci.Attributes {
			merged[k] = v
		}
		for k, v := range req.Attrs {
			merged[k] = v
		}
		ci.Attributes = merged

		if s.pg != nil {
			if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes, id); err != nil {
				failed = append(failed, failure{id, err.Error()})
				continue
			}
			if err := s.pg.UpdateCI(ctx, ci, model.WeightHuman, traceID); err != nil {
				failed = append(failed, failure{id, err.Error()})
				continue
			}
		} else {
			s.mem.updateCI(ci)
		}
		updated++
	}

	writeJSON(w, http.StatusOK, map[string]any{"updated": updated, "failed": failed})
}

// handleBatchDeleteCI 批量删除 CI(含订阅事件分发), 返回成功数与失败明细。
func (s *Service) handleBatchDeleteCI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids are required")
		return
	}

	traceID := traceIDFromCtx(ctx)
	type failure struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	var failed []failure
	deleted := 0

	for _, id := range req.IDs {
		if s.pg == nil {
			if !s.mem.deleteCI(id, tenantID) {
				failed = append(failed, failure{id, "ci not found"})
			} else {
				deleted++
			}
			continue
		}
		before, _ := s.pg.GetCI(ctx, id, tenantID)
		if before == nil {
			failed = append(failed, failure{id, "ci not found"})
			continue
		}
		if err := s.pg.DeleteCI(ctx, id, tenantID, traceID); err != nil {
			failed = append(failed, failure{id, err.Error()})
			continue
		}
		s.dispatchCIEvent(ctx, tenantID, "ci_deleted", before)
		deleted++
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "failed": failed})
}

// handleBatchCreateRelation 批量创建关系(逐条环路检测, ASSERT-CMDB-02)。
func (s *Service) handleBatchCreateRelation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var req struct {
		Relations []struct {
			SourceID     string         `json:"source_id"`
			TargetID     string         `json:"target_id"`
			RelationType string         `json:"relation_type"`
			Properties   map[string]any `json:"properties,omitempty"`
		} `json:"relations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Relations) == 0 {
		writeError(w, http.StatusBadRequest, "relations are required")
		return
	}

	traceID := traceIDFromCtx(ctx)
	type failure struct {
		SourceID string `json:"source_id"`
		TargetID string `json:"target_id"`
		Error    string `json:"error"`
	}
	var failed []failure
	created := 0

	for _, item := range req.Relations {
		// 环路检测(与单条创建一致)
		hasCycle := false
		if s.dg != nil {
			var err error
			hasCycle, err = s.dg.DetectCycle(ctx, item.SourceID, item.TargetID, tenantID)
			if err != nil {
				slog.Error("cycle detection failed", "error", err)
			}
		} else {
			hasCycle = s.mem.detectCycle(tenantID, item.SourceID, item.TargetID)
		}
		if hasCycle {
			failed = append(failed, failure{item.SourceID, item.TargetID, "would create a topological cycle [ASSERT-CMDB-02]"})
			continue
		}
		rel := &model.CIRelation{
			TenantID:     tenantID,
			SourceID:     item.SourceID,
			TargetID:     item.TargetID,
			RelationType: item.RelationType,
			Properties:   item.Properties,
		}
		if s.pg != nil {
			if err := s.pg.CreateRelation(ctx, rel, traceID); err != nil {
				failed = append(failed, failure{item.SourceID, item.TargetID, err.Error()})
				continue
			}
		} else {
			s.mem.createRelation(rel)
		}
		created++
	}

	writeJSON(w, http.StatusOK, map[string]any{"created": created, "failed": failed})
}

func (s *Service) handleDeleteRelation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	traceID := traceIDFromCtx(r.Context())

	if s.pg == nil {
		if !s.mem.deleteRelation(id, tenantID) {
			writeError(w, http.StatusNotFound, "relation not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.pg.DeleteRelation(r.Context(), id, tenantID, traceID); err != nil {
		if err.Error() == "relation not found" {
			writeError(w, http.StatusNotFound, "relation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (s *Service) handleCreateModelType(w http.ResponseWriter, r *http.Request) {
	var mt model.CIModelType
	if err := json.NewDecoder(r.Body).Decode(&mt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.Name("模型名称", mt.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mt.TenantID = tenantFromCtx(r.Context())

	// 模型继承：禁止自引用（读取侧有环检测兜底，此处阻断明显错误）。
	// 注意：新建时 mt.ID 与 mt.ParentID 均为空，不能误判为"继承自身"（历史 bug：新建必失败）。
	if mt.ID != "" && mt.ParentID == mt.ID {
		writeError(w, http.StatusBadRequest, "模型不能继承自身")
		return
	}
	if mt.Category == "" {
		mt.Category = model.ModelCategoryPlatform
	}

	if err := s.pg.CreateCIModelType(r.Context(), &mt); err != nil {
		slog.Error("create model type failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, mt)
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

// handleFieldUsage 字段消费度统计(以流定库/MVD 的执行依据):
// 扫描保存查询与 ITSM 表单 schema 对模型字段的引用, 产出每字段的
// 消费来源与次数; 无任何消费的字段标 unused(僵尸字段, 可考虑下线)。
func (s *Service) handleFieldUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	tenantID := tenantFromCtx(ctx)

	mt, err := s.pg.GetCIModelType(ctx, id, tenantID)
	if err != nil || mt == nil {
		writeError(w, http.StatusNotFound, "model type not found")
		return
	}

	// 收集消费引用: 保存查询 attrs + ITSM 表单字段 key
	saved := map[string]int{}
	savedQueries, err := s.pg.ListSavedQueries(ctx, tenantID)
	if err == nil {
		for _, q := range savedQueries {
			if attrs, ok := q.Query["attrs"].(map[string]any); ok {
				for k := range attrs {
					saved[k]++
				}
			}
		}
	}
	form := map[string]int{}
	formFields, err := s.pg.ListFormSchemaFields(ctx, tenantID)
	if err == nil {
		for _, f := range formFields {
			if k, ok := f["key"].(string); ok && k != "" {
				form[k]++
			}
		}
	}

	fields := make([]fieldUsage, 0, len(mt.Schema))
	for name, def := range mt.Schema {
		u := fieldUsage{Field: name, Type: def.Type, Required: def.Required,
			SavedQueries: saved[name], ITSMForms: form[name]}
		u.Total = u.SavedQueries + u.ITSMForms
		u.Unused = u.Total == 0
		fields = append(fields, u)
	}
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Unused != fields[j].Unused {
			return !fields[i].Unused // unused 排后
		}
		return fields[i].Total > fields[j].Total
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"model_type_id": id, "model_name": mt.Name,
		"fields": fields, "unused_count": countUnused(fields),
	})
}

func (s *Service) handleGetModelType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	tenantID := tenantFromCtx(r.Context())
	mt, err := s.ResolveModelType(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mt == nil {
		writeError(w, http.StatusNotFound, "model type not found")
		return
	}
	writeJSON(w, http.StatusOK, mt)
}

func (s *Service) handleListModelTypes(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())

	// 获取分页参数
	limit := 100
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	types, err := s.pg.ListCIModelTypes(r.Context(), tenantID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 合并继承属性并默认分类，保证列表/详情展示完整属性集
	for _, mt := range types {
		if resolved, rerr := s.ResolveModelType(r.Context(), mt.ID, tenantID); rerr == nil && resolved != nil {
			mt.Schema = resolved.Schema
		}
		if mt.Category == "" {
			mt.Category = model.ModelCategoryPlatform
		}
	}

	// 获取总数
	total, err := s.pg.CountCIModelTypes(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if types == nil {
		types = []*model.CIModelType{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":  types,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Service) handleUpdateModelType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	// 获取现有模型类型
	existing, err := s.pg.GetCIModelType(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "model type not found")
		return
	}

	var mt model.CIModelType
	if err := json.NewDecoder(r.Body).Decode(&mt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 保留不可更新的字段
	mt.ID = existing.ID
	mt.TenantID = existing.TenantID
	mt.CreatedAt = existing.CreatedAt
	mt.Version = existing.Version + 1

	// 模型版本留痕: 更新前把旧状态存快照(可追溯/回滚)。
	if err := s.pg.SaveModelTypeVersion(r.Context(), &model.ModelTypeVersion{
		ModelTypeID: existing.ID, TenantID: existing.TenantID, Version: existing.Version,
		Name: existing.Name, Description: existing.Description, ParentID: existing.ParentID,
		Category: existing.Category, Icon: existing.Icon, Abstract: existing.Abstract,
		Schema: existing.Schema, Operator: "admin", CreatedAt: time.Now(),
	}); err != nil {
		slog.Warn("save model type version failed", "error", err) // 版本留痕失败不阻断更新
	}

	if err := s.pg.UpdateCIModelType(r.Context(), &mt); err != nil {
		slog.Error("update model type failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, mt)
}

// handleListModelTypeVersions 模型版本历史(新→旧, 含 schema 快照供前端 diff)。
func (s *Service) handleListModelTypeVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	versions, err := s.pg.ListModelTypeVersions(r.Context(), id, tenantID, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []*model.ModelTypeVersion{}
	}
	writeJSON(w, http.StatusOK, versions)
}

// handleRollbackModelType 回滚模型到指定版本(恢复 schema/名称/分类等)。
func (s *Service) handleRollbackModelType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	versionStr := r.PathValue("version")
	tenantID := tenantFromCtx(ctx)

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	snap, err := s.pg.GetModelTypeVersion(ctx, id, tenantID, version)
	if err != nil || snap == nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	current, err := s.pg.GetCIModelType(ctx, id, tenantID)
	if err != nil || current == nil {
		writeError(w, http.StatusNotFound, "model type not found")
		return
	}

	// 当前状态先留痕(回滚本身也是一次变更)。
	if err := s.pg.SaveModelTypeVersion(ctx, &model.ModelTypeVersion{
		ModelTypeID: current.ID, TenantID: current.TenantID, Version: current.Version,
		Name: current.Name, Description: current.Description, ParentID: current.ParentID,
		Category: current.Category, Icon: current.Icon, Abstract: current.Abstract,
		Schema: current.Schema, Operator: "rollback", CreatedAt: time.Now(),
	}); err != nil {
		slog.Warn("save pre-rollback version failed", "error", err)
	}

	current.Name = snap.Name
	current.Description = snap.Description
	current.ParentID = snap.ParentID
	current.Category = snap.Category
	current.Icon = snap.Icon
	current.Abstract = snap.Abstract
	current.Schema = snap.Schema
	current.Version = snap.Version + 1

	if err := s.pg.UpdateCIModelType(ctx, current); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model_type_id": id, "version": current.Version})
}

func (s *Service) handleDeleteModelType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	// 检查是否有CI实例使用此模型类型
	count, err := s.pg.CountCIByTypeID(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count > 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot delete model type with %d CI instances", count))
		return
	}

	if err := s.pg.DeleteCIModelType(r.Context(), id, tenantID); err != nil {
		slog.Error("delete model type failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleExportModelTypes 导出模型定义为 JSON 数组（跳过内置模型：内置模型不可导出）。
func (s *Service) handleExportModelTypes(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	types, err := s.pg.ListCIModelTypes(r.Context(), tenantID, 1000, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ids := map[string]bool{}
	if q := r.URL.Query().Get("ids"); q != "" {
		for _, x := range strings.Split(q, ",") {
			if t := strings.TrimSpace(x); t != "" {
				ids[t] = true
			}
		}
	}
	out := make([]map[string]any, 0, len(types))
	for _, mt := range types {
		if mt.Builtin {
			continue
		}
		if len(ids) > 0 && !ids[mt.ID] {
			continue
		}
		out = append(out, map[string]any{
			"id":           mt.ID,
			"name":         mt.Name,
			"description":  mt.Description,
			"parent_id":    mt.ParentID,
			"category":     mt.Category,
			"sub_category": mt.SubCategory,
			"icon":         mt.Icon,
			"show_in_menu": mt.ShowInMenu,
			"schema":       mt.Schema,
		})
	}
	w.Header().Set("Content-Disposition", `attachment; filename="cmdb-model-types.json"`)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, http.StatusOK, out)
}

// handleImportModelTypes 从 JSON 数组导入模型定义。
// 父子排序后创建，old id → new id 映射保证继承关系重建；重名/内置/无名跳过。
func (s *Service) handleImportModelTypes(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var raw []model.CIModelType
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	inSet := map[string]bool{}
	for _, d := range raw {
		if d.ID != "" {
			inSet[d.ID] = true
		}
	}
	type item struct {
		d           model.CIModelType
		parentFirst bool
	}
	var seq []item
	for _, d := range raw {
		parentFirst := d.ParentID == "" || !inSet[d.ParentID]
		seq = append(seq, item{d: d, parentFirst: parentFirst})
	}
	// 父在前：parentFirst=true 的排前面
	sort.SliceStable(seq, func(i, j int) bool {
		return seq[i].parentFirst && !seq[j].parentFirst
	})

	idMap := map[string]string{} // old id -> new id
	created, skipped := 0, 0
	for _, it := range seq {
		d := it.d
		if d.Builtin || d.Name == "" {
			skipped++
			continue
		}
		if existing, _ := s.pg.GetCIModelTypeByName(r.Context(), d.Name, tenantID); existing != nil {
			skipped++
			continue
		}
		parentNew := ""
		if d.ParentID != "" {
			if v, ok := idMap[d.ParentID]; ok {
				parentNew = v
			} else if p, _ := s.pg.GetCIModelType(r.Context(), d.ParentID, tenantID); p != nil {
				parentNew = d.ParentID // 父已在本租户
			}
		}
		mt := &model.CIModelType{
			Name:        d.Name,
			Description: d.Description,
			ParentID:    parentNew,
			Category:    d.Category,
			SubCategory: d.SubCategory,
			Icon:        d.Icon,
			ShowInMenu:  d.ShowInMenu,
			Schema:      d.Schema,
			TenantID:    tenantID,
		}
		if mt.Category == "" {
			mt.Category = model.ModelCategoryPlatform
		}
		if err := s.pg.CreateCIModelType(r.Context(), mt); err != nil {
			slog.Error("import model type failed", "error", err, "name", d.Name)
			skipped++
			continue
		}
		if d.ID != "" {
			idMap[d.ID] = mt.ID
		}
		created++
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "skipped": skipped})
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

// handleExportCI 导出某类型全部 CI 为 CSV 附件（表头: id,name,description,status + 模型 schema 属性列）。
func (s *Service) handleExportCI(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	typeID := r.URL.Query().Get("type_id")
	if typeID == "" {
		writeError(w, http.StatusBadRequest, "type_id is required")
		return
	}
	mt, err := s.ResolveModelType(r.Context(), typeID, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	attrCols := []string{}
	if mt != nil {
		for k := range mt.Schema {
			attrCols = append(attrCols, k)
		}
		sort.Strings(attrCols)
	}
	cols := append([]string{"id", "name", "description", "status"}, attrCols...)

	var cis []*model.CIModel
	if s.pg != nil {
		cis, err = s.pg.ListCI(r.Context(), tenantID, typeID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		cis = s.mem.listCIs(tenantID, typeID)
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	if err := cw.Write(cols); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, ci := range cis {
		row := []string{ci.ID, ci.Name, ci.Description, string(ci.Status)}
		for _, c := range attrCols {
			row = append(row, toCSVValue(ci.Attributes[c]))
		}
		if err := cw.Write(row); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fname := fmt.Sprintf("cmdb_%s_%s.csv", typeID, time.Now().Format("20060102"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fname))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// handleImportCI 从上传的 CSV 批量导入 CI（支持按 id 列 upsert 更新，否则新建）。
func (s *Service) handleImportCI(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	typeID := r.URL.Query().Get("type_id")
	if typeID == "" {
		writeError(w, http.StatusBadRequest, "type_id is required")
		return
	}
	mt, err := s.ResolveModelType(r.Context(), typeID, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid csv: "+err.Error())
		return
	}
	if len(records) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"created": 0, "updated": 0, "failed": []any{}})
		return
	}

	header := records[0]
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	if _, ok := idx["name"]; !ok {
		writeError(w, http.StatusBadRequest, "csv must contain a 'name' column")
		return
	}
	get := func(row []string, col string) string {
		if i, ok := idx[col]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}

	var toCreate []*model.CIModel
	updated := 0
	var failed []map[string]any
	traceID := traceIDFromCtx(r.Context())

	for ri, row := range records[1:] {
		lineNo := ri + 2
		recordFailed := func(msg string) {
			failed = append(failed, map[string]any{"row": lineNo, "error": msg})
		}
		name := get(row, "name")
		if name == "" {
			recordFailed("name is empty")
			continue
		}
		attrs := map[string]any{}
		if mt != nil {
			loopBroken := false
			for k, def := range mt.Schema {
				raw := get(row, k)
				if raw == "" {
					continue
				}
				v, cerr := fromCSVValue(raw, def.Type)
				if cerr != nil {
					recordFailed(fmt.Sprintf("attr %s: %v", k, cerr))
					loopBroken = true
					break
				}
				attrs[k] = v
			}
			if loopBroken {
				continue
			}
			for k, def := range mt.Schema {
				if def.Required {
					if _, ok := attrs[k]; !ok {
						recordFailed(fmt.Sprintf("required attr %s missing", k))
						loopBroken = true
						break
					}
				}
			}
			if loopBroken {
				continue
			}
		}

		// upsert: 含 id 且该 id 存在则更新，否则新建
		if idVal := get(row, "id"); idVal != "" && s.pg != nil {
			if existing, gerr := s.pg.GetCI(r.Context(), idVal, tenantID); gerr == nil && existing != nil {
				existing.Name = name
				if d := get(row, "description"); d != "" {
					existing.Description = d
				}
				if st := get(row, "status"); st != "" {
					existing.Status = model.CIStatus(st)
				}
				for k, v := range attrs {
					existing.Attributes[k] = v
				}
				existing.Channel = model.WeightHuman
				if uerr := s.pg.UpdateCI(r.Context(), existing, model.WeightHuman, traceID); uerr != nil {
					recordFailed(uerr.Error())
				} else {
					updated++
				}
				continue
			}
		}

		ci := &model.CIModel{
			TypeID:      typeID,
			TenantID:    tenantID,
			Name:        name,
			Description: get(row, "description"),
			Status:      model.CIStatusActive,
			Attributes:  attrs,
			Channel:     model.WeightHuman,
		}
		if st := get(row, "status"); st != "" {
			ci.Status = model.CIStatus(st)
		}
		toCreate = append(toCreate, ci)
	}

	created := 0
	if len(toCreate) > 0 {
		if s.pg != nil {
			if berr := s.pg.BulkCreateCI(r.Context(), toCreate, traceID); berr != nil {
				writeError(w, http.StatusInternalServerError, "bulk create failed: "+berr.Error())
				return
			}
			created = len(toCreate)
		} else {
			for _, ci := range toCreate {
				s.mem.createCI(ci)
			}
			created = len(toCreate)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"created": created,
		"updated": updated,
		"failed":  failed,
	})
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	v = httputil.EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ============================================================
// AI 模型注册表 / 本体服务（设计文档阶段 1）
// ============================================================

// handleRegistryResolve 把 AI 特征解析为已有 leaf / 挂载 abstract 父类 / 新提案。
func (s *Service) handleRegistryResolve(w http.ResponseWriter, r *http.Request) {
	if s.typeRegistry == nil {
		writeError(w, http.StatusNotImplemented, "类型注册表未启用")
		return
	}
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var req struct {
		Features map[string]any `json:"features"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if len(req.Features) == 0 {
		writeError(w, http.StatusBadRequest, "features 不能为空")
		return
	}

	res, err := s.typeRegistry.ResolveType(ctx, tenantID, req.Features)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "类型解析失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleRegistryPropose 提交新类型提案（相似度去重 + 审核门控）。
func (s *Service) handleRegistryPropose(w http.ResponseWriter, r *http.Request) {
	if s.typeRegistry == nil {
		writeError(w, http.StatusNotImplemented, "类型注册表未启用")
		return
	}
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var prop registry.AITypeProposal
	if err := json.NewDecoder(r.Body).Decode(&prop); err != nil {
		writeError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	// 服务端托管的字段：租户来自上下文，状态/ID/时间戳由注册表内部生成。
	prop.TenantID = tenantID
	prop.Status = ""
	prop.ID = ""
	prop.CreatedAt = time.Time{}
	prop.UpdatedAt = time.Time{}

	id, status, err := s.typeRegistry.ProposeType(ctx, prop)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "提案提交失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status})
}

// handleRegistryPromote 证据累积后晋升某提案类型（提升置信度，达阈值即视为激活）。
func (s *Service) handleRegistryPromote(w http.ResponseWriter, r *http.Request) {
	if s.typeRegistry == nil {
		writeError(w, http.StatusNotImplemented, "类型注册表未启用")
		return
	}
	ctx := r.Context()
	tenantID := tenantFromCtx(ctx)

	var req struct {
		ID       string   `json:"id"`
		Evidence []string `json:"evidence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id 不能为空")
		return
	}

	if err := s.typeRegistry.PromoteType(ctx, tenantID, req.ID, req.Evidence); err != nil {
		writeError(w, http.StatusInternalServerError, "晋升失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID, "promoted": true})
}

// ============================================================
// Version History Handlers
// ============================================================

func (s *Service) handleListHistory(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	changeType := r.URL.Query().Get("change_type")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}

	versions, total, err := s.pg.ListAllCIHistory(r.Context(), tenantID, changeType, limit, offset)
	if err != nil {
		slog.Error("list history failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"items": versions, "total": total, "limit": limit, "offset": offset})
}

func (s *Service) handleListCIVersions(w http.ResponseWriter, r *http.Request) {
	ciID := r.PathValue("ciId")
	tenantID := tenantFromCtx(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 20
	}

	versions, total, err := s.pg.ListCIVersions(r.Context(), ciID, tenantID, limit, offset)
	if err != nil {
		slog.Error("list ci versions failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"items": versions, "total": total, "limit": limit, "offset": offset})
}

func (s *Service) handleGetVersionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, err := s.pg.GetCIVersion(r.Context(), id)
	if err != nil {
		slog.Error("get version detail failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if version == nil {
		writeError(w, 404, "version not found")
		return
	}
	writeJSON(w, 200, version)
}

// ============================================================
// Discovery Handlers
// ============================================================

func (s *Service) handleListDiscoveryTasks(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	tasks, err := s.pg.ListDiscoveryTasks(r.Context(), tenantID)
	if err != nil {
		slog.Error("list discovery tasks failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 200, tasks)
}

func (s *Service) handleCreateDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var input struct {
		Name   string         `json:"name"`
		Type   string         `json:"type"`
		Target string         `json:"target"`
		Config map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if input.Name == "" || input.Type == "" || input.Target == "" {
		writeError(w, 400, "name, type, target are required")
		return
	}

	task := &model.DiscoveryTask{
		TenantID: tenantID,
		Name:     input.Name,
		Type:     input.Type,
		Target:   input.Target,
		Config:   input.Config,
	}
	if task.Config == nil {
		task.Config = make(map[string]any)
	}

	if err := s.pg.CreateDiscoveryTask(r.Context(), task); err != nil {
		slog.Error("create discovery task failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 201, task)
}

func (s *Service) handleUpdateDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	existing, err := s.pg.GetDiscoveryTask(r.Context(), id, tenantID)
	if err != nil {
		slog.Error("get discovery task failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if existing == nil {
		writeError(w, 404, "task not found")
		return
	}

	var input struct {
		Name   string         `json:"name"`
		Type   string         `json:"type"`
		Target string         `json:"target"`
		Config map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, 400, "invalid body")
		return
	}

	if input.Name != "" {
		existing.Name = input.Name
	}
	if input.Type != "" {
		existing.Type = input.Type
	}
	if input.Target != "" {
		existing.Target = input.Target
	}
	if input.Config != nil {
		existing.Config = input.Config
	}

	if err := s.pg.UpdateDiscoveryTask(r.Context(), existing); err != nil {
		slog.Error("update discovery task failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 200, existing)
}

func (s *Service) handleDeleteDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.pg.DeleteDiscoveryTask(r.Context(), id, tenantID); err != nil {
		slog.Error("delete discovery task failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Service) handleRunDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	task, err := s.pg.GetDiscoveryTask(r.Context(), id, tenantID)
	if err != nil || task == nil {
		writeError(w, 404, "task not found")
		return
	}

	// 架构边界：云/主机/容器属于「资源纳管」层（internal/cloud）职责，由纳管层
	// 拉取后直接生成基础 CI（单一数据源）；CMDB 自动发现只负责在已纳管 CI 之上做
	// 关系/拓扑推断(relation)，以及在探针 Agent 上跑插件深度扫描网络/中间件/数据库/
	// 应用/物理服务器（经 discovery.Registry 注册的插件），不得重复拉取资源。
	switch task.Type {
	case "cloud", "host", "container":
		task.Status = "failed"
		_ = s.pg.UpdateDiscoveryTask(r.Context(), task)
		writeError(w, http.StatusBadRequest, "类型 "+task.Type+" 属于资源纳管层职责，请使用资源纳管（云/主机/容器）同步，而非 CMDB 自动发现")
		return
	case "relation":
		// 内置关系发现（在已纳管 CI 之上推断）
	default:
		// 其余类型经注册表路由到真实发现插件；引擎未初始化或类型未注册则拒绝。
		if s.discoveryReg == nil || s.discoveryRunner == nil {
			task.Status = "failed"
			_ = s.pg.UpdateDiscoveryTask(r.Context(), task)
			writeError(w, http.StatusInternalServerError, "自动发现引擎未初始化（discovery registry / runner 未注入）")
			return
		}
		if _, ok := s.discoveryReg.Lookup(task.Type); !ok {
			task.Status = "failed"
			_ = s.pg.UpdateDiscoveryTask(r.Context(), task)
			writeError(w, http.StatusBadRequest, "不支持的发现类型: "+task.Type+"（可用类型见 GET /api/v1/cmdb/discovery/types）")
			return
		}
	}

	// 更新状态为 running
	task.Status = "running"
	s.pg.UpdateDiscoveryTask(r.Context(), task)

	go func() {
		ctx := context.Background()
		now := time.Now()
		task.LastRunAt = &now

		var found int
		switch task.Type {
		case "relation":
			// 纳管后自动发现：在已纳管 CI 之上推断关系，直接落库（不走 CI 预览/确认流程）
			n, derr := s.discoverRelations(ctx, tenantID)
			if derr != nil {
				slog.Error("relation discovery failed", "err", derr)
				task.Status = "failed"
				_ = s.pg.UpdateDiscoveryTask(ctx, task)
				return
			}
			found = n
		default:
			// 经注册表路由到真实发现插件（server/host/snmp/network/database/middleware/application/app），
			// 在探针 Agent 上执行脚本并解析产出预览结果，供确认后落库为 CI。
			got, derr := s.runDiscoveryPlugin(ctx, task)
			if derr != nil {
				slog.Error("discovery plugin failed", "type", task.Type, "err", derr)
				task.Status = "failed"
				_ = s.pg.UpdateDiscoveryTask(ctx, task)
				return
			}
			for _, it := range got {
				res := &model.DiscoveryResult{
					TaskID:       task.ID,
					TenantID:     tenantID,
					CIName:       it.Name,
					CIType:       it.CIType,
					IPAddress:    it.IPAddress,
					Attributes:   it.Attributes,
					Source:       task.Name,
					DiscoveredAt: now,
				}
				s.pg.CreateDiscoveryResult(ctx, res)
			}
			found = len(got)
		}

		task.Status = "success"
		task.FoundCount = found
		s.pg.UpdateDiscoveryTask(ctx, task)
	}()

	writeJSON(w, 200, map[string]string{"status": "started"})
}

// runDiscoveryPlugin 按任务类型从注册表取出对应插件，在探针 Agent 上执行真实发现。
func (s *Service) runDiscoveryPlugin(ctx context.Context, task *model.DiscoveryTask) ([]*discovery.Item, error) {
	d, ok := s.discoveryReg.Lookup(task.Type)
	if !ok {
		return nil, fmt.Errorf("未注册的发现类型 %q", task.Type)
	}
	return d.Discover(ctx, task, s.discoveryRunner)
}

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
	writeJSON(w, 200, map[string]any{"types": out})
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

// EnsureDiscoveryModelTypes 在启动时确保内置模型类型存在（幂等：已存在则跳过）。
func (s *Service) EnsureDiscoveryModelTypes(ctx context.Context, tenantID string) error {
	for _, mt := range buildBuiltinModelTypes() {
		mt.TenantID = tenantID
		// 注意: GetCIModelType 在记录不存在时返回 (nil, nil),
		// 必须按返回值非 nil 判断"已存在", 而非 err==nil。
		existing, err := s.pg.GetCIModelType(ctx, mt.ID, tenantID)
		if err != nil {
			return fmt.Errorf("check model type %q: %w", mt.ID, err)
		}
		if existing != nil {
			continue // 已存在，跳过
		}
		if err := s.pg.CreateCIModelType(ctx, &mt); err != nil {
			return fmt.Errorf("ensure model type %q: %w", mt.ID, err)
		}
		slog.Info("builtin model type ensured", "id", mt.ID, "tenant", tenantID)
	}
	return nil
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
	tenantID := tenantFromCtx(ctx)
	var req struct {
		Dimension  string `json:"dimension"`
		RelationID string `json:"relation_id"`
		CIID       string `json:"ci_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	switch req.Dimension {
	case string(quality.DimConsistency):
		if req.RelationID == "" {
			writeError(w, http.StatusBadRequest, "relation_id is required for consistency fix")
			return
		}
		if s.pg == nil {
			writeError(w, http.StatusBadRequest, "requires pg backend")
			return
		}
		if err := s.pg.DeleteRelation(ctx, req.RelationID, tenantID, "quality-fix"); err != nil {
			if err.Error() == "relation not found" {
				writeError(w, http.StatusNotFound, "relation not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"fixed": true, "dimension": req.Dimension, "relation_id": req.RelationID})
	case string(quality.DimUniqueness):
		writeJSON(w, http.StatusOK, map[string]any{"fixed": false, "hint": "重复 IP/名称请使用「实体调解合并」按渠道置信度合并"})
	default:
		writeError(w, http.StatusBadRequest, "unsupported dimension: "+req.Dimension)
	}
}

// handleQualityScan 按需执行一次数据质量扫描（完整性/唯一性/一致性），返回质量报告。
func (s *Service) handleQualityScan(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	eng := quality.NewEngine(s.pg)
	report, err := eng.Scan(r.Context(), tenantID)
	if err != nil {
		slog.Error("quality scan failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 200, report)
}

func (s *Service) handleListDiscoveryResults(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	results, err := s.pg.ListDiscoveryResults(r.Context(), taskID, tenantID)
	if err != nil {
		slog.Error("list discovery results failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 200, results)
}

func (s *Service) handleSyncDiscoveryResult(w http.ResponseWriter, r *http.Request) {
	resultID := r.PathValue("resultId")
	tenantID := tenantFromCtx(r.Context())

	// 获取发现结果
	results, err := s.pg.ListDiscoveryResults(r.Context(), "", tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
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
		writeError(w, 404, "result not found")
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
		writeJSON(w, 200, map[string]any{"status": "drift_detected", "ci_id": existing.ID, "drifts": len(drifts)})
		return
	}

	if err := s.pg.CreateCI(r.Context(), ci, "discovery"); err != nil {
		slog.Error("sync discovery result failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create CI")
		return
	}

	// 标记已同步
	s.pg.MarkDiscoveryResultSynced(r.Context(), resultID, ci.ID)

	writeJSON(w, 200, map[string]any{"status": "synced", "ci_id": ci.ID})
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
	tenantID := tenantFromCtx(ctx)
	if s.pg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"racks": []*model.CIModel{}, "devices": []*model.CIModel{}})
		return
	}
	racks, err := s.pg.ListCI(ctx, tenantID, "rack")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, map[string]any{"racks": racks, "devices": devices})
}

// handleListDrifts 漂移清单(可按 CI 过滤, 默认未解决)。
func (s *Service) handleListDrifts(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	ciID := r.URL.Query().Get("ci_id")
	includeResolved := r.URL.Query().Get("resolved") == "true"
	if s.pg == nil {
		writeJSON(w, http.StatusOK, []*model.CMDBDrift{})
		return
	}
	drifts, err := s.pg.ListDrifts(r.Context(), tenantID, ciID, includeResolved, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if drifts == nil {
		drifts = []*model.CMDBDrift{}
	}
	writeJSON(w, http.StatusOK, drifts)
}

// handleResolveDrift 标记漂移已解决。
func (s *Service) handleResolveDrift(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.pg.ResolveDrift(r.Context(), id, tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resolved": true, "id": id})
}

func (s *Service) handleSyncAllDiscoveryResults(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskId")
	tenantID := tenantFromCtx(r.Context())

	affected, err := s.pg.SyncAllDiscoveryResults(r.Context(), taskID, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"synced_count": affected})
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
