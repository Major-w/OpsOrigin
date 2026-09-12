package cmdb

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/cmdb/model"

	"github.com/opsorigin/core/pkg/httputil"
)

// maxTopologyNodes 是单根拓扑返回的节点上限（大节点量保护）。
// 当某 CI 为"超级节点"（直连上千个邻居）时，自动收缩到前 N 个节点，
// 避免一次性拉取/渲染全部相邻节点导致前端卡死；前端据 truncated 标记提示用户。
const maxTopologyNodes = 600

// handleGetTopology godoc
// @Summary 获取拓扑图
// @Description 获取 CI 的拓扑关系图（3 层深度）
// @Tags CMDB
// @Produce json
// @Param id path string true "CI ID"
// @Success 200 {object} map[string]any "nodes: 节点列表, edges: 边列表"
// @Router /api/v1/cmdb/ci/{id}/topology [get]
// @Security BearerAuth
// topoDepthOf 解析拓扑展开深度: 默认 3, 允许 1..5, 越界回落到默认。
func topoDepthOf(r *http.Request) int {
	if v := r.URL.Query().Get("depth"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 5 {
			return n
		}
	}
	return 3
}

func (s *Service) handleGetTopology(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	depth := topoDepthOf(r)

	// 优先 dgraph(图库原生能力)
	if s.dg != nil {
		nodes, edges, err := s.dg.QueryTopology(r.Context(), tenantID, id, depth)
		if err != nil {
			slog.Error("query topology failed", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// nil 切片会被序列化成 JSON null，前端 topology.nodes.length 直接崩；
		// 三个分支统一兜底为空数组，保证"无关系"是一个正常状态而不是异常。
		// dgraph 返回的是指针切片，同样兜底空数组
		if nodes == nil {
			nodes = []*model.TopologyNode{}
		}
		if edges == nil {
			edges = []*model.TopologyEdge{}
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
		nodes, edges := buildTopologyFromRelations(tenantID, []string{id}, rels, depth)
		// nil 切片会被序列化成 JSON null，前端 topology.nodes.length 直接崩；
		// 统一兜底为空数组，保证"无关系"是一个正常状态而不是异常。
		if nodes == nil {
			nodes = []model.TopologyNode{}
		}
		if edges == nil {
			edges = []model.TopologyEdge{}
		}
		s.enrichTopologyNodes(r.Context(), tenantID, nodes)
		// 大节点量保护：超规模连接时自动收缩到前 maxTopologyNodes 个节点。
		// 节点已是 BFS 顺序(离根最近优先)，所以截断保留的是最有价值的那批，且结果稳定可复现。
		resp := map[string]any{"nodes": nodes, "edges": edges}
		if len(nodes) > maxTopologyNodes {
			kept := make(map[string]bool, maxTopologyNodes)
			truncatedNodes := nodes[:maxTopologyNodes]
			for _, n := range truncatedNodes {
				kept[n.ID] = true
			}
			keptEdges := make([]model.TopologyEdge, 0, len(edges))
			for _, e := range edges {
				if kept[e.SourceID] && kept[e.TargetID] {
					keptEdges = append(keptEdges, e)
				}
			}
			resp = map[string]any{"nodes": truncatedNodes, "edges": keptEdges, "truncated": true}
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
		return
	}

	nodes, edges := s.mem.queryTopology(tenantID, id, depth)
	if nodes == nil {
		nodes = []model.TopologyNode{}
	}
	if edges == nil {
		edges = []model.TopologyEdge{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
}

// maxSubgraphNodes 条件子图返回的节点上限。
// 依赖矩阵是 O(n²) DOM、图谱布局也是 O(n log n) 起步，这里比单根拓扑(600)更保守。
const maxSubgraphNodes = 300

// handleGetSubgraph godoc
// @Summary 条件子图
// @Description 按模型/环境/业务/关键字筛选 CI 组成子图，回答"某业务/某环境里谁依赖谁"，
// 而不是单根拓扑那种"以某个 CI 为中心的三层邻域"。
// expand=1 时改为以筛选结果为多根向外 BFS(depth)。
// @Tags CMDB
// @Produce json
// @Param model_id query string false "模型类型 ID"
// @Param env query string false "环境属性值"
// @Param business query string false "业务属性值"
// @Param q query string false "名称/属性关键字"
// @Param depth query int false "扩展深度 1..5(仅 expand=1 生效)"
// @Param limit query int false "节点上限，默认 200，最大 300"
// @Param expand query int false "0=仅筛选集内部关系(默认)；1=以筛选集为多根向外扩展"
// @Success 200 {object} map[string]any
// @Router /api/v1/cmdb/subgraph [get]
// @Security BearerAuth
func (s *Service) handleGetSubgraph(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "subgraph query requires postgres backend")
		return
	}

	q := r.URL.Query()
	modelID := q.Get("model_id")
	env := strings.TrimSpace(q.Get("env"))
	business := strings.TrimSpace(q.Get("business"))
	keyword := strings.TrimSpace(q.Get("q"))
	expand := q.Get("expand") == "1" || q.Get("expand") == "true"
	depth := topoDepthOf(r)
	limit := 200
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		if n > maxSubgraphNodes {
			n = maxSubgraphNodes
		}
		limit = n
	}

	// 1) 候选 CI：type/关键字下推到 SQL，env/business 是 EAV 属性只能内存过滤。
	cis, err := s.listCandidatesForSubgraph(ctx, tenantID, modelID, keyword, limit)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	kept := make([]*model.CIModel, 0, len(cis))
	for _, ci := range cis {
		if matchAttrAny(ci.Attributes, env, "env", "environment") &&
			matchAttrAny(ci.Attributes, business, "business", "business_service", "app") {
			kept = append(kept, ci)
		}
	}
	total := len(kept)
	if total > limit {
		kept = kept[:limit]
	}

	rels, err := s.pg.ListRelations(ctx, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var nodes []model.TopologyNode
	var edges []model.TopologyEdge

	if expand {
		roots := make([]string, 0, len(kept))
		for _, ci := range kept {
			roots = append(roots, ci.ID)
		}
		nodes, edges = buildTopologyFromRelations(tenantID, roots, rels, depth)
		if len(nodes) > limit {
			nodes = nodes[:limit]
			keptID := make(map[string]bool, len(nodes))
			for _, n := range nodes {
				keptID[n.ID] = true
			}
			filtered := make([]model.TopologyEdge, 0, len(edges))
			for _, e := range edges {
				if keptID[e.SourceID] && keptID[e.TargetID] {
					filtered = append(filtered, e)
				}
			}
			edges = filtered
		}
	} else {
		// 真正的"子图"：节点集即筛选结果，只保留两端都在集内的边。
		inSet := make(map[string]bool, len(kept))
		nodes = make([]model.TopologyNode, 0, len(kept))
		for _, ci := range kept {
			inSet[ci.ID] = true
			nodes = append(nodes, model.TopologyNode{ID: ci.ID, Name: ci.Name, Type: ci.TypeID, TenantID: tenantID})
		}
		seen := make(map[string]bool, len(rels))
		edges = make([]model.TopologyEdge, 0, len(rels))
		for _, rel := range rels {
			if rel.TenantID != tenantID || !inSet[rel.SourceID] || !inSet[rel.TargetID] {
				continue
			}
			key := rel.SourceID + "|" + rel.TargetID + "|" + rel.RelationType
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, model.TopologyEdge{
				SourceID: rel.SourceID, TargetID: rel.TargetID, RelationType: rel.RelationType,
			})
		}
	}

	s.enrichTopologyNodes(ctx, tenantID, nodes)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes, "edges": edges, "total": total, "truncated": total > limit,
	})
}

// listCandidatesForSubgraph 取子图候选 CI：带关键字时走 ILIKE 下推(限 100)，
// 否则走分页查询按 limit 取，避免 ListCI 全表反序列化。
func (s *Service) listCandidatesForSubgraph(ctx context.Context, tenantID, modelID, keyword string, limit int) ([]*model.CIModel, error) {
	if keyword != "" {
		return s.pg.ListCIByQuery(ctx, tenantID, modelID, keyword)
	}
	return s.pg.ListCIPaginated(ctx, tenantID, modelID, "name", "asc", limit, 0)
}

// matchAttrAny 在 CI 属性里按候选 key 依次匹配；期望值为空时视为不过滤。
// EAV 属性没统一命名(env/environment 都见过)，所以按候选 key 兜底匹配。
func matchAttrAny(attrs map[string]any, want string, keys ...string) bool {
	if want == "" {
		return true
	}
	for _, k := range keys {
		if v, ok := attrs[k]; ok {
			if s, ok := v.(string); ok && strings.EqualFold(strings.TrimSpace(s), want) {
				return true
			}
		}
	}
	return false
}

// enrichTopologyNodes 就地补全节点的名称与类型。
// 只查当前拓扑实际用到的 ID(而非全租户 ListCI), 避免大租户下的整表反序列化。
func (s *Service) enrichTopologyNodes(ctx context.Context, tenantID string, nodes []model.TopologyNode) {
	if s.pg == nil || len(nodes) == 0 {
		return
	}
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	cis, cerr := s.pg.GetCIsByIDs(ctx, tenantID, ids)
	if cerr != nil {
		slog.Warn("enrich topology nodes failed", "error", cerr)
		return
	}
	ciMap := make(map[string]*model.CIModel, len(cis))
	for _, ci := range cis {
		ciMap[ci.ID] = ci
	}
	for i := range nodes {
		missing := true
		if ci, ok := ciMap[nodes[i].ID]; ok {
			missing = false
			if ci.Name != "" {
				nodes[i].Name = ci.Name
			}
			if ci.TypeID != "" {
				nodes[i].Type = ci.TypeID
			}
		}
		// 名字留空会让前端卡片渲染成空白块，两种情况都要兜底：
		// 一是关系还在但 CI 已被删除（悬空关系），二是 CI 存在但名称字段为空。
		if nodes[i].Name == "" {
			if missing {
				nodes[i].Name = "(已删除) " + shortID(nodes[i].ID)
			} else {
				nodes[i].Name = "(未命名) " + shortID(nodes[i].ID)
			}
		}
		if nodes[i].Type == "" {
			nodes[i].Type = "unknown"
		}
	}
}

// shortID 取 UUID 前 8 位，用于悬空节点的可读标识。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
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
