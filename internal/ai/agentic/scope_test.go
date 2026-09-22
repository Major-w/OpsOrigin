package agentic

import (
	"context"
	"database/sql/driver"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
)

// —— 权限感知检索(scope.go)的契约测试 ——
//
// 这一批改造的风险集中在两处:
//  1. 参数位错位 —— 5 条 SQL 的 scope 起始位各不相同, 写错会把"用户 ID"绑到"租户"上,
//     结果不是报错而是静默查错数据;
//  2. 漏改调用点 —— 少改一处(尤其是引用校验 ValidKnowledgeIDs), 就是一条绕过 ACL 的侧门。
//
// 故这里既有参数分配的单元断言, 也有对源码结构的静态断言。

// placeholders 抽出 SQL 片段里的 $N。
func placeholders(s string) []int {
	var out []int
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > i+1 {
			if n, err := strconv.Atoi(s[i+1 : j]); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

// TestScopeArgsAllocation 钉死 5 条 SQL 的 scope 参数位, 并断言「参数个数 = 最大占位符号」。
//
// 后者是真正的关键不变量: lib/pq 按位置绑定, args 少了会报错, args 多了会静默忽略,
// 而占位符号若与 args 顺序不一致, 就会把值绑到错误的列上。
func TestScopeArgsAllocation(t *testing.T) {
	scope := VisibilityScope{TenantID: "t1", UserID: "u1", GroupIDs: []string{"g1"}}

	cases := []struct {
		name  string
		base  []any
		start int // 期望的 scope 起始参数位
	}{
		{"块级向量路(无分类)", []any{"lit", 15, "t1", 2048}, 5},
		{"块级向量路(带分类)", []any{"lit", 15, "t1", 2048, []string{"cat"}}, 6},
		{"块级 tsvector(无分类)", []any{"q", 15, "t1"}, 4},
		{"块级 tsvector(带分类)", []any{"q", 15, "t1", []string{"cat"}}, 5},
		{"块级 ILIKE(无分类)", []any{"%q%", 15, "t1"}, 4},
		{"块级 ILIKE(带分类)", []any{"%q%", 15, "t1", []string{"cat"}}, 5},
		{"文章级兜底(无分类)", []any{"%q%", 1200, 5, "t1"}, 5},
		{"文章级兜底(带分类)", []any{"%q%", 1200, 5, "t1", []string{"cat"}}, 6},
		{"ValidKnowledgeIDs", []any{"t1", []string{"a1"}}, 3},
		{"评估抽样(无分类)", []any{"t1", 20}, 3},
		{"评估抽样(带分类)", []any{"t1", 20, []string{"cat"}}, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sa := newScopeArgs(scope, c.base...)

			got := placeholders(sa.Predicate())
			if len(got) != 2 || got[0] != c.start || got[1] != c.start+1 {
				t.Fatalf("scope 参数位应为 $%d/$%d, 实际 %v\n%s", c.start, c.start+1, got, sa.Predicate())
			}

			// 参数个数必须正好覆盖到最后一个占位符 (base + 2 个 scope 参数)
			wantLen := c.start + 1
			if len(sa.Args()) != wantLen {
				t.Fatalf("参数个数应为 %d (最后一个占位符号), 实际 %d —— 参数位与 args 数量不匹配会让绑定错位",
					wantLen, len(sa.Args()))
			}
			// 前 len(base) 个参数必须原样保留, 后两个才是 scope 参数
			for i := range c.base {
				if !reflect.DeepEqual(sa.Args()[i], c.base[i]) {
					t.Fatalf("第 %d 个参数被改动: %v != %v", i+1, sa.Args()[i], c.base[i])
				}
			}
			if sa.Args()[wantLen-2] != "u1" {
				t.Fatalf("倒数第二个参数应为提问者 UserID, 实际 %v", sa.Args()[wantLen-2])
			}
		})
	}
}

// TestScopeArgsAnonDegradesToPublicOnly 未认证/解析失败时必须退化为仅公开, 且不追加参数。
//
// 这是 fail-closed 的落点: 零值 scope 绝不能被当成"可见所有"。
func TestScopeArgsAnonDegradesToPublicOnly(t *testing.T) {
	base := []any{"a", "b"}
	sa := newScopeArgs(VisibilityScope{}, base...)

	if sa.Predicate() != kbPublicOnlyPredicate {
		t.Fatalf("零值 scope 的谓词必须与退化常量逐字相同, 实际:\n%s", sa.Predicate())
	}
	if len(sa.Args()) != len(base) {
		t.Fatalf("零值 scope 不得追加任何参数, 实际 %d 个: %v", len(sa.Args()), sa.Args())
	}
	// UserID 为纯空白同样算未认证(解析器返回脏数据时不能被当成有效身份)
	if !(VisibilityScope{}).IsZero() {
		t.Fatal("空 scope 必须 IsZero")
	}
	if !(VisibilityScope{UserID: "  "}).IsZero() {
		t.Fatal("IsZero 应忽略纯空白 UserID")
	}
	if (VisibilityScope{UserID: "u1"}).IsZero() {
		t.Fatal("有用户身份时不得 IsZero")
	}
}

// TestOwnerGroupIDsPredicateUsesArrayOverlap 钉死 team 判定走数组重叠, 而不是等值/IN 展开。
//
// 三个理由:
//  1. 归属是多值列(TEXT[]), 等值比较 `owner_group_id = $n` 在列名上就不存在;
//  2. 不能拼 IN ('a','b') 字面量 —— 那是把用户数据拼进 SQL(SQL 红线);
//  3. '{}' && anything = false 天然 fail-closed, 无需额外的 <> ” 防御。
func TestOwnerGroupIDsPredicateUsesArrayOverlap(t *testing.T) {
	p := newScopeArgs(VisibilityScope{UserID: "u1", GroupIDs: []string{"g1"}}, "base1").Predicate()

	if !strings.Contains(p, "a.owner_group_ids && $") {
		t.Fatalf("team 分支必须用数组重叠 (a.owner_group_ids && $n::text[]), 实际:\n%s", p)
	}
	if !strings.Contains(p, "::text[]") {
		t.Fatalf("组参数必须显式转型为 text[], 否则驱动可能按未知类型传参:\n%s", p)
	}
	if strings.Contains(p, "IN (") || strings.Contains(p, "owner_group_id =") {
		t.Fatalf("不得退化为等值比较或 IN 字面量拼接:\n%s", p)
	}
	// owner 分支必须要求 author_id 非空, 否则"空 author_id = 空 author_id"会让未认领文章对所有人可见
	if !strings.Contains(p, "a.author_id <> ''") {
		t.Fatalf("owner 分支必须排除空 author_id(否则未认领文章会被判为对所有人可见):\n%s", p)
	}
	if !strings.Contains(p, "a.status = 'published'") || !strings.Contains(p, "a.expires_at") {
		t.Fatalf("scope 谓词必须同时保留状态与有效期约束, 否则受限分支会绕过它们:\n%s", p)
	}
}

// TestScopeArgsNoStringInterpolation 生成 SQL 不得包含任何用户输入字面量。
//
// 谓词里唯一的变量是 $N 占位符; 用户 ID / 组 ID 一律走参数。若哪天有人图省事写成
// fmt.Sprintf("... = '%s'", userID), 这个测试必须红。
func TestScopeArgsNoStringInterpolation(t *testing.T) {
	scope := VisibilityScope{UserID: "u-inject'--", GroupIDs: []string{"g'--", "g2"}}
	sa := newScopeArgs(scope, "base1", "base2")

	p := sa.Predicate()
	if strings.Contains(p, "u-inject") || strings.Contains(p, "g'--") {
		t.Fatalf("谓词不得包含用户输入字面量(SQL 注入风险):\n%s", p)
	}
	// 用户 ID 与组必须原样出现在参数里(证明它们确实走了参数通道)
	if sa.Args()[2] != "u-inject'--" {
		t.Fatalf("UserID 必须按参数传递, 实际 %v", sa.Args()[2])
	}
	if !strings.Contains(p, "a.view_permission = 'owner'") {
		// 顺带确认谓词结构没被上面的断言掩盖
		t.Fatalf("owner 分支缺失:\n%s", p)
	}
}

// TestScopeArgsGroupsNeverEncodeAsSQLNull 组为空时不得编码成 SQL NULL。
//
// pq.Array(nil) → SQL NULL, 会让 `&&` 返回 NULL 而非 false。虽然 OR 整体仍判假(安全),
// 但 NULL 会传染到整个表达式, 使检索优化器无法走索引, 语义也不清晰。
func TestScopeArgsGroupsNeverEncodeAsSQLNull(t *testing.T) {
	for _, groups := range [][]string{nil, {}} {
		sa := newScopeArgs(VisibilityScope{UserID: "u1", GroupIDs: groups}, "base1")
		v, ok := sa.Args()[2].(driver.Valuer)
		if !ok {
			t.Fatalf("组参数必须实现 driver.Valuer(应为 pq.Array 的产物), 实际 %T", sa.Args()[2])
		}
		enc, err := v.Value()
		if err != nil {
			t.Fatalf("编码组参数失败: %v", err)
		}
		if enc == nil {
			t.Fatalf("组为空时必须编码为 '{}' 而不是 SQL NULL (input=%v)", groups)
		}
	}
}

// TestScopeCacheKeyIsolatesUsersAndGroups 缓存键必须按用户与组集合区分。
//
// 这是 P0 隐蔽风险的落点: searchCache 与引擎级 toolCache 原本只带 (租户,查询,参数),
// 加 ACL 后不补 scope 就会出现「A 检索到的团队私有内容被 B 的同名查询复用」。
func TestScopeCacheKeyIsolatesUsersAndGroups(t *testing.T) {
	u1a := scopeCacheKey(VisibilityScope{UserID: "u1", GroupIDs: []string{"a"}})
	u2a := scopeCacheKey(VisibilityScope{UserID: "u2", GroupIDs: []string{"a"}})
	u1b := scopeCacheKey(VisibilityScope{UserID: "u1", GroupIDs: []string{"b"}})
	anon := scopeCacheKey(VisibilityScope{})

	for name, pair := range map[string][2]string{
		"不同用户":     {u1a, u2a},
		"同用户不同组":   {u1a, u1b},
		"认证 vs 匿名": {u1a, anon},
	} {
		if pair[0] == pair[1] {
			t.Fatalf("%s 的 scopeKey 不得相同(%q)", name, pair[0])
		}
	}
	if anon != "anon" {
		t.Fatalf("匿名 scope 的 key 应为 anon, 实际 %q", anon)
	}
	// 组顺序不影响可见性 → 也不应影响键(否则同一身份的两次请求各建一条缓存, 命中率腰斩)
	if scopeCacheKey(VisibilityScope{UserID: "u1", GroupIDs: []string{"a", "b"}}) !=
		scopeCacheKey(VisibilityScope{UserID: "u1", GroupIDs: []string{"b", "a"}}) {
		t.Fatal("组顺序不应影响 scopeKey")
	}
	// 短哈希: 不得泄漏用户 ID 明文
	if strings.Contains(u1a, "u1") {
		t.Fatalf("scopeKey 不得包含明文用户 ID: %q", u1a)
	}
}

// TestResolveScopeFailClosed 未注入解析器 / 未认证时必须返回零值 scope。
func TestResolveScopeFailClosed(t *testing.T) {
	ctxNoUser := context.WithValue(context.Background(), "tenant_id", "t1")
	ctxUser := context.WithValue(context.Background(), "user_id", "u1")
	ctxUser = context.WithValue(ctxUser, "tenant_id", "t1")

	// 1) 未注入解析器
	ts := NewMCPToolSet(nil)
	if got := ts.resolveScope(ctxUser); !got.IsZero() {
		t.Fatalf("未注入解析器时必须 fail-closed, 实际 %+v", got)
	}
	// 2) 注入了但请求无身份
	ts.SetScopeResolver(stubResolver{groups: []string{"g1"}})
	if got := ts.resolveScope(ctxNoUser); !got.IsZero() {
		t.Fatalf("无用户身份时必须 fail-closed, 实际 %+v", got)
	}
	// 3) 正常解析
	got := ts.resolveScope(ctxUser)
	if got.UserID != "u1" || len(got.GroupIDs) != 1 || got.TenantID != "t1" {
		t.Fatalf("scope 解析结果不符: %+v", got)
	}
	// 4) 解析器返回带空白的用户 ID 也要被归一
	ts.SetScopeResolver(stubResolver{userID: " u9 ", groups: []string{"g1"}})
	if got := ts.resolveScope(ctxUser); got.UserID != "u9" {
		t.Fatalf("用户 ID 应被 trim, 实际 %q", got.UserID)
	}
}

type stubResolver struct {
	userID string
	groups []string
}

func (s stubResolver) ResolveScope(_ context.Context, tenantID, userID string) VisibilityScope {
	out := VisibilityScope{TenantID: tenantID, UserID: userID, GroupIDs: s.groups}
	if s.userID != "" {
		out.UserID = s.userID
	}
	return out
}

// TestEngineToolCacheKeyIncludesScope 引擎级缓存键必须为 knowledge_search 拼入 scope。
//
// 且**必须拼在末尾**: knowledgeSearchCachePrefix 是按前缀失效的, 拼到前面会让
// FlushKnowledgeSearchCache 失效能力静默失效(下架的文章继续被缓存召回)。
func TestEngineToolCacheKeyIncludesScope(t *testing.T) {
	ctx := context.WithValue(context.Background(), "user_id", "u1")
	ctx = context.WithValue(ctx, "tenant_id", "t1")
	args := map[string]any{"query": "磁盘满"}

	eng := NewEngine(mcp.NewRegistry(), nil, nil, nil)
	eng.SetScopeResolver(stubResolver{groups: []string{"ops"}})
	k1 := eng.toolCacheKey(ctx, "knowledge_search", args)

	// 前缀不变(失效能力依赖它)
	if !strings.HasPrefix(k1, knowledgeSearchCachePrefix) {
		t.Fatalf("key 必须以 %q 开头, 实际 %q", knowledgeSearchCachePrefix, k1)
	}

	// 换个用户 → key 必须不同
	ctx2 := context.WithValue(context.Background(), "user_id", "u2")
	ctx2 = context.WithValue(ctx2, "tenant_id", "t1")
	if k2 := eng.toolCacheKey(ctx2, "knowledge_search", args); k2 == k1 {
		t.Fatalf("不同用户的 knowledge_search 缓存键必须不同, 都是 %q", k1)
	}

	// 别的工具不受影响(CMDB 等与身份无关, 加 scope 只会白白降低命中率)
	if k := eng.toolCacheKey(ctx, "cmdb_query", args); strings.Contains(k, "\x1fanon") {
		t.Fatalf("非检索类工具不应拼 scope: %q", k)
	}

	// 未注入解析器时退化为旧键格式(灰度期行为不变)
	eng2 := NewEngine(mcp.NewRegistry(), nil, nil, nil)
	if got := eng2.toolCacheKey(ctx, "knowledge_search", args); got != "knowledge_search\x1ft1\x1f"+canonicalArgsKey(args) {
		t.Fatalf("未注入解析器时 key 应退化为旧格式, 实际 %q", got)
	}
}

// TestEveryRetrievalPathUsesScopedPredicate 静态结构断言: 每条召回 SQL 都必须走 scope 谓词。
//
// 这是"漏改一处 = 一条绕过 ACL 的侧门"的机器化防线 —— 尤其是 ValidKnowledgeIDs(引用校验),
// 漏改它不会让检索多召回内容, 却会让受限文章的标题/ID 通过模型引用输出泄漏。
func TestEveryRetrievalPathUsesScopedPredicate(t *testing.T) {
	// 站点地图(每条召回/过滤 SQL 一处, 漏一处就是一条绕过 ACL 的侧门):
	//
	//	retrieval_explain.go  4 处: 块级向量 / 块级 tsvector / 块级 ILIKE / 测试台 ACL 计数
	//	tools.go              2 处: 文章级兜底 searchKnowledgeFallback / 引用校验 ValidKnowledgeIDs
	//	rageval.go            1 处: 评估抽样 SampleEvalCases
	const minRetrievalSites = 4 // retrieval_explain.go
	const minToolSites = 2      // tools.go

	// 检索实现(批次 4)已从 tools.go 拆到 retrieval_explain.go。守卫必须覆盖全部相关文件:
	// 只扫一个文件的话, 把 SQL 搬到另一个文件就能"合法地"绕过这条防线。
	retrievalSrc := stripGoComments(readSourceFile(t, "retrieval_explain.go"))
	toolsSrc := stripGoComments(readSourceFile(t, "tools.go"))
	ragevalSrc := stripGoComments(readSourceFile(t, "rageval.go"))

	if n := strings.Count(retrievalSrc, "newScopeArgs("); n < minRetrievalSites {
		t.Fatalf("retrieval_explain.go 应至少有 %d 处 newScopeArgs(每条召回 SQL 一处), 实际 %d 处 —— 漏改的那条会绕过可见性过滤",
			minRetrievalSites, n)
	}
	// 引用校验 ValidKnowledgeIDs 与文章级兜底仍留在 tools.go:
	// 前者不改变召回, 但漏改会让受限文章标题/ID 经模型引用泄漏。
	if n := strings.Count(toolsSrc, "newScopeArgs("); n < minToolSites {
		t.Fatalf("tools.go 应至少有 %d 处 newScopeArgs(文章级兜底 / 引用校验), 实际 %d 处", minToolSites, n)
	}
	total := strings.Count(retrievalSrc, "newScopeArgs(") + strings.Count(toolsSrc, "newScopeArgs(")
	if total < minRetrievalSites+minToolSites {
		t.Fatalf("检索路径合计至少 %d 处可见性谓词分配, 实际 %d 处", minRetrievalSites+minToolSites, total)
	}
	// 每个分配器都必须被真正用掉, 否则等于造了谓词却不拼进 SQL。
	// 允许"用多于分配"(向量路的 halfvec 降级重试用同一个 sa), 故只要求 use >= alloc。
	for _, f := range []struct{ name, src string }{
		{"retrieval_explain.go", retrievalSrc},
		{"tools.go", toolsSrc},
	} {
		if alloc, use := strings.Count(f.src, "newScopeArgs("), strings.Count(f.src, ".Predicate()"); use < alloc {
			t.Fatalf("%s 中 .Predicate() 少于 newScopeArgs(%d < %d): 有分配器未被使用, 对应 SQL 缺可见性过滤", f.name, use, alloc)
		}
	}
	if strings.Count(ragevalSrc, "newScopeArgs(") < 1 || strings.Count(ragevalSrc, ".Predicate()") < 1 {
		t.Fatal("评估抽样(SampleEvalCases)必须按可见性过滤, 否则评估语料含不可见文章导致指标失真")
	}
	// 退化常量只能在 scope.go 内部使用; 任何直接拼接都是绕过身份判定的通道。
	// kbVisibleStatusPredicate 不在此列: 它**不含**可见性判定(只有状态/过期), 仅被测试台的
	// 「ACL 过滤量」计数复用; 用在 count 查询里不构成取数通道。
	for name, src := range map[string]string{
		"retrieval_explain.go": retrievalSrc, "tools.go": toolsSrc, "rageval.go": ragevalSrc,
	} {
		if strings.Contains(src, "kbPublicOnlyPredicate") {
			t.Fatalf("%s 不得直接使用退化谓词常量(必须经 newScopeArgs 按身份生成)", name)
		}
		if strings.Contains(src, "kbRetrievablePredicate") {
			t.Fatalf("%s 仍在引用已删除的旧常量 kbRetrievablePredicate", name)
		}
	}
}

func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return string(b)
}

// stripGoComments 去掉行注释与块注释。
//
// 结构断言针对的是「代码是否使用了某标识符」, 而注释里提到标识符(例如解释"谓词已迁走"的说明)
// 不该被判成违规。粗粒度过滤即可 —— 这些源文件里不存在字符串字面量带 // 的情况。
func stripGoComments(src string) string {
	var b strings.Builder
	lines := strings.Split(src, "\n")
	inBlock := false
	for _, ln := range lines {
		if inBlock {
			if i := strings.Index(ln, "*/"); i >= 0 {
				inBlock = false
				ln = ln[i+2:]
			} else {
				continue
			}
		}
		if i := strings.Index(ln, "/*"); i >= 0 {
			inBlock = true
			ln = ln[:i]
		}
		if i := strings.Index(ln, "//"); i >= 0 {
			ln = ln[:i]
		}
		b.WriteString(ln)
		b.WriteString("\n")
	}
	return b.String()
}
