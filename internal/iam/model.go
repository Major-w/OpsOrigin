package iam

import "time"

// Permission 表示平台的一个功能权限点。权限以 "资源:动作" 形式命名，
// 与 pkg/auth/rbac.go 中的 Permission 常量保持一致，便于 RBAC 中间件统一校验。
type Permission struct {
	ID          string `json:"id"`
	Name        string `json:"name"`         // 如 cmdb:write
	Description string `json:"description"`  // 人类可读说明
	Category    string `json:"category"`     // 分组:cmdb / monitor / itsm / user / agent / audit / system
}

// Role 表示一个角色，聚合若干权限。IsBuiltIn=true 的角色由系统预置，
// 不可删除（但可编辑权限）。用户可通过 CRUD 创建自定义角色。
type Role struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`          // 唯一角色名(如 admin/ops-developer)
	DisplayName string    `json:"display_name"`  // 展示名
	Description string    `json:"description,omitempty"`
	Permissions []string  `json:"permissions"`   // permission name 列表
	IsBuiltIn   bool      `json:"is_built_in"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Group 表示用户组，用于按集合管理用户(如"DBA团队"、"SRE组")。
type Group struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	UserIDs     []string  `json:"user_ids"` // 组成员用户 ID 列表
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateRoleRequest 创建角色的请求体。
type CreateRoleRequest struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// UpdateRoleRequest 更新角色。
type UpdateRoleRequest struct {
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// CreateGroupRequest 创建用户组。
type CreateGroupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	UserIDs     []string `json:"user_ids"`
}

// UpdateGroupRequest 更新用户组。
type UpdateGroupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	UserIDs     []string `json:"user_ids"`
}

// AssignUserRolesRequest 为用户分配角色(全量覆盖)。
type AssignUserRolesRequest struct {
	RoleIDs []string `json:"role_ids"`
}

// UserRoles 表示某用户被分配的角色列表。
type UserRoles struct {
	UserID  string   `json:"user_id"`
	RoleIDs []string `json:"role_ids"`
	Roles   []*Role  `json:"roles,omitempty"` // 展开后的角色详情(响应时填充)
}
