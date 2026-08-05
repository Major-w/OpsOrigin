package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opsorigin/core/pkg/principal"
)

// captureHandler 记录中间件放行后 context 中的主体。
func captureHandler(got *principal.Principal, seenHeaders *http.Header) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := principal.FromContext(r.Context())
		*got = p
		if seenHeaders != nil {
			*seenHeaders = r.Header.Clone()
		}
		w.WriteHeader(http.StatusOK)
	})
}

func newTestProvider(t *testing.T) (*Provider, string) {
	t.Helper()
	p := NewProvider("unit-test-secret-not-the-dev-default", nil, nil)
	token, err := p.GenerateToken("u-1001", "t-acme", RoleOperator, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return p, token
}

// 核心回归:登录用户伪造 X-Principal-ID 冒充他人,必须无效。
func TestMiddlewareRejectsForgedPrincipalHeader(t *testing.T) {
	p, token := newTestProvider(t)

	var got principal.Principal
	var seen http.Header
	h := p.Middleware(captureHandler(&got, &seen))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/cmdb", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Principal-Type", "HUMAN")
	r.Header.Set("X-Principal-ID", "victim-admin") // 伪造
	r.Header.Set("X-Permissions", "system:admin")  // 自报提权
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("合法 Bearer 请求应放行, got %d", w.Code)
	}
	if got.ID != "u-1001" {
		t.Fatalf("主体必须从 JWT 派生,得到 %q(伪造头被采信=越权)", got.ID)
	}
	if got.Has("system:admin") {
		t.Fatal("客户端自报的 X-Permissions 绝不能进入权限集合")
	}
	// 防御纵深:下游读到的请求头必须已被剥离
	if seen.Get("X-Principal-ID") != "" || seen.Get("X-Permissions") != "" {
		t.Fatalf("不可信断言头未被剥离: principal=%q perms=%q",
			seen.Get("X-Principal-ID"), seen.Get("X-Permissions"))
	}
}

func TestMiddlewareDerivesPrincipalFromJWT(t *testing.T) {
	p, token := newTestProvider(t)

	var got principal.Principal
	h := p.Middleware(captureHandler(&got, nil))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/cmdb", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got.Type != principal.TypeHuman || got.ID != "u-1001" ||
		got.TenantID != "t-acme" || got.Role != RoleOperator {
		t.Fatalf("主体与 JWT claims 不一致: %+v", got)
	}
	// enricher 未注入时回落内置角色映射,而非空权限
	if !got.Has(PermissionCMDBRead) || got.Has(PermissionSystemAdmin) {
		t.Fatalf("回落权限集不正确: %v", got.Permissions)
	}
}

// 内部服务凭证有效时,断言头才被采信。
func TestMiddlewareTrustsAssertionOnlyWithServiceToken(t *testing.T) {
	p, _ := newTestProvider(t)
	p.SetServiceToken("s3cr3t-internal")

	var got principal.Principal
	h := p.Middleware(captureHandler(&got, nil))

	// 1) 凭证正确 → 采信断言
	r := httptest.NewRequest(http.MethodPost, "/api/v1/cmdb", nil)
	r.Header.Set("X-Internal-Token", "s3cr3t-internal")
	r.Header.Set("X-Principal-Type", "AI_AGENT")
	r.Header.Set("X-Principal-ID", "agent-inspector")
	r.Header.Set("X-Principal-Ticket", "TK-2026-001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("可信服务调用应放行, got %d", w.Code)
	}
	if !got.IsAI() || got.ID != "agent-inspector" || got.TicketID != "TK-2026-001" {
		t.Fatalf("断言主体解析错误: %+v", got)
	}

	// 2) 凭证错误 → 退化为普通请求,缺 Authorization 应 401
	got = principal.Principal{}
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/cmdb", nil)
	r2.Header.Set("X-Internal-Token", "wrong-token")
	r2.Header.Set("X-Principal-Type", "AI_AGENT")
	r2.Header.Set("X-Principal-ID", "agent-inspector")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("错误内部凭证必须拒绝, got %d", w2.Code)
	}

	// 3) 凭证正确但断言类型非法 → 401
	r3 := httptest.NewRequest(http.MethodPost, "/api/v1/cmdb", nil)
	r3.Header.Set("X-Internal-Token", "s3cr3t-internal")
	r3.Header.Set("X-Principal-Type", "GHOST")
	r3.Header.Set("X-Principal-ID", "x")
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, r3)
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("非法断言类型必须拒绝, got %d", w3.Code)
	}
}

// 未配置 serviceToken 时,断言头通道整体关闭(fail-closed)。
func TestAssertionChannelClosedByDefault(t *testing.T) {
	p, _ := newTestProvider(t)

	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodPost, "/api/v1/cmdb", nil)
	r.Header.Set("X-Internal-Token", "")
	r.Header.Set("X-Principal-Type", "AI_AGENT")
	r.Header.Set("X-Principal-ID", "agent-x")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未配置内部凭证时断言头不得放行, got %d", w.Code)
	}
}

// DEV 模式只写 context,不得回写断言请求头。
func TestDevModeDoesNotWriteAssertionHeaders(t *testing.T) {
	p := NewProvider("opsnexus-dev-secret-do-not-use-in-production", nil, nil)

	var got principal.Principal
	var seen http.Header
	h := p.Middleware(captureHandler(&got, &seen))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/cmdb", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got.ID != "dev" || got.Role != RoleAdmin {
		t.Fatalf("dev 主体注入失败: %+v", got)
	}
	if seen.Get("X-Principal-ID") != "" || seen.Get("X-Principal-Type") != "" {
		t.Fatal("dev 模式不得把断言头写回请求 —— 断言头只能由可信通道产生")
	}
	if !got.Has(PermissionSystemAdmin) {
		t.Fatalf("dev admin 应具备完整权限: %v", got.Permissions)
	}
}

type stubEnricher struct {
	perms []string
	name  string
}

func (s stubEnricher) Permissions(_ context.Context, _, _, _ string) []string { return s.perms }
func (s stubEnricher) DisplayName(_ context.Context, _, _ string) string      { return s.name }

func TestEnricherFillsPermissionsAndDisplayName(t *testing.T) {
	p, token := newTestProvider(t)
	p.SetPrincipalEnricher(stubEnricher{perms: []string{"cmdb:read", "itsm:*"}, name: "张三"})

	var got principal.Principal
	h := p.Middleware(captureHandler(&got, nil))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/cmdb", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got.DisplayName != "张三" {
		t.Fatalf("展示名未回填: %+v", got)
	}
	if !got.Has("itsm:approve") || got.Has("system:admin") {
		t.Fatalf("权限应以 enricher 为准: %v", got.Permissions)
	}
}

func TestLegacyContextHelpersStillWork(t *testing.T) {
	p, token := newTestProvider(t)

	var ctx context.Context
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx = r.Context()
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/cmdb", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), r)

	if UserIDFromContext(ctx) != "u-1001" || TenantIDFromContext(ctx) != "t-acme" ||
		RoleFromContext(ctx) != RoleOperator || TokenFromContext(ctx) != token {
		t.Fatal("旧 *FromContext 兼容入口取值失败")
	}

	// 业务代码手工切换租户(如机器人入站建单)必须能覆盖主体租户
	//nolint:staticcheck // 复现既有调用方的裸 key 用法
	switched := context.WithValue(ctx, legacyKeyTenantID, "t-other")
	if TenantIDFromContext(switched) != "t-other" {
		t.Fatal("裸 key 临时租户切换被主体值覆盖,会破坏既有调用方")
	}
}
