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
