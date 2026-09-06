// 任务级 Agent 权限策略(T3): permission_mode × tool risk 执行判定 + 中途审批排队。
//
// 四种权限模式(对齐 Claude Code Plan/default/acceptEdits/bypassPermissions):
//
//	readonly  — 仅放行只读工具, 任何写操作直接拒绝(不可批), 适合调研/出方案;
//	standard  — 默认模式; 只读与"任务工作区命名空间内"的写入(doc_write 等)自动放行,
//	            跨域写(ticket_create/script_run/写知识库等)挂起等人工批复;
//	auto      — 读/写自动放行, 仅 critical(script_run 等不可逆操作)需确认;
//	full_auto — 全部放行, 仅审计留痕。
//
// 审批流程: 闸门判定需审批 → 建审批单(内存) → 任务转 waiting_approval + SSE 广播
// → 用户经 POST /ai/tasks/{id}/approvals/{call_id} 批复 → 任务恢复 running, 模型继续。
// reject 时拒绝原因回注为工具结果, 模型据此改道。
// 注意: 闸门在 Runner goroutine 内同步阻塞(引擎工具执行链路上), 同一任务同一时刻至多一张待批单;
// task_confirm(模型主动发起的确认)复用同一套排队与批复通道。
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"github.com/opsorigin/core/pkg/llm"
)

// taskScopedWriteTools 任务命名空间内的产物写入工具: standard 模式下自动放行(写的是本任务自己的工作区)。
var taskScopedWriteTools = map[string]bool{
	"doc_write":        true,
	"task_update_step": true,
}

// toolRisk 解析工具风险等级: 显式 Risk 标注 > 内置默认表 > write(保守缺省)。
// 内置默认表兜底覆盖"未标 Risk 的存量注册"与"外部 MCP proxy 工具"。
func (s *Service) toolRisk(name string) string {
	if s.Registry != nil {
		if d, ok := s.Registry.Get(name); ok {
			switch d.Risk {
			case "read", "write", "critical":
				return d.Risk
			}
		}
	}
	switch name {
	case "cmdb_query", "script_list", "script_detail", "script_executions", "knowledge_search":
		return "read"
	case "script_run":
		return "critical"
	default:
		return "write"
	}
}

// judgeToolPermission 权限模式 × 风险等级 → allow | deny | approval。
func judgeToolPermission(mode, risk, tool string) string {
	switch mode {
	case "full_auto":
		return "allow"
	case "readonly":
		if risk == "read" {
			return "allow"
		}
		return "deny"
	case "auto":
		if risk == "critical" {
			return "approval"
		}
		return "allow"
	default: // standard
		if risk == "read" {
			return "allow"
		}
		if risk == "write" && taskScopedWriteTools[tool] {
			return "allow" // 本任务工作区写入, 不出命名空间
		}
		return "approval"
	}
}

// taskApproval 一张审批单(工具闸门拦截 或 模型主动 task_confirm)。
type taskApproval struct {
	CallID    string         `json:"call_id"`
	TaskID    string         `json:"task_id"`
	Kind      string         `json:"kind"`               // tool_gate=权限闸门拦截 | confirm=模型主动确认
	Tool      string         `json:"tool,omitempty"`     // tool_gate: 被拦截的工具名
	Args      map[string]any `json:"args,omitempty"`     // 调用参数(供用户评估)
	Question  string         `json:"question,omitempty"` // confirm: 模型想确认的问题
	Status    string         `json:"status"`             // pending | approved | rejected
	Comment   string         `json:"comment,omitempty"`  // 用户批注
	CreatedAt time.Time      `json:"created_at"`
	resolve   chan *taskApproval
}

// enforceTaskPolicy 闸门实现: 按 mode×risk 判定; 需审批时建单挂起等批复。
// 调用点在 Runner goroutine 的工具执行链路内, 可安全读写活体任务 t。
func (s *Service) enforceTaskPolicy(ctx context.Context, t *AITask, tool string, args map[string]any) (bool, string) {
	risk := s.toolRisk(tool)
	verdict := judgeToolPermission(t.PermissionMode, risk, tool)
	switch verdict {
	case "allow":
		return true, ""
	case "deny":
		slog.Info("ai task tool denied by mode", "task_id", t.ID, "tool", tool, "risk", risk, "mode", t.PermissionMode)
		return false, fmt.Sprintf("当前任务权限模式为「%s」, 不允许执行写入类工具 %q(%s 级); 请改用查询类工具完成任务, 或让用户调整任务权限模式后重试",
			permissionModeLabel(t.PermissionMode), tool, risk)
	}

	// approval: 建单 → waiting_approval → 阻塞等批复
	ap := &taskApproval{
		CallID: uuid.New().String(), TaskID: t.ID, Kind: "tool_gate",
		Tool: tool, Args: args, Status: "pending",
		CreatedAt: time.Now(), resolve: make(chan *taskApproval, 1),
	}
	s.taskMu.Lock()
	s.taskApprovals[ap.CallID] = ap
	s.taskMu.Unlock()

	slog.Info("ai task approval required", "task_id", t.ID, "tool", tool, "risk", risk, "call_id", ap.CallID)
	t.Status = "waiting_approval"
	s.saveTask(persistCtx(), t)
	s.emitTask(t.ID, "status", map[string]string{"status": "waiting_approval", "task_id": t.ID})
	s.emitTask(t.ID, "approval_required", ap)

	return s.waitApproval(ctx, t, ap)
}

// waitApproval 阻塞等待用户批复/任务取消/超时, 并在结束后恢复任务 running 态。
func (s *Service) waitApproval(ctx context.Context, t *AITask, ap *taskApproval) (bool, string) {
	select {
	case r := <-ap.resolve:
		s.taskMu.Lock()
		delete(s.taskApprovals, ap.CallID)
		s.taskMu.Unlock()
		// 恢复执行态(无论批否, 任务继续由模型接手)
		t.Status = "running"
		s.saveTask(persistCtx(), t)
		s.emitTask(t.ID, "status", map[string]string{"status": "running", "task_id": t.ID})
		s.emitTask(t.ID, "approval_resolved", map[string]string{"call_id": ap.CallID, "action": r.Status})
		if r.Status == "approved" {
			slog.Info("ai task approval granted", "task_id", t.ID, "tool", ap.Tool, "call_id", ap.CallID)
			return true, ""
		}
		msg := "用户拒绝了该操作"
		if r.Comment != "" {
			msg += ", 理由: " + r.Comment
		}
		slog.Info("ai task approval rejected", "task_id", t.ID, "tool", ap.Tool, "call_id", ap.CallID)
		return false, msg + "; 请根据用户理由调整方案(更换工具/修改参数/改变思路), 或说明需要该操作的原因后再次请求确认"
	case <-ctx.Done():
		s.taskMu.Lock()
		delete(s.taskApprovals, ap.CallID)
		ap.Status = "rejected"
		ap.Comment = "任务已取消"
		s.taskMu.Unlock()
		return false, "任务已取消"
	case <-time.After(24 * time.Hour):
		s.taskMu.Lock()
		delete(s.taskApprovals, ap.CallID)
		s.taskMu.Unlock()
		return false, "审批等待超时(24 小时无人批复), 任务终止该操作"
	}
}

// requestTaskConfirm 模型主动发起的中途确认(task_confirm 工具的后端实现)。
func (s *Service) requestTaskConfirm(ctx context.Context, t *AITask, question string, extra map[string]any) (map[string]any, error) {
	ap := &taskApproval{
		CallID: uuid.New().String(), TaskID: t.ID, Kind: "confirm",
		Question: question, Status: "pending",
		CreatedAt: time.Now(), resolve: make(chan *taskApproval, 1),
	}
	if extra != nil {
		ap.Args = extra
	}
	s.taskMu.Lock()
	s.taskApprovals[ap.CallID] = ap
	s.taskMu.Unlock()

	t.Status = "waiting_approval"
	s.saveTask(persistCtx(), t)
	s.emitTask(t.ID, "status", map[string]string{"status": "waiting_approval", "task_id": t.ID})
	s.emitTask(t.ID, "approval_required", ap)

	allowed, reason := s.waitApproval(ctx, t, ap)
	if !allowed && reason == "任务已取消" {
		return nil, ctx.Err()
	}
	out := map[string]any{
		"status": map[bool]string{true: "approved", false: "rejected"}[allowed],
	}
	if ap.Comment != "" {
		out["user_comment"] = ap.Comment
	}
	out["message"] = map[bool]string{true: "用户已同意, 请继续执行", false: "用户未同意, 请参考批复意见调整"}[allowed]
	return out, nil
}

// handleTaskApproval POST /ai/tasks/{id}/approvals/{call_id} — 用户批复 {action: approve|reject, comment}。
func (s *Service) handleTaskApproval(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	var req struct {
		Action  string `json:"action"`
		Comment string `json:"comment"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Action != "approve" && req.Action != "reject" {
		httputil.WriteError(w, http.StatusBadRequest, "action must be approve or reject")
		return
	}
	callID := r.PathValue("call_id")
	s.taskMu.Lock()
	ap := s.taskApprovals[callID]
	if ap == nil || ap.TaskID != t.ID || ap.Status != "pending" {
		s.taskMu.Unlock()
		httputil.WriteError(w, http.StatusNotFound, "approval not found or already resolved")
		return
	}
	ap.Status = map[string]string{"approve": "approved", "reject": "rejected"}[req.Action]
	ap.Comment = req.Comment
	s.taskMu.Unlock()
	ap.resolve <- ap // 闸门 goroutine 收取后负责清理与恢复任务状态
	httputil.WriteJSON(w, http.StatusOK, ap)
}

// handleListTaskApprovals GET /ai/tasks/{id}/approvals — 待批审批单列表(断线重连后恢复审批卡)。
func (s *Service) handleListTaskApprovals(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	out := []*taskApproval{}
	s.taskMu.Lock()
	for _, ap := range s.taskApprovals {
		if ap.TaskID == t.ID && ap.Status == "pending" {
			out = append(out, ap)
		}
	}
	s.taskMu.Unlock()
	httputil.WriteJSON(w, http.StatusOK, out)
}

func permissionModeLabel(mode string) string {
	switch mode {
	case "readonly":
		return "只读"
	case "auto":
		return "自动执行"
	case "full_auto":
		return "完全托管"
	default:
		return "标准确认"
	}
}

// persistCtx 存取用的短超时 ctx(与 Runner 既有落库风格一致; WithTimeout 到期自取消, 无需额外回收)。
func persistCtx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cancel // 显式放弃 cancel: 依赖 5s 超时自回收(调用点均为一次性短写)
	return ctx
}

// ——— 规划轮(T3): LLM 产 checklist ———

// planTask 规划轮: 用一次 LLM 调用把任务目标拆解为步骤 checklist。
// 走 Engine.ChatStream(与执行共享任务会话, 执行时模型可看到规划讨论), 解析模型输出的 JSON 步骤数组。
// 成功 → 更新 plan_steps 并广播 plan 事件; 失败 → 广播 plan_failed(保留手动编辑路径)。
func (s *Service) planTask(t *AITask) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sys := "你是任务规划器。把用户的任务目标拆解为清晰的执行步骤清单(checklist)。\n" +
		"要求:\n" +
		"1. 拆成 3~8 个步骤, 每步是一个可独立验证的产出(动词开头, 具体、不空洞);\n" +
		"2. 顺序合理, 前置信息收集在前, 汇总/交付在后; 产出文档类交付物的步骤放在收尾;\n" +
		"3. 只输出 JSON 数组, 格式: [{\"title\":\"步骤标题\"}, ...], 不要输出任何其他文字、解释或代码块围栏。"
	prompt := "任务目标: " + t.Goal

	ch, _, err := s.Engine.ChatStream(ctx, t.AgentID, prompt, []llm.Message{{Role: "system", Content: sys}},
		false, t.ConversationID, false, "")
	if err != nil {
		s.emitTask(t.ID, "plan_failed", map[string]string{"error": chatFriendlyError(err.Error())})
		return
	}
	var out strings.Builder
	for c := range ch {
		switch c.Type {
		case "token":
			out.WriteString(c.Content)
		case "error":
			s.emitTask(t.ID, "plan_failed", map[string]string{"error": chatFriendlyError(c.Content)})
			return
		}
	}
	steps := parsePlanSteps(out.String())
	if len(steps) == 0 {
		s.emitTask(t.ID, "plan_failed", map[string]string{"error": "规划输出无法解析为步骤清单, 请重试或手动编辑步骤"})
		return
	}
	t.Steps = steps
	s.saveTask(persistCtx(), t)
	s.emitTask(t.ID, "plan", map[string]any{"task_id": t.ID, "steps": steps})
	slog.Info("ai task plan generated", "task_id", t.ID, "steps", len(steps))
}

// planAndStart 创建时勾选「立即启动」: 先规划, 成功产出步骤后自动启动。
func (s *Service) planAndStart(t *AITask) {
	s.planTask(t)
	fresh := s.getTask(persistCtx(), t.ID, t.TenantID)
	if fresh == nil || len(fresh.Steps) == 0 {
		return // 规划失败: 留在 planning 态等用户手动处理
	}
	// T6: 走调度器提交(并发已满时排队, 而不是无视上限直接起 goroutine)
	s.submitTask(persistCtx(), fresh)
}

// parsePlanSteps 从模型输出中容错提取步骤数组: 兼容纯数组 / {"steps":[...]} / markdown 围栏。
func parsePlanSteps(raw string) []TaskStep {
	raw = strings.TrimSpace(raw)
	// 剥 ```json ... ``` 围栏
	if i := strings.Index(raw, "["); i >= 0 {
		if j := strings.LastIndex(raw, "]"); j > i {
			raw = raw[i : j+1]
		}
	}
	type stepItem struct {
		Title string `json:"title"`
	}
	var items []stepItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		var wrapped struct {
			Steps []stepItem `json:"steps"`
		}
		if err2 := json.Unmarshal([]byte(raw), &wrapped); err2 != nil || len(wrapped.Steps) == 0 {
			return nil
		}
		items = wrapped.Steps
	}
	steps := make([]TaskStep, 0, len(items))
	for _, it := range items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		steps = append(steps, TaskStep{ID: uuid.New().String()[:8], Title: title, Status: "pending"})
	}
	if len(steps) > 12 {
		steps = steps[:12]
	}
	return steps
}

// handlePlanTask POST /ai/tasks/{id}/plan — AI 生成计划(异步, 结果经 SSE plan/plan_failed 事件推送)。
func (s *Service) handlePlanTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	if t.Status != "planning" {
		httputil.WriteError(w, http.StatusConflict, "仅待启动(planning)状态可重新规划")
		return
	}
	s.taskMu.Lock()
	if s.taskPlanning[t.ID] {
		s.taskMu.Unlock()
		httputil.WriteError(w, http.StatusConflict, "规划进行中, 请稍候")
		return
	}
	s.taskPlanning[t.ID] = true
	s.taskMu.Unlock()
	go func() {
		defer func() {
			s.taskMu.Lock()
			delete(s.taskPlanning, t.ID)
			s.taskMu.Unlock()
			if rec := recover(); rec != nil {
				slog.Error("ai task plan panic", "task_id", t.ID, "panic", rec)
				s.emitTask(t.ID, "plan_failed", map[string]string{"error": fmt.Sprintf("规划器内部错误: %v", rec)})
			}
		}()
		s.planTask(t)
	}()
	httputil.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "planning"})
}

// handleUpdatePlan PUT /ai/tasks/{id}/plan — 用户手动编辑步骤清单(仅 planning 态)。
func (s *Service) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	if t.Status != "planning" {
		httputil.WriteError(w, http.StatusConflict, "仅待启动(planning)状态可编辑步骤")
		return
	}
	var req struct {
		Steps []struct {
			Title string `json:"title"`
		} `json:"steps"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	steps := []TaskStep{}
	for _, rs := range req.Steps {
		if title := strings.TrimSpace(rs.Title); title != "" {
			steps = append(steps, TaskStep{ID: uuid.New().String()[:8], Title: title, Status: "pending"})
		}
	}
	if len(steps) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "steps 不能为空")
		return
	}
	t.Steps = steps
	s.saveTask(r.Context(), t)
	s.emitTask(t.ID, "plan", map[string]any{"task_id": t.ID, "steps": steps})
	httputil.WriteJSON(w, http.StatusOK, t)
}
