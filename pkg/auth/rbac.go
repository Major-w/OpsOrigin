package auth

import (
	"net/http"
	"strings"
)

// Permission 权限常量
const (
	PermissionCMDBRead    = "cmdb:read"
	PermissionCMDBWrite   = "cmdb:write"
	PermissionCMDBAdmin   = "cmdb:admin"
	PermissionMonitorRead = "monitor:read"
	PermissionMonitorWrite = "monitor:write"
	PermissionITSMRead    = "itsm:read"
	PermissionITSMWrite   = "itsm:write"
	PermissionITSMApprove = "itsm:approve"
	PermissionUserRead    = "user:read"
	PermissionUserWrite   = "user:write"
	PermissionAgentRead   = "agent:read"
	PermissionAgentWrite  = "agent:write"
	PermissionAuditRead   = "audit:read"
	PermissionSystemAdmin = "system:admin"
)

// Role 角色常量
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// RolePermissions 角色默认权限映射
var RolePermissions = map[string][]string{
	RoleAdmin: {
		PermissionCMDBRead, PermissionCMDBWrite, PermissionCMDBAdmin,
		PermissionMonitorRead, PermissionMonitorWrite,
		PermissionITSMRead, PermissionITSMWrite, PermissionITSMApprove,
		PermissionUserRead, PermissionUserWrite,
		PermissionAgentRead, PermissionAgentWrite,
		PermissionAuditRead, PermissionSystemAdmin,
	},
	RoleOperator: {
		PermissionCMDBRead, PermissionCMDBWrite,
		PermissionMonitorRead, PermissionMonitorWrite,
		PermissionITSMRead, PermissionITSMWrite,
		PermissionUserRead,
		PermissionAgentRead, PermissionAgentWrite,
		PermissionAuditRead,
	},
	RoleViewer: {
		PermissionCMDBRead,
		PermissionMonitorRead,
		PermissionITSMRead,
		PermissionUserRead,
		PermissionAgentRead,
		PermissionAuditRead,
	},
}

// RBACMiddleware RBAC权限中间件
type RBACMiddleware struct {
	provider *Provider
}

// NewRBACMiddleware 创建新的RBAC中间件
func NewRBACMiddleware(provider *Provider) *RBACMiddleware {
	return &RBACMiddleware{provider: provider}
}

// RequirePermission 要求特定权限的中间件
func (m *RBACMiddleware) RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := RoleFromContext(r.Context())
			if role == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if !m.HasPermission(role, permission) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAnyPermission 要求任一权限的中间件
func (m *RBACMiddleware) RequireAnyPermission(permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := RoleFromContext(r.Context())
			if role == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			hasAny := false
			for _, perm := range permissions {
				if m.HasPermission(role, perm) {
					hasAny = true
					break
				}
			}

			if !hasAny {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole 要求特定角色的中间件
func (m *RBACMiddleware) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := RoleFromContext(r.Context())
			if role == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			hasRole := false
			for _, r := range roles {
				if role == r {
					hasRole = true
					break
				}
			}

			if !hasRole {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// HasPermission 检查角色是否有特定权限
func (m *RBACMiddleware) HasPermission(role, permission string) bool {
	permissions := RolePermissions[role]
	for _, p := range permissions {
		if p == permission {
			return true
		}
	}
	return false
}

// GetRolePermissions 获取角色的所有权限
func (m *RBACMiddleware) GetRolePermissions(role string) []string {
	return RolePermissions[role]
}

// PathPermissionMapping 路径权限映射
var PathPermissionMapping = map[string]string{
	"GET /api/v1/cmdb":              PermissionCMDBRead,
	"POST /api/v1/cmdb":             PermissionCMDBWrite,
	"PUT /api/v1/cmdb":              PermissionCMDBWrite,
	"DELETE /api/v1/cmdb":           PermissionCMDBWrite,
	"GET /api/v1/cmdb/model-type":   PermissionCMDBRead,
	"POST /api/v1/cmdb/model-type":  PermissionCMDBAdmin,
	"PUT /api/v1/cmdb/model-type":   PermissionCMDBAdmin,
	"DELETE /api/v1/cmdb/model-type": PermissionCMDBAdmin,
	"GET /api/v1/monitor":           PermissionMonitorRead,
	"POST /api/v1/monitor":          PermissionMonitorWrite,
	"PUT /api/v1/monitor":           PermissionMonitorWrite,
	"DELETE /api/v1/monitor":        PermissionMonitorWrite,
	"GET /api/v1/itsm":              PermissionITSMRead,
	"POST /api/v1/itsm":             PermissionITSMWrite,
	"PUT /api/v1/itsm":              PermissionITSMWrite,
	"DELETE /api/v1/itsm":           PermissionITSMWrite,
	"GET /api/v1/users":             PermissionUserRead,
	"POST /api/v1/users":            PermissionUserWrite,
	"PUT /api/v1/users":             PermissionUserWrite,
	"DELETE /api/v1/users":          PermissionUserWrite,
	"GET /api/v1/agents":            PermissionAgentRead,
	"POST /api/v1/agents":           PermissionAgentWrite,
	"PUT /api/v1/agents":            PermissionAgentWrite,
	"DELETE /api/v1/agents":         PermissionAgentWrite,
	"GET /api/v1/audit":             PermissionAuditRead,
	// IAM:角色/用户组/权限管理
	"GET /api/v1/iam":    PermissionUserRead,
	"POST /api/v1/iam":   PermissionUserWrite,
	"PUT /api/v1/iam":    PermissionUserWrite,
	"DELETE /api/v1/iam": PermissionUserWrite,
	// 通知管理:渠道/策略管理(user:write),历史查询(audit:read)
	"GET /api/v1/notification":    PermissionUserRead,
	"POST /api/v1/notification":   PermissionUserWrite,
	"PUT /api/v1/notification":    PermissionUserWrite,
	"DELETE /api/v1/notification": PermissionUserWrite,
}

// AutoPermissionMiddleware 自动权限检查中间件
func (m *RBACMiddleware) AutoPermissionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 跳过不需要权限的路径
		if shouldSkipPermission(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// 跳过 Agent 注册(POST /api/v1/agents)免认证
		if r.URL.Path == "/api/v1/agents" && r.Method == "POST" {
			next.ServeHTTP(w, r)
			return
		}

		role := RoleFromContext(r.Context())
		if role == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		// admin拥有所有权限
		if role == RoleAdmin {
			next.ServeHTTP(w, r)
			return
		}

		// 检查路径权限
		key := r.Method + " " + getPathPrefix(r.URL.Path)
		if permission, ok := PathPermissionMapping[key]; ok {
			if !m.HasPermission(role, permission) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// shouldSkipPermission 判断是否应该跳过权限检查
func shouldSkipPermission(path string) bool {
	skipPaths := []string{
		"/api/v1/auth/login",
		"/api/v1/auth/logout",
		"/api/v1/auth/refresh",
		"/api/v1/ops/health",
		"/api/v1/ops/status",
		"/api/v1/users/me",
		"/api/v1/agents/download",
		"/api/v1/agents/install-script",
		"/api/v1/agents/test-ssh",
		"/api/v1/agents/batch-install",
	}
	for _, p := range skipPaths {
		if path == p {
			return true
		}
	}
	// 跳过 Agent 通信路径(心跳/任务/CMDB上报)
	if strings.HasPrefix(path, "/api/v1/agents/") {
		if strings.HasSuffix(path, "/heartbeat") ||
			strings.HasSuffix(path, "/tasks") ||
			strings.HasSuffix(path, "/cmdb") ||
			strings.HasSuffix(path, "/download-tasks") ||
			strings.Contains(path, "/tasks/") {
			return true
		}
	}
	return false
}

// getPathPrefix 获取路径前缀（用于匹配路由）
func getPathPrefix(path string) string {
	// 移除路径参数部分
	parts := strings.Split(path, "/")
	if len(parts) > 4 {
		return strings.Join(parts[:4], "/")
	}
	return path
}