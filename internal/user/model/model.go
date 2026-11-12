package model

import "time"

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

type User struct {
	ID                string     `json:"id"`
	TenantID          string     `json:"tenant_id"`
	Username          string     `json:"username"`
	Password          string     `json:"-"`
	Email             string     `json:"email,omitempty"`
	Department        string     `json:"department,omitempty"` // 部门/组织（如门店名），ITSM 提单人属性派单用
	Role              Role       `json:"role"`
	Status            string     `json:"status"`
	LastLogin         *time.Time `json:"last_login,omitempty"`
	PasswordChangedAt *time.Time `json:"password_changed_at,omitempty"` // 密码策略有效期检查基准（nil=老数据按不过期处理）
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (u *User) IsActive() bool {
	return u.Status == "active"
}
