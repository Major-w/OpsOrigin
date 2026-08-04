package process

import (
	"context"
	"testing"
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
