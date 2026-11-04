package process

import (
	"context"
	"testing"
)

// --- subst / lookupVar ---

func TestSubst(t *testing.T) {
	varCtx := map[string]any{
		"ticket": map[string]any{"title": "核心交换机变更", "risk_level": "high"},
		"custom_fields": map[string]any{
			"env": "prod",
		},
		"steps": map[string]any{
			"prepare": map[string]any{"status": "success", "output": "backup-ok"},
		},
		"status": "pending_review",
	}
	cases := []struct{ in, want string }{
		{"变更单: ${ticket.title}", "变更单: 核心交换机变更"},
		{"环境=${custom_fields.env}", "环境=prod"},
		{"上游输出=${steps.prepare.output}", "上游输出=backup-ok"},
		{"状态=${status}", "状态=pending_review"},
		{"缺失=${ticket.nope}", "缺失="},
		{"缺失路径=${steps.nope.output}", "缺失路径="},
		{"未闭合 ${ticket.title", "未闭合 ${ticket.title"},
		{"无占位符原样", "无占位符原样"},
		{"${ticket.risk_level}-${custom_fields.env}", "high-prod"},
	}
	for _, tc := range cases {
		if got := subst(tc.in, varCtx); got != tc.want {
			t.Errorf("subst(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSubstMapDeep(t *testing.T) {
	varCtx := map[string]any{
		"ticket": map[string]any{"title": "T1"},
		"steps":  map[string]any{"s1": map[string]any{"output": "v1"}},
	}
	inputs := map[string]any{
		"title":  "${ticket.title}",
		"nested": map[string]any{"ref": "${steps.s1.output}"},
		"list":   []any{"${ticket.title}", 3, map[string]any{"k": "${steps.s1.output}"}},
		"num":    42,
	}
	got := substMap(inputs, varCtx)
	if got["title"] != "T1" {
		t.Errorf("title = %v", got["title"])
	}
	if nested, ok := got["nested"].(map[string]any); !ok || nested["ref"] != "v1" {
		t.Errorf("nested = %v", got["nested"])
	}
	list, ok := got["list"].([]any)
	if !ok || list[0] != "T1" || list[1] != 3 {
		t.Errorf("list = %v", got["list"])
	}
	if m, ok := list[2].(map[string]any); !ok || m["k"] != "v1" {
		t.Errorf("list[2] = %v", list[2])
	}
	if got["num"] != 42 {
		t.Errorf("num = %v", got["num"])
	}
	// 原配置不被修改
	if inputs["title"] != "${ticket.title}" {
		t.Errorf("inputs mutated: %v", inputs["title"])
	}
	// 空 varCtx / 空 inputs 原样返回
	if substMap(inputs, nil) == nil || len(substMap(nil, varCtx)) != 0 {
		t.Error("substMap edge cases should pass through")
	}
}

// --- buildVarCtx ---

type fakeTicketVars struct{ called int }

func (f *fakeTicketVars) TicketVars(ctx context.Context, tenantID, ticketID string) map[string]any {
	f.called++
	return map[string]any{
		"ticket":        map[string]any{"title": "工单A", "risk_level": "high"},
		"custom_fields": map[string]any{"env": "prod"},
	}
}

func TestBuildVarCtx(t *testing.T) {
	fp := &fakeTicketVars{}
	s := &Service{ticketVars: fp}
	inst := &ProcessInstance{
		TenantID: "default", TicketID: "t1",
		StepResults: map[string]any{
			"s1": map[string]any{"condition": "success", "output": "hello", "at": "t"},
			"s2": "plain-string-rec",
		},
	}
	output := map[string]any{"status": "pending_review", "custom_fields": map[string]any{"env": "stg"}}
	varCtx := s.buildVarCtx(context.Background(), inst, output)

	// ticket 上下文注入
	if tv, ok := varCtx["ticket"].(map[string]any); !ok || tv["title"] != "工单A" {
		t.Errorf("ticket ctx = %v", varCtx["ticket"])
	}
	if fp.called != 1 {
		t.Errorf("provider called %d times", fp.called)
	}
	// 当次 output 平铺优先于 provider 的 custom_fields(后写覆盖)
	if cf, ok := varCtx["custom_fields"].(map[string]any); !ok || cf["env"] != "stg" {
		t.Errorf("custom_fields flatten override failed: %v", varCtx["custom_fields"])
	}
	// output 平铺保持旧网关条件语义
	if varCtx["status"] != "pending_review" {
		t.Errorf("flattened status = %v", varCtx["status"])
	}
	// 累积步骤输出 + condition 别名
	steps, ok := varCtx["steps"].(map[string]any)
	if !ok {
		t.Fatalf("steps ctx missing")
	}
	s1, ok := steps["s1"].(map[string]any)
	if !ok || s1["output"] != "hello" || s1["status"] != "success" {
		t.Errorf("steps.s1 = %v", steps["s1"])
	}
	if steps["s2"] != "plain-string-rec" {
		t.Errorf("steps.s2 = %v", steps["s2"])
	}
	// output 键显式保存
	if om, ok := varCtx["output"].(map[string]any); !ok || om["status"] != "pending_review" {
		t.Errorf("output ctx = %v", varCtx["output"])
	}
}

func TestBuildVarCtxNoProvider(t *testing.T) {
	s := &Service{} // 未注入 provider: 不 panic, 变量仅含步骤输出
	inst := &ProcessInstance{TenantID: "default", TicketID: "t1", StepResults: map[string]any{}}
	varCtx := s.buildVarCtx(context.Background(), inst, nil)
	if _, ok := varCtx["ticket"]; ok {
		t.Error("ticket ctx should be absent without provider")
	}
	if _, ok := varCtx["steps"]; !ok {
		t.Error("steps ctx should always exist")
	}
}

// --- runAutomated: 变量链路端到端(脚本参数替换 + 上游输出传递) ---

type capturingScriptExec struct{ gotParams map[string]string }

func (f *capturingScriptExec) Execute(scriptID string, params map[string]string, timeout int, runAs, agentID, tenantID string) (string, int, error) {
	f.gotParams = params
	return "deploy-ok", 0, nil
}

func TestRunAutomatedSubstitutesScriptInputs(t *testing.T) {
	cap1, cap2 := &capturingScriptExec{}, &capturingScriptExec{}
	s := &Service{scriptExec: &multiScriptExec{m: map[string]ScriptExecutor{"sc1": cap1, "sc2": cap2}}}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc1", Type: StepService, ScriptID: "sc1", Inputs: map[string]any{"title": "${ticket.title}", "env": "${custom_fields.env}"},
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "svc2", Type: StepService, ScriptID: "sc2", Inputs: map[string]any{"upstream": "${steps.svc1.output}"},
			DependsOn: []Dependency{{NodeID: "svc1", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "svc2", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	varCtx := map[string]any{
		"ticket":        map[string]any{"title": "上线工单"},
		"custom_fields": map[string]any{"env": "prod"},
	}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("svc1"), varCtx)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("should land on end, got %+v", landing)
	}
	if cap1.gotParams["title"] != "上线工单" || cap1.gotParams["env"] != "prod" {
		t.Errorf("svc1 params = %v", cap1.gotParams)
	}
	// 变量链路核心断言: 下游脚本参数引用到了上游步骤的真实输出
	if cap2.gotParams["upstream"] != "deploy-ok" {
		t.Errorf("svc2 upstream = %v, want deploy-ok", cap2.gotParams["upstream"])
	}
}

// multiScriptExec 按 script_id 分发给不同桩(同进程内两个 service 节点各用一个)。
type multiScriptExec struct{ m map[string]ScriptExecutor }

func (f *multiScriptExec) Execute(scriptID string, params map[string]string, timeout int, runAs, agentID, tenantID string) (string, int, error) {
	if e, ok := f.m[scriptID]; ok {
		return e.Execute(scriptID, params, timeout, runAs, agentID, tenantID)
	}
	return "", 1, nil
}

// --- runAutomated: notify 节点真实执行 ---

func TestRunAutomatedNotifyStepSends(t *testing.T) {
	var gotEvent, gotTitle, gotBody string
	s := &Service{notifier: func(ctx context.Context, tenantID, event, title, content string) error {
		gotEvent, gotTitle, gotBody = event, title, content
		return nil
	}}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "nt", Type: StepNotify, NotifyChannel: "email", NotifyTitle: "变更: ${ticket.title}", NotifyBody: "风险=${custom_fields.risk}",
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "nt", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	varCtx := map[string]any{
		"ticket":        map[string]any{"title": "数据库变更"},
		"custom_fields": map[string]any{"risk": "high"},
	}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("nt"), varCtx)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("notify step should pass through to end, got %+v", landing)
	}
	if gotEvent != "email" || gotTitle != "变更: 数据库变更" || gotBody != "风险=high" {
		t.Errorf("notify got event=%q title=%q body=%q", gotEvent, gotTitle, gotBody)
	}
	if rec, ok := inst.StepResults["nt"].(map[string]any); !ok || rec["sent"] != true {
		t.Errorf("notify step result = %v", inst.StepResults["nt"])
	}
}

// notifier 未注入时 notify 节点保持直通(向后兼容既有流程定义)。
func TestRunAutomatedNotifyStepPassthroughWithoutNotifier(t *testing.T) {
	s := &Service{}
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "nt", Type: StepNotify, DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "nt", Condition: "success"}}},
	}}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	landing, err := s.runAutomated(context.Background(), proc, inst, proc.StepByID("nt"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("should land on end, got %+v", landing)
	}
	if _, ok := inst.StepResults["nt"]; ok {
		t.Error("no notifier: notify step should not record a result")
	}
}
