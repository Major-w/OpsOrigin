package process

// 阶段管理（stage）：节点通过 Step.Stage 声明阶段归属，运行期按流程定义中
// 阶段首次出现的顺序聚合推导进度。纯函数、零持久化——阶段是节点的展示属性，
// 无需独立状态列；工单详情按本视图渲染阶段进度条。

// StageView 单个阶段的进度视图。
type StageView struct {
	Stage  string     `json:"stage"`  // 阶段名
	Status string     `json:"status"` // done / current / pending
	Steps  []StepView `json:"steps"`  // 阶段内节点明细（按定义顺序）
}

// StepView 阶段内单节点状态。
type StepView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"` // done / failed / current / pending
}

// nodeStageStatus 推导单节点运行状态：
//   - StepResults 有记录且带终态 status（failed → failed，其余 → done）；
//   - 有记录但无 status（recordStepEntered 只落 started_at）且是当前停等节点 → current；
//   - 有记录但无 status 且非当前节点（历史停留后被跳过等边缘）→ done；
//   - 无记录且是当前停等节点 → current；否则 pending。
func nodeStageStatus(st *Step, inst *ProcessInstance) string {
	if inst != nil {
		if raw, ok := inst.StepResults[st.ID]; ok {
			if rec, ok := raw.(map[string]any); ok {
				if s, _ := rec["status"].(string); s != "" {
					if s == "failed" {
						return "failed"
					}
					return "done"
				}
			}
			// 记录存在但没有终态 status：仅当仍停在本节点时是"进行中"
			if inst.CurrentStep == st.ID {
				return "current"
			}
			return "done"
		}
		if inst.CurrentStep == st.ID {
			return "current"
		}
	}
	return "pending"
}

// StageProgress 由流程定义与实例运行态推导阶段进度视图。
// 只收集定义中带 Stage 名的节点；阶段顺序 = 阶段名在定义中的首次出现顺序。
// 阶段状态：任一节点 current → current；无 pending（全部已处理）→ done；否则 pending。
// 实例为 nil（未启动）时全部 pending。def/inst 为 nil 时返回 nil。
func StageProgress(def *Process, inst *ProcessInstance) []StageView {
	if def == nil {
		return nil
	}
	order := make([]string, 0, 4)          // 阶段首次出现顺序
	members := make(map[string][]*Step, 4) // 阶段名 → 节点（保持定义顺序）
	for i := range def.Steps {
		st := &def.Steps[i]
		if st.Stage == "" {
			continue
		}
		if _, seen := members[st.Stage]; !seen {
			order = append(order, st.Stage)
		}
		members[st.Stage] = append(members[st.Stage], st)
	}
	if len(order) == 0 {
		return nil
	}
	out := make([]StageView, 0, len(order))
	for _, name := range order {
		view := StageView{Stage: name, Status: "pending", Steps: make([]StepView, 0, len(members[name]))}
		hasCurrent, hasPending := false, false
		for _, st := range members[name] {
			status := nodeStageStatus(st, inst)
			view.Steps = append(view.Steps, StepView{ID: st.ID, Name: st.Name, Type: string(st.Type), Status: status})
			switch status {
			case "current":
				hasCurrent = true
			case "pending":
				hasPending = true
			}
		}
		switch {
		case hasCurrent:
			view.Status = "current"
		case !hasPending:
			view.Status = "done"
		}
		out = append(out, view)
	}
	return out
}
