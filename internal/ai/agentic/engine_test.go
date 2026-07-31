package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/llm"
)

func TestParseCoT(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantThink string
		wantAns   string
	}{
		{
			name:      "full tags",
			input:     "<think>先推理一下</think><answer>最终答案</answer>",
			wantThink: "先推理一下",
			wantAns:   "最终答案",
		},
		{
			name:      "no answer tag falls back to tail",
			input:     "<think>只在思考</think>剩下的就是答案",
			wantThink: "只在思考",
			wantAns:   "剩下的就是答案",
		},
		{
			name:      "no tags returns whole as answer",
			input:     "没有任何标签的内容",
			wantThink: "",
			wantAns:   "没有任何标签的内容",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			think, ans := parseCoT(c.input)
			if strings.TrimSpace(think) != c.wantThink {
				t.Errorf("thinking: got %q want %q", think, c.wantThink)
			}
			if strings.TrimSpace(ans) != c.wantAns {
				t.Errorf("answer: got %q want %q", ans, c.wantAns)
			}
		})
	}
}

func TestTrunc(t *testing.T) {
	if got := trunc("hello世界", 5); got != "hello" {
		t.Errorf("trunc short: got %q", got)
	}
	if got := trunc("hi", 10); got != "hi" {
		t.Errorf("trunc no-op: got %q", got)
	}
}

func TestParseNumbered(t *testing.T) {
	in := "1. [小明] 负责网络\n2、[小红] 负责存储\n3: [小刚] 负责计算\n随便一句废话\n4. 无成员名的子任务"
	got := parseNumbered(in)
	// 仅编号行被解析(保留 [成员名] 前缀,函数只剥离编号标记); 非编号行被忽略。
	if len(got) != 4 {
		t.Fatalf("expected 4 items, got %d: %#v", len(got), got)
	}
	want := []string{"[小明] 负责网络", "[小红] 负责存储", "[小刚] 负责计算", "无成员名的子任务"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestWithPersonaPrefix(t *testing.T) {
	got := withPersonaPrefix("严谨的架构师")
	if !strings.Contains(got, "严谨的架构师") {
		t.Errorf("persona not included: %q", got)
	}
}

// newTestEngine 构造带 script_run(写/执行类) 与 knowledge_search(只读) 两个工具的最小引擎, 用于校验 execToolChecked 的 RBAC/截断行为。
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDefinition{
		Name: "script_run",
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			return map[string]any{"status": "ok"}, nil
		},
	})
	reg.Register(mcp.ToolDefinition{
		Name: "knowledge_search",
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			return map[string]any{"count": 0}, nil
		},
	})
	return NewEngine(reg, ratelimit.New(), nil, nil)
}

// TestExecToolCheckedRBAC 验证 Function Calling 循环对高危工具的 RBAC 约束(P3-0):
// 低权限角色即便通过自然语言驱动 AI, 也无法越权执行 script_run; 只读工具则对任何已认证角色开放。
func TestExecToolCheckedRBAC(t *testing.T) {
	e := newTestEngine(t)

	scriptCall := func() llm.ToolCall {
		tc := llm.ToolCall{}
		tc.Function.Name = "script_run"
		tc.Function.Arguments = "{}"
		return tc
	}
	searchCall := func() llm.ToolCall {
		tc := llm.ToolCall{}
		tc.Function.Name = "knowledge_search"
		tc.Function.Arguments = "{}"
		return tc
	}

	// viewer 无 ai:tool:exec -> 写/执行类工具被拒
	viewerCtx := context.WithValue(context.Background(), "role", "viewer")
	res, execErr := e.execToolChecked(viewerCtx, scriptCall())
	if execErr {
		t.Fatalf("unexpected exec error for denied call: %v", res)
	}
	rm, ok := res.(map[string]any)
	if !ok || rm["error"] == nil {
		t.Fatalf("viewer should be denied script_run, got %#v", res)
	}
	if !strings.Contains(fmt.Sprint(rm["error"]), "permission denied") {
		t.Errorf("expected permission denied, got %v", rm["error"])
	}

	// admin 有 ai:tool:exec -> 允许执行
	adminCtx := context.WithValue(context.Background(), "role", "admin")
	res, _ = e.execToolChecked(adminCtx, scriptCall())
	rm, ok = res.(map[string]any)
	if !ok || rm["status"] != "ok" {
		t.Fatalf("admin should execute script_run, got %#v", res)
	}

	// 只读工具: 任意已认证角色均可(无需 ai:tool:exec)
	res, _ = e.execToolChecked(viewerCtx, searchCall())
	rm, ok = res.(map[string]any)
	if !ok || rm["count"] == nil {
		t.Fatalf("viewer should use read-only tool, got %#v", res)
	}

	// 无角色上下文 -> 视为未认证, 拒绝高危工具
	res, _ = e.execToolChecked(context.Background(), scriptCall())
	rm, ok = res.(map[string]any)
	if !ok || rm["error"] == nil {
		t.Fatalf("missing role should deny script_run, got %#v", res)
	}

	_ = auth.PermissionAIToolExec // 确保依赖被引用, 避免误改常量名
}

// TestTruncateToolResult 验证工具结果长度上限截断(P3-2)。
func TestTruncateToolResult(t *testing.T) {
	e := newTestEngine(t)
	big := string(make([]byte, maxToolResultLen+100))
	res := e.truncateToolResult(map[string]any{"log": big})
	rm, ok := res.(map[string]any)
	if !ok || rm["_truncated"] != true {
		t.Fatalf("expected truncation, got %#v", res)
	}
	// 小结果不截断
	small := e.truncateToolResult(map[string]any{"x": 1})
	if _, truncated := small.(map[string]any)["_truncated"]; truncated {
		t.Errorf("small result should not be truncated")
	}
}
