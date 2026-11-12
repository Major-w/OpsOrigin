package itsm

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// --- 自动化触发器 ---

// handleListAutomationRules 列出本租户全部自动化触发器。
// GET /api/v1/itsm/automation-rules
func (s *Service) handleListAutomationRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListAutomationRules(r.Context(), auth.TenantFromContext(r.Context()))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rules == nil {
		rules = []*model.AutomationRule{}
	}
	httputil.WriteJSON(w, http.StatusOK, rules)
}

// handleCreateAutomationRule 新建触发器。
// POST /api/v1/itsm/automation-rules
func (s *Service) handleCreateAutomationRule(w http.ResponseWriter, r *http.Request) {
	var rule model.AutomationRule
	if err := httputil.BindJSON(r, &rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if rule.Name == "" || rule.Event == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and event are required")
		return
	}
	if rule.Event == "schedule" && !rule.Schedule.Valid() {
		httputil.WriteError(w, http.StatusBadRequest, "schedule rule requires valid cron (5 fields) or interval_minutes")
		return
	}
	if rule.Actions == nil {
		rule.Actions = []model.RuleAction{}
	}
	rule.CreatedBy = getUserID(r, rule.CreatedBy)
	if err := s.store.UpsertAutomationRule(r.Context(), auth.TenantFromContext(r.Context()), &rule); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rule)
}

// handleUpdateAutomationRule 按路径 id 更新触发器。
// PUT /api/v1/itsm/automation-rules/{id}
func (s *Service) handleUpdateAutomationRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var rule model.AutomationRule
	if err := httputil.BindJSON(r, &rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if rule.Name == "" || rule.Event == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and event are required")
		return
	}
	if rule.Event == "schedule" && !rule.Schedule.Valid() {
		httputil.WriteError(w, http.StatusBadRequest, "schedule rule requires valid cron (5 fields) or interval_minutes")
		return
	}
	rule.ID = id
	rule.CreatedBy = getUserID(r, rule.CreatedBy)
	if err := s.store.UpsertAutomationRule(r.Context(), auth.TenantFromContext(r.Context()), &rule); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rule)
}

// handleDeleteAutomationRule 删除触发器。
// DELETE /api/v1/itsm/automation-rules/{id}
func (s *Service) handleDeleteAutomationRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteAutomationRule(r.Context(), auth.TenantFromContext(r.Context()), id); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "rule not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- 自定义按钮 ---

// handleListTicketButtons 列出本租户全部自定义按钮(按 sort_order 排序)。
// GET /api/v1/itsm/ticket-buttons
func (s *Service) handleListTicketButtons(w http.ResponseWriter, r *http.Request) {
	buttons, err := s.store.ListTicketButtons(r.Context(), auth.TenantFromContext(r.Context()))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if buttons == nil {
		buttons = []*model.TicketButton{}
	}
	httputil.WriteJSON(w, http.StatusOK, buttons)
}

// handleCreateTicketButton 新建自定义按钮。
// POST /api/v1/itsm/ticket-buttons
func (s *Service) handleCreateTicketButton(w http.ResponseWriter, r *http.Request) {
	var b model.TicketButton
	if err := httputil.BindJSON(r, &b); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if b.Name == "" || b.ActionType == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and action_type are required")
		return
	}
	b.CreatedBy = getUserID(r, b.CreatedBy)
	if err := s.store.UpsertTicketButton(r.Context(), auth.TenantFromContext(r.Context()), &b); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, b)
}

// handleUpdateTicketButton 按路径 id 更新自定义按钮。
// PUT /api/v1/itsm/ticket-buttons/{id}
func (s *Service) handleUpdateTicketButton(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var b model.TicketButton
	if err := httputil.BindJSON(r, &b); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if b.Name == "" || b.ActionType == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and action_type are required")
		return
	}
	b.ID = id
	b.CreatedBy = getUserID(r, b.CreatedBy)
	if err := s.store.UpsertTicketButton(r.Context(), auth.TenantFromContext(r.Context()), &b); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, b)
}

// handleDeleteTicketButton 删除自定义按钮。
// DELETE /api/v1/itsm/ticket-buttons/{id}
func (s *Service) handleDeleteTicketButton(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteTicketButton(r.Context(), auth.TenantFromContext(r.Context()), id); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "button not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- 触发器执行引擎 ---

// fireEvent 工单事件统一分发枢纽: 出站 webhook + 自动化触发器。
// 各状态迁移 handler 原先直调 fireWebhook, 现统一走 fireEvent,
// 使自动化规则与 webhook 在完全相同的事件点位生效。
func (s *Service) fireEvent(ctx context.Context, event string, ticket *model.Ticket, changedBy string) {
	s.fireWebhook(ctx, event, ticket, changedBy)
	s.fireTriggers(ctx, event, ticket, changedBy)
}

// fireTriggers 匹配并异步执行命中事件的启用规则。
// 条件全满足才触发(空条件=无条件触发); 动作异步执行避免阻塞状态迁移主链路。
func (s *Service) fireTriggers(ctx context.Context, event string, ticket *model.Ticket, changedBy string) {
	if ticket == nil {
		return
	}
	rules, err := s.store.ListAutomationRules(ctx, ticket.TenantID)
	if err != nil {
		slog.Warn("automation rules list failed", "event", event, "ticket_id", ticket.ID, "error", err)
		return
	}
	for _, rule := range rules {
		if !rule.Enabled || rule.Event != event {
			continue
		}
		if !ruleMatches(rule, ticket) {
			continue
		}
		// WithoutCancel: 动作在请求返回后仍能完成, 且保留 ctx 中的身份/租户值。
		ruleCtx := context.WithoutCancel(ctx)
		r := *rule
		t := *ticket
		go s.executeRuleActions(ruleCtx, &r, &t, changedBy)
	}
}

// ruleMatches 全部条件通过才返回 true。
func ruleMatches(rule *model.AutomationRule, ticket *model.Ticket) bool {
	for _, c := range rule.Conditions {
		if !conditionMatches(c, ticket) {
			return false
		}
	}
	return true
}

// conditionMatches 单条件匹配。未知字段 fail-safe 不命中。
func conditionMatches(c model.RuleCondition, ticket *model.Ticket) bool {
	var fieldVal string
	switch c.Field {
	case "status":
		fieldVal = string(ticket.Status)
	case "risk_level":
		fieldVal = string(ticket.RiskLevel)
	case "catalog_id":
		fieldVal = ticket.CatalogID
	case "assignee":
		fieldVal = ticket.Assignee
	case "created_by":
		fieldVal = ticket.CreatedBy
	default:
		if key, ok := strings.CutPrefix(c.Field, "custom_fields."); ok {
			v, exists := ticket.CustomFields[key]
			if !exists || v == nil {
				fieldVal = ""
			} else {
				fieldVal = fmt.Sprintf("%v", v)
			}
		} else {
			return false
		}
	}
	switch c.Op {
	case "not_equals":
		return fieldVal != c.Value
	case "contains":
		return strings.Contains(fieldVal, c.Value)
	default: // equals
		return fieldVal == c.Value
	}
}

// executeRuleActions 依序执行规则动作; 单个动作失败仅记录, 不中断后续动作。
func (s *Service) executeRuleActions(ctx context.Context, rule *model.AutomationRule, ticket *model.Ticket, changedBy string) {
	for _, act := range rule.Actions {
		if err := s.executeRuleAction(ctx, rule, ticket, act, changedBy); err != nil {
			slog.Warn("automation action failed", "rule", rule.Name, "action", act.Type,
				"ticket_id", ticket.ID, "error", err)
		}
	}
}

func (s *Service) executeRuleAction(ctx context.Context, rule *model.AutomationRule, ticket *model.Ticket, act model.RuleAction, changedBy string) error {
	switch act.Type {
	case "notify":
		if s.notify == nil {
			return nil // 未接入通知中心, 静默跳过
		}
		content := renderTemplate(act.Message, ticket)
		if content == "" {
			content = fmt.Sprintf("触发器 [%s] 命中工单 %s(%s)", rule.Name, ticket.Title, ticket.ID)
		}
		// 显式指定渠道：直接投递（绕过 Policy 匹配与免打扰）；未指定走 Policy 泛播。
		if len(act.Channels) > 0 {
			if cn, ok := s.notify.(ChannelNotifier); ok {
				return cn.NotifyChannels(ctx, ticket.TenantID, act.Channels, "ticket.automation",
					"自动化触发: "+rule.Name, content)
			}
			return fmt.Errorf("notify channels not supported by notifier")
		}
		return s.notify.NotifyEvent(ctx, ticket.TenantID, "ticket.automation",
			"自动化触发: "+rule.Name, content)
	case "assign":
		if act.Target == "" {
			return fmt.Errorf("assign action missing target")
		}
		t, err := s.store.Get(ctx, ticket.ID, ticket.TenantID)
		if err != nil {
			return err
		}
		if model.IsTerminal(t.Status) {
			return nil // 终态工单不自动分派
		}
		if t.Assignee == act.Target {
			return nil // 已是目标受派人, 幂等跳过
		}
		previousStatus := t.Status
		now := time.Now()
		t.Assignee = act.Target
		t.AssignedAt = &now
		if err := s.store.Update(ctx, t); err != nil {
			return err
		}
		s.recordEvent(ctx, t, previousStatus, t.Status, "automation:"+rule.Name, "触发器自动指派给 "+act.Target)
		s.broadcastTicketUpdate(t)
		slog.Info("automation rule assigned ticket", "rule", rule.Name, "ticket_id", t.ID, "assignee", act.Target)
		return nil
	case "webhook":
		if act.Target == "" {
			return fmt.Errorf("webhook action missing target url")
		}
		return s.webhook.Notify(ctx, act.Target, rule.Event,
			ticket.ID, ticket.TenantID, string(ticket.Status), string(ticket.RiskLevel), changedBy)
	case "start_process":
		// 触发器驱动流程: 命中后为工单启动指定流程实例(事件驱动入口, 补齐"仅建单触发"的空缺)。
		// Target=流程ID; 语义与建单绑流程一致(落点步骤映射工单状态、重读受派人防覆盖)。
		if act.Target == "" {
			return fmt.Errorf("start_process action missing target process id")
		}
		if s.processStore == nil {
			return fmt.Errorf("process engine not configured")
		}
		// 幂等: 工单已有流程实例(一单一实例)则跳过, 避免触发器重复命中重复拉起。
		if inst, gerr := s.processStore.GetInstance(ctx, ticket.ID, ticket.TenantID); gerr == nil && inst != nil {
			slog.Info("automation start_process skipped, instance exists", "rule", rule.Name,
				"ticket_id", ticket.ID, "process_id", act.Target)
			return nil
		}
		_, landing, err := s.processStore.StartInstance(ctx, ticket.TenantID, act.Target, ticket.ID)
		if err != nil {
			return fmt.Errorf("start process %s: %w", act.Target, err)
		}
		t, err := s.store.Get(ctx, ticket.ID, ticket.TenantID)
		if err != nil {
			return err
		}
		if model.IsTerminal(t.Status) {
			return nil // 终态工单不拉起流程
		}
		previousStatus := t.Status
		t.ProcessID = act.Target
		if landing != nil {
			if mapped := process.StepTypeToTicketStatus(landing); mapped != "" {
				t.Status = model.TicketStatus(mapped)
			}
		}
		// StartInstance 内可能已自动派单(dispatch 节点): 重读补齐受派人再落库。
		if fresh, ferr := s.store.Get(ctx, ticket.ID, ticket.TenantID); ferr == nil && fresh != nil {
			if t.Assignee == "" && fresh.Assignee != "" {
				t.Assignee = fresh.Assignee
				t.AssignedAt = fresh.AssignedAt
			}
		}
		if err := s.store.Update(ctx, t); err != nil {
			return err
		}
		s.recordEvent(ctx, t, previousStatus, t.Status, "automation:"+rule.Name,
			"触发器自动启动流程(流程ID: "+act.Target+")")
		s.broadcastTicketUpdate(t)
		slog.Info("automation rule started process", "rule", rule.Name,
			"ticket_id", t.ID, "process_id", act.Target)
		return nil
	default:
		return fmt.Errorf("unknown action type: %s", act.Type)
	}
}

// renderTemplate 替换 ${ticket.<字段>} 与 ${custom_fields.<key>} 占位符; 未识别的占位符渲染为空串。
func renderTemplate(tpl string, ticket *model.Ticket) string {
	if tpl == "" {
		return ""
	}
	var sb strings.Builder
	for {
		start := strings.Index(tpl, "${")
		if start < 0 {
			sb.WriteString(tpl)
			break
		}
		end := strings.Index(tpl[start:], "}")
		if end < 0 {
			sb.WriteString(tpl)
			break
		}
		end += start
		sb.WriteString(tpl[:start])
		path := tpl[start+2 : end]
		sb.WriteString(lookupTicketVar(ticket, path))
		tpl = tpl[end+1:]
	}
	return sb.String()
}

func lookupTicketVar(ticket *model.Ticket, path string) string {
	if ticket == nil {
		return "" // 定时触发器无工单上下文, 模板变量渲染为空串
	}
	if key, ok := strings.CutPrefix(path, "custom_fields."); ok {
		if ticket.CustomFields == nil {
			return ""
		}
		if v, exists := ticket.CustomFields[key]; exists && v != nil {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
	if key, ok := strings.CutPrefix(path, "ticket."); ok {
		switch key {
		case "id":
			return ticket.ID
		case "title":
			return ticket.Title
		case "description":
			return ticket.Description
		case "status":
			return string(ticket.Status)
		case "risk_level":
			return string(ticket.RiskLevel)
		case "assignee":
			return ticket.Assignee
		case "created_by":
			return ticket.CreatedBy
		case "process_id":
			return ticket.ProcessID
		case "catalog_id":
			return ticket.CatalogID
		}
	}
	return ""
}
