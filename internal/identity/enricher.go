// Package identity 把「用户是谁、能做什么」的服务端解析收敛到一处,
// 作为 pkg/auth.PrincipalEnricher 的实现注入鉴权链。
//
// 解决两件事:
//  1. 权限:此前 CMDB 等模块直接读客户端 X-Permissions 头做字段级鉴权,
//     客户端可自报提权。现统一由 IAM 角色库聚合(角色 + 用户组)。
//  2. 展示名:JWT 只有 user_id,审计事件里看到的是一串 ID。
//     这里按 uid 回查 users.username,让 actor 可读。
//
// 两类查询都在请求热路径上,因此带 TTL 缓存;权限配置变更后最长 TTL 秒生效。
package identity

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// 默认缓存时长:权限变更后最长 30s 生效,展示名 5min。
const (
	defaultPermTTL = 30 * time.Second
	defaultNameTTL = 5 * time.Minute
)

// PermissionResolver 由 IAM 服务提供:聚合 (租户, 用户, 主角色) 的最终权限。
type PermissionResolver func(ctx context.Context, tenantID, userID, primaryRole string) []string

type cacheEntry[T any] struct {
	val T
	exp time.Time
}

type ttlCache[T any] struct {
	mu   sync.RWMutex
	ttl  time.Duration
	data map[string]cacheEntry[T]
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, data: make(map[string]cacheEntry[T])}
}

func (c *ttlCache[T]) get(key string) (T, bool) {
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.exp) {
		var zero T
		return zero, false
	}
	return e.val, true
}

func (c *ttlCache[T]) set(key string, val T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 朴素容量保护:超过阈值直接清空,避免长期运行内存无界增长。
	if len(c.data) > 10000 {
		c.data = make(map[string]cacheEntry[T])
	}
	c.data[key] = cacheEntry[T]{val: val, exp: time.Now().Add(c.ttl)}
}

// Invalidate 清空缓存(角色权限被编辑后调用,让新配置立即生效)。
func (c *ttlCache[T]) Invalidate() {
	c.mu.Lock()
	c.data = make(map[string]cacheEntry[T])
	c.mu.Unlock()
}

// Enricher 实现 auth.PrincipalEnricher。
type Enricher struct {
	db       *sql.DB
	resolver PermissionResolver
	perms    *ttlCache[[]string]
	names    *ttlCache[string]
}

// NewEnricher 构造富化器。db 为空时展示名回落空串(不阻断鉴权);
// resolver 为空时权限回落空集,由 auth 层用内置角色映射兜底。
func NewEnricher(db *sql.DB, resolver PermissionResolver) *Enricher {
	return &Enricher{
		db:       db,
		resolver: resolver,
		perms:    newTTLCache[[]string](defaultPermTTL),
		names:    newTTLCache[string](defaultNameTTL),
	}
}

// Permissions 返回服务端解析的最终权限集合(带缓存)。
func (e *Enricher) Permissions(ctx context.Context, tenantID, userID, role string) []string {
	if e == nil || e.resolver == nil {
		return nil
	}
	key := tenantID + "|" + userID + "|" + role
	if v, ok := e.perms.get(key); ok {
		return v
	}
	perms := e.resolver(ctx, tenantID, userID, role)
	e.perms.set(key, perms)
	return perms
}

// DisplayName 按 uid 查 users.username(带缓存)。查不到返回空串,由调用方回落 ID。
func (e *Enricher) DisplayName(ctx context.Context, tenantID, userID string) string {
	if e == nil || e.db == nil || userID == "" {
		return ""
	}
	key := tenantID + "|" + userID
	if v, ok := e.names.get(key); ok {
		return v
	}

	// 查询限定在 3 秒内,避免用户目录抖动拖垮整条请求链。
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var name sql.NullString
	err := e.db.QueryRowContext(qctx,
		`SELECT username FROM users WHERE id = $1 AND ($2 = '' OR tenant_id = $2) LIMIT 1`,
		userID, tenantID).Scan(&name)
	switch {
	case err == sql.ErrNoRows:
		// 缓存空结果,避免不存在的 ID(如 AI Agent、服务主体)每次都打库。
		e.names.set(key, "")
		return ""
	case err != nil:
		slog.Debug("identity: display name lookup failed", "user_id", userID, "error", err)
		return ""
	}
	e.names.set(key, name.String)
	return name.String
}

// InvalidatePermissions 权限配置变更后调用,使缓存立即失效。
func (e *Enricher) InvalidatePermissions() {
	if e != nil {
		e.perms.Invalidate()
	}
}

// InvalidateNames 用户资料变更后调用。
func (e *Enricher) InvalidateNames() {
	if e != nil {
		e.names.Invalidate()
	}
}
