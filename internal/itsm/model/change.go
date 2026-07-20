package model

import "time"

type ChangeRequest struct {
	ID           string        `json:"id"`
	TenantID     string        `json:"tenant_id"`
	TicketID     string        `json:"ticket_id"`
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	RiskLevel    RiskLevel     `json:"risk_level"`
	Status       ChangeStatus  `json:"status"`
	ChangeType   string        `json:"change_type"`
	Impact       string        `json:"impact"`
	Urgency      string        `json:"urgency"`
	CABMembers   []string      `json:"cab_members"`
	Approvals    []CABApproval `json:"approvals"`
	Plan         string        `json:"plan"`
	RollbackPlan string        `json:"rollback_plan"`
	CreatedBy    string        `json:"created_by"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

type ChangeStatus string

const (
	ChangeStatusPending  ChangeStatus = "pending"
	ChangeStatusApproved ChangeStatus = "approved"
	ChangeStatusRejected ChangeStatus = "rejected"
	ChangeStatusExecuted ChangeStatus = "executed"
)

type CABApproval struct {
	Member   string    `json:"member"`
	Approved bool      `json:"approved"`
	Comment  string    `json:"comment,omitempty"`
	VotedAt  time.Time `json:"voted_at"`
}

type CreateChangeRequest struct {
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	RiskLevel    RiskLevel `json:"risk_level"`
	ChangeType   string   `json:"change_type"`
	Impact       string   `json:"impact"`
	Urgency      string   `json:"urgency"`
	CABMembers   []string `json:"cab_members"`
	Plan         string   `json:"plan"`
	RollbackPlan string   `json:"rollback_plan"`
	CreatedBy    string   `json:"created_by"`
	TicketID     string   `json:"ticket_id,omitempty"`
}

type VoteChangeRequest struct {
	Member   string `json:"member"`
	Approved bool   `json:"approved"`
	Comment  string `json:"comment,omitempty"`
}
