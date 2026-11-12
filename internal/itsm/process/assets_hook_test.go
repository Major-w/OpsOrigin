package process

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// P1/P2 流程引擎接线测试：决策绑定注入 decision.* 变量、动作模板运行期解析。
// 复用 service_form_hook_test.go 的 formHookStore（嵌入 Store 接口的最小 fake）。

type stubDecisionEvaluator struct {
	calls int
	in    map[string]any
	out   map[string]any
	err   error
}

func (e *stubDecisionEvaluator) EvaluateDecision(_ context.Context, _, _ string, _ int, inputs map[string]any) (map[string]any, []string, error) {
	e.calls++
	e.in = inputs
	if e.err != nil {
		return nil, nil, e.err
	}
	return e.out, []string{"决策表:矩阵#第1行"}, nil
}

type stubTemplateResolver struct {
	actionType string
	inputs     map[string]any
	err        error
}

func (r *stubTemplateResolver) ResolveActionTemplate(_ context.Context, _, _ string, _ int) (string, map[string]any, error) {
	if r.err != nil {
		return "", nil, r.err
	}
	return r.actionType, r.inputs, nil
}

type recordingActionExec struct {
	action string
	params map[string]any
}

func (r *recordingActionExec) ExecuteAction(_ context.Context, actionType string, params map[string]any, _, _ string, _ map[string]any) (map[string]any, error) {
	r.action, r.params = actionType, params
	return map[string]any{"ok": true}, nil
}

func newAssetHookTestService(bindings []DecisionBinding) (*Service, *formHookStore) {
	st := &formHookStore{items: map[string]*Process{}}
	st.items["p1"] = &Process{
		ID: "p1", TenantID: "default", Name: "测试流程",
		DecisionBindings: bindings,
		Steps:            []Step{{ID: "start", Type: StepStart}},
	}
	return &Service{store: st}, st
}

func newHookInst() *ProcessInstance {
	return &ProcessInstance{ProcessID: "p1", TenantID: "default", TicketID: "t1", StepResults: map[string]any{}}
}

func TestDecisionBindingInjectsVarCtx(t *testing.T) {
	svc, _ := newAssetHookTestService([]DecisionBinding{
		{AssetID: "a1", Alias: "route", Inputs: map[string]string{"env": "${custom_fields.env}"}},
	})
	ev := &stubDecisionEvaluator{out: map[string]any{"level": "P1", "owner": "alice"}}
	svc.SetDecisionEvaluator(ev)

	ctx := svc.buildVarCtx(context.Background(), newHookInst(), map[string]any{
		"custom_fields": map[string]any{"env": "prod", "impact": "global"},
		"ticket":        map[string]any{"risk_level": "high"},
	})
	if ev.calls != 1 {
		t.Fatalf("求值器应被调用 1 次，实际 %d", ev.calls)
	}
	if ev.in["env"] != "prod" {
		t.Fatalf("显式 ${custom_fields.env} 映射未生效: %v", ev.in)
	}
	if ev.in["impact"] != "global" {
		t.Fatalf("未映射条件列应按同名字段取默认值: %v", ev.in)
	}
	d, ok := ctx["decision"].(map[string]any)
	if !ok {
		t.Fatal("decision 命名空间缺失")
	}
	route, ok := d["route"].(map[string]any)
	if !ok || route["level"] != "P1" {
		t.Fatalf("decision.route 输出异常: %v", d)
	}
	// 网关条件（点路径）与动作参数（${...} 替换）都能直接引用决策输出。
	if ok, err := EvaluateCondition(`decision.route.level == "P1"`, ctx); err != nil || !ok {
		t.Fatalf("网关条件未能命中决策输出: ok=%v err=%v ctx=%v", ok, err, ctx)
	}
	if got := subst("派给 ${decision.route.owner}", ctx); got != "派给 alice" {
		t.Fatalf("变量替换未生效: %q", got)
	}
}

func TestDecisionBindingDisabledWithoutEvaluator(t *testing.T) {
	svc, _ := newAssetHookTestService([]DecisionBinding{{AssetID: "a1", Alias: "route"}})
	ctx := svc.buildVarCtx(context.Background(), newHookInst(), nil)
	if _, ok := ctx["decision"]; ok {
		t.Fatal("未注入求值器时不应出现 decision 命名空间（存量流程零改动）")
	}
}

func TestDecisionBindingFailureIsNonBlocking(t *testing.T) {
	svc, _ := newAssetHookTestService([]DecisionBinding{{AssetID: "a1", Alias: "route"}})
	svc.SetDecisionEvaluator(&stubDecisionEvaluator{err: errors.New("资产已删除")})
	ctx := svc.buildVarCtx(context.Background(), newHookInst(), nil)
	d, ok := ctx["decision"].(map[string]any)
	if !ok || len(d) != 0 {
		t.Fatalf("求值失败应保留空 decision 命名空间而非阻断流程: %v", ctx["decision"])
	}
}

func TestValidateDecisionBindings(t *testing.T) {
	cases := []struct {
		name    string
		p       *Process
		wantErr string
	}{
		{"空绑定", &Process{}, ""},
		{"合法", &Process{DecisionBindings: []DecisionBinding{{AssetID: "a1", Alias: "route"}}}, ""},
		{"缺 alias", &Process{DecisionBindings: []DecisionBinding{{AssetID: "a1"}}}, "均不能为空"},
		{"alias 重复", &Process{DecisionBindings: []DecisionBinding{
			{AssetID: "a1", Alias: "x"}, {AssetID: "a2", Alias: "x"},
		}}, "重复"},
		{"模板 id 为空", &Process{Steps: []Step{{ID: "svc", TemplateRef: &ActionTemplateRef{}}}}, "template_id 为空"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateDecisionBindings(c.p)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("不应报错: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("期望包含 %q 的错误，实际 %v", c.wantErr, err)
			}
		})
	}
}

func TestResolveStepActionTemplateOverride(t *testing.T) {
	svc := &Service{}
	// 无引用：原样返回
	plain := &Step{ActionType: "http", Inputs: map[string]any{"url": "http://local"}}
	if at, in, err := svc.resolveStepAction(context.Background(), "default", plain); err != nil || at != "http" || in["url"] != "http://local" {
		t.Fatalf("无模板引用时应原样返回: %q %v %v", at, in, err)
	}
	// 未装配解析器：fail-closed
	ref := &Step{TemplateRef: &ActionTemplateRef{TemplateID: "tpl1"}, Inputs: map[string]any{"url": "http://override"}}
	if _, _, err := svc.resolveStepAction(context.Background(), "default", ref); err == nil {
		t.Fatal("解析器未装配时应返回错误")
	}
	// 正常解析：模板为权威来源，步骤同名键覆盖
	svc.SetActionTemplateResolver(&stubTemplateResolver{
		actionType: "notify", inputs: map[string]any{"event": "ticket.created", "title": "模板标题"},
	})
	at, in, err := svc.resolveStepAction(context.Background(), "default", ref)
	if err != nil || at != "notify" {
		t.Fatalf("模板动作类型应生效: %q %v", at, err)
	}
	if in["event"] != "ticket.created" || in["url"] != "http://override" {
		t.Fatalf("模板默认参数与步骤覆盖参数应合并: %v", in)
	}
	// 模板解析失败：error 上抛，由动作分支走 failure
	svc.SetActionTemplateResolver(&stubTemplateResolver{err: errors.New("模板未发布")})
	if _, _, err := svc.resolveStepAction(context.Background(), "default", ref); err == nil {
		t.Fatal("模板解析失败应返回错误")
	}
}

func TestRunAutomatedExecutesTemplateRef(t *testing.T) {
	svc, _ := newAssetHookTestService(nil)
	exec := &recordingActionExec{}
	svc.SetActionExecutor(exec)
	svc.SetActionTemplateResolver(&stubTemplateResolver{
		actionType: "http", inputs: map[string]any{"url": "http://tpl", "method": "POST"},
	})
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc", Type: StepService, TemplateRef: &ActionTemplateRef{TemplateID: "tpl1"},
			Inputs: map[string]any{"url": "http://override"}},
		{ID: "end", Type: StepEnd, DependsOn: []Dependency{{NodeID: "svc", Condition: "success"}}},
	}}
	inst := newHookInst()
	landing, err := svc.runAutomated(context.Background(), proc, inst, &proc.Steps[0], map[string]any{})
	if err != nil {
		t.Fatalf("自动链执行失败: %v", err)
	}
	if landing == nil || landing.ID != "end" {
		t.Fatalf("应推进到结束节点，实际 %+v", landing)
	}
	if exec.action != "http" || exec.params["url"] != "http://override" || exec.params["method"] != "POST" {
		t.Fatalf("动作未按模板解析+步骤覆盖执行: action=%q params=%v", exec.action, exec.params)
	}
	if _, ok := inst.StepResults["svc"].(map[string]any)["template_ref"]; !ok {
		t.Fatal("步骤结果应记录 template_ref 便于排障")
	}
}

func TestRunAutomatedTemplateResolveFailureIsFailClosed(t *testing.T) {
	svc, _ := newAssetHookTestService(nil)
	exec := &recordingActionExec{}
	svc.SetActionExecutor(exec)
	svc.SetActionTemplateResolver(&stubTemplateResolver{err: errors.New("模板未发布")})
	proc := &Process{ID: "p1", Steps: []Step{
		{ID: "svc", Type: StepService, TemplateRef: &ActionTemplateRef{TemplateID: "tpl1"}},
	}}
	inst := newHookInst()
	if _, err := svc.runAutomated(context.Background(), proc, inst, &proc.Steps[0], map[string]any{}); err != nil {
		t.Fatalf("自动链不应返回硬错误: %v", err)
	}
	if exec.action != "" {
		t.Fatal("模板解析失败时绝不能执行动作")
	}
	rec, _ := inst.StepResults["svc"].(map[string]any)
	if rec == nil || rec["error"] == nil {
		t.Fatalf("应记录失败原因: %v", inst.StepResults["svc"])
	}
}
