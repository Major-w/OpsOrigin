package form

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Engine 表单校验引擎：持有 SchemaStore，按作用域取 schema 并校验值。
// 可选注入 SourceResolver，用于校验 source 动态选项字段（cmdb:* / iam:*）。
type Engine struct {
	store    SchemaStore
	resolver SourceResolver
}

func NewEngine(s SchemaStore) *Engine {
	return &Engine{store: s}
}

// SetSourceResolver 注入动态选项解析器（可为 nil，此时带 source 的字段不做取值范围校验）。
func (e *Engine) SetSourceResolver(r SourceResolver) {
	e.resolver = r
}

// SourceResolver 返回已注入的解析器（供上层 form-options 端点复用）。
func (e *Engine) SourceResolver() SourceResolver {
	return e.resolver
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
	return e.ValidateValues(ctx, tenantID, schema, values)
}

// ValidateValues 带 resolver 的校验入口：带 source 的 select/multiselect 会实时拉取选项后校验。
// resolver 缺失或解析失败时按"宽松"处理（不阻断工单流转），与"无 schema 放行"策略一致。
// 校验基于 EffectiveStates（字段自身规则 + BehaviorRules 联动叠加）：
// 隐藏字段跳过校验，只读/禁用字段不做取值范围校验（保留必填判定——只读但必填且为空仍拦截）。
func (e *Engine) ValidateValues(ctx context.Context, tenantID string, schema *FormSchema, values map[string]any) error {
	if schema == nil {
		return nil
	}
	states := EffectiveStates(schema, values)
	for _, f := range FlattenFields(schema.Fields) {
		st, hasState := states[f.Key]
		if hasState && !st.Visible {
			continue
		}
		if !hasState && !FieldVisible(f, values) {
			continue
		}
		v, present := values[f.Key]
		// 空判定统一走 isEmptyValue：多选 / 级联提交的空数组同样算"未填"
		if !present || isEmptyValue(v) {
			required := f.Required
			if hasState {
				required = st.Required
			}
			if required {
				return fmt.Errorf("字段 %s(%s) 为必填", f.Label, f.Key)
			}
			continue
		}
		if hasState && (st.ReadOnly || st.Disabled) {
			continue // 只读/禁用字段不校验用户输入
		}
		if !hasState && f.ReadOnly {
			continue
		}
		if err := validateFieldWith(f, v, e.effectiveOptions(ctx, tenantID, f)); err != nil {
			return err
		}
	}
	return nil
}

// ValidateValuesFull 完整校验：数据联动/决策表推导 + 计算字段求值 + 字段级校验 + 跨字段组合校验。
// 先用 ApplyDataRules 推导出派生值（"选 A 自动带出 B"），再对 expression 字段求值落定，
// 字段校验与组合校验均基于推导后的完整值进行——前端未回传派生字段时（如 API 直调），
// 后端也能按联动结果放行必填判定。
// 返回 computed = 推导+计算后的完整值（调用方应合并落库，保证服务端计算结果与前端展示一致）。
// block 级组合规则命中时返回 error；warn 级命中以 warnings 返回（不阻断）。
func (e *Engine) ValidateValuesFull(ctx context.Context, tenantID string, schema *FormSchema, values map[string]any) (warnings []string, computed map[string]any, err error) {
	if schema == nil {
		return nil, nil, nil
	}
	vals, _ := ApplyDataRules(schema, values)
	ComputeExpressionFields(schema, vals) // 计算字段: 就地写入求值结果, 失败字段跳过不阻断
	if err = e.ValidateValues(ctx, tenantID, schema, vals); err != nil {
		return nil, nil, err
	}
	cr := ValidateComboRules(schema, vals)
	return cr.Warnings, vals, cr.Err
}

// effectiveOptions 计算字段的有效选项集合：优先静态 Options，其次按 Source 实时解析。
// 解析失败返回 nil，调用方视作"不限制取值范围"。
func (e *Engine) effectiveOptions(ctx context.Context, tenantID string, f FieldDef) []FieldOption {
	if len(f.Options) > 0 {
		return f.Options
	}
	if f.Source == "" || e.resolver == nil {
		return nil
	}
	opts, err := e.resolver.Resolve(ctx, tenantID, f.Source)
	if err != nil {
		return nil // 上游抖动不阻断
	}
	return opts
}

// ValidateSchema 纯函数校验（静态 options），便于单测，返回首个错误。
// 与 ValidateValues 同样走 EffectiveStates（含 BehaviorRules 联动）。
func ValidateSchema(schema *FormSchema, values map[string]any) error {
	states := EffectiveStates(schema, values)
	for _, f := range FlattenFields(schema.Fields) {
		st, hasState := states[f.Key]
		if hasState && !st.Visible {
			continue
		}
		if !hasState && !FieldVisible(f, values) {
			continue
		}
		v, present := values[f.Key]
		// 空判定统一走 isEmptyValue：多选 / 级联提交的空数组同样算"未填"
		if !present || isEmptyValue(v) {
			required := f.Required
			if hasState {
				required = st.Required
			}
			if required {
				return fmt.Errorf("字段 %s(%s) 为必填", f.Label, f.Key)
			}
			continue
		}
		if hasState && (st.ReadOnly || st.Disabled) {
			continue // 只读/禁用字段不校验用户输入
		}
		if !hasState && f.ReadOnly {
			continue
		}
		if err := validateFieldWith(f, v, f.Options); err != nil {
			return err
		}
	}
	return nil
}

// validateFieldWith 校验单个字段值；opts 为该字段的有效选项集合（静态或 source 解析所得），
// 为空表示不限制取值范围。
func validateFieldWith(f FieldDef, v any, opts []FieldOption) error {
	switch f.Type {
	case TypeText, TypeTextarea, TypeDate, TypeUser, TypeCI:
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
	case TypeCascader:
		path, ok := cascaderPath(v)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为级联路径（字符串数组）", f.Label, f.Key)
		}
		for _, seg := range path {
			if err := checkLen(f, seg); err != nil {
				return err
			}
			if err := checkRegex(f, seg); err != nil {
				return err
			}
		}
		// opts 为完整选项树时才做归属校验；允许只选到中间层级后提交
		if len(opts) > 0 && !treeContainsPath(opts, path) {
			return fmt.Errorf("字段 %s(%s) 取值 %q 不在可选范围内", f.Label, f.Key, strings.Join(path, "/"))
		}
	case TypeSelect:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为单选值", f.Label, f.Key)
		}
		if len(opts) > 0 && !optionContains(opts, s) {
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
			if len(opts) > 0 && !optionContains(opts, s) {
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
	case TypeDatetime:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为日期时间", f.Label, f.Key)
		}
		if _, err := parseDatetime(s); err != nil {
			return fmt.Errorf("字段 %s(%s) 日期时间格式不正确", f.Label, f.Key)
		}
	case TypeDuration:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为时长", f.Label, f.Key)
		}
		if _, err := parseDuration(s); err != nil {
			return fmt.Errorf("字段 %s(%s) 时长格式不正确(如 1h / 30m / 3d)", f.Label, f.Key)
		}
	case TypeAttachment:
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为附件 ID 数组", f.Label, f.Key)
		}
		for _, item := range arr {
			switch item.(type) {
			case string, float64, int, int64, json.Number:
			default:
				return fmt.Errorf("字段 %s(%s) 附件 ID 应为数字或字符串", f.Label, f.Key)
			}
		}
		// 数量约束兜底：单文件体积/类型在上传阶段由前端拦截，
		// 这里只校验服务端能独立判定的数量维度，防止绕过前端直接提交。
		if f.Multiple != nil && !*f.Multiple && len(arr) > 1 {
			return fmt.Errorf("字段 %s(%s) 只允许上传 1 个附件", f.Label, f.Key)
		}
		if f.MaxCount > 0 && len(arr) > f.MaxCount {
			return fmt.Errorf("字段 %s(%s) 最多上传 %d 个附件", f.Label, f.Key, f.MaxCount)
		}
	case TypeRichText:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("字段 %s(%s) 应为富文本", f.Label, f.Key)
		}
	case TypeTable:
		rows, ok := v.([]any)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为表格(二维数组)", f.Label, f.Key)
		}
		if len(f.Columns) > 0 {
			return validateStructuredTable(f, rows)
		}
		for i, row := range rows {
			cols, ok := row.([]any)
			if !ok {
				return fmt.Errorf("字段 %s(%s) 第 %d 行应为数组", f.Label, f.Key, i+1)
			}
			for _, c := range cols {
				if _, ok := c.(string); !ok {
					if _, err := toFloat(c); err != nil {
						return fmt.Errorf("字段 %s(%s) 表格单元格应为文本或数字", f.Label, f.Key)
					}
				}
			}
		}
	case TypePassword, TypeTime, TypeLink, TypeExpression:
		// 字符串族：密码/时间/链接/表达式均按文本校验（长度 + 可选正则）。
		// 表达式通常是只读字段，校验在 ReadOnly 分支已提前 return，这里仅兜底。
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
	case TypeRating:
		// 评分：1~5 的数值（对标 SN 5-point scale）；min/max 可用 Validation 覆盖默认范围。
		n, err := toFloat(v)
		if err != nil {
			return fmt.Errorf("字段 %s(%s) 应为评分数字", f.Label, f.Key)
		}
		lo, hi := 1.0, 5.0
		if f.Validation != nil {
			if f.Validation.Min != nil {
				lo = *f.Validation.Min
			}
			if f.Validation.Max != nil {
				hi = *f.Validation.Max
			}
		}
		if n < lo || n > hi {
			return fmt.Errorf("字段 %s(%s) 评分须在 %v~%v 之间", f.Label, f.Key, lo, hi)
		}
	case TypeDateRange:
		// 日期区间：值为 "开始~结束"（与 timerange 同构），两端须为合法日期且开始不晚于结束。
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为日期区间文本", f.Label, f.Key)
		}
		parts := strings.Split(s, "~")
		if len(parts) != 2 {
			return fmt.Errorf("字段 %s(%s) 应为 \"开始~结束\" 格式", f.Label, f.Key)
		}
		start, err1 := parseDatetime(strings.TrimSpace(parts[0]))
		end, err2 := parseDatetime(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			return fmt.Errorf("字段 %s(%s) 含非法日期", f.Label, f.Key)
		}
		if start.After(end) {
			return fmt.Errorf("字段 %s(%s) 开始日期不能晚于结束日期", f.Label, f.Key)
		}
	case TypeNote:
		// 说明区块：纯展示不产生值（前端不提交），即使误传也放行不校验。
		return nil
	case TypeTimeRange:
		// 时间段：值为 "开始~结束"（HH:mm~HH:mm）的纯文本，宽松按字符串校验。
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 应为时间段文本", f.Label, f.Key)
		}
		if err := checkLen(f, s); err != nil {
			return err
		}
	default:
		return fmt.Errorf("字段 %s(%s) 类型 %s 不支持", f.Label, f.Key, f.Type)
	}
	return nil
}

// validateStructuredTable 结构化表格（MRVS）校验：每行必须是对象（列 key → 单元格文本），
// 必填列逐行核对非空；未知列忽略（向前兼容列定义变更），单元格允许数字（统一转文本语义）。
func validateStructuredTable(f FieldDef, rows []any) error {
	for i, row := range rows {
		obj, ok := row.(map[string]any)
		if !ok {
			return fmt.Errorf("字段 %s(%s) 第 %d 行应为对象(列 key → 值)", f.Label, f.Key, i+1)
		}
		for _, col := range f.Columns {
			raw, _ := obj[col.Key] // key 缺失同样按空值处理（必填报错）
			if isEmptyValue(raw) {
				if col.Required {
					return fmt.Errorf("字段 %s(%s) 第 %d 行的列 %s(%s) 为必填", f.Label, f.Key, i+1, col.Label, col.Key)
				}
				continue
			}
			s, isStr := raw.(string)
			if !isStr {
				if _, err := toFloat(raw); err != nil {
					return fmt.Errorf("字段 %s(%s) 第 %d 行的列 %s(%s) 应为文本或数字", f.Label, f.Key, i+1, col.Label, col.Key)
				}
				s = fmt.Sprintf("%v", raw)
			}
			if col.Required && strings.TrimSpace(s) == "" {
				return fmt.Errorf("字段 %s(%s) 第 %d 行的列 %s(%s) 为必填", f.Label, f.Key, i+1, col.Label, col.Key)
			}
		}
	}
	return nil
}

// parseDatetime 宽松解析日期时间: 兼容 RFC3339 与 "2006-01-02 15:04:05" 及纯日期。
func parseDatetime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid datetime %q", s)
}

// parseDuration 解析时长(1h / 30m / 3d / 1h30m), 返回秒数。与 process 包语义一致。
func parseDuration(s string) (float64, error) {
	re := regexp.MustCompile(`^(\d+(?:\.\d+)?[smhd])+$`)
	if !re.MatchString(s) {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	var total float64
	unitRe := regexp.MustCompile(`(\d+(?:\.\d+)?)([smhd])`)
	for _, m := range unitRe.FindAllStringSubmatch(s, -1) {
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, err
		}
		switch m[2] {
		case "s":
			total += v
		case "m":
			total += v * 60
		case "h":
			total += v * 3600
		case "d":
			total += v * 86400
		}
	}
	return total, nil
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

// cascaderPath 把级联值归一化成路径片段。
// 新版前端提交 []string / []any；旧版是文本输入，按 "业务系统/应用/实例" 斜杠分隔兼容。
func cascaderPath(v any) ([]string, bool) {
	appendSeg := func(out []string, s string) []string {
		if s = strings.TrimSpace(s); s != "" {
			return append(out, s)
		}
		return out
	}
	switch t := v.(type) {
	case string:
		parts := strings.Split(t, "/")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			out = appendSeg(out, p)
		}
		return out, true
	case []string:
		out := make([]string, 0, len(t))
		for _, p := range t {
			out = appendSeg(out, p)
		}
		return out, true
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = appendSeg(out, s)
		}
		return out, true
	}
	return nil, false
}

// treeContainsPath 自顶向下逐级匹配路径；路径可短于树深（选到中间层级即可提交）。
func treeContainsPath(opts []FieldOption, path []string) bool {
	cur := opts
	for _, seg := range path {
		idx := -1
		for i, o := range cur {
			if o.Value == seg {
				idx = i
				break
			}
		}
		if idx < 0 {
			return false
		}
		cur = cur[idx].Children
	}
	return true
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

// FieldVisible 判断字段是否显示。
// 优先使用 VisibleWhenRules（多规则 + and/or 组合）；为空时回退旧版 VisibleWhen 单条表达式；
// 都为空则显示。任何无法解析的规则一律按"显示"处理，避免配置写错把字段藏没了。
func FieldVisible(f FieldDef, values map[string]any) bool {
	if len(f.VisibleWhenRules) > 0 {
		return EvalConditions(f.VisibleWhenRules, f.VisibleLogic, values)
	}
	rules := ParseVisibleWhen(f.VisibleWhen)
	if len(rules) == 0 {
		return true
	}
	return EvalConditions(rules, LogicAnd, values)
}

// ParseVisibleWhen 把旧版单条表达式 "key==value" / "key!=value" 解析成 Condition 列表。
// 无法解析返回 nil（调用方视作无条件）。
func ParseVisibleWhen(expr string) []Condition {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	for _, pair := range []struct {
		sep string
		op  string
	}{{"!=", OpNeq}, {"==", OpEq}} {
		if idx := strings.Index(expr, pair.sep); idx > 0 {
			return []Condition{{
				Field: strings.TrimSpace(expr[:idx]),
				Op:    pair.op,
				Value: strings.TrimSpace(expr[idx+len(pair.sep):]),
			}}
		}
	}
	return nil
}

// EvalConditions 组合求值：logic=or 时任一命中即显示，否则要求全部命中。
func EvalConditions(rules []Condition, logic string, values map[string]any) bool {
	if len(rules) == 0 {
		return true
	}
	or := strings.EqualFold(strings.TrimSpace(logic), LogicOr)
	for _, c := range rules {
		hit := evalCondition(c, values)
		if or && hit {
			return true
		}
		if !or && !hit {
			return false
		}
	}
	return !or
}

func evalCondition(c Condition, values map[string]any) bool {
	field := strings.TrimSpace(c.Field)
	if field == "" {
		return true
	}
	raw, exists := values[field]
	op := strings.TrimSpace(strings.ToLower(c.Op))
	if op == "" {
		op = OpEq
	}

	switch op {
	case OpEmpty:
		return !exists || isEmptyValue(raw)
	case OpNotEmpty:
		return exists && !isEmptyValue(raw)
	case OpIsTrue:
		return truthy(raw)
	case OpIsFalse:
		return exists && !truthy(raw)
	}

	if !exists {
		// 字段还没填：不等/不属于视为成立，其余不成立
		return op == OpNeq || op == OpNotIn
	}

	switch op {
	case OpEq:
		return matchAny(raw, func(s string) bool { return s == c.Value })
	case OpNeq:
		return !matchAny(raw, func(s string) bool { return s == c.Value })
	case OpIn:
		set := splitList(c.Value)
		return matchAny(raw, func(s string) bool { return containsStr(set, s) })
	case OpNotIn:
		set := splitList(c.Value)
		return !matchAny(raw, func(s string) bool { return containsStr(set, s) })
	case OpContains:
		return matchAny(raw, func(s string) bool { return strings.Contains(s, c.Value) })
	case OpGt, OpGte, OpLt, OpLte:
		left, err1 := toFloat(raw)
		right, err2 := toFloat(c.Value)
		if err1 != nil || err2 != nil {
			return false
		}
		switch op {
		case OpGt:
			return left > right
		case OpGte:
			return left >= right
		case OpLt:
			return left < right
		default:
			return left <= right
		}
	}
	return true // 未知运算符不隐藏字段
}

// matchAny 对标量按自身比较，对数组（多选值）只要任一元素命中即算命中。
func matchAny(raw any, pred func(string) bool) bool {
	if arr, ok := raw.([]any); ok {
		for _, item := range arr {
			if pred(fmt.Sprintf("%v", item)) {
				return true
			}
		}
		return false
	}
	if arr, ok := raw.([]string); ok {
		for _, item := range arr {
			if pred(item) {
				return true
			}
		}
		return false
	}
	return pred(fmt.Sprintf("%v", raw))
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	}
	return false
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "1" || s == "yes"
	case nil:
		return false
	}
	if n, err := toFloat(v); err == nil {
		return n != 0
	}
	return false
}

// MergeDefaults 将 schema 中声明的默认值注入到未填写的字段（原地修改 values）。
func MergeDefaults(schema *FormSchema, values map[string]any) {
	MergeDefaultsAs(schema, values, "")
}

// MergeDefaultsAs 同 MergeDefaults，并解析动态默认值 token（对标 SN g_user/日期默认值）：
//   - {{current_user}}     当前用户名（user 为空时跳过注入，避免落脏数据）
//   - {{current_date}}     当前日期 YYYY-MM-DD
//   - {{current_datetime}} 当前时间 2006-01-02 15:04
//
// token 可出现在字符串默认值的任意位置（整串或混排）；非字符串默认值不受影响。
func MergeDefaultsAs(schema *FormSchema, values map[string]any, currentUser string) {
	now := time.Now()
	for _, f := range FlattenFields(schema.Fields) {
		if _, ok := values[f.Key]; ok {
			continue
		}
		if f.Default == nil {
			continue
		}
		if s, ok := f.Default.(string); ok && strings.Contains(s, "{{") {
			v := ResolveDefaultTokens(s, currentUser, now)
			if v == nil {
				continue
			}
			values[f.Key] = v
			continue
		}
		values[f.Key] = f.Default
	}
}

// ResolveDefaultTokens 把默认值字符串里的动态 token 解析成实际值。
// 解析后为空串（如 {{current_user}} 且无用户上下文）返回 nil，调用方跳过注入。
func ResolveDefaultTokens(s, currentUser string, now time.Time) any {
	if !strings.Contains(s, "{{") {
		return s
	}
	r := strings.NewReplacer(
		"{{current_user}}", currentUser,
		"{{current_date}}", now.Format("2006-01-02"),
		"{{current_datetime}}", now.Format("2006-01-02 15:04"),
	)
	out := r.Replace(s)
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return out
}
