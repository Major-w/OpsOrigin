// Package message 提供平台级「站内消息」能力——即 per-user 收件箱。
//
// 与 internal/notification 的分工:
//   - notification 是「事件 → 外部渠道」的单向外发(邮件/钉钉/企微/飞书/Webhook),
//     按 Policy 匹配事件后投递到群/邮箱,不关心具体收件人是谁、有没有读过。
//   - message 是「事件 → 站内收件箱」,每条消息归属一个具体用户,有已读状态,
//     支撑顶栏铃铛未读角标与消息中心列表。
//
// 收件人标识刻意做成 (UserID, Username) 双字段:平台鉴权链给出的是 user_id(UUID),
// 而 ITSM 业务主体(assignee / created_by / @提及)历史上一直用 username。
// 投递方能拿到哪个就填哪个,查询时两者任一命中即算本人消息,避免为了统一标识
// 去改动 ITSM 全量业务字段。
package message

import (
	"context"
	"strings"
	"time"
)

// Category 消息分类,用于前端分组与筛选。
type Category string

const (
	// CategoryTicket 工单流转(创建/指派/状态变更/关闭/重开/催办)。
	CategoryTicket Category = "ticket"
	// CategoryApproval 审批相关(待我审批 / 审批结果)。
	CategoryApproval Category = "approval"
	// CategoryMention 评论 @提及。
	CategoryMention Category = "mention"
	// CategorySubtask 子任务派发与进展。
	CategorySubtask Category = "subtask"
	// CategorySLA SLA/OLA 预警与违约。
	CategorySLA Category = "sla"
	// CategorySystem 系统公告/其他。
	CategorySystem Category = "system"
)

// Level 消息级别,决定前端角标与配色。
type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

// Message 一条站内消息(收件箱条目)。
type Message struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	// UserID 收件人 user_id(鉴权链主体 ID),投递方拿不到时可空。
	UserID string `json:"user_id,omitempty"`
	// Username 收件人用户名(ITSM 业务主体),投递方拿不到时可空。
	// UserID 与 Username 至少要有一个非空,否则消息无人可收。
	Username string   `json:"username,omitempty"`
	Category Category `json:"category"`
	Level    Level    `json:"level"`
	Title    string   `json:"title"`
	Content  string   `json:"content,omitempty"`
	// Link 前端跳转路由(如 /itsm/tickets?id=xxx)。
	Link string `json:"link,omitempty"`
	// RefType/RefID 业务对象引用,便于前端做二次跳转或去重。
	RefType string `json:"ref_type,omitempty"`
	RefID   string `json:"ref_id,omitempty"`
	// Actor 触发人展示名(谁指派的 / 谁 @ 的)。
	Actor     string     `json:"actor,omitempty"`
	Read      bool       `json:"read"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Recipient 收件人标识。UserID 与 Username 任一命中即视为本人。
type Recipient struct {
	UserID   string
	Username string
}

// Empty 判断收件人是否完全为空(无法定位任何用户)。
func (r Recipient) Empty() bool {
	return strings.TrimSpace(r.UserID) == "" && strings.TrimSpace(r.Username) == ""
}

// Matches 判断消息是否投递给该收件人。
func (r Recipient) Matches(m *Message) bool {
	if m == nil || r.Empty() {
		return false
	}
	if r.UserID != "" && m.UserID != "" && r.UserID == m.UserID {
		return true
	}
	if r.Username != "" && m.Username != "" && strings.EqualFold(r.Username, m.Username) {
		return true
	}
	return false
}

// ListQuery 收件箱查询条件。
type ListQuery struct {
	TenantID  string
	Recipient Recipient
	Category  string
	// OnlyUnread 仅返回未读。
	OnlyUnread bool
	Limit      int
	Offset     int
}

func (q ListQuery) normalized() ListQuery {
	if q.TenantID == "" {
		q.TenantID = "default"
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 20
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return q
}

// Store 站内消息持久化能力。
type Store interface {
	// Insert 批量写入消息(投递)。
	Insert(ctx context.Context, msgs ...*Message) error
	// List 分页查询收件箱,第二返回值为符合条件的总数。
	List(ctx context.Context, q ListQuery) ([]*Message, int, error)
	// UnreadCount 未读数(按分类可选过滤)。
	UnreadCount(ctx context.Context, tenantID string, r Recipient) (int, error)
	// MarkRead 标记单条已读(仅限本人消息)。
	MarkRead(ctx context.Context, tenantID, id string, r Recipient) error
	// MarkAllRead 标记全部已读,返回影响条数。
	MarkAllRead(ctx context.Context, tenantID string, r Recipient) (int, error)
	// Delete 删除单条(仅限本人消息)。
	Delete(ctx context.Context, tenantID, id string, r Recipient) error
}
