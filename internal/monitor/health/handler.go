package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// HealthHandler 健康检查 Handler，支持并行检查 PostgreSQL 和 Redis。
// 同一实例按路径区分两类探针：
//   - /healthz  liveness  进程存活即 200（最快路径，供 K8s livenessProbe / LB 保活）
//   - /readyz   readiness 依赖(PG/Redis)就绪才 200，否则 503（供 K8s readinessProbe / LB 摘流）
//
// /api/v1/health 与 /readyz 行为一致，保持向后兼容。
type HealthHandler struct {
	db         *sql.DB
	rdb        *redis.Client
	instanceID string
	version    string
	startTime  time.Time
}

// NewHealthHandler 创建健康检查 Handler。
// instanceID 由 hostname+pid 生成，用于多实例部署时区分请求落在哪个实例。
func NewHealthHandler(db *sql.DB, rdb *redis.Client) *HealthHandler {
	return &HealthHandler{
		db:         db,
		rdb:        rdb,
		instanceID: instanceID(),
		startTime:  time.Now(),
	}
}

// WithVersion 设置构建版本号（可选，注入编译期版本信息）。
func (h *HealthHandler) WithVersion(v string) *HealthHandler {
	h.version = v
	return h
}

// instanceID 生成进程级唯一标识: hostname-pid。
func instanceID() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return host + "-" + itoa(os.Getpid())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// HealthResponse 健康检查响应格式。
type HealthResponse struct {
	Status        string            `json:"status"`
	Services      map[string]string `json:"services"`
	InstanceID    string            `json:"instance_id"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	Version       string            `json:"version,omitempty"`
}

// ServeHTTP 实现 http.Handler。路径分流 liveness/readiness。
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /healthz: 纯存活探针, 不查依赖, 立即响应
	if r.URL.Path == "/healthz" {
		h.write(w, http.StatusOK, HealthResponse{
			Status:        "ok",
			Services:      map[string]string{},
			InstanceID:    h.instanceID,
			UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
			Version:       h.version,
		})
		return
	}

	// /readyz 与 /api/v1/health: 依赖就绪检查
	resp := HealthResponse{
		Status:        "healthy",
		Services:      make(map[string]string),
		InstanceID:    h.instanceID,
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
		Version:       h.version,
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	if h.db != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			err := h.db.PingContext(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				resp.Services["postgres"] = "down"
				resp.Status = "unhealthy"
			} else {
				resp.Services["postgres"] = "up"
			}
		}()
	}

	if h.rdb != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			err := h.rdb.Ping(ctx).Err()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				resp.Services["redis"] = "down"
				resp.Status = "unhealthy"
			} else {
				resp.Services["redis"] = "up"
			}
		}()
	}

	wg.Wait()

	statusCode := http.StatusOK
	if resp.Status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}
	h.write(w, statusCode, resp)
}

func (h *HealthHandler) write(w http.ResponseWriter, code int, resp HealthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}
