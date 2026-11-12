package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/llm"
)

// M0 characterization: 把现有三套 SSE 帧格式逐字节钉死。
//
// 背景: 内部 AI 链路有三套互不兼容的流协议, 而全仓原先没有任何帧级断言。
// C 最小收拢(M1)要合并 P1/P2 时, 这组测试是唯一的安全网 —— 旧端点输出必须保持不变。
// 本文件只读捕获现状, 不改变任何业务行为; P3 属于任务控制面, 不在收拢范围。
//
// 事实基线(2026-11-09 实测):
//   P1 POST /ai/chat            service.go:642-790  event: X + data: 双行帧, 裸 data 是 JSON 字符串
//   P2 POST /ai/teams/{id}/chat service.go:1650-1723 纯 data: JSON, 无 event: 行
//   P3 GET  /ai/tasks/{id}/events tasks.go:849-889  event: snapshot, 无 [DONE]

type sseFrame struct {
	event string
	data  string
}

// parseSSEFrames 按空行切帧, 保留 event/data 两行; ": keepalive" 注释行产出空帧。
func parseSSEFrames(body string) []sseFrame {
	var out []sseFrame
	for _, block := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var f sseFrame
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				f.data = strings.TrimPrefix(line, "data: ")
			}
		}
		out = append(out, f)
	}
	return out
}

func frameAnswer(content string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "c", "object": "chat.completion", "model": "test",
		"choices": []map[string]any{{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": content},
		}},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 3, "total_tokens": 6},
	})
	return string(b)
}

// newFrameTestHandler 起一个确定性假 LLM 后端, 装配真实 Service + ServeMux(不依赖 PG/Redis)。
func newFrameTestHandler(t *testing.T) (*Service, http.Handler) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, frameAnswer("frame-ok"))
	}))
	t.Cleanup(upstream.Close)

	client := llm.NewClient(llm.Config{
		APIKey: "test", Model: "test", BaseURL: upstream.URL, Timeout: 5e9,
	})
	svc := NewService(&config.Config{}, client, nil, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, "tenant_id", "t-frame")
		ctx = context.WithValue(ctx, "user_id", "u-frame")
		ctx = context.WithValue(ctx, "role", "admin")
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
	return svc, h
}

func frameCall(t *testing.T, h http.Handler, method, path, body string) (int, string, http.Header) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header()
}

// P1: 主聊天双行帧。裸 data 是 json.Marshal(string) 的结果, 不是纯文本、也不是结构化对象
// (前端 stores/ai/chat.ts:202 用 JSON.parse(data) as string 还原)。
func TestSSEFrameContractP1Chat(t *testing.T) {
	_, h := newFrameTestHandler(t)
	// 明确且 >10 字、不含模糊词, 避免触发 clarify 并发 LLM 调用干扰帧序列。
	// 必须带 conversation_id: event: conversation 是条件帧(service.go:761 仅 convID != "" 时发),
	// 不带 ID 的新会话不会出现该帧; 显式给 ID 才能把它一并钉死。
	const frameConvID = "conv-frame-p1"
	reqBody := `{"prompt":"请输出一段用于协议契约测试的完整说明文字，内容需要足够具体明确","stream":true,"conversation_id":"` + frameConvID + `"}`
	code, body, hdr := frameCall(t, h, http.MethodPost, "/api/v1/ai/chat", reqBody)
	if code != http.StatusOK {
		t.Fatalf("P1 status = %d, body = %q", code, body)
	}
	if got := hdr.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("P1 Content-Type = %q, want text/event-stream", got)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("P1 必须以 data: [DONE] 结束, got tail = %q", body[len(body)-min(len(body), 80):])
	}

	frames := parseSSEFrames(body)
	if len(frames) == 0 {
		t.Fatal("P1 未解析出任何帧")
	}
	var (
		hasConversation bool
		bareTokens      int
	)
	for _, f := range frames {
		if f.event != "" {
			// 命名事件帧必须紧跟 data 行
			if f.data == "" {
				t.Errorf("P1 event %q 帧缺少 data 行", f.event)
			}
			if f.event == "conversation" {
				hasConversation = true
				if f.data != frameConvID {
					t.Errorf("P1 conversation data = %q, want %q", f.data, frameConvID)
				}
			}
			if f.event == "final" {
				t.Error("P1 不应出现 event: final(那是团队流协议)")
			}
			continue
		}
		if f.data == "" || f.data == "[DONE]" {
			continue
		}
		bareTokens++
		var token string
		if err := json.Unmarshal([]byte(f.data), &token); err != nil {
			t.Errorf("P1 裸 data 必须是 JSON 字符串, got %q: %v", f.data, err)
		}
	}
	if !hasConversation {
		t.Error("P1 缺少 event: conversation 帧")
	}
	if bareTokens == 0 {
		t.Error("P1 缺少裸 data token 帧")
	}
}

// P2: 团队纯 data JSON 流。后端发 [DONE], 但前端 teams.ts:113 只做 JSON.parse,
// [DONE] 被 catch 吞掉, 实际靠 final{Done:true} 收尾 —— 这是现存隐式不一致, M1 必须显式处理。
func TestSSEFrameContractP2Team(t *testing.T) {
	svc, h := newFrameTestHandler(t)
	team := &model.AgentTeam{
		ID: "team-frame", TenantID: "t-frame", Name: "帧协议测试团队",
		Mode: model.TeamModeParallel,
		Members: []model.TeamMember{
			{AgentID: "m1", AgentName: "成员一", Role: model.TeamRoleMember},
			{AgentID: "m2", AgentName: "成员二", Role: model.TeamRoleMember},
		},
	}
	if err := svc.Engine.CreateTeam(context.Background(), team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	code, body, hdr := frameCall(t, h, http.MethodPost, "/api/v1/ai/teams/team-frame/chat",
		`{"message":"请输出用于协议契约测试的完整结论"}`)
	if code != http.StatusOK {
		t.Fatalf("P2 status = %d, body = %q", code, body)
	}
	if got := hdr.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("P2 Content-Type = %q", got)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("P2 必须以 data: [DONE] 结束, got tail = %q", body[len(body)-min(len(body), 80):])
	}

	frames := parseSSEFrames(body)
	if len(frames) == 0 {
		t.Fatal("P2 未解析出任何帧")
	}
	types := map[string]bool{}
	for _, f := range frames {
		if f.event != "" {
			t.Errorf("P2 不应有 event: 行, got event=%q data=%q", f.event, f.data)
		}
		if f.data == "" || f.data == "[DONE]" {
			continue
		}
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(f.data), &ev); err != nil {
			t.Errorf("P2 data 必须是 JSON 对象, got %q: %v", f.data, err)
			continue
		}
		if ev.Type == "" {
			t.Errorf("P2 事件缺少 type 字段: %q", f.data)
		}
		types[ev.Type] = true
	}
	for _, want := range []string{"agent_start", "agent_end", "final"} {
		if !types[want] {
			t.Errorf("P2 缺少 %q 事件, got %v", want, types)
		}
	}
	if types["error"] {
		t.Errorf("P2 出现 error 事件, 假 LLM 后端应始终成功: %q", body)
	}
}

// cancelOnFlushWriter 在第一次 Flush(snapshot 首帧写完)后取消请求 ctx,
// 让长连接 handleTaskEvents 从 select 退出, 避免测试依赖 sleep 或泄漏 goroutine。
type cancelOnFlushWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	once   sync.Once
}

func (w *cancelOnFlushWriter) Flush() {
	w.ResponseRecorder.Flush()
	w.once.Do(w.cancel)
}

// P3: 任务事件流首帧。它无 [DONE], 终态 done/failed/cancelled 后由服务端关流
// (tasks.go:884-887), 15s 发 : keepalive(tasks.go:872-880)。P3 属控制面, 不参与 C 收拢。
func TestSSEFrameContractP3Task(t *testing.T) {
	_, h := newFrameTestHandler(t)
	code, body, _ := frameCall(t, h, http.MethodPost, "/api/v1/ai/tasks",
		`{"goal":"用于验证任务事件流首帧格式的契约测试目标","title":"帧协议测试任务"}`)
	if code != http.StatusCreated {
		t.Fatalf("create task status = %d, body = %q", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == "" {
		t.Fatalf("解析任务 ID 失败: %v, body = %q", err, body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/tasks/"+created.ID+"/events", nil).WithContext(ctx)
	cw := &cancelOnFlushWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	h.ServeHTTP(cw, req)
	got := cw.Body.String()

	if !strings.HasPrefix(got, "event: snapshot\ndata: ") {
		t.Fatalf("P3 首帧必须是 event: snapshot, got %q", got)
	}
	if strings.Contains(got, "[DONE]") {
		t.Error("P3 不应出现 [DONE](终态关流, 无显式结束标记)")
	}
	frames := parseSSEFrames(got)
	if len(frames) == 0 || frames[0].event != "snapshot" {
		t.Fatalf("P3 首帧解析失败: %+v", frames)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(frames[0].data), &snap); err != nil {
		t.Errorf("P3 snapshot data 必须是 JSON 对象: %v, got %q", err, frames[0].data)
	}
}

// 三协议互斥: 证明不能用同一套解析器同时消费 P1/P2/P3, 即 C 收拢确有必要。
func TestSSEFrameProtocolsMutuallyIncompatible(t *testing.T) {
	p1 := "event: conversation\ndata: conv-1\n\ndata: \"hi\"\n\ndata: [DONE]\n\n"
	p2 := "data: {\"type\":\"final\",\"content\":\"ok\",\"done\":true}\n\ndata: [DONE]\n\n"
	p3 := "event: snapshot\ndata: {\"id\":\"t1\"}\n\n"

	f1, f2, f3 := parseSSEFrames(p1), parseSSEFrames(p2), parseSSEFrames(p3)
	hasEvent := func(fs []sseFrame) bool {
		for _, f := range fs {
			if f.event != "" {
				return true
			}
		}
		return false
	}
	hasDone := func(body string) bool { return strings.Contains(body, "data: [DONE]") }

	if !hasEvent(f1) || hasEvent(f2) || !hasEvent(f3) {
		t.Fatal("P1/P3 有 event: 行、P2 没有 —— 协议判别前提不成立")
	}
	if !hasDone(p1) || !hasDone(p2) || hasDone(p3) {
		t.Fatal("P1/P2 以 [DONE] 结束、P3 没有 —— 协议判别前提不成立")
	}
}
