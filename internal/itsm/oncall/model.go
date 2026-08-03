package oncall

import "time"

type Shift struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name"`
	Role      string    `json:"role"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Phone     string    `json:"phone,omitempty"`
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Rotation struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Name      string            `json:"name"`
	Members   []string          `json:"members"`
	CycleDays int               `json:"cycle_days"`
	Labels    map[string]string `json:"labels,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}
