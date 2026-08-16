package cmdb

import (
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func TestMatchPolicyCondition_Equality(t *testing.T) {
	ci := &model.CIModel{ID: "a", TypeID: "server", Attributes: map[string]any{"env": "prod", "region": "cn-east"}}
	if !matchPolicyCondition(ci, []model.PolicyCondition{{Attr: "env", Op: "eq", Value: "prod"}}) {
		t.Error("env=prod 应匹配")
	}
	if matchPolicyCondition(ci, []model.PolicyCondition{{Attr: "env", Op: "eq", Value: "dev"}}) {
		t.Error("env=dev 不应匹配")
	}
	if matchPolicyCondition(ci, []model.PolicyCondition{{Attr: "owner", Op: "eq", Value: "x"}}) {
		t.Error("缺失属性不应匹配")
	}
}

func TestMatchPolicyCondition_In(t *testing.T) {
	ci := &model.CIModel{ID: "a", Attributes: map[string]any{"env": "staging"}}
	if !matchPolicyCondition(ci, []model.PolicyCondition{{Attr: "env", Op: "in", Value: "prod,staging"}}) {
		t.Error("in 命中应匹配")
	}
	if matchPolicyCondition(ci, []model.PolicyCondition{{Attr: "env", Op: "in", Value: "prod,dev"}}) {
		t.Error("in 未命中不应匹配")
	}
}

func TestMatchPolicyCondition_And(t *testing.T) {
	ci := &model.CIModel{ID: "a", Attributes: map[string]any{"env": "prod", "region": "cn-east"}}
	cond := []model.PolicyCondition{
		{Attr: "env", Op: "eq", Value: "prod"},
		{Attr: "region", Op: "eq", Value: "cn-east"},
	}
	if !matchPolicyCondition(ci, cond) {
		t.Error("AND 全满足应匹配")
	}
	cond[1].Value = "cn-west"
	if matchPolicyCondition(ci, cond) {
		t.Error("AND 任一不满足不应匹配")
	}
}

func TestFilterByDataPolicy(t *testing.T) {
	cis := []*model.CIModel{
		{ID: "a", TypeID: "server", Attributes: map[string]any{"env": "prod"}},
		{ID: "b", TypeID: "server", Attributes: map[string]any{"env": "dev"}},
		{ID: "c", TypeID: "database", Attributes: map[string]any{"env": "dev"}},
	}
	policies := []*model.DataPolicy{
		{Role: "operator", ModelTypeID: "server", Action: "query", Condition: []model.PolicyCondition{{Attr: "env", Op: "eq", Value: "prod"}}},
	}
	// operator 只能看到 server 且 env=prod; database 无策略 → 可见
	out := filterByDataPolicy(cis, policies, "operator")
	if len(out) != 2 {
		t.Fatalf("operator 应见 prod server + database(无策略), got %d", len(out))
	}
	for _, ci := range out {
		if ci.TypeID == "server" && ci.ID != "a" {
			t.Errorf("server 应只剩 prod 的 a, got %s", ci.ID)
		}
	}
	// admin 无策略 → 全部可见
	if out := filterByDataPolicy(cis, policies, "admin"); len(out) != 3 {
		t.Fatalf("无策略角色应全部可见, got %d", len(out))
	}
	// 无策略时不拦截(空策略列表)
	if out := filterByDataPolicy(cis, nil, "operator"); len(out) != 3 {
		t.Fatalf("空策略不应过滤, got %d", len(out))
	}
}

func TestFilterByDataPolicy_TypeWithoutPolicyVisible(t *testing.T) {
	cis := []*model.CIModel{
		{ID: "c", TypeID: "database", Attributes: map[string]any{"env": "dev"}},
	}
	policies := []*model.DataPolicy{
		{Role: "operator", ModelTypeID: "server", Action: "query", Condition: []model.PolicyCondition{{Attr: "env", Op: "eq", Value: "prod"}}},
	}
	out := filterByDataPolicy(cis, policies, "operator")
	if len(out) != 1 {
		t.Fatalf("无策略的模型类型不应被过滤, got %d", len(out))
	}
}
