package process

import (
	"testing"
)

func mkStageDef() *Process {
	return &Process{ID: "p1", Steps: []Step{
		{ID: "start", Name: "开始", Type: StepStart},
		{ID: "triage", Name: "受理分派", Type: StepUser, Stage: "受理"},
		{ID: "diag", Name: "诊断", Type: StepUser, Stage: "处理"},
		{ID: "fix", Name: "修复", Type: StepService, Stage: "处理"},
		{ID: "review", Name: "回访确认", Type: StepUser, Stage: "关闭"},
		{ID: "end", Name: "结束", Type: StepEnd},
	}}
}

// 阶段顺序按定义中首次出现顺序；未分阶段节点不进视图。
func TestStageProgressPending(t *testing.T) {
	views := StageProgress(mkStageDef(), nil)
	if len(views) != 3 {
		t.Fatalf("stage count = %d, want 3", len(views))
	}
	wantOrder := []string{"受理", "处理", "关闭"}
	for i, v := range views {
		if v.Stage != wantOrder[i] || v.Status != "pending" {
			t.Fatalf("view[%d] = %s/%s, want %s/pending", i, v.Stage, v.Status, wantOrder[i])
		}
	}
}

// 当前节点停在"处理"阶段 → 该阶段 current，上游 done。
func TestStageProgressCurrent(t *testing.T) {
	inst := &ProcessInstance{
		CurrentStep: "diag",
		StepResults: map[string]any{
			"triage": map[string]any{"status": "success"},
		},
	}
	views := StageProgress(mkStageDef(), inst)
	if views[0].Status != "done" {
		t.Fatalf("受理 stage = %s, want done", views[0].Status)
	}
	if views[1].Status != "current" {
		t.Fatalf("处理 stage = %s, want current", views[1].Status)
	}
	if views[2].Status != "pending" {
		t.Fatalf("关闭 stage = %s, want pending", views[2].Status)
	}
	// 节点级状态：triage done / diag current / fix pending
	got := map[string]string{}
	for _, sv := range views[1].Steps {
		got[sv.ID] = sv.Status
	}
	if got["diag"] != "current" || got["fix"] != "pending" {
		t.Fatalf("处理 stage steps = %v", got)
	}
}

// failed 记录视为已到达（阶段可 done），且节点级状态保留 failed。
func TestStageProgressFailedNode(t *testing.T) {
	inst := &ProcessInstance{
		StepResults: map[string]any{
			"triage": map[string]any{"status": "success"},
			"diag":   map[string]any{"status": "failed"},
			"fix":    map[string]any{"status": "success"},
			"review": map[string]any{"status": "success"},
		},
	}
	views := StageProgress(mkStageDef(), inst)
	if views[1].Status != "done" {
		t.Fatalf("处理 stage = %s, want done (含 failed 但全部已到达)", views[1].Status)
	}
	for _, sv := range views[1].Steps {
		if sv.ID == "diag" && sv.Status != "failed" {
			t.Fatalf("diag status = %s, want failed", sv.Status)
		}
	}
}

// 仅 started_at 记录（recordStepEntered）且停在本节点 → current 而非 done。
func TestStageProgressEnteredWaiting(t *testing.T) {
	inst := &ProcessInstance{
		CurrentStep: "triage",
		StepResults: map[string]any{
			"triage": map[string]any{"started_at": "2026-10-31T00:00:00Z"},
		},
	}
	views := StageProgress(mkStageDef(), inst)
	if views[0].Status != "current" {
		t.Fatalf("受理 stage = %s, want current (仅 started_at 停等中)", views[0].Status)
	}
	if views[0].Steps[0].Status != "current" {
		t.Fatalf("triage node = %s, want current", views[0].Steps[0].Status)
	}
}

// 纯函数：同一输入两次调用结果一致，且不修改 def/inst。
func TestStageProgressPure(t *testing.T) {
	def := mkStageDef()
	inst := &ProcessInstance{CurrentStep: "fix", StepResults: map[string]any{"triage": map[string]any{"status": "success"}}}
	a := StageProgress(def, inst)
	b := StageProgress(def, inst)
	if len(a) != len(b) || a[1].Status != b[1].Status {
		t.Fatalf("not deterministic: %+v vs %+v", a, b)
	}
	if def.Steps[1].Stage != "受理" || inst.CurrentStep != "fix" {
		t.Fatalf("StageProgress mutated inputs")
	}
}

// 无任何节点配置 Stage → 返回 nil（零配置零输出，完全向后兼容）。
func TestStageProgressNoStages(t *testing.T) {
	def := &Process{ID: "p2", Steps: []Step{{ID: "s", Name: "s", Type: StepUser}}}
	if views := StageProgress(def, &ProcessInstance{}); views != nil {
		t.Fatalf("want nil for no-stage def, got %+v", views)
	}
}
