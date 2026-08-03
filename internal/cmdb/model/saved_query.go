package model

import "time"

// SavedQuery 是「保存的查询策略」（"我想查"能力）。
// 用户把常用实例查询（模型 + 关键字 + 属性过滤 + 状态）存为策略，下次一键复用；
// 同时可被采集任务引用为「动态执行目标」（P1-4）：调度时按策略实时解析命中的 CI 集合。
type SavedQuery struct {
	ID        string         `json:"id"`
	TenantID  string         `json:"tenant_id"`
	Name      string         `json:"name"`
	TypeID    string         `json:"type_id,omitempty"` // 限定的模型类型（可空=不限）
	Query     map[string]any `json:"query"`             // 查询条件：{q, status, attrs:{k:v}}
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// SavedQueryFilter 从 Query 中解析出的结构化过滤条件（供 repo SQL 拼装）。
type SavedQueryFilter struct {
	TypeID string            // 限定模型类型（SavedQuery.TypeID，可空=不限）
	Q      string            // 名称/描述关键字（ILIKE）
	Status string            // CI 状态精确匹配（含 archived）
	Attrs  map[string]string // 属性精确匹配（attributes->>'k' = v）
}
