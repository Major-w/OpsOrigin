package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// ApprovalPolicyStore 审批策略的持久化提供者(P0 治理层)。
//
// 背景: 改造前策略只存在 Engine.approvalPolicies 内存 map 里, 重启即丢、多副本不共享,
// 且判定散落在各工具自己的 checker 中, 平台层无法统一审计「谁在什么时候放宽了哪个员工的边界」。
// 抽成接口后, 由 ai 层用 PG 实现并注入, agentic 不反向依赖具体存储。
//
// agentID == "" 表示租户级全局默认策略。
type ApprovalPolicyStore interface {
	GetApprovalPolicy(ctx context.Context, tenantID, agentID string) (*ApprovalPolicy, error)
	SaveApprovalPolicy(ctx context.Context, tenantID, agentID string, p ApprovalPolicy, updatedBy string) error
}

// SetApprovalPolicyStore 注入持久化策略提供者; nil = 退回纯内存策略(本地/测试)。
func (e *Engine) SetApprovalPolicyStore(s ApprovalPolicyStore) { e.policyStore = s }

// GetApprovalPolicyCtx 读取当前请求上下文下生效的审批策略。
// 优先读持久化层(Agent 级覆盖 → 租户级全局), 查询失败或为空时回退内存策略, 保证可用性。
// 注意: 持久化策略一旦存在即为权威来源, 内存 map 仅作为宕机/未装配时的兜底。
func (e *Engine) GetApprovalPolicyCtx(ctx context.Context, agentID string) ApprovalPolicy {
	if e.policyStore != nil {
		tenantID := tenantOf(ctx)
		if agentID != "" {
			if p, err := e.policyStore.GetApprovalPolicy(ctx, tenantID, agentID); err == nil && p != nil {
				return *p
			}
		}
		if p, err := e.policyStore.GetApprovalPolicy(ctx, tenantID, ""); err == nil && p != nil {
			return *p
		}
	}
	return e.GetApprovalPolicy(agentID)
}

// approvalGrants 记录「本次工具调用已由网关层审批通过」的一次性凭证。
//
// 统一审批点前移到 approvalGate 后, script_run / coding_* 等工具内部仍保留历史 checker
// 作为纵深防御。若二者都弹审批窗会造成双弹窗; 网关批复后写入凭证, checker 消费到即直接放行,
// 被网关拒绝时工具根本不会执行, checker 也不会被调用。凭证是瞬态执行态(非策略), TTL 2 分钟,
// 进程重启丢失不影响安全性 —— 最坏情况是多弹一次窗。
type approvalGrantKey struct {
	tenant  string
	agentID string
	tool    string
}

func (e *Engine) grantApproval(ctx context.Context, agentID, tool string) {
	e.approvalGrants.Store(approvalGrantKey{tenantOf(ctx), agentID, tool}, time.Now())
}

func (e *Engine) consumeApprovalGrant(ctx context.Context, agentID, tool string) bool {
	key := approvalGrantKey{tenantOf(ctx), agentID, tool}
	v, ok := e.approvalGrants.LoadAndDelete(key)
	if !ok {
		return false
	}
	ts, ok := v.(time.Time)
	return ok && time.Since(ts) < 2*time.Minute
}

// hasDedicatedChecker 标记仍保留工具内置审批 checker 的敏感工具。
// 这些工具在无交互审批通道时由自身 checker 兜底(与改造前行为一致), 避免网关与 checker 重复挂起。
func hasDedicatedChecker(name string) bool {
	switch name {
	case "script_run", "coding_edit_file", "coding_write_file", "coding_apply_patch", "coding_run_shell":
		return true
	default:
		return false
	}
}

// approvalGate 平台层统一审批闸门(PreToolCall 钩子)。
//
// 与旧的 ScriptApprovalChecker 不同, 这里对**所有**工具调用生效: 只要策略判定 NeedsApproval,
// 无论该工具是否主动接入过 checker, 都先经过人工确认再执行 —— 提示词绕不过、新工具也漏不掉。
// 超时/拒绝一律 fail-closed。钩子注册在 rbacGate 之后, 先鉴权再审批, 避免无权用户触发弹窗。
//
// 无交互审批通道的上下文(后台任务 / 直连工具端点 / 单测)不发「注定超时」的请求:
//   - 有专属 checker 的工具: 放行给 checker 兜底(保持旧行为);
//   - 其余敏感工具: 快速 fail-closed 拒绝, 不静默放行、也不白等 60s。
func (e *Engine) approvalGate(ctx context.Context, agentID, name string, args map[string]any) (bool, map[string]any, string) {
	policy := e.GetApprovalPolicyCtx(ctx, agentID)
	if !policy.NeedsApproval(name) {
		return true, nil, ""
	}
	if approvalNotifyFrom(ctx) == nil && !e.approvals.HasSink() && hasDedicatedChecker(name) {
		return true, nil, ""
	}
	if approvalNotifyFrom(ctx) == nil && !e.approvals.HasSink() {
		AIToolExecDenied.Inc()
		return false, nil, "敏感工具需人工审批, 但当前上下文无审批通道, 已按 fail-closed 拒绝"
	}
	argsJSON, _ := json.Marshal(args)
	summary := fmt.Sprintf("数字员工 %q 请求执行工具 %s", agentID, name)
	timeout := time.Duration(policy.AskTimeoutSec) * time.Second
	resp := e.approvals.Request(ctx, tenantOf(ctx), agentID, name, string(argsJSON), summary, timeout)
	if resp == nil {
		AIToolExecDenied.Inc()
		return false, nil, "操作未获批准(审批超时或已取消), 已停止执行"
	}
	if resp.Action != "approve" {
		AIToolExecDenied.Inc()
		return false, nil, "操作被用户拒绝" + suffixComment(resp.Comment)
	}
	slog.Info("ai tool approved by gateway approval policy", "tool", name, "agent", agentID, "by", resp.By)
	e.grantApproval(ctx, agentID, name)
	return true, nil, ""
}
