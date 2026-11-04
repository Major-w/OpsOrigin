package model

import (
	"fmt"
)

// 状态流转规则（状态管理自定义，P2-⑯）：
// 在内置状态机（state.go validTransitions）之上提供租户级"覆盖层"——
// 只收紧、不放大：配置仅允许禁用内置允许的边（白名单内裁剪）与要求备注，
// 内置禁止的边不因配置放行，保护流程引擎/审批链对状态机的不变量假设。
// 另支持状态显示名覆盖（StatusLabels），用于租户自定义状态叫法。

// TransitionRule 单条流转规则覆盖。
type TransitionRule struct {
	From TicketStatus `json:"from"`
	To   TicketStatus `json:"to"`
	// Allowed false=禁用该内置边；true=保持内置允许（冗余存合理默认便于前端矩阵渲染）。
	Allowed bool `json:"allowed"`
	// RequireComment 流转时必须携带备注（意见留痕）。
	RequireComment bool `json:"require_comment,omitempty"`
}

// StatusFlow 租户级状态流转配置。CatalogID 非空表示仅对该服务目录生效，
// 查询时按「目录精确 → 全局缺省(空) → 无配置」回退。
type StatusFlow struct {
	ID           string            `json:"id"`
	TenantID     string            `json:"tenant_id"`
	CatalogID    string            `json:"catalog_id,omitempty"`
	Transitions  []TransitionRule  `json:"transitions"`
	StatusLabels map[string]string `json:"status_labels,omitempty"` // key=status 值, 覆盖前端显示名
	Enabled      bool              `json:"enabled"`
	CreatedBy    string            `json:"created_by,omitempty"`
	UpdatedAt    string            `json:"updated_at,omitempty"`
}

// ruleFor 返回 (from→to) 边的规则覆盖；无则 ok=false。
func (f *StatusFlow) ruleFor(from, to TicketStatus) (TransitionRule, bool) {
	if f == nil {
		return TransitionRule{}, false
	}
	for _, r := range f.Transitions {
		if r.From == from && r.To == to {
			return r, true
		}
	}
	return TransitionRule{}, false
}

// LabelOf 状态显示名：配置覆盖优先，否则回退内置名。
func (f *StatusFlow) LabelOf(s TicketStatus, builtin string) string {
	if f == nil {
		return builtin
	}
	if v, ok := f.StatusLabels[string(s)]; ok && v != "" {
		return v
	}
	return builtin
}

// CanTransitionWithPolicy 在内置状态机上叠加租户流转规则。
// 语义：
//  1. 内置禁止的边 → 一律拒绝（配置不能放大）；
//  2. 内置允许的边且规则 Allowed=false → 拒绝；
//  3. 内置允许的边且规则 RequireComment=true 且 comment 为空 → 要求备注；
//  4. 其余放行。
func CanTransitionWithPolicy(from, to TicketStatus, flow *StatusFlow, comment string) error {
	if err := CanTransition(from, to); err != nil {
		// 内置禁止: 配置不放大。但保持原错误语义（终态拦截等）。
		return err
	}
	if flow == nil || !flow.Enabled {
		return nil // 未启用配置: 完全走内置
	}
	if rule, ok := flow.ruleFor(from, to); ok {
		if !rule.Allowed {
			return fmt.Errorf("%w: %s -> %s (已被流转规则禁用)", ErrInvalidTransition, from, to)
		}
		if rule.RequireComment && comment == "" {
			return fmt.Errorf("流转 %s -> %s 需要填写备注", from, to)
		}
	}
	return nil
}

// AllStatuses 返回内置全部状态（稳定顺序，与 state.go 定义一致）。
func AllStatuses() []TicketStatus {
	return []TicketStatus{
		StatusDraft, StatusPendingReview, StatusApproved, StatusRejected,
		StatusExecuting, StatusSucceeded, StatusFailed, StatusRolledBack,
		StatusClosed, StatusWithdrawn, StatusReopened,
	}
}

// ValidTransitionsFrom 返回内置状态机中 from 允许流转到的状态列表。
func ValidTransitionsFrom(from TicketStatus) []TicketStatus {
	return validTransitions[from]
}

// IsValidStatus 判断是否内置状态。
func IsValidStatus(s TicketStatus) bool {
	for _, v := range AllStatuses() {
		if v == s {
			return true
		}
	}
	return false
}

// StatusLabels 内置状态显示名（前端覆盖前的回退值）。
func StatusLabels() map[string]string {
	return map[string]string{
		string(StatusDraft):         "草稿",
		string(StatusPendingReview): "待审核",
		string(StatusApproved):      "已审批",
		string(StatusRejected):      "已驳回",
		string(StatusExecuting):     "执行中",
		string(StatusSucceeded):     "已成功",
		string(StatusFailed):        "失败",
		string(StatusRolledBack):    "已回滚",
		string(StatusClosed):        "已关闭",
		string(StatusWithdrawn):     "已撤回",
		string(StatusReopened):      "已重开",
	}
}
