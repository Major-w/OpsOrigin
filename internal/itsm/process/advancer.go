package process

// Advancer — 根据流程定义和当前步骤, 返回下一步应该推进到的步骤
// 规则:
//   1. 查当前步骤在流程里的所有下游节点 (其他步骤 depends_on 包含 currentStepID)
//   2. 根据 condition (success/failure) 筛选
//   3. 如果多个下游条件匹配, 返回第一个
//   4. 如果没有下游, 返回 nil (流程结束)

func (p *Process) Advancer(currentStepID, condition string) *Step {
	// 收集所有 depends_on 包含 currentStepID 的步骤
	var candidates []*Step
	for i := range p.Steps {
		step := &p.Steps[i]
		for _, dep := range step.DependsOn {
			if dep.NodeID == currentStepID && dep.Condition == condition {
				candidates = append(candidates, step)
				break
			}
		}
	}

	// 如果没找到匹配的, 尝试 always 条件
	if len(candidates) == 0 {
		for i := range p.Steps {
			step := &p.Steps[i]
			for _, dep := range step.DependsOn {
				if dep.NodeID == currentStepID && dep.Condition == "always" {
					candidates = append(candidates, step)
					break
				}
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

// isLiteralCondition 判断依赖条件是否为固定字面量(success/failure/always)。
// 非字面量的条件视为表达式, 由 AdvancerExpr 基于表单字段上下文求值。
func isLiteralCondition(c string) bool {
	return c == "success" || c == "failure" || c == "always"
}

// AdvancerLiteral 只按指定字面量条件匹配下游步骤, 不含 always 兜底
// (Advancer 自带 always 兜底, AdvancerExpr 需要可控的分支顺序)。
func (p *Process) AdvancerLiteral(currentStepID, condition string) *Step {
	for i := range p.Steps {
		step := &p.Steps[i]
		for _, dep := range step.DependsOn {
			if dep.NodeID == currentStepID && dep.Condition == condition {
				return step
			}
		}
	}
	return nil
}

// AdvancerExpr 在字面量匹配的基础上增加表达式条件分支:
// 1. 按字面量 condition(success/failure) 匹配
// 2. 无匹配时, 对下游依赖中非字面量的条件用 EvaluateCondition 求值(ctx 提供表单字段/系统字段)
// 3. 仍无匹配时回退 always 兜底
// 使流程网关能根据工单表单字段内容决定走向。
func (p *Process) AdvancerExpr(currentStepID, condition string, ctx map[string]any) *Step {
	if s := p.AdvancerLiteral(currentStepID, condition); s != nil {
		return s
	}
	for i := range p.Steps {
		step := &p.Steps[i]
		for _, dep := range step.DependsOn {
			if dep.NodeID != currentStepID || isLiteralCondition(dep.Condition) {
				continue
			}
			if ok, err := EvaluateCondition(dep.Condition, ctx); err == nil && ok {
				return step
			}
		}
	}
	if s := p.AdvancerLiteral(currentStepID, "always"); s != nil {
		return s
	}
	return nil
}

// StartStep 返回流程的起始步骤
func (p *Process) StartStep() *Step {
	for i := range p.Steps {
		if p.Steps[i].Type == StepStart {
			return &p.Steps[i]
		}
	}
	return nil
}

// StepByID 按 ID 查找步骤
func (p *Process) StepByID(id string) *Step {
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return &p.Steps[i]
		}
	}
	return nil
}

// StepTypeToTicketStatus 将 BPMN 步骤类型映射到工单状态
func StepTypeToTicketStatus(st *Step) string {
	switch st.Type {
	case StepUser:
		return "pending_review"
	case StepService:
		return "executing"
	case StepEnd:
		return "succeeded"
	case StepTimer:
		return "pending_review" // 定时器超时前也是等审批
	case StepNotify:
		return "" // 不改变状态,只是触发通知
	default:
		return ""
	}
}
