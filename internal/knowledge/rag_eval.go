package knowledge

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/opsorigin/core/pkg/httputil"
)

// —— RAG 效果评估(P1-1) ——
//
// 此前只有「检索可观测」这样的运行态统计(有多少次检索、多少次零结果),
// 它回答的是"用得怎么样", 回答不了"检索本身准不准":
//   - 明明库里有一篇《磁盘满处理》, 用户问"磁盘满了怎么办"能不能被召回到?
//   - 修改分块策略 / 换 embedding 模型 / 加 rerank 之后, 检索质量是变好还是变坏?
//
// 评估口径(行业通用, 无需人工标注即可自动化跑):
//   - HitRate   命中率: 期望文章出现在 top-k 里的查询占比;
//   - Recall@k  召回率: 期望文章集合被 top-k 覆盖的比例(多期望时不足 1);
//   - MRR       平均倒数排名: 期望文章首次出现位置的倒数均值(越接近 1 越靠前);
//   - ZeroRate  零结果率: 一条都没召回到的比例(通常意味着内容缺失或问法与文档差异过大)。
//
// 语料来源两种:
//  1. 自动: 从已发布文章里抽样, 用「标题」当查询, 期望命中该文章 —— 冷启动即可跑, 用于回归监控;
//  2. 指定: 调用方直接给 (查询, 期望文章 ID) 对, 用于真实场景的验收评估。

// RAGEvalCase 单条评估用例。
type RAGEvalCase struct {
	Query       string   `json:"query"`
	ExpectedIDs []string `json:"expected_ids"`
	// SourceID 自动生成的用例会标记来源文章, 便于回溯这条用例是怎么来的。
	SourceID string `json:"source_id,omitempty"`
}

// RAGEvalOptions 评估参数。
type RAGEvalOptions struct {
	K      int           // top-k, 默认 5
	Sample int           // 自动抽样条数, 默认 20, 上限 100
	Cases  []RAGEvalCase // 指定用例(非空时忽略 Sample)
	// Categories 限定知识范围(与 Agent 知识库绑定一致), 空 = 全库。
	Categories []string
}

// RAGEvalCaseResult 单条用例的评估明细。
type RAGEvalCaseResult struct {
	Query      string   `json:"query"`
	Expected   []string `json:"expected"`
	Retrieved  []string `json:"retrieved"`
	Rank       int      `json:"rank"` // 首个期望结果的排名(1 起), 0 = 未召回
	Hit        bool     `json:"hit"`
	Recall     float64  `json:"recall"`
	LatencyMS  float64  `json:"latency_ms"`
	ZeroResult bool     `json:"zero_result"`
}

// RAGEvalReport 评估报告。
type RAGEvalReport struct {
	K            int     `json:"k"`
	Cases        int     `json:"cases"`
	Hits         int     `json:"hits"`
	HitRate      float64 `json:"hit_rate"`
	RecallAt1    float64 `json:"recall_at_1"`
	RecallAt3    float64 `json:"recall_at_3"`
	RecallAtK    float64 `json:"recall_at_k"`
	MRR          float64 `json:"mrr"`
	ZeroRate     float64 `json:"zero_rate"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
	// Source 用例来源: sampled(按标题自动抽样) | custom(调用方指定)。
	Source  string              `json:"source"`
	Details []RAGEvalCaseResult `json:"details"`
}

// RAGEvaluator 由 AI 检索链路的实现方注入(agentic 工具集): 评估必须跑在真实检索路径上,
// 否则测的是"另一套检索", 对生产没有解释力。
// 用接口而非直接依赖, 是为了让 knowledge 模块不反向依赖 ai 引擎。
type RAGEvaluator interface {
	EvaluateRAG(ctx context.Context, opts RAGEvalOptions) (*RAGEvalReport, error)
	// SampleEvalCases 供评估入口按语料自动生成用例(标题当查询)。
	SampleEvalCases(ctx context.Context, sample int, categories []string) ([]RAGEvalCase, error)
}

// SetRAGEvaluator 注入 RAG 评估器(nil = 未启用, 评估端点返回 501)。
func (s *Service) SetRAGEvaluator(e RAGEvaluator) { s.ragEval = e }

// RAGEvalEnabled 是否已接入评估器(供前端决定是否展示评估入口)。
func (s *Service) RAGEvalEnabled() bool { return s.ragEval != nil }

// handleRAGEval 运行一次 RAG 效果评估。
// POST /api/v1/knowledge/rag-eval  body: {"k":5,"sample":20,"cases":[{"query":"...","expected_ids":["..."]}]}
func (s *Service) handleRAGEval(w http.ResponseWriter, r *http.Request) {
	if s.ragEval == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "RAG evaluation requires the AI retrieval engine (vector search enabled)")
		return
	}
	var req struct {
		K       int           `json:"k"`
		Sample  int           `json:"sample"`
		Cases   []RAGEvalCase `json:"cases"`
		AgentID string        `json:"agent_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		// 允许空 body: 直接按默认参数跑一次冒烟评估。
		req = struct {
			K       int           `json:"k"`
			Sample  int           `json:"sample"`
			Cases   []RAGEvalCase `json:"cases"`
			AgentID string        `json:"agent_id"`
		}{}
	}
	opts := NormalizeRAGEvalOptions(RAGEvalOptions{K: req.K, Sample: req.Sample, Cases: req.Cases})
	if req.AgentID != "" {
		// Agent 绑定的知识范围: 评估必须与线上检索同一范围, 否则指标无意义。
		opts.Categories = s.knowledgeCategories(req.AgentID)
	}
	report, err := s.ragEval.EvaluateRAG(r.Context(), opts)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, report)
}

// knowledgeCategories 解析 Agent 绑定的知识分类(未注入解析器时返回 nil = 全库)。
func (s *Service) knowledgeCategories(agentID string) []string {
	if s.categoryResolver == nil {
		return nil
	}
	return s.categoryResolver(agentID)
}

// SetKnowledgeCategoryResolver 注入「按 Agent 解析知识范围」的函数(由 AI 引擎提供)。
func (s *Service) SetKnowledgeCategoryResolver(fn func(agentID string) []string) {
	s.categoryResolver = fn
}

// NormalizeRAGEvalOptions 收敛评估参数(k/样本量边界), 供实现方复用同一套边界, 避免两处各写一份。
// 越界值向上/向下夹紧(而非重置为默认): 调用方要 50 条只给上限 100, 要 k=50 给 k=20,
// 结果仍是"尽量满足请求", 比静默退回默认值更可预期。幂等, 可安全重复调用。
func NormalizeRAGEvalOptions(opts RAGEvalOptions) RAGEvalOptions {
	if opts.K <= 0 {
		opts.K = 5
	} else if opts.K > 20 {
		opts.K = 20
	}
	if opts.Sample <= 0 {
		opts.Sample = 20
	} else if opts.Sample > 100 {
		opts.Sample = 100
	}
	for i := range opts.Cases {
		opts.Cases[i].Query = strings.TrimSpace(opts.Cases[i].Query)
	}
	return opts
}

// RankOf 返回首个期望结果在召回列表中的排名(1 起); 未命中返回 0。
// 纯函数, 不依赖存储层 —— 评估口径集中在这里, 便于单测与跨入口复用。
func RankOf(retrieved, expected []string) int {
	if len(expected) == 0 {
		return 0
	}
	want := make(map[string]bool, len(expected))
	for _, id := range expected {
		want[id] = true
	}
	for i, id := range retrieved {
		if want[id] {
			return i + 1
		}
	}
	return 0
}

// RecallAt 计算 top-n 内命中的期望结果占比(多期望结果时可能小于 1);
// n <= 0 表示不截断(即 Recall@K 取完整返回列表)。
func RecallAt(retrieved, expected []string, n int) float64 {
	if len(expected) == 0 {
		return 0
	}
	if n > 0 && len(retrieved) > n {
		retrieved = retrieved[:n]
	}
	got := make(map[string]bool, len(retrieved))
	for _, id := range retrieved {
		got[id] = true
	}
	hit := 0
	for _, id := range expected {
		if got[id] {
			hit++
		}
	}
	return float64(hit) / float64(len(expected))
}

// SummarizeRAGEval 汇总明细为报告(纯计算)。明细按「未命中优先」排序:
// 报告的第一屏应该是哪里不行, 而不是一堆通过的用例。
func SummarizeRAGEval(results []RAGEvalCaseResult, k int) *RAGEvalReport {
	rep := &RAGEvalReport{K: k, Cases: len(results), Details: results}
	if len(results) == 0 {
		rep.Details = []RAGEvalCaseResult{}
		return rep
	}
	var mrr, zero, latency, rec1, rec3, recK float64
	for _, r := range results {
		if r.Rank > 0 {
			rep.Hits++
			mrr += 1 / float64(r.Rank)
		}
		rec1 += RecallAt(r.Retrieved, r.Expected, 1)
		rec3 += RecallAt(r.Retrieved, r.Expected, 3)
		recK += RecallAt(r.Retrieved, r.Expected, k)
		if r.ZeroResult {
			zero++
		}
		latency += r.LatencyMS
	}
	n := float64(len(results))
	rep.HitRate = float64(rep.Hits) / n
	rep.RecallAt1, rep.RecallAt3, rep.RecallAtK = rec1/n, rec3/n, recK/n
	rep.MRR = mrr / n
	rep.ZeroRate = zero / n
	rep.AvgLatencyMS = latency / n
	// 失败优先(未命中在前, 同组按查询排序)。
	sort.SliceStable(rep.Details, func(i, j int) bool {
		if rep.Details[i].Hit != rep.Details[j].Hit {
			return !rep.Details[i].Hit
		}
		if rep.Details[i].Rank != rep.Details[j].Rank {
			return rep.Details[i].Rank > rep.Details[j].Rank
		}
		return rep.Details[i].Query < rep.Details[j].Query
	})
	return rep
}
