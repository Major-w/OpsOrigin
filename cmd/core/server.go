package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	httpSwagger "github.com/swaggo/http-swagger"

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
	mux         *http.ServeMux
	middlewares []func(http.Handler) http.Handler
	addr        string

	mu             sync.Mutex
	httpServer     *http.Server
	bypassPrefixes []string
	handlerWrapper func(http.Handler) http.Handler
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

// WithBypassPrefixes 追加旁路前缀路径，命中这些前缀的请求将跳过所有中间件直接路由到 mux。
func (s *Server) WithBypassPrefixes(prefixes ...string) {
	s.bypassPrefixes = append(s.bypassPrefixes, prefixes...)
}

// WithHandlerWrapper 设置一个最外层的 HTTP Handler 包装函数（如 promhttp.InstrumentHandlerDuration）。
func (s *Server) WithHandlerWrapper(wrap func(http.Handler) http.Handler) {
	s.handlerWrapper = wrap
}

// buildHandler 组装最终 HTTP handler：中间件按注册顺序由外到内包裹，
// 命中 bypass 前缀的请求跳过中间件直达 mux，最后应用可选的最外层包装。
func (s *Server) buildHandler() http.Handler {
	var h http.Handler = s.mux
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		h = s.middlewares[i](h)
	}

	// 最外层：旁路检查，命中的路径跳过所有中间件直接走 mux
	var final http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, prefix := range s.bypassPrefixes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				s.mux.ServeHTTP(w, r)
				return
			}
		}
		h.ServeHTTP(w, r)
	})

	// 应用可选的 Handler 包装（如 promhttp.InstrumentHandlerDuration）
	if s.handlerWrapper != nil {
		final = s.handlerWrapper(final)
	}
	return final
}

// Handler 返回组装完成的 http.Handler（中间件链 + 旁路前缀 + 外层包装），
// 与 Serve 实际使用的处理链完全一致。测试与嵌入式场景直接调用即可，
// 无需复制中间件包裹逻辑。
func (s *Server) Handler() http.Handler {
	return s.buildHandler()
}

// Serve 启动网关，并在 ctx 取消或 GracefulStop 触发时优雅关闭。
func (s *Server) Serve(ctx context.Context) error {
	final := s.buildHandler()

	srv := &http.Server{
		Addr:    s.addr,
		Handler: final,
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

// swaggerRegistrar 注册 Swagger UI 路由
type swaggerRegistrar struct{}

func (s *swaggerRegistrar) RegisterRoutes(mux *http.ServeMux) {
	// 服务 swagger.json 文件
	mux.HandleFunc("GET /swagger/doc.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "docs/swagger/swagger.json")
	})
	// 服务 Swagger UI
	mux.Handle("/swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))
}
