package form

import (
	"strings"
	"testing"
	"time"
)

// TestExpandDictRefsPreservesAssociation 引用式展开：展开产物保留 dict_ref/dict_key 标记，
// 设计器加载展开结果再保存（模拟往返）不会断开关联，字典更新后重新展开即全局生效。
func TestExpandDictRefsPreservesAssociation(t *testing.T) {
	dicts := map[string]*FieldDict{
		"d1": {ID: "d1", Fields: []FieldDef{
			{Key: "owner", Label: "负责人", Type: TypeUser, Source: "iam:user"},
		}},
	}
	schema := &FormSchema{Scope: "ticket:default", Fields: []FieldDef{
		{Key: "field_1", Label: "占位", Type: TypeText, DictRef: "d1"},
	}}
	ExpandDictRefs(schema, dicts)
	if len(schema.Fields) != 1 || schema.Fields[0].Key != "owner" {
		t.Fatalf("展开结果不符: %+v", schema.Fields)
	}
	if schema.Fields[0].DictRef != "d1" {
		t.Fatalf("展开产物应保留 dict_ref 引用标记, got %+v", schema.Fields[0])
	}

	// 模拟设计器往返：展开结果原样回存（schema2），再展开一次 —— 幂等且关联仍在
	stored := &FormSchema{Scope: schema.Scope, Fields: append([]FieldDef(nil), schema.Fields...)}
	ExpandDictRefs(stored, dicts)
	if len(stored.Fields) != 1 || stored.Fields[0].DictRef != "d1" || stored.Fields[0].Key != "owner" {
		t.Fatalf("往返后再展开应幂等且保留引用: %+v", stored.Fields)
	}

	// 字典更新（改标签）→ 重新展开立即生效（渲染时解析，非落库副本）
	dicts["d1"].Fields[0].Label = "负责人(改)"
	ExpandDictRefs(stored, dicts)
	if stored.Fields[0].Label != "负责人(改)" {
		t.Fatalf("字典更新应在重新展开时生效, got %q", stored.Fields[0].Label)
	}

	// 字段级引用同样保留 dict_key
	schema2 := &FormSchema{Fields: []FieldDef{{Key: "field_2", Label: "x", Type: TypeText, DictRef: "d1", DictKey: "owner"}}}
	ExpandDictRefs(schema2, dicts)
	if len(schema2.Fields) != 1 || schema2.Fields[0].DictKey != "owner" || schema2.Fields[0].DictRef != "d1" {
		t.Fatalf("字段级引用展开应保留 dict_ref/dict_key: %+v", schema2.Fields)
	}
}

// TestMergeDefaultsAs 动态默认值 token：当前用户/日期/时间，及无用户上下文跳过注入。
func TestMergeDefaultsAs(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "applicant", Label: "申请人", Type: TypeUser, Default: "{{current_user}}"},
		{Key: "apply_date", Label: "申请日期", Type: TypeDate, Default: "{{current_date}}"},
		{Key: "apply_at", Label: "申请时间", Type: TypeDatetime, Default: "{{current_datetime}}"},
		{Key: "mixed", Label: "混排", Type: TypeText, Default: "值班人：{{current_user}}"},
		{Key: "static", Label: "静态", Type: TypeText, Default: "fixed"},
		{Key: "filled", Label: "已填", Type: TypeText, Default: "{{current_user}}"},
	}}
	values := map[string]any{"filled": "someone"}

	MergeDefaultsAs(schema, values, "zhang.san")
	if values["applicant"] != "zhang.san" {
		t.Fatalf("applicant = %v", values["applicant"])
	}
	if values["apply_date"] != time.Now().Format("2006-01-02") {
		t.Fatalf("apply_date = %v", values["apply_date"])
	}
	if values["apply_at"] != time.Now().Format("2006-01-02 15:04") {
		t.Fatalf("apply_at = %v", values["apply_at"])
	}
	if values["mixed"] != "值班人：zhang.san" {
		t.Fatalf("mixed = %v", values["mixed"])
	}
	if values["static"] != "fixed" {
		t.Fatalf("static = %v", values["static"])
	}
	if values["filled"] != "someone" {
		t.Fatalf("已有值不应被默认值覆盖: %v", values["filled"])
	}

	// 无用户上下文：{{current_user}} 解析为空 → 跳过注入，不落脏数据
	v2 := map[string]any{}
	MergeDefaultsAs(schema, v2, "")
	if _, ok := v2["applicant"]; ok {
		t.Fatalf("无用户上下文时不应注入 {{current_user}}")
	}
	if v2["apply_date"] != time.Now().Format("2006-01-02") {
		t.Fatalf("日期 token 不依赖用户上下文: %v", v2["apply_date"])
	}

	// 旧入口 MergeDefaults 行为兼容（静态默认值照常注入）
	v3 := map[string]any{}
	MergeDefaults(schema, v3)
	if v3["static"] != "fixed" {
		t.Fatalf("MergeDefaults 静态默认值失效: %v", v3["static"])
	}
}

// TestValidateStructuredTable 结构化表格（MRVS）校验：对象行 + 必填列 + 旧自由表格兼容。
func TestValidateStructuredTable(t *testing.T) {
	f := FieldDef{Key: "servers", Label: "服务器清单", Type: TypeTable, Columns: []TableColumn{
		{Key: "hostname", Label: "主机名", Required: true},
		{Key: "ip", Label: "IP"},
	}}

	// 合法：对象行、必填列有值、数字单元格允许
	okRows := []any{
		map[string]any{"hostname": "web-01", "ip": "10.0.0.1"},
		map[string]any{"hostname": "db-01", "ip": 100},
	}
	if err := validateFieldWith(f, okRows, nil); err != nil {
		t.Fatalf("合法结构化行不应报错: %v", err)
	}

	// 必填列缺失 / 空串 / 空白
	for i, bad := range []any{
		map[string]any{"ip": "10.0.0.2"},
		map[string]any{"hostname": "", "ip": "x"},
		map[string]any{"hostname": "   "},
	} {
		if err := validateFieldWith(f, []any{bad}, nil); err == nil {
			t.Fatalf("用例 %d: 必填列缺失应报错", i)
		} else if !strings.Contains(err.Error(), "主机名") {
			t.Fatalf("用例 %d: 错误信息应指明列: %v", i, err)
		}
	}

	// 行不是对象 → 报错（旧二维数组混入结构化字段）
	if err := validateFieldWith(f, []any{[]any{"a", "b"}}, nil); err == nil {
		t.Fatal("结构化字段的数组行应报错")
	}

	// 未知列忽略（向前兼容列定义变更）
	tolerant := []any{map[string]any{"hostname": "web-02", "legacy_col": "x"}}
	if err := validateFieldWith(f, tolerant, nil); err != nil {
		t.Fatalf("未知列应忽略: %v", err)
	}

	// 未定义列定义 → 走旧自由表格校验（二维数组仍合法）
	legacy := FieldDef{Key: "t", Label: "自由表格", Type: TypeTable}
	if err := validateFieldWith(legacy, []any{[]any{"a", "b"}}, nil); err != nil {
		t.Fatalf("旧自由表格应兼容: %v", err)
	}
}
