package report

import (
	"context"
	"strings"
	"testing"
)

// —— 测试夹具 ——

// stubResolver 不连库，只记录最后一次收到的查询，用于验证 Registry 的编排逻辑。
type stubResolver struct {
	rows [][]any
	last *Query
}

func (s *stubResolver) Resolve(ctx context.Context, tenantID string, q *Query, ds *Dataset) (*Result, error) {
	s.last = q
	return &Result{Columns: q.Columns(ds), Rows: s.rows}, nil
}

func testDataset(res Resolver) *Dataset {
	return &Dataset{
		Name:      "test.tickets",
		Label:     "测试工单",
		Module:    "test",
		TimeField: "created_at",
		Dimensions: []Dimension{
			{Name: "created_at", Label: "创建时间", Type: FieldTime, Filterable: true, Groupable: true},
			{Name: "status", Label: "状态", Type: FieldString, Filterable: true, Groupable: true},
			{Name: "title", Label: "标题", Type: FieldString, Filterable: true, Groupable: false},
			{Name: "risk_level", Label: "风险等级", Type: FieldString, Filterable: true, Groupable: true},
		},
		Metrics: []Metric{
			{Name: "count", Label: "工单数", Agg: AggCount, Unit: "个"},
			{Name: "distinct_creator", Label: "创建人数", Agg: AggCountDistinct},
		},
		Resolver: res,
	}
}

func sqlDataset() (*Dataset, *SQLResolver) {
	r := NewSQLResolver(nil, SQLSpec{
		Table: "itsm_tickets",
		MetricExprs: map[string]string{
			"distinct_creator": "created_by",
		},
	})
	ds := testDataset(r)
	return ds, r
}

// —— 注册表 ——

func TestRegistryRegisterAndQuery(t *testing.T) {
	reg := NewRegistry()
	stub := &stubResolver{rows: [][]any{{"open", 3}}}
	ds := testDataset(stub)

	if err := reg.Register(ds); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := reg.Register(testDataset(stub)); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	if !reg.Exists("test.tickets") {
		t.Fatal("dataset should exist")
	}
	if names := reg.Names(); len(names) != 1 || names[0] != "test.tickets" {
		t.Fatalf("unexpected names: %v", names)
	}
	if got := reg.List(); len(got) != 1 {
		t.Fatalf("expected 1 dataset, got %d", len(got))
	}

	res, err := reg.Query(context.Background(), "t1", &Query{
		Dataset:    "test.tickets",
		Dimensions: []string{"status"},
		Metrics:    []string{"count"},
	})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if res.RowCount != 1 {
		t.Fatalf("expected rowCount 1, got %d", res.RowCount)
	}
	if len(res.Columns) != 2 || res.Columns[0].Role != RoleDimension || res.Columns[1].Role != RoleMetric {
		t.Fatalf("unexpected columns: %+v", res.Columns)
	}
	// 指标列应带上单位与小数位信息，供前端格式化
	if res.Columns[1].Unit != "个" {
		t.Fatalf("expected metric unit to be carried, got %q", res.Columns[1].Unit)
	}
}

func TestRegistryUnknownDataset(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Query(context.Background(), "t1", &Query{Dataset: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown dataset")
	}
	if _, ok := err.(*DatasetNotFound); !ok {
		t.Fatalf("expected DatasetNotFound, got %T", err)
	}
}

// —— 白名单：非法字段一律拒绝(注入防护第一道闸) ——

func TestQueryRejectsUnknownFields(t *testing.T) {
	ds := testDataset(&stubResolver{})
	cases := []struct {
		name string
		q    Query
	}{
		{"unknown dimension", Query{Dimensions: []string{"password"}}},
		{"sql in dimension", Query{Dimensions: []string{"status; DROP TABLE itsm_tickets"}}},
		{"unknown metric", Query{Metrics: []string{"secret_sum"}}},
		{"non-groupable dimension", Query{Dimensions: []string{"title"}}},
		{"unknown filter field", Query{Filters: []Filter{{Field: "1=1", Op: OpEq, Value: "x"}}}},
		{"bad operator", Query{Filters: []Filter{{Field: "status", Op: FilterOp("union"), Value: "x"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := c.q
			if err := q.Normalize(ds); err == nil {
				t.Fatalf("expected %s to be rejected", c.name)
			}
		})
	}
}

func TestQueryNormalizeDefaults(t *testing.T) {
	ds := testDataset(&stubResolver{})
	q := Query{Dimensions: []string{"status", "status"}}
	if err := q.Normalize(ds); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if len(q.Dimensions) != 1 {
		t.Fatalf("expected dedup to 1 dimension, got %v", q.Dimensions)
	}
	if len(q.Metrics) != 1 || q.Metrics[0] != "count" {
		t.Fatalf("expected default metric, got %v", q.Metrics)
	}
	if q.Limit <= 0 {
		t.Fatalf("expected default limit, got %d", q.Limit)
	}
}

// —— SQL 构建：参数化 + 强制租户 ——

func TestBuildSQLTenantIsolationAndParams(t *testing.T) {
	ds, r := sqlDataset()
	q := Query{
		Dimensions: []string{"status"},
		Metrics:    []string{"count"},
		Filters: []Filter{
			{Field: "risk_level", Op: OpIn, Value: []any{"high", "critical"}},
		},
	}
	if err := q.Normalize(ds); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	sqlText, args, err := r.BuildSQL("tenant-a", &q, ds)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if !strings.Contains(sqlText, "tenant_id = $1") {
		t.Fatalf("tenant filter must be injected, got: %s", sqlText)
	}
	if args[0] != "tenant-a" {
		t.Fatalf("expected tenant arg first, got %v", args[0])
	}
	// 过滤值必须走占位符，绝不出现在 SQL 文本里
	if strings.Contains(sqlText, "critical") {
		t.Fatalf("filter values must not be inlined: %s", sqlText)
	}
	if !strings.Contains(sqlText, "GROUP BY 1") {
		t.Fatalf("expected ordinal group by, got: %s", sqlText)
	}
	if !strings.Contains(sqlText, "COUNT(*) AS count") {
		t.Fatalf("expected count aggregate, got: %s", sqlText)
	}
	if !strings.HasSuffix(sqlText, "LIMIT $4") {
		t.Fatalf("expected parameterized limit, got: %s", sqlText)
	}
}

func TestBuildSQLInjectionAttemptsAreParameterized(t *testing.T) {
	ds, r := sqlDataset()
	payload := "' OR 1=1 --"
	q := Query{
		Metrics: []string{"count"},
		Filters: []Filter{
			{Field: "status", Op: OpEq, Value: payload},
			{Field: "title", Op: OpContains, Value: "x'; DROP TABLE itsm_tickets; --"},
		},
	}
	if err := q.Normalize(ds); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	sqlText, args, err := r.BuildSQL("t1", &q, ds)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if strings.Contains(sqlText, "OR 1=1") || strings.Contains(strings.ToUpper(sqlText), "DROP TABLE") {
		t.Fatalf("payload leaked into SQL text: %s", sqlText)
	}
	found := false
	for _, a := range args {
		if a == payload {
			found = true
		}
	}
	if !found {
		t.Fatalf("payload should be passed as an argument, got args=%v", args)
	}
}

func TestBuildSQLGranularityWhitelist(t *testing.T) {
	ds, r := sqlDataset()
	q := Query{Metrics: []string{"count"}, Granularity: GranDay}
	if err := q.Normalize(ds); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	sqlText, _, err := r.BuildSQL("t1", &q, ds)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if !strings.Contains(sqlText, "date_trunc('day', created_at) AS time_bucket") {
		t.Fatalf("expected date_trunc bucket, got: %s", sqlText)
	}

	// 非法粒度必须在归一化阶段就被拒绝
	bad := Query{Metrics: []string{"count"}, Granularity: Granularity("day'); DROP TABLE x --")}
	if err := bad.Normalize(ds); err == nil {
		t.Fatal("expected invalid granularity to be rejected")
	}
}

func TestBuildSQLOrderByMustBeSelected(t *testing.T) {
	ds, r := sqlDataset()
	q := Query{
		Dimensions: []string{"status"},
		Metrics:    []string{"count"},
		OrderBy:    []OrderBy{{Field: "count", Desc: true}},
	}
	if err := q.Normalize(ds); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	sqlText, _, err := r.BuildSQL("t1", &q, ds)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	// ORDER BY 用序号而非标识符，杜绝标识符注入
	if !strings.Contains(sqlText, "ORDER BY 2 DESC") {
		t.Fatalf("expected ordinal order by, got: %s", sqlText)
	}

	// 排序字段必须在已选维度/指标里，归一化阶段就该拒绝
	q2 := Query{Metrics: []string{"count"}, OrderBy: []OrderBy{{Field: "risk_level"}}}
	if err := q2.Normalize(ds); err == nil {
		t.Fatal("expected order by on unselected field to be rejected at normalize")
	}
	// 直接绕过归一化时，构建器仍要兜底拦截
	q3 := Query{Dataset: ds.Name, Metrics: []string{"count"}, Limit: 100, OrderBy: []OrderBy{{Field: "risk_level"}}}
	if _, _, err := r.BuildSQL("t1", &q3, ds); err == nil {
		t.Fatal("expected builder to reject order by on unselected field")
	}
}

func TestBuildSQLCountDistinctRequiresExpr(t *testing.T) {
	ds, r := sqlDataset()
	q := Query{Metrics: []string{"distinct_creator"}}
	if err := q.Normalize(ds); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	sqlText, _, err := r.BuildSQL("t1", &q, ds)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if !strings.Contains(sqlText, "COUNT(DISTINCT created_by)") {
		t.Fatalf("expected count distinct expr, got: %s", sqlText)
	}
}

func TestBuildSQLNoTenantColumn(t *testing.T) {
	r := NewSQLResolver(nil, SQLSpec{Table: "global_dict", TenantColumn: SQLNoTenant})
	ds := testDataset(r)
	q := Query{Metrics: []string{"count"}}
	if err := q.Normalize(ds); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	sqlText, _, err := r.BuildSQL("t1", &q, ds)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if strings.Contains(sqlText, "tenant_id") {
		t.Fatalf("global dataset should not filter tenant, got: %s", sqlText)
	}
}

// —— 看板校验与持久化 ——

func TestDashboardValidate(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(testDataset(&stubResolver{})); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	okWidget := Widget{
		ID:     "w_status",
		Title:  "状态分布",
		Type:   "pie",
		Query:  Query{Dataset: "test.tickets", Dimensions: []string{"status"}, Metrics: []string{"count"}},
		Layout: Layout{W: 6, H: 4},
	}

	// 空看板是合法草稿态
	empty := &Dashboard{Name: "空看板"}
	if err := empty.Validate(reg); err != nil {
		t.Fatalf("empty dashboard should be valid: %v", err)
	}

	d := &Dashboard{Name: "运营看板", Widgets: []Widget{okWidget}}
	if err := d.Validate(reg); err != nil {
		t.Fatalf("valid dashboard rejected: %v", err)
	}

	// 无名看板
	if err := (&Dashboard{Widgets: []Widget{okWidget}}).Validate(reg); err == nil {
		t.Fatal("expected unnamed dashboard to be rejected")
	}
	// 重复 widget id
	dup := &Dashboard{Name: "x", Widgets: []Widget{okWidget, okWidget}}
	if err := dup.Validate(reg); err == nil {
		t.Fatal("expected duplicate widget id to be rejected")
	}
	// 非法 widget id
	badID := okWidget
	badID.ID = "w status/../etc"
	if err := (&Dashboard{Name: "x", Widgets: []Widget{badID}}).Validate(reg); err == nil {
		t.Fatal("expected invalid widget id to be rejected")
	}
	// widget 引用未注册数据集
	badDS := okWidget
	badDS.Query = Query{Dataset: "ghost.table", Metrics: []string{"count"}}
	if err := (&Dashboard{Name: "x", Widgets: []Widget{badDS}}).Validate(reg); err == nil {
		t.Fatal("expected unknown dataset in widget to be rejected")
	}
	// widget 查询引用非法字段
	badField := okWidget
	badField.Query = Query{Dataset: "test.tickets", Dimensions: []string{"salary"}}
	if err := (&Dashboard{Name: "x", Widgets: []Widget{badField}}).Validate(reg); err == nil {
		t.Fatal("expected unknown dimension in widget to be rejected")
	}
	// 非法布局
	badLayout := okWidget
	badLayout.Layout = Layout{W: 0, H: 0}
	if err := (&Dashboard{Name: "x", Widgets: []Widget{badLayout}}).Validate(reg); err == nil {
		t.Fatal("expected invalid layout to be rejected")
	}
}

func TestMemoryDashboardStoreCRUDAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryDashboardStore()

	d := &Dashboard{TenantID: "t1", Name: "看板A"}
	if err := st.Create(ctx, d); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if d.ID == "" || d.CreatedAt.IsZero() {
		t.Fatal("create should assign id and timestamps")
	}

	got, err := st.Get(ctx, d.ID, "t1")
	if err != nil || got == nil {
		t.Fatalf("get failed: %v (got=%v)", err, got)
	}
	// 跨租户读取必须为空
	other, err := st.Get(ctx, d.ID, "t2")
	if err != nil || other != nil {
		t.Fatalf("cross-tenant read must be blocked, got %v", other)
	}

	list, _ := st.List(ctx, "t1")
	if len(list) != 1 {
		t.Fatalf("expected 1 dashboard for t1, got %d", len(list))
	}
	if l2, _ := st.List(ctx, "t2"); len(l2) != 0 {
		t.Fatalf("expected 0 dashboards for t2, got %d", len(l2))
	}

	upd := &Dashboard{ID: d.ID, TenantID: "t1", Name: "看板A改", Widgets: []Widget{{
		ID: "w1", Title: "t", Type: "table",
		Query:  Query{Dataset: "test.tickets", Metrics: []string{"count"}},
		Layout: Layout{W: 6, H: 4},
	}}}
	if err := st.Update(ctx, upd); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	after, _ := st.Get(ctx, d.ID, "t1")
	if after.Name != "看板A改" || len(after.Widgets) != 1 {
		t.Fatalf("update not applied: %+v", after)
	}
	if after.CreatedAt.IsZero() {
		t.Fatal("update must preserve createdAt")
	}

	// 跨租户删除不生效
	if err := st.Delete(ctx, d.ID, "t2"); err != nil {
		t.Fatalf("delete returned error: %v", err)
	}
	if still, _ := st.Get(ctx, d.ID, "t1"); still == nil {
		t.Fatal("cross-tenant delete must not remove the dashboard")
	}
	if err := st.Delete(ctx, d.ID, "t1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if gone, _ := st.Get(ctx, d.ID, "t1"); gone != nil {
		t.Fatal("dashboard should be deleted")
	}
}

// —— 数据集自身声明合法性 ——

func TestDatasetValidateRejectsBadFieldNames(t *testing.T) {
	bad := &Dataset{
		Name:       "test.bad",
		Label:      "坏数据集",
		Module:     "test",
		Dimensions: []Dimension{{Name: "DROP TABLE x", Label: "x", Type: FieldString}},
		Metrics:    []Metric{{Name: "count", Label: "数", Agg: AggCount}},
		Resolver:   &stubResolver{},
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected illegal dimension name to be rejected")
	}

	noResolver := testDataset(nil)
	if err := noResolver.Validate(); err == nil {
		t.Fatal("expected dataset without resolver to be rejected")
	}
}
