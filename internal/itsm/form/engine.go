package form

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Engine 表单校验引擎：持有 SchemaStore，按作用域取 schema 并校验值。
type Engine struct {
	store SchemaStore
}

func NewEngine(s SchemaStore) *Engine {
	return &Engine{store: s}
}

// ResolveSchema 取出某作用域的 schema（无则返回 nil，不报错）。
func (e *Engine) ResolveSchema(ctx context.Context, tenantID, scope string) (*FormSchema, error) {
	return e.store.GetFormSchema(ctx, tenantID, scope)
}

// Validate 按 scope 取出 schema 并校验自定义字段值。
// 无 schema（未配置表单）时放行；存储异常不阻断创建。
func (e *Engine) Validate(ctx context.Context, tenantID, scope string, values map[string]any) error {
	schema, err := e.store.GetFormSchema(ctx, tenantID, scope)
	if err != nil || schema == nil {
		return nil
	}
	return ValidateSchema(schema, values)
}

// ValidateSchema 纯函数校验，便于单测，返回首个错误。
func ValidateSchema(schema *FormSchema, values map[string]any) error {
	for _, f := range schema.Fields {
		if !FieldVisible(f, values) {
			continue
		}
		v, ok := values[f.Key]
		if !ok || v == nil {
			if f.Required {
				return fmt.Errorf("字段 %s(%s) 为必填", f.Label, f.Key)
			}
			continue
		}
		if f.ReadOnly {
			continue // 只读字段不校验用户输入
		}
		if err := validateField(f, v); err != nil {
			return err
		}
	}
	return nil
}

func validateField(f FieldDef, v any) error {
	switch f.Type {
	case TypeText, TypeTextarea, TypeDate, TypeUser, TypeCI, TypeCascader:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为文本", f.Label, f.Key)
		}
		if err := checkLen(f, s); err != nil {
			return err
		}
		if err := checkRegex(f, s); err != nil {
			return err
		}
	case TypeSelect:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为单选值", f.Label, f.Key)
		}
		if len(f.Options) > 0 && !optionContains(f.Options, s) {
			return fmt.Errorf("字段 %s(%s) 取值 %q 不在可选范围内", f.Label, f.Key, s)
		}
	case TypeMultiSelect:
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为多选数组", f.Label, f.Key)
		}
		for _, item := range arr {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("字段 %s(%s) 多选值应为字符串", f.Label, f.Key)
			}
			if len(f.Options) > 0 && !optionContains(f.Options, s) {
				return fmt.Errorf("字段 %s(%s) 取值 %q 不在可选范围内", f.Label, f.Key, s)
			}
		}
	case TypeNumber:
		n, err := toFloat(v)
		if err != nil {
			return fmt.Errorf("字段 %s(%s) 应为数字", f.Label, f.Key)
		}
		if f.Validation != nil {
			if f.Validation.Min != nil && n < *f.Validation.Min {
				return fmt.Errorf("字段 %s(%s) 不能小于 %v", f.Label, f.Key, *f.Validation.Min)
			}
			if f.Validation.Max != nil && n > *f.Validation.Max {
				return fmt.Errorf("字段 %s(%s) 不能大于 %v", f.Label, f.Key, *f.Validation.Max)
			}
		}
	case TypeBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("字段 %s(%s) 应为布尔", f.Label, f.Key)
		}
	default:
		return fmt.Errorf("字段 %s(%s) 类型 %s 不支持", f.Label, f.Key, f.Type)
	}
	return nil
}

func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case json.Number:
		return n.Float64()
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
			return f, nil
		}
	}
	return 0, fmt.Errorf("not a number")
}

func optionContains(opts []FieldOption, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

func checkLen(f FieldDef, s string) error {
	if f.Validation == nil {
		return nil
	}
	n := len([]rune(s))
	if f.Validation.MinLen > 0 && n < f.Validation.MinLen {
		return fmt.Errorf("字段 %s(%s) 长度不能少于 %d", f.Label, f.Key, f.Validation.MinLen)
	}
	if f.Validation.MaxLen > 0 && n > f.Validation.MaxLen {
		return fmt.Errorf("字段 %s(%s) 长度不能多于 %d", f.Label, f.Key, f.Validation.MaxLen)
	}
	return nil
}

func checkRegex(f FieldDef, s string) error {
	if f.Validation == nil || f.Validation.Regex == "" {
		return nil
	}
	re, err := regexp.Compile(f.Validation.Regex)
	if err != nil {
		return nil // 无效正则跳过，不阻断
	}
	if !re.MatchString(s) {
		return fmt.Errorf("字段 %s(%s) 格式不正确", f.Label, f.Key)
	}
	return nil
}

// FieldVisible 按 VisibleWhen 表达式判断是否显示该字段。
// 支持 "key==value" / "key!=value" 两种简单形式；无表达式或无法解析时默认显示。
func FieldVisible(f FieldDef, values map[string]any) bool {
	expr := strings.TrimSpace(f.VisibleWhen)
	if expr == "" {
		return true
	}
	if strings.Contains(expr, "==") {
		parts := strings.SplitN(expr, "==", 2)
		k := strings.TrimSpace(parts[0])
		want := strings.TrimSpace(parts[1])
		v, ok := values[k]
		if !ok {
			return false
		}
		return fmt.Sprintf("%v", v) == want
	}
	if strings.Contains(expr, "!=") {
		parts := strings.SplitN(expr, "!=", 2)
		k := strings.TrimSpace(parts[0])
		want := strings.TrimSpace(parts[1])
		v, ok := values[k]
		if !ok {
			return true // 不存在视为不等
		}
		return fmt.Sprintf("%v", v) != want
	}
	return true
}

// MergeDefaults 将 schema 中声明的默认值注入到未填写的字段（原地修改 values）。
func MergeDefaults(schema *FormSchema, values map[string]any) {
	for _, f := range schema.Fields {
		if _, ok := values[f.Key]; ok {
			continue
		}
		if f.Default != nil {
			values[f.Key] = f.Default
		}
	}
}
