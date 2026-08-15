package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/opsorigin/core/pkg/llm"
)

// viewer 无 ai:tool:exec; admin 有。复用 TestExecToolCheckedRBAC 的角色约定。
func viewerCtx() context.Context { return context.WithValue(context.Background(), "role", "viewer") }
func adminCtx() context.Context  { return context.WithValue(context.Background(), "role", "admin") }

// TestToolPolicyPluggable 验证连接器审批策略可插拔(C5): 只读免审、写/执行需权限、未配置回落、热插拔生效。
func TestToolPolicyPluggable(t *testing.T) {
	e := newTestEngine(t)

	// 种子策略已生效
	if pol, ok := e.policyOf("script_run"); !ok || pol != PolicyApproval {
		t.Fatalf("script_run should default to PolicyApproval, got %v/%v", pol, ok)
	}
	if pol, ok := e.policyOf("cmdb_query"); !ok || pol != PolicyAllow {
		t.Fatalf("cmdb_query should default to PolicyAllow, got %v/%v", pol, ok)
	}
	// 未配置且非特权 -> 回落放行(兼容改造前行为)
	if pol, ok := e.policyOf("some_unknown_tool"); ok || pol != PolicyAllow {
		t.Fatalf("unknown tool should fall back to (PolicyAllow, false), got %v/%v", pol, ok)
	}

	// 只读工具: 任意已认证角色免审(viewer 无 ai:tool:exec 也放行)
	if ok, _, msg := e.rbacGate(viewerCtx(), "", "cmdb_query", nil); !ok {
		t.Fatalf("read-only cmdb_query must be allowed for viewer, got deny: %s", msg)
	}

	// 写/执行类: viewer 拒、admin 放
	if ok, _, _ := e.rbacGate(viewerCtx(), "", "script_run", nil); ok {
		t.Fatalf("script_run must be denied for viewer")
	}
	if ok, _, _ := e.rbacGate(adminCtx(), "", "script_run", nil); !ok {
		t.Fatalf("script_run must be allowed for admin")
	}

	// 热插拔: 新增连接器审批策略(参考 Harness 把 policy 做成可配置插件)
	e.SetToolPolicy("provision_vm", PolicyApproval)
	if ok, _, _ := e.rbacGate(viewerCtx(), "", "provision_vm", nil); ok {
		t.Fatalf("provision_vm (Approval) must be denied for viewer")
	}
	if ok, _, _ := e.rbacGate(adminCtx(), "", "provision_vm", nil); !ok {
		t.Fatalf("provision_vm (Approval) must be allowed for admin")
	}
	// 同一连接器切回免审: 策略表热更新立即生效
	e.SetToolPolicy("provision_vm", PolicyAllow)
	if ok, _, msg := e.rbacGate(viewerCtx(), "", "provision_vm", nil); !ok {
		t.Fatalf("provision_vm switched to Allow must be allowed for viewer, got: %s", msg)
	}
}

// TestControlledModeCustomTool 验证受控创作模式(Creator 模式, S3):
// 关闭时 agent 自造工具免审执行; 开启后自造工具执行前需 ai:tool:exec, 平台内置工具不受影响。
func TestControlledModeCustomTool(t *testing.T) {
	e := newTestEngine(t)
	const customName = "tmp_diag_script"

	// 注册一个 agent 自造工具(经引擎内部映射)
	e.mu.Lock()
	e.customTools["c1"] = &CustomTool{Name: customName, Type: "script"}
	e.mu.Unlock()

	if !e.isCustomTool(customName) {
		t.Fatalf("isCustomTool should detect %s", customName)
	}
	if e.isCustomTool("script_run") {
		t.Fatalf("script_run is platform-builtin, must not be treated as custom")
	}

	// 默认受控关闭: 自造工具对 viewer 放行
	if ok, _, msg := e.controlledGate(viewerCtx(), "", customName, nil); !ok {
		t.Fatalf("controlledMode off: custom tool must be allowed for viewer, got: %s", msg)
	}

	// 开启受控模式: 自造工具需权限, viewer 被拒
	e.SetControlledMode(true)
	if ok, _, _ := e.controlledGate(viewerCtx(), "", customName, nil); ok {
		t.Fatalf("controlledMode on: custom tool must be denied for viewer")
	}
	if ok, _, _ := e.controlledGate(adminCtx(), "", customName, nil); !ok {
		t.Fatalf("controlledMode on: custom tool must be allowed for admin")
	}
	// 平台内置高危工具不受受控模式影响(只管自造工具)
	if ok, _, msg := e.controlledGate(viewerCtx(), "", "script_run", nil); !ok {
		t.Fatalf("controlledMode must not gate platform-builtin tools, got: %s", msg)
	}

	// 关闭受控模式恢复放行
	e.SetControlledMode(false)
	if ok, _, msg := e.controlledGate(viewerCtx(), "", customName, nil); !ok {
		t.Fatalf("controlledMode off again: custom tool must be allowed, got: %s", msg)
	}
}

// TestConnectorPolicyHookChain 端到端验证: 经 #404 钩子链 PreToolCall 执行连接器策略,
// 拒绝时 execToolChecked 回填结构化错误而非真执行(复用真实 mock registry)。
func TestConnectorPolicyHookChain(t *testing.T) {
	e := newTestEngine(t)

	deny := func() llm.ToolCall {
		tc := llm.ToolCall{}
		tc.Function.Name = "script_run"
		tc.Function.Arguments = "{}"
		return tc
	}
	// viewer 经钩子链被拒, 且回填错误(不真执行)
	res, execErr := e.execToolChecked(viewerCtx(), deny())
	if execErr {
		t.Fatalf("denied call should not be an exec error: %v", res)
	}
	rm, ok := res.(map[string]any)
	if !ok || rm["error"] == nil || !strings.Contains(fmt.Sprint(rm["error"]), "permission denied") {
		t.Fatalf("viewer script_run should be denied via hook chain, got %#v", res)
	}
	// admin 经钩子链放行, 真执行并返回 mock 结果
	res, _ = e.execToolChecked(adminCtx(), deny())
	rm, ok = res.(map[string]any)
	if !ok || rm["status"] != "ok" {
		t.Fatalf("admin script_run should execute via hook chain, got %#v", res)
	}
}
