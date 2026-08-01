package dgraph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// TestClientCompat_V250 用 dgo v250 客户端连真实 Dgraph 验证项目 client 完整路径。
// 需要本地 docker 起了 dgraph alpha (localhost:19080, whitelist=0.0.0.0/0)。
// 未起服务时跳过。
func TestClientCompat_V250(t *testing.T) {
	c, err := NewClient("localhost:19080")
	if err != nil {
		t.Skipf("dgraph not running: %v", err)
	}
	ctx := context.Background()
	if err := c.SetupSchema(ctx); err != nil {
		t.Fatalf("setup schema: %v", err)
	}

	// 写入 CI + Relation
	ci1 := &model.CIModel{ID: "ci-v250-1", TenantID: "default", TypeID: "server", Name: "web-01", Status: "running"}
	ci2 := &model.CIModel{ID: "ci-v250-2", TenantID: "default", TypeID: "db", Name: "db-01", Status: "running"}
	if err := c.UpsertCI(ctx, ci1); err != nil {
		t.Fatalf("upsert ci1: %v", err)
	}
	if err := c.UpsertCI(ctx, ci2); err != nil {
		t.Fatalf("upsert ci2: %v", err)
	}
	rel := &model.CIRelation{ID: "rel-v250-1", TenantID: "default", SourceID: "ci-v250-1", TargetID: "ci-v250-2", RelationType: "connects"}
	if err := c.UpsertRelation(ctx, rel); err != nil {
		t.Fatalf("upsert relation: %v", err)
	}

	// 重复 upsert 不应产生重复节点(upsert 语义)
	if err := c.UpsertCI(ctx, ci1); err != nil {
		t.Fatalf("re-upsert ci1: %v", err)
	}
	count := countNodes(t, c, "ci_id", "ci-v250-1")
	if count != 1 {
		t.Fatalf("expected 1 ci-v250-1 node after re-upsert, got %d", count)
	}

	// 拓扑查询(项目 QueryTopology 真实路径): ci1 -> ci2 一条边
	nodes, edges, err := c.QueryTopology(ctx, "default", "ci-v250-1", 3)
	if err != nil {
		t.Fatalf("query topology: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected non-empty topology nodes")
	}
	t.Logf("topology nodes=%d edges=%d", len(nodes), len(edges))

	// 影响分析: ci1 受影响(自身 + 关系指向)
	impact, err := c.ImpactAnalysis(ctx, "default", "ci-v250-1", 3)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	t.Logf("impact affected=%d hasCycle=%v", len(impact.AffectedCI), impact.HasCycle)
	if impact.HasCycle {
		t.Fatal("expected no cycle in acyclic graph")
	}

	// 环检测(无环场景应 false)
	hasCycle, err := c.DetectCycle(ctx, "ci-v250-1", "ci-v250-2", "default")
	if err != nil {
		t.Fatalf("detect cycle: %v", err)
	}
	if hasCycle {
		t.Fatal("expected no cycle")
	}

	// 清理
	if err := c.DeleteRelation(ctx, "rel-v250-1", "default"); err != nil {
		t.Fatalf("delete relation: %v", err)
	}
	if err := c.DeleteCI(ctx, "ci-v250-1", "default"); err != nil {
		t.Fatalf("delete ci1: %v", err)
	}
	if err := c.DeleteCI(ctx, "ci-v250-2", "default"); err != nil {
		t.Fatalf("delete ci2: %v", err)
	}
	t.Log("ALL PROJECT CLIENT METHODS PASSED with dgo v250")
}

// countNodes 查询具有指定谓词值的节点数(验证 upsert 不重复)。
func countNodes(t *testing.T, c *Client, pred, val string) int {
	t.Helper()
	q := `{ all(func: eq(` + pred + `, "` + val + `")) { uid } }`
	resp, err := c.dg.NewTxn().Query(context.Background(), q)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	var out struct {
		All []struct{ UID string `json:"uid"` } `json:"all"`
	}
	if err := json.Unmarshal(resp.GetJson(), &out); err != nil {
		t.Fatalf("count unmarshal: %v", err)
	}
	return len(out.All)
}
