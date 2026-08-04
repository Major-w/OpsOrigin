package form

import (
	"context"
	"errors"
	"testing"
)

// --- 多规则显隐 ---

func TestFieldVisibleRulesAnd(t *testing.T) {
	f := FieldDef{
		Key: "rollback_plan", Label: "回滚方案", Type: TypeTextarea,
		VisibleWhenRules: []Condition{
			{Field: "risk_level", Op: OpIn, Value: "high,critical"},
			{Field: "need_approval", Op: OpIsTrue},
		},
		VisibleLogic: LogicAnd,
	}
	cases := []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{"两条都命中", map[string]any{"risk_level": "critical", "need_approval": true}, true},
		{"风险命中但未勾选审批", map[string]any{"risk_level": "critical", "need_approval": false}, false},
		{"风险不命中", map[string]any{"risk_level": "low", "need_approval": true}, false},
		{"字段缺失", map[string]any{}, false},
	}
	for _, c := range cases {
		if got := FieldVisible(f, c.values); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestFieldVisibleRulesOr(t *testing.T) {
	f := FieldDef{
		Key: "extra", Type: TypeText,
		VisibleWhenRules: []Condition{
			{Field: "env", Op: OpEq, Value: "prod"},
			{Field: "impact_users", Op: OpGte, Value: "1000"},
		},
		VisibleLogic: LogicOr,
	}
	if !FieldVisible(f, map[string]any{"env": "prod", "impact_users": 1.0}) {
		t.Error("env 命中即应显示")
	}
	if !FieldVisible(f, map[string]any{"env": "dev", "impact_users": 5000.0}) {
		t.Error("影响人数命中即应显示")
	}
	if FieldVisible(f, map[string]any{"env": "dev", "impact_users": 10.0}) {
		t.Error("两条都不命中应隐藏")
	}
}

func TestEvalConditionOperators(t *testing.T) {
	values := map[string]any{
		"env":     "prod",
		"tags":    []any{"db", "core"},
		"count":   12.0,
		"enabled": true,
		"blank":   "   ",
	}
	cases := []struct {
		name string
		c    Condition
		want bool
	}{
		{"eq 命中", Condition{"env", OpEq, "prod"}, true},
		{"eq 不命中", Condition{"env", OpEq, "dev"}, false},
		{"neq", Condition{"env", OpNeq, "dev"}, true},
		{"in", Condition{"env", OpIn, "dev, prod"}, true},
		{"not_in", Condition{"env", OpNotIn, "dev,test"}, true},
		{"contains 字符串", Condition{"env", OpContains, "pro"}, true},
		{"多选数组 eq 任一命中", Condition{"tags", OpEq, "core"}, true},
		{"多选数组 in", Condition{"tags", OpIn, "core,web"}, true},
		{"多选数组 not_in", Condition{"tags", OpNotIn, "web"}, true},
		{"gt", Condition{"count", OpGt, "10"}, true},
		{"lte 不命中", Condition{"count", OpLte, "10"}, false},
		{"is_true", Condition{"enabled", OpIsTrue, ""}, true},
		{"is_false", Condition{"enabled", OpIsFalse, ""}, false},
		{"empty 空白字符串", Condition{"blank", OpEmpty, ""}, true},
		{"not_empty", Condition{"env", OpNotEmpty, ""}, true},
		{"缺失字段 empty", Condition{"missing", OpEmpty, ""}, true},
		{"缺失字段 eq", Condition{"missing", OpEq, "x"}, false},
		{"缺失字段 neq 视为成立", Condition{"missing", OpNeq, "x"}, true},
		{"未知运算符不隐藏", Condition{"env", "weird_op", "x"}, true},
		{"空字段名不隐藏", Condition{"", OpEq, "x"}, true},
	}
	for _, tc := range cases {
		if got := evalCondition(tc.c, values); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// --- 旧版单条表达式向后兼容 ---

func TestParseVisibleWhenBackwardCompatible(t *testing.T) {
	if rules := ParseVisibleWhen(""); rules != nil {
		t.Error("空表达式应返回 nil")
	}
	if rules := ParseVisibleWhen("no_operator_here"); rules != nil {
		t.Error("无运算符应返回 nil")
	}
	rules := ParseVisibleWhen(" risk_level == critical ")
	if len(rules) != 1 || rules[0].Field != "risk_level" || rules[0].Op != OpEq || rules[0].Value != "critical" {
		t.Fatalf("== 解析错误: %+v", rules)
	}
	rules = ParseVisibleWhen("env!=dev")
	if len(rules) != 1 || rules[0].Op != OpNeq || rules[0].Value != "dev" {
		t.Fatalf("!= 解析错误: %+v", rules)
	}

	// 老 schema 走 FieldVisible 行为不变
	old := FieldDef{Key: "plan", VisibleWhen: "risk_level==critical"}
	if !FieldVisible(old, map[string]any{"risk_level": "critical"}) {
		t.Error("旧表达式命中应显示")
	}
	if FieldVisible(old, map[string]any{"risk_level": "low"}) {
		t.Error("旧表达式不命中应隐藏")
	}
}

func TestVisibleRulesTakePrecedenceOverLegacy(t *testing.T) {
	f := FieldDef{
		Key:              "plan",
		VisibleWhen:      "env==never_match", // 旧表达式：不该生效
		VisibleWhenRules: []Condition{{Field: "env", Op: OpEq, Value: "prod"}},
	}
	if !FieldVisible(f, map[string]any{"env": "prod"}) {
		t.Error("有 rules 时应以 rules 为准")
	}
}

func TestHiddenRequiredFieldSkipped(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "risk_level", Type: TypeText},
		{Key: "rollback", Type: TypeTextarea, Required: true,
			VisibleWhenRules: []Condition{{Field: "risk_level", Op: OpEq, Value: "critical"}}},
	}}
	if err := ValidateSchema(schema, map[string]any{"risk_level": "low"}); err != nil {
		t.Fatalf("隐藏的必填字段不应报错: %v", err)
	}
	if err := ValidateSchema(schema, map[string]any{"risk_level": "critical"}); err == nil {
		t.Fatal("显示的必填字段缺失应报错")
	}
}

func TestRequiredRejectsEmptyString(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{{Key: "reason", Type: TypeText, Required: true}}}
	if err := ValidateSchema(schema, map[string]any{"reason": ""}); err == nil {
		t.Fatal("空字符串应视为未填写")
	}
}

// --- source 动态选项 ---

type fakeStore struct{ schema *FormSchema }

func (f *fakeStore) GetFormSchema(_ context.Context, _, _ string) (*FormSchema, error) {
	return f.schema, nil
}
func (f *fakeStore) UpsertFormSchema(_ context.Context, _, _ string, s FormSchema) error {
	f.schema = &s
	return nil
}
func (f *fakeStore) ListFormScopes(_ context.Context, _ string) ([]string, error) { return nil, nil }

type fakeResolver struct {
	opts  []FieldOption
	err   error
	calls int
	// tree: parent → 子选项，用于 ResolveTree（cascader 级联）
	tree      map[string][]FieldOption
	treeCalls int
}

func (r *fakeResolver) ResolveTree(_ context.Context, _, source, parent string) ([]FieldOption, error) {
	r.treeCalls++
	if r.err != nil {
		return nil, r.err
	}
	if source != "cmdb:types" {
		return nil, errors.New("unsupported source")
	}
	return r.tree[parent], nil
}

func (r *fakeResolver) Resolve(_ context.Context, _, source string) ([]FieldOption, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	if source != "cmdb:host" {
		return nil, errors.New("unsupported source")
	}
	return r.opts, nil
}

func TestValidateValuesWithSourceResolver(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "host", Label: "目标主机", Type: TypeSelect, Source: "cmdb:host"},
	}}
	store := &fakeStore{schema: schema}
	eng := NewEngine(store)
	ctx := context.Background()

	// 未注入 resolver：不限制取值范围
	if err := eng.ValidateValues(ctx, "t1", schema, map[string]any{"host": "any-id"}); err != nil {
		t.Fatalf("无 resolver 应放行: %v", err)
	}

	res := &fakeResolver{opts: []FieldOption{{Value: "ci-1", Label: "web-01"}, {Value: "ci-2", Label: "web-02"}}}
	eng.SetSourceResolver(res)
	if eng.SourceResolver() == nil {
		t.Fatal("SourceResolver 应可取回")
	}

	if err := eng.ValidateValues(ctx, "t1", schema, map[string]any{"host": "ci-1"}); err != nil {
		t.Fatalf("合法取值应通过: %v", err)
	}
	if err := eng.ValidateValues(ctx, "t1", schema, map[string]any{"host": "ci-999"}); err == nil {
		t.Fatal("非法取值应报错")
	}
	if res.calls == 0 {
		t.Fatal("resolver 未被调用")
	}
}

func TestValidateValuesResolverErrorIsLenient(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "host", Type: TypeSelect, Source: "cmdb:host"},
	}}
	eng := NewEngine(&fakeStore{schema: schema})
	eng.SetSourceResolver(&fakeResolver{err: errors.New("cmdb down")})
	if err := eng.ValidateValues(context.Background(), "t1", schema, map[string]any{"host": "whatever"}); err != nil {
		t.Fatalf("上游故障应宽松放行，实际: %v", err)
	}
}

func TestStaticOptionsWinOverSource(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "env", Type: TypeSelect, Source: "cmdb:host",
			Options: []FieldOption{{Value: "prod", Label: "生产"}}},
	}}
	res := &fakeResolver{opts: []FieldOption{{Value: "ci-1"}}}
	eng := NewEngine(&fakeStore{schema: schema})
	eng.SetSourceResolver(res)
	if err := eng.ValidateValues(context.Background(), "t1", schema, map[string]any{"env": "prod"}); err != nil {
		t.Fatalf("静态 options 应优先: %v", err)
	}
	if res.calls != 0 {
		t.Fatal("有静态 options 时不应调用 resolver")
	}
}

func TestMultiSelectWithSource(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "hosts", Type: TypeMultiSelect, Source: "cmdb:host"},
	}}
	eng := NewEngine(&fakeStore{schema: schema})
	eng.SetSourceResolver(&fakeResolver{opts: []FieldOption{{Value: "ci-1"}, {Value: "ci-2"}}})
	ctx := context.Background()
	if err := eng.ValidateValues(ctx, "t1", schema, map[string]any{"hosts": []any{"ci-1", "ci-2"}}); err != nil {
		t.Fatalf("合法多选应通过: %v", err)
	}
	if err := eng.ValidateValues(ctx, "t1", schema, map[string]any{"hosts": []any{"ci-1", "ci-x"}}); err == nil {
		t.Fatal("含非法项应报错")
	}
}

func TestEngineValidateNoSchemaPasses(t *testing.T) {
	eng := NewEngine(&fakeStore{schema: nil})
	if err := eng.Validate(context.Background(), "t1", "ticket:default", map[string]any{"x": 1}); err != nil {
		t.Fatalf("无 schema 应放行: %v", err)
	}
}

// ---- cascader 级联 ----

func cascaderTree() []FieldOption {
	return []FieldOption{
		{Value: "biz", Label: "业务系统", Children: []FieldOption{
			{Value: "app", Label: "应用", Children: []FieldOption{
				{Value: "mysql", Label: "MySQL 实例"},
			}},
			{Value: "gateway", Label: "网关"},
		}},
		{Value: "infra", Label: "基础设施"},
	}
}

func TestCascaderPathValidation(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "target", Label: "纳管对象", Type: TypeCascader, Options: cascaderTree()},
	}}
	cases := []struct {
		name    string
		value   any
		wantErr bool
	}{
		{"完整路径", []any{"biz", "app", "mysql"}, false},
		{"选到中间层级也允许", []any{"biz", "app"}, false},
		{"仅一级", []any{"infra"}, false},
		{"string 切片同样接受", []string{"biz", "gateway"}, false},
		{"旧版斜杠文本兼容", "biz/app/mysql", false},
		{"层级顺序错乱", []any{"app", "biz"}, true},
		{"越过树深", []any{"infra", "whatever"}, true},
		{"不存在的取值", []any{"nope"}, true},
		{"非字符串元素", []any{"biz", 42}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSchema(schema, map[string]any{"target": tc.value})
			if tc.wantErr && err == nil {
				t.Fatalf("期望报错，实际通过（value=%v）", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望通过，实际报错: %v", err)
			}
		})
	}
}

func TestCascaderEmptyArrayCountsAsUnfilled(t *testing.T) {
	required := &FormSchema{Fields: []FieldDef{
		{Key: "target", Label: "纳管对象", Type: TypeCascader, Options: cascaderTree(), Required: true},
	}}
	if err := ValidateSchema(required, map[string]any{"target": []any{}}); err == nil {
		t.Fatal("必填级联传空数组应视为未填并报错")
	}
	optional := &FormSchema{Fields: []FieldDef{
		{Key: "target", Label: "纳管对象", Type: TypeCascader, Options: cascaderTree()},
	}}
	if err := ValidateSchema(optional, map[string]any{"target": []any{}}); err != nil {
		t.Fatalf("非必填级联传空数组应放行: %v", err)
	}
}

// 同一处修复也覆盖多选：此前 v == "" 判空对空数组失效，必填校验会被绕过
func TestRequiredMultiSelectRejectsEmptyArray(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "hosts", Label: "主机", Type: TypeMultiSelect, Required: true,
			Options: []FieldOption{{Value: "ci-1"}}},
	}}
	if err := ValidateSchema(schema, map[string]any{"hosts": []any{}}); err == nil {
		t.Fatal("必填多选传空数组应报错")
	}
}

func TestCascaderNoOptionsSkipsRangeCheck(t *testing.T) {
	// 未配 options 且无 resolver：不限制取值范围，但仍走长度/正则校验
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "target", Label: "对象", Type: TypeCascader,
			Validation: &FieldValidation{MaxLen: 4}},
	}}
	if err := ValidateSchema(schema, map[string]any{"target": []any{"abc", "de"}}); err != nil {
		t.Fatalf("无选项约束时应放行: %v", err)
	}
	if err := ValidateSchema(schema, map[string]any{"target": []any{"abc", "toolongsegment"}}); err == nil {
		t.Fatal("超长片段应被 MaxLen 拦下")
	}
}

func TestResolveTreeThroughEngineResolver(t *testing.T) {
	res := &fakeResolver{tree: map[string][]FieldOption{
		"":    {{Value: "biz", Label: "业务系统"}},
		"biz": {{Value: "app", Label: "应用"}},
	}}
	var tr TreeSourceResolver = res

	roots, err := tr.ResolveTree(context.Background(), "t1", "cmdb:types", "")
	if err != nil || len(roots) != 1 || roots[0].Value != "biz" {
		t.Fatalf("根层级解析异常: %v / %+v", err, roots)
	}
	kids, err := tr.ResolveTree(context.Background(), "t1", "cmdb:types", "biz")
	if err != nil || len(kids) != 1 || kids[0].Value != "app" {
		t.Fatalf("子层级解析异常: %v / %+v", err, kids)
	}
	if _, err := tr.ResolveTree(context.Background(), "t1", "cmdb:host", ""); err == nil {
		t.Fatal("非层级来源应报错")
	}
	if res.treeCalls != 3 {
		t.Fatalf("ResolveTree 调用次数应为 3，实际 %d", res.treeCalls)
	}
}

// --- 新字段类型: datetime / duration / attachment ---

func TestDatetimeFieldValidation(t *testing.T) {
	f := FieldDef{Key: "window", Label: "变更窗口", Type: TypeDatetime}
	valid := []any{"2026-08-05 20:00", "2026-08-05T20:00:00Z", "2026-08-05T20:00:00+08:00", "2026-08-05", "2026-08-05 20:00:00"}
	for _, v := range valid {
		if err := validateFieldWith(f, v, nil); err != nil {
			t.Fatalf("valid value %v rejected: %v", v, err)
		}
	}
	invalid := []any{"2026/08/05", "20:00", "not-a-date", 123}
	for _, v := range invalid {
		if err := validateFieldWith(f, v, nil); err == nil {
			t.Fatalf("invalid value %v accepted", v)
		}
	}
}

func TestDurationFieldValidation(t *testing.T) {
	f := FieldDef{Key: "est", Label: "预估时长", Type: TypeDuration}
	valid := []any{"1h", "30m", "3d", "1h30m", "90s"}
	for _, v := range valid {
		if err := validateFieldWith(f, v, nil); err != nil {
			t.Fatalf("valid duration %v rejected: %v", v, err)
		}
	}
	invalid := []any{"h", "1x", "", "90", 30}
	for _, v := range invalid {
		if err := validateFieldWith(f, v, nil); err == nil {
			t.Fatalf("invalid duration %v accepted", v)
		}
	}
}

func TestAttachmentFieldValidation(t *testing.T) {
	f := FieldDef{Key: "files", Label: "附件", Type: TypeAttachment}
	if err := validateFieldWith(f, []any{"12", "13"}, nil); err != nil {
		t.Fatalf("valid attachment ids rejected: %v", err)
	}
	if err := validateFieldWith(f, []any{12, 13}, nil); err != nil {
		t.Fatalf("numeric ids rejected: %v", err)
	}
	if err := validateFieldWith(f, "12", nil); err == nil {
		t.Fatal("plain string accepted, want array")
	}
	if err := validateFieldWith(f, []any{map[string]any{"id": 1}}, nil); err == nil {
		t.Fatal("map element accepted, want scalar id")
	}
}
