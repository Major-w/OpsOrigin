package iam

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
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
	// 用户-角色分配
	SetUserRoles(ctx context.Context, userID, tenantID string, roleIDs []string) error
	GetUserRoles(ctx context.Context, userID, tenantID string) ([]string, error)
}

// MemoryStore 是并发安全的内存实现。
type MemoryStore struct {
	mu        sync.RWMutex
	roles     map[string]*Role
	groups    map[string]*Group
	userRoles map[string][]string // userID -> roleIDs
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		roles:     make(map[string]*Role),
		groups:    make(map[string]*Group),
		userRoles: make(map[string][]string),
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
}

// NewService 创建 Service 并 seed 预置权限与内置角色。
func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = newPGStore(db)
	}
	s := &Service{
		store:       st,
		permissions: seedPermissions(),
	}
	s.seedBuiltinRoles()
	return s
}

// seedPermissions 返回平台预定义的权限点清单。
// 与 pkg/auth/rbac.go 中的 Permission 常量保持一致。
func seedPermissions() []Permission {
	return []Permission{
		{Name: "cmdb:read", Description: "查看 CMDB 资源", Category: "CMDB 资产管理"},
		{Name: "cmdb:write", Description: "新增/修改 CMDB 资源", Category: "CMDB 资产管理"},
		{Name: "cmdb:admin", Description: "管理 CMDB 模型(删除/改模型)", Category: "CMDB 资产管理"},
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
	}
}

// seedBuiltinRoles 预置 admin/operator/viewer 三个内置角色,
// 权限清单与 pkg/auth/rbac.go 的 RolePermissions 保持一致。
func (s *Service) seedBuiltinRoles() {
	ctx := context.Background()
	builtins := []struct {
		name, display, desc string
		perms                []string
	}{
		{"admin", "管理员", "系统管理员,拥有全部权限", []string{
			"cmdb:read", "cmdb:write", "cmdb:admin",
			"monitor:read", "monitor:write",
			"itsm:read", "itsm:write", "itsm:approve",
			"user:read", "user:write",
			"agent:read", "agent:write",
			"audit:read", "system:admin",
		}},
		{"operator", "操作员", "日常运维操作,无系统级权限", []string{
			"cmdb:read", "cmdb:write",
			"monitor:read", "monitor:write",
			"itsm:read", "itsm:write",
			"user:read",
			"agent:read", "agent:write",
			"audit:read",
		}},
		{"viewer", "只读用户", "仅可查看,不可修改", []string{
			"cmdb:read",
			"monitor:read",
			"itsm:read",
			"user:read",
			"agent:read",
			"audit:read",
		}},
	}
	for _, b := range builtins {
		r := &Role{
			TenantID:    "default",
			Name:        b.name,
			DisplayName: b.display,
			Description: b.desc,
			Permissions: b.perms,
			IsBuiltIn:   true,
		}
		s.store.CreateRole(ctx, r)
	}
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
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
	// 用户-角色分配
	mux.HandleFunc("GET /api/v1/iam/users/{user_id}/roles", s.handleGetUserRoles)
	mux.HandleFunc("PUT /api/v1/iam/users/{user_id}/roles", s.handleAssignUserRoles)
}

// ---------- 权限 ----------

func (s *Service) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.permissions)
}

// ---------- 角色 ----------

func (s *Service) handleListRoles(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	roles, err := s.store.ListRoles(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if roles == nil {
		roles = []*Role{}
	}
	writeJSON(w, http.StatusOK, roles)
}

func (s *Service) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var req CreateRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	// 角色名唯一性校验
	if existing, _ := s.store.GetRoleByName(r.Context(), req.Name, tenantID); existing != nil {
		writeError(w, http.StatusConflict, "role name already exists")
		return
	}
	// 校验权限名合法
	if invalid := s.findInvalidPermissions(req.Permissions); len(invalid) > 0 {
		writeError(w, http.StatusBadRequest, "unknown permissions: "+joinStrings(invalid))
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

func (s *Service) handleGetRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	role, err := s.store.GetRole(r.Context(), id, tenantID)
	if err != nil || role == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (s *Service) handleUpdateRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, err := s.store.GetRole(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	var req UpdateRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if invalid := s.findInvalidPermissions(req.Permissions); len(invalid) > 0 {
		writeError(w, http.StatusBadRequest, "unknown permissions: "+joinStrings(invalid))
		return
	}
	updated := *existing
	updated.DisplayName = req.DisplayName
	updated.Description = req.Description
	updated.Permissions = req.Permissions
	if err := s.store.UpdateRole(r.Context(), &updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Service) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, err := s.store.GetRole(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "role not found")
		return
	}
	if existing.IsBuiltIn {
		writeError(w, http.StatusBadRequest, "built-in role cannot be deleted")
		return
	}
	if err := s.store.DeleteRole(r.Context(), id, tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------- 用户组 ----------

func (s *Service) handleListGroups(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	groups, err := s.store.ListGroups(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if groups == nil {
		groups = []*Group{}
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Service) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var req CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, group)
}

func (s *Service) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	group, err := s.store.GetGroup(r.Context(), id, tenantID)
	if err != nil || group == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Service) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, err := s.store.GetGroup(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	var req UpdateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Service) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.DeleteGroup(r.Context(), id, tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------- 用户-角色分配 ----------

func (s *Service) handleGetUserRoles(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	tenantID := tenantFromCtx(r.Context())
	roleIDs, err := s.store.GetUserRoles(r.Context(), userID, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, UserRoles{UserID: userID, RoleIDs: roleIDs, Roles: roles})
}

func (s *Service) handleAssignUserRoles(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	tenantID := tenantFromCtx(r.Context())
	var req AssignUserRolesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.SetUserRoles(r.Context(), userID, tenantID, req.RoleIDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, UserRoles{UserID: userID, RoleIDs: req.RoleIDs})
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
