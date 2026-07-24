// heartbeat 子系统压测: 高频 Agent 心跳上报。
// Agent 通信路径在 auth 中间件里免认证, 且 Redis 不可用时心跳回退到内存存储 ——
// 验证该路径在 stub 模式下高并发稳定返回 200, 不 panic。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"loadtest/internal/loadlib"
)

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "Core base URL")
	conc := flag.Int("conc", 100, "concurrency")
	reqs := flag.Int("reqs", 1000, "total requests")
	flag.Parse()

	cfg := loadlib.Config{
		Name:        "heartbeat",
		BaseURL:     *base,
		Concurrency: *conc,
		Requests:    *reqs,
		Timeout:     10 * time.Second,
		Target: loadlib.Target{
			// 固定 agent id 即可(心跳路径对未知 id 也接受, 落内存存储)
			Method: "POST",
			Path:   "/api/v1/agents/agent-loadtest-1/heartbeat",
			Body: func() []byte {
				b, _ := json.Marshal(map[string]any{
					"hostname":     "lt-host",
					"os":           "linux",
					"agent_version": "1.0",
					"ips":          []string{"10.0.0.1"},
				})
				return b
			},
		},
	}

	rep := loadlib.Run(cfg)
	passRate := 0.0
	if rep.Total > 0 {
		passRate = float64(rep.OK) / float64(rep.Total)
	}
	pass := passRate >= 0.99

	fmt.Println(rep.SummaryJSON(map[string]any{"pass": pass}))
	if !pass {
		os.Exit(1)
	}
}
