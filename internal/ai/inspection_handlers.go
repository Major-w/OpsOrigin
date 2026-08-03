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

// handleRunInspectionExperiment 执行一次对比实验(策略优化 A/B): 用覆盖的 prompt/scope
// 跑同一巡检任务, 运行记录带 experiment 标记, 与基线运行并列对比。
// body 可选: {"prompt_override":"...", "scope_override":"..."}。需 ai:tool:exec 权限。
func (s *Service) handleRunInspectionExperiment(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	existing, ok := s.Engine.GetInspection(r.PathValue("id"))
	if !ok || existing.TenantID != tenantFromCtx(r.Context()) {
		writeError(w, http.StatusNotFound, "inspection not found")
		return
	}
	var req agentic.ExperimentMeta
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.CreatedBy == "" {
		req.CreatedBy = tenantFromCtx(r.Context())
	}
	run, err := s.Engine.RunInspectionExperiment(r.Context(), r.PathValue("id"), &req)
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

// handleWorkforceMetrics 返回数字员工运营效果聚合指标(巡检健康 + 发现/转单/处置漏斗 + 工单统计)。
// 只读统计, 普通鉴权即可(无需 ai:tool:exec)。
func (s *Service) handleWorkforceMetrics(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	m := s.Engine.GetWorkforceMetrics(tenantID)
	s.Engine.FillTicketMetrics(r.Context(), tenantID, m)
	writeJSON(w, http.StatusOK, m)
}

// handleExportDataset 导出训练数据集(P3-1 反馈驱动微调):
// 聚合用户反馈(点赞/点踩 + 对话上下文)与处置案例(知识库), 返回 JSONL 文本。
// query 参数 format=jsonl(默认)/json。需 ai:tool:exec 权限。
func (s *Service) handleExportDataset(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	tenantID := tenantFromCtx(r.Context())

	feedbacks := []*agentic.Feedback{}
	if s.feedbackStore != nil {
		if fbs, err := s.feedbackStore.ListAll(r.Context(), tenantID); err == nil && fbs != nil {
			feedbacks = fbs
		}
	}

	jsonl, summary, err := s.Engine.ExportDataset(r.Context(), tenantID, feedbacks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if r.URL.Query().Get("format") == "json" {
		writeJSON(w, http.StatusOK, map[string]any{
			"summary": summary,
			"entries": jsonl,
		})
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="ai-dataset.ndjson"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(jsonl))
}

// handleListModels 返回模型池状态(多模型路由 P3): provider 名/默认模型/健康状态/权重。
// 只读, 普通鉴权即可。
func (s *Service) handleListModels(w http.ResponseWriter, r *http.Request) {
	models := s.Engine.GetModels()
	if models == nil {
		models = []agentic.ModelStatus{}
	}
	writeJSON(w, http.StatusOK, models)
}
