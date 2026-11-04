package form

import (
	"testing"
)

// Revert（Reverse if false）: 条件不满足时施加反向动作（required→optional 等），
// 不清掉其他规则已产生的效果。
func TestEffectiveStatesRevert(t *testing.T) {
	schema := &FormSchema{
		Fields: []FieldDef{
			{Key: "type", Label: "类型", Type: TypeSelect, Required: false},
			{Key: "rollback", Label: "回滚方案", Type: TypeTextarea, Required: false, ReadOnly: true},
		},
		BehaviorRules: []BehaviorRule{
			// 条件命中: rollback 必填; 未命中且 Revert=true: 反向动作 optional → 必填解除,
			// 只读(字段自身定义)不受影响
			{Action: ActRequire, Targets: []string{"rollback"}, Revert: true,
				Conditions: []Condition{{Field: "type", Op: "eq", Value: "emergency"}}},
			// 无 Revert 的规则: 未命中时不改变状态(对照)
			{Action: ActHide, Targets: []string{"rollback"},
				Conditions: []Condition{{Field: "type", Op: "eq", Value: "never_match"}}},
		},
	}

	// 命中: 必填生效
	st := EffectiveStates(schema, map[string]any{"type": "emergency"})
	if !st["rollback"].Required {
		t.Fatal("hit: rollback should be required")
	}

	// 未命中 + Revert: 反向动作 optional 解除必填; 只读保持字段初始定义
	st = EffectiveStates(schema, map[string]any{"type": "normal"})
	if st["rollback"].Required {
		t.Fatal("miss+revert: rollback required should be cleared by inverse action")
	}
	if !st["rollback"].ReadOnly {
		t.Fatal("miss+revert: rollback readonly (initial) should be preserved")
	}

	// Revert 恢复的是初始快照: 前置规则改过状态后再 revert 不应错位
	schema2 := &FormSchema{
		Fields: []FieldDef{{Key: "a", Type: TypeText}, {Key: "b", Type: TypeText}},
		BehaviorRules: []BehaviorRule{
			{Action: ActRequire, Targets: []string{"b"}, Conditions: []Condition{{Field: "a", Op: "eq", Value: "x"}}},
			{Action: ActReadonly, Targets: []string{"b"}, Revert: true, Conditions: []Condition{{Field: "a", Op: "eq", Value: "y"}}},
		},
	}
	st = EffectiveStates(schema2, map[string]any{"a": "x"})
	if !st["b"].Required || st["b"].ReadOnly {
		t.Fatalf("rule1 hit then rule2 miss: required=%v readonly=%v, want required=true readonly=false", st["b"].Required, st["b"].ReadOnly)
	}
}

// 对照: 未开 Revert 的规则未命中时不改变状态（既有行为回归）。
func TestEffectiveStatesNoRevertKeepsPriorState(t *testing.T) {
	schema := &FormSchema{
		Fields: []FieldDef{{Key: "f", Type: TypeText}},
		BehaviorRules: []BehaviorRule{
			{Action: ActRequire, Targets: []string{"f"}, Conditions: []Condition{{Field: "x", Op: "eq", Value: "1"}}},
		},
	}
	st := EffectiveStates(schema, map[string]any{"x": "2"})
	if st["f"].Required {
		t.Fatal("non-revert miss should leave initial state untouched")
	}
}
