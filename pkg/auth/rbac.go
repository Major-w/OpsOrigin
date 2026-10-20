package auth

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
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
	// PermissionAPIKeySelf 允许管理「属于自己的」API Key(列表/新建/轮换/吊销)。
	// 默认授予所有内置角色:API Key 绑定到用户并继承该用户的权限,
	// 因此自助管理不会带来任何额外授权面(能力上限 = 用户自身权限)。
	PermissionAPIKeySelf = "apikey:self"
	// PermissionVulnRead 查看漏洞扫描任务、漏洞明细与统计。
	PermissionVulnRead = "vuln:read"
	// PermissionVulnWrite 发起/删除扫描,以及处置漏洞(标记修复/忽略/误报)。
	PermissionVulnWrite = "vuln:write"

	// ---- 知识库 ----
	// PermissionKnowledgeRead 查看知识库文章/分类/版本/附件与检索结果。
	PermissionKnowledgeRead = "knowledge:read"
	// PermissionKnowledgeWrite 新建/编辑文章与分类、上传附件、版本回滚。
	PermissionKnowledgeWrite = "knowledge:write"
	// PermissionKnowledgeManage 删除文章/分类、导入、重建向量索引(高危: 影响 AI 检索语料)。
	PermissionKnowledgeManage = "knowledge:manage"

	// ---- AI 助手 ----
	// PermissionAIRead 查看 AI 会话/智能体/提示词/技能/巡检记录等只读资源。
	PermissionAIRead = "ai:read"
	// PermissionAIWrite 管理 AI 会话/智能体/提示词/技能, 发起对话与巡检。
	// 注意: 「执行」类动作(工具执行/修复执行/剧本执行)仍由 PermissionAIToolExec 把关。
	PermissionAIWrite = "ai:write"

	// ---- 自动化: 脚本 ----
	// PermissionScriptRead 查看脚本与执行历史。
	PermissionScriptRead = "script:read"
	// PermissionScriptWrite 新建/编辑/审核脚本。
	PermissionScriptWrite = "script:write"
	// PermissionScriptExec 在受管主机上执行脚本(高危, 等价于远程代码执行)。
	PermissionScriptExec = "script:exec"

	// ---- 自动化: 工作流 ----
	// PermissionWorkflowRead 查看工作流与执行历史。
	PermissionWorkflowRead = "workflow:read"
	// PermissionWorkflowWrite 新建/编辑工作流、审批工作流执行单。
	PermissionWorkflowWrite = "workflow:write"
	// PermissionWorkflowExec 执行/取消工作流编排(高危)。
	PermissionWorkflowExec = "workflow:exec"

	// ---- 自动化: 定时任务 ----
	// PermissionScheduleRead 查看定时任务。
	PermissionScheduleRead = "schedule:read"
	// PermissionScheduleWrite 新建/编辑/删除定时任务(会周期性触发脚本与工作流)。
	PermissionScheduleWrite = "schedule:write"

	// ---- 自动化: 入站渠道配置 ----
	// PermissionAutomationRead 查看入站渠道(企微/钉钉/飞书/邮件)配置。
	PermissionAutomationRead = "automation:read"
	// PermissionAutomationWrite 修改入站渠道配置(含渠道凭据)。
	PermissionAutomationWrite = "automation:write"

	// ---- 报表 ----
	// PermissionReportRead 查看数据集与仪表盘。
	PermissionReportRead = "report:read"
	// PermissionReportWrite 编辑仪表盘、执行自定义数据集查询(查询下推数据库, 属高危读)。
	PermissionReportWrite = "report:write"

	// ---- 低代码平台 ----
	// PermissionLowcodeRead 查看低代码模型/页面/实例。
	PermissionLowcodeRead = "lowcode:read"
	// PermissionLowcodeWrite 编辑低代码模型/页面/实例、安装市场组件。
	PermissionLowcodeWrite = "lowcode:write"

	// ---- 发布管理 ----
	// PermissionDeployRead 查看发布应用/环境/发布单。
	PermissionDeployRead = "deploy:read"
	// PermissionDeployWrite 编辑应用与环境、审批/执行/回滚发布。
	PermissionDeployWrite = "deploy:write"

	// ---- 凭据库 ----
	// PermissionCredentialRead 查看凭据条目元数据。
	PermissionCredentialRead = "credential:read"
	// PermissionCredentialWrite 新增/修改/删除凭据(SSH 私钥、云 AK/SK 等敏感信息)。
	PermissionCredentialWrite = "credential:write"
)

// AllPermissions 返回平台全部权限点(用于 IAM 权限点种子与角色补齐校验)。
// 与常量区保持同步; 新增权限点时应同步加入此列表。
var AllPermissions = []string{
	PermissionCMDBRead, PermissionCMDBWrite, PermissionCMDBAdmin, PermissionCMDBDelete,
	PermissionMonitorRead, PermissionMonitorWrite,
	PermissionITSMRead, PermissionITSMWrite, PermissionITSMApprove,
	PermissionUserRead, PermissionUserWrite,
	PermissionAgentRead, PermissionAgentWrite,
	PermissionAuditRead, PermissionSystemAdmin,
	PermissionAIToolExec, PermissionAPIKeySelf,
	PermissionVulnRead, PermissionVulnWrite,
	PermissionKnowledgeRead, PermissionKnowledgeWrite, PermissionKnowledgeManage,
	PermissionAIRead, PermissionAIWrite,
	PermissionScriptRead, PermissionScriptWrite, PermissionScriptExec,
	PermissionWorkflowRead, PermissionWorkflowWrite, PermissionWorkflowExec,
	PermissionScheduleRead, PermissionScheduleWrite,
	PermissionAutomationRead, PermissionAutomationWrite,
	PermissionReportRead, PermissionReportWrite,
	PermissionLowcodeRead, PermissionLowcodeWrite,
	PermissionDeployRead, PermissionDeployWrite,
	PermissionCredentialRead, PermissionCredentialWrite,
}

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
		PermissionAIToolExec, PermissionAPIKeySelf,
		PermissionVulnRead, PermissionVulnWrite,
		PermissionKnowledgeRead, PermissionKnowledgeWrite, PermissionKnowledgeManage,
		PermissionAIRead, PermissionAIWrite,
		PermissionScriptRead, PermissionScriptWrite, PermissionScriptExec,
		PermissionWorkflowRead, PermissionWorkflowWrite, PermissionWorkflowExec,
		PermissionScheduleRead, PermissionScheduleWrite,
		PermissionAutomationRead, PermissionAutomationWrite,
		PermissionReportRead, PermissionReportWrite,
		PermissionLowcodeRead, PermissionLowcodeWrite,
		PermissionDeployRead, PermissionDeployWrite,
		PermissionCredentialRead, PermissionCredentialWrite,
	},
	RoleOperator: {
		PermissionCMDBRead, PermissionCMDBWrite,
		PermissionMonitorRead, PermissionMonitorWrite,
		PermissionITSMRead, PermissionITSMWrite,
		PermissionUserRead,
		PermissionAgentRead, PermissionAgentWrite,
		PermissionAuditRead, PermissionAIToolExec, PermissionAPIKeySelf,
		PermissionVulnRead, PermissionVulnWrite,
		PermissionKnowledgeRead, PermissionKnowledgeWrite,
		PermissionAIRead, PermissionAIWrite,
		PermissionScriptRead, PermissionScriptWrite, PermissionScriptExec,
		PermissionWorkflowRead, PermissionWorkflowWrite, PermissionWorkflowExec,
		PermissionScheduleRead, PermissionScheduleWrite,
		PermissionAutomationRead, PermissionAutomationWrite,
		PermissionReportRead, PermissionReportWrite,
		PermissionLowcodeRead, PermissionLowcodeWrite,
		PermissionDeployRead, PermissionDeployWrite,
		PermissionCredentialRead, PermissionCredentialWrite,
	},
	RoleViewer: {
		PermissionCMDBRead,
		PermissionMonitorRead,
		PermissionITSMRead,
		PermissionUserRead,
		PermissionAgentRead,
		PermissionAuditRead, PermissionAPIKeySelf,
		PermissionVulnRead,
		PermissionKnowledgeRead,
		PermissionAIRead,
		PermissionScriptRead,
		PermissionWorkflowRead,
		PermissionScheduleRead,
		PermissionAutomationRead,
		PermissionReportRead,
		PermissionLowcodeRead,
		PermissionDeployRead,
	},
}

// ModuleGrantRule 声明「存量自定义角色权限补齐」规则。
//
// 背景: 知识库/AI/脚本/工作流/定时任务/报表/低代码/发布/凭据等模块此前完全没有
// 登记路径权限映射, 鉴权中间件未命中映射时只认证不鉴权 —— 即任意已登录用户都可读写。
// 引入权限点后这些接口开始真正鉴权, 存量自定义角色若不带新权限点会被静默降权,
// 故按「等价能力」补齐: 具备写权限的角色补齐读写, 只读角色补齐只读。
//
// Requires 任一命中即补齐 Grants(并集); 内置角色不走此规则, 由 RolePermissions 显式声明。
type ModuleGrantRule struct {
	// Requires 角色已拥有的任一权限点即触发补齐。
	Requires []string
	// Grants 需要补齐的权限点。
	Grants []string
}

// ModuleGrantRules 存量角色补齐规则表(顺序无关, 结果取并集)。
var ModuleGrantRules = []ModuleGrantRule{
	{
		// 具备任意「运维写」能力的角色: 保留其原有的全模块写能力。
		Requires: []string{
			PermissionCMDBWrite, PermissionMonitorWrite, PermissionITSMWrite,
			PermissionAgentWrite, PermissionVulnWrite,
		},
		Grants: []string{
			PermissionKnowledgeRead, PermissionKnowledgeWrite, PermissionKnowledgeManage,
			PermissionAIRead, PermissionAIWrite,
			PermissionScriptRead, PermissionScriptWrite,
			PermissionWorkflowRead, PermissionWorkflowWrite,
			PermissionScheduleRead, PermissionScheduleWrite,
			PermissionAutomationRead, PermissionAutomationWrite,
			PermissionReportRead, PermissionReportWrite,
			PermissionLowcodeRead, PermissionLowcodeWrite,
			PermissionDeployRead, PermissionDeployWrite,
			PermissionCredentialRead, PermissionCredentialWrite,
		},
	},
	{
		// 具备任意只读能力的角色: 只补只读权限点(不含脚本执行/凭据写等)。
		Requires: []string{
			PermissionCMDBRead, PermissionMonitorRead, PermissionITSMRead,
			PermissionAgentRead, PermissionVulnRead, PermissionAuditRead,
		},
		Grants: []string{
			PermissionKnowledgeRead, PermissionAIRead,
			PermissionScriptRead, PermissionWorkflowRead, PermissionScheduleRead,
			PermissionAutomationRead, PermissionReportRead,
			PermissionLowcodeRead, PermissionDeployRead,
		},
	},
	{
		// 具备工具执行权的角色: 保留受管主机脚本/工作流执行能力(高危)。
		Requires: []string{PermissionAIToolExec},
		Grants:   []string{PermissionScriptExec, PermissionWorkflowExec},
	},
}

// BackfillModulePermissions 按 ModuleGrantRules 为存量角色补齐新模块权限点。
// 返回补齐后的权限列表以及是否发生了变化(供调用方决定是否落库)。
func BackfillModulePermissions(perms []string) ([]string, bool) {
	owned := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		owned[p] = struct{}{}
	}
	missing := make([]string, 0, 8)
	for _, rule := range ModuleGrantRules {
		hit := false
		for _, req := range rule.Requires {
			if _, ok := owned[req]; ok {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		for _, g := range rule.Grants {
			if _, ok := owned[g]; !ok {
				missing = append(missing, g)
			}
		}
	}
	if len(missing) == 0 {
		return perms, false
	}
	out := make([]string, 0, len(perms)+len(missing))
	out = append(out, perms...)
	out = append(out, missing...)
	return out, true
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
	// API Key 自助管理:Key 绑定用户并继承其权限,故管理自己的 Key 不额外提权。
	// 越权边界(代他人建 Key / 查看全租户 Key)在 apikey 服务内按角色二次判定。
	"GET /api/v1/api-keys":    PermissionAPIKeySelf,
	"POST /api/v1/api-keys":   PermissionAPIKeySelf,
	"DELETE /api/v1/api-keys": PermissionAPIKeySelf,
	// CMDB 数据消费分析:与 CMDB 读权限同级(只读统计视图)。
	"GET /api/v1/cmdb/usage": PermissionCMDBRead,
	// 漏洞扫描:查看(vuln:read)与发起/处置(vuln:write)。
	// 未映射时中间件只认证不鉴权,故必须显式声明,否则任意登录用户可建/删扫描。
	"GET /api/v1/vuln":    PermissionVulnRead,
	"POST /api/v1/vuln":   PermissionVulnWrite,
	"PUT /api/v1/vuln":    PermissionVulnWrite,
	"PATCH /api/v1/vuln":  PermissionVulnWrite,
	"DELETE /api/v1/vuln": PermissionVulnWrite,

	// 知识库:查看(knowledge:read) / 编辑(knowledge:write) / 删除·导入·重建索引(knowledge:manage)。
	"GET /api/v1/knowledge":    PermissionKnowledgeRead,
	"POST /api/v1/knowledge":   PermissionKnowledgeWrite,
	"PUT /api/v1/knowledge":    PermissionKnowledgeWrite,
	"PATCH /api/v1/knowledge":  PermissionKnowledgeWrite,
	"DELETE /api/v1/knowledge": PermissionKnowledgeManage,

	// AI 助手:查看(ai:read) / 编辑与对话(ai:write)。
	// 「执行」类动作(工具/修复/剧本)由 ActionPermissionMapping 提升到 ai:tool:exec。
	"GET /api/v1/ai":    PermissionAIRead,
	"POST /api/v1/ai":   PermissionAIWrite,
	"PUT /api/v1/ai":    PermissionAIWrite,
	"PATCH /api/v1/ai":  PermissionAIWrite,
	"DELETE /api/v1/ai": PermissionAIWrite,

	// 自动化 - 脚本:查看(script:read) / 编辑(script:write),
	// 执行由 ActionPermissionMapping 提升到 script:exec(受管主机远程代码执行)。
	"GET /api/v1/scripts":    PermissionScriptRead,
	"POST /api/v1/scripts":   PermissionScriptWrite,
	"PUT /api/v1/scripts":    PermissionScriptWrite,
	"PATCH /api/v1/scripts":  PermissionScriptWrite,
	"DELETE /api/v1/scripts": PermissionScriptWrite,

	// 自动化 - 工作流:查看(workflow:read) / 编辑与审批(workflow:write),
	// 执行由 ActionPermissionMapping 提升到 workflow:exec。
	"GET /api/v1/workflows":    PermissionWorkflowRead,
	"POST /api/v1/workflows":   PermissionWorkflowWrite,
	"PUT /api/v1/workflows":    PermissionWorkflowWrite,
	"PATCH /api/v1/workflows":  PermissionWorkflowWrite,
	"DELETE /api/v1/workflows": PermissionWorkflowWrite,

	// 自动化 - 定时任务。
	"GET /api/v1/schedules":    PermissionScheduleRead,
	"POST /api/v1/schedules":   PermissionScheduleWrite,
	"PUT /api/v1/schedules":    PermissionScheduleWrite,
	"PATCH /api/v1/schedules":  PermissionScheduleWrite,
	"DELETE /api/v1/schedules": PermissionScheduleWrite,

	// 自动化 - 入站渠道配置(含渠道凭据)。渠道回调 /api/v1/inbound/ 走 bypass 前缀。
	"GET /api/v1/automation":    PermissionAutomationRead,
	"POST /api/v1/automation":   PermissionAutomationWrite,
	"PUT /api/v1/automation":    PermissionAutomationWrite,
	"DELETE /api/v1/automation": PermissionAutomationWrite,

	// 报表:查看(report:read) / 编辑仪表盘与执行自定义查询(report:write)。
	"GET /api/v1/report":    PermissionReportRead,
	"POST /api/v1/report":   PermissionReportWrite,
	"PUT /api/v1/report":    PermissionReportWrite,
	"DELETE /api/v1/report": PermissionReportWrite,

	// 低代码平台:模型/页面/实例/市场。
	"GET /api/v1/lowcode":    PermissionLowcodeRead,
	"POST /api/v1/lowcode":   PermissionLowcodeWrite,
	"PUT /api/v1/lowcode":    PermissionLowcodeWrite,
	"DELETE /api/v1/lowcode": PermissionLowcodeWrite,

	// 发布管理:查看(deploy:read) / 编辑·审批·执行·回滚(deploy:write)。
	"GET /api/v1/deploy":    PermissionDeployRead,
	"POST /api/v1/deploy":   PermissionDeployWrite,
	"PUT /api/v1/deploy":    PermissionDeployWrite,
	"DELETE /api/v1/deploy": PermissionDeployWrite,

	// 凭据库(SSH 私钥 / 云 AK-SK 等):独立权限点,默认不授予 viewer。
	"GET /api/v1/keys":    PermissionCredentialRead,
	"POST /api/v1/keys":   PermissionCredentialWrite,
	"PUT /api/v1/keys":    PermissionCredentialWrite,
	"DELETE /api/v1/keys": PermissionCredentialWrite,

	// 云资源纳管:云凭据与资源同步最终落入 CMDB,故与 CMDB 权限同级。
	"GET /api/v1/cloud":    PermissionCMDBRead,
	"POST /api/v1/cloud":   PermissionCMDBWrite,
	"DELETE /api/v1/cloud": PermissionCMDBAdmin,

	// CMDB 全景视图:只读聚合。
	"GET /api/v1/panorama": PermissionCMDBRead,

	// 值班表(oncall)属 ITSM 域。
	"GET /api/v1/oncall":    PermissionITSMRead,
	"POST /api/v1/oncall":   PermissionITSMWrite,
	"PUT /api/v1/oncall":    PermissionITSMWrite,
	"DELETE /api/v1/oncall": PermissionITSMWrite,

	// 系统配置(含 LLM 凭据、采集策略等)按系统级管理;
	// 运行状态/日志是监控只读视图,与 monitor:read 同级。
	"GET /api/v1/system/config": PermissionSystemAdmin,
	"PUT /api/v1/system/config": PermissionSystemAdmin,
	"GET /api/v1/system":        PermissionMonitorRead,

	// Agent 安装包等文件:与 Agent 管理同级。
	"GET /api/v1/files":    PermissionAgentRead,
	"POST /api/v1/files":   PermissionAgentWrite,
	"DELETE /api/v1/files": PermissionAgentWrite,
}

// ActionPermissionMapping 动作级权限映射,用于把「/模块/{id}/<动作>」形态的
// 高危操作提升到独立权限点(路径前缀匹配无法区分 /scripts/{id} 与 /scripts/{id}/execute)。
//
// 键 = "METHOD /路径模式", 路径段以 "/" 分割, "*" 匹配任意单个路径段且段数必须一致。
// 命中优先级高于 PathPermissionMapping 的最长前缀匹配。
var ActionPermissionMapping = map[string]string{
	// 受管主机远程代码执行:与 ai:tool:exec 同级的高危操作。
	"POST /api/v1/scripts/*/execute":             PermissionScriptExec,
	"POST /api/v1/workflows/*/execute":           PermissionWorkflowExec,
	"POST /api/v1/workflows/executions/*/cancel": PermissionWorkflowExec,

	// 脚本审核动作属编辑态变更。
	"POST /api/v1/scripts/*/publish": PermissionScriptWrite,
	"POST /api/v1/scripts/*/approve": PermissionScriptWrite,
	"POST /api/v1/scripts/*/reject":  PermissionScriptWrite,

	// AI 执行类动作统一收敛到 ai:tool:exec(与 handler 内的二次校验形成纵深防御)。
	"POST /api/v1/ai/tools/*/execute":     PermissionAIToolExec,
	"POST /api/v1/ai/remediate/*/execute": PermissionAIToolExec,
	"POST /api/v1/ai/runbooks/*/execute":  PermissionAIToolExec,

	// 发布单的高危动作。
	"POST /api/v1/deploy/releases/*/deploy":   PermissionDeployWrite,
	"POST /api/v1/deploy/releases/*/rollback": PermissionDeployWrite,

	// 检索留痕(P1-2/P1-4): 留痕里存的是用户原始提问文本与召回详情,
	// 属运行态敏感观测数据, 收敛到管理级(knowledge:manage), 普通只读账号不可见。
	"GET /api/v1/knowledge/search-logs":  PermissionKnowledgeManage,
	"GET /api/v1/knowledge/search-stats": PermissionKnowledgeManage,
	// RAG 效果评估(P1-1): 一次评估会跑几十次真实检索(消耗 embedding 额度), 收敛到管理级。
	"POST /api/v1/knowledge/rag-eval": PermissionKnowledgeManage,
	// 检索测试台(批次 4): 可指定 scope_user_id「以他人身份检索」, 等于窥探他人的可见范围,
	// 属敏感能力, 必须显式登记到管理级 —— 不登记会回退到前缀匹配的 knowledge:write。
	"POST /api/v1/knowledge/retrieval-preview": PermissionKnowledgeManage,
	// 用户级知识搜索: 只读能力, 任何能看知识库的人都应能搜; 谓词按请求者身份生成, 无越权面。
	// 显式登记避免被「/knowledge/{id} → knowledge:write」一类前缀规则误拦只读账号。
	"GET /api/v1/knowledge/search": PermissionKnowledgeRead,
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
		} else {
			// 未登记映射的路径只认证不鉴权(直接放行)。这类遗漏是「任意登录用户可增删改」
			// 的根因, 且表现为静默通过, 极易被忽略 —— 故按 (method, 路径模板) 去重后
			// 打 WARN 日志, 让新增接口漏登记权限映射时能被观测到。
			warnUnmappedPath(r.Method, r.Pattern)
		}

		next.ServeHTTP(w, r)
	})
}

// unmappedWarned 记录已告警过的 (method, 路径模板), 避免每请求一条日志刷屏。
var unmappedWarned sync.Map

// warnUnmappedPath 对未登记权限映射的受保护路径打一次 WARN。
// pattern 为空时(理论上仅在没有路由模板匹配时)跳过, 避免噪音。
func warnUnmappedPath(method, pattern string) {
	// 仅关注 /api/v1 下的业务接口; /metrics 等已在 bypass/shouldSkip 中处理。
	if pattern == "" || !strings.HasPrefix(pattern, "/api/v1/") {
		return
	}
	key := method + " " + pattern
	if _, loaded := unmappedWarned.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	slog.Warn("rbac: 路径未登记权限映射, 仅认证未鉴权 —— 请补充 pkg/auth.PathPermissionMapping",
		"method", method, "pattern", pattern)
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

// resolvePathPermission 解析请求所需的权限点。
// 先尝试动作级规则(ActionPermissionMapping, 支持 "*" 段通配, 用于 {id}/动作 形态),
// 未命中再按最长路径前缀匹配 PathPermissionMapping。
// 从完整路径逐级向上收敛到 4 段前缀(/api/v1/<mod>)，返回第一个命中的权限。
// 这样 /api/v1/cmdb/model-type 的 cmdb:admin 专属映射可命中，
// 而 /api/v1/cmdb/ci/{id} 等仍回落到 /api/v1/cmdb 的 cmdb:write。
func resolvePathPermission(method, path string) (string, bool) {
	if perm, ok := resolveActionPermission(method, path); ok {
		return perm, true
	}
	parts := strings.Split(path, "/")
	for i := len(parts); i >= 4; i-- {
		key := method + " " + strings.Join(parts[:i], "/")
		if perm, ok := PathPermissionMapping[key]; ok {
			return perm, true
		}
	}
	return "", false
}

// resolveActionPermission 按段匹配 ActionPermissionMapping:
// 模式与请求路径的段数必须一致, 除 "*" 段外逐段字符串相等即命中。
func resolveActionPermission(method, path string) (string, bool) {
	reqParts := strings.Split(path, "/")
	for pattern, perm := range ActionPermissionMapping {
		patMethod, patPath, ok := strings.Cut(pattern, " ")
		if !ok || patMethod != method {
			continue
		}
		patParts := strings.Split(patPath, "/")
		if len(patParts) != len(reqParts) {
			continue
		}
		matched := true
		for i := range patParts {
			if patParts[i] == "*" {
				continue
			}
			if patParts[i] != reqParts[i] {
				matched = false
				break
			}
		}
		if matched {
			return perm, true
		}
	}
	return "", false
}
