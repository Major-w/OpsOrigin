// itsm 子系统压测: 并发创建工单。
// 验证 stub 模式(无 PG, 内存回退)下工单创建链路在高并发不崩、无 5xx。
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
	conc := flag.Int("conc", 50, "concurrency")
	reqs := flag.Int("reqs", 500, "total requests")
	flag.Parse()

	cfg := loadlib.Config{
		Name:        "itsm-tickets",
		BaseURL:     *base,
		Concurrency: *conc,
		Requests:    *reqs,
		Timeout:     10 * time.Second,
		Target: loadlib.Target{
			Method: "POST",
			Path:   "/api/v1/itsm/tickets",
			Body: func() []byte {
				b, _ := json.Marshal(map[string]any{
					"title":          "loadtest ticket",
					"description":    "auto-generated",
					"risk_level":     "low",
					"created_by":     "dev",
					"created_by_type": "human",
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
