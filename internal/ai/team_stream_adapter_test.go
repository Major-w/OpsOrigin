package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/opsorigin/core/internal/ai/model"
)

// TestTeamEventV1Mapping 覆盖 P2→v1 映射表(target-ai-assistant-c-convergence.md §3.4):
// 每个 TeamStreamEvent.Type 都产出正确的 v1 事件名 + 结构化 payload, 且帧格式为 "event: X\ndata: Y\n\n"。
func TestTeamEventV1Mapping(t *testing.T) {
	cases := []struct {
		name      string
		ev        model.TeamStreamEvent
		wantEvent string
		wantKeys  []string
	}{
		{"agent_start", model.TeamStreamEvent{Type: "agent_start", AgentID: "m1", AgentName: "M1", Role: "member"}, "agent", []string{"phase", "agent_id", "agent_name", "role"}},
		{"agent_end", model.TeamStreamEvent{Type: "agent_end", AgentID: "m1", AgentName: "M1", Role: "member", Content: "c"}, "agent", []string{"phase", "content"}},
		{"token", model.TeamStreamEvent{Type: "token", AgentID: "m1", AgentName: "M1", Role: "member", Content: "c"}, "agent_token", []string{"agent_id", "agent_name", "role", "content"}},
		{"tool_call", model.TeamStreamEvent{Type: "tool_call", ToolName: "t", ToolArgs: "{}"}, "tool", []string{"name", "args", "status", "agent_id"}},
		{"tool_result", model.TeamStreamEvent{Type: "tool_result", ToolName: "t", ToolResult: "{}"}, "tool", []string{"name", "result", "status", "agent_id"}},
		{"supervisor_review", model.TeamStreamEvent{Type: "supervisor_review", AgentID: "s", AgentName: "S", Role: "supervisor", Content: "c"}, "agent_token", []string{"agent_id", "agent_name", "content", "role", "member_role"}},
		{"final", model.TeamStreamEvent{Type: "final", Content: "f", Done: true}, "final", []string{"content", "done"}},
		{"error", model.TeamStreamEvent{Type: "error", Error: "boom"}, "error", nil},
		{"error_member", model.TeamStreamEvent{Type: "error", AgentID: "m1", AgentName: "M1", Role: "member", Error: "boom"}, "error", []string{"message", "agent_id", "agent_name", "role"}},
		{"unknown_defaults_to_error", model.TeamStreamEvent{Type: "weird", Error: "x"}, "error", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evName, payload := teamEventV1(c.ev)
			if evName != c.wantEvent {
				t.Errorf("event name = %q, want %q", evName, c.wantEvent)
			}
			if c.wantKeys != nil {
				var m map[string]any
				if err := json.Unmarshal([]byte(payload), &m); err != nil {
					t.Fatalf("payload not JSON object: %v (%s)", err, payload)
				}
				for _, k := range c.wantKeys {
					if _, ok := m[k]; !ok {
						t.Errorf("payload missing key %q: %s", k, payload)
					}
				}
			}
			// 帧完整性: 经 writeTeamEventV1 写出后必须为 "event: X\ndata: <payload>\n\n"。
			var sb strings.Builder
			writeTeamEventV1(&sb, &sync.Mutex{}, c.ev)
			wantFrame := "event: " + evName + "\ndata: " + payload + "\n\n"
			if sb.String() != wantFrame {
				t.Errorf("frame format unexpected:\n got %q\nwant %q", sb.String(), wantFrame)
			}
		})
	}
}

// TestTeamEventV1ErrorMessage 防止团队失败被写成空错误帧:
// 两条 handler 的顶层失败历史上把友好文案放在 Content, RunTeamChat 的成员级错误放在 Error;
// 顶层帧必须是可读文本, 成员级帧必须连身份一起下发, 否则前端无法标红具体成员。
func TestTeamEventV1ErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		ev   model.TeamStreamEvent
		want string
	}{
		{"content_fallback", model.TeamStreamEvent{Type: "error", Content: "模型不可用"}, "模型不可用"},
		{"error_wins", model.TeamStreamEvent{Type: "error", Error: "限流", Content: "旧文案"}, "限流"},
		{"empty_guard", model.TeamStreamEvent{Type: "error"}, "团队运行失败"},
		{"unknown_type_fallback", model.TeamStreamEvent{Type: "weird", Content: "未知事件"}, "未知事件"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evName, payload := teamEventV1(c.ev)
			if evName != "error" {
				t.Fatalf("event = %q, want error", evName)
			}
			var msg string
			if err := json.Unmarshal([]byte(payload), &msg); err != nil {
				t.Fatalf("顶层 error 帧必须是 JSON 字符串, got %q: %v", payload, err)
			}
			if msg != c.want {
				t.Errorf("message = %q, want %q", msg, c.want)
			}
		})
	}

	_, payload := teamEventV1(model.TeamStreamEvent{Type: "error", AgentID: "m1", AgentName: "M1", Role: "member", Error: "boom"})
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		t.Fatalf("成员级 error 帧必须是 JSON 对象, got %q: %v", payload, err)
	}
	for k, want := range map[string]string{"message": "boom", "agent_id": "m1", "agent_name": "M1", "role": "member"} {
		if obj[k] != want {
			t.Errorf("%s = %v, want %q", k, obj[k], want)
		}
	}
}

// TestResolveExecutor 覆盖 §3.2 确定性优先级裁决。
func TestResolveExecutor(t *testing.T) {
	svc := &Service{}
	cases := []struct {
		name     string
		req      chatRequest
		wantType string
		wantID   string
	}{
		{"executor_wins_over_others", chatRequest{Executor: ExecutorSpec{Type: "team", ID: "t1"}, TeamID: "t2", AgentID: "a1"}, "team", "t1"},
		{"only_team_id", chatRequest{TeamID: "t2", AgentID: "a1"}, "team", "t2"},
		{"only_agent_id", chatRequest{AgentID: "a1"}, "agent", "a1"},
		{"all_empty_default", chatRequest{}, "default", ""},
		{"executor_agent", chatRequest{Executor: ExecutorSpec{Type: "agent", ID: "a9"}}, "agent", "a9"},
		{"executor_default_ignores_agent_id", chatRequest{Executor: ExecutorSpec{Type: "default"}, AgentID: "a1"}, "default", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			typ, id := svc.resolveExecutor(&c.req)
			if typ != c.wantType || id != c.wantID {
				t.Errorf("got (%q,%q) want (%q,%q)", typ, id, c.wantType, c.wantID)
			}
		})
	}
}

// TestUnifiedChatTeamStream 验证 M1 统一入口: POST /api/v1/ai/chat + executor{type:team}
// 经 handleTeamStreamUnified 复用 RunTeamChat, 输出 v1 帧(event 行 + data), 以 [DONE] 闭流,
// 且不得退化为 P2 纯 data: 协议。
func TestUnifiedChatTeamStream(t *testing.T) {
	svc, h := newFrameTestHandler(t)
	ctx := context.WithValue(context.Background(), "tenant_id", "t-frame")
	if err := svc.Engine.CreateTeam(ctx, &model.AgentTeam{
		ID:       "t-uniq-1",
		Name:     "T",
		TenantID: "t-frame",
		Mode:     model.TeamModeParallel,
		Members: []model.TeamMember{
			{AgentID: "m1", AgentName: "M1", Role: model.TeamRoleMember},
			{AgentID: "f", AgentName: "F", Role: model.TeamRoleFinalizer, CanFinalize: true},
		},
	}); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	body := `{"prompt":"请输出一段用于协议契约测试的完整说明文字，内容需要足够具体明确","stream":true,"executor":{"type":"team","id":"t-uniq-1"}}`
	code, raw, hdr := frameCall(t, h, http.MethodPost, "/api/v1/ai/chat", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", code, raw)
	}
	if got := hdr.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	frames := parseSSEFrames(raw)
	if len(frames) == 0 {
		t.Fatalf("no frames; body=%q", raw)
	}
	events := map[string]int{}
	for i, f := range frames {
		if f.data == "[DONE]" {
			continue
		}
		if f.event == "" {
			t.Errorf("frame %d has data but no event (P2 leak): %q", i, f.data)
		}
		events[f.event]++
	}
	if events["agent"] == 0 {
		t.Errorf("missing event: agent; frames=%#v", frames)
	}
	if events["agent_token"] == 0 {
		t.Errorf("missing event: agent_token; frames=%#v", frames)
	}
	if events["final"] == 0 {
		t.Errorf("missing event: final; frames=%#v", frames)
	}
	last := frames[len(frames)-1]
	if last.data != "[DONE]" {
		t.Errorf("last frame = %q, want [DONE]", last.data)
	}
}

// TestUnifiedChatTeamNonStreamRejected 验证 §3.2 rule 5: 团队 + stream=false → 400。
func TestUnifiedChatTeamNonStreamRejected(t *testing.T) {
	svc, h := newFrameTestHandler(t)
	ctx := context.WithValue(context.Background(), "tenant_id", "t-frame")
	if err := svc.Engine.CreateTeam(ctx, &model.AgentTeam{
		ID: "t-uniq-2", Name: "T", TenantID: "t-frame", Mode: model.TeamModeParallel,
		Members: []model.TeamMember{{AgentID: "m1", AgentName: "M1", Role: model.TeamRoleMember}, {AgentID: "f", AgentName: "F", Role: model.TeamRoleFinalizer, CanFinalize: true}},
	}); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	body := `{"prompt":"x","stream":false,"executor":{"type":"team","id":"t-uniq-2"}}`
	code, raw, _ := frameCall(t, h, http.MethodPost, "/api/v1/ai/chat", body)
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%q", code, raw)
	}
}

// TestUnifiedChatTeamNotFound 验证团队不可见/跨租户 → 404(与旧 handleTeamChat 语义一致, 不泄漏存在性)。
func TestUnifiedChatTeamNotFound(t *testing.T) {
	_, h := newFrameTestHandler(t)
	body := `{"prompt":"x","stream":true,"executor":{"type":"team","id":"does-not-exist"}}`
	code, raw, _ := frameCall(t, h, http.MethodPost, "/api/v1/ai/chat", body)
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%q", code, raw)
	}
}
