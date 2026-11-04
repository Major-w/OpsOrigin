package process

import (
	"time"
)

// 汇聚网关(join)运行时语义 —— 与并行网关(parallel)配对使用:
//
//	parallel ──┬── 分支A ──┐
//	           └── 分支B ──┴── join ── 下游
//
// - 分支自动链终止于 join 或直接终结(到 end / 无下游)时, 均视为该分支「已到汇合点」,
//   以分支端点(endpoint)ID 记入 join 步骤的 StepResults.arrived_by(去重, 跨 Advance 持久化)。
// - 分支停在人工/定时等待点时整体暂停(与并行既有语义一致), join 不判定;
//   该分支后续 Advance 推进到 join 时再登记到达。
// - all(缺省): FanIn 全部上游都出现在 arrived_by 才放行;
//   any:       任一上游到达即放行。
// - 放行时清除 join 的等待记录并沿 success 出边继续; 未放行时工单停在 join
//   (状态映射 executing), 由最后一个到达的分支触发放行。
//   非法画布(某上游永不汇入, 如直接连 end 之外的悬空)会导致 all 模式停在 join ——
//   属画布配置错误, 由流程设计器的连线校验兜底, 引擎不做启发式放行。

// markJoinArrival 登记一次分支到达 join, 返回去重后的到达者列表。
func markJoinArrival(inst *ProcessInstance, joinID, fromID string) []string {
	if fromID == "" {
		fromID = "_branch_" + joinID
	}
	rec, _ := inst.StepResults[joinID].(map[string]any)
	var arrived []string
	if rec != nil {
		if list, ok := toStringSlice(rec["arrived_by"]); ok {
			arrived = list
		}
	}
	for _, a := range arrived {
		if a == fromID {
			return arrived // 幂等: 同一分支重复到达不重复计数
		}
	}
	arrived = append(arrived, fromID)
	inst.StepResults[joinID] = map[string]any{"arrived_by": arrived, "at": time.Now()}
	return arrived
}

// joinPass 判定 join 是否满足放行条件(不修改状态)。
func joinPass(proc *Process, inst *ProcessInstance, j *Step, arrived []string) bool {
	mode := j.JoinMode
	if mode != "any" {
		mode = "all"
	}
	if mode == "any" {
		return len(arrived) > 0
	}
	ups := proc.FanIn(j.ID)
	if len(ups) == 0 {
		return true // 无上游引用的 join 视为直通(防御)
	}
	seen := make(map[string]struct{}, len(arrived))
	for _, a := range arrived {
		seen[a] = struct{}{}
	}
	for _, up := range ups {
		if _, ok := seen[up.ID]; !ok {
			return false
		}
	}
	return true
}

// passJoin 放行 join: 清等待记录并沿 success 出边返回下游(无下游返回 nil=流程结束)。
func passJoin(proc *Process, inst *ProcessInstance, j *Step, exprCtx map[string]any) *Step {
	delete(inst.StepResults, j.ID)
	return proc.AdvancerExpr(j.ID, "success", exprCtx)
}

func toStringSlice(v any) ([]string, bool) {
	switch list := v.(type) {
	case []string:
		return list, true
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}
