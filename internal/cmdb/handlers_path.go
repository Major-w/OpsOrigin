package cmdb

import (
	"net/http"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// handlePathQuery 求两个 CI 之间的最短依赖路径（无向 BFS，记录实际关系方向与类型）。
// GET /api/v1/cmdb/path?from=X&to=Y
func (s *Service) handlePathQuery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantFromContext(ctx)
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		httputil.WriteError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "path query requires postgres backend")
		return
	}
	rels, err := s.pg.ListRelations(ctx, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type edge struct {
		to      string
		relType string
		dir     int // 1: 正向(source->target), -1: 反向
	}
	adj := map[string][]edge{}
	for _, rel := range rels {
		adj[rel.SourceID] = append(adj[rel.SourceID], edge{rel.TargetID, rel.RelationType, 1})
		adj[rel.TargetID] = append(adj[rel.TargetID], edge{rel.SourceID, rel.RelationType, -1})
	}

	type step struct {
		from    string
		relType string
		dir     int
	}
	prev := map[string]step{}
	visited := map[string]bool{from: true}
	queue := []string{from}
	found := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			found = true
			break
		}
		for _, e := range adj[cur] {
			if !visited[e.to] {
				visited[e.to] = true
				prev[e.to] = step{from: cur, relType: e.relType, dir: e.dir}
				queue = append(queue, e.to)
			}
		}
	}

	if !found {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"found": false, "path": []any{}, "edges": []any{}})
		return
	}

	// 回溯路径
	ids := []string{}
	for cur := to; ; cur = prev[cur].from {
		ids = append([]string{cur}, ids...)
		if cur == from {
			break
		}
	}

	// 边（沿路径）
	pathEdges := make([]map[string]any, 0, len(ids)-1)
	for i := 0; i < len(ids)-1; i++ {
		st := prev[ids[i+1]]
		dirLabel := "forward"
		if st.dir == -1 {
			dirLabel = "reverse"
		}
		pathEdges = append(pathEdges, map[string]any{
			"source":        st.from,
			"target":        ids[i+1],
			"relation_type": st.relType,
			"direction":     dirLabel,
		})
	}

	// 富化 CI 名称/类型
	ciMap := map[string]*model.CIModel{}
	if cis, e := s.pg.ListCI(ctx, tenantID, ""); e == nil {
		for _, ci := range cis {
			ciMap[ci.ID] = ci
		}
	}
	pathNodes := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		node := map[string]any{"id": id}
		if ci, ok := ciMap[id]; ok {
			node["name"] = ci.Name
			node["type_id"] = ci.TypeID
		}
		pathNodes = append(pathNodes, node)
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"found": true,
		"path":  pathNodes,
		"edges": pathEdges,
	})
}
