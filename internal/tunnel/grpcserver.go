package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	pb "github.com/opsorigin/core/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCServer struct {
	pb.UnimplementedTunnelServiceServer
	addr       string
	grpcServer *grpc.Server
	proxies    sync.Map
	sessions   sync.Map
	onCommand  func(ctx context.Context, proxyID string, cmd *CommandRequest) *CommandResponse
}

type GRPCProxySession struct {
	ID           string
	ProxyID      string
	Stream       grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]
	Hostname     string
	Capabilities []string
	ConnectedAt  time.Time
	mu           sync.Mutex
}

func NewGRPCServer(addr string) *GRPCServer {
	return &GRPCServer{addr: addr}
}

func (s *GRPCServer) OnCommand(handler func(ctx context.Context, proxyID string, cmd *CommandRequest) *CommandResponse) {
	s.onCommand = handler
}

func (s *GRPCServer) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("grpc listen on %s: %w", s.addr, err)
	}

	s.grpcServer = grpc.NewServer()
	pb.RegisterTunnelServiceServer(s.grpcServer, s)

	go func() {
		<-ctx.Done()
		s.grpcServer.GracefulStop()
	}()

	slog.Info("gRPC tunnel server started", "addr", s.addr)

	if err := s.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

func (s *GRPCServer) Connect(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error {
	msg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive register: %w", err)
	}

	reg := &pb.RegisterRequest{}
	if err := extractPayload(msg, reg); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid register: %v", err)
	}

	sessionID := fmt.Sprintf("grpc-%s", time.Now().Format("20060102150405"))
	session := &GRPCProxySession{
		ID:           sessionID,
		ProxyID:      reg.ProxyId,
		Stream:       stream,
		Hostname:     reg.Hostname,
		Capabilities: reg.Capabilities,
		ConnectedAt:  time.Now(),
	}

	s.proxies.Store(reg.ProxyId, session)
	s.sessions.Store(sessionID, session)

	slog.Info("proxy registered via gRPC",
		"proxy_id", reg.ProxyId,
		"hostname", reg.Hostname,
		"session_id", sessionID)

	// send register response
	resp := &pb.TunnelMessage{
		MessageId: msg.MessageId,
		TraceId:   msg.TraceId,
		Payload: &pb.TunnelMessage_RegisterResp{
			RegisterResp: &pb.RegisterResponse{
				Accepted:  true,
				SessionId: sessionID,
			},
		},
	}
	if err := stream.Send(resp); err != nil {
		return fmt.Errorf("send register response: %w", err)
	}

	// read loop
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			slog.Info("proxy stream closed", "proxy_id", reg.ProxyId)
			break
		}
		if err != nil {
			slog.Warn("proxy stream error", "proxy_id", reg.ProxyId, "error", err)
			break
		}

		s.handleMessage(session, msg)
	}

	s.proxies.Delete(reg.ProxyId)
	s.sessions.Delete(sessionID)
	slog.Info("proxy disconnected via gRPC", "proxy_id", reg.ProxyId)
	return nil
}

func (s *GRPCServer) handleMessage(session *GRPCProxySession, msg *pb.TunnelMessage) {
	switch p := msg.Payload.(type) {
	case *pb.TunnelMessage_Heartbeat:
		slog.Debug("gRPC heartbeat from proxy",
			"proxy_id", session.ProxyID,
			"active_agents", p.Heartbeat.ActiveAgents)

	case *pb.TunnelMessage_CommandResp:
		slog.Info("gRPC command response",
			"command_id", p.CommandResp.CommandId,
			"success", p.CommandResp.Success)

	case *pb.TunnelMessage_ForwardResp:
		slog.Info("gRPC agent response",
			"command_id", p.ForwardResp.CommandId,
			"agent_id", p.ForwardResp.AgentId,
			"success", p.ForwardResp.Success)
	}
}

func (s *GRPCServer) SendCommand(ctx context.Context, proxyID string, cmd *CommandRequest) (*CommandResponse, error) {
	val, ok := s.proxies.Load(proxyID)
	if !ok {
		return nil, fmt.Errorf("proxy %s not connected", proxyID)
	}
	session := val.(*GRPCProxySession)

	msg := &pb.TunnelMessage{
		MessageId: cmd.CommandID,
		Payload: &pb.TunnelMessage_Command{
			Command: &pb.CommandRequest{
				CommandId:      cmd.CommandID,
				Target:         cmd.TargetID,
				Action:         cmd.Action,
				Payload:        cmd.Payload,
				TimeoutSeconds: cmd.TimeoutSecond,
			},
		},
	}

	session.mu.Lock()
	err := session.Stream.Send(msg)
	session.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("send command: %w", err)
	}

	return &CommandResponse{
		CommandID: cmd.CommandID,
		Success:   true,
	}, nil
}

func extractPayload(msg *pb.TunnelMessage, out interface{}) error {
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
