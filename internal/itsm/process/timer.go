package process

import (
	"context"
	"log/slog"
	"strconv"
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

// checkTimers 扫描所有 running 实例, 推进到期的定时器步骤; 人工审批节点则执行
// 超时策略(周期催办 / 到期自动通过、自动驳回或催办通知)。
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
		if step == nil {
			continue
		}
		switch step.Type {
		case StepTimer:
			s.checkTimerStep(ctx, proc, inst, step, now)
		case StepUser:
			// 审批时间语义: 配置了时限或催办间隔的人工节点纳入扫描。
			if step.TimeoutDuration != "" || step.ReminderInterval != "" {
				s.checkUserStepTimeout(ctx, inst, step, now)
			}
		case StepWait:
			s.checkWaitStep(ctx, inst, step)
		}
	}
}

// checkTimerStep 处理定时器步骤到期推进(原 checkTimers 主体)。
func (s *Service) checkTimerStep(ctx context.Context, proc *Process, inst *ProcessInstance, step *Step, now time.Time) {
	due := timerDueAt(inst, step, now)
	if due == nil || now.Before(*due) {
		return
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
		s.syncAfterEngineAdvance(ctx, inst.TenantID, inst.TicketID)
	}
}

// checkUserStepTimeout 执行人工审批节点的时间语义:
//   - 周期催办: 每过 ReminderInterval 向审批人发一次催办(经 notifier, 计数幂等防重发);
//   - 超时处置: 停留超过 TimeoutDuration 后按 TimeoutAction 处置 ——
//     auto_approve 补齐名册投票为通过并回调 itsm 完成工单侧自动通过;
//     auto_reject 回调 itsm 完成工单侧自动驳回;
//     notify_only/空 仅发超时通知, 流程继续等待人工。
func (s *Service) checkUserStepTimeout(ctx context.Context, inst *ProcessInstance, step *Step, now time.Time) {
	started := StepEnteredAt(inst)
	if started == nil {
		return
	}
	if inst.StepResults == nil {
		inst.StepResults = map[string]any{}
	}
	rec, _ := inst.StepResults[step.ID].(map[string]any)
	if rec == nil {
		rec = map[string]any{}
	}

	// --- 周期催办(到期前后都提醒, 催的是"该表态了"; 配置日历时按业务时间累计) ---
	if step.ReminderInterval != "" {
		if d, ok := parseStepDuration(step.ReminderInterval); ok && d > 0 {
			elapsed := s.stepElapsed(ctx, inst, step, started, now)
			passed := int(elapsed / d)
			sent, _ := rec["reminders_sent"].(float64)
			if passed > int(sent) {
				if s.notifier != nil {
					s.notifier(ctx, inst.TenantID, "workflow.approval_reminder",
						"审批催办: "+step.Name,
						"工单 "+inst.TicketID+" 在流程节点 ["+step.Name+"] 已停留 "+
							formatDurationFriendly(now.Sub(*started))+" 待审批, 请及时处理")
				}
				rec["reminders_sent"] = float64(passed)
				rec["last_reminder_at"] = now.Format(time.RFC3339)
				slog.Info("itsm process: approval reminder sent", "ticket_id", inst.TicketID, "step", step.ID, "round", passed)
			}
		}
	}

	// --- 超时处置 ---
	if step.TimeoutDuration == "" {
		inst.StepResults[step.ID] = rec
		s.store.UpdateInstance(ctx, inst) // 催办计数落库(无催办配置时无变化, 幂等)
		return
	}
	td, ok := parseStepDuration(step.TimeoutDuration)
	if !ok {
		return
	}
	// 配置了工作日历: 时限按业务时间展开(如 8x5 日历下 24h ≈ 3 个工作日), 否则自然时间。
	if elapsed := s.stepElapsed(ctx, inst, step, started, now); elapsed < td {
		inst.StepResults[step.ID] = rec
		s.store.UpdateInstance(ctx, inst)
		return
	}
	if handled, _ := rec["timeout_handled"].(bool); handled {
		return // 幂等: 已处置过, 不重复触发
	}
	rec["timeout_handled"] = true
	rec["timeout_action"] = step.TimeoutAction
	rec["timed_out_at"] = now.Format(time.RFC3339)
	inst.StepResults[step.ID] = rec
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		slog.Warn("itsm process: persist timeout mark failed", "ticket_id", inst.TicketID, "step", step.ID, "error", err)
	}

	switch step.TimeoutAction {
	case "auto_approve":
		// 补齐静态名册内未表态审批人为"通过"(会签视角), 表决记录与人工审批一致可审计。
		s.fillApprovalVotes(inst, step, true)
		if err := s.store.UpdateInstance(ctx, inst); err != nil {
			slog.Warn("itsm process: persist auto-approve votes failed", "ticket_id", inst.TicketID, "step", step.ID, "error", err)
		}
		fallthrough
	case "auto_reject":
		if s.approvalTimeoutHandler == nil {
			slog.Warn("itsm process: approval timeout but no handler, skip", "ticket_id", inst.TicketID, "step", step.ID, "action", step.TimeoutAction)
			return
		}
		if err := s.approvalTimeoutHandler(ctx, inst.TenantID, inst.TicketID, step.ID, step.TimeoutAction); err != nil {
			slog.Warn("itsm process: approval timeout handler failed", "ticket_id", inst.TicketID, "step", step.ID, "action", step.TimeoutAction, "error", err)
			return
		}
		slog.Info("itsm process: approval timeout auto-handled", "ticket_id", inst.TicketID, "step", step.ID, "action", step.TimeoutAction)
	default:
		// notify_only / 未配置动作: 仅通知, 流程继续等待人工审批。
		if s.notifier != nil {
			s.notifier(ctx, inst.TenantID, "workflow.approval_timeout",
				"审批超时: "+step.Name,
				"工单 "+inst.TicketID+" 在流程节点 ["+step.Name+"] 已超过审批时限("+step.TimeoutDuration+"), 仍待人工处理")
		}
		slog.Info("itsm process: approval timeout notified", "ticket_id", inst.TicketID, "step", step.ID)
	}
}

// fillApprovalVotes 把静态审批人名册中尚未表态的成员补记为指定表决结果(auto_approve 用)。
func (s *Service) fillApprovalVotes(inst *ProcessInstance, step *Step, approved bool) {
	rec, _ := inst.StepResults[step.ID].(map[string]any)
	if rec == nil {
		rec = map[string]any{}
	}
	approvals, _ := rec["approvals"].(map[string]any)
	if approvals == nil {
		approvals = map[string]any{}
	}
	for _, a := range step.Approvers {
		if _, voted := approvals[a]; !voted {
			approvals[a] = approved
		}
	}
	rec["approvals"] = approvals
	rec["timeout_votes_filled"] = true
	inst.StepResults[step.ID] = rec
}

// formatDurationFriendly 把时长格式化为人类可读中文摘要(催办文案用)。
func formatDurationFriendly(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if h := int(d.Hours()); h >= 24 {
		return strconv.Itoa(h/24) + "天" + strconv.Itoa(h%24) + "小时"
	}
	if h := int(d.Hours()); h >= 1 {
		return strconv.Itoa(h) + "小时" + strconv.Itoa(int(d.Minutes())%60) + "分钟"
	}
	return strconv.Itoa(int(d.Minutes())) + "分钟"
}

// checkWaitStep 等待条件节点: 每轮扫描重评条件, 成立后沿 success 自动推进。
// 条件表达式复用网关的 EvaluateCondition, 变量上下文与自动节点一致(工单字段/表单字段/步骤输出)。
func (s *Service) checkWaitStep(ctx context.Context, inst *ProcessInstance, step *Step) {
	if step.Condition == "" {
		return // 空条件视为配置错误: 永不满足, 避免误推进
	}
	exprCtx := s.buildVarCtx(ctx, inst, nil)
	// 顶层平铺工单字段, 使 wait 条件与网关条件同语义: 网关侧 itsm.advanceProcess 把
	// status/risk_level/custom_fields 等作为 output 平铺, 这里对齐(已有键不覆盖)。
	if t, ok := exprCtx["ticket"].(map[string]any); ok {
		for k, v := range t {
			if _, exists := exprCtx[k]; !exists {
				exprCtx[k] = v
			}
		}
	}
	ok, err := EvaluateCondition(step.Condition, exprCtx)
	if err != nil {
		// 求值异常(变量缺失/表达式非法)不推进, 记录一次避免刷屏
		if inst.StepResults != nil {
			if rec, _ := inst.StepResults[step.ID].(map[string]any); rec != nil && rec["wait_eval_error"] != err.Error() {
				rec["wait_eval_error"] = err.Error()
				inst.StepResults[step.ID] = rec
				s.store.UpdateInstance(ctx, inst)
			}
		}
		return
	}
	if !ok {
		return
	}
	if _, _, err := s.Advance(ctx, inst.TenantID, inst.TicketID, "success", nil); err != nil {
		slog.Warn("itsm process: wait advance failed", "ticket_id", inst.TicketID, "step", step.ID, "error", err)
	} else {
		slog.Info("itsm process: wait condition met, advanced", "ticket_id", inst.TicketID, "step", step.ID, "condition", step.Condition)
		s.syncAfterEngineAdvance(ctx, inst.TenantID, inst.TicketID)
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
