package cmdb

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// memStore 是 CMDB 的内存回退实现,在 PG/Dgraph 不可用时使用。
// 预置示例数据,用于演示关系图谱可视化与影响分析。
type memStore struct {
	mu        sync.RWMutex
	cis       map[string]*model.CIModel
	relations map[string]*model.CIRelation
}

func newMemStore() *memStore {
	m := &memStore{
		cis:       make(map[string]*model.CIModel),
		relations: make(map[string]*model.CIRelation),
	}
	m.seed()
	return m
}

// seed 预置示例 CI 与关系数据,构建一个有意义的拓扑图:
//
//	[LB] --connects_to--> [web-app] --depends_on--> [postgres-db]
//	                      [web-app] --deployed_on-> [web-server-01]
//	[postgres-db] --deployed_on-> [db-server-02]
//	[web-server-01] --backup_of-> [web-server-02]
//	[cache-svc] --connects_to--> [web-app]
func (m *memStore) seed() {
	now := time.Now()
	cis := []struct {
		id, typeID, name, desc string
		attrs                  map[string]any
	}{
		{"ci-lb-01", "loadbalancer", "前端负载均衡器", "Nginx LB", map[string]any{"vip": "10.0.1.100", "port": 443}},
		{"ci-app-01", "application", "核心业务应用", "订单服务", map[string]any{"runtime": "go", "port": 8080}},
		{"ci-db-01", "database", "主数据库", "PostgreSQL 主库", map[string]any{"engine": "postgres", "version": "15.3"}},
		{"ci-srv-01", "server", "应用服务器-01", "web-server-01", map[string]any{"ip": "10.0.2.11", "cpu": 8, "mem_gb": 32}},
		{"ci-srv-02", "server", "数据库服务器-02", "db-server-02", map[string]any{"ip": "10.0.2.12", "cpu": 16, "mem_gb": 64}},
		{"ci-srv-03", "server", "应用服务器-02", "web-server-02(备)", map[string]any{"ip": "10.0.2.13", "cpu": 8, "mem_gb": 32}},
		{"ci-cache-01", "service", "缓存服务", "Redis 集群", map[string]any{"engine": "redis", "mode": "cluster"}},
	}
	for _, c := range cis {
		m.cis[c.id] = &model.CIModel{
			ID:          c.id,
			TypeID:      c.typeID,
			TenantID:    "default",
			Name:        c.name,
			Description: c.desc,
			Status:      model.CIStatusActive,
			Attributes:  c.attrs,
			Labels:      map[string]string{"env": "production"},
			Channel:     model.WeightHuman,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}

	rels := []struct {
		source, target, relType, id string
	}{
		{"ci-lb-01", "ci-app-01", "connects_to", "rel-1"},
		{"ci-app-01", "ci-db-01", "depends_on", "rel-2"},
		{"ci-app-01", "ci-srv-01", "deployed_on", "rel-3"},
		{"ci-db-01", "ci-srv-02", "deployed_on", "rel-4"},
		{"ci-srv-01", "ci-srv-03", "backup_of", "rel-5"},
		{"ci-cache-01", "ci-app-01", "connects_to", "rel-6"},
	}
	for _, r := range rels {
		m.relations[r.id] = &model.CIRelation{
			ID:           r.id,
			TenantID:     "default",
			SourceID:     r.source,
			TargetID:     r.target,
			RelationType: r.relType,
			CreatedAt:    now,
		}
	}
}

// ---------- CI ----------

func (m *memStore) listCIs(tenantID, typeID string) []*model.CIModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*model.CIModel
	for _, ci := range m.cis {
		if ci.TenantID != tenantID || ci.Status == model.CIStatusDeleted {
			continue
		}
		if typeID != "" && ci.TypeID != typeID {
			continue
		}
		result = append(result, ci)
	}
	return result
}

func (m *memStore) getCI(id, tenantID string) *model.CIModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ci, ok := m.cis[id]
	if !ok || ci.TenantID != tenantID {
		return nil
	}
	return ci
}

func (m *memStore) createCI(ci *model.CIModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ci.ID == "" {
		ci.ID = uuid.New().String()
	}
	now := time.Now()
	ci.CreatedAt = now
	ci.UpdatedAt = now
	m.cis[ci.ID] = ci
}

func (m *memStore) updateCI(ci *model.CIModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ci.UpdatedAt = time.Now()
	m.cis[ci.ID] = ci
}

// setCIStatus 内存回退模式下设置 CI 状态（归档/恢复）。返回是否找到并更新成功。
func (m *memStore) setCIStatus(tenantID, id string, status model.CIStatus) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ci, ok := m.cis[id]
	if !ok || ci.TenantID != tenantID {
		return false
	}
	ci.Status = status
	ci.UpdatedAt = time.Now()
	return true
}

func (m *memStore) deleteCI(id, tenantID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ci, ok := m.cis[id]
	if !ok || ci.TenantID != tenantID {
		return false
	}
	ci.Status = model.CIStatusDeleted
	ci.UpdatedAt = time.Now()
	// 删除关联关系
	for rid, rel := range m.relations {
		if rel.SourceID == id || rel.TargetID == id {
			delete(m.relations, rid)
		}
	}
	return true
}

// ---------- Relation ----------

func (m *memStore) listRelations(tenantID string) []*model.CIRelation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*model.CIRelation
	for _, rel := range m.relations {
		if rel.TenantID != tenantID {
			continue
		}
		result = append(result, rel)
	}
	return result
}

func (m *memStore) createRelation(rel *model.CIRelation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rel.ID == "" {
		rel.ID = uuid.New().String()
	}
	rel.CreatedAt = time.Now()
	m.relations[rel.ID] = rel
}

func (m *memStore) deleteRelation(id, tenantID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	rel, ok := m.relations[id]
	if !ok || rel.TenantID != tenantID {
		return false
	}
	delete(m.relations, id)
	return true
}

// ---------- 拓扑查询(BFS)----------

// queryTopology 以 rootID 为根,做 BFS 遍历,返回深度 maxDepth 内的节点与边。
func (m *memStore) queryTopology(tenantID, rootID string, maxDepth int) ([]model.TopologyNode, []model.TopologyEdge) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	visited := map[string]bool{rootID: true}
	var nodes []model.TopologyNode
	var edges []model.TopologyEdge

	// 根节点
	if root, ok := m.cis[rootID]; ok && root.TenantID == tenantID {
		nodes = append(nodes, model.TopologyNode{
			ID: root.ID, Type: root.TypeID, Name: root.Name, TenantID: root.TenantID,
		})
	}

	frontier := []string{rootID}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, cid := range frontier {
			for _, rel := range m.relations {
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
				// 记录边(避免重复)
				edgeKey := rel.SourceID + "->" + rel.TargetID
				if !edgeExists(edges, rel.SourceID, rel.TargetID, rel.RelationType) {
					edges = append(edges, model.TopologyEdge{
						SourceID: rel.SourceID, TargetID: rel.TargetID, RelationType: rel.RelationType,
					})
					_ = edgeKey
				}
				if !visited[neighbor] {
					visited[neighbor] = true
					next = append(next, neighbor)
					if ci, ok := m.cis[neighbor]; ok && ci.TenantID == tenantID {
						nodes = append(nodes, model.TopologyNode{
							ID: ci.ID, Type: ci.TypeID, Name: ci.Name, TenantID: ci.TenantID,
						})
					}
				}
			}
		}
		frontier = next
	}
	return nodes, edges
}

func edgeExists(edges []model.TopologyEdge, src, tgt, rtype string) bool {
	for _, e := range edges {
		if e.SourceID == src && e.TargetID == tgt && e.RelationType == rtype {
			return true
		}
	}
	return false
}

// ---------- 影响分析(BFS,仅正向传播)----------

// impactAnalysis 以 sourceCI 为根,沿 source→target 方向 BFS,
// 返回所有受影响的 CI 列表(不含根本身)。
func (m *memStore) impactAnalysis(tenantID, sourceCI string, maxDepth int) (*model.ImpactResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	visited := map[string]bool{sourceCI: true}
	var affected []string
	hasCycle := false

	frontier := []string{sourceCI}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, cid := range frontier {
			for _, rel := range m.relations {
				if rel.TenantID != tenantID || rel.SourceID != cid {
					continue
				}
				if visited[rel.TargetID] {
					// 重新访问已访问节点说明存在环路
					hasCycle = true
					continue
				}
				visited[rel.TargetID] = true
				affected = append(affected, rel.TargetID)
				next = append(next, rel.TargetID)
			}
		}
		frontier = next
	}

	return &model.ImpactResult{
		SourceCI:   sourceCI,
		AffectedCI: affected,
		Depth:      maxDepth,
		HasCycle:   hasCycle,
	}, nil
}

// ---------- 环检测(DFS)----------

// detectCycle 检查从 source 到 target 新增一条边是否会形成环路。
func (m *memStore) detectCycle(tenantID, source, target string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 如果从 target 能到达 source,则新增 source->target 会成环
	visited := map[string]bool{}
	var dfs func(node string) bool
	dfs = func(node string) bool {
		if node == source {
			return true
		}
		if visited[node] {
			return false
		}
		visited[node] = true
		for _, rel := range m.relations {
			if rel.TenantID != tenantID || rel.SourceID != node {
				continue
			}
			if dfs(rel.TargetID) {
				return true
			}
		}
		return false
	}
	return dfs(target)
}

// ---------- 辅助 ----------
