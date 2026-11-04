package itsm

import (
	"context"
	"errors"
	"net/http"

	"github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/store"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// canTransition 带租户流转规则的状态迁移校验（P2-⑯ 状态管理自定义）。
// 规则语义见 model.CanTransitionWithPolicy：只收紧不放大——
// 规则可禁用内置允许的边、可要求备注；内置禁止的边一律不放行。
// 无配置 / 规则停用 / 存储不支持时回退内置状态机，零配置行为完全不变。
func (s *Service) canTransition(ctx context.Context, ticket *model.Ticket, to model.TicketStatus, comment string) error {
	flow, err := s.store.GetStatusFlow(ctx, ticket.TenantID, ticket.CatalogID)
	if err == nil && flow != nil {
		return model.CanTransitionWithPolicy(ticket.Status, to, flow, comment)
	}
	// ErrNotFound(无配置) / 其他存储异常均回退内置(配置查询失败不放大也不阻断流转)。
	return model.CanTransition(ticket.Status, to)
}

// effectiveStatusFlow 返回当前生效配置；无配置返回 nil（非错误）。
func (s *Service) effectiveStatusFlow(ctx context.Context, tenantID, catalogID string) *model.StatusFlow {
	flow, err := s.store.GetStatusFlow(ctx, tenantID, catalogID)
	if err != nil {
		return nil
	}
	if flow != nil && !flow.Enabled {
		return nil
	}
	return flow
}

// handleGetStatusFlow 返回状态流转配置视图（内置允许边 + 覆盖合并，供前端矩阵渲染）。
// GET /api/v1/itsm/status-flows?catalog_id=
func (s *Service) handleGetStatusFlow(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	catalogID := r.URL.Query().Get("catalog_id")

	// 内置矩阵投影: {from: {to: true}}
	builtin := map[string]map[string]bool{}
	for _, from := range model.AllStatuses() {
		m := map[string]bool{}
		for _, to := range model.ValidTransitionsFrom(from) {
			m[string(to)] = true
		}
		builtin[string(from)] = m
	}

	flow := s.effectiveStatusFlow(r.Context(), tenantID, catalogID)
	overrides := []model.TransitionRule{}
	labels := map[string]string{}
	if flow != nil {
		overrides = flow.Transitions
		if flow.StatusLabels != nil {
			labels = flow.StatusLabels
		}
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"catalog_id":     catalogID,
		"builtin":        builtin,
		"overrides":      overrides, // 仅显式覆盖的边(Allowed=false / RequireComment=true)
		"status_labels":  labels,
		"enabled":        flow != nil,
		"statuses":       model.AllStatuses(),
		"builtin_labels": model.StatusLabels(), // 内置显示名, 前端做覆盖前展示
	})
}

// handlePutStatusFlow 保存租户流转配置（catalog_id 空=全局缺省）。
// PUT /api/v1/itsm/status-flows
func (s *Service) handlePutStatusFlow(w http.ResponseWriter, r *http.Request) {
	var req model.StatusFlow
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	// 目录维度由 query/body 共同决定: 优先 body.catalog_id
	if req.CatalogID == "" {
		req.CatalogID = r.URL.Query().Get("catalog_id")
	}
	// 服务端硬校验: 规则只允许引用内置状态对, 防御性拦脏数据
	for _, rule := range req.Transitions {
		if !model.IsValidStatus(rule.From) || !model.IsValidStatus(rule.To) {
			httputil.WriteError(w, http.StatusBadRequest, "unknown status in transitions: "+string(rule.From)+" -> "+string(rule.To))
			return
		}
	}
	req.CreatedBy = getUserID(r, req.CreatedBy)
	if err := s.store.UpsertStatusFlow(r.Context(), tenantID, &req); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, req)
}

// handleDeleteStatusFlow 清除配置（回退内置状态机）。
// DELETE /api/v1/itsm/status-flows?catalog_id=
func (s *Service) handleDeleteStatusFlow(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	catalogID := r.URL.Query().Get("catalog_id")
	if err := s.store.DeleteStatusFlow(r.Context(), tenantID, catalogID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "no status flow config")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}
