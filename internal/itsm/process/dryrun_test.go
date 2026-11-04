package process

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// --- voteSettled: 5 种表决语义 ---

func TestVoteSettled(t *testing.T) {
	roster := []string{"a", "b", "c"}
	votes := func(m map[string]bool) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	cases := []struct {
		name      string
		typ       string
		threshold float64
		approvals map[string]any
		want      bool
	}{
		{"会签-全部通过", ApprovalCountersign, 0, votes(map[string]bool{"a": true, "b": true, "c": true}), true},
		{"会签-一票未投", ApprovalCountersign, 0, votes(map[string]bool{"a": true, "b": true}), false},
		{"会签-一票反对", ApprovalCountersign, 0, votes(map[string]bool{"a": true, "b": true, "c": false}), false},
		{"或签-任一通过", ApprovalOrSign, 0, votes(map[string]bool{"b": true}), true},
		{"或签-全部反对", ApprovalOrSign, 0, votes(map[string]bool{"a": false, "b": false, "c": false}), false},
		{"全员表态-尚有人未投", ApprovalRespondedAny, 0, votes(map[string]bool{"a": true, "b": false}), false},
		{"全员表态-有人通过", ApprovalRespondedAny, 0, votes(map[string]bool{"a": true, "b": false, "c": false}), true},
		{"全员表态-全反对", ApprovalRespondedAny, 0, votes(map[string]bool{"a": false, "b": false, "c": false}), false},
		{"比例-50% 达标(2/3=66%)", ApprovalPercent, 50, votes(map[string]bool{"a": true, "b": true, "c": false}), true},
		{"比例-50% 未达(1/3)", ApprovalPercent, 50, votes(map[string]bool{"a": true, "b": false, "c": false}), false},
		{"比例-默认阈值50", ApprovalPercent, 0, votes(map[string]bool{"a": true, "b": true, "c": false}), true},
		{"比例-100%(2/3)", ApprovalPercent, 100, votes(map[string]bool{"a": true, "b": true, "c": false}), false},
		{"比例-100%(3/3)", ApprovalPercent, 100, votes(map[string]bool{"a": true, "b": true, "c": true}), true},
		{"数量-默认1", ApprovalCount, 0, votes(map[string]bool{"c": true}), true},
		{"数量-2人(仅1通过)", ApprovalCount, 2, votes(map[string]bool{"a": true, "b": false, "c": false}), false},
		{"数量-2人(2通过)", ApprovalCount, 2, votes(map[string]bool{"a": true, "b": true, "c": false}), true},
		{"数量-阈值超过名册按名册封顶", ApprovalCount, 5, votes(map[string]bool{"a": true, "b": true, "c": true}), true},
	}
	for _, tc := range cases {
		if got := voteSettled(tc.typ, tc.threshold, roster, tc.approvals); got != tc.want {
			t.Errorf("%s: voteSettled(%s, %.0f) = %v, want %v", tc.name, tc.typ, tc.threshold, got, tc.want)
		}
	}
}

// --- DryRun ---

func TestDryRunGatewayBranchAndPause(t *testing.T) {
	s := &Service{}
	proc := testProcess() // gateway → (high)approve / (always)execute → end
	// 带示例变量: high → 走 approve(人工), 停在 approve
	res := s.DryRun(context.Background(), proc, map[string]any{"risk_level": "high"})
	if res.PausedAt != "approve" {
		t.Fatalf("high risk should pause at approve, got paused_at=%s summary=%s", res.PausedAt, res.Summary)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("no warnings expected with sample vars, got %v", res.Warnings)
	}
	// 无示例变量: 网关按默认分支, 应有告警且停在 execute
	res = s.DryRun(context.Background(), proc, nil)
	if res.PausedAt != "execute" {
		t.Fatalf("without vars should fall to default branch execute, got %s", res.PausedAt)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected warning about missing sample vars")
	}
	// execute(不经过的分支) 应标记 skipped
	foundSkip := false
	for _, st := range res.Steps {
		if st.StepID == "approve" && st.Status == "skipped" {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Error("unreached branch should be marked skipped")
	}
}

func TestDryRunPauseDetailsAndAutoEnd(t *testing.T) {
	s := &Service{}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "start", Type: StepStart, Name: "开始"},
		{ID: "svc", Type: StepService, Name: "跑脚本", ScriptID: "sc1",
			Inputs: map[string]any{"target": "${ticket.title}"}, RetryCount: 2,
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, Name: "结束", DependsOn: []Dependency{{NodeID: "svc", Condition: "success"}}},
	}}
	res := s.DryRun(context.Background(), proc, map[string]any{"ticket": map[string]any{"title": "磁盘告警"}})
	if res.PausedAt != "" {
		t.Fatalf("auto-only flow should reach end, got paused_at=%s", res.PausedAt)
	}
	var svc *DryRunStep
	for i := range res.Steps {
		if res.Steps[i].StepID == "svc" {
			svc = &res.Steps[i]
		}
	}
	if svc == nil || svc.Status != "would_run" {
		t.Fatalf("service step should be would_run, got %+v", svc)
	}
	if !strings.Contains(svc.Detail, "脚本 sc1") || !strings.Contains(svc.Detail, "磁盘告警") || !strings.Contains(svc.Detail, "重试最多 2 次") {
		t.Errorf("service detail should preview script/params/retry, got: %s", svc.Detail)
	}
}

func TestDryRunWaitAndTimerPauseDetails(t *testing.T) {
	s := &Service{}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "w", Type: StepWait, Name: "等解决", Condition: `ticket.status == "resolved"`,
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "w", Condition: "success"}}},
	}}
	res := s.DryRun(context.Background(), proc, map[string]any{"ticket": map[string]any{"status": "resolved"}})
	if res.PausedAt != "w" {
		t.Fatalf("wait should pause, got %s", res.PausedAt)
	}
	var w *DryRunStep
	for i := range res.Steps {
		if res.Steps[i].StepID == "w" {
			w = &res.Steps[i]
		}
	}
	if !strings.Contains(w.Detail, "已成立") {
		t.Errorf("resolved sample should note condition currently true, got: %s", w.Detail)
	}
	// 条件为空的 wait 应告警
	proc.Steps[1].Condition = ""
	res = s.DryRun(context.Background(), proc, nil)
	if len(res.Warnings) == 0 {
		t.Error("empty wait condition should produce warning")
	}
}

// --- service 重试 ---

func TestRunAutomatedScriptRetryThenSuccess(t *testing.T) {
	fe := &fakeScriptExecFail{}
	s := &Service{scriptExec: fe}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc", Type: StepService, Name: "跑脚本", ScriptID: "sc1", RetryCount: 3,
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "svc", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("svc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("retry then success should land on end, got %+v", landing)
	}
	if fe.calls != 2 {
		t.Fatalf("expected 2 executions (1 fail + 1 success), got %d", fe.calls)
	}
	if r, _ := inst.StepResults["svc"].(map[string]any); r == nil || r["attempts"] != 2 {
		t.Fatalf("attempts should be recorded as 2, got %+v", inst.StepResults["svc"])
	}
}

func TestRunAutomatedScriptRetryExhausted(t *testing.T) {
	s := &Service{scriptExec: &fakeScriptExecAlwaysFail{}}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc", Type: StepService, Name: "跑脚本", ScriptID: "sc1", RetryCount: 2,
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "rb", Type: StepUser, Name: "人工回滚", DependsOn: []Dependency{{NodeID: "svc", Condition: "failure"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("svc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "rb" {
		t.Fatalf("exhausted retries should route failure branch, got %+v", landing)
	}
	if r, _ := inst.StepResults["svc"].(map[string]any); r == nil || r["attempts"] != 3 {
		t.Fatalf("attempts should be 3 (1+2 retries), got %+v", inst.StepResults["svc"])
	}
}

// fakeScriptExecFail 首次失败, 之后成功。
type fakeScriptExecFail struct{ calls int }

func (f *fakeScriptExecFail) Execute(string, map[string]string, int, string, string, string) (string, int, error) {
	f.calls++
	if f.calls == 1 {
		return "", 1, errors.New("transient")
	}
	return "ok", 0, nil
}

// fakeScriptExecAlwaysFail 永远失败。
type fakeScriptExecAlwaysFail struct{ calls int }

func (f *fakeScriptExecAlwaysFail) Execute(string, map[string]string, int, string, string, string) (string, int, error) {
	f.calls++
	return "", 1, errors.New("boom")
}
