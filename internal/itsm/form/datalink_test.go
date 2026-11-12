package form

import (
	"context"
	"strings"
	"testing"
)

func testLinkSchema() *FormSchema {
	return &FormSchema{
		Version: 1,
		Scope:   "test:link",
		Fields: []FieldDef{
			{Key: "env", Label: "环境", Type: TypeSelect, Options: []FieldOption{
				{Value: "prod", Label: "生产"}, {Value: "staging", Label: "预发"},
			}},
			{Key: "impact", Label: "影响范围", Type: TypeSelect, Options: []FieldOption{
				{Value: "global", Label: "全局"}, {Value: "single", Label: "单点"},
			}},
			{Key: "ci_name", Label: "配置项", Type: TypeText},
			{Key: "affected", Label: "受影响系统", Type: TypeText},
			{Key: "level", Label: "级别", Type: TypeText},
			{Key: "window", Label: "生效时间", Type: TypeText},
		},
	}
}

// 数据联动 fixed：环境=生产 → 影响范围自动置为 global
func TestApplyDataRulesFixedSet(t *testing.T) {
	schema := testLinkSchema()
	schema.DataLinks = []DataLinkRule{{
		Name:       "生产环境即全局",
		Conditions: []Condition{{Field: "env", Op: OpEq, Value: "prod"}},
		Sets:       []DataSet{{Target: "impact", Mode: SetFixed, Value: "global"}},
	}}

	out, fired := ApplyDataRules(schema, map[string]any{"env": "prod"})
	if out["impact"] != "global" {
		t.Fatalf("impact 应被联动为 global，实际 %v", out["impact"])
	}
	if len(fired) != 1 || !strings.Contains(fired[0], "生产环境即全局") {
		t.Fatalf("fired 应含规则名，实际 %v", fired)
	}

	// 条件不命中：不写入
	out2, _ := ApplyDataRules(schema, map[string]any{"env": "staging"})
	if _, ok := out2["impact"]; ok {
		t.Fatalf("非生产环境不应联动 impact")
	}

	// 入参不被修改
	src := map[string]any{"env": "prod"}
	ApplyDataRules(schema, src)
	if _, ok := src["impact"]; ok {
		t.Fatalf("ApplyDataRules 不应修改入参 map")
	}
}

// 数据联动 copy_from：选配置项 → 受影响系统复制其名称
func TestApplyDataRulesCopyFrom(t *testing.T) {
	schema := testLinkSchema()
	schema.DataLinks = []DataLinkRule{{
		Conditions: []Condition{{Field: "ci_name", Op: OpNotEmpty}},
		Sets:       []DataSet{{Target: "affected", Mode: SetCopyFrom, Value: "ci_name"}},
	}}

	out, _ := ApplyDataRules(schema, map[string]any{"ci_name": "core-db-01"})
	if out["affected"] != "core-db-01" {
		t.Fatalf("affected 应复制 ci_name，实际 %v", out["affected"])
	}

	// 来源为空：不写入
	out2, _ := ApplyDataRules(schema, map[string]any{})
	if _, ok := out2["affected"]; ok {
		t.Fatalf("来源为空不应写入 affected")
	}
}

// 决策表：首个命中行生效；通配行兜底；未启用行跳过；"a|b" 任一命中
func TestDecisionTableEvaluation(t *testing.T) {
	schema := testLinkSchema()
	disabled := false
	schema.DecisionTables = []DecisionTable{{
		Name:          "变更分级",
		ConditionKeys: []string{"env", "impact"},
		OutputKeys:    []string{"level", "window"},
		Rows: []DecisionRow{
			{ // 未启用：即使命中也跳过
				Enabled: &disabled,
				Cells:   map[string]string{"env": "prod", "impact": "global"},
				Outputs: map[string]string{"level": "P0", "window": "工作时间内"},
			},
			{ // 首个生效行：impact 通配 + env 任一命中（逗号分隔）
				Cells:   map[string]string{"env": "prod,staging", "impact": ""},
				Outputs: map[string]string{"level": "P1", "window": "非工作时间"},
			},
			{ // 兜底行
				Cells:   map[string]string{},
				Outputs: map[string]string{"level": "P3", "window": "工作时间内"},
			},
		},
	}}

	// 命中第 2 行（第 1 行禁用；impact 通配；env prod|staging 任一）
	out, fired := ApplyDataRules(schema, map[string]any{"env": "prod", "impact": "global"})
	if out["level"] != "P1" || out["window"] != "非工作时间" {
		t.Fatalf("应命中 P1 行，实际 level=%v window=%v", out["level"], out["window"])
	}
	if len(fired) != 1 || !strings.Contains(fired[0], "变更分级") {
		t.Fatalf("fired 应含决策表名，实际 %v", fired)
	}

	// 全不命中具体行 → 兜底行
	out2, _ := ApplyDataRules(schema, map[string]any{"env": "dev", "impact": "single"})
	if out2["level"] != "P3" {
		t.Fatalf("应命中兜底行 P3，实际 %v", out2["level"])
	}
}

// 决策表输出插值：可引用表单变量与前序决策表输出，拼出动态值（门店+工种 → 组名）。
func TestDecisionTableOutputInterpolation(t *testing.T) {
	schema := testLinkSchema()
	schema.Fields = append(schema.Fields,
		FieldDef{Key: "store", Label: "门店", Type: TypeText},
		FieldDef{Key: "role_key", Label: "工种", Type: TypeText},
		FieldDef{Key: "assignee", Label: "处理组", Type: TypeText},
	)
	schema.DecisionTables = []DecisionTable{
		{
			Name: "类型→工种", ConditionKeys: []string{"env"}, OutputKeys: []string{"role_key"},
			Rows: []DecisionRow{{
				Cells:   map[string]string{"env": "prod"},
				Outputs: map[string]string{"role_key": "pos_l1"},
			}},
		},
		{
			Name: "门店+工种→组名", ConditionKeys: []string{"store"}, OutputKeys: []string{"assignee"},
			Rows: []DecisionRow{{
				Outputs: map[string]string{"assignee": "${store}-${role_key}-group"},
			}},
		},
	}

	out, fired := ApplyDataRules(schema, map[string]any{"env": "prod", "store": "上海南京路店"})
	if out["assignee"] != "上海南京路店-pos_l1-group" {
		t.Fatalf("输出应插值拼出组名，实际 %v", out["assignee"])
	}
	if len(fired) != 2 || !strings.Contains(fired[1], "门店+工种→组名#第1行") {
		t.Fatalf("fired 应记录命中行号，实际 %v", fired)
	}
}

// 插值容错：变量缺失 / 占位符未闭合时不报错，缺失段为空串、未闭合原样保留。
func TestInterpolateTolerant(t *testing.T) {
	vals := map[string]any{"a": "A", "nested": map[string]any{"b": "B"}}
	if got := interpolate("${a}-${missing}-${nested.b}", vals); got != "A--B" {
		t.Fatalf("缺失变量应为空串，实际 %q", got)
	}
	if got := interpolate("${a}-${unclosed", vals); got != "A-${unclosed" {
		t.Fatalf("未闭合占位符应原样保留，实际 %q", got)
	}
	if got := interpolate("plain", nil); got != "plain" {
		t.Fatalf("无占位符应原样返回，实际 %q", got)
	}
}

// 决策表配置校验：硬错误拦截 / 警告提示
func TestValidateDecisionTables(t *testing.T) {
	valid := func() *FormSchema {
		s := testLinkSchema()
		s.DecisionTables = []DecisionTable{{
			Name: "分级", ConditionKeys: []string{"env"}, OutputKeys: []string{"level"},
			Rows: []DecisionRow{
				{Cells: map[string]string{"env": "prod"}, Outputs: map[string]string{"level": "P1"}},
				{Outputs: map[string]string{"level": "P3"}},
			},
		}}
		return s
	}
	if warns, err := ValidateDecisionTables(valid()); err != nil || len(warns) != 0 {
		t.Fatalf("合法配置应无错误无警告，err=%v warns=%v", err, warns)
	}
	if _, err := ValidateDecisionTables(nil); err != nil {
		t.Fatalf("nil schema 应放行，实际 %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*FormSchema)
	}{
		{"未配置条件列", func(s *FormSchema) { s.DecisionTables[0].ConditionKeys = nil }},
		{"未配置输出列", func(s *FormSchema) { s.DecisionTables[0].OutputKeys = nil }},
		{"列未在表单声明", func(s *FormSchema) { s.DecisionTables[0].OutputKeys = []string{"ghost"} }},
		{"列名重复", func(s *FormSchema) { s.DecisionTables[0].ConditionKeys = []string{"env", "env"} }},
		{"未配置行", func(s *FormSchema) { s.DecisionTables[0].Rows = nil }},
		{"条件单元格不属于条件列", func(s *FormSchema) {
			s.DecisionTables[0].Rows[0].Cells = map[string]string{"ghost": "x"}
		}},
	}
	for _, tc := range cases {
		s := valid()
		tc.mutate(s)
		if _, err := ValidateDecisionTables(s); err == nil {
			t.Fatalf("%s 应被拦截", tc.name)
		}
	}

	// 无兜底行 → 警告但不拦截
	s := valid()
	s.DecisionTables[0].Rows = s.DecisionTables[0].Rows[:1]
	warns, err := ValidateDecisionTables(s)
	if err != nil {
		t.Fatalf("无兜底行不应拦截，实际 %v", err)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "兜底行") {
		t.Fatalf("应提示缺少兜底行，实际 %v", warns)
	}
}

// 决策表输出参与必填校验：未填的必填字段由决策表自动带出后应放行
func TestValidateValuesFullDerivedSatisfiesRequired(t *testing.T) {
	schema := testLinkSchema()
	for i := range schema.Fields {
		if schema.Fields[i].Key == "impact" {
			schema.Fields[i].Required = true
		}
	}
	schema.DecisionTables = []DecisionTable{{
		ConditionKeys: []string{"env"},
		OutputKeys:    []string{"impact"},
		Rows: []DecisionRow{
			{Cells: map[string]string{"env": "prod"}, Outputs: map[string]string{"impact": "global"}},
		},
	}}
	e := NewEngine(nil)

	// 未填 impact（选了 prod）→ 决策表带出 → 放行
	if _, _, err := e.ValidateValuesFull(t.Context(), "t1", schema, map[string]any{"env": "prod"}); err != nil {
		t.Fatalf("决策表带出必填字段后应放行，实际 %v", err)
	}
	// 无命中（env=dev）→ 必填缺失 → 拦截
	if _, _, err := e.ValidateValuesFull(t.Context(), "t1", schema, map[string]any{"env": "dev"}); err == nil {
		t.Fatalf("决策表未命中时必填缺失应拦截")
	}
}

// 目标字段未在 schema 声明：容错跳过，不写入、不报错
func TestApplyDataRulesSkipsUndeclaredTarget(t *testing.T) {
	schema := testLinkSchema()
	schema.DataLinks = []DataLinkRule{{
		Conditions: []Condition{{Field: "env", Op: OpEq, Value: "prod"}},
		Sets:       []DataSet{{Target: "ghost_field", Mode: SetFixed, Value: "x"}},
	}}
	out, fired := ApplyDataRules(schema, map[string]any{"env": "prod"})
	if _, ok := out["ghost_field"]; ok {
		t.Fatalf("未声明字段不应被写入")
	}
	if len(fired) != 0 {
		t.Fatalf("全部赋值被跳过时不应计入 fired，实际 %v", fired)
	}
}

// ---- ci_attr 模式（CMDB 属性回填） ----

type fakeCIAttrResolver struct {
	vals map[string]string // "type|name|attr" → value
	err  error
}

func (f *fakeCIAttrResolver) ResolveCIAttr(_ context.Context, _, typeName, ciName, attr string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.vals[typeName+"|"+ciName+"|"+attr]
	return v, ok, nil
}

func TestApplyDataRulesCIAttr(t *testing.T) {
	schema := &FormSchema{
		Fields: []FieldDef{
			{Key: "store", Label: "门店", Type: "select"},
			{Key: "handler", Label: "处理人", Type: "text"},
		},
		DataLinks: []DataLinkRule{{
			Name:       "门店→处理人",
			Conditions: []Condition{{Field: "store", Op: OpNotEmpty}},
			Sets: []DataSet{{
				Target: "handler", Mode: SetFromCIAttr, Value: "owner",
				CIAttr: &DataSetCIAttr{CIType: "门店", NameField: "store"},
			}},
		}},
	}

	// 命中：从 CMDB 回填 owner
	res := &fakeCIAttrResolver{vals: map[string]string{"门店|门店A|owner": "user2"}}
	out, fired := ApplyDataRulesWithCI(context.Background(), "default", schema,
		map[string]any{"store": "门店A"}, res)
	if out["handler"] != "user2" {
		t.Fatalf("handler 应回填为 user2，实际 %v", out["handler"])
	}
	if len(fired) != 1 {
		t.Fatalf("应记录 1 条触发规则，实际 %v", fired)
	}

	// CI 不存在且无 fallback：不写
	out2, _ := ApplyDataRulesWithCI(context.Background(), "default", schema,
		map[string]any{"store": "不存在门店"}, res)
	if _, ok := out2["handler"]; ok {
		t.Fatalf("CI 不存在时不应写 handler")
	}

	// 无 resolver：跳过（纯函数兼容）
	out3, _ := ApplyDataRules(schema, map[string]any{"store": "门店A"})
	if _, ok := out3["handler"]; ok {
		t.Fatalf("无 resolver 时 ci_attr 应跳过")
	}

	// 解析失败：保守跳过
	resErr := &fakeCIAttrResolver{err: context.DeadlineExceeded}
	out4, _ := ApplyDataRulesWithCI(context.Background(), "default", schema,
		map[string]any{"store": "门店A"}, resErr)
	if _, ok := out4["handler"]; ok {
		t.Fatalf("解析失败时应跳过 handler")
	}
}

func TestApplyDataRulesCIAttrFallback(t *testing.T) {
	schema := &FormSchema{
		Fields: []FieldDef{
			{Key: "store", Label: "门店"},
			{Key: "handler", Label: "处理人"},
		},
		DataLinks: []DataLinkRule{{
			Conditions: []Condition{{Field: "store", Op: OpNotEmpty}},
			Sets: []DataSet{{
				Target: "handler", Mode: SetFromCIAttr, Value: "owner",
				CIAttr: &DataSetCIAttr{CIType: "门店", NameField: "store", Fallback: "一线值班"},
			}},
		}},
	}
	res := &fakeCIAttrResolver{vals: map[string]string{}}
	out, _ := ApplyDataRulesWithCI(context.Background(), "default", schema,
		map[string]any{"store": "未知门店"}, res)
	if out["handler"] != "一线值班" {
		t.Fatalf("fallback 应生效，实际 %v", out["handler"])
	}
}

func TestValidateValuesFullWithCIAttr(t *testing.T) {
	// 端到端：Full 校验链路上 ci_attr 回填落定 computed（门店→处理人必填可过）
	schema := &FormSchema{
		Fields: []FieldDef{
			{Key: "store", Label: "门店", Type: "text"},
			{Key: "handler", Label: "处理人", Required: true, Type: "text"},
		},
		DataLinks: []DataLinkRule{{
			Conditions: []Condition{{Field: "store", Op: OpNotEmpty}},
			Sets: []DataSet{{
				Target: "handler", Mode: SetFromCIAttr, Value: "owner",
				CIAttr: &DataSetCIAttr{CIType: "门店", NameField: "store"},
			}},
		}},
	}
	eng := NewEngine(nil)
	eng.SetCIAttrResolver(&fakeCIAttrResolver{vals: map[string]string{"门店|门店A|owner": "user2"}})
	_, computed, err := eng.ValidateValuesFull(context.Background(), "default", schema, map[string]any{"store": "门店A"})
	if err != nil {
		t.Fatalf("handler 由联动回填，必填校验应通过: %v", err)
	}
	if computed["handler"] != "user2" {
		t.Fatalf("computed.handler 应为 user2，实际 %v", computed["handler"])
	}
}

// ---- submitter_attr 模式（提单人属性回填） ----

type fakeSubmitterResolver struct {
	vals map[string]string // attr → value
	err  error
}

func (f *fakeSubmitterResolver) ResolveSubmitterAttr(_ context.Context, _, attr string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.vals[attr]
	return v, ok, nil
}

func TestApplyDataRulesSubmitterAttr(t *testing.T) {
	schema := &FormSchema{
		Fields: []FieldDef{
			{Key: "store", Label: "部门(门店)", Type: "text"},
			{Key: "store_it", Label: "门店IT", Type: "text"},
		},
		DataLinks: []DataLinkRule{{
			Name:       "提单人部门回填",
			Conditions: []Condition{},
			Sets: []DataSet{{
				Target:    "store",
				Mode:      SetFromSubmitter,
				Value:     "department",
				Submitter: &DataSetSubmitter{Attr: "department"},
			}},
		}},
		DecisionTables: []DecisionTable{{
			Name:          "门店→门店IT",
			ConditionKeys: []string{"store"},
			OutputKeys:    []string{"store_it"},
			Rows: []DecisionRow{{
				Cells:   map[string]string{"store": "门店A"},
				Outputs: map[string]string{"store_it": "user2"},
			}},
		}},
	}

	// 命中：提单人 department=门店A → 决策表推出 store_it=user2
	res := &fakeSubmitterResolver{vals: map[string]string{"department": "门店A"}}
	out, fired := ApplyDataRulesFull(context.Background(), "default", schema,
		map[string]any{}, nil, res)
	if out["store"] != "门店A" {
		t.Fatalf("store 应回填为提单人部门 门店A，实际 %v", out["store"])
	}
	if out["store_it"] != "user2" {
		t.Fatalf("决策表应推出 store_it=user2，实际 %v", out["store_it"])
	}
	if len(fired) != 2 {
		t.Fatalf("应记录 2 条触发（联动+决策表），实际 %v", fired)
	}

	// 属性为空且无 fallback：不写，决策表也不命中
	out2, _ := ApplyDataRulesFull(context.Background(), "default", schema,
		map[string]any{}, nil, &fakeSubmitterResolver{vals: map[string]string{}})
	if _, ok := out2["store"]; ok {
		t.Fatalf("提单人无部门时不应写 store")
	}
	if _, ok := out2["store_it"]; ok {
		t.Fatalf("store 为空时决策表不应命中")
	}

	// 无 resolver：跳过（纯函数兼容）
	out3, _ := ApplyDataRules(schema, map[string]any{})
	if _, ok := out3["store"]; ok {
		t.Fatalf("无 resolver 时 submitter_attr 应跳过")
	}

	// 解析失败：保守跳过
	out4, _ := ApplyDataRulesFull(context.Background(), "default", schema,
		map[string]any{}, nil, &fakeSubmitterResolver{err: context.DeadlineExceeded})
	if _, ok := out4["store"]; ok {
		t.Fatalf("解析失败时应跳过 store")
	}
}

func TestApplyDataRulesSubmitterAttrFallback(t *testing.T) {
	schema := &FormSchema{
		Fields: []FieldDef{{Key: "store", Label: "部门(门店)"}},
		DataLinks: []DataLinkRule{{
			Sets: []DataSet{{
				Target:    "store",
				Mode:      SetFromSubmitter,
				Submitter: &DataSetSubmitter{Attr: "department", Fallback: "总店"},
			}},
		}},
	}
	out, _ := ApplyDataRulesFull(context.Background(), "default", schema,
		map[string]any{}, nil, &fakeSubmitterResolver{vals: map[string]string{}})
	if out["store"] != "总店" {
		t.Fatalf("fallback 应生效，实际 %v", out["store"])
	}
}

func TestValidateValuesFullWithSubmitterAttr(t *testing.T) {
	// 端到端：Full 校验链路上 submitter_attr 回填落定 computed（部门必填可过）
	schema := &FormSchema{
		Fields: []FieldDef{
			{Key: "store", Label: "部门(门店)", Required: true, Type: "text"},
		},
		DataLinks: []DataLinkRule{{
			Sets: []DataSet{{
				Target:    "store",
				Mode:      SetFromSubmitter,
				Submitter: &DataSetSubmitter{Attr: "department"},
			}},
		}},
	}
	eng := NewEngine(nil)
	eng.SetSubmitterAttrResolver(&fakeSubmitterResolver{vals: map[string]string{"department": "门店A"}})
	_, computed, err := eng.ValidateValuesFull(context.Background(), "default", schema, map[string]any{})
	if err != nil {
		t.Fatalf("store 由联动回填，必填校验应通过: %v", err)
	}
	if computed["store"] != "门店A" {
		t.Fatalf("computed.store 应为 门店A，实际 %v", computed["store"])
	}
}
