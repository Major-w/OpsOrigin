package knowledge

import "time"

type Article struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Category  string            `json:"category"`
	Tags      []string          `json:"tags,omitempty"`
	Author    string            `json:"author"`
	Views     int               `json:"views"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}