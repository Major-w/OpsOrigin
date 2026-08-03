package model

import "time"

// ModelCategory 是 CI 模型的资源分类（业务/应用/中间件/数据库/基础设施 + 用户自定义）。
// 与 CIModelType.Category 通过分类名关联；内置分类不可删除。
type ModelCategory struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`   // 分类名（如"业务资源"），租户内唯一
	Builtin   bool      `json:"builtin"` // 内置分类（seed 5 类，不可删除）
	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
}
