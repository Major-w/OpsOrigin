package form

import "testing"

// schemaWithGroup 主表单含 group 分组容器：子字段 host/owner 值平铺 custom_fields。
func schemaWithGroup() *FormSchema {
	return &FormSchema{
		Version: 1,
		Scope:   "ticket:default",
		Fields: []FieldDef{
			{Key: "title", Label: "标题", Type: TypeText},
			{Key: "grp", Label: "资源信息", Type: TypeGroup, Children: []FieldDef{
				{Key: "host", Label: "主机", Type: TypeText, Required: true},
				{Key: "owner", Label: "负责人", Type: TypeText},
			}},
			{Key: "top_readonly", Label: "节点只读", Type: TypeText},
			{Key: "top_hidden", Label: "节点隐藏", Type: TypeText, Required: true},
		},
	}
}

// permsWithGroup 节点权限：grp 容器 editable、host 只读（组内）、owner 隐藏（组内）、
// top_readonly 只读、top_hidden 隐藏。
func permsWithGroup() []StepFieldPerm {
	return []StepFieldPerm{
		{Key: "grp", Mode: PermEditable},
		{Key: "host", Mode: PermReadonly},
		{Key: "owner", Mode: PermHidden},
		{Key: "top_readonly", Mode: PermReadonly},
		{Key: "top_hidden", Mode: PermHidden},
	}
}

// ApplyStepPerms 投影：hidden 剔除（含 group 子字段）、readonly 标记、editable+required 标必填。
func TestApplyStepPermsProjection(t *testing.T) {
	s := schemaWithGroup()
	perms := []StepFieldPerm{
		{Key: "grp", Mode: PermEditable},
		{Key: "host", Mode: PermReadonly},
		{Key: "owner", Mode: PermHidden},
		{Key: "top_readonly", Mode: PermReadonly},
		{Key: "top_hidden", Mode: PermHidden},
		{Key: "new_req", Mode: PermEditable, Required: true},
	}
	// new_req 不在 schema 中（配置漂移）→ 投影后应无新增字段，不报错
	projected := ApplyStepPerms(s, perms)
	if projected == nil {
		t.Fatal("投影不应返回 nil")
	}

	// 顶层：top_hidden 剔除；top_readonly 只读
	var topHidden, topReadonly *FieldDef
	for i := range projected.Fields {
		switch projected.Fields[i].Key {
		case "top_hidden":
			topHidden = &projected.Fields[i]
		case "top_readonly":
			topReadonly = &projected.Fields[i]
		}
	}
	if topHidden != nil {
		t.Error("hidden 顶层字段应被剔除")
	}
	if topReadonly == nil || !topReadonly.ReadOnly {
		t.Error("readonly 顶层字段应标记只读")
	}

	// group 子字段：owner 剔除、host 只读；grp 容器保留
	var grp *FieldDef
	for i := range projected.Fields {
		if projected.Fields[i].Key == "grp" {
			grp = &projected.Fields[i]
		}
	}
	if grp == nil {
		t.Fatal("group 容器应保留")
	}
	if len(grp.Children) != 1 || grp.Children[0].Key != "host" {
		t.Fatalf("group 内 hidden 子字段应被剔除, got %d children", len(grp.Children))
	}
	if !grp.Children[0].ReadOnly {
		t.Error("group 内 readonly 子字段应标记只读")
	}

	// 入参 schema 不被修改
	if s.Fields[1].Children[0].ReadOnly {
		t.Error("ApplyStepPerms 不应修改入参 schema")
	}
	if len(s.Fields[1].Children) != 2 {
		t.Error("入参 schema 的 group children 不应被剔除")
	}
}

// 无 perms 时 ApplyStepPerms 原样返回（同一指针），EffectiveStatesForStep 等价 EffectiveStates。
func TestApplyStepPermsNoop(t *testing.T) {
	s := schemaWithGroup()
	if ApplyStepPerms(s, nil) != s {
		t.Error("无 perms 时应原样返回同一 schema")
	}
	states1 := EffectiveStates(s, map[string]any{})
	states2 := EffectiveStatesForStep(s, map[string]any{}, nil)
	if len(states1) != len(states2) {
		t.Fatalf("states 数量不一致: %d vs %d", len(states1), len(states2))
	}
}

// 节点权限投影后字段级校验：hidden 必填字段剔除后不再拦截；group 内 readonly 豁免必填。
func TestValidateValuesForStepProjectedValidation(t *testing.T) {
	s := schemaWithGroup()
	perms := permsWithGroup()
	e := NewEngine(nil)

	// top_hidden 必填但节点隐藏：提交值缺失不拦截（投影剔除）
	vals := map[string]any{"title": "t", "host": "h-1"}
	if err := e.ValidateValuesForStep(t.Context(), "t1", s, vals, perms); err != nil {
		t.Fatalf("节点 hidden 必填字段不应拦截: %v", err)
	}

	// top_hidden 权限未配置（无 perms）：按字段自身必填拦截
	if err := e.ValidateValuesForStep(t.Context(), "t1", s, map[string]any{"title": "t"}, nil); err == nil {
		t.Fatal("无节点权限时 top_hidden 应按自身必填拦截")
	}

	// group 内 host 必填但节点只读：豁免必填
	vals2 := map[string]any{"title": "t"}
	if err := e.ValidateValuesForStep(t.Context(), "t1", s, vals2, perms); err != nil {
		t.Fatalf("group 内 readonly 子字段应豁免必填: %v", err)
	}
}

// ValidateValuesFullForStep：cap 语义下节点必填不被 optional 行为规则放宽；computed 回传推导值。
func TestValidateValuesFullForStepCapAndComputed(t *testing.T) {
	s := &FormSchema{
		Version: 1,
		Scope:   "ticket:default",
		Fields: []FieldDef{
			{Key: "trigger", Label: "触发", Type: TypeText},
			{Key: "must", Label: "节点必填", Type: TypeText},
		},
		BehaviorRules: []BehaviorRule{
			{Conditions: []Condition{{Field: "trigger", Op: OpEq, Value: "x"}}, Action: ActOptional, Targets: []string{"must"}},
		},
	}
	perms := []StepFieldPerm{{Key: "must", Mode: PermEditable, Required: true}}
	e := NewEngine(nil)

	// trigger=x 命中 optional（试图放宽）→ cap 强制必填 → 空值拦截
	_, _, err := e.ValidateValuesFullForStep(t.Context(), "t1", s, map[string]any{"trigger": "x"}, perms)
	if err == nil {
		t.Fatal("节点必填不应被行为规则 optional 放宽（cap 应拦截空值）")
	}

	// 填值后通过
	if _, _, err := e.ValidateValuesFullForStep(t.Context(), "t1", s, map[string]any{"trigger": "x", "must": "v"}, perms); err != nil {
		t.Fatalf("已填值不应拦截: %v", err)
	}

	// 无 perms 时 optional 正常放宽（行为规则语义不受影响）
	if _, _, err := e.ValidateValuesFullForStep(t.Context(), "t1", s, map[string]any{"trigger": "x"}, nil); err != nil {
		t.Fatalf("无节点权限时 optional 应正常放宽: %v", err)
	}
}
