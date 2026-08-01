package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSchedulerRunsTasksImmediately(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	sched := NewScheduler(func(task *Task, _ []MetricPoint) {
		mu.Lock()
		ran = append(ran, task.ID)
		mu.Unlock()
	})
	sched.Register(&Task{
		ID: "t1", Type: TaskMonitor, Interval: time.Hour, // 仅靠首轮立即执行
		Run: func(ctx context.Context) ([]MetricPoint, error) {
			return []MetricPoint{{Name: "x", Value: 1}}, nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	sched.Start(ctx)
	defer cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(ran)
		mu.Unlock()
		if n >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task not executed on start")
}

func TestSchedulerIntervalTicks(t *testing.T) {
	var mu sync.Mutex
	count := 0
	sched := NewScheduler(func(task *Task, _ []MetricPoint) {
		mu.Lock()
		count++
		mu.Unlock()
	})
	sched.Register(&Task{
		ID: "t2", Type: TaskMonitor, Interval: 30 * time.Millisecond,
		Run: func(ctx context.Context) ([]MetricPoint, error) {
			return []MetricPoint{{Name: "x", Value: 1}}, nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	sched.Start(ctx)
	time.Sleep(120 * time.Millisecond)
	cancel()

	mu.Lock()
	defer mu.Unlock()
	// 首轮立即 + 30ms tick → 120ms 内应至少 2 次
	if count < 2 {
		t.Fatalf("expected >= 2 executions, got %d", count)
	}
}

func TestSchedulerSkipsEmptyAndError(t *testing.T) {
	var mu sync.Mutex
	reports := 0
	sched := NewScheduler(func(task *Task, _ []MetricPoint) {
		mu.Lock()
		reports++
		mu.Unlock()
	})
	sched.Register(&Task{
		ID: "empty", Type: TaskMonitor, Interval: time.Hour,
		Run: func(ctx context.Context) ([]MetricPoint, error) { return nil, nil },
	})
	sched.Register(&Task{
		ID: "err", Type: TaskMonitor, Interval: time.Hour,
		Run: func(ctx context.Context) ([]MetricPoint, error) { return nil, errors.New("boom") },
	})

	ctx, cancel := context.WithCancel(context.Background())
	sched.Start(ctx)
	defer cancel()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if reports != 0 {
		t.Fatalf("expected 0 reports (empty/error skipped), got %d", reports)
	}
}

func TestCollectSystemMetrics(t *testing.T) {
	pts := CollectSystemMetrics()
	seen := map[string]bool{}
	for _, p := range pts {
		if seen[p.Name] {
			t.Fatalf("duplicate metric name %s", p.Name)
		}
		seen[p.Name] = true
	}
	// 平台差异: disk.* 依赖 syscall.Statfs(跨平台可用); cpu/net 仅 Linux。
	if pts == nil {
		t.Fatal("expected non-nil points")
	}
}

func TestReadDiskUsage(t *testing.T) {
	total, used, percent, ok := readDiskUsage("/")
	if !ok || total <= 0 {
		t.Fatalf("disk usage unavailable: total=%v ok=%v", total, ok)
	}
	if used < 0 || percent < 0 || percent > 100 {
		t.Fatalf("invalid disk values: used=%v percent=%v", used, percent)
	}
}

func TestSendMetricsNotConnected(t *testing.T) {
	c := NewGRPCAgentClient("127.0.0.1:1", "agent-x")
	err := c.SendMetrics("t1", TaskMonitor, time.Now(), 5, []MetricPoint{{Name: "x", Value: 1}})
	if err == nil {
		t.Fatal("expected error when gRPC not connected (monitor QoS: drop)")
	}
}

func TestSchedulerAddOrUpdateAndRemove(t *testing.T) {
	var mu sync.Mutex
	count := 0
	sched := NewScheduler(func(task *Task, _ []MetricPoint) {
		mu.Lock()
		count++
		mu.Unlock()
	})
	sched.Register(&Task{
		ID: "custom.1", Type: TaskMetric, Interval: 30 * time.Millisecond,
		Run: func(ctx context.Context) ([]MetricPoint, error) {
			return []MetricPoint{{Name: "x", Value: 1}}, nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	sched.Start(ctx)
	time.Sleep(70 * time.Millisecond) // 首轮立即 + ~2 tick

	// 运行中更新: 更换 Run 后旧循环被取消, 新循环立即执行
	sched.AddOrUpdate(&Task{
		ID: "custom.1", Type: TaskMetric, Interval: time.Hour,
		Run: func(ctx context.Context) ([]MetricPoint, error) {
			return []MetricPoint{{Name: "y", Value: 2}}, nil
		},
	})

	// 移除: 任务停止(允许 1 次在途 execute 的异步 report 落地)
	sched.Remove("custom.1")
	before := func() int { mu.Lock(); defer mu.Unlock(); return count }()
	time.Sleep(200 * time.Millisecond)
	after := func() int { mu.Lock(); defer mu.Unlock(); return count }()

	if after-before > 1 {
		t.Fatalf("task not stopped after Remove: before=%d after=%d", before, after)
	}
	cancel()
}
