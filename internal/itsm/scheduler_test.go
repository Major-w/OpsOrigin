package itsm

import (
	"context"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/store"
)

// --- cron 解析与匹配 ---

func TestParseCronMatches(t *testing.T) {
	cases := []struct {
		expr  string
		time  string // "2006-01-02 15:04" 本地时间
		match bool
	}{
		{"0 9 * * 1-5", "2026-11-02 09:00", true},  // 周一 9 点
		{"0 9 * * 1-5", "2026-11-01 09:00", false}, // 周日
		{"0 9 * * 1-5", "2026-11-02 10:00", false},
		{"*/15 * * * *", "2026-11-02 09:30", true},
		{"*/15 * * * *", "2026-11-02 09:31", false},
		{"30 2 1 * *", "2026-11-01 02:30", true}, // 每月 1 号
		{"30 2 1 * *", "2026-11-02 02:30", false},
		{"0 8 * * 0", "2026-11-01 08:00", true},   // 0=周日
		{"0 8 * * 7", "2026-11-01 08:00", true},   // 7 也=周日
		{"0 9 * * *", "2026-11-02 09:00", true},   // 全 * 日/周
		{"5 4 * 11 1", "2026-11-02 04:05", true},  // 月+周同时受限 OR
		{"5 4 * 11 1", "2026-12-01 04:05", false}, // 月份不符
		{"0 9 1 11 1", "2026-11-02 09:00", true},  // 1 号且周一(2026-11-02 恰为周一) OR 命中
	}
	for _, c := range cases {
		expr, err := model.ParseCron(c.expr)
		if err != nil {
			t.Fatalf("ParseCron(%q): %v", c.expr, err)
		}
		tm, _ := time.ParseInLocation("2006-01-02 15:04", c.time, time.Local)
		if got := expr.Matches(tm); got != c.match {
			t.Errorf("cron %q at %s: got %v want %v", c.expr, c.time, got, c.match)
		}
	}
}

func TestParseCronInvalid(t *testing.T) {
	for _, bad := range []string{"", "0 9 * *", "61 * * * *", "* 25 * * *", "*/0 * * * *", "a b c d e"} {
		if _, err := model.ParseCron(bad); err == nil {
			t.Errorf("ParseCron(%q) should fail", bad)
		}
	}
}

// --- 调度判定 ---

func TestScheduleDue(t *testing.T) {
	s := &Service{}
	tm, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-03 09:00", time.Local)
	if !s.scheduleDue(&model.RuleSchedule{Cron: "0 9 * * *"}, tm) {
		t.Error("cron 9:00 should be due at 09:00")
	}
	if s.scheduleDue(&model.RuleSchedule{Cron: "0 9 * * *"}, tm.Add(time.Minute)) {
		t.Error("cron 9:00 should not be due at 09:01")
	}
	// 间隔模式: 30 分钟间隔, unix 分钟数对齐
	if !s.scheduleDue(&model.RuleSchedule{IntervalMinutes: 30}, tm) {
		t.Error("interval 30 should be due at minute-aligned time")
	}
	if s.scheduleDue(&model.RuleSchedule{IntervalMinutes: 30}, tm.Add(time.Minute)) {
		t.Error("interval 30 should not be due at +1min")
	}
	// 非法配置一律不触发
	if s.scheduleDue(&model.RuleSchedule{Cron: "bad"}, tm) {
		t.Error("invalid cron should never be due")
	}
}

// --- 定时规则点火(端到端): 条件扫描匹配 + 动作执行 ---

func TestFireScheduleRuleWithConditions(t *testing.T) {
	st := store.NewMemoryStore()
	hi := &model.Ticket{ID: "t-hi", TenantID: "default", Title: "高风险", Status: model.StatusPendingReview, RiskLevel: model.RiskHigh}
	lo := &model.Ticket{ID: "t-lo", TenantID: "default", Title: "低风险", Status: model.StatusPendingReview, RiskLevel: model.RiskLow}
	_ = st.Create(context.Background(), hi)
	_ = st.Create(context.Background(), lo)
	_ = st.UpsertAutomationRule(context.Background(), "default", &model.AutomationRule{
		ID: "r1", TenantID: "default", Name: "定时巡检", Event: "schedule", Enabled: true,
		Conditions: []model.RuleCondition{{Field: "risk_level", Op: "equals", Value: "high"}},
		Actions:    []model.RuleAction{{Type: "notify", Message: "巡检 ${ticket.id}"}},
	})
	s := &Service{store: st}
	s.fireScheduleRule(context.Background(), &model.AutomationRule{
		ID: "r1", TenantID: "default", Name: "定时巡检", Event: "schedule", Enabled: true,
		Conditions: []model.RuleCondition{{Field: "risk_level", Op: "equals", Value: "high"}},
		Actions:    []model.RuleAction{{Type: "unknown_action"}}, // 未接通知中心, 用 unknown 动作验证调用路径(仅记日志)
	})
	// 无 panic 且条件过滤正确性由 ruleMatches 既有单测保证; 此处验证扫描路径可跑通。
}

// 无条件定时规则走 ticketless 路径(仅 notify), 不扫描工单。
func TestFireScheduleRuleTicketless(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.Create(context.Background(), &model.Ticket{ID: "t1", TenantID: "default", Status: model.StatusPendingReview})
	s := &Service{store: st}
	// notify 未接入 → 静默跳过; 关键是无工单上下文不 panic
	s.fireScheduleRule(context.Background(), &model.AutomationRule{
		ID: "r2", TenantID: "default", Name: "日报", Event: "schedule", Enabled: true,
		Actions: []model.RuleAction{{Type: "notify", Message: "日报 ${ticket.id}"}},
	})
}

// lookupTicketVar nil 防护: 定时规则模板渲染不 panic。
func TestRenderTemplateNilTicket(t *testing.T) {
	if got := renderTemplate("工单 ${ticket.id} ${custom_fields.x}", nil); got != "工单  " {
		t.Errorf("renderTemplate(nil) = %q", got)
	}
}
