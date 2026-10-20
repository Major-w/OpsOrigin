package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/iam"
)

// newGroupSeeder 通过 iam 自己的 HTTP 路由建组, 而不是绕过 Service 直接写 store。
//
// 理由: 该适配器消费的是 (tenant, userID) → 组 ID 的真实数据, 建组路径若与线上不一致
// (例如漏了 UserIDs 归一或租户归属), 测试就会"绿得很假"。走真实 handler 可以顺带覆盖它。
func newGroupSeeder(t *testing.T, svc *iam.Service) func(name string, userIDs []string) string {
	t.Helper()
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux) // 同一 mux 重复注册同一 pattern 会 panic, 故全局只建一次

	return func(name string, userIDs []string) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{"name": name, "user_ids": userIDs})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/iam/groups", bytes.NewReader(body)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create group %s: status=%d body=%s", name, rec.Code, rec.Body.String())
		}
		var g iam.Group
		if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
			t.Fatalf("decode group: %v", err)
		}
		if g.ID == "" {
			t.Fatalf("建组未返回 ID: %s", rec.Body.String())
		}
		return g.ID
	}
}

// TestIAMScopeResolverResolvesGroups 权限感知检索的装配层适配器:
// 「提问者 → 所属用户组」这一步错了, 后果是 team 文章对所有人不可见(作者却以为发给了团队)。
func TestIAMScopeResolverResolvesGroups(t *testing.T) {
	ctx := context.Background()
	svc := iam.NewService(nil) // 内存 store, 不需要 DB
	seed := newGroupSeeder(t, svc)

	opsID := seed("ops-team", []string{"u1", "u2"})
	netID := seed("net-team", []string{"u1"})
	_ = seed("fin-team", []string{"u9"})

	r := newIAMScopeResolver(svc)

	// 一个用户可在多个组 → 全部返回, 且顺序稳定(缓存键依赖它)
	got := r.ResolveScope(ctx, "default", "u1")
	if got.UserID != "u1" || got.TenantID != "default" {
		t.Fatalf("身份字段不符: %+v", got)
	}
	want := []string{opsID, netID}
	sort.Strings(want) // 解析结果按字典序排列(缓存键依赖稳定顺序)
	if !reflect.DeepEqual(got.GroupIDs, want) {
		t.Fatalf("应返回该用户的两个组(已排序), 实际 %v want %v", got.GroupIDs, want)
	}

	// 只返回自己所在的组: u2 只在运维组
	if got := r.ResolveScope(ctx, "default", "u2"); !reflect.DeepEqual(got.GroupIDs, []string{opsID}) {
		t.Fatalf("u2 应只属于运维组, 实际 %v", got.GroupIDs)
	}

	// 无组用户 → 空集合(而非 nil 之外的东西), 上层据此判 team 不可见
	if got := r.ResolveScope(ctx, "default", "u-nobody"); len(got.GroupIDs) != 0 {
		t.Fatalf("无组用户不应有组: %v", got.GroupIDs)
	}

	// 租户必须透传: 单租户模式下建组只会落到 default, 因此 "other" 必须查不到任何组。
	// 若适配器把租户写死成 "default", 这里会立刻返回运维组 → 跨租户越权。
	if got := r.ResolveScope(ctx, "other", "u1"); len(got.GroupIDs) != 0 {
		t.Fatalf("租户未透传(可能被写死为 default): %v", got.GroupIDs)
	}

	// 未传用户 → 零值 scope(上层 fail-closed 退化为仅公开)
	if got := r.ResolveScope(ctx, "default", ""); !got.IsZero() {
		t.Fatalf("空用户必须得到零值 scope, 实际 %+v", got)
	}
}

// TestIAMScopeResolverCachesAndExpires 缓存是这条链路的性能前提(检索高频, 每次 ListGroups 会把 DB 打满),
// 但缓存必须**过期生效** —— 否则用户换组后永久看不到新团队的知识。
func TestIAMScopeResolverCachesAndExpires(t *testing.T) {
	ctx := context.Background()
	svc := iam.NewService(nil)
	seed := newGroupSeeder(t, svc)
	firstID := seed("ops-team", []string{"u1"})

	r := newIAMScopeResolver(svc)
	r.ttl = time.Hour // 明确一个长 TTL, 便于观察"缓存生效"

	if got := r.ResolveScope(ctx, "default", "u1"); len(got.GroupIDs) != 1 {
		t.Fatalf("首次解析应有 1 个组, 实际 %v", got.GroupIDs)
	}

	// 新组加入后, 在 TTL 内仍读到缓存(这是设计取舍: 组变更延迟上限 = TTL)
	secondID := seed("net-team", []string{"u1"})
	if got := r.ResolveScope(ctx, "default", "u1"); !reflect.DeepEqual(got.GroupIDs, []string{firstID}) {
		t.Fatalf("TTL 内应复用缓存(仅 %s), 实际 %v", firstID, got.GroupIDs)
	}

	// TTL 到期后必须重新解析, 否则换组永远不生效。
	// 直接把已写入的条目过期(TTL 是写入时算的, 改 r.ttl 不会影响已有条目)。
	r.mu.Lock()
	for k, e := range r.entries {
		e.expires = time.Now().Add(-time.Second)
		r.entries[k] = e
	}
	r.mu.Unlock()

	got := r.ResolveScope(ctx, "default", "u1")
	if len(got.GroupIDs) != 2 {
		t.Fatalf("TTL 到期后应重新解析出 2 个组, 实际 %v", got.GroupIDs)
	}
	if !containsID(got.GroupIDs, secondID) {
		t.Fatalf("重新解析后应包含新加入的组 %s, 实际 %v", secondID, got.GroupIDs)
	}
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// TestIAMScopeResolverNilSafe 未启用 IAM(内存模式/未装配)时不得 panic, 且必须 fail-closed。
func TestIAMScopeResolverNilSafe(t *testing.T) {
	ctx := context.Background()

	if got := newIAMScopeResolver(nil).ResolveScope(ctx, "default", "u1"); len(got.GroupIDs) != 0 || got.UserID != "u1" {
		t.Fatalf("nil service 应返回无组的 scope, 实际 %+v", got)
	}
	var r *iamScopeResolver
	if got := r.ResolveScope(ctx, "default", "u1"); len(got.GroupIDs) != 0 {
		t.Fatalf("nil resolver 应返回无组的 scope, 实际 %+v", got)
	}
}

// TestIAMScopeResolverEvictsWhenFull 缓存有容量上限: 超限整体清空而不是无界增长。
func TestIAMScopeResolverEvictsWhenFull(t *testing.T) {
	ctx := context.Background()
	r := newIAMScopeResolver(iam.NewService(nil))

	// 直接灌满 entries(等价于上限被触及), 再解析一次应触发清空并正常返回
	r.mu.Lock()
	for i := 0; i < scopeCacheMaxEntries; i++ {
		r.entries[string(rune(i))+"k"] = scopeCacheEntry{expires: time.Now().Add(time.Hour)}
	}
	r.mu.Unlock()

	if got := r.ResolveScope(ctx, "default", "u1"); len(got.GroupIDs) != 0 {
		t.Fatalf("无组用户应得到空集合, 实际 %v", got.GroupIDs)
	}
	r.mu.Lock()
	n := len(r.entries)
	r.mu.Unlock()
	if n > scopeCacheMaxEntries {
		t.Fatalf("缓存条目数 %d 超过上限 %d", n, scopeCacheMaxEntries)
	}
}
