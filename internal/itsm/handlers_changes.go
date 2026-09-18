package itsm

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleCreateChange(w http.ResponseWriter, r *http.Request) {
	var req model.CreateChangeRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		httputil.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create change request")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, change)
}

func (s *Service) handleListChanges(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	p := store.ParsePageParams(r.URL.Query())

	result, err := s.store.ListChangesPaged(r.Context(), tenantID, p)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleGetChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	change, err := s.store.GetChange(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "change request not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, change)
}

func (s *Service) handleVoteChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req model.VoteChangeRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Member == "" {
		httputil.WriteError(w, http.StatusBadRequest, "member is required")
		return
	}

	change, err := s.store.GetChange(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "change request not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if change.Status != model.ChangeStatusPending {
		httputil.WriteError(w, http.StatusConflict, "change request is not in pending status")
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
		httputil.WriteError(w, http.StatusBadRequest, "member is not a CAB member")
		return
	}

	for i, a := range change.Approvals {
		if a.Member == req.Member {
			change.Approvals[i].Approved = req.Approved
			change.Approvals[i].Comment = req.Comment
			change.Approvals[i].VotedAt = time.Now()
			if err := s.store.UpdateChange(r.Context(), change); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "failed to update vote")
				return
			}
			httputil.WriteJSON(w, http.StatusOK, change)
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
				Title:         "执行变更: " + change.Title,
				Description:   change.Plan,
				RiskLevel:     change.RiskLevel,
				ChangePlan:    change.Plan,
				RollbackPlan:  change.RollbackPlan,
				CreatedBy:     change.CreatedBy,
				CreatedByType: "system",
			})
			if terr == nil && tk != nil {
				change.TicketID = tk.ID
			}
		}
	}

	if err := s.store.UpdateChange(r.Context(), change); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update change request")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, change)
}

func (s *Service) handleExecuteChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	change, err := s.store.GetChange(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "change request not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if change.Status != model.ChangeStatusApproved {
		httputil.WriteError(w, http.StatusConflict, "change request must be approved before execution")
		return
	}

	change.Status = model.ChangeStatusExecuted
	if err := s.store.UpdateChange(r.Context(), change); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to execute change request")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, change)
}
