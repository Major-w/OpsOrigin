package ai

import (
	"encoding/json"
	"net/http"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
)

// handleListInspectionTemplates 返回内置巡检模板目录(配置漂移/容量/安全基线/证书/主机健康/知识时效)。
func (s *Service) handleListInspectionTemplates(w http.ResponseWriter, r *http.Request) {
	items := s.Engine.ListInspectionTemplates()
	if items == nil {
		items = []*agentic.InspectionTemplate{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleListInspections 列出当前租户的巡检任务。
func (s *Service) handleListInspections(w http.ResponseWriter, r *http.Request) {
	items := s.Engine.ListInspections(tenantFromCtx(r.Context()))
	if items == nil {
		items = []*agentic.Inspection{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleGetInspection 查询单个巡检任务(含下次运行时间与最近状态)。
func (s *Service) handleGetInspection(w http.ResponseWriter, r *http.Request) {
	in, ok := s.Engine.GetInspection(r.PathValue("id"))
	if !ok || in.TenantID != tenantFromCtx(r.Context()) {
		writeError(w, http.StatusNotFound, "inspection not found")
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// handleCreateInspection 创建巡检任务。可基于模板(template_id)预填巡检指令。
func (s *Service) handleCreateInspection(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var in agentic.Inspection
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.TenantID = tenantFromCtx(r.Context())
	created, err := s.Engine.CreateInspection(r.Context(), &in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleUpdateInspection 更新巡检任务(改周期/范围/启停会自动重排下次运行时间)。
func (s *Service) handleUpdateInspection(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	existing, ok := s.Engine.GetInspection(r.PathValue("id"))
	if !ok || existing.TenantID != tenantFromCtx(r.Context()) {
		writeError(w, http.StatusNotFound, "inspection not found")
		return
	}
	var in agentic.Inspection
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.ID = r.PathValue("id")
	in.TenantID = existing.TenantID
	updated, err := s.Engine.UpdateInspection(r.Context(), &in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteInspection 删除巡检任务及其运行记录。
func (s *Service) handleDeleteInspection(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	tenantID := tenantFromCtx(r.Context())
	existing, ok := s.Engine.GetInspection(r.PathValue("id"))
	if !ok || existing.TenantID != tenantID {
		writeError(w, http.StatusNotFound, "inspection not found")
		return
	}
	if err := s.Engine.DeleteInspection(r.Context(), r.PathValue("id"), tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunInspection 立即手动触发一次巡检, 同步返回本次运行记录。
func (s *Service) handleRunInspection(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	existing, ok := s.Engine.GetInspection(r.PathValue("id"))
	if !ok || existing.TenantID != tenantFromCtx(r.Context()) {
		writeError(w, http.StatusNotFound, "inspection not found")
		return
	}
	run, err := s.Engine.RunInspection(r.Context(), r.PathValue("id"), "manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleListInspectionRuns 列出某巡检任务的历史运行记录(最新在前)。
func (s *Service) handleListInspectionRuns(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.Engine.GetInspection(r.PathValue("id"))
	if !ok || existing.TenantID != tenantFromCtx(r.Context()) {
		writeError(w, http.StatusNotFound, "inspection not found")
		return
	}
	runs := s.Engine.ListInspectionRuns(r.PathValue("id"))
	if runs == nil {
		runs = []*agentic.InspectionRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleEscalateInspectionRun 将某次巡检运行记录中的发现批量转 ITSM 工单, 形成处置闭环。
// body 可选: {"severity": "high"|"medium"|"low"}。默认 medium(高危+中危一起转)。
// 幂等: 已转工单的发现自动跳过。需 ai:tool:exec 权限。
func (s *Service) handleEscalateInspectionRun(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		Severity string `json:"severity"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	run, err := s.Engine.EscalateInspectionRun(r.Context(), tenantID, r.PathValue("runID"), req.Severity)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}
