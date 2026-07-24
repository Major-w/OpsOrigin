// Package loadlib 是零依赖的轻量压测库, 供 scripts/ 下的各子系统压测程序复用。
// 仅使用标准库, 因此 scripts/ 是一个完全自包含的 Go module(无需 go.sum / 外网)。
package loadlib

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Result 单次请求的结果。
type Result struct {
	Status  int
	Latency time.Duration
	Err     error
}

// Target 描述一次请求的模板。
type Target struct {
	Method string
	Path   string
	Body   func() []byte            // GET 时可省略
	Header func() map[string]string // 可选
}

// Config 压测运行配置。
type Config struct {
	Name        string
	BaseURL     string
	Concurrency int
	Requests    int
	Target      Target
	Think       time.Duration // 每个 worker 两次迭代之间的思考时间
	Client      *http.Client
	Timeout     time.Duration
}

func (c Config) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	t := c.Timeout
	if t <= 0 {
		t = 10 * time.Second
	}
	return &http.Client{Timeout: t}
}

// Report 聚合一次压测的结果。
type Report struct {
	Name      string
	Total     int
	OK        int
	Fail      int
	byStatus  map[int]int
	latencies []time.Duration
	mu        sync.Mutex
	errs      []string
}

// NewReport 构造空报告。
func NewReport(name string) *Report {
	return &Report{Name: name, byStatus: map[int]int{}}
}

func (r *Report) record(res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Total++
	if res.Err != nil {
		r.Fail++
		if len(r.errs) < 5 {
			r.errs = append(r.errs, res.Err.Error())
		}
		return
	}
	r.byStatus[res.Status]++
	if res.Status >= 200 && res.Status < 400 {
		r.OK++
	} else {
		r.Fail++
	}
	r.latencies = append(r.latencies, res.Latency)
}

// P 返回第 pct 百分位的延迟(0<pct<=100)。
func (r *Report) P(pct float64) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.latencies) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), r.latencies...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	idx := int((pct / 100.0) * float64(len(s)-1))
	if idx < 0 {
		idx = 0
	}
	if idx > len(s)-1 {
		idx = len(s) - 1
	}
	return s[idx]
}

// RPS 计算整体吞吐。
func (r *Report) RPS(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(r.Total) / d.Seconds()
}

// Run 执行压测: 以 Concurrency 个 worker 并发吃掉 Requests 个请求。
func Run(cfg Config) *Report {
	rep := NewReport(cfg.Name)
	reqCh := make(chan struct{}, cfg.Requests)
	for i := 0; i < cfg.Requests; i++ {
		reqCh <- struct{}{}
	}
	close(reqCh)

	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cl := cfg.client()
			for range reqCh {
				rep.record(doOne(cl, cfg))
				if cfg.Think > 0 {
					time.Sleep(cfg.Think)
				}
			}
		}()
	}
	wg.Wait()
	return rep
}

func doOne(cl *http.Client, cfg Config) Result {
	var bodyReader io.Reader
	if cfg.Target.Body != nil {
		bodyReader = bytes.NewReader(cfg.Target.Body())
	}
	method := cfg.Target.Method
	if method == "" {
		method = http.MethodGet
	}
	start := time.Now()
	req, err := http.NewRequest(method, cfg.BaseURL+cfg.Target.Path, bodyReader)
	if err != nil {
		return Result{Status: 0, Latency: time.Since(start), Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Target.Header != nil {
		for k, v := range cfg.Target.Header() {
			req.Header.Set(k, v)
		}
	}
	resp, err := cl.Do(req)
	lat := time.Since(start)
	if err != nil {
		return Result{Status: 0, Latency: lat, Err: err}
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return Result{Status: resp.StatusCode, Latency: lat}
}

// ScrapeGauge 从 Prometheus 文本格式 /metrics 端点读取单个 gauge 的当前值。
func ScrapeGauge(metricsURL, name string) (float64, error) {
	resp, err := http.Get(metricsURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, name+" ") {
			var v float64
			if _, err := fmt.Sscanf(line, name+" %f", &v); err == nil {
				return v, nil
			}
		}
	}
	return 0, fmt.Errorf("metric %s not found", name)
}

// PollGaugesMax 以 interval 间隔采样给定 gauges, 直到 stop 关闭(或内部超时),
// 返回每个 gauge 观测到的峰值。用于在压测过程中捕获瞬时指标的最大值。
func PollGaugesMax(metricsURL string, names []string, interval time.Duration, stop <-chan struct{}) map[string]float64 {
	maxes := make(map[string]float64, len(names))
	for _, n := range names {
		maxes[n] = 0
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	sample := func() {
		for _, n := range names {
			if v, err := ScrapeGauge(metricsURL, n); err == nil && v > maxes[n] {
				maxes[n] = v
			}
		}
	}
	sample()
	for {
		select {
		case <-stop:
			sample()
			return maxes
		case <-t.C:
			sample()
		}
	}
}

// SummaryJSON 输出机器可解析的 JSON 汇总行(供 runner 收集)。
func (r *Report) SummaryJSON(extra map[string]any) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := map[string]any{
		"name":     r.Name,
		"total":    r.Total,
		"ok":       r.OK,
		"fail":     r.Fail,
		"by_status": r.byStatus,
		"p50_ms":   int64(r.P(50) / time.Millisecond),
		"p95_ms":   int64(r.P(95) / time.Millisecond),
		"p99_ms":   int64(r.P(99) / time.Millisecond),
		"errors":   r.errs,
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}
