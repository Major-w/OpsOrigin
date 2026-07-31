package knowledge

import (
	"context"
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
	chunks := chunkMarkdown(content)
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
	chunks := chunkMarkdown(long)
	if len(chunks) < 2 {
		t.Fatalf("expected long content split into >=2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len(c.Content) > chunkMaxLen*3 {
			t.Fatalf("chunk too long: %d chars", len(c.Content))
		}
	}
}

// TestChunkMarkdownCodeBlock 验证代码块内部不按标题切分(整体保留)。
func TestChunkMarkdownCodeBlock(t *testing.T) {
	content := "# 配置示例\n\n```yaml\n# 这不是标题\n# 也不是\nname: test\n```\n\n结束。"
	chunks := chunkMarkdown(content)
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
	mock.ExpectQuery("SELECT a.id, a.tenant_id FROM knowledge_articles a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).AddRow("a1", "default"))
	// 2) drainPending: 拉取文章
	mock.ExpectQuery("SELECT id,tenant_id,title,content,category,tags,views,status,version,created_at,updated_at FROM knowledge_articles WHERE id=\\$1 AND tenant_id=\\$2").
		WithArgs("a1", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "content", "category", "tags", "views", "status", "version", "created_at", "updated_at"}).
			AddRow("a1", "default", "Title", "Content", "cat", []byte("[]"), 0, "draft", 1, now, now))
	// 3) syncChunks: 删旧块 + 插块(embedding [0.1,0.2])
	mock.ExpectExec("DELETE FROM knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// 4) 刷新指标: 此时已无未分块文章
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_articles a").
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
	mock.ExpectQuery("SELECT id,tenant_id,title,content,category,tags,views,status,version,created_at,updated_at FROM knowledge_articles WHERE id=\\$1 AND tenant_id=\\$2").
		WithArgs("a1", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "content", "category", "tags", "views", "status", "version", "created_at", "updated_at"}).
			AddRow("a1", "default", "Title", "Content", "cat", []byte("[]"), 0, "draft", 1, now, now))
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

	mock.ExpectQuery("SELECT a.id, a.tenant_id FROM knowledge_articles a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).AddRow("a1", "default"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_articles a").
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
