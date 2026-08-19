package service

import (
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/internal/lowcode/version"
)

// VersionHandler 版本管理API处理器
type VersionHandler struct {
	versionSvc *version.VersionService
}

// NewVersionHandler 创建版本管理处理器
func NewVersionHandler(versionSvc *version.VersionService) *VersionHandler {
	return &VersionHandler{versionSvc: versionSvc}
}

// RegisterRoutes 注册路由
func (h *VersionHandler) RegisterRoutes(mux *http.ServeMux) {
	// 页面版本管理
	mux.HandleFunc("GET /api/v1/lowcode/pages/{id}/versions", h.handlePageVersions)
	mux.HandleFunc("GET /api/v1/lowcode/pages/{id}/versions/{versionId}", h.handlePageVersion)
	mux.HandleFunc("POST /api/v1/lowcode/pages/{id}/versions/{versionId}/publish", h.handlePublishPageVersion)
	mux.HandleFunc("POST /api/v1/lowcode/pages/{id}/versions/{versionId}/rollback", h.handleRollbackPageVersion)

	// 模型版本管理
	mux.HandleFunc("GET /api/v1/lowcode/models/{id}/versions", h.handleModelVersions)
	mux.HandleFunc("GET /api/v1/lowcode/models/{id}/versions/{versionId}", h.handleModelVersion)
	mux.HandleFunc("POST /api/v1/lowcode/models/{id}/versions/{versionId}/publish", h.handlePublishModelVersion)
	mux.HandleFunc("POST /api/v1/lowcode/models/{id}/versions/{versionId}/rollback", h.handleRollbackModelVersion)
}

// handlePageVersions 处理页面版本列表
func (h *VersionHandler) handlePageVersions(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
	if pageID == "" {
		http.Error(w, `{"error":"missing page id"}`, http.StatusBadRequest)
		return
	}

	versions := h.versionSvc.GetPageVersions(pageID)
	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    versions,
	})
}

// handlePageVersion 处理单个页面版本
func (h *VersionHandler) handlePageVersion(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
	versionID := r.PathValue("versionId")
	if pageID == "" || versionID == "" {
		http.Error(w, `{"error":"missing page id or version id"}`, http.StatusBadRequest)
		return
	}

	pageVersion, err := h.versionSvc.GetPageVersion(pageID, versionID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    pageVersion,
	})
}

// handlePublishPageVersion 处理发布页面版本
func (h *VersionHandler) handlePublishPageVersion(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
	versionID := r.PathValue("versionId")
	if pageID == "" || versionID == "" {
		http.Error(w, `{"error":"missing page id or version id"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		PublishedBy string `json:"published_by"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	err := h.versionSvc.PublishPageVersion(pageID, versionID, req.PublishedBy)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "version published successfully",
	})
}

// handleRollbackPageVersion 处理回滚页面版本
func (h *VersionHandler) handleRollbackPageVersion(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
	versionID := r.PathValue("versionId")
	if pageID == "" || versionID == "" {
		http.Error(w, `{"error":"missing page id or version id"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		OperatorID string `json:"operator_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	newVersion, err := h.versionSvc.RollbackPageVersion(pageID, versionID, req.OperatorID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    newVersion,
	})
}

// handleModelVersions 处理模型版本列表
func (h *VersionHandler) handleModelVersions(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	if modelID == "" {
		http.Error(w, `{"error":"missing model id"}`, http.StatusBadRequest)
		return
	}

	versions := h.versionSvc.GetModelVersions(modelID)
	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    versions,
	})
}

// handleModelVersion 处理单个模型版本
func (h *VersionHandler) handleModelVersion(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	versionID := r.PathValue("versionId")
	if modelID == "" || versionID == "" {
		http.Error(w, `{"error":"missing model id or version id"}`, http.StatusBadRequest)
		return
	}

	modelVersion, err := h.versionSvc.GetModelVersion(modelID, versionID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    modelVersion,
	})
}

// handlePublishModelVersion 处理发布模型版本
func (h *VersionHandler) handlePublishModelVersion(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	versionID := r.PathValue("versionId")
	if modelID == "" || versionID == "" {
		http.Error(w, `{"error":"missing model id or version id"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		PublishedBy string `json:"published_by"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	err := h.versionSvc.PublishModelVersion(modelID, versionID, req.PublishedBy)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "version published successfully",
	})
}

// handleRollbackModelVersion 处理回滚模型版本
func (h *VersionHandler) handleRollbackModelVersion(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("id")
	versionID := r.PathValue("versionId")
	if modelID == "" || versionID == "" {
		http.Error(w, `{"error":"missing model id or version id"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		OperatorID string `json:"operator_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	newVersion, err := h.versionSvc.RollbackModelVersion(modelID, versionID, req.OperatorID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    newVersion,
	})
}
