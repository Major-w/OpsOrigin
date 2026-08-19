package service

import (
	"net/http"
	"strconv"

	"github.com/opsorigin/core/internal/lowcode/model"

	"github.com/opsorigin/core/pkg/httputil"
)

// Handler 低代码HTTP处理器
type Handler struct {
	svc *Service
}

// NewHandler 创建HTTP处理器
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 注册路由
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// 数据模型管理
	mux.HandleFunc("POST /api/v1/lowcode/models", h.handleCreateModel)
	mux.HandleFunc("GET /api/v1/lowcode/models", h.handleListModels)
	mux.HandleFunc("GET /api/v1/lowcode/models/{id}", h.handleGetModel)
	mux.HandleFunc("PUT /api/v1/lowcode/models/{id}", h.handleUpdateModel)
	mux.HandleFunc("DELETE /api/v1/lowcode/models/{id}", h.handleDeleteModel)

	// 模型实例管理
	mux.HandleFunc("POST /api/v1/lowcode/instances", h.handleCreateInstance)
	mux.HandleFunc("GET /api/v1/lowcode/instances", h.handleListInstances)
	mux.HandleFunc("GET /api/v1/lowcode/instances/{id}", h.handleGetInstance)
	mux.HandleFunc("PUT /api/v1/lowcode/instances/{id}", h.handleUpdateInstance)
	mux.HandleFunc("DELETE /api/v1/lowcode/instances/{id}", h.handleDeleteInstance)

	// 页面管理
	mux.HandleFunc("POST /api/v1/lowcode/pages", h.handleCreatePage)
	mux.HandleFunc("GET /api/v1/lowcode/pages", h.handleListPages)
	mux.HandleFunc("GET /api/v1/lowcode/pages/{id}", h.handleGetPage)
	mux.HandleFunc("PUT /api/v1/lowcode/pages/{id}", h.handleUpdatePage)
	mux.HandleFunc("DELETE /api/v1/lowcode/pages/{id}", h.handleDeletePage)

	// 表单验证
	mux.HandleFunc("POST /api/v1/lowcode/validate", h.handleValidateForm)

	// 工具接口
	mux.HandleFunc("GET /api/v1/lowcode/sql/{model_id}", h.handleGenerateSQL)
}

// ========== 数据模型管理 ==========

func (h *Handler) handleCreateModel(w http.ResponseWriter, r *http.Request) {
	var m model.DataModel
	if err := httputil.BindJSON(r, &m); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 获取租户ID（从上下文）
	m.TenantID = getTenantID(r)

	if err := h.svc.CreateDataModel(r.Context(), &m); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, m)
}

func (h *Handler) handleListModels(w http.ResponseWriter, r *http.Request) {
	tenantID := getTenantID(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = 20
	}

	models, total, err := h.svc.ListDataModels(r.Context(), tenantID, limit, offset)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"items":  models,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) handleGetModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	m, err := h.svc.GetDataModel(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if m == nil {
		httputil.WriteError(w, http.StatusNotFound, "model not found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) handleUpdateModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	// 获取现有模型
	existing, err := h.svc.GetDataModel(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "model not found")
		return
	}

	// 解析更新数据
	var m model.DataModel
	if err := httputil.BindJSON(r, &m); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 保留不可更新的字段
	m.ID = existing.ID
	m.TenantID = existing.TenantID
	m.CreatedAt = existing.CreatedAt

	if err := h.svc.UpdateDataModel(r.Context(), &m); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	if err := h.svc.DeleteDataModel(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ========== 模型实例管理 ==========

func (h *Handler) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	var inst model.ModelInstance
	if err := httputil.BindJSON(r, &inst); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 获取租户ID
	inst.TenantID = getTenantID(r)

	if err := h.svc.CreateInstance(r.Context(), &inst); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, inst)
}

func (h *Handler) handleListInstances(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model_id")
	tenantID := getTenantID(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = 20
	}

	instances, total, err := h.svc.ListInstances(r.Context(), modelID, tenantID, limit, offset)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"items":  instances,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	inst, err := h.svc.GetInstance(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if inst == nil {
		httputil.WriteError(w, http.StatusNotFound, "instance not found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, inst)
}

func (h *Handler) handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	// 获取现有实例
	existing, err := h.svc.GetInstance(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "instance not found")
		return
	}

	// 解析更新数据
	var inst model.ModelInstance
	if err := httputil.BindJSON(r, &inst); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 保留不可更新的字段
	inst.ID = existing.ID
	inst.ModelID = existing.ModelID
	inst.TenantID = existing.TenantID
	inst.CreatedAt = existing.CreatedAt

	if err := h.svc.UpdateInstance(r.Context(), &inst); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, inst)
}

func (h *Handler) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	if err := h.svc.DeleteInstance(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ========== 页面管理 ==========

func (h *Handler) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	var p model.PageDefinition
	if err := httputil.BindJSON(r, &p); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 获取租户ID
	p.TenantID = getTenantID(r)

	if err := h.svc.CreatePage(r.Context(), &p); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, p)
}

func (h *Handler) handleListPages(w http.ResponseWriter, r *http.Request) {
	tenantID := getTenantID(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = 20
	}

	pages, total, err := h.svc.ListPages(r.Context(), tenantID, limit, offset)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"items":  pages,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) handleGetPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	p, err := h.svc.GetPage(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == nil {
		httputil.WriteError(w, http.StatusNotFound, "page not found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) handleUpdatePage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	// 获取现有页面
	existing, err := h.svc.GetPage(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "page not found")
		return
	}

	// 解析更新数据
	var p model.PageDefinition
	if err := httputil.BindJSON(r, &p); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 保留不可更新的字段
	p.ID = existing.ID
	p.TenantID = existing.TenantID
	p.CreatedAt = existing.CreatedAt

	if err := h.svc.UpdatePage(r.Context(), &p); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := getTenantID(r)

	if err := h.svc.DeletePage(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ========== 表单验证 ==========

func (h *Handler) handleValidateForm(w http.ResponseWriter, r *http.Request) {
	var data model.FormData
	if err := httputil.BindJSON(r, &data); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	errors, err := h.svc.ValidateForm(r.Context(), &data)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"valid":  len(errors) == 0,
		"errors": errors,
	})
}

// ========== 工具接口 ==========

func (h *Handler) handleGenerateSQL(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("model_id")
	tenantID := getTenantID(r)

	m, err := h.svc.GetDataModel(r.Context(), modelID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if m == nil {
		httputil.WriteError(w, http.StatusNotFound, "model not found")
		return
	}

	sql := h.svc.GenerateCreateTableSQL(m)

	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"sql": sql,
	})
}

// ========== 工具函数 ==========

func getTenantID(r *http.Request) string {
	if t := r.Context().Value("tenant_id"); t != nil {
		if tenantID, ok := t.(string); ok && tenantID != "" {
			return tenantID
		}
	}
	return "default"
}
