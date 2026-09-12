package itsm

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/itsm/catalog"
	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/ola"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/internal/itsm/risk"
	"github.com/opsorigin/core/internal/itsm/sla"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/internal/itsm/webhook"
	"github.com/opsorigin/core/internal/objectstore"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
)

// getUserID 从请求上下文获取用户 ID，未认证时回退到请求体字段或 "system"。
func getUserID(r *http.Request, fallback string) string {
	if uid := auth.UserIDFromContext(r.Context()); uid != "" {
		return uid
	}
	if fallback != "" {
		return fallback
	}
	return "system"
}

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
	olaStore     *ola.Store
	formEngine   *form.Engine
	wsHub        interface{ BroadcastToTenant(interface{}, string) }
	stopCh       chan struct{}
	stopOnce     sync.Once
	// objStore 对象存储(MinIO/COS/OSS): 附件二进制落对象存储; nil = 关闭, 二进制回退数据库。
	objStore *objectstore.Client
	// knowledgeArchiver 由 cmd/core 注入: 工单完成后将处置案例沉淀到知识库(nil=关闭)。
	knowledgeArchiver func(ctx context.Context, tenantID, title, content string, tags []string) (string, error)
	// experienceSink 由 cmd/core 注入(P0-1 长期记忆): 工单完成后将处置经验沉淀到 AI 记忆库(nil=关闭)。
	experienceSink func(ctx context.Context, tenantID, agentID, problem, resolution string, outcome bool) error
	// notify 对接通知中心(解耦接口, 避免 import 环): 工单关键事件触发 Policy 分发。
	notify Notifier
	// messages 对接站内消息(解耦接口): 工单关键事件投递到当事人收件箱。
	// 与 notify 的区别 —— notify 往外部渠道群发, messages 进具体某个人的铃铛。
	messages MessageSender
	// catalogStore 由 cmd/core 注入: 从服务目录发起申请时套用目录项配置的流程/表单。
	catalogStore catalogGetter
	// ciQualityChecker 流程门禁: 由 cmd/core 注入, 返回 CI 列表的 CMDB 数据质量分
	// (0~100)。工单创建关联 CI 时校验, 低分记录警告事件(与 cmdb 包零耦合)。
	ciQualityChecker func(ctx context.Context, tenantID string, ciIDs []string) map[string]float64
}

// SetObjectStore 注入对象存储(MinIO/COS/OSS)。注入后新上传附件二进制进对象存储, DB 只存 object_key。
// 不注入(或注入失败)时保持原行为: 二进制存数据库 bytea。
func (s *Service) SetObjectStore(os *objectstore.Client) { s.objStore = os }

// SetCIQualityChecker 注入 CMDB 数据质量检查器(流程门禁)。
func (s *Service) SetCIQualityChecker(fn func(ctx context.Context, tenantID string, ciIDs []string) map[string]float64) {
	s.ciQualityChecker = fn
}

// catalogGetter 服务目录项读取能力(由 catalog.Store 实现; 接口化便于测试注入)。
type catalogGetter interface {
	Get(ctx context.Context, id, tenantID string) (*catalog.ServiceItem, error)
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

func (s *Service) SetOLAStore(st *ola.Store) {
	s.olaStore = st
}

// SetNotifier 注入通知中心, 使工单关键事件能经 Policy 分发到各渠道(nil=关闭通知)。
func (s *Service) SetNotifier(n Notifier) {
	s.notify = n
}

// SetCatalogStore 注入服务目录存储(从目录发起申请时套用流程/表单作用域)。
func (s *Service) SetCatalogStore(c catalogGetter) {
	s.catalogStore = c
}

// CreateTicketFromCatalog 从服务目录发起申请: 读取目录项, 自动套用其配置的
// 表单作用域(catalog:<id> 或自定义 form_scope)与流程(process_id), 再走 CreateTicket
// 完整链路(风险/SLA/表单校验/流程实例)。返回创建的工单。
func (s *Service) CreateTicketFromCatalog(ctx context.Context, catalogID, tenantID string, req model.CreateTicketRequest) (*model.Ticket, error) {
	if s.catalogStore == nil {
		return nil, fmt.Errorf("catalog store not configured")
	}
	item, err := s.catalogStore.Get(ctx, catalogID, tenantID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("catalog item %s not found", catalogID)
	}
	if !item.Enabled {
		return nil, fmt.Errorf("catalog item %s is disabled", catalogID)
	}
	// 套用目录项配置: 表单作用域优先用项自定义值, 否则默认 catalog:<id>; 流程同理。
	if req.FormScope == "" {
		req.FormScope = item.FormScope
		if req.FormScope == "" {
			req.FormScope = "catalog:" + catalogID
		}
	}
	if req.ProcessID == "" {
		req.ProcessID = item.ProcessID
	}
	req.CatalogID = catalogID // 记录工单来源目录项，供使用量统计与满意度聚合
	if req.Labels == nil {
		req.Labels = map[string]string{}
	}
	req.Labels["source"] = "catalog"
	return s.CreateTicket(ctx, req)
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
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/resubmit", s.handleResubmit) // 被拒后重新提交
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/hold", s.handleHold)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/resume", s.handleResume)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/remind", s.handleRemind)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/relate", s.handleRelate)
	mux.HandleFunc("PUT /api/v1/itsm/tickets/{id}", s.handleUpdateTicket)
	// 服务目录: 从目录发起申请(自动套用目录项配置的流程/表单)
	mux.HandleFunc("POST /api/v1/itsm/catalog/{id}/fulfill", s.handleFulfillCatalog)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/events", s.handleGetEvents)
	mux.HandleFunc("GET /api/v1/itsm/stats", s.handleStats)
	// Comments
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/comments", s.handleAddComment)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/comments", s.handleListComments)
	mux.HandleFunc("PUT /api/v1/itsm/tickets/{id}/comments/{cid}", s.handleEditComment)
	mux.HandleFunc("DELETE /api/v1/itsm/tickets/{id}/comments/{cid}", s.handleDeleteComment)
	// Subtasks
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/subtasks", s.handleAddSubtask)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/subtasks", s.handleListSubtasks)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/subtasks/{sid}", s.handleGetSubtask)
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

// CreateTicket 创建一张工单, 供 HTTP handler 与内部模块(如 AI 巡检发现转单)复用。
// 完整走与 handleCreate 相同的链路: 风险定级 → SLA 截止 → 落库 → webhook → 流程引擎接入。
func (s *Service) CreateTicket(ctx context.Context, req model.CreateTicketRequest) (*model.Ticket, error) {
	tenantID := auth.TenantFromContext(ctx)
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
		CatalogID:     req.CatalogID,
		ApprovalChain: req.ApprovalChain, // 审批链配置(顺序/并行), 之前漏装配导致多级审批从未生效
		TraceID:       traceIDFromCtx(ctx),
		ScheduledAt:   req.ScheduledAt,
		DeadlineAt:    req.DeadlineAt,
	}
	// 流程门禁(数据质量): 关联 CI 质量分低于阈值的, 记录警告并通知(不阻断创建)。
	if len(ticket.CIIDs) > 0 && s.ciQualityChecker != nil {
		scores := s.ciQualityChecker(ctx, tenantID, ticket.CIIDs)
		for id, score := range scores {
			if score < 60 {
				msg := fmt.Sprintf("关联资产 %s 数据质量分 %.0f 低于 60，建议先修复 CMDB 数据再推进", id, score)
				slog.Warn("itsm quality gate", "ticket", ticket.Title, "warning", msg)
				s.recordEvent(ctx, ticket, "", "", "", msg)
			}
		}
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
	// 建单时已指定负责人的(目录申请/自动派单)直接进对方收件箱; 未指派的等 assign 再发。
	if ticket.Assignee != "" {
		s.notifyTicketInbox(ctx, ticket, msgCategoryTicket, msgLevelInfo,
			"新工单待处理: "+ticket.Title,
			fmt.Sprintf("申请人: %s / 风险: %s", ticket.CreatedBy, ticket.RiskLevel),
			req.CreatedBy, ticket.Assignee)
	}

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

// GetTicket 按 ID 查询工单(供 AI 修复验证器等回查派发结果, 复用 store.Get)。
func (s *Service) GetTicket(ctx context.Context, tenantID, id string) (*model.Ticket, error) {
	return s.store.Get(ctx, id, tenantID)
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

// archiveTicketToKnowledge 工单成功完成后, 将处置案例自动沉淀到知识库,
// 供后续 AI 检索复用(持续改进闭环: 发现 → 处置 → 沉淀 → 复用)。
// 支持两种触发条件: AI 巡检来源工单(source=ai_inspection) 或 标记了 auto_archived=true 的普通工单。
func (s *Service) archiveTicketToKnowledge(ctx context.Context, ticket *model.Ticket, resultComment string) {
	if s.knowledgeArchiver == nil {
		return
	}
	// 检查是否需要沉淀: AI 巡检来源 或 标记了 auto_archived
	isAISource := ticket.Labels != nil && ticket.Labels["source"] == "ai_inspection"
	autoArchived := ticket.Labels != nil && ticket.Labels["auto_archived"] == "true"
	if !isAISource && !autoArchived {
		return
	}
	if ticket.Labels != nil && ticket.Labels["kb_archived"] == "true" {
		return // 幂等: 已沉淀过
	}

	var title, source string
	if isAISource {
		title = fmt.Sprintf("[处置案例] %s", ticket.Title)
		source = "主动巡检发现并转工单处置"
	} else {
		title = fmt.Sprintf("[工单案例] %s", ticket.Title)
		source = "工单完成后自动沉淀"
	}

	content := "来源: " + source + "\n"
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

	tags := []string{"disposal-case", string(ticket.RiskLevel)}
	if isAISource {
		tags = append(tags, "ai_inspection")
	}

	articleID, err := s.knowledgeArchiver(ctx, ticket.TenantID, title, content, tags)
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

// stepVoter 是可选的多人审批表决能力接口(由 process.Service 实现, 可选能力不破坏既有 ProcessEngine)。
type stepVoter interface {
	ProcessEngine
	VoteStep(ctx context.Context, tenantID, ticketID, stepID, approver string, approved bool, extraApprovers []string) (allApproved bool, err error)
}

// dynamicApprovers 按步骤配置的 ApproverField 从工单表单取值解析出动态审批人。
// 未配置或工单未采集到该字段时返回 nil，此时仅静态审批人名单生效。
func dynamicApprovers(step *process.Step, ticket *model.Ticket) []string {
	if step == nil || step.ApproverField == "" || ticket == nil {
		return nil
	}
	return process.ApproversFromValue(ticket.CustomFields[step.ApproverField])
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// --- Subtasks ---

// --- SLA Checker ---

// --- Form schema handlers (dynamic forms) ---

// --- 数据字典 handlers ---

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

// --- CSV Import Handler ---

// --- CSV Export Handlers ---

func traceIDFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("trace_id").(string); ok && t != "" {
		return t
	}
	return "unknown"
}
