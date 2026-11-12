package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/internal/ai/model"
)

// C 收拢 M3 验收测试(.docs/target-ai-assistant-c-convergence.md §6 验收口径)。
// 只做端到端断言, 业务代码零改动。复用 M0/M1 的确定性假 LLM + 内存轨迹记录器。

// m3Svc 自建 mux 以支持按用例注入不同 tenant_id, 验证轨迹端点的租户隔离。
func m3Svc(t *testing.T) (*Service, func(tenant, method, path, body string) (int, string, http.Header)) {
	t.Helper()
	svc, _ := newFrameTestHandler(t)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	call := func(tenant, method, path, body string) (int, string, http.Header) {
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, r)
		req.Header.Set("Content-Type", "application/json")
		ctx := req.Context()
		ctx = context.WithValue(ctx, "tenant_id", tenant)
		ctx = context.WithValue(ctx, "user_id", "u-m3")
		ctx = context.WithValue(ctx, "role", "admin")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req.WithContext(ctx))
		return rec.Code, rec.Body.String(), rec.Header()
	}
	return svc, call
}

func m3CreateTeam(t *testing.T, svc *Service, tenant, id string) {
	t.Helper()
	ctx := context.WithValue(context.Background(), "tenant_id", tenant)
	if err := svc.Engine.CreateTeam(ctx, &model.AgentTeam{
		ID: id, Name: "M3", TenantID: tenant, Mode: model.TeamModeParallel,
		Members: []model.TeamMember{
			{AgentID: "m1", AgentName: "M1", Role: model.TeamRoleMember},
			{AgentID: "f", AgentName: "F", Role: model.TeamRoleFinalizer, CanFinalize: true},
		},
	}); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
}

const m3Prompt = "请输出一段用于协议契约验收测试的完整说明文字，内容需要足够具体明确"

// TestM3TeamTrajectoryTenantIsolated 验收点 3:
// 统一入口(executor=team)跑完的团队流必须产生归属正确租户的团队级 turn(team:<id>);
// GET /ai/trajectories 同租户可查、跨租户列表为空且单条 404。
func TestM3TeamTrajectoryTenantIsolated(t *testing.T) {
	svc, call := m3Svc(t)
	const tenantA, tenantB = "tenant-a", "tenant-b"
	const teamID, convID = "t-m3-iso", "team:t-m3-iso"
	m3CreateTeam(t, svc, tenantA, teamID)

	body := `{"prompt":"` + m3Prompt + `","stream":true,"executor":{"type":"team","id":"` + teamID + `"}}`
	code, raw, _ := call(tenantA, http.MethodPost, "/api/v1/ai/chat", body)
	if code != http.StatusOK {
		t.Fatalf("team chat status=%d body=%q", code, raw)
	}
	frames := parseSSEFrames(raw)
	if len(frames) == 0 || frames[len(frames)-1].data != "[DONE]" {
		t.Fatalf("team stream 未以 [DONE] 闭流: %q", raw)
	}
	seenFinal := false
	for _, f := range frames {
		if f.event == "final" {
			seenFinal = true
		}
	}
	if !seenFinal {
		t.Fatalf("team stream 缺 event: final, 轨迹可能未收尾: %q", raw)
	}

	listPath := "/api/v1/ai/trajectories?conversation_id=" + url.QueryEscape(convID)
	code, raw, _ = call(tenantA, http.MethodGet, listPath, "")
	if code != http.StatusOK {
		t.Fatalf("list(tenantA) status=%d body=%q", code, raw)
	}
	var turns []*agentic.TrajectoryTurn
	if err := json.Unmarshal([]byte(raw), &turns); err != nil {
		t.Fatalf("unmarshal list: %v raw=%q", err, raw)
	}
	var teamTurn *agentic.TrajectoryTurn
	for _, tr := range turns {
		if tr.AgentID == convID {
			teamTurn = tr
			break
		}
	}
	if teamTurn == nil {
		t.Fatalf("同租户未查到团队 turn: %s", raw)
	}
	if teamTurn.TenantID != tenantA || teamTurn.ConversationID != convID {
		t.Errorf("turn 归属错误: tenant=%q conv=%q", teamTurn.TenantID, teamTurn.ConversationID)
	}
	if teamTurn.Prompt != m3Prompt || teamTurn.FinalContent == "" {
		t.Errorf("turn 内容不完整: prompt=%q final=%q", teamTurn.Prompt, teamTurn.FinalContent)
	}
	if len(teamTurn.Steps) < 2 {
		t.Errorf("团队 turn 应含成员 step + finalizer step, got %d", len(teamTurn.Steps))
	} else if last := teamTurn.Steps[len(teamTurn.Steps)-1]; last.Assistant != teamTurn.FinalContent || last.Model != "team:finalizer" {
		t.Errorf("末步应为 finalizer 且等于最终答复: %#v", last)
	}

	// 跨租户: 列表为空
	code, raw, _ = call(tenantB, http.MethodGet, listPath, "")
	if code != http.StatusOK {
		t.Fatalf("list(tenantB) status=%d body=%q", code, raw)
	}
	var other []*agentic.TrajectoryTurn
	if err := json.Unmarshal([]byte(raw), &other); err != nil {
		t.Fatalf("unmarshal cross-tenant list: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("跨租户泄漏 %d 条轨迹: %s", len(other), raw)
	}

	turnPath := "/api/v1/ai/trajectories/" + url.PathEscape(teamTurn.ID)
	if code, _, _ := call(tenantB, http.MethodGet, turnPath, ""); code != http.StatusNotFound {
		t.Errorf("跨租户单条应 404, got %d", code)
	}
	if code, _, _ := call(tenantA, http.MethodGet, turnPath, ""); code != http.StatusOK {
		t.Errorf("同租户单条应 200, got %d", code)
	}
}

// TestM3ExecutorV1FrameParity 验收点 2 + 5:
// executor 三种取值(default/agent/team)都产出合法 v1 帧并以 [DONE] 闭流。
// v1 协议: 正文 token 走裸 data 且必须是 JSON 字符串; 团队增量走独立 event 帧。
// 退化成 P2(纯 data: {json} 对象、无 event 行)必须被检出。
// 默认助手(executor=default)零工具行为不变 —— 流内不得出现 event: tool。
func TestM3ExecutorV1FrameParity(t *testing.T) {
	svc, call := m3Svc(t)
	const tenant = "tenant-a"
	m3CreateTeam(t, svc, tenant, "t-m3-parity")
	ctx := context.WithValue(context.Background(), "tenant_id", tenant)
	if err := svc.Engine.CreateAgent(ctx, &model.AgentInstance{
		ID: "a-m3", TenantID: tenant, Name: "M3 Agent", ModelName: "test", Visibility: "public",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	cases := []struct {
		name, executor string
		wantNoTool     bool
	}{
		{"default", `{"type":"default"}`, true},
		{"agent", `{"type":"agent","id":"a-m3"}`, false},
		{"team", `{"type":"team","id":"t-m3-parity"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `{"prompt":"` + m3Prompt + `","stream":true,"executor":` + c.executor + `}`
			code, raw, hdr := call(tenant, http.MethodPost, "/api/v1/ai/chat", body)
			if code != http.StatusOK {
				t.Fatalf("status=%d body=%q", code, raw)
			}
			if ct := hdr.Get("Content-Type"); ct != "text/event-stream" {
				t.Errorf("Content-Type=%q, want text/event-stream", ct)
			}
			frames := parseSSEFrames(raw)
			if len(frames) == 0 || frames[len(frames)-1].data != "[DONE]" {
				t.Fatalf("未以 [DONE] 闭流: %q", raw)
			}
			for i, f := range frames {
				if f.data == "[DONE]" || f.data == "" {
					continue
				}
				if f.event == "" {
					// P1 正文 token 允许裸 data, 但必须是 JSON 字符串;
					// P2 的纯 data: {"type":...} 对象解不进 string, 会在此报错。
					var token string
					if err := json.Unmarshal([]byte(f.data), &token); err != nil {
						t.Errorf("frame %d 裸 data 非 JSON 字符串(P2 对象帧泄漏?): %q", i, f.data)
					}
					continue
				}
				if c.wantNoTool && f.event == "tool" {
					t.Errorf("默认助手不应产出 event: tool(零工具行为被改变)")
				}
			}
		})
	}
}
