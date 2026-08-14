package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/httputil"
)

func TestRecoveryOnServerChain(t *testing.T) {
	cfg := &config.Config{}
	cfg.Core.Addr = ":0"
	srv := NewServer(cfg)

	panicSvc := &panicRegistrar{}
	srv.RegisterService(panicSvc)
	srv.WithBypassPrefixes("/bypass")

	// 模拟 main 的最外层 wrapper 接入: recover 贴近业务, 外层再叠一个标记 wrapper
	// 验证组合链没有被破坏。
	srv.WithHandlerWrapper(func(next http.Handler) http.Handler {
		recovered := httputil.RecoverHandler(next, slog.Default())
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Outer", "1")
			recovered.ServeHTTP(w, r)
		})
	})

	h := srv.Handler()

	// 普通端点 panic → 500 且不崩
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("普通端点 panic 应返回 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Fatalf("响应应含 error 信息, got %s", rec.Body.String())
	}

	// bypass 前缀内的路由 panic 同样被兜住
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/bypass/boom", nil))
	if rec2.Code != http.StatusInternalServerError {
		t.Fatalf("bypass 端点 panic 应返回 500, got %d", rec2.Code)
	}

	// 最外层 wrapper 仍生效(验证 wrapper 组合没有破坏原有链)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest("GET", "/api/v1/ok", nil))
	if rec3.Header().Get("X-Outer") != "1" {
		t.Fatalf("外层 wrapper 应仍生效")
	}
	if rec3.Code != http.StatusOK {
		t.Fatalf("正常请求应 200, got %d", rec3.Code)
	}
}

type panicRegistrar struct{}

func (p *panicRegistrar) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	mux.HandleFunc("GET /bypass/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("boom-bypass")
	})
	mux.HandleFunc("GET /api/v1/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}
