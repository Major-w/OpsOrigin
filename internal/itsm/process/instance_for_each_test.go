package process

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// subflowForEachFlow 构造 For Each 场景: 父流程 sub 步骤循环执行 child 全自动子流程,
// 子流程 service 步骤的脚本参数引用 ${params.host} 与 ${item}。
func subflowForEachFlow(params map[string]string, forEach string) (*Process, *Process) {
	child := &Process{ID: "child", Steps: []Step{
		{ID: "cstart", Type: StepStart},
		{ID: "csvc", Type: StepService, ScriptID: "sc_each", Inputs: map[string]any{
			"host": "${params.host}",
			"item": "${item}",
		}, DependsOn: []Dependency{{NodeID: "cstart", Condition: "success"}}},
		{ID: "cend", Type: StepEnd, DependsOn: []Dependency{{NodeID: "csvc", Condition: "success"}}},
	}}
	parent := &Process{ID: "parent", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "sub", Type: StepSubflow, SubflowID: "child", SubflowParams: params, ForEach: forEach,
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "sub", Condition: "success"}}},
	}}
	return parent, child
}

// forEachScript 记录每次脚本执行的参数表, 供断言 item/item_index/params 注入。
type forEachScript struct {
	calls []map[string]string
}

func (f *forEachScript) Execute(scriptID string, params map[string]string, timeout int, runAs, agentID, tenantID string) (string, int, error) {
	f.calls = append(f.calls, params)
	return "ok", 0, nil
}

// TestSubflowForEachItemsAndParams 三项列表逐项执行: item/item_index 注入 + params 命名空间生效,
// 循环完成后 for_each 结果数组记录 items_total=3, 父流程落到 end。
func TestSubflowForEachItemsAndParams(t *testing.T) {
	script := &forEachScript{}
	parent, child := subflowForEachFlow(
		map[string]string{"host": "${custom_fields.primary_host}"},
		"${steps.query.output.items}",
	)
	s := &Service{
		scriptExec:    script,
		subflowLoader: func(ctx context.Context, id, tenantID string) (*Process, error) { return child, nil },
	}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	exprCtx := map[string]any{
		"steps": map[string]any{
			"query": map[string]any{"output": map[string]any{"items": []any{"h1", "h2", "h3"}}},
		},
		"custom_fields": map[string]any{"primary_host": "h1"},
	}

	landing, err := s.runAutomated(context.Background(), parent, inst, parent.StepByID("sub"), exprCtx)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("for-each over auto subflow should finish at end, got %+v", landing)
	}
	if len(script.calls) != 3 {
		t.Fatalf("script should run once per item (3), got %d", len(script.calls))
	}
	for i, call := range script.calls {
		if call["item"] != fmt.Sprintf("h%d", i+1) {
			t.Errorf("call %d item = %q, want h%d", i, call["item"], i+1)
		}
		if call["host"] != "h1" {
			t.Errorf("call %d params.host = %q, want h1 (explicit param resolved from parent ctx)", i, call["host"])
		}
	}
	rec, _ := inst.StepResults["sub"].(map[string]any)
	if rec == nil || rec["items_total"] != 3 {
		t.Fatalf("for_each record with items_total=3 expected, got %+v", rec)
	}
	if arr, ok := rec["for_each"].([]any); !ok || len(arr) != 3 {
		t.Fatalf("for_each results array of 3 expected, got %+v", rec["for_each"])
	}
}

// TestSubflowForEachEmptyList 空列表循环零次: 直接完成, 不执行脚本, 父流程继续。
func TestSubflowForEachEmptyList(t *testing.T) {
	script := &forEachScript{}
	parent, child := subflowForEachFlow(nil, "${custom_fields.hosts}")
	s := &Service{
		scriptExec:    script,
		subflowLoader: func(ctx context.Context, id, tenantID string) (*Process, error) { return child, nil },
	}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	exprCtx := map[string]any{"custom_fields": map[string]any{"hosts": ""}}

	landing, err := s.runAutomated(context.Background(), parent, inst, parent.StepByID("sub"), exprCtx)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("empty list should complete immediately, got %+v", landing)
	}
	if len(script.calls) != 0 {
		t.Fatalf("no script call expected for empty list, got %d", len(script.calls))
	}
	rec, _ := inst.StepResults["sub"].(map[string]any)
	if rec == nil || rec["items_total"] != 0 {
		t.Fatalf("items_total=0 expected, got %+v", rec)
	}
}

// TestSubflowForEachRejectsWaitSteps 子流程含人工等待点时报错(仅支持全自动子流程)。
func TestSubflowForEachRejectsWaitSteps(t *testing.T) {
	child := &Process{ID: "child", Steps: []Step{
		{ID: "cstart", Type: StepStart},
		{ID: "capprove", Type: StepUser, DependsOn: []Dependency{{NodeID: "cstart", Condition: "success"}}},
		{ID: "cend", Type: StepEnd, DependsOn: []Dependency{{NodeID: "capprove", Condition: "success"}}},
	}}
	parent := &Process{ID: "parent", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "sub", Type: StepSubflow, SubflowID: "child", ForEach: "${custom_fields.hosts}",
			DependsOn: []Dependency{{NodeID: "start", Condition: "success"}}},
	}}
	s := &Service{
		subflowLoader: func(ctx context.Context, id, tenantID string) (*Process, error) { return child, nil },
	}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	exprCtx := map[string]any{"custom_fields": map[string]any{"hosts": "a,b"}}

	if _, err := s.runAutomated(context.Background(), parent, inst, parent.StepByID("sub"), exprCtx); err == nil {
		t.Fatal("for-each over subflow with user step should fail")
	} else if !strings.Contains(err.Error(), "For Each") {
		t.Fatalf("error should mention For Each, got %v", err)
	}
}

// TestForEachItems 归一化: 数组/逗号分隔串/标量/nil 四种形态。
func TestForEachItems(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want int
	}{
		{"nil", nil, 0},
		{"array", []any{"a", "b"}, 2},
		{"string slice", []string{"a", "b", "c"}, 3},
		{"comma string", "a, b,,c", 3},
		{"empty string", "", 0},
		{"scalar", "solo", 1},
	}
	for _, c := range cases {
		if got := len(forEachItems(c.raw)); got != c.want {
			t.Errorf("%s: got %d items, want %d", c.name, got, c.want)
		}
	}
}

// TestSubflowExplicitParamsOnly 不带 For Each 的显式传参: params 命名空间注入且父变量仍可用。
func TestSubflowExplicitParamsOnly(t *testing.T) {
	child := &Process{ID: "child", Steps: []Step{
		{ID: "cstart", Type: StepStart},
		{ID: "csvc", Type: StepService, ScriptID: "sc_p", Inputs: map[string]any{
			"owner": "${params.owner}",
			"host":  "${custom_fields.primary_host}",
		}, DependsOn: []Dependency{{NodeID: "cstart", Condition: "success"}}},
		{ID: "cend", Type: StepEnd, DependsOn: []Dependency{{NodeID: "csvc", Condition: "success"}}},
	}}
	parent := &Process{ID: "parent", Steps: []Step{
		{ID: "start", Type: StepStart},
		{ID: "sub", Type: StepSubflow, SubflowID: "child",
			SubflowParams: map[string]string{"owner": "${steps.query.output.owner}"},
			DependsOn:     []Dependency{{NodeID: "start", Condition: "success"}}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "sub", Condition: "success"}}},
	}}
	script := &forEachScript{}
	s := &Service{
		scriptExec:    script,
		subflowLoader: func(ctx context.Context, id, tenantID string) (*Process, error) { return child, nil },
	}
	inst := &ProcessInstance{TicketID: "t1", TenantID: "default", StepResults: map[string]any{}}
	exprCtx := map[string]any{
		"steps": map[string]any{
			"query": map[string]any{"output": map[string]any{"owner": "alice"}},
		},
		"custom_fields": map[string]any{"primary_host": "web-01"},
	}

	landing, err := s.runAutomated(context.Background(), parent, inst, parent.StepByID("sub"), exprCtx)
	if err != nil {
		t.Fatal(err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("subflow with explicit params should finish at parent end, got %+v", landing)
	}
	if len(script.calls) != 1 {
		t.Fatalf("script should run once, got %d", len(script.calls))
	}
	if script.calls[0]["owner"] != "alice" {
		t.Fatalf("params.owner = %q, want alice", script.calls[0]["owner"])
	}
	if script.calls[0]["host"] != "web-01" {
		t.Fatalf("parent var host = %q, want web-01 (context inheritance preserved)", script.calls[0]["host"])
	}
}
