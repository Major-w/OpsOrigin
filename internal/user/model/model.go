package model

import "time"

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

type User struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	Username  string     `json:"username"`
	Password  string     `json:"-"`
	Email     string     `json:"email,omitempty"`
	Role      Role       `json:"role"`
	Status    string     `json:"status"`
	LastLogin *time.Time `json:"last_login,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (u *User) IsActive() bool {
	return u.Status == "active"
}
