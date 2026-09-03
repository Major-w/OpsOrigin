package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/opsorigin/core/pkg/llm"
)

func llmToolCall(name, args string) llm.ToolCall {
	tc := llm.ToolCall{}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

func TestHookChainPreToolCallOrderAndOverride(t *testing.T) {
	c := NewHookChain()
	var order []string
	c.Add(Hook{PreToolCall: func(_ context.Context, _ string, name string, args map[string]any) (bool, map[string]any, string) {
		order = append(order, "first:"+name)
		return true, args, ""
	}})
	c.Add(Hook{PreToolCall: func(_ context.Context, _ string, name string, _ map[string]any) (bool, map[string]any, string) {
		order = append(order, "second:"+name)
		return true, map[string]any{"x": 1}, ""
	}})
	allow, out, msg := c.RunPreToolCall(context.Background(), "a1", "toolA", map[string]any{"y": 2})
	if !allow || msg != "" {
		t.Fatalf("expected allow, got %v msg %q", allow, msg)
	}
	if len(order) != 2 || order[0] != "first:toolA" || order[1] != "second:toolA" {
		t.Fatalf("order wrong: %#v", order)
	}
	if v, ok := out["x"]; !ok || v != 1 {
		t.Fatalf("args should be overridden by second hook: %#v", out)
	}
	if _, ok := out["y"]; ok {
		t.Fatalf("old args should be replaced by override: %#v", out)
	}
}

func TestHookChainPreToolCallDenies(t *testing.T) {
	c := NewHookChain()
	c.Add(Hook{PreToolCall: func(_ context.Context, _ string, name string, _ map[string]any) (bool, map[string]any, string) {
		if name == "deny_me" {
			return false, nil, "blocked by policy"
		}
		return true, nil, ""
	}})
	allow, _, msg := c.RunPreToolCall(context.Background(), "a1", "deny_me", nil)
	if allow || !strings.Contains(msg, "blocked by policy") {
		t.Fatalf("expected deny, got allow=%v msg=%q", allow, msg)
	}
	if allow2, _, _ := c.RunPreToolCall(context.Background(), "a1", "ok", nil); !allow2 {
		t.Fatalf("ok should pass")
	}
}

// TestEngineHookChainWiredIntoExec 验证钩子链确实接入 execToolChecked:
// 注册一个拒绝 knowledge_search(只读, RBAC 本允许)的钩子应短路; script_run 经钩子放行后仍需过 RBAC。
func TestEngineHookChainWiredIntoExec(t *testing.T) {
	e := newTestEngine(t)
	e.RegisterHook(Hook{PreToolCall: func(_ context.Context, _ string, name string, _ map[string]any) (bool, map[string]any, string) {
		if name == "knowledge_search" {
			return false, nil, "blocked by test hook"
		}
		return true, nil, ""
	}})
	ctx := context.WithValue(context.Background(), "role", "admin")
	res, _ := e.execToolChecked(ctx, llmToolCall("knowledge_search", "{}"))
	rm, ok := res.(map[string]any)
	if !ok || rm["error"] == nil || !strings.Contains(fmt.Sprint(rm["error"]), "blocked by test hook") {
		t.Fatalf("hook should have denied knowledge_search, got %#v", res)
	}
	// script_run: 测试钩子放行 -> 内置 rbacGate(admin)也放行 -> 执行成功
	res2, _ := e.execToolChecked(ctx, llmToolCall("script_run", "{}"))
	rm2, ok := res2.(map[string]any)
	if !ok || rm2["status"] != "ok" {
		t.Fatalf("admin+script_run should pass hook+RBAC, got %#v", res2)
	}
}

func TestEnginePostToolHookFires(t *testing.T) {
	e := newTestEngine(t)
	var fired []string
	e.RegisterHook(Hook{PostTool: func(_ context.Context, _ string, name string, _ any, _ bool) {
		fired = append(fired, name)
	}})
	ctx := context.WithValue(context.Background(), "role", "admin")
	e.execToolChecked(ctx, llmToolCall("script_run", "{}"))
	if len(fired) != 1 || fired[0] != "script_run" {
		t.Fatalf("post-tool hook should fire once, got %#v", fired)
	}
}

// TestEngineTrajectoryRecorded 端到端验证 ChatWithTools 会记录 append-only 轨迹。
func TestEngineTrajectoryRecorded(t *testing.T) {
	var captured []llm.Message
	e, closeFn := newTestEngineWithLLM(t, &captured)
	defer closeFn()
	rec := NewMemTrajectoryRecorder()
	e.SetTrajectoryRecorder(rec)

	convID := "traj-conv-1"
	out, _, retConv, err := e.ChatWithTools(context.Background(), "agent-x", "hello", nil, false, convID, "", false)
	if err != nil {
		t.Fatalf("ChatWithTools err: %v", err)
	}
	if out != "ok" {
		t.Fatalf("expected 'ok', got %q", out)
	}
	if retConv != convID {
		t.Fatalf("conversation id mismatch: %q", retConv)
	}
	turns, err := rec.GetTurns(context.Background(), convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	turn := turns[0]
	if turn.AgentID != "agent-x" || turn.Prompt != "hello" {
		t.Errorf("turn meta: %#v", turn)
	}
	if len(turn.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(turn.Steps))
	}
	step := turn.Steps[0]
	if !step.Finished || step.Assistant != "ok" {
		t.Errorf("step: %#v", step)
	}
}
