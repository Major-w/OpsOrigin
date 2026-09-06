package agentic

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/pkg/llm"
)

// newScopeTestEngine 构造一个最小可用的引擎: 注册两个工具(one 有 scope、two 无 scope),
// 并注入可调用的 handler 以便 execToolChecked 走到 scope 护栏。
func newScopeTestEngine(t *testing.T) *Engine {
	t.Helper()
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDefinition{
		Name:        "scoped-tool",
		Description: "scoped connector",
		Parameters:  map[string]mcp.ParamSchema{},
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})
	reg.Register(mcp.ToolDefinition{
		Name:        "open-tool",
		Description: "open connector",
		Parameters:  map[string]mcp.ParamSchema{},
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})
	e := NewEngine(reg, nil, nil, nil)
	e.SetToolScope("scoped-tool", []string{"agentA"})
	return e
}

// TestBuildToolDefsScopeVisibility 验证 buildToolDefs 按 agent 过滤连接器可见性。
func TestBuildToolDefsScopeVisibility(t *testing.T) {
	e := newScopeTestEngine(t)

	// agentA 能看到 scoped-tool 与 open-tool
	defsA := e.buildToolDefs("agentA")
	if !hasTool(defsA, "scoped-tool") || !hasTool(defsA, "open-tool") {
		t.Fatalf("agentA 应看到 scoped-tool 与 open-tool, got %v", toolNames(defsA))
	}

	// agentB 看不到 scoped-tool, 但仍能看到 open-tool
	defsB := e.buildToolDefs("agentB")
	if hasTool(defsB, "scoped-tool") {
		t.Fatalf("agentB 不应看到 scoped-tool, got %v", toolNames(defsB))
	}
	if !hasTool(defsB, "open-tool") {
		t.Fatalf("agentB 应看到 open-tool, got %v", toolNames(defsB))
	}

	// 未绑定 agent(空串 = 默认助手)不暴露任何工具(产品意图: 默认助手不关联工具, 仅纯对话/推理)
	defsNone := e.buildToolDefs("")
	if len(defsNone) != 0 {
		t.Fatalf("未绑定 agent(默认助手) 不应暴露任何工具, got %v", toolNames(defsNone))
	}
}

// TestExecToolCheckedScopeGuard 验证运行时 scope 护栏: 越权 agent 即便拿到工具名也无法执行。
func TestExecToolCheckedScopeGuard(t *testing.T) {
	e := newScopeTestEngine(t)

	// agentA 可正常执行 scoped-tool(在 scope 内, 不应被拒绝)
	ctxA := withAgentID(context.Background(), "agentA")
	resA, failedA := e.execToolChecked(ctxA, toolCall("scoped-tool", nil))
	if failedA {
		t.Fatalf("agentA 执行 scoped-tool 不应发生执行错误, got %v", resA)
	}
	if errMap, ok := resA.(map[string]any); ok && errMap["error"] != nil {
		t.Fatalf("agentA 在 scope 内不应被拒绝, got %v", resA)
	}

	// agentB 被 scope 护栏拒绝(返回结构化 error 结果, 但不记为"工具执行错误"——与 RBAC 闸门语义一致)。
	ctxB := withAgentID(context.Background(), "agentB")
	res, failed := e.execToolChecked(ctxB, toolCall("scoped-tool", nil))
	if failed {
		t.Fatalf("scope 拒绝不应记为工具执行错误(failed 应为 false)")
	}
	if errMap, ok := res.(map[string]any); !ok || errMap["error"] == nil {
		t.Fatalf("scope 拒绝应回填 error 结果, got %v", res)
	}

	// open-tool 对任意 agent 均可见可执行
	if _, failed := e.execToolChecked(ctxB, toolCall("open-tool", nil)); failed {
		t.Fatalf("open-tool 对所有 agent 应可执行")
	}
}

// TestSetToolScopeOverrideClearsAutoSeed 验证人工覆盖 SetToolScope 会清除自动播种标记,
// 使后续反注册不会误删该覆盖。
func TestSetToolScopeOverrideClearsAutoSeed(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), nil, nil, nil)
	// 模拟一个经生命周期自动播种的 scope(来自 CustomTool.AllowedAgents)
	e.autoSeededScopes["conn-x"] = true
	e.toolScopes["conn-x"] = []string{"agentA"}

	// 人工覆盖: 扩大到 agentA + agentB, 并应清除 autoSeeded 标记
	e.SetToolScope("conn-x", []string{"agentA", "agentB"})
	e.mu.RLock()
	scope := e.toolScopes["conn-x"]
	_, stillAuto := e.autoSeededScopes["conn-x"]
	e.mu.RUnlock()
	if len(scope) != 2 || stillAuto {
		t.Fatalf("人工覆盖后 scope=%v autoSeeded=%v, 期望 scope=[agentA agentB] autoSeeded=false", scope, stillAuto)
	}

	// 清空 scope 取消隔离
	e.SetToolScope("conn-x", nil)
	if sc := e.GetToolScope("conn-x"); len(sc) != 0 {
		t.Fatalf("清空后 GetToolScope 应返回空, got %v", sc)
	}
}

// TestScopeSeedFromCustomToolLifecycle 验证经自定义工具生命周期自动播种 scope,
// 且反注册时仅清理自动播种项。
func TestScopeSeedFromCustomToolLifecycle(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), nil, nil, nil)
	tool := &CustomTool{
		ID:            "c1",
		TenantID:      "default",
		Name:          "billing-api",
		Type:          "http",
		HTTPMethod:    "GET",
		HTTPURL:       "https://example.com/billing",
		Enabled:       true,
		AllowedAgents: []string{"fin-agent"},
	}
	if err := e.CreateCustomTool(context.Background(), tool); err != nil {
		t.Fatalf("CreateCustomTool failed: %v", err)
	}
	if sc := e.GetToolScope("billing-api"); len(sc) != 1 || sc[0] != "fin-agent" {
		t.Fatalf("注册后应自动播种 scope=[fin-agent], got %v", sc)
	}

	// 反注册应清理自动播种的 scope
	if err := e.DeleteCustomTool(context.Background(), "c1", "default"); err != nil {
		t.Fatalf("DeleteCustomTool failed: %v", err)
	}
	if sc := e.GetToolScope("billing-api"); len(sc) != 0 {
		t.Fatalf("反注册后 scope 应被清理, got %v", sc)
	}
}

// TestTeamMemberScopeIsolation 验证团队编排时成员按自身 agentID 受 scope 约束:
// 借用 buildToolDefs(memberAgentID) 的语义(团队 runOneMemberRound 已改为按成员 agentID 取工具)。
func TestTeamMemberScopeIsolation(t *testing.T) {
	e := newScopeTestEngine(t)
	// 团队某成员绑定 agentB, 其可见工具集不应含 scoped-tool
	memberDefs := e.buildToolDefs("agentB")
	if hasTool(memberDefs, "scoped-tool") {
		t.Fatalf("成员(agentB) 不应在工具集中看到 scoped-tool")
	}
	// 成员绑定 agentA, 应看到
	memberDefsA := e.buildToolDefs("agentA")
	if !hasTool(memberDefsA, "scoped-tool") {
		t.Fatalf("成员(agentA) 应在工具集中看到 scoped-tool")
	}
}

// ——— 测试辅助 ———

func toolCall(name string, args map[string]any) llm.ToolCall {
	tc := llm.ToolCall{}
	tc.Function.Name = name
	if args != nil {
		if b, err := json.Marshal(args); err == nil {
			tc.Function.Arguments = string(b)
		}
	}
	return tc
}

func hasTool(defs []llm.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Function.Name == name {
			return true
		}
	}
	return false
}

func toolNames(defs []llm.ToolDef) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Function.Name)
	}
	return names
}
