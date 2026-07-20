package model

import "time"

type Comment struct {
	ID        int64     `json:"id"`
	TicketID  string    `json:"ticket_id"`
	TenantID  string    `json:"tenant_id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
