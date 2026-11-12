// Package ldapsync AD/LDAP 目录同步引擎：把目录中的用户与组织架构（组）同步到平台。
//
// 分层约束：本包只依赖 pkg/auth（LDAP 连接与搜索）并定义最小写侧接口
// （UserWriter / GroupWriter），由 cmd/core/main.go 装配 user / iam 层实现注入，
// 不反向依赖任何具体实现（与 RAGEvaluator / MCPToolSet 同范式）。
//
// 同步语义（全量对齐，幂等可重跑）：
//   - 用户：按 SyncUserFilter 全量拉取 → 按用户名 Upsert（不存在建档 role=operator、
//     存在同步邮箱）。目录中已删除的用户不做本地禁用（避免误伤手工账号），如需
//     硬一致可后续扩展 exclude 策略。
//   - 组：按 GroupFilter 全量拉取 → 按组名 Upsert，成员属性支持 memberUid（直接
//     是用户名）与 member（DN 列表，单连接批量反查用户名）；目录中不存在映射的
//     本地用户记 warning 跳过，不阻断整轮同步。
package ldapsync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// UserWriter 用户建档能力（由 user 服务层适配实现）。
type UserWriter interface {
	// EnsureUser 按用户名 Upsert：返回用户 ID。不存在时建档（默认 operator/active），
	// 已存在时同步邮箱与部门。created=true 表示本次新建。
	EnsureUser(ctx context.Context, tenantID, username, email, department string) (id string, created bool, err error)
	// ListUsernameIDMap 本地 username → user ID 映射（组成员名转 ID 用）。
	ListUsernameIDMap(ctx context.Context, tenantID string) (map[string]string, error)
}

// GroupWriter 组织架构建档能力（由 iam 服务层适配实现）。
type GroupWriter interface {
	// EnsureGroup 按组名 Upsert：memberIDs 全量覆盖组成员。
	EnsureGroup(ctx context.Context, tenantID, name, description string, memberIDs []string) error
}

// DepartmentWriter 部门镜像能力（由 iam 服务层适配实现）：把目录中用户携带的
// 部门属性值（如 AD department）按名幂等建档为平台部门实体（source=ldap）。
type DepartmentWriter interface {
	EnsureDepartment(ctx context.Context, tenantID, name, source string) (id string, created bool, err error)
}

// Result 一轮同步的摘要（同步端点返回体）。
type Result struct {
	UsersTotal   int      `json:"users_total"`
	UsersCreated int      `json:"users_created"`
	GroupsTotal  int      `json:"groups_total"`
	GroupsSynced int      `json:"groups_synced"`
	Warnings     []string `json:"warnings,omitempty"`
}

// DirectorySearcher 目录搜索能力（*auth.LDAPProvider 实现；抽象成接口便于单测 fake）。
type DirectorySearcher interface {
	SearchEntries(base, filter string, attrs []string) ([]*ldap.Entry, error)
	ResolveDNAttrs(dns []string, attr string) map[string]string
	UserAttrName() string
	DepartmentAttrName() string
	SyncUserFilter() string
	GroupFilter() string
	GroupBaseDN() string
	GroupUserAttr() string
}

// Syncer 同步引擎。LDAP/Users/Groups/Depts 由 main.go 装配注入。
type Syncer struct {
	LDAP   DirectorySearcher
	Users  UserWriter
	Groups GroupWriter
	// Depts 部门镜像（可选）：nil 时目录部门值不做归档（记 warning）。
	Depts DepartmentWriter
	// MaxDNLookup member(DN) 模式下单组反查 DN 上限（防目录异常导致请求风暴），默认 500。
	MaxDNLookup int
}

// Sync 执行一轮全量同步（用户 → 组），幂等可重跑。
func (s *Syncer) Sync(ctx context.Context, tenantID string) (*Result, error) {
	if s.LDAP == nil || s.Users == nil {
		return nil, fmt.Errorf("ldap sync: engine not configured")
	}
	res := &Result{}
	maxLookup := s.MaxDNLookup
	if maxLookup <= 0 {
		maxLookup = 500
	}

	// ---- 1. 用户全量同步 ----
	userFilter := s.LDAP.SyncUserFilter()
	if userFilter == "" {
		return nil, fmt.Errorf("ldap sync: user sync filter not configured (OPSORIGIN_LDAP_SYNC_USER_FILTER)")
	}
	attr := s.LDAP.UserAttrName()
	deptAttr := s.LDAP.DepartmentAttrName()
	entries, err := s.LDAP.SearchEntries("", userFilter, []string{attr, "mail", "cn", deptAttr})
	if err != nil {
		return nil, err
	}
	// 有序去重：同名取首个条目（目录里同名多 OU 场景保证确定性）
	emails := map[string]string{}
	depts := map[string]string{}
	var usernames []string
	for _, e := range entries {
		name := strings.TrimSpace(e.GetAttributeValue(attr))
		if name == "" {
			continue
		}
		if _, seen := emails[name]; seen {
			continue
		}
		emails[name] = strings.TrimSpace(e.GetAttributeValue("mail"))
		depts[name] = strings.TrimSpace(e.GetAttributeValue(deptAttr))
		usernames = append(usernames, name)
	}
	res.UsersTotal = len(usernames)
	for _, name := range usernames {
		id, created, err := s.Users.EnsureUser(ctx, tenantID, name, emails[name], depts[name])
		if err != nil {
			return nil, fmt.Errorf("ldap sync: ensure user %s: %w", name, err)
		}
		if created {
			res.UsersCreated++
		}
		_ = id
	}
	slog.Info("ldap sync: users done", "tenant", tenantID, "total", res.UsersTotal, "created", res.UsersCreated)

	// ---- 1.5 部门镜像（可选）：目录用户携带的部门属性值 → 平台部门实体 ----
	var deptNames []string
	for _, name := range usernames {
		if d := depts[name]; d != "" {
			deptNames = append(deptNames, d)
		}
	}
	if len(deptNames) > 0 && s.Depts == nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d department values found but department writer not wired", len(deptNames)))
	}
	deptSeen := map[string]bool{}
	if s.Depts != nil {
		for _, name := range deptNames {
			if deptSeen[name] {
				continue
			}
			deptSeen[name] = true
			if _, _, err := s.Depts.EnsureDepartment(ctx, tenantID, name, "ldap"); err != nil {
				// 部门镜像失败不阻断整轮同步，记 warning
				res.Warnings = append(res.Warnings, fmt.Sprintf("ensure department %s: %v", name, err))
			}
		}
		if len(deptSeen) > 0 {
			slog.Info("ldap sync: departments mirrored", "tenant", tenantID, "total", len(deptSeen))
		}
	}

	// ---- 2. 组同步（可选） ----
	groupFilter := s.LDAP.GroupFilter()
	if groupFilter == "" {
		return res, nil
	}
	if s.Groups == nil {
		res.Warnings = append(res.Warnings, "group filter configured but group writer not wired; groups skipped")
		return res, nil
	}
	local, err := s.Users.ListUsernameIDMap(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("ldap sync: list local users: %w", err)
	}
	isDNMode := s.LDAP.GroupUserAttr() == "member"
	gentries, err := s.LDAP.SearchEntries(s.LDAP.GroupBaseDN(), groupFilter, []string{"cn", s.LDAP.GroupUserAttr()})
	if err != nil {
		return nil, err
	}
	res.GroupsTotal = len(gentries)
	for _, ge := range gentries {
		name := strings.TrimSpace(ge.GetAttributeValue("cn"))
		if name == "" {
			continue
		}
		raw := ge.GetAttributeValues(s.LDAP.GroupUserAttr())
		var usernamesOfGroup []string
		if isDNMode {
			if len(raw) > maxLookup {
				raw = raw[:maxLookup]
				res.Warnings = append(res.Warnings, fmt.Sprintf("group %s: members truncated to %d", name, maxLookup))
			}
			resolved := s.LDAP.ResolveDNAttrs(raw, attr)
			for _, dn := range raw {
				if u := resolved[dn]; u != "" {
					usernamesOfGroup = append(usernamesOfGroup, u)
				}
			}
		} else {
			usernamesOfGroup = raw // memberUid：即用户名列表
		}
		var memberIDs []string
		for _, u := range usernamesOfGroup {
			if id, ok := local[strings.TrimSpace(u)]; ok {
				memberIDs = append(memberIDs, id)
			} else {
				res.Warnings = append(res.Warnings, fmt.Sprintf("group %s: user %q not found locally, skipped", name, u))
			}
		}
		if err := s.Groups.EnsureGroup(ctx, tenantID, name, ge.DN, memberIDs); err != nil {
			return nil, fmt.Errorf("ldap sync: ensure group %s: %w", name, err)
		}
		res.GroupsSynced++
	}
	slog.Info("ldap sync: groups done", "tenant", tenantID, "total", res.GroupsTotal, "synced", res.GroupsSynced, "warnings", len(res.Warnings))
	return res, nil
}
