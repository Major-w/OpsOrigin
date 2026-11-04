package form

import (
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
