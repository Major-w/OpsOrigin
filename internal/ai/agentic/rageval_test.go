package agentic

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/opsorigin/core/internal/knowledge"
)

// TestSampleEvalCases 验证自动抽样: 只取可召回(已发布/未过期/公开)且标题够长的文章,
// 同名文章只保留一条(避免"期望命中哪一篇"产生歧义)。
func TestSampleEvalCases(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT a.id, a.title FROM knowledge_articles a`).
		WithArgs("default", 5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).
			AddRow("a1", "磁盘满处理").
			AddRow("a2", "磁盘满处理").
			AddRow("a3", "CPU 高排查"))

	ts := NewMCPToolSet(db)
	cases, err := ts.SampleEvalCases(context.Background(), 5, nil)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("duplicate titles must be collapsed, got %d: %+v", len(cases), cases)
	}
	if cases[0].Query != "磁盘满处理" || cases[0].ExpectedIDs[0] != "a1" || cases[0].SourceID != "a1" {
		t.Fatalf("unexpected case: %+v", cases[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestSampleEvalCasesCategoryScope 验证传了分类时按分类过滤(与线上检索范围一致)。
func TestSampleEvalCasesCategoryScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`AND a.category = ANY\(\$3::text\[\]\)`).
		WithArgs("default", 5, pq.Array([]string{"故障处理"})).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title"}))

	ts := NewMCPToolSet(db)
	if _, err := ts.SampleEvalCases(context.Background(), 5, []string{"故障处理"}); err != nil {
		t.Fatalf("sample: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestEvaluateRAGWithCustomCases 验证指定用例的端到端评估: 走真实检索路径(块级双路 → 融合),
// 并正确算出命中/排名。
func TestEvaluateRAGWithCustomCases(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	ts := &MCPToolSet{db: db, embedCache: newTTLCache(embedCacheTTL, 8), searchCache: newTTLCache(searchCacheTTL, 8)}

	// 用例 1: 期望 a1 —— ILIKE 路命中 a1(排名 1), tsvector 路为空。
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("磁盘满处理", 6, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols))
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("%磁盘满处理%", 6, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols).
			AddRow("a1", "磁盘满处理", "故障处理", "full", "k1", "article", 0, 1.0))
	mock.ExpectQuery("SELECT chunk_index, content FROM knowledge_chunks").
		WillReturnRows(sqlmock.NewRows([]string{"chunk_index", "content"}))

	// 用例 2: 期望 a9, 但两路都空 → 走文章级兜底, 兜底返回的是 a1 → 未命中。
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("不存在的主题", 6, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols))
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("%不存在的主题%", 6, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols))
	mock.ExpectQuery("SELECT a.id, a.title").
		WithArgs("%不存在的主题%", kbChunkContextRunes, 2, "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "snippet"}).
			AddRow("a1", "磁盘满处理", "full"))

	report, err := ts.EvaluateRAG(context.Background(), knowledge.RAGEvalOptions{
		K: 2,
		Cases: []knowledge.RAGEvalCase{
			{Query: "磁盘满处理", ExpectedIDs: []string{"a1"}},
			{Query: "不存在的主题", ExpectedIDs: []string{"a9"}},
		},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if report.Source != "custom" || report.Cases != 2 || report.Hits != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.HitRate != 0.5 || report.MRR != 0.5 {
		t.Fatalf("hit rate/MRR wrong: %+v", report)
	}
	// 失望先报: 未命中的用例排在第一条。
	if report.Details[0].Query != "不存在的主题" || report.Details[0].Hit {
		t.Fatalf("failures must be first: %+v", report.Details[0])
	}
	if report.Details[1].Rank != 1 || report.Details[1].Retrieved[0] != "a1" {
		t.Fatalf("unexpected hit detail: %+v", report.Details[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestEvaluateRAGSamplesWhenNoCases 验证未指定用例时自动抽样(标题当查询)。
func TestEvaluateRAGSamplesWhenNoCases(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	ts := &MCPToolSet{db: db, embedCache: newTTLCache(embedCacheTTL, 8), searchCache: newTTLCache(searchCacheTTL, 8)}

	mock.ExpectQuery(`SELECT a.id, a.title FROM knowledge_articles a`).
		WithArgs("default", 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("a1", "磁盘满处理"))
	// 该用例的检索: 关键词两路各返回一条, 融合后命中 a1。
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("磁盘满处理", 15, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols))
	mock.ExpectQuery("SELECT c.article_id, a.title, c.heading").
		WithArgs("%磁盘满处理%", 15, "default").
		WillReturnRows(sqlmock.NewRows(chunkHitCols).
			AddRow("a1", "磁盘满处理", "故障处理", "full", "k1", "article", 0, 1.0))
	mock.ExpectQuery("SELECT chunk_index, content FROM knowledge_chunks").
		WillReturnRows(sqlmock.NewRows([]string{"chunk_index", "content"}))

	report, err := ts.EvaluateRAG(context.Background(), knowledge.RAGEvalOptions{Sample: 3})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if report.Source != "sampled" || report.Cases != 1 || report.HitRate != 1 {
		t.Fatalf("unexpected sampled report: %+v", report)
	}
	if report.K != 5 {
		t.Fatalf("K must default to 5, got %d", report.K)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestEvaluateRAGNoCorpus 验证空知识库时给出明确错误(而不是返回一份全是 0 的报告骗人)。
func TestEvaluateRAGNoCorpus(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT a.id, a.title FROM knowledge_articles a`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title"}))

	ts := &MCPToolSet{db: db, embedCache: newTTLCache(embedCacheTTL, 8), searchCache: newTTLCache(searchCacheTTL, 8)}
	if _, err := ts.EvaluateRAG(context.Background(), knowledge.RAGEvalOptions{Sample: 3}); err == nil {
		t.Fatal("empty corpus must surface an error")
	}
}
