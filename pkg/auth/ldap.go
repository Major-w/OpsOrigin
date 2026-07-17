package auth

import (
	"crypto/tls"
	"fmt"
	"log/slog"

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
	UserID   string
	Username string
	Email    string
	DN       string
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
		[]string{"dn", "cn", "mail", "uid"},
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
		UserID:   entry.GetAttributeValue("uid"),
		Username: entry.GetAttributeValue("cn"),
		Email:    entry.GetAttributeValue("mail"),
		DN:       entry.DN,
	}
	if info.UserID == "" {
		info.UserID = username
	}

	slog.Info("ldap authentication successful", "username", username)
	return info, nil
}

func (p *LDAPProvider) dial() (*ldap.Conn, error) {
	if len(p.cfg.Addr) > 3 && p.cfg.Addr[:4] == "ldaps" {
		return ldap.DialTLS("tcp", p.cfg.Addr[7:], &tls.Config{InsecureSkipVerify: true})
	}
	return ldap.Dial("tcp", p.cfg.Addr[7:])
}
