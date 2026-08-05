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

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/cmdb/ci", s.handleListCI)
	mux.HandleFunc("GET /api/v1/cmdb/ci/export", s.handleExportCI)  // P1-1 CSV 导出
	mux.HandleFunc("POST /api/v1/cmdb/ci/import", s.handleImportCI) // P1-1 CSV 导入
	mux.HandleFunc("POST /api/v1/cmdb/ci", s.handleCreateCI)
	mux.HandleFunc("POST /api/v1/cmdb/ci/batch", s.handleBatchCreateCI) // F4-B 批量 + D2 修复
	mux.HandleFunc("GET /api/v1/cmdb/ci/{id}", s.handleGetCI)
	mux.HandleFunc("PUT /api/v1/cmdb/ci/{id}", s.handleUpdateCI)
	mux.HandleFunc("DELETE /api/v1/cmdb/ci/{id}", s.handleDeleteCI)
	mux.HandleFunc("POST /api/v1/cmdb/ci/{id}/archive", s.handleArchiveCI) // P1-2 归档
	mux.HandleFunc("POST /api/v1/cmdb/ci/{id}/restore", s.handleRestoreCI) // P1-2 恢复
	mux.HandleFunc("GET /api/v1/cmdb/ci/{id}/audit", s.handleCIAudit)
	mux.HandleFunc("GET /api/v1/cmdb/search", s.handleSearchCI)

	// 保存的查询策略（"我想查"，P1-3）
	mux.HandleFunc("GET /api/v1/cmdb/saved-queries", s.handleListSavedQueries)
	mux.HandleFunc("POST /api/v1/cmdb/saved-queries", s.handleCreateSavedQuery)
	mux.HandleFunc("GET /api/v1/cmdb/saved-queries/{id}/resolve", s.handleResolveSavedQuery)
	mux.HandleFunc("DELETE /api/v1/cmdb/saved-queries/{id}", s.handleDeleteSavedQuery)

	mux.HandleFunc("GET /api/v1/cmdb/topology/{id}", s.handleGetTopology)
	mux.HandleFunc("GET /api/v1/cmdb/impact/{id}", s.handleImpactAnalysis)

	mux.HandleFunc("GET /api/v1/cmdb/relation", s.handleListRelations)
	mux.HandleFunc("POST /api/v1/cmdb/relation", s.handleCreateRelation)
	mux.HandleFunc("DELETE /api/v1/cmdb/relation/{id}", s.handleDeleteRelation)

	mux.HandleFunc("POST /api/v1/cmdb/model-type", s.handleCreateModelType)
	mux.HandleFunc("GET /api/v1/cmdb/model-type", s.handleListModelTypes)
	mux.HandleFunc("GET /api/v1/cmdb/model-type/{id}", s.handleGetModelType)
	mux.HandleFunc("PUT /api/v1/cmdb/model-type/{id}", s.handleUpdateModelType)
	mux.HandleFunc("DELETE /api/v1/cmdb/model-type/{id}", s.handleDeleteModelType)
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
}

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
		if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes); err != nil {
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

	if s.dg == nil {
		result, _ := s.mem.impactAnalysis(tenantID, id, 3)
		writeJSON(w, http.StatusOK, result)
		return
	}
	result, err := s.dg.ImpactAnalysis(r.Context(), tenantID, id, 3)
	if err != nil {
		slog.Error("impact analysis failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
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

func (s *Service) validateAttributes(ctx context.Context, typeID string, attrs map[string]any) error {
	mt, err := s.ResolveModelType(ctx, typeID, "default")
	if err != nil {
		return nil
	}
	if mt == nil {
		return nil
	}

	for name, def := range mt.Schema {
		if def.Required {
			if _, ok := attrs[name]; !ok {
				return fmt.Errorf("required attribute '%s' is missing for CI type '%s'", name, typeID)
			}
		}
		if val, ok := attrs[name]; ok && def.Type != "" {
			if !validateAttrType(def.Type, val) {
				return fmt.Errorf("attribute '%s' expected type '%s', got %T", name, def.Type, val)
			}
		}
	}
	return nil
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

	if err := s.pg.UpdateCIModelType(r.Context(), &mt); err != nil {
		slog.Error("update model type failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, mt)
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

// EnsureDiscoveryModelTypes 在启动时确保自动发现插件产出的 CI 模型类型存在
// （middleware 等目前未内置种子；server/switch/database/app 已由内置种子覆盖）。
// 缺失则创建为内置模型类型，供发现结果落库与前端展示使用。
func (s *Service) EnsureDiscoveryModelTypes(ctx context.Context, tenantID string) error {
	want := []model.CIModelType{
		{
			ID:          "middleware",
			TenantID:    tenantID,
			Name:        "中间件",
			Description: "经自动发现探测的中间件实例（Redis/Nginx/ES/Kafka/ZK/RabbitMQ 等）",
			Category:    model.ModelCategoryMiddleware,
			Icon:        "layers",
			ShowInMenu:  true,
			Builtin:     true,
			Author:      model.BuiltinAuthor,
			Schema: map[string]model.AttrDef{
				"middleware_type": {Type: "string", Description: "中间件类型（redis/nginx/elasticsearch/kafka/zookeeper/rabbitmq）"},
				"version":         {Type: "string", Description: "版本"},
				"port":            {Type: "int", Description: "监听端口"},
				"role":            {Type: "string", Description: "角色（如 redis master/slave）"},
			},
		},
	}
	for _, mt := range want {
		if _, err := s.pg.GetCIModelType(ctx, mt.ID, tenantID); err == nil {
			continue // 已存在，跳过
		}
		if err := s.pg.CreateCIModelType(ctx, &mt); err != nil {
			return fmt.Errorf("ensure model type %q: %w", mt.ID, err)
		}
		slog.Info("discovery model type ensured", "id", mt.ID, "tenant", tenantID)
	}
	return nil
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

	if err := s.pg.CreateCI(r.Context(), ci, "discovery"); err != nil {
		slog.Error("sync discovery result failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create CI")
		return
	}

	// 标记已同步
	s.pg.MarkDiscoveryResultSynced(r.Context(), resultID, ci.ID)

	writeJSON(w, 200, map[string]any{"status": "synced", "ci_id": ci.ID})
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
		if err := s.validateAttributes(ctx, ci.TypeID, ci.Attributes); err != nil {
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
