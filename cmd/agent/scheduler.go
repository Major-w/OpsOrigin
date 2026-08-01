package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// MetricPoint Agent 本地采集到的单个指标点(内部表示)。
type MetricPoint struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Unit      string            `json:"unit,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"-"`
}

// TaskType 任务类型: cmdb(低频→可缓存) | monitor(高频→即时) | metric(自定义指标)。
type TaskType string

const (
	TaskCMDB    TaskType = "cmdb"
	TaskMonitor TaskType = "monitor"
	TaskMetric  TaskType = "metric"
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

// Scheduler 本地任务调度器: 每任务独立 goroutine 按 interval 触发。
// 支持动态注册(AddOrUpdate/Remove)——Server 下发任务清单时增量同步(阶段 3b)。
// QoS 语义(§4.7.1): monitor 即时上报(断连丢弃), cmdb 可缓存——由 report 回调实现。
type Scheduler struct {
	report  func(task *Task, points []MetricPoint)
	mu      sync.Mutex
	tasks   map[string]*Task
	cancels map[string]context.CancelFunc
	ctx     context.Context
	running bool
}

// NewScheduler 创建调度器。report 在每轮采集后回调(由 Agent 注入上报通道)。
func NewScheduler(report func(task *Task, points []MetricPoint)) *Scheduler {
	return &Scheduler{
		report:  report,
		tasks:   make(map[string]*Task),
		cancels: make(map[string]context.CancelFunc),
	}
}

// Register 注册任务(Start 前调用; 运行中请用 AddOrUpdate)。
func (s *Scheduler) Register(t *Task) {
	s.AddOrUpdate(t)
}

// AddOrUpdate 注册或更新任务。运行中更新同 ID 任务会重启其调度循环(新 interval/run 生效)。
func (s *Scheduler) AddOrUpdate(t *Task) {
	var childCtx context.Context
	s.mu.Lock()
	if cancel, ok := s.cancels[t.ID]; ok {
		cancel() // 停止旧循环
	}
	s.tasks[t.ID] = t
	running := s.running
	ctx := s.ctx
	var cancel context.CancelFunc
	if running {
		childCtx, cancel = context.WithCancel(ctx)
		s.cancels[t.ID] = cancel
	}
	s.mu.Unlock()

	if running && cancel != nil {
		t := t
		go s.runLoop(childCtx, t)
		slog.Info("agent: task updated", "task", t.ID, "type", t.Type, "interval", t.Interval.String())
	}
}

// Remove 移除任务并停止其调度循环。
func (s *Scheduler) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.cancels[id]; ok {
		cancel()
		delete(s.cancels, id)
	}
	delete(s.tasks, id)
}

// TaskIDs 返回当前任务 ID 列表(供调试/测试)。
func (s *Scheduler) TaskIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.tasks))
	for id := range s.tasks {
		ids = append(ids, id)
	}
	return ids
}

// Start 启动所有任务的调度循环。ctx 取消时全部退出。
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.running = true
	for id, t := range s.tasks {
		childCtx, c := context.WithCancel(ctx)
		s.cancels[id] = c
		t := t
		go s.runLoop(childCtx, t)
		slog.Info("agent: scheduled task started",
			"task", t.ID, "type", t.Type, "interval", t.Interval.String())
	}
	s.mu.Unlock()
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
