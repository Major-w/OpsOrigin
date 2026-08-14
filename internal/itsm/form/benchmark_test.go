package form

import (
	"testing"
)

// BenchmarkValidateSchema 基准: 10 字段 schema 校验(必填/类型/枚举/正则/长度)。
func BenchmarkValidateSchema(b *testing.B) {
	schema := &FormSchema{
		Scope: "process:p:step:s",
		Fields: []FieldDef{
			{Key: "env", Type: TypeSelect, Required: true, Options: []FieldOption{{Value: "prod", Label: "生产"}, {Value: "dev", Label: "开发"}}},
			{Key: "count", Type: TypeNumber, Validation: &FieldValidation{Min: f64p(1), Max: f64p(100)}},
			{Key: "reason", Type: TypeText, Required: true, Validation: &FieldValidation{MaxLen: 200}},
			{Key: "url", Type: TypeText, Validation: &FieldValidation{Regex: `^https?://`}},
			{Key: "owner", Type: TypeText, Required: true},
		},
	}
	values := map[string]any{
		"env": "prod", "count": 50.0, "reason": "容量扩容", "url": "https://x.com", "owner": "ops",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ValidateSchema(schema, values); err != nil {
			b.Fatalf("validate: %v", err)
		}
	}
}
