package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// MetricPoint Agent 本地采集到的单个指标点(内部表示)。
type MetricPoint struct {
	Name      string
	Value     float64
	Unit      string
	Labels    map[string]string
	Timestamp time.Time
}

// TaskType 任务类型: cmdb(低频→可缓存) | monitor(高频→即时)。
type TaskType string

const (
	TaskCMDB    TaskType = "cmdb"
	TaskMonitor TaskType = "monitor"
)

// Task 一个本地调度任务(内置或自定义)。"任务归任务": Agent 拿任务定义后本地自主调度执行。
type Task struct {
	ID       string
	Name     string
	Type     TaskType
	Interval time.Duration
	// Run 执行采集,返回指标点(空 = 本轮无数据)。错误由调用方记录。
	Run func(ctx context.Context) ([]MetricPoint, error)
}

// Scheduler 本地任务调度器: 每个任务独立 goroutine 按 interval 触发。
// QoS 语义(§4.7.1): monitor 即时上报(断连丢弃), cmdb 可缓存——由 report 回调实现。
type Scheduler struct {
	tasks  []*Task
	report func(task *Task, points []MetricPoint)
	mu     sync.Mutex
	stopCh chan struct{}
}

// NewScheduler 创建调度器。report 在每轮采集后回调(由 Agent 注入上报通道)。
func NewScheduler(report func(task *Task, points []MetricPoint)) *Scheduler {
	return &Scheduler{
		report: report,
		stopCh: make(chan struct{}),
	}
}

// Register 注册任务。调度器启动后调用无效(请先 Register 再 Start)。
func (s *Scheduler) Register(t *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, t)
}

// Start 启动所有任务的调度循环。ctx 取消时退出。
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	tasks := make([]*Task, len(s.tasks))
	copy(tasks, s.tasks)
	s.mu.Unlock()

	for _, t := range tasks {
		t := t
		go s.runLoop(ctx, t)
		slog.Info("agent: scheduled task started",
			"task", t.ID, "type", t.Type, "interval", t.Interval.String())
	}
}

func (s *Scheduler) runLoop(ctx context.Context, t *Task) {
	// 首轮立即执行(装机即有数据), 之后按 interval 触发。
	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()

	for {
		s.execute(ctx, t)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// execute 执行一轮任务采集并上报。
func (s *Scheduler) execute(ctx context.Context, t *Task) {
	pts, err := t.Run(ctx)
	if err != nil {
		slog.Warn("agent: task run failed", "task", t.ID, "err", err)
		return
	}
	if len(pts) == 0 {
		return
	}
	if s.report != nil {
		s.report(t, pts)
	}
}
