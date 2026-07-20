package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/opsorigin/core/pkg/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// 命令行参数
	serverAddr := flag.String("server", "", "server address (host:port)")
	wsServer := flag.String("ws-server", "", "websocket server URL (e.g. ws://host:5588/agent-ws)")
	grpcServer := flag.String("grpc-server", "", "gRPC server address (host:port) for direct connection")
	agentName := flag.String("name", "", "agent name (defaults to hostname)")
	proxyAddr := flag.String("proxy", "", "proxy address (host:port) for indirect connection, e.g. 10.0.0.1:9090")
	flag.Parse()

	// 优先使用命令行参数,其次环境变量,最后默认值
	addr := *serverAddr
	if addr == "" {
		addr = os.Getenv("OPSORIGIN_SERVER")
	}
	if addr == "" {
		addr = cfg.Core.Addr
	}
	if addr == "" {
		addr = "localhost:8080"
	}

	wsAddr := *wsServer
	if wsAddr == "" {
		wsAddr = os.Getenv("OPSORIGIN_WS_SERVER")
	}

	grpcAddr := *grpcServer
	if grpcAddr == "" {
		grpcAddr = os.Getenv("OPSORIGIN_GRPC_SERVER")
	}

	name := *agentName
	if name == "" {
		name = os.Getenv("OPSORIGIN_AGENT_NAME")
	}

	// Proxy 地址:命令行 > 环境变量 > config > 空
	proxy := *proxyAddr
	if proxy == "" {
		proxy = os.Getenv("OPSORIGIN_PROXY_ADDR")
	}
	if proxy == "" {
		proxy = cfg.Agent.ProxyAddr
	}
	if proxy == "localhost:9090" || proxy == "" {
		proxy = "" // 默认值视为未设置,走直连
	}

	agentCfg := AgentConfig{
		ServerAddr: addr,
		WSServer:   wsAddr,
		GRPCServer: grpcAddr,
		ProxyAddr:  proxy,
		Interval:   30 * time.Second,
		AgentName:  name,
	}

	agent := NewAgent(agentCfg)
	go func() {
		if err := agent.Run(ctx); err != nil {
			slog.Error("agent stopped", "error", err)
			cancel()
		}
	}()

	slog.Info("edge agent started", "server_addr", addr, "ws_server", wsAddr, "grpc_server", grpcAddr, "proxy", proxy)
	<-sigCh
	slog.Info("shutting down agent...")
	agent.Stop()
}
