package cmdb

import (
	"context"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// handleListSavedQueries 列出当前租户的查询策略。
func (s *Service) handleListSavedQueries(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteJSON(w, 200, []*model.SavedQuery{})
		return
	}
	qs, err := s.pg.ListSavedQueries(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if qs == nil {
		qs = []*model.SavedQuery{}
	}
	httputil.WriteJSON(w, 200, qs)
}

// handleCreateSavedQuery 保存一条查询策略（"我想查"）。
func (s *Service) handleCreateSavedQuery(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var q model.SavedQuery
	if err := httputil.BindJSON(r, &q); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if q.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	if q.Query == nil {
		q.Query = map[string]any{}
	}
	q.TenantID = tenantID

	if s.pg == nil {
		httputil.WriteJSON(w, 201, &q)
		return
	}
	if err := s.pg.CreateSavedQuery(r.Context(), &q); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 201, &q)
}

// handleDeleteSavedQuery 删除查询策略。
func (s *Service) handleDeleteSavedQuery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
		return
	}
	if err := s.pg.DeleteSavedQuery(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}

// handleResolveSavedQuery 应用一条保存查询，返回命中的 CI 列表（"我想查"一键复用）。
func (s *Service) handleResolveSavedQuery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteJSON(w, 200, []*model.CIModel{})
		return
	}
	q, err := s.pg.GetSavedQuery(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if q == nil {
		httputil.WriteError(w, 404, "saved query not found")
		return
	}
	cis, err := s.resolveSavedQueryCIs(r.Context(), tenantID, q)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cis == nil {
		cis = []*model.CIModel{}
	}
	httputil.WriteJSON(w, 200, map[string]any{"query": q, "count": len(cis), "items": cis})
}

// resolveSavedQueryFilter 把保存查询的结构化 Query 转为 repo 过滤条件。
// Query 形状：{"q": "...", "status": "...", "attrs": {"ip": "1.2.3.4"}}。
func resolveSavedQueryFilter(q *model.SavedQuery) *model.SavedQueryFilter {
	f := &model.SavedQueryFilter{Attrs: map[string]string{}}
	if q == nil || q.Query == nil {
		return f
	}
	f.TypeID = q.TypeID
	if v, ok := q.Query["q"].(string); ok {
		f.Q = v
	}
	if v, ok := q.Query["status"].(string); ok {
		f.Status = v
	}
	if attrs, ok := q.Query["attrs"].(map[string]any); ok {
		for k, v := range attrs {
			if s, ok := v.(string); ok {
				f.Attrs[k] = s
			}
		}
	}
	return f
}

// resolveSavedQueryCIs 按保存查询解析命中的 CI 列表（供"我想查"与采集动态目标复用）。
func (s *Service) resolveSavedQueryCIs(ctx context.Context, tenantID string, q *model.SavedQuery) ([]*model.CIModel, error) {
	if s.pg == nil {
		return nil, nil
	}
	return s.pg.ListCIBySavedQuery(ctx, tenantID, resolveSavedQueryFilter(q))
}

// ResolveSavedQueryTargetIDs 解析保存查询命中的 CI，并提取可下发的采集目标标识
// （优先 resource_id 属性，回退到 CI id）。供 P1-4 采集动态目标使用。
func (s *Service) ResolveSavedQueryTargetIDs(ctx context.Context, tenantID, savedQueryID string) ([]string, error) {
	q, err := s.pg.GetSavedQuery(ctx, savedQueryID, tenantID)
	if err != nil {
		return nil, err
	}
	if q == nil {
		return nil, nil
	}
	cis, err := s.resolveSavedQueryCIs(ctx, tenantID, q)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(cis))
	for _, ci := range cis {
		if ci.Attributes != nil {
			if rid, ok := ci.Attributes["resource_id"].(string); ok && rid != "" {
				ids = append(ids, rid)
				continue
			}
		}
		ids = append(ids, ci.ID)
	}
	return ids, nil
}
