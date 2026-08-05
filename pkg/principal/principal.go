// Package principal 提供全平台统一的「请求主体」上下文载体。
//
// 设计目标:
//  1. 收口:此前用户身份散落在三处——pkg/auth 的裸字符串 context key
//     ("user_id"/"tenant_id"/"role")、internal/audit/interceptor 的 Principal、
//     以及各业务模块自行解析的请求头。本包作为唯一出口。
//  2. 可信:Principal 只能由服务端鉴权链构造(JWT 解析 / 内部服务凭证校验),
//     业务代码只读不写。客户端请求头不再是权限判定的依据。
//  3. 零依赖:仅用标准库,可被 pkg/auth、internal/* 任意层引用而不产生环。
//
// 典型用法:
//
//	// 鉴权中间件(唯一写入点)
//	ctx = principal.NewContext(ctx, principal.Principal{...})
//
//	// 业务代码(只读)
//	uid := principal.UserID(ctx)
//	if !principal.Has(ctx, "cmdb:write") { ... }
package principal

import (
	"context"
	"strings"
)

// Type 主体类型。
type Type string

const (
	// TypeHuman 人类用户(浏览器 Bearer 登录)。
	TypeHuman Type = "HUMAN"
	// TypeAIAgent AI 数字员工(自主执行动作,需可追溯到触发工单)。
	TypeAIAgent Type = "AI_AGENT"
	// TypeService 内部服务/Agent 进程(服务间调用,凭内部凭证)。
	TypeService Type = "SERVICE"
)

// ParseType 归一化主体类型字符串(大小写不敏感)。第二返回值表示是否为已知类型。
func ParseType(s string) (Type, bool) {
	switch Type(strings.ToUpper(strings.TrimSpace(s))) {
	case TypeHuman:
		return TypeHuman, true
	case TypeAIAgent:
		return TypeAIAgent, true
	case TypeService:
		return TypeService, true
	}
	return "", false
}

// Principal 请求主体。所有字段均由服务端派生,不接受客户端直接赋值。
type Principal struct {
	// Type 主体类型:HUMAN / AI_AGENT / SERVICE。
	Type Type `json:"type"`
	// ID 主体唯一标识:人类为 user_id,AI 为 agent_id,服务为服务名。
	ID string `json:"id"`
	// DisplayName 展示名(人类为 users.username),缺失时回落 ID。
	DisplayName string `json:"display_name,omitempty"`
	// TenantID 租户,仅来自 JWT 或内部凭证声明,禁止来自普通请求头。
	TenantID string `json:"tenant_id,omitempty"`
	// Role 主角色名(RBAC 入口),权限以 Permissions 为准。
	Role string `json:"role,omitempty"`
	// Permissions 服务端解析出的最终权限集合(角色 + 用户组聚合)。
	Permissions []string `json:"permissions,omitempty"`
	// TicketID AI 主体的触发工单号,用于动作可追溯。
	TicketID string `json:"ticket_id,omitempty"`
}

// Valid 判断主体是否可用于鉴权决策。
func (p Principal) Valid() bool {
	if p.ID == "" {
		return false
	}
	_, ok := ParseType(string(p.Type))
	return ok
}

// IsHuman 是否为人类主体。
func (p Principal) IsHuman() bool { return p.Type == TypeHuman }

// IsAI 是否为 AI 数字员工主体。
func (p Principal) IsAI() bool { return p.Type == TypeAIAgent }

// IsService 是否为内部服务主体。
func (p Principal) IsService() bool { return p.Type == TypeService }

// Name 返回展示名,缺失时回落到 ID(保证审计 actor 永不为空)。
func (p Principal) Name() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.ID
}

// Has 判断是否拥有指定权限。支持三种匹配:
//   - 精确匹配:"cmdb:write"
//   - 模块通配:持有 "cmdb:*" 命中所有 cmdb:xxx
//   - 超级通配:持有 "*" 命中一切(仅内置 admin 场景使用)
//
// 空权限集一律返回 false —— 最小权限原则,缺省不放行。
func (p Principal) Has(permission string) bool {
	if permission == "" {
		return false
	}
	for _, granted := range p.Permissions {
		if granted == permission || granted == "*" {
			return true
		}
		if strings.HasSuffix(granted, ":*") {
			prefix := strings.TrimSuffix(granted, "*")
			if strings.HasPrefix(permission, prefix) {
				return true
			}
		}
	}
	return false
}

// HasAny 判断是否拥有任一权限。
func (p Principal) HasAny(permissions ...string) bool {
	for _, perm := range permissions {
		if p.Has(perm) {
			return true
		}
	}
	return false
}

// HasAll 判断是否拥有全部权限。
func (p Principal) HasAll(permissions ...string) bool {
	for _, perm := range permissions {
		if !p.Has(perm) {
			return false
		}
	}
	return len(permissions) > 0
}

// WithPermissions 返回补充权限后的副本(供鉴权链后段的权限解析器回填)。
func (p Principal) WithPermissions(perms []string) Principal {
	p.Permissions = perms
	return p
}

// WithDisplayName 返回补充展示名后的副本(供身份富化器回填)。
func (p Principal) WithDisplayName(name string) Principal {
	p.DisplayName = name
	return p
}

// String 用于日志,不输出权限明细避免日志膨胀。
func (p Principal) String() string {
	var b strings.Builder
	b.WriteString(string(p.Type))
	b.WriteByte(':')
	b.WriteString(p.ID)
	if p.DisplayName != "" && p.DisplayName != p.ID {
		b.WriteByte('(')
		b.WriteString(p.DisplayName)
		b.WriteByte(')')
	}
	if p.TenantID != "" {
		b.WriteString("@")
		b.WriteString(p.TenantID)
	}
	return b.String()
}

// ---------------------------------------------------------------
// context 载体:类型化 key,杜绝裸字符串碰撞
// ---------------------------------------------------------------

type contextKey struct{}

var principalKey contextKey

// NewContext 将主体写入 context。仅鉴权链应调用,业务代码只读。
func NewContext(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// FromContext 取出主体。第二返回值表示是否存在。
func FromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// Get 取出主体,不存在时返回零值(便于链式取字段)。
func Get(ctx context.Context) Principal {
	p, _ := FromContext(ctx)
	return p
}

// UserID 当前主体 ID,不存在返回空串。
func UserID(ctx context.Context) string { return Get(ctx).ID }

// TenantID 当前租户,不存在返回空串。
func TenantID(ctx context.Context) string { return Get(ctx).TenantID }

// DisplayName 当前主体展示名(回落 ID)。
func DisplayName(ctx context.Context) string { return Get(ctx).Name() }

// Role 当前主体主角色。
func Role(ctx context.Context) string { return Get(ctx).Role }

// Permissions 当前主体的服务端解析权限集合(副本,防止调用方篡改)。
func Permissions(ctx context.Context) []string {
	perms := Get(ctx).Permissions
	if len(perms) == 0 {
		return nil
	}
	out := make([]string, len(perms))
	copy(out, perms)
	return out
}

// Has 当前主体是否拥有指定权限。上下文缺失时返回 false(最小权限)。
func Has(ctx context.Context, permission string) bool { return Get(ctx).Has(permission) }

// HasAny 当前主体是否拥有任一权限。
func HasAny(ctx context.Context, permissions ...string) bool {
	return Get(ctx).HasAny(permissions...)
}
