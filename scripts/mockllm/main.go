// mockllm 是一个零依赖的伪 LLM 服务, 仅实现 OpenAI 兼容的
// POST /v1/chat/completions 端点, 供 F4 压测在 stub 模式下驱动 AI 子系统。
//
// 它故意引入 ~120ms 的固定延迟, 以模拟真实 LLM 的推理耗时 —— 这样才能
// 让物理 LLM 并发信号量(容量 32)真正生效, 从而在压测中观测到:
//   ai_concurrent_sessions 峰值≈逻辑会话数(如 100)
//   ai_llm_inflight        峰值≤物理槽位数(32)
package main

import (
	"fmt"
	"net/http"
	"time"
)

const mockLatency = 120 * time.Millisecond

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		// 模拟 LLM 推理耗时
		time.Sleep(mockLatency)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// 必须返回 OpenAI 兼容结构: choices[0].message.content / usage
		fmt.Fprint(w, `{
  "id": "chatcmpl-mock",
  "object": "chat.completion",
  "created": 1700000000,
  "model": "mock",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "mock ok"},
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
}`)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	addr := ":18999"
	fmt.Printf("mock-llm listening on %s (latency=%s)\n", addr, mockLatency)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Printf("mock-llm exited: %v\n", err)
	}
}
