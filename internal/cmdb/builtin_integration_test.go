//go:build integration

package cmdb

import (
	"context"
	"os"
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

// 集成测试: EnsureDiscoveryModelTypes 真实落库 —— 6 类内置模型创建成功、
// 幂等(重复执行不报错不重复)、business_service 携带 business_criticality 字段。
// 依赖: PostgreSQL(OPSORIGIN_POSTGRES_DSN), 未配置时跳过。
func TestEnsureBuiltinModelTypes(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("pg repo: %v", err)
	}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{pg: repo}
	const tenant = "default"

	// 1. 首次执行: 6 类全部创建
	if err := svc.EnsureDiscoveryModelTypes(ctx, tenant); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, mt := range buildBuiltinModelTypes() {
		got, err := repo.GetCIModelType(ctx, mt.ID, tenant)
		if err != nil || got == nil {
			t.Fatalf("内置模型 %s 应已落库: err=%v", mt.ID, err)
		}
		if !got.Builtin {
			t.Errorf("模型 %s 应标记 builtin", mt.ID)
		}
	}

	// 2. 幂等: 再次执行不报错、不产生重复
	if err := svc.EnsureDiscoveryModelTypes(ctx, tenant); err != nil {
		t.Fatalf("重复 ensure 应幂等: %v", err)
	}
	types, err := repo.ListCIModelTypes(ctx, tenant, 1000, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	count := 0
	for _, mt := range types {
		if mt.ID == model.ModelTypeBusinessService || mt.ID == model.ModelTypeServer {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("幂等失败: business_service/server 各应只有 1 个, got %d", count)
	}

	// 3. business_service 的 criticality 字段
	bs, err := repo.GetCIModelType(ctx, model.ModelTypeBusinessService, tenant)
	if err != nil {
		t.Fatalf("get business_service: %v", err)
	}
	def, ok := bs.Schema[model.AttrKeyCriticality]
	if !ok {
		t.Fatal("business_service 应含 business_criticality 字段")
	}
	if def.Type != "string" {
		t.Errorf("criticality 类型应为 string: %s", def.Type)
	}
}
