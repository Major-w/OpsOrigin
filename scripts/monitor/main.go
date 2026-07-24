// monitor 子系统压测: 并发拉取监控指标端点。
// 验证 F4-R3(并发读 monitor_probe_results 无数据竞争/无 panic)——
// 高频 GET /api/v1/monitor/metrics 在并发下稳定返回 2xx。
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
	conc := flag.Int("conc", 50, "concurrency")
	reqs := flag.Int("reqs", 1000, "total requests")
	flag.Parse()

	cfg := loadlib.Config{
		Name:        "monitor-metrics",
		BaseURL:     *base,
		Concurrency: *conc,
		Requests:    *reqs,
		Timeout:     10 * time.Second,
		Target:      loadlib.Target{Method: "GET", Path: "/api/v1/monitor/metrics"},
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
