package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// withRole 把角色写入请求 context，模拟认证中间件注入的声明。
func withRole(req *http.Request, role string) *http.Request {
	if role == "" {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), "role", role))
}

// TestAutoPermissionMiddleware_CMDB 验证 CMDB 各路由被正确 RBAC 管制：
//   - viewer 仅可读，写/删均 403
//   - operator 可读写数据，但模型管理(model-type)仅 admin 可写
//   - admin 全部放行
//   - 未携带角色 → 401
//
// 重点覆盖此前 model-type→cmdb:admin 专属映射因路径前缀截断而失效的缺陷。
func TestAutoPermissionMiddleware_CMDB(t *testing.T) {
	m := NewRBACMiddleware(&Provider{})

	reach := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := m.AutoPermissionMiddleware(reach)

	cases := []struct {
		name   string
		role   string
		method string
		path   string
		want   int
	}{
		// 无角色 → 401
		{"no role unauthorized", "", "GET", "/api/v1/cmdb/ci", http.StatusUnauthorized},

		// viewer：只读
		{"viewer read ci list", "viewer", "GET", "/api/v1/cmdb/ci", http.StatusOK},
		{"viewer read ci by id", "viewer", "GET", "/api/v1/cmdb/ci/mt-123", http.StatusOK},
		{"viewer read search", "viewer", "GET", "/api/v1/cmdb/search", http.StatusOK},
		{"viewer read model-type", "viewer", "GET", "/api/v1/cmdb/model-type", http.StatusOK},
		{"viewer read collection-tasks", "viewer", "GET", "/api/v1/cmdb/collection-tasks", http.StatusOK},
		{"viewer create ci forbidden", "viewer", "POST", "/api/v1/cmdb/ci", http.StatusForbidden},
		{"viewer delete ci forbidden", "viewer", "DELETE", "/api/v1/cmdb/ci/mt-123", http.StatusForbidden},

		// operator：可读写数据，但删除(cmdb:delete)与模型管理(cmdb:admin)需 admin
		{"operator create ci", "operator", "POST", "/api/v1/cmdb/ci", http.StatusOK},
		{"operator update ci", "operator", "PUT", "/api/v1/cmdb/ci/mt-123", http.StatusOK},
		{"operator delete ci forbidden", "operator", "DELETE", "/api/v1/cmdb/ci/mt-123", http.StatusForbidden},
		{"operator relation write", "operator", "POST", "/api/v1/cmdb/relation", http.StatusOK},
		{"operator onboard", "operator", "POST", "/api/v1/cmdb/onboard", http.StatusOK},
		{"operator run collection task", "operator", "POST", "/api/v1/cmdb/collection-tasks/t1/run", http.StatusOK},
		// 关键修复点：model-type 写操作必须 admin，operator 应被拒绝
		{"operator create model-type forbidden", "operator", "POST", "/api/v1/cmdb/model-type", http.StatusForbidden},
		{"operator update model-type forbidden", "operator", "PUT", "/api/v1/cmdb/model-type/mt1", http.StatusForbidden},
		{"operator delete model-type forbidden", "operator", "DELETE", "/api/v1/cmdb/model-type/mt1", http.StatusForbidden},

		// admin：全部放行（含 model-type 管理与删除）
		{"admin create model-type", "admin", "POST", "/api/v1/cmdb/model-type", http.StatusOK},
		{"admin delete model-type", "admin", "DELETE", "/api/v1/cmdb/model-type/mt1", http.StatusOK},
		{"admin delete ci", "admin", "DELETE", "/api/v1/cmdb/ci/mt-123", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			req = withRole(req, c.role)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("method=%s path=%s role=%q: got %d want %d", c.method, c.path, c.role, rec.Code, c.want)
			}
		})
	}
}

// TestResolvePathPermission 单测前缀解析：更长前缀优先。
func TestResolvePathPermission(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
		ok     bool
	}{
		{"GET", "/api/v1/cmdb/ci", PermissionCMDBRead, true},
		{"POST", "/api/v1/cmdb/ci", PermissionCMDBWrite, true},
		{"POST", "/api/v1/cmdb/model-type", PermissionCMDBAdmin, true},
		{"DELETE", "/api/v1/cmdb/model-type/mt1", PermissionCMDBAdmin, true},
		{"POST", "/api/v1/cmdb/ci/abc", PermissionCMDBWrite, true},
		{"GET", "/api/v1/unknown/xyz", "", false},
	}
	for _, c := range cases {
		got, ok := resolvePathPermission(c.method, c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("resolvePathPermission(%s,%s) = (%q,%v), want (%q,%v)", c.method, c.path, got, ok, c.want, c.ok)
		}
	}
}

// withTenant 把 tenant 写入 context，模拟认证中间件注入的租户声明。
func withTenant(req *http.Request, tenant string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), "tenant_id", tenant))
}

// TestAutoPermissionMiddleware_Resolver 验证中间件在注入 resolver 后,
// 以 resolver 返回的权限为准(而非写死的 RolePermissions),使角色管理页的编辑实时生效。
// 这里用一个内存 map 模拟 IAM 角色库,无需 PG。
func TestAutoPermissionMiddleware_Resolver(t *testing.T) {
	// "unknown tenant falls back" 子用例依赖多租户语义(未带租户时应回落写死映射),
	// 单租户模式会强制 "default" 使该路径不可达,故在此启用多租户。
	prev := MultiTenantEnabled()
	SetMultiTenant(true)
	defer SetMultiTenant(prev)

	// 模拟 IAM 角色库:operator 被收窄为仅 cmdb:read(不再有 cmdb:write)
	iamRoles := map[string][]string{
		"operator": {PermissionCMDBRead},
		"editor":   {PermissionCMDBRead, PermissionCMDBWrite, PermissionCMDBDelete},
	}
	m := NewRBACMiddleware(&Provider{})
	m.SetPermissionResolver(func(tenantID, role string) []string {
		if tenantID != "default" {
			return nil
		}
		return iamRoles[role]
	})

	reach := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := m.AutoPermissionMiddleware(reach)

	cases := []struct {
		name   string
		role   string
		method string
		path   string
		want   int
	}{
		// resolver 把 operator 收窄为只读:写/删均 403(即便写死映射里 operator 可写)
		{"resolver operator read ok", "operator", "GET", "/api/v1/cmdb/ci", http.StatusOK},
		{"resolver operator write forbidden", "operator", "POST", "/api/v1/cmdb/ci", http.StatusForbidden},
		{"resolver operator delete forbidden", "operator", "DELETE", "/api/v1/cmdb/ci/x", http.StatusForbidden},
		// editor 由 resolver 授予 cmdb:delete:可删
		{"resolver editor delete ok", "editor", "DELETE", "/api/v1/cmdb/ci/x", http.StatusOK},
		// 未知租户:resolver 返回 nil → 回落写死映射(operator 可写)
		{"unknown tenant falls back", "operator", "POST", "/api/v1/cmdb/ci", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			req = withRole(req, c.role)
			if c.name != "unknown tenant falls back" {
				req = withTenant(req, "default")
			}
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("method=%s path=%s role=%q: got %d want %d", c.method, c.path, c.role, rec.Code, c.want)
			}
		})
	}
}
