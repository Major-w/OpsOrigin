package identity

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPermissionsCachedAndInvalidated(t *testing.T) {
	var calls int32
	e := NewEnricher(nil, func(_ context.Context, tenantID, userID, role string) []string {
		atomic.AddInt32(&calls, 1)
		return []string{"cmdb:read", role + ":marker"}
	})

	ctx := context.Background()
	got := e.Permissions(ctx, "t1", "u1", "operator")
	if len(got) != 2 || got[1] != "operator:marker" {
		t.Fatalf("解析结果错误: %v", got)
	}

	// 同 key 命中缓存,不再回源
	e.Permissions(ctx, "t1", "u1", "operator")
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("缓存未命中,回源 %d 次", calls)
	}

	// 不同角色是不同 key
	e.Permissions(ctx, "t1", "u1", "admin")
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("不同角色应各自回源, calls=%d", calls)
	}

	// 失效后重新回源
	e.InvalidatePermissions()
	e.Permissions(ctx, "t1", "u1", "operator")
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("失效后应回源, calls=%d", calls)
	}
}

func TestPermissionsNilSafe(t *testing.T) {
	var nilEnricher *Enricher
	if p := nilEnricher.Permissions(context.Background(), "t", "u", "r"); p != nil {
		t.Fatal("nil 富化器不得 panic,应返回 nil")
	}
	if n := nilEnricher.DisplayName(context.Background(), "t", "u"); n != "" {
		t.Fatal("nil 富化器展示名应为空串")
	}
	nilEnricher.InvalidatePermissions()
	nilEnricher.InvalidateNames()

	// resolver 未注入 → 返回 nil,交由 auth 层回落内置角色映射
	e := NewEnricher(nil, nil)
	if p := e.Permissions(context.Background(), "t", "u", "admin"); p != nil {
		t.Fatal("resolver 未注入应返回 nil")
	}
	// db 未注入 → 展示名空串,不阻断鉴权
	if n := e.DisplayName(context.Background(), "t", "u"); n != "" {
		t.Fatal("db 未注入应返回空串")
	}
}

func TestTTLExpiry(t *testing.T) {
	c := newTTLCache[string](20 * time.Millisecond)
	c.set("k", "v")
	if v, ok := c.get("k"); !ok || v != "v" {
		t.Fatal("写入后应可读")
	}
	time.Sleep(35 * time.Millisecond)
	if _, ok := c.get("k"); ok {
		t.Fatal("过期后不应命中")
	}
	if _, ok := c.get("missing"); ok {
		t.Fatal("未写入的 key 不应命中")
	}
}

func TestTTLCacheCapacityGuard(t *testing.T) {
	c := newTTLCache[int](time.Minute)
	for i := 0; i < 10005; i++ {
		c.set(string(rune(i)), i)
	}
	c.mu.RLock()
	size := len(c.data)
	c.mu.RUnlock()
	if size > 10001 {
		t.Fatalf("容量保护未生效, size=%d", size)
	}
}

func TestConcurrentAccess(t *testing.T) {
	e := NewEnricher(nil, func(_ context.Context, _, _, role string) []string {
		return []string{role}
	})
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				e.Permissions(context.Background(), "t", "u", "operator")
				if j%10 == 0 {
					e.InvalidatePermissions()
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}
