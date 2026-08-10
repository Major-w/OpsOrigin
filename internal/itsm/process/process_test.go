package process

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// --- EvaluateCondition ---

func TestEvaluateCondition(t *testing.T) {
	ctx := map[string]any{
		"risk_level":      "high",
		"status":          "executing",
		"created_by_type": "user",
		"assignee":        "alice",
		"custom_fields": map[string]any{
			"change_type": "标准变更",
			"count":       float64(3),
			"urgent":      true,
			"env":         "prod",
		},
	}
	cases := []struct {
		expr string
		want bool
	}{
		{`risk_level == "high"`, true},
		{`risk_level == "low"`, false},
		{`risk_level != "low"`, true},
		{`custom_fields.change_type == "标准变更"`, true},
		{`env == "prod"`, true},
		{`env contains "pro"`, true},
		{`env not contains "stg"`, true},
		{`change_type in (标准变更, 紧急变更)`, true},
		{`change_type not in (紧急变更)`, true},
		{`count > 2`, true},
		{`count >= 3`, true},
		{`count < 2`, false},
		{`urgent == true`, true},
		{`missing_field exists`, false},
		{`assignee exists`, true},
		{`risk_level == "high" && count > 2`, true},
		{`risk_level == "low" && count > 2`, false},
		{`risk_level == "high" && count < 2`, false},
	}
	for _, tc := range cases {
		got, err := EvaluateCondition(tc.expr, ctx)
		if err != nil {
			t.Errorf("EvaluateCondition(%q) unexpected error: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("EvaluateCondition(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestEvaluateConditionErrors(t *testing.T) {
	if _, err := EvaluateCondition("", nil); err == nil {
		t.Error("empty condition should error")
	}
	if _, err := EvaluateCondition("risk_level ?? high", nil); err == nil {
		t.Error("unknown operator should error")
	}
}

// --- AdvancerExpr ---

func testProcess() *Process {
	return &Process{
		ID: "p1",
		Steps: []Step{
			{ID: "start", Type: StepStart, Name: "开始"},
			{ID: "gateway", Type: StepGateway, Name: "风险判断", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
			{ID: "approve", Type: StepUser, Name: "审批", DependsOn: []Dependency{{NodeID: "gateway", Condition: `risk_level == "high"`}}},
			{ID: "execute", Type: StepUser, Name: "执行", DependsOn: []Dependency{{NodeID: "gateway", Condition: "always"}}},
			{ID: "end", Type: StepEnd, Name: "结束", DependsOn: []Dependency{{NodeID: "approve", Condition: "success"}}},
		},
	}
}

func TestAdvancerExprLiteralFirst(t *testing.T) {
	p := &Process{Steps: []Step{
		{ID: "a", Type: StepStart},
		{ID: "b", Type: StepUser, DependsOn: []Dependency{{NodeID: "a", Condition: "success"}}},
		{ID: "c", Type: StepUser, DependsOn: []Dependency{{NodeID: "a", Condition: `risk_level == "high"`}}},
	}}
	// 字面量 success 优先命中 b, 即使表达式条件也存在
	got := p.AdvancerExpr("a", "success", map[string]any{"risk_level": "high"})
	if got == nil || got.ID != "b" {
		t.Fatalf("literal success should win, got %+v", got)
	}
}

func TestAdvancerExprExpressionBranch(t *testing.T) {
	p := testProcess()
	// 从 gateway 出发, 无字面量 success/failure 匹配 → 表达式求值: high → approve
	got := p.AdvancerExpr("gateway", "success", map[string]any{"risk_level": "high"})
	if got == nil || got.ID != "approve" {
		t.Fatalf("expression branch should pick approve for high risk, got %+v", got)
	}
	// low → 不命中表达式 → always 兜底 execute
	got = p.AdvancerExpr("gateway", "success", map[string]any{"risk_level": "low"})
	if got == nil || got.ID != "execute" {
		t.Fatalf("always fallback should pick execute for low risk, got %+v", got)
	}
}

func TestAdvancerExprExpressionFalseNoMatch(t *testing.T) {
	p := &Process{Steps: []Step{
		{ID: "a", Type: StepStart},
		{ID: "b", Type: StepUser, DependsOn: []Dependency{{NodeID: "a", Condition: `count > 10`}}},
	}}
	got := p.AdvancerExpr("a", "success", map[string]any{"custom_fields": map[string]any{"count": float64(3)}})
	if got != nil {
		t.Fatalf("expression false and no always should yield nil, got %+v", got)
	}
}

// --- runAutomated ---

type fakeScriptExec struct {
	results map[string]struct {
		output   string
		exitCode int
		err      error
	}
	calls []string
}

func (f *fakeScriptExec) Execute(scriptID string, params map[string]string, timeout int, runAs, agentID, tenantID string) (string, int, error) {
	f.calls = append(f.calls, scriptID)
	r := f.results[scriptID]
	return r.output, r.exitCode, r.err
}

func TestRunAutomatedScriptSuccessToEnd(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExec{results: map[string]struct {
		output   string
		exitCode int
		err      error
	}{
		"sc1": {output: "ok", exitCode: 0},
	}}}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc", Type: StepService, Name: "跑脚本", ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, Name: "结束", DependsOn: []Dependency{{NodeID: "svc", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("svc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("script success should land on end, got %+v", landing)
	}
	if _, ok := inst.StepResults["svc"]; !ok {
		t.Fatal("script step result should be recorded")
	}
}

func TestRunAutomatedScriptFailureBranch(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExec{results: map[string]struct {
		output   string
		exitCode int
		err      error
	}{
		"sc1": {output: "boom", exitCode: 1},
	}}}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc", Type: StepService, Name: "跑脚本", ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "rollback", Type: StepUser, Name: "人工回滚", DependsOn: []Dependency{{NodeID: "svc", Condition: "failure"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("svc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "rollback" {
		t.Fatalf("script failure should route to failure branch, got %+v", landing)
	}
	if r, _ := inst.StepResults["svc"].(map[string]any); r == nil || r["exit_code"] != 1 {
		t.Fatalf("script failure exit_code should be recorded, got %+v", inst.StepResults["svc"])
	}
}

func TestRunAutomatedStopsAtUser(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExec{results: map[string]struct {
		output   string
		exitCode int
		err      error
	}{
		"sc1": {output: "ok", exitCode: 0},
	}}}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc", Type: StepService, Name: "跑脚本", ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "approve", Type: StepUser, Name: "审批", DependsOn: []Dependency{{NodeID: "svc", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("svc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "approve" {
		t.Fatalf("should stop at user step, got %+v", landing)
	}
}

func TestRunAutomatedNoScriptPassthrough(t *testing.T) {
	s := &Service{} // 无 scriptExec: 无脚本 service 按 success 直通
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc", Type: StepService, Name: "空服务", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, Name: "结束", DependsOn: []Dependency{{NodeID: "svc", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("svc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("scriptless service should passthrough to end, got %+v", landing)
	}
}

// --- duration 解析 ---

func TestParseStepDuration(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"1h", true}, {"30m", true}, {"90s", true}, {"3d", true}, {"1h30m", true}, {"1.5h", true},
		{"", false}, {"abc", false}, {"0h", false}, {"h", false}, {"-5m", false},
	}
	for _, tc := range cases {
		d, ok := parseStepDuration(tc.in)
		if ok != tc.want {
			t.Errorf("parseStepDuration(%q) ok=%v want %v", tc.in, ok, tc.want)
		}
		if ok && d <= 0 {
			t.Errorf("parseStepDuration(%q) should be > 0, got %v", tc.in, d)
		}
	}
}

// --- 定时器到期判定 ---

func TestTimerDueAt(t *testing.T) {
	now := time.Now()
	inst := &ProcessInstance{StepResults: map[string]any{
		"timer1": map[string]any{"started_at": now.Add(-2 * time.Hour).Format(time.RFC3339)},
	}}
	step := &Step{ID: "timer1", Type: StepTimer, Duration: "1h"}
	due := timerDueAt(inst, step, now)
	if due == nil {
		t.Fatal("due should be computed")
	}
	if now.Before(*due) {
		t.Fatalf("timer should be due, now=%v due=%v", now, *due)
	}
}

func TestTimerDueAtNotReady(t *testing.T) {
	now := time.Now()
	inst := &ProcessInstance{StepResults: map[string]any{
		"timer1": map[string]any{"started_at": now.Format(time.RFC3339)},
	}}
	step := &Step{ID: "timer1", Type: StepTimer, Duration: "1h"}
	due := timerDueAt(inst, step, now)
	if due == nil {
		t.Fatal("due should be computed")
	}
	if !now.Before(*due) {
		t.Fatalf("timer should not be due yet, now=%v due=%v", now, *due)
	}
}

// --- 并行网关: 自动分支顺序执行 ---

func TestRunAutomatedParallelAllAuto(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExec{results: map[string]struct {
		output   string
		exitCode int
		err      error
	}{
		"sc1": {output: "ok", exitCode: 0},
		"sc2": {output: "ok", exitCode: 0},
	}}}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "par", Type: StepParallel, Name: "并行", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "svc1", Type: StepService, Name: "分支1", ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "par", Condition: "success"}}},
		{ID: "svc2", Type: StepService, Name: "分支2", ScriptID: "sc2", DependsOn: []Dependency{{NodeID: "par", Condition: "success"}}},
		{ID: "end", Type: StepEnd, Name: "结束", DependsOn: []Dependency{{NodeID: "svc1", Condition: "success"}, {NodeID: "svc2", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("par"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing != nil {
		t.Fatalf("all-auto parallel should finish (nil landing), got %+v", landing)
	}
	for _, id := range []string{"svc1", "svc2"} {
		if _, ok := inst.StepResults[id]; !ok {
			t.Fatalf("branch %s should be executed", id)
		}
	}
}

func TestRunAutomatedParallelStopsAtUser(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExec{results: map[string]struct {
		output   string
		exitCode int
		err      error
	}{
		"sc1": {output: "ok", exitCode: 0},
		"sc2": {output: "ok", exitCode: 0},
	}}}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "par", Type: StepParallel, Name: "并行", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "svc1", Type: StepService, Name: "分支1", ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "par", Condition: "success"}}},
		{ID: "approve", Type: StepUser, Name: "人工审批", DependsOn: []Dependency{{NodeID: "svc1", Condition: "success"}}},
		{ID: "svc2", Type: StepService, Name: "分支2", ScriptID: "sc2", DependsOn: []Dependency{{NodeID: "par", Condition: "success"}}},
		{ID: "end", Type: StepEnd, Name: "结束", DependsOn: []Dependency{{NodeID: "approve", Condition: "success"}, {NodeID: "svc2", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("par"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "approve" {
		t.Fatalf("parallel with user branch should stop at approve, got %+v", landing)
	}
	// 两条自动分支都应已执行
	for _, id := range []string{"svc1", "svc2"} {
		if _, ok := inst.StepResults[id]; !ok {
			t.Fatalf("branch %s should be executed before stopping", id)
		}
	}
}

// --- 可执行子流程 ---

func TestRunSubflowAutoComplete(t *testing.T) {
	child := &Process{ID: "child", Steps: []Step{
		{ID: "cstart", Type: StepStart},
		{ID: "csvc", Type: StepService, ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "cstart", Condition: "success"}}},
		{ID: "cend", Type: StepEnd, DependsOn: []Dependency{{NodeID: "csvc", Condition: "success"}}},
	}}
	parent := &Process{ID: "parent", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "sub", Type: StepSubflow, SubflowID: "child", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "sub", Condition: "success"}}},
	}}
	s := &Service{
		scriptExec: &fakeScriptExec{results: map[string]struct {
			output   string
			exitCode int
			err      error
		}{
			"sc1": {output: "ok", exitCode: 0},
		}},
		subflowLoader: func(ctx context.Context, id, tenantID string) (*Process, error) { return child, nil },
	}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), parent, inst, parent.StepByID("sub"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("subflow auto-complete should land on parent end, got %+v", landing)
	}
	if _, ok := inst.StepResults["csvc"]; !ok {
		t.Fatal("subflow service step should be recorded")
	}
}

func TestRunSubflowPauseAndResume(t *testing.T) {
	child := &Process{ID: "child", Steps: []Step{
		{ID: "cstart", Type: StepStart},
		{ID: "csvc", Type: StepService, ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "cstart", Condition: "success"}}},
		{ID: "capprove", Type: StepUser, DependsOn: []Dependency{{NodeID: "csvc", Condition: "success"}}},
		{ID: "cend", Type: StepEnd, DependsOn: []Dependency{{NodeID: "capprove", Condition: "success"}}},
	}}
	parent := &Process{ID: "parent", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "sub", Type: StepSubflow, SubflowID: "child", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "sub", Condition: "success"}}},
	}}
	s := &Service{
		scriptExec: &fakeScriptExec{results: map[string]struct {
			output   string
			exitCode int
			err      error
		}{
			"sc1": {output: "ok", exitCode: 0},
		}},
		subflowLoader: func(ctx context.Context, id, tenantID string) (*Process, error) { return child, nil },
	}
	ctx := context.Background()
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}

	// 第一次: 子流程停在 capprove(user), 父流程返回 sub 步骤
	landing, err := s.runAutomated(ctx, parent, inst, parent.StepByID("sub"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "sub" {
		t.Fatalf("subflow pause should return parent sub step, got %+v", landing)
	}
	rec, _ := inst.StepResults["sub"].(map[string]any)
	if rec == nil || rec["subflow_next"] != "capprove" {
		t.Fatalf("pause should record subflow_next=capprove, got %+v", inst.StepResults["sub"])
	}

	// 第二次(模拟用户推进): 从暂停点继续, 子流程到 end, 父流程到 end
	landing2, err := s.runAutomated(ctx, parent, inst, parent.StepByID("sub"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing2 == nil || landing2.ID != "end" {
		t.Fatalf("subflow resume should land on parent end, got %+v", landing2)
	}
	if rec2, _ := inst.StepResults["sub"].(map[string]any); rec2 != nil {
		if _, still := rec2["subflow_next"]; still {
			t.Fatal("subflow_next should be cleared after completion")
		}
	}
}

// --- 测试用内存 store(VoteStep/快照测试需要) ---

type memStore struct {
	procs map[string]*Process
	insts map[string]*ProcessInstance
	vers  []*ProcessVersion
}

func (m *memStore) Create(ctx context.Context, p *Process) error {
	m.procs[p.ID] = p
	return nil
}
func (m *memStore) Get(ctx context.Context, id, tenantID string) (*Process, error) {
	return m.procs[id], nil
}
func (m *memStore) List(ctx context.Context, tenantID string) ([]*Process, error) {
	var out []*Process
	for _, p := range m.procs {
		out = append(out, p)
	}
	return out, nil
}
func (m *memStore) Update(ctx context.Context, p *Process) error {
	m.procs[p.ID] = p
	return nil
}
func (m *memStore) Delete(ctx context.Context, id, tenantID string) error {
	delete(m.procs, id)
	return nil
}
func (m *memStore) CreateInstance(ctx context.Context, inst *ProcessInstance) error {
	m.insts[inst.TicketID] = inst
	return nil
}
func (m *memStore) GetInstanceByTicket(ctx context.Context, ticketID, tenantID string) (*ProcessInstance, error) {
	return m.insts[ticketID], nil
}
func (m *memStore) UpdateInstance(ctx context.Context, inst *ProcessInstance) error {
	m.insts[inst.TicketID] = inst
	return nil
}
func (m *memStore) ListRunningInstances(ctx context.Context) ([]*ProcessInstance, error) {
	var out []*ProcessInstance
	for _, inst := range m.insts {
		out = append(out, inst)
	}
	return out, nil
}
func (m *memStore) SaveVersion(ctx context.Context, v *ProcessVersion) error {
	for i, old := range m.vers {
		if old.ProcessID == v.ProcessID && old.Version == v.Version {
			m.vers[i] = v
			return nil
		}
	}
	m.vers = append(m.vers, v)
	return nil
}
func (m *memStore) ListVersions(ctx context.Context, processID, tenantID string) ([]*ProcessVersion, error) {
	var out []*ProcessVersion
	for _, v := range m.vers {
		if v.ProcessID == processID {
			out = append(out, v)
		}
	}
	return out, nil
}
func (m *memStore) GetVersion(ctx context.Context, processID, tenantID string, version int) (*ProcessVersion, error) {
	for _, v := range m.vers {
		if v.ProcessID == processID && v.Version == version {
			return v, nil
		}
	}
	return nil, nil
}

// --- 会签投票 ---

func TestVoteStep(t *testing.T) {
	m := &memStore{procs: map[string]*Process{
		"p1": {ID: "p1", Steps: []Step{{ID: "approve", Type: StepUser, Approvers: []string{"alice", "bob"}}}},
	}, insts: map[string]*ProcessInstance{}}
	s := &Service{store: m}
	ctx := context.Background()
	m.CreateInstance(ctx, &ProcessInstance{ID: "i1", ProcessID: "p1", TicketID: "t1", TenantID: "default", Status: "running", StepResults: map[string]any{}})

	all, err := s.VoteStep(ctx, "default", "t1", "approve", "alice", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all {
		t.Fatal("first vote should not be all-approved yet")
	}
	all, err = s.VoteStep(ctx, "default", "t1", "approve", "bob", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !all {
		t.Fatal("second vote should be all-approved")
	}
	rec := m.insts["t1"].StepResults["approve"].(map[string]any)
	approvals := rec["approvals"].(map[string]any)
	if approvals["alice"] != true || approvals["bob"] != true {
		t.Fatalf("approvals not recorded: %+v", approvals)
	}
}

// 或签：名册内任一人通过即达成，无需等待其余人表态。
func TestVoteStepOrSign(t *testing.T) {
	m := &memStore{procs: map[string]*Process{
		"p1": {ID: "p1", Steps: []Step{
			{ID: "approve", Type: StepUser, Approvers: []string{"alice", "bob"}, ApprovalType: ApprovalOrSign},
		}},
	}, insts: map[string]*ProcessInstance{}}
	s := &Service{store: m}
	ctx := context.Background()
	m.CreateInstance(ctx, &ProcessInstance{ID: "i1", ProcessID: "p1", TicketID: "t1", TenantID: "default", Status: "running", StepResults: map[string]any{}})

	all, err := s.VoteStep(ctx, "default", "t1", "approve", "alice", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !all {
		t.Fatal("或签下首个通过票即应达成")
	}

	// 反对票不构成达成
	m2 := &memStore{procs: m.procs, insts: map[string]*ProcessInstance{}}
	s2 := &Service{store: m2}
	m2.CreateInstance(ctx, &ProcessInstance{ID: "i2", ProcessID: "p1", TicketID: "t2", TenantID: "default", Status: "running", StepResults: map[string]any{}})
	if all, _ = s2.VoteStep(ctx, "default", "t2", "approve", "alice", false, nil); all {
		t.Fatal("或签下反对票不应达成")
	}
}

// 审批人从表单字段动态注入：静态名单为空时也能形成有效名册。
func TestVoteStepDynamicApprovers(t *testing.T) {
	m := &memStore{procs: map[string]*Process{
		"p1": {ID: "p1", Steps: []Step{
			{ID: "approve", Type: StepUser, ApproverField: "owner"},
		}},
	}, insts: map[string]*ProcessInstance{}}
	s := &Service{store: m}
	ctx := context.Background()
	m.CreateInstance(ctx, &ProcessInstance{ID: "i1", ProcessID: "p1", TicketID: "t1", TenantID: "default", Status: "running", StepResults: map[string]any{}})

	// 未传动态审批人 → 名册为空，直接放行
	if all, _ := s.VoteStep(ctx, "default", "t1", "approve", "carol", true, nil); !all {
		t.Fatal("空名册应直接放行")
	}
	// 传入两人 → 会签需两票
	if all, _ := s.VoteStep(ctx, "default", "t1", "approve", "carol", true, []string{"carol", "dave"}); all {
		t.Fatal("会签下仅一人投票不应达成")
	}
	if all, _ := s.VoteStep(ctx, "default", "t1", "approve", "dave", true, []string{"carol", "dave"}); !all {
		t.Fatal("会签下两人均通过应达成")
	}
}

func TestApproversFromValue(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{nil, 0},
		{"alice", 1},
		{"alice, bob ,", 2},
		{[]string{"alice", "bob"}, 2},
		{[]any{"alice", "", "bob"}, 2},
		{42, 0},
	}
	for _, c := range cases {
		if got := ApproversFromValue(c.in); len(got) != c.want {
			t.Fatalf("ApproversFromValue(%v) = %v, want %d 项", c.in, got, c.want)
		}
	}
}

func TestApproverRosterDedup(t *testing.T) {
	st := &Step{Approvers: []string{"alice", " bob ", ""}}
	got := st.ApproverRoster([]string{"bob", "carol"})
	if len(got) != 3 || got[0] != "alice" || got[1] != "bob" || got[2] != "carol" {
		t.Fatalf("名册去重/排序不符合预期: %v", got)
	}
}

// --- 版本快照隔离: 在途实例按创建时定义推进 ---

func TestProcessSnapshotIsolation(t *testing.T) {
	m := &memStore{procs: map[string]*Process{
		"p1": {ID: "p1", Steps: []Step{
			{ID: "start", Type: StepStart},
			{ID: "approve", Type: StepUser, DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
			{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "approve", Condition: "success"}}},
		}},
	}, insts: map[string]*ProcessInstance{}}
	s := &Service{store: m}
	ctx := context.Background()
	m.CreateInstance(ctx, &ProcessInstance{
		ID: "i1", ProcessID: "p1", TicketID: "t1", TenantID: "default", Status: "running", CurrentStep: "start",
		StepResults: map[string]any{},
		ProcessSnapshot: []Step{
			{ID: "start", Type: StepStart},
			{ID: "svc", Type: StepService, DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
			{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "svc", Condition: "success"}}},
		},
	})
	_, next, err := s.Advance(ctx, "default", "t1", "success", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 若用快照: svc(无脚本)直通 → end; 若误用当前定义: 落 approve
	if next == nil || next.ID != "end" {
		t.Fatalf("snapshot isolation broken: should advance to end(via snapshot svc), got %+v", next)
	}
}

// --- BPMN 2.0 round-trip ---

func TestBPMNRoundTrip(t *testing.T) {
	orig := &Process{ID: "p1", Name: "发布流程", Steps: []Step{
		{ID: "start", Type: StepStart, Name: "开始"},
		{ID: "gw", Type: StepGateway, Name: "风险判断", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "approve", Type: StepUser, Name: "审批", Approvers: []string{"alice", "bob"}, FormScope: "process:p1:step:approve",
			DependsOn: []Dependency{{NodeID: "gw", Condition: `risk_level == "high"`}}},
		{ID: "svc", Type: StepService, Name: "执行", ScriptID: "sc1", DependsOn: []Dependency{{NodeID: "gw", Condition: "always"}}},
		{ID: "timer", Type: StepTimer, Name: "等待", Duration: "30m", DependsOn: []Dependency{{NodeID: "approve", Condition: "success"}}},
		{ID: "end", Type: StepEnd, Name: "结束", DependsOn: []Dependency{{NodeID: "timer", Condition: "success"}, {NodeID: "svc", Condition: "success"}}},
	}}
	xmlData, err := ToBPMNXML(orig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xmlData, "bpmn:process") {
		t.Fatal("export should contain bpmn:process")
	}
	back, err := FromBPMNXML(xmlData)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Steps) != len(orig.Steps) {
		t.Fatalf("step count %d != %d", len(back.Steps), len(orig.Steps))
	}
	byID := map[string]*Step{}
	for i := range back.Steps {
		byID[back.Steps[i].ID] = &back.Steps[i]
	}
	app := byID["approve"]
	if app == nil || len(app.Approvers) != 2 || app.Approvers[0] != "alice" {
		t.Fatalf("approvers lost: %+v", app)
	}
	if app.FormScope != "process:p1:step:approve" {
		t.Fatalf("form_scope lost: %q", app.FormScope)
	}
	sc := byID["svc"]
	if sc == nil || sc.ScriptID != "sc1" {
		t.Fatalf("script_id lost: %+v", sc)
	}
	tm := byID["timer"]
	if tm == nil || tm.Duration != "30m" {
		t.Fatalf("duration lost: %+v", tm)
	}
	foundExpr := false
	for _, d := range app.DependsOn {
		if d.NodeID == "gw" && d.Condition == `risk_level == "high"` {
			foundExpr = true
		}
	}
	if !foundExpr {
		t.Fatalf("expression condition lost: %+v", app.DependsOn)
	}
}

// --- 动作库分发: service 步骤带 ActionType 时走 ActionExecutor ---

type fakeActionExec struct {
	calls int
	fail  bool
	last  map[string]any
}

func (f *fakeActionExec) ExecuteAction(ctx context.Context, actionType string, params map[string]any, tenantID, ticketID string, exprCtx map[string]any) (map[string]any, error) {
	f.calls++
	f.last = params
	if f.fail {
		return nil, fmt.Errorf("boom")
	}
	return map[string]any{"status_code": 200}, nil
}

func TestRunAutomatedActionDispatch(t *testing.T) {
	ae := &fakeActionExec{}
	s := &Service{actionExec: ae}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "act", Type: StepService, ActionType: "http", Inputs: map[string]any{"url": "http://x"}, DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "act", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("act"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ae.calls != 1 {
		t.Fatalf("action executor calls = %d", ae.calls)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("should land on end, got %+v", landing)
	}
	rec := inst.StepResults["act"].(map[string]any)
	if rec["action"] != "http" {
		t.Fatalf("step result missing action: %+v", rec)
	}
}

func TestRunAutomatedActionFailureBranch(t *testing.T) {
	ae := &fakeActionExec{fail: true}
	s := &Service{actionExec: ae}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "act", Type: StepService, ActionType: "http", DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "rollback", Type: StepUser, DependsOn: []Dependency{{NodeID: "act", Condition: "failure"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("act"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 动作失败走 failure 分支 → 落在人工回滚步骤(等待人工)
	if landing == nil || landing.ID != "rollback" {
		t.Fatalf("failure should route to rollback, got %+v", landing)
	}
	if _, hasErr := inst.StepResults["act"].(map[string]any)["error"]; !hasErr {
		t.Fatalf("step result missing error: %+v", inst.StepResults["act"])
	}
}

// --- 表达式条件: 时间 / 时长比较 ---

func TestEvaluateConditionTimeCompare(t *testing.T) {
	ctx := map[string]any{
		"custom_fields": map[string]any{
			"window_start": "2026-08-05 20:00",
			"window_end":   "2026-08-05 22:30",
			"est_duration": "90m",
		},
	}
	cases := []struct {
		expr string
		want bool
	}{
		// 时间比较(右侧为字面量; 字段间比较不在表达式语法内)
		{`window_end > "2026-08-05 20:00"`, true},
		{`window_start < "2026-08-06 00:00"`, true},
		{`window_end <= "2026-08-05 22:30"`, true},
		{`window_start > "2026-08-05 23:00"`, false},
		// 时长比较
		{`est_duration > "1h"`, true},
		{`est_duration <= "2h"`, true},
		{`est_duration < "1h"`, false},
		{`est_duration == "90m"`, true},
	}
	for _, c := range cases {
		got, err := EvaluateCondition(c.expr, ctx)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got != c.want {
			t.Fatalf("%s = %v, want %v", c.expr, got, c.want)
		}
	}
}
