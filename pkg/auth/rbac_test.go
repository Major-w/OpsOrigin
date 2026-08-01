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

		// operator：可读写数据，但模型管理需 admin
		{"operator create ci", "operator", "POST", "/api/v1/cmdb/ci", http.StatusOK},
		{"operator update ci", "operator", "PUT", "/api/v1/cmdb/ci/mt-123", http.StatusOK},
		{"operator delete ci", "operator", "DELETE", "/api/v1/cmdb/ci/mt-123", http.StatusOK},
		{"operator relation write", "operator", "POST", "/api/v1/cmdb/relation", http.StatusOK},
		{"operator onboard", "operator", "POST", "/api/v1/cmdb/onboard", http.StatusOK},
		{"operator run collection task", "operator", "POST", "/api/v1/cmdb/collection-tasks/t1/run", http.StatusOK},
		// 关键修复点：model-type 写操作必须 admin，operator 应被拒绝
		{"operator create model-type forbidden", "operator", "POST", "/api/v1/cmdb/model-type", http.StatusForbidden},
		{"operator update model-type forbidden", "operator", "PUT", "/api/v1/cmdb/model-type/mt1", http.StatusForbidden},
		{"operator delete model-type forbidden", "operator", "DELETE", "/api/v1/cmdb/model-type/mt1", http.StatusForbidden},

		// admin：全部放行（含 model-type 管理）
		{"admin create model-type", "admin", "POST", "/api/v1/cmdb/model-type", http.StatusOK},
		{"admin delete model-type", "admin", "DELETE", "/api/v1/cmdb/model-type/mt1", http.StatusOK},
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
