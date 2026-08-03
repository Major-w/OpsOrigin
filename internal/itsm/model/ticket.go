package model

import "time"

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

func (r RiskLevel) RequiresApproval() bool {
	return r == RiskHigh || r == RiskCritical
}

func (r RiskLevel) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	}
	return false
}

type TicketStatus string

const (
	StatusDraft         TicketStatus = "draft"
	StatusPendingReview TicketStatus = "pending_review"
	StatusApproved      TicketStatus = "approved"
	StatusRejected      TicketStatus = "rejected"
	StatusExecuting     TicketStatus = "executing"
	StatusSucceeded     TicketStatus = "succeeded"
	StatusFailed        TicketStatus = "failed"
	StatusRolledBack    TicketStatus = "rolled_back"
)

func (s TicketStatus) Valid() bool {
	switch s {
	case StatusDraft, StatusPendingReview, StatusApproved, StatusRejected,
		StatusExecuting, StatusSucceeded, StatusFailed, StatusRolledBack:
		return true
	}
	return false
}

type Ticket struct {
	ID                 string            `json:"id"`
	TenantID           string            `json:"tenant_id"`
	Title              string            `json:"title"`
	Description        string            `json:"description,omitempty"`
	RiskLevel          RiskLevel         `json:"risk_level"`
	Status             TicketStatus      `json:"status"`
	ChangePlan         string            `json:"change_plan,omitempty"`
	RollbackPlan       string            `json:"rollback_plan,omitempty"`
	CreatedBy          string            `json:"created_by"`
	CreatedByType      string            `json:"created_by_type"`
	ApprovedBy         string            `json:"approved_by,omitempty"`
	RejectReason       string            `json:"reject_reason,omitempty"`
	CompletedBy        string            `json:"completed_by,omitempty"`
	RolledBackBy       string            `json:"rolled_back_by,omitempty"`
	RollbackReason     string            `json:"rollback_reason,omitempty"`
	CIIDs              []string          `json:"ci_ids,omitempty"`
	WebhookURL         string            `json:"webhook_url,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	ProcessID          string            `json:"process_id,omitempty"` // 绑定的流程模板 ID
	TraceID            string            `json:"trace_id"`
	ScheduledAt        *time.Time        `json:"scheduled_at,omitempty"`
	DeadlineAt         *time.Time        `json:"deadline_at,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	ApprovedAt         *time.Time        `json:"approved_at,omitempty"`
	ExecutedAt         *time.Time        `json:"executed_at,omitempty"`
	CompletedAt        *time.Time        `json:"completed_at,omitempty"`
	RolledBackAt       *time.Time        `json:"rolled_back_at,omitempty"`
	SLAWarningNotified bool              `json:"sla_warning_notified,omitempty"`
	SLABreached        bool              `json:"sla_breached,omitempty"`
	SLABreachedAt      *time.Time        `json:"sla_breached_at,omitempty"`
	SLAEscalationLevel int               `json:"sla_escalation_level,omitempty"`
}

type CreateTicketRequest struct {
	Title         string            `json:"title"`
	Description   string            `json:"description,omitempty"`
	RiskLevel     RiskLevel         `json:"risk_level"`
	ChangePlan    string            `json:"change_plan,omitempty"`
	RollbackPlan  string            `json:"rollback_plan,omitempty"`
	CreatedBy     string            `json:"created_by"`
	CreatedByType string            `json:"created_by_type"`
	WebhookURL    string            `json:"webhook_url,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	CIIDs         []string          `json:"ci_ids,omitempty"`
	ProcessID     string            `json:"process_id,omitempty"` // 绑定的流程模板 ID（触发流程引擎驱动）
	ScheduledAt   *time.Time        `json:"scheduled_at,omitempty"`
	DeadlineAt    *time.Time        `json:"deadline_at,omitempty"`
	TraceID       string            `json:"trace_id,omitempty"`
}

type ApproveRequest struct {
	ApprovedBy string `json:"approved_by"`
	Comment    string `json:"comment,omitempty"`
}

type RejectRequest struct {
	RejectedBy string `json:"rejected_by"`
	Reason     string `json:"reason"`
}

type ExecuteRequest struct {
	ExecutedBy string `json:"executed_by"`
	Result     string `json:"result,omitempty"`
}

type CompleteRequest struct {
	CompletedBy string `json:"completed_by"`
	Comment     string `json:"comment,omitempty"`
	// Assessment 处置效果评估(供 AI 数字员工运营效果统计): confirmed=确认有效 / false_positive=误报。
	// 留空不写; 仅对 labels.source=ai_inspection / ai_oncall 的 AI 工单有意义。
	Assessment string `json:"assessment,omitempty"`
}

// Assessment 值常量。
const (
	AssessmentConfirmed     = "confirmed"      // 确认有效(命中)
	AssessmentFalsePositive = "false_positive" // 误报
)

type RollbackRequest struct {
	RolledBackBy   string `json:"rolled_back_by"`
	RollbackReason string `json:"rollback_reason,omitempty"`
	Comment        string `json:"comment,omitempty"`
}

type TicketEvent struct {
	ID         int64        `json:"id,omitempty"`
	TicketID   string       `json:"ticket_id"`
	FromStatus TicketStatus `json:"from_status,omitempty"`
	ToStatus   TicketStatus `json:"to_status"`
	ChangedBy  string       `json:"changed_by"`
	Comment    string       `json:"comment,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
}

type ITSMStats struct {
	Total          int            `json:"total"`
	ByStatus       map[string]int `json:"by_status"`
	ByRisk         map[string]int `json:"by_risk"`
	ApprovalRate   float64        `json:"approval_rate"`
	AvgApprovalMs  int64          `json:"avg_approval_ms"`
	AvgExecutionMs int64          `json:"avg_execution_ms"`
}

type SLAStats struct {
	TotalActive     int     `json:"total_active"`
	SLAMet          int     `json:"sla_met"`
	SLABreached     int     `json:"sla_breached"`
	SLAWarning      int     `json:"sla_warning"`
	ComplianceRate  float64 `json:"compliance_rate"`
	AvgResponseMs   int64   `json:"avg_response_ms"`
	AvgResolutionMs int64   `json:"avg_resolution_ms"`
}
