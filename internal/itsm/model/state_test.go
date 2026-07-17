package model

import (
	"testing"
)

func TestCanTransition_Valid(t *testing.T) {
	tests := []struct {
		from TicketStatus
		to   TicketStatus
	}{
		{StatusDraft, StatusPendingReview},
		{StatusPendingReview, StatusApproved},
		{StatusPendingReview, StatusRejected},
		{StatusApproved, StatusExecuting},
		{StatusExecuting, StatusSucceeded},
		{StatusExecuting, StatusFailed},
		{StatusSucceeded, StatusRolledBack},
		{StatusFailed, StatusRolledBack},
		{StatusFailed, StatusPendingReview},
	}

	for _, tc := range tests {
		err := CanTransition(tc.from, tc.to)
		if err != nil {
			t.Errorf("expected valid transition %s -> %s, got error: %v", tc.from, tc.to, err)
		}
	}
}

func TestCanTransition_Invalid(t *testing.T) {
	tests := []struct {
		from TicketStatus
		to   TicketStatus
	}{
		{StatusDraft, StatusApproved},
		{StatusDraft, StatusExecuting},
		{StatusRejected, StatusApproved},
		{StatusRolledBack, StatusSucceeded},
		{StatusApproved, StatusDraft},
	}

	for _, tc := range tests {
		err := CanTransition(tc.from, tc.to)
		if err == nil {
			t.Errorf("expected invalid transition %s -> %s, but got nil", tc.from, tc.to)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	if !IsTerminal(StatusRejected) {
		t.Error("expected rejected to be terminal")
	}
	if !IsTerminal(StatusRolledBack) {
		t.Error("expected rolled_back to be terminal")
	}
	if IsTerminal(StatusApproved) {
		t.Error("expected approved not to be terminal")
	}
}

func TestRiskRequiresApproval(t *testing.T) {
	if !RiskHigh.RequiresApproval() {
		t.Error("high risk should require approval")
	}
	if !RiskCritical.RequiresApproval() {
		t.Error("critical risk should require approval")
	}
	if RiskLow.RequiresApproval() {
		t.Error("low risk should not require approval")
	}
	if RiskMedium.RequiresApproval() {
		t.Error("medium risk should not require approval")
	}
}
