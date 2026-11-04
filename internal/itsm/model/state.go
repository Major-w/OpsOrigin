package model

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidTransition    = errors.New("invalid state transition")
	ErrRiskRequiresApproval = errors.New("high/critical risk requires approval before execution")
	ErrTicketClosed         = errors.New("ticket is already in a terminal state")
)

type Transition struct {
	From TicketStatus
	To   TicketStatus
}

var validTransitions = map[TicketStatus][]TicketStatus{
	StatusDraft: {
		StatusPendingReview,
		StatusRejected,
		StatusExecuting,
		StatusWithdrawn,
	},
	StatusPendingReview: {
		StatusApproved,
		StatusRejected,
		// 免审批直达处理：低/中风险工单允许在待审核态直接开始处理；
		// 高风险工单由 handleExecute 的 RiskLevel 门禁拦截，此处不区分。
		StatusExecuting,
	},
	StatusApproved: {
		StatusExecuting,
		StatusRejected,
	},
	StatusRejected: {
		StatusPendingReview, // 允许被拒后修改重新提交
	},
	StatusExecuting: {
		StatusSucceeded,
		StatusFailed,
		// 处理中直接关闭：用户取消 / 重复单 / 无需继续处理，不必先走"完成"。
		StatusClosed,
	},
	StatusSucceeded: {
		StatusRolledBack,
		StatusClosed,
	},
	StatusFailed: {
		StatusRolledBack,
		StatusPendingReview, // 修改方案重新审批
		StatusExecuting,     // 直接重试
	},
	StatusRolledBack: {
		StatusPendingReview, // 修改方案重新审批
		StatusExecuting,     // 直接重试
	},
	StatusClosed: {
		StatusReopened,
	},
	StatusWithdrawn: {},
	StatusReopened: {
		StatusExecuting,
		StatusClosed,
	},
}

func IsTerminal(status TicketStatus) bool {
	return status == StatusRejected || status == StatusRolledBack || status == StatusClosed || status == StatusWithdrawn
}

func CanTransition(from, to TicketStatus) error {
	allowed, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("%w: %s has no defined transitions", ErrInvalidTransition, from)
	}
	// 终态且无任何出边(如 rejected/rolled_back/withdrawn)才拦截;
	// 终态但定义了出边(如 closed → reopened)允许重开。
	if len(allowed) == 0 && IsTerminal(from) {
		return ErrTicketClosed
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
