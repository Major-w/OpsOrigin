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

// TestSetDefaultSystemPrompt 验证提示词外置化: 引擎默认回退到内置常量,
// SetDefaultSystemPrompt 可覆盖(供 sysconfig 热更新/env 注入), 空串回退到内置常量而非置空。
func TestSetDefaultSystemPrompt(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)

	if got := e.resolveSystemPrompt(""); got != DefaultSystemPrompt {
		t.Fatalf("default prompt mismatch:\n got=%q\nwant=%q", got, DefaultSystemPrompt)
	}

	const overridden = "你是外部定义的运维助手,用于回归测试。"
	e.SetDefaultSystemPrompt(overridden)
	if got := e.resolveSystemPrompt(""); got != overridden {
		t.Fatalf("overridden prompt mismatch: got=%q want=%q", got, overridden)
	}

	// 空串应回退到内置常量(不置空)。
	e.SetDefaultSystemPrompt("")
	if got := e.resolveSystemPrompt(""); got != DefaultSystemPrompt {
		t.Fatalf("empty override should fall back to builtin constant, got=%q", got)
	}
}

// TestExtractCitations 验证从模型回答中解析 [^<id>] 引用标注(去重)。
func TestExtractCitations(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "依据文章[^a1b2c3]可知故障原因。", []string{"a1b2c3"}},
		{"multi and dedup", "见[^x][^y][^x]两篇。", []string{"x", "y"}},
		{"with refs section", "结论[^id1]。\n\n## 引用来源\n- [id1] 标题", []string{"id1"}},
		{"none", "没有任何引用。", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractCitations(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len mismatch: got %#v want %#v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("idx %d: got %q want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestBuildMessagesStripsCitations 验证历史中的助手消息下发给模型前会剥离 [^id] 引用标注,
// 防止模型把旧引用抄进新答案(引用来源应是回答级而非对话级)。
func TestBuildMessagesStripsCitations(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
	history := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "之前参考[^kubectl-ref]和[^disk-ref]回答过。"},
	}
	msgs, _ := e.buildMessages(context.Background(), "", "新问", history, "", nil)
	if len(msgs) != 4 {
		t.Fatalf("expected system+history+user=4 msgs, got %d", len(msgs))
	}
	got := msgs[2].Content
	want := "之前参考和回答过。"
	if got != want {
		t.Errorf("assistant history citation not stripped: got %q want %q", got, want)
	}
}

// TestBuildMessagesIncludesAnswerHygiene 守卫: system prompt 必须包含输出纪律(answerHygiene),
// 抑制推理模型把思维链收尾(回答计划/自我评估)续写进正文造成"思考与答案混在一起"。
// 若移除注入点(buildMessages 中的 answerHygiene 拼接), 本测试失败。
func TestBuildMessagesIncludesAnswerHygiene(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
	msgs, _ := e.buildMessages(context.Background(), "", "你好", nil, "", nil)
	if len(msgs) == 0 || msgs[0].Role != "system" {
		t.Fatalf("expected system message first, got %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "【输出纪律】") {
		t.Errorf("system prompt missing answerHygiene guidance")
	}
	// 自主模式豁免条款必须同时存在, 避免 <plan>/<step> 结构化标注被输出纪律压制。
	if !strings.Contains(msgs[0].Content, "<plan>/<step>") {
		t.Errorf("answerHygiene missing structured-tag exemption")
	}
}

// TestFilterEmptyAssistant 验证无正文/无工具调用/无推理的助手消息不会落库。
func TestFilterEmptyAssistant(t *testing.T) {
	in := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "  ", ReasoningContent: "  "},
		{Role: "assistant", Content: "ok"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "1"}}},
	}
	out := filterEmptyAssistant(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages (user+ok+toolcall), got %d: %+v", len(out), out)
	}
	for _, m := range out {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			t.Fatalf("empty assistant message should be filtered: %+v", m)
		}
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

	out, gotConvID, err := e.Chat(ctx, "agent1", "你好", nil, convID, "", false)
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
	out2, gotConvID2, err := e.Chat(ctx, "agent1", "再问一次", nil, convID, "", false)
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

	out, convID, err := e.Chat(context.Background(), "agent1", "一次性提问", nil, "", "", false)
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

// makeHistory 构造 n 条交替 user/assistant 的历史消息, 供压缩测试使用。
func makeHistory(n int) []llm.Message {
	msgs := make([]llm.Message, 0, n)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, llm.Message{Role: role, Content: fmt.Sprintf("msg-%d", i)})
	}
	return msgs
}

// TestCompactHistoryNoOpShort 验证未超阈值时不压缩。
func TestCompactHistoryNoOpShort(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
	// historyKeepRecent=16, 构造 16 条(等于阈值, 不触发)。
	h := makeHistory(historyKeepRecent)
	got := e.compactHistory(context.Background(), h, "", 0)
	compacted := got.history
	if len(compacted) != historyKeepRecent {
		t.Fatalf("expected %d unchanged, got %d", historyKeepRecent, len(compacted))
	}
}

// TestCompactHistoryDeterministicFallback 验证无摘要器时回退到"保留首条 user 原始意图 + 最近窗口"。
func TestCompactHistoryDeterministicFallback(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil) // historySummarizer=nil
	const total = 25
	h := makeHistory(total)
	got := e.compactHistory(context.Background(), h, "", 0)
	compacted := got.history
	// 1(首条 user) + 最近 16 = 17
	if len(compacted) != historyKeepRecent+1 {
		t.Fatalf("expected %d, got %d", historyKeepRecent+1, len(compacted))
	}
	// 首条必须是原始意图锚点(history[0] = msg-0, user)。
	if compacted[0].Role != "user" || compacted[0].Content != "msg-0" {
		t.Fatalf("anchor not preserved: %#v", compacted[0])
	}
	// 末尾必须是原始历史的最后一条。
	if compacted[len(compacted)-1].Content != fmt.Sprintf("msg-%d", total-1) {
		t.Fatalf("recent tail not preserved: %#v", compacted[len(compacted)-1])
	}
}

// TestCompactHistorySummarizer 验证配置摘要器时用其生成摘要。
func TestCompactHistorySummarizer(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
	e.SetHistorySummarizer(func(ctx context.Context, msgs []llm.Message) (string, error) {
		return "SUMMARY_OF_PAST", nil
	})
	got := e.compactHistory(context.Background(), makeHistory(25), "", 0)
	compacted := got.history
	if len(compacted) != historyKeepRecent+1 {
		t.Fatalf("expected %d, got %d", historyKeepRecent+1, len(compacted))
	}
	if compacted[0].Role != "system" || !strings.Contains(compacted[0].Content, "SUMMARY_OF_PAST") {
		t.Fatalf("summary not injected: %#v", compacted[0])
	}
}

// TestCompactHistorySummarizerFallbackOnError 验证摘要器失败时回退到确定性压缩。
func TestCompactHistorySummarizerFallbackOnError(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
	e.SetHistorySummarizer(func(ctx context.Context, msgs []llm.Message) (string, error) {
		return "", fmt.Errorf("boom")
	})
	got := e.compactHistory(context.Background(), makeHistory(25), "", 0)
	compacted := got.history
	if len(compacted) != historyKeepRecent+1 {
		t.Fatalf("expected %d, got %d", historyKeepRecent+1, len(compacted))
	}
	if compacted[0].Role != "user" || compacted[0].Content != "msg-0" {
		t.Fatalf("should fall back to anchor, got %#v", compacted[0])
	}
}

// TestCompactHistoryTokenBudgetTrigger 验证条数未超窗口但估算 token 超预算时也触发压缩,
// 且压缩后(摘要 + 最近窗口)落回预算内(含硬裁剪)。
func TestCompactHistoryTokenBudgetTrigger(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
	e.SetHistorySummarizer(func(ctx context.Context, msgs []llm.Message) (string, error) {
		return "TOK_SUMMARY", nil
	})
	// 10 条中文长消息: 条数 ≤16 不触发条数压缩, 但 10×3000 汉字 ≈ 30000 token > 12000 预算。
	h := make([]llm.Message, 0, 10)
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		h = append(h, llm.Message{Role: role, Content: strings.Repeat("运", 3000)})
	}
	got := e.compactHistory(context.Background(), h, "", 0)
	if est := estimateTokens(got.history); est > historyTokenBudget {
		t.Fatalf("compacted history still over budget: %d > %d", est, historyTokenBudget)
	}
	if !got.changed || got.summary != "TOK_SUMMARY" || got.covered == 0 {
		t.Fatalf("unexpected compaction result: changed=%v summary=%q covered=%d", got.changed, got.summary, got.covered)
	}
}

// TestCompactHistoryRollingReuse 验证溢出部分已被旧摘要覆盖时直接复用摘要, 不重调摘要器(跨轮复用)。
func TestCompactHistoryRollingReuse(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
	calls := 0
	e.SetHistorySummarizer(func(ctx context.Context, msgs []llm.Message) (string, error) {
		calls++
		return "ROLL_SUM", nil
	})
	h := makeHistory(25)
	first := e.compactHistory(context.Background(), h, "", 0)
	if calls != 1 || first.summary != "ROLL_SUM" || !first.changed {
		t.Fatalf("first compaction unexpected: calls=%d %+v", calls, first)
	}
	// 同一历史 + 已持久化的摘要状态 → 复用, 不再调用摘要器。
	second := e.compactHistory(context.Background(), h, first.summary, first.covered)
	if calls != 1 {
		t.Fatalf("summary should be reused without new LLM call, calls=%d", calls)
	}
	if second.summary != first.summary || second.changed {
		t.Fatalf("reuse should keep summary unchanged: %+v", second)
	}
}

// TestEstimateStrTokens 验证中英混排的粗估口径: CJK≈1 token/字, ASCII≈4 字符/token。
func TestEstimateStrTokens(t *testing.T) {
	if got := estimateStrTokens("运维监控"); got != 4 {
		t.Fatalf("CJK estimate: want 4, got %d", got)
	}
	if got := estimateStrTokens("abcdefgh"); got != 2 {
		t.Fatalf("ASCII estimate: want 2, got %d", got)
	}
}

// TestExecToolCheckedCacheHit 验证幂等/只读工具的结果被缓存: 相同 (name+tenant+args) 的第二次
// 调用直接命中缓存, 不再触达底层 Handler。
func TestExecToolCheckedCacheHit(t *testing.T) {
	reg := mcp.NewRegistry()
	var calls int
	reg.Register(mcp.ToolDefinition{
		Name: "cacheable_echo",
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			calls++
			return map[string]any{"echo": in["q"]}, nil
		},
	})
	e := NewEngine(reg, ratelimit.New(), nil, nil)
	e.lim = nil // 关闭限流, 聚焦缓存行为
	ctx := context.Background()
	tc := llm.ToolCall{}
	tc.Function.Name = "cacheable_echo"
	tc.Function.Arguments = `{"q":"hello"}`

	r1, _ := e.execToolChecked(ctx, tc)
	r2, _ := e.execToolChecked(ctx, tc)
	if calls != 1 {
		t.Fatalf("handler should be called exactly once (2nd served from cache), got %d", calls)
	}
	if r1.(map[string]any)["echo"] != "hello" || r2.(map[string]any)["echo"] != "hello" {
		t.Fatalf("unexpected cached results: %v / %v", r1, r2)
	}
}

// TestExecToolCheckedNoCacheForSideEffects 验证 noCacheTools 中的工具不被结果缓存,
// 每次都真实触达 Handler, 避免"缓存一次结果"导致后续调用跳过真实执行。
func TestExecToolCheckedNoCacheForSideEffects(t *testing.T) {
	reg := mcp.NewRegistry()
	var calls int
	const toolName = "nocache_dummy"
	reg.Register(mcp.ToolDefinition{
		Name: toolName,
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			calls++
			return map[string]any{"ok": true}, nil
		},
	})
	// 注册一个测试专用工具名到 noCacheTools(避免与内建 http_get 撞名触发真实网络调用)。
	noCacheTools[toolName] = true
	defer delete(noCacheTools, toolName)

	e := NewEngine(reg, ratelimit.New(), nil, nil)
	e.lim = nil
	ctx := context.Background()
	tc := llm.ToolCall{}
	tc.Function.Name = toolName
	tc.Function.Arguments = `{"x":"1"}`

	e.execToolChecked(ctx, tc)
	e.execToolChecked(ctx, tc)
	if calls != 2 {
		t.Fatalf("noCacheTools entry must not be cached; expected 2 handler calls, got %d", calls)
	}
}
