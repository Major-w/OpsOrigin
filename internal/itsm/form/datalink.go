package form

import (
	"context"
	"fmt"
	"strings"
)

// 赋值模式
const (
	SetFixed         = "fixed"          // 固定值（默认）
	SetCopyFrom      = "copy_from"      // 复制其它字段当前值
	SetFromCIAttr    = "ci_attr"        // 从 CMDB CI 属性回填（如 门店 → 该门店 IT 处理人）
	SetFromSubmitter = "submitter_attr" // 从提单人属性回填（如 department=部门/门店，用于门店 IT 自动派单）
)

// DataSet 单个赋值动作：条件命中后把值写入 Target 字段。
type DataSet struct {
	Target string `json:"target"`          // 目标字段 key
	Mode   string `json:"mode,omitempty"`  // fixed(默认) / copy_from / ci_attr / submitter_attr
	Value  string `json:"value,omitempty"` // fixed=写入的值；copy_from=来源字段 key；ci_attr=CI 属性名（如 owner）；submitter_attr=提单人属性名（如 department）
	// CIAttr 仅 Mode=ci_attr 时生效：CI 属性回填的来源声明。
	CIAttr *DataSetCIAttr `json:"ci_attr,omitempty"`
	// Submitter 仅 Mode=submitter_attr 时生效：提单人属性回填的来源声明。
	Submitter *DataSetSubmitter `json:"submitter,omitempty"`
}

// DataSetCIAttr ci_attr 模式的来源声明：以 NameField 字段的当前值作为 CI 名称，
// 在 CMDB 的 CIType 模型类型下查找该 CI，取其 attr（Value）属性写入目标字段。
type DataSetCIAttr struct {
	CIType    string `json:"ci_type"`            // CMDB 模型类型名（如 门店）
	NameField string `json:"name_field"`         // 表单字段 key，其值为 CI 名称
	Fallback  string `json:"fallback,omitempty"` // CI 未找到 / 属性为空时的兜底值（可空=不写）
}

// CIAttrResolver 从 CMDB 按「模型类型名 + CI 名称」取单个属性（ci_attr 回填通道）。
// form 包不依赖 cmdb：接口由 itsm 层适配实现并注入 Engine（分层单向）。
type CIAttrResolver interface {
	ResolveCIAttr(ctx context.Context, tenantID, typeName, ciName, attr string) (value string, found bool, err error)
}

// DataSetSubmitter submitter_attr 模式的来源声明：取当前提单人（请求 ctx 中的用户身份）
// 的 attr 属性（如 department）写入目标字段，用于「提单人 → 部门(门店) → 门店IT」派单链。
type DataSetSubmitter struct {
	Attr     string `json:"attr"`               // 提单人属性名（如 department / email）
	Fallback string `json:"fallback,omitempty"` // 属性为空 / 未登录 / 解析失败时的兜底值（可空=不写）
}

// SubmitterAttrResolver 按请求上下文取提单人单个属性（submitter_attr 回填通道）。
// 用户身份由实现方从 ctx 解析（itsm 适配层取 auth.UserIDFromContext），form 包保持零依赖。
type SubmitterAttrResolver interface {
	ResolveSubmitterAttr(ctx context.Context, tenantID, attr string) (value string, found bool, err error)
}

// DataLinkRule 数据联动规则："选 A 自动带出 B"。
// 例：环境=生产 → 影响范围 自动置为"全局"（fixed）；
//
//	选择配置项 → 受影响系统 复制该配置项名称（copy_from）。
//
// 规则按声明顺序执行，后执行的覆盖先执行的；目标字段须在本 schema 中声明（容错跳过）。
type DataLinkRule struct {
	ID         string      `json:"id,omitempty"`
	Name       string      `json:"name,omitempty"`
	Conditions []Condition `json:"conditions"`      // 触发条件（基于任意字段值）
	Logic      string      `json:"logic,omitempty"` // and(默认) / or
	Sets       []DataSet   `json:"sets"`            // 命中后的赋值动作列表
}

// DecisionTable 决策表：条件列 × 输出列的规则矩阵。
// 自上而下逐行求值，首个「启用且全部条件列命中」的行生效（其输出写入对应字段）。
// 典型用法：环境=生产,预发 且 变更类型=紧急 → 生效时间=非工作时间、审批链=P1。
type DecisionTable struct {
	ID            string        `json:"id,omitempty"`
	Name          string        `json:"name,omitempty"`
	ConditionKeys []string      `json:"condition_keys"` // 条件列（字段 key，按列顺序）
	OutputKeys    []string      `json:"output_keys"`    // 输出列（字段 key，按列顺序）
	Rows          []DecisionRow `json:"rows"`
	// AssetRef 非空表示本表引用「决策表资产」而非内嵌定义。
	// 读取/求值前由 ExpandDecisionRefs 填入已发布版本内容，但标记始终保留；
	// 保存前由 NormalizeDecisionRefs 剥离内容，避免把快照固化进表单。
	AssetRef *DecisionTableAssetRef `json:"asset_ref,omitempty"`
}

// DecisionRow 决策表行：Cells 空串=通配（不限该列）；"a,b"=任一命中（逗号分隔）；
// Outputs 为该行命中后写入对应输出列的值；Enabled 缺省启用。
type DecisionRow struct {
	ID      string            `json:"id,omitempty"`
	Cells   map[string]string `json:"cells,omitempty"`
	Outputs map[string]string `json:"outputs,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

func rowEnabled(r DecisionRow) bool { return r.Enabled == nil || *r.Enabled }

// cellHits 决策表单元格匹配：空=通配；"a,b" 任一命中（逗号分隔，与 OpIn 同语义）；
// （对标量与数组都成立——多选字段任一元素命中即算命中，与 evalCondition 同语义）。
func cellHits(cell string, raw any) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return true
	}
	set := splitList(cell)
	return matchAny(raw, func(s string) bool { return containsStr(set, s) })
}

// interpolate 把字符串中的 ${path} 占位符按 values 求值替换（点路径逐层下钻 map）。
// 与流程引擎的 subst 语义一致；form 包保持零依赖，故独立实现。
// 用于决策表输出列：使输出可引用本行求值前的其它变量（含前序决策表的输出），
// 从而拼出动态值（如 "${store}-${role_key}-l1" 形式的组名），避免逐组合硬编码。
func interpolate(s string, values map[string]any) string {
	if !strings.Contains(s, "${") || len(values) == 0 {
		return s
	}
	var b strings.Builder
	rest := s
	for {
		start := strings.Index(rest, "${")
		if start < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:start])
		tail := rest[start+2:]
		end := strings.Index(tail, "}")
		if end < 0 {
			b.WriteString(rest[start:])
			break
		}
		b.WriteString(fmt.Sprint(lookupPath(values, tail[:end])))
		rest = tail[end+1:]
	}
	return b.String()
}

// lookupPath 按点路径逐层下钻 map 取值；缺失/非 map 返回空串（容错不阻断）。
func lookupPath(values map[string]any, path string) any {
	cur := any(values)
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[seg]
		if !ok {
			return ""
		}
	}
	return cur
}

// ApplyDataRules 应用数据联动与决策表推导（纯函数）。
// 返回推导后的值副本（不修改入参）与触发的规则说明列表（供日志/前端提示）。
// 求值顺序：先 DataLinks（声明顺序），再 DecisionTables（声明顺序，各自取首个命中行）。
// ci_attr 模式无 resolver 时跳过（保守不写），保持既有纯函数语义。
func ApplyDataRules(schema *FormSchema, values map[string]any) (map[string]any, []string) {
	return ApplyDataRulesWithCI(nil, "", schema, values, nil)
}

// ApplyDataRulesWithCI 带 CMDB 属性回填通道的数据联动推导。
// resolver 为 nil 时 ci_attr 赋值跳过；解析失败/未找到按 Fallback 处理（无 Fallback 不写）。
func ApplyDataRulesWithCI(ctx context.Context, tenantID string, schema *FormSchema, values map[string]any, resolver CIAttrResolver) (map[string]any, []string) {
	return ApplyDataRulesFull(ctx, tenantID, schema, values, resolver, nil)
}

// ApplyDataRulesFull 完整数据联动推导：CMDB 属性（ci_attr）+ 提单人属性（submitter_attr）双通道。
// 任一 resolver 为 nil 时对应模式跳过；解析失败/未找到按各自 Fallback 处理（无 Fallback 不写）。
func ApplyDataRulesFull(ctx context.Context, tenantID string, schema *FormSchema, values map[string]any, ciResolver CIAttrResolver, submitterResolver SubmitterAttrResolver) (map[string]any, []string) {
	out := make(map[string]any, len(values)+4)
	for k, v := range values {
		out[k] = v
	}
	var fired []string
	if schema == nil {
		return out, fired
	}
	declared := make(map[string]bool, len(schema.Fields))
	for _, f := range schema.Fields {
		declared[f.Key] = true
	}

	// 1) 数据联动规则
	for _, r := range schema.DataLinks {
		if len(r.Sets) == 0 || !EvalConditions(r.Conditions, r.Logic, out) {
			continue
		}
		applied := 0
		for _, s := range r.Sets {
			target := strings.TrimSpace(s.Target)
			if target == "" || !declared[target] {
				continue // 目标字段不在本 schema（配置漂移，容错跳过）
			}
			switch strings.TrimSpace(strings.ToLower(s.Mode)) {
			case SetCopyFrom:
				src := strings.TrimSpace(s.Value)
				if src == "" || src == target {
					continue
				}
				if v, ok := out[src]; ok && v != nil {
					out[target] = v
					applied++
				}
			case SetFromCIAttr:
				v, ok := resolveCIAttrSet(ctx, tenantID, s, out, ciResolver)
				if !ok {
					continue
				}
				out[target] = v
				applied++
			case SetFromSubmitter:
				v, ok := resolveSubmitterSet(ctx, tenantID, s, submitterResolver)
				if !ok {
					continue
				}
				out[target] = v
				applied++
			default: // fixed
				out[target] = s.Value
				applied++
			}
		}
		if applied == 0 {
			continue
		}
		label := strings.TrimSpace(r.Name)
		if label == "" {
			label = r.ID
		}
		fired = append(fired, "联动:"+label)
	}

	// 2) 决策表：首个命中且启用的行生效
	for _, dt := range schema.DecisionTables {
		for ri, row := range dt.Rows {
			if !rowEnabled(row) {
				continue
			}
			hit := true
			for _, k := range dt.ConditionKeys {
				if !cellHits(row.Cells[k], out[k]) {
					hit = false
					break
				}
			}
			if !hit {
				continue
			}
			for _, k := range dt.OutputKeys {
				if !declared[k] {
					continue
				}
				if v, ok := row.Outputs[k]; ok {
					// 输出支持 ${field} 插值：可引用本行求值前的所有变量（含前序决策表输出）。
					out[k] = interpolate(v, out)
				}
			}
			label := strings.TrimSpace(dt.Name)
			if label == "" {
				label = dt.ID
			}
			// 记录命中行号，供试算/排障定位「为什么派到这个人」。
			fired = append(fired, fmt.Sprintf("决策表:%s#第%d行", label, ri+1))
			break
		}
	}
	return out, fired
}

// ValidateDecisionTables 决策表配置校验（纯函数，保存 schema 时调用）。
// 拦截会导致运行期静默失效的硬错误；易被忽略的可维护性风险以 warnings 返回。
// 硬错误：条件列/输出列为空、列名重复、列指向未声明字段、无行、条件单元格写了非条件列。
// 警告：无兜底行（全空条件单元格）、输出列缺值、所有行均停用。
func ValidateDecisionTables(schema *FormSchema) ([]string, error) {
	var warnings []string
	if schema == nil || len(schema.DecisionTables) == 0 {
		return warnings, nil
	}
	declared := make(map[string]bool, len(schema.Fields))
	for _, f := range schema.Fields {
		declared[f.Key] = true
	}
	var errs []string
	for i, dt := range schema.DecisionTables {
		name := strings.TrimSpace(dt.Name)
		if name == "" {
			name = fmt.Sprintf("表%d", i+1)
		}
		prefix := "决策表[" + name + "]"
		if dt.AssetRef != nil {
			if strings.TrimSpace(dt.AssetRef.AssetID) == "" {
				errs = append(errs, prefix+" 引用的决策表资产 ID 为空")
			}
			if dt.AssetRef.Version < 0 {
				errs = append(errs, prefix+" 引用的版本号不能为负数")
			}
			// 纯引用态（尚未展开）：内容由已发布版本在求值前注入，保存态只有标记，
			// 列/行校验应针对资产本身（发布端点校验），此处只校验引用合法性。
			// 展开态（GET 后回传、已带列与行）继续走下方完整校验，确保列在表单字段中声明。
			if len(dt.Rows) == 0 && len(dt.ConditionKeys) == 0 && len(dt.OutputKeys) == 0 {
				continue
			}
		}
		if len(dt.ConditionKeys) == 0 {
			errs = append(errs, prefix+" 未配置条件列")
		}
		if len(dt.OutputKeys) == 0 {
			errs = append(errs, prefix+" 未配置输出列")
		}
		seen := map[string]bool{}
		for _, k := range append(append([]string{}, dt.ConditionKeys...), dt.OutputKeys...) {
			if k == "" {
				errs = append(errs, prefix+" 存在空列名")
				continue
			}
			if seen[k] {
				errs = append(errs, fmt.Sprintf("%s 列 %q 重复（条件列与输出列不得重复/同名）", prefix, k))
				continue
			}
			seen[k] = true
			if !declared[k] {
				errs = append(errs, fmt.Sprintf("%s 列 %q 未在表单字段中声明，运行期会被静默跳过", prefix, k))
			}
		}
		if len(dt.Rows) == 0 {
			errs = append(errs, prefix+" 未配置任何行")
			continue
		}
		condSet := make(map[string]bool, len(dt.ConditionKeys))
		outSet := make(map[string]bool, len(dt.OutputKeys))
		for _, k := range dt.ConditionKeys {
			condSet[k] = true
		}
		for _, k := range dt.OutputKeys {
			outSet[k] = true
		}
		hasFallback := false
		enabledRows := 0
		for ri, row := range dt.Rows {
			for k := range row.Cells {
				if !condSet[k] {
					errs = append(errs, fmt.Sprintf("%s 第%d行 条件单元格 %q 不属于条件列", prefix, ri+1, k))
				}
			}
			for k := range row.Outputs {
				if !outSet[k] {
					errs = append(errs, fmt.Sprintf("%s 第%d行 输出单元格 %q 不属于输出列", prefix, ri+1, k))
				}
			}
			if !rowEnabled(row) {
				continue
			}
			enabledRows++
			allWild := true
			for _, k := range dt.ConditionKeys {
				if strings.TrimSpace(row.Cells[k]) != "" {
					allWild = false
					break
				}
			}
			if allWild {
				hasFallback = true
			}
			for _, k := range dt.OutputKeys {
				if _, ok := row.Outputs[k]; !ok {
					warnings = append(warnings, fmt.Sprintf("%s 第%d行 未填写输出列 %q（该列保持原值）", prefix, ri+1, k))
				}
			}
		}
		if enabledRows == 0 {
			warnings = append(warnings, prefix+" 所有行均已停用，运行期不会产生任何输出")
		}
		if !hasFallback && enabledRows > 0 {
			warnings = append(warnings, prefix+" 没有兜底行（条件单元格全空），条件全不命中时输出列无值")
		}
	}
	if len(errs) > 0 {
		return warnings, fmt.Errorf("决策表配置有误: %s", strings.Join(errs, "; "))
	}
	return warnings, nil
}

// resolveCIAttrSet 执行 ci_attr 赋值：以 NameField 当前值为 CI 名称查 CMDB 属性。
// 返回 (写入值, 是否写入)。未配置 resolver / 名称字段为空 / 解析失败 / 未找到且无
// Fallback 时不写（保守跳过，与目标字段缺省语义一致）。
func resolveCIAttrSet(ctx context.Context, tenantID string, s DataSet, out map[string]any, resolver CIAttrResolver) (string, bool) {
	if s.CIAttr == nil || resolver == nil || ctx == nil {
		return "", false
	}
	nameField := strings.TrimSpace(s.CIAttr.NameField)
	if nameField == "" || nameField == s.Target {
		return "", false
	}
	raw, ok := out[nameField]
	if !ok || raw == nil {
		return "", false
	}
	ciName := strings.TrimSpace(fmt.Sprint(raw))
	if ciName == "" {
		return "", false
	}
	attr := strings.TrimSpace(s.Value)
	if attr == "" {
		return "", false
	}
	v, found, err := resolver.ResolveCIAttr(ctx, tenantID, strings.TrimSpace(s.CIAttr.CIType), ciName, attr)
	if err != nil {
		return "", false // 解析失败保守跳过（联动失败不阻断提交）
	}
	if found && strings.TrimSpace(v) != "" {
		return v, true
	}
	if s.CIAttr.Fallback != "" {
		return s.CIAttr.Fallback, true
	}
	return "", false
}

// resolveSubmitterSet 执行 submitter_attr 赋值：取提单人（实现方从 ctx 解析身份）的属性。
// 返回 (写入值, 是否写入)。未配置 resolver / 属性名为空 / 解析失败 / 无值且无
// Fallback 时不写（保守跳过，与 ci_attr 语义一致；联动失败不阻断提交）。
func resolveSubmitterSet(ctx context.Context, tenantID string, s DataSet, resolver SubmitterAttrResolver) (string, bool) {
	if s.Submitter == nil || resolver == nil || ctx == nil {
		return "", false
	}
	attr := strings.TrimSpace(s.Submitter.Attr)
	if attr == "" {
		return "", false
	}
	v, found, err := resolver.ResolveSubmitterAttr(ctx, tenantID, attr)
	if err != nil {
		return "", false
	}
	if found && strings.TrimSpace(v) != "" {
		return v, true
	}
	if s.Submitter.Fallback != "" {
		return s.Submitter.Fallback, true
	}
	return "", false
}
