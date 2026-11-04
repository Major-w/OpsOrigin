package model

import (
	"errors"
	"testing"
)

func flowWith(rules ...TransitionRule) *StatusFlow {
	return &StatusFlow{Enabled: true, Transitions: rules}
}

// 无规则覆盖时与内置状态机行为一致。
func TestCanTransitionWithPolicyPassthrough(t *testing.T) {
	if err := CanTransitionWithPolicy(StatusDraft, StatusPendingReview, nil, ""); err != nil {
		t.Fatalf("nil flow should passthrough, got %v", err)
	}
	if err := CanTransitionWithPolicy(StatusDraft, StatusPendingReview, flowWith(), ""); err != nil {
		t.Fatalf("empty flow should passthrough, got %v", err)
	}
	// 内置禁止的边: 配置不放大
	bad := flowWith(TransitionRule{From: StatusDraft, To: StatusSucceeded, Allowed: true})
	if err := CanTransitionWithPolicy(StatusDraft, StatusSucceeded, bad, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("builtin-forbidden edge must stay forbidden, got %v", err)
	}
}

// 规则禁用内置允许的边。
func TestCanTransitionWithPolicyDisable(t *testing.T) {
	flow := flowWith(TransitionRule{From: StatusExecuting, To: StatusClosed, Allowed: false})
	if err := CanTransitionWithPolicy(StatusExecuting, StatusClosed, flow, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("disabled edge should be rejected, got %v", err)
	}
	// 其他边不受影响
	if err := CanTransitionWithPolicy(StatusExecuting, StatusSucceeded, flow, ""); err != nil {
		t.Fatalf("unrelated edge should pass, got %v", err)
	}
}

// require_comment: 无备注拦截、有备注放行。
func TestCanTransitionWithPolicyRequireComment(t *testing.T) {
	flow := flowWith(TransitionRule{From: StatusSucceeded, To: StatusClosed, Allowed: true, RequireComment: true})
	if err := CanTransitionWithPolicy(StatusSucceeded, StatusClosed, flow, ""); err == nil {
		t.Fatalf("missing comment should be rejected")
	}
	if err := CanTransitionWithPolicy(StatusSucceeded, StatusClosed, flow, "验收通过"); err != nil {
		t.Fatalf("with comment should pass, got %v", err)
	}
}

// 停用配置(false)与 LabelOf 覆盖。
func TestStatusFlowDisabledAndLabels(t *testing.T) {
	flow := &StatusFlow{Enabled: false, Transitions: []TransitionRule{{From: StatusExecuting, To: StatusClosed, Allowed: false}}}
	if err := CanTransitionWithPolicy(StatusExecuting, StatusClosed, flow, ""); err != nil {
		t.Fatalf("disabled flow should not apply rules, got %v", err)
	}
	lf := &StatusFlow{StatusLabels: map[string]string{"executing": "处理中"}}
	if got := lf.LabelOf(StatusExecuting, "执行中"); got != "处理中" {
		t.Fatalf("LabelOf override = %q, want 处理中", got)
	}
	if got := lf.LabelOf(StatusClosed, "已关闭"); got != "已关闭" {
		t.Fatalf("LabelOf fallback = %q, want 已关闭", got)
	}
	var nilFlow *StatusFlow
	if got := nilFlow.LabelOf(StatusClosed, "已关闭"); got != "已关闭" {
		t.Fatalf("nil flow LabelOf = %q", got)
	}
}
