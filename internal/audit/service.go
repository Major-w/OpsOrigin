package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/opsorigin/core/internal/audit/interceptor"
	"github.com/opsorigin/core/internal/audit/model"
	"github.com/opsorigin/core/internal/audit/rls"
	"github.com/opsorigin/core/internal/audit/store"
	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/principal"

	"github.com/opsorigin/core/pkg/httputil"
)

type Service struct {
	cfg   *config.Config
	store store.Store
	rls   *rls.Enforcer
}

func NewService(cfg *config.Config, db *sql.DB) *Service {
	var s store.Store
	if db != nil {
		pgStore := store.NewPGStore(db)
		if err := pgStore.Init(context.Background()); err != nil {
			slog.Error("failed to init audit pg store, falling back to memory", "error", err)
			s = store.NewMemoryStore()
		} else {
			s = pgStore
			slog.Info("audit: using postgresql store")
		}
	}
	if s == nil {
		s = store.NewMemoryStore()
		slog.Info("audit: using in-memory store")
	}

	return &Service{
		cfg:   cfg,
		store: s,
		rls:   rls.New(),
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/audit/events", s.handleRecordEvent)
	mux.HandleFunc("GET /api/v1/audit/events", s.handleQueryEvents)
	mux.HandleFunc("POST /api/v1/audit/validate-query", s.handleValidateQuery)
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return interceptor.Middleware(next)
}

// ActorFromContext 由可信主体上下文构造审计操作者。
// 任何模块写审计事件都应经由此函数取 actor,保证「谁干的」全平台口径一致,
// 且永远不取自请求体/请求头等调用方自述内容。
func ActorFromContext(ctx context.Context) model.AuditActor {
	pr, ok := principal.FromContext(ctx)
	if !ok || pr.ID == "" {
		return model.AuditActor{Type: string(principal.TypeService), ID: "system", DisplayName: "系统"}
	}
	return model.AuditActor{
		Type:        string(pr.Type),
		ID:          pr.ID,
		DisplayName: pr.Name(), // 无展示名时回落 ID,保证 actor 永不为空
		TicketID:    pr.TicketID,
	}
}

func (s *Service) RecordEvent(ctx context.Context, event *model.AuditEvent) error {
	if event.Actor.ID == "" {
		event.Actor = ActorFromContext(ctx)
	}
	return s.store.Record(ctx, event)
}

func (s *Service) handleRecordEvent(w http.ResponseWriter, r *http.Request) {
	var event model.AuditEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// actor 一律由服务端可信主体覆盖 —— 请求体里的 actor 不可信,
	// 否则任何登录用户都能伪造「某某删除了核心 CI」这类审计记录。
	event.Actor = ActorFromContext(r.Context())

	event.TraceID = interceptor.TraceIDFromContext(r.Context())
	event.TenantID = interceptor.TenantIDFromContext(r.Context())
	if event.TenantID == "" {
		// /api/v1/audit/* 被审计中间件跳过(避免自审计递归),租户回落主体上下文。
		event.TenantID = principal.TenantID(r.Context())
	}

	if event.TraceID == "" || event.TenantID == "" {
		writeError(w, http.StatusForbidden, "[ASSERT-AUDIT-01] missing trace_id or tenant_id")
		return
	}

	if err := s.store.Record(r.Context(), &event); err != nil {
		slog.Error("audit record failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record audit event")
		return
	}

	slog.Info("audit event recorded",
		"trace_id", event.TraceID,
		"event_type", event.EventType,
		"actor_type", event.Actor.Type)
	writeJSON(w, http.StatusCreated, event)
}

func (s *Service) handleQueryEvents(w http.ResponseWriter, r *http.Request) {
	tenantID := interceptor.TenantIDFromContext(r.Context())
	q := r.URL.Query()

	filter := model.AuditFilter{
		TenantID:   tenantID,
		TraceID:    q.Get("trace_id"),
		EventType:  q.Get("event_type"),
		ActorType:  q.Get("actor_type"),
		ActorID:    q.Get("actor_id"),
		TargetType: q.Get("target_type"),
		Keyword:    q.Get("q"),
	}
	if v := q.Get("success"); v == "true" || v == "false" {
		b := v == "true"
		filter.Success = &b
	}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.From = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.To = t
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	filter.Limit = limit
	filter.Offset = offset

	events, total, err := s.store.Query(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":  events,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Service) handleValidateQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query    string `json:"query"`
		TenantID string `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	enforced, err := s.rls.Enforce(req.Query, req.TenantID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"original": req.Query,
		"enforced": enforced,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	v = httputil.EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
