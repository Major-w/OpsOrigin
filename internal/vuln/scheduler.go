package vuln

import (
	"context"
	"log/slog"
	"time"
)

// schedulerTick 调度检查间隔。
const schedulerTick = time.Minute

// scanRunTimeout 单个计划扫描的执行上限(超时按失败处理,避免长期占用调度循环)。
const scanRunTimeout = 30 * time.Minute

// StartScheduler 启动计划扫描调度器:定期检查到期的 daily/weekly 扫描并按原参数重跑。
// 计划任务复用同一条 Scan 记录(只刷新统计与执行时间),使任务列表不因每天执行而膨胀。
func (s *Service) StartScheduler(ctx context.Context) {
	go func() {
		// 启动后延迟首轮,避开启动期 DDL 自愈与依赖探测的资源争抢。
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		s.runDueScans(ctx)

		ticker := time.NewTicker(schedulerTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runDueScans(ctx)
			}
		}
	}()
}

// runDueScans 执行一轮到期检查(串行执行,避免同时向 Agent 通道压入大量扫描)。
func (s *Service) runDueScans(ctx context.Context) {
	now := time.Now()
	due, err := s.store.ListDueScans(ctx, now)
	if err != nil {
		slog.Error("vuln: list due scans failed", "error", err)
		return
	}
	for _, scan := range due {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// 先推进下次执行时间再执行,避免长扫描期间被重复触发。
		scan.NextRunAt = nextRunAt(scan.Schedule, now)
		scan.LastRunAt = &now
		if err := s.store.UpdateScan(ctx, scan); err != nil {
			slog.Error("vuln: advance schedule failed", "scan", scan.ID, "error", err)
			continue
		}
		req := decodeOptions(scan.Options)
		slog.Info("vuln: running scheduled scan", "scan", scan.ID, "name", scan.Name, "schedule", scan.Schedule)
		runCtx, cancel := context.WithTimeout(ctx, scanRunTimeout)
		err := s.execute(runCtx, scan, req)
		cancel()
		if err != nil {
			slog.Warn("vuln: scheduled scan failed", "scan", scan.ID, "detail", err)
		}
	}
}
