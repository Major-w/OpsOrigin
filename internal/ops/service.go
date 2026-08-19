package ops

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/opsorigin/core/pkg/config"

	"github.com/opsorigin/core/pkg/httputil"
)

type Service struct {
	cfg     *config.Config
	db      *sql.DB
	svcCtx  context.Context
	svcStop context.CancelFunc
	status  atomic.Value
}

type HealthStatus struct {
	Status    string            `json:"status"`
	Uptime    string            `json:"uptime"`
	Version   string            `json:"version"`
	Modules   map[string]string `json:"modules"`
	StartedAt time.Time         `json:"started_at"`
}

func NewService(cfg *config.Config, db *sql.DB) *Service {
	s := &Service{
		cfg: cfg,
		db:  db,
	}

	hs := HealthStatus{
		Status:    "running",
		Version:   "1.0.0",
		Modules:   make(map[string]string),
		StartedAt: time.Now(),
	}
	hs.Modules["core"] = "running"
	hs.Modules["api"] = "running"
	if db != nil {
		hs.Modules["postgresql"] = "running"
	} else {
		hs.Modules["postgresql"] = "unavailable"
	}
	s.status.Store(hs)
	return s
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/ops/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/ops/status", s.handleStatus)
	mux.HandleFunc("POST /api/v1/ops/stop", s.handleStop)
	mux.HandleFunc("POST /api/v1/ops/restart", s.handleRestart)
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	hs := s.status.Load().(HealthStatus)

	statusCode := http.StatusOK
	overall := "ok"
	for _, m := range hs.Modules {
		if m == "unavailable" || m == "down" {
			overall = "degraded"
			statusCode = http.StatusServiceUnavailable
			break
		}
	}

	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			m := hs.Modules
			m["postgresql"] = "down"
			overall = "degraded"
			statusCode = http.StatusServiceUnavailable
		}
	}

	httputil.WriteJSON(w, statusCode, map[string]any{
		"status":  overall,
		"uptime":  time.Since(hs.StartedAt).String(),
		"version": hs.Version,
		"modules": hs.Modules,
		"started": hs.StartedAt.Format(time.RFC3339),
	})
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	hs := s.status.Load().(HealthStatus)

	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			m := hs.Modules
			m["postgresql"] = "down"
		}
	}

	hs.Uptime = time.Since(hs.StartedAt).String()
	httputil.WriteJSON(w, http.StatusOK, hs)
}

func (s *Service) handleStop(w http.ResponseWriter, r *http.Request) {
	slog.Warn("graceful shutdown requested via API")
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "shutting_down"})

	go func() {
		time.Sleep(500 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		p.Signal(syscall.SIGTERM)
	}()
}

func (s *Service) handleRestart(w http.ResponseWriter, r *http.Request) {
	slog.Warn("restart requested via API")
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "restarting"})

	go func() {
		time.Sleep(500 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		p.Signal(syscall.SIGHUP)
	}()
}

func HandleSignal(cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig := <-sigCh
		slog.Info("received signal", "signal", sig)
		cancel()
	}()
}
