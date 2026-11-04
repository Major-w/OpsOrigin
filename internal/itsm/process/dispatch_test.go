package process

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakeAssigner 捕获派单落库调用。
type fakeAssigner struct {
	calls []string // "tenant/ticket/assignee"
	err   error
}

func (f *fakeAssigner) AssignTicketAssignee(ctx context.Context, tenantID, ticketID, assignee, assignedBy string) error {
	f.calls = append(f.calls, tenantID+"/"+ticketID+"/"+assignee)
	return f.err
}

// dispatchFlow 构造 start → dispatch → user → end 的画布, dispatch 可指定模式/受派人配置。
func dispatchFlow(mode, assigneeVar, assignee string) *Process {
	return &Process{ID: "p1", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "disp", Type: StepDispatch, DispatchMode: mode, AssigneeVar: assigneeVar, Assignee: assignee,
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "u1", Type: StepUser, DependsOn: []Dependency{{NodeID: "disp", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "u1", Condition: "success"}}},
	}}
}

// TestResolveAssignee 受派人解析优先级与形态兼容。
func TestResolveAssignee(t *testing.T) {
	varCtx := map[string]any{
		"custom_fields": map[string]any{"owner": "alice", "team": "netops"},
		"ticket":        map[string]any{"created_by": "bob"},
	}
	cases := []struct {
		name string
		step Step
		want string
	}{
		{"assignee_var 优先", Step{AssigneeVar: "custom_fields.owner", Assignee: "static"}, "alice"},
		{"静态 ${} 占位替换", Step{Assignee: "${custom_fields.team}"}, "netops"},
		{"逗号多候选取首个非空", Step{Assignee: " , ${custom_fields.owner}, carol"}, "alice"},
		{"变量缺失回落静态", Step{AssigneeVar: "custom_fields.nobody", Assignee: "dave"}, "dave"},
		{"全空返回空串", Step{Assignee: " ${custom_fields.nobody} "}, ""},
		{"ticket 上下文可用", Step{AssigneeVar: "ticket.created_by"}, "bob"},
	}
	for _, c := range cases {
		if got := ResolveAssignee(&c.step, varCtx); got != c.want {
			t.Errorf("%s: ResolveAssignee = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestDispatchAutoAssignsAndContinues auto 模式: 解析受派人 → 落库 → 停到下游人工节点;
// 结果含 assignee 且下游可经 steps.<id>.output.assignee 引用。
func TestDispatchAutoAssignsAndContinues(t *testing.T) {
	asg := &fakeAssigner{}
	s := &Service{ticketAssigner: asg}
	proc := dispatchFlow("auto", "custom_fields.owner", "")
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}

	// 变量上下文含 custom_fields(模拟 TicketVarProvider 注入后的形态)
	exprCtx := map[string]any{
		"steps":         map[string]any{},
		"custom_fields": map[string]any{"owner": "alice"},
	}
	landing, err := s.runAutomatedDepth(context.Background(), proc, inst, proc.StepByID("disp"), exprCtx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.Type != StepUser {
		t.Fatalf("auto dispatch should continue to user step, landed on %+v", landing)
	}
	if len(asg.calls) != 1 || asg.calls[0] != "default/t1/alice" {
		t.Fatalf("assigner calls = %v, want [default/t1/alice]", asg.calls)
	}
	rec, _ := inst.StepResults["disp"].(map[string]any)
	if rec == nil || rec["assignee"] != "alice" || rec["error"] != nil {
		t.Fatalf("dispatch result = %+v", rec)
	}
	// 变量链路: 同轮下游可引用受派人
	if got := ResolveAssignee(&Step{Assignee: "${steps.disp.output.assignee}"}, exprCtx); got != "alice" {
		t.Fatalf("downstream var chain broken, got %q", got)
	}
}

// TestDispatchAutoEmptyStops auto 模式受派人解析为空: 按 failure 停在派单节点并记录错误。
func TestDispatchAutoEmptyStops(t *testing.T) {
	s := &Service{ticketAssigner: &fakeAssigner{}}
	proc := dispatchFlow("auto", "custom_fields.nobody", "")
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}

	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("disp"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "disp" {
		t.Fatalf("empty assignee without failure branch should stop on dispatch, landed on %+v", landing)
	}
	rec, _ := inst.StepResults["disp"].(map[string]any)
	if rec == nil || !strings.Contains(fmt.Sprint(rec["error"]), "assignee") {
		t.Fatalf("dispatch result should record error, got %+v", rec)
	}
}

// TestDispatchManualWaitsAndResumes manual 模式: 引擎停等(awaiting=dispatch), 人工指派后
// CompleteDispatch 续跑到下游人工节点; 非派单等待点的普通指派不触发推进。
func TestDispatchManualWaitsAndResumes(t *testing.T) {
	asg := &fakeAssigner{}
	proc := dispatchFlow("manual", "", "")
	m := &memStore{
		procs: map[string]*Process{"p1": proc},
		insts: map[string]*ProcessInstance{},
	}
	s := &Service{store: m, ticketAssigner: asg}
	inst := &ProcessInstance{ID: "i1", TicketID: "t1", TenantID: "default", ProcessID: "p1",
		Status: "running", StepResults: map[string]any{}}

	// 引擎到达 manual dispatch: 停等, 不调 assigner
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("disp"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "disp" {
		t.Fatalf("manual dispatch should stop on itself, landed on %+v", landing)
	}
	inst.CurrentStep = "disp" // 与 StartInstance/Advance 的落点登记一致
	rec, _ := inst.StepResults["disp"].(map[string]any)
	if rec == nil || rec["awaiting"] != "dispatch" {
		t.Fatalf("manual dispatch should mark awaiting, got %+v", rec)
	}
	if len(asg.calls) != 0 {
		t.Fatalf("manual dispatch must not auto-assign, calls = %v", asg.calls)
	}
	m.insts["t1"] = inst

	// 非派单等待点(未标记 awaiting): CompleteDispatch 静默跳过
	inst.StepResults["disp"] = map[string]any{}
	if i2, n2, err := s.CompleteDispatch(context.Background(), "default", "t1", "alice", "boss"); err != nil || i2 != nil || n2 != nil {
		t.Fatalf("CompleteDispatch on non-dispatch step should no-op, got (%v,%v,%v)", i2, n2, err)
	}

	// 恢复 awaiting 标记后人工指派 → 续跑到 user
	inst.StepResults["disp"] = rec
	got, next, err := s.CompleteDispatch(context.Background(), "default", "t1", "alice", "boss")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || next == nil || next.ID != "u1" {
		t.Fatalf("CompleteDispatch should advance to u1, got next=%+v", next)
	}
	if len(asg.calls) != 0 {
		t.Fatalf("manual resume must not auto-assign, calls = %v", asg.calls)
	}
	drec, _ := got.StepResults["disp"].(map[string]any)
	// Advance 会以 {condition,output,at} 重写步骤记录: 受派人在 output 里
	outm, _ := drec["output"].(map[string]any)
	if drec == nil || outm == nil || outm["assignee"] != "alice" {
		t.Fatalf("dispatch result after resume = %+v", drec)
	}
	if got.CurrentStep != "u1" {
		t.Fatalf("instance current step = %q, want u1", got.CurrentStep)
	}
}
