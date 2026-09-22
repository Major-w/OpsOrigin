package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opsorigin/core/internal/monitor/metrics"
)

// stubEmbedder 是 Embedder 的测试桩。
type stubEmbedder struct {
	fn func(ctx context.Context, text string) ([]float32, error)
}

func (e *stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return e.fn(ctx, text)
}

// gatherGauge 从共享 Registry 读取指定 gauge 的当前值(避免引入 prometheus testutil 依赖)。
func gatherGauge(t *testing.T, name string) float64 {
	t.Helper()
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			for _, m := range mf.GetMetric() {
				if g := m.GetGauge(); g != nil {
					return g.GetValue()
				}
			}
		}
	}
	return -1
}

// TestChunkMarkdown 验证 Markdown 标题感知切块: 按标题分段、heading 记录标题路径。
func TestChunkMarkdown(t *testing.T) {
	content := "# 故障处理指南\n\n常见故障分类。\n\n## 磁盘满\n\n清理方法。\n\n### 大文件定位\n\n用 du 命令。\n\n## CPU 高\n\n排查步骤。"
	chunks, truncated := chunkMarkdown(content)
	if truncated {
		t.Fatalf("short article must not be truncated")
	}
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks (3 headings + trailing), got %d: %+v", len(chunks), chunks)
	}
	// 块 0: 标题为 "# 故障处理指南", 内容含"常见故障分类"
	if chunks[0].Heading != "故障处理指南" {
		t.Fatalf("chunk0 heading = %q, want 故障处理指南", chunks[0].Heading)
	}
	// 块 1: 二级标题路径
	if chunks[1].Heading != "故障处理指南 / 磁盘满" {
		t.Fatalf("chunk1 heading = %q, want 故障处理指南 / 磁盘满", chunks[1].Heading)
	}
	if chunks[2].Heading != "故障处理指南 / 磁盘满 / 大文件定位" {
		t.Fatalf("chunk2 heading = %q", chunks[2].Heading)
	}
	if chunks[3].Heading != "故障处理指南 / CPU 高" {
		t.Fatalf("chunk3 heading = %q", chunks[3].Heading)
	}
}

// TestChunkMarkdownLongContent 验证超长无标题正文按窗口二次切分(带重叠)。
func TestChunkMarkdownLongContent(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "这是一个用于填充的句子内容，为了构造足够长的段落以触发二次切分逻辑。"
	}
	chunks, _ := chunkMarkdown(long)
	if len(chunks) < 2 {
		t.Fatalf("expected long content split into >=2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if tokenEstimate(c.Content) > chunkMaxTokens*2 {
			t.Fatalf("chunk too long: %d tokens", tokenEstimate(c.Content))
		}
		// rune 完整性: 按 byte 切会切出半个汉字, 这里必须能原样 round-trip
		if r := []rune(c.Content); len(r) > 0 && string(r) != c.Content {
			t.Fatalf("chunk content not rune-safe")
		}
	}
}

// TestChunkMarkdownCodeBlock 验证代码块内部不按标题切分(整体保留)。
func TestChunkMarkdownCodeBlock(t *testing.T) {
	content := "# 配置示例\n\n```yaml\n# 这不是标题\n# 也不是\nname: test\n```\n\n结束。"
	chunks, _ := chunkMarkdown(content)
	// 代码块内 "# 这不是标题" 不应触发切分 → 整篇只有 1 块
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk (code block preserved, no split inside), got %d", len(chunks))
	}
	if !contains(chunks[0].Content, "name: test") {
		t.Fatalf("code block should be preserved intact, got: %q", chunks[0].Content)
	}
	if chunks[0].Heading != "配置示例" {
		t.Fatalf("heading = %q, want 配置示例", chunks[0].Heading)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestSyncChunksEmptyContentSkipsEmbedding 验证空/纯空白正文同步时: 只删旧块、不插块、
// 不调用 embedding。
//
// 回归背景: chunkMarkdown 曾为空内容补一个空块, 于是每篇空文章都会白花一次 embedding 调用,
// 并落下一个占据 top-k 名额的空块。这里用 embedding 调用计数做硬断言 —— 修复前计数为 1。
func TestSyncChunksEmptyContentSkipsEmbedding(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	embedCalls := 0
	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		embedCalls++
		return []float32{0.1, 0.2}, nil
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}

	// 只应发生删除旧块; 空的 INSERT 期望若被消费即为回归
	mock.ExpectExec("DELETE FROM knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(0, 1))

	missing, err := s.syncChunks(context.Background(), &Article{
		ID: "a1", TenantID: "default", Content: "  \n\n\t\n  ",
	})
	if err != nil {
		t.Fatalf("syncChunks failed: %v", err)
	}
	if missing {
		t.Fatalf("missing must be false: no chunk means nothing to embed")
	}
	if embedCalls != 0 {
		t.Fatalf("empty article must not call embedder, got %d call(s)", embedCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestInsertChunksSkipsBlankContent 验证 insertChunks 的防御性兜底: 传入含空内容的块时,
// 空块被跳过(不 embedding、不入库), 有效块照常写入。
func TestInsertChunksSkipsBlankContent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	embedCalls := 0
	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		embedCalls++
		return []float32{0.3}, nil
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}

	// 恰好 1 次 INSERT(有效块), 空块既不入库也不 embedding
	mock.ExpectExec("INSERT INTO knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	missing := s.insertChunks(context.Background(), "default", "a1", 0, "article", "", []Chunk{
		{Index: 0, Content: "   "},
		{Index: 1, Content: "磁盘扩容的实际步骤"},
	}, nil)
	if missing {
		t.Fatalf("missing must be false: the only real chunk embedded fine")
	}
	if embedCalls != 1 {
		t.Fatalf("expected exactly 1 embedder call (blank chunk skipped), got %d", embedCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestEmbedFailureEnqueuesRetry 验证 embedder 出错时:文章照常落库(块 embedding NULL),
// 且文章被加入异步重试队列——不再"静默写 NULL 后永久脱离语义检索"。
func TestEmbedFailureEnqueuesRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		return nil, context.DeadlineExceeded // 模拟 embedding 服务超时
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}

	// Create: INSERT 文章 → DELETE 旧块 → INSERT 块(embedding NULL, 因 embed 失败)
	mock.ExpectExec("INSERT INTO knowledge_articles").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("DELETE FROM knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	a := &Article{ID: "a1", TenantID: "default", Title: "T", Content: "C"}
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if got := s.PendingReindexCount(); got != 1 {
		t.Fatalf("expected 1 pending reindex task after embed failure, got %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestRunReindexPassReindexesNullRows 验证一轮自愈:扫描无块文章→拉取文章→分块并补 embedding→刷新指标。
func TestRunReindexPassReindexesNullRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		return []float32{0.1, 0.2}, nil
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}
	now := time.Now()

	// 1) enqueueUnindexed: 扫描无块文章(LEFT JOIN)
	mock.ExpectQuery("SELECT DISTINCT a.id, a.tenant_id FROM knowledge_articles a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).AddRow("a1", "default"))
	// 2) drainPending: 拉取文章
	mock.ExpectQuery("SELECT id,tenant_id,title,content,category,tags,views,status,version,expires_at,view_permission,edit_permission,author_id,owner_group_ids,created_at,updated_at FROM knowledge_articles WHERE id=\\$1 AND tenant_id=\\$2").
		WithArgs("a1", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "content", "category", "tags", "views", "status", "version", "expires_at", "view_permission", "edit_permission", "author_id", "owner_group_ids", "created_at", "updated_at"}).
			AddRow("a1", "default", "Title", "Content", "cat", []byte("[]"), 0, "draft", 1, now, "", "", "", []byte("{}"), now, now))
	// 3) syncChunks: 先取内容哈希复用缓存(空) → 删旧块 → 插块(embedding [0.1,0.2])
	mock.ExpectQuery("SELECT content_hash, embedding::text, embed_dim").
		WillReturnRows(sqlmock.NewRows([]string{"content_hash", "embedding", "embed_dim"}))
	mock.ExpectExec("DELETE FROM knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// 4) 刷新指标: 此时已无未分块文章
	mock.ExpectQuery("SELECT COUNT\\(DISTINCT a.id\\) FROM knowledge_articles a").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	s.RunReindexPass(context.Background())

	if got := gatherGauge(t, "knowledge_unindexed_articles"); got != 0 {
		t.Fatalf("expected unindexed gauge = 0 after successful pass, got %v", got)
	}
	if got := s.PendingReindexCount(); got != 0 {
		t.Fatalf("expected empty pending queue after success, got %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestReindexBackoffAvoidsHammering 验证 embedder 持续失败时按退避重新入队,
// 而非每次循环都立即重试(避免对 embedding 服务持续施压)。
func TestReindexBackoffAvoidsHammering(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		return nil, context.DeadlineExceeded
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}
	now := time.Now()

	s.enqueueRetry("a1", "default")
	// 第一次 drain: 拉取文章 → syncChunks(删块+插块, embed 失败→missing=true) → 退避重新入队。
	mock.ExpectQuery("SELECT id,tenant_id,title,content,category,tags,views,status,version,expires_at,view_permission,edit_permission,author_id,owner_group_ids,created_at,updated_at FROM knowledge_articles WHERE id=\\$1 AND tenant_id=\\$2").
		WithArgs("a1", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "content", "category", "tags", "views", "status", "version", "expires_at", "view_permission", "edit_permission", "author_id", "owner_group_ids", "created_at", "updated_at"}).
			AddRow("a1", "default", "Title", "Content", "cat", []byte("[]"), 0, "draft", 1, now, "", "", "", []byte("{}"), now, now))
	mock.ExpectQuery("SELECT content_hash, embedding::text, embed_dim").
		WillReturnRows(sqlmock.NewRows([]string{"content_hash", "embedding", "embed_dim"}))
	mock.ExpectExec("DELETE FROM knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(1, 1))

	s.drainPending(context.Background())
	if got := s.PendingReindexCount(); got != 1 {
		t.Fatalf("expected 1 pending after failed drain (backoff re-enqueue), got %d", got)
	}
	// 立即第二次 drain: 任务未到退避时间,不应再触碰 DB,也不应重试。
	s.drainPending(context.Background())
	if got := s.PendingReindexCount(); got != 1 {
		t.Fatalf("expected still 1 pending (backoff prevents immediate retry), got %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestUnindexedMetricRefreshed 验证未分块文章数指标被正确刷新。
func TestUnindexedMetricRefreshed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		return []float32{0.1}, nil
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}

	mock.ExpectQuery("SELECT DISTINCT a.id, a.tenant_id FROM knowledge_articles a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).AddRow("a1", "default"))
	mock.ExpectQuery("SELECT COUNT\\(DISTINCT a.id\\) FROM knowledge_articles a").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	s.enqueueUnindexed(context.Background())
	s.refreshUnindexedMetric(context.Background())

	if got := gatherGauge(t, "knowledge_unindexed_articles"); got != 5 {
		t.Fatalf("expected unindexed gauge = 5, got %v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestUnindexedPredicateCoversHalfIndexed 回归 P1-3:
// 自愈扫描口径必须同时覆盖「完全没有块」与「有块但缺向量」两种情形。
// 修复前只判定 c.id IS NULL, embedding 部分失败(半索引)的文章既不入队重试、
// 也不计入未索引指标, 表现为"文章明明存在却检索不到, 且监控无异常"。
func TestUnindexedPredicateCoversHalfIndexed(t *testing.T) {
	if !strings.Contains(unindexedArticlePredicate, "c.id IS NULL") {
		t.Error("自愈口径必须包含「完全没有正文块」(c.id IS NULL)")
	}
	if !strings.Contains(unindexedArticlePredicate, "c.embedding IS NULL") {
		t.Error("自愈口径必须包含「有块但缺向量」(c.embedding IS NULL), 否则半索引文章不会被重试")
	}
	if !strings.Contains(unindexedArticlePredicate, "btrim(coalesce(a.content, ''))") {
		t.Error("自愈口径必须排除空正文文章, 否则永远切不出块、会被反复入队空转")
	}
	if !strings.Contains(unindexedArticleJoin, "attachment_id IS NULL") {
		t.Error("自愈口径必须只统计正文块(附件块由 syncAttachmentChunks 独立维护)")
	}
}

// TestReindexAllEnqueuesHalfIndexed 回归 P1-3:
// ReindexAll 单篇重算后若仍有块缺向量, 必须把文章放入异步重试队列, 而不是静默丢弃 missing。
func TestReindexAllEnqueuesHalfIndexed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		return nil, context.DeadlineExceeded
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}
	now := time.Now()

	mock.ExpectQuery("SELECT DISTINCT a.id, a.tenant_id, a.title").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "content", "category", "tags", "created_at", "updated_at"}).
			AddRow("a1", "default", "Title", "Content", "cat", []byte("[]"), now, now))
	mock.ExpectQuery("SELECT content_hash, embedding::text, embed_dim").
		WillReturnRows(sqlmock.NewRows([]string{"content_hash", "embedding", "embed_dim"}))
	mock.ExpectExec("DELETE FROM knowledge_chunks").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO knowledge_chunks").WillReturnResult(sqlmock.NewResult(1, 1))

	n, err := s.ReindexAll(context.Background(), false)
	if err != nil {
		t.Fatalf("ReindexAll failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed article, got %d", n)
	}
	if got := s.PendingReindexCount(); got != 1 {
		t.Fatalf("expected 1 pending retry task for half-indexed article, got %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestInsertChunksReusesUnchangedVectors 回归 P2-2:
// 编辑文章时, 内容哈希未变化的块必须直接复用旧向量, 只对新增/改动的块调用 embedding。
// 修复前 syncChunks 一律删光旧块全文重算, 改一个小节也要为整篇付费。
func TestInsertChunksReusesUnchangedVectors(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	embedCalls := 0
	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		embedCalls++
		return []float32{9, 9}, nil
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}

	unchanged := Chunk{Index: 0, Content: "磁盘扩容的实际步骤"}
	changed := Chunk{Index: 1, Content: "本次新增的小节内容"}
	cached := map[string]cachedVector{
		chunkContentHash(&unchanged): {literal: "[0.1,0.2]", dim: 2},
	}

	// 两个块都入库(复用块 + 新算块)
	mock.ExpectExec("INSERT INTO knowledge_chunks").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO knowledge_chunks").WillReturnResult(sqlmock.NewResult(1, 1))

	missing := s.insertChunks(context.Background(), "default", "a1", 0, "article", "", []Chunk{unchanged, changed}, cached)
	if missing {
		t.Fatal("missing must be false: both chunks have vectors (one reused, one computed)")
	}
	if embedCalls != 1 {
		t.Fatalf("expected exactly 1 embedder call (unchanged chunk reused), got %d", embedCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestInsertChunksHashReuseDisabledWhenNoCache 验证无复用缓存(如换 embedding 模型后的强制重建)时,
// 所有块都必须重新计算向量 —— 否则旧维度向量会被原样搬回。
func TestInsertChunksHashReuseDisabledWhenNoCache(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	embedCalls := 0
	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		embedCalls++
		return []float32{1, 2}, nil
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}

	chunk := Chunk{Index: 0, Content: "磁盘扩容的实际步骤"}
	mock.ExpectExec("INSERT INTO knowledge_chunks").WillReturnResult(sqlmock.NewResult(1, 1))
	_ = s.insertChunks(context.Background(), "default", "a1", 0, "article", "", []Chunk{chunk}, nil)
	if embedCalls != 1 {
		t.Fatalf("expected 1 embedder call when cache is nil, got %d", embedCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestListArticleChunks 验证分块可视化列表: 只取正文块(attachment_id IS NULL + source 为正文),
// 按 chunk_index 升序, 不混入附件抽取块。
func TestListArticleChunks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()
	s := &pgStore{db: db, vectorEnabled: true}

	rows := sqlmock.NewRows([]string{"id", "heading", "content"}).
		AddRow("k1", "故障处理 / 磁盘满", "清理步骤").
		AddRow("k2", "故障处理 / CPU 高", "排查步骤")
	// 过滤条件必须含正文块判定, 否则附件块会泄漏进可视化列表
	mock.ExpectQuery("SELECT id, heading, content FROM knowledge_chunks").
		WithArgs("default", "a1", 200).
		WillReturnRows(rows)

	list, err := s.ListArticleChunks(context.Background(), "default", "a1", 200)
	if err != nil {
		t.Fatalf("ListArticleChunks failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 article chunks, got %d", len(list))
	}
	if list[0].ID != "k1" || list[1].ID != "k2" {
		t.Fatalf("order must follow chunk_index (k1,k2), got %s,%s", list[0].ID, list[1].ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
