package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	pb "github.com/opsorigin/core/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type GRPCAgentClient struct {
	coreAddr  string
	agentID   string
	conn      *grpc.ClientConn
	stream    pb.TunnelService_ConnectClient
	mu        sync.Mutex
	connected bool
	onAction  func(action string, payload []byte) ([]byte, error)
	pending   sync.Map
}

func NewGRPCAgentClient(coreAddr, agentID string) *GRPCAgentClient {
	return &GRPCAgentClient{
		coreAddr: coreAddr,
		agentID:  agentID,
	}
}

func (c *GRPCAgentClient) OnAction(handler func(action string, payload []byte) ([]byte, error)) {
	c.onAction = handler
}

func (c *GRPCAgentClient) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := c.connectAndRun(ctx); err != nil {
				slog.Warn("gRPC agent connect failed, retrying in 5s",
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

func (c *GRPCAgentClient) connectAndRun(ctx context.Context) error {
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
	hostname, _ := os.Hostname()
	reg := &pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Register{
			Register: &pb.RegisterRequest{
				ProxyId:      c.agentID,
				Version:      "1.0.0",
				Hostname:     hostname,
				Capabilities: []string{"shell", "script", "monitor"},
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
		slog.Info("gRPC agent registered",
			"session_id", regResp.RegisterResp.SessionId,
			"core", c.coreAddr)
	}

	// heartbeat goroutine
	go c.heartbeatLoop(ctx, stream)

	// command receive loop
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			slog.Info("gRPC stream closed")
			return nil
		}
		if err != nil {
			return fmt.Errorf("grpc recv: %w", err)
		}

		c.handleMessage(ctx, msg)
	}
}

func (c *GRPCAgentClient) handleMessage(ctx context.Context, msg *pb.TunnelMessage) {
	switch p := msg.Payload.(type) {
	case *pb.TunnelMessage_Command:
		go c.handleCommand(ctx, msg.MessageId, p.Command)

	case *pb.TunnelMessage_Forward:
		go c.handleForward(ctx, msg.MessageId, p.Forward)

	case *pb.TunnelMessage_Heartbeat:
		// server heartbeat, ignore
	}
}

func (c *GRPCAgentClient) handleCommand(ctx context.Context, msgID string, cmd *pb.CommandRequest) {
	resp := &pb.TunnelMessage{
		MessageId: msgID,
		Payload: &pb.TunnelMessage_CommandResp{
			CommandResp: &pb.CommandResponse{
				CommandId: cmd.CommandId,
				Success:   false,
				Error:     "no handler",
			},
		},
	}

	if c.onAction != nil {
		output, err := c.onAction(cmd.Action, cmd.Payload)
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
					Success:   true,
					Output:    output,
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

func (c *GRPCAgentClient) handleForward(ctx context.Context, msgID string, fwd *pb.AgentCommand) {
	resp := &pb.TunnelMessage{
		MessageId: msgID,
		Payload: &pb.TunnelMessage_ForwardResp{
			ForwardResp: &pb.AgentResponse{
				CommandId: fwd.CommandId,
				AgentId:   fwd.AgentId,
				Success:   false,
				Error:     "no handler",
			},
		},
	}

	if c.onAction != nil {
		output, err := c.onAction(fwd.Action, fwd.Payload)
		if err != nil {
			resp.Payload = &pb.TunnelMessage_ForwardResp{
				ForwardResp: &pb.AgentResponse{
					CommandId: fwd.CommandId,
					AgentId:   fwd.AgentId,
					Success:   false,
					Error:     err.Error(),
				},
			}
		} else {
			resp.Payload = &pb.TunnelMessage_ForwardResp{
				ForwardResp: &pb.AgentResponse{
					CommandId: fwd.CommandId,
					AgentId:   fwd.AgentId,
					Success:   true,
					Output:    output,
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

func (c *GRPCAgentClient) heartbeatLoop(ctx context.Context, stream pb.TunnelService_ConnectClient) {
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
						Timestamp:   time.Now().UnixMilli(),
						ActiveAgents: 1,
					},
				},
			}
			c.mu.Lock()
			err := stream.Send(hb)
			c.mu.Unlock()
			if err != nil {
				slog.Warn("gRPC heartbeat send failed", "error", err)
				return
			}
		}
	}
}

func (c *GRPCAgentClient) SendTaskResult(ctx context.Context, taskID string, result interface{}) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stream == nil {
		return fmt.Errorf("not connected")
	}

	return c.stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_ForwardResp{
			ForwardResp: &pb.AgentResponse{
				CommandId: taskID,
				AgentId:   c.agentID,
				Success:   true,
				Output:    data,
			},
		},
	})
}
