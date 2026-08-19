package cmdb

import (
	"encoding/json"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// handleListModelCategories 列出租户下全部分类（内置 + 自定义）。
func (s *Service) handleListModelCategories(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	if s.pg == nil {
		// 内存回退：仅内置 5 类
		now := time.Now()
		items := make([]*model.ModelCategory, 0, len(model.ModelCategories()))
		for i, name := range model.ModelCategories() {
			items = append(items, &model.ModelCategory{ID: name, TenantID: tenantID, Name: name, Builtin: true, SortOrder: i + 1, CreatedAt: now})
		}
		httputil.WriteJSON(w, 200, items)
		return
	}
	items, err := s.pg.ListModelCategories(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*model.ModelCategory{}
	}
	httputil.WriteJSON(w, 200, items)
}

// handleCreateModelCategory 新增自定义分类（内置分类不可新增同名）。
func (s *Service) handleCreateModelCategory(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "分类名称不能为空")
		return
	}
	if s.pg == nil {
		httputil.WriteError(w, http.StatusBadRequest, "postgres unavailable")
		return
	}
	existing, err := s.pg.GetModelCategoryByName(r.Context(), req.Name, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing != nil {
		httputil.WriteError(w, http.StatusBadRequest, "分类「"+req.Name+"」已存在")
		return
	}
	c := &model.ModelCategory{TenantID: tenantID, Name: req.Name, SortOrder: req.SortOrder}
	if err := s.pg.CreateModelCategory(r.Context(), c); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 201, c)
}

// handleDeleteModelCategory 删除自定义分类（内置 / 被模型引用时拒绝）。
func (s *Service) handleDeleteModelCategory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if s.pg == nil {
		httputil.WriteError(w, http.StatusBadRequest, "postgres unavailable")
		return
	}
	if err := s.pg.DeleteModelCategory(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}
