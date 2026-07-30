package workflow

// compileDAG 将前端画布的 depends_on(DAG 边) 编译为执行器消费的
// on_success/on_failure(链表式)，并返回起始步骤 ID。
//
// 背景：前端画布以 depends_on([{nodeId, condition}]) 表达节点连线（DAG 模型），
// 而后端执行器 runFromStep 以 on_success/on_failure 单后继链表驱动。二者不对齐时，
// UI 建的工作流保存后 on_success/on_failure 恒空 → 执行器跑完首节点即静默终止。
//
// 本编译器在执行前把 depends_on 反向翻译到各上游节点的 on_success/on_failure，
// 使既有链表执行器可直接跑通线性 / 条件分支 DAG（真并行 join 仍需 parallel 节点）。
//
// 仅当存在 depends_on 边时才编译，且不覆盖已显式设置的 on_success/on_failure，
// 从而与旧链表式工作流完全向后兼容。返回空串表示非 DAG 模型（调用方回退 Steps[0]）。
func compileDAG(wf *Workflow) (startID string) {
	if wf == nil || len(wf.Steps) == 0 {
		return ""
	}
	hasDAG := false
	for i := range wf.Steps {
		if len(wf.Steps[i].DependsOn) > 0 {
			hasDAG = true
			break
		}
	}
	if !hasDAG {
		return "" // 旧链表模型，交给现有逻辑(Steps[0])
	}

	idx := make(map[string]int, len(wf.Steps))
	incoming := make(map[string]int, len(wf.Steps))
	for i := range wf.Steps {
		idx[wf.Steps[i].ID] = i
	}
	// 依据 depends_on 反向填充上游节点的 on_success/on_failure
	for i := range wf.Steps {
		target := &wf.Steps[i]
		for _, dep := range target.DependsOn {
			up, ok := idx[dep.NodeID]
			if !ok {
				continue
			}
			incoming[target.ID]++
			upStep := &wf.Steps[up]
			switch dep.Condition {
			case "failure":
				if upStep.OnFailure == "" {
					upStep.OnFailure = target.ID
				}
			case "always":
				if upStep.OnSuccess == "" {
					upStep.OnSuccess = target.ID
				}
				if upStep.OnFailure == "" {
					upStep.OnFailure = target.ID
				}
			default: // success 或未指定
				if upStep.OnSuccess == "" {
					upStep.OnSuccess = target.ID
				}
			}
		}
	}

	// 起点选择：优先 type==start；否则取无入边(不被任何节点依赖)的首个节点
	for i := range wf.Steps {
		if wf.Steps[i].Type == StepTypeStart {
			return wf.Steps[i].ID
		}
	}
	for i := range wf.Steps {
		if incoming[wf.Steps[i].ID] == 0 {
			return wf.Steps[i].ID
		}
	}
	return wf.Steps[0].ID
}
