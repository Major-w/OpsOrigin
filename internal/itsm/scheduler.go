package itsm

import (
	"context"
	"log/slog"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
)

// StartScheduleRunner 启动定时触发器调度循环: 每分钟对齐扫描 Event="schedule" 的
// 启用规则（对标 SN Schedule 触发器），命中后:
//   - Conditions 非空: 扫描本租户全部工单，对 ruleMatches 命中的工单逐单执行动作
//     （典型场景: 每天 9 点对所有 open 高风险工单拉起变更流程/发送催办）;
//   - Conditions 为空: 仅执行 notify 动作（纯定时通知，无工单上下文，
//     start_process/assign/webhook 等依赖工单的动作记 warn 跳过）。
//
// 分钟去重靠内存 lastFired（同分钟只触发一次）；重启后同分钟内可能重触发一次，
// 动作侧已有幂等护栏（start_process 一单一实例、assign 已是目标即跳过），可接受。
func (s *Service) StartScheduleRunner(ctx context.Context) {
	go func() {
		// 对齐到下一分钟边界, 保证 cron 分钟语义不被 30s ticker 漂移破坏
		now := time.Now()
		time.Sleep(time.Until(now.Truncate(time.Minute).Add(time.Minute)))
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		slog.Info("itsm: schedule runner started")
		lastFired := map[string]string{} // ruleID -> "2006-01-02 15:04"
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case t := <-ticker.C:
				s.fireScheduleRules(ctx, t, lastFired)
			}
		}
	}()
}

func (s *Service) fireScheduleRules(ctx context.Context, now time.Time, lastFired map[string]string) {
	tenants, err := s.store.ListTenantIDs(ctx)
	if err != nil || len(tenants) == 0 {
		tenants = []string{"default"}
	}
	minKey := now.Format("2006-01-02 15:04")
	for _, tenantID := range tenants {
		rules, err := s.store.ListAutomationRules(ctx, tenantID)
		if err != nil {
			slog.Warn("itsm: schedule rules list failed", "tenant", tenantID, "error", err)
			continue
		}
		for _, rule := range rules {
			if !rule.Enabled || rule.Event != "schedule" || !rule.Schedule.Valid() {
				continue
			}
			if lastFired[rule.ID] == minKey {
				continue
			}
			if !s.scheduleDue(rule.Schedule, now) {
				continue
			}
			lastFired[rule.ID] = minKey
			s.fireScheduleRule(ctx, rule)
		}
	}
}

// scheduleDue 判定当前分钟是否命中调度: cron 按本地时间匹配；间隔模式按
// 「自 Unix 纪元起的整分钟数 % interval == 0」对齐（服务重启不重置相位）。
func (s *Service) scheduleDue(sc *model.RuleSchedule, now time.Time) bool {
	if sc.Cron != "" {
		expr, err := model.ParseCron(sc.Cron)
		if err != nil {
			return false
		}
		return expr.Matches(now)
	}
	if sc.IntervalMinutes > 0 {
		return int(now.Unix()/60)%sc.IntervalMinutes == 0
	}
	return false
}

func (s *Service) fireScheduleRule(ctx context.Context, rule *model.AutomationRule) {
	if len(rule.Conditions) == 0 {
		s.executeScheduleRuleActions(ctx, rule, nil)
		slog.Info("itsm: schedule rule fired (ticketless)", "rule", rule.Name, "rule_id", rule.ID)
		return
	}
	tickets, err := s.store.List(ctx, rule.TenantID, "")
	if err != nil {
		slog.Warn("itsm: schedule rule ticket scan failed", "rule", rule.Name, "error", err)
		return
	}
	matched := 0
	for _, t := range tickets {
		if !ruleMatches(rule, t) {
			continue
		}
		matched++
		r := *rule
		tk := *t
		s.executeScheduleRuleActions(ctx, &r, &tk)
	}
	slog.Info("itsm: schedule rule fired", "rule", rule.Name, "rule_id", rule.ID, "matched", matched)
}

// executeScheduleRuleActions 同步执行定时规则动作(独立于事件路径, 支持无工单上下文)。
func (s *Service) executeScheduleRuleActions(ctx context.Context, rule *model.AutomationRule, ticket *model.Ticket) {
	for _, act := range rule.Actions {
		if err := s.executeScheduleAction(ctx, rule, ticket, act); err != nil {
			slog.Warn("itsm: schedule action failed", "rule", rule.Name, "action", act.Type, "error", err)
		}
	}
}

func (s *Service) executeScheduleAction(ctx context.Context, rule *model.AutomationRule, ticket *model.Ticket, act model.RuleAction) error {
	if ticket == nil {
		// 无工单上下文: 仅 notify 有意义(模板变量渲染为空串)。
		if act.Type == "notify" {
			if s.notify == nil {
				return nil
			}
			content := renderTemplate(act.Message, nil)
			if content == "" {
				content = "定时触发: " + rule.Name
			}
			return s.notify.NotifyEvent(ctx, rule.TenantID, "ticket.automation", "定时触发: "+rule.Name, content)
		}
		slog.Warn("itsm: schedule action requires ticket context, skipped", "rule", rule.Name, "action", act.Type)
		return nil
	}
	// 有工单上下文: 复用事件触发器的动作执行器(notify/assign/webhook/start_process 全量可用)。
	return s.executeRuleAction(ctx, rule, ticket, act, "schedule:"+rule.Name)
}
