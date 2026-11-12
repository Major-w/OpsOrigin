package form

import (
	"fmt"
	"strings"
)

// UI 联动动作（对标竞品"UI 规则"四态：显隐 / 必填 / 只读 / 可编辑）。
// 同一条规则可命中多个目标字段；规则按声明顺序叠加，后声明覆盖先声明。
const (
	ActShow     = "show"     // 显示（解除隐藏）
	ActHide     = "hide"     // 隐藏
	ActRequire  = "required" // 必填
	ActOptional = "optional" // 选填（解除必填）
	ActReadonly = "readonly" // 只读
	ActEditable = "editable" // 可编辑（解除只读/禁用）
)

// 组合校验严重级别
const (
	SevBlock = "block" // 阻断提交（默认）
	SevWarn  = "warn"  // 仅提示不阻断
)

// BehaviorRule UI 联动规则：Conditions 命中时对 Targets 字段施加 Action。
// 与字段级 VisibleWhenRules（字段自己"什么时候显示"）互补：
// BehaviorRules 是"由别的字段驱动本字段的状态"，支持显隐/必填/只读/可编辑四类动作。
// Revert=true 时对齐 SN UI Policy "Reverse if false"：条件不满足时自动把目标字段
// 恢复到 schema 初始态（字段自身定义的显隐/必填/只读），无需手写反向规则。
type BehaviorRule struct {
	ID         string      `json:"id,omitempty"`
	Name       string      `json:"name,omitempty"`
	Conditions []Condition `json:"conditions"`      // 触发条件（基于任意字段值）
	Logic      string      `json:"logic,omitempty"` // and(默认) / or
	Action     string      `json:"action"`          // show/hide/required/optional/readonly/editable
	Targets    []string    `json:"targets"`         // 目标字段 key 列表
	Revert     bool        `json:"revert,omitempty"`
}

// ComboRule 跨字段组合校验规则：Conditions 命中且 Severity=block 时拒绝提交。
// 例："变更类型=紧急 且 未填回滚方案 → 阻断并提示"。
type ComboRule struct {
	ID         string      `json:"id,omitempty"`
	Name       string      `json:"name,omitempty"`
	Conditions []Condition `json:"conditions"`
	Logic      string      `json:"logic,omitempty"`
	Message    string      `json:"message"`            // 命中时的提示文案
	Severity   string      `json:"severity,omitempty"` // block(默认) / warn
}

// FieldState 字段运行期有效状态：字段自身定义 + BehaviorRules 叠加后的最终结果。
type FieldState struct {
	Visible  bool
	Required bool
	ReadOnly bool
	Disabled bool
}

// EffectiveStates 计算所有字段的有效状态。
// 叠加规则：
//   - 初始状态取字段自身定义（VisibleWhenRules / Required / ReadOnly）
//   - 依 BehaviorRules 声明顺序依次应用命中的动作（后者覆盖前者）
//   - Revert 规则（SN UI Policy "Reverse if false"）：条件不满足时施加反向动作
//     （show↔hide、required↔optional、readonly↔editable），声明位置生效、后续规则仍可覆盖
//   - 隐藏字段其必填自动失效（不可见自然不可填）
func EffectiveStates(schema *FormSchema, values map[string]any) map[string]FieldState {
	out := map[string]FieldState{}
	if schema == nil {
		return out
	}
	for _, f := range schema.Fields {
		out[f.Key] = FieldState{
			Visible:  FieldVisible(f, values),
			Required: f.Required,
			ReadOnly: f.ReadOnly,
		}
	}
	for _, r := range schema.BehaviorRules {
		if len(r.Targets) == 0 || strings.TrimSpace(r.Action) == "" {
			continue
		}
		action := strings.TrimSpace(strings.ToLower(r.Action))
		if !ruleHits(r, values) {
			if !r.Revert {
				continue
			}
			// Reverse if false: 施加反向动作
			action = inverseAction(action)
		}
		for _, target := range r.Targets {
			st, ok := out[target]
			if !ok {
				continue // 目标字段不在本 schema 中（配置漂移，容错跳过）
			}
			switch action {
			case ActShow:
				st.Visible = true
			case ActHide:
				st.Visible = false
				st.Required = false
			case ActRequire:
				if st.Visible {
					st.Required = true
				}
			case ActOptional:
				st.Required = false
			case ActReadonly:
				st.ReadOnly = true
			case ActEditable:
				st.ReadOnly = false
				st.Disabled = false
			}
			out[target] = st
		}
	}
	return out
}

// inverseAction 返回动作的反向动作（Revert 用）；未识别的原样返回。
func inverseAction(a string) string {
	switch a {
	case ActShow:
		return ActHide
	case ActHide:
		return ActShow
	case ActRequire:
		return ActOptional
	case ActOptional:
		return ActRequire
	case ActReadonly:
		return ActEditable
	case ActEditable:
		return ActReadonly
	}
	return a
}

func ruleHits(r BehaviorRule, values map[string]any) bool {
	if len(r.Targets) == 0 || strings.TrimSpace(r.Action) == "" {
		return false
	}
	return EvalConditions(r.Conditions, r.Logic, values)
}

// 节点字段权限模式（对齐前端 StepFieldPerm.mode 四态）。
const (
	PermHidden   = "hidden"   // 本节点不可见（前端投影为剔除，后端等价为不可见+免必填）
	PermReadonly = "readonly" // 本节点只读展示
	PermDisabled = "disabled" // 本节点禁用（渲染同只读，语义为"权限禁用"）
	PermEditable = "editable" // 本节点可编辑（Required=true 时为本节点必填）
)

// StepFieldPerm 节点字段权限：流程节点声明「主表单某字段在本节点的状态」。
// 与 BehaviorRule 的关系 = 权限上限（cap）与边界内联动：
//   - 节点权限先投影为字段基准态（hidden 剔除 / readonly 标记 / editable+required 标必填）；
//   - BehaviorRules 在基准态上叠加（可进一步收紧：hide/optional/readonly）；
//   - 但节点权限是硬约束——EffectiveStatesForStep 在规则叠加后按 cap 兜底，
//     联动动作不得放宽节点权限（readonly 不被 editable 反转、required 不被 optional 反转）。
type StepFieldPerm struct {
	Key      string `json:"key"`
	Mode     string `json:"mode,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// EffectiveStatesForStep 带节点字段权限的有效状态计算（投影 → 联动 → cap 三段）：
//  1. ApplyStepPerms 将节点权限投影进 schema（hidden 剔除 / readonly 标记 / editable+required 标必填）；
//  2. EffectiveStates 在投影后 schema 上叠加字段自身规则与 BehaviorRules；
//  3. cap 兜底：节点权限不可被联动放宽（readonly 不被 editable 反转、required 不被 optional 反转）。
//
// 无 perms 时等价 EffectiveStates。
func EffectiveStatesForStep(schema *FormSchema, values map[string]any, perms []StepFieldPerm) map[string]FieldState {
	states := EffectiveStates(ApplyStepPerms(schema, perms), values)
	for _, p := range perms {
		st, ok := states[p.Key]
		if !ok {
			continue // hidden 已被投影剔除；或权限声明的字段不在 schema 中（容错跳过）
		}
		switch strings.TrimSpace(strings.ToLower(p.Mode)) {
		case PermReadonly, PermDisabled:
			st.ReadOnly = true
			st.Required = false // 只读不可填，豁免必填（与 ValidateValues 只读跳过校验对齐）
		case PermEditable:
			if p.Required {
				st.Required = true // 本节点必填：即便行为规则 optional 也不放宽
			}
		}
		states[p.Key] = st
	}
	return states
}

// ApplyStepPerms 把节点字段权限投影到 schema（镜像前端 itsm-constants.applyStepFieldPerms）：
//   - hidden 字段剔除（不下发、不参与校验），递归作用于 group 子字段；
//   - readonly/disabled 字段标记只读（后端 FieldDef 无 disabled，二者渲染语义一致）；
//   - editable 且 required 的字段标记必填（本节点动态必填）。
//   - 未声明权限的字段沿用自身定义；perms 为空时原样返回，不修改入参。
func ApplyStepPerms(schema *FormSchema, perms []StepFieldPerm) *FormSchema {
	if schema == nil || len(perms) == 0 {
		return schema
	}
	byKey := make(map[string]StepFieldPerm, len(perms))
	for _, p := range perms {
		byKey[strings.TrimSpace(p.Key)] = p
	}
	out := *schema
	out.Fields = applyPermsToFields(schema.Fields, byKey)
	return &out
}

func applyPermsToFields(fields []FieldDef, byKey map[string]StepFieldPerm) []FieldDef {
	out := make([]FieldDef, 0, len(fields))
	for _, f := range fields {
		p, ok := byKey[f.Key]
		if ok {
			mode := strings.TrimSpace(strings.ToLower(p.Mode))
			switch mode {
			case PermHidden:
				continue // 本节点不可见 → 剔除
			case PermEditable:
				if p.Required && !f.Required {
					f.Required = true
				}
			case PermReadonly, PermDisabled:
				f.ReadOnly = true
				f.Required = false // 只读不可填 → 豁免必填（否则"只读+必填"校验死锁）
			}
		}
		if f.Type == TypeGroup && len(f.Children) > 0 {
			f.Children = applyPermsToFields(f.Children, byKey)
		}
		out = append(out, f)
	}
	return out
}

// ComboResult 组合校验结果：block 级命中合并为 error，warn 级命中返回提示列表。
type ComboResult struct {
	Warnings []string
	Err      error
}

// ValidateComboRules 跨字段组合校验（纯函数，便于单测）。
// 返回 Err 当且仅当存在 block 级规则命中；warn 级命中汇总进 Warnings。
func ValidateComboRules(schema *FormSchema, values map[string]any) ComboResult {
	var res ComboResult
	if schema == nil {
		return res
	}
	var blocked []string
	for _, r := range schema.ComboRules {
		if len(r.Conditions) == 0 || !EvalConditions(r.Conditions, r.Logic, values) {
			continue
		}
		msg := strings.TrimSpace(r.Message)
		if msg == "" {
			msg = "组合校验未通过"
		}
		if strings.TrimSpace(strings.ToLower(r.Severity)) == SevWarn {
			res.Warnings = append(res.Warnings, msg)
			continue
		}
		blocked = append(blocked, msg)
	}
	if len(blocked) > 0 {
		res.Err = fmt.Errorf("%s", strings.Join(blocked, "；"))
	}
	return res
}
