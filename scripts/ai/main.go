// ai 子系统压测: 高频并发 AI 对话请求。
//
// 验证 F4-D1(逻辑/物理并发解耦): 在压测期间轮询 /metrics,
// 捕获两个关键指标的峰值 ——
//   ai_concurrent_sessions 峰值应≈逻辑会话数(并发度, 如 100)
//   ai_llm_inflight        峰值应≤物理 LLM 槽位数(32)
// 二者明显背离即证明解耦生效: SSE/会话层不受物理 LLM 并发上限约束。
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"loadtest/internal/loadlib"
)

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "Core base URL")
	conc := flag.Int("conc", 100, "concurrency (logical sessions)")
	reqs := flag.Int("reqs", 2000, "total requests (sustained to observe peaks)")
	flag.Parse()

	cfg := loadlib.Config{
		Name:        "ai-chat",
		BaseURL:     *base,
		Concurrency: *conc,
		Requests:    *reqs,
		Timeout:     30 * time.Second,
		Target: loadlib.Target{
			Method: "POST",
			Path:   "/api/v1/ai/chat",
			Body: func() []byte {
				// agent_id 留空 -> 走默认 system prompt, 且跳过 agent 维度限流,
				// 从而让 100 路并发全部进入会话计数。
				return []byte(`{"prompt":"ping","agent_id":"","stream":false}`)
			},
		},
	}

	// 在压测全程轮询指标峰值
	stop := make(chan struct{})
	peaks := make(chan map[string]float64, 1)
	go func() {
		peaks <- loadlib.PollGaugesMax(
			*base+"/metrics",
			[]string{"ai_concurrent_sessions", "ai_llm_inflight", "ai_llm_ratelimit_hit_total"},
			20*time.Millisecond, stop)
	}()

	rep := loadlib.Run(cfg)
	close(stop)
	maxes := <-peaks

	passRate := 0.0
	if rep.Total > 0 {
		passRate = float64(rep.OK) / float64(rep.Total)
	}
	// D1 判定: 逻辑并发达到≈并发度, 物理并发被 32 槽位限住, 且无限流命中。
	pass := passRate >= 0.99 &&
		maxes["ai_concurrent_sessions"] >= float64(*conc)*0.9 &&
		maxes["ai_llm_inflight"] <= 32 &&
		maxes["ai_llm_ratelimit_hit_total"] == 0

	fmt.Println(rep.SummaryJSON(map[string]any{
		"peak_ai_concurrent_sessions": maxes["ai_concurrent_sessions"],
		"peak_ai_llm_inflight":        maxes["ai_llm_inflight"],
		"ai_llm_ratelimit_hits":      maxes["ai_llm_ratelimit_hit_total"],
		"pass":                        pass,
	}))
	if !pass {
		os.Exit(1)
	}
}
