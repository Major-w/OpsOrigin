package itsm

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/ola"
	"github.com/opsorigin/core/internal/itsm/sla"
	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/httputil"
)

// StartOLAChecker 启动 OLA 超时检查: 定期扫描运行中工单，对超时步骤发送升级通知。
func (s *Service) StartOLAChecker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(120 * time.Second)
		defer ticker.Stop()
		slog.Info("itsm: OLA checker started")
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.checkOLA(ctx)
			}
		}
	}()
}

func (s *Service) checkOLA(ctx context.Context) {
	if s.olaStore == nil {
		return
	}
	tenants, err := s.store.ListTenantIDs(ctx)
	if err != nil || len(tenants) == 0 {
		tenants = []string{"default"}
	}
	for _, t := range tenants {
		s.checkOLATenant(ctx, t)
	}
}

func (s *Service) checkOLATenant(ctx context.Context, tenantID string) {
	policies, err := s.olaStore.List(ctx, tenantID)
	if err != nil || len(policies) == 0 {
		return
	}
	for _, pol := range policies {
		if !pol.Enabled {
			continue
		}
		if pol.DurationHours <= 0 {
			continue
		}
		s.checkOLAPolicy(ctx, tenantID, pol)
	}
}

func (s *Service) checkOLAPolicy(ctx context.Context, tenantID string, pol ola.Policy) {
	tickets, err := s.store.ListActiveTicketsWithDeadline(ctx, tenantID)
	if err != nil {
		return
	}
	deadline := time.Duration(pol.DurationHours) * time.Hour
	for _, t := range tickets {
		if pol.ProcessID != "" && t.ProcessID != pol.ProcessID {
			continue
		}
		elapsed := time.Since(t.UpdatedAt)
		remaining := deadline - elapsed
		if remaining <= 0 {
			slog.Warn("itsm: OLA breached", "ticket", t.ID, "policy", pol.Name)
			s.broadcastEvent(tenantID, map[string]any{"type": "ola_breached", "ticket_id": t.ID, "policy": pol.Name})
		} else if remaining < 1*time.Hour {
			slog.Info("itsm: OLA warning", "ticket", t.ID, "policy", pol.Name, "remaining", remaining)
			s.broadcastEvent(tenantID, map[string]any{"type": "ola_warning", "ticket_id": t.ID, "policy": pol.Name, "remaining_seconds": int(remaining.Seconds())})
		}
	}
}

func (s *Service) slaForRisk(risk model.RiskLevel) time.Duration {
	switch risk {
	case model.RiskLow:
		return 72 * time.Hour
	case model.RiskMedium:
		return 48 * time.Hour
	case model.RiskHigh:
		return 24 * time.Hour
	case model.RiskCritical:
		return 4 * time.Hour
	}
	return 72 * time.Hour
}

func (s *Service) StartSLAChecker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		slog.Info("itsm: SLA checker started")
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.checkSLA(ctx)
			}
		}
	}()
}

func (s *Service) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *Service) checkSLA(ctx context.Context) {
	tenants, err := s.store.ListTenantIDs(ctx)
	if err != nil || len(tenants) == 0 {
		tenants = []string{"default"}
	}
	for _, t := range tenants {
		s.checkSLATenant(ctx, t)
		s.checkHoldAutoResume(ctx, t)
	}
}

func (s *Service) checkHoldAutoResume(ctx context.Context, tenantID string) {
	tickets, err := s.store.ListOnHoldTicketsPastDue(ctx, tenantID)
	if err != nil {
		slog.Error("itsm: hold auto-resume check failed", "tenant", tenantID, "error", err)
		return
	}
	for _, t := range tickets {
		slog.Info("itsm: auto-resuming hold ticket", "ticket", t.ID, "hold_until", t.HoldUntilAt)
		t.OnHold = false
		t.HoldReason = ""
		t.HoldUntilAt = nil
		t.UpdatedAt = time.Now()
		if err := s.store.Update(ctx, t); err != nil {
			slog.Error("itsm: failed to auto-resume ticket", "ticket", t.ID, "error", err)
			continue
		}
		s.broadcastEvent(tenantID, map[string]any{"type": "hold_resumed", "ticket_id": t.ID})
	}
}

// checkSLATenant 检查单个租户下未关闭且带截止时间的工单, 触发 SLA 预警/违约标记与通知。
func (s *Service) checkSLATenant(ctx context.Context, tenantID string) {
	tickets, err := s.store.ListActiveTicketsWithDeadline(ctx, tenantID)
	if err != nil {
		slog.Error("itsm: SLA check failed", "tenant", tenantID, "error", err)
		return
	}
	now := time.Now()
	for _, t := range tickets {
		if t.DeadlineAt == nil {
			continue
		}
		deadline := *t.DeadlineAt
		totalDuration := deadline.Sub(t.CreatedAt)
		elapsed := now.Sub(t.CreatedAt)

		// 扣除挂起时间: 挂起期间 SLA 不计时，需从已用时中减去累计挂起秒数
		if t.HoldSeconds > 0 {
			elapsed -= time.Duration(t.HoldSeconds) * time.Second
		}

		// SLA breached
		if now.After(deadline) && !t.SLABreached {
			t.SLABreached = true
			t.SLABreachedAt = &now
			t.SLAEscalationLevel = 2
			// 多级升级策略: 遍历 EscalationLevels，命中的最高级别生效
			pol := s.slaPolicyForTicket(ctx, tenantID, t)
			if pol != nil && len(pol.EscalationLevels) > 0 {
				elapsedHours := elapsed.Hours()
				for _, lvl := range pol.EscalationLevels {
					if elapsedHours >= float64(lvl.Hours) && lvl.Level > t.SLAEscalationLevel {
						t.SLAEscalationLevel = lvl.Level
						if lvl.NotifyTo != "" && t.Assignee != lvl.NotifyTo {
							prev := t.Assignee
							at := time.Now()
							t.Assignee = lvl.NotifyTo
							t.AssignedAt = &at
							s.recordEvent(ctx, t, t.Status, t.Status, "sla_escalation", fmt.Sprintf("SLA 多级升级(L%d): %s → %s", lvl.Level, prev, lvl.NotifyTo))
						}
					}
				}
			} else if pol != nil && pol.EscalationTo != "" && t.Assignee != pol.EscalationTo {
				// 兼容旧单级升级: 命中策略且配置 EscalationTo 时自动转派
				prev := t.Assignee
				at := time.Now()
				t.Assignee = pol.EscalationTo
				t.AssignedAt = &at
				s.recordEvent(ctx, t, t.Status, t.Status, "sla_escalation", fmt.Sprintf("SLA 违约升级转派: %s → %s", prev, pol.EscalationTo))
			}
			if err := s.store.Update(ctx, t); err != nil {
				slog.Error("itsm: failed to mark SLA breached", "ticket_id", t.ID, "error", err)
				continue
			}
			slog.Warn("itsm: SLA breached", "ticket_id", t.ID, "title", t.Title, "deadline", deadline)
			s.broadcastTicketUpdate(t)
			s.notifySLA(ctx, tenantID, t, true)
			continue
		}

		// 多级 SLA 预警: 遍历 EscalationLevels，在到达各级阈值时通知
		if pol := s.slaPolicyForTicket(ctx, tenantID, t); pol != nil && len(pol.EscalationLevels) > 0 {
			elapsedHours := elapsed.Hours()
			for _, lvl := range pol.EscalationLevels {
				if elapsedHours >= float64(lvl.Hours) && lvl.Level > t.SLAEscalationLevel {
					t.SLAEscalationLevel = lvl.Level
					if err := s.store.Update(ctx, t); err != nil {
						slog.Error("itsm: failed to update SLA escalation level", "ticket_id", t.ID, "error", err)
						continue
					}
					slog.Info("itsm: SLA multi-level escalation", "ticket_id", t.ID, "level", lvl.Level, "hours", lvl.Hours)
					s.broadcastTicketUpdate(t)
					if lvl.NotifyTo != "" {
						content := fmt.Sprintf("工单 SLA 升级至 L%d (已用时 %.1f 小时)\n负责人: %s → %s", lvl.Level, elapsedHours, t.Assignee, lvl.NotifyTo)
						s.notifyTicket(ctx, tenantID, evSLAWarning, fmt.Sprintf("SLA 升级 L%d: %s", lvl.Level, t.Title), content)
					}
				}
			}
		}

		// SLA warning at 80%
		if totalDuration > 0 && elapsed.Seconds()/totalDuration.Seconds() >= 0.8 && !t.SLAWarningNotified && !t.SLABreached {
			t.SLAWarningNotified = true
			if t.SLAEscalationLevel == 0 {
				t.SLAEscalationLevel = 1
			}
			if err := s.store.Update(ctx, t); err != nil {
				slog.Error("itsm: failed to mark SLA warning", "ticket_id", t.ID, "error", err)
				continue
			}
			slog.Info("itsm: SLA warning (80%)", "ticket_id", t.ID, "title", t.Title, "deadline", deadline)
			s.broadcastTicketUpdate(t)
			s.notifySLA(ctx, tenantID, t, false)
		}
	}
}

// slaPolicyForTicket 取工单命中的 SLA 策略: 流程绑定优先(GetByProcessID), 否则按风险等级。
func (s *Service) slaPolicyForTicket(ctx context.Context, tenantID string, t *model.Ticket) *sla.Policy {
	if s.slaStore == nil {
		return nil
	}
	if t.ProcessID != "" {
		if p, _ := s.slaStore.GetByProcessID(ctx, tenantID, t.ProcessID); p != nil {
			return p
		}
	}
	p, _ := s.slaStore.GetByRisk(ctx, tenantID, string(t.RiskLevel))
	return p
}

// notifySLA 在 SLA 预警/违约时经通知中心分发, 并带上策略配置的升级角色(EscalationTo)。
func (s *Service) notifySLA(ctx context.Context, tenantID string, t *model.Ticket, breached bool) {
	escalation := ""
	if pol := s.slaPolicyForTicket(ctx, tenantID, t); pol != nil {
		escalation = pol.EscalationTo
	}
	if breached {
		content := fmt.Sprintf("工单已超出 SLA 截止时间\n负责人: %s\n升级至: %s", t.Assignee, escalation)
		s.notifyTicket(ctx, tenantID, evSLABreached, "SLA 违约: "+t.Title, content)
		// 升级对象一并进收件箱: 违约时光通知负责人是不够的, 升级人得同步看到。
		s.notifyTicketInbox(ctx, t, msgCategorySLA, msgLevelCritical,
			"SLA 违约: "+t.Title, content, "", t.Assignee, t.CreatedBy, escalation)
	} else {
		content := fmt.Sprintf("工单即将达到 SLA 截止时间(剩余<20%%)\n负责人: %s\n升级至: %s", t.Assignee, escalation)
		s.notifyTicket(ctx, tenantID, evSLAWarning, "SLA 预警: "+t.Title, content)
		s.notifyTicketInbox(ctx, t, msgCategorySLA, msgLevelWarning,
			"SLA 预警: "+t.Title, content, "", t.Assignee)
	}
}

func (s *Service) handleSLAStats(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	stats, err := s.store.GetSLAStats(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, stats)
}

func (s *Service) handleSLABreached(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	tickets, err := s.store.List(r.Context(), tenantID, "")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var breached []*model.Ticket
	for _, t := range tickets {
		if t.SLABreached {
			breached = append(breached, t)
		}
	}
	if breached == nil {
		breached = []*model.Ticket{}
	}
	httputil.WriteJSON(w, http.StatusOK, breached)
}

func (s *Service) handleTicketSLA(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	now := time.Now()
	result := map[string]any{
		"ticket_id":        ticket.ID,
		"deadline_at":      ticket.DeadlineAt,
		"sla_breached":     ticket.SLABreached,
		"sla_breached_at":  ticket.SLABreachedAt,
		"sla_warning":      ticket.SLAWarningNotified,
		"escalation_level": ticket.SLAEscalationLevel,
	}
	if ticket.DeadlineAt != nil {
		remaining := time.Until(*ticket.DeadlineAt)
		result["remaining_ms"] = remaining.Milliseconds()
		result["remaining_human"] = formatDuration(remaining)
		totalElapsed := now.Sub(ticket.CreatedAt)
		totalDuration := ticket.DeadlineAt.Sub(ticket.CreatedAt)
		if totalDuration > 0 {
			result["elapsed_percent"] = float64(totalElapsed) / float64(totalDuration) * 100
		}
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}
