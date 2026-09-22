package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestSearchLogStoreNilSafe 验证内存模式(无 DB)下留痕存储为 nil 且所有方法可安全调用。
// 内存存储用于测试与降级部署, 留痕属可选能力, 不应因此 panic。
func TestSearchLogStoreNilSafe(t *testing.T) {
	if s := NewSearchLogStore(nil); s != nil {
		t.Fatalf("nil db must yield nil store")
	}
	svc := NewService(nil)
	if svc.SearchLogs() != nil {
		t.Fatalf("memory-mode service must expose nil search log store")
	}
	var s *SearchLogStore
	ctx := context.Background()
	if err := s.Record(ctx, &SearchLog{Query: "q", TenantID: "t"}); err != nil {
		t.Fatalf("nil store Record must be no-op, got %v", err)
	}
	items, total, err := s.List(ctx, "t", SearchLogFilter{})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("nil store List must return empty, got %v/%d/%v", items, total, err)
	}
	stats, err := s.Stats(ctx, "t", 7)
	if err != nil {
		t.Fatalf("nil store Stats must not error: %v", err)
	}
	if stats.Total != 0 || stats.Days != 7 || stats.ZeroQueries == nil || stats.TopQueries == nil || stats.Series == nil {
		t.Fatalf("nil store Stats must return zeroed non-nil aggregates, got %+v", stats)
	}
	if n, err := s.Prune(ctx, time.Now()); err != nil || n != 0 {
		t.Fatalf("nil store Prune must be no-op, got %d/%v", n, err)
	}
}

// TestSearchLogRecordInsertsTrace 验证一次检索留痕被完整落库(含超长查询截断)。
func TestSearchLogRecordInsertsTrace(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	long := strings.Repeat("啊", searchLogQueryMaxRunes+50)
	want := strings.Repeat("啊", searchLogQueryMaxRunes)
	mock.ExpectExec("INSERT INTO knowledge_search_logs").
		WithArgs(sqlmock.AnyArg(), "default", want, "rrf", "故障,数据库",
			3, 5, 2, false, 12.5, 0.91, "agent-1", "user-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	s := NewSearchLogStore(db)
	if s == nil {
		t.Fatal("store must not be nil with a DB")
	}
	rec := &SearchLog{
		TenantID: "default", Query: long, Mode: "rrf",
		Categories: []string{"故障", "数据库"},
		VecHits:    3, KwHits: 5, HitCount: 2, LatencyMS: 12.5, TopScore: 0.91,
		AgentID: "agent-1", UserID: "user-1",
	}
	if err := s.Record(context.Background(), rec); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("Record must assign an id")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestSearchLogRecordSkipsBlankQuery 验证空查询不落库(避免无意义的表膨胀)。
func TestSearchLogRecordSkipsBlankQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	s := NewSearchLogStore(db)
	if err := s.Record(context.Background(), &SearchLog{TenantID: "default", Query: "   "}); err != nil {
		t.Fatalf("Record with blank query must be no-op: %v", err)
	}
	// 无任何期望: 一旦执行了 INSERT, ExpectationsWereMet 之前的严格模式会报错。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("blank query must not hit the DB: %v", err)
	}
}

// TestSearchLogListZeroOnly 验证「只看零结果查询」的过滤与总数统计。
func TestSearchLogListZeroOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT count\(\*\) FROM knowledge_search_logs`).
		WithArgs("default").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, tenant_id, query, mode, categories`).
		WithArgs("default", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "query", "mode", "categories",
			"vec_hits", "kw_hits", "hit_count", "zero_result", "latency_ms", "top_score",
			"agent_id", "user_id", "created_at"}).
			AddRow("l1", "default", "如何扩容 C 盘", "rrf", "主机", 0, 0, 0, true, 8.5, 0, "a1", "u1", now))

	s := NewSearchLogStore(db)
	items, total, err := s.List(context.Background(), "default", SearchLogFilter{ZeroOnly: true, Limit: 20})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 log, got total=%d items=%d", total, len(items))
	}
	if !items[0].ZeroResult || items[0].Query != "如何扩容 C 盘" || items[0].UserID != "u1" {
		t.Fatalf("unexpected log: %+v", items[0])
	}
	if len(items[0].Categories) != 1 || items[0].Categories[0] != "主机" {
		t.Fatalf("categories must be split from csv: %+v", items[0].Categories)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestSearchLogListClampsLimit 验证 limit 越界时收敛到默认值(避免一次拉全表)。
func TestSearchLogListClampsLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT count\(\*\) FROM knowledge_search_logs`).
		WithArgs("default").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT id, tenant_id, query, mode, categories`).
		WithArgs("default", 50, 0). // 1000 → 默认 50
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "query", "mode", "categories",
			"vec_hits", "kw_hits", "hit_count", "zero_result", "latency_ms", "top_score",
			"agent_id", "user_id", "created_at"}))

	s := NewSearchLogStore(db)
	if _, _, err := s.List(context.Background(), "default", SearchLogFilter{Limit: 1000}); err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestSearchLogStatsAggregates 验证检索概览的聚合口径(总数/零结果率/各路贡献/TOP 查询/趋势)。
func TestSearchLogStatsAggregates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT count\(\*\), count\(\*\) FILTER \(WHERE zero_result\)`).
		WillReturnRows(sqlmock.NewRows([]string{"total", "zero", "avg_hits", "avg_latency", "p95"}).
			AddRow(10, 3, 2.4, 130.5, 420.0))
	mock.ExpectQuery(`SELECT count\(\*\) FILTER \(WHERE vec_hits > 0\)`).
		WillReturnRows(sqlmock.NewRows([]string{"vector", "keyword", "fallback"}).AddRow(6, 8, 1))
	mock.ExpectQuery(`SELECT mode, count\(\*\) FROM knowledge_search_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"mode", "count"}).
			AddRow("rrf", 8).AddRow("fallback", 1).AddRow("cache", 1))
	mock.ExpectQuery(`AND zero_result = false`).
		WillReturnRows(sqlmock.NewRows([]string{"query", "c", "avg_hits", "last_at"}).
			AddRow("磁盘满怎么处理", 4, 3.0, now))
	mock.ExpectQuery(`AND zero_result = true`).
		WillReturnRows(sqlmock.NewRows([]string{"query", "c", "avg_hits", "last_at"}).
			AddRow("如何扩容 C 盘", 2, 0.0, now))
	mock.ExpectQuery(`to_char\(date_trunc\('day'`).
		WillReturnRows(sqlmock.NewRows([]string{"d", "total", "zero", "avg_latency"}).
			AddRow("2026-09-18", 6, 2, 120.0).AddRow("2026-09-19", 4, 1, 145.0))

	s := NewSearchLogStore(db)
	stats, err := s.Stats(context.Background(), "default", 7)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.Total != 10 || stats.ZeroResults != 3 {
		t.Fatalf("totals wrong: %+v", stats)
	}
	if stats.ZeroRate < 0.29 || stats.ZeroRate > 0.31 {
		t.Fatalf("zero rate = %v, want 0.3", stats.ZeroRate)
	}
	if stats.AvgHitCount != 2.4 || stats.AvgLatencyMS != 130.5 || stats.P95LatencyMS != 420 {
		t.Fatalf("aggregates wrong: %+v", stats)
	}
	if stats.VectorRoutes != 6 || stats.KeywordRoutes != 8 || stats.FallbackRoutes != 1 {
		t.Fatalf("route contribution wrong: %+v", stats)
	}
	if stats.ModeStats["rrf"] != 8 || stats.ModeStats["cache"] != 1 {
		t.Fatalf("mode stats wrong: %+v", stats.ModeStats)
	}
	if len(stats.TopQueries) != 1 || stats.TopQueries[0].Query != "磁盘满怎么处理" {
		t.Fatalf("top queries wrong: %+v", stats.TopQueries)
	}
	if len(stats.ZeroQueries) != 1 || stats.ZeroQueries[0].Query != "如何扩容 C 盘" {
		t.Fatalf("zero queries wrong: %+v", stats.ZeroQueries)
	}
	if len(stats.Series) != 2 || stats.Series[1].Zero != 1 {
		t.Fatalf("series wrong: %+v", stats.Series)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestSearchLogStatsClampsDays 验证 days 参数上下界(<=0 取 7, >90 取 90)。
func TestSearchLogStatsClampsDays(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	for _, days := range []int{0, 365} {
		mock.ExpectQuery(`SELECT count\(\*\), count\(\*\) FILTER \(WHERE zero_result\)`).
			WillReturnRows(sqlmock.NewRows([]string{"total", "zero", "avg_hits", "avg_latency", "p95"}).
				AddRow(0, 0, 0, 0, 0))
		mock.ExpectQuery(`SELECT count\(\*\) FILTER \(WHERE vec_hits > 0\)`).
			WillReturnRows(sqlmock.NewRows([]string{"vector", "keyword", "fallback"}).AddRow(0, 0, 0))
		mock.ExpectQuery(`SELECT mode, count\(\*\) FROM knowledge_search_logs`).
			WillReturnRows(sqlmock.NewRows([]string{"mode", "count"}))
		mock.ExpectQuery(`AND zero_result = false`).
			WillReturnRows(sqlmock.NewRows([]string{"query", "c", "avg_hits", "last_at"}))
		mock.ExpectQuery(`AND zero_result = true`).
			WillReturnRows(sqlmock.NewRows([]string{"query", "c", "avg_hits", "last_at"}))
		mock.ExpectQuery(`to_char\(date_trunc\('day'`).
			WillReturnRows(sqlmock.NewRows([]string{"d", "total", "zero", "avg_latency"}))

		s := NewSearchLogStore(db)
		stats, err := s.Stats(context.Background(), "default", days)
		if err != nil {
			t.Fatalf("Stats(%d) failed: %v", days, err)
		}
		want := days
		if days <= 0 {
			want = 7
		}
		if days > 90 {
			want = 90
		}
		if stats.Days != want {
			t.Fatalf("days = %d, want %d", stats.Days, want)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestSearchLogPrune 验证过期留痕清理。
func TestSearchLogPrune(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM knowledge_search_logs WHERE created_at < \$1`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 7))

	s := NewSearchLogStore(db)
	n, err := s.Prune(context.Background(), time.Now().Add(-SearchLogRetention))
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if n != 7 {
		t.Fatalf("pruned = %d, want 7", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestSearchLogHandlersMemoryMode 验证内存模式(未接 DB)下两个观测端点返回空结构而非报错。
func TestSearchLogHandlersMemoryMode(t *testing.T) {
	svc := NewService(nil)

	rec := httptest.NewRecorder()
	svc.handleSearchLogs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/search-logs?zero_only=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("search-logs status = %d, want 200", rec.Code)
	}
	var list map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode search-logs response: %v", err)
	}
	if items, ok := list["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("memory mode must return empty items, got %v", list["items"])
	}

	rec = httptest.NewRecorder()
	svc.handleSearchStats(rec, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/search-stats?days=30", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("search-stats status = %d, want 200", rec.Code)
	}
	var stats SearchStats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode search-stats response: %v", err)
	}
	if stats.Days != 30 || stats.Total != 0 {
		t.Fatalf("unexpected stats in memory mode: %+v", stats)
	}
}
