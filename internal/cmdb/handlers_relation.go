package cmdb

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/cmdb/model"

	"github.com/opsorigin/core/pkg/httputil"
)

// handleGetTopology godoc
// @Summary 获取拓扑图
// @Description 获取 CI 的拓扑关系图（3 层深度）
// @Tags CMDB
// @Produce json
// @Param id path string true "CI ID"
// @Success 200 {object} map[string]any "nodes: 节点列表, edges: 边列表"
// @Router /api/v1/cmdb/ci/{id}/topology [get]
// @Security BearerAuth
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

	// 模型级关系基数校验：仅当源 CI 模型声明了该 relation_type 时强制，
	// 未建模(自由)关系放行(兼容自动发现/历史关系)。
	if verr := s.validateRelationCardinality(ctx, tenantID, req.SourceID, req.TargetID, req.RelationType); verr != nil {
		httputil.WriteError(w, http.StatusBadRequest, verr.Error())
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
		// 模型级关系基数校验(与单条创建一致, 仅建模关系强制)。
		if verr := s.validateRelationCardinality(ctx, tenantID, item.SourceID, item.TargetID, item.RelationType); verr != nil {
			failed = append(failed, failure{item.SourceID, item.TargetID, verr.Error()})
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

// validateRelationCardinality 按源 CI 模型的 RelationDef 校验目标模型匹配 + 基数约束。
// 仅当源模型声明了匹配的 relation_type 时才强制；未建模(自由)关系放行。
// 无 pg(内存模式)或无法解析模型时直接放行, 不阻断。
func (s *Service) validateRelationCardinality(ctx context.Context, tenantID, sourceID, targetID, relationType string) error {
	if s.pg == nil {
		return nil
	}
	src, err := s.pg.GetCI(ctx, sourceID, tenantID)
	if err != nil || src == nil {
		return fmt.Errorf("source CI not found")
	}
	mt, err := s.ResolveModelType(ctx, src.TypeID, tenantID)
	if err != nil || mt == nil {
		return nil // 无法解析模型, 放行
	}
	// 收集源模型声明的所有同名 relation_type 的 RelationDef（一个模型可对同一
	// 关系类型声明多个目标，如 business_service 既 depends_on 业务又 depends_on 应用）。
	var defs []model.RelationDef
	for _, r := range mt.Relations {
		if r.RelationType == relationType {
			defs = append(defs, r)
		}
	}
	if len(defs) == 0 {
		return nil // 未建模关系, 放行
	}
	// 目标模型匹配校验（支持子类）：目标只需匹配任一声明（或其子类）即放行。
	// 例：app 声明 depends_on → database（抽象父类），实际目标为 mysql 实例（子类）应放行。
	tgt, terr := s.pg.GetCI(ctx, targetID, tenantID)
	if terr != nil || tgt == nil {
		return fmt.Errorf("target CI not found")
	}
	matched := false
	for _, d := range defs {
		if s.modelIsOrExtends(ctx, tgt.TypeID, d.TargetModel, tenantID) {
			matched = true
			break
		}
	}
	if !matched {
		allowed := make([]string, 0, len(defs))
		for _, d := range defs {
			allowed = append(allowed, d.TargetModel)
		}
		return fmt.Errorf("关系 %q 要求目标为以下模型之一（或其子类）：%s，实际为 %s", defs[0].Name, strings.Join(allowed, " / "), tgt.TypeID)
	}
	// 基数约束: 扫描同租户该 relation_type 的既有边，对每条命中的声明分别检查。
	rels, rerr := s.pg.ListRelations(ctx, tenantID)
	if rerr != nil {
		return nil
	}
	for _, d := range defs {
		switch d.Cardinality {
		case model.CardinalityOneToOne:
			for _, r := range rels {
				if r.RelationType != relationType {
					continue
				}
				if r.SourceID == sourceID || r.TargetID == targetID {
					return fmt.Errorf("关系 %q 为一对一：源或目标已存在该关系", d.Name)
				}
			}
		case model.CardinalityOneToMany:
			for _, r := range rels {
				if r.RelationType != relationType {
					continue
				}
				if r.TargetID == targetID {
					return fmt.Errorf("关系 %q 为一对多：目标已被其它源占用", d.Name)
				}
			}
		}
	}
	return nil
}

// modelIsOrExtends 判断 typeID 是否等于 base 或沿 ParentID 继承链是 base 的子类。
// 用于关系目标模型校验时允许"目标是抽象父类（如 database）的子类（如 mysql）"。
// 带深度上限防止 ParentID 异常成环。
func (s *Service) modelIsOrExtends(ctx context.Context, typeID, base, tenantID string) bool {
	if typeID == base {
		return true
	}
	cur := typeID
	for depth := 0; depth < 16; depth++ {
		mt, err := s.ResolveModelType(ctx, cur, tenantID)
		if err != nil || mt == nil || mt.ParentID == "" {
			return false
		}
		if mt.ParentID == base {
			return true
		}
		cur = mt.ParentID
	}
	return false
}
