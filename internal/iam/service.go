package iam

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/iam/ldapsync"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/validate"

	"github.com/opsorigin/core/pkg/httputil"
)

// Store 定义 IAM 的持久化接口。当前由 MemoryStore 实现，
// 与项目内 script/workflow/agentmgr 模块保持一致的 stub 模式。
type Store interface {
	// 角色
	CreateRole(ctx context.Context, r *Role) error
	GetRole(ctx context.Context, id, tenantID string) (*Role, error)
	GetRoleByName(ctx context.Context, name, tenantID string) (*Role, error)
	ListRoles(ctx context.Context, tenantID string) ([]*Role, error)
	UpdateRole(ctx context.Context, r *Role) error
	DeleteRole(ctx context.Context, id, tenantID string) error
	// 用户组
	CreateGroup(ctx context.Context, g *Group) error
	GetGroup(ctx context.Context, id, tenantID string) (*Group, error)
	ListGroups(ctx context.Context, tenantID string) ([]*Group, error)
	UpdateGroup(ctx context.Context, g *Group) error
	DeleteGroup(ctx context.Context, id, tenantID string) error
	// 组织架构（部门树；成员经 users.department 名关联）
	CreateDepartment(ctx context.Context, d *Department) error
	GetDepartment(ctx context.Context, id, tenantID string) (*Department, error)
	ListDepartments(ctx context.Context, tenantID string) ([]*Department, error)
	UpdateDepartment(ctx context.Context, d *Department) error
	DeleteDepartment(ctx context.Context, id, tenantID string) error
	EnsureDepartment(ctx context.Context, tenantID, name, source string) (string, bool, error)
	EnsureDepartmentUnder(ctx context.Context, tenantID, name, parentID, source string) (string, bool, error)
	// 用户-角色分配
	SetUserRoles(ctx context.Context, userID, tenantID string, roleIDs []string) error
	GetUserRoles(ctx context.Context, userID, tenantID string) ([]string, error)
}

// MemoryStore 是并发安全的内存实现。
type MemoryStore struct {
	mu          sync.RWMutex
	roles       map[string]*Role
	groups      map[string]*Group
	departments map[string]*Department
	userRoles   map[string][]string // userID -> roleIDs
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		roles:       make(map[string]*Role),
		groups:      make(map[string]*Group),
		departments: make(map[string]*Department),
		userRoles:   make(map[string][]string),
	}
}

func (s *MemoryStore) CreateRole(ctx context.Context, r *Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = uuid.New().String()
	now := time.Now()
	r.CreatedAt = now
	r.UpdatedAt = now
	s.roles[r.ID] = r
	return nil
}

func (s *MemoryStore) GetRole(ctx context.Context, id, tenantID string) (*Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.roles[id]
	if !ok || r.TenantID != tenantID {
		return nil, nil
	}
	return r, nil
}

func (s *MemoryStore) GetRoleByName(ctx context.Context, name, tenantID string) (*Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.roles {
		if r.TenantID == tenantID && r.Name == name {
			return r, nil
		}
	}
	return nil, nil
}

func (s *MemoryStore) ListRoles(ctx context.Context, tenantID string) ([]*Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Role
	for _, r := range s.roles {
		if r.TenantID == tenantID {
			result = append(result, r)
		}
	}
	return result, nil
}

func (s *MemoryStore) UpdateRole(ctx context.Context, r *Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.roles[r.ID]
	if !ok || existing.TenantID != r.TenantID {
		return nil
	}
	r.CreatedAt = existing.CreatedAt
	r.IsBuiltIn = existing.IsBuiltIn
	r.Name = existing.Name // 角色名不可改
	r.UpdatedAt = time.Now()
	s.roles[r.ID] = r
	return nil
}

func (s *MemoryStore) DeleteRole(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.roles[id]
	if !ok || r.TenantID != tenantID {
		return nil
	}
	delete(s.roles, id)
	// 从所有用户的角色分配中移除该角色
	for uid, roleIDs := range s.userRoles {
		filtered := make([]string, 0, len(roleIDs))
		for _, rid := range roleIDs {
			if rid != id {
				filtered = append(filtered, rid)
			}
		}
		s.userRoles[uid] = filtered
	}
	return nil
}

func (s *MemoryStore) CreateGroup(ctx context.Context, g *Group) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.ID = uuid.New().String()
	now := time.Now()
	g.CreatedAt = now
	g.UpdatedAt = now
	s.groups[g.ID] = g
	return nil
}

func (s *MemoryStore) GetGroup(ctx context.Context, id, tenantID string) (*Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.groups[id]
	if !ok || g.TenantID != tenantID {
		return nil, nil
	}
	return g, nil
}

func (s *MemoryStore) ListGroups(ctx context.Context, tenantID string) ([]*Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Group
	for _, g := range s.groups {
		if g.TenantID == tenantID {
			result = append(result, g)
		}
	}
	return result, nil
}

func (s *MemoryStore) UpdateGroup(ctx context.Context, g *Group) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.groups[g.ID]
	if !ok || existing.TenantID != g.TenantID {
		return nil
	}
	g.CreatedAt = existing.CreatedAt
	g.UpdatedAt = time.Now()
	s.groups[g.ID] = g
	return nil
}

func (s *MemoryStore) DeleteGroup(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[id]
	if !ok || g.TenantID != tenantID {
		return nil
	}
	delete(s.groups, id)
	return nil
}

func (s *MemoryStore) CreateDepartment(ctx context.Context, d *Department) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.departments {
		if e.TenantID == d.TenantID && e.Name == d.Name {
			return fmt.Errorf("部门 %q 已存在", d.Name)
		}
	}
	d.ID = uuid.New().String()
	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now
	s.departments[d.ID] = d
	return nil
}

func (s *MemoryStore) GetDepartment(ctx context.Context, id, tenantID string) (*Department, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.departments[id]
	if !ok || d.TenantID != tenantID {
		return nil, nil
	}
	return d, nil
}

// EnsureDepartmentUnder 按名+父幂等建档（批量导入用）：同名已存在时校验父一致。
func (s *MemoryStore) EnsureDepartmentUnder(ctx context.Context, tenantID, name, parentID, source string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.departments {
		if e.TenantID == tenantID && e.Name == name {
			if e.ParentID != parentID {
				return "", false, fmt.Errorf("部门 %q 已存在但上级不一致（已有挂载与导入路径冲突）", name)
			}
			return e.ID, false, nil
		}
	}
	d := &Department{TenantID: tenantID, Name: name, ParentID: parentID, Source: source, ID: uuid.New().String()}
	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now
	s.departments[d.ID] = d
	return d.ID, true, nil
}

func (s *MemoryStore) ListDepartments(ctx context.Context, tenantID string) ([]*Department, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Department
	for _, d := range s.departments {
		if d.TenantID == tenantID {
			result = append(result, d)
		}
	}
	return result, nil
}

func (s *MemoryStore) UpdateDepartment(ctx context.Context, d *Department) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.departments[d.ID]
	if !ok || existing.TenantID != d.TenantID {
		return fmt.Errorf("部门不存在")
	}
	// 同名冲突（改名撞别的部门）
	for _, e := range s.departments {
		if e.ID != d.ID && e.TenantID == d.TenantID && e.Name == d.Name {
			return fmt.Errorf("部门 %q 已存在", d.Name)
		}
	}
	d.CreatedAt = existing.CreatedAt
	d.UpdatedAt = time.Now()
	s.departments[d.ID] = d
	return nil
}

func (s *MemoryStore) DeleteDepartment(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.departments[id]
	if !ok || d.TenantID != tenantID {
		return nil
	}
	for _, e := range s.departments {
		if e.TenantID == tenantID && e.ParentID == id {
			return fmt.Errorf("存在子部门，无法删除")
		}
	}
	delete(s.departments, id)
	return nil
}

// EnsureDepartment 按名幂等建档（AD 镜像/本地引用归档共用）：存在直接返回。
func (s *MemoryStore) EnsureDepartment(ctx context.Context, tenantID, name, source string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.departments {
		if e.TenantID == tenantID && e.Name == name {
			return e.ID, false, nil
		}
	}
	d := &Department{TenantID: tenantID, Name: name, Source: source}
	d.ID = uuid.New().String()
	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now
	s.departments[d.ID] = d
	return d.ID, true, nil
}

func (s *MemoryStore) SetUserRoles(ctx context.Context, userID, tenantID string, roleIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 只保留实际存在的角色
	filtered := make([]string, 0, len(roleIDs))
	for _, rid := range roleIDs {
		if r, ok := s.roles[rid]; ok && r.TenantID == tenantID {
			filtered = append(filtered, rid)
		}
	}
	s.userRoles[userID] = filtered
	return nil
}

func (s *MemoryStore) GetUserRoles(ctx context.Context, userID, tenantID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userRoles[userID], nil
}

// Service 暴露 IAM 的 HTTP API。
type Service struct {
	store       Store
	permissions []Permission

	// permCache 缓存 (tenantID:roleName) -> 权限列表,避免每次请求回查 PG。
	// 角色权限变更(Create/Update/Delete)时整体失效。
	permCacheMu sync.Mutex
	permCache   map[string][]string
	// onPermChange 权限配置变更时的外部通知钩子(如鉴权链的身份富化器缓存)。
	// 仅在装配期设置一次,运行期只读,故不加锁。
	onPermChange func()

	// ldapSyncer AD/LDAP 目录同步引擎(装配期注入; nil=同步端点返回 501)。
	ldapSyncer *ldapsync.Syncer
}

// SetPermissionChangeHook 注册权限配置变更回调。角色被增删改后触发,
// 供上游缓存(如 identity.Enricher)同步失效,避免改完权限还要等 TTL 才生效。
func (s *Service) SetPermissionChangeHook(fn func()) { s.onPermChange = fn }

// NewService 创建 Service 并 seed 预置权限与内置角色。
func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = newPGStore(db)
	}
	s := &Service{
		store:       st,
		permissions: seedPermissions(),
		permCache:   make(map[string][]string),
	}
	s.seedBuiltinRoles()
	return s
}

// ListRoles / ListGroups 委托给底层存储, 满足 itsm.IAMProvider 接口,
// 供 ITSM 动态表单 source=iam:role / iam:group 解析配置项可选来源。
func (s *Service) ListRoles(ctx context.Context, tenantID string) ([]*Role, error) {
	return s.store.ListRoles(ctx, tenantID)
}

func (s *Service) ListGroups(ctx context.Context, tenantID string) ([]*Group, error) {
	return s.store.ListGroups(ctx, tenantID)
}

// EnsureDepartment 按名幂等建档（实现 ldapsync.DepartmentWriter：AD 部门属性镜像）。
func (s *Service) EnsureDepartment(ctx context.Context, tenantID, name, source string) (string, bool, error) {
	return s.store.EnsureDepartment(ctx, tenantID, name, source)
}

// ResolvePermissions 按 (tenantID, roleName) 解析角色权限,供 RBAC 中间件鉴权使用。
// 优先返回 IAM 角色库中配置的权限;角色不存在时返回 nil(由中间件回落到写死映射)。
// 结果按 (tenant,role) 缓存,角色变更时通过 invalidatePermCache 失效。
func (s *Service) ResolvePermissions(tenantID, roleName string) []string {
	if tenantID == "" {
		tenantID = "default"
	}
	key := tenantID + ":" + roleName
	s.permCacheMu.Lock()
	if perms, ok := s.permCache[key]; ok {
		s.permCacheMu.Unlock()
		return perms
	}
	s.permCacheMu.Unlock()

	role, err := s.store.GetRoleByName(context.Background(), roleName, tenantID)
	if err != nil || role == nil {
		return nil
	}
	s.permCacheMu.Lock()
	s.permCache[key] = role.Permissions
	s.permCacheMu.Unlock()
	return role.Permissions
}

// invalidatePermCache 使权限解析缓存整体失效(角色增删改后调用),
// 并通知上游缓存一并失效。
func (s *Service) invalidatePermCache() {
	s.permCacheMu.Lock()
	s.permCache = make(map[string][]string)
	s.permCacheMu.Unlock()
	if s.onPermChange != nil {
		s.onPermChange()
	}
}

// seedPermissions 返回平台预定义的权限点清单。
// 与 pkg/auth/rbac.go 中的 Permission 常量保持一致。
func seedPermissions() []Permission {
	return []Permission{
		{Name: "cmdb:read", Description: "查看 CMDB 资源", Category: "CMDB 资产管理"},
		{Name: "cmdb:write", Description: "新增/修改 CMDB 资源", Category: "CMDB 资产管理"},
		{Name: "cmdb:admin", Description: "管理 CMDB 模型(删除/改模型)", Category: "CMDB 资产管理"},
		{Name: "cmdb:delete", Description: "删除 CMDB 实例/关系(高危)", Category: "CMDB 资产管理"},
		{Name: "monitor:read", Description: "查看监控指标与告警", Category: "监控告警"},
		{Name: "monitor:write", Description: "配置监控策略与告警规则", Category: "监控告警"},
		{Name: "itsm:read", Description: "查看工单", Category: "ITSM 工单"},
		{Name: "itsm:write", Description: "创建/修改工单", Category: "ITSM 工单"},
		{Name: "itsm:approve", Description: "审批工单", Category: "ITSM 工单"},
		{Name: "user:read", Description: "查看用户/角色/用户组", Category: "用户权限"},
		{Name: "user:write", Description: "管理用户/角色/用户组", Category: "用户权限"},
		{Name: "agent:read", Description: "查看 Agent 主机", Category: "Agent 管理"},
		{Name: "agent:write", Description: "纳管/卸载 Agent", Category: "Agent 管理"},
		{Name: "audit:read", Description: "查看审计日志", Category: "审计"},
		{Name: "system:admin", Description: "系统级管理(全权限)", Category: "系统"},
		{Name: "ai:tool:exec", Description: "执行运维工具/脚本(高危)", Category: "运维工具"},
		{Name: "apikey:self", Description: "管理自己的 API Key(继承本人权限)", Category: "用户权限"},
		{Name: "vuln:read", Description: "查看漏洞扫描与漏洞明细", Category: "漏洞扫描"},
		{Name: "vuln:write", Description: "发起/删除扫描,处置漏洞(修复/忽略/误报)", Category: "漏洞扫描"},
		{Name: "knowledge:read", Description: "查看知识库文章/分类/版本/附件", Category: "知识库"},
		{Name: "knowledge:write", Description: "新建/编辑知识库文章与分类、上传附件", Category: "知识库"},
		{Name: "knowledge:manage", Description: "删除文章/分类、导入、重建向量索引(高危)", Category: "知识库"},
		{Name: "ai:read", Description: "查看 AI 会话/智能体/提示词/技能", Category: "AI 助手"},
		{Name: "ai:write", Description: "管理 AI 会话/智能体/提示词/技能, 发起对话与巡检", Category: "AI 助手"},
		{Name: "ai:trace:read", Description: "查看数字员工运行轨迹与 OTel 导出(含工具入参与结果)", Category: "AI 助手"},
		{Name: "script:read", Description: "查看脚本与执行历史", Category: "自动化"},
		{Name: "script:write", Description: "新建/编辑/审核脚本", Category: "自动化"},
		{Name: "script:exec", Description: "在受管主机执行脚本(高危)", Category: "自动化"},
		{Name: "workflow:read", Description: "查看工作流与执行历史", Category: "自动化"},
		{Name: "workflow:write", Description: "编辑工作流、审批执行单", Category: "自动化"},
		{Name: "workflow:exec", Description: "执行/取消工作流编排(高危)", Category: "自动化"},
		{Name: "schedule:read", Description: "查看定时任务", Category: "自动化"},
		{Name: "schedule:write", Description: "编辑定时任务", Category: "自动化"},
		{Name: "automation:read", Description: "查看入站渠道(企微/钉钉/飞书/邮件)配置", Category: "自动化"},
		{Name: "automation:write", Description: "修改入站渠道配置与凭据", Category: "自动化"},
		{Name: "report:read", Description: "查看报表数据集与仪表盘", Category: "报表"},
		{Name: "report:write", Description: "编辑仪表盘、执行自定义数据集查询", Category: "报表"},
		{Name: "lowcode:read", Description: "查看低代码模型/页面/实例", Category: "低代码"},
		{Name: "lowcode:write", Description: "编辑低代码模型/页面/实例", Category: "低代码"},
		{Name: "deploy:read", Description: "查看发布应用/环境/发布单", Category: "发布管理"},
		{Name: "deploy:write", Description: "编辑应用与环境、审批/执行/回滚发布", Category: "发布管理"},
		{Name: "credential:read", Description: "查看凭据库条目", Category: "凭据管理"},
		{Name: "credential:write", Description: "新增/修改/删除凭据(SSH 私钥、云 AK/SK)", Category: "凭据管理"},
	}
}

// seedBuiltinRoles 预置 admin/operator/viewer 三个内置角色。
// 幂等:角色已存在时跳过创建,但补齐种子中新增的权限点(如 cmdb:delete / ai:tool:exec),
// 不删除用户已自定义的权限,使新增权限自动生效而不破坏既有配置。
// 权限清单与 pkg/auth/rbac.go 的 RolePermissions 保持一致(operator 不含 cmdb:admin/cmdb:delete)。
func (s *Service) seedBuiltinRoles() {
	ctx := context.Background()
	builtins := []struct {
		name, display, desc string
		perms               []string
	}{
		{"admin", "管理员", "系统管理员,拥有全部权限", []string{
			"cmdb:read", "cmdb:write", "cmdb:admin", "cmdb:delete",
			"monitor:read", "monitor:write",
			"itsm:read", "itsm:write", "itsm:approve",
			"user:read", "user:write",
			"agent:read", "agent:write",
			"audit:read", "system:admin", "ai:tool:exec",
			"vuln:read", "vuln:write",
			"knowledge:read", "knowledge:write", "knowledge:manage",
			"ai:read", "ai:write", "ai:trace:read",
			"script:read", "script:write", "script:exec",
			"workflow:read", "workflow:write", "workflow:exec",
			"schedule:read", "schedule:write",
			"automation:read", "automation:write",
			"report:read", "report:write",
			"lowcode:read", "lowcode:write",
			"deploy:read", "deploy:write",
			"credential:read", "credential:write",
		}},
		{"operator", "操作员", "日常运维操作,无系统级权限", []string{
			"cmdb:read", "cmdb:write",
			"monitor:read", "monitor:write",
			"itsm:read", "itsm:write",
			"user:read",
			"agent:read", "agent:write",
			"audit:read", "ai:tool:exec",
			"vuln:read", "vuln:write",
			"knowledge:read", "knowledge:write",
			"ai:read", "ai:write", "ai:trace:read",
			"script:read", "script:write", "script:exec",
			"workflow:read", "workflow:write", "workflow:exec",
			"schedule:read", "schedule:write",
			"automation:read", "automation:write",
			"report:read", "report:write",
			"lowcode:read", "lowcode:write",
			"deploy:read", "deploy:write",
			"credential:read", "credential:write",
		}},
		{"viewer", "只读用户", "仅可查看,不可修改", []string{
			"cmdb:read",
			"monitor:read",
			"itsm:read",
			"user:read",
			"agent:read",
			"audit:read",
			"vuln:read",
			"knowledge:read",
			"ai:read",
			"script:read",
			"workflow:read",
			"schedule:read",
			"automation:read",
			"report:read",
			"lowcode:read",
			"deploy:read",
		}},
	}
	for _, b := range builtins {
		existing, _ := s.store.GetRoleByName(ctx, b.name, "default")
		if existing == nil {
			r := &Role{
				TenantID:    "default",
				Name:        b.name,
				DisplayName: b.display,
				Description: b.desc,
				Permissions: b.perms,
				IsBuiltIn:   true,
			}
			s.store.CreateRole(ctx, r)
			continue
		}
		// 已存在:补齐种子中新增的权限点(并集,不删既有)
		merged := mergeUnique(existing.Permissions, b.perms)
		if len(merged) != len(existing.Permissions) {
			updated := *existing
			updated.Permissions = merged
			s.store.UpdateRole(ctx, &updated)
		}
	}
	s.backfillModulePermissions(ctx)
}

// backfillModulePermissions 为「存量自定义角色」补齐新引入模块的权限点。
//
// 知识库/AI/脚本/工作流/定时任务/报表/低代码/发布/凭据等模块此前未登记路径权限映射,
// 中间件未命中映射时只认证不鉴权,等价于所有已登录用户都能读写。补登记映射后这些接口
// 开始真正鉴权,存量自定义角色若不带新权限点会被静默降权,故按 pkg/auth.ModuleGrantRules
// 的「等价能力」规则补齐(具备写权限→补读写,只读角色→补只读)。
//
// 幂等:仅当补齐后权限集合发生变化才落库。
func (s *Service) backfillModulePermissions(ctx context.Context) {
	roles, err := s.store.ListRoles(ctx, "default")
	if err != nil {
		slog.Warn("iam: 列出角色失败, 跳过存量角色权限补齐", "error", err)
		return
	}
	for _, role := range roles {
		if role == nil || role.IsBuiltIn {
			continue
		}
		merged, changed := auth.BackfillModulePermissions(role.Permissions)
		if !changed {
			continue
		}
		updated := *role
		updated.Permissions = merged
		if err := s.store.UpdateRole(ctx, &updated); err != nil {
			slog.Warn("iam: 存量角色权限补齐失败", "role", role.Name, "error", err)
			continue
		}
		slog.Info("iam: 已为存量角色补齐新模块权限点", "role", role.Name, "added", len(merged)-len(role.Permissions))
	}
}

// mergeUnique 返回 a 与 b 的并集(去重,保持原有顺序)。
func mergeUnique(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, p := range append(append([]string{}, a...), b...) {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// RegisterRoutes 注册所有 IAM 路由。注意 "permissions" / "roles" / "groups"
// 都是字面量段,在 Go 1.22+ ServeMux 中优先于 {id} 通配符匹配。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	// 权限(只读列表)
	mux.HandleFunc("GET /api/v1/iam/permissions", s.handleListPermissions)
	// 角色 CRUD
	mux.HandleFunc("GET /api/v1/iam/roles", s.handleListRoles)
	mux.HandleFunc("POST /api/v1/iam/roles", s.handleCreateRole)
	mux.HandleFunc("GET /api/v1/iam/roles/{id}", s.handleGetRole)
	mux.HandleFunc("PUT /api/v1/iam/roles/{id}", s.handleUpdateRole)
	mux.HandleFunc("DELETE /api/v1/iam/roles/{id}", s.handleDeleteRole)
	// 用户组 CRUD
	mux.HandleFunc("GET /api/v1/iam/groups", s.handleListGroups)
	mux.HandleFunc("POST /api/v1/iam/groups", s.handleCreateGroup)
	mux.HandleFunc("GET /api/v1/iam/groups/{id}", s.handleGetGroup)
	mux.HandleFunc("PUT /api/v1/iam/groups/{id}", s.handleUpdateGroup)
	mux.HandleFunc("DELETE /api/v1/iam/groups/{id}", s.handleDeleteGroup)

	// 组织架构（部门树）
	mux.HandleFunc("GET /api/v1/iam/departments", s.handleListDepartments)
	mux.HandleFunc("POST /api/v1/iam/departments", s.handleCreateDepartment)
	mux.HandleFunc("POST /api/v1/iam/departments/import", s.handleImportDepartments)
	mux.HandleFunc("PUT /api/v1/iam/departments/{id}", s.handleUpdateDepartment)
	mux.HandleFunc("DELETE /api/v1/iam/departments/{id}", s.handleDeleteDepartment)
	// 用户-角色分配
	mux.HandleFunc("GET /api/v1/iam/users/{user_id}/roles", s.handleGetUserRoles)
	mux.HandleFunc("PUT /api/v1/iam/users/{user_id}/roles", s.handleAssignUserRoles)
	mux.HandleFunc("POST /api/v1/iam/ldap/sync", s.handleLdapSync)
	// 当前用户自身权限(供前端权限感知,任意已登录用户可读)
	mux.HandleFunc("GET /api/v1/iam/me/permissions", s.handleGetMyPermissions)
}

// ---------- 权限 ----------

func (s *Service) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.permissions)
}

// ---------- 角色 ----------

func (s *Service) handleListRoles(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	roles, err := s.store.ListRoles(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if roles == nil {
		roles = []*Role{}
	}
	httputil.WriteJSON(w, http.StatusOK, roles)
}

func (s *Service) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var req CreateRoleRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.Name("角色名称", req.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 角色名唯一性校验
	if existing, _ := s.store.GetRoleByName(r.Context(), req.Name, tenantID); existing != nil {
		httputil.WriteError(w, http.StatusConflict, "role name already exists")
		return
	}
	// 校验权限名合法
	if invalid := s.findInvalidPermissions(req.Permissions); len(invalid) > 0 {
		httputil.WriteError(w, http.StatusBadRequest, "unknown permissions: "+joinStrings(invalid))
		return
	}
	role := &Role{
		TenantID:    tenantID,
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Permissions: req.Permissions,
	}
	if err := s.store.CreateRole(r.Context(), role); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidatePermCache()
	httputil.WriteJSON(w, http.StatusCreated, role)
}

func (s *Service) handleGetRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	role, err := s.store.GetRole(r.Context(), id, tenantID)
	if err != nil || role == nil {
		httputil.WriteError(w, http.StatusNotFound, "role not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, role)
}

func (s *Service) handleUpdateRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	existing, err := s.store.GetRole(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "role not found")
		return
	}
	var req UpdateRoleRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if invalid := s.findInvalidPermissions(req.Permissions); len(invalid) > 0 {
		httputil.WriteError(w, http.StatusBadRequest, "unknown permissions: "+joinStrings(invalid))
		return
	}
	updated := *existing
	updated.DisplayName = req.DisplayName
	updated.Description = req.Description
	updated.Permissions = req.Permissions
	if err := s.store.UpdateRole(r.Context(), &updated); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidatePermCache()
	httputil.WriteJSON(w, http.StatusOK, updated)
}

func (s *Service) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	existing, err := s.store.GetRole(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "role not found")
		return
	}
	if existing.IsBuiltIn {
		httputil.WriteError(w, http.StatusBadRequest, "built-in role cannot be deleted")
		return
	}
	if err := s.store.DeleteRole(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidatePermCache()
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------- 用户组 ----------

func (s *Service) handleListGroups(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	groups, err := s.store.ListGroups(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if groups == nil {
		groups = []*Group{}
	}
	httputil.WriteJSON(w, http.StatusOK, groups)
}

func (s *Service) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var req CreateGroupRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.Name("用户组名称", req.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	group := &Group{
		TenantID:    tenantID,
		Name:        req.Name,
		Description: req.Description,
		UserIDs:     req.UserIDs,
	}
	if group.UserIDs == nil {
		group.UserIDs = []string{}
	}
	if err := s.store.CreateGroup(r.Context(), group); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, group)
}

func (s *Service) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	group, err := s.store.GetGroup(r.Context(), id, tenantID)
	if err != nil || group == nil {
		httputil.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, group)
}

func (s *Service) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	existing, err := s.store.GetGroup(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "group not found")
		return
	}
	var req UpdateGroupRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated := *existing
	updated.Name = req.Name
	updated.Description = req.Description
	updated.UserIDs = req.UserIDs
	if updated.UserIDs == nil {
		updated.UserIDs = []string{}
	}
	if err := s.store.UpdateGroup(r.Context(), &updated); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, updated)
}

func (s *Service) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.DeleteGroup(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------- AD/LDAP 目录同步 ----------

// SetLdapSyncer 注入目录同步引擎（main.go 装配；nil = 同步端点不可用）。
func (s *Service) SetLdapSyncer(sy *ldapsync.Syncer) { s.ldapSyncer = sy }

// EnsureGroup 按组名 Upsert 组织架构（实现 ldapsync.GroupWriter）：
// 同名组存在则全量覆盖成员与描述，否则新建。description 存目录 DN 便于溯源。
func (s *Service) EnsureGroup(ctx context.Context, tenantID, name, description string, memberIDs []string) error {
	if memberIDs == nil {
		memberIDs = []string{}
	}
	groups, err := s.store.ListGroups(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if g.Name == name {
			g.Description = description
			g.UserIDs = memberIDs
			return s.store.UpdateGroup(ctx, g)
		}
	}
	return s.store.CreateGroup(ctx, &Group{
		TenantID:    tenantID,
		Name:        name,
		Description: description,
		UserIDs:     memberIDs,
	})
}

// handleLdapSync 手动触发一轮 AD/LDAP 用户与组织架构同步（user:write 权限经 RBAC 链校验）。
func (s *Service) handleLdapSync(w http.ResponseWriter, r *http.Request) {
	if s.ldapSyncer == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "ldap sync not configured")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	res, err := s.ldapSyncer.Sync(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, res)
}

// ---------- 用户-角色分配 ----------

func (s *Service) handleGetUserRoles(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	tenantID := auth.TenantFromContext(r.Context())
	roleIDs, err := s.store.GetUserRoles(r.Context(), userID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if roleIDs == nil {
		roleIDs = []string{}
	}
	// 展开角色详情
	var roles []*Role
	for _, rid := range roleIDs {
		if role, _ := s.store.GetRole(r.Context(), rid, tenantID); role != nil {
			roles = append(roles, role)
		}
	}
	httputil.WriteJSON(w, http.StatusOK, UserRoles{UserID: userID, RoleIDs: roleIDs, Roles: roles})
}

func (s *Service) handleAssignUserRoles(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	tenantID := auth.TenantFromContext(r.Context())
	var req AssignUserRolesRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.SetUserRoles(r.Context(), userID, tenantID, req.RoleIDs); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, UserRoles{UserID: userID, RoleIDs: req.RoleIDs})
}

// handleGetMyPermissions 返回当前登录用户的有效权限(主角色 + 已分配 IAM 角色聚合),
// 供前端做权限感知(按角色隐藏越权按钮)。与 RBAC 中间件使用同一套 IAM 解析逻辑,
// 故角色管理页的权限调整会实时反映到这里。
func (s *Service) handleGetMyPermissions(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	role := auth.RoleFromContext(r.Context())
	perms := s.GetAggregatedPermissions(r.Context(), tenantID, userID, role)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"permissions": perms})
}

// GetAggregatedPermissions 聚合某用户的有效权限:主角色权限(经 ResolvePermissions 读取
// iam_roles 最新配置,含 cmdb:delete 等)与已分配 IAM 角色权限的并集。
func (s *Service) GetAggregatedPermissions(ctx context.Context, tenantID, userID, primaryRole string) []string {
	if tenantID == "" {
		tenantID = "default"
	}
	set := make(map[string]struct{})
	add := func(ps []string) {
		for _, p := range ps {
			set[p] = struct{}{}
		}
	}
	if primaryRole != "" {
		add(s.ResolvePermissions(tenantID, primaryRole))
	}
	roleIDs, _ := s.store.GetUserRoles(ctx, userID, tenantID)
	for _, rid := range roleIDs {
		if role, _ := s.store.GetRole(ctx, rid, tenantID); role != nil {
			add(role.Permissions)
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	return out
}

// ---------- 辅助 ----------

func (s *Service) findInvalidPermissions(perms []string) []string {
	valid := make(map[string]bool, len(s.permissions))
	for _, p := range s.permissions {
		valid[p.Name] = true
	}
	var invalid []string
	for _, p := range perms {
		if !valid[p] {
			invalid = append(invalid, p)
		}
	}
	return invalid
}

func joinStrings(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
