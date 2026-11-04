package process

import (
	"context"
	"log/slog"
	"time"
)

// WorkCalendar 工作日历接口（P3-2 审批时间语义）。
// 由 main.go 装配注入 servicehours.Window 实现（BusinessElapsed 按服务窗口
// + 节假日计算业务耗时）；process 包不反向依赖 servicehours，同 sla/ola 范式。
type WorkCalendar interface {
	// BusinessElapsed 返回 [from, to] 区间内的业务时间累计（跳过非工作时段/节假日）。
	BusinessElapsed(from, to time.Time) time.Duration
}

// CalendarResolver 按名称/ID 解析租户日历；未命中返回 false（调用方回退自然时间）。
type CalendarResolver func(ctx context.Context, tenantID, nameOrID string) (WorkCalendar, bool)

// SetCalendarResolver 注入工作日历解析器（main.go 装配 servicehours）。
func (s *Service) SetCalendarResolver(r CalendarResolver) { s.calendarResolver = r }

// stepElapsed 计算实例在当前步骤的业务耗时：
// 步骤配置了 Calendar 且解析器可用时按日历展开，否则自然时间（向后兼容）。
func (s *Service) stepElapsed(ctx context.Context, inst *ProcessInstance, step *Step, started *time.Time, now time.Time) time.Duration {
	if step.Calendar != "" && s.calendarResolver != nil {
		if cal, ok := s.calendarResolver(ctx, inst.TenantID, step.Calendar); ok && cal != nil {
			return cal.BusinessElapsed(*started, now)
		}
		slog.Warn("itsm process: calendar not resolved, fallback to wall clock",
			"tenant", inst.TenantID, "calendar", step.Calendar, "ticket_id", inst.TicketID)
	}
	return now.Sub(*started)
}
