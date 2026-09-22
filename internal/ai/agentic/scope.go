package agentic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/lib/pq"
	"github.com/opsorigin/core/pkg/auth"
)

// —— 权限感知检索: 可见性范围与谓词参数化 ——
//
// 背景: 此前 AI 检索的可见性谓词是常量 kbRetrievablePredicate, 对 view_permission=team/owner
// 的文章一律 fail-closed (因为当时库里没有作者/归属列, 无法判定归属), 于是「团队私有 SOP」
// 与「个人笔记」对 AI 永久不可见 —— 只能改成 all (等于全租户可见) 或干脆用不上。
//
// 批次 1 已补上归属列 (author_id / owner_group_ids), 本文件把「谁在问」这件事
// 下沉为可注入的 ScopeResolver, 并把常量谓词改成按身份生成。
//
// 分层: agentic 不得反向依赖 internal/iam, 故这里只定义接口与零依赖的值类型,
// 由 cmd/core 注入 iam 适配器 (与 RAGEvaluator / KnowledgeCreator 同款范式)。

// VisibilityScope 一次检索的调用者可见范围。
//
// 零值 (UserID 为空) 表示「未认证或身份解析失败」→ fail-closed, 只可见公开文章。
// 这不是错误路径而是安全默认: AI 是代表提问者作答, 解析不出提问者就只能给公开内容。
type VisibilityScope struct {
	TenantID string
	UserID   string
	GroupIDs []string // 提问者所属用户组 ID
}

// IsZero 判定 scope 是否不可用 (未认证/解析失败)。
func (s VisibilityScope) IsZero() bool {
	return strings.TrimSpace(s.UserID) == ""
}

// ScopeResolver 把 (租户, 用户) 解析成可见范围。
//
// 实现由 cmd/core/main.go 注入 (内部委托 iam.Service.ListGroups), 必须带缓存 ——
// 检索是高频路径, 每次召回都扫一遍用户组会把 DB 打满。
type ScopeResolver interface {
	ResolveScope(ctx context.Context, tenantID, userID string) VisibilityScope
}

// ScopeResolverFunc 把普通函数适配成 ScopeResolver。
// 供装配层写轻量实现、以及测试里构造特定身份用 —— 避免每个测试都定义一个结构体。
type ScopeResolverFunc func(ctx context.Context, tenantID, userID string) VisibilityScope

// ResolveScope 实现 ScopeResolver。
func (f ScopeResolverFunc) ResolveScope(ctx context.Context, tenantID, userID string) VisibilityScope {
	return f(ctx, tenantID, userID)
}

// SetScopeResolver 注入可见性范围解析器。未注入时 resolveScope 返回零值 scope
// (行为等同改造前: 只召回公开文章), 属 fail-closed 而非 fail-open。
func (ts *MCPToolSet) SetScopeResolver(r ScopeResolver) { ts.scopeResolver = r }

// SetScopeResolver 注入可见性范围解析器(引擎侧)。用途与 MCPToolSet 的同名方法相同,
// 但服务的是**引擎级工具结果缓存**的键隔离 —— 见 execToolChecked 的缓存键构造。
// 两处必须注入同一个实现, 否则会出现「谓词按身份过滤、缓存却跨用户复用」的错配。
func (e *Engine) SetScopeResolver(r ScopeResolver) { e.scopeResolver = r }

// resolveScope 解析本次检索的可见范围。任何一环缺失 (未注入 / 未认证) 都返回零值。
func (ts *MCPToolSet) resolveScope(ctx context.Context) VisibilityScope {
	return resolveScopeWith(ts.scopeResolver, ctx)
}

// resolveScope 引擎侧同上。
func (e *Engine) resolveScope(ctx context.Context) VisibilityScope {
	return resolveScopeWith(e.scopeResolver, ctx)
}

// resolveScopeWith 两个调用方共用的解析逻辑, 保证「未认证 → 零值 → fail-closed」只有一处实现。
func resolveScopeWith(r ScopeResolver, ctx context.Context) VisibilityScope {
	userID := strings.TrimSpace(auth.UserIDFromContext(ctx))
	if r == nil || userID == "" {
		return VisibilityScope{}
	}
	scope := r.ResolveScope(ctx, tenantOf(ctx), userID)
	scope.UserID = strings.TrimSpace(scope.UserID)
	if scope.TenantID == "" {
		scope.TenantID = tenantOf(ctx)
	}
	return scope
}

// kbPublicOnlyPredicate 是可见性谓词的**退化分支**: 未认证/无法解析身份时只召回
// 「已发布 + 未过期 + 公开可见」的文章。
//
// 该常量的内容与改造前的 kbRetrievablePredicate 逐字相同, 保留它有两个作用:
//  1. 未认证路径 (ChatOps 桥接、无身份的定时任务) 的 SQL 与改造前完全一致, 便于灰度对照;
//  2. 参数位为 0, 可安全拼接在任意 WHERE 末尾, 不需要参数分配器参与。
//
// ⚠️ 空串必须等价于 all: 该列的语义是 "all(默认)"(见 model.Article), 建表默认值也是 'all',
// 但写入路径不保证带上该字段 —— 历史数据与按 API 直接创建的文章大量是 ” 而非 'all'。
// 早期只写 COALESCE(view_permission,'all') 只兜了 NULL, 于是这一批「已发布但未显式声明可见性」
// 的文章被整体挡在检索之外(实测: 库里 published 文章仅 2/5 可召回), 属于静默的功能性故障。
//
// 拆成两段是为了给检索测试台复用:「不含可见性」的计数查询要的正是 kbVisibleStatusPredicate
// (保留状态/过期约束、摘掉可见性约束), 用它减掉带可见性的候选数, 才得到"ACL 过滤掉多少"。
// 拼接后的字节与拆开前逐字相同, 契约测试(visibility_predicate_test.go)不受影响。
const kbVisibleStatusPredicate = ` AND a.status = 'published'
				   AND (a.expires_at IS NULL OR a.expires_at > now())`

const kbPublicOnlyPredicate = kbVisibleStatusPredicate + `
				   AND COALESCE(NULLIF(a.view_permission, ''), 'all') = 'all'`

// scopeArgs 统一分配 scope 相关参数位, 杜绝多条召回 SQL 手写 $N 错位。
//
// 改造涉及 5 条 SQL, 各自已有参数个数不同 (向量路 4 个、关键词路 3 个、兜底与引用校验各 4/2 个),
// 且带不带分类过滤还会再差 1 个 —— 手写 $N 一旦错位, 最坏情况是**过滤条件绑到错误的值上**
// (例如把用户 ID 当成租户), 静默失效且很难从结果上看出来。故统一由构造器按 base 长度推导。
//
// 参数位分配 (与设计文档 2.3 的表一致):
//
//	块级向量路      base=$1..$4 [+$5 分类] → scope $5/$6 [+分类时 $6/$7]
//	块级 tsvector   base=$1..$3 [+$4 分类] → scope $4/$5
//	块级 ILIKE      base=$1..$3 [+$4 分类] → scope $4/$5
//	文章级兜底      base=$1..$4 [+$5 分类] → scope $5/$6
//	ValidKnowledgeIDs base=$1..$2          → scope $3/$4
type scopeArgs struct {
	clause string
	args   []any
}

// newScopeArgs 以 base (该 SQL 已绑定的前置参数, 按占位符顺序) 为前缀构造 scope 参数。
//
// scope 为零值时退化为 kbPublicOnlyPredicate, 且不追加任何参数 —— 此时 SQL 与改造前逐字相同。
func newScopeArgs(scope VisibilityScope, base ...any) *scopeArgs {
	if scope.IsZero() {
		return &scopeArgs{clause: kbPublicOnlyPredicate, args: base}
	}
	start := len(base) + 1
	u, g := start, start+1

	// 「公开」判定放最前: 它是绝大多数文章的命中分支, 短路求值可省掉后续比较。
	// owner 分支额外要求 author_id <> '' —— 存量文章 author_id 为空, 不能让空串与空串相等
	// (那是"没人认领的文章对所有人可见", 与 fail-closed 相悖)。
	// team 分支用数组重叠 '&&': '{ops}' && '{ops,net}' = true; '{}' && anything = false → 天然 fail-closed。
	clause := fmt.Sprintf(` AND a.status = 'published'
				   AND (a.expires_at IS NULL OR a.expires_at > now())
				   AND (
				         COALESCE(NULLIF(a.view_permission, ''), 'all') = 'all'
				      OR (a.view_permission = 'owner' AND a.author_id <> '' AND a.author_id = $%d)
				      OR (a.view_permission = 'team' AND a.owner_group_ids && $%d::text[])
				   )`, u, g)

	// pq.Array(nil) 会编码成 SQL NULL, 使 `&& NULL` 得到 NULL 而非 false。
	// 虽然 OR 整体仍会判假 (fail-closed), 但显式传空数组能让 SQL 语义清晰、也可被索引正常处理。
	groups := scope.GroupIDs
	if groups == nil {
		groups = []string{}
	}
	args := make([]any, 0, len(base)+2)
	args = append(args, base...)
	args = append(args, scope.UserID, pq.Array(groups))
	return &scopeArgs{clause: clause, args: args}
}

// Predicate 返回可见性子句 (含前导 " AND ")。
func (sa *scopeArgs) Predicate() string { return sa.clause }

// Args 返回拼接后的完整参数 (base + scope 参数), 直接展开进 QueryContext。
func (sa *scopeArgs) Args() []any { return sa.args }

// scopeCacheKey 生成可见范围的短哈希, 用于给两条检索缓存做键隔离。
//
// 为什么必须隔离: searchCache 与引擎级 toolCache 都只带 (租户, 查询, 参数),
// 没有用户维度。一旦谓词开始按身份变化, 同租户下 A 检索到的受限内容会被 B 的相同查询
// 直接复用 —— 越权泄漏, 且因为走的是缓存分支, 连检索留痕都显示为正常命中。
//
// 用哈希而非明文: 缓存键可能进日志/调试输出, 明文会暴露用户的组成员关系。
// 8 hex = 32bit, 对「进程内 1024 条」量级的缓存而言碰撞概率可忽略 (碰撞后果仅是命中率略降)。
func scopeCacheKey(scope VisibilityScope) string {
	if scope.IsZero() {
		return "anon"
	}
	groups := append([]string(nil), scope.GroupIDs...)
	sort.Strings(groups) // 组顺序不影响可见性, 但必须影响键 → 排序保证同一组集合得到同一键
	h := sha256.Sum256([]byte(scope.UserID + "\x1f" + strings.Join(groups, ",")))
	return hex.EncodeToString(h[:4])
}
