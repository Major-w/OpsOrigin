package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/process"
)

var ErrNotFound = errors.New("ticket not found")

type Store interface {
	Create(ctx context.Context, ticket *model.Ticket) error
	// NextTicketNumber 按规则生成唯一可读工单号（rule 为 nil 或未启用则全局默认 TK-YYYY-NNNN）。
	NextTicketNumber(ctx context.Context, tenantID, processID string, rule *process.TicketNumberRule) (string, error)
	Get(ctx context.Context, id, tenantID string) (*model.Ticket, error)
	Update(ctx context.Context, ticket *model.Ticket) error
	List(ctx context.Context, tenantID string, status model.TicketStatus) ([]*model.Ticket, error)
	ListPaged(ctx context.Context, tenantID string, status model.TicketStatus, p PageParams) (*PageResult[*model.Ticket], error)
	AddEvent(ctx context.Context, event *model.TicketEvent) error
	GetEvents(ctx context.Context, ticketID, tenantID string) ([]*model.TicketEvent, error)
	GetStats(ctx context.Context, tenantID string) (*model.ITSMStats, error)
	// Comments
	AddComment(ctx context.Context, comment *model.Comment) error
	ListComments(ctx context.Context, ticketID, tenantID string) ([]*model.Comment, error)
	DeleteComment(ctx context.Context, id int64, ticketID, tenantID string) error
	EditComment(ctx context.Context, id int64, ticketID, tenantID, content string) error
	// Subtasks
	CreateSubtask(ctx context.Context, st *model.Subtask) error
	GetSubtask(ctx context.Context, id int64, ticketID, tenantID string) (*model.Subtask, error)
	ListSubtasks(ctx context.Context, ticketID, tenantID string) ([]*model.Subtask, error)
	UpdateSubtask(ctx context.Context, st *model.Subtask) error
	DeleteSubtask(ctx context.Context, id int64, ticketID, tenantID string) error
	// Attachments
	AddAttachment(ctx context.Context, att *model.Attachment) error
	ListAttachments(ctx context.Context, ticketID, tenantID string) ([]*model.Attachment, error)
	GetAttachment(ctx context.Context, id int64, ticketID, tenantID string) (*model.Attachment, error)
	DeleteAttachment(ctx context.Context, id int64, ticketID, tenantID string) error
	// SLA
	ListActiveTicketsWithDeadline(ctx context.Context, tenantID string) ([]*model.Ticket, error)
	ListOnHoldTicketsPastDue(ctx context.Context, tenantID string) ([]*model.Ticket, error)
	GetSLAStats(ctx context.Context, tenantID string) (*model.SLAStats, error)
	// ListTenantIDs 返回存在工单的所有租户, 供 SLA 检查器等跨租户任务遍历。
	ListTenantIDs(ctx context.Context) ([]string, error)
	// Change Requests
	CreateChange(ctx context.Context, change *model.ChangeRequest) error
	GetChange(ctx context.Context, id, tenantID string) (*model.ChangeRequest, error)
	ListChanges(ctx context.Context, tenantID string) ([]*model.ChangeRequest, error)
	ListChangesPaged(ctx context.Context, tenantID string, p PageParams) (*PageResult[*model.ChangeRequest], error)
	UpdateChange(ctx context.Context, change *model.ChangeRequest) error
	// Form schemas (dynamic form definitions)
	GetFormSchema(ctx context.Context, tenantID, scope string) (*form.FormSchema, error)
	UpsertFormSchema(ctx context.Context, tenantID, scope string, schema form.FormSchema) error
	ListFormScopes(ctx context.Context, tenantID string) ([]string, error)
	// Data dictionary (field dicts)
	GetFieldDict(ctx context.Context, tenantID, id string) (*form.FieldDict, error)
	ListFieldDicts(ctx context.Context, tenantID string) ([]*form.FieldDict, error)
	UpsertFieldDict(ctx context.Context, tenantID string, dict *form.FieldDict) error
	DeleteFieldDict(ctx context.Context, tenantID, id string) error
	// Ticket templates (prefilled ticket creation)
	GetTicketTemplate(ctx context.Context, tenantID, id string) (*model.TicketTemplate, error)
	ListTicketTemplates(ctx context.Context, tenantID string) ([]*model.TicketTemplate, error)
	UpsertTicketTemplate(ctx context.Context, tenantID string, t *model.TicketTemplate) error
	DeleteTicketTemplate(ctx context.Context, tenantID, id string) error

	// 自动化触发器(P1: 工单事件 -> 条件 -> 动作)
	GetAutomationRule(ctx context.Context, tenantID, id string) (*model.AutomationRule, error)
	ListAutomationRules(ctx context.Context, tenantID string) ([]*model.AutomationRule, error)
	UpsertAutomationRule(ctx context.Context, tenantID string, r *model.AutomationRule) error
	DeleteAutomationRule(ctx context.Context, tenantID, id string) error

	// 自定义按钮(工单详情页快捷操作, 执行由前端映射既有工单端点)
	GetTicketButton(ctx context.Context, tenantID, id string) (*model.TicketButton, error)
	ListTicketButtons(ctx context.Context, tenantID string) ([]*model.TicketButton, error)
	UpsertTicketButton(ctx context.Context, tenantID string, b *model.TicketButton) error
	DeleteTicketButton(ctx context.Context, tenantID, id string) error

	// 状态流转规则(P2-⑯: 自定义状态+流转规则, 只收紧不放大)
	GetStatusFlow(ctx context.Context, tenantID, catalogID string) (*model.StatusFlow, error)
	UpsertStatusFlow(ctx context.Context, tenantID string, f *model.StatusFlow) error
	DeleteStatusFlow(ctx context.Context, tenantID, catalogID string) error
}

type MemoryStore struct {
	mu          sync.RWMutex
	tickets     map[string]*model.Ticket
	events      map[string][]*model.TicketEvent
	comments    []*model.Comment
	subtasks    []*model.Subtask
	attachments []*model.Attachment
	changes     map[string]*model.ChangeRequest
	schemas     map[string]map[string]*form.FormSchema      // tenantID -> scope -> schema
	dicts       map[string]map[string]*form.FieldDict       // tenantID -> dictID -> dict
	templates   map[string]map[string]*model.TicketTemplate // tenantID -> templateID -> template
	autoRules   map[string]map[string]*model.AutomationRule // tenantID -> ruleID -> rule
	tktButtons  map[string]map[string]*model.TicketButton   // tenantID -> buttonID -> button
	statusFlows map[string]map[string]*model.StatusFlow     // tenantID -> catalogID(""=全局) -> flow
	nextID      int64
	ticketSeq   map[string]int // 工单号计数器: key = tenantID + "-" + year
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tickets:     make(map[string]*model.Ticket),
		events:      make(map[string][]*model.TicketEvent),
		changes:     make(map[string]*model.ChangeRequest),
		schemas:     make(map[string]map[string]*form.FormSchema),
		dicts:       make(map[string]map[string]*form.FieldDict),
		templates:   make(map[string]map[string]*model.TicketTemplate),
		autoRules:   make(map[string]map[string]*model.AutomationRule),
		tktButtons:  make(map[string]map[string]*model.TicketButton),
		statusFlows: make(map[string]map[string]*model.StatusFlow),
		ticketSeq:   make(map[string]int),
	}
}

var _ Store = (*MemoryStore)(nil)

func (s *MemoryStore) Create(ctx context.Context, ticket *model.Ticket) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ticket.ID = uuid.New().String()
	if ticket.Number == "" {
		ticket.Number = s.nextTicketNumber(ticket.TenantID, ticket.ProcessID, nil)
	}
	now := time.Now()
	ticket.CreatedAt = now
	ticket.UpdatedAt = now

	s.tickets[ticket.ID] = ticket
	return nil
}

// nextTicketNumber 内存实现：按 (租户, scope_key) 自增，与 PG 规则一致（含流程自定义编号组成）。
func (s *MemoryStore) nextTicketNumber(tenantID, processID string, rule *process.TicketNumberRule) string {
	key := ticketCounterScopeKey(tenantID, processID, rule)
	s.ticketSeq[key]++
	seq := s.ticketSeq[key]
	if rule == nil || !rule.Enabled {
		return fmt.Sprintf("TK-%d-%04d", time.Now().Year(), seq)
	}
	sep := rule.EffectiveSeparator()
	parts := []string{}
	if rule.Prefix != "" {
		parts = append(parts, rule.Prefix)
	}
	if ds := formatNumberDate(rule.DateFormat); ds != "" {
		parts = append(parts, ds)
	}
	parts = append(parts, fmt.Sprintf("%0*d", rule.EffectiveSeqWidth(), seq))
	return strings.Join(parts, sep)
}

// NextTicketNumber 实现 Store 接口（供 Service 在建单前按规则预生成编号）。
func (s *MemoryStore) NextTicketNumber(ctx context.Context, tenantID, processID string, rule *process.TicketNumberRule) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextTicketNumber(tenantID, processID, rule), nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*model.Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.tickets[id]
	if !ok {
		return nil, ErrNotFound
	}
	if t.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return t, nil
}

func (s *MemoryStore) Update(ctx context.Context, ticket *model.Ticket) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.tickets[ticket.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.TenantID != ticket.TenantID {
		return ErrNotFound
	}

	ticket.UpdatedAt = time.Now()
	s.tickets[ticket.ID] = ticket
	return nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID string, status model.TicketStatus) ([]*model.Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*model.Ticket
	for _, t := range s.tickets {
		if t.TenantID != tenantID {
			continue
		}
		if status != "" && t.Status != status {
			continue
		}
		result = append(result, t)
	}
	return result, nil
}

func (s *MemoryStore) ListPaged(ctx context.Context, tenantID string, status model.TicketStatus, p PageParams) (*PageResult[*model.Ticket], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var filtered []*model.Ticket
	for _, t := range s.tickets {
		if t.TenantID != tenantID {
			continue
		}
		if status != "" && t.Status != status {
			continue
		}
		filtered = append(filtered, t)
	}
	total := len(filtered)
	start := p.Offset()
	if start > total {
		start = total
	}
	end := start + p.PageSize
	if end > total {
		end = total
	}
	items := filtered[start:end]
	if items == nil {
		items = []*model.Ticket{}
	}
	return &PageResult[*model.Ticket]{Items: items, Total: total, Page: p.Page, PageSize: p.PageSize}, nil
}

func (s *MemoryStore) AddEvent(ctx context.Context, event *model.TicketEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event.CreatedAt = time.Now()
	s.events[event.TicketID] = append(s.events[event.TicketID], event)
	return nil
}

func (s *MemoryStore) GetEvents(ctx context.Context, ticketID, tenantID string) ([]*model.TicketEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events, ok := s.events[ticketID]
	if !ok {
		return []*model.TicketEvent{}, nil
	}
	result := make([]*model.TicketEvent, len(events))
	copy(result, events)
	return result, nil
}

func (s *MemoryStore) GetStats(ctx context.Context, tenantID string) (*model.ITSMStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &model.ITSMStats{
		ByStatus:  make(map[string]int),
		ByRisk:    make(map[string]int),
		ByCreator: make(map[string]int),
		ByChannel: make(map[string]int),
	}
	var approvedCount int
	var totalApprovalMs int64
	var approvedWithAt int
	var totalExecutionMs int64
	var executedWithAt int
	days := map[string]*struct{ created, closed int }{}
	var dayOrder []string

	for _, t := range s.tickets {
		if t.TenantID != tenantID {
			continue
		}
		stats.Total++
		stats.ByStatus[string(t.Status)]++
		stats.ByRisk[string(t.RiskLevel)]++
		if t.CreatedBy != "" {
			stats.ByCreator[t.CreatedBy]++
		}
		ch := t.CreatedByType
		if ch == "" {
			ch = "manual"
		}
		stats.ByChannel[ch]++
		if t.Status == model.StatusApproved {
			approvedCount++
		}
		day := t.CreatedAt.Format("2006-01-02")
		if _, ok := days[day]; !ok {
			days[day] = &struct{ created, closed int }{}
			dayOrder = append(dayOrder, day)
		}
		days[day].created++
		if t.CompletedAt != nil {
			cd := t.CompletedAt.Format("2006-01-02")
			if _, ok := days[cd]; !ok {
				days[cd] = &struct{ created, closed int }{}
				dayOrder = append(dayOrder, cd)
			}
			days[cd].closed++
		}
		if t.ApprovedAt != nil && t.CreatedAt.Before(*t.ApprovedAt) {
			totalApprovalMs += t.ApprovedAt.Sub(t.CreatedAt).Milliseconds()
			approvedWithAt++
		}
		if t.ExecutedAt != nil && t.ApprovedAt != nil && t.ExecutedAt.After(*t.ApprovedAt) {
			totalExecutionMs += t.ExecutedAt.Sub(*t.ApprovedAt).Milliseconds()
			executedWithAt++
		}
	}

	if stats.Total > 0 {
		stats.ApprovalRate = float64(approvedCount) / float64(stats.Total)
	}
	if approvedWithAt > 0 {
		stats.AvgApprovalMs = totalApprovalMs / int64(approvedWithAt)
	}
	if executedWithAt > 0 {
		stats.AvgExecutionMs = totalExecutionMs / int64(executedWithAt)
	}
	stats.Trends = make([]model.TrendPoint, 0, len(dayOrder))
	for _, d := range dayOrder {
		stats.Trends = append(stats.Trends, model.TrendPoint{Date: d, Created: days[d].created, Closed: days[d].closed})
	}
	if len(stats.Trends) > 30 {
		stats.Trends = stats.Trends[len(stats.Trends)-30:]
	}
	return stats, nil
}

func (s *MemoryStore) AddComment(ctx context.Context, comment *model.Comment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	comment.ID = s.nextID
	comment.CreatedAt = time.Now()
	s.comments = append(s.comments, comment)
	return nil
}

func (s *MemoryStore) ListComments(ctx context.Context, ticketID, tenantID string) ([]*model.Comment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.Comment
	for _, c := range s.comments {
		if c.TicketID == ticketID && c.TenantID == tenantID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (s *MemoryStore) DeleteComment(ctx context.Context, id int64, ticketID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.comments {
		if c.ID == id && c.TicketID == ticketID && c.TenantID == tenantID {
			s.comments = append(s.comments[:i], s.comments[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (s *MemoryStore) EditComment(ctx context.Context, id int64, ticketID, tenantID, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, c := range s.comments {
		if c.ID == id && c.TicketID == ticketID && c.TenantID == tenantID {
			c.Content = content
			c.Edited = true
			c.EditedAt = &now
			return nil
		}
	}
	return ErrNotFound
}

// --- Subtasks (in-memory) ---

func (s *MemoryStore) CreateSubtask(ctx context.Context, st *model.Subtask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	st.ID = s.nextID
	now := time.Now()
	st.CreatedAt = now
	st.UpdatedAt = now
	if st.Status == "" {
		st.Status = model.SubtaskPending
	}
	if st.Status == model.SubtaskDone {
		st.CompletedAt = &now
	}
	s.subtasks = append(s.subtasks, st)
	return nil
}

func (s *MemoryStore) GetSubtask(ctx context.Context, id int64, ticketID, tenantID string) (*model.Subtask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, st := range s.subtasks {
		if st.ID == id && st.TicketID == ticketID && st.TenantID == tenantID {
			cp := *st
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) ListSubtasks(ctx context.Context, ticketID, tenantID string) ([]*model.Subtask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.Subtask
	for _, st := range s.subtasks {
		if st.TicketID == ticketID && st.TenantID == tenantID {
			result = append(result, st)
		}
	}
	return result, nil
}

func (s *MemoryStore) UpdateSubtask(ctx context.Context, st *model.Subtask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, cur := range s.subtasks {
		if cur.ID == st.ID && cur.TicketID == st.TicketID && cur.TenantID == st.TenantID {
			st.UpdatedAt = time.Now()
			if st.Status == model.SubtaskDone && cur.Status != model.SubtaskDone {
				now := st.UpdatedAt
				st.CompletedAt = &now
			} else if st.Status != model.SubtaskDone {
				st.CompletedAt = nil
			}
			// 与 PG 实现保持一致: 进入处理中且未记开始时间时补齐。
			if st.Status == model.SubtaskInProgress && st.StartedAt == nil {
				now := st.UpdatedAt
				st.StartedAt = &now
			}
			s.subtasks[i] = st
			return nil
		}
	}
	return ErrNotFound
}

func (s *MemoryStore) DeleteSubtask(ctx context.Context, id int64, ticketID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, st := range s.subtasks {
		if st.ID == id && st.TicketID == ticketID && st.TenantID == tenantID {
			s.subtasks = append(s.subtasks[:i], s.subtasks[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (s *MemoryStore) AddAttachment(ctx context.Context, att *model.Attachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	att.ID = s.nextID
	att.CreatedAt = time.Now()
	s.attachments = append(s.attachments, att)
	return nil
}

func (s *MemoryStore) ListAttachments(ctx context.Context, ticketID, tenantID string) ([]*model.Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.Attachment
	for _, a := range s.attachments {
		if a.TicketID == ticketID && a.TenantID == tenantID {
			result = append(result, a)
		}
	}
	return result, nil
}

func (s *MemoryStore) GetAttachment(ctx context.Context, id int64, ticketID, tenantID string) (*model.Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.attachments {
		if a.ID == id && a.TicketID == ticketID && a.TenantID == tenantID {
			return a, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) DeleteAttachment(ctx context.Context, id int64, ticketID, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.attachments {
		if a.ID == id && a.TicketID == ticketID && a.TenantID == tenantID {
			s.attachments = append(s.attachments[:i], s.attachments[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (s *MemoryStore) ListActiveTicketsWithDeadline(ctx context.Context, tenantID string) ([]*model.Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.Ticket
	activeStatuses := map[model.TicketStatus]bool{
		model.StatusDraft:         true,
		model.StatusPendingReview: true,
		model.StatusApproved:      true,
		model.StatusExecuting:     true,
	}
	for _, t := range s.tickets {
		if t.TenantID != tenantID {
			continue
		}
		if !activeStatuses[t.Status] {
			continue
		}
		if t.DeadlineAt == nil {
			continue
		}
		if t.OnHold {
			continue // 挂起工单 SLA 停表
		}
		result = append(result, t)
	}
	return result, nil
}

func (s *MemoryStore) ListOnHoldTicketsPastDue(ctx context.Context, tenantID string) ([]*model.Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.Ticket
	now := time.Now()
	for _, t := range s.tickets {
		if t.TenantID != tenantID {
			continue
		}
		if !t.OnHold || t.HoldUntilAt == nil {
			continue
		}
		if !t.HoldUntilAt.Before(now) {
			continue
		}
		result = append(result, t)
	}
	return result, nil
}

func (s *MemoryStore) ListTenantIDs(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, t := range s.tickets {
		if t.TenantID != "" && !seen[t.TenantID] {
			seen[t.TenantID] = true
			out = append(out, t.TenantID)
		}
	}
	if len(out) == 0 {
		out = []string{"default"}
	}
	return out, nil
}

func (s *MemoryStore) GetSLAStats(ctx context.Context, tenantID string) (*model.SLAStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &model.SLAStats{}
	var totalResponseMs, totalResolutionMs int64
	var responseCount, resolutionCount int

	for _, t := range s.tickets {
		if t.TenantID != tenantID {
			continue
		}
		if t.DeadlineAt == nil {
			continue
		}
		activeStatuses := map[model.TicketStatus]bool{
			model.StatusDraft:         true,
			model.StatusPendingReview: true,
			model.StatusApproved:      true,
			model.StatusExecuting:     true,
		}
		if activeStatuses[t.Status] {
			stats.TotalActive++
			if t.SLABreached {
				stats.SLABreached++
			} else if t.SLAWarningNotified {
				stats.SLAWarning++
			}
		}
		if t.Status == model.StatusSucceeded || t.Status == model.StatusRolledBack || t.Status == model.StatusFailed {
			if !t.SLABreached {
				stats.SLAMet++
			} else {
				stats.SLABreached++
			}
		}
		if t.ApprovedAt != nil {
			totalResponseMs += t.ApprovedAt.Sub(t.CreatedAt).Milliseconds()
			responseCount++
		}
		if t.CompletedAt != nil {
			totalResolutionMs += t.CompletedAt.Sub(t.CreatedAt).Milliseconds()
			resolutionCount++
		}
	}
	total := stats.SLAMet + stats.SLABreached
	if total > 0 {
		stats.ComplianceRate = float64(stats.SLAMet) / float64(total)
	}
	if responseCount > 0 {
		stats.AvgResponseMs = totalResponseMs / int64(responseCount)
	}
	if resolutionCount > 0 {
		stats.AvgResolutionMs = totalResolutionMs / int64(resolutionCount)
	}
	return stats, nil
}

func (s *MemoryStore) CreateChange(ctx context.Context, change *model.ChangeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	change.ID = uuid.New().String()
	now := time.Now()
	change.CreatedAt = now
	change.UpdatedAt = now
	if change.Status == "" {
		change.Status = model.ChangeStatusPending
	}
	s.changes[change.ID] = change
	return nil
}

func (s *MemoryStore) GetChange(ctx context.Context, id, tenantID string) (*model.ChangeRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.changes[id]
	if !ok {
		return nil, ErrNotFound
	}
	if c.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return c, nil
}

func (s *MemoryStore) ListChanges(ctx context.Context, tenantID string) ([]*model.ChangeRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.ChangeRequest
	for _, c := range s.changes {
		if c.TenantID == tenantID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (s *MemoryStore) ListChangesPaged(ctx context.Context, tenantID string, p PageParams) (*PageResult[*model.ChangeRequest], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var filtered []*model.ChangeRequest
	for _, c := range s.changes {
		if c.TenantID == tenantID {
			filtered = append(filtered, c)
		}
	}
	total := len(filtered)
	start := p.Offset()
	if start > total {
		start = total
	}
	end := start + p.PageSize
	if end > total {
		end = total
	}
	items := filtered[start:end]
	if items == nil {
		items = []*model.ChangeRequest{}
	}
	return &PageResult[*model.ChangeRequest]{Items: items, Total: total, Page: p.Page, PageSize: p.PageSize}, nil
}

func (s *MemoryStore) UpdateChange(ctx context.Context, change *model.ChangeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.changes[change.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.TenantID != change.TenantID {
		return ErrNotFound
	}
	change.UpdatedAt = time.Now()
	s.changes[change.ID] = change
	return nil
}

// --- Form schema methods (in-memory) ---

func (s *MemoryStore) GetFormSchema(ctx context.Context, tenantID, scope string) (*form.FormSchema, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	scopes, ok := s.schemas[tenantID]
	if !ok {
		return nil, nil
	}
	return scopes[scope], nil
}

func (s *MemoryStore) UpsertFormSchema(ctx context.Context, tenantID, scope string, schema form.FormSchema) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schemas[tenantID] == nil {
		s.schemas[tenantID] = make(map[string]*form.FormSchema)
	}
	schema.Scope = scope
	s.schemas[tenantID][scope] = &schema
	return nil
}

func (s *MemoryStore) ListFormScopes(ctx context.Context, tenantID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	scopes, ok := s.schemas[tenantID]
	if !ok {
		return []string{}, nil
	}
	out := make([]string, 0, len(scopes))
	for sc := range scopes {
		out = append(out, sc)
	}
	return out, nil
}

// --- 数据字典(Memory) ---

func (s *MemoryStore) GetFieldDict(ctx context.Context, tenantID, id string) (*form.FieldDict, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.dicts[tenantID]; ok {
		if d, ok := m[id]; ok {
			return d, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) ListFieldDicts(ctx context.Context, tenantID string) ([]*form.FieldDict, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*form.FieldDict
	for _, d := range s.dicts[tenantID] {
		out = append(out, d)
	}
	return out, nil
}

func (s *MemoryStore) UpsertFieldDict(ctx context.Context, tenantID string, dict *form.FieldDict) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dict.ID == "" {
		dict.ID = uuid.New().String()
	}
	dict.TenantID = tenantID
	if s.dicts[tenantID] == nil {
		s.dicts[tenantID] = map[string]*form.FieldDict{}
	}
	s.dicts[tenantID][dict.ID] = dict
	return nil
}

func (s *MemoryStore) DeleteFieldDict(ctx context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.dicts[tenantID]; ok {
		if _, ok := m[id]; ok {
			delete(m, id)
			return nil
		}
	}
	return ErrNotFound
}

// --- 提单模板(Memory) ---

func (s *MemoryStore) GetTicketTemplate(ctx context.Context, tenantID, id string) (*model.TicketTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.templates[tenantID]; ok {
		if t, ok := m[id]; ok {
			cp := *t
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) ListTicketTemplates(ctx context.Context, tenantID string) ([]*model.TicketTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.TicketTemplate
	for _, t := range s.templates[tenantID] {
		cp := *t
		out = append(out, &cp)
	}
	if out == nil {
		out = []*model.TicketTemplate{}
	}
	return out, nil
}

func (s *MemoryStore) UpsertTicketTemplate(ctx context.Context, tenantID string, t *model.TicketTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	t.TenantID = tenantID
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if s.templates[tenantID] == nil {
		s.templates[tenantID] = map[string]*model.TicketTemplate{}
	}
	cp := *t
	s.templates[tenantID][t.ID] = &cp
	return nil
}

func (s *MemoryStore) DeleteTicketTemplate(ctx context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.templates[tenantID]; ok {
		if _, ok := m[id]; ok {
			delete(m, id)
			return nil
		}
	}
	return ErrNotFound
}

// --- 自动化触发器(Memory) ---

func (s *MemoryStore) GetAutomationRule(ctx context.Context, tenantID, id string) (*model.AutomationRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.autoRules[tenantID]; ok {
		if r, ok := m[id]; ok {
			cp := *r
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) ListAutomationRules(ctx context.Context, tenantID string) ([]*model.AutomationRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.AutomationRule
	for _, r := range s.autoRules[tenantID] {
		cp := *r
		out = append(out, &cp)
	}
	if out == nil {
		out = []*model.AutomationRule{}
	}
	return out, nil
}

func (s *MemoryStore) UpsertAutomationRule(ctx context.Context, tenantID string, r *model.AutomationRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	r.TenantID = tenantID
	now := time.Now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	if s.autoRules[tenantID] == nil {
		s.autoRules[tenantID] = map[string]*model.AutomationRule{}
	}
	cp := *r
	s.autoRules[tenantID][r.ID] = &cp
	return nil
}

func (s *MemoryStore) DeleteAutomationRule(ctx context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.autoRules[tenantID]; ok {
		if _, ok := m[id]; ok {
			delete(m, id)
			return nil
		}
	}
	return ErrNotFound
}

// --- 自定义按钮(Memory) ---

func (s *MemoryStore) GetTicketButton(ctx context.Context, tenantID, id string) (*model.TicketButton, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.tktButtons[tenantID]; ok {
		if b, ok := m[id]; ok {
			cp := *b
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) ListTicketButtons(ctx context.Context, tenantID string) ([]*model.TicketButton, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.TicketButton
	for _, b := range s.tktButtons[tenantID] {
		cp := *b
		out = append(out, &cp)
	}
	// sort_order 升序稳定输出
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].SortOrder < out[j-1].SortOrder; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if out == nil {
		out = []*model.TicketButton{}
	}
	return out, nil
}

func (s *MemoryStore) UpsertTicketButton(ctx context.Context, tenantID string, b *model.TicketButton) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.ID == "" {
		b.ID = uuid.New().String()
	}
	b.TenantID = tenantID
	now := time.Now()
	if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	if s.tktButtons[tenantID] == nil {
		s.tktButtons[tenantID] = map[string]*model.TicketButton{}
	}
	cp := *b
	s.tktButtons[tenantID][b.ID] = &cp
	return nil
}

func (s *MemoryStore) DeleteTicketButton(ctx context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.tktButtons[tenantID]; ok {
		if _, ok := m[id]; ok {
			delete(m, id)
			return nil
		}
	}
	return ErrNotFound
}

// --- 状态流转规则(Memory): key = catalogID(""=全局) ---

func (s *MemoryStore) GetStatusFlow(ctx context.Context, tenantID, catalogID string) (*model.StatusFlow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.statusFlows[tenantID]; ok {
		if catalogID != "" {
			if f, ok := m[catalogID]; ok {
				cp := *f
				return &cp, nil
			}
		}
		if f, ok := m[""]; ok {
			cp := *f
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) UpsertStatusFlow(ctx context.Context, tenantID string, f *model.StatusFlow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	f.TenantID = tenantID
	if f.Transitions == nil {
		f.Transitions = []model.TransitionRule{}
	}
	if s.statusFlows[tenantID] == nil {
		s.statusFlows[tenantID] = map[string]*model.StatusFlow{}
	}
	cp := *f
	s.statusFlows[tenantID][f.CatalogID] = &cp
	return nil
}

func (s *MemoryStore) DeleteStatusFlow(ctx context.Context, tenantID, catalogID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.statusFlows[tenantID]; ok {
		if _, ok := m[catalogID]; ok {
			delete(m, catalogID)
			return nil
		}
	}
	return ErrNotFound
}
