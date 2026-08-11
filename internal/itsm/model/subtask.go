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
// 子任务按"派发任务"语义运作: 处理人首次查看即认领(pending → in_progress),
// 过程中回填处理情况(Detail), 完成后置 done。
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
	// Detail 处理情况: 处理人回填的执行过程/结果说明(区别于派发时写死的 Description)。
	Detail string `json:"detail,omitempty"`
	// ViewedAt 处理人首次查看时间(用于判断任务是否已被接收)。
	ViewedAt *time.Time `json:"viewed_at,omitempty"`
	// StartedAt 进入处理中的时间(首次查看自动认领, 或手动置 in_progress)。
	StartedAt *time.Time `json:"started_at,omitempty"`
	// UpdatedBy 最后一次更新处理情况的人。
	UpdatedBy string `json:"updated_by,omitempty"`
	// CloseWithParent 是否随主工单关闭而自动关闭(默认 true, 兼容既有级联行为)。
	// 置 false 时主单关闭不动该子任务, 适用于需独立验收/跨期跟进的任务。
	CloseWithParent bool `json:"close_with_parent"`
	// Meta 主单字段同步的快照(创建时按 sync_fields 拷贝, 键=字段 key, 值=主单值)。
	Meta map[string]any `json:"meta,omitempty"`
}

// CreateSubtaskRequest 创建子任务。
type CreateSubtaskRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Assignee    string     `json:"assignee,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	CreatedBy   string     `json:"created_by"`
	// SyncFields 从主工单同步的字段 key 列表(系统字段如 risk_level, 或 custom_fields.xxx)。
	SyncFields []string `json:"sync_fields,omitempty"`
	// CloseWithParent 是否随主单关闭而自动关闭。指针类型: 不传 = 默认 true。
	CloseWithParent *bool `json:"close_with_parent,omitempty"`
}

// UpdateSubtaskRequest 更新子任务(标题/描述/负责人/状态/截止时间/处理情况)。
type UpdateSubtaskRequest struct {
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Assignee    string        `json:"assignee,omitempty"`
	Status      SubtaskStatus `json:"status,omitempty"`
	DueAt       *time.Time    `json:"due_at,omitempty"`
	// Detail 处理情况。指针类型: 不传 = 保持原值, 传空串 = 清空。
	Detail *string `json:"detail,omitempty"`
	// UpdatedBy 操作人, 缺省时由服务端从鉴权上下文推导。
	UpdatedBy string `json:"updated_by,omitempty"`
	// CloseWithParent 是否随主单关闭。指针类型: 不传 = 保持原值。
	CloseWithParent *bool `json:"close_with_parent,omitempty"`
}
