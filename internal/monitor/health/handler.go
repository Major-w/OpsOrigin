package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// HealthHandler 健康检查 Handler，支持并行检查 PostgreSQL 和 Redis。
type HealthHandler struct {
	db  *sql.DB
	rdb *redis.Client
}

// NewHealthHandler 创建健康检查 Handler。
func NewHealthHandler(db *sql.DB, rdb *redis.Client) *HealthHandler {
	return &HealthHandler{db: db, rdb: rdb}
}

// HealthResponse 健康检查响应格式。
type HealthResponse struct {
	Status   string          `json:"status"`
	Services map[string]string `json:"services"`
}

// ServeHTTP 实现 http.Handler。
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:   "healthy",
		Services: make(map[string]string),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// PG 检查
	if h.db != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			err := h.db.PingContext(ctx)
			mu.Lock()
			if err != nil {
				resp.Services["postgres"] = "down"
				resp.Status = "unhealthy"
			} else {
				resp.Services["postgres"] = "up"
			}
			mu.Unlock()
		}()
	}

	// Redis 检查
	if h.rdb != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			err := h.rdb.Ping(ctx).Err()
			mu.Lock()
			if err != nil {
				resp.Services["redis"] = "down"
				resp.Status = "unhealthy"
			} else {
				resp.Services["redis"] = "up"
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	statusCode := http.StatusOK
	if resp.Status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(resp)
}
