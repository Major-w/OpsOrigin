package ai

import (
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/ai/agentic"
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

// —— 可复用剧本(runbook)接口: 沉淀复用, 降低 LLM 成本并提升可审计性 ——

// handleListRunbooks 返回租户可复用剧本列表。
func (s *Service) handleListRunbooks(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.Engine.ListRunbooks(auth.TenantFromContext(r.Context())))
}

// handleGetRunbook 剧本详情。
func (s *Service) handleGetRunbook(w http.ResponseWriter, r *http.Request) {
	rb, ok := s.Engine.GetRunbook(r.PathValue("id"))
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "runbook not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rb)
}

// handleExecuteRunbook 按剧本执行处置(复用修复执行器+审批闸门+验证闭环)。
func (s *Service) handleExecuteRunbook(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	rb, err := s.Engine.ExecuteRunbook(r.Context(), auth.TenantFromContext(r.Context()), r.PathValue("id"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rb)
}

// —— 自治分级配置 (notify/suggest/act_with_approval/autonomous) ——

// handleGetAutonomy 返回当前自治档位。
func (s *Service) handleGetAutonomy(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"autonomy": s.Engine.GetAutonomy(),
		"levels":   []string{"notify", "suggest", "act_with_approval", "autonomous"},
		"note":     "notify=仅通知; suggest=生成建议不执行; act_with_approval=高危走审批(默认); autonomous=自主执行(仅危险词拦截)",
	})
}

// handleSetAutonomy 设置自治档位。body: {"autonomy": "autonomous"}。
func (s *Service) handleSetAutonomy(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		Autonomy string `json:"autonomy"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	level := agentic.AutonomyLevel(req.Autonomy)
	switch level {
	case agentic.AutonomyNotify, agentic.AutonomySuggest, agentic.AutonomyActWithApproval, agentic.AutonomyAutonomous:
	default:
		httputil.WriteError(w, http.StatusBadRequest, "invalid autonomy level: "+req.Autonomy)
		return
	}
	s.Engine.SetAutonomy(level)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"autonomy": s.Engine.GetAutonomy()})
}

// —— 告警第一道防线 (告警 → agent 自动分级, 结果回写告警系统) ——

// handleAlertTriage 对单条告警执行 AI 分级(根因假设/分级/建议动作)。
// 供监控告警 Webhook / 事件链自动调用, 也可人工触发。
func (s *Service) handleAlertTriage(w http.ResponseWriter, r *http.Request) {
	var req agentic.AlertTriageRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Title == "" {
		httputil.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}
	res, err := s.Engine.RunAlertTriage(r.Context(), req)
	if err != nil {
		// LLM 失败仍返回结构化结果(含 escalate=true), 不阻断调用方。
		httputil.WriteJSON(w, http.StatusOK, res)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, res)
}

// —— RCA 基准 (记录 AI 根因结论 + 人工标注准确率, 沉淀内部证据) ——

// handleRecordRCA 记录一条 AI 根因分析(供事后标注)。
func (s *Service) handleRecordRCA(w http.ResponseWriter, r *http.Request) {
	var rec agentic.RCARecord
	if err := httputil.BindJSON(r, &rec); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, s.Engine.RecordRCA(auth.TenantFromContext(r.Context()), &rec))
}

// handleLabelRCA 人工标注 RCA 是否准确。body: {"accurate": true, "note": "..."}。
func (s *Service) handleLabelRCA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Accurate bool   `json:"accurate"`
		Note     string `json:"note,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	rec, err := s.Engine.LabelRCA(auth.TenantFromContext(r.Context()), r.PathValue("id"), req.Accurate, req.Note)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rec)
}

// handleListRCARecords 列出 RCA 记录 + 准确率统计。
func (s *Service) handleListRCARecords(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"records": s.Engine.ListRCARecords(tenantID),
		"stats":   s.Engine.RCAAccuracy(tenantID),
	})
}

// —— 事故复盘沉淀 (复盘学习, 可回灌记忆) ——

// handleCreatePostmortem 创建复盘记录。
func (s *Service) handleCreatePostmortem(w http.ResponseWriter, r *http.Request) {
	var pm agentic.Postmortem
	if err := httputil.BindJSON(r, &pm); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if pm.Title == "" {
		httputil.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, s.Engine.CreatePostmortem(auth.TenantFromContext(r.Context()), &pm))
}

// handlePublishPostmortem 发布复盘(可选回灌记忆)。body: {"with_memory": true}。
func (s *Service) handlePublishPostmortem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WithMemory bool `json:"with_memory"`
	}
	_ = httputil.BindJSON(r, &req)
	pm, err := s.Engine.PublishPostmortem(auth.TenantFromContext(r.Context()), r.PathValue("id"), req.WithMemory)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, pm)
}

// handleListPostmortems 列出复盘记录。
func (s *Service) handleListPostmortems(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.Engine.ListPostmortems(auth.TenantFromContext(r.Context())))
}
