package form

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// 计算字段（TypeExpression）求值：此前 expression 仅把表达式文本只读展示，
// 本文件提供真正的求值能力 —— 服务端校验/落库时计算，前端渲染时实时计算（同语义 TS 移植）。
//
// 语法（递归下降）:
//   expr   := term (('+'|'-') term)*        // + 对两个字符串操作数做拼接, 否则数值加
//   term   := unary (('*'|'/'|'%') unary)*  // % 对数值取模
//   unary  := '-' unary | primary
//   primary:= NUMBER | 'STRING' | "STRING" | {field} | FUNC(args) | '(' expr ')'
//
// 字段引用统一写 {key}（与流程变量 ${path} 区分）；引用的值按数值优先解析（"3" 视作 3）。
// 内置函数: abs/round(x[,位数])/floor/ceil/min/max。
// 求值失败返回 error —— 调用方（ComputeExpressionFields）跳过该字段，不阻断表单。

// EvalExpression 求值一个表达式，返回 number(float64) 或 string。
func EvalExpression(expr string, values map[string]any) (any, error) {
	p := &exprParser{src: expr, values: values}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if p.pos < len(p.src) {
		return nil, fmt.Errorf("表达式存在多余字符: %q", p.src[p.pos:])
	}
	return v, nil
}

// ComputeExpressionFields 对 schema 中全部 expression 字段求值并就地写入 values。
// 使用展平字段视图（分组容器内的计算字段同样生效）；求值失败跳过该字段不阻断。
// expression 是只读字段：计算结果始终覆盖传入值，保证服务端落库与前端展示一致。
func ComputeExpressionFields(schema *FormSchema, values map[string]any) {
	if schema == nil || values == nil {
		return
	}
	for _, f := range FlattenFields(schema.Fields) {
		if f.Type != TypeExpression {
			continue
		}
		expr, _ := f.Default.(string)
		if strings.TrimSpace(expr) == "" {
			continue
		}
		if v, err := EvalExpression(expr, values); err == nil {
			values[f.Key] = v
		}
	}
}

type exprParser struct {
	src    string
	pos    int
	values map[string]any
}

func (p *exprParser) skipWS() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *exprParser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

func (p *exprParser) parseExpr() (any, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		p.skipWS()
		op := p.peek()
		if op != '+' && op != '-' {
			return left, nil
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		if op == '+' {
			if ls, lok := left.(string); lok {
				if rs, rok := right.(string); rok {
					left = ls + rs
					continue
				}
				left = ls + formatNumber(right)
				continue
			}
			if rs, rok := right.(string); rok {
				left = formatNumber(left) + rs
				continue
			}
		}
		lv, rv, nerr := numericPair(left, right)
		if nerr != nil {
			return nil, nerr
		}
		if op == '+' {
			left = lv + rv
		} else {
			left = lv - rv
		}
	}
}

func (p *exprParser) parseTerm() (any, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		p.skipWS()
		op := p.peek()
		if op != '*' && op != '/' && op != '%' {
			return left, nil
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		lv, rv, nerr := numericPair(left, right)
		if nerr != nil {
			return nil, nerr
		}
		switch op {
		case '*':
			left = lv * rv
		case '/':
			if rv == 0 {
				return nil, fmt.Errorf("除数为零")
			}
			left = lv / rv
		case '%':
			if int(rv) == 0 {
				return nil, fmt.Errorf("取模除数为零")
			}
			left = float64(int(lv) % int(rv))
		}
	}
}

func (p *exprParser) parseUnary() (any, error) {
	p.skipWS()
	if p.peek() == '-' {
		p.pos++
		v, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		n, nerr := toNumber(v)
		if nerr != nil {
			return nil, nerr
		}
		return -n, nil
	}
	return p.parsePrimary()
}

func (p *exprParser) parsePrimary() (any, error) {
	p.skipWS()
	c := p.peek()
	switch {
	case c == '(':
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if p.peek() != ')' {
			return nil, fmt.Errorf("缺少右括号")
		}
		p.pos++
		return v, nil
	case c == '{': // 字段引用 {key}
		end := strings.IndexByte(p.src[p.pos:], '}')
		if end < 0 {
			return nil, fmt.Errorf("字段引用缺少右花括号")
		}
		key := p.src[p.pos+1 : p.pos+end]
		p.pos += end + 1
		if key == "" {
			return nil, fmt.Errorf("字段引用为空")
		}
		v, ok := p.values[key]
		if !ok {
			return nil, fmt.Errorf("引用字段 %q 不存在", key)
		}
		return v, nil
	case c == '\'' || c == '"': // 字符串字面量
		p.pos++
		var b strings.Builder
		for p.pos < len(p.src) {
			ch := p.src[p.pos]
			if ch == '\\' && p.pos+1 < len(p.src) { // 简单转义: \' \" \\
				b.WriteByte(p.src[p.pos+1])
				p.pos += 2
				continue
			}
			if ch == c {
				p.pos++
				return b.String(), nil
			}
			b.WriteByte(ch)
			p.pos++
		}
		return nil, fmt.Errorf("字符串未闭合")
	case c >= '0' && c <= '9' || c == '.': // 数字
		start := p.pos
		for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '.') {
			p.pos++
		}
		n, err := strconv.ParseFloat(p.src[start:p.pos], 64)
		if err != nil {
			return nil, fmt.Errorf("非法数字 %q", p.src[start:p.pos])
		}
		return n, nil
	case unicode.IsLetter(rune(c)): // 函数
		start := p.pos
		for p.pos < len(p.src) && (unicode.IsLetter(rune(p.src[p.pos])) || unicode.IsDigit(rune(p.src[p.pos]))) {
			p.pos++
		}
		name := p.src[start:p.pos]
		p.skipWS()
		if p.peek() != '(' {
			return nil, fmt.Errorf("未知标识符 %q（字段引用请写 {key}）", name)
		}
		p.pos++
		var args []any
		if p.peek() == ')' {
			p.pos++
		} else {
			for {
				v, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, v)
				p.skipWS()
				if p.peek() == ',' {
					p.pos++
					continue
				}
				if p.peek() == ')' {
					p.pos++
					break
				}
				return nil, fmt.Errorf("函数参数缺少右括号")
			}
		}
		return applyFunc(name, args)
	default:
		return nil, fmt.Errorf("非法字符 %q", string(c))
	}
}

// applyFunc 内置函数。round(x) 取整, round(x, d) 保留 d 位小数。
func applyFunc(name string, args []any) (any, error) {
	nums := make([]float64, 0, len(args))
	for _, a := range args {
		n, err := toNumber(a)
		if err != nil {
			return nil, fmt.Errorf("函数 %s 参数需为数值: %v", name, err)
		}
		nums = append(nums, n)
	}
	switch name {
	case "abs":
		if len(nums) != 1 {
			return nil, fmt.Errorf("abs 需要 1 个参数")
		}
		return math.Abs(nums[0]), nil
	case "round":
		if len(nums) == 1 {
			return math.Round(nums[0]), nil
		}
		if len(nums) == 2 {
			shift := math.Pow(10, nums[1])
			return math.Round(nums[0]*shift) / shift, nil
		}
		return nil, fmt.Errorf("round 需要 1~2 个参数")
	case "floor":
		if len(nums) != 1 {
			return nil, fmt.Errorf("floor 需要 1 个参数")
		}
		return math.Floor(nums[0]), nil
	case "ceil":
		if len(nums) != 1 {
			return nil, fmt.Errorf("ceil 需要 1 个参数")
		}
		return math.Ceil(nums[0]), nil
	case "min":
		if len(nums) < 1 {
			return nil, fmt.Errorf("min 至少 1 个参数")
		}
		m := nums[0]
		for _, n := range nums[1:] {
			if n < m {
				m = n
			}
		}
		return m, nil
	case "max":
		if len(nums) < 1 {
			return nil, fmt.Errorf("max 至少 1 个参数")
		}
		m := nums[0]
		for _, n := range nums[1:] {
			if n > m {
				m = n
			}
		}
		return m, nil
	}
	return nil, fmt.Errorf("未知函数 %q", name)
}

// numericPair 双操作数归一为数值（字符串数字可隐式转换）。
func numericPair(l, r any) (float64, float64, error) {
	lv, err := toNumber(l)
	if err != nil {
		return 0, 0, err
	}
	rv, err := toNumber(r)
	if err != nil {
		return 0, 0, err
	}
	return lv, rv, nil
}

// toNumber 数值归一: number 直通; 字符串尝试解析; 其余报错（不静默当 0, 便于暴露配置错误）。
func toNumber(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case float32:
		return float64(t), nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, fmt.Errorf("空串不能作为数值")
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("%q 不能作为数值", t)
		}
		return n, nil
	case nil:
		return 0, fmt.Errorf("空值不能作为数值")
	}
	return 0, fmt.Errorf("%v 不能作为数值", v)
}

// formatNumber 数值 → 展示字符串（整数去小数尾、浮点保留有效位），供字符串拼接用。
func formatNumber(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	n, err := toNumber(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	if n == math.Trunc(n) && math.Abs(n) < 1e15 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}
