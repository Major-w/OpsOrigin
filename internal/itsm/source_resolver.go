package itsm

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/iam"
	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/user"
	"github.com/opsorigin/core/pkg/auth"
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
	// ListCIsByAttrs 按类型+属性等值过滤（表单级联: 机房→机柜 场景）。缺省实现见 cmdb.Service。
	ListCIsByAttrs(ctx context.Context, tenantID, typeName string, attrs map[string]string, limit int) ([]cmdb.CIRef, error)
	// GetCIAttrByName 按类型名+CI 名称取单个属性（数据联动 ci_attr 回填通道）。
	GetCIAttrByName(ctx context.Context, tenantID, typeName, name, attr string) (string, bool, error)
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

// NewCIAttrResolver 构造数据联动 ci_attr 回填通道（把 CIProvider.GetCIAttrByName
// 转为 form.CIAttrResolver，注入 formEngine 后提交校验/落库时自动执行回填）。
func NewCIAttrResolver(ci CIProvider) form.CIAttrResolver {
	return &ciAttrResolverAdapter{ci: ci}
}

type ciAttrResolverAdapter struct{ ci CIProvider }

func (a *ciAttrResolverAdapter) ResolveCIAttr(ctx context.Context, tenantID, typeName, ciName, attr string) (string, bool, error) {
	return a.ci.GetCIAttrByName(ctx, tenantID, typeName, ciName, attr)
}

// UserAttrProvider 用户属性只读能力（由 *user.Service 实现，submitter_attr 回填用）。
type UserAttrProvider interface {
	GetUserAttr(ctx context.Context, tenantID, userID, attr string) (string, bool, error)
}

// NewSubmitterAttrResolver 构造数据联动 submitter_attr 回填通道：从请求 ctx 解析
// 提单人身份（pkg/auth 中间件已写入 user_id），查其属性（如 department）回填目标字段。
func NewSubmitterAttrResolver(users UserAttrProvider) form.SubmitterAttrResolver {
	return &submitterAttrResolverAdapter{users: users}
}

type submitterAttrResolverAdapter struct{ users UserAttrProvider }

func (a *submitterAttrResolverAdapter) ResolveSubmitterAttr(ctx context.Context, tenantID, attr string) (string, bool, error) {
	userID := auth.UserIDFromContext(ctx)
	if userID == "" || strings.TrimSpace(attr) == "" {
		return "", false, nil
	}
	return a.users.GetUserAttr(ctx, tenantID, userID, attr)
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
	return r.ResolveWithParams(ctx, tenantID, source, nil)
}

// sourcePlaceholderRe 匹配 source 串里的 ${field_key} 占位符。
var sourcePlaceholderRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// SourcePlaceholderKeys 返回 source 串引用的全部参数键（前端据此判断级联依赖与是否就绪）。
// 无占位符时返回 nil（注意: 不能返回空切片, 调用方以 != nil 判断是否参数化来源）。
func SourcePlaceholderKeys(source string) []string {
	m := sourcePlaceholderRe.FindAllStringSubmatch(source, -1)
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for _, mm := range m {
		keys = append(keys, strings.TrimSpace(mm[1]))
	}
	return keys
}

// ExpandSourceParams 把 source 串中的 ${key} 占位符替换为 params[key]。
// 返回展开后的串与缺失(参数未提供或为空)的键列表。
func ExpandSourceParams(source string, params map[string]string) (string, []string) {
	missing := []string{}
	expanded := sourcePlaceholderRe.ReplaceAllStringFunc(source, func(m string) string {
		key := strings.TrimSpace(m[2 : len(m)-1])
		v, ok := params[key]
		if !ok || strings.TrimSpace(v) == "" {
			missing = append(missing, key)
			return ""
		}
		return v
	})
	return expanded, missing
}

// ResolveWithParams 参数化解析：source 串支持 ${field_key} 占位符（表单级联，如
// "cmdb:机柜?room=${room}" —— 机房字段的值作为机柜下拉的过滤参数）。
// 语法: <base_source>[?<attr>=${field}[&<attr2>=<static>...]]，attr 过滤仅对 cmdb:<type> 生效。
// 任一占位符缺失时返回空选项（级联上级未选, 下级不给候选），并携带 missing 参数提示。
func (r *cmdbIAMResolver) ResolveWithParams(ctx context.Context, tenantID, source string, params map[string]string) ([]form.FieldOption, error) {
	source = strings.TrimSpace(source)
	if source == "" || source == "static" {
		return nil, nil
	}
	if len(SourcePlaceholderKeys(source)) == 0 && len(params) == 0 {
		return r.resolveCached(ctx, tenantID, source)
	}
	expanded, missing := ExpandSourceParams(source, params)
	if len(missing) > 0 {
		// 级联前置字段未选: 返回空选项, 由前端提示"先选择上级字段"。
		return []form.FieldOption{}, nil
	}
	// 带 attr 过滤的 cmdb 源: cmdb:<type>?k=v&k2=v2
	if base, filters, ok := splitSourceFilters(expanded); ok {
		if !strings.HasPrefix(base, SourcePrefixCMDB) || strings.HasPrefix(base, SourcePrefixCMDB+"types") {
			// 过滤语法仅对 CI 类型源有意义; 其他来源忽略过滤段退化平铺
			return r.resolveCached(ctx, tenantID, base)
		}
		typeName := strings.TrimSpace(strings.TrimPrefix(base, SourcePrefixCMDB))
		if r.ci == nil {
			return nil, fmt.Errorf("CMDB 未接入，无法解析选项来源 %q", source)
		}
		refs, err := r.ci.ListCIsByAttrs(ctx, tenantID, typeName, filters, 0)
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
	}
	// 无过滤段但 source 本身含占位符（如 iam:user@${team}）: 不走 TTL 缓存, 直接解析。
	return r.resolveUncached(ctx, tenantID, expanded)
}

// resolveCached 带 TTL 缓存的解析（原 Resolve 主体）。
func (r *cmdbIAMResolver) resolveCached(ctx context.Context, tenantID, source string) ([]form.FieldOption, error) {
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

// splitSourceFilters 拆出 source 的 "?k=v&k2=v2" 过滤段（? 后按 & 分割）。
// 无 ? 段返回 ok=false。
func splitSourceFilters(source string) (base string, filters map[string]string, ok bool) {
	at := strings.Index(source, "?")
	if at < 0 {
		return source, nil, false
	}
	base = strings.TrimSpace(source[:at])
	filters = map[string]string{}
	for _, pair := range strings.Split(source[at+1:], "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			continue
		}
		filters[strings.TrimSpace(kv[0])] = kv[1]
	}
	return base, filters, true
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
		// "iam:user@<组ID>[,<组ID>...]" 把候选人限定在若干用户组的成员并集内
		groupScope := ""
		if at := strings.Index(kind, "@"); at >= 0 {
			groupScope = strings.TrimSpace(kind[at+1:])
			kind = strings.TrimSpace(kind[:at])
		}
		switch kind {
		case "user":
			if r.users == nil {
				return nil, fmt.Errorf("用户中心未接入，无法解析选项来源 %q", source)
			}
			allow, err := r.groupMemberSet(ctx, tenantID, groupScope)
			if err != nil {
				return nil, err
			}
			us, err := r.users.ListUsers(ctx, tenantID, "", 0)
			if err != nil {
				return nil, err
			}
			opts := make([]form.FieldOption, 0, len(us))
			for _, u := range us {
				if allow != nil {
					if _, ok := allow[u.ID]; !ok {
						continue
					}
				}
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

// groupMemberSet 把逗号分隔的用户组 ID 列表展开为成员用户 ID 集合。
// scope 为空返回 nil，表示不做限定（全量用户可选）；引用了不存在的组时只跳过该组，
// 避免组被删除后整个字段的候选人凭空清零。
func (r *cmdbIAMResolver) groupMemberSet(ctx context.Context, tenantID, scope string) (map[string]struct{}, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, nil
	}
	if r.iam == nil {
		return nil, fmt.Errorf("IAM 未接入，无法按用户组限定候选人")
	}
	wanted := map[string]struct{}{}
	for _, id := range strings.Split(scope, ",") {
		if id = strings.TrimSpace(id); id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	groups, err := r.iam.ListGroups(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	members := map[string]struct{}{}
	for _, g := range groups {
		if g == nil {
			continue
		}
		if _, ok := wanted[g.ID]; !ok {
			continue
		}
		for _, uid := range g.UserIDs {
			if uid = strings.TrimSpace(uid); uid != "" {
				members[uid] = struct{}{}
			}
		}
	}
	return members, nil
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
