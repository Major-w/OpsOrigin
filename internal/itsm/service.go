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

type Service struct {
	cfg          *config.Config
	store        store.Store
	risk         *risk.Classifier
	webhook      *webhook.Callback
	processStore ProcessEngine
	slaStore     *sla.Store
	wsHub        interface{ BroadcastToTenant(interface{}, string) }
	stopCh       chan struct{}
	stopOnce     sync.Once
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
		cfg:     cfg,
		store:   s,
		risk:    risk.New(),
		webhook: webhook.New(),
		stopCh:  make(chan struct{}),
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

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/itsm/tickets", s.handleCreate)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}", s.handleGet)
	mux.HandleFunc("GET /api/v1/itsm/tickets", s.handleList)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/process", s.handleGetProcessInstance)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/approve", s.handleApprove)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/reject", s.handleReject)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/execute", s.handleExecute)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/complete", s.handleComplete)
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/rollback", s.handleRollback)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/events", s.handleGetEvents)
	mux.HandleFunc("GET /api/v1/itsm/stats", s.handleStats)
	// Comments
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/comments", s.handleAddComment)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/comments", s.handleListComments)
	mux.HandleFunc("DELETE /api/v1/itsm/tickets/{id}/comments/{cid}", s.handleDeleteComment)
	// Attachments
	mux.HandleFunc("POST /api/v1/itsm/tickets/{id}/attachments", s.handleUploadAttachment)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/attachments", s.handleListAttachments)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/attachments/{aid}", s.handleDownloadAttachment)
	mux.HandleFunc("DELETE /api/v1/itsm/tickets/{id}/attachments/{aid}", s.handleDeleteAttachment)
	// SLA
	mux.HandleFunc("GET /api/v1/itsm/sla/stats", s.handleSLAStats)
	mux.HandleFunc("GET /api/v1/itsm/sla/breached", s.handleSLABreached)
	mux.HandleFunc("GET /api/v1/itsm/tickets/{id}/sla", s.handleTicketSLA)
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

	tenantID := tenantFromCtx(r.Context())

	if req.RiskLevel == "" || !req.RiskLevel.Valid() {
		req.RiskLevel = s.risk.Classify(req.Title, req.Description, req.ChangePlan)
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
		TraceID:       traceIDFromCtx(r.Context()),
		ScheduledAt:   req.ScheduledAt,
		DeadlineAt:    req.DeadlineAt,
	}
	if ticket.DeadlineAt == nil {
		if s.slaStore != nil {
			if pol, perr := s.slaStore.GetByRisk(r.Context(), tenantID, string(req.RiskLevel)); perr == nil && pol != nil && pol.ResolutionHours > 0 {
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

	if err := s.store.Create(r.Context(), ticket); err != nil {
		slog.Error("create ticket failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create ticket")
		return
	}

	s.fireWebhook(r.Context(), "ticket.created", ticket, req.CreatedBy)

	// 流程引擎接入：绑定了流程模板则创建流程实例并推进到首个真实步骤，
	// 由落点步骤类型映射工单初始状态（流程优先于基于风险的自动流转）。
	processGoverned := false
	if req.ProcessID != "" && s.processStore != nil {
		_, landing, perr := s.processStore.StartInstance(r.Context(), tenantID, req.ProcessID, ticket.ID)
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
			if err := s.store.Update(r.Context(), ticket); err != nil {
				slog.Error("persist ticket process binding failed", "error", err)
			}
			if prev != ticket.Status {
				s.recordEvent(r.Context(), ticket, prev, ticket.Status, req.CreatedBy, "process instance started")
			}
		}
	}

	if !processGoverned && req.RiskLevel.RequiresApproval() {
		if err := s.transition(r.Context(), ticket, model.StatusPendingReview, req.CreatedBy); err != nil {
			slog.Error("auto-transition to pending_review failed", "error", err)
		}
	}

	writeJSON(w, http.StatusCreated, ticket)
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

	ticket.Status = model.StatusApproved
	ticket.ApprovedBy = req.ApprovedBy
	now := time.Now()
	ticket.ApprovedAt = &now

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, model.StatusPendingReview, model.StatusApproved, req.ApprovedBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.approved", ticket, req.ApprovedBy)
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

	if err := s.store.Update(r.Context(), ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusSucceeded, req.CompletedBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.completed", ticket, req.CompletedBy)
	s.advanceProcess(r.Context(), ticket, "success")
	s.broadcastTicketUpdate(ticket)
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

func (s *Service) transition(ctx context.Context, ticket *model.Ticket, to model.TicketStatus, changedBy string) error {
	if err := model.CanTransition(ticket.Status, to); err != nil {
		return err
	}
	ticket.Status = to
	return s.store.Update(ctx, ticket)
}

// advanceProcess 若工单绑定了流程实例，则按 condition(success/failure) 推进流程引擎——
// 记录当前步骤结果并落到下一步。无实例(未绑定流程)时静默跳过，不影响普通工单。
func (s *Service) advanceProcess(ctx context.Context, ticket *model.Ticket, condition string) {
	if s.processStore == nil || ticket == nil || ticket.ProcessID == "" {
		return
	}
	if _, _, err := s.processStore.Advance(ctx, ticket.TenantID, ticket.ID, condition, string(ticket.Status)); err != nil {
		slog.Warn("advance process instance failed", "ticket_id", ticket.ID, "condition", condition, "error", err)
	}
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

func (s *Service) handleListComments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
	go func() {
		if err := s.webhook.Notify(ctx, ticket.WebhookURL, event,
			ticket.ID, ticket.TenantID, string(ticket.Status),
			string(ticket.RiskLevel), changedBy); err != nil {
			slog.Warn("webhook notify failed", "event", event, "ticket_id", ticket.ID, "error", err)
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
	tickets, err := s.store.ListActiveTicketsWithDeadline(ctx, "default")
	if err != nil {
		slog.Error("itsm: SLA check failed", "error", err)
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
			if err := s.store.Update(ctx, t); err != nil {
				slog.Error("itsm: failed to mark SLA breached", "ticket_id", t.ID, "error", err)
				continue
			}
			slog.Warn("itsm: SLA breached", "ticket_id", t.ID, "title", t.Title, "deadline", deadline)
			s.broadcastTicketUpdate(t)
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
		}
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
