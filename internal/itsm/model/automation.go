package model

import "time"

// RuleCondition 触发器条件: 对工单字段做匹配。
// Field 取值: status / priority / risk_level / category / assignee / created_by,
// 或 custom_fields.<key> 匹配自定义表单字段(值统一转字符串比较)。
// Op 取值: equals / not_equals / contains。
type RuleCondition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// RuleAction 触发器动作。
// Type=notify:   Target 为通知接收对象标识(交由通知中心 Policy 匹配), Message 为内容模板。
// Type=assign:   Target 为受派人, 自动分派工单。
// Type=webhook:  Target 为回调 URL, 复用工单出站 webhook 通道。
// Message 支持 ${ticket.<字段>} 与 ${custom_fields.<key>} 占位符。
type RuleAction struct {
	Type    string `json:"type"`
	Target  string `json:"target"`
	Message string `json:"message,omitempty"`
	// Channels 仅 Type=notify 时生效：显式指定通知渠道 ID 列表（直接投递，绕过
	// 通知 Policy 匹配与免打扰）。为空走默认路径：NotifyEvent("ticket.automation")
	// 由通知中心 Policy 决定投递到哪些渠道。
	Channels []string `json:"channels,omitempty"`
}

// RuleSchedule 定时调度配置（Event="schedule" 时必填，对标 SN Schedule 触发器）。
// Cron 与 IntervalMinutes 二选一：Cron 优先。
// Cron 为标准 5 段表达式「分 时 日 月 周」，支持 * , - / 数字（如 "0 9 * * 1-5" 工作日每天 9 点）；
// 按 Asia/Shanghai 本地时间对齐分钟粒度判定。
type RuleSchedule struct {
	Cron            string `json:"cron,omitempty"`
	IntervalMinutes int    `json:"interval_minutes,omitempty"`
}

// Valid 校验调度配置合法性：Cron 与 IntervalMinutes 至少配置一个且均合法。
func (rs *RuleSchedule) Valid() bool {
	if rs == nil {
		return false
	}
	if rs.IntervalMinutes > 0 {
		return true
	}
	_, err := ParseCron(rs.Cron)
	return err == nil
}

// AutomationRule ITSM 工单自动化触发器。
// Event 与工单事件同名(ticket.created/submitted/approved/rejected/executing/
// completed/rolled_back/closed/reopened/withdrawn)，命中条件后依序执行动作；
// Event="schedule" 时为定时触发器：按 Schedule 周期扫描，Conditions 非空时
// 对全部命中工单逐单执行动作（如定时巡检拉起变更流程），Conditions 为空时
// 仅执行 notify 动作（纯定时通知，无工单上下文）。
type AutomationRule struct {
	ID         string          `json:"id"`
	TenantID   string          `json:"tenant_id"`
	Name       string          `json:"name"`
	Event      string          `json:"event"`
	Enabled    bool            `json:"enabled"`
	Conditions []RuleCondition `json:"conditions,omitempty"`
	Actions    []RuleAction    `json:"actions"`
	Schedule   *RuleSchedule   `json:"schedule,omitempty"`
	CreatedBy  string          `json:"created_by,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// TicketButton 工单详情页自定义按钮。
// 点击后由前端映射到既有工单操作端点执行(ActionType 与工单操作 API 一一对应),
// 权限仍由既有端点把守; VisibleStatuses/Roles 控制按钮可见性(空=不限制)。
type TicketButton struct {
	ID              string         `json:"id"`
	TenantID        string         `json:"tenant_id"`
	Name            string         `json:"name"`
	ActionType      string         `json:"action_type"`
	ActionParams    map[string]any `json:"action_params,omitempty"`
	VisibleStatuses []string       `json:"visible_statuses,omitempty"`
	Roles           []string       `json:"roles,omitempty"`
	ConfirmText     string         `json:"confirm_text,omitempty"`
	SortOrder       int            `json:"sort_order"`
	Enabled         bool           `json:"enabled"`
	CreatedBy       string         `json:"created_by,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}
