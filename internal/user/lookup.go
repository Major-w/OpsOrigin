package user

import (
	"context"
	"sort"
	"strings"
)

// UserRef 轻量用户引用：给外部模块（如 ITSM 表单"选人"字段）取选项用，
// 不暴露密码等敏感字段。
type UserRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Role     string `json:"role,omitempty"`
}

// ListUsers 列出本租户的启用用户（按用户名排序）。
// keyword 非空时按用户名/邮箱模糊过滤；limit <= 0 时不限。
func (s *Service) ListUsers(ctx context.Context, tenantID, keyword string, limit int) ([]UserRef, error) {
	users, err := s.store.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))
	out := make([]UserRef, 0, len(users))
	for _, u := range users {
		if u == nil || strings.EqualFold(u.Status, "disabled") {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(u.Username), kw) &&
			!strings.Contains(strings.ToLower(u.Email), kw) {
			continue
		}
		out = append(out, UserRef{ID: u.ID, Username: u.Username, Email: u.Email, Role: string(u.Role)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
