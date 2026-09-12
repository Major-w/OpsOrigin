package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/pkg/principal"
)

// ——— 多租户开关 ———
// OPSORIGIN_MULTI_TENANT=false(默认)时全局单租户, 所有请求强制租户 "default";
// 开启后按 JWT/请求头区分租户。由 cmd/core 在启动时注入。
var (
	mtMu        sync.RWMutex
	multiTenant bool
)

// SetMultiTenant 设置多租户模式(启动时/运行时热切换)。
func SetMultiTenant(v bool) {
	mtMu.Lock()
	multiTenant = v
	mtMu.Unlock()
}

// MultiTenantEnabled 返回当前是否多租户模式。
func MultiTenantEnabled() bool {
	mtMu.RLock()
	defer mtMu.RUnlock()
	return multiTenant
}

const defaultTokenTTL = 24 * time.Hour

// 断言头:仅在请求通过内部服务凭证校验后才被采信,否则一律剥离。
const (
	headerInternalToken  = "X-Internal-Token"
	headerPrincipalType  = "X-Principal-Type"
	headerPrincipalID    = "X-Principal-ID"
	headerPrincipalName  = "X-Principal-Name"
	headerPrincipalTkt   = "X-Principal-Ticket"
	headerClientPermsBad = "X-Permissions" // 客户端自报权限,永不采信
)

// PrincipalEnricher 由上层(IAM 角色库 / 用户目录)注入,负责把服务端解析出的
// 权限集合与展示名回填到主体。未注入时回落到内置 RolePermissions,保证可用。
type PrincipalEnricher interface {
	// Permissions 返回 (租户, 用户, 主角色) 聚合后的最终权限集合。
	Permissions(ctx context.Context, tenantID, userID, role string) []string
	// DisplayName 返回用户展示名;查不到时返回空串。
	DisplayName(ctx context.Context, tenantID, userID string) string
}

type Provider struct {
	jwtManager *JWTManager
	ldap       *LDAPProvider
	sso        *SSOProvider
	devMode    bool
	// serviceToken 内部服务共享密钥。空串 = 断言头通道关闭(fail-closed)。
	serviceToken string
	enricher     PrincipalEnricher
	// apiKeyResolver API Key 校验器。nil = X-API-Key 通道关闭(fail-closed)。
	apiKeyResolver APIKeyResolver
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
		devMode:    secret == "opsorigin-dev-secret-do-not-use-in-production",
	}
}

// SetServiceToken 注入内部服务共享密钥。仅携带匹配 X-Internal-Token 的请求
// 才被允许通过 X-Principal-* 断言主体身份(服务间调用 / AI Agent 旁路)。
func (p *Provider) SetServiceToken(token string) { p.serviceToken = token }

// SetPrincipalEnricher 注入权限/展示名解析器(通常来自 IAM 服务与用户目录)。
func (p *Provider) SetPrincipalEnricher(e PrincipalEnricher) { p.enricher = e }

// isTrustedService 恒定时间比对内部服务凭证。密钥未配置时一律返回 false。
func (p *Provider) isTrustedService(r *http.Request) bool {
	if p.serviceToken == "" {
		return false
	}
	got := r.Header.Get(headerInternalToken)
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(p.serviceToken)) == 1
}

// stripAssertionHeaders 剥离不可信请求自带的身份/权限断言头。
// 防御纵深:即便下游模块仍在读这些头,也读不到攻击者注入的值。
func stripAssertionHeaders(r *http.Request) {
	r.Header.Del(headerPrincipalType)
	r.Header.Del(headerPrincipalID)
	r.Header.Del(headerPrincipalName)
	r.Header.Del(headerPrincipalTkt)
	r.Header.Del(headerClientPermsBad)
}

// enrich 回填服务端解析的权限与展示名。enricher 未注入时回落内置角色映射。
func (p *Provider) enrich(ctx context.Context, pr principal.Principal) principal.Principal {
	if p.enricher != nil {
		if perms := p.enricher.Permissions(ctx, pr.TenantID, pr.ID, pr.Role); len(perms) > 0 {
			pr.Permissions = perms
		}
		if pr.DisplayName == "" {
			if name := p.enricher.DisplayName(ctx, pr.TenantID, pr.ID); name != "" {
				pr.DisplayName = name
			}
		}
	}
	if len(pr.Permissions) == 0 && pr.Role != "" {
		pr.Permissions = RolePermissions[pr.Role]
	}
	return pr
}

// 历史裸字符串 context key。新代码一律走 pkg/principal 的类型化 key;
// 这里仅为兼容尚未迁移的调用方(含在业务代码里手工 WithValue("tenant_id") 的地方)保留双写。
const (
	legacyKeyUserID   = "user_id"
	legacyKeyTenantID = "tenant_id"
	legacyKeyRole     = "role"
	legacyKeyToken    = "token"
)

// injectPrincipal 把主体写入 context(类型化 key 为准),同时双写历史裸字符串 key。
//
//nolint:staticcheck // SA1029: 裸 key 为向后兼容保留,新代码请用 principal.FromContext
func injectPrincipal(ctx context.Context, pr principal.Principal, token string) context.Context {
	ctx = principal.NewContext(ctx, pr)
	ctx = context.WithValue(ctx, legacyKeyUserID, pr.ID)
	ctx = context.WithValue(ctx, legacyKeyTenantID, pr.TenantID)
	ctx = context.WithValue(ctx, legacyKeyRole, pr.Role)
	if token != "" {
		ctx = context.WithValue(ctx, legacyKeyToken, token)
	}
	return ctx
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

		// 内部服务通道:携带有效 X-Internal-Token 的请求可用 X-Principal-* 断言主体
		// (服务间调用 / AI Agent 代表用户执行)。这是断言头唯一被采信的入口。
		if p.isTrustedService(r) {
			pr, ok := assertedPrincipal(r)
			if !ok {
				http.Error(w, `{"error":"invalid principal assertion"}`, http.StatusUnauthorized)
				return
			}
			pr = p.enrich(r.Context(), pr)
			next.ServeHTTP(w, r.WithContext(injectPrincipal(r.Context(), pr, "")))
			return
		}

		// 到这里的请求一律视为不可信来源:剥离其自带的身份/权限断言头,
		// 杜绝「登录用户伪造 X-Principal-ID 冒充他人 / 自报 X-Permissions 提权」。
		stripAssertionHeaders(r)

		// API Key 通道:X-API-Key / Authorization: ApiKey <key>。
		// 主体由服务端按 Key 绑定关系解析(非请求自述),再经 enrich 按用户当前角色
		// 走 IAM 聚合富化 —— 与 JWT 同一条路径,故 Key 自动继承用户权限。
		// 未注入校验器(通道未开启)时不拦截,回落常规鉴权路径。
		if p.apiKeyResolver != nil {
			if rawKey := extractAPIKey(r); rawKey != "" {
				nctx, ok := p.authenticateAPIKey(r.Context(), rawKey)
				if !ok {
					http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r.WithContext(nctx))
				return
			}
		}

		// DEV 模式：JWT secret 是默认值时自动注入 admin 主体，免登录。
		// 注意:此处只写 context,绝不回写请求头 —— 断言头必须只有可信通道能产生。
		if p.devMode {
			pr := p.enrich(r.Context(), principal.Principal{
				Type:        principal.TypeHuman,
				ID:          "dev",
				DisplayName: "开发者",
				TenantID:    "default",
				Role:        RoleAdmin,
			})
			next.ServeHTTP(w, r.WithContext(injectPrincipal(r.Context(), pr, "")))
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

		// 主体一律从 JWT 派生 —— 浏览器请求无权自述身份。
		pr := p.enrich(r.Context(), principal.Principal{
			Type:     principal.TypeHuman,
			ID:       claims.UserID,
			TenantID: claims.TenantID,
			Role:     claims.Role,
		})
		r = r.WithContext(injectPrincipal(r.Context(), pr, token))

		w.Header().Set("X-User-ID", claims.UserID)
		w.Header().Set("X-Tenant-ID", claims.TenantID)

		next.ServeHTTP(w, r)
	})
}

// assertedPrincipal 从已通过内部凭证校验的请求中解析断言主体。
func assertedPrincipal(r *http.Request) (principal.Principal, bool) {
	id := strings.TrimSpace(r.Header.Get(headerPrincipalID))
	if id == "" {
		return principal.Principal{}, false
	}
	ptype, ok := principal.ParseType(r.Header.Get(headerPrincipalType))
	if !ok {
		return principal.Principal{}, false
	}
	// 租户仍不接受请求头指定,由服务凭证所属上下文决定;此处沿用默认租户,
	// 后续如需多租户服务调用,应改为在服务凭证中绑定租户而非请求头声明。
	return principal.Principal{
		Type:        ptype,
		ID:          id,
		DisplayName: strings.TrimSpace(r.Header.Get(headerPrincipalName)),
		TenantID:    "default",
		Role:        RoleOperator,
		TicketID:    strings.TrimSpace(r.Header.Get(headerPrincipalTkt)),
	}, true
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

// 以下 *FromContext 为兼容入口:优先读类型化主体,回落历史裸字符串 key
// (仍有业务代码手工 WithValue("tenant_id", ...) 构造上下文)。
// 新代码请直接用 principal.UserID / principal.TenantID / principal.Role。

func UserIDFromContext(ctx context.Context) string {
	if pr, ok := principal.FromContext(ctx); ok && pr.ID != "" {
		return pr.ID
	}
	if v, ok := ctx.Value(legacyKeyUserID).(string); ok {
		return v
	}
	return ""
}

func TenantIDFromContext(ctx context.Context) string {
	// 单租户模式(默认): 一律强制 "default", 无视上下文/主体租户。
	if !MultiTenantEnabled() {
		return "default"
	}
	// 裸 key 优先:业务代码常用 WithValue("tenant_id") 做临时租户切换(如机器人入站建单),
	// 这类派生上下文必须能覆盖主体自带的租户。
	if v, ok := ctx.Value(legacyKeyTenantID).(string); ok && v != "" {
		return v
	}
	if pr, ok := principal.FromContext(ctx); ok {
		return pr.TenantID
	}
	return ""
}

// TenantFromContext 返回请求租户，带 "default" 兜底：
// 优先裸字符串 key(兼容业务临时租户切换)，回落类型化主体租户，最后兜底 "default"。
// 这是各业务模块统一的租户取数入口（曾散落 11 份 tenantFromCtx 副本，均指向此函数）。
func TenantFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(legacyKeyTenantID).(string); ok && v != "" {
		return v
	}
	if pr, ok := principal.FromContext(ctx); ok && pr.TenantID != "" {
		return pr.TenantID
	}
	return "default"
}

func RoleFromContext(ctx context.Context) string {
	if pr, ok := principal.FromContext(ctx); ok && pr.Role != "" {
		return pr.Role
	}
	if v, ok := ctx.Value(legacyKeyRole).(string); ok {
		return v
	}
	return ""
}

func TokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(legacyKeyToken).(string); ok {
		return v
	}
	return ""
}
