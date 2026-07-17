package notification

import "time"

// ChannelType 标识通知渠道的技术类型。
// 与各 Sender 实现一一对应,Config 字段保存该类型特定的连接参数。
type ChannelType string

const (
	ChannelEmail    ChannelType = "email"    // SMTP 邮件
	ChannelDingTalk ChannelType = "dingtalk" // 钉钉机器人
	ChannelWeCom    ChannelType = "wecom"    // 企业微信群机器人
	ChannelFeishu   ChannelType = "feishu"   // 飞书自定义机器人
	ChannelWebhook  ChannelType = "webhook"  // 通用 HTTP Webhook
)

// EventType 标识可触发通知的平台事件类型。与 Policy 关联,
// 当事件发生时,NotificationService 查找匹配的 Policy 并向其绑定的 Channel 分发。
type EventType string

const (
	EventAlertTriggered  EventType = "alert.triggered"  // 告警触发
	EventAlertRecovered   EventType = "alert.recovered"  // 告警恢复
	EventTicketCreated    EventType = "ticket.created"   // 工单创建
	EventTicketAssigned   EventType = "ticket.assigned" // 工单指派
	EventTicketApproved   EventType = "ticket.approved"  // 工单审批通过
	EventAgentOffline     EventType = "agent.offline"    // Agent 离线
	EventWorkflowWaiting  EventType = "workflow.waiting" // 工作流等待审批
	EventWorkflowFailed   EventType = "workflow.failed"  // 工作流执行失败
)

// AllEventTypes 返回所有支持的事件类型,供前端展示与 Policy 配置使用。
func AllEventTypes() []EventType {
	return []EventType{
		EventAlertTriggered, EventAlertRecovered,
		EventTicketCreated, EventTicketAssigned, EventTicketApproved,
		EventAgentOffline,
		EventWorkflowWaiting, EventWorkflowFailed,
	}
}

// AllChannelTypes 返回所有支持的渠道类型。
func AllChannelTypes() []ChannelType {
	return []ChannelType{
		ChannelEmail, ChannelDingTalk, ChannelWeCom, ChannelFeishu, ChannelWebhook,
	}
}

// Channel 表示一个可发送通知的外部渠道配置。
// Config 字段因类型而异,详见各 Sender 实现的 Validate 方法。
type Channel struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Name      string            `json:"name"`
	Type      ChannelType       `json:"type"`
	Config    map[string]string `json:"config"`
	Enabled   bool              `json:"enabled"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Policy 表示事件到渠道的订阅策略。
// 当 Event 发生时,通知服务向 ChannelIDs 列表中的所有启用渠道分发消息。
// QuietHours 为免打扰时段 "HH:MM-HH:MM"(如 "22:00-08:00"),该时段内不发送。
type Policy struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	Name       string    `json:"name"`
	Event      EventType `json:"event"`
	ChannelIDs []string  `json:"channel_ids"`
	Template   string    `json:"template,omitempty"`   // 消息模板,支持 ${title} ${content} 变量
	QuietHours string    `json:"quiet_hours,omitempty"` // 免打扰时段 "HH:MM-HH:MM"
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Notification 表示一次通知发送的历史记录,用于审计与排错。
type Notification struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	ChannelID string    `json:"channel_id"`
	Event     EventType `json:"event"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Status    string    `json:"status"` // sent / failed
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateChannelRequest 创建渠道。
type CreateChannelRequest struct {
	Name    string            `json:"name"`
	Type    ChannelType       `json:"type"`
	Config  map[string]string `json:"config"`
	Enabled bool              `json:"enabled"`
}

// UpdateChannelRequest 更新渠道。
type UpdateChannelRequest struct {
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Enabled bool              `json:"enabled"`
}

// CreatePolicyRequest 创建策略。
type CreatePolicyRequest struct {
	Name       string    `json:"name"`
	Event      EventType `json:"event"`
	ChannelIDs []string  `json:"channel_ids"`
	Template   string    `json:"template"`
	QuietHours string    `json:"quiet_hours"`
	Enabled    bool      `json:"enabled"`
}

// UpdatePolicyRequest 更新策略。
type UpdatePolicyRequest struct {
	Name       string    `json:"name"`
	Event      EventType `json:"event"`
	ChannelIDs []string  `json:"channel_ids"`
	Template   string    `json:"template"`
	QuietHours string    `json:"quiet_hours"`
	Enabled    bool      `json:"enabled"`
}

// TestRequest 测试渠道发送。
type TestRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// SendRequest 触发一次事件通知(内部调用,用于测试整条链路)。
type SendRequest struct {
	Event   EventType `json:"event"`
	Title   string    `json:"title"`
	Content string    `json:"content"`
}
