package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"

	pb "github.com/opsorigin/core/api/proto"
	"github.com/opsorigin/core/internal/tunnel"
	"github.com/opsorigin/core/pkg/config"
)

type Proxy struct {
	cfg        *config.Config
	wg         sync.WaitGroup
	cancel     context.CancelFunc
	client     *tunnel.Client      // TCP tunnel client (legacy)
	grpcClient *GRPCProxyClient    // gRPC client (new)
	agents     *AgentRegistry
}

func NewProxy(cfg *config.Config) *Proxy {
	hostname, _ := os.Hostname()
	return &Proxy{
		cfg:    cfg,
		agents: NewAgentRegistry(),
		client: tunnel.NewClient(
			cfg.Proxy.CoreAddr,
			cfg.Proxy.ID,
			hostname,
			[]string{"shell", "script", "file"},
			cfg.Tunnel.AuthSecret,
		),
	}
}

func (p *Proxy) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	// 指标纯转发: Agent 上报(MsgMetrics) → gRPC MetricBatch 透传 Core。
	// 不聚合不缓冲, 时效由 Agent 上报周期决定, Proxy 零额外延迟(§4.5)。
	p.agents.OnMetrics(func(agentID string, msg *tunnel.MetricsMessage) {
		if p.grpcClient == nil {
			slog.Warn("proxy: metrics dropped, grpc client not ready", "agent_id", agentID)
			return
		}
		pts := make([]*pb.MetricPoint, 0, len(msg.Points))
		for _, pt := range msg.Points {
			pts = append(pts, &pb.MetricPoint{
				Name:   pt.Name,
				Value:  pt.Value,
				Unit:   pt.Unit,
				Labels: pt.Labels,
			})
		}
		batch := &pb.MetricBatch{
			TaskId:      msg.TaskID,
			TaskType:    msg.TaskType,
			Timestamp:   msg.Timestamp,
			IntervalSec: msg.IntervalSec,
			Points:      pts,
		}
		if err := p.grpcClient.SendMetrics(agentID, batch); err != nil {
			slog.Warn("proxy: metrics forward failed", "agent_id", agentID, "error", err)
		}
	})

	// 优先使用 gRPC 连接 Core
	if p.cfg.Proxy.GRPCListen != "" {
		hostname, _ := os.Hostname()
		p.grpcClient = NewGRPCProxyClient(
			p.cfg.Proxy.CoreAddr,
			p.cfg.Proxy.ID,
			hostname,
			p.agents,
			p.cfg.Tunnel.AuthSecret,
		)

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			if err := p.grpcClient.Start(ctx); err != nil {
				slog.Error("grpc proxy client stopped", "error", err)
			}
		}()
	} else {
		// 回退到 TCP tunnel
		p.client.OnAgentCommand(func(cmd *tunnel.AgentCommand) *tunnel.AgentResponse {
			slog.Info("routing command to agent",
				"agent_id", cmd.AgentID,
				"action", cmd.Action,
				"command_id", cmd.CommandID)

			agent := p.agents.Get(cmd.AgentID)
			if agent == nil {
				return &tunnel.AgentResponse{
					CommandID: cmd.CommandID,
					AgentID:   cmd.AgentID,
					Success:   false,
					Error:     "agent not connected",
				}
			}

			resp, err := agent.SendCommand(ctx, cmd.Action, cmd.Payload)
			if err != nil {
				return &tunnel.AgentResponse{
					CommandID: cmd.CommandID,
					AgentID:   cmd.AgentID,
					Success:   false,
					Error:     err.Error(),
				}
			}
			return resp
		})

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			if err := p.client.Start(ctx); err != nil {
				slog.Error("tunnel client stopped", "error", err)
			}
		}()
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.agents.Serve(ctx, p.cfg.Proxy.Listen)
	}()

	// §5 HTTP 文件代理:若配置了 FileListen,启动独立 HTTP 服务,
	// 拓扑上经 Proxy 接入的 Agent 由此拉取文件(令牌边缘校验 + Range 转发)。
	if p.cfg.Proxy.FileListen != "" {
		fp := NewFileProxy(p.cfg.Proxy.CoreAddr, p.cfg.FileStore.TokenSecret)
		httpSrv := &http.Server{
			Addr:    p.cfg.Proxy.FileListen,
			Handler: fp.Handler(),
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			slog.Info("file proxy listening", "addr", p.cfg.Proxy.FileListen)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("file proxy stopped", "error", err)
			}
		}()
		// 上下文取消时关闭文件代理。
		go func() {
			<-ctx.Done()
			_ = httpSrv.Close()
		}()
	}

	slog.Info("proxy started",
		"core", p.cfg.Proxy.CoreAddr,
		"listen", p.cfg.Proxy.Listen,
		"grpc", p.cfg.Proxy.GRPCListen != "")
	return nil
}

func (p *Proxy) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}
