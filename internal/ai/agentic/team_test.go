package agentic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/pkg/llm"
)

// newTeamTestEngine 用 httptest 起一个假 LLM 后端, 按 responder 决定每次 Chat 的返回,
// 便于确定性地验证团队编排(工具调用 / 模型路由 / 重试 / 辩论轮次 / 主管审核)。
func newTeamTestEngine(t *testing.T, responder func(req llm.ChatRequest) (status int, body string)) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req llm.ChatRequest
		_ = json.Unmarshal(b, &req)
		status, body := responder(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	client := llm.NewClient(llm.Config{
		APIKey:  "test",
		Model:   "test",
		BaseURL: srv.URL,
		Timeout: 5e9, // 5s
	})
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDefinition{
		Name: "knowledge_search",
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			return map[string]any{"count": 1, "hit": "k8s-pod-crash"}, nil
		},
	})
	e := NewEngine(reg, ratelimit.New(), client, nil)
	return e
}

func ansResp(content string) string {
	b, _ := json.Marshal(map[string]any{
		"id":     "c",
		"object": "chat.completion",
		"model":  "test",
		"choices": []map[string]any{
			{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}},
		},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 3, "total_tokens": 6},
	})
	return string(b)
}

func toolResp(name, args string) string {
	b, _ := json.Marshal(map[string]any{
		"id":     "c",
		"object": "chat.completion",
		"model":  "test",
		"choices": []map[string]any{
			{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{
					{"id": "call_1", "type": "function", "function": map[string]any{"name": name, "arguments": args}},
				},
			}},
		},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 3, "total_tokens": 6},
	})
	return string(b)
}

func emptyResp() string {
	return `{"id":"c","model":"test","choices":[]}`
}

func hasToolMessage(req llm.ChatRequest) bool {
	for _, m := range req.Messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

func systemContent(req llm.ChatRequest) string {
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
		return req.Messages[0].Content
	}
	return ""
}

// TestTeamMemberUsesTools 验证开启 EnableTools 的成员可在作答中调用工具(Function Calling),
// 工具调用经由 tool_call / tool_result 事件透出, 且最终答复融合了工具结果。
func TestTeamMemberUsesTools(t *testing.T) {
	var events []model.TeamStreamEvent
	var mu sync.Mutex
	emit := func(ev model.TeamStreamEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	e := newTeamTestEngine(t, func(req llm.ChatRequest) (int, string) {
		if hasToolMessage(req) {
			return 200, ansResp("基于检索结论 FINAL_WITH_TOOL")
		}
		if strings.Contains(systemContent(req), "IDENTITY_M1") {
			return 200, toolResp("knowledge_search", `{"q":"x"}`)
		}
		return 200, ansResp("FINAL_WITH_TOOL")
	})
	team := &model.AgentTeam{
		ID:          "t1",
		Name:        "T",
		Mode:        model.TeamModeParallel,
		EnableTools: true,
		Members: []model.TeamMember{
			{AgentID: "m1", AgentName: "M1", Role: model.TeamRoleMember, SystemPrompt: "IDENTITY_M1"},
			{AgentID: "f", AgentName: "F", Role: model.TeamRoleFinalizer, CanFinalize: true, SystemPrompt: "IDENTITY_F"},
		},
	}
	final, _, err := e.RunTeamChat(context.WithValue(context.Background(), "role", "admin"), team, "故障排查", nil, emit, "")
	if err != nil {
		t.Fatalf("RunTeamChat error: %v", err)
	}
	if !strings.Contains(final, "FINAL_WITH_TOOL") {
		t.Errorf("final answer missing tool-derived content: %q", final)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "tool_call" && ev.ToolName == "knowledge_search" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a knowledge_search tool_call event; events=%#v", events)
	}
}

// TestTeamMemberModelRouting 验证 per-member 模型覆盖(ModelName)在团队对话中生效。
func TestTeamMemberModelRouting(t *testing.T) {
	var models []string
	var mu sync.Mutex
	e := newTeamTestEngine(t, func(req llm.ChatRequest) (int, string) {
		mu.Lock()
		models = append(models, req.Model)
		mu.Unlock()
		return 200, ansResp("ok")
	})
	team := &model.AgentTeam{
		ID:   "t2",
		Name: "T",
		Mode: model.TeamModeParallel,
		Members: []model.TeamMember{
			{AgentID: "m1", AgentName: "M1", Role: model.TeamRoleMember, ModelName: "model-m1"},
			{AgentID: "m2", AgentName: "M2", Role: model.TeamRoleMember},
			{AgentID: "f", AgentName: "F", Role: model.TeamRoleFinalizer, CanFinalize: true},
		},
	}
	if _, _, err := e.RunTeamChat(context.Background(), team, "hi", nil, func(model.TeamStreamEvent) {}, ""); err != nil {
		t.Fatalf("err: %v", err)
	}
	found := false
	for _, m := range models {
		if m == "model-m1" {
			found = true
		}
	}
	if !found {
		t.Errorf("per-member model not used; requested models=%v", models)
	}
}

// TestTeamMemberRetry 验证成员级失败重试: 前两次 LLM 返回空 choices(错误), 第三次成功,
// 整体应在重试后给出正确答复, 并下发重试错误事件。
func TestTeamMemberRetry(t *testing.T) {
	var calls int
	var mu sync.Mutex
	var events []model.TeamStreamEvent
	emit := func(ev model.TeamStreamEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	e := newTeamTestEngine(t, func(req llm.ChatRequest) (int, string) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n < 3 {
			// 400 + 空 body -> 非可重试错误, 触发 runMemberWithRetry 重试(无内部重试放大)
			return 400, ""
		}
		return 200, ansResp("succeed after retries")
	})
	team := &model.AgentTeam{
		ID:             "t3",
		Name:           "T",
		Mode:           model.TeamModeParallel,
		MemberRetries:  2,
		Members: []model.TeamMember{
			{AgentID: "m1", AgentName: "M1", Role: model.TeamRoleMember},
			{AgentID: "f", AgentName: "F", Role: model.TeamRoleFinalizer, CanFinalize: true},
		},
	}
	final, _, err := e.RunTeamChat(context.Background(), team, "hi", nil, emit, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(final, "succeed after retries") {
		t.Errorf("final=%q", final)
	}
	if calls != 4 {
		t.Errorf("expected 4 LLM calls (3 member attempts after retries + 1 finalizer), got %d", calls)
	}
	retrySeen := false
	for _, ev := range events {
		if ev.Type == "error" && strings.Contains(ev.Error, "重试") {
			retrySeen = true
		}
	}
	if !retrySeen {
		t.Errorf("expected a retry error event; events=%#v", events)
	}
}

// TestTeamDebateRounds 验证辩论模式按 DebateRounds 进行多轮往返。
func TestTeamDebateRounds(t *testing.T) {
	var calls int
	var mu sync.Mutex
	e := newTeamTestEngine(t, func(req llm.ChatRequest) (int, string) {
		mu.Lock()
		calls++
		mu.Unlock()
		return 200, ansResp("debate-answer")
	})
	team := &model.AgentTeam{
		ID:           "t4",
		Name:         "T",
		Mode:         model.TeamModeDebate,
		DebateRounds: 2,
		Members: []model.TeamMember{
			{AgentID: "a", AgentName: "A", Role: model.TeamRoleMember},
			{AgentID: "b", AgentName: "B", Role: model.TeamRoleMember},
			{AgentID: "f", AgentName: "F", Role: model.TeamRoleFinalizer, CanFinalize: true},
		},
	}
	final, _, err := e.RunTeamChat(context.Background(), team, "问题?", nil, func(model.TeamStreamEvent) {}, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(final, "debate-answer") {
		t.Errorf("final=%q", final)
	}
	// 2 成员 x 2 轮 + 1 汇总者 = 5 次 LLM 调用
	if calls != 5 {
		t.Errorf("expected 5 LLM calls (2 members x 2 rounds + finalizer), got %d", calls)
	}
}

// TestTeamSupervisorVerify 验证主管审核回路: 主管判定需修订时, 指定成员补充重做一轮再定稿。
func TestTeamSupervisorVerify(t *testing.T) {
	var calls int
	var mu sync.Mutex
	var events []model.TeamStreamEvent
	emit := func(ev model.TeamStreamEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	e := newTeamTestEngine(t, func(req llm.ChatRequest) (int, string) {
		mu.Lock()
		calls++
		mu.Unlock()
		isReview := false
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "VERDICT: APPROVED") {
				isReview = true
			}
		}
		if isReview {
			return 200, ansResp("VERDICT: REVISE W 请补充容量评估")
		}
		return 200, ansResp("member-output")
	})
	team := &model.AgentTeam{
		ID:               "t5",
		Name:             "T",
		Mode:             model.TeamModeSequential,
		SupervisorVerify: true,
		Members: []model.TeamMember{
			{AgentID: "s", AgentName: "S", Role: model.TeamRoleSupervisor},
			{AgentID: "w", AgentName: "W", Role: model.TeamRoleMember},
			{AgentID: "f", AgentName: "F", Role: model.TeamRoleFinalizer, CanFinalize: true},
		},
	}
	final, _, err := e.RunTeamChat(context.Background(), team, "评估容量", nil, emit, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(final, "member-output") {
		t.Errorf("final=%q", final)
	}
	// S 初始 + W 初始 + S 审核 + W 修订 + F 定稿 = 5
	if calls != 5 {
		t.Errorf("expected 5 LLM calls (with one revision), got %d", calls)
	}
	reviewSeen := false
	for _, ev := range events {
		if ev.Type == "supervisor_review" {
			reviewSeen = true
		}
	}
	if !reviewSeen {
		t.Errorf("expected a supervisor_review event; events=%#v", events)
	}
}
