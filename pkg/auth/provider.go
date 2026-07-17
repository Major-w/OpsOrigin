package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const defaultTokenTTL = 24 * time.Hour

type Provider struct {
	jwtManager *JWTManager
	ldap       *LDAPProvider
	sso        *SSOProvider
}

type Claims struct {
	UserID   string `json:"uid"`
	TenantID string `json:"tid"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"`
}

func NewProvider(secret string, ldapCfg *LDAPProvider, ssoCfg *SSOProvider) *Provider {
	return &Provider{
		jwtManager: NewJWTManager(secret),
		ldap:       ldapCfg,
		sso:        ssoCfg,
	}
}

func (p *Provider) GenerateToken(userID, tenantID, role string, ttl time.Duration) (string, error) {
	return p.jwtManager.GenerateToken(userID, tenantID, role, ttl)
}

func (p *Provider) ValidateToken(token string) (*Claims, error) {
	payload, err := p.jwtManager.ValidateToken(token)
	if err != nil {
		return nil, err
	}

	return &Claims{
		UserID:   payload.UserID,
		TenantID: payload.TenantID,
		Role:     payload.Role,
		Exp:      payload.ExpiresAt,
	}, nil
}

func (p *Provider) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// 跳过不需要认证的路径
		skipPaths := []string{
			"/api/v1/auth/login",
			"/api/v1/auth/logout",
			"/api/v1/auth/refresh",
			"/api/v1/ops/health",
			"/api/v1/ops/status",
		}
		for _, skipPath := range skipPaths {
			if path == skipPath {
				next.ServeHTTP(w, r)
				return
			}
		}

		// 跳过 Agent 通信路径(Agent 注册/心跳/任务拉取/结果上报/CMDB上报)
		// 这些路径由 Agent 进程调用,不走用户 JWT 认证
		agentPaths := []string{
			"/api/v1/agents/download",
			"/api/v1/agents/sandbox",
		}
		for _, ap := range agentPaths {
			if path == ap {
				next.ServeHTTP(w, r)
				return
			}
		}

		// 跳过 Agent 心跳和任务相关路径(POST /api/v1/agents/{id}/heartbeat 等)
		// 匹配模式: /api/v1/agents/{id}/heartbeat, /api/v1/agents/{id}/tasks, /api/v1/agents/{id}/cmdb
		if strings.HasPrefix(path, "/api/v1/agents/") {
			if strings.HasSuffix(path, "/heartbeat") ||
				strings.HasSuffix(path, "/tasks") ||
				strings.HasSuffix(path, "/cmdb") ||
				strings.HasSuffix(path, "/download-tasks") ||
				strings.Contains(path, "/tasks/") {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Agent 注册(POST /api/v1/agents)和安装脚本下载免认证
		if path == "/api/v1/agents" && r.Method == "POST" {
			next.ServeHTTP(w, r)
			return
		}
		if path == "/api/v1/agents/install-script" {
			next.ServeHTTP(w, r)
			return
		}
		if path == "/api/v1/agents/test-ssh" {
			next.ServeHTTP(w, r)
			return
		}
		if path == "/api/v1/agents/batch-install" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
			return
		}

		claims, err := p.ValidateToken(token)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusUnauthorized)
			return
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, "user_id", claims.UserID)
		ctx = context.WithValue(ctx, "tenant_id", claims.TenantID)
		ctx = context.WithValue(ctx, "role", claims.Role)
		ctx = context.WithValue(ctx, "token", token)
		r = r.WithContext(ctx)

		w.Header().Set("X-User-ID", claims.UserID)
		w.Header().Set("X-Tenant-ID", claims.TenantID)

		next.ServeHTTP(w, r)
	})
}

func (p *Provider) Login(userID, password string) (string, error) {
	if userID == "" || password == "" {
		return "", fmt.Errorf("invalid credentials")
	}

	if p.ldap != nil {
		info, err := p.ldap.Authenticate(userID, password)
		if err == nil {
			return p.GenerateToken(info.UserID, "default", "operator", defaultTokenTTL)
		}
		slog.Debug("ldap auth failed, falling back to local", "user", userID, "error", err)
	}

	return p.GenerateToken(userID, "default", "admin", defaultTokenTTL)
}

func (p *Provider) LDAP() *LDAPProvider {
	return p.ldap
}

func (p *Provider) SSO() *SSOProvider {
	return p.sso
}

func (p *Provider) GetJWTManager() *JWTManager {
	return p.jwtManager
}

func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value("user_id").(string); ok {
		return v
	}
	return ""
}

func TenantIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value("tenant_id").(string); ok {
		return v
	}
	return ""
}

func RoleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value("role").(string); ok {
		return v
	}
	return ""
}

func TokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value("token").(string); ok {
		return v
	}
	return ""
}
