package apikey

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// Service 提供 API Key 的生成/列表/吊销，并实现 pkg/auth 的 APIKeyResolver，
// 让 X-API-Key 通道成为与 JWT 平行的第二套鉴权入口。
//
// 「继承用户权限」的实现路径：
//
//	X-API-Key → ResolveAPIKey 解析出 (KeyID, UserID, TenantID, Role)
//	         → pkg/auth 的 enrich() 走与 JWT 完全相同的 IAM 聚合富化
//	         → 主体权限 = 绑定用户当前的“角色 + 用户组”聚合权限
//
// 因此用户角色变更后，其名下 Key 的权限自动跟随，无需重建 Key。
type Service struct {
	store    *Store
	identity userRefResolver
	usageCh  chan string
}

func NewService(store *Store) *Service {
	return &Service{
		store:   store,
		usageCh: make(chan string, 1024),
	}
}

// SetIdentityResolver 注入「按用户 ID 取当前角色/状态」的解析器（cmd/core 注入 user.Service）。
// 未注入时 API Key 校验一律失败（fail-closed）。
func (s *Service) SetIdentityResolver(fn userRefResolver) { s.identity = fn }

// EnsureSchema 建表（幂等）。Service 未接 PG 时返回错误，由调用方决定是否忽略。
func (s *Service) EnsureSchema(ctx context.Context) error { return s.store.EnsureSchema(ctx) }

// StartUsageWorker 启动用量累加后台工作者（异步，不阻塞鉴权路径）。
func (s *Service) StartUsageWorker(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-s.usageCh:
				// 合并同一批次的重复 Key，减少重复 UPDATE。
				uniq := map[string]struct{}{id: {}}
			drain:
				for {
					select {
					case next := <-s.usageCh:
						uniq[next] = struct{}{}
					default:
						break drain
					}
				}
				ids := make([]string, 0, len(uniq))
				for k := range uniq {
					ids = append(ids, k)
				}
				uctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if err := s.store.TouchUsage(uctx, ids); err != nil {
					slog.Warn("apikey usage touch failed", "error", err)
				}
				cancel()
			}
		}
	}()
}

// ResolveAPIKey 实现 auth.APIKeyResolver：校验明文并返回绑定主体。
// 任何一步不满足（未启用/未命中/已吊销/已过期/用户停用）都返回 false。
func (s *Service) ResolveAPIKey(ctx context.Context, raw string) (auth.APIKeyIdentity, bool) {
	if !s.store.Available() || !strings.HasPrefix(raw, KeyPrefix) {
		return auth.APIKeyIdentity{}, false
	}
	k, err := s.store.GetByHash(ctx, raw)
	if err != nil {
		return auth.APIKeyIdentity{}, false
	}
	if k.RevokedAt != nil {
		return auth.APIKeyIdentity{}, false
	}
	if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
		return auth.APIKeyIdentity{}, false
	}
	if s.identity == nil {
		slog.Warn("apikey: identity resolver not wired, rejecting api key auth")
		return auth.APIKeyIdentity{}, false
	}
	role, status, ok := s.identity(ctx, k.TenantID, k.UserID)
	if !ok || strings.EqualFold(status, "disabled") {
		return auth.APIKeyIdentity{}, false
	}
	if role == "" {
		role = auth.RoleViewer // 角色缺失时按最小权限，避免越权
	}

	// 异步累加用量（非阻塞；通道满则丢弃，仅影响统计不影响鉴权）。
	select {
	case s.usageCh <- k.ID:
	default:
	}

	tenantID := k.TenantID
	if tenantID == "" {
		tenantID = "default"
	}
	return auth.APIKeyIdentity{
		KeyID:    k.ID,
		UserID:   k.UserID,
		TenantID: tenantID,
		Role:     role,
	}, true
}

// RegisterRoutes 注册 API Key 自助管理路由。
// 约定：任何已登录用户可管理「自己的」Key；管理员（admin）可查看/管理全租户的 Key。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/api-keys", s.handleList)
	mux.HandleFunc("POST /api/v1/api-keys", s.handleCreate)
	mux.HandleFunc("POST /api/v1/api-keys/{id}/rotate", s.handleRotate)
	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", s.handleRevoke)
}

func (s *Service) available(w http.ResponseWriter) bool {
	if s.store.Available() {
		return true
	}
	httputil.WriteError(w, http.StatusNotImplemented, "api key management requires postgres backend")
	return false
}

// handleList GET /api/v1/api-keys[?all=true]
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	if !s.available(w) {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	isAdmin := auth.RoleFromContext(r.Context()) == auth.RoleAdmin

	// 管理员加 ?all=true 可看全租户（含用户名），用于密钥治理。
	if isAdmin && r.URL.Query().Get("all") == "true" {
		keys, err := s.store.ListByTenant(r.Context(), tenantID, 500)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"keys": keys, "scope": "tenant"})
		return
	}

	keys, err := s.store.ListByUser(r.Context(), tenantID, userID, 200)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"keys": keys, "scope": "self"})
}

// handleCreate POST /api/v1/api-keys
// 响应中一次性返回明文（raw_key），此后任何接口都不再提供 —— 落库只有摘要。
func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !s.available(w) {
		return
	}
	var req createRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}

	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthenticated principal")
		return
	}
	// 代他人创建仅限管理员，且目标用户必须存在且启用。
	if req.UserID != "" && req.UserID != userID {
		if auth.RoleFromContext(r.Context()) != auth.RoleAdmin {
			httputil.WriteError(w, http.StatusForbidden, "only admin can create keys for other users")
			return
		}
		if _, status, ok := s.identityUser(r.Context(), tenantID, req.UserID); !ok || strings.EqualFold(status, "disabled") {
			httputil.WriteError(w, http.StatusBadRequest, "target user not found or disabled")
			return
		}
		userID = req.UserID
	}

	raw, err := newRawKey()
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	k := &Key{
		ID:       uuid.New().String(),
		TenantID: tenantID,
		UserID:   userID,
		Name:     req.Name,
	}
	if req.ExpiresInDays > 0 {
		exp := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		k.ExpiresAt = &exp
	}
	if err := s.store.Create(r.Context(), k, raw); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("api key created", "key_id", k.ID, "user_id", userID, "prefix", k.KeyPrefix)

	// raw_key 仅此一次返回。
	httputil.WriteJSON(w, http.StatusCreated, map[string]any{
		"key":     k,
		"raw_key": raw,
		"notice":  "请立即保存该密钥，关闭后无法再次查看（服务端仅存摘要）",
	})
}

// handleRotate POST /api/v1/api-keys/{id}/rotate —— 生成同属主新密钥并吊销旧密钥。
func (s *Service) handleRotate(w http.ResponseWriter, r *http.Request) {
	if !s.available(w) {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	isAdmin := auth.RoleFromContext(r.Context()) == auth.RoleAdmin
	id := httputil.Param(r, "id")
	if id == "" {
		httputil.WriteError(w, http.StatusBadRequest, "missing key id")
		return
	}
	// 归属校验：普通用户只能操作自己的 Key；管理员不限。
	scopeUser := userID
	if isAdmin {
		scopeUser = ""
	}
	old, err := s.store.GetByID(r.Context(), tenantID, id, scopeUser)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "api key not found")
		return
	}
	ok, err := s.store.Revoke(r.Context(), tenantID, id, scopeUser)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "api key not found or already revoked")
		return
	}

	// 新 Key 沿用旧 Key 的属主与有效期窗口；名称可由入参覆盖。
	var req createRequest
	_ = httputil.BindJSON(r, &req)
	req.Name = strings.TrimSpace(req.Name)
	name := req.Name
	if name == "" {
		name = old.Name
	}
	raw, err := newRawKey()
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	k := &Key{
		ID:       uuid.New().String(),
		TenantID: tenantID,
		UserID:   old.UserID,
		Name:     name,
	}
	if old.ExpiresAt != nil {
		exp := time.Now().Add(time.Until(*old.ExpiresAt))
		k.ExpiresAt = &exp
	}
	if err := s.store.Create(r.Context(), k, raw); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]any{
		"key":     k,
		"raw_key": raw,
		"revoked": id,
		"notice":  "旧密钥已吊销，请保存新密钥（仅此一次可见）",
	})
}

// handleRevoke DELETE /api/v1/api-keys/{id}
func (s *Service) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.available(w) {
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	isAdmin := auth.RoleFromContext(r.Context()) == auth.RoleAdmin
	id := httputil.Param(r, "id")
	if id == "" {
		httputil.WriteError(w, http.StatusBadRequest, "missing key id")
		return
	}
	scopeUser := userID
	if isAdmin {
		scopeUser = ""
	}
	ok, err := s.store.Revoke(r.Context(), tenantID, id, scopeUser)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "api key not found or already revoked")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"revoked": id})
}

// identityUser 安全调用 identity 解析器（nil 安全）。
func (s *Service) identityUser(ctx context.Context, tenantID, userID string) (string, string, bool) {
	if s.identity == nil {
		return "", "", false
	}
	return s.identity(ctx, tenantID, userID)
}
