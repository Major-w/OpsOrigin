package form

import "testing"

func f64p(v float64) *float64 { return &v }

func TestValidateSchemaRequired(t *testing.T) {
	schema := &FormSchema{Scope: "ticket:default", Fields: []FieldDef{
		{Key: "env", Label: "环境", Type: TypeSelect, Required: true,
			Options: []FieldOption{{Value: "prod", Label: "生产"}}},
	}}
	if err := ValidateSchema(schema, map[string]any{}); err == nil {
		t.Fatal("expected required error for missing field")
	}
	if err := ValidateSchema(schema, map[string]any{"env": "prod"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateSchema(schema, map[string]any{"env": "dev"}); err == nil {
		t.Fatal("expected option-range error")
	}
}

func TestValidateSchemaNumber(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "count", Type: TypeNumber, Validation: &FieldValidation{Min: f64p(1), Max: f64p(10)}},
	}}
	if err := ValidateSchema(schema, map[string]any{"count": 5.0}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := ValidateSchema(schema, map[string]any{"count": 20.0}); err == nil {
		t.Fatal("expected max error")
	}
}

func TestValidateSchemaTextRegexLen(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{
		{Key: "ip", Type: TypeText, Validation: &FieldValidation{Regex: `^\d+\.\d+\.\d+\.\d+$`, MinLen: 7}},
	}}
	if err := ValidateSchema(schema, map[string]any{"ip": "10.0.0.1"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := ValidateSchema(schema, map[string]any{"ip": "abc"}); err == nil {
		t.Fatal("expected regex error")
	}
}

func TestFieldVisible(t *testing.T) {
	f := FieldDef{Key: "plan", Label: "灰度策略", VisibleWhen: "risk_level==critical", Type: TypeText}
	if !FieldVisible(f, map[string]any{"risk_level": "critical"}) {
		t.Fatal("should be visible when critical")
	}
	if FieldVisible(f, map[string]any{"risk_level": "low"}) {
		t.Fatal("should be hidden when not critical")
	}
	if !FieldVisible(FieldDef{}, map[string]any{}) {
		t.Fatal("no expression should be visible")
	}
}

func TestMergeDefaults(t *testing.T) {
	schema := &FormSchema{Fields: []FieldDef{{Key: "a", Type: TypeText, Default: "x"}}}
	v := map[string]any{}
	MergeDefaults(schema, v)
	if v["a"] != "x" {
		t.Fatal("default not merged")
	}
}

func boolp(v bool) *bool { return &v }

// 附件字段的数量约束：老 schema(Multiple 为 nil) 不受限，显式配置后服务端兜底拦截。
func TestValidateSchemaAttachmentLimits(t *testing.T) {
	two := map[string]any{"files": []any{"1", "2"}}

	legacy := &FormSchema{Fields: []FieldDef{{Key: "files", Label: "附件", Type: TypeAttachment}}}
	if err := ValidateSchema(legacy, two); err != nil {
		t.Fatalf("未配置约束时不应报错: %v", err)
	}

	single := &FormSchema{Fields: []FieldDef{{Key: "files", Label: "附件", Type: TypeAttachment, Multiple: boolp(false)}}}
	if err := ValidateSchema(single, two); err == nil {
		t.Fatal("单文件字段传两个附件应报错")
	}
	if err := ValidateSchema(single, map[string]any{"files": []any{"1"}}); err != nil {
		t.Fatalf("单文件字段传一个不应报错: %v", err)
	}

	capped := &FormSchema{Fields: []FieldDef{{Key: "files", Label: "附件", Type: TypeAttachment, Multiple: boolp(true), MaxCount: 1}}}
	if err := ValidateSchema(capped, two); err == nil {
		t.Fatal("超过 max_count 应报错")
	}
}
