package collect

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// PrometheusSource 通过 HTTP 抓取 Prometheus exporter 的 /metrics 端点。
//
// 兼容任意 Prometheus exporter(node_exporter / mysqld_exporter / 自定义 / ...),
// 复用整个 Prometheus 采集生态, 无需 OpsOrigin 自己写采集器。
type PrometheusSource struct {
	cfg     Config
	url     string
	client  *http.Client
	timeout time.Duration
}

// NewPrometheusSource 构造 Prometheus 采集源(端口默认 9100, 路径默认 /metrics)。
func NewPrometheusSource(cfg Config) *PrometheusSource {
	port := cfg.Port
	if port == 0 {
		port = 9100
	}
	path := cfg.Path
	if path == "" {
		path = "/metrics"
	}
	host := cfg.Address
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return &PrometheusSource{
		cfg:     cfg,
		url:     fmt.Sprintf("http://%s%s", host, path),
		client:  &http.Client{Timeout: 10 * time.Second},
		timeout: 10 * time.Second,
	}
}

// Type 实现 Source。
func (s *PrometheusSource) Type() string { return TypePrometheus }

// Collect 抓取并解析 exposition 格式。
func (s *PrometheusSource) Collect(ctx context.Context) ([]Sample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus scrape status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return ParseExposition(string(body), s.baseLabels()), nil
}

func (s *PrometheusSource) baseLabels() map[string]string {
	labels := map[string]string{
		"instance": s.cfg.Address,
		"source":   "prometheus",
	}
	if s.cfg.CIID != "" {
		labels["ci_id"] = s.cfg.CIID
	}
	for k, v := range s.cfg.Labels {
		labels[k] = v
	}
	return labels
}

// ParseExposition 解析 Prometheus 文本暴露格式, 返回样本切片。
// 支持 `name value`、`name{labels} value` 及可选的时间戳后缀;
// 跳过注释行与直方图桶(_bucket)以降低噪声。
func ParseExposition(text string, extra map[string]string) []Sample {
	out := make([]Sample, 0)
	now := time.Now()
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, valueStr, ok := splitSample(line)
		if !ok {
			continue
		}
		if strings.HasSuffix(name, "_bucket") {
			continue
		}
		v, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			continue
		}
		out = append(out, Sample{Name: name, Value: v, Labels: mergeLabels(extra, labels), Timestamp: now})
	}
	return out
}

// splitSample 解析一行 exposition: name{labels} value [timestamp]。
// 返回指标名、标签、数值字符串。
func splitSample(line string) (name string, labels map[string]string, value string, ok bool) {
	sp := strings.IndexAny(line, " \t")
	if sp < 0 {
		return "", nil, "", false
	}
	head := line[:sp]
	value = strings.TrimSpace(line[sp+1:])
	if sp2 := strings.IndexAny(value, " \t"); sp2 >= 0 { // 去掉可选时间戳
		value = value[:sp2]
	}
	labels = map[string]string{}
	if i := strings.Index(head, "{"); i >= 0 && strings.HasSuffix(head, "}") {
		name = head[:i]
		labelStr := head[i+1 : len(head)-1]
		for _, kv := range strings.Split(labelStr, ",") {
			kv = strings.TrimSpace(kv)
			if kv == "" {
				continue
			}
			eq := strings.Index(kv, "=")
			if eq < 0 {
				continue
			}
			labels[kv[:eq]] = strings.Trim(kv[eq+1:], `"`)
		}
	} else {
		name = head
	}
	if name == "" {
		return "", nil, "", false
	}
	return name, labels, value, true
}
