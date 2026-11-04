package model

import "time"

// TicketTemplateContent 提单模板的预填内容：工单创建时的系统字段与自定义字段初值。
// 与 TicketTemplate 元数据分离存储（PG 中以 content JSONB 落库），便于整段读写。
type TicketTemplateContent struct {
	RiskLevel    string            `json:"risk_level,omitempty"`    // 风险级别 low/medium/high/critical
	Title        string            `json:"title,omitempty"`         // 预填标题
	Description  string            `json:"description,omitempty"`   // 预填描述
	ChangePlan   string            `json:"change_plan,omitempty"`   // 预填变更计划
	RollbackPlan string            `json:"rollback_plan,omitempty"` // 预填回滚计划
	CIIDs        []string          `json:"ci_ids,omitempty"`        // 预填关联资产
	Labels       map[string]string `json:"labels,omitempty"`        // 预填标签
	CustomFields map[string]any    `json:"custom_fields,omitempty"` // 预填自定义字段（与工单 custom_fields 同结构）
}

// TicketTemplate 提单模板：保存一类工单的预填字段，便于快速创建同类工单
// （如「数据库故障」「机房断电」）。模板本身可按 category / form_scope / process_id 归类，
// 但创建工单时仅作预填参考，不强制约束。
type TicketTemplate struct {
	ID        string                `json:"id"`
	TenantID  string                `json:"tenant_id"`
	Name      string                `json:"name"`                 // 模板名称（唯一语义上建议唯一）
	Remark    string                `json:"remark,omitempty"`     // 模板说明
	Category  string                `json:"category,omitempty"`   // 变更/事件/问题/请求
	FormScope string                `json:"form_scope,omitempty"` // 适用的表单作用域（如 ticket:default）
	ProcessID string                `json:"process_id,omitempty"` // 关联流程模板 ID（创建时一并带入）
	CatalogID string                `json:"catalog_id,omitempty"` // 关联服务目录项 ID
	Content   TicketTemplateContent `json:"content"`              // 预填内容
	CreatedBy string                `json:"created_by"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}
