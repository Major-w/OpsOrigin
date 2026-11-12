package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
)

// 轨迹: OTel 标识补全 + 凭据脱敏 + 长文本截断。
func TestFinalizeTrajectoryRedactionAndOTel(t *testing.T) {
	long := strings.Repeat("x", maxTrajectoryText+500)
	turn := &TrajectoryTurn{
		AgentID: "a1", ConversationID: "c1", TenantID: "t1", Prompt: "hello",
		Steps: []TrajectoryStep{{
			Model: "deepseek-chat", RequestMsgs: 3, Assistant: long,
			ToolCalls: []ToolCallRecord{{
				Name: "script_run",
				Args: map[string]any{"host": "h1", "api_key": "SUPER_SECRET"},
				Result: map[string]any{
					"stdout": long,
					"nested": map[string]any{"password": "p1", "ok": true},
				},
			}},
		}},
	}
	FinalizeTrajectory(turn)

	if turn.ID == "" || len(turn.TraceID) != 32 || len(turn.RootSpanID) != 16 {
		t.Fatalf("trace ids not finalized: id=%q trace=%q root=%q", turn.ID, turn.TraceID, turn.RootSpanID)
	}
	st := turn.Steps[0]
	if len(st.SpanID) != 16 || st.OperationName != "gen_ai.chat" {
		t.Fatalf("step span not finalized: %+v", st)
	}
	if len(st.Assistant) > maxTrajectoryText+40 {
		t.Errorf("assistant text not truncated: len=%d", len(st.Assistant))
	}
	tc := st.ToolCalls[0]
	if tc.Args["api_key"] != "[REDACTED]" {
		t.Errorf("sensitive arg not redacted: %v", tc.Args["api_key"])
	}
	res, ok := tc.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type changed: %T", tc.Result)
	}
	if nested, ok := res["nested"].(map[string]any); !ok || nested["password"] != "[REDACTED]" {
		t.Errorf("nested sensitive key not redacted: %v", res["nested"])
	}
	if stdout, _ := res["stdout"].(string); len(stdout) > maxTrajectoryText+40 {
		t.Errorf("nested long text not truncated: len=%d", len(stdout))
	}

	otel := ToOTelJSON(turn)
	rs, ok := otel["resourceSpans"].([]any)
	if !ok || len(rs) != 1 {
		t.Fatalf("OTel resourceSpans malformed: %v", otel["resourceSpans"])
	}
	scope, ok := rs[0].(map[string]any)["scopeSpans"].([]any)
	if !ok || len(scope) != 1 {
		t.Fatalf("OTel scopeSpans malformed")
	}
	spans, ok := scope[0].(map[string]any)["spans"].([]map[string]any)
	if !ok || len(spans) != 3 { // root + chat step + tool call
		t.Fatalf("OTel spans want 3, got %d", len(spans))
	}
	if spans[2]["name"] != "execute_tool.script_run" {
		t.Errorf("tool span name = %v", spans[2]["name"])
	}
	if spans[1]["parentSpanId"] != turn.RootSpanID {
		t.Errorf("step span parent mismatch")
	}
}

// 端点用的 ListTurns / GetTurn 必须按租户隔离(修复旧 GetTurns 的越权面)。
func TestMemTrajectoryTenantIsolation(t *testing.T) {
	r := NewMemTrajectoryRecorder()
	ctx := context.Background()
	for _, tc := range []struct{ tenant, conv, id string }{
		{"t1", "c1", "turn-1"}, {"t2", "c1", "turn-2"},
	} {
		if err := r.RecordTurn(ctx, &TrajectoryTurn{ID: tc.id, TenantID: tc.tenant, ConversationID: tc.conv}); err != nil {
			t.Fatal(err)
		}
	}
	turns, err := r.ListTurns(ctx, "t1", "c1", 10, 0)
	if err != nil || len(turns) != 1 || turns[0].ID != "turn-1" {
		t.Fatalf("ListTurns leaked cross-tenant data: %v err=%v", turns, err)
	}
	if got, _ := r.GetTurn(ctx, "t1", "turn-2"); got != nil {
		t.Errorf("GetTurn across tenant should be nil, got %+v", got)
	}
	if got, _ := r.GetTurn(ctx, "t2", "turn-2"); got == nil || got.ID != "turn-2" {
		t.Errorf("GetTurn owner failed: %+v", got)
	}
}

type fakePolicyStore struct {
	policies map[string]ApprovalPolicy
	saved    int
}

func (f *fakePolicyStore) GetApprovalPolicy(_ context.Context, _, agentID string) (*ApprovalPolicy, error) {
	p, ok := f.policies[agentID]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *fakePolicyStore) SaveApprovalPolicy(_ context.Context, _, agentID string, p ApprovalPolicy, _ string) error {
	f.policies[agentID] = p
	f.saved++
	return nil
}

// 持久化策略是权威来源, 内存 map 只兜底。
func TestApprovalPolicyStoreIsAuthority(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), nil, nil, nil)
	e.SetApprovalPolicy("a1", ApprovalPolicy{Mode: ApprovalAlwaysAllow, AskTimeoutSec: 60})
	store := &fakePolicyStore{policies: map[string]ApprovalPolicy{
		"a1": {Mode: ApprovalAlwaysAsk, AskTimeoutSec: 9},
	}}
	e.SetApprovalPolicyStore(store)
	got := e.GetApprovalPolicyCtx(context.Background(), "a1")
	if got.Mode != ApprovalAlwaysAsk || got.AskTimeoutSec != 9 {
		t.Fatalf("persisted policy ignored: %+v", got)
	}
	if e.GetApprovalPolicy("a1").Mode != ApprovalAlwaysAllow {
		t.Fatal("legacy in-memory policy should remain untouched as fallback")
	}
	// 未装配 store 时回落内存
	e2 := NewEngine(mcp.NewRegistry(), nil, nil, nil)
	e2.SetApprovalPolicy("a1", ApprovalPolicy{Mode: ApprovalAlwaysAllow, AskTimeoutSec: 60})
	if e2.GetApprovalPolicyCtx(context.Background(), "a1").Mode != ApprovalAlwaysAllow {
		t.Fatal("fallback to in-memory policy failed")
	}
}

// 网关统一审批: 所有工具都过策略; 批复后工具内置 checker 消费凭证, 不再二次弹窗。
func TestApprovalGateIsSingleApprovalPoint(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), nil, nil, nil)
	e.SetApprovalPolicy("a1", ApprovalPolicy{Mode: ApprovalAlwaysAsk, AskTimeoutSec: 5})
	notifies := 0
	ctx := WithApprovalNotify(context.Background(), func(pa *PendingApproval) {
		notifies++
		e.Approvals().Respond(pa.ID, "approve", "tester", "")
	})
	ctx = withAgentID(ctx, "a1")

	allow, _, msg := e.approvalGate(ctx, "a1", "ticket_create", map[string]any{"id": "1"})
	if !allow || msg != "" {
		t.Fatalf("gateway approval should allow: allow=%v msg=%q", allow, msg)
	}
	if notifies != 1 {
		t.Fatalf("expected exactly one approval prompt, got %d", notifies)
	}
	// ticket_create 不是 script_run, 凭证 key 不同 → checker 仍会走自己的审批(fail-closed 兜底)。
	// 关键验证: 同一工具 script_run 经网关批复后, checker 不再重复弹窗。
	allow2, _, _ := e.approvalGate(ctx, "a1", "script_run", map[string]any{"script": "x"})
	if !allow2 {
		t.Fatal("gateway approval for script_run failed")
	}
	if notifies != 2 {
		t.Fatalf("expected 2 prompts total, got %d", notifies)
	}
	if ok, reason := e.ScriptApprovalChecker(ctx, "s1", "host-1", map[string]any{"script": "x"}); !ok {
		t.Fatalf("checker should honor gateway grant, got reason %q", reason)
	}
	if notifies != 2 {
		t.Fatalf("checker triggered a duplicate approval prompt: %d", notifies)
	}
}

// 拒绝/超时一律 fail-closed, 工具不执行。
func TestApprovalGateRejectIsFailClosed(t *testing.T) {
	e := NewEngine(mcp.NewRegistry(), nil, nil, nil)
	e.SetApprovalPolicy("a1", ApprovalPolicy{Mode: ApprovalAlwaysAsk, AskTimeoutSec: 5})
	ctx := WithApprovalNotify(context.Background(), func(pa *PendingApproval) {
		e.Approvals().Respond(pa.ID, "reject", "tester", "no")
	})
	allow, _, msg := e.approvalGate(withAgentID(ctx, "a1"), "a1", "script_run", nil)
	if allow || msg == "" {
		t.Fatalf("rejected approval must be fail-closed: allow=%v msg=%q", allow, msg)
	}
}
