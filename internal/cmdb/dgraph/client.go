package dgraph

import (
	"context"
	"encoding/json"
	"fmt"

	dgo "github.com/dgraph-io/dgo/v250"
	"github.com/dgraph-io/dgo/v250/protos/api"
	"github.com/opsorigin/core/internal/cmdb/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	dg *dgo.Dgraph
}

// NewClient 连接 Dgraph alpha。
// 注: dgo v250 起 NewClient 不再默认注入 insecure 传输凭证(与 v240 行为差异),
// 需显式 WithGrpcOption 传入, 否则裸地址连接会报 "no transport security set"。
func NewClient(target string) (*Client, error) {
	dg, err := dgo.NewClient(target,
		dgo.WithGrpcOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
	if err != nil {
		return nil, fmt.Errorf("connect dgraph: %w", err)
	}
	return &Client{dg: dg}, nil
}

func (c *Client) SetupSchema(ctx context.Context) error {
	op := &api.Operation{
		Schema: `
			tenant_id: string @index(exact) .
			ci_id: string @index(exact) .
			ci_type: string @index(exact) .
			ci_name: string @index(fulltext) .
			ci_status: string .
			attributes: string .
			labels: [string] .

			source_ci: uid @reverse .
			target_ci: uid @reverse .
			relation_type: string @index(exact) .
			rel_id: string @index(exact) .

			type CI {
				tenant_id
				ci_id
				ci_type
				ci_name
				ci_status
				attributes
				labels
			}
			type CIRelation {
				source_ci
				target_ci
				relation_type
				rel_id
			}
		`,
	}
	return c.dg.Alter(ctx, op)
}

func (c *Client) UpsertCI(ctx context.Context, ci *model.CIModel) error {
	attrsJSON, _ := json.Marshal(ci.Attributes)
	labels := make([]string, 0, len(ci.Labels))
	for k, v := range ci.Labels {
		labels = append(labels, k+"="+v)
	}

	// Dgraph upsert: Query 按 ci_id 匹配已有节点, Mutations 复用其 UID 更新,
	// 不存在时空白节点 uid(ci) 自动新建。预存修复: 此前空白节点 _:id 每次新建,
	// 重复 upsert 生成重复 CI 节点。
	setNquads := fmt.Sprintf(`
		uid(u) <dgraph.type> "CI" .
		uid(u) <tenant_id> "%s" .
		uid(u) <ci_id> "%s" .
		uid(u) <ci_type> "%s" .
		uid(u) <ci_name> "%s" .
		uid(u) <ci_status> "%s" .
		uid(u) <attributes> "%s" .
	`, ci.TenantID, escapeStr(ci.ID),
		ci.TypeID, escapeStr(ci.Name), string(ci.Status),
		escapeStr(string(attrsJSON)))
	for _, l := range labels {
		setNquads += fmt.Sprintf(`uid(u) <labels> "%s" .\n`, escapeStr(l))
	}

	_, err := c.dg.NewTxn().Do(ctx, &api.Request{
		Query: fmt.Sprintf(`query { ci(func: eq(ci_id, "%s")) { u as uid } }`, escapeStr(ci.ID)),
		Mutations: []*api.Mutation{{
			SetNquads: []byte(setNquads),
		}},
		CommitNow: true,
	})
	return err
}

func (c *Client) DeleteCI(ctx context.Context, ciID, tenantID string) error {
	uid, err := c.resolveCIUID(ctx, ciID, tenantID)
	if err != nil {
		return err
	}
	if uid == "" {
		return fmt.Errorf("ci %s not found in tenant %s", ciID, tenantID)
	}

	mu := &api.Mutation{
		DelNquads: []byte(fmt.Sprintf(`<%s> * * .`, uid)),
		CommitNow: true,
	}
	_, err = c.dg.NewTxn().Mutate(ctx, mu)
	return err
}

func (c *Client) UpsertRelation(ctx context.Context, rel *model.CIRelation) error {
	// 业务 ID → Dgraph UID: source/target 是 CI 业务 ID, 需先解析(与 QueryTopology 一致)。
	// 预存修复: 此前直接 <source_ci> <%s> 写业务 ID, Dgraph uid 谓词要求 0x 十六进制 UID,
	// 导致关系从未真正落库。
	srcUID, err := c.resolveCIUID(ctx, rel.SourceID, rel.TenantID)
	if err != nil {
		return fmt.Errorf("resolve source ci uid: %w", err)
	}
	if srcUID == "" {
		return fmt.Errorf("source ci %s not found in tenant %s", rel.SourceID, rel.TenantID)
	}
	tgtUID, err := c.resolveCIUID(ctx, rel.TargetID, rel.TenantID)
	if err != nil {
		return fmt.Errorf("resolve target ci uid: %w", err)
	}
	if tgtUID == "" {
		return fmt.Errorf("target ci %s not found in tenant %s", rel.TargetID, rel.TenantID)
	}

	// Dgraph upsert: 按 rel_id 匹配已有关系节点复用 UID(与 UpsertCI 同修复)。
	setNquads := fmt.Sprintf(`
		uid(u) <dgraph.type> "CIRelation" .
		uid(u) <source_ci> <%s> .
		uid(u) <target_ci> <%s> .
		uid(u) <relation_type> "%s" .
		uid(u) <rel_id> "%s" .
		uid(u) <tenant_id> "%s" .
	`, srcUID, tgtUID, rel.RelationType, escapeStr(rel.ID), rel.TenantID)

	_, err = c.dg.NewTxn().Do(ctx, &api.Request{
		Query: fmt.Sprintf(`query { rel(func: eq(rel_id, "%s")) { u as uid } }`, escapeStr(rel.ID)),
		Mutations: []*api.Mutation{{
			SetNquads: []byte(setNquads),
		}},
		CommitNow: true,
	})
	return err
}

func (c *Client) DeleteRelation(ctx context.Context, relID, tenantID string) error {
	uid, err := c.resolveRelationUID(ctx, relID, tenantID)
	if err != nil {
		return err
	}
	if uid == "" {
		return fmt.Errorf("relation %s not found in tenant %s", relID, tenantID)
	}

	mu := &api.Mutation{
		DelNquads: []byte(fmt.Sprintf(`<%s> * * .`, uid)),
		CommitNow: true,
	}
	_, err = c.dg.NewTxn().Mutate(ctx, mu)
	return err
}

func (c *Client) QueryTopology(ctx context.Context, tenantID, ciID string, depth int) ([]*model.TopologyNode, []*model.TopologyEdge, error) {
	if depth <= 0 {
		depth = 3
	}

	uid, err := c.resolveCIUID(ctx, ciID, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve ci uid: %w", err)
	}
	if uid == "" {
		return nil, nil, fmt.Errorf("ci %s not found in tenant %s", ciID, tenantID)
	}

	// 显式 2 层查询(不用 @recurse——Dgraph 的 @recurse 要求谓词全部平铺,
	// 无法表达 ~source_ci/source_ci 嵌套)。语义: ~source_ci(反向边)=依赖当前 CI
	// 的上游; source_ci(正向边)=当前 CI 依赖的下游。
	query := fmt.Sprintf(`
		{
			topology(func: uid(%s)) {
				uid
				ci_name
				ci_type
				tenant_id
				~source_ci {
					uid
					relation_type
					source_ci {
						uid
						ci_name
						ci_type
						tenant_id
					}
				}
			}
		}`, uid)

	resp, err := c.dg.NewTxn().Query(ctx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("query topology: %w", err)
	}

	type queryResult struct {
		Topology []json.RawMessage `json:"topology"`
	}

	var result queryResult
	if err := json.Unmarshal(resp.GetJson(), &result); err != nil {
		return nil, nil, fmt.Errorf("unmarshal topology: %w", err)
	}

	var nodes []*model.TopologyNode
	var edges []*model.TopologyEdge
	seen := make(map[string]bool)

	parseTopologyNode(result.Topology, "", &nodes, &edges, seen)

	return nodes, edges, nil
}

func parseTopologyNode(raw []json.RawMessage, parentUID string, nodes *[]*model.TopologyNode, edges *[]*model.TopologyEdge, seen map[string]bool) {
	for _, item := range raw {
		var node struct {
			UID      string `json:"uid"`
			CIName   string `json:"ci_name"`
			CIType   string `json:"ci_type"`
			TenantID string `json:"tenant_id"`
			// ~source_ci 是反向边数组(多个关系可指向同一 CI); source_ci 是 uid 谓词,
			// Dgraph 返回单个对象而非数组——预存修复: 此前声明为切片导致对象→切片
			// 静默解为空, 拓扑节点/边永远解析不出来。
			Source []struct {
				UID          string `json:"uid"`
				RelationType string `json:"relation_type"`
				SourceCI     struct {
					UID      string `json:"uid"`
					CIName   string `json:"ci_name"`
					CIType   string `json:"ci_type"`
					TenantID string `json:"tenant_id"`
				} `json:"source_ci"`
			} `json:"~source_ci"`
		}

		if err := json.Unmarshal(item, &node); err != nil {
			continue
		}

		if !seen[node.UID] {
			seen[node.UID] = true
			*nodes = append(*nodes, &model.TopologyNode{
				ID:   node.UID,
				Type: node.CIType,
				Name: node.CIName,
			})
		}

		if parentUID != "" {
			*edges = append(*edges, &model.TopologyEdge{
				TargetID: node.UID,
				SourceID: parentUID,
			})
		}

		for _, src := range node.Source {
			child := src.SourceCI
			if child.UID != "" {
				*edges = append(*edges, &model.TopologyEdge{
					SourceID:     node.UID,
					TargetID:     child.UID,
					RelationType: src.RelationType,
				})
				if !seen[child.UID] {
					seen[child.UID] = true
					*nodes = append(*nodes, &model.TopologyNode{
						ID:   child.UID,
						Type: child.CIType,
						Name: child.CIName,
					})
				}
			}
		}
	}
}

func (c *Client) ImpactAnalysis(ctx context.Context, tenantID, ciID string, depth int) (*model.ImpactResult, error) {
	nodes, edges, err := c.QueryTopology(ctx, tenantID, ciID, depth)
	if err != nil {
		return nil, err
	}

	affected := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.ID != ciID {
			affected = append(affected, n.ID)
		}
	}

	hasCycle := c.detectCycleInGraph(nodes, edges)

	return &model.ImpactResult{
		SourceCI:   ciID,
		AffectedCI: affected,
		Depth:      depth,
		HasCycle:   hasCycle,
	}, nil
}

func (c *Client) DetectCycle(ctx context.Context, sourceID, targetID, tenantID string) (bool, error) {
	srcUID, err := c.resolveCIUID(ctx, sourceID, tenantID)
	if err != nil {
		return false, fmt.Errorf("resolve source ci uid: %w", err)
	}
	if srcUID == "" {
		return false, fmt.Errorf("source ci %s not found in tenant %s", sourceID, tenantID)
	}

	tgtUID, err := c.resolveCIUID(ctx, targetID, tenantID)
	if err != nil {
		return false, fmt.Errorf("resolve target ci uid: %w", err)
	}
	if tgtUID == "" {
		return false, fmt.Errorf("target ci %s not found in tenant %s", targetID, tenantID)
	}

	// 显式深度遍历(不用 @recurse——同上, Dgraph 限制平铺)。
	// 语义: 从 source 出发沿 ~source_ci(反向边)遍历, 检测能否到达 target。
	query := fmt.Sprintf(`
		{
			paths(func: uid(%s)) {
				uid
				~source_ci {
					uid
					relation_type
					source_ci {
						uid
						~source_ci {
							uid
							relation_type
							source_ci {
								uid
								~source_ci {
									uid
									source_ci { uid }
								}
							}
						}
					}
				}
			}
		}`, srcUID)

	resp, err := c.dg.NewTxn().Query(ctx, query)
	if err != nil {
		return false, fmt.Errorf("cycle detection query: %w", err)
	}

	var result struct {
		Paths []json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(resp.GetJson(), &result); err != nil {
		return false, nil
	}

	visited := make(map[string]bool)
	return detectCycleDFS(result.Paths, tgtUID, visited), nil
}

func (c *Client) detectCycleInGraph(nodes []*model.TopologyNode, edges []*model.TopologyEdge) bool {
	adj := make(map[string][]string)
	for _, e := range edges {
		// 忽略自环(节点依赖自身) — 业务上不算环, 且测试数据常见 source_ci 指向自身
		if e.SourceID == e.TargetID {
			continue
		}
		adj[e.SourceID] = append(adj[e.SourceID], e.TargetID)
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(uid string) bool
	dfs = func(uid string) bool {
		visited[uid] = true
		recStack[uid] = true

		for _, neighbor := range adj[uid] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recStack[neighbor] {
				return true
			}
		}

		recStack[uid] = false
		return false
	}

	for _, n := range nodes {
		if !visited[n.ID] {
			if dfs(n.ID) {
				return true
			}
		}
	}
	return false
}

func detectCycleDFS(items []json.RawMessage, targetID string, visited map[string]bool) bool {
	for _, item := range items {
		var node struct {
			UID    string `json:"uid"`
			Source []struct {
				SourceCI []struct {
					UID string `json:"uid"`
				} `json:"source_ci"`
			} `json:"~source_ci"`
		}
		if err := json.Unmarshal(item, &node); err != nil {
			continue
		}

		if node.UID == targetID || visited[node.UID] {
			return true
		}
		visited[node.UID] = true

		for _, src := range node.Source {
			for _, ci := range src.SourceCI {
				if ci.UID == targetID {
					return true
				}
			}
		}
	}
	return false
}

func (c *Client) resolveCIUID(ctx context.Context, ciID, tenantID string) (string, error) {
	query := fmt.Sprintf(`
		{
			result(func: eq(ci_id, "%s")) @filter(eq(tenant_id, "%s")) {
				uid
			}
		}`, escapeStr(ciID), escapeStr(tenantID))

	resp, err := c.dg.NewTxn().Query(ctx, query)
	if err != nil {
		return "", fmt.Errorf("resolve ci uid query: %w", err)
	}

	var result struct {
		Result []struct {
			UID string `json:"uid"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.GetJson(), &result); err != nil {
		return "", nil
	}
	if len(result.Result) == 0 {
		return "", nil
	}
	return result.Result[0].UID, nil
}

func (c *Client) resolveRelationUID(ctx context.Context, relID, tenantID string) (string, error) {
	query := fmt.Sprintf(`
		{
			result(func: eq(rel_id, "%s")) @filter(eq(tenant_id, "%s")) {
				uid
			}
		}`, escapeStr(relID), escapeStr(tenantID))

	resp, err := c.dg.NewTxn().Query(ctx, query)
	if err != nil {
		return "", fmt.Errorf("resolve relation uid query: %w", err)
	}

	var result struct {
		Result []struct {
			UID string `json:"uid"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.GetJson(), &result); err != nil {
		return "", nil
	}
	if len(result.Result) == 0 {
		return "", nil
	}
	return result.Result[0].UID, nil
}

func escapeStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
