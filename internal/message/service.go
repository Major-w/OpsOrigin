package message

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/opsorigin/core/pkg/httputil"
	"github.com/opsorigin/core/pkg/principal"
)

// Pusher 实时推送出口(由 cmd/core 桥接到 WebSocket Hub)。
// 未注入时消息仍会落库,前端靠轮询兜底拿到。
type Pusher interface {
	// PushToUser 向指定收件人推送事件。userID / username 任一为空时,
	// 实现方应按非空的那个匹配在线连接。
	PushToUser(tenantID, userID, username string, payload any) int
}

// Service 站内消息服务:对内提供 Deliver 投递能力给各业务模块,
// 对外暴露收件箱 REST API。
type Service struct {
	store  Store
	pusher Pusher
}

// NewService 构造服务。db 非 nil 时用 PG 持久化,建表失败自动回落内存实现
// (保证站内消息永远不会因为 DDL 失败而拖垮主流程)。
func NewService(db *sql.DB) *Service {
	var st Store
	if db != nil {
		pg := NewPGStore(db)
		if err := pg.Init(context.Background()); err != nil {
			slog.Error("message: init pg store failed, falling back to memory", "error", err)
		} else {
			st = pg
			slog.Info("message: using postgresql store")
		}
	}
	if st == nil {
		st = NewMemStore()
		slog.Info("message: using in-memory store")
	}
	return &Service{store: st}
}

// NewServiceWithStore 供测试注入自定义 store。
func NewServiceWithStore(st Store) *Service { return &Service{store: st} }

// SetPusher 注入实时推送出口。
func (s *Service) SetPusher(p Pusher) { s.pusher = p }

// ---------------------------------------------------------------
// 投递:供业务模块调用
// ---------------------------------------------------------------

// Deliver 投递若干条消息:先落库,再尽力实时推送。
// 落库失败会返回错误;推送失败只记日志——收件箱是唯一真相源,推送只是加速。
func (s *Service) Deliver(ctx context.Context, msgs ...*Message) error {
	valid := make([]*Message, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		// 收件人为空的消息没有归属,直接丢弃而不是写成"孤儿"。
		if strings.TrimSpace(m.UserID) == "" && strings.TrimSpace(m.Username) == "" {
			continue
		}
		valid = append(valid, m)
	}
	if len(valid) == 0 {
		return nil
	}
	if err := s.store.Insert(ctx, valid...); err != nil {
		return err
	}
	s.push(valid)
	return nil
}

// DeliverTo 便捷投递:给一组收件人发同一条消息(自动去重)。
func (s *Service) DeliverTo(ctx context.Context, recipients []Recipient, tpl Message) error {
	seen := make(map[string]bool, len(recipients))
	var msgs []*Message
	for _, r := range recipients {
		if r.Empty() {
			continue
		}
		key := strings.ToLower(r.UserID + "|" + r.Username)
		if seen[key] {
			continue
		}
		seen[key] = true

		m := tpl // 值拷贝,避免多个收件人共享同一 ID
		m.ID = ""
		m.UserID = r.UserID
		m.Username = r.Username
		msgs = append(msgs, &m)
	}
	return s.Deliver(ctx, msgs...)
}

func (s *Service) push(msgs []*Message) {
	if s.pusher == nil {
		return
	}
	for _, m := range msgs {
		s.pusher.PushToUser(m.TenantID, m.UserID, m.Username, map[string]any{
			"type":    "message.new",
			"message": m,
		})
	}
}

// ---------------------------------------------------------------
// REST API
// ---------------------------------------------------------------

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/messages", s.handleList)
	mux.HandleFunc("GET /api/v1/messages/unread-count", s.handleUnreadCount)
	mux.HandleFunc("PUT /api/v1/messages/read-all", s.handleReadAll)
	mux.HandleFunc("PUT /api/v1/messages/{id}/read", s.handleRead)
	mux.HandleFunc("DELETE /api/v1/messages/{id}", s.handleDelete)
}

// recipientOf 从鉴权上下文推导当前收件人。
// 只信服务端解析出的主体,绝不读客户端自报的 user 参数——否则任何人都能翻别人的收件箱。
func recipientOf(r *http.Request) Recipient {
	p := principal.Get(r.Context())
	return Recipient{UserID: p.ID, Username: p.DisplayName}
}

func tenantOf(r *http.Request) string {
	if t := principal.TenantID(r.Context()); t != "" {
		return t
	}
	return "default"
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	recip := recipientOf(r)
	if recip.Empty() {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	list, total, err := s.store.List(r.Context(), ListQuery{
		TenantID:   tenantOf(r),
		Recipient:  recip,
		Category:   strings.TrimSpace(q.Get("category")),
		OnlyUnread: q.Get("unread") == "true" || q.Get("only_unread") == "true",
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"items": httputil.EmptySliceIfNil(list),
		"total": total,
	})
}

func (s *Service) handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	recip := recipientOf(r)
	if recip.Empty() {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	n, err := s.store.UnreadCount(r.Context(), tenantOf(r), recip)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (s *Service) handleRead(w http.ResponseWriter, r *http.Request) {
	recip := recipientOf(r)
	if recip.Empty() {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if err := s.store.MarkRead(r.Context(), tenantOf(r), r.PathValue("id"), recip); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleReadAll(w http.ResponseWriter, r *http.Request) {
	recip := recipientOf(r)
	if recip.Empty() {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	n, err := s.store.MarkAllRead(r.Context(), tenantOf(r), recip)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"updated": n})
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	recip := recipientOf(r)
	if recip.Empty() {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if err := s.store.Delete(r.Context(), tenantOf(r), r.PathValue("id"), recip); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusNoContent, nil)
}
