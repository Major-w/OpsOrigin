//go:build integration

package cmdb

import (
	"context"
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// TestRelationTargetSubclassMatching 验证关系目标模型校验支持"子类匹配"：
// 业务/应用声明 depends_on → database（抽象父类）时，实际目标为 mysql 实例（子类）应放行。
// 关联需求：让内置模型的预置关联路径（app→database/middleware 抽象父类）对 leaf 子类生效。
func TestRelationTargetSubclassMatching(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	const tenant = "default"

	dbParent := &model.CIModelType{
		Name:     "__rel_db_parent",
		TenantID: tenant, Category: model.ModelCategoryDatabase, Abstract: true,
		Schema: map[string]model.AttrDef{"port": {Type: "int"}},
	}
	if err := repo.CreateCIModelType(ctx, dbParent); err != nil {
		t.Fatalf("create db parent: %v", err)
	}
	defer repo.DeleteCIModelType(ctx, dbParent.ID, tenant)

	mysqlChild := &model.CIModelType{
		Name: "__rel_mysql_child", TenantID: tenant, ParentID: dbParent.ID, Category: model.ModelCategoryDatabase,
		Schema: map[string]model.AttrDef{"binlog": {Type: "string"}},
	}
	if err := repo.CreateCIModelType(ctx, mysqlChild); err != nil {
		t.Fatalf("create mysql child: %v", err)
	}
	defer repo.DeleteCIModelType(ctx, mysqlChild.ID, tenant)

	svc := &Service{pg: repo}

	// mysql 是 database 的子类 → 匹配
	if !svc.modelIsOrExtends(ctx, mysqlChild.ID, dbParent.ID, tenant) {
		t.Errorf("mysql 应识别为 database 子类")
	}
	// 自身相等 → 匹配
	if !svc.modelIsOrExtends(ctx, dbParent.ID, dbParent.ID, tenant) {
		t.Errorf("自身应匹配")
	}
	// 无关模型不匹配
	if svc.modelIsOrExtends(ctx, mysqlChild.ID, "server", tenant) {
		t.Errorf("mysql 不应匹配 server")
	}
	// 不存在的子类链不应误判
	if svc.modelIsOrExtends(ctx, "nonexistent", dbParent.ID, tenant) {
		t.Errorf("不存在模型不应匹配")
	}
}
