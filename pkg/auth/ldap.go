package auth

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/opsorigin/core/pkg/config"
)

type LDAPProvider struct {
	cfg config.LDAPConfig
}

func NewLDAPProvider(cfg config.LDAPConfig) *LDAPProvider {
	return &LDAPProvider{cfg: cfg}
}

type LDAPUserInfo struct {
	UserID     string
	Username   string
	Email      string
	Department string // 目录部门属性（AD: department），ITSM 提单人属性派单用
	DN         string
}

func (p *LDAPProvider) Authenticate(username, password string) (*LDAPUserInfo, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("invalid credentials")
	}

	l, err := p.dial()
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	defer l.Close()

	if err := l.Bind(p.cfg.BindDN, p.cfg.BindPass); err != nil {
		slog.Warn("ldap bind failed", "error", err)
		return nil, fmt.Errorf("ldap bind: %w", err)
	}

	filter := fmt.Sprintf(p.cfg.UserFilter, username)
	searchReq := ldap.NewSearchRequest(
		p.cfg.BaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, 0, false,
		filter,
		[]string{"dn", "cn", "mail", "uid", p.DepartmentAttrName()},
		nil,
	)

	result, err := l.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}

	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("user not found in ldap")
	}

	entry := result.Entries[0]
	if err := l.Bind(entry.DN, password); err != nil {
		return nil, fmt.Errorf("invalid ldap credentials")
	}

	info := &LDAPUserInfo{
		UserID:     entry.GetAttributeValue("uid"),
		Username:   entry.GetAttributeValue("cn"),
		Email:      entry.GetAttributeValue("mail"),
		Department: entry.GetAttributeValue(p.DepartmentAttrName()),
		DN:         entry.DN,
	}
	if info.UserID == "" {
		info.UserID = username
	}

	slog.Info("ldap authentication successful", "username", username)
	return info, nil
}

func (p *LDAPProvider) dial() (*ldap.Conn, error) {
	addr := p.cfg.Addr
	if strings.HasPrefix(addr, "ldaps://") {
		return ldap.DialTLS("tcp", strings.TrimPrefix(addr, "ldaps://"), &tls.Config{InsecureSkipVerify: true})
	}
	return ldap.Dial("tcp", strings.TrimPrefix(addr, "ldap://"))
}

// SearchEntries 以服务账号（BindDN）执行目录搜索，供用户/组织架构同步使用。
// base 为空时使用 BaseDN；filter 需调用方自带括号（如 "(objectClass=person)"）。
func (p *LDAPProvider) SearchEntries(base, filter string, attrs []string) ([]*ldap.Entry, error) {
	if strings.TrimSpace(filter) == "" {
		return nil, fmt.Errorf("ldap search: empty filter")
	}
	if base == "" {
		base = p.cfg.BaseDN
	}
	l, err := p.dial()
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	defer l.Close()
	if err := l.Bind(p.cfg.BindDN, p.cfg.BindPass); err != nil {
		return nil, fmt.Errorf("ldap bind: %w", err)
	}
	result, err := l.Search(ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases, 0, 0, false, filter, attrs, nil))
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	return result.Entries, nil
}

// GetEntry 按 DN 精确读取单条目（ScopeBaseObject），供组 member(DN) 反查用户属性。
func (p *LDAPProvider) GetEntry(dn string, attrs []string) (*ldap.Entry, error) {
	l, err := p.dial()
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	defer l.Close()
	if err := l.Bind(p.cfg.BindDN, p.cfg.BindPass); err != nil {
		return nil, fmt.Errorf("ldap bind: %w", err)
	}
	result, err := l.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject,
		ldap.NeverDerefAliases, 1, 0, false, "(objectClass=*)", attrs, nil))
	if err != nil {
		return nil, fmt.Errorf("ldap get entry: %w", err)
	}
	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("entry not found: %s", dn)
	}
	return result.Entries[0], nil
}

// ResolveDNAttrs 单连接批量按 DN 反查指定属性（组 member(DN) → 用户名场景）。
// 返回 map：dn → 属性值；单条失败/属性为空时该 DN 不出现在结果里。
func (p *LDAPProvider) ResolveDNAttrs(dns []string, attr string) map[string]string {
	out := map[string]string{}
	if attr == "" || len(dns) == 0 {
		return out
	}
	l, err := p.dial()
	if err != nil {
		slog.Warn("ldap resolve dns: dial failed", "error", err)
		return out
	}
	defer l.Close()
	if err := l.Bind(p.cfg.BindDN, p.cfg.BindPass); err != nil {
		slog.Warn("ldap resolve dns: bind failed", "error", err)
		return out
	}
	for _, dn := range dns {
		if strings.TrimSpace(dn) == "" {
			continue
		}
		result, err := l.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject,
			ldap.NeverDerefAliases, 1, 0, false, "(objectClass=*)", []string{attr}, nil))
		if err != nil || len(result.Entries) == 0 {
			continue
		}
		if v := result.Entries[0].GetAttributeValue(attr); v != "" {
			out[dn] = v
		}
	}
	return out
}

// UserAttrName 用户名属性（AD 为 sAMAccountName，OpenLDAP 为 uid），空回退 uid。
func (p *LDAPProvider) UserAttrName() string {
	if s := strings.TrimSpace(p.cfg.UserAttr); s != "" {
		return s
	}
	return "uid"
}

// DepartmentAttrName 用户部门属性（AD 原生 department），空回退 department。
func (p *LDAPProvider) DepartmentAttrName() string {
	if s := strings.TrimSpace(p.cfg.DepartmentAttr); s != "" {
		return s
	}
	return "department"
}

// SyncUserFilter 全量用户同步过滤器，空 = 未开启用户同步。
func (p *LDAPProvider) SyncUserFilter() string { return strings.TrimSpace(p.cfg.SyncUserFilter) }

// GroupFilter 组同步过滤器，空 = 未开启组同步。
func (p *LDAPProvider) GroupFilter() string { return strings.TrimSpace(p.cfg.GroupFilter) }

// GroupBaseDN 组搜索基准 DN，空回退 BaseDN。
func (p *LDAPProvider) GroupBaseDN() string {
	if s := strings.TrimSpace(p.cfg.GroupBaseDN); s != "" {
		return s
	}
	return p.cfg.BaseDN
}

// GroupUserAttr 组成员属性（member=DN 列表 / memberUid=用户名列表），空回退 member。
func (p *LDAPProvider) GroupUserAttr() string {
	if s := strings.TrimSpace(p.cfg.GroupUserAttr); s != "" {
		return s
	}
	return "member"
}

// SyncIntervalMinutes 定时同步间隔（分钟），0 = 仅手动触发。
func (p *LDAPProvider) SyncIntervalMinutes() int { return p.cfg.SyncIntervalMin }
