package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	pb "github.com/opsorigin/core/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

	// tlsConfig 非 nil 时,以 mTLS 双向认证连接 Core(§3.1 / §6)。
	tlsConfig *tls.Config
}

func NewGRPCAgentClient(coreAddr, agentID string) *GRPCAgentClient {
	return &GRPCAgentClient{
		coreAddr: coreAddr,
		agentID:  agentID,
	}
}

// WithMTLS 配置客户端证书以启用双向 TLS(§3.1)。
// certFile/keyFile 为 Agent 自身证书;caFile 为签发 Core 服务端证书的 CA(用于校验对端)。
// 失败返回 error,由调用方决定是否退出(未配置证书时不应静默放行)。
func (c *GRPCAgentClient) WithMTLS(certFile, keyFile, caFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load agent client cert: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read core CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("parse core CA: no certificates found in %s", caFile)
	}
	c.tlsConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13, // §6 TLS 1.3
	}
	return nil
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
	creds := grpc.WithTransportCredentials(insecure.NewCredentials())
	if c.tlsConfig != nil {
		creds = grpc.WithTransportCredentials(credentials.NewTLS(c.tlsConfig))
	}
	conn, err := grpc.NewClient(c.coreAddr, creds)
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
