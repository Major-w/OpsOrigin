package model

import "time"

// SubtaskStatus 子任务状态。
type SubtaskStatus string

const (
	SubtaskPending    SubtaskStatus = "pending"     // 待处理
	SubtaskInProgress SubtaskStatus = "in_progress" // 处理中
	SubtaskDone       SubtaskStatus = "done"        // 已完成
	SubtaskBlocked    SubtaskStatus = "blocked"     // 受阻
)

// Subtask 工单子任务: 大工单拆分为可指派、可跟踪的小任务。
type Subtask struct {
	ID          int64         `json:"id"`
	TicketID    string        `json:"ticket_id"`
	TenantID    string        `json:"tenant_id"`
	Title       string        `json:"title"`
	Description string        `json:"description,omitempty"`
	Assignee    string        `json:"assignee,omitempty"`
	Status      SubtaskStatus `json:"status"`
	DueAt       *time.Time    `json:"due_at,omitempty"`
	CreatedBy   string        `json:"created_by"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
}

// CreateSubtaskRequest 创建子任务。
type CreateSubtaskRequest struct {
	Title       string        `json:"title"`
	Description string        `json:"description,omitempty"`
	Assignee    string        `json:"assignee,omitempty"`
	DueAt       *time.Time    `json:"due_at,omitempty"`
	CreatedBy   string        `json:"created_by"`
}

// UpdateSubtaskRequest 更新子任务(标题/描述/负责人/状态/截止时间)。
type UpdateSubtaskRequest struct {
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Assignee    string        `json:"assignee,omitempty"`
	Status      SubtaskStatus `json:"status,omitempty"`
	DueAt       *time.Time    `json:"due_at,omitempty"`
}
