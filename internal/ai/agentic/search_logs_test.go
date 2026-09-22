package agentic

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opsorigin/core/internal/knowledge"
	"github.com/opsorigin/core/internal/monitor/metrics"
)

// gatherCounterValue 从共享 Registry 读取指定 counter 的当前值(可选按标签过滤)。
// 与 knowledge 包内的 gatherGauge 同思路, 避免引入 prometheus testutil 依赖。
// 未出现过的标签组合(prometheus 的 CounterVec 惰性导出)按 0 计。
func gatherCounterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			match := true
			for k, v := range labels {
				found := false
				for _, lp := range m.GetLabel() {
					if lp.GetName() == k && lp.GetValue() == v {
						found = true
						break
					}
				}
				if !found {
					match = false
					break
				}
			}
			if match {
				if c := m.GetCounter(); c != nil {
					return c.GetValue()
				}
			}
		}
	}
	return 0
}

// TestRecordSearchPersistsTrace 验证一次检索的观测快照被落库:
// 模式/各路命中数/召回条数/零结果标记/延迟/首条分数全部回填(P1-2/P1-4)。
func TestRecordSearchPersistsTrace(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO knowledge_search_logs").
		WithArgs(sqlmock.AnyArg(), "default", "磁盘满了怎么处理", "rrf", "主机",
			2, 3, 1, false, sqlmock.AnyArg(), 0.87, "", "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	ts := NewMCPToolSet(nil)
	ts.SetSearchLogStore(knowledge.NewSearchLogStore(db))
	if ts.searchLogs == nil {
		t.Fatal("search log store must be injected")
	}

	ts.recordSearch(context.Background(), searchTrace{
		mode: "rrf", query: "磁盘满了怎么处理", tenant: "default",
		categories: []string{"主机"}, vecHits: 2, kwHits: 3,
		resultCount: 1, topScore: 0.87,
	}, false)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestRecordSearchZeroResultMetric 验证零结果检索被单独计数(知识盲区可观测)。
func TestRecordSearchZeroResultMetric(t *testing.T) {
	before := gatherCounterValue(t, "ai_knowledge_search_zero_total", map[string]string{"mode": "fallback"})
	beforeTotal := gatherCounterValue(t, "ai_knowledge_search_total", map[string]string{"mode": "fallback"})

	ts := NewMCPToolSet(nil) // 无留痕存储: 指标仍必须统计
	ts.recordSearch(context.Background(), searchTrace{
		mode: "fallback", query: "如何扩容 C 盘", tenant: "default", resultCount: 0,
	}, false)

	if after := gatherCounterValue(t, "ai_knowledge_search_zero_total", map[string]string{"mode": "fallback"}); after != before+1 {
		t.Fatalf("zero-result counter = %v, want %v", after, before+1)
	}
	if after := gatherCounterValue(t, "ai_knowledge_search_total", map[string]string{"mode": "fallback"}); after != beforeTotal+1 {
		t.Fatalf("retrieval counter = %v, want %v", after, beforeTotal+1)
	}
}

// TestRecordSearchRouteContribution 验证「某条路有命中」才计入该路贡献, 空结果不计。
func TestRecordSearchRouteContribution(t *testing.T) {
	beforeVec := gatherCounterValue(t, "ai_knowledge_search_route_hits_total", map[string]string{"route": "vector"})
	beforeKw := gatherCounterValue(t, "ai_knowledge_search_route_hits_total", map[string]string{"route": "keyword"})

	ts := NewMCPToolSet(nil)
	// 向量路命中、关键词路全空: 只应计入 vector。
	ts.recordSearch(context.Background(), searchTrace{
		mode: "rrf", query: "q", tenant: "default", vecHits: 4, kwHits: 0, resultCount: 2, topScore: 0.5,
	}, false)

	if after := gatherCounterValue(t, "ai_knowledge_search_route_hits_total", map[string]string{"route": "vector"}); after != beforeVec+1 {
		t.Fatalf("vector route counter = %v, want %v", after, beforeVec+1)
	}
	if after := gatherCounterValue(t, "ai_knowledge_search_route_hits_total", map[string]string{"route": "keyword"}); after != beforeKw {
		t.Fatalf("keyword route counter must stay %v, got %v", beforeKw, after)
	}
}

// TestTopScoreOf 验证首条融合分提取(多类型断言与空结果兜底)。
func TestTopScoreOf(t *testing.T) {
	cases := []struct {
		name  string
		items []map[string]any
		want  float64
	}{
		{"empty", nil, 0},
		{"float64", []map[string]any{{"score": 0.75}}, 0.75},
		{"float32", []map[string]any{{"score": float32(0.5)}}, 0.5},
		{"int", []map[string]any{{"score": 1}}, 1},
		{"missing", []map[string]any{{"id": "a1"}}, 0},
	}
	for _, tc := range cases {
		if got := topScoreOf(tc.items); got != tc.want {
			t.Fatalf("%s: topScoreOf = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSearchKnowledgeRecordsLog 验证检索主路径在结束后确实写了留痕(缓存命中路径 mode=cache)。
func TestSearchKnowledgeRecordsLog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	ts := &MCPToolSet{db: db, embedCache: newTTLCache(embedCacheTTL, 8), searchCache: newTTLCache(searchCacheTTL, 8)}
	ts.SetSearchLogStore(knowledge.NewSearchLogStore(db))
	ctx := context.Background()

	// 关键词两路均空 → 走文章级兜底(mode=fallback), 兜底 SQL 返回 0 行 → 零结果留痕。
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("no such topic", 15, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols))
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("%no such topic%", 15, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols))
	mock.ExpectQuery("SELECT a.id, a.title").
		WithArgs("%no such topic%", 500, 5, "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "snippet"}))
	mock.ExpectExec("INSERT INTO knowledge_search_logs").
		WithArgs(sqlmock.AnyArg(), "default", "no such topic", "fallback", "",
			0, 0, 0, true, sqlmock.AnyArg(), float64(0), "", "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	items, err := ts.searchKnowledge(ctx, "no such topic", 5, 500, nil)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected zero result, got %v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("fallback search must leave a zero-result trace: %v", err)
	}
}
