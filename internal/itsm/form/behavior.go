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
