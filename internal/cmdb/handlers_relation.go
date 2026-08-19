package cmdb

import (
	"log/slog"
	"net/http"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/cmdb/model"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleGetTopology(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	// 优先 dgraph(图库原生能力)
	if s.dg != nil {
		nodes, edges, err := s.dg.QueryTopology(r.Context(), tenantID, id, 3)
		if err != nil {
			slog.Error("query topology failed", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
		return
	}

	// 轻量部署(dg 不在): 优先用 pg 关系表做 BFS 主源, 富化节点名称/类型;
	// 仅当 pg 也不在时才退回 memStore(纯内存模式)。
	if s.pg != nil {
		rels, err := s.pg.ListRelations(r.Context(), tenantID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		nodes, edges := buildTopologyFromRelations(tenantID, id, rels, 3)
		if cis, cerr := s.pg.ListCI(r.Context(), tenantID, ""); cerr == nil {
			ciMap := make(map[string]*model.CIModel, len(cis))
			for _, ci := range cis {
				ciMap[ci.ID] = ci
			}
			for i := range nodes {
				if ci, ok := ciMap[nodes[i].ID]; ok {
					nodes[i].Name = ci.Name
					nodes[i].Type = ci.TypeID
				}
			}
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
		return
	}

	nodes, edges := s.mem.queryTopology(tenantID, id, 3)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
}

func (s *Service) handleImpactAnalysis(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var result *model.ImpactResult
	if s.dg == nil {
		result, _ = s.mem.impactAnalysis(tenantID, id, 3)
	} else {
		var err error
		result, err = s.dg.ImpactAnalysis(r.Context(), tenantID, id, 3)
		if err != nil {
			slog.Error("impact analysis failed", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// 业务视角富化：受影响 CI 明细 + 业务服务 + 综合影响等级。
	enriched, err := s.enrichImpactWithBusiness(r.Context(), tenantID, result)
	if err != nil {
		slog.Warn("impact enrich failed, fallback to raw result", "error", err)
	}
	httputil.WriteJSON(w, http.StatusOK, enriched)
}

func (s *Service) handleListRelations(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	ciID := r.URL.Query().Get("ci_id")

	if s.pg == nil {
		rels := s.mem.listRelations(tenantID)
		if ciID != "" {
			filtered := make([]*model.CIRelation, 0, len(rels))
			for _, rel := range rels {
				if rel.SourceID == ciID || rel.TargetID == ciID {
					filtered = append(filtered, rel)
				}
			}
			rels = filtered
		}
		if rels == nil {
			rels = []*model.CIRelation{}
		}
		httputil.WriteJSON(w, http.StatusOK, rels)
		return
	}

	var (
		rels []*model.CIRelation
		err  error
	)
	if ciID != "" {
		rels, err = s.pg.ListRelationsByCI(r.Context(), tenantID, ciID)
	} else {
		rels, err = s.pg.ListRelations(r.Context(), tenantID)
	}
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rels == nil {
		rels = []*model.CIRelation{}
	}
	httputil.WriteJSON(w, http.StatusOK, rels)
}

func (s *Service) handleCreateRelation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		SourceID     string         `json:"source_id"`
		TargetID     string         `json:"target_id"`
		RelationType string         `json:"relation_type"`
		Properties   map[string]any `json:"properties,omitempty"`
	}

	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
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
		httputil.WriteError(w, http.StatusConflict, "adding this relation would create a topological cycle [ASSERT-CMDB-02]")
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
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		s.mem.createRelation(rel)
	}

	httputil.WriteJSON(w, http.StatusCreated, rel)
}

// handleBatchCreateRelation 批量创建关系(逐条环路检测, ASSERT-CMDB-02)。
func (s *Service) handleBatchCreateRelation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)

	var req struct {
		Relations []struct {
			SourceID     string         `json:"source_id"`
			TargetID     string         `json:"target_id"`
			RelationType string         `json:"relation_type"`
			Properties   map[string]any `json:"properties,omitempty"`
		} `json:"relations"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Relations) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "relations are required")
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

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"created": created, "failed": failed})
}

func (s *Service) handleDeleteRelation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	traceID := traceIDFromCtx(r.Context())

	if s.pg == nil {
		if !s.mem.deleteRelation(id, tenantID) {
			httputil.WriteError(w, http.StatusNotFound, "relation not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.pg.DeleteRelation(r.Context(), id, tenantID, traceID); err != nil {
		if err.Error() == "relation not found" {
			httputil.WriteError(w, http.StatusNotFound, "relation not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
