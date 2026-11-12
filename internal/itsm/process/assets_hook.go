package process

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// 决策表资产 / 动作模板在流程引擎中的运行期入口。
//
// 两个解析器都由 cmd/core 注入 itsm.Service 实现，process 包只认接口、
// 不反向依赖 internal/itsm/assets（与 FormSchemaResolver / TicketVarProvider 同范式）。
// 未注入时相关能力整体关闭，存量流程定义与单测零改动。

// DecisionEvaluator 按决策表资产的已发布版本求值，返回仅含输出列的 map。
// inputs 为条件列取值；fired 为命中行标记（供审计/试算）。
type DecisionEvaluator interface {
	EvaluateDecision(ctx context.Context, tenantID, assetID string, version int, inputs map[string]any) (map[string]any, []string, error)
}

// ActionTemplateResolver 解析动作模板引用，返回模板的动作类型与默认参数。
// 模板不存在 / 未发布 / 已停用 / 版本缺失时必须返回 error，调用方 fail-closed。
type ActionTemplateResolver interface {
	ResolveActionTemplate(ctx context.Context, tenantID, templateID string, version int) (string, map[string]any, error)
}

// SetDecisionEvaluator 注入流程级决策表求值器。
func (s *Service) SetDecisionEvaluator(e DecisionEvaluator) {
	s.decisionEval = e
}

// SetActionTemplateResolver 注入动作模板引用解析器。
func (s *Service) SetActionTemplateResolver(r ActionTemplateResolver) {
	s.templateResolver = r
}

// ValidateDecisionBindings 保存流程定义时拦截会导致运行期静默失效的绑定配置。
// alias 是 decision.<alias> 变量命名空间的唯一键，重复会让后一张表覆盖前一张。
func ValidateDecisionBindings(p *Process) error {
	if p == nil {
		return nil
	}
	seen := map[string]bool{}
	for i, b := range p.DecisionBindings {
		alias := strings.TrimSpace(b.Alias)
		if strings.TrimSpace(b.AssetID) == "" || alias == "" {
			return fmt.Errorf("决策表绑定 #%d 的 asset_id 与 alias 均不能为空", i+1)
		}
		if seen[alias] {
			return fmt.Errorf("决策表绑定 alias %q 重复，求值结果会互相覆盖", alias)
		}
		seen[alias] = true
		if b.Version < 0 {
			return fmt.Errorf("决策表绑定 %q 的版本号不能为负数", alias)
		}
	}
	for _, st := range p.Steps {
		if st.TemplateRef != nil && strings.TrimSpace(st.TemplateRef.TemplateID) == "" {
			return fmt.Errorf("步骤 %q 引用了动作模板但 template_id 为空", st.ID)
		}
		if st.TemplateRef != nil && st.TemplateRef.Version < 0 {
			return fmt.Errorf("步骤 %q 引用的模板版本号不能为负数", st.ID)
		}
	}
	return nil
}

// applyDecisionBindings 在 buildVarCtx 末尾求值流程级决策表绑定，把输出注入
// decision.<alias> 命名空间。之后网关条件 / 派单变量 / 动作参数都能直接引用。
//
// 与 buildVarCtx 同频（每次推进重算），因此 version=0 的绑定会跟随资产最新发布版本；
// 资产缺失 / 解析失败按容错跳过该绑定（不阻断工单流转），但保留 decision 空命名空间，
// 使 `${decision.x.y}` 稳定渲染为空串而非 panic。
func (s *Service) applyDecisionBindings(ctx context.Context, inst *ProcessInstance, varCtx map[string]any) {
	if s.decisionEval == nil || inst == nil || inst.ProcessID == "" || varCtx == nil {
		return
	}
	// 决策绑定是配置资产引用，取当前流程定义（不从 ProcessSnapshot 取：快照只含 Steps，
	// 会丢失绑定；资产版本由 binding.Version 独立锁定，不依赖流程版本隔离）。
	proc, err := s.store.Get(ctx, inst.ProcessID, inst.TenantID)
	if err != nil || proc == nil || len(proc.DecisionBindings) == 0 {
		return
	}
	decision := map[string]any{}
	for _, b := range proc.DecisionBindings {
		alias := strings.TrimSpace(b.Alias)
		if strings.TrimSpace(b.AssetID) == "" || alias == "" {
			continue
		}
		inputs := defaultDecisionInputs(varCtx)
		for k, expr := range b.Inputs {
			inputs[k] = resolveRefValue(expr, varCtx)
		}
		out, fired, err := s.decisionEval.EvaluateDecision(ctx, inst.TenantID, b.AssetID, b.Version, inputs)
		if err != nil {
			slog.Warn("itsm process decision binding failed", "ticket_id", inst.TicketID,
				"asset_id", b.AssetID, "alias", alias, "error", err)
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		if len(fired) > 0 {
			slog.Debug("itsm process decision binding fired", "ticket_id", inst.TicketID,
				"asset_id", b.AssetID, "alias", alias, "fired", fired)
		}
		decision[alias] = out
	}
	varCtx["decision"] = decision
}

// defaultDecisionInputs 给未显式映射的条件列提供默认取值：
// custom_fields.* 优先，其次变量上下文顶层的标量字段（跳过命名空间与复杂结构）。
func defaultDecisionInputs(varCtx map[string]any) map[string]any {
	inputs := map[string]any{}
	if cf, ok := varCtx["custom_fields"].(map[string]any); ok {
		for k, v := range cf {
			inputs[k] = v
		}
	}
	for k, v := range varCtx {
		switch k {
		case "ticket", "custom_fields", "steps", "decision", "output":
			continue
		}
		switch v.(type) {
		case map[string]any, []any:
			continue
		}
		if _, exists := inputs[k]; !exists {
			inputs[k] = v
		}
	}
	return inputs
}

// resolveStepAction 返回服务步骤最终生效的动作类型与参数。
// 无模板引用时原样返回（存量零改动）；有引用时模板为权威来源、步骤 Inputs 同名覆盖。
func (s *Service) resolveStepAction(ctx context.Context, tenantID string, cur *Step) (string, map[string]any, error) {
	if cur.TemplateRef == nil {
		return cur.ActionType, cur.Inputs, nil
	}
	if s.templateResolver == nil {
		return "", nil, fmt.Errorf("动作模板解析器未装配（步骤 %s 引用模板 %s）", cur.ID, cur.TemplateRef.TemplateID)
	}
	actionType, defaults, err := s.templateResolver.ResolveActionTemplate(ctx, tenantID,
		cur.TemplateRef.TemplateID, cur.TemplateRef.Version)
	if err != nil {
		return "", nil, fmt.Errorf("解析动作模板 %s 失败: %w", cur.TemplateRef.TemplateID, err)
	}
	merged := make(map[string]any, len(defaults)+len(cur.Inputs))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range cur.Inputs {
		merged[k] = v
	}
	return actionType, merged, nil
}
