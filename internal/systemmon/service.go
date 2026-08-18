// Package systemmon 提供系统自监控：主要服务/组件状态探活 + 日志查看。
// 状态：core(运行时)/postgres/redis/minio/dgraph/llm。
// 日志：core 走进程内环形缓冲(启动即捕获); 其他组件走 docker socket 的 docker logs
// (需在 compose 给 core 挂载 /var/run/docker.sock, 未挂载时优雅降级提示)。
package systemmon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/objectstore"
	"github.com/opsorigin/core/pkg/redis"
)

// StatusItem 单个组件状态。
type StatusItem struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Status  string `json:"status"` // ok | down | degraded | unknown
	Detail  string `json:"detail"`
	Latency int64  `json:"latency_ms"`
}

// Service 自监控服务。
type Service struct {
	db              *sql.DB
	rdb             *redis.Client
	obj             *objectstore.Client
	dgraphAddr      string
	llmConfigured   bool
	startTime       time.Time
	ring            *LogRing
	dockerSock      string // /var/run/docker.sock; 空=未挂载
	containerPrefix string // compose 项目名前缀, 如 "deploy-"
}

// New 创建自监控服务。
func New(db *sql.DB, rdb *redis.Client, obj *objectstore.Client, dgraphAddr string, llmConfigured bool) *Service {
	return &Service{
		db:              db,
		rdb:             rdb,
		obj:             obj,
		dgraphAddr:      dgraphAddr,
		llmConfigured:   llmConfigured,
		startTime:       time.Now(),
		ring:            NewLogRing(600),
		dockerSock:      "/var/run/docker.sock",
		containerPrefix: "deploy-",
	}
}

// SetRing 注入日志环形缓冲(由 cmd/core 在启动早期创建并挂到 slog 上)。
func (s *Service) SetRing(r *LogRing) { s.ring = r }

// SetDockerSocket 覆盖 docker socket 路径; 空串禁用 docker 日志。
func (s *Service) SetDockerSocket(p string) { s.dockerSock = p }

// SetContainerPrefix 覆盖容器名前缀(compose 项目名)。
func (s *Service) SetContainerPrefix(p string) { s.containerPrefix = p }

// Ring 返回日志环形缓冲(slog handler 注入用)。
func (s *Service) Ring() *LogRing { return s.ring }

// RegisterRoutes 注册状态与日志端点。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/system/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/system/logs", s.handleLogs)
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	items := []StatusItem{
		s.probeCore(),
		s.probePostgres(ctx),
		s.probeRedis(ctx),
		s.probeMinio(ctx),
		s.probeDgraph(ctx),
		s.probeLLM(),
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"server_time": time.Now().Format(time.RFC3339),
	})
}

func (s *Service) probeCore() StatusItem {
	return StatusItem{
		Name: "core", Label: "Core 主服务", Status: "ok",
		Detail: fmt.Sprintf("运行 %.0f 分钟 · goroutines %d · 堆 %.1f MB",
			time.Since(s.startTime).Minutes(), runtime.NumGoroutine(), float64(heapMB())),
	}
}

func (s *Service) probePostgres(ctx context.Context) StatusItem {
	it := StatusItem{Name: "postgres", Label: "PostgreSQL"}
	if s.db == nil {
		it.Status, it.Detail = "degraded", "未连接(仅内存模式)"
		return it
	}
	start := time.Now()
	if err := s.db.PingContext(ctx); err != nil {
		it.Status, it.Detail = "down", "连接失败: "+err.Error()
		return it
	}
	it.Status, it.Detail = "ok", "Ping OK"
	it.Latency = time.Since(start).Milliseconds()
	return it
}

func (s *Service) probeRedis(ctx context.Context) StatusItem {
	it := StatusItem{Name: "redis", Label: "Redis"}
	if s.rdb == nil {
		it.Status, it.Detail = "degraded", "未连接(内存模式)"
		return it
	}
	start := time.Now()
	// 注意: 不能 Conn().Close() —— Conn() 返回共享 client, Close 会毒化限流/心跳等服务。
	if err := s.rdb.Ping(ctx); err != nil {
		it.Status, it.Detail = "down", "Ping 失败: "+err.Error()
		return it
	}
	it.Status, it.Detail = "ok", "Ping OK"
	it.Latency = time.Since(start).Milliseconds()
	return it
}

func (s *Service) probeMinio(ctx context.Context) StatusItem {
	it := StatusItem{Name: "minio", Label: "MinIO 对象存储"}
	if s.obj == nil {
		it.Status, it.Detail = "degraded", "未启用(附件回退数据库)"
		return it
	}
	start := time.Now()
	if err := s.obj.Health(ctx); err != nil {
		it.Status, it.Detail = "down", "不可达: "+err.Error()
		return it
	}
	it.Status, it.Detail = "ok", "桶 "+s.obj.Bucket()+" 可访问"
	it.Latency = time.Since(start).Milliseconds()
	return it
}

func (s *Service) probeDgraph(ctx context.Context) StatusItem {
	it := StatusItem{Name: "dgraph", Label: "Dgraph 图数据库"}
	if s.dgraphAddr == "" {
		it.Status, it.Detail = "unknown", "未配置"
		return it
	}
	start := time.Now()
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", s.dgraphAddr)
	if err != nil {
		it.Status, it.Detail = "down", "TCP 不可达: "+err.Error()
		return it
	}
	conn.Close()
	it.Status, it.Detail = "ok", "TCP 可达 "+s.dgraphAddr
	it.Latency = time.Since(start).Milliseconds()
	return it
}

func (s *Service) probeLLM() StatusItem {
	it := StatusItem{Name: "llm", Label: "LLM 大模型"}
	if !s.llmConfigured {
		it.Status, it.Detail = "degraded", "未配置 API Key(对话/主动进化不可用)"
		return it
	}
	it.Status, it.Detail = "ok", "已配置(详细健康见 AI 设置→模型)"
	return it
}

// handleLogs 返回指定来源的日志。source=core 走进程内缓冲; 其他走 docker logs。
func (s *Service) handleLogs(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "core"
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			lines = n
		}
	}
	if source == "core" {
		writeJSON(w, http.StatusOK, map[string]any{"source": "core", "lines": s.ring.Tail(lines)})
		return
	}
	if s.dockerSock == "" {
		writeError(w, http.StatusNotImplemented, "docker socket 未挂载, 无法读取其他组件日志")
		return
	}
	container := s.containerPrefix + source
	logs, err := s.dockerLogs(r.Context(), container, lines)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取容器日志失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "container": container, "lines": logs})
}

// dockerLogs 通过 docker socket HTTP API 拉取容器日志。
func (s *Service) dockerLogs(ctx context.Context, container string, tail int) ([]string, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", s.dockerSock)
			},
		},
		Timeout: 6 * time.Second,
	}
	url := fmt.Sprintf("http://docker/containers/%s/logs?stdout=1&stderr=1&tail=%d&timestamps=1", container, tail)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, tail)
	for len(raw) >= 8 {
		size := int(raw[4])<<24 | int(raw[5])<<16 | int(raw[6])<<8 | int(raw[7])
		if size <= 0 || len(raw) < 8+size {
			break
		}
		line := strings.TrimSpace(string(raw[8 : 8+size]))
		if line != "" {
			out = append(out, line)
		}
		raw = raw[8+size:]
	}
	return out, nil
}

func heapMB() int {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int(m.HeapAlloc / 1024 / 1024)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ——— 日志环形缓冲 + slog Handler ———

// LogRing 保存最近 N 行格式化日志(新在前)。
type LogRing struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func NewLogRing(max int) *LogRing { return &LogRing{max: max} }

func (r *LogRing) Append(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	if len(r.lines) > r.max {
		r.lines = r.lines[len(r.lines)-r.max:]
	}
}

// Tail 返回最近 n 行(新→旧)。
func (r *LogRing) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = r.lines[len(r.lines)-1-i]
	}
	return out
}

// RingHandler 是一个 slog.Handler: 先向环形缓冲写格式化文本, 再转发给原 handler 正常输出。
type RingHandler struct {
	next slog.Handler
	fmt  slog.Handler
	ring *LogRing
}

// NewRingHandler 包装现有 handler, 使每条日志同时进入环形缓冲。
func NewRingHandler(next slog.Handler, ring *LogRing) *RingHandler {
	return &RingHandler{next: next, fmt: slog.NewTextHandler(&ringWriter{ring: ring}, nil), ring: ring}
}

type ringWriter struct{ ring *LogRing }

func (w *ringWriter) Write(p []byte) (int, error) {
	w.ring.Append(strings.TrimSpace(string(p)))
	return len(p), nil
}

func (h *RingHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *RingHandler) Handle(ctx context.Context, r slog.Record) error {
	_ = h.fmt.Handle(ctx, r) // 写入环形缓冲(忽略错误, 缓冲为尽力而为)
	return h.next.Handle(ctx, r)
}

func (h *RingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RingHandler{next: h.next.WithAttrs(attrs), fmt: h.fmt.WithAttrs(attrs), ring: h.ring}
}

func (h *RingHandler) WithGroup(name string) slog.Handler {
	return &RingHandler{next: h.next.WithGroup(name), fmt: h.fmt.WithGroup(name), ring: h.ring}
}
