package agentic

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/opsorigin/core/internal/monitor/metrics"
)

// AI 引擎相关 Prometheus 指标(F4-C 决策 C)。
// 全部注册到全局 metrics.Registry, 经 /metrics 暴露。
var (
	// AIConcurrentSessions 当前进行中的 AI 会话数(逻辑并发)。
	// 在 HTTP 会话入口计入, 与物理 LLM 调用槽位解耦 ——
	// 例: 100 路并发 SSE 会话时该值峰值=100, 而物理 LLM 调用受 llmSem 限为 32。
	AIConcurrentSessions = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ai_concurrent_sessions",
		Help: "Current number of in-flight AI chat sessions (logical concurrency, decoupled from physical LLM slots)",
	})

	// AILLMInFlight 当前正在占用物理 LLM 调用槽位的请求数(受 llmSem 约束)。
	AILLMInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ai_llm_inflight",
		Help: "Current number of in-flight physical LLM calls (bounded by the llmSem semaphore)",
	})

	// AILLMRateLimitHits LLM 层(agent/plan 维度)限流命中次数。
	AILLMRateLimitHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ai_llm_ratelimit_hit_total",
		Help: "Total number of LLM-layer rate-limit hits (agent/plan dimension)",
	})

	// AIToolCalls AI 助手通过 Function Calling 自主发起的 MCP 工具调用次数。
	AIToolCalls = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ai_tool_calls_total",
		Help: "Total number of MCP tool invocations initiated by the model via Function Calling",
	})

	// AIToolExecDenied 因 RBAC 权限不足或限流而被拒绝执行的工具调用次数。
	AIToolExecDenied = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ai_tool_exec_denied_total",
		Help: "Total number of AI tool executions denied by RBAC or rate-limit (e.g. script_run by a viewer)",
	})

	// AIHistoryCompactions 因超长对话触发的 Token 预算压缩次数(确定性回退或 LLM 摘要)。
	AIHistoryCompactions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ai_history_compactions_total",
		Help: "Total number of long-conversation history compactions (Token-budget compression) performed before building LLM messages",
	})

	// AIToolCacheHits 工具结果缓存命中次数(幂等/只读工具按 name+tenant+args 复用, 减少重复 LLM 工具往返)。
	AIToolCacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ai_tool_cache_hits_total",
		Help: "Total number of AI tool-result cache hits (idempotent/read-only tools reused by name+tenant+args)",
	})

	// AIKnowledgeCacheHits 知识库检索缓存命中次数(embedding 去重 + 向量/ILIKE 检索结果复用)。
	AIKnowledgeCacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ai_knowledge_cache_hits_total",
		Help: "Total number of knowledge-base search cache hits (embedding dedup + vector/ILIKE result reuse)",
	})

	// —— 知识库检索可观测(P1-2/P1-4) ——
	// 此前 RAG 检索只有一个「未索引文章数」指标, 检索本身是黑盒:
	// 命中率/延迟/零结果/各条召回路的实际贡献全都看不到, 回答质量差时无法区分
	// 「知识库里没有」和「检索没召回到」。以下四个指标 + knowledge_search_logs 留痕表补齐这一层。

	// AIKnowledgeSearchTotal 知识库检索次数(按检索路径拆分)。
	// mode: rrf(块级双路融合) | fallback(块级全空回退文章级) | cache(结果缓存命中)。
	AIKnowledgeSearchTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ai_knowledge_search_total",
		Help: "Total number of knowledge-base retrievals issued by the AI engine, labeled by retrieval path (rrf/fallback/cache)",
	}, []string{"mode"})

	// AIKnowledgeSearchLatency 知识库检索端到端耗时(含 query embedding 与多路召回)。
	AIKnowledgeSearchLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ai_knowledge_search_latency_seconds",
		Help:    "End-to-end latency of a knowledge-base retrieval (query embedding + multi-route recall + fusion)",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"mode"})

	// AIKnowledgeSearchHits 单次检索返回的知识条数分布。
	// 峰值长期贴在 0 而请求量不低 => 知识覆盖不足, 应按零结果留痕补知识。
	AIKnowledgeSearchHits = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "ai_knowledge_search_hits",
		Help:    "Distribution of the number of knowledge articles returned per AI retrieval",
		Buckets: []float64{0, 1, 2, 3, 4, 5, 8},
	})

	// AIKnowledgeSearchZeroResults 零结果检索次数(知识盲区查询)。
	AIKnowledgeSearchZeroResults = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ai_knowledge_search_zero_total",
		Help: "Total number of knowledge-base retrievals that returned no article at all (knowledge gap)",
	}, []string{"mode"})

	// AIKnowledgeSearchRouteHits 各条召回路「有命中」的检索次数(用于判断某条路是否形同虚设)。
	// route: vector(块级向量) | keyword(tsvector/ILIKE) | fallback(文章级兜底)。
	AIKnowledgeSearchRouteHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ai_knowledge_search_route_hits_total",
		Help: "Total number of retrievals in which the given recall route produced at least one hit (vector/keyword/fallback)",
	}, []string{"route"})
)

func init() {
	metrics.Registry.MustRegister(
		AIConcurrentSessions, AILLMInFlight, AILLMRateLimitHits, AIToolCalls, AIToolExecDenied,
		AIHistoryCompactions, AIToolCacheHits, AIKnowledgeCacheHits,
		AIKnowledgeSearchTotal, AIKnowledgeSearchLatency, AIKnowledgeSearchHits,
		AIKnowledgeSearchZeroResults, AIKnowledgeSearchRouteHits,
	)
}
