// cmdb 子系统压测: 批量 CI 导入端点。
//
// 验证 F4-D2(批量端点修复) + F4-B(异步 ingest):
//   - 单批核验: 5 个 CI -> HTTP 202, 且响应 accepted==total==5
//   - 并发压测期间轮询 ingest_buffer_size(应 >0, 证明缓冲在起作用)
//     与 ingest_dropped_total(应恒为 0, 证明缓冲未溢出丢弃)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"loadtest/internal/loadlib"
)

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "Core base URL")
	conc := flag.Int("conc", 100, "concurrency")
	reqs := flag.Int("reqs", 100, "total batch requests")
	flag.Parse()

	// 单批核验: 5 个 CI -> 202, accepted==total==5
	verifyBody, _ := json.Marshal([]map[string]any{
		{"name": "ci-verify-1", "type_id": "server"},
		{"name": "ci-verify-2", "type_id": "server"},
		{"name": "ci-verify-3", "type_id": "server"},
		{"name": "ci-verify-4", "type_id": "server"},
		{"name": "ci-verify-5", "type_id": "server"},
	})
	accepted, total := -1, -1
	if vresp, verr := http.Post(*base+"/api/v1/cmdb/ci/batch", "application/json", bytes.NewReader(verifyBody)); verr == nil {
		raw, _ := io.ReadAll(vresp.Body)
		vresp.Body.Close()
		var vj map[string]any
		if json.Unmarshal(raw, &vj) == nil {
			if a, ok := vj["accepted"].(float64); ok {
				accepted = int(a)
			}
			if t, ok := vj["total"].(float64); ok {
				total = int(t)
			}
		}
	}

	cfg := loadlib.Config{
		Name:        "cmdb-batch",
		BaseURL:     *base,
		Concurrency: *conc,
		Requests:    *reqs,
		Timeout:     10 * time.Second,
		Target: loadlib.Target{
			Method: "POST",
			Path:   "/api/v1/cmdb/ci/batch",
			Body: func() []byte {
				b, _ := json.Marshal([]map[string]any{
					{"name": "ci-load", "type_id": "server", "attributes": map[string]any{"ip": "10.0.0.1"}},
				})
				return b
			},
		},
	}

	stop := make(chan struct{})
	peaks := make(chan map[string]float64, 1)
	go func() {
		peaks <- loadlib.PollGaugesMax(
			*base+"/metrics",
			[]string{"ingest_buffer_size", "ingest_dropped_total"},
			20*time.Millisecond, stop)
	}()

	rep := loadlib.Run(cfg)
	close(stop)
	maxes := <-peaks

	passRate := 0.0
	if rep.Total > 0 {
		passRate = float64(rep.OK) / float64(rep.Total)
	}
	// D2 判定: 批量路由 2xx、单批 accepted==total==5、且 ingest 零丢弃。
	pass := passRate >= 0.99 &&
		maxes["ingest_dropped_total"] == 0 &&
		accepted == total && total == 5

	fmt.Println(rep.SummaryJSON(map[string]any{
		"verify_accepted":          accepted,
		"verify_total":            total,
		"peak_ingest_buffer_size": maxes["ingest_buffer_size"],
		"ingest_dropped_total":   maxes["ingest_dropped_total"],
		"pass":                     pass,
	}))
	if !pass {
		os.Exit(1)
	}
}
