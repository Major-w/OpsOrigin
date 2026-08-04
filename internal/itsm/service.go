package itsm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/internal/itsm/risk"
	"github.com/opsorigin/core/internal/itsm/sla"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/internal/itsm/webhook"
	"github.com/opsorigin/core/pkg/config"

	"github.com/opsorigin/core/pkg/httputil"
)

// ProcessEngine — itsm 消费的流程引擎能力：加载流程定义 + 驱动流程实例。
// 由 process.Service 实现（鸭子类型），使工单绑定流程后真正跑起流程实例。
type ProcessEngine interface {
	Get(ctx context.Context, id, tenantID string) (*process.Process, error)
	StartInstance(ctx context.Context, tenantID, processID, ticketID string) (*process.ProcessInstance, *process.Step, error)
	Advance(ctx context.Context, tenantID, ticketID, condition string, output any) (*process.ProcessInstance, *process.Step, error)
	GetInstance(ctx context.Context, ticketID, tenantID string) (*process.ProcessInstance, error)
}

// Notifier 是通知中心能力的极小接口(由 notification.Service 实现, 避免 import 环)。
// ITSM 在工单关键事件(创建/指派/审批/关单/SLA)触发 Policy 分发。
type Notifier interface {
	NotifyEvent(ctx context.Context, tenantID, event, title, content string) error
}

// ITSM 内部事件名, 取值与 notification 包 EventType 对齐, 用于匹配通知 Policy。
const (
	evTicketCreated  = "ticket.created"
	evTicketAssigned = "ticket.assigned"
	evTicketApproved = "ticket.approved"
	evTicketClosed   = "ticket.closed"
	evTicketSubmit   = "ticket.submitted"
	evSLABreached    = "ticket.sla_breached"
	evSLAWarning     = "ticket.sla_warning"
	evTicketReopened = "ticket.reopened"
	evTicketReminded = "ticket.reminded"
)

type Service struct {
	cfg          *config.Config
	db           *sql.DB
	store        store.Store
	risk         *risk.Classifier
	webhook      *webhook.Callback
	processStore ProcessEngine
	slaStore     *sla.Store
	formEngine   *form.Engine
	wsHub        interface{ BroadcastToTenant(interface{}, string) }
	stopCh       chan struct{}
	stopOnce     sync.Once
	// knowledgeArchiver 由 cmd/core 注入: 工单完成后将处置案例沉淀到知识库(nil=关闭)。
	knowledgeArchiver func(ctx context.Context, tenantID, title, content string, tags []string) (string, error)
	// experienceSink 由 cmd/core 注入(P0-1 长期记忆): 工单完成后将处置经验沉淀到 AI 记忆库(nil=关闭)。
	experienceSink func(ctx context.Context, tenantID, agentID, problem, resolution string, outcome bool) error
	// notify 对接通知中心(解耦接口, 避免 import 环): 工单关键事件触发 Policy 分发。
	notify Notifier
}

func NewService(cfg *config.Config, db *sql.DB) *Service {
	var s store.Store
	if db != nil {
		pgStore := store.NewPGStore(db)
		if err := pgStore.Init(context.Background()); err != nil {
			slog.Error("failed to init itsm pg store, falling back to memory", "error", err)
			s = store.NewMemoryStore()
		} else {
			s = pgStore
			slog.Info("itsm: using postgresql store")
		}
	}
	if s == nil {
		s = store.NewMemoryStore()
		slog.Info("itsm: using in-memory store")
	}

	return &Service{
		cfg:        cfg,
		db:         db,
		store:      s,
		risk:       risk.New(),
		webhook:    webhook.New(),
		formEngine: form.NewEngine(s),
		stopCh:     make(chan struct{}),
	}
}

// SetFormEngine 注入可配置表单校验引擎（由 NewService 内部基于 store 构造时可选）。
func (s *Service) SetFormEngine(e *form.Engine) {
	s.formEngine = e
}

// SetSourceResolver 注入表单动态选项解析器（cmdb:* / iam:*），由 cmd/core 在各模块就绪后装配。
func (s *Service) SetSourceResolver(r form.SourceResolver) {
	if s.formEngine != nil {
		s.formEngine.SetSourceResolver(r)
	}
}

func (s *Service) SetWebSocketHub(hub interface{ BroadcastToTenant(interface{}, string) }) {
	s.wsHub = hub
}

func (s *Service) SetProcessStore(ps ProcessEngine) {
	s.processStore = ps
}

// SetSLAStore 注入 SLA 策略存储,使工单截止时间按可配置策略计算
// (而非硬编码 slaForRisk)。
func (s *Service) SetSLAStore(st *sla.Store) {
	s.slaStore = st
}

// SetNotifier 注入通知中心, 使工单关键事件能经 Policy 分发到各渠道(nil=关闭通知)。
func (s *Service) SetNotifier(n Notifier) {
	s.notify = n
}

// notifyTicket 经通知中心分发工单事件(无 notify 注入时静默跳过)。
func (s *Service) notifyTicket(ctx context.Context, tenantID, event, title, content string) {
	if s.notify == nil {
		return
	}
	if err := s.notify.NotifyEvent(ctx, tenantID, event, title, content); err != nil {
		slog.Warn("itsm: notify event failed", "event", event, "error", err)
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/itsm/tickets", s.handleCreate)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}", s.handleGet)
	mux.HandleFunc("GET /api/v1/itsm/tickets", s.handleList)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/process", s.handleGetProcessInstance)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/approve", s.handleApprove)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/reject", s.handleReject)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/execute", s.handleExecute)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/complete", s.handleComplete)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/submit", s.handleSubmit)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/assign", s.handleAssign)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/close", s.handleClose)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/rollback", s.handleRollback)
	// 工单操作补齐: 撤回/重开/挂起恢复/催办/关联/通用更新
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/withdraw", s.handleWithdraw)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/reopen", s.handleReopen)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/hold", s.handleHold)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/resume", s.handleResume)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/remind", s.handleRemind)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/relate", s.handleRelate)
	mux.HandleFunc("PUT /api/v1/itsm/tickets/{id}", s.handleUpdateTicket)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/events", s.handleGetEvents)
	mux.HandleFunc("GET /api/v1/itsm/stats", s.handleStats)
	// Comments
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/comments", s.handleAddComment)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/comments", s.handleListComments)
	mux.HandleFunc("DELETE /api/v1/itsm/tickets/{id}/comments/{cid}", s.handleDeleteComment)
	// Subtasks
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/subtasks", s.handleAddSubtask)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/subtasks", s.handleListSubtasks)
	mux.HandleFunc("PUT /api/v1/itsm/tickets/{id}/subtasks/{sid}", s.handleUpdateSubtask)
	mux.HandleFunc("DELETE /api/v1/itsm/tickets/{id}/subtasks/{sid}", s.handleDeleteSubtask)
	// Attachments
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/attachments", s.handleUploadAttachment)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/attachments", s.handleListAttachments)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/attachments/{aid}", s.handleDownloadAttachment)
	mux.HandleFunc("DELETE /api/v1/itsm/tickets/{id}/attachments/{aid}", s.handleDeleteAttachment)
	// SLA
	mux.HandleFunc("GET /api/v1/itsm/sla/stats", s.handleSLAStats)
	mux.HandleFunc("GET /api/v1/itsm/sla/breached", s.handleSLABreached)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/sla", s.handleTicketSLA)
	// Form schemas (dynamic forms)
	mux.HandleFunc("GET /api/v1/itsm/forms", s.handleListFormScopes)
	mux.HandleFunc("PUT /api/v1/itsm/forms", s.handleUpsertFormSchema)
	// 数据字典
	mux.HandleFunc("GET /api/v1/itsm/form-dicts", s.handleListFieldDicts)
	mux.HandleFunc("POST /api/v1/itsm/form-dicts", s.handleUpsertFieldDict)
	mux.HandleFunc("DELETE /api/v1/itsm/form-dicts/{id}", s.handleDeleteFieldDict)
	mux.HandleFunc("GET /api/v1/itsm/form-options", s.handleFormOptions)
	mux.HandleFunc("PATCH /api/v1/itsm/tickets/{id}/custom-fields", s.handleUpdateCustomFields)
	// Change Management
	mux.HandleFunc("POST /api/v1/itsm/changes", s.handleCreateChange)
	mux.HandleFunc("GET /api/v1/itsm/changes", s.handleListChanges)
	mux.HandleFunc("GET /api/v1/itsm/changes/{id}", s.handleGetChange)
	mux.HandleFunc("POST /api/v1/itsm/changes/{id}/vote", s.handleVoteChange)
	mux.HandleFunc("POST /api/v1/itsm/changes/{id}/execute", s.handleExecuteChange)
	// Import / Export
	mux.HandleFunc("POST /api/v1/itsm/tickets/import", s.handleImportTickets)
	mux.HandleFunc("GET /api/v1/itsm/tickets/export/csv", s.handleExportTicketsCSV)
	mux.HandleFunc("GET /api/v1/itsm/stats/export/csv", s.handleExportStatsCSV)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req model.CreateTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	ticket, err := s.CreateTicket(r.Context(), req)
	if err != nil {
		slog.Error("create ticket failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create ticket")
		return
	}
	writeJSON(w, http.StatusCreated, ticket)
}

// CreateTicket 创建一张工单, 供 HTTP handler 与内部模块(如 AI 巡检发现转单)复用。
// 完整走与 handleCreate 相同的链路: 风险定级 → SLA 截止 → 落库 → webhook → 流程引擎接入。
func (s *Service) CreateTicket(ctx context.Context, req model.CreateTicketRequest) (*model.Ticket, error) {
	tenantID := tenantFromCtx(ctx)
	if req.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	if req.RiskLevel == "" || !req.RiskLevel.Valid() {
		req.RiskLevel = s.risk.Classify(req.Title, req.Description, req.ChangePlan)
	}

	// 可配置表单：按作用域注入默认值并校验自定义字段（无 schema 时跳过，向后兼容）
	scope := req.FormScope
	if scope == "" {
		scope = "ticket:default"
	}
	if req.CustomFields == nil {
		req.CustomFields = map[string]any{}
	}
	if s.formEngine != nil {
		if schema, serr := s.formEngine.ResolveSchema(ctx, tenantID, scope); serr == nil && schema != nil {
			form.MergeDefaults(schema, req.CustomFields)
			if verr := s.formEngine.ValidateValues(ctx, tenantID, schema, req.CustomFields); verr != nil {
				return nil, fmt.Errorf("表单校验失败: %w", verr)
			}
		}
	}

	now := time.Now()
	ticket := &model.Ticket{
		TenantID:      tenantID,
		Title:         req.Title,
		Description:   req.Description,
		RiskLevel:     req.RiskLevel,
		Status:        model.StatusDraft,
		ChangePlan:    req.ChangePlan,
		RollbackPlan:  req.RollbackPlan,
		CreatedBy:     req.CreatedBy,
		CreatedByType: req.CreatedByType,
		WebhookURL:    req.WebhookURL,
		Labels:        req.Labels,
		CIIDs:         req.CIIDs,
		CustomFields:  req.CustomFields,
		TraceID:       traceIDFromCtx(ctx),
		ScheduledAt:   req.ScheduledAt,
		DeadlineAt:    req.DeadlineAt,
	}
	if ticket.DeadlineAt == nil {
		if s.slaStore != nil {
			// 流程级 SLA 优先(process_id 绑定), 否则回退风险级
			var pol *sla.Policy
			if req.ProcessID != "" {
				pol, _ = s.slaStore.GetByProcessID(ctx, tenantID, req.ProcessID)
			}
			if pol == nil {
				pol, _ = s.slaStore.GetByRisk(ctx, tenantID, string(req.RiskLevel))
			}
			if pol != nil && pol.ResolutionHours > 0 {
				deadline := now.Add(time.Duration(pol.ResolutionHours) * time.Hour)
				ticket.DeadlineAt = &deadline
			} else if d := s.slaForRisk(req.RiskLevel); d > 0 {
				deadline := now.Add(d)
				ticket.DeadlineAt = &deadline
			}
		} else if d := s.slaForRisk(req.RiskLevel); d > 0 {
			deadline := now.Add(d)
			ticket.DeadlineAt = &deadline
		}
	}

	if err := s.store.Create(ctx, ticket); err != nil {
		return nil, err
	}

	s.fireWebhook(ctx, "ticket.created", ticket, req.CreatedBy)
	s.notifyTicket(ctx, tenantID, evTicketCreated, "新工单: "+ticket.Title, fmt.Sprintf("风险: %s / 状态: %s", ticket.RiskLevel, ticket.Status))

	// 流程引擎接入：绑定了流程模板则创建流程实例并推进到首个真实步骤，
	// 由落点步骤类型映射工单初始状态（流程优先于基于风险的自动流转）。
	processGoverned := false
	if req.ProcessID != "" && s.processStore != nil {
		_, landing, perr := s.processStore.StartInstance(ctx, tenantID, req.ProcessID, ticket.ID)
		if perr != nil {
			slog.Warn("start process instance failed", "process_id", req.ProcessID, "ticket_id", ticket.ID, "error", perr)
		} else {
			processGoverned = true
			ticket.ProcessID = req.ProcessID
			prev := ticket.Status
			if landing != nil {
				if mapped := process.StepTypeToTicketStatus(landing); mapped != "" {
					ticket.Status = model.TicketStatus(mapped)
				}
			}
			if err := s.store.Update(ctx, ticket); err != nil {
				slog.Error("persist ticket process binding failed", "error", err)
			}
			if prev != ticket.Status {
				s.recordEvent(ctx, ticket, prev, ticket.Status, req.CreatedBy, "process instance started")
			}
		}
	}

	if !processGoverned && req.RiskLevel.RequiresApproval() {
		if err := s.transition(ctx, ticket, model.StatusPendingReview, req.CreatedBy); err != nil {
			slog.Error("auto-transition to pending_review failed", "error", err)
		}
	}

	return ticket, nil
}

// SetKnowledgeArchiver 注入工单完成后的知识库沉淀回调(由 cmd/core 装配)。
// 仅对来源为 ai_inspection 的工单生效, 且同一工单只沉淀一次(幂等, 通过 labels.kb_archived 标记)。
func (s *Service) SetKnowledgeArchiver(f func(ctx context.Context, tenantID, title, content string, tags []string) (string, error)) {
	s.knowledgeArchiver = f
}

// SetExperienceSink 注入 AI 长期记忆回调(P0-1): 工单成功完成后, 若为 AI 巡检来源且未标记
// memory_archived, 把"问题+处置结果"沉淀为 Agent 记忆(nil=关闭)。
func (s *Service) SetExperienceSink(f func(ctx context.Context, tenantID, agentID, problem, resolution string, outcome bool) error) {
	s.experienceSink = f
}

// archiveTicketToKnowledge 工单成功完成后, 将 AI 巡检产生的处置案例自动沉淀到知识库,
// 供后续 AI 检索复用(持续改进闭环: 发现 → 处置 → 沉淀 → 复用)。
func (s *Service) archiveTicketToKnowledge(ctx context.Context, ticket *model.Ticket, resultComment string) {
	if s.knowledgeArchiver == nil {
		return
	}
	if ticket.Labels == nil || ticket.Labels["source"] != "ai_inspection" {
		return // 仅沉淀 AI 巡检来源的工单
	}
	if ticket.Labels["kb_archived"] == "true" {
		return // 幂等: 已沉淀过
	}

	title := fmt.Sprintf("[处置案例] %s", ticket.Title)
	content := "来源: 主动巡检发现并转工单处置\n"
	if ticket.Description != "" {
		content += "\n【问题描述】\n" + ticket.Description
	}
	content += "\n【风险级别】" + string(ticket.RiskLevel)
	if resultComment != "" {
		content += "\n【处置结果】\n" + resultComment
	}
	if len(ticket.CIIDs) > 0 {
		content += "\n【关联资产】" + strings.Join(ticket.CIIDs, ", ")
	}

	articleID, err := s.knowledgeArchiver(ctx, ticket.TenantID, title, content,
		[]string{"ai_inspection", "disposal-case", string(ticket.RiskLevel)})
	if err != nil {
		slog.Warn("archive ticket to knowledge failed", "ticket_id", ticket.ID, "error", err)
		return
	}
	// 写回标记, 保证同一工单不重复沉淀
	if ticket.Labels == nil {
		ticket.Labels = map[string]string{}
	}
	ticket.Labels["kb_archived"] = "true"
	ticket.Labels["kb_article_id"] = articleID
	if err := s.store.Update(ctx, ticket); err != nil {
		slog.Warn("persist ticket kb_archived label failed", "ticket_id", ticket.ID, "error", err)
		return
	}
	slog.Info("ticket archived to knowledge", "ticket_id", ticket.ID, "article_id", articleID)
	s.rememberTicketExperience(ctx, ticket, resultComment)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ticket)
}

// TicketMetricsBySource 按工单 labels 中 source 字段过滤统计(供 AI 数字员工运营效果评估)。
// 返回: 工单总数、按状态分布、平均处置时长(分钟, 基于 succeeded 工单 completed_at-created_at)。
// source 为空时统计该租户全部工单。db 为 nil(纯内存模式)时返回零值。
// TicketMetricsBySource 按工单 labels 中 source 字段过滤统计(供 AI 数字员工运营效果评估)。
// 返回: 工单总数、按状态分布、平均处置时长(分钟, 基于 succeeded 工单 completed_at-created_at)、
// 处置效果评估分布(confirmed/false_positive, 取自 labels.ai_assessment)。
// source 为空时统计该租户全部工单。
func (s *Service) TicketMetricsBySource(ctx context.Context, tenantID, source string) (int, map[string]int, float64, map[string]int, error) {
	if s.db == nil {
		return 0, map[string]int{}, 0, map[string]int{}, nil
	}
	query := `SELECT status, completed_at, created_at, labels::jsonb->>'ai_assessment' FROM itsm_tickets WHERE tenant_id=$1`
	args := []any{tenantID}
	if source != "" {
		query += ` AND labels::jsonb->>'source'=$2`
		args = append(args, source)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, nil, 0, nil, err
	}
	defer rows.Close()

	byStatus := map[string]int{}
	byAssessment := map[string]int{}
	total := 0
	var totalResolutionMin float64
	var resolvedCount int
	for rows.Next() {
		var status string
		var completedAt, createdAt sql.NullTime
		var assessment sql.NullString
		if err := rows.Scan(&status, &completedAt, &createdAt, &assessment); err != nil {
			return 0, nil, 0, nil, err
		}
		total++
		byStatus[status]++
		if assessment.Valid && assessment.String != "" {
			byAssessment[assessment.String]++
		}
		if status == string(model.StatusSucceeded) && completedAt.Valid && createdAt.Valid && completedAt.Time.After(createdAt.Time) {
			totalResolutionMin += completedAt.Time.Sub(createdAt.Time).Minutes()
			resolvedCount++
		}
	}
	var avg float64
	if resolvedCount > 0 {
		avg = totalResolutionMin / float64(resolvedCount)
	}
	return total, byStatus, avg, byAssessment, rows.Err()
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	status := model.TicketStatus(r.URL.Query().Get("status"))

	tickets, err := s.store.List(r.Context(), tenantID, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tickets == nil {
		tickets = []*model.Ticket{}
	}

	writeJSON(w, http.StatusOK, tickets)
}

// stepVoter 是可选的会签投票能力接口(由 process.Service 实现, 可选能力不破坏既有 ProcessEngine)。
type stepVoter interface {
	ProcessEngine
	VoteStep(ctx context.Context, tenantID, ticketID, stepID, approver string, approved bool) (allApproved bool, err error)
}

// currentVoteStep 返回工单当前步骤中需要会签的 user 步骤(Approvers 非空), 否则 nil。
func (s *Service) currentVoteStep(ctx context.Context, ticket *model.Ticket) *process.Step {
	if s.processStore == nil || ticket.ProcessID == "" {
		return nil
	}
	inst, err := s.processStore.GetInstance(ctx, ticket.ID, ticket.TenantID)
	if err != nil || inst == nil || inst.CurrentStep == "" {
		return nil
	}
	proc, err := s.processStore.Get(ctx, inst.ProcessID, ticket.TenantID)
	if err != nil || proc == nil {
		return nil
	}
	for i := range proc.Steps {
		st := &proc.Steps[i]
		if st.ID == inst.CurrentStep && st.Type == process.StepUser && len(st.Approvers) > 0 {
			return st
		}
	}
	return nil
}

func (s *Service) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.ApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusApproved); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	// 会签: 当前步骤多审批人时先记录投票, 未全票通过则保持待审批(不推进状态/流程)。
	if voter, ok := s.processStore.(stepVoter); ok {
		if step := s.currentVoteStep(r.Context(), ticket); step != nil {
			all, err := voter.VoteStep(r.Context(), ticket.TenantID, ticket.ID, step.ID, req.ApprovedBy, true)
			if err == nil && !all {
				writeJSON(w, http.StatusOK, map[string]any{
					"status":  "waiting",
					"message": "已记录审批意见, 等待其余审批人",
					"ticket":  ticket,
				})
				return
			}
		}
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusApproved
	ticket.ApprovedBy = req.ApprovedBy
	now := time.Now()
	ticket.ApprovedAt = &now

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusApproved, req.ApprovedBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.approved", ticket, req.ApprovedBy)
	s.notifyTicket(r.Context(), tenantID, evTicketApproved, "工单已审批通过: "+ticket.Title, fmt.Sprintf("审批人: %s", req.ApprovedBy))
	s.advanceProcess(r.Context(), ticket, "success")
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.RejectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusRejected); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusRejected
	ticket.RejectReason = req.Reason

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusRejected, req.RejectedBy, req.Reason)
	s.fireWebhook(r.Context(), "ticket.rejected", ticket, req.RejectedBy)
	s.advanceProcess(r.Context(), ticket, "failure")
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleExecute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if ticket.RiskLevel.RequiresApproval() && ticket.Status != model.StatusApproved {
		writeError(w, http.StatusForbidden, model.ErrRiskRequiresApproval.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusExecuting); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusExecuting
	now := time.Now()
	ticket.ExecutedAt = &now

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusExecuting, req.ExecutedBy, req.Result)
	s.fireWebhook(r.Context(), "ticket.executing", ticket, req.ExecutedBy)
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleComplete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.CompleteRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.CompletedBy == "" {
		req.CompletedBy = "system"
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusSucceeded); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusSucceeded
	ticket.CompletedBy = req.CompletedBy
	now := time.Now()
	ticket.CompletedAt = &now

	// 处置效果评估: AI 工单(巡检/接管)完成时可标记确认有效或误报, 写入 labels 供运营效果统计。
	if req.Assessment == model.AssessmentConfirmed || req.Assessment == model.AssessmentFalsePositive {
		if ticket.Labels == nil {
			ticket.Labels = map[string]string{}
		}
		ticket.Labels["ai_assessment"] = req.Assessment
	}

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusSucceeded, req.CompletedBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.completed", ticket, req.CompletedBy)
	s.advanceProcess(r.Context(), ticket, "success")
	s.broadcastTicketUpdate(ticket)
	s.archiveTicketToKnowledge(r.Context(), ticket, req.Comment)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.RollbackRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.RolledBackBy == "" {
		req.RolledBackBy = "system"
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusRolledBack); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusRolledBack
	ticket.RolledBackBy = req.RolledBackBy
	ticket.RollbackReason = req.RollbackReason
	now := time.Now()
	ticket.RolledBackAt = &now

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusRolledBack, req.RolledBackBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.rolled_back", ticket, req.RolledBackBy)
	s.advanceProcess(r.Context(), ticket, "failure")
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleSubmit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.SubmitRequest
	json.NewDecoder(r.Body).Decode(&req)

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if model.IsTerminal(ticket.Status) {
		writeError(w, http.StatusConflict, model.ErrTicketClosed.Error())
		return
	}

	// 高风险走审批, 低/中风险直接进执行(避免 draft 死锁)。
	target := model.StatusPendingReview
	if !ticket.RiskLevel.RequiresApproval() {
		target = model.StatusExecuting
	}
	if err := model.CanTransition(ticket.Status, target); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = target

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, target, req.SubmittedBy, "提交工单")
	s.fireWebhook(r.Context(), "ticket.submitted", ticket, req.SubmittedBy)
	s.advanceProcess(r.Context(), ticket, "success")
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleAssign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.AssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Assignee == "" {
		writeError(w, http.StatusBadRequest, "assignee is required")
		return
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if model.IsTerminal(ticket.Status) {
		writeError(w, http.StatusConflict, model.ErrTicketClosed.Error())
		return
	}

	previousStatus := ticket.Status
	now := time.Now()
	ticket.Assignee = req.Assignee
	ticket.AssignedAt = &now

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, ticket.Status, req.AssignedBy, "指派给 "+req.Assignee)
	s.notifyTicket(r.Context(), tenantID, evTicketAssigned, "工单已指派: "+ticket.Title, fmt.Sprintf("负责人: %s", req.Assignee))
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.CloseRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.ClosedBy == "" {
		req.ClosedBy = "system"
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := model.CanTransition(ticket.Status, model.StatusClosed); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusClosed
	if req.Satisfaction > 0 {
		ticket.Satisfaction = req.Satisfaction
	}

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	// 子任务联动: 主工单关闭时级联关闭所有未完成子任务(记完成时间)。
	if subs, serr := s.store.ListSubtasks(r.Context(), id, tenantID); serr == nil {
		now := time.Now()
		for _, sub := range subs {
			if sub.Status == model.SubtaskDone {
				continue
			}
			sub.Status = model.SubtaskDone
			sub.CompletedAt = &now
			_ = s.store.UpdateSubtask(r.Context(), sub)
		}
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusClosed, req.ClosedBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.closed", ticket, req.ClosedBy)
	s.notifyTicket(r.Context(), tenantID, evTicketClosed, "工单已关闭: "+ticket.Title, req.Comment)
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req model.WithdrawRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.WithdrawnBy == "" {
		req.WithdrawnBy = "system"
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if err := model.CanTransition(ticket.Status, model.StatusWithdrawn); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	prev := ticket.Status
	ticket.Status = model.StatusWithdrawn
	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, prev, model.StatusWithdrawn, req.WithdrawnBy, req.Reason)
	s.fireWebhook(r.Context(), "ticket.withdrawn", ticket, req.WithdrawnBy)
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleReopen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req model.ReopenRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.ReopenedBy == "" {
		req.ReopenedBy = "system"
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if err := model.CanTransition(ticket.Status, model.StatusReopened); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	prev := ticket.Status
	ticket.Status = model.StatusReopened
	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, prev, model.StatusReopened, req.ReopenedBy, req.Reason)
	s.fireWebhook(r.Context(), "ticket.reopened", ticket, req.ReopenedBy)
	s.notifyTicket(r.Context(), tenantID, evTicketReopened, "工单重开: "+ticket.Title, req.Reason)
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleHold(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req model.HoldRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.HoldBy == "" {
		req.HoldBy = "system"
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if model.IsTerminal(ticket.Status) {
		writeError(w, http.StatusConflict, "terminal ticket cannot be held")
		return
	}
	ticket.OnHold = true
	ticket.HoldReason = req.Reason
	ticket.HoldUntilAt = req.HoldUntilAt
	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, ticket.Status, ticket.Status, req.HoldBy, "挂起: "+req.Reason)
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		ResumedBy string `json:"resumed_by"`
		Reason    string `json:"reason,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ResumedBy == "" {
		req.ResumedBy = "system"
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if !ticket.OnHold {
		writeError(w, http.StatusConflict, "ticket is not on hold")
		return
	}
	ticket.OnHold = false
	ticket.HoldReason = ""
	ticket.HoldUntilAt = nil
	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, ticket.Status, ticket.Status, req.ResumedBy, "恢复: "+req.Reason)
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleRemind(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req model.RemindRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.RemindedBy == "" {
		req.RemindedBy = "system"
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	content := fmt.Sprintf("负责人: %s\n催办人: %s", ticket.Assignee, req.RemindedBy)
	if req.Message != "" {
		content += "\n" + req.Message
	}
	s.notifyTicket(r.Context(), tenantID, evTicketReminded, "工单催办: "+ticket.Title, content)
	s.recordEvent(r.Context(), ticket, ticket.Status, ticket.Status, req.RemindedBy, "催办: "+req.Message)
	writeJSON(w, http.StatusOK, map[string]string{"status": "reminded"})
}

func (s *Service) handleRelate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req model.RelateRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.RelatedID == "" {
		writeError(w, http.StatusBadRequest, "related_id is required")
		return
	}
	if req.RelatedID == id {
		writeError(w, http.StatusBadRequest, "cannot relate to itself")
		return
	}
	a, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	b, err := s.store.Get(r.Context(), req.RelatedID, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "related ticket not found")
		return
	}
	if !containsStr(a.RelatedTicketIDs, b.ID) {
		a.RelatedTicketIDs = append(a.RelatedTicketIDs, b.ID)
	}
	if !containsStr(b.RelatedTicketIDs, a.ID) {
		b.RelatedTicketIDs = append(b.RelatedTicketIDs, a.ID)
	}
	if err := s.store.Update(r.Context(), a); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	_ = s.store.Update(r.Context(), b)
	s.recordEvent(r.Context(), a, a.Status, a.Status, req.RelateBy, "关联工单: "+b.ID)
	s.broadcastTicketUpdate(a)
	writeJSON(w, http.StatusOK, a)
}

func (s *Service) handleUpdateTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req model.UpdateTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	prev := ticket.Status
	if req.Title != "" {
		ticket.Title = req.Title
	}
	if req.Description != "" {
		ticket.Description = req.Description
	}
	if req.RiskLevel != "" && req.RiskLevel.Valid() {
		ticket.RiskLevel = req.RiskLevel
	}
	if req.Assignee != "" {
		ticket.Assignee = req.Assignee
		now := time.Now()
		ticket.AssignedAt = &now
	}
	if req.Labels != nil {
		ticket.Labels = req.Labels
	}
	if req.CIIDs != nil {
		ticket.CIIDs = req.CIIDs
	}
	if req.ChangePlan != "" {
		ticket.ChangePlan = req.ChangePlan
	}
	if req.RollbackPlan != "" {
		ticket.RollbackPlan = req.RollbackPlan
	}
	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, prev, ticket.Status, req.Assignee, "更新工单信息")
	s.broadcastTicketUpdate(ticket)
	writeJSON(w, http.StatusOK, ticket)
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (s *Service) transition(ctx context.Context, ticket *model.Ticket, to model.TicketStatus, changedBy string) error {
	if err := model.CanTransition(ticket.Status, to); err != nil {
		return err
	}
	ticket.Status = to
	return s.store.Update(ctx, ticket)
}

// advanceProcess 若工单绑定了流程实例，则按 condition(success/failure) 推进流程引擎——
// 记录当前步骤结果并落到下一步，并将落点步骤类型映射回工单状态(流程真正驱动工单)。
// 无实例(未绑定流程)时静默跳过，不影响普通工单。
func (s *Service) advanceProcess(ctx context.Context, ticket *model.Ticket, condition string) {
	if s.processStore == nil || ticket == nil || ticket.ProcessID == "" {
		return
	}
	_, next, err := s.processStore.Advance(ctx, ticket.TenantID, ticket.ID, condition, map[string]any{
		"status":          string(ticket.Status),
		"risk_level":      string(ticket.RiskLevel),
		"created_by_type": ticket.CreatedByType,
		"assignee":        ticket.Assignee,
		"custom_fields":   ticket.CustomFields,
	})
	if err != nil {
		slog.Warn("advance process instance failed", "ticket_id", ticket.ID, "condition", condition, "error", err)
		return
	}
	if next == nil {
		return // 流程结束, 工单状态由 handler 已设置(succeeded 等)
	}
	mapped := process.StepTypeToTicketStatus(next)
	if mapped == "" || model.TicketStatus(mapped) == ticket.Status {
		return
	}
	prev := ticket.Status
	ticket.Status = model.TicketStatus(mapped)
	if err := s.store.Update(ctx, ticket); err != nil {
		slog.Error("itsm: persist process-driven status failed", "ticket_id", ticket.ID, "error", err)
		return
	}
	s.recordEvent(ctx, ticket, prev, ticket.Status, "process", "流程推进: "+string(next.Type))
}

// handleGetProcessInstance 返回工单当前流程实例进度（当前步骤/状态/各步骤结果）。
func (s *Service) handleGetProcessInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if s.processStore == nil {
		writeError(w, http.StatusNotImplemented, "process engine not configured")
		return
	}
	inst, err := s.processStore.GetInstance(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if inst == nil {
		writeError(w, http.StatusNotFound, "no process instance for ticket")
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *Service) handleGetEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}

	events, err := s.store.GetEvents(r.Context(), ticket.ID, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []*model.TicketEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Service) handleStats(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	stats, err := s.store.GetStats(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Service) handleAddComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req struct {
		Author  string `json:"author"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Author == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "author and content are required")
		return
	}

	_, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	comment := &model.Comment{
		TicketID: id,
		TenantID: tenantID,
		Author:   req.Author,
		Content:  req.Content,
	}
	if err := s.store.AddComment(r.Context(), comment); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add comment")
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

// AddTicketComment 以指定作者追加评论(供邮件入站等外部通道调用)。
func (s *Service) AddTicketComment(ctx context.Context, tenantID, ticketID, author, body string) error {
	tk, err := s.store.Get(ctx, ticketID, tenantID)
	if err != nil {
		return err
	}
	c := &model.Comment{TicketID: ticketID, TenantID: tenantID, Author: author, Content: body}
	if err := s.store.AddComment(ctx, c); err != nil {
		return err
	}
	s.broadcastTicketUpdate(tk)
	return nil
}

func (s *Service) handleListComments(w http.ResponseWriter, r *http.Request) {	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	comments, err := s.store.ListComments(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if comments == nil {
		comments = []*model.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}

func (s *Service) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cidStr := r.PathValue("cid")
	tenantID := tenantFromCtx(r.Context())

	cid, err := strconv.ParseInt(cidStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid comment id")
		return
	}

	if err := s.store.DeleteComment(r.Context(), cid, id, tenantID); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "comment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Subtasks ---

func (s *Service) handleAddSubtask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.CreateSubtaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.CreatedBy == "" {
		req.CreatedBy = "admin"
	}

	st := &model.Subtask{
		TicketID:    id,
		TenantID:    tenantID,
		Title:       req.Title,
		Description: req.Description,
		Assignee:    req.Assignee,
		Status:      model.SubtaskPending,
		DueAt:       req.DueAt,
		CreatedBy:   req.CreatedBy,
	}
	// 主单字段同步: 按 sync_fields 从主工单拷贝字段值到子任务 meta(系统字段/自定义字段均可)。
	if len(req.SyncFields) > 0 {
		parent, err := s.store.Get(r.Context(), id, tenantID)
		if err == nil && parent != nil {
			meta := map[string]any{}
			for _, key := range req.SyncFields {
				fieldKey := strings.TrimPrefix(key, "custom_fields.")
				if v, ok := parent.CustomFields[fieldKey]; ok {
					meta[key] = v
					continue
				}
				switch fieldKey {
				case "risk_level":
					meta[key] = string(parent.RiskLevel)
				case "title":
					meta[key] = parent.Title
				case "description":
					meta[key] = parent.Description
				case "assignee":
					meta[key] = parent.Assignee
				case "status":
					meta[key] = string(parent.Status)
				case "deadline_at":
					if parent.DeadlineAt != nil {
						meta[key] = parent.DeadlineAt.Format(time.RFC3339)
					}
				default:
					if v, ok := parent.CustomFields[fieldKey]; ok {
						meta[key] = v
					}
				}
			}
			st.Meta = meta
		}
	}
	if err := s.store.CreateSubtask(r.Context(), st); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create subtask")
		return
	}
	writeJSON(w, http.StatusCreated, st)
}

func (s *Service) handleListSubtasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	subtasks, err := s.store.ListSubtasks(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if subtasks == nil {
		subtasks = []*model.Subtask{}
	}
	writeJSON(w, http.StatusOK, subtasks)
}

func (s *Service) handleUpdateSubtask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sidStr := r.PathValue("sid")
	tenantID := tenantFromCtx(r.Context())

	sid, err := strconv.ParseInt(sidStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid subtask id")
		return
	}

	var req model.UpdateSubtaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 先取原记录, 未传字段保持原值
	subtasks, err := s.store.ListSubtasks(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var cur *model.Subtask
	for _, st := range subtasks {
		if st.ID == sid {
			cur = st
			break
		}
	}
	if cur == nil {
		writeError(w, http.StatusNotFound, "subtask not found")
		return
	}

	// 值拷贝后再更新: 避免直接改 store 返回的内部引用, 使 store 能判断"从非 done 变 done"以记录完成时间
	updated := *cur
	if req.Title != "" {
		updated.Title = req.Title
	}
	if req.Description != "" {
		updated.Description = req.Description
	}
	if req.Assignee != "" {
		updated.Assignee = req.Assignee
	}
	if req.Status != "" {
		switch req.Status {
		case model.SubtaskPending, model.SubtaskInProgress, model.SubtaskDone, model.SubtaskBlocked:
			updated.Status = req.Status
		default:
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
	}
	if req.DueAt != nil {
		updated.DueAt = req.DueAt
	}

	if err := s.store.UpdateSubtask(r.Context(), &updated); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "subtask not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &updated)
}

func (s *Service) handleDeleteSubtask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sidStr := r.PathValue("sid")
	tenantID := tenantFromCtx(r.Context())

	sid, err := strconv.ParseInt(sidStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid subtask id")
		return
	}

	if err := s.store.DeleteSubtask(r.Context(), sid, id, tenantID); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "subtask not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	uploadedBy := r.FormValue("uploaded_by")
	if uploadedBy == "" {
		uploadedBy = "anonymous"
	}

	att := &model.Attachment{
		TicketID:   id,
		TenantID:   tenantID,
		FileName:   header.Filename,
		FileSize:   header.Size,
		FileType:   header.Header.Get("Content-Type"),
		UploadedBy: uploadedBy,
		Data:       data,
	}
	if err := s.store.AddAttachment(r.Context(), att); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save attachment")
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

func (s *Service) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	atts, err := s.store.ListAttachments(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if atts == nil {
		atts = []*model.Attachment{}
	}
	writeJSON(w, http.StatusOK, atts)
}

func (s *Service) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	aidStr := r.PathValue("aid")
	tenantID := tenantFromCtx(r.Context())

	aid, err := strconv.ParseInt(aidStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}

	att, err := s.store.GetAttachment(r.Context(), aid, id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "attachment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=\""+att.FileName+"\"")
	if att.FileType != "" {
		w.Header().Set("Content-Type", att.FileType)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(att.FileSize, 10))
	w.Write(att.Data)
}

func (s *Service) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	aidStr := r.PathValue("aid")
	tenantID := tenantFromCtx(r.Context())

	aid, err := strconv.ParseInt(aidStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}

	if err := s.store.DeleteAttachment(r.Context(), aid, id, tenantID); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "attachment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) recordEvent(ctx context.Context, ticket *model.Ticket, from, to model.TicketStatus, changedBy string, comment string) {
	event := &model.TicketEvent{
		TicketID:   ticket.ID,
		FromStatus: from,
		ToStatus:   to,
		ChangedBy:  changedBy,
		Comment:    comment,
	}
	if err := s.store.AddEvent(ctx, event); err != nil {
		slog.Error("record ticket event failed", "error", err)
	}
}

func (s *Service) broadcastTicketUpdate(ticket *model.Ticket) {
	if s.wsHub != nil {
		s.wsHub.BroadcastToTenant(map[string]any{
			"type":    "itsm",
			"payload": ticket,
			"time":    time.Now().UnixMilli(),
		}, ticket.TenantID)
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

func (s *Service) fireWebhook(ctx context.Context, event string, ticket *model.Ticket, changedBy string) {
	// 异步 goroutine 前快照字段: ticket 指针会在调用方后续状态流转中被写
	// (如 transition 修改 Status), 直接引用会与主 goroutine 产生数据竞争。
	webhookURL := ticket.WebhookURL
	id := ticket.ID
	tenantID := ticket.TenantID
	status := string(ticket.Status)
	riskLevel := string(ticket.RiskLevel)
	go func() {
		if err := s.webhook.Notify(ctx, webhookURL, event,
			id, tenantID, status, riskLevel, changedBy); err != nil {
			slog.Warn("webhook notify failed", "event", event, "ticket_id", id, "error", err)
		}
	}()
}

// --- SLA Checker ---

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

		// SLA breached
		if now.After(deadline) && !t.SLABreached {
			t.SLABreached = true
			t.SLABreachedAt = &now
			t.SLAEscalationLevel = 2
			// 升级策略落地: 命中策略且配置 EscalationTo 时自动转派(当前无负责人或负责人非升级角色)。
			if pol := s.slaPolicyForTicket(ctx, tenantID, t); pol != nil && pol.EscalationTo != "" && t.Assignee != pol.EscalationTo {
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

		// SLA warning at 80%
		if totalDuration > 0 && elapsed.Seconds()/totalDuration.Seconds() >= 0.8 && !t.SLAWarningNotified && !t.SLABreached {
			t.SLAWarningNotified = true
			t.SLAEscalationLevel = 1
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
	} else {
		content := fmt.Sprintf("工单即将达到 SLA 截止时间(剩余<20%%)\n负责人: %s\n升级至: %s", t.Assignee, escalation)
		s.notifyTicket(ctx, tenantID, evSLAWarning, "SLA 预警: "+t.Title, content)
	}
}

func (s *Service) handleSLAStats(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	stats, err := s.store.GetSLAStats(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Service) handleSLABreached(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	tickets, err := s.store.List(r.Context(), tenantID, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, breached)
}

func (s *Service) handleTicketSLA(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
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
	writeJSON(w, http.StatusOK, result)
}

// --- Form schema handlers (dynamic forms) ---

// handleListFormScopes 列出已配置的作用域；若带 scope 查询参数则返回该作用域的 schema（无则 schema 为 null）。
func (s *Service) handleListFormScopes(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	scope := r.URL.Query().Get("scope")
	if scope != "" {
		schema, err := s.store.GetFormSchema(r.Context(), tenantID, scope)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.expandSchemaDicts(r.Context(), tenantID, schema)
		writeJSON(w, http.StatusOK, map[string]any{"scope": scope, "schema": schema})
		return
	}
	scopes, err := s.store.ListFormScopes(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if scopes == nil {
		scopes = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"scopes": scopes})
}

// handleUpsertFormSchema 按 scope 写入/更新一张表单 schema（管理端可配）。
func (s *Service) handleUpsertFormSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope  string          `json:"scope"`
		Schema form.FormSchema `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Scope == "" {
		req.Scope = req.Schema.Scope
	}
	if req.Scope == "" {
		writeError(w, http.StatusBadRequest, "scope is required")
		return
	}
	req.Schema.Scope = req.Scope
	if req.Schema.Version < 1 {
		req.Schema.Version = 1
	}
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.UpsertFormSchema(r.Context(), tenantID, req.Scope, req.Schema); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "scope": req.Scope})
}

// expandSchemaDicts 展开 schema 中的 dict_ref 引用: 从数据字典拉取字段定义替换引用字段。
func (s *Service) expandSchemaDicts(ctx context.Context, tenantID string, schema *form.FormSchema) {
	if schema == nil {
		return
	}
	needExpand := false
	for _, f := range schema.Fields {
		if f.DictRef != "" {
			needExpand = true
			break
		}
	}
	if !needExpand {
		return
	}
	dicts := map[string]*form.FieldDict{}
	if list, err := s.store.ListFieldDicts(ctx, tenantID); err == nil {
		for _, d := range list {
			dicts[d.ID] = d
		}
	}
	form.ExpandDictRefs(schema, dicts)
}

// --- 数据字典 handlers ---

func (s *Service) handleListFieldDicts(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	dicts, err := s.store.ListFieldDicts(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if dicts == nil {
		dicts = []*form.FieldDict{}
	}
	writeJSON(w, http.StatusOK, dicts)
}

func (s *Service) handleUpsertFieldDict(w http.ResponseWriter, r *http.Request) {
	var dict form.FieldDict
	if err := json.NewDecoder(r.Body).Decode(&dict); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if dict.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.UpsertFieldDict(r.Context(), tenantID, &dict); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dict)
}

func (s *Service) handleDeleteFieldDict(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.DeleteFieldDict(r.Context(), tenantID, id); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "dict not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleFormOptions 解析表单字段的动态选项来源（cmdb:<type> / iam:user|role|group / cmdb:types）。
// 不带 source 参数时返回本租户可用的 source 清单，供表单配置界面下拉选择。
// keyword 用于前端下拉搜索时做服务端过滤，limit 限制返回条数（默认 200）。
// 带 parent 参数（可为空串）时进入层级模式：返回该父节点下的子选项，供 cascader 级联逐级展开。
func (s *Service) handleFormOptions(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	if s.formEngine == nil || s.formEngine.SourceResolver() == nil {
		writeJSON(w, http.StatusOK, map[string]any{"options": []form.FieldOption{}, "sources": []string{}})
		return
	}
	resolver := s.formEngine.SourceResolver()
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source == "" {
		sources := []string{}
		if c, ok := resolver.(sourceCataloger); ok {
			sources = c.SourceCatalog(r.Context(), tenantID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
		return
	}
	// 层级模式：parent 参数存在即按树取子节点（parent 为空取根层级）
	if r.URL.Query().Has("parent") {
		tr, ok := resolver.(form.TreeSourceResolver)
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"source": source, "options": []form.FieldOption{}})
			return
		}
		parent := strings.TrimSpace(r.URL.Query().Get("parent"))
		children, err := tr.ResolveTree(r.Context(), tenantID, source, parent)
		if err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("解析选项来源失败: %v", err))
			return
		}
		if children == nil {
			children = []form.FieldOption{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"source": source, "parent": parent, "options": children})
		return
	}
	opts, err := resolver.Resolve(r.Context(), tenantID, source)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("解析选项来源失败: %v", err))
		return
	}
	if kw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword"))); kw != "" {
		filtered := make([]form.FieldOption, 0, len(opts))
		for _, o := range opts {
			if strings.Contains(strings.ToLower(o.Label), kw) || strings.Contains(strings.ToLower(o.Value), kw) {
				filtered = append(filtered, o)
			}
		}
		opts = filtered
	}
	limit := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	if len(opts) > limit {
		opts = opts[:limit]
	}
	if opts == nil {
		opts = []form.FieldOption{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "options": opts})
}

// handleUpdateCustomFields 按作用域更新工单自定义字段（流程步骤级表单回写）。
// 若给定 scope 存在 schema，则先注入默认值并校验，再合并进 ticket.custom_fields；无 schema 直接合并（向后兼容）。
func (s *Service) handleUpdateCustomFields(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		Scope  string         `json:"scope"`
		Fields map[string]any `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if ticket.CustomFields == nil {
		ticket.CustomFields = map[string]any{}
	}
	if req.Fields == nil {
		req.Fields = map[string]any{}
	}
	if req.Scope != "" && s.formEngine != nil {
		if schema, serr := s.formEngine.ResolveSchema(r.Context(), tenantID, req.Scope); serr == nil && schema != nil {
			form.MergeDefaults(schema, req.Fields)
			if verr := s.formEngine.ValidateValues(r.Context(), tenantID, schema, req.Fields); verr != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("表单校验失败: %v", verr))
				return
			}
		}
	}
	for k, v := range req.Fields {
		ticket.CustomFields[k] = v
	}
	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 步骤表单保存后推进流程: 若当前步骤下游是网关(表达式条件), 基于刚填写的表单字段选路;
	// 下游若是 service 步骤则自动执行脚本。无流程绑定时静默跳过。
	if ticket.ProcessID != "" && s.processStore != nil {
		s.advanceProcess(r.Context(), ticket, "success")
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "custom_fields": ticket.CustomFields})
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "已超时"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 24 {
		days := h / 24
		h = h % 24
		return fmt.Sprintf("%dd %dh %dm", days, h, m)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// --- Change Management Handlers ---

func (s *Service) handleCreateChange(w http.ResponseWriter, r *http.Request) {
	var req model.CreateChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	tenantID := tenantFromCtx(r.Context())
	if req.RiskLevel == "" || !req.RiskLevel.Valid() {
		req.RiskLevel = s.risk.Classify(req.Title, req.Description, req.Plan)
	}
	if req.ChangeType == "" {
		req.ChangeType = "normal"
	}
	change := &model.ChangeRequest{
		TenantID:     tenantID,
		TicketID:     req.TicketID,
		Title:        req.Title,
		Description:  req.Description,
		RiskLevel:    req.RiskLevel,
		ChangeType:   req.ChangeType,
		Impact:       req.Impact,
		Urgency:      req.Urgency,
		CABMembers:   req.CABMembers,
		Approvals:    []model.CABApproval{},
		Plan:         req.Plan,
		RollbackPlan: req.RollbackPlan,
		CreatedBy:    req.CreatedBy,
		ScheduledAt:  req.ScheduledAt,
		WindowStart:  req.WindowStart,
		WindowEnd:    req.WindowEnd,
	}
	if err := s.store.CreateChange(r.Context(), change); err != nil {
		slog.Error("create change failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create change request")
		return
	}
	writeJSON(w, http.StatusCreated, change)
}

func (s *Service) handleListChanges(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	changes, err := s.store.ListChanges(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if changes == nil {
		changes = []*model.ChangeRequest{}
	}
	writeJSON(w, http.StatusOK, changes)
}

func (s *Service) handleGetChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	change, err := s.store.GetChange(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "change request not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, change)
}

func (s *Service) handleVoteChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req model.VoteChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Member == "" {
		writeError(w, http.StatusBadRequest, "member is required")
		return
	}

	change, err := s.store.GetChange(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "change request not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if change.Status != model.ChangeStatusPending {
		writeError(w, http.StatusConflict, "change request is not in pending status")
		return
	}

	found := false
	for _, m := range change.CABMembers {
		if m == req.Member {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusBadRequest, "member is not a CAB member")
		return
	}

	for i, a := range change.Approvals {
		if a.Member == req.Member {
			change.Approvals[i].Approved = req.Approved
			change.Approvals[i].Comment = req.Comment
			change.Approvals[i].VotedAt = time.Now()
			if err := s.store.UpdateChange(r.Context(), change); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update vote")
				return
			}
			writeJSON(w, http.StatusOK, change)
			return
		}
	}

	change.Approvals = append(change.Approvals, model.CABApproval{
		Member:   req.Member,
		Approved: req.Approved,
		Comment:  req.Comment,
		VotedAt:  time.Now(),
	})

	allApproved := true
	for _, m := range change.CABMembers {
		voted := false
		for _, a := range change.Approvals {
			if a.Member == m && a.Approved {
				voted = true
				break
			}
		}
		if !voted {
			allApproved = false
			break
		}
	}
	if allApproved && len(change.CABMembers) > 0 {
		change.Status = model.ChangeStatusApproved
		// 变更联动: CAB 全票通过后自动创建执行工单(变更日历闭环)。
		if change.TicketID == "" {
			tctx := context.WithValue(r.Context(), "tenant_id", tenantID)
			tk, terr := s.CreateTicket(tctx, model.CreateTicketRequest{
				Title:        "执行变更: " + change.Title,
				Description:  change.Plan,
				RiskLevel:    change.RiskLevel,
				ChangePlan:   change.Plan,
				RollbackPlan: change.RollbackPlan,
				CreatedBy:    change.CreatedBy,
				CreatedByType: "system",
			})
			if terr == nil && tk != nil {
				change.TicketID = tk.ID
			}
		}
	}

	if err := s.store.UpdateChange(r.Context(), change); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update change request")
		return
	}
	writeJSON(w, http.StatusOK, change)
}

func (s *Service) handleExecuteChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	change, err := s.store.GetChange(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "change request not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if change.Status != model.ChangeStatusApproved {
		writeError(w, http.StatusConflict, "change request must be approved before execution")
		return
	}

	change.Status = model.ChangeStatusExecuted
	if err := s.store.UpdateChange(r.Context(), change); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to execute change request")
		return
	}
	writeJSON(w, http.StatusOK, change)
}

// --- CSV Import Handler ---

func (s *Service) handleImportTickets(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse CSV: "+err.Error())
		return
	}

	tenantID := tenantFromCtx(r.Context())
	type importError struct {
		Row     int    `json:"row"`
		Message string `json:"message"`
	}
	var errors []importError
	success := 0

	for i, row := range records {
		if i == 0 {
			continue
		}
		if len(row) < 2 {
			errors = append(errors, importError{Row: i + 1, Message: "insufficient columns"})
			continue
		}
		title := strings.TrimSpace(row[0])
		description := strings.TrimSpace(row[1])
		if title == "" {
			errors = append(errors, importError{Row: i + 1, Message: "title is empty"})
			continue
		}

		riskLevel := model.RiskLow
		if len(row) > 2 && strings.TrimSpace(row[2]) != "" {
			rl := model.RiskLevel(strings.TrimSpace(row[2]))
			if rl.Valid() {
				riskLevel = rl
			}
		}
		createdBy := "import"
		if len(row) > 3 && strings.TrimSpace(row[3]) != "" {
			createdBy = strings.TrimSpace(row[3])
		}
		changePlan := ""
		if len(row) > 4 {
			changePlan = strings.TrimSpace(row[4])
		}
		rollbackPlan := ""
		if len(row) > 5 {
			rollbackPlan = strings.TrimSpace(row[5])
		}

		ticket := &model.Ticket{
			TenantID:     tenantID,
			Title:        title,
			Description:  description,
			RiskLevel:    riskLevel,
			Status:       model.StatusDraft,
			ChangePlan:   changePlan,
			RollbackPlan: rollbackPlan,
			CreatedBy:    createdBy,
			TraceID:      traceIDFromCtx(r.Context()),
		}
		if err := s.store.Create(r.Context(), ticket); err != nil {
			errors = append(errors, importError{Row: i + 1, Message: err.Error()})
			continue
		}
		success++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":   len(records) - 1,
		"success": success,
		"errors":  errors,
	})
}

// --- CSV Export Handlers ---

func (s *Service) handleExportTicketsCSV(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	tickets, err := s.store.List(r.Context(), tenantID, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	writer.Write([]string{
		"id", "title", "description", "risk_level", "status", "change_plan", "rollback_plan",
		"created_by", "created_by_type", "approved_by", "reject_reason",
		"completed_by", "rolled_back_by", "rollback_reason",
		"created_at", "updated_at", "approved_at", "executed_at", "completed_at", "rolled_back_at",
		"deadline_at", "scheduled_at", "sla_breached",
	})

	for _, t := range tickets {
		approvedAt := ""
		if t.ApprovedAt != nil {
			approvedAt = t.ApprovedAt.Format(time.RFC3339)
		}
		executedAt := ""
		if t.ExecutedAt != nil {
			executedAt = t.ExecutedAt.Format(time.RFC3339)
		}
		completedAt := ""
		if t.CompletedAt != nil {
			completedAt = t.CompletedAt.Format(time.RFC3339)
		}
		rolledBackAt := ""
		if t.RolledBackAt != nil {
			rolledBackAt = t.RolledBackAt.Format(time.RFC3339)
		}
		deadlineAt := ""
		if t.DeadlineAt != nil {
			deadlineAt = t.DeadlineAt.Format(time.RFC3339)
		}
		scheduledAt := ""
		if t.ScheduledAt != nil {
			scheduledAt = t.ScheduledAt.Format(time.RFC3339)
		}

		writer.Write([]string{
			t.ID, t.Title, t.Description, string(t.RiskLevel), string(t.Status),
			t.ChangePlan, t.RollbackPlan, t.CreatedBy, t.CreatedByType,
			t.ApprovedBy, t.RejectReason, t.CompletedBy, t.RolledBackBy, t.RollbackReason,
			t.CreatedAt.Format(time.RFC3339), t.UpdatedAt.Format(time.RFC3339),
			approvedAt, executedAt, completedAt, rolledBackAt,
			deadlineAt, scheduledAt, strconv.FormatBool(t.SLABreached),
		})
	}
	writer.Flush()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=tickets.csv")
	w.Write([]byte("\xEF\xBB\xBF"))
	w.Write(buf.Bytes())
}

func (s *Service) handleExportStatsCSV(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	stats, err := s.store.GetStats(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	writer.Write([]string{"metric", "value"})
	writer.Write([]string{"total", strconv.Itoa(stats.Total)})
	writer.Write([]string{"approval_rate", fmt.Sprintf("%.4f", stats.ApprovalRate)})
	writer.Write([]string{"avg_approval_ms", strconv.FormatInt(stats.AvgApprovalMs, 10)})
	writer.Write([]string{"avg_execution_ms", strconv.FormatInt(stats.AvgExecutionMs, 10)})

	for k, v := range stats.ByStatus {
		writer.Write([]string{"status_" + k, strconv.Itoa(v)})
	}
	for k, v := range stats.ByRisk {
		writer.Write([]string{"risk_" + k, strconv.Itoa(v)})
	}

	writer.Flush()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=itsm_stats.csv")
	w.Write([]byte("\xEF\xBB\xBF"))
	w.Write(buf.Bytes())
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func traceIDFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("trace_id").(string); ok && t != "" {
		return t
	}
	return "unknown"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	v = httputil.EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// rememberTicketExperience 工单处置成功后, 把处置经验沉淀到 AI 长期记忆库(P0-1)。
// 幂等: 通过 labels.memory_archived 标记, 同一工单只沉淀一次。
func (s *Service) rememberTicketExperience(ctx context.Context, ticket *model.Ticket, resultComment string) {
	if s.experienceSink == nil {
		return
	}
	if ticket.Labels == nil || ticket.Labels["source"] != "ai_inspection" {
		return
	}
	if ticket.Labels["memory_archived"] == "true" {
		return
	}
	agentID := ticket.Labels["agent_id"]
	problem := ticket.Title
	if ticket.Description != "" && len(ticket.Description) <= 200 {
		problem = ticket.Title + ": " + ticket.Description
	}
	resolution := resultComment
	if resolution == "" {
		resolution = "已处置完成(见工单备注)"
	}
	if err := s.experienceSink(ctx, ticket.TenantID, agentID, problem, resolution, true); err != nil {
		slog.Warn("remember ticket experience failed", "ticket_id", ticket.ID, "error", err)
		return
	}
	ticket.Labels["memory_archived"] = "true"
	if err := s.store.Update(ctx, ticket); err != nil {
		slog.Warn("persist ticket memory_archived label failed", "ticket_id", ticket.ID, "error", err)
	}
	slog.Info("ticket experience remembered", "ticket_id", ticket.ID, "agent", agentID)
}
