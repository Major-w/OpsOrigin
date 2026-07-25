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
