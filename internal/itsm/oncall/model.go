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

// CurrentOncall 计算当前当班成员(按创建时间 + cycle_days 周期推进):
//   idx = (now - CreatedAt) / (cycle_days 天) % len(members)
// cycle_days <= 0 时视为 1(每日轮换); 空成员返回空字符串。
func (r *Rotation) CurrentOncall(now time.Time) string {
	if len(r.Members) == 0 {
		return ""
	}
	if len(r.Members) == 1 {
		return r.Members[0]
	}
	cycle := r.CycleDays
	if cycle <= 0 {
		cycle = 1
	}
	base := r.CreatedAt
	if base.IsZero() {
		base = now
	}
	elapsed := now.Sub(base)
	if elapsed < 0 {
		elapsed = 0
	}
	periods := int(elapsed.Hours() / (24 * float64(cycle)))
	idx := periods % len(r.Members)
	return r.Members[idx]
}
