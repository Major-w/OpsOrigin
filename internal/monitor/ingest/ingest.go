// Package ingest 承载监控/CMDB 指标上报的接收缓冲与存储分流。
//
// 设计(对应需求文档 §4.3/§4.7):
//   - Buffer: 有界内存队列 + 单 worker, 攒批(条数/时间窗口)后批量交给 Sink;
//   - 背压: 队列满时丢弃并计数(dropped), 保证 gRPC 读循环永不阻塞(监控可丢点, 保时效);
//   - Sink 按 TaskType 分流: cmdb(低频)→PG, monitor(高频)→VM(阶段 2 接入)。
package ingest

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opsorigin/core/internal/tunnel"
)

// Batch 一次可落库的指标批次(内部表示)。
type Batch struct {
	TenantID    string
	AgentID     string
	TaskID      string
	TaskType    string // cmdb | monitor | metric
	Timestamp   time.Time
	IntervalSec int64
	Points      []tunnel.MetricPoint
}

// Sink 指标批次的存储目标。实现: MemorySink(演示/测试)、PGSink(阶段 1)、VMSink(阶段 2)。
type Sink interface {
	Write(ctx context.Context, b Batch) error
}

// Stats 缓冲运行统计(供监控打点/健康检查)。
type Stats struct {
	Received   int64 // 收到批次总数
	Points     int64 // 收到指标点总数
	Dropped    int64 // 因背压丢弃的批次
	Flushed    int64 // 已成功落库批次
	FlushError int64 // 落库失败批次
}

// Buffer 批量缓冲: 有界队列 + worker 攒批 + 背压丢弃。
type Buffer struct {
	ch            chan Batch
	sink          Sink
	batchSize     int           // 攒够 N 批即 flush(0 = 仅按时间窗口)
	flushInterval time.Duration // 时间窗口(默认 2s)
	stopped       atomic.Bool

	stats Stats
	mu    sync.Mutex // 保护 flush 时攒批切片
}

// Option 配置 Buffer。
type Option func(*Buffer)

// WithBatchSize 设置按条数攒批阈值。
func WithBatchSize(n int) Option { return func(b *Buffer) { b.batchSize = n } }

// WithFlushInterval 设置 flush 时间窗口。
func WithFlushInterval(d time.Duration) Option { return func(b *Buffer) { b.flushInterval = d } }

// NewBuffer 创建批量缓冲并绑定 sink。
func NewBuffer(sink Sink, opts ...Option) *Buffer {
	b := &Buffer{
		ch:            make(chan Batch, 4096), // 有界队列: 背压上限
		sink:          sink,
		flushInterval: 2 * time.Second,
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Start 启动后台 worker(攒批 + flush)。ctx 取消时退出并做一次最终 flush。
func (b *Buffer) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(b.flushInterval)
		defer ticker.Stop()
		pending := make([]Batch, 0, 64)

		flush := func() {
			if len(pending) == 0 {
				return
			}
			// 逐批写 sink(阶段 2 可改为合并成单次批量写 VM)
			ok := true
			for _, bch := range pending {
				if err := b.sink.Write(ctx, bch); err != nil {
					slog.Warn("ingest: sink write failed", "task_id", bch.TaskID, "err", err)
					atomic.AddInt64(&b.stats.FlushError, 1)
					ok = false
				} else {
					atomic.AddInt64(&b.stats.Flushed, 1)
				}
			}
			if ok {
				slog.Debug("ingest: flushed batch", "count", len(pending))
			}
			pending = pending[:0]
		}

		for {
			select {
			case <-ctx.Done():
				flush()
				b.stopped.Store(true)
				return
			case bch := <-b.ch:
				pending = append(pending, bch)
				if b.batchSize > 0 && len(pending) >= b.batchSize {
					flush()
				}
			case <-ticker.C:
				flush()
			}
		}
	}()
}

// Submit 非阻塞入队一次上报。队列满时丢弃并计数(背压策略: 监控可丢点)。
// 永不阻塞调用方(gRPC 读循环/回调)。
func (b *Buffer) Submit(report tunnel.MetricsReport) {
	atomic.AddInt64(&b.stats.Received, 1)
	atomic.AddInt64(&b.stats.Points, int64(len(report.Points)))
	select {
	case b.ch <- Batch{
		TenantID:    "default",
		AgentID:     report.AgentID,
		TaskID:      report.TaskID,
		TaskType:    report.TaskType,
		Timestamp:   report.Timestamp,
		IntervalSec: report.IntervalSec,
		Points:      report.Points,
	}:
	default:
		atomic.AddInt64(&b.stats.Dropped, 1)
		slog.Warn("ingest: buffer full, dropping batch", "agent", report.AgentID, "task", report.TaskID)
	}
}

// Stats 返回运行统计快照。
func (b *Buffer) Stats() Stats {
	return Stats{
		Received:   atomic.LoadInt64(&b.stats.Received),
		Points:     atomic.LoadInt64(&b.stats.Points),
		Dropped:    atomic.LoadInt64(&b.stats.Dropped),
		Flushed:    atomic.LoadInt64(&b.stats.Flushed),
		FlushError: atomic.LoadInt64(&b.stats.FlushError),
	}
}

// MemorySink 内存 Sink: 记录累计统计 + 保留最近批次(测试/演示用)。
type MemorySink struct {
	mu     sync.Mutex
	total  int64
	points int64
	last   *Batch
}

// NewMemorySink 创建内存 Sink。
func NewMemorySink() *MemorySink { return &MemorySink{} }

// Write 实现 Sink。
func (m *MemorySink) Write(_ context.Context, b Batch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.total++
	m.points += int64(len(b.Points))
	m.last = &b
	return nil
}

// Total 返回已写入批次。
func (m *MemorySink) Total() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total
}

// Last 返回最近一次写入的批次(可能为 nil)。
func (m *MemorySink) Last() *Batch {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}
