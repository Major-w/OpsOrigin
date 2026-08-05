package workflow

import "testing"

func TestCompileDAGNoDAG(t *testing.T) {
	wf := &Workflow{Steps: []WorkflowStep{
		{ID: "a", Type: StepTypeScript},
		{ID: "b", Type: StepTypeScript},
	}}
	if start := compileDAG(wf); start != "" {
		t.Errorf("non-DAG should return empty start, got %q", start)
	}
}

func TestCompileDAGLinear(t *testing.T) {
	wf := &Workflow{Steps: []WorkflowStep{
		{ID: "a", Type: StepTypeScript},
		{ID: "b", Type: StepTypeScript, DependsOn: []DAGDependency{{NodeID: "a", Condition: "success"}}},
		{ID: "c", Type: StepTypeScript, DependsOn: []DAGDependency{{NodeID: "b", Condition: "success"}}},
	}}
	start := compileDAG(wf)
	if start != "a" {
		t.Fatalf("start=%q want a", start)
	}
	stepA := findStep(wf, "a")
	stepB := findStep(wf, "b")
	stepC := findStep(wf, "c")
	if stepA.OnSuccess != "b" {
		t.Errorf("a.OnSuccess=%q want b", stepA.OnSuccess)
	}
	if stepB.OnSuccess != "c" {
		t.Errorf("b.OnSuccess=%q want c", stepB.OnSuccess)
	}
	if stepC.OnSuccess != "" {
		t.Errorf("c.OnSuccess=%q want empty", stepC.OnSuccess)
	}
}

func TestCompileDAGConditionEdges(t *testing.T) {
	wf := &Workflow{Steps: []WorkflowStep{
		{ID: "a", Type: StepTypeScript},
		{ID: "failNode", Type: StepTypeScript, DependsOn: []DAGDependency{{NodeID: "a", Condition: "failure"}}},
		{ID: "alwaysNode", Type: StepTypeScript, DependsOn: []DAGDependency{{NodeID: "a", Condition: "always"}}},
	}}
	compileDAG(wf)
	stepA := findStep(wf, "a")
	if stepA.OnFailure != "failNode" {
		t.Errorf("a.OnFailure=%q want failNode", stepA.OnFailure)
	}
	if stepA.OnSuccess != "alwaysNode" {
		t.Errorf("a.OnSuccess=%q want alwaysNode", stepA.OnSuccess)
	}
}

func TestCompileDAGStartNode(t *testing.T) {
	wf := &Workflow{Steps: []WorkflowStep{
		{ID: "a", Type: StepTypeScript, DependsOn: []DAGDependency{{NodeID: "b", Condition: "success"}}},
		{ID: "b", Type: StepTypeStart},
	}}
	if start := compileDAG(wf); start != "b" {
		t.Errorf("start=%q want b (explicit start node)", start)
	}
}

func TestResolveValue(t *testing.T) {
	ctx := map[string]any{"step1": map[string]any{"output": "hello", "nested": map[string]any{"v": "deep"}}}
	if got := resolveValue("pre ${step1.output} post", ctx); got != "pre hello post" {
		t.Errorf("got %q", got)
	}
	if got := resolveValue("${step1.nested.v}", ctx); got != "deep" {
		t.Errorf("nested got %q", got)
	}
	if got := resolveValue("${nope.x}", ctx); got != "${nope.x}" {
		t.Errorf("unknown var should be preserved, got %q", got)
	}
}

func TestResolveParams(t *testing.T) {
	ctx := map[string]any{"step1": map[string]any{"output": "hello"}}
	inputs := map[string]any{"cmd": "run ${step1.output}", "run_as": "root", "count": 3}
	params := resolveParams(inputs, ctx)
	if params["cmd"] != "run hello" {
		t.Errorf("cmd=%q want \"run hello\"", params["cmd"])
	}
	if _, ok := params["run_as"]; ok {
		t.Error("run_as should be skipped")
	}
	if params["count"] != "3" {
		t.Errorf("count=%q want 3", params["count"])
	}
}
