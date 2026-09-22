//go:build integration

package agentic

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/opsorigin/core/internal/knowledge"
)

// constVecEmbedder 返回固定维度的常量向量。
// 目的只是让向量路真的跑起来(检索侧要求 len(qv) == vector_dims(embedding)),
// 距离值本身无意义 —— 本测试断言的是"哪些文章被召回", 不是排序质量。
type constVecEmbedder struct{ dim int }

func (s constVecEmbedder) Embed(context.Context, string) ([]float32, error) {
	return make([]float32, s.dim), nil
}

// mapResolver 按 userID 返回预设的组集合, 模拟 iam 适配器。
type mapResolver map[string][]string

func (m mapResolver) ResolveScope(_ context.Context, tenantID, userID string) VisibilityScope {
	return VisibilityScope{TenantID: tenantID, UserID: userID, GroupIDs: m[userID]}
}

// TestPGScopeRecallMatrix 权限感知检索的**决定性**验收: 真库 + 真实检索 SQL 的召回矩阵。
//
// 为什么必须真库: 谓词里的数组重叠(`owner_group_ids && $n::text[]`)、NULLIF 归一、
// OR 组合的短路语义 —— sqlmock 只能验证"SQL 字符串长什么样", 验证不了"数据库怎么算"。
// 越权类缺陷代价极高(受限内容进入他人模型上下文), 必须有真库断言。
//
// 覆盖 6 篇文章 × 5 种身份 × 4 条召回 SQL(向量 / tsvector / ILIKE / 文章级兜底) + 引用校验。
// 故意含两个脏数据用例:
//   - team[] 归属为空 → 不得对任何人可见(数组重叠天然 false, fail-closed);
//   - 草稿 / 已过期 → 与可见性正交的约束, 不得因身份变化而被放行。
func TestPGScopeRecallMatrix(t *testing.T) {
	dsn := os.Getenv("OPSORIGIN_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSORIGIN_POSTGRES_DSN not set, skipping real-db test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Skipf("postgres unreachable (%v), skipping real-db test", err)
	}

	const (
		tenant = "it-scope-matrix"
		dim    = 2048
		// 两个互斥的探针词: 一个只出现在块内容里(逼出块级路径), 一个只出现在标题里(逼出文章级兜底)。
		chunkProbe = "scopematrixchunkprobe"
		titleProbe = "scopematrixtitleprobe"
		// 缓存隔离专用词: 关键词零命中, 只走向量路 —— 用于保证两个用户都是冷启动。
		cacheProbe = "scopematrixcacheprobe"
	)
	const (
		artPublic  = "it-scope-all"
		artTeam    = "it-scope-team"
		artTeamNil = "it-scope-team-empty"
		artOwner   = "it-scope-owner"
		artDraft   = "it-scope-draft"
		artExpired = "it-scope-expired"
	)
	const (
		ownerUser = "u-owner"
		groupA    = "g-alpha"
	)

	ctx := context.Background()
	cleanup := func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE tenant_id=$1`, tenant)
		_, _ = db.ExecContext(ctx, `DELETE FROM knowledge_articles WHERE tenant_id=$1`, tenant)
	}
	cleanup()
	defer cleanup()

	now := time.Now()
	type seedArt struct {
		id      string
		title   string
		view    string
		author  string
		groups  []string
		status  string
		expired bool
	}
	seed := []seedArt{
		{artPublic, "公开知识 " + titleProbe, "all", "", nil, "published", false},
		{artTeam, "团队知识 " + titleProbe, "team", "", []string{groupA}, "published", false},
		{artTeamNil, "空归属团队知识 " + titleProbe, "team", "", []string{}, "published", false},
		{artOwner, "个人知识 " + titleProbe, "owner", ownerUser, []string{}, "published", false},
		{artDraft, "草稿知识 " + titleProbe, "all", "", nil, "draft", false},
		{artExpired, "过期知识 " + titleProbe, "all", "", nil, "published", true},
	}

	for _, a := range seed {
		expires := sql.NullTime{}
		if a.expired {
			expires = sql.NullTime{Time: now.Add(-time.Hour), Valid: true}
		}
		// 注意 owner_group_ids 显式走 pq 数组字面量: 归属为空时写 '{}' 而非 NULL(列是 NOT NULL)。
		if _, err := db.ExecContext(ctx, `
			INSERT INTO knowledge_articles
				(id, tenant_id, title, content, category, tags, status, version, view_permission, edit_permission,
				 author_id, owner_group_ids, expires_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'it-scope','[]',$5,1,$6,'owner',$7,$8,$9,$10,$10)`,
			a.id, tenant, a.title, "正文(不含探针词)", a.status, a.view,
			a.author, "{"+strings.Join(a.groups, ",")+"}", expires, now); err != nil {
			t.Fatalf("insert article %s: %v", a.id, err)
		}
		// 每篇一个块, 块内容含块探针词, 并预置 embedding(让向量路真的跑起来)。
		if _, err := db.ExecContext(ctx, `
			INSERT INTO knowledge_chunks
				(id, article_id, tenant_id, chunk_index, heading, content, embedding, created_at, source, embed_dim, content_tsv)
			VALUES ($1,$2,$3,0,'正文',$4,
			        array_fill(0::real, ARRAY[$5::int])::vector, $6, 'article', $5,
			        to_tsvector('simple', $4))`,
			a.id+"-c0", a.id, tenant, chunkProbe+" 块内容", dim, now); err != nil {
			t.Fatalf("insert chunk for %s: %v", a.id, err)
		}
	}

	ts := NewMCPToolSet(db)
	ts.SetEmbedder(constVecEmbedder{dim: dim})
	ts.SetScopeResolver(mapResolver{
		ownerUser: {groupA},   // 作者本人, 同时在 g-alpha
		"u-mate":  {groupA},   // 同组同事
		"u-other": {"g-beta"}, // 别组同事
		"u-none":  {},         // 无组用户
	})

	// 身份 → 期望可见的文章集合
	identities := []struct {
		name   string
		userID string // 空 = 匿名(请求上下文无身份)
		want   map[string]bool
	}{
		{"作者本人(u-owner, 属 g-alpha)", ownerUser, map[string]bool{
			artPublic: true, artTeam: true, artOwner: true,
		}},
		{"同组同事(u-mate, 属 g-alpha)", "u-mate", map[string]bool{
			artPublic: true, artTeam: true,
		}},
		{"别组同事(u-other, 属 g-beta)", "u-other", map[string]bool{
			artPublic: true,
		}},
		{"无组用户(u-none)", "u-none", map[string]bool{
			artPublic: true,
		}},
		{"匿名(无身份)", "", map[string]bool{
			artPublic: true,
		}},
	}

	allIDs := make([]string, 0, len(seed))
	for _, a := range seed {
		allIDs = append(allIDs, a.id)
	}

	// 三条检索路径 + 引用校验, 必须全部遵守同一可见性口径
	paths := []struct {
		name string
		run  func(ctx context.Context) []string
	}{
		{"块级双路(向量+tsvector+ILIKE)", func(ctx context.Context) []string {
			items, err := ts.searchKnowledge(ctx, chunkProbe, 20, 300, nil)
			if err != nil {
				t.Fatalf("searchKnowledge: %v", err)
			}
			return idsOf(items)
		}},
		// 关键词零命中的查询词: 此时只有向量路能产出结果(块向量全为常量向量, 距离并列)。
		// 这既证明向量路确实执行了(不是静默降级到关键词路), 也验证了向量路的可见性谓词 ——
		// 若向量路漏改谓词, 这里会立刻看到别组同事召回 team/owner 文章。
		{"块级向量路(关键词零命中)", func(ctx context.Context) []string {
			items, err := ts.searchKnowledge(ctx, "zzznowherezzzprobe", 20, 300, nil)
			if err != nil {
				t.Fatalf("searchKnowledge: %v", err)
			}
			return idsOf(items)
		}},
		{"文章级兜底 ILIKE", func(ctx context.Context) []string {
			items, err := ts.searchKnowledgeFallback(ctx, titleProbe, 20, 300, nil)
			if err != nil {
				t.Fatalf("searchKnowledgeFallback: %v", err)
			}
			return idsOf(items)
		}},
		{"引用幻觉过滤 ValidKnowledgeIDs", func(ctx context.Context) []string {
			m := ts.ValidKnowledgeIDs(ctx, tenant, allIDs)
			out := make([]string, 0, len(m))
			for id := range m {
				out = append(out, id)
			}
			return out
		}},
	}

	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			for _, idn := range identities {
				idn := idn
				t.Run(idn.name, func(t *testing.T) {
					reqCtx := context.WithValue(context.Background(), "tenant_id", tenant)
					if idn.userID != "" {
						reqCtx = context.WithValue(reqCtx, "user_id", idn.userID)
					}
					got := map[string]bool{}
					for _, id := range p.run(reqCtx) {
						got[id] = true
					}
					for _, a := range seed {
						want := idn.want[a.id]
						if got[a.id] != want {
							t.Errorf("文章 %s(view=%s groups=%v author=%s status=%s expired=%v) 可见性不符: got=%v want=%v",
								a.id, a.view, a.groups, a.author, a.status, a.expired, got[a.id], want)
						}
					}
					// 反向断言: 不得混入本租户之外/本用例之外的文章
					for id := range got {
						if !strings.HasPrefix(id, "it-scope-") {
							t.Errorf("召回了非本用例文章: %s", id)
						}
					}
				})
			}
		})
	}

	// 缓存隔离的端到端断言: 作者先检索(会把私密文章写入缓存), 随后别组同事用**同一个查询词**检索,
	// 不得复用前者的结果。这是 P0 级越权场景, 必须走真实缓存路径验证。
	t.Run("缓存不得跨用户复用", func(t *testing.T) {
		// 接入留痕存储, 以便断言"第二次检索确实穿透了缓存"而不是悄悄命中了作者那条缓存。
		ts.SetSearchLogStore(knowledge.NewSearchLogStore(db))
		defer func() {
			_, _ = db.ExecContext(ctx, `DELETE FROM knowledge_search_logs WHERE tenant_id=$1`, tenant)
		}()

		authorCtx := context.WithValue(context.Background(), "tenant_id", tenant)
		authorCtx = context.WithValue(authorCtx, "user_id", ownerUser)
		otherCtx := context.WithValue(context.Background(), "tenant_id", tenant)
		otherCtx = context.WithValue(otherCtx, "user_id", "u-other")

		// 专用查询词: 保证两个用户都是"冷启动"(否则前面的子测试已经替他们把缓存建好了,
		// 断言 mode!=cache 就会因为"同身份命中同一缓存"这个正常现象而误报)。
		gotAuthor := idsOf(mustSearch(t, ts, authorCtx, cacheProbe))
		gotOther := idsOf(mustSearch(t, ts, otherCtx, cacheProbe))

		if !containsStr(gotAuthor, artOwner) {
			t.Fatalf("作者本人应能看到自己的 owner 文章, 实际 %v", gotAuthor)
		}
		if containsStr(gotOther, artOwner) || containsStr(gotOther, artTeam) {
			t.Fatalf("别组同事不得复用作者的缓存结果(越权): %v", gotOther)
		}

		// 留痕侧证据: 两个用户各自的这条查询都必须真的跑了检索(mode != cache)。
		// 若 scope 没进缓存键, 第二个用户会直接命中第一个用户的缓存条目 → mode=cache, 这里必红。
		logs, _, err := knowledge.NewSearchLogStore(db).List(ctx, tenant, knowledge.SearchLogFilter{Keyword: cacheProbe, Limit: 50})
		if err != nil {
			t.Fatalf("list search logs: %v", err)
		}
		seen := map[string]string{}
		for _, lg := range logs {
			if lg.Query == cacheProbe {
				seen[lg.UserID] = lg.Mode
			}
		}
		for _, u := range []string{ownerUser, "u-other"} {
			mode, ok := seen[u]
			if !ok {
				t.Fatalf("用户 %s 的检索没有留痕(应记录一条): %v", u, seen)
			}
			if mode == "cache" {
				t.Fatalf("用户 %s 的检索命中了缓存(mode=cache) —— 说明缓存键未按可见范围隔离, 存在越权复用", u)
			}
		}
	})
}

func idsOf(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if id, ok := it["id"].(string); ok && id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func mustSearch(t *testing.T, ts *MCPToolSet, ctx context.Context, q string) []map[string]any {
	t.Helper()
	items, err := ts.searchKnowledge(ctx, q, 20, 300, nil)
	if err != nil {
		t.Fatalf("searchKnowledge: %v", err)
	}
	return items
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
