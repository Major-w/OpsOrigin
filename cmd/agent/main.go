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
	grpcServer := flag.String("grpc-server", "", "gRPC server address (host:port) for direct connection")
	agentName := flag.String("name", "", "agent name (defaults to hostname)")
	proxyAddr := flag.String("proxy", "", "proxy address (host:port) for indirect connection, e.g. 10.0.0.1:9090")
	cpuLimit := flag.Int("cpu-limit", 0, "resource guard: agent self CPU usage limit percent (0=unlimited)")
	memLimit := flag.Int("mem-limit", 0, "resource guard: agent self RSS memory limit in MB (0=unlimited)")
	rgInterval := flag.Int("rg-interval", 30, "resource guard: check interval in seconds")
	rgEnabled := flag.Bool("rg-enabled", false, "resource guard: enable self-terminate on resource limit exceeded")
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

	// gRPC 接入是唯一支持的连接方式(直连或经 Proxy)。
	if grpcAddr == "" && proxy == "" {
		log.Fatalf("agent 仅支持 gRPC 接入: 请通过 --grpc-server 直连 Core,或通过 --proxy 经代理中转;HTTP 轮询与 WebSocket 模式已禁用")
	}

	agentCfg := AgentConfig{
		ServerAddr: addr,
		GRPCServer: grpcAddr,
		ProxyAddr:  proxy,
		Interval:   30 * time.Second,
		AgentName:  name,
		ResourceGuard: ResourceGuardConfig{
			Enabled:          *rgEnabled,
			CPUPercent:       *cpuLimit,
			MemMB:            *memLimit,
			CheckIntervalSec: *rgInterval,
		},
	}

	agent := NewAgent(agentCfg)
	go func() {
		if err := agent.Run(ctx); err != nil {
			slog.Error("agent stopped", "error", err)
			cancel()
		}
	}()

	slog.Info("edge agent started", "server_addr", addr, "grpc_server", grpcAddr, "proxy", proxy)
	<-sigCh
	slog.Info("shutting down agent...")
	agent.Stop()
}
