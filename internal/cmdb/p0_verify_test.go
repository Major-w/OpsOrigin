package cmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

func newTestRepo(t *testing.T) *pg.Repository {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set")
	}
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return repo
}

func TestP0ResolveModelTypeInheritance(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	const tenant = "default"

	parent := &model.CIModelType{
		Name: "__p0_parent", TenantID: tenant, Category: model.ModelCategoryPlatform,
		Schema: map[string]model.AttrDef{"ip": {Type: "string", Required: true}, "os": {Type: "string"}},
	}
	if err := repo.CreateCIModelType(ctx, parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	defer repo.DeleteCIModelType(ctx, parent.ID, tenant)

	child := &model.CIModelType{
		Name: "__p0_child", TenantID: tenant, ParentID: parent.ID, Category: model.ModelCategoryPlatform,
		Schema: map[string]model.AttrDef{"role": {Type: "string"}},
	}
	if err := repo.CreateCIModelType(ctx, child); err != nil {
		t.Fatalf("create child: %v", err)
	}
	defer repo.DeleteCIModelType(ctx, child.ID, tenant)

	svc := &Service{pg: repo}
	resolved, err := svc.ResolveModelType(ctx, child.ID, tenant)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved == nil {
		t.Fatal("resolved is nil")
	}
	// 子类应合并父类属性，且自身属性覆盖同名
	if _, ok := resolved.Schema["ip"]; !ok {
		t.Error("继承属性 'ip' 缺失")
	}
	if !resolved.Schema["ip"].Required {
		t.Error("继承属性的 required 标记丢失")
	}
	if _, ok := resolved.Schema["os"]; !ok {
		t.Error("继承属性 'os' 缺失")
	}
	if _, ok := resolved.Schema["role"]; !ok {
		t.Error("自身属性 'role' 缺失")
	}
	t.Logf("merged schema keys: %v", keysOf(resolved.Schema))
}

func TestP0ExportImportRoundtrip(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	const tenant = "default"

	parent := &model.CIModelType{
		Name: "__p0_exp_parent", TenantID: tenant, Category: model.ModelCategoryInfra, Icon: "🖥️",
		Schema: map[string]model.AttrDef{"ip": {Type: "string", Required: true}},
	}
	if err := repo.CreateCIModelType(ctx, parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	defer repo.DeleteCIModelType(ctx, parent.ID, tenant)

	svc := &Service{pg: repo}

	// 导出
	req := httptest.NewRequest("GET", "/api/v1/cmdb/model-type/export", nil)
	w := httptest.NewRecorder()
	svc.handleExportModelTypes(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("export code %d body=%s", w.Code, w.Body.String())
	}
	var exported []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &exported); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	found := false
	for _, m := range exported {
		if m["name"] == "__p0_exp_parent" {
			found = true
			if m["category"] != model.ModelCategoryInfra {
				t.Errorf("export category mismatch: %v", m["category"])
			}
		}
	}
	if !found {
		t.Error("导出的模型列表中未包含测试模型")
	}

	// 导入：一个带父引用的新模型，验证 old id → new id 映射与创建计数
	importPayload := []map[string]any{
		{"id": "old-parent", "name": "__p0_imp_parent", "category": "platform", "icon": "📦", "schema": map[string]any{}},
		{"id": "old-child", "name": "__p0_imp_child", "parent_id": "old-parent", "category": "platform", "schema": map[string]any{}},
	}
	body, _ := json.Marshal(importPayload)
	ireq := httptest.NewRequest("POST", "/api/v1/cmdb/model-type/import", bytes.NewReader(body))
	iw := httptest.NewRecorder()
	svc.handleImportModelTypes(iw, ireq)
	if iw.Code != http.StatusOK {
		t.Fatalf("import code %d body=%s", iw.Code, iw.Body.String())
	}
	var res struct {
		Created int `json:"created"`
		Skipped int `json:"skipped"`
	}
	json.Unmarshal(iw.Body.Bytes(), &res)
	if res.Created != 2 {
		t.Errorf("期望导入创建 2 个，实际 %+v", res)
	}
	// 清理导入的模型
	if ms, err := repo.ListCIModelTypes(ctx, tenant, 1000, 0); err == nil {
		for _, m := range ms {
			if m.Name == "__p0_imp_parent" || m.Name == "__p0_imp_child" {
				repo.DeleteCIModelType(ctx, m.ID, tenant)
			}
		}
	}
}

func keysOf(m map[string]model.AttrDef) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
