package itsm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/iam"
	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/user"
)

// 动态选项来源前缀约定：
//
//	cmdb:<typeName>  → CMDB 中该模型类型下的配置项（value=CI ID，label=CI 名称）
//	iam:user         → 平台用户（value=用户 ID，label=用户名）
//	iam:role         → IAM 角色（value=角色名，label=显示名）
//	iam:group        → IAM 用户组（value=组 ID，label=组名）
//	cmdb:types       → CMDB 模型类型层级树（value=类型 ID，label=类型名，供 cascader 级联）
const (
	SourcePrefixCMDB = "cmdb:"
	SourcePrefixIAM  = "iam:"

	// SourceCMDBTypes 模型类型层级树，是当前唯一带真实层级语义的来源。
	SourceCMDBTypes = "cmdb:types"
)

// maxTypeTreeDepth 组树深度上限，防御脏数据造成的环路 / 超深继承链。
const maxTypeTreeDepth = 8

// CIProvider CMDB 侧只读能力（由 *cmdb.Service 实现，鸭子类型解耦）。
type CIProvider interface {
	ListCIsByTypeName(ctx context.Context, tenantID, typeName, keyword string, limit int) ([]cmdb.CIRef, error)
	ListCITypeNames(ctx context.Context, tenantID string) ([]string, error)
	ListCITypeTree(ctx context.Context, tenantID string) ([]cmdb.CITypeNode, error)
}

// UserProvider 用户中心只读能力（由 *user.Service 实现）。
type UserProvider interface {
	ListUsers(ctx context.Context, tenantID, keyword string, limit int) ([]user.UserRef, error)
}

// IAMProvider 角色/用户组只读能力（由 *iam.Service 实现）。
type IAMProvider interface {
	ListRoles(ctx context.Context, tenantID string) ([]*iam.Role, error)
	ListGroups(ctx context.Context, tenantID string) ([]*iam.Group, error)
}

// cmdbIAMResolver 把 FieldDef.Source 解析为可选项列表，带 TTL 缓存避免每次渲染打后端。
type cmdbIAMResolver struct {
	ci    CIProvider
	users UserProvider
	iam   IAMProvider

	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]cachedOptions
}

type cachedOptions struct {
	opts     []form.FieldOption
	expireAt time.Time
}

// NewCMDBIAMResolver 构造动态选项解析器。任一 provider 可为 nil，对应前缀直接报错（上层按宽松策略放行）。
func NewCMDBIAMResolver(ci CIProvider, users UserProvider, iamSvc IAMProvider) form.SourceResolver {
	return &cmdbIAMResolver{
		ci:    ci,
		users: users,
		iam:   iamSvc,
		ttl:   30 * time.Second,
		cache: map[string]cachedOptions{},
	}
}

// Resolve 实现 form.SourceResolver。
func (r *cmdbIAMResolver) Resolve(ctx context.Context, tenantID, source string) ([]form.FieldOption, error) {
	source = strings.TrimSpace(source)
	if source == "" || source == "static" {
		return nil, nil
	}
	key := tenantID + "|" + source
	if opts, ok := r.get(key); ok {
		return opts, nil
	}
	opts, err := r.resolveUncached(ctx, tenantID, source)
	if err != nil {
		return nil, err
	}
	r.put(key, opts)
	return opts, nil
}

// ResolveTree 实现 form.TreeSourceResolver：按层级取选项。
// 目前仅 cmdb:types 有真实层级；其余来源退化为"根层级返回全量、子层级为空"。
func (r *cmdbIAMResolver) ResolveTree(ctx context.Context, tenantID, source, parent string) ([]form.FieldOption, error) {
	source = strings.TrimSpace(source)
	parent = strings.TrimSpace(parent)
	if source == "" || source == "static" {
		return nil, nil
	}
	if source != SourceCMDBTypes {
		if parent != "" {
			return nil, nil
		}
		return r.Resolve(ctx, tenantID, source)
	}
	if r.ci == nil {
		return nil, fmt.Errorf("CMDB 未接入，无法解析选项来源 %q", source)
	}

	key := tenantID + "|" + source + "|tree:" + parent
	if opts, ok := r.get(key); ok {
		return opts, nil
	}
	nodes, err := r.ci.ListCITypeTree(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	opts := buildTypeTree(nodes, parent)
	r.put(key, opts)
	return opts, nil
}

// buildTypeTree 把扁平的模型类型列表按 ParentID 组装成以 parent 为根的嵌套选项树。
// 父节点缺失（跨租户引用 / 已删除）的节点上提到根，避免整棵子树凭空消失；
// 同时用 visited + 深度上限双保险防御脏数据环路。
func buildTypeTree(nodes []cmdb.CITypeNode, parent string) []form.FieldOption {
	known := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		if strings.TrimSpace(n.ID) != "" {
			known[n.ID] = struct{}{}
		}
	}
	childrenBy := make(map[string][]cmdb.CITypeNode, len(nodes))
	for _, n := range nodes {
		if strings.TrimSpace(n.ID) == "" {
			continue
		}
		pid := n.ParentID
		if pid != "" {
			if _, ok := known[pid]; !ok {
				pid = ""
			}
		}
		childrenBy[pid] = append(childrenBy[pid], n)
	}

	visited := make(map[string]struct{}, len(nodes))
	var walk func(pid string, depth int) []form.FieldOption
	walk = func(pid string, depth int) []form.FieldOption {
		kids := childrenBy[pid]
		if len(kids) == 0 || depth > maxTypeTreeDepth {
			return nil
		}
		out := make([]form.FieldOption, 0, len(kids))
		for _, k := range kids {
			if _, dup := visited[k.ID]; dup {
				continue
			}
			visited[k.ID] = struct{}{}
			label := k.Name
			if label == "" {
				label = k.ID
			}
			out = append(out, form.FieldOption{
				Value:    k.ID,
				Label:    label,
				Children: walk(k.ID, depth+1),
			})
		}
		return out
	}
	return walk(parent, 0)
}

func (r *cmdbIAMResolver) resolveUncached(ctx context.Context, tenantID, source string) ([]form.FieldOption, error) {
	switch {
	case source == SourceCMDBTypes:
		if r.ci == nil {
			return nil, fmt.Errorf("CMDB 未接入，无法解析选项来源 %q", source)
		}
		nodes, err := r.ci.ListCITypeTree(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		return buildTypeTree(nodes, ""), nil

	case strings.HasPrefix(source, SourcePrefixCMDB):
		typeName := strings.TrimSpace(strings.TrimPrefix(source, SourcePrefixCMDB))
		if r.ci == nil {
			return nil, fmt.Errorf("CMDB 未接入，无法解析选项来源 %q", source)
		}
		refs, err := r.ci.ListCIsByTypeName(ctx, tenantID, typeName, "", 0)
		if err != nil {
			return nil, err
		}
		opts := make([]form.FieldOption, 0, len(refs))
		for _, c := range refs {
			label := c.Name
			if label == "" {
				label = c.ID
			}
			opts = append(opts, form.FieldOption{Value: c.ID, Label: label})
		}
		return opts, nil

	case strings.HasPrefix(source, SourcePrefixIAM):
		kind := strings.TrimSpace(strings.TrimPrefix(source, SourcePrefixIAM))
		switch kind {
		case "user":
			if r.users == nil {
				return nil, fmt.Errorf("用户中心未接入，无法解析选项来源 %q", source)
			}
			us, err := r.users.ListUsers(ctx, tenantID, "", 0)
			if err != nil {
				return nil, err
			}
			opts := make([]form.FieldOption, 0, len(us))
			for _, u := range us {
				label := u.Username
				if u.Email != "" {
					label = fmt.Sprintf("%s (%s)", u.Username, u.Email)
				}
				opts = append(opts, form.FieldOption{Value: u.ID, Label: label})
			}
			return opts, nil
		case "role":
			if r.iam == nil {
				return nil, fmt.Errorf("IAM 未接入，无法解析选项来源 %q", source)
			}
			roles, err := r.iam.ListRoles(ctx, tenantID)
			if err != nil {
				return nil, err
			}
			opts := make([]form.FieldOption, 0, len(roles))
			for _, role := range roles {
				if role == nil {
					continue
				}
				label := role.DisplayName
				if label == "" {
					label = role.Name
				}
				opts = append(opts, form.FieldOption{Value: role.Name, Label: label})
			}
			return opts, nil
		case "group":
			if r.iam == nil {
				return nil, fmt.Errorf("IAM 未接入，无法解析选项来源 %q", source)
			}
			groups, err := r.iam.ListGroups(ctx, tenantID)
			if err != nil {
				return nil, err
			}
			opts := make([]form.FieldOption, 0, len(groups))
			for _, g := range groups {
				if g == nil {
					continue
				}
				opts = append(opts, form.FieldOption{Value: g.ID, Label: g.Name})
			}
			return opts, nil
		}
		return nil, fmt.Errorf("不支持的 IAM 选项来源 %q", source)
	}
	return nil, fmt.Errorf("不支持的选项来源 %q", source)
}

// SourceCatalog 返回可用的 source 取值清单，供表单配置界面下拉选择。
func (r *cmdbIAMResolver) SourceCatalog(ctx context.Context, tenantID string) []string {
	out := []string{"iam:user", "iam:role", "iam:group"}
	if r.ci != nil {
		out = append(out, SourceCMDBTypes) // 级联专用：模型类型层级树
		if names, err := r.ci.ListCITypeNames(ctx, tenantID); err == nil {
			for _, n := range names {
				if n = strings.TrimSpace(n); n != "" {
					out = append(out, SourcePrefixCMDB+n)
				}
			}
		}
	}
	return out
}

func (r *cmdbIAMResolver) get(key string) ([]form.FieldOption, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.cache[key]
	if !ok || time.Now().After(e.expireAt) {
		return nil, false
	}
	return e.opts, true
}

func (r *cmdbIAMResolver) put(key string, opts []form.FieldOption) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 简单容量兜底：超过阈值直接清空重来，避免多租户 × 多来源无限增长
	if len(r.cache) > 512 {
		r.cache = map[string]cachedOptions{}
	}
	r.cache[key] = cachedOptions{opts: opts, expireAt: time.Now().Add(r.ttl)}
}

// sourceCataloger 可选能力：解析器若能列出可用 source 清单则实现之。
type sourceCataloger interface {
	SourceCatalog(ctx context.Context, tenantID string) []string
}

// 编译期断言：内置解析器同时具备平铺与层级解析能力。
var _ form.TreeSourceResolver = (*cmdbIAMResolver)(nil)
