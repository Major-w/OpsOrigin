package ai

import (
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/pkg/auth"
)

// handleCreateFinetuneTask 创建反馈驱动微调任务(L3-1)。
// body: {"name":"...","dataset_ref":"latest","base_model":"deepseek-chat",
//
//	"method":"loRA","params":{"epochs":3}}。
//
// 需 ai:tool:exec 权限(创建训练任务属于配置变更)。
func (s *Service) handleCreateFinetuneTask(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		Name       string         `json:"name"`
		DatasetRef string         `json:"dataset_ref"`
		BaseModel  string         `json:"base_model"`
		Method     string         `json:"method"`
		Params     map[string]any `json:"params"`
	}
	_ = httputil.BindJSON(r, &req)

	t, err := s.Engine.CreateFinetuneTask(auth.TenantFromContext(r.Context()), req.Name, req.DatasetRef,
		req.BaseModel, req.Method, req.Params, func() string { u, _ := r.Context().Value("user_id").(string); return u }())
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, t)
}

// handleListFinetuneTasks 返回微调任务列表(创建时间倒序)。
func (s *Service) handleListFinetuneTasks(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.Engine.ListFinetuneTasks(auth.TenantFromContext(r.Context())))
}

// handleGetFinetuneTask 查询单个微调任务详情。
func (s *Service) handleGetFinetuneTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Engine.GetFinetuneTask(r.PathValue("id"))
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "finetune task not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}
