//go:build integration

package cmdb

import (
	"context"
	"os"
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

// 集成测试: 模型字段语义(unique 唯一约束 / ref 引用 / enum 选项)的写入拦截。
// 依赖: PostgreSQL(OPSORIGIN_POSTGRES_DSN), 未配置时跳过。
func TestFieldSemanticsValidation(t *testing.T) {
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

	// 目标模型(被引用)
	if err := repo.CreateCIModelType(ctx, &model.CIModelType{
		ID: "svc-app-a", TenantID: tenant, Name: "应用A", Category: model.ModelCategoryApp,
		Schema: map[string]model.AttrDef{"port": {Type: "number"}},
	}); err != nil {
		t.Fatalf("create target model: %v", err)
	}
	t.Cleanup(func() { _ = repo.DeleteCIModelType(ctx, "svc-app-a", tenant) })

	// 被引用实例
	target := &model.CIModel{TypeID: "svc-app-a", TenantID: tenant, Name: "app-a-1", Status: model.CIStatusActive, Attributes: map[string]any{"port": 8080}}
	if err := repo.CreateCI(ctx, target, "t"); err != nil {
		t.Fatalf("create target ci: %v", err)
	}
	t.Cleanup(func() { _ = repo.DeleteCI(ctx, target.ID, tenant, "t") })

	// 带语义字段的模型
	if err := repo.CreateCIModelType(ctx, &model.CIModelType{
		ID: "svc-srv-a", TenantID: tenant, Name: "服务器A", Category: model.ModelCategoryInfra,
		Schema: map[string]model.AttrDef{
			"ip":      {Type: "string", Required: true, Unique: true},
			"app_ref": {Type: "ref", RefModel: "svc-app-a"},
			"env":     {Type: "enum", Options: []string{"prod", "dev"}},
		},
	}); err != nil {
		t.Fatalf("create semantic model: %v", err)
	}
	t.Cleanup(func() { _ = repo.DeleteCIModelType(ctx, "svc-srv-a", tenant) })

	newCI := func(attrs map[string]any) *model.CIModel {
		return &model.CIModel{TypeID: "svc-srv-a", TenantID: tenant, Name: "srv-a", Status: model.CIStatusActive, Attributes: attrs}
	}

	// 1. enum 非法值 → 拒绝
	err = svc.validateAttributes(ctx, "svc-srv-a", map[string]any{"ip": "10.0.0.1", "env": "staging"}, "")
	if err == nil {
		t.Fatal("enum 非法值应被拒绝")
	}
	t.Logf("enum reject ok: %v", err)

	// 2. ref 指向不存在的 CI → 拒绝
	err = svc.validateAttributes(ctx, "svc-srv-a", map[string]any{"ip": "10.0.0.1", "app_ref": "no-such-ci"}, "")
	if err == nil {
		t.Fatal("ref 指向不存在的 CI 应被拒绝")
	}
	t.Logf("ref reject ok: %v", err)

	// 3. 合法创建(唯一值 + 有效 ref + 合法 enum)
	ci := newCI(map[string]any{"ip": "10.0.0.1", "app_ref": target.ID, "env": "prod"})
	if err := svc.validateAttributes(ctx, ci.TypeID, ci.Attributes, ""); err != nil {
		t.Fatalf("合法属性应通过: %v", err)
	}
	if err := repo.CreateCI(ctx, ci, "t"); err != nil {
		t.Fatalf("create ci: %v", err)
	}
	t.Cleanup(func() { _ = repo.DeleteCI(ctx, ci.ID, tenant, "t") })

	// 4. 唯一字段重复 → 拒绝(创建场景)
	err = svc.validateAttributes(ctx, "svc-srv-a", map[string]any{"ip": "10.0.0.1"}, "")
	if err == nil || !containsString([]string{err.Error()}, err.Error()) {
		if err == nil {
			t.Fatal("唯一字段重复应被拒绝")
		}
	}
	t.Logf("unique create reject ok: %v", err)

	// 5. 更新场景: 排除自身后可保留原值; 撞他人则拒绝
	if err := svc.validateAttributes(ctx, "svc-srv-a", map[string]any{"ip": "10.0.0.1"}, ci.ID); err != nil {
		t.Fatalf("更新排除自身应通过: %v", err)
	}
	other := &model.CIModel{TypeID: "svc-srv-a", TenantID: tenant, Name: "srv-b", Status: model.CIStatusActive, Attributes: map[string]any{"ip": "10.0.0.2"}}
	if err := repo.CreateCI(ctx, other, "t"); err != nil {
		t.Fatalf("create other: %v", err)
	}
	t.Cleanup(func() { _ = repo.DeleteCI(ctx, other.ID, tenant, "t") })
	err = svc.validateAttributes(ctx, "svc-srv-a", map[string]any{"ip": "10.0.0.2"}, ci.ID)
	if err == nil {
		t.Fatal("更新撞他人唯一值应被拒绝")
	}
	t.Logf("unique update reject ok: %v", err)
}
