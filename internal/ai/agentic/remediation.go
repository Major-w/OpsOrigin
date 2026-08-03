package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RemediationAction 一条由巡检发现生成的自动修复动作(L1 分级处置闭环)。
//
// 分级策略(复用既有审批闸门与危险脚本拦截思路):
//   - risk=blocked: 处置指令命中危险词(rm -rf /, dd, mkfs, shutdown, drop table 等), 直接拦截不执行;
//   - risk=auto: 低危(level=low)且无危险词, 自动执行(经 RemediationRunnerFunc 回调);
//   - risk=review: 中高危(level=medium/high), 触发审批闸门(ApprovalManager.Request),
//     人工批准后执行, 拒绝/超时则标记 rejected。
//
// 执行端默认内置演示执行器(不真实影响系统); cmd/core 可通过 SetRemediationRunner
// 注入真实 Agent/脚本执行回调。
type RemediationAction struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenant_id"`
	RunID         string     `json:"run_id"`
	InspectionID  string     `json:"inspection_id"`
	FindingIdx    int        `json:"finding_idx"`
	Level         string     `json:"level"`    // high | medium | low
	Title         string     `json:"title"`    // 发现标题
	Suggestion    string     `json:"suggestion"` // 处置指令(执行内容)
	Risk          string     `json:"risk"`     // blocked | auto | review
	Status        string     `json:"status"`   // pending | awaiting_approval | approved | rejected | executing | succeeded | failed
	ApprovalID    string     `json:"approval_id,omitempty"` // 审批单 ID(走审批时)
	Result        string     `json:"result,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// Remediation 状态常量。
const (
	RemediationPending          = "pending"
	RemediationAwaitingApproval = "awaiting_approval"
	RemediationApproved         = "approved"
	RemediationRejected         = "rejected"
	RemediationExecuting        = "executing"
	RemediationSucceeded        = "succeeded"
	RemediationFailed           = "failed"
)

// RemediationRunnerFunc 修复执行回调(由 cmd/core 注入真实 Agent/脚本执行; nil=内置演示执行器)。
type RemediationRunnerFunc func(ctx context.Context, action *RemediationAction) (result string, err error)

// remediationBlockedPatterns 危险处置指令黑名单(命中即拦截, 与 script_generate 同思路)。
var remediationBlockedPatterns = []string{
	"rm -rf /", "rm -fr /", "rm -rf /*",
	"dd if=", "mkfs", "format ", "fdisk",
	"shutdown", "reboot", "poweroff", "halt",
	"drop table", "truncate table", "delete from",
	"chmod 777 /", "chown -r 0:0 /",
}

// GenerateRemediations 从巡检运行记录生成修复动作(幂等: 同一 run+finding 已生成则跳过)。
// 返回该 run 的修复动作列表。生成时即完成风险分级:
// 命中危险词 -> blocked; level=low -> auto; medium/high -> review。
func (e *Engine) GenerateRemediations(ctx context.Context, tenantID, runID string) ([]*RemediationAction, error) {
	e.mu.RLock()
	run := e.inspectionRunsByID(runID, tenantID)
	e.mu.RUnlock()
	if run == nil {
		return nil, fmt.Errorf("inspection run not found: %s", runID)
	}

	var findings []InspectionFinding
	_ = json.Unmarshal([]byte(run.Findings), &findings)

	e.mu.Lock()
	defer e.mu.Unlock()
	// 幂等: 已生成该 run 动作的直接返回
	var existing []*RemediationAction
	for _, a := range e.remediations {
		if a.TenantID == tenantID && a.RunID == runID {
			cp := *a
			existing = append(existing, &cp)
		}
	}
	if len(existing) > 0 {
		sort.Slice(existing, func(i, j int) bool { return existing[i].FindingIdx < existing[j].FindingIdx })
		return existing, nil
	}

	now := time.Now()
	for idx, f := range findings {
		a := &RemediationAction{
			ID:           uuid.New().String(),
			TenantID:     tenantID,
			RunID:        runID,
			InspectionID: run.InspectionID,
			FindingIdx:   idx,
			Level:        f.Level,
			Title:        f.Title,
			Suggestion:   f.Suggestion,
			Status:       RemediationPending,
			CreatedAt:    now,
		}
		a.Risk = classifyRemediationRisk(f.Level, f.Suggestion)
		if a.Risk == "blocked" {
			a.Status = RemediationFailed
			a.Error = "危险处置指令已拦截(命中黑名单)"
		}
		e.remediations[a.ID] = a
		existing = append(existing, a)
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].FindingIdx < existing[j].FindingIdx })
	slog.Info("remediations generated", "run", runID, "count", len(existing))
	return existing, nil
}

// classifyRemediationRisk 分级: 危险词 -> blocked; low -> auto; 其余 -> review。
func classifyRemediationRisk(level, suggestion string) string {
	low := strings.ToLower(suggestion)
	for _, p := range remediationBlockedPatterns {
		if strings.Contains(low, p) {
			return "blocked"
		}
	}
	if level == "low" {
		return "auto"
	}
	return "review"
}

// ExecuteRemediation 执行一条修复动作(分级处置):
//   - blocked: 返回错误(已拦截);
//   - auto: 立即执行(经 runner; nil 用演示执行器);
//   - review: 触发审批闸门(goroutine 等待), 批准后执行, 拒绝/超时置 rejected。
func (e *Engine) ExecuteRemediation(ctx context.Context, tenantID, actionID string) (*RemediationAction, error) {
	e.mu.Lock()
	a, ok := e.remediations[actionID]
	if !ok || a.TenantID != tenantID {
		e.mu.Unlock()
		return nil, fmt.Errorf("remediation action not found: %s", actionID)
	}
	if a.Status == RemediationSucceeded || a.Status == RemediationExecuting {
		e.mu.Unlock()
		return a, nil
	}
	if a.Status == RemediationFailed && a.Risk == "blocked" {
		e.mu.Unlock()
		return a, fmt.Errorf("remediation blocked: %s", a.Error)
	}
	runner := e.RemediationRunnerFunc
	e.mu.Unlock()

	switch a.Risk {
	case "blocked":
		return a, fmt.Errorf("remediation blocked: %s", a.Error)
	case "auto":
		return e.runRemediationAction(ctx, a, runner)
	default: // review
		return e.runRemediationWithApproval(ctx, a, runner)
	}
}

// runRemediationWithApproval 审批驱动执行: Request 阻塞, 放 goroutine 等待结果。
func (e *Engine) runRemediationWithApproval(ctx context.Context, a *RemediationAction, runner RemediationRunnerFunc) (*RemediationAction, error) {
	e.setRemediationStatus(a.ID, RemediationAwaitingApproval, "", "")
	go func() {
		resp := e.approvals.Request(ctx, a.TenantID, "", "remediation_exec",
			fmt.Sprintf("finding#%d %s", a.FindingIdx, a.Title),
			"执行自动修复: "+truncateStr(a.Suggestion, 200), 5*time.Minute)
		if resp == nil || resp.Action != "approve" {
			e.setRemediationStatus(a.ID, RemediationRejected, "", "审批未通过或超时")
			return
		}
		e.mu.Lock()
		cur := e.remediations[a.ID]
		runnerNow := runner
		e.mu.Unlock()
		if cur == nil {
			return
		}
		e.setRemediationStatus(a.ID, RemediationApproved, "", "")
		_, _ = e.runRemediationAction(context.Background(), cur, runnerNow)
	}()
	return a, nil
}

// runRemediationAction 实际执行修复动作(pending/approved -> executing -> succeeded/failed)。
// runner 为 nil 时用内置演示执行器(返回"演示执行: 建议截断", 不真实影响系统)。
func (e *Engine) runRemediationAction(ctx context.Context, a *RemediationAction, runner RemediationRunnerFunc) (*RemediationAction, error) {
	e.setRemediationStatus(a.ID, RemediationExecuting, "", "")
	exec := runner
	if exec == nil {
		exec = func(ctx context.Context, action *RemediationAction) (string, error) {
			// 演示执行器: 不真实影响系统, 明确标注 demo
			return "演示执行(未注入执行器): " + truncateStr(action.Suggestion, 120), nil
		}
	}
	result, err := exec(ctx, a)
	if err != nil {
		e.setRemediationStatus(a.ID, RemediationFailed, "", err.Error())
		return e.remediations[a.ID], nil
	}
	e.setRemediationStatus(a.ID, RemediationSucceeded, result, "")
	return e.remediations[a.ID], nil
}

// setRemediationStatus 更新动作状态。
func (e *Engine) setRemediationStatus(id, status, result, errMsg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.remediations[id]
	if !ok {
		return
	}
	a.Status = status
	if result != "" {
		a.Result = result
	}
	if errMsg != "" {
		a.Error = errMsg
	}
	if status == RemediationSucceeded || status == RemediationFailed || status == RemediationRejected {
		now := time.Now()
		a.FinishedAt = &now
	}
}

// ListRemediations 返回该 run 的修复动作列表(按 finding 索引排序)。
func (e *Engine) ListRemediations(tenantID, runID string) []*RemediationAction {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*RemediationAction
	for _, a := range e.remediations {
		if a.TenantID == tenantID && (runID == "" || a.RunID == runID) {
			cp := *a
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FindingIdx < out[j].FindingIdx })
	return out
}

// GetRemediation 按 ID 查询。
func (e *Engine) GetRemediation(id string) (*RemediationAction, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	a, ok := e.remediations[id]
	if !ok {
		return nil, false
	}
	cp := *a
	return &cp, true
}

// SetRemediationRunner 注入真实修复执行回调(cmd/core 装配; nil=内置演示执行器)。
func (e *Engine) SetRemediationRunner(fn RemediationRunnerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.RemediationRunnerFunc = fn
}

// inspectionRunsByID 从 run 索引查找指定 run(按 run 主键 ID 匹配)。
func (e *Engine) inspectionRunsByID(runID, tenantID string) *InspectionRun {
	for _, runs := range e.inspectionRuns {
		for _, r := range runs {
			if r.ID == runID && r.TenantID == tenantID {
				return r
			}
		}
	}
	return nil
}
