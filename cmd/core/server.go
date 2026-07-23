package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/opsorigin/core/pkg/config"
)

// routeRegistrar 是 Server.RegisterService 接受的统一接口。
// 仓库内所有业务 service（audit/cmdb/monitor/itsm/agent/sysconfig/
// script/workflow/scheduler/knowledge/oncall/keystore/user/iam/
// notification/ai/ops，以及 lowcode 各 handler）均实现
// RegisterRoutes(*http.ServeMux)，由本网关聚合到同一个 mux 上。
type routeRegistrar interface {
	RegisterRoutes(mux *http.ServeMux)
}

// Server 是 Core 的主控 HTTP API 网关：聚合各业务 service 的 REST 路由，
// 叠加 auth→audit→rbac 等中间件链后，监听 cfg.Core.Addr 对外服务。
//
// 注意：Agent / Proxy 的连接通道（internal/tunnel 的 TCP/TLS tunnel server、
// gRPC tunnel server，以及 agentmgr 的 Agent WebSocket hub）均由各自独立监听，
// 不经过本网关——本网关只负责把控制面 REST API 暴露给所有调用方。
type Server struct {
	mux        *http.ServeMux
	middlewares []func(http.Handler) http.Handler
	addr       string

	mu         sync.Mutex
	httpServer *http.Server
}

// NewServer 构造 Core 主 HTTP 网关，监听地址取自 cfg.Core.Addr。
func NewServer(cfg *config.Config) *Server {
	return &Server{
		mux:  http.NewServeMux(),
		addr: cfg.Core.Addr,
	}
}

// RegisterService 将某个业务 service 的路由注册进网关 mux。
func (s *Server) RegisterService(svc routeRegistrar) {
	svc.RegisterRoutes(s.mux)
}

// WithMiddleware 向网关追加一层 http 中间件（按注册顺序由外到内包裹）。
func (s *Server) WithMiddleware(mw func(http.Handler) http.Handler) {
	s.middlewares = append(s.middlewares, mw)
}

// Serve 启动网关，并在 ctx 取消或 GracefulStop 触发时优雅关闭。
func (s *Server) Serve(ctx context.Context) error {
	var h http.Handler = s.mux
	// 中间件按注册顺序由外到内包裹：最后一个注册的处于最内层。
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		h = s.middlewares[i](h)
	}

	srv := &http.Server{
		Addr:    s.addr,
		Handler: h,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	s.mu.Lock()
	s.httpServer = srv
	s.mu.Unlock()

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("core server listen %s: %w", s.addr, err)
	}

	// ctx 被取消（例如其他组件出错触发 cancel）时主动优雅关闭。
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// GracefulStop 主动优雅关闭网关，由主 goroutine 在收到 SIGINT/SIGTERM 后调用。
func (s *Server) GracefulStop() {
	s.mu.Lock()
	srv := s.httpServer
	s.mu.Unlock()
	if srv == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
