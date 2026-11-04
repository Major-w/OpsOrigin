package process

import (
	"context"
	"testing"
)

// joinFlow 构造 start → parallel → {分支A, 分支B} → join → end 的画布。
func joinFlow(joinMode string) *Process {
	return &Process{ID: "p1", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "par", Type: StepParallel, DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "a1", Type: StepService, ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "par", Condition: "success"}}},
		{ID: "b1", Type: StepService, ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "par", Condition: "success"}}},
		{ID: "join", Type: StepJoin, JoinMode: joinMode, DependsOn: []Dependency{
			{NodeID: "a1", Condition: "success"}, {NodeID: "b1", Condition: "success"},
		}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "join", Condition: "success"}}},
	}}
}

// TestJoinAllWaitAndPass all 模式: 两分支均到 join → 收齐放行 → 流程到 end。
func TestJoinAllWaitAndPass(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExec{results: map[string]struct {
		output   string
		exitCode int
		err      error
	}{"sc1": {output: "ok", exitCode: 0}}}}
	proc := joinFlow("all")
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}

	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("par"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing != nil && landing.Type != StepEnd {
		t.Fatalf("all branches auto-complete: flow should finish at end, landed on %+v", landing)
	}
	if _, waiting := inst.StepResults["join"]; waiting {
		t.Fatalf("join should be passed and cleaned, got %+v", inst.StepResults["join"])
	}
}

// TestJoinAnyPasses any 模式: 任一分支到达即放行。
func TestJoinAnyPasses(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExec{results: map[string]struct {
		output   string
		exitCode int
		err      error
	}{"sc1": {output: "ok", exitCode: 0}}}}
	proc := joinFlow("any")
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}

	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("par"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing != nil && landing.Type != StepEnd {
		t.Fatalf("any mode should pass immediately, landed on %+v", landing)
	}
}

// TestJoinAllNotMetWaits all 模式: 分支A到 join、分支B停人工 → 暂停于等待点, join 登记保留;
// 模拟 B 分支推进到 join 后(直接调用与 Advance 相同的判定路径)收齐放行。
func TestJoinAllNotMetWaits(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExec{results: map[string]struct {
		output   string
		exitCode int
		err      error
	}{"sc1": {output: "ok", exitCode: 0}}}}
	proc := &Process{ID: "p2", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "par", Type: StepParallel, DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "a1", Type: StepService, ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "par", Condition: "success"}}},
		{ID: "b1", Type: StepUser, DependsOn: []Dependency{{NodeID: "par", Condition: "success"}}},
		{ID: "join", Type: StepJoin, DependsOn: []Dependency{
			{NodeID: "a1", Condition: "success"}, {NodeID: "b1", Condition: "success"},
		}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "join", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}

	// 分支A自动到 join(登记 a1), 分支B停 user → 整体暂停于 b1。
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("par"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "b1" {
		t.Fatalf("should pause at user step b1, got %+v", landing)
	}

	// 模拟 Advance: b1 审批完成, 推进落点为 join —— 与 Advance 内判定路径一致。
	branch := proc.AdvancerExpr("b1", "success", nil)
	if branch == nil || branch.Type != StepJoin {
		t.Fatalf("b1 success should land on join, got %+v", branch)
	}
	arrived := markJoinArrival(inst, branch.ID, "b1")
	if !joinPass(proc, inst, branch, arrived) {
		t.Fatal("all upstream arrived (a1 via parallel, b1 now) should pass join")
	}
	nxt := passJoin(proc, inst, branch, nil)
	if nxt == nil || nxt.ID != "end" {
		t.Fatalf("after join pass should land on end, got %+v", nxt)
	}
}

// TestJoinArrivalIdempotent 到达登记去重: 同一分支重复到达不重复计数。
func TestJoinArrivalIdempotent(t *testing.T) {
	inst := &ProcessInstance{StepResults: map[string]any{}}
	a1 := markJoinArrival(inst, "j", "a1")
	a2 := markJoinArrival(inst, "j", "a1")
	if len(a1) != 1 || len(a2) != 1 {
		t.Fatalf("duplicate arrival should be idempotent, got %v then %v", a1, a2)
	}
}

// TestJoinPassPure 纯函数判定: all 缺上游不放行; any 任一即放行; 无上游防御直通。
func TestJoinPassPure(t *testing.T) {
	proc := &Process{Steps: []Step{
		{ID: "a"}, {ID: "b"},
		{ID: "j", Type: StepJoin, DependsOn: []Dependency{{NodeID: "a"}, {NodeID: "b"}}},
		{ID: "free", Type: StepJoin},
	}}
	inst := &ProcessInstance{StepResults: map[string]any{}}
	j := proc.StepByID("j")
	if joinPass(proc, inst, j, []string{"a"}) {
		t.Fatal("all mode with only a arrived should not pass")
	}
	if !joinPass(proc, inst, j, []string{"a", "b"}) {
		t.Fatal("all mode with a+b arrived should pass")
	}
	jAny := *j
	jAny.JoinMode = "any"
	if !joinPass(proc, inst, &jAny, []string{"a"}) {
		t.Fatal("any mode with one arrival should pass")
	}
	if !joinPass(proc, inst, proc.StepByID("free"), nil) {
		t.Fatal("join without upstream should defensively pass")
	}
}
