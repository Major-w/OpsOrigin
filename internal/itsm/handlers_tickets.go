package itsm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/httputil"
)

// notifyTicket 经通知中心分发工单事件(无 notify 注入时静默跳过)。
func (s *Service) notifyTicket(ctx context.Context, tenantID, event, title, content string) {
	if s.notify == nil {
		return
	}
	if err := s.notify.NotifyEvent(ctx, tenantID, event, title, content); err != nil {
		slog.Warn("itsm: notify event failed", "event", event, "error", err)
	}
}

// handleCreate godoc
// @Summary 创建工单
// @Description 创建一个新的 ITSM 工单
// @Tags ITSM
// @Accept json
// @Produce json
// @Param ticket body model.CreateTicketRequest true "工单信息"
// @Success 201 {object} model.Ticket
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/itsm/tickets [post]
// @Security BearerAuth
func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req model.CreateTicketRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" {
		httputil.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}

	// 优先从 JWT 提取用户身份，回退到请求体字段
	if req.CreatedBy == "" || req.CreatedBy == "admin" {
		req.CreatedBy = getUserID(r, req.CreatedBy)
	}

	ticket, err := s.CreateTicket(r.Context(), req)
	if err != nil {
		slog.Error("create ticket failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create ticket")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, ticket)
}

// handleGet godoc
// @Summary 获取工单详情
// @Description 根据 ID 获取工单详情
// @Tags ITSM
// @Accept json
// @Produce json
// @Param id path string true "工单 ID"
// @Success 200 {object} model.Ticket
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/itsm/tickets/{id} [get]
// @Security BearerAuth
func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, ticket)
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

// handleList godoc
// @Summary 获取工单列表
// @Description 获取工单列表，支持按状态过滤
// @Tags ITSM
// @Accept json
// @Produce json
// @Param status query string false "工单状态过滤"
// @Success 200 {array} model.Ticket
// @Failure 500 {object} map[string]string
// @Router /api/v1/itsm/tickets [get]
// @Security BearerAuth
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	status := model.TicketStatus(r.URL.Query().Get("status"))

	tickets, err := s.store.List(r.Context(), tenantID, status)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tickets == nil {
		tickets = []*model.Ticket{}
	}

	httputil.WriteJSON(w, http.StatusOK, tickets)
}

// currentVoteStep 返回工单当前步骤中需要多人表决的 user 步骤(配了静态审批人或审批人取值字段), 否则 nil。
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
		if st.ID == inst.CurrentStep && st.Type == process.StepUser && (len(st.Approvers) > 0 || st.ApproverField != "") {
			return st
		}
	}
	return nil
}

func (s *Service) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.ApproveRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusApproved); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	// 审批链处理: 支持顺序/并行审批模式
	if ticket.ApprovalChain != nil && len(ticket.ApprovalChain.Approvers) > 1 {
		if ticket.ApprovalStatus == nil {
			ticket.ApprovalStatus = &model.ApprovalStatus{
				CurrentIndex: 0,
				Votes:        []model.ApprovalVote{},
				FinalResult:  "pending",
			}
		}

		vote := model.ApprovalVote{
			Approver: req.ApprovedBy,
			Approved: true,
			Comment:  req.Comment,
			VotedAt:  time.Now(),
		}
		ticket.ApprovalStatus.Votes = append(ticket.ApprovalStatus.Votes, vote)

		if ticket.ApprovalChain.Mode == "parallel" {
			// 并行模式: 检查是否所有审批人都已投票
			allVoted := len(ticket.ApprovalStatus.Votes) >= len(ticket.ApprovalChain.Approvers)
			allApproved := true
			for _, v := range ticket.ApprovalStatus.Votes {
				if !v.Approved {
					allApproved = false
					break
				}
			}
			if !allVoted {
				// 还有审批人未投票，等待
				if err := s.store.Update(r.Context(), ticket); err != nil {
					httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
					return
				}
				httputil.WriteJSON(w, http.StatusOK, map[string]any{
					"status":  "waiting",
					"message": fmt.Sprintf("已记录审批意见, 等待其余审批人 (%d/%d)", len(ticket.ApprovalStatus.Votes), len(ticket.ApprovalChain.Approvers)),
					"ticket":  ticket,
				})
				return
			}
			// 所有人都已投票
			if !allApproved {
				ticket.ApprovalStatus.FinalResult = "rejected"
				ticket.Status = model.StatusRejected
				ticket.RejectReason = "审批链驳回"
				if err := s.store.Update(r.Context(), ticket); err != nil {
					httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
					return
				}
				s.recordEvent(r.Context(), ticket, model.StatusPendingReview, model.StatusRejected, req.ApprovedBy, "审批链驳回")
				s.broadcastTicketUpdate(ticket)
				httputil.WriteJSON(w, http.StatusOK, ticket)
				return
			}
			// 全部通过
			ticket.ApprovalStatus.FinalResult = "approved"
		} else {
			// 顺序模式: 检查当前审批人是否是正确的顺序
			currentApprover := ticket.ApprovalChain.Approvers[ticket.ApprovalStatus.CurrentIndex]
			if req.ApprovedBy != currentApprover {
				httputil.WriteError(w, http.StatusConflict, fmt.Sprintf("当前审批人应为 %s", currentApprover))
				return
			}
			ticket.ApprovalStatus.CurrentIndex++
			// 检查是否还有下一个审批人
			if ticket.ApprovalStatus.CurrentIndex < len(ticket.ApprovalChain.Approvers) {
				if err := s.store.Update(r.Context(), ticket); err != nil {
					httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
					return
				}
				nextApprover := ticket.ApprovalChain.Approvers[ticket.ApprovalStatus.CurrentIndex]
				httputil.WriteJSON(w, http.StatusOK, map[string]any{
					"status":  "waiting",
					"message": fmt.Sprintf("已通过, 等待下一级审批人: %s", nextApprover),
					"ticket":  ticket,
				})
				return
			}
			// 所有顺序审批人都已通过
			ticket.ApprovalStatus.FinalResult = "approved"
		}
	} else {
		// 多人审批: 当前步骤先记录投票, 未达成表决条件(会签未全票 / 或签无人通过)则保持待审批, 不推进状态与流程。
		if voter, ok := s.processStore.(stepVoter); ok {
			if step := s.currentVoteStep(r.Context(), ticket); step != nil {
				extra := dynamicApprovers(step, ticket)
				all, err := voter.VoteStep(r.Context(), ticket.TenantID, ticket.ID, step.ID, req.ApprovedBy, true, extra)
				if err == nil && !all {
					httputil.WriteJSON(w, http.StatusOK, map[string]any{
						"status":  "waiting",
						"message": "已记录审批意见, 等待其余审批人",
						"ticket":  ticket,
					})
					return
				}
			}
		}
	}

	previousStatus := ticket.Status
	ticket.ApprovedBy = req.ApprovedBy
	now := time.Now()
	ticket.ApprovedAt = &now

	// 审批通过后自动推进：无流程绑定时自动进入 executing 状态
	if ticket.ProcessID == "" {
		ticket.Status = model.StatusExecuting
		ticket.ExecutedAt = &now
	} else {
		ticket.Status = model.StatusApproved
	}

	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, ticket.Status, req.ApprovedBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.approved", ticket, req.ApprovedBy)
	s.notifyTicket(r.Context(), tenantID, evTicketApproved, "工单已审批通过: "+ticket.Title, fmt.Sprintf("审批人: %s", req.ApprovedBy))
	s.notifyTicketInbox(r.Context(), ticket, msgCategoryApproval, msgLevelInfo,
		"工单审批通过: "+ticket.Title,
		fmt.Sprintf("审批人: %s%s", req.ApprovedBy, optionalComment(req.Comment)),
		req.ApprovedBy, assigneeAndOwner(ticket)...)
	s.advanceProcess(r.Context(), ticket, "success")
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.RejectRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusRejected); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusRejected
	ticket.RejectReason = req.Reason

	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusRejected, req.RejectedBy, req.Reason)
	s.fireWebhook(r.Context(), "ticket.rejected", ticket, req.RejectedBy)
	s.notifyTicketInbox(r.Context(), ticket, msgCategoryApproval, msgLevelWarning,
		"工单被驳回: "+ticket.Title,
		fmt.Sprintf("审批人: %s / 原因: %s", req.RejectedBy, truncate(req.Reason, 120)),
		req.RejectedBy, assigneeAndOwner(ticket)...)
	s.advanceProcess(r.Context(), ticket, "failure")
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleExecute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.ExecuteRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if ticket.RiskLevel.RequiresApproval() && ticket.Status != model.StatusApproved {
		httputil.WriteError(w, http.StatusForbidden, model.ErrRiskRequiresApproval.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusExecuting); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusExecuting
	now := time.Now()
	ticket.ExecutedAt = &now

	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusExecuting, req.ExecutedBy, req.Result)
	s.fireWebhook(r.Context(), "ticket.executing", ticket, req.ExecutedBy)
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleComplete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.CompleteRequest
	httputil.BindJSON(r, &req)
	if req.CompletedBy == "" {
		req.CompletedBy = getUserID(r, "")
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusSucceeded); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusSucceeded, req.CompletedBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.completed", ticket, req.CompletedBy)
	s.advanceProcess(r.Context(), ticket, "success")
	s.broadcastTicketUpdate(ticket)
	s.archiveTicketToKnowledge(r.Context(), ticket, req.Comment)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.RollbackRequest
	httputil.BindJSON(r, &req)
	if req.RolledBackBy == "" {
		req.RolledBackBy = getUserID(r, "")
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := model.CanTransition(ticket.Status, model.StatusRolledBack); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusRolledBack
	ticket.RolledBackBy = req.RolledBackBy
	ticket.RollbackReason = req.RollbackReason
	now := time.Now()
	ticket.RolledBackAt = &now

	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusRolledBack, req.RolledBackBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.rolled_back", ticket, req.RolledBackBy)
	s.advanceProcess(r.Context(), ticket, "failure")
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleSubmit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.SubmitRequest
	httputil.BindJSON(r, &req)

	// 优先从 JWT 提取用户身份
	if req.SubmittedBy == "" || req.SubmittedBy == "admin" {
		req.SubmittedBy = getUserID(r, req.SubmittedBy)
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if model.IsTerminal(ticket.Status) {
		httputil.WriteError(w, http.StatusConflict, model.ErrTicketClosed.Error())
		return
	}

	// 高风险走审批, 低/中风险直接进执行(避免 draft 死锁)。
	target := model.StatusPendingReview
	if !ticket.RiskLevel.RequiresApproval() {
		target = model.StatusExecuting
	}
	if err := model.CanTransition(ticket.Status, target); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = target

	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, target, req.SubmittedBy, "提交工单")
	s.fireWebhook(r.Context(), "ticket.submitted", ticket, req.SubmittedBy)
	s.advanceProcess(r.Context(), ticket, "success")
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleAssign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.AssignRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Assignee == "" {
		httputil.WriteError(w, http.StatusBadRequest, "assignee is required")
		return
	}

	// 优先从 JWT 提取用户身份
	if req.AssignedBy == "" || req.AssignedBy == "admin" {
		req.AssignedBy = getUserID(r, req.AssignedBy)
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if model.IsTerminal(ticket.Status) {
		httputil.WriteError(w, http.StatusConflict, model.ErrTicketClosed.Error())
		return
	}

	previousStatus := ticket.Status
	now := time.Now()
	ticket.Assignee = req.Assignee
	ticket.AssignedAt = &now

	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	s.recordEvent(r.Context(), ticket, previousStatus, ticket.Status, req.AssignedBy, "指派给 "+req.Assignee)
	s.notifyTicket(r.Context(), tenantID, evTicketAssigned, "工单已指派: "+ticket.Title, fmt.Sprintf("负责人: %s", req.Assignee))
	s.notifyTicketInbox(r.Context(), ticket, msgCategoryTicket, msgLevelInfo,
		"工单指派给你: "+ticket.Title,
		fmt.Sprintf("指派人: %s / 风险: %s", req.AssignedBy, ticket.RiskLevel),
		req.AssignedBy, req.Assignee)
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.CloseRequest
	httputil.BindJSON(r, &req)
	if req.ClosedBy == "" {
		req.ClosedBy = getUserID(r, "")
	}

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "ticket not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := model.CanTransition(ticket.Status, model.StatusClosed); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	previousStatus := ticket.Status
	ticket.Status = model.StatusClosed
	if req.Satisfaction > 0 {
		ticket.Satisfaction = req.Satisfaction
	}

	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}

	// 子任务联动: 仅对 close_with_parent=true 的未完成子任务级联关闭(记完成时间)。
	// 置 false 的子任务独立于主单生命周期, 需单独验收, 关单时保持原状态。
	if subs, serr := s.store.ListSubtasks(r.Context(), id, tenantID); serr == nil {
		now := time.Now()
		for _, sub := range subs {
			if sub.Status == model.SubtaskDone || !sub.CloseWithParent {
				continue
			}
			sub.Status = model.SubtaskDone
			sub.CompletedAt = &now
			if sub.Detail == "" {
				sub.Detail = "主工单关闭时自动结束"
			}
			sub.UpdatedBy = req.ClosedBy
			_ = s.store.UpdateSubtask(r.Context(), sub)
		}
	}

	s.recordEvent(r.Context(), ticket, previousStatus, model.StatusClosed, req.ClosedBy, req.Comment)
	s.fireWebhook(r.Context(), "ticket.closed", ticket, req.ClosedBy)
	s.notifyTicket(r.Context(), tenantID, evTicketClosed, "工单已关闭: "+ticket.Title, req.Comment)
	s.notifyTicketInbox(r.Context(), ticket, msgCategoryTicket, msgLevelInfo,
		"工单已关闭: "+ticket.Title,
		fmt.Sprintf("关闭人: %s%s", req.ClosedBy, optionalComment(req.Comment)),
		req.ClosedBy, assigneeAndOwner(ticket)...)
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req model.WithdrawRequest
	httputil.BindJSON(r, &req)
	if req.WithdrawnBy == "" {
		req.WithdrawnBy = getUserID(r, "")
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if err := model.CanTransition(ticket.Status, model.StatusWithdrawn); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	prev := ticket.Status
	ticket.Status = model.StatusWithdrawn
	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, prev, model.StatusWithdrawn, req.WithdrawnBy, req.Reason)
	s.fireWebhook(r.Context(), "ticket.withdrawn", ticket, req.WithdrawnBy)
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleReopen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req model.ReopenRequest
	httputil.BindJSON(r, &req)
	if req.ReopenedBy == "" {
		req.ReopenedBy = getUserID(r, "")
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if err := model.CanTransition(ticket.Status, model.StatusReopened); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	prev := ticket.Status
	ticket.Status = model.StatusReopened
	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, prev, model.StatusReopened, req.ReopenedBy, req.Reason)
	s.fireWebhook(r.Context(), "ticket.reopened", ticket, req.ReopenedBy)
	s.notifyTicket(r.Context(), tenantID, evTicketReopened, "工单重开: "+ticket.Title, req.Reason)
	s.notifyTicketInbox(r.Context(), ticket, msgCategoryTicket, msgLevelWarning,
		"工单被重开: "+ticket.Title,
		fmt.Sprintf("重开人: %s / 原因: %s", req.ReopenedBy, truncate(req.Reason, 120)),
		req.ReopenedBy, assigneeAndOwner(ticket)...)
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

// handleResubmit 允许被拒工单修改后重新提交到 pending_review 状态。
func (s *Service) handleResubmit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		ResubmittedBy string `json:"resubmitted_by"`
		Reason        string `json:"reason,omitempty"`
	}
	httputil.BindJSON(r, &req)
	if req.ResubmittedBy == "" {
		req.ResubmittedBy = getUserID(r, "")
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if err := model.CanTransition(ticket.Status, model.StatusPendingReview); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	prev := ticket.Status
	ticket.Status = model.StatusPendingReview
	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, prev, model.StatusPendingReview, req.ResubmittedBy, "重新提交: "+req.Reason)
	s.fireWebhook(r.Context(), "ticket.resubmitted", ticket, req.ResubmittedBy)
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleHold(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req model.HoldRequest
	httputil.BindJSON(r, &req)
	if req.HoldBy == "" {
		req.HoldBy = getUserID(r, "")
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if model.IsTerminal(ticket.Status) {
		httputil.WriteError(w, http.StatusConflict, "terminal ticket cannot be held")
		return
	}
	now := time.Now()
	ticket.OnHold = true
	ticket.HoldReason = req.Reason
	ticket.HoldUntilAt = req.HoldUntilAt
	ticket.HoldAt = &now // 记录挂起开始时间
	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, ticket.Status, ticket.Status, req.HoldBy, "挂起: "+req.Reason)
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		ResumedBy string `json:"resumed_by"`
		Reason    string `json:"reason,omitempty"`
	}
	httputil.BindJSON(r, &req)
	if req.ResumedBy == "" {
		req.ResumedBy = getUserID(r, "")
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	if !ticket.OnHold {
		httputil.WriteError(w, http.StatusConflict, "ticket is not on hold")
		return
	}
	now := time.Now()
	ticket.OnHold = false
	ticket.HoldReason = ""
	ticket.HoldUntilAt = nil

	// 计算本次挂起时长并累加
	if ticket.HoldAt != nil {
		holdDuration := int(now.Sub(*ticket.HoldAt).Seconds())
		ticket.HoldSeconds += holdDuration
		ticket.HoldAt = nil
	}

	// 如果有截止时间，延长挂起时长
	if ticket.DeadlineAt != nil {
		extended := ticket.DeadlineAt.Add(time.Duration(ticket.HoldSeconds) * time.Second)
		ticket.DeadlineAt = &extended
	}

	if err := s.store.Update(r.Context(), ticket); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, ticket.Status, ticket.Status, req.ResumedBy, "恢复: "+req.Reason)
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
}

func (s *Service) handleRemind(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req model.RemindRequest
	httputil.BindJSON(r, &req)
	if req.RemindedBy == "" {
		req.RemindedBy = getUserID(r, "")
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	content := fmt.Sprintf("负责人: %s\n催办人: %s", ticket.Assignee, req.RemindedBy)
	if req.Message != "" {
		content += "\n" + req.Message
	}
	s.notifyTicket(r.Context(), tenantID, evTicketReminded, "工单催办: "+ticket.Title, content)
	s.notifyTicketInbox(r.Context(), ticket, msgCategoryTicket, msgLevelWarning,
		"工单催办: "+ticket.Title,
		fmt.Sprintf("催办人: %s%s", req.RemindedBy, optionalComment(req.Message)),
		req.RemindedBy, ticket.Assignee)
	s.recordEvent(r.Context(), ticket, ticket.Status, ticket.Status, req.RemindedBy, "催办: "+req.Message)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "reminded"})
}

func (s *Service) handleRelate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req model.RelateRequest
	httputil.BindJSON(r, &req)
	if req.RelatedID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "related_id is required")
		return
	}
	if req.RelatedID == id {
		httputil.WriteError(w, http.StatusBadRequest, "cannot relate to itself")
		return
	}
	a, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}
	b, err := s.store.Get(r.Context(), req.RelatedID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "related ticket not found")
		return
	}
	if !containsStr(a.RelatedTicketIDs, b.ID) {
		a.RelatedTicketIDs = append(a.RelatedTicketIDs, b.ID)
	}
	if !containsStr(b.RelatedTicketIDs, a.ID) {
		b.RelatedTicketIDs = append(b.RelatedTicketIDs, a.ID)
	}
	if err := s.store.Update(r.Context(), a); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	_ = s.store.Update(r.Context(), b)
	s.recordEvent(r.Context(), a, a.Status, a.Status, req.RelateBy, fmt.Sprintf("关联工单: %s (类型: %s)", b.ID, req.Relation))
	s.broadcastTicketUpdate(a)
	httputil.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) handleUpdateTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req model.UpdateTicketRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update ticket")
		return
	}
	s.recordEvent(r.Context(), ticket, prev, ticket.Status, req.Assignee, "更新工单信息")
	s.broadcastTicketUpdate(ticket)
	httputil.WriteJSON(w, http.StatusOK, ticket)
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
		// 流程到达 End 节点：工单视为已完成（与 StepTypeToTicketStatus(StepEnd)="succeeded" 一致）。
		// 此前漏置工单状态，导致自动流程走到 End 时工单状态停留在执行中 / 为空。
		if ticket.Status != model.StatusSucceeded {
			prev := ticket.Status
			ticket.Status = model.StatusSucceeded
			if err := s.store.Update(ctx, ticket); err != nil {
				slog.Error("itsm: persist process-end status failed", "ticket_id", ticket.ID, "error", err)
				return
			}
			s.recordEvent(ctx, ticket, prev, ticket.Status, "process", "流程结束(到达结束节点)")
			s.broadcastTicketUpdate(ticket)
		}
		return
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
	tenantID := auth.TenantFromContext(r.Context())
	if s.processStore == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "process engine not configured")
		return
	}
	inst, err := s.processStore.GetInstance(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if inst == nil {
		httputil.WriteError(w, http.StatusNotFound, "no process instance for ticket")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, inst)
}

func (s *Service) handleGetEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	ticket, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "ticket not found")
		return
	}

	events, err := s.store.GetEvents(r.Context(), ticket.ID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []*model.TicketEvent{}
	}
	httputil.WriteJSON(w, http.StatusOK, events)
}

func (s *Service) handleStats(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	stats, err := s.store.GetStats(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, stats)
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
			"type":   "ticket.updated",
			"ticket": ticket,
			"time":   time.Now().UnixMilli(),
		}, ticket.TenantID)
	}
}

func (s *Service) broadcastEvent(tenantID string, event map[string]any) {
	if s.wsHub != nil {
		event["time"] = time.Now().UnixMilli()
		s.wsHub.BroadcastToTenant(event, tenantID)
	}
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

func (s *Service) handleImportTickets(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to parse form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to parse CSV: "+err.Error())
		return
	}

	tenantID := auth.TenantFromContext(r.Context())
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

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"total":   len(records) - 1,
		"success": success,
		"errors":  errors,
	})
}

func (s *Service) handleExportTicketsCSV(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	tickets, err := s.store.List(r.Context(), tenantID, "")
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
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
	tenantID := auth.TenantFromContext(r.Context())
	stats, err := s.store.GetStats(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
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
