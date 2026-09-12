package process

import (
	"context"
	"log/slog"
	"time"
)

// StartTimerChecker 启动定时器步骤调度器: 周期性扫描 running 流程实例,
// 对当前步骤为 timer 且已到期的实例执行 Advance(success) 自动推进。
// 与 ITSM 的 SLA checker 同模式(进程内 ticker), 重启后未到期实例由下次扫描接续。
func (s *Service) StartTimerChecker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		slog.Info("itsm process: timer checker started")
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.checkTimers(ctx)
			}
		}
	}()
}

// checkTimers 扫描所有 running 实例, 推进到期的定时器步骤。
func (s *Service) checkTimers(ctx context.Context) {
	insts, err := s.store.ListRunningInstances(ctx)
	if err != nil {
		slog.Error("itsm process: list running instances failed", "error", err)
		return
	}
	now := time.Now()
	for _, inst := range insts {
		if inst.Status != "running" || inst.CurrentStep == "" {
			continue
		}
		proc, err := s.store.Get(ctx, inst.ProcessID, inst.TenantID)
		if err != nil || proc == nil {
			continue
		}
		step := proc.StepByID(inst.CurrentStep)
		if step == nil || step.Type != StepTimer {
			continue
		}
		due := timerDueAt(inst, step, now)
		if due == nil || now.Before(*due) {
			continue
		}
		// OLA 节点时限: 步骤配置了 OlaEscalateTo 时, 超时先发升级通知(未推进前), 再自动推进。
		if step.OlaEscalateTo != "" && s.notifier != nil {
			s.notifier(ctx, inst.TenantID, "workflow.ola_escalated",
				"流程节点超时: "+step.Name,
				"流程 "+proc.Name+" 节点 ["+step.Name+"] 已超时(时限 "+step.Duration+"), 升级至: "+step.OlaEscalateTo)
			slog.Info("itsm process: OLA escalated", "ticket_id", inst.TicketID, "step", step.ID, "to", step.OlaEscalateTo)
		}
		if _, _, err := s.Advance(ctx, inst.TenantID, inst.TicketID, "success", nil); err != nil {
			slog.Warn("itsm process: timer advance failed", "ticket_id", inst.TicketID, "step", step.ID, "error", err)
		} else {
			slog.Info("itsm process: timer step fired", "ticket_id", inst.TicketID, "step", step.ID)
		}
	}
}

// timerDueAt 计算定时器步骤的到期时间: StepResults[stepID].started_at + duration。
// started_at 缺失或 duration 不可解析返回 nil(不触发)。
func timerDueAt(inst *ProcessInstance, step *Step, now time.Time) *time.Time {
	rec, ok := inst.StepResults[step.ID].(map[string]any)
	if !ok {
		return nil
	}
	startStr, ok := rec["started_at"].(string)
	if !ok {
		return nil
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		return nil
	}
	d, ok := parseStepDuration(step.Duration)
	if !ok {
		return nil
	}
	due := start.Add(d)
	return &due
}

// recordStepEntered 记录流程实例"进入某步骤"的时间(RFC3339, 幂等不覆盖)。
// 人工任务与定时器都是"停留型"步骤——OLA 与定时器到期判定都以此作为计时起点,
// 与工单 UpdatedAt 无关(写评论/改字段不应重置环节耗时)。
func recordStepEntered(inst *ProcessInstance, step *Step, now time.Time) {
	if inst.StepResults == nil {
		inst.StepResults = map[string]any{}
	}
	rec, _ := inst.StepResults[step.ID].(map[string]any)
	if rec == nil {
		rec = map[string]any{}
	}
	if _, ok := rec["started_at"]; !ok {
		rec["started_at"] = now.Format(time.RFC3339)
	}
	inst.StepResults[step.ID] = rec
}

// recordTimerStarted 是 recordStepEntered 的历史别名，保留以免打断既有调用方。
func recordTimerStarted(inst *ProcessInstance, step *Step, now time.Time) {
	recordStepEntered(inst, step, now)
}

// StepEnteredAt 返回实例停留在当前步骤的起始时间(供 OLA 环节耗时计算)。
// 取不到(步骤未记录/非停留步骤)时返回 nil，由调用方决定兜底基准。
func StepEnteredAt(inst *ProcessInstance) *time.Time {
	if inst == nil || inst.StepResults == nil || inst.CurrentStep == "" {
		return nil
	}
	rec, ok := inst.StepResults[inst.CurrentStep].(map[string]any)
	if !ok {
		return nil
	}
	switch v := rec["started_at"].(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return &t
		}
	case time.Time:
		return &v
	}
	return nil
}
