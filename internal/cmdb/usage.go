package cmdb

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// ——— CMDB 数据消费分析 ———
//
// 组成：
//  1) UsageMiddleware —— 挂在网关最内层（紧贴 mux），采集每一次 /api/v1/cmdb*
//     调用的 method/route/status/latency/ci_id/调用方。写在 next.ServeHTTP 之后，
//     因此可读取 Go 1.22 ServeMux 在路由期间回填的 r.Pattern / r.PathValue("id")。
//  2) usageRecorder —— 内存有界 channel + 后台批量落库。请求路径零阻塞：
//     缓冲满时直接丢弃并计数，绝不因为分析采集拖慢业务接口。
//  3) 只读聚合端点：summary / hotspots / errors / status。
//
// 自噪声规避：/api/v1/cmdb/usage* 自身不采集，避免"看报表"这一动作把报表数据顶上去。
//
// 采集边界：中间件位于鉴权链最内层（auth → audit → rbac → usage → mux），
// 因此只记录「通过认证与授权、真正触达业务处理器」的调用 —— 即真实的**数据消费**。
// 401/403 这类安全层拒绝属审计日志（/api/v1/audit）职责，不在此重复统计，
// 也避免未授权扫描把调用量口径污染。错误率口径 = 处理器返回的 4xx/5xx。

const (
	usageChanSize      = 4096
	usageFlushMax      = 200
	usageFlushInterval = 2 * time.Second
	usageRetention     = 30 * 24 * time.Hour
)

// UsageRecorderStats 暴露采集器的健康度（丢弃计数等），供运维排查。
type UsageRecorderStats struct {
	Enqueued  uint64 `json:"enqueued"`
	Dropped   uint64 `json:"dropped"`
	Flushed   uint64 `json:"flushed"`
	LastError string `json:"last_error,omitempty"`
}

type usageRecorder struct {
	repo *pg.Repository
	ch   chan pg.UsageEvent

	enqueued atomic.Uint64
	dropped  atomic.Uint64
	flushed  atomic.Uint64
	lastErr  atomic.Value // string
}

// EnableUsageAnalytics 装配采集器并启动后台批量落库。pg 不可用时为空操作，
// 相关端点返回 501（不影响 CMDB 其它能力）。由 cmd/core 在装配期调用。
func (s *Service) EnableUsageAnalytics(ctx context.Context) {
	if s.pg == nil {
		slog.Info("cmdb usage analytics disabled: postgres backend unavailable")
		return
	}
	s.usage = &usageRecorder{
		repo: s.pg,
		ch:   make(chan pg.UsageEvent, usageChanSize),
	}
	go s.usage.loop(ctx)
}

// UsageMiddleware 采集 /api/v1/cmdb* 的调用记录。非 CMDB 路径零开销直通。
// 必须注册为最后一层中间件（最内层），以保证 r.Pattern 已由 mux 回填。
func (s *Service) UsageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.usage == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/cmdb") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/cmdb/usage") {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		sw := &usageStatusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		route := r.Pattern
		if route == "" {
			// 未命中路由（404）时 Pattern 为空，用占位符保住计数。
			route = "(unmatched)"
		}

		ciID := ""
		if strings.Contains(route, "{id}") && strings.Contains(route, "/cmdb/ci/") {
			ciID = r.PathValue("id")
		}

		s.usage.enqueue(pg.UsageEvent{
			TenantID:   auth.TenantFromContext(r.Context()),
			UserID:     auth.UserIDFromContext(r.Context()),
			Method:     r.Method,
			Route:      route,
			Path:       truncatePath(r.URL.Path, 300),
			CIID:       ciID,
			StatusCode: sw.status,
			LatencyMS:  int(time.Since(start).Milliseconds()),
		})
	})
}

func (u *usageRecorder) enqueue(e pg.UsageEvent) {
	if e.TenantID == "" {
		e.TenantID = "default"
	}
	select {
	case u.ch <- e:
		u.enqueued.Add(1)
	default:
		// 缓冲满：丢弃并计数，绝不阻塞请求。
		if n := u.dropped.Add(1); n%500 == 1 {
			slog.Warn("cmdb usage recorder backlog, dropping events", "dropped", n)
		}
	}
}

func (u *usageRecorder) loop(ctx context.Context) {
	ticker := time.NewTicker(usageFlushInterval)
	defer ticker.Stop()
	purgeTicker := time.NewTicker(time.Hour)
	defer purgeTicker.Stop()

	buf := make([]pg.UsageEvent, 0, usageFlushMax)
	for {
		select {
		case <-ctx.Done():
			// 退出前把缓冲里的最后一批落库（ctx 已取消，故用 WithoutCancel 派生独立超时）。
			u.flush(context.WithoutCancel(ctx), buf)
			return
		case e := <-u.ch:
			buf = append(buf, e)
			if len(buf) >= usageFlushMax {
				buf = u.flush(ctx, buf)
			}
		case <-ticker.C:
			buf = u.flush(ctx, buf)
		case <-purgeTicker.C:
			// 保留窗口外的记录定期回收，避免表无限膨胀。
			pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if _, err := u.repo.PurgeUsageBefore(pctx, "default", time.Now().Add(-usageRetention)); err != nil {
				slog.Warn("cmdb usage purge failed", "error", err)
			}
			cancel()
		}
	}
}

// flush 落库当前缓冲，返回清空后的切片（复用底层数组）。落库失败即丢弃该批，
// 只记日志——分析数据不应回压业务路径。
func (u *usageRecorder) flush(ctx context.Context, buf []pg.UsageEvent) []pg.UsageEvent {
	if len(buf) == 0 {
		return buf
	}
	fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := u.repo.RecordUsageBatch(fctx, buf); err != nil {
		u.lastErr.Store(err.Error())
		slog.Warn("cmdb usage flush failed, events dropped", "count", len(buf), "error", err)
		return buf[:0]
	}
	u.flushed.Add(uint64(len(buf)))
	return buf[:0]
}

// UsageStats 返回采集器健康度（nil 安全）。
func (s *Service) UsageStats() UsageRecorderStats {
	if s.usage == nil {
		return UsageRecorderStats{}
	}
	st := UsageRecorderStats{
		Enqueued: s.usage.enqueued.Load(),
		Dropped:  s.usage.dropped.Load(),
		Flushed:  s.usage.flushed.Load(),
	}
	if v, ok := s.usage.lastErr.Load().(string); ok {
		st.LastError = v
	}
	return st
}

func truncatePath(p string, n int) string {
	if len(p) <= n {
		return p
	}
	return p[:n]
}

// usageStatusWriter 记录响应状态码。透传 Flush，避免破坏流式响应。
type usageStatusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *usageStatusWriter) WriteHeader(code int) {
	if !w.written {
		w.status = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *usageStatusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *usageStatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ——— 查询端点 ———

// usageWindow 解析 ?hours= 窗口参数（默认 24h，上限 30 天）并推导时间桶粒度。
func usageWindow(r *http.Request) (since time.Time, bucket string, hours int) {
	hours = 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hours = n
		}
	}
	if hours > 24*30 {
		hours = 24 * 30
	}
	bucket = "hour"
	if hours > 48 {
		bucket = "day"
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour), bucket, hours
}

// handleUsageSummary GET /api/v1/cmdb/usage/summary
func (s *Service) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "usage analytics requires postgres backend")
		return
	}
	since, bucket, hours := usageWindow(r)
	sum, err := s.pg.UsageSummary(r.Context(), auth.TenantFromContext(r.Context()), since, bucket)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sum.WindowHours = hours
	httputil.WriteJSON(w, http.StatusOK, sum)
}

// handleUsageHotspots GET /api/v1/cmdb/usage/hotspots
func (s *Service) handleUsageHotspots(w http.ResponseWriter, r *http.Request) {
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "usage analytics requires postgres backend")
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	since, _, _ := usageWindow(r)
	hot, err := s.pg.UsageHotspots(r.Context(), auth.TenantFromContext(r.Context()), since, limit)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, hot)
}

// handleUsageErrors GET /api/v1/cmdb/usage/errors
func (s *Service) handleUsageErrors(w http.ResponseWriter, r *http.Request) {
	if s.pg == nil {
		httputil.WriteError(w, http.StatusNotImplemented, "usage analytics requires postgres backend")
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	since, bucket, _ := usageWindow(r)
	res, err := s.pg.UsageErrors(r.Context(), auth.TenantFromContext(r.Context()), since, bucket, limit)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, res)
}

// handleUsageStatus GET /api/v1/cmdb/usage/status —— 采集器自身健康度。
func (s *Service) handleUsageStatus(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.UsageStats())
}
