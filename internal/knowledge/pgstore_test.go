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

// TestEmbedFailureEnqueuesRetry 验证 embedder 出错时:文章照常落库(NULL embedding),
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

	// Create 仅执行一次 INSERT(embedding 为 NULL)。
	mock.ExpectExec("INSERT INTO knowledge_articles").
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

// TestRunReindexPassReindexesNullRows 验证一轮自愈:扫描 NULL 行→拉取文章→补算 embedding→写回→刷新指标。
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

	// 1) enqueueUnindexed: 扫描 NULL 行
	mock.ExpectQuery("SELECT id, tenant_id FROM knowledge_articles WHERE embedding IS NULL").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).AddRow("a1", "default"))
	// 2) drainPending: 拉取文章
	mock.ExpectQuery("SELECT id,tenant_id,title,content,category,tags,created_at,updated_at FROM knowledge_articles WHERE id=\\$1 AND tenant_id=\\$2").
		WithArgs("a1", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "content", "category", "tags", "created_at", "updated_at"}).
			AddRow("a1", "default", "Title", "Content", "cat", []byte("[]"), now, now))
	// 3) 写回 embedding(ExecContext 实参顺序为 id, embedding, tenant_id)
	mock.ExpectExec("UPDATE knowledge_articles SET embedding").
		WithArgs("a1", sqlmock.AnyArg(), "default").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 4) 刷新指标: 此时已无 NULL 行
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_articles WHERE embedding IS NULL").
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
	// 第一次 drain: 拉取文章(触发一次 getArticle),embed 失败→退避重新入队。
	mock.ExpectQuery("SELECT id,tenant_id,title,content,category,tags,created_at,updated_at FROM knowledge_articles WHERE id=\\$1 AND tenant_id=\\$2").
		WithArgs("a1", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "content", "category", "tags", "created_at", "updated_at"}).
			AddRow("a1", "default", "Title", "Content", "cat", []byte("[]"), now, now))

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

// TestUnindexedMetricRefreshed 验证未索引文章数指标被正确刷新。
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

	mock.ExpectQuery("SELECT id, tenant_id FROM knowledge_articles WHERE embedding IS NULL").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).AddRow("a1", "default"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM knowledge_articles WHERE embedding IS NULL").
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
