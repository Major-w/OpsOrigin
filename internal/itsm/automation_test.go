package itsm

import (
	"testing"

	"github.com/opsorigin/core/internal/itsm/model"
)

func TestConditionMatches(t *testing.T) {
	tk := &model.Ticket{
		Status:    model.StatusExecuting,
		RiskLevel: model.RiskHigh,
		Assignee:  "netops",
		CreatedBy: "admin",
		CustomFields: map[string]any{
			"env":   "prod",
			"count": 3,
			"svc":   "mysql,redis",
		},
	}
	cases := []struct {
		name string
		c    model.RuleCondition
		want bool
	}{
		{"risk equals hit", model.RuleCondition{Field: "risk_level", Op: "equals", Value: "high"}, true},
		{"risk equals miss", model.RuleCondition{Field: "risk_level", Op: "equals", Value: "low"}, false},
		{"status not_equals hit", model.RuleCondition{Field: "status", Op: "not_equals", Value: "closed"}, true},
		{"assignee equals hit", model.RuleCondition{Field: "assignee", Op: "equals", Value: "netops"}, true},
		{"custom string equals hit", model.RuleCondition{Field: "custom_fields.env", Op: "equals", Value: "prod"}, true},
		{"custom int equals hit", model.RuleCondition{Field: "custom_fields.count", Op: "equals", Value: "3"}, true},
		{"custom contains hit", model.RuleCondition{Field: "custom_fields.svc", Op: "contains", Value: "redis"}, true},
		{"custom missing field miss", model.RuleCondition{Field: "custom_fields.nope", Op: "equals", Value: ""}, true}, // 空串==空串
		{"unknown field fail-safe", model.RuleCondition{Field: "hacker", Op: "equals", Value: ""}, false},
		{"unknown op defaults equals", model.RuleCondition{Field: "risk_level", Op: "weird", Value: "high"}, true},
	}
	for _, tc := range cases {
		if got := conditionMatches(tc.c, tk); got != tc.want {
			t.Errorf("%s: conditionMatches(%+v) = %v, want %v", tc.name, tc.c, got, tc.want)
		}
	}
}

func TestRuleMatchesAllConditions(t *testing.T) {
	tk := &model.Ticket{RiskLevel: model.RiskHigh, Status: model.StatusDraft}
	rule := &model.AutomationRule{Conditions: []model.RuleCondition{
		{Field: "risk_level", Op: "equals", Value: "high"},
		{Field: "status", Op: "not_equals", Value: "closed"},
	}}
	if !ruleMatches(rule, tk) {
		t.Error("all-conditions-met should match")
	}
	rule.Conditions = append(rule.Conditions, model.RuleCondition{Field: "risk_level", Op: "equals", Value: "low"})
	if ruleMatches(rule, tk) {
		t.Error("one failing condition should not match")
	}
	// 空条件 = 无条件触发
	if !ruleMatches(&model.AutomationRule{}, tk) {
		t.Error("empty conditions should match unconditionally")
	}
}

func TestRenderTemplate(t *testing.T) {
	tk := &model.Ticket{
		ID:           "t-1",
		Title:        "核心交换机变更",
		RiskLevel:    model.RiskHigh,
		CreatedBy:    "admin",
		CustomFields: map[string]any{"env": "prod"},
	}
	got := renderTemplate("工单 ${ticket.title}(${ticket.id}) 风险 ${ticket.risk_level}，环境 ${custom_fields.env}，缺失 ${ticket.nope}", tk)
	want := "工单 核心交换机变更(t-1) 风险 high，环境 prod，缺失 "
	if got != want {
		t.Errorf("renderTemplate = %q, want %q", got, want)
	}
	if renderTemplate("", tk) != "" {
		t.Error("empty template should render empty")
	}
	// 无闭合大括号: 原样输出
	if renderTemplate("no closing ${oops", tk) != "no closing ${oops" {
		t.Error("unclosed placeholder should be kept as-is")
	}
}
