package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/pkg/llm"
)

// buildToolCallResponse 构造一个 OpenAI 兼容的 chat 完成响应, 其 message 始终请求
// 一次 echo 工具调用(参数由 argsJSON 指定, 调用 ID 由 callID 指定)。
// 用 json.Marshal 生成, 避免手写 JSON 的转义歧义。
func buildToolCallResponse(callID, argsJSON string) []byte {
	resp := map[string]any{
		"id":      "fake",
		"object":  "chat.completion",
		"created": 1,
		"model":   "fake",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{
						{
							"id":   callID,
							"type": "function",
							"function": map[string]any{
								"name":      "echo",
								"arguments": argsJSON,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// buildToolCallResponseNamed 与 buildToolCallResponse 相同, 但允许指定工具名,
// 用于让"单工具连击检测"不会误伤(交替工具名可绕过该检测, 从而单独验证 maxToolRounds 降级路径)。
func buildToolCallResponseNamed(callID, name, argsJSON string) []byte {
	resp := map[string]any{
		"id":      "fake",
		"object":  "chat.completion",
		"created": 1,
		"model":   "fake",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{
						{
							"id":   callID,
							"type": "function",
							"function": map[string]any{
								"name":      name,
								"arguments": argsJSON,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// buildTextResponse 构造一个纯文本(无工具调用)的 OpenAI 兼容 chat 完成响应, 用于模拟
// "模型在收到 nudge 后基于自身知识产出最终答案"的轮次。
func buildTextResponse(text string) []byte {
	resp := map[string]any{
		"id":      "fake",
		"object":  "chat.completion",
		"created": 1,
		"model":   "fake",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": text,
				},
				"finish_reason": "stop",
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// 用于复现"推理模型反复发起相同工具调用"的死循环场景。
func fakeRepeatingToolServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildToolCallResponse("call_1", `{"q":"x"}`))
	}))
}

// fakeDistinctToolServer 每轮返回参数不同的工具调用, 且工具名在 echo/echo2 间交替,
// 使重复签名检测与"单工具连击检测"均无法命中, 用以验证"超过工具轮次上限后优雅降级而非 502 硬失败"。
func fakeDistinctToolServer() *httptest.Server {
	var n int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		round := atomic.AddInt64(&n, 1)
		argsJSON := fmt.Sprintf(`{"round":%d}`, round)
		callID := fmt.Sprintf("call_%d", round)
		name := "echo"
		if round%2 == 0 {
			name = "echo2"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildToolCallResponseNamed(callID, name, argsJSON))
	}))
}

// TestChatToolCallLoopDedup 回归测试: 模型反复发起完全相同的工具调用时,
// 引擎应在第二轮检测到重复签名并优雅终止, 不返回硬错误、不耗尽轮次、不触发 502。
func TestChatToolCallLoopDedup(t *testing.T) {
	srv := fakeRepeatingToolServer()
	defer srv.Close()

	var execCount int64
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDefinition{
		Name:        "echo",
		Description: "测试用回显工具",
		Parameters: map[string]mcp.ParamSchema{
			"q": {Type: "string", Description: "输入", Required: true},
		},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			atomic.AddInt64(&execCount, 1)
			return map[string]any{"echo": input["q"]}, nil
		},
	})

	client := llm.NewClient(llm.Config{BaseURL: srv.URL})
	e := NewEngine(reg, nil, client, nil)

	content, _, err := e.Chat(context.Background(), "", "请帮我查一下 echo", nil, "", "", false)
	if err != nil {
		t.Fatalf("expected no error (no 502), got: %v", err)
	}
	if !strings.Contains(content, "重复调用相同工具") {
		t.Fatalf("expected graceful dedup message, got content=%q", content)
	}
	// 重复签名检测在第二轮命中, 工具只应被执行一次(第二轮 break 在执行之前)。
	if got := atomic.LoadInt64(&execCount); got != 1 {
		t.Fatalf("expected echo executed exactly once (early break), got %d", got)
	}
}

// TestChatToolCallLoopMaxRounds 回归测试: 模型每轮调用不同工具(签名不重复),
// 引擎应在达到 maxToolRounds 上限后优雅降级返回友好提示而非 502 硬失败。
func TestChatToolCallLoopMaxRounds(t *testing.T) {
	srv := fakeDistinctToolServer()
	defer srv.Close()

	var execCount int64
	reg := mcp.NewRegistry()
	echoHandler := func(_ context.Context, input map[string]any) (any, error) {
		atomic.AddInt64(&execCount, 1)
		return map[string]any{"round": input["round"]}, nil
	}
	// echo 与 echo2 共用同一 handler(均计入 execCount), 使交替调用下每轮都执行一次, 共 maxToolRounds 次。
	reg.Register(mcp.ToolDefinition{
		Name:        "echo",
		Description: "测试用回显工具",
		Parameters: map[string]mcp.ParamSchema{
			"round": {Type: "integer", Description: "轮次", Required: true},
		},
		Handler: echoHandler,
	})
	reg.Register(mcp.ToolDefinition{
		Name:        "echo2",
		Description: "测试用回显工具(交替)",
		Parameters: map[string]mcp.ParamSchema{
			"round": {Type: "integer", Description: "轮次", Required: true},
		},
		Handler: echoHandler,
	})

	client := llm.NewClient(llm.Config{BaseURL: srv.URL})
	e := NewEngine(reg, nil, client, nil)

	content, _, err := e.Chat(context.Background(), "", "请分步完成一个复杂任务", nil, "", "", false)
	if err != nil {
		t.Fatalf("expected no error (no 502) after max rounds, got: %v", err)
	}
	if !strings.Contains(content, "连续调用了多次工具") {
		t.Fatalf("expected graceful max-rounds message, got content=%q", content)
	}
	// 应执行 maxToolRounds 次(每轮一次, echo/echo2 交替各占一半, 合计 maxToolRounds), 之后循环自然结束。
	if got := atomic.LoadInt64(&execCount); got != int64(maxToolRounds) {
		t.Fatalf("expected tools executed %d times (one per round), got %d", maxToolRounds, got)
	}
}

// TestChatToolCallLoopSoloStreak 回归测试: 模型每轮都调用同一工具(echo), 但参数随轮次变化,
// 故重复签名检测(toolCallSig)不命中; 引擎应通过"连续单工具轮次"检测在达到 maxToolRounds 之前提前优雅终止,
// 不返回硬错误、不耗尽轮次、不触发 502。
func TestChatToolCallLoopSoloStreak(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		round := atomic.AddInt64(&n, 1)
		argsJSON := fmt.Sprintf(`{"round":%d}`, round)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildToolCallResponse("call", argsJSON)) // 始终 echo, 但参数随轮次变化
	}))
	defer srv.Close()

	var execCount int64
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDefinition{
		Name:        "echo",
		Description: "测试用回显工具",
		Parameters: map[string]mcp.ParamSchema{
			"round": {Type: "integer", Description: "轮次", Required: true},
		},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			atomic.AddInt64(&execCount, 1)
			return map[string]any{"round": input["round"]}, nil
		},
	})

	client := llm.NewClient(llm.Config{BaseURL: srv.URL})
	e := NewEngine(reg, nil, client, nil)

	content, _, err := e.Chat(context.Background(), "", "请反复查 echo", nil, "", "", false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(content, "被反复调用") {
		t.Fatalf("expected solo-tool-loop guard message, got content=%q", content)
	}
	// 单工具连击在 maxConsecutiveSoloToolRounds 连击后提前终止, 应远早于 maxToolRounds 轮。
	got := atomic.LoadInt64(&execCount)
	if got > int64(maxConsecutiveSoloToolRounds)+1 {
		t.Fatalf("expected early stop via solo-tool guard (<= %d execs), got %d", maxConsecutiveSoloToolRounds+1, got)
	}
}

// TestChatToolCallLoopEmptyRetry 回归测试: 复现"推理模型无视 retry:false 提示、反复重试同一空工具"的真实场景。
// 工具首轮返回 count:0(空/无数据), 模型第二轮用"变化参数"重试同一工具(专门绕过字节级 dedup),
// 引擎应通过"空结果重试硬拦截"(模型无关)在第二轮直接终止, 而非烧满 8 轮或依赖模型遵守 hint。
func TestChatToolCallLoopEmptyRetry(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		round := atomic.AddInt64(&n, 1)
		// 参数随轮次变化(模拟 GLM 换措辞重试), 使 toolCallSig 字节级 dedup 不命中。
		argsJSON := fmt.Sprintf(`{"query":"介绍自身角色和功能 round %d"}`, round)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildToolCallResponse("call", argsJSON)) // 始终同一工具 echo
	}))
	defer srv.Close()

	var execCount int64
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDefinition{
		Name:        "echo",
		Description: "测试用回显工具(模拟知识库检索, 返回空结果)",
		Parameters: map[string]mcp.ParamSchema{
			"query": {Type: "string", Description: "查询", Required: true},
		},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			atomic.AddInt64(&execCount, 1)
			// 模拟知识库为空: 返回 count:0 空结果(非错误)。
			return map[string]any{"count": 0, "items": []any{}}, nil
		},
	})

	client := llm.NewClient(llm.Config{BaseURL: srv.URL})
	e := NewEngine(reg, nil, client, nil)

	content, _, err := e.Chat(context.Background(), "", "介绍自身角色和功能", nil, "", "", false)
	if err != nil {
		t.Fatalf("expected no error (no 502), got: %v", err)
	}
	if !strings.Contains(content, "工具返回为空") {
		t.Fatalf("expected empty-result hard-break message, got content=%q", content)
	}
	// 第一轮工具执行(返回空); 第 2、3 轮重试被抑制执行(最多 maxEmptyRetryNudges 次 nudge),
	// 第 4 轮仍重试才兜底 break——但工具始终只在首轮真正执行 1 次。
	if got := atomic.LoadInt64(&execCount); got != 1 {
		t.Fatalf("expected tool executed exactly once (empty-retry suppressed before re-exec), got %d", got)
	}
}

// TestChatToolCallLoopEmptyRetryThenAnswer 回归测试: 验证"空结果硬拦截"的抑制重试+继续作答语义。
// 模型首轮调空工具拿到 count:0, 第二轮又重试同一工具(无视 retry:false 提示), 引擎应抑制执行并注入
// "请直接作答"指令, 模型第三轮基于自身知识产出真实答案; 最终用户拿到的是真实回答, 而非"请换个问法"提示语。
// 这正是修复"用户问'介绍自身角色和功能'却只收到一句提示语"的核心验收点。
func TestChatToolCallLoopEmptyRetryThenAnswer(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		round := atomic.AddInt64(&n, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if round <= 2 {
			// 第 1、2 轮: 模型坚持调用同一空工具(模拟 GLM 无视 hint 重试)。
			argsJSON := fmt.Sprintf(`{"query":"介绍自身角色和功能 round %d"}`, round)
			_, _ = w.Write(buildToolCallResponse("call", argsJSON))
		} else {
			// 第 3 轮起: 模型听从"请直接作答"指令, 产出真实答案(无工具调用)。
			_, _ = w.Write(buildTextResponse("我是 OpsOrigin 智能运维助手, 可帮助你完成主机巡检、故障排查与报告生成等任务。"))
		}
	}))
	defer srv.Close()

	var execCount int64
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDefinition{
		Name:        "echo",
		Description: "测试用回显工具(模拟知识库检索, 返回空结果)",
		Parameters: map[string]mcp.ParamSchema{
			"query": {Type: "string", Description: "查询", Required: true},
		},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			atomic.AddInt64(&execCount, 1)
			return map[string]any{"count": 0, "items": []any{}}, nil
		},
	})

	client := llm.NewClient(llm.Config{BaseURL: srv.URL})
	e := NewEngine(reg, nil, client, nil)

	content, _, err := e.Chat(context.Background(), "", "介绍自身角色和功能", nil, "", "", false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// 关键: 用户拿到的是真实自我介绍, 而不是"工具返回为空/请换个问法"的提示语。
	if !strings.Contains(content, "智能运维助手") {
		t.Fatalf("expected real answer (self-intro), got content=%q", content)
	}
	if strings.Contains(content, "工具返回为空") {
		t.Fatalf("should NOT surface empty-retry guard message when model heeds the nudge, got content=%q", content)
	}
	// 空工具仅首轮真正执行 1 次; 第 2 轮重试被抑制, 不应再执行。
	if got := atomic.LoadInt64(&execCount); got != 1 {
		t.Fatalf("expected tool executed exactly once (retry suppressed), got %d", got)
	}
}
