package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	pb "github.com/opsorigin/core/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type GRPCProxyClient struct {
	coreAddr  string
	proxyID   string
	hostname  string
	authToken string
	conn      *grpc.ClientConn
	stream    pb.TunnelService_ConnectClient
	mu        sync.Mutex
	connected bool
	onCommand func(cmd *pb.AgentCommand) *pb.AgentResponse
	pending   sync.Map
	agents    *AgentRegistry
}

func NewGRPCProxyClient(coreAddr, proxyID, hostname string, agents *AgentRegistry, authToken string) *GRPCProxyClient {
	return &GRPCProxyClient{
		coreAddr:  coreAddr,
		proxyID:   proxyID,
		hostname:  hostname,
		authToken: authToken,
		agents:    agents,
	}
}

func (c *GRPCProxyClient) OnAgentCommand(handler func(cmd *pb.AgentCommand) *pb.AgentResponse) {
	c.onCommand = handler
}

func (c *GRPCProxyClient) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := c.connectAndRun(ctx); err != nil {
				slog.Warn("gRPC proxy connect failed, retrying in 5s",
					"core", c.coreAddr, "error", err)
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(5 * time.Second):
				}
				continue
			}
			return nil
		}
	}
}

func (c *GRPCProxyClient) connectAndRun(ctx context.Context) error {
	conn, err := grpc.NewClient(c.coreAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	defer conn.Close()

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	client := pb.NewTunnelServiceClient(conn)
	ctx = metadata.AppendToOutgoingContext(ctx, "x-tunnel-token", c.authToken)
	stream, err := client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("grpc connect stream: %w", err)
	}

	c.mu.Lock()
	c.stream = stream
	c.connected = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.connected = false
		c.stream = nil
		c.mu.Unlock()
	}()

	// send register
	reg := &pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Register{
			Register: &pb.RegisterRequest{
				ProxyId:      c.proxyID,
				Version:      "1.0.0",
				Hostname:     c.hostname,
				Capabilities: []string{"shell", "script", "file", "relay"},
			},
		},
	}
	if err := stream.Send(reg); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	// receive register response
	resp, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive register response: %w", err)
	}

	if regResp, ok := resp.Payload.(*pb.TunnelMessage_RegisterResp); ok {
		if !regResp.RegisterResp.Accepted {
			return fmt.Errorf("registration rejected: %s", regResp.RegisterResp.Error)
		}
		slog.Info("gRPC proxy registered",
			"session_id", regResp.RegisterResp.SessionId,
			"core", c.coreAddr)
	}

	// heartbeat goroutine
	go c.heartbeatLoop(ctx, stream)

	// command receive loop
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			slog.Info("gRPC proxy stream closed")
			return nil
		}
		if err != nil {
			return fmt.Errorf("grpc proxy recv: %w", err)
		}

		c.handleMessage(ctx, msg)
	}
}

func (c *GRPCProxyClient) handleMessage(ctx context.Context, msg *pb.TunnelMessage) {
	switch p := msg.Payload.(type) {
	case *pb.TunnelMessage_Command:
		go c.handleCommand(ctx, msg.MessageId, p.Command)

	case *pb.TunnelMessage_Heartbeat:
		// server heartbeat, ignore

	case *pb.TunnelMessage_ForwardResp:
		// agent response forwarded back
		if ch, ok := c.pending.Load(p.ForwardResp.CommandId); ok {
			ch.(chan *pb.AgentResponse) <- p.ForwardResp
		}
	}
}

func (c *GRPCProxyClient) handleCommand(ctx context.Context, msgID string, cmd *pb.CommandRequest) {
	resp := &pb.TunnelMessage{
		MessageId: msgID,
		Payload: &pb.TunnelMessage_CommandResp{
			CommandResp: &pb.CommandResponse{
				CommandId: cmd.CommandId,
				Success:   false,
				Error:     "agent not connected",
			},
		},
	}

	// route to connected agent
	agent := c.agents.Get(cmd.Target)
	if agent != nil {
		agentResp, err := agent.SendCommand(ctx, cmd.Action, cmd.Payload)
		if err != nil {
			resp.Payload = &pb.TunnelMessage_CommandResp{
				CommandResp: &pb.CommandResponse{
					CommandId: cmd.CommandId,
					Success:   false,
					Error:     err.Error(),
				},
			}
		} else {
			resp.Payload = &pb.TunnelMessage_CommandResp{
				CommandResp: &pb.CommandResponse{
					CommandId: cmd.CommandId,
					Success:   agentResp.Success,
					Output:    agentResp.Output,
					Error:     agentResp.Error,
				},
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stream != nil {
		c.stream.Send(resp)
	}
}

func (c *GRPCProxyClient) heartbeatLoop(ctx context.Context, stream pb.TunnelService_ConnectClient) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hb := &pb.TunnelMessage{
				Payload: &pb.TunnelMessage_Heartbeat{
					Heartbeat: &pb.Heartbeat{
						Timestamp:    time.Now().UnixMilli(),
						ActiveAgents: int32(c.agents.Count()),
					},
				},
			}
			c.mu.Lock()
			err := stream.Send(hb)
			c.mu.Unlock()
			if err != nil {
				slog.Warn("gRPC proxy heartbeat failed", "error", err)
				return
			}
		}
	}
}

func (c *GRPCProxyClient) SendToAgent(ctx context.Context, agentID, action string, payload []byte) (*pb.AgentResponse, error) {
	cmdID := fmt.Sprintf("cmd-%d", time.Now().UnixNano())

	c.mu.Lock()
	stream := c.stream
	c.mu.Unlock()

	if stream == nil {
		return nil, fmt.Errorf("not connected")
	}

	msg := &pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Forward{
			Forward: &pb.AgentCommand{
				CommandId: cmdID,
				AgentId:   agentID,
				Action:    action,
				Payload:   payload,
			},
		},
	}

	ch := make(chan *pb.AgentResponse, 1)
	c.pending.Store(cmdID, ch)
	defer c.pending.Delete(cmdID)

	if err := stream.Send(msg); err != nil {
		return nil, fmt.Errorf("send agent command: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("agent command timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// helper to marshal tunnel message payload
func marshalTunnelMsg(msgType string, payload interface{}) []byte {
	data, _ := json.Marshal(payload)
	return data
}
