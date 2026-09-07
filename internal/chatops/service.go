package chatops

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// Service ChatOps 多渠道接入服务(P3-2a)。
//
// 路由分两类:
//   - /api/v1/chatops/callback/{channelID}: IM 平台回调(飞书事件订阅), 走平台验签, 在 bypass 前缀下免 JWT
//   - 其余管理 API: 走正常 auth + system:admin 权限
type Service struct {
	st     *store
	engine *agentic.Engine
	bridge *bridge
	// callbackPrefix 回调路径前缀(与 cmd/core bypass 前缀保持一致)。
	callbackPrefix string
}

// NewService 构造 ChatOps 服务。db 为 nil 时(如单测/降级环境)返回的 Service 仅管理 API 可用并报错降级。
func NewService(db *sql.DB, engine *agentic.Engine, botAgentID string) *Service {
	s := &Service{
		callbackPrefix: "/api/v1/chatops/callback/",
	}
	if db != nil {
		s.st = newStore(db)
	}
	if engine != nil {
		s.engine = engine
		s.bridge = &bridge{st: s.st, engine: engine, botAgentID: botAgentID}
	}
	return s
}

// Name 服务名(诊断日志用)。
func (s *Service) Name() string { return "chatops" }

// Init 建表(启动时由 main 调用, 失败仅告警不阻断进程)。
func (s *Service) Init(ctx context.Context) error {
	if s.st == nil {
		return fmt.Errorf("chatops: db not available")
	}
	return s.st.init(ctx)
}

// RegisterRoutes 注册管理 API 与平台回调。
// 管理 API 的 RBAC 由网关层 AutoPermissionMiddleware 按 pkg/auth.PathPermissionMapping
// 的 chatops 映射(→ system:admin)统一强制, handler 内不重复检查。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	// 平台回调: 免 JWT(WithBypassPrefixes 加同前缀), 由平台验签保证来源可信。
	// channelID 是随机串(不可枚举), 相当于 URL 携带的凭据之一, 再叠加 verification_token 校验。
	mux.HandleFunc("POST /api/v1/chatops/callback/{channelID}", s.handlePlatformCallback)

	// 渠道管理
	mux.HandleFunc("GET /api/v1/chatops/channels", s.handleListChannels)
	mux.HandleFunc("POST /api/v1/chatops/channels", s.handleCreateChannel)
	mux.HandleFunc("PUT /api/v1/chatops/channels/{id}", s.handleUpdateChannel)
	mux.HandleFunc("DELETE /api/v1/chatops/channels/{id}", s.handleDeleteChannel)
	mux.HandleFunc("POST /api/v1/chatops/channels/{id}/test", s.handleTestChannel)

	// 会话绑定查看/解绑
	mux.HandleFunc("GET /api/v1/chatops/bindings", s.handleListBindings)
	mux.HandleFunc("DELETE /api/v1/chatops/bindings/{platform}/{chatID}", s.handleDeleteBinding)

	// 平台元信息(前端设置页: 支持的平台与所需凭据字段)
	mux.HandleFunc("GET /api/v1/chatops/platforms", s.handleListPlatforms)
}

// ---- 平台回调 ----

func (s *Service) handlePlatformCallback(w http.ResponseWriter, r *http.Request) {
	if s.st == nil || s.bridge == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "chatops not ready")
		return
	}
	chID := httputil.Param(r, "channelID")
	ch, err := s.st.getChannel(r.Context(), chID)
	if err != nil {
		// 不区分不存在/DB 错误的细节, 防探测
		httputil.WriteError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !ch.Enabled {
		httputil.WriteError(w, http.StatusNotFound, "channel not found")
		return
	}
	p := platformOf(ch.Platform)
	if p == nil {
		httputil.WriteError(w, http.StatusInternalServerError, "platform adapter missing")
		return
	}
	if err := p.HandleWebhook(w, r, ch, s.bridge); err != nil {
		slog.Warn("chatops: platform webhook failed", "platform", ch.Platform, "channel", chID, "error", err)
	}
}

// ---- 渠道管理 ----

type channelResp struct {
	Channel
	CallbackURL  string          `json:"callback_url"` // 填到平台事件订阅中心的 URL
	SecretMasked json.RawMessage `json:"settings"`     // secret 字段脱敏后的配置
}

func (s *Service) maskSettings(ch *Channel) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(ch.Settings, &m); err != nil || m == nil {
		return ch.Settings
	}
	for k := range m {
		switch strings.ToLower(k) {
		case "app_secret", "verification_token", "encrypt_key":
			if v, ok := m[k].(string); ok && v != "" {
				m[k] = maskSecret(v)
			}
		}
	}
	out, _ := json.Marshal(m)
	return out
}

func maskSecret(v string) string {
	if len(v) <= 6 {
		return "******"
	}
	return v[:3] + "******" + v[len(v)-3:]
}

func (s *Service) toResp(r *http.Request, ch *Channel) *channelResp {
	scheme := "http"
	if r != nil && r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	}
	host := r.Host
	return &channelResp{
		Channel:      *ch,
		CallbackURL:  fmt.Sprintf("%s://%s%s%s", scheme, host, s.callbackPrefix, ch.ID),
		SecretMasked: s.maskSettings(ch),
	}
}

func (s *Service) handleListChannels(w http.ResponseWriter, r *http.Request) {
	if s.st == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "chatops db not available")
		return
	}
	channels, err := s.st.listChannels(r.Context(), tenantOfReq(r))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "查询渠道失败")
		return
	}
	out := make([]*channelResp, 0, len(channels))
	for _, ch := range channels {
		out = append(out, s.toResp(r, ch))
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.EmptySliceIfNil(out))
}

func (s *Service) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if s.st == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "chatops db not available")
		return
	}
	var req struct {
		Platform string          `json:"platform"`
		Name     string          `json:"name"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "参数错误")
		return
	}
	if platformOf(req.Platform) == nil {
		httputil.WriteError(w, http.StatusBadRequest, "不支持的平台: "+req.Platform)
		return
	}
	if len(req.Settings) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "缺少平台凭据配置")
		return
	}
	// 回调 URL 的不可枚举性是安全边界之一: 128bit 随机
	id, err := randomToken()
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "生成渠道 ID 失败")
		return
	}
	ch := &Channel{ID: id, TenantID: tenantOfReq(r), Platform: req.Platform, Name: req.Name, Enabled: true, Settings: req.Settings}
	if err := s.st.createChannel(r.Context(), ch); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "创建渠道失败")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, s.toResp(r, ch))
}

func (s *Service) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	if s.st == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "chatops db not available")
		return
	}
	id := httputil.Param(r, "id")
	ch, err := s.st.getChannel(r.Context(), id)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	if ch.TenantID != tenantOfReq(r) {
		httputil.WriteError(w, http.StatusForbidden, "无权操作该渠道")
		return
	}
	var req struct {
		Name     *string          `json:"name"`
		Enabled  *bool            `json:"enabled"`
		Settings *json.RawMessage `json:"settings"` // 未传=不修改凭据(前端脱敏回显不应整包写回)
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Name != nil {
		ch.Name = *req.Name
	}
	if req.Enabled != nil {
		ch.Enabled = *req.Enabled
	}
	if req.Settings != nil && len(*req.Settings) > 0 {
		ch.Settings = *req.Settings
	}
	if err := s.st.updateChannel(r.Context(), ch); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "更新渠道失败")
		return
	}
	updated, _ := s.st.getChannel(r.Context(), id)
	httputil.WriteJSON(w, http.StatusOK, s.toResp(r, updated))
}

func (s *Service) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if s.st == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "chatops db not available")
		return
	}
	id := httputil.Param(r, "id")
	ch, err := s.st.getChannel(r.Context(), id)
	if err != nil || ch.TenantID != tenantOfReq(r) {
		httputil.WriteError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	if err := s.st.deleteChannel(r.Context(), id, ch.TenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "删除渠道失败")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Service) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	if s.st == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "chatops db not available")
		return
	}
	var req struct {
		ChatID string `json:"chat_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil || strings.TrimSpace(req.ChatID) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "需要 chat_id(在 IM 群里 @机器人 发任意消息后从绑定列表获取)")
		return
	}
	id := httputil.Param(r, "id")
	ch, err := s.st.getChannel(r.Context(), id)
	if err != nil || ch.TenantID != tenantOfReq(r) {
		httputil.WriteError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	p := platformOf(ch.Platform)
	if p == nil {
		httputil.WriteError(w, http.StatusInternalServerError, "platform adapter missing")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := p.SendText(ctx, ch, req.ChatID, "✅ OpsOrigin ChatOps 渠道连通性测试成功"); err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "测试消息发送失败: "+truncForIM(err.Error(), 200))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- 绑定管理 ----

func (s *Service) handleListBindings(w http.ResponseWriter, r *http.Request) {
	if s.st == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "chatops db not available")
		return
	}
	bindings, err := s.st.listBindings(r.Context(), tenantOfReq(r))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "查询绑定失败")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, httputil.EmptySliceIfNil(bindings))
}

func (s *Service) handleDeleteBinding(w http.ResponseWriter, r *http.Request) {
	if s.st == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "chatops db not available")
		return
	}
	if err := s.st.deleteBinding(r.Context(), httputil.Param(r, "platform"), httputil.Param(r, "chatID"), tenantOfReq(r)); err != nil {
		httputil.WriteError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Service) handleListPlatforms(w http.ResponseWriter, _ *http.Request) {
	type platformInfo struct {
		Name   string   `json:"name"`
		Fields []string `json:"fields"` // settings 所需字段
	}
	httputil.WriteJSON(w, http.StatusOK, []platformInfo{
		{Name: PlatformFeishu, Fields: []string{"app_id", "app_secret", "verification_token", "encrypt_key", "default_agent_id"}},
		{Name: PlatformSlack, Fields: []string{"(P3-2b 接入)"}},
		{Name: PlatformWecom, Fields: []string{"(P3-2b 接入)"}},
	})
}

// ---- helpers ----

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// tenantOfReq 取当前租户(管理 API 走正常鉴权链, 由 auth 中间件注入)。
func tenantOfReq(r *http.Request) string {
	if t := auth.TenantIDFromContext(r.Context()); t != "" {
		return t
	}
	return "default"
}
