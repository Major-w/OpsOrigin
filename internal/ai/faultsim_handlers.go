package ai

import (
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/pkg/auth"
)

// handleListFaultScenarios 返回预置故障场景库。
func (s *Service) handleListFaultScenarios(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.Engine.ListFaultScenarios())
}

// handleStartFaultInjection 启动故障仿真演练(注入 + 触发巡检评估)。
// body: {"scenario_id":"fault-network-delay","target_id":"host-1","target_name":"web-01",
//
//	"inspection_id":"<巡检任务ID>"}。
//
// 需 ai:tool:exec 权限。
func (s *Service) handleStartFaultInjection(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		ScenarioID   string `json:"scenario_id"`
		TargetID     string `json:"target_id"`
		TargetName   string `json:"target_name"`
		InspectionID string `json:"inspection_id"`
	}
	_ = httputil.BindJSON(r, &req)

	run, err := s.Engine.StartFaultInjection(r.Context(), auth.TenantFromContext(r.Context()),
		req.ScenarioID, req.TargetID, req.TargetName, req.InspectionID)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, run)
}

// handleListFaultInjections 返回演练记录列表。
func (s *Service) handleListFaultInjections(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.Engine.ListFaultInjections(auth.TenantFromContext(r.Context())))
}

// handleRecoverFaultInjection 恢复/回滚一次演练(解除注入)。
func (s *Service) handleRecoverFaultInjection(w http.ResponseWriter, r *http.Request) {
	run, err := s.Engine.RecoverFaultInjection(r.PathValue("id"))
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, run)
}
