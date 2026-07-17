package dgraph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dgraph-io/dgo/v240"
	"github.com/dgraph-io/dgo/v240/protos/api"
	"github.com/opsorigin/core/internal/cmdb/model"
)

type Client struct {
	dg  *dgo.Dgraph
}

func NewClient(target string) (*Client, error) {
	dg, err := dgo.NewClient(target)
	if err != nil {
		return nil, fmt.Errorf("connect dgraph: %w", err)
	}
	return &Client{dg: dg}, nil
}

func (c *Client) SetupSchema(ctx context.Context) error {
	op := &api.Operation{
		Schema: `
			tenant_id: string @index(exact) .
			ci_type: string @index(exact) .
			ci_name: string @index(fulltext) .
			ci_status: string .
			attributes: string .
			labels: [string] .

			source_ci: uid @reverse .
			target_ci: uid @reverse .
			relation_type: string @index(exact) .

			type Tenant.ci
			type CI {
				tenant_id
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

	mu := &api.Mutation{
		SetNquads: []byte(fmt.Sprintf(`
			_:%s <dgraph.type> "CI" .
			_:%s <tenant_id> "%s" .
			_:%s <ci_type> "%s" .
			_:%s <ci_name> "%s" .
			_:%s <ci_status> "%s" .
			_:%s <attributes> "%s" .
		`, ci.ID, ci.ID, ci.TenantID, ci.ID, ci.TypeID,
			ci.ID, escapeStr(ci.Name), ci.ID, string(ci.Status),
			ci.ID, escapeStr(string(attrsJSON)))),
		CommitNow: true,
	}

	for _, l := range labels {
		mu.SetNquads = append(mu.SetNquads, []byte(fmt.Sprintf(`_:%s <labels> "%s" .\n`, ci.ID, escapeStr(l)))...)
	}

	_, err := c.dg.NewTxn().Mutate(ctx, mu)
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
	mu := &api.Mutation{
		SetNquads: []byte(fmt.Sprintf(`
			_:%s <dgraph.type> "CIRelation" .
			_:%s <source_ci> <%s> .
			_:%s <target_ci> <%s> .
			_:%s <relation_type> "%s" .
			_:%s <tenant_id> "%s" .
		`, rel.ID, rel.ID, rel.SourceID, rel.ID, rel.TargetID,
			rel.ID, rel.RelationType, rel.ID, rel.TenantID)),
		CommitNow: true,
	}
	_, err := c.dg.NewTxn().Mutate(ctx, mu)
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

	query := fmt.Sprintf(`
		{
			topology(func: uid(%s)) @recurse(depth: %d) {
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
		}`, uid, depth)

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
			Source   []struct {
				UID          string `json:"uid"`
				RelationType string `json:"relation_type"`
				SourceCI     []struct {
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
			*edges = append(*edges, &model.TopologyEdge{
				SourceID:     node.UID,
				TargetID:     src.SourceCI[0].UID,
				RelationType: src.RelationType,
			})
			for _, child := range src.SourceCI {
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

	query := fmt.Sprintf(`
		{
			paths(func: uid(%s)) @recurse(depth: 10) {
				uid
				~source_ci {
					source_ci { uid }
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
			result(func: eq(tenant_id, "%s")) @filter(uid(%s)) {
				uid
			}
		}`, escapeStr(tenantID), ciID)

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
			result(func: eq(tenant_id, "%s")) @filter(uid(%s)) {
				uid
			}
		}`, escapeStr(tenantID), relID)

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
