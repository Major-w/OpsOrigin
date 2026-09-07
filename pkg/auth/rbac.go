package auth

import (
	"net/http"
	"strings"
)

// Permission 权限常量
const (
	PermissionCMDBRead     = "cmdb:read"
	PermissionCMDBWrite    = "cmdb:write"
	PermissionCMDBAdmin    = "cmdb:admin"
	PermissionCMDBDelete   = "cmdb:delete"
	PermissionMonitorRead  = "monitor:read"
	PermissionMonitorWrite = "monitor:write"
	PermissionITSMRead     = "itsm:read"
	PermissionITSMWrite    = "itsm:write"
	PermissionITSMApprove  = "itsm:approve"
	PermissionUserRead     = "user:read"
	PermissionUserWrite    = "user:write"
	PermissionAgentRead    = "agent:read"
	PermissionAgentWrite   = "agent:write"
	PermissionAuditRead    = "audit:read"
	PermissionSystemAdmin  = "system:admin"
	// PermissionAIToolExec 允许调用 /api/v1/ai/tools/{name}/execute 直接执行运维工具
	// (含 script_run 在受管主机上执行脚本,属高风险操作)。
	PermissionAIToolExec = "ai:tool:exec"
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
		PermissionCMDBRead, PermissionCMDBWrite, PermissionCMDBAdmin, PermissionCMDBDelete,
		PermissionMonitorRead, PermissionMonitorWrite,
		PermissionITSMRead, PermissionITSMWrite, PermissionITSMApprove,
		PermissionUserRead, PermissionUserWrite,
		PermissionAgentRead, PermissionAgentWrite,
		PermissionAuditRead, PermissionSystemAdmin,
		PermissionAIToolExec,
	},
	RoleOperator: {
		PermissionCMDBRead, PermissionCMDBWrite,
		PermissionMonitorRead, PermissionMonitorWrite,
		PermissionITSMRead, PermissionITSMWrite,
		PermissionUserRead,
		PermissionAgentRead, PermissionAgentWrite,
		PermissionAuditRead, PermissionAIToolExec,
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
	// resolver 从外部(如 IAM 角色库)按 (tenantID, role) 解析角色权限。
	// 若设置且返回非空,则取代写死的 RolePermissions,使角色管理页的权限配置实时生效。
	resolver func(tenantID, role string) []string
}

// NewRBACMiddleware 创建新的RBAC中间件
func NewRBACMiddleware(provider *Provider) *RBACMiddleware {
	return &RBACMiddleware{provider: provider}
}

// SetPermissionResolver 注入角色权限解析器(通常来自 IAM 服务)。
// 注入后,鉴权将以 IAM 角色库中的权限为准,回落到 RolePermissions 兜底。
func (m *RBACMiddleware) SetPermissionResolver(fn func(tenantID, role string) []string) {
	m.resolver = fn
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

			if !m.hasPerm(TenantIDFromContext(r.Context()), role, permission) {
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

			tenantID := TenantIDFromContext(r.Context())
			hasAny := false
			for _, perm := range permissions {
				if m.hasPerm(tenantID, role, perm) {
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

// resolvePerms 解析某角色的最终权限列表:优先使用注入的 resolver(IAM 角色库),
// 若 resolver 未设置或返回空则回落到写死的 RolePermissions(保证内置角色始终可用)。
func (m *RBACMiddleware) resolvePerms(tenantID, role string) []string {
	if m.resolver != nil {
		if perms := m.resolver(tenantID, role); len(perms) > 0 {
			return perms
		}
	}
	return RolePermissions[role]
}

// hasPerm 检查 (tenantID, role) 是否拥有指定权限。
func (m *RBACMiddleware) hasPerm(tenantID, role, permission string) bool {
	for _, p := range m.resolvePerms(tenantID, role) {
		if p == permission {
			return true
		}
	}
	return false
}

// HasPermission 检查角色是否有特定权限(无 tenant 上下文时按写死映射兜底)。
func (m *RBACMiddleware) HasPermission(role, permission string) bool {
	return m.hasPerm("", role, permission)
}

// HasPermission 包级便捷函数:检查某角色是否拥有指定权限(无需构造 RBACMiddleware)。
func HasPermission(role, permission string) bool {
	perms, ok := RolePermissions[role]
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == permission {
			return true
		}
	}
	return false
}

// GetRolePermissions 获取角色的所有权限
func (m *RBACMiddleware) GetRolePermissions(role string) []string {
	return m.resolvePerms("", role)
}

// PathPermissionMapping 路径权限映射
var PathPermissionMapping = map[string]string{
	"GET /api/v1/cmdb":               PermissionCMDBRead,
	"POST /api/v1/cmdb":              PermissionCMDBWrite,
	"PUT /api/v1/cmdb":               PermissionCMDBWrite,
	"DELETE /api/v1/cmdb":            PermissionCMDBDelete,
	"GET /api/v1/cmdb/model-type":    PermissionCMDBRead,
	"POST /api/v1/cmdb/model-type":   PermissionCMDBAdmin,
	"PUT /api/v1/cmdb/model-type":    PermissionCMDBAdmin,
	"DELETE /api/v1/cmdb/model-type": PermissionCMDBAdmin,
	"GET /api/v1/monitor":            PermissionMonitorRead,
	"POST /api/v1/monitor":           PermissionMonitorWrite,
	"PUT /api/v1/monitor":            PermissionMonitorWrite,
	"DELETE /api/v1/monitor":         PermissionMonitorWrite,
	"GET /api/v1/itsm":               PermissionITSMRead,
	"POST /api/v1/itsm":              PermissionITSMWrite,
	"PUT /api/v1/itsm":               PermissionITSMWrite,
	"DELETE /api/v1/itsm":            PermissionITSMWrite,
	"GET /api/v1/users":              PermissionUserRead,
	"POST /api/v1/users":             PermissionUserWrite,
	"PUT /api/v1/users":              PermissionUserWrite,
	"DELETE /api/v1/users":           PermissionUserWrite,
	"GET /api/v1/agents":             PermissionAgentRead,
	"POST /api/v1/agents":            PermissionAgentWrite,
	"PUT /api/v1/agents":             PermissionAgentWrite,
	"DELETE /api/v1/agents":          PermissionAgentWrite,
	"GET /api/v1/audit":              PermissionAuditRead,
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
	// ChatOps 渠道管理:IM 渠道凭据属系统级敏感配置(system:admin);
	// 平台回调 /api/v1/chatops/callback/ 走 bypass 前缀, 不经过本中间件。
	"GET /api/v1/chatops":    PermissionSystemAdmin,
	"POST /api/v1/chatops":   PermissionSystemAdmin,
	"PUT /api/v1/chatops":    PermissionSystemAdmin,
	"DELETE /api/v1/chatops": PermissionSystemAdmin,
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

		tenantID := TenantIDFromContext(r.Context())

		// 检查路径权限（最长前缀优先匹配，使 /api/v1/cmdb/model-type 等
		// 专属映射能命中，而非一律回落到 /api/v1/cmdb 的通用映射）
		if permission, ok := resolvePathPermission(r.Method, r.URL.Path); ok {
			if !m.hasPerm(tenantID, role, permission) {
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
		"/api/v1/agents/sandbox",
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

// resolvePathPermission 按最长路径前缀匹配权限映射。
// 从完整路径逐级向上收敛到 4 段前缀(/api/v1/<mod>)，返回第一个命中的权限。
// 这样 /api/v1/cmdb/model-type 的 cmdb:admin 专属映射可命中，
// 而 /api/v1/cmdb/ci/{id} 等仍回落到 /api/v1/cmdb 的 cmdb:write。
func resolvePathPermission(method, path string) (string, bool) {
	parts := strings.Split(path, "/")
	for i := len(parts); i >= 4; i-- {
		key := method + " " + strings.Join(parts[:i], "/")
		if perm, ok := PathPermissionMapping[key]; ok {
			return perm, true
		}
	}
	return "", false
}
