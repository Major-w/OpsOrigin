package process

import (
	"context"
	"fmt"
	"strings"
)

// TicketVarProvider 提供工单级变量上下文（工单字段 + 自定义表单字段）。
// 由 internal/itsm 实现并经 cmd/core 装配注入（SetTicketVarProvider），
// process 包不反向依赖 itsm 实体（分层单向）。
//
// 返回结构约定:
//
//	{
//	  "ticket":        {id,title,description,risk_level,status,created_by,assignee,priority},
//	  "custom_fields": {...工单自定义表单字段...},
//	}
//
// 实现方在任何失败场景下应返回非 nil 的空 map（变量链路缺失不阻断流程推进）。
type TicketVarProvider interface {
	TicketVars(ctx context.Context, tenantID, ticketID string) map[string]any
}

// SetTicketVarProvider 注入工单变量提供者。nil = 变量上下文仅含步骤输出与当次 output。
func (s *Service) SetTicketVarProvider(p TicketVarProvider) {
	s.ticketVars = p
}

// buildVarCtx 构建节点变量上下文（自动节点变量链路的核心）。
//
// 组成（后写入者覆盖先写入者）:
//  1. 工单上下文: ticket.* / custom_fields.*（TicketVarProvider 注入）;
//  2. 累积步骤输出: steps.<step_id>.{status,output,...} —— 上游节点产出可被全部下游节点引用,
//     形成「脚本输出 → 下游动作参数」的自动变量链路;
//  3. 当次 output 平铺到顶层 —— 保持既有网关条件(status=='pending_review' 等直接写字段名)的求值语义不变;
//  4. output 键显式保存当次 output（map 时），供 ${output.xxx} 引用。
//
// 顶层平铺仅来自工单侧传来的 output（itsm advanceProcess 传 status/risk_level/custom_fields 等少量键），
// 与 ticket./steps. 命名空间无碰撞风险。
func (s *Service) buildVarCtx(ctx context.Context, inst *ProcessInstance, output any) map[string]any {
	varCtx := map[string]any{}

	// 1) 工单上下文（provider 未注入或查询失败时静默跳过, 变量渲染为空串）
	if s.ticketVars != nil && inst != nil {
		if tv := s.ticketVars.TicketVars(ctx, inst.TenantID, inst.TicketID); tv != nil {
			if t, ok := tv["ticket"]; ok {
				varCtx["ticket"] = t
			}
			if cf, ok := tv["custom_fields"]; ok {
				varCtx["custom_fields"] = cf
			}
		}
	}

	// 2) 累积步骤输出: steps.<step_id> = 步骤结果记录（含 condition/output/at 等, 原样透传）
	steps := map[string]any{}
	for id, rec := range inst.StepResults {
		if rm, ok := rec.(map[string]any); ok {
			view := make(map[string]any, len(rm)+1)
			for k, v := range rm {
				view[k] = v
			}
			// condition 语义化别名: status=success/failure, 便于 ${steps.s1.status} 判断
			if _, ok := view["status"]; !ok {
				view["status"] = rm["condition"]
			}
			steps[id] = view
		} else {
			steps[id] = rec
		}
	}
	varCtx["steps"] = steps

	// 3) 当次 output 平铺（保持旧网关条件兼容）
	if m, ok := output.(map[string]any); ok {
		for k, v := range m {
			varCtx[k] = v
		}
		varCtx["output"] = m
	}
	// 4) 流程级决策表绑定：按已发布版本求值，输出注入 decision.<alias> 命名空间。
	//    放在最后，使绑定输入能看到本次 output，且 decision 为保留命名空间不被平铺覆盖。
	s.applyDecisionBindings(ctx, inst, varCtx)
	return varCtx
}

// injectStepResult 把刚执行完的自动步骤结果并入变量上下文的 steps 命名空间,
// 使同一轮自动链中的下游节点能引用 ${steps.<id>.output}（上游产出 → 下游参数的链路闭环）。
func injectStepResult(exprCtx map[string]any, stepID string, rec map[string]any, cond string) {
	if exprCtx == nil || rec == nil {
		return
	}
	steps, ok := exprCtx["steps"].(map[string]any)
	if !ok {
		steps = map[string]any{}
		exprCtx["steps"] = steps
	}
	view := make(map[string]any, len(rec)+1)
	for k, v := range rec {
		view[k] = v
	}
	view["status"] = cond
	steps[stepID] = view
}

// subst 将 s 中的 ${path} 占位符按 varCtx 点路径取值替换。
// 无匹配时替换为空串（容错: 变量缺失不阻断动作执行）；不含 ${ 的字符串原样返回。
func subst(s string, varCtx map[string]any) string {
	if !strings.Contains(s, "${") || len(varCtx) == 0 {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:start])
		rest := s[start+2:]
		end := strings.Index(rest, "}")
		if end < 0 {
			b.WriteString(s[start:]) // 未闭合, 原样保留
			break
		}
		b.WriteString(fmt.Sprint(lookupVar(varCtx, rest[:end])))
		s = rest[end+1:]
	}
	return b.String()
}

// lookupVar 按点路径逐层下钻嵌套 map 取值（如 steps.prepare.output / ticket.title / custom_fields.env）。
func lookupVar(varCtx map[string]any, path string) any {
	cur := any(varCtx)
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[seg]
		if !ok {
			return ""
		}
	}
	return cur
}

// substMap 深度替换 map 中所有字符串值（递归处理嵌套 map 与切片），返回新 map 不改原配置。
// 服务节点 Inputs / 通知标题正文等静态配置借此引用工单字段与上游步骤输出。
func substMap(inputs map[string]any, varCtx map[string]any) map[string]any {
	if len(inputs) == 0 || len(varCtx) == 0 {
		return inputs
	}
	out := make(map[string]any, len(inputs))
	for k, v := range inputs {
		out[k] = substValue(v, varCtx)
	}
	return out
}

func substValue(v any, varCtx map[string]any) any {
	switch t := v.(type) {
	case string:
		return subst(t, varCtx)
	case map[string]any:
		return substMap(t, varCtx)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = substValue(item, varCtx)
		}
		return out
	default:
		return v
	}
}
