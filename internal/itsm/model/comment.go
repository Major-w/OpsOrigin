package model

import "time"

type Comment struct {
	ID        int64      `json:"id"`
	TicketID  string     `json:"ticket_id"`
	TenantID  string     `json:"tenant_id"`
	Author    string     `json:"author"`
	Content   string     `json:"content"`
	Type      string     `json:"type,omitempty"`      // normal / system / approval / internal
	Mentions  []string   `json:"mentions,omitempty"`  // @提及的用户列表
	Edited    bool       `json:"edited,omitempty"`    // 是否被编辑过
	EditedAt  *time.Time `json:"edited_at,omitempty"` // 最后编辑时间
	CreatedAt time.Time  `json:"created_at"`
}
