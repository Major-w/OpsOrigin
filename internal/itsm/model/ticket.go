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
	StatusClosed        TicketStatus = "closed"
	StatusWithdrawn     TicketStatus = "withdrawn" // 撤回(草稿废弃)
	StatusReopened      TicketStatus = "reopened"  // 重开(closed 后重新激活)
)

func (s TicketStatus) Valid() bool {
	switch s {
	case StatusDraft, StatusPendingReview, StatusApproved, StatusRejected,
		StatusExecuting, StatusSucceeded, StatusFailed, StatusRolledBack, StatusClosed,
		StatusWithdrawn, StatusReopened:
		return true
	}
	return false
}

type Ticket struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id"`
	Title          string            `json:"title"`
	Description    string            `json:"description,omitempty"`
	RiskLevel      RiskLevel         `json:"risk_level"`
	Status         TicketStatus      `json:"status"`
	ChangePlan     string            `json:"change_plan,omitempty"`
	RollbackPlan   string            `json:"rollback_plan,omitempty"`
	CreatedBy      string            `json:"created_by"`
	CreatedByType  string            `json:"created_by_type"`
	ApprovedBy     string            `json:"approved_by,omitempty"`
	RejectReason   string            `json:"reject_reason,omitempty"`
	CompletedBy    string            `json:"completed_by,omitempty"`
	RolledBackBy   string            `json:"rolled_back_by,omitempty"`
	RollbackReason string            `json:"rollback_reason,omitempty"`
	CIIDs          []string          `json:"ci_ids,omitempty"`
	WebhookURL     string            `json:"webhook_url,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	// CustomFields 可配置表单采集的业务自定义字段（JSONB），与系统字段并存。
	// 由 form.FormSchema 驱动（internal/itsm/form），无 schema 时为空。
	CustomFields       map[string]any `json:"custom_fields,omitempty"`
	ProcessID          string         `json:"process_id,omitempty"` // 绑定的流程模板 ID
	CatalogID          string         `json:"catalog_id,omitempty"` // 发起来源的服务目录项 ID（用于使用量统计与满意度聚合）
	TraceID            string         `json:"trace_id"`
	ScheduledAt        *time.Time     `json:"scheduled_at,omitempty"`
	DeadlineAt         *time.Time     `json:"deadline_at,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	ApprovedAt         *time.Time     `json:"approved_at,omitempty"`
	ExecutedAt         *time.Time     `json:"executed_at,omitempty"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	RolledBackAt       *time.Time     `json:"rolled_back_at,omitempty"`
	Assignee           string         `json:"assignee,omitempty"`
	AssignedAt         *time.Time     `json:"assigned_at,omitempty"`
	SLAWarningNotified bool           `json:"sla_warning_notified,omitempty"`
	SLABreached        bool           `json:"sla_breached,omitempty"`
	SLABreachedAt      *time.Time     `json:"sla_breached_at,omitempty"`
	SLAEscalationLevel int            `json:"sla_escalation_level,omitempty"`
	Satisfaction       int            `json:"satisfaction,omitempty"`
	// 挂起/恢复: OnHold 置位时 SLA 计时暂停(标记而非状态, 状态机零侵入)。
	OnHold      bool       `json:"on_hold,omitempty"`
	HoldReason  string     `json:"hold_reason,omitempty"`
	HoldUntilAt *time.Time `json:"hold_until_at,omitempty"`
	HoldAt      *time.Time `json:"hold_at,omitempty"`      // 挂起开始时间
	HoldSeconds int        `json:"hold_seconds,omitempty"` // 累计挂起秒数
	// 审批链: 支持顺序/并行审批模式
	ApprovalChain  *ApprovalChain  `json:"approval_chain,omitempty"`
	ApprovalStatus *ApprovalStatus `json:"approval_status,omitempty"`
	// 关联工单: 与其它工单的双向关联(互相持有对方 ID)。
	RelatedTicketIDs []string `json:"related_ticket_ids,omitempty"`
}

// ApprovalChain 审批链定义
type ApprovalChain struct {
	Mode      string   `json:"mode"`      // sequential(顺序) / parallel(并行)
	Approvers []string `json:"approvers"` // 审批人列表
}

// ApprovalStatus 审批状态记录
type ApprovalStatus struct {
	CurrentIndex int              `json:"current_index"` // 当前审批人索引(顺序模式)
	Votes        []ApprovalVote   `json:"votes"`         // 投票记录
	FinalResult  string           `json:"final_result"`  // pending / approved / rejected
}

// ApprovalVote 单次审批投票
type ApprovalVote struct {
	Approver string    `json:"approver"`
	Approved bool      `json:"approved"`
	Comment  string    `json:"comment,omitempty"`
	VotedAt  time.Time `json:"voted_at"`
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
	// CustomFields 可配置表单采集的业务自定义字段（JSONB）。
	CustomFields map[string]any `json:"custom_fields,omitempty"`
	// FormScope 指定本工单套用的表单作用域（默认 ticket:default）；用于目录/流程按场景套不同字段。
	FormScope      string         `json:"form_scope,omitempty"`
	ProcessID      string         `json:"process_id,omitempty"` // 绑定的流程模板 ID（触发流程引擎驱动）
	CatalogID      string         `json:"catalog_id,omitempty"` // 发起来源的服务目录项 ID
	ScheduledAt    *time.Time     `json:"scheduled_at,omitempty"`
	DeadlineAt     *time.Time     `json:"deadline_at,omitempty"`
	TraceID        string         `json:"trace_id,omitempty"`
	ApprovalChain  *ApprovalChain `json:"approval_chain,omitempty"` // 审批链配置
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

// WithdrawRequest 撤回(草稿废弃)。
type WithdrawRequest struct {
	WithdrawnBy string `json:"withdrawn_by"`
	Reason      string `json:"reason,omitempty"`
}

// ReopenRequest 重开(closed 后重新激活)。
type ReopenRequest struct {
	ReopenedBy string `json:"reopened_by"`
	Reason     string `json:"reason,omitempty"`
}

// HoldRequest 挂起(暂停 SLA 计时)。
type HoldRequest struct {
	HoldBy      string     `json:"hold_by"`
	Reason      string     `json:"reason,omitempty"`
	HoldUntilAt *time.Time `json:"hold_until_at,omitempty"`
}

// RemindRequest 催办。
type RemindRequest struct {
	RemindedBy string `json:"reminded_by"`
	Message    string `json:"message,omitempty"`
}

// RelateRequest 工单关联(双向)。
type RelateRequest struct {
	RelatedID string `json:"related_id"`
	RelateBy  string `json:"relate_by,omitempty"`
	Relation  string `json:"relation,omitempty"`  // parent / child / depends_on / blocks / related
	Reason    string `json:"reason,omitempty"`    // 关联原因
}

// UpdateTicketRequest 通用更新基础信息(非状态)。
type UpdateTicketRequest struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	RiskLevel   RiskLevel         `json:"risk_level,omitempty"`
	Assignee    string            `json:"assignee,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CIIDs       []string          `json:"ci_ids,omitempty"`
	ChangePlan  string            `json:"change_plan,omitempty"`
	RollbackPlan string           `json:"rollback_plan,omitempty"`
}

// SubmitRequest 提交工单(离开 draft): 高风险进待审批, 低/中风险直接进执行。
type SubmitRequest struct {
	SubmittedBy string `json:"submitted_by"`
	Comment     string `json:"comment,omitempty"`
}

// AssignRequest 指派工单负责人。
type AssignRequest struct {
	Assignee   string `json:"assignee"`
	AssignedBy string `json:"assigned_by,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

// CloseRequest 关单(进入 closed 终态), 可附带满意度评分(1-5)。
type CloseRequest struct {
	ClosedBy     string `json:"closed_by"`
	Comment      string `json:"comment,omitempty"`
	Satisfaction int    `json:"satisfaction,omitempty"`
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

// TrendPoint 工单趋势单日数据点。
type TrendPoint struct {
	Date    string `json:"date"` // YYYY-MM-DD
	Created int    `json:"created"`
	Closed  int    `json:"closed"`
}

type ITSMStats struct {
	Total          int            `json:"total"`
	ByStatus       map[string]int `json:"by_status"`
	ByRisk         map[string]int `json:"by_risk"`
	ApprovalRate   float64        `json:"approval_rate"`
	AvgApprovalMs  int64          `json:"avg_approval_ms"`
	AvgExecutionMs int64          `json:"avg_execution_ms"`
	// 报表看板: 按天趋势(近 N 天)/按创建人效能/按来源渠道分布
	Trends    []TrendPoint   `json:"trends,omitempty"`
	ByCreator map[string]int `json:"by_creator,omitempty"`
	ByChannel map[string]int `json:"by_channel,omitempty"`
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
