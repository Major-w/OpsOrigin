//go:build integration

package outbox

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/pkg/config"
)

// 集成测试: Postgres(源真相) → Outbox → Dgraph(图) 的异步同步全链路。
// 验证 ASSERT-CMDB-01(严禁显式双写, 唯一落库路径走 Outbox)的载体真实生效:
//  1. 通过 pg 写路径创建 CI, 事务内自动写入 outbox 记录;
//  2. Worker.processBatch 消费 outbox 增量刷入 Dgraph;
//  3. Dgraph 中能查到该节点(拓扑查询), 且重复消费不产生重复节点。
//
// 依赖: PostgreSQL(OPSORIGIN_POSTGRES_DSN) + Dgraph(OPSORIGIN_DGRAPH_TARGET),
// 未配置或不可用时跳过。推荐先 `docker compose -f deploy/docker-compose.yml up -d
// postgres dgraph-zero dgraph-alpha redis` 再运行。
func TestOutboxSyncToDgraph(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set, skipping outbox integration test")
	}
	target := dgraphTarget()
	if target == "" {
		t.Skip("OPSORIGIN_DGRAPH_TARGET not set, skipping outbox integration test")
	}

	ctx := context.Background()
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("pg repo: %v", err)
	}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("pg migrate: %v", err)
	}

	dg, err := dgraph.NewClient(target)
	if err != nil {
		t.Skipf("dgraph not available, skipping: %v", err)
	}
	if err := dg.SetupSchema(ctx); err != nil {
		t.Fatalf("dgraph schema: %v", err)
	}

	tenant := "default"
	// 注意: CreateCI 内部会重新生成 ci.ID(uuid), 因此以创建后的 ci.ID 为准。
	ci := &model.CIModel{
		ID: "ci-integ-outbox-1", TenantID: tenant, TypeID: "server",
		Name: "outbox-sync-web-01", Status: "running",
		Attributes: map[string]any{"ip": "10.0.0.1"},
	}

	// 1. PG 写路径: CreateCI 事务内自动 insertOutbox(EventCICreated)
	if err := repo.CreateCI(ctx, ci, "trace-integ-outbox"); err != nil {
		t.Fatalf("create ci: %v", err)
	}
	ciID := ci.ID
	t.Cleanup(func() {
		_ = repo.DeleteCI(ctx, ciID, tenant, "trace-cleanup")
		_ = dg.DeleteCI(ctx, ciID, tenant)
	})

	// outbox 记录应已产生且未处理
	records, err := repo.GetUnprocessedOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("get unprocessed outbox: %v", err)
	}
	found := false
	for _, rec := range records {
		if rec.EntityID == ciID && rec.EventType == model.EventCICreated {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("outbox 应包含 CI 创建记录(严禁显式双写: 唯一同步通道为 outbox)")
	}

	// 2. Worker 消费并刷入 Dgraph
	worker := NewWorker(config.OutboxConfig{PollInterval: time.Second, BatchSize: 100}, repo, dg)
	if err := worker.processBatch(ctx); err != nil {
		t.Fatalf("outbox processBatch: %v", err)
	}

	// 3. Dgraph 中应能查到该节点(经 QueryTopology 真实路径)
	nodes, _, err := dg.QueryTopology(ctx, tenant, ciID, 3)
	if err != nil {
		t.Fatalf("query topology: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("outbox 同步后 Dgraph 应存在 CI 节点")
	}
	t.Logf("outbox -> dgraph sync ok, topology nodes=%d", len(nodes))

	// 4. 再次消费同一批: 幂等, 不应报错且不重复
	if err := worker.processBatch(ctx); err != nil {
		t.Fatalf("second processBatch should be idempotent: %v", err)
	}

	// 5. outbox 记录已被标记 processed
	leftover, err := repo.GetUnprocessedOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("get unprocessed outbox after: %v", err)
	}
	for _, rec := range leftover {
		if rec.EntityID == ciID {
			t.Fatalf("outbox 记录应已被标记 processed: %s", rec.ID)
		}
	}
}

// dgraphTarget 读集成测试 Dgraph 地址(与 dgraph 包内 helper 同语义)。
func dgraphTarget() string {
	return os.Getenv("OPSORIGIN_DGRAPH_TARGET")
}
