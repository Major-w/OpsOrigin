package model

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrRiskRequiresApproval = errors.New("high/critical risk requires approval before execution")
	ErrTicketClosed        = errors.New("ticket is already in a terminal state")
)

type Transition struct {
	From TicketStatus
	To   TicketStatus
}

var validTransitions = map[TicketStatus][]TicketStatus{
	StatusDraft: {
		StatusPendingReview,
		StatusRejected,
	},
	StatusPendingReview: {
		StatusApproved,
		StatusRejected,
	},
	StatusApproved: {
		StatusExecuting,
		StatusRejected,
	},
	StatusRejected: {},
	StatusExecuting: {
		StatusSucceeded,
		StatusFailed,
	},
	StatusSucceeded: {
		StatusRolledBack,
	},
	StatusFailed: {
		StatusRolledBack,
		StatusPendingReview,
	},
	StatusRolledBack: {},
}

func IsTerminal(status TicketStatus) bool {
	return status == StatusRejected || status == StatusRolledBack
}

func CanTransition(from, to TicketStatus) error {
	if IsTerminal(from) {
		return ErrTicketClosed
	}
	allowed, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("%w: %s has no defined transitions", ErrInvalidTransition, from)
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}

func MustBlockForApproval(from TicketStatus, risk RiskLevel) bool {
	return risk.RequiresApproval() && from == StatusDraft
}
