package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// newTestEngineWithLLM 构造带 mock LLM(本地 httptest 服务, 始终返回固定回复) 的引擎, 用于验证
// 需要真实 LLM 往返的逻辑(如会话持久化/历史加载)。返回的闭包用于关闭测试服务。
func newTestEngineWithLLM(t *testing.T, capture *[]llm.Message) (*Engine, func()) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if capture != nil {
			*capture = body.Messages
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "ok"}},
			},
		})
	}))
	client := llm.NewClient(llm.Config{
		BaseURL: ts.URL, APIKey: "test", Model: "mock", Timeout: 5 * time.Second,
	})
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
	e := NewEngine(reg, ratelimit.New(), client, nil)
	return e, ts.Close
}

// TestConversationPersistence 验证单智能体会话持久化与跨轮记忆(P2):
//  1. 首轮带 conversation_id 调用后, 会话落库(含 user+assistant);
//  2. 第二轮传同一 conversation_id, 模型收到的上下文包含首轮历史(记忆生效),
//     且返回/持久化的 conversation_id 保持一致。
func TestConversationPersistence(t *testing.T) {
	var captured []llm.Message
	e, closeFn := newTestEngineWithLLM(t, &captured)
	defer closeFn()

	ctx := context.Background()
	const convID = "conv-p2-test"

	out, gotConvID, err := e.Chat(ctx, "agent1", "你好", nil, convID)
	if err != nil {
		t.Fatalf("chat1: %v", err)
	}
	if out != "ok" {
		t.Errorf("chat1 content = %q, want ok", out)
	}
	if gotConvID != convID {
		t.Errorf("chat1 convID = %q, want %q", gotConvID, convID)
	}

	// 落库校验
	conv, err := e.GetConversation(convID)
	if err != nil {
		t.Fatalf("get conv: %v", err)
	}
	if len(conv.Messages) < 2 {
		t.Fatalf("expected >=2 persisted messages, got %d: %#v", len(conv.Messages), conv.Messages)
	}
	if conv.Messages[0].Role != "user" || conv.Messages[0].Content != "你好" {
		t.Errorf("first persisted message wrong: %#v", conv.Messages[0])
	}

	// 第二轮: 同一会话, 模型上下文应含首轮历史
	out2, gotConvID2, err := e.Chat(ctx, "agent1", "再问一次", nil, convID)
	if err != nil {
		t.Fatalf("chat2: %v", err)
	}
	if out2 != "ok" {
		t.Errorf("chat2 content = %q, want ok", out2)
	}
	if gotConvID2 != convID {
		t.Errorf("chat2 convID = %q, want %q", gotConvID2, convID)
	}
	// 捕获到的第二轮请求至少含: system + 首轮user + 首轮assistant + 本轮user
	if len(captured) < 4 {
		t.Fatalf("2nd-round context too short, got %d msgs: %#v", len(captured), captured)
	}
	found := false
	for _, m := range captured {
		if m.Role == "user" && m.Content == "你好" {
			found = true
		}
	}
	if !found {
		t.Errorf("1st-round user message not present in 2nd-round context: %#v", captured)
	}

	// 持久化应累积到 4 条(user/assistant/user/assistant)
	conv2, _ := e.GetConversation(convID)
	if len(conv2.Messages) != 4 {
		t.Errorf("expected 4 persisted messages after 2 rounds, got %d: %#v", len(conv2.Messages), conv2.Messages)
	}
}

// TestConversationNotPersistedWithoutID 验证未提供 conversation_id 时不落库(向后兼容, 无副作用)。
func TestConversationNotPersistedWithoutID(t *testing.T) {
	var captured []llm.Message
	e, closeFn := newTestEngineWithLLM(t, &captured)
	defer closeFn()

	out, convID, err := e.Chat(context.Background(), "agent1", "一次性提问", nil, "")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if out != "ok" {
		t.Errorf("content = %q", out)
	}
	if convID != "" {
		t.Errorf("expected empty convID when not requested, got %q", convID)
	}
	// 内存存储中不应出现任何会话
	e.mu.RLock()
	n := len(e.convs)
	e.mu.RUnlock()
	if n != 0 {
		t.Errorf("expected no persisted conversation, got %d", n)
	}
}
