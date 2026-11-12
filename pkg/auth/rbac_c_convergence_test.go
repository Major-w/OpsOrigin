package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// C 收拢(POST /ai/chat 统一入口 + executor 分流)的 RBAC 回归。
//
// 背景: 团队对话原走独立端点 POST /api/v1/ai/teams/{id}/chat(P2 纯 data 协议),
// M1 后前端改走统一入口 POST /api/v1/ai/chat, 在请求体带 executor={type:team}。
// executor 只存在于 body, 不参与 URL 匹配 —— 权限裁决完全由路径前缀决定。
// 本用例钉死「收拢前后三角色权限语义一致」, 防止将来给旧别名补充
// ActionPermissionMapping 提升规则(如 ai:tool:exec)时只改一处, 造成统一入口越权。
//
// 注: 前端 ai/page.tsx 用 useCan('ai:tool:exec') 作为「能否使用 AI」的门禁,
// 导致 viewer 连提问入口都被隐藏 —— 属既有产品行为, 非本轮收拢引入, 后端测试不覆盖。
const (
	chatUnifiedPath    = "/api/v1/ai/chat"              // M1 统一入口
	chatLegacyTeamPath = "/api/v1/ai/teams/team-1/chat" // M0 保留的 P2 旧别名
	traceListPath      = "/api/v1/ai/trajectories"      // M3 轨迹验收端点
)

// TestCConvergenceChatPermissionResolve 锁定解析层: 两个端点必须同归 ai:write 且显式登记,
// 不得命中 ActionPermissionMapping 的高危提升规则, 也不得漏登记而 fail-open。
func TestCConvergenceChatPermissionResolve(t *testing.T) {
	resolved := make(map[string]string, 2)
	for _, path := range []string{chatUnifiedPath, chatLegacyTeamPath} {
		t.Run(path, func(t *testing.T) {
			got, ok := resolvePathPermission(http.MethodPost, path)
			if !ok {
				t.Fatalf("POST %s 未登记权限映射: 中间件将 fail-open(仅认证不鉴权)", path)
			}
			if got != PermissionAIWrite {
				t.Errorf("POST %s 解析到 %q, want %q(收拢前后权限点必须一致)", path, got, PermissionAIWrite)
			}
			resolved[path] = got
		})
	}
	if resolved[chatUnifiedPath] != resolved[chatLegacyTeamPath] {
		t.Errorf("统一入口与旧别名权限点不一致: %q vs %q", resolved[chatUnifiedPath], resolved[chatLegacyTeamPath])
	}

	// 轨迹端点必须收敛到 ai:trace:read, 不回落 ai:read(viewer 可见即为泄漏)。
	for _, path := range []string{
		traceListPath,
		traceListPath + "/turn-1",
		traceListPath + "/turn-1/otel",
	} {
		got, ok := resolvePathPermission(http.MethodGet, path)
		if !ok || got != PermissionAITraceRead {
			t.Errorf("GET %s = (%q,%v), want (%q,true)", path, got, ok, PermissionAITraceRead)
		}
	}
}

// TestCConvergenceChatRBACParity 穿过真实 AutoPermissionMiddleware 断言:
// admin/operator 可用、viewer 拒绝、无身份 401, 且统一入口与旧别名逐角色结果相同。
func TestCConvergenceChatRBACParity(t *testing.T) {
	m := NewRBACMiddleware(&Provider{})
	mw := m.AutoPermissionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name string
		role string
		want int
	}{
		{"no identity", "", http.StatusUnauthorized},
		{"admin", RoleAdmin, http.StatusOK},
		{"operator", RoleOperator, http.StatusOK},
		{"viewer", RoleViewer, http.StatusForbidden},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codes := make(map[string]int, 2)
			for _, path := range []string{chatUnifiedPath, chatLegacyTeamPath} {
				req := withRole(httptest.NewRequest(http.MethodPost, path, nil), c.role)
				rec := httptest.NewRecorder()
				mw.ServeHTTP(rec, req)
				if rec.Code != c.want {
					t.Errorf("POST %s role=%q: got %d want %d", path, c.role, rec.Code, c.want)
				}
				codes[path] = rec.Code
			}
			if codes[chatUnifiedPath] != codes[chatLegacyTeamPath] {
				t.Errorf("权限不一致(role=%q): 统一入口=%d 旧别名=%d",
					c.role, codes[chatUnifiedPath], codes[chatLegacyTeamPath])
			}
		})
	}
}

// TestCConvergenceTrajectoryRBAC 补齐 M3 验收点 3 的权限面:
// 团队 turn 写入后, 只读账号不得通过轨迹列表/详情/OTel 导出读取运行态数据。
func TestCConvergenceTrajectoryRBAC(t *testing.T) {
	m := NewRBACMiddleware(&Provider{})
	mw := m.AutoPermissionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	paths := []string{
		traceListPath,
		traceListPath + "/turn-1",
		traceListPath + "/turn-1/otel",
	}
	cases := []struct {
		name string
		role string
		want int
	}{
		{"no identity", "", http.StatusUnauthorized},
		{"admin", RoleAdmin, http.StatusOK},
		{"operator", RoleOperator, http.StatusOK},
		{"viewer", RoleViewer, http.StatusForbidden},
	}

	for _, c := range cases {
		for _, path := range paths {
			t.Run(c.name+" "+path, func(t *testing.T) {
				req := withRole(httptest.NewRequest(http.MethodGet, path, nil), c.role)
				rec := httptest.NewRecorder()
				mw.ServeHTTP(rec, req)
				if rec.Code != c.want {
					t.Errorf("GET %s role=%q: got %d want %d", path, c.role, rec.Code, c.want)
				}
			})
		}
	}
}
