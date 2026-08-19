package cmdb

import (
	"context"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
	"strings"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// handleGetPanorama 处理 GET /api/v1/panorama?scope=<rootCI>&layers=cmdb,callchain。
func (s *Service) handleGetPanorama(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		httputil.WriteError(w, http.StatusBadRequest, "scope (root CI id) is required")
		return
	}
	var layers []string
	if l := strings.TrimSpace(r.URL.Query().Get("layers")); l != "" {
		for _, p := range strings.Split(l, ",") {
			if p = strings.TrimSpace(p); p != "" {
				layers = append(layers, p)
			}
		}
	}
	resp, err := s.GetPanorama(r.Context(), tenantID, scope, layers)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// GetPanorama 组合 CMDB 拓扑 + 监控快照 + 告警 rollup，产出融合全景图（设计 §7）。
// scope 为拓扑根 CI ID（业务系统/应用）；layers 过滤边类型（cmdb/callchain）。
func (s *Service) GetPanorama(ctx context.Context, tenantID, scope string, layers []string) (*model.PanoramaResponse, error) {
	// 1. CMDB 拓扑（节点 + 边）。
	var topoNodes []model.TopologyNode
	var topoEdges []model.TopologyEdge
	if s.dg != nil {
		nn, ee, err := s.dg.QueryTopology(ctx, tenantID, scope, 4)
		if err != nil {
			return nil, err
		}
		for _, n := range nn {
			topoNodes = append(topoNodes, *n)
		}
		for _, e := range ee {
			topoEdges = append(topoEdges, *e)
		}
	} else {
		topoNodes, topoEdges = s.mem.queryTopology(tenantID, scope, 4)
	}

	// 2. 类型 → 分类映射（节点 category 推导）。
	catByType := s.categoryMap(ctx, tenantID)

	// 3. 监控快照（nil 安全）。
	var snap *model.MonitorSnapshot
	if s.monitor != nil {
		if s2, err := s.monitor.Snapshot(ctx, tenantID); err == nil {
			snap = s2
		}
	}
	if snap == nil {
		snap = &model.MonitorSnapshot{StatusByCI: map[string]string{}}
	}

	// 4. 按 CI 聚合告警。
	alertsByCI := map[string][]model.PanoramaAlert{}
	for _, a := range snap.Alerts {
		alertsByCI[a.CIID] = append(alertsByCI[a.CIID], a)
	}

	// 5. 构造节点 + 告警汇总。
	nodes := make([]model.PanoramaNode, 0, len(topoNodes))
	for _, n := range topoNodes {
		pn := model.PanoramaNode{
			ID:       n.ID,
			Type:     n.Type,
			Category: catByType[n.Type],
			Name:     n.Name,
			Status:   snap.StatusByCI[n.ID],
		}
		if pn.Status == "" {
			pn.Status = "unknown"
		}
		for _, a := range alertsByCI[n.ID] {
			switch strings.ToLower(a.Severity) {
			case "critical":
				pn.AlertSummary.Critical++
			case "warning":
				pn.AlertSummary.Warning++
			default:
				pn.AlertSummary.Info++
			}
			if a.RootCause {
				pn.AlertSummary.RootCause = true
			}
		}
		nodes = append(nodes, pn)
	}

	// 6. 构造边（layer 维度）。未指定 layers 时默认含 cmdb 层。
	wantCallchain := containsStr(layers, "callchain")
	wantCMDB := containsStr(layers, "cmdb") || len(layers) == 0
	edges := make([]model.PanoramaEdge, 0, len(topoEdges)*2)
	for _, e := range topoEdges {
		layer := model.LayerOfRelation(e.RelationType)
		if (layer == "cmdb-structural" || layer == "cmdb-logical") && !wantCMDB {
			continue
		}
		edges = append(edges, model.PanoramaEdge{
			Source: e.SourceID, Target: e.TargetID,
			Layer: layer, RelationType: e.RelationType,
		})
		// MVP：横向调用链先复用 connects_to / depends_on 生成 calls 边（设计 §4 路线 1）。
		if wantCallchain && (e.RelationType == "connects_to" || e.RelationType == "depends_on") {
			edges = append(edges, model.PanoramaEdge{
				Source: e.SourceID, Target: e.TargetID,
				Layer: "callchain", RelationType: "calls",
			})
		}
	}

	// 7. 健康度 rollup。监控未注入时整体为 unknown（nil 安全）。
	if s.monitor == nil {
		for i := range nodes {
			nodes[i].Health = "unknown"
		}
	} else {
		criticalSet := map[string]bool{}
		for _, n := range nodes {
			if n.AlertSummary.Critical > 0 || n.AlertSummary.RootCause {
				criticalSet[n.ID] = true
			}
		}
		for i := range nodes {
			nodes[i].Health = ComputePanoramaHealth(nodes[i], criticalSet, topoEdges)
		}
	}

	// 8. 汇总统计。
	resp := &model.PanoramaResponse{Nodes: nodes, Edges: edges, Alerts: snap.Alerts}
	for _, n := range nodes {
		resp.Summary.Total++
		switch n.Health {
		case "critical":
			resp.Summary.Critical++
		case "warning":
			resp.Summary.Warning++
		case "healthy":
			resp.Summary.Healthy++
		default:
			resp.Summary.Unknown++
		}
	}
	return resp, nil
}

// ComputePanoramaHealth 按设计 §5.2 的优先级计算单节点健康度（纯函数，便于单测）：
//  1. critical 告警 或 自身为根因 → critical
//  2. warning 告警 或 监控状态 down → warning
//  3. 依赖链上游有 critical 节点（受影响）→ warning
//  4. 否则 healthy
func ComputePanoramaHealth(node model.PanoramaNode, criticalSet map[string]bool, edges []model.TopologyEdge) string {
	if node.AlertSummary.Critical > 0 || node.AlertSummary.RootCause {
		return "critical"
	}
	if node.AlertSummary.Warning > 0 || node.Status == "down" {
		return "warning"
	}
	if hasCriticalDependency(node.ID, criticalSet, edges) {
		return "warning"
	}
	return "healthy"
}

// hasCriticalDependency 沿 depends_on/connects_to 方向（source→target）BFS，
// 判断 start 节点的依赖上游中是否存在 critical 节点（影响传播）。
func hasCriticalDependency(start string, criticalSet map[string]bool, edges []model.TopologyEdge) bool {
	adj := map[string][]string{}
	for _, e := range edges {
		if e.RelationType == "depends_on" || e.RelationType == "connects_to" {
			adj[e.SourceID] = append(adj[e.SourceID], e.TargetID)
		}
	}
	visited := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nxt := range adj[cur] {
			if criticalSet[nxt] {
				return true
			}
			if !visited[nxt] {
				visited[nxt] = true
				queue = append(queue, nxt)
			}
		}
	}
	return false
}

// categoryMap 构建 typeID → Category 映射，供全景节点推导分类。
func (s *Service) categoryMap(ctx context.Context, tenantID string) map[string]string {
	m := map[string]string{}
	if s.pg == nil {
		return m
	}
	types, err := s.pg.ListCIModelTypes(ctx, tenantID, 1000, 0)
	if err != nil {
		return m
	}
	for _, t := range types {
		m[t.ID] = t.Category
	}
	return m
}
