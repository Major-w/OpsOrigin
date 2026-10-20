package main

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/internal/iam"
)

// iamScopeResolver 把 iam 用户组解析成 AI 检索的可见范围(权限感知检索的装配层适配器)。
//
// 为什么放在 cmd/core 而不是 agentic 内部: agentic 不得反向依赖 internal/iam(分层单向契约)。
// 范式与已有的 RAGEvaluator / KnowledgeCreator / ScriptExecutor 一致 ——
// 领域侧只定义接口, 装配层注入实现。
//
// 为什么必须带缓存: 检索是高频路径(每次对话可能多次召回), 而 ListGroups 要取全部组
// 并逐个比对成员列表。不加缓存等于把一次知识库问答变成一次"全租户用户组扫描"。
// 缓存 30s 意味着组变更最多 30s 后生效 —— 检索场景不需要强一致(用户换组后立刻
// 期待旧文章的可见性翻转并不现实), 这个窗口可接受。
//
// fail-closed: 查询失败时返回空组集合, 而不是"返回全部组"。空组 → team 文章不可见,
// 即"DB 抖动时宁可少召回, 不可多召回"。
type iamScopeResolver struct {
	svc *iam.Service

	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]scopeCacheEntry
}

type scopeCacheEntry struct {
	groups  []string
	expires time.Time
}

// scopeCacheMaxEntries 缓存条目上限。键是 (租户,用户), 基数与用户数同阶;
// 超限时整体清空而不是 LRU —— 清空的代价只是几分钟内的重复查询, 换来无锁竞争的简单实现。
const scopeCacheMaxEntries = 4096

func newIAMScopeResolver(svc *iam.Service) *iamScopeResolver {
	return &iamScopeResolver{
		svc:     svc,
		ttl:     30 * time.Second,
		entries: make(map[string]scopeCacheEntry),
	}
}

// ResolveScope 实现 agentic.ScopeResolver。
//
// 返回的 scope 一定带 UserID(UserID 为空说明调用方没传身份), 此时上层会判为零值 → 仅公开。
func (r *iamScopeResolver) ResolveScope(ctx context.Context, tenantID, userID string) agentic.VisibilityScope {
	scope := agentic.VisibilityScope{TenantID: tenantID, UserID: userID}
	if r == nil || r.svc == nil || userID == "" {
		return scope
	}
	scope.GroupIDs = r.groupsOf(ctx, tenantID, userID)
	return scope
}

// groupsOf 返回 userID 所属的用户组 ID 列表(已排序, 便于缓存键稳定)。
func (r *iamScopeResolver) groupsOf(ctx context.Context, tenantID, userID string) []string {
	if tenantID == "" {
		tenantID = "default"
	}
	key := tenantID + "\x1f" + userID

	r.mu.Lock()
	if e, ok := r.entries[key]; ok && time.Now().Before(e.expires) {
		r.mu.Unlock()
		return e.groups
	}
	r.mu.Unlock()

	groups, err := r.svc.ListGroups(ctx, tenantID)
	if err != nil {
		// 不缓存失败: 下次请求重试, 避免一次抖动被放大成 30s 的可见性缺失。
		slog.Warn("ai scope: list iam groups failed, degrading to public-only retrieval", "err", err)
		return nil
	}
	out := make([]string, 0, 2)
	for _, g := range groups {
		for _, uid := range g.UserIDs {
			if uid == userID {
				out = append(out, g.ID)
				break
			}
		}
	}
	sort.Strings(out)

	r.mu.Lock()
	if len(r.entries) >= scopeCacheMaxEntries {
		r.entries = make(map[string]scopeCacheEntry)
	}
	r.entries[key] = scopeCacheEntry{groups: out, expires: time.Now().Add(r.ttl)}
	r.mu.Unlock()
	return out
}
