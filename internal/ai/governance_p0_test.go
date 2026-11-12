package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
)

// P0 治理层路由级集成测试: 不依赖 Docker/PostgreSQL, 走真实 Service + ServeMux + 内存 recorder。

func newP0TestHandler(t *testing.T) (*Service, http.Handler) {
	t.Helper()
	svc := NewService(&config.Config{}, nil, nil, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, "tenant_id", "t1")
		ctx = context.WithValue(ctx, "user_id", r.Header.Get("X-User"))
		ctx = context.WithValue(ctx, "role", r.Header.Get("X-Role"))
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
	return svc, h
}

func p0Call(t *testing.T, h http.Handler, method, path, user, role string, body any) (int, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User", user)
	req.Header.Set("X-Role", role)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode map: %v (%s)", err, string(b))
	}
	return m
}

// 新建数字员工默认私有: 属主可见可改, 他人列表/详情/改/删一律不可达。
func TestAgentIdentityPrivateByDefault(t *testing.T) {
	_, h := newP0TestHandler(t)
	code, body := p0Call(t, h, http.MethodPost, "/api/v1/ai/agents", "u1", "viewer",
		map[string]any{"name": "私有巡检员", "model_name": "deepseek-chat"})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	created := decodeMap(t, body)
	id, _ := created["id"].(string)
	if id == "" || created["owner_id"] != "u1" || created["visibility"] != "private" {
		t.Fatalf("identity not stamped: %+v", created)
	}

	code, body = p0Call(t, h, http.MethodGet, "/api/v1/ai/agents", "u2", "viewer", nil)
	if code != 200 {
		t.Fatalf("list = %d", code)
	}
	var list []map[string]any
	_ = json.Unmarshal(body, &list)
	for _, a := range list {
		if a["id"] == id {
			t.Fatal("private agent leaked into another user's list")
		}
	}

	if code, _ = p0Call(t, h, http.MethodGet, "/api/v1/ai/agents/"+id, "u2", "viewer", nil); code != 404 {
		t.Errorf("private agent detail should be 404, got %d", code)
	}
	if code, _ = p0Call(t, h, http.MethodPut, "/api/v1/ai/agents/"+id, "u2", "viewer",
		map[string]any{"name": "hacked"}); code != 404 {
		t.Errorf("private agent update should be 404, got %d", code)
	}
	if code, _ = p0Call(t, h, http.MethodDelete, "/api/v1/ai/agents/"+id, "u2", "viewer", nil); code != 404 {
		t.Errorf("private agent delete should be 404, got %d", code)
	}

	code, body = p0Call(t, h, http.MethodGet, "/api/v1/ai/agents/"+id, "u1", "viewer", nil)
	if code != 200 || decodeMap(t, body)["permission"] != "owner" {
		t.Fatalf("owner detail = %d %s", code, body)
	}
}

// 显式设为 public 后同租户可见但不可改; 管理员具备治理级 owner 权限。
func TestAgentVisibilityPublicAndAdminOverride(t *testing.T) {
	_, h := newP0TestHandler(t)
	code, body := p0Call(t, h, http.MethodPost, "/api/v1/ai/agents", "u1", "viewer",
		map[string]any{"name": "公开助手", "visibility": "public"})
	if code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}
	id := decodeMap(t, body)["id"].(string)

	code, body = p0Call(t, h, http.MethodGet, "/api/v1/ai/agents", "u2", "viewer", nil)
	var list []map[string]any
	_ = json.Unmarshal(body, &list)
	found := false
	for _, a := range list {
		if a["id"] == id {
			found = true
			if a["permission"] != "view" {
				t.Errorf("public agent permission = %v, want view", a["permission"])
			}
		}
	}
	if !found {
		t.Fatal("public agent missing from another user's list")
	}
	if code, _ = p0Call(t, h, http.MethodPut, "/api/v1/ai/agents/"+id, "u2", "viewer",
		map[string]any{"name": "hacked"}); code != 403 {
		t.Errorf("view-only update should be 403, got %d", code)
	}
	code, body = p0Call(t, h, http.MethodGet, "/api/v1/ai/agents/"+id, "u2", "admin", nil)
	if code != 200 || decodeMap(t, body)["permission"] != "owner" {
		t.Fatalf("admin governance access = %d %s", code, body)
	}
}

// 轨迹端点: 生产已装配 recorder + 强制按租户过滤 + OTel 导出。
func TestTrajectoryEndpointsTenantScoped(t *testing.T) {
	svc, h := newP0TestHandler(t)
	if svc.trajRecorder == nil {
		t.Fatal("production trajectory recorder is not assembled")
	}
	ctx := context.Background()
	for _, tc := range []struct{ id, tenant string }{{"turn-t1", "t1"}, {"turn-t2", "t2"}} {
		turn := &agentic.TrajectoryTurn{
			ID: tc.id, TenantID: tc.tenant, ConversationID: "conv-1", AgentID: "a1",
			Steps: []agentic.TrajectoryStep{{Index: 0, Model: "m1", Finished: true}},
		}
		agentic.FinalizeTrajectory(turn)
		if err := svc.trajRecorder.RecordTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}

	code, body := p0Call(t, h, http.MethodGet, "/api/v1/ai/trajectories?conversation_id=conv-1", "u1", "viewer", nil)
	if code != 200 {
		t.Fatalf("list trajectories = %d %s", code, body)
	}
	var turns []map[string]any
	if err := json.Unmarshal(body, &turns); err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0]["id"] != "turn-t1" {
		t.Fatalf("cross-tenant trajectory leaked: %s", body)
	}
	if code, _ = p0Call(t, h, http.MethodGet, "/api/v1/ai/trajectories/turn-t2", "u1", "viewer", nil); code != 404 {
		t.Errorf("cross-tenant turn should be 404, got %d", code)
	}
	code, body = p0Call(t, h, http.MethodGet, "/api/v1/ai/trajectories/turn-t1/otel", "u1", "viewer", nil)
	if code != 200 {
		t.Fatalf("otel export = %d %s", code, body)
	}
	otel := decodeMap(t, body)
	if _, ok := otel["resourceSpans"]; !ok {
		t.Fatalf("OTel export missing resourceSpans: %s", body)
	}
}

// 无 DB(本地/单测)时审批策略接口仍可用, 内存兜底生效。
func TestApprovalPolicyHandlerFallbackWithoutDB(t *testing.T) {
	_, h := newP0TestHandler(t)
	code, body := p0Call(t, h, http.MethodPut, "/api/v1/ai/approval-policy", "u1", "admin",
		map[string]any{"mode": "always_ask", "ask_timeout_sec": 15})
	if code != 200 {
		t.Fatalf("put policy = %d %s", code, body)
	}
	code, body = p0Call(t, h, http.MethodGet, "/api/v1/ai/approval-policy", "u1", "admin", nil)
	if code != 200 {
		t.Fatalf("get policy = %d", code)
	}
	policy, ok := decodeMap(t, body)["policy"].(map[string]any)
	if !ok || policy["mode"] != "always_ask" || policy["ask_timeout_sec"] != float64(15) {
		t.Fatalf("policy fallback broken: %s", body)
	}
}

// 新增权限点与路径映射必须显式登记, 防止 RBAC fail-open。
func TestGovernanceRBACRegistration(t *testing.T) {
	if auth.PathPermissionMapping["GET /api/v1/ai/trajectories"] != auth.PermissionAITraceRead {
		t.Error("trajectory endpoints not mapped to ai:trace:read")
	}
	found := false
	for _, p := range auth.AllPermissions {
		if p == auth.PermissionAITraceRead {
			found = true
		}
	}
	if !found {
		t.Error("ai:trace:read missing from AllPermissions")
	}
	if !auth.HasPermission(auth.RoleAdmin, auth.PermissionAITraceRead) {
		t.Error("admin should have ai:trace:read")
	}
	if auth.HasPermission(auth.RoleViewer, auth.PermissionAITraceRead) {
		t.Error("viewer must not have ai:trace:read")
	}
}
