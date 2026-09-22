package collect

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestZabbixSource 起一个 mock TCP 被动 agent, 验证 key→value 解析与跳过非数值。
func TestZabbixSource(t *testing.T) {
	// mock: 收到 key 后回一行值; 未知 key 回 ZBX_NOTSUPPORTED。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 256)
				n, _ := c.Read(buf)
				key := string(buf[:n])
				switch key {
				case "system.cpu.load[all,avg1]\n":
					c.Write([]byte("1.23\n"))
				case "vm.memory.size[used]\n":
					c.Write([]byte("4096\n"))
				default:
					c.Write([]byte("ZBX_NOTSUPPORTED\r\n"))
				}
			}(conn)
		}
	}()

	src := NewZabbixSource(Config{
		ID:      "t",
		TenantID: "default",
		Type:    TypeZabbix,
		Address: ln.Addr().String(),
		Keys:    []string{"system.cpu.load[all,avg1]", "vm.memory.size[used]", "not.exist"},
		Labels:  map[string]string{"env": "test"},
	})
	samples, err := src.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect err: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples (unsupported skipped), got %d: %+v", len(samples), samples)
	}
	if samples[0].Name != "system_cpu_load_all_avg1" {
		t.Errorf("sanitized name = %q", samples[0].Name)
	}
	if samples[0].Value != 1.23 {
		t.Errorf("value = %v", samples[0].Value)
	}
	if samples[0].Labels["source"] != "zabbix" || samples[0].Labels["env"] != "test" {
		t.Errorf("labels = %+v", samples[0].Labels)
	}
}

// TestParseExposition 验证 Prometheus 文本格式解析(带标签/不带标签/跳过 bucket)。
func TestParseExposition(t *testing.T) {
	text := `# HELP node_cpu_seconds_total CPU.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{mode="idle"} 12345.6
node_memory_MemTotal_bytes 8.0e9
http_requests_total{code="200"} 42
my_hist_bucket{le="0.5"} 3
my_hist_count 7
`
	extra := map[string]string{"instance": "10.0.0.1:9100", "source": "prometheus"}
	out := ParseExposition(text, extra)
	// 4 条有效(1 counter + 1 gauge + 1 code-labeled + 1 count), bucket 被跳过
	if len(out) != 4 {
		t.Fatalf("expected 4 samples, got %d: %+v", len(out), out)
	}
	byName := map[string]Sample{}
	for _, s := range out {
		byName[s.Name] = s
	}
	if v, ok := byName["node_cpu_seconds_total"]; !ok || v.Value != 12345.6 || v.Labels["mode"] != "idle" {
		t.Errorf("cpu sample wrong: %+v", v)
	}
	if v, ok := byName["node_memory_MemTotal_bytes"]; !ok || v.Value != 8.0e9 {
		t.Errorf("mem sample wrong: %+v", v)
	}
	// instance 来自 extra, 不被覆盖
	if byName["node_cpu_seconds_total"].Labels["instance"] != "10.0.0.1:9100" {
		t.Errorf("instance label wrong: %+v", byName["node_cpu_seconds_total"].Labels)
	}
}

// TestPrometheusSource 起 httptest server, 验证 HTTP 抓取端到端。
func TestPrometheusSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("up 1\nfoo{bar=\"baz\"} 2.5\n"))
	}))
	defer srv.Close()

	src := NewPrometheusSource(Config{
		ID: "p", TenantID: "default", Type: TypePrometheus,
		Address: srv.Listener.Addr().String(), Path: "/metrics",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	samples, err := src.Collect(ctx)
	if err != nil {
		t.Fatalf("collect err: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d: %+v", len(samples), samples)
	}
	if samples[1].Name != "foo" || samples[1].Value != 2.5 || samples[1].Labels["bar"] != "baz" {
		t.Errorf("foo sample wrong: %+v", samples[1])
	}
}
