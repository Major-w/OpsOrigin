package cmdb

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

// TestP1CICSV 在真实 Postgres 上验证 CI 实例的 CSV 导出/导入往返：
// 导出表头含 id/name/description/status + schema 属性列；导入支持按 id upsert 更新 + 新建。
func TestP1CICSV(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set; skipping live DB test")
	}
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := &Service{pg: repo}
	ctx := context.Background()
	traceID := "p1-csv-test"

	typeID := "p1_csv_host_" + time.Now().Format("150405")
	mt := &model.CIModelType{
		TenantID: "default",
		Name:     typeID,
		Category: model.ModelCategoryInfra,
		Schema: map[string]model.AttrDef{
			"ip":   {Type: "string", Required: true},
			"role": {Type: "string"},
		},
	}
	if err := repo.CreateCIModelType(ctx, mt); err != nil {
		t.Fatalf("create model type: %v", err)
	}
	defer func() {
		cis, _ := repo.ListCI(ctx, "default", mt.ID)
		for _, c := range cis {
			_ = repo.DeleteCI(ctx, c.ID, "default", traceID)
		}
		_ = repo.DeleteCIModelType(ctx, mt.ID, "default")
	}()

	ciA := &model.CIModel{TypeID: mt.ID, TenantID: "default", Name: "host-a", Status: model.CIStatusActive, Attributes: map[string]any{"ip": "10.0.0.1", "role": "web"}, Channel: model.WeightHuman}
	ciB := &model.CIModel{TypeID: mt.ID, TenantID: "default", Name: "host-b", Status: model.CIStatusActive, Attributes: map[string]any{"ip": "10.0.0.2", "role": "db"}, Channel: model.WeightHuman}
	if err := repo.CreateCI(ctx, ciA, traceID); err != nil {
		t.Fatalf("create ci a: %v", err)
	}
	if err := repo.CreateCI(ctx, ciB, traceID); err != nil {
		t.Fatalf("create ci b: %v", err)
	}

	// ---- 导出 ----
	ereq := httptest.NewRequest("GET", "/api/v1/cmdb/ci/export?type_id="+mt.ID, nil)
	erec := httptest.NewRecorder()
	svc.handleExportCI(erec, ereq)
	if erec.Code != http.StatusOK {
		t.Fatalf("export status %d body=%s", erec.Code, erec.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(erec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse export csv: %v", err)
	}
	if len(rows) < 3 {
		t.Fatalf("export expected >=3 rows, got %d", len(rows))
	}
	header := rows[0]
	need := []string{"id", "name", "description", "status", "ip", "role"}
	for _, col := range need {
		if indexOf(header, col) < 0 {
			t.Fatalf("export header missing column %s; got %v", col, header)
		}
	}
	if len(rows)-1 != 2 {
		t.Fatalf("export expected 2 data rows, got %d", len(rows)-1)
	}

	// ---- 构造导入 CSV：更新 host-a(按 id upsert) + 新增 host-c ----
	idCol := indexOf(header, "id")
	firstID := rows[1][idCol]
	var sb strings.Builder
	sb.WriteString(strings.Join(header, ",") + "\n")
	sb.WriteString(strings.Join([]string{firstID, "host-a", "", "active", "10.0.0.1", "web-edited"}, ",") + "\n")
	sb.WriteString(strings.Join([]string{"", "host-c", "", "active", "10.0.0.3", "db-new"}, ",") + "\n")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "ci.csv")
	fw.Write([]byte(sb.String()))
	mw.Close()

	ireq := httptest.NewRequest("POST", "/api/v1/cmdb/ci/import?type_id="+mt.ID, &body)
	ireq.Header.Set("Content-Type", mw.FormDataContentType())
	irec := httptest.NewRecorder()
	svc.handleImportCI(irec, ireq)
	if irec.Code != http.StatusOK {
		t.Fatalf("import status %d body=%s", irec.Code, irec.Body.String())
	}
	var result struct {
		Created int           `json:"created"`
		Updated int           `json:"updated"`
		Failed  []map[string]any `json:"failed"`
	}
	if err := json.Unmarshal(irec.Body.Bytes(), &result); err != nil {
		t.Fatalf("parse import result: %v", err)
	}
	if result.Created != 1 {
		t.Errorf("expected created=1, got %d", result.Created)
	}
	if result.Updated != 1 {
		t.Errorf("expected updated=1, got %d", result.Updated)
	}
	if len(result.Failed) != 0 {
		t.Errorf("expected 0 failed rows, got %v", result.Failed)
	}

	// ---- 验证更新生效 ----
	updated, err := repo.GetCI(ctx, firstID, "default")
	if err != nil {
		t.Fatalf("get updated ci: %v", err)
	}
	if updated.Attributes["role"] != "web-edited" {
		t.Errorf("expected role=web-edited after import, got %v", updated.Attributes["role"])
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// TestP1ArchiveRestore 在真实 Postgres 上验证实例归档/恢复与列表过滤：
//   - 归档后 status=archived，普通列表(排除归档)不再出现，归档视图仅含归档项；
//   - 恢复后 status=active，普通列表重新出现。
func TestP1ArchiveRestore(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set; skipping live DB test")
	}
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	traceID := "p1-archive-test"

	typeID := "p1_arch_host_" + time.Now().Format("150405")
	mt := &model.CIModelType{
		TenantID: "default",
		Name:     typeID,
		Category: model.ModelCategoryInfra,
		Schema:   map[string]model.AttrDef{"ip": {Type: "string"}},
	}
	if err := repo.CreateCIModelType(ctx, mt); err != nil {
		t.Fatalf("create model type: %v", err)
	}
	ci := &model.CIModel{TypeID: mt.ID, TenantID: "default", Name: "arch-host", Status: model.CIStatusActive, Attributes: map[string]any{"ip": "10.9.9.9"}, Channel: model.WeightHuman}
	if err := repo.CreateCI(ctx, ci, traceID); err != nil {
		t.Fatalf("create ci: %v", err)
	}
	defer func() {
		_ = repo.DeleteCI(ctx, ci.ID, "default", traceID)
		_ = repo.DeleteCIModelType(ctx, mt.ID, "default")
	}()

	// 普通列表应含该 CI
	normal, err := repo.ListCIFiltered(ctx, "default", mt.ID, "", "name", "desc", 0, 0)
	if err != nil {
		t.Fatalf("list normal: %v", err)
	}
	if !containsCI(normal, ci.ID) {
		t.Fatalf("active CI should appear in normal list")
	}
	// 归档视图不应含该 CI
	archived, err := repo.ListCIFiltered(ctx, "default", mt.ID, "archived", "name", "desc", 0, 0)
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if containsCI(archived, ci.ID) {
		t.Fatalf("active CI should NOT appear in archived view")
	}

	// 归档
	if err := repo.UpdateCIStatus(ctx, ci.ID, "default", model.CIStatusArchived, "archive"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, _ := repo.GetCI(ctx, ci.ID, "default")
	if got.Status != model.CIStatusArchived {
		t.Fatalf("expected archived status, got %s", got.Status)
	}
	// 归档后普通列表不再含，归档视图含
	normal2, _ := repo.ListCIFiltered(ctx, "default", mt.ID, "", "name", "desc", 0, 0)
	if containsCI(normal2, ci.ID) {
		t.Fatalf("archived CI should NOT appear in normal list")
	}
	archived2, _ := repo.ListCIFiltered(ctx, "default", mt.ID, "archived", "name", "desc", 0, 0)
	if !containsCI(archived2, ci.ID) {
		t.Fatalf("archived CI should appear in archived view")
	}

	// 恢复
	if err := repo.UpdateCIStatus(ctx, ci.ID, "default", model.CIStatusActive, "restore"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got2, _ := repo.GetCI(ctx, ci.ID, "default")
	if got2.Status != model.CIStatusActive {
		t.Fatalf("expected active status after restore, got %s", got2.Status)
	}
}

// TestP1SavedQueryDynamicTarget 在真实 Postgres 上验证保存查询（"我想查"）与
// 采集动态目标解析：保存查询命中带 resource_id 的 CI，解析为可下发采集目标集合。
func TestP1SavedQueryDynamicTarget(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set; skipping live DB test")
	}
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := &Service{pg: repo}
	ctx := context.Background()
	traceID := "p1-sq-test"

	typeID := "p1_sq_host_" + time.Now().Format("150405")
	mt := &model.CIModelType{
		TenantID: "default",
		Name:     typeID,
		Category: model.ModelCategoryInfra,
		Schema:   map[string]model.AttrDef{"ip": {Type: "string"}},
	}
	if err := repo.CreateCIModelType(ctx, mt); err != nil {
		t.Fatalf("create model type: %v", err)
	}
	ciA := &model.CIModel{TypeID: mt.ID, TenantID: "default", Name: "sq-host-a", Status: model.CIStatusActive, Attributes: map[string]any{"ip": "10.8.0.1", "region": "cn-east"}, Channel: model.WeightHuman}
	ciB := &model.CIModel{TypeID: mt.ID, TenantID: "default", Name: "sq-host-b", Status: model.CIStatusActive, Attributes: map[string]any{"ip": "10.8.0.2", "region": "cn-east"}, Channel: model.WeightHuman}
	ciC := &model.CIModel{TypeID: mt.ID, TenantID: "default", Name: "sq-host-c", Status: model.CIStatusActive, Attributes: map[string]any{"ip": "10.8.0.3", "region": "cn-west"}, Channel: model.WeightHuman}
	for _, c := range []*model.CIModel{ciA, ciB, ciC} {
		if err := repo.CreateCI(ctx, c, traceID); err != nil {
			t.Fatalf("create ci: %v", err)
		}
	}
	// 给 A、B 打上 resource_id（动态目标解析优先用 resource_id）
	ciA.Attributes["resource_id"] = "aliyun:ecs:i-a"
	ciB.Attributes["resource_id"] = "aliyun:ecs:i-b"
	if err := repo.UpdateCI(ctx, ciA, model.WeightHuman, traceID); err != nil {
		t.Fatalf("set resource_id a: %v", err)
	}
	if err := repo.UpdateCI(ctx, ciB, model.WeightHuman, traceID); err != nil {
		t.Fatalf("set resource_id b: %v", err)
	}
	defer func() {
		for _, c := range []*model.CIModel{ciA, ciB, ciC} {
			_ = repo.DeleteCI(ctx, c.ID, "default", traceID)
		}
		_ = repo.DeleteCIModelType(ctx, mt.ID, "default")
	}()

	// 保存查询：region=cn-east
	sq := &model.SavedQuery{
		TenantID: "default",
		Name:     "华东主机",
		TypeID:   mt.ID,
		Query: map[string]any{
			"attrs": map[string]any{"region": "cn-east"},
		},
	}
	if err := repo.CreateSavedQuery(ctx, sq); err != nil {
		t.Fatalf("create saved query: %v", err)
	}
	defer func() { _ = repo.DeleteSavedQuery(ctx, sq.ID, "default") }()

	// 解析命中 CI：应含 A、B，不含 C
	cis, err := svc.resolveSavedQueryCIs(ctx, "default", sq)
	if err != nil {
		t.Fatalf("resolve saved query cis: %v", err)
	}
	if len(cis) != 2 {
		t.Fatalf("expected 2 matched CIs, got %d", len(cis))
	}

	// 动态目标解析：应返回 A、B 的 resource_id
	targets, err := svc.ResolveSavedQueryTargetIDs(ctx, "default", sq.ID)
	if err != nil {
		t.Fatalf("resolve target ids: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 target ids, got %d (%v)", len(targets), targets)
	}
	if !containsString(targets, "aliyun:ecs:i-a") || !containsString(targets, "aliyun:ecs:i-b") {
		t.Fatalf("expected resource_ids aliyun:ecs:i-a/i-b, got %v", targets)
	}
}

func containsCI(cis []*model.CIModel, id string) bool {
	for _, c := range cis {
		if c.ID == id {
			return true
		}
	}
	return false
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestP1CreateModelTypeHandler 验证 #3 修复：新建模型（ID/ParentID 均为空）不再被
// 「模型不能继承自身」误判拦截；且列表接口 items 永不为 null（页面不再空白）。
func TestP1CreateModelTypeHandler(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set; skipping live DB test")
	}
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := &Service{pg: repo}

	// 通过 handler 路径新建模型（与前端行为一致：无 parent_id，schema 由前端拼装）
	body := `{"name":"__p1_handler_model","description":"handler test","icon":"🖥️","category":"基础设施资源","show_in_menu":true,"schema":{"ip":{"type":"string","required":true}}}`
	req := httptest.NewRequest("POST", "/api/v1/cmdb/model-type", strings.NewReader(body))
	req = req.WithContext(contextWithTenant(context.Background(), "default"))
	rec := httptest.NewRecorder()
	svc.handleCreateModelType(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create model status %d body=%s", rec.Code, rec.Body.String())
	}
	var created model.CIModelType
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("parse created: %v", err)
	}
	defer func() {
		cis, _ := repo.ListCI(ctxAll(), "default", created.ID)
		for _, c := range cis {
			_ = repo.DeleteCI(context.Background(), c.ID, "default", "p1-cleanup")
		}
		_ = repo.DeleteCIModelType(context.Background(), created.ID, "default")
	}()

	// 列表接口：items 必须为数组（不能是 null），且包含刚创建的模型
	lreq := httptest.NewRequest("GET", "/api/v1/cmdb/model-type?limit=1000", nil)
	lreq = lreq.WithContext(contextWithTenant(context.Background(), "default"))
	lrec := httptest.NewRecorder()
	svc.handleListModelTypes(lrec, lreq)
	var list struct {
		Items []*model.CIModelType `json:"items"`
	}
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if list.Items == nil {
		t.Fatalf("items is null (should be []), page would render empty")
	}
	found := false
	for _, m := range list.Items {
		if m.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created model not in list")
	}
}

// TestP1ModelCategories 验证 #2：内置 5 分类种子齐全 + 新增/删除自定义分类。
func TestP1ModelCategories(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set; skipping live DB test")
	}
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()

	cats, err := repo.ListModelCategories(ctx, "default")
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(cats) < 5 {
		t.Fatalf("expected >=5 builtin categories, got %d", len(cats))
	}
	for _, want := range []string{"业务资源", "应用资源", "中间件资源", "数据库资源", "基础设施资源"} {
		found := false
		for _, c := range cats {
			if c.Name == want {
				found = true
				if !c.Builtin {
					t.Errorf("category %q should be builtin", want)
				}
			}
		}
		if !found {
			t.Errorf("builtin category %q missing", want)
		}
	}

	// 新增自定义分类 + 删除
	c := &model.ModelCategory{TenantID: "default", Name: "__p1_test_cat"}
	if err := repo.CreateModelCategory(ctx, c); err != nil {
		t.Fatalf("create custom category: %v", err)
	}
	if err := repo.DeleteModelCategory(ctx, c.ID, "default"); err != nil {
		t.Fatalf("delete custom category: %v", err)
	}
}

// contextWithTenant 注入 tenant 上下文（key 与真实中间件 tenantFromCtx 一致）。
func contextWithTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, "tenant_id", tenant)
}

// ctxAll 返回带 default 租户的上下文。
func ctxAll() context.Context {
	return contextWithTenant(context.Background(), "default")
}
