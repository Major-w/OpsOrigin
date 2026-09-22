package agentic

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opsorigin/core/internal/knowledge"
)

// —— 检索测试台(批次 4)测试 ——

// kbKeywordNoDistanceSentinel 关键词路占位距离的期望值(mock 行与真实 SQL 都必须是它)。
const kbKeywordNoDistanceSentinel = -1.0

// TestKeywordPathHasNoVectorDistance 关键词路的占位距离必须是 -1。
//
// 回归守卫: knowledgeHit.distance 的契约是「-1 = 无向量距离」, rerankBonus 与分数分解
// 都靠 `>= 0` 判断是否计入相似度。曾经这里写的是 1.0, 于是纯关键词命中的文章被算出
// 0.3 的相似度加成、把只靠 ILIKE 命中的文章顶到向量召回之前 —— 排序表面正常但口径是错的。
func TestKeywordPathHasNoVectorDistance(t *testing.T) {
	fields := strings.Fields(kbKeywordNoDistanceExpr)
	if len(fields) < 1 || !strings.HasPrefix(fields[0], "-1") {
		t.Fatalf("关键词路距离表达式必须以 -1 占位, 实际 %q", kbKeywordNoDistanceExpr)
	}
	got, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		t.Fatalf("无法解析占位距离 %q: %v", fields[0], err)
	}
	if got != kbKeywordNoDistanceSentinel {
		t.Fatalf("kbKeywordNoDistanceExpr 的数值 = %v, 与契约哨兵 %v 不一致", got, kbKeywordNoDistanceSentinel)
	}
	// 关键词路自身的 SQL 不得出现正向距离常量(排查时最容易把 1.0 又写回来)。
	// 注意: 必须比字段而非子串 —— "-1.0 AS distance" 本身含子串 "1.0 AS distance"。
	for _, bad := range []string{"1.0", "0.0", "0", "1"} {
		if fields[0] == bad {
			t.Fatalf("关键词路距离退化为 %q —— 会让纯关键词命中白得相似度加成", bad)
		}
	}
	// 关键词命中且无向量命中: 相似度分量必须为 0
	if bonus := rerankBonus(kbKeywordNoDistanceSentinel, true, false); bonus != 0.25 {
		t.Fatalf("关键词命中无向量命中时加成应恰为 0.25, 实际 %v", bonus)
	}
	if bonus := rerankBonus(kbKeywordNoDistanceSentinel, false, false); bonus != 0 {
		t.Fatalf("无任何命中加成应为 0, 实际 %v", bonus)
	}
}

// TestNormalizeRetrieveModeRejectsUnknown 参数拼错不许静默换一套召回逻辑。
func TestNormalizeRetrieveModeRejectsUnknown(t *testing.T) {
	cases := map[string]string{
		"": "rrf", "RRF": "rrf", " rrF ": "rrf",
		"vector": "vector", "keyword": "keyword", "fallback": "fallback",
		"VECTOR": "vector",
		// 未知值必须退回默认, 否则一个拼错的 mode 会悄悄改变检索语义
		"semantic": "rrf", "sql": "rrf", "-": "rrf",
	}
	for in, want := range cases {
		if got := NormalizeRetrieveMode(in); got != want {
			t.Fatalf("NormalizeRetrieveMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// newExplainTestToolSet 造一个带 sqlmock 的工具集(无 embedder → 向量路自动跳过)。
func newExplainTestToolSet(t *testing.T) (*MCPToolSet, *sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ts := &MCPToolSet{db: db, embedCache: newTTLCache(embedCacheTTL, 8), searchCache: newTTLCache(searchCacheTTL, 8)}
	return ts, db, mock
}

// TestSearchKnowledgeExplainedModeGatesRoutes 验证模式选择真的决定了跑哪几条路,
// 且未执行的路必须留下原因(测试台要能解释"为什么没有向量结果")。
func TestSearchKnowledgeExplainedModeGatesRoutes(t *testing.T) {
	ts, _, mock := newExplainTestToolSet(t)
	ctx := context.Background()

	// mode=vector 但未配 embedder: 向量路跳过, 关键词路按模式跳过, 且不做兜底 → 零 DB 查询。
	items, ex, err := ts.searchKnowledgeExplained(ctx, "disk full", 5, 500, RetrieveOptions{Mode: "vector", BypassCache: true, NoLog: true})
	if err != nil {
		t.Fatalf("explained search: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("no embedder + vector-only mode must yield no results, got %v", items)
	}
	if !strings.Contains(ex.Vector.Skipped, "embedding") {
		t.Fatalf("向量路应说明跳过原因(未配 embedding), 实际 %q", ex.Vector.Skipped)
	}
	for name, rt := range map[string]RouteTrace{"tsvector": ex.Keyword.TSVector, "ilike": ex.Keyword.ILIKE} {
		if rt.Skipped == "" {
			t.Fatalf("mode=vector 时 %s 路必须标记为未执行", name)
		}
	}
	if ex.Route != "rrf" {
		t.Fatalf("route = %q, want rrf(融合分支)", ex.Route)
	}
	if ex.Scoped {
		t.Fatal("无身份 ctx 时必须 Scoped=false(退化为仅公开), 否则测试台会误报权限正常")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("vector-only 且无 embedder 时不应查库: %v", err)
	}

	// mode=keyword: 只跑 tsvector + ILIKE, 向量路标记跳过。
	// 关键词行的 distance 必须回填 -1(与 kbKeywordNoDistanceExpr 一致): 关键词路没有向量距离,
	// 若这里填 1.0 就等于给纯关键词命中白送 0.3 的相似度加成 —— 那正是本批次修掉的失真。
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("disk full", 15, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols).
			AddRow("a1", "Disk Full", "2 磁盘满", "cleanup steps", "k1", "article", 3, kbKeywordNoDistanceSentinel))
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("%disk full%", 15, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols))
	// 首命中块的相邻块上下文查询(small-to-big)
	mock.ExpectQuery("SELECT chunk_index, content FROM knowledge_chunks").
		WillReturnRows(sqlmock.NewRows([]string{"chunk_index", "content"}))

	items, ex, err = ts.searchKnowledgeExplained(ctx, "disk full", 5, 500, RetrieveOptions{Mode: "keyword", BypassCache: true, NoLog: true})
	if err != nil {
		t.Fatalf("keyword search: %v", err)
	}
	if len(items) != 1 || items[0]["id"] != "a1" {
		t.Fatalf("unexpected keyword result: %v", items)
	}
	if ex.Vector.Skipped == "" {
		t.Fatal("mode=keyword 时向量路必须标记为未执行")
	}
	if ex.Keyword.TSVector.Count != 1 || ex.Keyword.ILIKE.Count != 0 {
		t.Fatalf("关键词两路子查询命中数应分别为 1/0, 实际 %d/%d", ex.Keyword.TSVector.Count, ex.Keyword.ILIKE.Count)
	}
	if len(ex.Keyword.TSVector.Items) != 1 || ex.Keyword.TSVector.Items[0].ChunkID != "k1" {
		t.Fatalf("tsvector 轨迹应带 chunk_id: %+v", ex.Keyword.TSVector.Items)
	}
	// 分数分解必须自洽: 最终分 = RRF 名次分 + 三部分加成。
	if len(ex.Fusion) != 1 {
		t.Fatalf("融合明细应有 1 行, 实际 %d", len(ex.Fusion))
	}
	f := ex.Fusion[0]
	if got := round3(f.RRFScore + f.SimPart + f.KwPart + f.TitlePart); got != f.FinalScore {
		t.Fatalf("分数分解不自洽: rrf(%v)+sim(%v)+kw(%v)+title(%v) = %v, 但 final = %v",
			f.RRFScore, f.SimPart, f.KwPart, f.TitlePart, got, f.FinalScore)
	}
	if !f.KwHit || f.KwPart != 0.25 {
		t.Fatalf("关键词命中应计入 0.25 加成: %+v", f)
	}
	if f.SimPart != 0 {
		t.Fatalf("无向量命中时相似度分量必须为 0: %+v", f)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestSearchKnowledgeExplainedNoLogSkipsLogAndCache 测试台的诊断请求既不写留痕也不污染缓存。
//
// 留痕是「线上真实检索」的观测基线: 人工探测混进去会让零结果率、延迟分布全部失真。
// 这里给工具集接上**真实的 sqlmock 留痕存储** —— 若 NoLog 失效, 落库语句会立刻变成未预期的查询。
func TestSearchKnowledgeExplainedNoLogSkipsLogAndCache(t *testing.T) {
	ts, db, mock := newExplainTestToolSet(t)
	ts.SetSearchLogStore(knowledge.NewSearchLogStore(db))
	if ts.searchLogs == nil {
		t.Fatal("留痕存储必须已注入, 否则本测试失去意义")
	}
	ctx := context.Background()

	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("q", 15, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols))
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("%q%", 15, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols).
			AddRow("a1", "T", "", "content here", "k1", "article", 0, kbKeywordNoDistanceSentinel))
	mock.ExpectQuery("SELECT chunk_index, content FROM knowledge_chunks").
		WillReturnRows(sqlmock.NewRows([]string{"chunk_index", "content"}))

	if _, _, err := ts.searchKnowledgeExplained(ctx, "q", 5, 500, RetrieveOptions{Mode: "keyword", BypassCache: true, NoLog: true}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("NoLog 模式不得写留痕/缓存: %v", err)
	}
	key := ts.searchCacheKey("default", "q", "keyword", "anon", 5, 500, nil)
	if _, ok := ts.searchCache.get(key); ok {
		t.Fatal("BypassCache 模式下不得写入检索缓存")
	}
}

// TestAclFilteredCandidatesReturnsDifference ACL 过滤量 = 摘掉可见性后的候选数 − 实际候选数。
//
// 这是测试台最关键的单个数字: 它把「检索不到」在「向量模型不行」与「权限挡住了」之间分开。
func TestAclFilteredCandidatesReturnsDifference(t *testing.T) {
	ts, _, mock := newExplainTestToolSet(t)
	mock.ExpectQuery("SELECT count").
		WithArgs("%q%", 15, "default").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
	mock.ExpectQuery("SELECT count").
		WithArgs("%q%", 15, "default", "u1", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	got := ts.aclFilteredCandidates(context.Background(), "q", "default",
		VisibilityScope{TenantID: "default", UserID: "u1", GroupIDs: []string{"g1"}}, nil, 15)
	if got != 5 {
		t.Fatalf("ACL 过滤量 = %d, want 5", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestAclFilteredCandidatesNeverNegative 宽松身份不会算出负数过滤量。
func TestAclFilteredCandidatesNeverNegative(t *testing.T) {
	ts, _, mock := newExplainTestToolSet(t)
	mock.ExpectQuery("SELECT count").
		WithArgs("%q%", 15, "default").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT count").
		WithArgs("%q%", 15, "default", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

	if got := ts.aclFilteredCandidates(context.Background(), "q", "default",
		VisibilityScope{TenantID: "default", UserID: "u1"}, nil, 15); got != 0 {
		t.Fatalf("过滤量不得为负, got %d", got)
	}
}

// TestPreviewRetrievalGuards 前置条件缺失时必须给明确错误, 而不是静默返回空报告。
func TestPreviewRetrievalGuards(t *testing.T) {
	// 空 query
	ts := &MCPToolSet{}
	if _, err := ts.PreviewRetrieval(context.Background(), knowledge.RetrievalPreviewRequest{Query: "   "}); err == nil {
		t.Fatal("空 query 必须报错")
	}

	// 有 query 但无数据库
	if _, err := ts.PreviewRetrieval(context.Background(), knowledge.RetrievalPreviewRequest{Query: "磁盘满"}); err == nil {
		t.Fatal("无数据库必须报错(而不是返回空报告误导使用者)")
	}

	// 指定他人身份但未注入解析器 → 明确报错, 不允许退化成"本人身份"静默给出错误结论
	ts2, _, _ := newExplainTestToolSet(t)
	_, err := ts2.PreviewRetrieval(context.Background(), knowledge.RetrievalPreviewRequest{
		Query: "磁盘满", ScopeUserID: "u-other",
	})
	if err == nil || !strings.Contains(err.Error(), "scope resolver") {
		t.Fatalf("未注入 ScopeResolver 时应报错, got %v", err)
	}
}

// TestPreviewRetrievalUsesGivenIdentity 指定身份时按该用户解析可见范围, 且租户恒取自 ctx。
func TestPreviewRetrievalUsesGivenIdentity(t *testing.T) {
	ts, _, mock := newExplainTestToolSet(t)
	called := 0
	ts.SetScopeResolver(ScopeResolverFunc(func(_ context.Context, tenantID, userID string) VisibilityScope {
		called++
		return VisibilityScope{TenantID: tenantID, UserID: userID, GroupIDs: []string{"ops"}}
	}))
	ctx := context.WithValue(context.Background(), "tenant_id", "t-ctx")

	// 两轮检索: 第一轮按请求的 k=5(topN=15), 第二轮是 prompt 预览(limit=3 → topN=9)。
	// 每轮: tsvector(1 命中) + ILIKE(0 命中) + 首命中块的相邻块查询。
	for _, topN := range []int{15, 9} {
		mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
			WithArgs("q", topN, "t-ctx", "u-other", sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows(chunkHitCols).
				AddRow("a1", "T", "", "content here", "k1", "article", 0, kbKeywordNoDistanceSentinel))
		mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
			WithArgs("%q%", topN, "t-ctx", "u-other", sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows(chunkHitCols))
		mock.ExpectQuery("SELECT chunk_index, content FROM knowledge_chunks").
			WillReturnRows(sqlmock.NewRows([]string{"chunk_index", "content"}))
	}

	rep, err := ts.PreviewRetrieval(ctx, knowledge.RetrievalPreviewRequest{Query: "q", ScopeUserID: "u-other"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if called != 1 {
		t.Fatalf("指定身份只应解析一次可见范围, 实际 %d 次", called)
	}
	if !rep.Scoped {
		t.Fatal("指定了身份就不该是「仅公开」退化分支")
	}
	if rep.ScopeKey == "" || rep.ScopeKey == "anon" {
		t.Fatalf("scope_key 必须是真实身份哈希, got %q", rep.ScopeKey)
	}
	if rep.PromptPreview == "" || len(rep.PromptItems) == 0 {
		t.Fatal("prompt 预览必须非空(否则测试台无法回答'模型到底读到了什么')")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestPreviewWarningsSurfacesMisleadingCases 测试台的核心价值是防止误判:
// 「零结果 + 有 ACL 过滤」与「零结果 + 无 ACL 过滤」的结论完全不同, 必须显式写出来。
func TestPreviewWarningsSurfacesMisleadingCases(t *testing.T) {
	anon := previewWarnings(&knowledge.RetrievalPreviewReport{Scoped: false})
	if len(anon) == 0 || !strings.Contains(strings.Join(anon, " "), "未按身份过滤") {
		t.Fatalf("未按身份过滤必须告警: %v", anon)
	}

	aclZero := previewWarnings(&knowledge.RetrievalPreviewReport{Scoped: true, FilteredByACL: 3})
	joined := strings.Join(aclZero, " ")
	if !strings.Contains(joined, "被过滤") || !strings.Contains(joined, "可见范围") {
		t.Fatalf("零结果且存在 ACL 过滤时应引导先查权限: %v", aclZero)
	}

	plain := previewWarnings(&knowledge.RetrievalPreviewReport{Scoped: true})
	if !strings.Contains(strings.Join(plain, " "), "内容缺失") {
		t.Fatalf("零结果且无 ACL 过滤时应指向内容/分块问题: %v", plain)
	}

	degraded := previewWarnings(&knowledge.RetrievalPreviewReport{
		Scoped: true, Degraded: map[string]bool{"halfvec": true, "tsvector": true},
		Items: []map[string]any{{"id": "a1"}},
	})
	if !strings.Contains(strings.Join(degraded, " "), "halfvec") || !strings.Contains(strings.Join(degraded, " "), "tsvector") {
		t.Fatalf("降级必须显式提示(否则会把降级误判为模型不行): %v", degraded)
	}
}

// TestRagPromptFromItemsShape 注入片段的结构契约: 标题带文章 ID(支撑 [^id] 引用)、
// 每篇文章最多 2 个命中块、空输入返回空串。
func TestRagPromptFromItemsShape(t *testing.T) {
	if got := ragPromptFromItems(nil); got != "" {
		t.Fatalf("空输入必须返回空串, got %q", got)
	}
	items := []map[string]any{{
		"id": "art-1", "title": "磁盘满处理", "snippet": "fallback",
		"matches": []map[string]any{
			{"heading": "一、判断", "snippet": "s1"},
			{"heading": "二、清理", "snippet": "s2"},
			{"heading": "三、验证", "snippet": "s3"},
		},
	}}
	got := ragPromptFromItems(items)
	if !strings.Contains(got, "[art-1] 磁盘满处理 / 一、判断") {
		t.Fatalf("标题行必须带文章 ID 与块标题: %q", got)
	}
	if strings.Contains(got, "三、验证") {
		t.Fatalf("每篇文章最多注入 %d 个命中块: %q", kbRAGMaxMatchesPerArticle, got)
	}
	if !strings.HasPrefix(got, "\n\n--- 知识库相关文档 ---\n") {
		t.Fatalf("片段前缀是对外契约: %q", got)
	}
}
