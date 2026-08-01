package ingest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// VMSink 将指标批次写入 VictoriaMetrics(时序库)。
// 写入走 Prometheus 文本导入 API(POST /api/v1/import/prometheus), 标准库实现零额外依赖。
// 指标命名: 原 name 直接作为 metric 名, labels 合并 agent_id/task_id/unit 等维度。
type VMSink struct {
	baseURL string // 如 http://localhost:8428
	client  *http.Client
}

// NewVMSink 创建 VM sink。
func NewVMSink(baseURL string) *VMSink {
	return &VMSink{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Write 实现 Sink: ���一批指标点转为 Prometheus 文本格式并 POST 到 VM。
func (s *VMSink) Write(ctx context.Context, b Batch) error {
	body := batchToPromText(b)
	if body == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/api/v1/import/prometheus", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; version=0.0.4")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("vm import: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("vm import status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// batchToPromText 将批次转为 Prometheus 文本格式(每行一条, 时间戳毫秒)。
func batchToPromText(b Batch) string {
	var sb strings.Builder
	ts := b.Timestamp.UnixMilli()
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}
	for _, p := range b.Points {
		if p.Name == "" {
			continue
		}
		labels := make([]string, 0, len(p.Labels)+3)
		labels = append(labels, fmt.Sprintf(`agent_id="%s"`, escapeLabel(b.AgentID)))
		if b.TaskID != "" {
			labels = append(labels, fmt.Sprintf(`task_id="%s"`, escapeLabel(b.TaskID)))
		}
		if b.TaskType != "" {
			labels = append(labels, fmt.Sprintf(`task_type="%s"`, escapeLabel(b.TaskType)))
		}
		if p.Unit != "" {
			labels = append(labels, fmt.Sprintf(`unit="%s"`, escapeLabel(p.Unit)))
		}
		for k, v := range p.Labels {
			labels = append(labels, fmt.Sprintf(`%s="%s"`, escapeLabel(k), escapeLabel(v)))
		}
		sb.WriteString(p.Name)
		sb.WriteString("{")
		sb.WriteString(strings.Join(labels, ","))
		sb.WriteString("} ")
		sb.WriteString(fmt.Sprintf("%g %d\n", p.Value, ts))
	}
	return sb.String()
}

func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

// QueryHandler 返回 PromQL 查询代理 Handler: 转发到 VM 的 /api/v1/query 或 /api/v1/query_range。
// 供监控大盘/告警使用。GET /api/v1/monitor/ts/query?query=<promql>[&time/start/end/step]
func QueryHandler(baseURL string) http.Handler {
	client := &http.Client{Timeout: 30 * time.Second}
	vmBase := strings.TrimRight(baseURL, "/")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		promQL := q.Get("query")
		if promQL == "" {
			http.Error(w, `{"error":"query parameter is required"}`, http.StatusBadRequest)
			return
		}
		// 透传 query/time/start/end/step 等参数到 VM
		params := url.Values{}
		for k, vs := range q {
			for _, v := range vs {
				params.Add(k, v)
			}
		}
		endpoint := "/api/v1/query"
		if params.Get("start") != "" || params.Get("end") != "" || params.Get("step") != "" {
			endpoint = "/api/v1/query_range"
		}
		target := vmBase + endpoint + "?" + params.Encode()

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("vm query failed", "error", err, "query", promQL)
			http.Error(w, `{"error":"timeseries backend unreachable"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}
