package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRankOf 验证首个期望结果排名(1 起; 未命中为 0)。
func TestRankOf(t *testing.T) {
	cases := []struct {
		name      string
		retrieved []string
		expected  []string
		want      int
	}{
		{"first", []string{"a", "b"}, []string{"a"}, 1},
		{"third", []string{"a", "b", "c"}, []string{"c"}, 3},
		{"miss", []string{"a", "b"}, []string{"z"}, 0},
		{"no-expectation", []string{"a"}, nil, 0},
		{"multi-expectation-takes-earliest", []string{"a", "b", "c"}, []string{"c", "b"}, 2},
		{"empty-retrieval", nil, []string{"a"}, 0},
	}
	for _, tc := range cases {
		if got := RankOf(tc.retrieved, tc.expected); got != tc.want {
			t.Fatalf("%s: RankOf = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestRecallAt 验证 top-n 覆盖率(截断 + 多期望结果部分命中)。
func TestRecallAt(t *testing.T) {
	if got := RecallAt([]string{"a", "b"}, []string{"b"}, 1); got != 0 {
		t.Fatalf("recall@1 must respect truncation, got %v", got)
	}
	if got := RecallAt([]string{"a", "b"}, []string{"b"}, 2); got != 1 {
		t.Fatalf("recall@2 = %v, want 1", got)
	}
	if got := RecallAt([]string{"a", "b", "c"}, []string{"a", "c", "z"}, 0); got != 2.0/3.0 {
		t.Fatalf("recall@K (no truncation) = %v, want 2/3", got)
	}
	if got := RecallAt(nil, []string{"a"}, 5); got != 0 {
		t.Fatalf("empty retrieval recall = %v, want 0", got)
	}
}

// TestSummarizeRAGEval 验证报告口径: 命中率/各级召回/MRR/零结果率/平均延迟 + 失败优先排序。
func TestSummarizeRAGEval(t *testing.T) {
	results := []RAGEvalCaseResult{
		// 命中在 rank 1
		{Query: "q1", Expected: []string{"a"}, Retrieved: []string{"a", "b"}, Rank: 1, Hit: true, Recall: 1, LatencyMS: 10},
		// 命中在 rank 3
		{Query: "q2", Expected: []string{"c"}, Retrieved: []string{"x", "y", "c"}, Rank: 3, Hit: true, Recall: 1, LatencyMS: 30},
		// 零结果
		{Query: "q3", Expected: []string{"d"}, Retrieved: nil, Rank: 0, Hit: false, Recall: 0, LatencyMS: 20, ZeroResult: true},
	}
	rep := SummarizeRAGEval(results, 5)
	if rep.Cases != 3 || rep.Hits != 2 {
		t.Fatalf("unexpected totals: %+v", rep)
	}
	if rep.HitRate < 0.66 || rep.HitRate > 0.67 {
		t.Fatalf("hit rate = %v, want 2/3", rep.HitRate)
	}
	if rep.RecallAt1 < 0.33 || rep.RecallAt1 > 0.34 {
		t.Fatalf("recall@1 = %v, want 1/3", rep.RecallAt1)
	}
	if rep.RecallAt3 < 0.66 || rep.RecallAt3 > 0.67 {
		t.Fatalf("recall@3 = %v, want 2/3", rep.RecallAt3)
	}
	if rep.RecallAtK < 0.66 || rep.RecallAtK > 0.67 {
		t.Fatalf("recall@k = %v, want 2/3", rep.RecallAtK)
	}
	wantMRR := (1.0 + 1.0/3.0) / 3.0
	if rep.MRR < wantMRR-0.001 || rep.MRR > wantMRR+0.001 {
		t.Fatalf("MRR = %v, want %v", rep.MRR, wantMRR)
	}
	if rep.ZeroRate < 0.33 || rep.ZeroRate > 0.34 {
		t.Fatalf("zero rate = %v, want 1/3", rep.ZeroRate)
	}
	if rep.AvgLatencyMS != 20 {
		t.Fatalf("avg latency = %v, want 20", rep.AvgLatencyMS)
	}
	// 失败优先: 未命中的 q3 必须排在第一屏。
	if rep.Details[0].Query != "q3" || rep.Details[0].Hit {
		t.Fatalf("failures must be listed first, got %+v", rep.Details[0])
	}
	// 同组内 rank 越大越靠前 => q2(rank3) 在 q1(rank1) 之前。
	if rep.Details[1].Query != "q2" {
		t.Fatalf("worse rank must come first, got %+v", rep.Details[1])
	}
}

// TestSummarizeRAGEvalEmpty 验证空用例集返回零值报告且 Details 非 nil(避免前端 undefined)。
func TestSummarizeRAGEvalEmpty(t *testing.T) {
	rep := SummarizeRAGEval(nil, 5)
	if rep.Cases != 0 || rep.HitRate != 0 || rep.Details == nil {
		t.Fatalf("empty report must be zeroed with non-nil details, got %+v", rep)
	}
}

// TestNormalizeRAGEvalOptions 验证参数边界(k ≤ 20, sample ≤ 100, 查询去空白)。
func TestNormalizeRAGEvalOptions(t *testing.T) {
	got := NormalizeRAGEvalOptions(RAGEvalOptions{K: 0, Sample: 0})
	if got.K != 5 || got.Sample != 20 {
		t.Fatalf("defaults wrong: %+v", got)
	}
	got = NormalizeRAGEvalOptions(RAGEvalOptions{K: 999, Sample: 999, Cases: []RAGEvalCase{{Query: "  q1  "}}})
	if got.K != 20 || got.Sample != 100 {
		t.Fatalf("clamp wrong: %+v", got)
	}
	if got.Cases[0].Query != "q1" {
		t.Fatalf("query must be trimmed, got %q", got.Cases[0].Query)
	}
}

// stubRAGEvaluator 是 RAGEvaluator 的测试桩。
type stubRAGEvaluator struct {
	called bool
	opts   RAGEvalOptions
	err    error
}

func (s *stubRAGEvaluator) EvaluateRAG(_ context.Context, opts RAGEvalOptions) (*RAGEvalReport, error) {
	s.called, s.opts = true, opts
	if s.err != nil {
		return nil, s.err
	}
	return SummarizeRAGEval([]RAGEvalCaseResult{
		{Query: "q1", Expected: []string{"a"}, Retrieved: []string{"a"}, Rank: 1, Hit: true, Recall: 1},
	}, opts.K), nil
}

func (s *stubRAGEvaluator) SampleEvalCases(_ context.Context, _ int, _ []string) ([]RAGEvalCase, error) {
	return nil, nil
}

// TestHandleRAGEvalNotImplemented 验证未接 AI 引擎时评估端点给出明确的 501(而不是 500)。
func TestHandleRAGEvalNotImplemented(t *testing.T) {
	svc := NewService(nil)
	if svc.RAGEvalEnabled() {
		t.Fatal("evaluator must be disabled by default")
	}
	rec := httptest.NewRecorder()
	svc.handleRAGEval(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/rag-eval", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// TestHandleRAGEvalRuns 验证评估入口把参数与 Agent 知识范围透传给评估器并回传报告。
func TestHandleRAGEvalRuns(t *testing.T) {
	svc := NewService(nil)
	stub := &stubRAGEvaluator{}
	svc.SetRAGEvaluator(stub)
	svc.SetKnowledgeCategoryResolver(func(agentID string) []string {
		if agentID == "agent-1" {
			return []string{"故障处理"}
		}
		return nil
	})

	body := `{"k":3,"sample":10,"agent_id":"agent-1","cases":[{"query":"磁盘满","expected_ids":["a1"]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/rag-eval", strings.NewReader(body))
	rec := httptest.NewRecorder()
	svc.handleRAGEval(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var report RAGEvalReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Cases != 1 || report.K != 3 || report.HitRate != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !stub.called || stub.opts.K != 3 || len(stub.opts.Cases) != 1 {
		t.Fatalf("options not forwarded: %+v", stub.opts)
	}
	if len(stub.opts.Categories) != 1 || stub.opts.Categories[0] != "故障处理" {
		t.Fatalf("agent knowledge scope must be forwarded, got %+v", stub.opts.Categories)
	}
}

// TestHandleRAGEvalEmptyBody 验证空 body 也能按默认参数跑一次冒烟评估。
func TestHandleRAGEvalEmptyBody(t *testing.T) {
	svc := NewService(nil)
	stub := &stubRAGEvaluator{}
	svc.SetRAGEvaluator(stub)
	rec := httptest.NewRecorder()
	svc.handleRAGEval(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/rag-eval", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if stub.opts.K != 5 || stub.opts.Sample != 20 {
		t.Fatalf("defaults must apply when body is absent: %+v", stub.opts)
	}
}

// TestHandleRAGEvalError 验证评估器报错时返回 500 且带上原因(便于定位"没有可抽样文章"等前置条件)。
func TestHandleRAGEvalError(t *testing.T) {
	svc := NewService(nil)
	svc.SetRAGEvaluator(&stubRAGEvaluator{err: errors.New("no evaluation cases")})
	rec := httptest.NewRecorder()
	svc.handleRAGEval(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/rag-eval", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no evaluation cases") {
		t.Fatalf("error reason must be surfaced, got %s", rec.Body.String())
	}
}
