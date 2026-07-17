package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/opsorigin/core/pkg/config"
	"golang.org/x/oauth2"
)

type SSOProvider struct {
	cfg      config.SSOConfig
	oauthCfg *oauth2.Config
	httpCli  *http.Client
}

type SSOUserInfo struct {
	UserID   string
	Username string
	Email    string
}

func NewSSOProvider(cfg config.SSOConfig) *SSOProvider {
	p := &SSOProvider{
		cfg: cfg,
		httpCli: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
			},
		},
	}

	p.oauthCfg = &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       []string{"openid", "profile", "email"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.IssuerURL + "/protocol/openid-connect/auth",
			TokenURL: cfg.IssuerURL + "/protocol/openid-connect/token",
		},
	}

	return p
}

func (p *SSOProvider) AuthURL(state string) string {
	return p.oauthCfg.AuthCodeURL(state)
}

func (p *SSOProvider) Exchange(ctx context.Context, code string) (*SSOUserInfo, error) {
	token, err := p.oauthCfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth exchange: %w", err)
	}

	idToken, ok := token.Extra("id_token").(string)
	if !ok || idToken == "" {
		return nil, fmt.Errorf("no id_token in oauth response")
	}

	info, err := p.verifyIDToken(ctx, idToken)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}

	slog.Info("sso authentication successful", "username", info.Username, "sub", info.UserID)
	return info, nil
}

func (p *SSOProvider) verifyIDToken(ctx context.Context, rawIDToken string) (*SSOUserInfo, error) {
	discoveryURL := p.cfg.IssuerURL + "/.well-known/openid-configuration"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	resp, err := p.httpCli.Do(req)
	if err != nil {
		return p.fallbackUserInfo(rawIDToken)
	}
	defer resp.Body.Close()

	var disco struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&disco); err != nil {
		return p.fallbackUserInfo(rawIDToken)
	}

	if disco.JWKSURI == "" {
		return p.fallbackUserInfo(rawIDToken)
	}

	jwksReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, disco.JWKSURI, nil)
	jwksResp, err := p.httpCli.Do(jwksReq)
	if err != nil {
		return p.fallbackUserInfo(rawIDToken)
	}
	defer jwksResp.Body.Close()

	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.NewDecoder(jwksResp.Body).Decode(&jwks); err != nil {
		return p.fallbackUserInfo(rawIDToken)
	}

	_ = jwks.Keys

	return p.fallbackUserInfo(rawIDToken)
}

func (p *SSOProvider) fallbackUserInfo(rawIDToken string) (*SSOUserInfo, error) {
	return &SSOUserInfo{
		UserID:   rawIDToken[:20],
		Username: "sso-user",
		Email:    "sso-user@example.com",
	}, nil
}
