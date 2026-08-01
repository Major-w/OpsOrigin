package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/tunnel"
)

func TestBufferFlushByInterval(t *testing.T) {
	sink := NewMemorySink()
	buf := NewBuffer(sink, WithFlushInterval(50*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	buf.Start(ctx)
	defer cancel()

	buf.Submit(tunnel.MetricsReport{
		AgentID:  "a1",
		TaskID:   "t-monitor-1",
		TaskType: "monitor",
		Points: []tunnel.MetricPoint{
			{Name: "cpu.usage", Value: 0.55, Labels: map[string]string{"core": "0"}},
			{Name: "mem.used", Value: 4096, Unit: "MB"},
		},
	})

	waitFor(t, 2*time.Second, func() bool { return sink.Total() == 1 })
	last := sink.Last()
	if last == nil {
		t.Fatal("expected batch written to sink")
	}
	if last.TaskID != "t-monitor-1" || last.TaskType != "monitor" || last.AgentID != "a1" {
		t.Fatalf("unexpected batch: %+v", last)
	}
	if len(last.Points) != 2 || last.Points[0].Labels["core"] != "0" {
		t.Fatalf("unexpected points: %+v", last.Points)
	}
}

func TestBufferBatchSizeFlush(t *testing.T) {
	sink := NewMemorySink()
	buf := NewBuffer(sink, WithBatchSize(3), WithFlushInterval(time.Hour)) // 仅按条数触发
	ctx, cancel := context.WithCancel(context.Background())
	buf.Start(ctx)
	defer cancel()

	for i := 0; i < 3; i++ {
		buf.Submit(tunnel.MetricsReport{AgentID: "a1", TaskID: "t", Points: []tunnel.MetricPoint{{Name: "x", Value: 1}}})
	}
	waitFor(t, 2*time.Second, func() bool { return sink.Total() == 3 })

	st := buf.Stats()
	if st.Received != 3 || st.Points != 3 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestBufferBackpressureDrop(t *testing.T) {
	sink := NewMemorySink()
	// 队列极小 + 永不 flush(时间窗口极大), 强制触发背压丢弃
	buf := NewBuffer(sink, WithFlushInterval(time.Hour))
	buf.ch = make(chan Batch, 1)
	ctx, cancel := context.WithCancel(context.Background())
	buf.Start(ctx)
	defer cancel()

	for i := 0; i < 100; i++ {
		buf.Submit(tunnel.MetricsReport{AgentID: "a1", TaskID: "t", Points: []tunnel.MetricPoint{{Name: "x", Value: 1}}})
	}
	st := buf.Stats()
	if st.Dropped == 0 {
		t.Fatalf("expected dropped > 0, stats: %+v", st)
	}
	if st.Received != 100 {
		t.Fatalf("expected received 100, got %d", st.Received)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
