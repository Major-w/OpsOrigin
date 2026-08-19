package notification

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/opsorigin/core/pkg/auth"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

// Store 定义通知模块的持久化接口。
type Store interface {
	// 渠道
	CreateChannel(ctx context.Context, c *Channel) error
	GetChannel(ctx context.Context, id, tenantID string) (*Channel, error)
	ListChannels(ctx context.Context, tenantID string) ([]*Channel, error)
	UpdateChannel(ctx context.Context, c *Channel) error
	DeleteChannel(ctx context.Context, id, tenantID string) error
	// 策略
	CreatePolicy(ctx context.Context, p *Policy) error
	GetPolicy(ctx context.Context, id, tenantID string) (*Policy, error)
	ListPolicies(ctx context.Context, tenantID string) ([]*Policy, error)
	UpdatePolicy(ctx context.Context, p *Policy) error
	DeletePolicy(ctx context.Context, id, tenantID string) error
	// 历史
	SaveNotification(ctx context.Context, n *Notification) error
	ListNotifications(ctx context.Context, tenantID string, limit int) ([]*Notification, error)
}

// MemoryStore 是并发安全的内存实现。
type MemoryStore struct {
	mu            sync.RWMutex
	channels      map[string]*Channel
	policies      map[string]*Policy
	notifications []*Notification
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		channels: make(map[string]*Channel),
		policies: make(map[string]*Policy),
	}
}

func (s *MemoryStore) CreateChannel(ctx context.Context, c *Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.ID = uuid.New().String()
	now := time.Now()
	c.CreatedAt = now
	c.UpdatedAt = now
	s.channels[c.ID] = c
	return nil
}

func (s *MemoryStore) GetChannel(ctx context.Context, id, tenantID string) (*Channel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.channels[id]
	if !ok || c.TenantID != tenantID {
		return nil, nil
	}
	return c, nil
}

func (s *MemoryStore) ListChannels(ctx context.Context, tenantID string) ([]*Channel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Channel
	for _, c := range s.channels {
		if c.TenantID == tenantID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (s *MemoryStore) UpdateChannel(ctx context.Context, c *Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.channels[c.ID]
	if !ok || existing.TenantID != c.TenantID {
		return fmt.Errorf("channel not found")
	}
	c.CreatedAt = existing.CreatedAt
	c.UpdatedAt = time.Now()
	s.channels[c.ID] = c
	return nil
}

func (s *MemoryStore) DeleteChannel(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.channels[id]
	if !ok || c.TenantID != tenantID {
		return nil
	}
	delete(s.channels, id)
	// 从所有 Policy 中移除该渠道
	for _, p := range s.policies {
		filtered := make([]string, 0, len(p.ChannelIDs))
		for _, cid := range p.ChannelIDs {
			if cid != id {
				filtered = append(filtered, cid)
			}
		}
		p.ChannelIDs = filtered
	}
	return nil
}

func (s *MemoryStore) CreatePolicy(ctx context.Context, p *Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.ID = uuid.New().String()
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	s.policies[p.ID] = p
	return nil
}

func (s *MemoryStore) GetPolicy(ctx context.Context, id, tenantID string) (*Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.policies[id]
	if !ok || p.TenantID != tenantID {
		return nil, nil
	}
	return p, nil
}

func (s *MemoryStore) ListPolicies(ctx context.Context, tenantID string) ([]*Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Policy
	for _, p := range s.policies {
		if p.TenantID == tenantID {
			result = append(result, p)
		}
	}
	return result, nil
}

func (s *MemoryStore) UpdatePolicy(ctx context.Context, p *Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.policies[p.ID]
	if !ok || existing.TenantID != p.TenantID {
		return fmt.Errorf("policy not found")
	}
	p.CreatedAt = existing.CreatedAt
	p.UpdatedAt = time.Now()
	s.policies[p.ID] = p
	return nil
}

func (s *MemoryStore) DeletePolicy(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.policies[id]
	if !ok || p.TenantID != tenantID {
		return nil
	}
	delete(s.policies, id)
	return nil
}

func (s *MemoryStore) SaveNotification(ctx context.Context, n *Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.ID == "" {
		n.ID = uuid.New().String()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	s.notifications = append(s.notifications, n)
	// 保留最近 500 条
	if len(s.notifications) > 500 {
		s.notifications = s.notifications[len(s.notifications)-500:]
	}
	return nil
}

func (s *MemoryStore) ListNotifications(ctx context.Context, tenantID string, limit int) ([]*Notification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// 倒序返回最近的
	var result []*Notification
	for i := len(s.notifications) - 1; i >= 0 && len(result) < limit; i-- {
		if s.notifications[i].TenantID == tenantID {
			result = append(result, s.notifications[i])
		}
	}
	return result, nil
}

// Service 暴露通知管理的 HTTP API。
type Service struct {
	store   Store
	senders map[ChannelType]Sender
}

// NewService 创建通知服务,注入 5 种内置 Sender 实现。
func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = newPGStore(db)
	}
	return &Service{
		store:   st,
		senders: Senders(),
	}
}

// RegisterRoutes 注册所有通知路由。
// 注意 "channels" / "policies" / "history" / "events" 都是字面量段,
// 在 Go 1.22+ ServeMux 中优先于 {id} 通配符匹配。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	// 渠道 CRUD
	mux.HandleFunc("GET /api/v1/notification/channels", s.handleListChannels)
	mux.HandleFunc("POST /api/v1/notification/channels", s.handleCreateChannel)
	mux.HandleFunc("GET /api/v1/notification/channels/{id}", s.handleGetChannel)
	mux.HandleFunc("PUT /api/v1/notification/channels/{id}", s.handleUpdateChannel)
	mux.HandleFunc("DELETE /api/v1/notification/channels/{id}", s.handleDeleteChannel)
	mux.HandleFunc("POST /api/v1/notification/channels/{id}/test", s.handleTestChannel)

	// 策略 CRUD
	mux.HandleFunc("GET /api/v1/notification/policies", s.handleListPolicies)
	mux.HandleFunc("POST /api/v1/notification/policies", s.handleCreatePolicy)
	mux.HandleFunc("GET /api/v1/notification/policies/{id}", s.handleGetPolicy)
	mux.HandleFunc("PUT /api/v1/notification/policies/{id}", s.handleUpdatePolicy)
	mux.HandleFunc("DELETE /api/v1/notification/policies/{id}", s.handleDeletePolicy)

	// 元信息(供前端表单生成)
	mux.HandleFunc("GET /api/v1/notification/events", s.handleListEvents)
	mux.HandleFunc("GET /api/v1/notification/channel-types", s.handleListChannelTypes)

	// 发送历史
	mux.HandleFunc("GET /api/v1/notification/history", s.handleListHistory)

	// 触发一次事件通知(内部调用入口,用于测试整条链路)
	mux.HandleFunc("POST /api/v1/notification/send", s.handleSend)
}

// ---------- 渠道 ----------

func (s *Service) handleListChannels(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	channels, err := s.store.ListChannels(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if channels == nil {
		channels = []*Channel{}
	}
	// 列表接口对敏感字段做掩码,不返回原始凭证
	masked := make([]*Channel, 0, len(channels))
	for _, c := range channels {
		mc := *c
		mc.Config = maskConfig(c.Type, c.Config)
		masked = append(masked, &mc)
	}
	httputil.WriteJSON(w, http.StatusOK, masked)
}

func (s *Service) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var req CreateChannelRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Type == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and type are required")
		return
	}
	sender, ok := s.senders[req.Type]
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "unsupported channel type: "+string(req.Type))
		return
	}
	if err := sender.Validate(req.Config); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ch := &Channel{
		TenantID: tenantID,
		Name:     req.Name,
		Type:     req.Type,
		Config:   req.Config,
		Enabled:  req.Enabled,
	}
	if err := s.store.CreateChannel(r.Context(), ch); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 返回时掩码
	masked := *ch
	masked.Config = maskConfig(ch.Type, ch.Config)
	httputil.WriteJSON(w, http.StatusCreated, masked)
}

func (s *Service) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	ch, err := s.store.GetChannel(r.Context(), id, tenantID)
	if err != nil || ch == nil {
		httputil.WriteError(w, http.StatusNotFound, "channel not found")
		return
	}
	// 详情接口也掩码(如需明文走 reveal 查询参数)
	masked := *ch
	masked.Config = maskConfig(ch.Type, ch.Config)
	httputil.WriteJSON(w, http.StatusOK, masked)
}

func (s *Service) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	existing, err := s.store.GetChannel(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "channel not found")
		return
	}
	var req UpdateChannelRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// 合并 config:前端可能只传改动的字段,空值保留原值
	merged := make(map[string]string)
	for k, v := range existing.Config {
		merged[k] = v
	}
	for k, v := range req.Config {
		if v != "" {
			merged[k] = v
		}
	}
	sender, ok := s.senders[existing.Type]
	if !ok {
		httputil.WriteError(w, http.StatusInternalServerError, "sender not found for type")
		return
	}
	if err := sender.Validate(merged); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated := *existing
	updated.Name = req.Name
	updated.Config = merged
	updated.Enabled = req.Enabled
	if err := s.store.UpdateChannel(r.Context(), &updated); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	masked := updated
	masked.Config = maskConfig(updated.Type, updated.Config)
	httputil.WriteJSON(w, http.StatusOK, masked)
}

func (s *Service) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.DeleteChannel(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	ch, err := s.store.GetChannel(r.Context(), id, tenantID)
	if err != nil || ch == nil {
		httputil.WriteError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !ch.Enabled {
		httputil.WriteError(w, http.StatusBadRequest, "channel is disabled")
		return
	}
	var req TestRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		req = TestRequest{}
	}
	if req.Title == "" {
		req.Title = "OpsOrigin 测试通知"
	}
	if req.Content == "" {
		req.Content = fmt.Sprintf("这是一条来自 OpsOrigin 的测试通知,发送时间 %s", time.Now().Format(time.RFC3339))
	}
	sender, ok := s.senders[ch.Type]
	if !ok {
		httputil.WriteError(w, http.StatusInternalServerError, "sender not found")
		return
	}
	err = sender.Send(ch, req.Title, req.Content)
	// 无论成功失败都记录历史
	n := &Notification{
		TenantID:  tenantID,
		ChannelID: ch.ID,
		Event:     "",
		Title:     req.Title,
		Content:   req.Content,
		Status:    "sent",
	}
	if err != nil {
		n.Status = "failed"
		n.Error = err.Error()
		s.store.SaveNotification(r.Context(), n)
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	s.store.SaveNotification(r.Context(), n)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------- 策略 ----------

func (s *Service) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	policies, err := s.store.ListPolicies(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if policies == nil {
		policies = []*Policy{}
	}
	httputil.WriteJSON(w, http.StatusOK, policies)
}

func (s *Service) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var req CreatePolicyRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Event == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and event are required")
		return
	}
	p := &Policy{
		TenantID:   tenantID,
		Name:       req.Name,
		Event:      req.Event,
		ChannelIDs: req.ChannelIDs,
		Template:   req.Template,
		QuietHours: req.QuietHours,
		Enabled:    req.Enabled,
	}
	if p.ChannelIDs == nil {
		p.ChannelIDs = []string{}
	}
	if err := s.store.CreatePolicy(r.Context(), p); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, p)
}

func (s *Service) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	p, err := s.store.GetPolicy(r.Context(), id, tenantID)
	if err != nil || p == nil {
		httputil.WriteError(w, http.StatusNotFound, "policy not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	existing, err := s.store.GetPolicy(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "policy not found")
		return
	}
	var req UpdatePolicyRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated := *existing
	updated.Name = req.Name
	updated.Event = req.Event
	updated.ChannelIDs = req.ChannelIDs
	updated.Template = req.Template
	updated.QuietHours = req.QuietHours
	updated.Enabled = req.Enabled
	if updated.ChannelIDs == nil {
		updated.ChannelIDs = []string{}
	}
	if err := s.store.UpdatePolicy(r.Context(), &updated); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, updated)
}

func (s *Service) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.DeletePolicy(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------- 元信息 ----------

func (s *Service) handleListEvents(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, AllEventTypes())
}

func (s *Service) handleListChannelTypes(w http.ResponseWriter, r *http.Request) {
	types := AllChannelTypes()
	result := make([]map[string]any, 0, len(types))
	for _, t := range types {
		sender, ok := s.senders[t]
		if !ok {
			continue
		}
		result = append(result, map[string]any{
			"type":            t,
			"required_fields": sender.RequiredFields(),
		})
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

// ---------- 历史 ----------

func (s *Service) handleListHistory(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		var n int
		if _, err := fmt.Sscanf(l, "%d", &n); err == nil && n > 0 {
			limit = n
		}
	}
	notifications, err := s.store.ListNotifications(r.Context(), tenantID, limit)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if notifications == nil {
		notifications = []*Notification{}
	}
	httputil.WriteJSON(w, http.StatusOK, notifications)
}

// ---------- 事件触发 ----------

func (s *Service) handleSend(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var req SendRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Event == "" {
		httputil.WriteError(w, http.StatusBadRequest, "event is required")
		return
	}
	sent, failed := s.dispatchEvent(r.Context(), tenantID, req.Event, req.Title, req.Content)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"event":  req.Event,
		"sent":   sent,
		"failed": failed,
	})
}

// dispatchEvent 是内部事件分发逻辑:查找匹配 Policy -> 向其绑定的渠道发送。
// 该方法不阻塞调用方,失败仅记录历史。
func (s *Service) dispatchEvent(ctx context.Context, tenantID string, event EventType, title, content string) (sent, failed int) {
	policies, err := s.store.ListPolicies(ctx, tenantID)
	if err != nil {
		slog.Error("failed to list policies", "event", event, "error", err)
		return 0, 0
	}
	for _, p := range policies {
		if !p.Enabled || p.Event != event {
			continue
		}
		// 检查免打扰时段
		if isInQuietHours(p.QuietHours, time.Now()) {
			slog.Info("notification skipped due to quiet hours", "policy", p.Name, "event", event)
			continue
		}
		// 应用模板
		t, c := applyTemplate(p.Template, title, content)
		for _, cid := range p.ChannelIDs {
			ch, err := s.store.GetChannel(ctx, cid, tenantID)
			if err != nil || ch == nil || !ch.Enabled {
				continue
			}
			sender, ok := s.senders[ch.Type]
			if !ok {
				continue
			}
			n := &Notification{
				TenantID:  tenantID,
				ChannelID: ch.ID,
				Event:     event,
				Title:     t,
				Content:   c,
				Status:    "sent",
			}
			if err := sender.Send(ch, t, c); err != nil {
				n.Status = "failed"
				n.Error = err.Error()
				failed++
				slog.Warn("notification send failed", "channel", ch.Name, "error", err)
			} else {
				sent++
			}
			s.store.SaveNotification(ctx, n)
		}
	}
	return sent, failed
}

// applyTemplate 应用模板变量替换。模板支持 ${title} ${content} 占位符。
// 若模板为空,直接返回原始 title/content。
func applyTemplate(tmpl, title, content string) (string, string) {
	if tmpl == "" {
		return title, content
	}
	// 简单实现:模板整体作为 content,title 保持原样
	rendered := strings.ReplaceAll(tmpl, "${title}", title)
	rendered = strings.ReplaceAll(rendered, "${content}", content)
	return title, rendered
}

// isInQuietHours 判断当前时间是否在免打扰时段内。
// 格式 "HH:MM-HH:MM",支持跨天(如 "22:00-08:00")。
func isInQuietHours(quiet string, now time.Time) bool {
	if quiet == "" {
		return false
	}
	parts := strings.Split(quiet, "-")
	if len(parts) != 2 {
		return false
	}
	start, err1 := parseHHMM(parts[0])
	end, err2 := parseHHMM(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if start <= end {
		// 同一天内,如 "09:00-18:00"
		return cur >= start && cur < end
	}
	// 跨天,如 "22:00-08:00"
	return cur >= start || cur < end
}

func parseHHMM(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil {
		return 0, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid time")
	}
	return h*60 + m, nil
}

// maskConfig 对渠道配置中的敏感字段做掩码,仅返回掩码后的副本。
// 用于 list/get 接口响应,避免凭证泄露。
func maskConfig(t ChannelType, cfg map[string]string) map[string]string {
	if cfg == nil {
		return map[string]string{}
	}
	masked := make(map[string]string, len(cfg))
	for k, v := range cfg {
		masked[k] = v
	}
	// 敏感字段:password / secret / auth_header / access_token
	sensitiveKeys := []string{"password", "secret", "auth_header", "access_token", "key"}
	for _, k := range sensitiveKeys {
		if v, ok := masked[k]; ok && v != "" {
			masked[k] = maskString(v)
		}
	}
	return masked
}

// NotifyEvent 公开的事件通知接口，供 scheduler 等模块调用。
func (s *Service) NotifyEvent(ctx context.Context, tenantID string, event string, title, content string) error {
	sent, failed := s.dispatchEvent(ctx, tenantID, EventType(event), title, content)
	if sent > 0 {
		slog.Info("notification sent", "event", event, "sent", sent, "failed", failed)
	}
	return nil
}

func maskString(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + strings.Repeat("*", 4) + s[len(s)-2:]
}

// ---------- 辅助 HTTP 函数 ----------
