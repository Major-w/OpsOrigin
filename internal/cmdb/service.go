package cmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/ingest"
	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/validate"
)

type Service struct {
	cfg         *config.Config
	pg          *pg.Repository
	dg          *dgraph.Client
	mem            *memStore // PG/Dgraph 不可用时的内存回退
	ing            *ingest.Ingestor // 异步批量落库器(F4-B)
	onCICreated    func(ctx context.Context, ciID, tenantID string)

	registry *collectorRegistry      // 采集器注册表（kind × resourceType → Collector）
	onMetric func(point MetricPoint) // 监控指标落库回调（由 cmd/core 注入，路由到监控模块）
	sched    *Scheduler              // 采集调度器（StartScheduler 后非 nil）
	agentChan AgentChannel           // 经 Agent 通道上报的任务下发接口（方案 X：投影到 agentmgr）
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
	mux.HandleFunc("POST /api/v1/cmdb/ci", s.handleCreateCI)
	mux.HandleFunc("POST /api/v1/cmdb/ci/batch", s.handleBatchCreateCI) // F4-B 批量 + D2 修复
	mux.HandleFunc("GET /api/v1/cmdb/ci/{id}", s.handleGetCI)
	mux.HandleFunc("PUT /api/v1/cmdb/ci/{id}", s.handleUpdateCI)
	mux.HandleFunc("DELETE /api/v1/cmdb/ci/{id}", s.handleDeleteCI)
	mux.HandleFunc("GET /api/v1/cmdb/ci/{id}/audit", s.handleCIAudit)
	mux.HandleFunc("GET /api/v1/cmdb/search", s.handleSearchCI)

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

	// Version History
	mux.HandleFunc("GET /api/v1/cmdb/history", s.handleListHistory)
	mux.HandleFunc("GET /api/v1/cmdb/history/{ciId}", s.handleListCIVersions)
	mux.HandleFunc("GET /api/v1/cmdb/history/detail/{id}", s.handleGetVersionDetail)

	// Discovery
	mux.HandleFunc("GET /api/v1/cmdb/discovery", s.handleListDiscoveryTasks)
	mux.HandleFunc("POST /api/v1/cmdb/discovery", s.handleCreateDiscoveryTask)
	mux.HandleFunc("PUT /api/v1/cmdb/discovery/{id}", s.handleUpdateDiscoveryTask)
	mux.HandleFunc("DELETE /api/v1/cmdb/discovery/{id}", s.handleDeleteDiscoveryTask)
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
	if err := s.pg.DeleteCI(r.Context(), id, tenantID, traceID); err != nil {
		if err.Error() == "ci not found" {
			writeError(w, http.StatusNotFound, "ci not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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

	if s.pg == nil {
		cis := s.mem.listCIs(tenantID, typeID)
		if cis == nil {
			cis = []*model.CIModel{}
		}
		writeJSON(w, http.StatusOK, cis)
		return
	}
	cis, err := s.pg.ListCIPaginated(r.Context(), tenantID, typeID, sortBy, sortOrder, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cis == nil {
		cis = []*model.CIModel{}
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
		if cis == nil {
			cis = []*model.CIModel{}
		}
		writeJSON(w, http.StatusOK, cis)
		return
	}
	cis, err := s.pg.ListCI(r.Context(), tenantID, typeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}
	writeJSON(w, http.StatusOK, cis)
}

func (s *Service) validateAttributes(ctx context.Context, typeID string, attrs map[string]any) error {
	mt, err := s.pg.GetCIModelType(ctx, typeID, "default")
	if err != nil || mt == nil {
		tenantID := "default"
		mt, err = s.pg.GetCIModelType(ctx, typeID, tenantID)
	}
	if mt == nil || err != nil {
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
	mt, err := s.pg.GetCIModelType(r.Context(), id, tenantID)
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

	// 获取总数
	total, err := s.pg.CountCIModelTypes(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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
	// 关系/拓扑推断(relation)及 agent/snmp 深度扫描，不得重复拉取资源。
	switch task.Type {
	case "cloud", "host", "container":
		task.Status = "failed"
		_ = s.pg.UpdateDiscoveryTask(r.Context(), task)
		writeError(w, http.StatusBadRequest, "类型 "+task.Type+" 属于资源纳管层职责，请使用资源纳管（云/主机/容器）同步，而非 CMDB 自动发现")
		return
	case "relation", "agent", "snmp":
		// 合法发现类型
	default:
		task.Status = "failed"
		_ = s.pg.UpdateDiscoveryTask(r.Context(), task)
		writeError(w, http.StatusBadRequest, "不支持的发现类型: "+task.Type)
		return
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
			// agent / snmp 仍为模拟实现（已知 P0 缺口），产出预览结果供确认
			time.Sleep(2 * time.Second)
			discoveredCIs := simulateDiscovery(task)
			for _, ci := range discoveredCIs {
				res := &model.DiscoveryResult{
					TaskID:       task.ID,
					TenantID:     tenantID,
					CIName:       ci["name"].(string),
					CIType:       ci["type"].(string),
					IPAddress:    ci["ip"].(string),
					Attributes:   ci["attributes"].(map[string]any),
					Source:       task.Name,
					DiscoveredAt: now,
				}
				s.pg.CreateDiscoveryResult(ctx, res)
			}
			found = len(discoveredCIs)
		}

		task.Status = "success"
		task.FoundCount = found
		s.pg.UpdateDiscoveryTask(ctx, task)
	}()

	writeJSON(w, 200, map[string]string{"status": "started"})
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

// simulateDiscovery 模拟发现过程
func simulateDiscovery(task *model.DiscoveryTask) []map[string]any {
	var results []map[string]any

	switch task.Type {
	case "agent":
		results = []map[string]any{
			{"name": "agent-host-01", "type": "server", "ip": "192.168.1.10", "attributes": map[string]any{"os": "linux", "cpu": 4, "memory": "8GB"}},
			{"name": "agent-host-02", "type": "server", "ip": "192.168.1.11", "attributes": map[string]any{"os": "linux", "cpu": 8, "memory": "16GB"}},
		}
	case "snmp":
		results = []map[string]any{
			{"name": "switch-core-01", "type": "switch", "ip": "10.0.0.1", "attributes": map[string]any{"vendor": "cisco", "ports": 48, "firmware": "v4.2"}},
			{"name": "router-01", "type": "switch", "ip": "10.0.0.254", "attributes": map[string]any{"vendor": "huawei", "ports": 24}},
		}
	case "cloud":
		results = []map[string]any{
			{"name": "cloud-ecs-01", "type": "server", "ip": "47.96.1.100", "attributes": map[string]any{"provider": "aliyun", "region": "cn-hangzhou", "instance_type": "ecs.g6.large"}},
			{"name": "cloud-rds-01", "type": "database", "ip": "rm-xxx.mysql.rds.aliyuncs.com", "attributes": map[string]any{"provider": "aliyun", "engine": "mysql", "version": "8.0"}},
		}
	default:
		results = []map[string]any{
			{"name": "discovered-device-01", "type": "server", "ip": "192.168.0.10", "attributes": map[string]any{"source": "api"}},
		}
	}
	return results
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
