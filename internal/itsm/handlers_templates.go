package itsm

import (
	"net/http"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// handleListTicketTemplates 列出本租户全部提单模板。
// GET /api/v1/itsm/ticket-templates
func (s *Service) handleListTicketTemplates(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	templates, err := s.store.ListTicketTemplates(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if templates == nil {
		templates = []*model.TicketTemplate{}
	}
	httputil.WriteJSON(w, http.StatusOK, templates)
}

// handleCreateTicketTemplate 新建或更新提单模板（按 body.id 是否存在决定插入或更新）。
// POST /api/v1/itsm/ticket-templates
func (s *Service) handleCreateTicketTemplate(w http.ResponseWriter, r *http.Request) {
	var t model.TicketTemplate
	if err := httputil.BindJSON(r, &t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if t.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	t.CreatedBy = getUserID(r, t.CreatedBy)
	if err := s.store.UpsertTicketTemplate(r.Context(), auth.TenantFromContext(r.Context()), &t); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

// handleUpdateTicketTemplate 按路径 id 更新提单模板。
// PUT /api/v1/itsm/ticket-templates/{id}
func (s *Service) handleUpdateTicketTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var t model.TicketTemplate
	if err := httputil.BindJSON(r, &t); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if t.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	t.ID = id
	t.CreatedBy = getUserID(r, t.CreatedBy)
	if err := s.store.UpsertTicketTemplate(r.Context(), auth.TenantFromContext(r.Context()), &t); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

// handleDeleteTicketTemplate 删除提单模板。
// DELETE /api/v1/itsm/ticket-templates/{id}
func (s *Service) handleDeleteTicketTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.DeleteTicketTemplate(r.Context(), tenantID, id); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "template not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
