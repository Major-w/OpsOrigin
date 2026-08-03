package cmdb

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// Scheduler 是 CMDB 统一采集调度器：进程内 ticker 扫描到期且启用的采集任务，
// 按 (kind × resourceType) 查注册表执行采集，结果按 kind 路由落库。
type Scheduler struct {
	svc     *Service
	check   time.Duration
	mu      sync.Mutex
	running map[string]bool
	stop    chan struct{}
	wg      sync.WaitGroup
}

// NewScheduler 构造调度器。check 为扫描间隔，<=0 时默认 30s。
func NewScheduler(svc *Service, check time.Duration) *Scheduler {
	if check <= 0 {
		check = 30 * time.Second
	}
	return &Scheduler{svc: svc, check: check, running: make(map[string]bool)}
}

// Start 启动调度循环，直到 ctx 取消或 Stop 调用。
func (s *Scheduler) Start(ctx context.Context) {
	s.stop = make(chan struct{})
	ticker := time.NewTicker(s.check)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-ticker.C:
				s.scan(ctx)
			}
		}
	}()
}

// Stop 停止调度并等待在途任务结束。
func (s *Scheduler) Stop() {
	if s.stop != nil {
		select {
		case <-s.stop:
		default:
			close(s.stop)
		}
	}
	s.wg.Wait()
}

// scan 扫描到期任务并并发触发（同一任务在途时不重复触发）。
func (s *Scheduler) scan(ctx context.Context) {
	tasks, err := s.svc.listAllEnabledTasks(ctx)
	if err != nil {
		slog.Error("scheduler: list enabled tasks failed", "err", err)
		return
	}
	now := time.Now()
	for _, t := range tasks {
		interval := parseFrequency(t.Frequency)
		if !t.IsDue(now, interval) {
			continue
		}
		s.mu.Lock()
		if s.running[t.ID] {
			s.mu.Unlock()
			continue
		}
		s.running[t.ID] = true
		s.mu.Unlock()

		s.wg.Add(1)
		go func(t *model.CollectionTask) {
			defer s.wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.running, t.ID)
				s.mu.Unlock()
			}()
			s.runOne(ctx, t, interval)
		}(t)
	}
}

// runOne 执行单条任务：查采集器 → 采集 → 路由落库。
func (s *Scheduler) runOne(ctx context.Context, t *model.CollectionTask, interval time.Duration) {
	// 方案 X：channel=agent 的任务（主机 / 容器经 Agent 通道）由 Agent 本地采集并 push 上报，
	// server 侧不主动拉取，也不应刷成 pending（那会误报"采集器未配置"）。
	// 标记为 delegated —— 调度权仍在 CMDB，但采集动作 delegated 给 Agent（由 agentmgr 投影下发）。
	if t.Channel == "agent" {
		s.svc.markTaskDelegated(ctx, t)
		return
	}
	if _, _, _, err := s.svc.runCollectionTaskResolved(ctx, t, t.TenantID); err != nil {
		s.svc.markTaskFailed(ctx, t, err.Error())
		return
	}
	s.svc.markTaskSuccess(ctx, t)
}

// parseFrequency 把频率字符串解析为 time.Duration。
// 支持 Go duration 字符串（"15m"/"1h"）；非法或缺失回退 1h。
func parseFrequency(s string) time.Duration {
	if s == "" {
		return time.Hour
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	switch s {
	case "daily":
		return 24 * time.Hour
	case "hourly":
		return time.Hour
	}
	return time.Hour
}
