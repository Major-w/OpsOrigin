package main

import (
	"context"
	"log/slog"
	"os"
	"sync"

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
		),
	}
}

func (p *Proxy) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	// 优先使用 gRPC 连接 Core
	if p.cfg.Proxy.GRPCListen != "" {
		hostname, _ := os.Hostname()
		p.grpcClient = NewGRPCProxyClient(
			p.cfg.Proxy.CoreAddr,
			p.cfg.Proxy.ID,
			hostname,
			p.agents,
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
