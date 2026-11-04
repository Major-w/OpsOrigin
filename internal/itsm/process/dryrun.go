package process

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/opsorigin/core/pkg/httputil"
)

// 流程测试运行（dry-run，对齐 ServiceNow「Test」按钮的模拟语义）：
// 不建实例、不执行动作、不改工单 —— 纯静态沿 DAG 走一遍，逐节点报告
// 「会做什么 / 停在哪里 / 分支怎么选」，用于上线前验证流程定义。
//
// 与真实引擎(runAutomated)的对应关系：
//   - service/notify   → would_run（附参数模板预览，不真正执行）
//   - user/timer/wait  → would_pause（真实运行会停在这里等人工/定时/条件）
//   - dispatch         → 按模式区分 auto=would_run / manual=would_pause
//   - gateway          → 用示例变量对出口条件求值并选中分支；无变量时按默认分支并告警
//   - parallel         → 顺序枚举各分支（与引擎顺序执行语义一致）

// DryRunStep dry-run 报告中的单节点条目。
type DryRunStep struct {
	StepID   string `json:"step_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Status   string `json:"status"`             // would_run / would_pause / branch_end / skipped
	Detail   string `json:"detail"`             // 人类可读的「该节点会做什么」
	Branches string `json:"branches,omitempty"` // gateway/parallel: 各出口走向说明
}

// DryRunResult 一次模拟运行的完整报告。
type DryRunResult struct {
	ProcessID  string       `json:"process_id"`
	PausedAt   string       `json:"paused_at,omitempty"` // 停住的节点 ID（空=走完到结束）
	PausedName string       `json:"paused_name,omitempty"`
	Summary    string       `json:"summary"` // 一句话结论
	Warnings   []string     `json:"warnings,omitempty"`
	Steps      []DryRunStep `json:"steps"`
}

// dryRunMaxSteps 防御异常定义导致的报告爆炸（正常流程远小于该值）。
const dryRunMaxSteps = 60

// DryRun 对流程定义做一次无副作用模拟推进。sampleVars 是调用方提供的示例变量
// （顶层平铺 + 可选 custom_fields 子 map），用于网关条件与 ${} 占位符的求值演示；
// 为 nil 时网关按默认分支走并产生告警。
func (s *Service) DryRun(_ context.Context, proc *Process, sampleVars map[string]any) *DryRunResult {
	res := &DryRunResult{ProcessID: proc.ID, Steps: []DryRunStep{}}
	if proc.StartStep() == nil {
		res.Summary = "流程缺少「开始」节点，无法模拟"
		res.Warnings = append(res.Warnings, "missing start step")
		return res
	}
	if proc.EndStep() == nil {
		res.Warnings = append(res.Warnings, "流程没有「结束」节点，模拟只能按可达性展开")
	}
	exprCtx := map[string]any{"steps": map[string]any{}}
	for k, v := range sampleVars {
		exprCtx[k] = v
	}
	if sampleVars == nil {
		// 无示例字段时提示：网关表达式条件将全部按默认分支处理
		for i := range proc.Steps {
			if proc.Steps[i].Type != StepGateway {
				continue
			}
			for _, d := range outgoingDeps(proc, proc.Steps[i].ID) {
				if !isLiteralCondition(d.condition) {
					res.Warnings = append(res.Warnings, "未提供示例变量，网关「"+proc.Steps[i].Name+"」的条件分支将按默认分支（always）模拟；可在请求中传 vars 提供示例值")
					break
				}
			}
		}
	}

	visited := map[string]bool{}
	var walk func(cur *Step, depth int)
	var walkErr error
	walk = func(cur *Step, depth int) {
		if walkErr != nil || cur == nil {
			return
		}
		if depth > dryRunMaxSteps || len(res.Steps) >= dryRunMaxSteps {
			walkErr = fmt.Errorf("模拟步数超过上限 %d，流程可能存在环或过长", dryRunMaxSteps)
			return
		}
		if visited[cur.ID] {
			return
		}
		visited[cur.ID] = true

		switch cur.Type {
		case StepStart:
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "branch_end", Detail: "流程起点"})
			walk(nextStep(proc, cur.ID, "success", exprCtx), depth+1)
		case StepEnd:
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "branch_end", Detail: "流程结束"})
		case StepUser:
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_pause",
				Detail: "等待人工处理：" + userPauseDetail(cur)})
			res.PausedAt, res.PausedName = cur.ID, cur.Name
		case StepTimer:
			d, ok := parseStepDuration(cur.Duration)
			detail := "等待定时器到期（时长 " + cur.Duration + "）"
			if !ok {
				detail = "等待定时器到期，但时长配置「" + cur.Duration + "」无法解析"
				res.Warnings = append(res.Warnings, "定时器「"+cur.Name+"」时长无法解析")
			} else if d <= 0 {
				detail = "等待定时器到期，但时长配置为零或负值"
				res.Warnings = append(res.Warnings, "定时器「"+cur.Name+"」时长为零/负值")
			}
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_pause", Detail: detail})
			res.PausedAt, res.PausedName = cur.ID, cur.Name
		case StepWait:
			detail := "挂起等待条件成立（每 30s 重评）"
			if strings.TrimSpace(cur.Condition) == "" {
				detail = "挂起等待条件成立，但条件表达式为空（永不推进）"
				res.Warnings = append(res.Warnings, "等待条件「"+cur.Name+"」未配置条件表达式")
			} else if sampleVars != nil {
				if ok, err := EvaluateCondition(cur.Condition, exprCtx); err == nil {
					if ok {
						detail += "；按示例变量求值：条件当前已成立（真实运行会直接继续）"
					} else {
						detail += "；按示例变量求值：条件当前不成立（真实运行会在此挂起）"
					}
				}
			}
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_pause", Detail: detail})
			res.PausedAt, res.PausedName = cur.ID, cur.Name
		case StepDispatch:
			if cur.DispatchMode == "manual" {
				res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_pause",
					Detail: "手动派单：停在派单点等待人工指派（受派人变量 " + dispatchTarget(cur) + "）"})
				res.PausedAt, res.PausedName = cur.ID, cur.Name
			} else {
				res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_run",
					Detail: "自动派单 → " + dispatchTarget(cur)})
				walk(nextStep(proc, cur.ID, "success", exprCtx), depth+1)
			}
		case StepService:
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_run",
				Detail: "执行" + serviceDetail(cur, exprCtx) + serviceRetryDetail(cur)})
			walk(nextStep(proc, cur.ID, "success", exprCtx), depth+1)
		case StepNotify:
			title := subst(cur.NotifyTitle, exprCtx)
			body := subst(cur.NotifyBody, exprCtx)
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_run",
				Detail: "发送通知（渠道 " + notifyTarget(cur) + "）：" + title + " / " + body})
			walk(nextStep(proc, cur.ID, "success", exprCtx), depth+1)
		case StepGateway:
			var b strings.Builder
			for _, d := range outgoingDeps(proc, cur.ID) {
				mark := "—"
				if !isLiteralCondition(d.condition) {
					if sampleVars == nil {
						mark = "未求值"
					} else if ok, err := EvaluateCondition(d.condition, exprCtx); err == nil && ok {
						mark = "✓ 命中"
					} else if err != nil {
						mark = "表达式错误"
						res.Warnings = append(res.Warnings, "网关「"+cur.Name+"」条件「"+d.condition+"」求值失败: "+err.Error())
					}
				}
				b.WriteString("\n→ " + d.targetName + " [" + condLabel(d.condition) + "] " + mark)
			}
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_run",
				Detail: "排他网关择一分支", Branches: strings.TrimPrefix(b.String(), "\n")})
			walk(nextStep(proc, cur.ID, "success", exprCtx), depth+1)
		case StepParallel:
			branches := proc.FanOut(cur.ID)
			var b strings.Builder
			for _, br := range branches {
				b.WriteString("\n→ 分支 " + br.Name)
			}
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_run",
				Detail:   fmt.Sprintf("并行网关：顺序展开 %d 条分支（引擎按顺序执行，遇等待点整体暂停）", len(branches)),
				Branches: strings.TrimPrefix(b.String(), "\n")})
			for _, br := range branches {
				walk(br, depth+1)
			}
		case StepJoin:
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_run",
				Detail: "汇聚网关（" + joinModeLabel(cur.JoinMode) + "）：等上游分支汇合后继续"})
			walk(nextStep(proc, cur.ID, "success", exprCtx), depth+1)
		case StepSubflow:
			detail := "执行子流程 " + cur.SubflowID + "（本次模拟不展开子流程内部）"
			if cur.ForEach != "" {
				detail += "；For Each 循环：对 " + cur.ForEach + " 求值列表逐项执行（仅支持全自动子流程）"
			}
			if len(cur.SubflowParams) > 0 {
				keys := make([]string, 0, len(cur.SubflowParams))
				for k := range cur.SubflowParams {
					keys = append(keys, "params."+k)
				}
				sort.Strings(keys)
				detail += "；显式传参：" + strings.Join(keys, "、")
			}
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "would_run",
				Detail: detail})
			walk(nextStep(proc, cur.ID, "success", exprCtx), depth+1)
		default:
			res.Steps = append(res.Steps, DryRunStep{StepID: cur.ID, Name: cur.Name, Type: string(cur.Type), Status: "skipped",
				Detail: "未知节点类型，模拟跳过"})
			res.Warnings = append(res.Warnings, "未知节点类型: "+string(cur.Type))
		}
	}

	walk(proc.StartStep(), 0)
	if walkErr != nil {
		res.Warnings = append(res.Warnings, walkErr.Error())
	}
	// 未被访问到的节点标记 skipped（环路/孤立/停在等待点之后的节点）
	for i := range proc.Steps {
		st := &proc.Steps[i]
		if visited[st.ID] || st.Type == StepStart {
			continue
		}
		res.Steps = append(res.Steps, DryRunStep{StepID: st.ID, Name: st.Name, Type: string(st.Type), Status: "skipped",
			Detail: "本次模拟路径未经过（流程停在等待点 / 其他分支 / 定义不连通）"})
	}
	switch {
	case res.PausedAt != "":
		res.Summary = "模拟完成：流程会停在「" + res.PausedName + "」等待人工/定时/条件"
	case walkErr != nil:
		res.Summary = "模拟中止：" + walkErr.Error()
	default:
		res.Summary = "模拟完成：流程可一路自动走到「结束」节点"
	}
	return res
}

// ---- dry-run 辅助 ----

type dryDep struct {
	nodeID     string
	condition  string
	targetName string
}

// outgoingDeps 返回 stepID 的全部出边（目标名已解析）。
func outgoingDeps(proc *Process, stepID string) []dryDep {
	var out []dryDep
	for i := range proc.Steps {
		for _, d := range proc.Steps[i].DependsOn {
			if d.NodeID == stepID {
				out = append(out, dryDep{nodeID: proc.Steps[i].ID, condition: d.Condition, targetName: proc.Steps[i].Name})
			}
		}
	}
	return out
}

// nextStep 复用引擎同款推进逻辑（字面量 → 表达式求值 → always 兜底）。
func nextStep(proc *Process, stepID, condition string, exprCtx map[string]any) *Step {
	return proc.AdvancerExpr(stepID, condition, exprCtx)
}

func userPauseDetail(st *Step) string {
	var parts []string
	if len(st.Approvers) > 0 {
		parts = append(parts, fmt.Sprintf("固定审批人 %d 人", len(st.Approvers)))
	}
	if st.ApproverField != "" {
		parts = append(parts, "动态审批人取自字段 "+st.ApproverField)
	}
	parts = append(parts, voteLabel(st.ApprovalType, st.ApprovalThreshold))
	if st.TimeoutDuration != "" {
		parts = append(parts, "时限 "+st.TimeoutDuration+"（"+timeoutActionLabel(st.TimeoutAction)+"）")
	}
	return strings.Join(parts, " · ")
}

func serviceDetail(st *Step, exprCtx map[string]any) string {
	switch {
	case st.ActionType != "":
		params := substMap(st.Inputs, exprCtx)
		return "预置动作 " + st.ActionType + "，参数 " + fmt.Sprint(params)
	case st.ScriptID != "":
		params := substMap(st.Inputs, exprCtx)
		return "脚本 " + st.ScriptID + "，参数 " + fmt.Sprint(params)
	default:
		return "（未配置动作/脚本，按成功直通）"
	}
}

func serviceRetryDetail(st *Step) string {
	if st.RetryCount <= 0 {
		return ""
	}
	return fmt.Sprintf("；失败重试最多 %d 次", st.RetryCount)
}

func dispatchTarget(st *Step) string {
	if st.AssigneeVar != "" {
		return st.AssigneeVar
	}
	if st.Assignee != "" {
		return st.Assignee
	}
	return "（未配置，将走失败分支）"
}

func notifyTarget(st *Step) string {
	if st.NotifyChannel != "" {
		return st.NotifyChannel
	}
	return "默认 workflow.notify"
}

func condLabel(c string) string {
	switch c {
	case "success":
		return "成功"
	case "failure":
		return "失败"
	case "always":
		return "默认分支"
	default:
		return c
	}
}

func joinModeLabel(m string) string {
	if m == "any" {
		return "任一到达即放行"
	}
	return "全部到达放行"
}

func timeoutActionLabel(a string) string {
	switch a {
	case "auto_approve":
		return "到期自动通过"
	case "auto_reject":
		return "到期自动驳回"
	default:
		return "到期仅催办"
	}
}

// ---- HTTP ----

// handleDryRun POST /api/v1/itsm/processes/{id}/dry-run
// body: {"vars": {...}} 可选示例变量（顶层平铺 + custom_fields 子 map）。
func (s *Service) handleDryRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenant := tenantOf(r)
	var body struct {
		Vars map[string]any `json:"vars"`
	}
	if err := httputil.BindJSON(r, &body); err != nil {
		body.Vars = nil // 空 body 允许（仅结构校验，不强制 vars）
	}
	proc, err := s.store.Get(r.Context(), id, tenant)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	if proc == nil {
		httputil.WriteError(w, 404, "not found")
		return
	}
	res := s.DryRun(r.Context(), proc, body.Vars)
	slog.Info("itsm process dry-run", "process_id", id, "steps", len(res.Steps), "paused_at", res.PausedAt)
	httputil.WriteJSON(w, 200, res)
}
