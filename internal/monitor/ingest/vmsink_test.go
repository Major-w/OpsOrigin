package ingest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/tunnel"
)

func TestBatchToPromText(t *testing.T) {
	b := Batch{
		AgentID:   "agent-001",
		TaskID:    "t-monitor-1",
		TaskType:  "monitor",
		Timestamp: time.Unix(1722500000, 0),
		Points: []tunnel.MetricPoint{
			{Name: "cpu.usage", Value: 0.62, Labels: map[string]string{"core": "0"}},
			{Name: "mem.used", Value: 8192, Unit: "MB"},
			{Name: `weird"name`, Value: 1}, // 标签转义测试
		},
	}
	text := batchToPromText(b)

	if !strings.Contains(text, `cpu.usage{agent_id="agent-001",task_id="t-monitor-1",task_type="monitor",core="0"} 0.62 1722500000000`) {
		t.Fatalf("unexpected line: %q", text)
	}
	if !strings.Contains(text, `mem.used{agent_id="agent-001",task_id="t-monitor-1",task_type="monitor",unit="MB"} 8192 1722500000000`) {
		t.Fatalf("unexpected line: %q", text)
	}
	if !strings.Contains(text, `unit="MB"`) {
		t.Fatalf("missing unit label: %q", text)
	}
	if strings.Count(text, "\n") != 3 {
		t.Fatalf("expected 3 lines, got %q", text)
	}
}

func TestVMSinkWrite(t *testing.T) {
	var gotBody string
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/import/prometheus" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer vm.Close()

	sink := NewVMSink(vm.URL)
	err := sink.Write(context.Background(), Batch{
		AgentID:   "a1",
		TaskID:    "t1",
		TaskType:  "monitor",
		Timestamp: time.Unix(1722500000, 0),
		Points:    []tunnel.MetricPoint{{Name: "cpu.usage", Value: 0.5, Labels: map[string]string{"core": "1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, `cpu.usage{agent_id="a1",task_id="t1",task_type="monitor",core="1"} 0.5`) {
		t.Fatalf("unexpected body: %q", gotBody)
	}
}

func TestVMSinkWriteError(t *testing.T) {
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "too many open files")
	}))
	defer vm.Close()

	sink := NewVMSink(vm.URL)
	err := sink.Write(context.Background(), Batch{
		AgentID: "a1", Points: []tunnel.MetricPoint{{Name: "x", Value: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestQueryHandlerProxy(t *testing.T) {
	var gotPath string
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	}))
	defer vm.Close()

	h := QueryHandler(vm.URL)
	req := httptest.NewRequest("GET", "/api/v1/monitor/ts/query?query=avg(cpu.usage)&start=100&end=200&step=30", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(gotPath, "/api/v1/query_range") {
		t.Fatalf("expected query_range endpoint, got %s", gotPath)
	}
	if !strings.Contains(gotPath, "query=avg%28cpu.usage%29") {
		t.Fatalf("query param not forwarded: %s", gotPath)
	}
	if !strings.Contains(w.Body.String(), "resultType") {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestQueryHandlerRequiresQuery(t *testing.T) {
	h := QueryHandler("http://localhost:8428")
	req := httptest.NewRequest("GET", "/api/v1/monitor/ts/query", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
