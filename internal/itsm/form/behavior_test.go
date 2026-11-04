package form

import (
	"strings"
	"testing"
)

func schemaWithBehavior() *FormSchema {
	return &FormSchema{
		Version: 1,
		Scope:   "ticket:default",
		Fields: []FieldDef{
			{Key: "change_type", Label: "变更类型", Type: TypeSelect, Options: []FieldOption{{Value: "normal"}, {Value: "urgent"}}},
			{Key: "rollback_plan", Label: "回滚方案", Type: TypeTextarea},
			{Key: "approve_note", Label: "审批备注", Type: TypeText},
			{Key: "window", Label: "实施窗口", Type: TypeText},
		},
		BehaviorRules: []BehaviorRule{
			{ID: "r1", Name: "紧急变更必填回滚方案", Conditions: []Condition{{Field: "change_type", Op: OpEq, Value: "urgent"}}, Action: ActRequire, Targets: []string{"rollback_plan"}},
			{ID: "r2", Name: "紧急变更隐藏审批备注", Conditions: []Condition{{Field: "change_type", Op: OpEq, Value: "urgent"}}, Action: ActHide, Targets: []string{"approve_note"}},
			{ID: "r3", Name: "普通变更窗口只读", Conditions: []Condition{{Field: "change_type", Op: OpEq, Value: "normal"}}, Action: ActReadonly, Targets: []string{"window"}},
		},
		ComboRules: []ComboRule{
			{ID: "c1", Conditions: []Condition{{Field: "change_type", Op: OpEq, Value: "urgent"}, {Field: "rollback_plan", Op: OpEmpty}}, Message: "紧急变更必须填写回滚方案", Severity: SevBlock},
			{ID: "c2", Conditions: []Condition{{Field: "change_type", Op: OpEq, Value: "normal"}}, Message: "普通变更建议避开业务高峰", Severity: SevWarn},
		},
	}
}

func TestEffectiveStatesBehaviorRules(t *testing.T) {
	s := schemaWithBehavior()

	// urgent: rollback_plan 必填、approve_note 隐藏、window 保持默认
	st := EffectiveStates(s, map[string]any{"change_type": "urgent"})
	if !st["rollback_plan"].Required {
		t.Error("urgent 时 rollback_plan 应必填")
	}
	if st["approve_note"].Visible {
		t.Error("urgent 时 approve_note 应隐藏")
	}
	if !st["window"].Visible {
		t.Error("无显隐规则的字段应默认可见")
	}

	// normal: rollback_plan 不必填、approve_note 可见、window 只读
	st = EffectiveStates(s, map[string]any{"change_type": "normal"})
	if st["rollback_plan"].Required {
		t.Error("normal 时 rollback_plan 不应必填")
	}
	if !st["approve_note"].Visible {
		t.Error("normal 时 approve_note 应可见")
	}
	if !st["window"].ReadOnly {
		t.Error("normal 时 window 应只读")
	}
}

func TestBehaviorHideClearsRequired(t *testing.T) {
	s := &FormSchema{
		Fields: []FieldDef{{Key: "a", Type: TypeText}, {Key: "b", Type: TypeText, Required: true}},
		BehaviorRules: []BehaviorRule{
			{Conditions: []Condition{{Field: "a", Op: OpEq, Value: "x"}}, Action: ActHide, Targets: []string{"b"}},
		},
	}
	// 隐藏后必填失效：a=x 时不校验 b
	if err := ValidateSchema(s, map[string]any{"a": "x"}); err != nil {
		t.Fatalf("隐藏字段不应触发必填: %v", err)
	}
	// a 非 x：b 必填生效
	if err := ValidateSchema(s, map[string]any{"a": "y"}); err == nil {
		t.Fatal("b 应为必填")
	}
}

func TestValidateComboRulesBlockAndWarn(t *testing.T) {
	s := schemaWithBehavior()

	// 紧急 + 无回滚方案 → block
	res := ValidateComboRules(s, map[string]any{"change_type": "urgent"})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "回滚方案") {
		t.Fatalf("期望 block 命中回滚方案规则, got %v", res.Err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("urgent 不应命中 warn 规则: %v", res.Warnings)
	}

	// 紧急 + 已填回滚方案 → 仅 warn
	res = ValidateComboRules(s, map[string]any{"change_type": "urgent", "rollback_plan": "关闭实例"})
	if res.Err != nil {
		t.Fatalf("已填回滚方案不应 block: %v", res.Err)
	}

	// 普通 → 仅 warn
	res = ValidateComboRules(s, map[string]any{"change_type": "normal"})
	if res.Err != nil || len(res.Warnings) != 1 {
		t.Fatalf("期望 1 条 warn 无 block, got err=%v warnings=%v", res.Err, res.Warnings)
	}
}

func TestComboLogicOr(t *testing.T) {
	s := &FormSchema{
		Fields: []FieldDef{{Key: "a", Type: TypeText}, {Key: "b", Type: TypeText}},
		ComboRules: []ComboRule{{
			Conditions: []Condition{{Field: "a", Op: OpEq, Value: "1"}, {Field: "b", Op: OpEq, Value: "2"}},
			Logic:      LogicOr,
			Message:    "a=1 或 b=2 不允许",
		}},
	}
	if res := ValidateComboRules(s, map[string]any{"a": "1"}); res.Err == nil {
		t.Fatal("or 逻辑 a=1 应命中")
	}
	if res := ValidateComboRules(s, map[string]any{"a": "9", "b": "9"}); res.Err != nil {
		t.Fatalf("不应命中: %v", res.Err)
	}
}

func TestValidateSchemaBehaviorRequiredBlocksSubmit(t *testing.T) {
	s := schemaWithBehavior()
	// urgent 但没填 rollback_plan → 必填错误
	err := ValidateSchema(s, map[string]any{"change_type": "urgent"})
	if err == nil || !strings.Contains(err.Error(), "回滚方案") {
		t.Fatalf("urgent 应要求回滚方案必填, got %v", err)
	}
	// urgent 且填了 → 通过（approve_note 隐藏不校验）
	if err := ValidateSchema(s, map[string]any{"change_type": "urgent", "rollback_plan": "重启"}); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
}
