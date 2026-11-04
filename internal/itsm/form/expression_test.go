package form

import (
	"math"
	"testing"
)

// TestEvalExpression 表达式求值: 四则/优先级/括号/字段引用/函数/字符串拼接/容错。
func TestEvalExpression(t *testing.T) {
	values := map[string]any{
		"unit_price": "9.5", // 字符串数字隐式转换
		"count":      float64(4),
		"name":       "服务器",
		"disk":       float64(200),
	}
	cases := []struct {
		name string
		expr string
		want any
	}{
		{"四则与优先级", "2 + 3 * 4", float64(14)},
		{"括号", "(2 + 3) * 4", float64(20)},
		{"一元负号", "-3 + 5", float64(2)},
		{"取模", "10 % 3", float64(1)},
		{"字段引用+字符串数字", "{unit_price} * {count}", float64(38)},
		{"嵌套引用括号", "({count} + 1) * {unit_price}", float64(47.5)},
		{"函数 abs/round", "round({unit_price} * {count} / 3, 2)", 12.67},
		{"min/max", "max({disk} - 100, min(10, 999))", float64(100)},
		{"floor/ceil", "floor({unit_price}) + ceil({unit_price})", float64(19)},
		{"数字转字符串拼接", "{name} + '-' + {count}", "服务器-4"},
		{"字符串字面量", "'单价: ' + {unit_price}", "单价: 9.5"},
		{"嵌套分组引用无关性", "{disk} / 2 / {count}", float64(25)},
	}
	for _, c := range cases {
		got, err := EvalExpression(c.expr, values)
		if err != nil {
			t.Errorf("%s: EvalExpression(%q) err = %v", c.name, c.expr, err)
			continue
		}
		if !numEq(got, c.want) && got != c.want {
			t.Errorf("%s: EvalExpression(%q) = %v, want %v", c.name, c.expr, got, c.want)
		}
	}
}

func numEq(a, b any) bool {
	af, aok := a.(float64)
	bf, bok := b.(float64)
	return aok && bok && math.Abs(af-bf) < 1e-9
}

// TestEvalExpressionErrors 求值失败场景: 除零/未知字段/未知函数/未闭合。
func TestEvalExpressionErrors(t *testing.T) {
	values := map[string]any{"a": float64(1)}
	for _, expr := range []string{
		"1 / 0",
		"{missing} + 1",
		"foo(a)",          // 未知标识符(无括号前的裸标识)
		"unknown_fn(1)",   // 未知函数
		"'unclosed",       // 字符串未闭合
		"(1 + 2",          // 缺右括号
		"{} + 1",          // 空字段引用
		"{a} + 'x' * 'y'", // 字符串不能乘
	} {
		if _, err := EvalExpression(expr, values); err == nil {
			t.Errorf("EvalExpression(%q) 应报错, 实际 nil", expr)
		}
	}
}

// TestComputeExpressionFields 计算字段批量求值: 只写 expression 字段, 失败跳过。
func TestComputeExpressionFields(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "total", Type: TypeExpression, Default: "{price} * {qty}"},
		{Key: "label", Type: TypeExpression, Default: "'共 ' + {qty} + ' 件'"},
		{Key: "bad", Type: TypeExpression, Default: "{nope} + 1"},
		{Key: "price", Type: TypeNumber, Default: "2.5"},
		// 分组容器内的计算字段同样生效
		{Key: "g1", Type: TypeGroup, Children: []FieldDef{
			{Key: "g_total", Type: TypeExpression, Default: "{price} * 2"},
		}},
	}}
	values := map[string]any{"price": "2.5", "qty": float64(4)}
	ComputeExpressionFields(schema, values)
	if got := values["total"]; !numEq(got, float64(10)) {
		t.Errorf("total = %v, want 10", got)
	}
	if got := values["label"]; got != "共 4 件" {
		t.Errorf("label = %v, want 共 4 件", got)
	}
	if _, ok := values["bad"]; ok {
		t.Errorf("求值失败字段不应写入, got %v", values["bad"])
	}
	if got := values["g_total"]; !numEq(got, float64(5)) {
		t.Errorf("g_total(分组内) = %v, want 5", got)
	}
	if got := values["price"]; got != "2.5" {
		t.Errorf("非 expression 字段不应被改写, got %v", got)
	}
}

// TestFlattenFields 分组展平: group 不产生叶子, Children 平铺, 嵌套分组递归。
func TestFlattenFields(t *testing.T) {
	fields := []FieldDef{
		{Key: "a", Type: TypeText},
		{Key: "g1", Type: TypeGroup, Children: []FieldDef{
			{Key: "b", Type: TypeNumber},
			{Key: "g2", Type: TypeGroup, Children: []FieldDef{
				{Key: "c", Type: TypeText},
			}},
		}},
		{Key: "d", Type: TypeText},
	}
	flat := FlattenFields(fields)
	keys := make([]string, 0, len(flat))
	for _, f := range flat {
		keys = append(keys, f.Key)
	}
	want := "a,b,c,d"
	if got := joinKeys(keys); got != want {
		t.Errorf("FlattenFields = %v, want %v", got, want)
	}
}

// TestExpandDictRefsFieldLevel 字段级字典引用: dict_key 只展开匹配的单个字段。
func TestExpandDictRefsFieldLevel(t *testing.T) {
	dicts := map[string]*FieldDict{
		"d1": {ID: "d1", Fields: []FieldDef{
			{Key: "env", Type: TypeSelect, Label: "环境"},
			{Key: "owner", Type: TypeUser, Label: "负责人"},
		}},
	}
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "placeholder", Type: TypeText, DictRef: "d1", DictKey: "owner", Section: "分组A", Order: 3},
	}}
	ExpandDictRefs(schema, dicts)
	if len(schema.Fields) != 1 {
		t.Fatalf("字段级引用应只展开 1 个字段, got %d", len(schema.Fields))
	}
	f := schema.Fields[0]
	if f.Key != "owner" || f.Label != "负责人" || f.Section != "分组A" || f.Order != 3 {
		t.Errorf("展开结果 = %+v", f)
	}
	// 分组容器内的 dict_ref 同样展开
	schema2 := &FormSchema{Fields: []FieldDef{
		{Key: "g", Type: TypeGroup, Children: []FieldDef{
			{Key: "p2", Type: TypeText, DictRef: "d1"},
		}},
	}}
	ExpandDictRefs(schema2, dicts)
	if len(schema2.Fields[0].Children) != 2 {
		t.Fatalf("分组内全量引用应展开 2 个字段, got %d", len(schema2.Fields[0].Children))
	}
}

func joinKeys(keys []string) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k
	}
	return out
}
