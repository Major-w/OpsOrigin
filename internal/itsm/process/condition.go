package process

import (
	"fmt"
	"strconv"
	"strings"
)

// EvaluateCondition 对网关步骤的简单条件表达式求值, 使流程能根据工单表单字段内容决定走向。
//
// 语法: `field op value [&& field op value ...]`
//   - op: == != contains "not contains" in "not in" exists > < >= <=
//   - field: 工单系统字段(risk_level/status/created_by_type/assignee 等)或表单自定义字段,
//     取值优先级: ctx["custom_fields"][field] > ctx[field]
//   - value: 引号字符串、数字、true/false、或 in 集合 `(a, b, c)`
//
// 示例: `risk_level == "high" && change_type in (标准变更, 紧急变更)`
func EvaluateCondition(expr string, ctx map[string]any) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false, fmt.Errorf("empty condition")
	}
	for _, part := range strings.Split(expr, "&&") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ok, err := evalSingle(part, ctx)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// condOps 按长度降序排列, 保证 "not contains" 先于 "contains" 匹配。
var condOps = []string{"not contains", "not in", "contains", "exists", ">=", "<=", "==", "!=", "in", ">", "<"}

// evalSingle 求值单个条件 `field op value`; 无 op 时视为存在性判断。
func evalSingle(cond string, ctx map[string]any) (bool, error) {
	cond = strings.TrimSpace(cond)
	sp := strings.IndexAny(cond, " \t")
	if sp < 0 {
		// 整个条件是字段名: 存在性判断
		_, ok := lookupCondField(cond, ctx)
		return ok, nil
	}
	field := cond[:sp]
	rest := strings.TrimSpace(cond[sp:])
	for _, op := range condOps {
		if strings.HasPrefix(rest, op) {
			value := strings.TrimSpace(rest[len(op):])
			return evalField(field, op, value, ctx)
		}
	}
	return false, fmt.Errorf("unknown operator in condition %q", cond)
}

type condValue struct {
	kind int // 0=string 1=number 2=bool
	str  string
	num  float64
}

// parseCondValue 解析条件右侧的值字面量。
func parseCondValue(raw string) condValue {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return condValue{kind: 0}
	}
	if raw[0] == '"' || raw[0] == '\'' {
		if len(raw) >= 2 && raw[len(raw)-1] == raw[0] {
			return condValue{kind: 0, str: raw[1 : len(raw)-1]}
		}
		return condValue{kind: 0, str: strings.Trim(raw, `"'`)}
	}
	if raw == "true" || raw == "false" {
		return condValue{kind: 2, str: raw}
	}
	if n, err := strconv.ParseFloat(raw, 64); err == nil {
		return condValue{kind: 1, num: n}
	}
	return condValue{kind: 0, str: raw}
}

// fieldCondValue 将字段实际值归一化为可比较的 condValue。
func fieldCondValue(v any) condValue {
	switch t := v.(type) {
	case string:
		return condValue{kind: 0, str: t}
	case bool:
		return condValue{kind: 2, str: strconv.FormatBool(t)}
	case float64:
		return condValue{kind: 1, num: t}
	case int:
		return condValue{kind: 1, num: float64(t)}
	case int64:
		return condValue{kind: 1, num: float64(t)}
	default:
		return condValue{kind: 0, str: fmt.Sprint(v)}
	}
}

// lookupCondField 查字段值: custom_fields 子 map 优先, 其次顶层; 支持点路径(如 custom_fields.change_type)。
func lookupCondField(field string, ctx map[string]any) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	if strings.Contains(field, ".") {
		seg := strings.Split(field, ".")
		var cur any = ctx
		for _, s := range seg {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			cur, ok = m[s]
			if !ok {
				return nil, false
			}
		}
		return cur, true
	}
	if cf, ok := ctx["custom_fields"].(map[string]any); ok {
		if v, ok := cf[field]; ok {
			return v, true
		}
	}
	v, ok := ctx[field]
	return v, ok
}

// parseInSet 解析 `(a, b, c)` 集合。
func parseInSet(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "(") || !strings.HasSuffix(raw, ")") {
		return nil, false
	}
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return []string{}, true
	}
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.Trim(strings.TrimSpace(p), `"'`))
	}
	return out, true
}

// evalField 执行具体比较。
func evalField(field, op, value string, ctx map[string]any) (bool, error) {
	v, ok := lookupCondField(field, ctx)
	switch op {
	case "exists":
		return ok, nil
	case "==":
		return ok && condEq(fieldCondValue(v), parseCondValue(value)), nil
	case "!=":
		if !ok {
			return true, nil // 字段不存在视为不等于任何值
		}
		return !condEq(fieldCondValue(v), parseCondValue(value)), nil
	case "contains":
		return ok && strings.Contains(fieldCondValue(v).str, parseCondValue(value).str), nil
	case "not contains":
		if !ok {
			return true, nil
		}
		return !strings.Contains(fieldCondValue(v).str, parseCondValue(value).str), nil
	case "in", "not in":
		set, valid := parseInSet(value)
		if !valid {
			return false, fmt.Errorf("invalid set %q for %q", value, op)
		}
		hit := ok && stringInSet(fieldCondValue(v).str, set)
		if op == "not in" {
			return !hit, nil
		}
		return hit, nil
	case ">", "<", ">=", "<=":
		if !ok {
			return false, nil
		}
		return condCompare(fieldCondValue(v), parseCondValue(value), op), nil
	}
	return false, fmt.Errorf("unknown operator %q", op)
}

func condEq(a, b condValue) bool {
	if a.kind == 1 && b.kind == 1 {
		return a.num == b.num
	}
	return a.str == b.str
}

func condCompare(a, b condValue, op string) bool {
	switch op {
	case ">":
		return a.num > b.num
	case "<":
		return a.num < b.num
	case ">=":
		return a.num >= b.num
	case "<=":
		return a.num <= b.num
	}
	return false
}

func stringInSet(s string, set []string) bool {
	for _, item := range set {
		if s == item {
			return true
		}
	}
	return false
}
