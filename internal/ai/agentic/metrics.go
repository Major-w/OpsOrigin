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
)

func init() {
	metrics.Registry.MustRegister(AIConcurrentSessions, AILLMInFlight, AILLMRateLimitHits, AIToolCalls, AIToolExecDenied)
}
