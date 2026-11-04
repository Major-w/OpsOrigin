package process

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// TicketAssigner 把工单指派给受派人（派单节点 auto 模式的落库通道）。
// 由 internal/itsm 实现并经 cmd/core 装配注入（SetTicketAssigner），
// process 包不反向依赖 itsm 实体（分层单向）。
// 实现方负责：更新工单 assignee/assigned_at + 记录事件 + 通知受派人。
type TicketAssigner interface {
	AssignTicketAssignee(ctx context.Context, tenantID, ticketID, assignee, assignedBy string) error
}

// SetTicketAssigner 注入工单指派器。nil = auto 派单解析出受派人后仅记录不落库（按 failure 处理）。
func (s *Service) SetTicketAssigner(a TicketAssigner) {
	s.ticketAssigner = a
}

// ResolveAssignee 解析派单节点的受派人（纯函数, 供引擎与单测共用）。
// 优先级：AssigneeVar（变量点路径直查）> Assignee（${...} 占位替换后按逗号取首个非空候选）。
// 全部为空返回 ""（调用方按 failure 分支处理）。
func ResolveAssignee(step *Step, varCtx map[string]any) string {
	if step == nil {
		return ""
	}
	if p := strings.TrimSpace(step.AssigneeVar); p != "" {
		if v := lookupVar(varCtx, p); v != nil && v != "" {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	for _, cand := range strings.Split(subst(step.Assignee, varCtx), ",") {
		if s := strings.TrimSpace(cand); s != "" {
			return s
		}
	}
	return ""
}

// CompleteDispatch 手动派单（manual 模式）的续跑入口：工单既有「指派」操作完成后调用。
// 校验当前步骤确为等待派单的 dispatch 节点（存在 awaiting=dispatch 标记），随后按 success
// 推进流程（output 携带 assignee, 下游可经 ${steps.<id>.output.assignee} 引用受派人）。
// 工单未绑定流程 / 不在派单等待点 / 已派过单时返回 (nil,nil,nil) 静默跳过 —— 普通指派零感知。
func (s *Service) CompleteDispatch(ctx context.Context, tenantID, ticketID, assignee, dispatchedBy string) (*ProcessInstance, *Step, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	inst, err := s.store.GetInstanceByTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if inst == nil || inst.Status != "running" {
		return nil, nil, nil
	}
	rec, _ := inst.StepResults[inst.CurrentStep].(map[string]any)
	if rec == nil || rec["awaiting"] != "dispatch" {
		return nil, nil, nil // 当前步骤不是等待派单的 dispatch 节点: 普通指派, 不推进
	}
	return s.Advance(ctx, tenantID, ticketID, "success", map[string]any{
		"assignee":      assignee,
		"dispatched_by": dispatchedBy,
	})
}

// runDispatchStep 执行派单节点（runAutomatedDepth 的 dispatch 分支）。
// auto 模式：解析受派人 → 经 TicketAssigner 落库 → 记录结果并入变量上下文 → 按 success/failure 继续；
// manual 模式：写 awaiting=dispatch 标记后返回该步骤（停等人工指派）。
// 返回落点步骤（manual 停等 / failure 无下游时为 dispatch 自身）。
func (s *Service) runDispatchStep(ctx context.Context, proc *Process, inst *ProcessInstance, cur *Step, exprCtx map[string]any) (*Step, error) {
	mode := cur.DispatchMode
	if mode == "" {
		mode = "auto"
	}
	if mode == "manual" {
		// 停等人工派单: 标记 awaiting, 保留 started_at 供观察; 工单状态由 StepTypeToTicketStatus 映射 executing。
		recordStepEntered(inst, cur, time.Now())
		rec, _ := inst.StepResults[cur.ID].(map[string]any)
		rec["awaiting"] = "dispatch"
		rec["mode"] = "manual"
		inst.StepResults[cur.ID] = rec
		slog.Info("itsm process dispatch waiting for manual assign", "ticket_id", inst.TicketID, "step", cur.ID)
		return cur, nil
	}

	rec := map[string]any{"mode": "auto", "at": time.Now()}
	assignee := ResolveAssignee(cur, exprCtx)
	cond := "success"
	switch {
	case assignee == "":
		rec["error"] = "assignee resolved empty (assignee_var/assignee 均未解析到非空值)"
		cond = "failure"
	case s.ticketAssigner == nil:
		rec["error"] = "ticket assigner not configured"
		rec["assignee"] = assignee
		cond = "failure"
	default:
		assignedBy := "dispatch:" + cur.ID
		if err := s.ticketAssigner.AssignTicketAssignee(ctx, inst.TenantID, inst.TicketID, assignee, assignedBy); err != nil {
			rec["error"] = err.Error()
			rec["assignee"] = assignee
			cond = "failure"
			slog.Warn("itsm process dispatch assign failed", "ticket_id", inst.TicketID, "step", cur.ID, "assignee", assignee, "error", err)
		} else {
			rec["assignee"] = assignee
			// output 形态与脚本/动作节点对齐: 下游可经 ${steps.<id>.output.assignee} 引用受派人。
			rec["output"] = map[string]any{"assignee": assignee}
		}
	}
	inst.StepResults[cur.ID] = rec
	injectStepResult(exprCtx, cur.ID, rec, cond)
	nxt := proc.AdvancerExpr(cur.ID, cond, exprCtx)
	if nxt == nil {
		return cur, nil // failure 无下游: 停在派单节点(状态 executing), 错误已记录可排查
	}
	return nxt, nil
}
