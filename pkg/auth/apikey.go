package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/opsorigin/core/pkg/principal"
)

// APIKeyIdentity 是 API Key 校验成功后解析出的绑定主体信息。
//
// 注意此处不含权限集合：权限由 Middleware 经 enrich（IAM 聚合）按「用户当前角色」
// 派生，从而保证「Key 继承用户权限」——用户角色/用户组变更后 Key 权限自动跟随，
// 不需要在 Key 上冗余权限点，也不需要重建 Key。
type APIKeyIdentity struct {
	KeyID    string
	UserID   string
	TenantID string
	Role     string
}

// APIKeyResolver 由上层（internal/apikey）注入，负责校验 API Key 明文并返回绑定主体。
// pkg/auth 不能反向依赖 internal/*，故以接口解耦；未注入时 X-API-Key 通道关闭（fail-closed）。
type APIKeyResolver interface {
	ResolveAPIKey(ctx context.Context, rawKey string) (APIKeyIdentity, bool)
}

// SetAPIKeyResolver 注入 API Key 校验器。
func (p *Provider) SetAPIKeyResolver(r APIKeyResolver) { p.apiKeyResolver = r }

const (
	// headerAPIKey 是 API Key 的首选传递头。
	headerAPIKey = "X-API-Key"
	// apiKeyAuthScheme 支持 Authorization: ApiKey <key>（兼容不便设置自定义头的客户端）。
	apiKeyAuthScheme = "ApiKey "

	// legacyKeyAPIKeyID 把命中的 Key ID 写进 context，供审计与用量溯源。
	legacyKeyAPIKeyID = "api_key_id"
)

// APIKeyIDFromContext 返回本次请求命中的 API Key ID（JWT 请求为空串）。
func APIKeyIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(legacyKeyAPIKeyID).(string); ok {
		return v
	}
	return ""
}

// extractAPIKey 提取请求携带的 API Key 明文：
// 优先 X-API-Key 头，其次 Authorization: ApiKey <key>。
// 与 JWT 的 Authorization: Bearer <token> 互不干扰（scheme 不同）。
func extractAPIKey(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(headerAPIKey)); v != "" {
		return v
	}
	ah := r.Header.Get("Authorization")
	if len(ah) > len(apiKeyAuthScheme) && strings.EqualFold(ah[:len(apiKeyAuthScheme)], apiKeyAuthScheme) {
		return strings.TrimSpace(ah[len(apiKeyAuthScheme):])
	}
	return ""
}

// authenticateAPIKey 校验 API Key 并派生主体。返回 ok=false 表示鉴权失败。
// 权限富化走与 JWT 完全相同的 enrich 路径，因此 Key 天然继承用户权限。
func (p *Provider) authenticateAPIKey(ctx context.Context, raw string) (context.Context, bool) {
	if p.apiKeyResolver == nil {
		return ctx, false
	}
	id, ok := p.apiKeyResolver.ResolveAPIKey(ctx, raw)
	if !ok || id.UserID == "" {
		return ctx, false
	}
	tenantID := id.TenantID
	if !MultiTenantEnabled() || tenantID == "" {
		tenantID = "default"
	}
	pr := p.enrich(ctx, principal.Principal{
		Type:     principal.TypeHuman,
		ID:       id.UserID,
		TenantID: tenantID,
		Role:     id.Role,
	})
	nctx := injectPrincipal(ctx, pr, "")
	if id.KeyID != "" {
		//nolint:staticcheck // SA1029: 裸 key 与 injectPrincipal 的历史 key 同风格，仅为内部溯源
		nctx = context.WithValue(nctx, legacyKeyAPIKeyID, id.KeyID)
	}
	return nctx, true
}
