// Package ingest 提供异步批量落库能力: 提交方(如 CMDB 批量导入)无需阻塞等待
// 数据库写入, 只需把载荷 Submit 进缓冲通道; 后台 goroutine 周期性(或缓冲满时)
// 把一批 Item 交给 flushFn 落库。
//
// 设计目标(对应 F4 决策 B):
//   - 缓冲通道容量 1000, 默认 500ms 冲刷一次, 把"逐条同步写"变成"批量异步写",
//     显著降低高并发导入对 PG 的冲击;
//   - Submit 非阻塞: 缓冲满时立即返回 false 并计入 ingest_dropped_total, 绝不阻塞调用方;
//   - 暴露 ingest_buffer_size / ingest_dropped_total 两个指标供观测。
package ingest

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/opsorigin/core/internal/monitor/metrics"
)

// Item 是异步入口提交的任意待落库载荷。
// 调用方在 flushFn 中做类型断言并分发到具体存储(如 pg.BulkCreateCI 或内存回退)。
type Item interface{}

var (
	bufferSizeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ingest_buffer_size",
		Help: "Current number of items buffered in the async ingest channel",
	})
	droppedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ingest_dropped_total",
		Help: "Total number of items dropped because the ingest buffer was full",
	})
)

func init() {
	metrics.Registry.MustRegister(bufferSizeGauge, droppedTotal)
}

// Ingestor 异步批量落库器。
type Ingestor struct {
	ch           chan Item
	flushFn      func(ctx context.Context, items []Item)
	flushInterval time.Duration

	mu      sync.Mutex
	stopped bool
	stopCh  chan struct{}
}

// New 构造一个 Ingestor。
//   - bufferSize: 缓冲通道容量(满则 Submit 返回 false);
//   - flushInterval: 最大批处理延迟(到点即使未满也冲刷一次)。
func New(bufferSize int, flushInterval time.Duration, flushFn func(ctx context.Context, items []Item)) *Ingestor {
	if bufferSize < 1 {
		bufferSize = 1
	}
	if flushInterval <= 0 {
		flushInterval = 500 * time.Millisecond
	}
	return &Ingestor{
		ch:            make(chan Item, bufferSize),
		flushFn:       flushFn,
		flushInterval:  flushInterval,
		stopCh:        make(chan struct{}),
	}
}

// Submit 提交一个 Item。缓冲满时立即返回 false(计入 dropped 指标), 不阻塞调用方。
func (in *Ingestor) Submit(item Item) bool {
	select {
	case in.ch <- item:
		bufferSizeGauge.Set(float64(len(in.ch)))
		return true
	default:
		droppedTotal.Inc()
		return false
	}
}

// Start 启动后台冲刷循环, 直到 ctx 取消或 Stop 被调用。
func (in *Ingestor) Start(ctx context.Context) {
	ticker := time.NewTicker(in.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			in.drain(ctx)
			return
		case <-in.stopCh:
			in.drain(ctx)
			return
		case <-ticker.C:
			in.drain(ctx)
		}
	}
}

// drain 取出当前缓冲的所有 Item 并交给 flushFn 同步落库。
func (in *Ingestor) drain(ctx context.Context) {
	batch := make([]Item, 0, len(in.ch))
drainLoop:
	for {
		select {
		case it := <-in.ch:
			batch = append(batch, it)
		default:
			break drainLoop
		}
	}
	bufferSizeGauge.Set(float64(len(in.ch)))
	if len(batch) == 0 {
		return
	}
	in.flushFn(ctx, batch)
}

// Stop 优雅停止: 排空剩余并退出。
func (in *Ingestor) Stop() {
	in.mu.Lock()
	if in.stopped {
		in.mu.Unlock()
		return
	}
	in.stopped = true
	in.mu.Unlock()
	close(in.stopCh)
}
