package ai

import (
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/pkg/auth"
)

// handleGenerateRemediations 从巡检运行记录生成自动修复动作(分级: blocked/auto/review)。
// 幂等: 同一 run 已生成则直接返回。需 ai:tool:exec 权限。
func (s *Service) handleGenerateRemediations(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	acts, err := s.Engine.GenerateRemediations(r.Context(), auth.TenantFromContext(r.Context()), r.PathValue("runID"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, acts)
}

// handleListRemediations 返回某 run 的修复动作列表。
func (s *Service) handleListRemediations(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.Engine.ListRemediations(auth.TenantFromContext(r.Context()), r.PathValue("runID")))
}

// handleExecuteRemediation 执行一条修复动作(分级: auto 直接执行 / review 触发审批)。
// body 可选: {"action_id": "..."}。
func (s *Service) handleExecuteRemediation(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		ActionID string `json:"action_id"`
	}
	_ = httputil.BindJSON(r, &req)
	if req.ActionID == "" {
		req.ActionID = r.PathValue("actionID")
	}
	act, err := s.Engine.ExecuteRemediation(r.Context(), auth.TenantFromContext(r.Context()), req.ActionID)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, act)
}
