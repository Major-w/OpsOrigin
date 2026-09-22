package agentic

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/opsorigin/core/internal/knowledge"
)

// —— RAG 效果评估(P1-1, 实现侧) ——
//
// 评估必须跑在**生产检索路径**上(同一个 searchKnowledge: 同一套可见性过滤、同一套多路召回与 rerank),
// 否则测出来的是"另一套检索", 对线上没有任何解释力。
//
// 语料两种来源:
//   - SampleEvalCases: 从已发布文章里抽样, 用标题当查询 —— 冷启动即可跑, 适合纳入回归监控;
//   - 调用方直接给 (查询, 期望文章 ID) 对 —— 真实问法的验收评估。
//
// 注意: 自动抽样用"标题当查询"天然偏简单(标题词与正文高度重合), 所以它的绝对分数应视为**上界基准**,
// 真正有意义的是它的**变化趋势**(改分块/换模型/加 rerank 之后是升还是降)。

// EvaluateRAG 运行一次评估并返回报告。
func (ts *MCPToolSet) EvaluateRAG(ctx context.Context, opts knowledge.RAGEvalOptions) (*knowledge.RAGEvalReport, error) {
	opts = knowledge.NormalizeRAGEvalOptions(opts)
	cases := opts.Cases
	source := "custom"
	if len(cases) == 0 {
		sampled, err := ts.SampleEvalCases(ctx, opts.Sample, opts.Categories)
		if err != nil {
			return nil, err
		}
		cases = sampled
		source = "sampled"
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no evaluation cases: knowledge base has no published article to sample from")
	}

	results := make([]knowledge.RAGEvalCaseResult, 0, len(cases))
	for _, c := range cases {
		if strings.TrimSpace(c.Query) == "" {
			continue
		}
		started := time.Now()
		items, err := ts.searchKnowledge(ctx, c.Query, opts.K, kbChunkContextRunes, opts.Categories)
		latency := float64(time.Since(started).Microseconds()) / 1000
		if err != nil {
			return nil, err
		}
		retrieved := make([]string, 0, len(items))
		for _, it := range items {
			if id, _ := it["id"].(string); id != "" {
				retrieved = append(retrieved, id)
			}
		}
		results = append(results, knowledge.RAGEvalCaseResult{
			Query:      c.Query,
			Expected:   c.ExpectedIDs,
			Retrieved:  retrieved,
			LatencyMS:  latency,
			ZeroResult: len(retrieved) == 0,
			Rank:       knowledge.RankOf(retrieved, c.ExpectedIDs),
			Recall:     knowledge.RecallAt(retrieved, c.ExpectedIDs, opts.K),
		})
	}
	for i := range results {
		results[i].Hit = results[i].Rank > 0
	}
	report := knowledge.SummarizeRAGEval(results, opts.K)
	report.Source = source
	return report, nil
}

// SampleEvalCases 从已发布文章抽样生成用例: 查询 = 标题, 期望 = 该文章自身。
// 同时排除标题过短(检索无区分度)与标题重复(期望值会有歧义)的文章。
func (ts *MCPToolSet) SampleEvalCases(ctx context.Context, sample int, categories []string) ([]knowledge.RAGEvalCase, error) {
	if ts.db == nil {
		return nil, nil
	}
	if sample <= 0 {
		sample = 20
	}
	if sample > 100 {
		sample = 100
	}
	// 与检索同口径的可见性过滤: 只拿"AI 真的能召回"的文章(含当前提问者可见的 team/owner 文章),
	// 否则会制造一批注定失败的用例。
	args := []any{tenantOf(ctx), sample}
	catClause := ""
	if len(categories) > 0 {
		// pq.Array: 显式按 PostgreSQL 数组传参(与 lib/pq 的隐式数组编码相比, 不依赖驱动的隐式转换)。
		args = append(args, pq.Array(categories))
		catClause = " AND a.category = ANY($3::text[])"
	}
	sa := newScopeArgs(ts.resolveScope(ctx), args...)
	rows, err := ts.db.QueryContext(ctx, `
		SELECT a.id, a.title FROM knowledge_articles a
		WHERE a.tenant_id=$1`+sa.Predicate()+catClause+
		` AND char_length(btrim(coalesce(a.title,''))) >= 4
		ORDER BY md5(a.id || $1::text) LIMIT $2`, sa.Args()...)
	if err != nil {
		slog.Warn("knowledge: sample eval cases failed", "err", err)
		return nil, err
	}
	defer rows.Close()
	out := make([]knowledge.RAGEvalCase, 0, sample)
	seen := map[string]bool{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.TrimSpace(title))
		if seen[key] {
			continue // 同名文章会让"期望命中哪一篇"产生歧义
		}
		seen[key] = true
		out = append(out, knowledge.RAGEvalCase{Query: strings.TrimSpace(title), ExpectedIDs: []string{id}, SourceID: id})
	}
	return out, rows.Err()
}

// rankOf / recallOf 只保留在 knowledge 包(RankOf / RecallAt): 评估口径集中一处,
// 避免"MCP 检索侧算一套、评估侧再算一套"的口径漂移。
