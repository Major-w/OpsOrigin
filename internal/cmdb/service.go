package cmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/pkg/config"
)



type Service struct {
	cfg         *config.Config
	pg          *pg.Repository
	dg          *dgraph.Client
	mem         *memStore // PG/Dgraph 不可用时的内存回退
	onCICreated func(ctx context.Context, ciID, tenantID string)
}

func NewService(cfg *config.Config, pgRepo *pg.Repository, dgClient *dgraph.Client) *Service {
	return &Service{
		cfg: cfg,
		pg:  pgRepo,
		dg:  dgClient,
		mem: newMemStore(),
	}
}

func (s *Service) OnCICreated(fn func(ctx context.Context, ciID, tenantID string)) {
	s.onCICreated = fn
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/cmdb/ci", s.handleListCI)
	mux.HandleFunc("POST /api/v1/cmdb/ci", s.handleCreateCI)
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
		if err := s.validateAttributes(ci.TypeID, ci.Attributes); err != nil {
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

	existing := s.mem.getCI(id, tenantID)
	if s.pg != nil {
		var err error
		existing, err = s.pg.GetCI(ctx, id, tenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
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
				if contains(ci.Name, q) || contains(ci.Description, q) || contains(ci.TypeID, q) {
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
	cis, err := s.pg.ListCI(r.Context(), tenantID, typeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if q != "" {
		filtered := make([]*model.CIModel, 0)
		for _, ci := range cis {
			if contains(ci.Name, q) || contains(ci.Description, q) || contains(ci.TypeID, q) {
				filtered = append(filtered, ci)
			}
		}
		cis = filtered
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}
	writeJSON(w, http.StatusOK, cis)
}

func (s *Service) validateAttributes(typeID string, attrs map[string]any) error {
	mt, err := s.pg.GetCIModelType(context.Background(), typeID, "default")
	if err != nil || mt == nil {
		tenantID := "default"
		mt, err = s.pg.GetCIModelType(context.Background(), typeID, tenantID)
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
		"items": types,
		"total": total,
		"limit": limit,
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

func contains(s, substr string) bool {
	return len(substr) == 0 || len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsAny(s, substr))
}
func containsAny(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
	if limit <= 0 { limit = 50 }

	versions, total, err := s.pg.ListAllCIHistory(r.Context(), tenantID, changeType, limit, offset)
	if err != nil {
		slog.Error("list history failed", "err", err)
		writeError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"items": versions, "total": total, "limit": limit, "offset": offset})
}

func (s *Service) handleListCIVersions(w http.ResponseWriter, r *http.Request) {
	ciID := r.PathValue("ciId")
	tenantID := tenantFromCtx(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 { limit = 20 }

	versions, total, err := s.pg.ListCIVersions(r.Context(), ciID, tenantID, limit, offset)
	if err != nil {
		slog.Error("list ci versions failed", "err", err)
		writeError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"items": versions, "total": total, "limit": limit, "offset": offset})
}

func (s *Service) handleGetVersionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, err := s.pg.GetCIVersion(r.Context(), id)
	if err != nil {
		slog.Error("get version detail failed", "err", err)
		writeError(w, 500, "internal error")
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
		writeError(w, 500, "internal error")
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
		writeError(w, 500, "internal error")
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
		writeError(w, 500, "internal error")
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

	if input.Name != "" { existing.Name = input.Name }
	if input.Type != "" { existing.Type = input.Type }
	if input.Target != "" { existing.Target = input.Target }
	if input.Config != nil { existing.Config = input.Config }

	if err := s.pg.UpdateDiscoveryTask(r.Context(), existing); err != nil {
		slog.Error("update discovery task failed", "err", err)
		writeError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, existing)
}

func (s *Service) handleDeleteDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.pg.DeleteDiscoveryTask(r.Context(), id, tenantID); err != nil {
		slog.Error("delete discovery task failed", "err", err)
		writeError(w, 500, "internal error")
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

	// 更新状态为 running
	task.Status = "running"
	s.pg.UpdateDiscoveryTask(r.Context(), task)

	// 模拟发现过程（实际应根据 task.Type 调用不同发现器）
	go func() {
		time.Sleep(2 * time.Second)
		now := time.Now()
		task.LastRunAt = &now

		// 生成模拟发现结果
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
			s.pg.CreateDiscoveryResult(context.Background(), res)
		}

		task.Status = "success"
		task.FoundCount = len(discoveredCIs)
		s.pg.UpdateDiscoveryTask(context.Background(), task)
	}()

	writeJSON(w, 200, map[string]string{"status": "started"})
}

func (s *Service) handleListDiscoveryResults(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	results, err := s.pg.ListDiscoveryResults(r.Context(), taskID, tenantID)
	if err != nil {
		slog.Error("list discovery results failed", "err", err)
		writeError(w, 500, "internal error")
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
		writeError(w, 500, "internal error")
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
		writeError(w, 500, "failed to create CI")
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
		writeError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"synced_count": affected})
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
