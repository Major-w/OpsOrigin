package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"

	pb "github.com/opsorigin/core/api/proto"
	"github.com/opsorigin/core/internal/monitor/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type GRPCServer struct {
	pb.UnimplementedTunnelServiceServer
	addr       string
	authToken  string
	insecure   bool
	grpcServer *grpc.Server
	proxies    sync.Map
	sessions   sync.Map
	onCommand  func(ctx context.Context, proxyID string, cmd *CommandRequest) *CommandResponse

	// pending 关联 Core→Agent 命令的回包:CommandID -> chan。
	pending sync.Map

	// tlsConfig 非 nil 时启用 mTLS 双向认证(§3.1 / §6)。
	tlsConfig *tls.Config
}

type GRPCProxySession struct {
	ID           string
	ProxyID      string
	CertCN       string // 客户端证书 CN(§3.1 从证书解析 Agent/Proxy 身份)
	Stream       grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]
	Hostname     string
	Capabilities []string
	ConnectedAt  time.Time
	mu           sync.Mutex
}

func NewGRPCServer(addr, authToken string, insecure bool) *GRPCServer {
	return &GRPCServer{addr: addr, authToken: authToken, insecure: insecure}
}

// EnableMTLS 开启双向 TLS(§3.1)。自动强制 ClientAuth=RequireAndVerifyClientCert,
// 并锁定 TLS 1.3(§6 传输加密要求)。cfg.Certificates 为 Core 服务端证书。
func (s *GRPCServer) EnableMTLS(cfg *tls.Config) {
	if cfg.ClientAuth == tls.NoClientCert {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	if cfg.MinVersion == 0 {
		cfg.MinVersion = tls.VersionTLS13
	}
	s.tlsConfig = cfg
}

func (s *GRPCServer) OnCommand(handler func(ctx context.Context, proxyID string, cmd *CommandRequest) *CommandResponse) {
	s.onCommand = handler
}

func (s *GRPCServer) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("grpc listen on %s: %w", s.addr, err)
	}

	opts := []grpc.ServerOption{}
	if s.tlsConfig != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(s.tlsConfig)))
	}
	s.grpcServer = grpc.NewServer(opts...)
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
	if !s.verifyToken(stream.Context()) {
		slog.Warn("proxy registration rejected: invalid or missing auth token / client certificate")
		return status.Errorf(codes.Unauthenticated, "authentication required")
	}
	msg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive register: %w", err)
	}

	reg := &pb.RegisterRequest{}
	if err := extractPayload(msg, reg); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid register: %v", err)
	}

	// §3.1 从客户端证书 CN 解析身份(若启用 mTLS)。
	cn := clientCertCN(stream.Context())

	sessionID := fmt.Sprintf("grpc-%s", uuid.New().String())
	session := &GRPCProxySession{
		ID:           sessionID,
		ProxyID:      reg.ProxyId,
		CertCN:       cn,
		Stream:       stream,
		Hostname:     reg.Hostname,
		Capabilities: reg.Capabilities,
		ConnectedAt:  time.Now(),
	}

	s.proxies.Store(reg.ProxyId, session)
	s.sessions.Store(sessionID, session)

	// T13: gRPC 连接指标打点
	metrics.AgentmgrGrpcConnections.WithLabelValues(reg.ProxyId, "default").Inc()

	slog.Info("proxy registered via gRPC",
		"proxy_id", reg.ProxyId,
		"cert_cn", cn,
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

	// T13: gRPC 连接断开打点
	metrics.AgentmgrGrpcConnections.WithLabelValues(reg.ProxyId, "default").Dec()

	slog.Info("proxy disconnected via gRPC", "proxy_id", reg.ProxyId)
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

		if ch, ok := s.pending.Load(p.CommandResp.CommandId); ok {
			if c, ok := ch.(chan *CommandResponse); ok {
				c <- &CommandResponse{
					CommandID: p.CommandResp.CommandId,
					Success:   p.CommandResp.Success,
					Output:    p.CommandResp.Output,
					Error:     p.CommandResp.Error,
				}
			}
		}

	case *pb.TunnelMessage_ForwardResp:
		slog.Info("gRPC agent response",
			"command_id", p.ForwardResp.CommandId,
			"agent_id", p.ForwardResp.AgentId,
			"success", p.ForwardResp.Success)

		if ch, ok := s.pending.Load(p.ForwardResp.CommandId); ok {
			if c, ok := ch.(chan *CommandResponse); ok {
				c <- &CommandResponse{
					CommandID: p.ForwardResp.CommandId,
					Success:   p.ForwardResp.Success,
					Output:    p.ForwardResp.Output,
					Error:     p.ForwardResp.Error,
				}
			}
		}
	}
}

// SendCommand 经 gRPC 流下发命令到目标 Agent/Proxy,并**阻塞等待其回包**(与 TCP Server 一致)。
// 之前此处发完即返回 Success:true,导致 deploy/script/vuln/k8s 等经 gRPC 直连 Agent 的执行
// 永远拿不到真实输出。现通过 pending 关联 CommandID,在 handleMessage 收到回包时回填。
func (s *GRPCServer) SendCommand(ctx context.Context, proxyID string, cmd *CommandRequest) (*CommandResponse, error) {
	val, ok := s.proxies.Load(proxyID)
	if !ok {
		return nil, fmt.Errorf("proxy %s not connected", proxyID)
	}
	session := val.(*GRPCProxySession)

	ch := make(chan *CommandResponse, 1)
	s.pending.Store(cmd.CommandID, ch)
	defer s.pending.Delete(cmd.CommandID)

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

	wait := time.Duration(cmd.TimeoutSecond) * time.Second
	if wait <= 0 {
		wait = 5 * time.Second
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(wait):
		return nil, fmt.Errorf("command %s timed out waiting for agent response", cmd.CommandID)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// HasProxy 返回指定 proxy/agent 会话是否在 gRPC 隧道上已注册(供 agentmgr 选路)。
func (s *GRPCServer) HasProxy(id string) bool {
	_, ok := s.proxies.Load(id)
	return ok
}

func extractPayload(msg *pb.TunnelMessage, out interface{}) error {
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// verifyToken 校验 gRPC 流身份。优先级:
//   1) mTLS 已启用且客户端证书有效(CN 非空) => 直接通过(证书即凭证,§3.1)。
//   2) Insecure 模式 => 通过(仅本地测试)。
//   3) 共享密钥(metadata: x-tunnel-token)匹配 => 通过。
// 安全默认(fail-closed): 未配置密钥且非 mTLS/Insecure 时拒绝所有注册。
func (s *GRPCServer) verifyToken(ctx context.Context) bool {
	if s.tlsConfig != nil {
		// mTLS:客户端证书已由 TLS 层校验,此处仅需 CN 非空即可确认身份。
		return clientCertCN(ctx) != ""
	}
	if s.insecure {
		return true
	}
	if s.authToken == "" {
		slog.Error("grpc tunnel auth: AUTH_SECRET not configured - refusing proxy registration")
		return false
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	tokens := md.Get("x-tunnel-token")
	if len(tokens) == 0 {
		return false
	}
	return tokens[0] == s.authToken
}

// clientCertCN 从 gRPC peer 证书提取客户端 CN(§3.1 身份解析)。
// 无证书或解析失败返回空串。
func clientCertCN(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return ""
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return ""
	}
	return tlsInfo.State.PeerCertificates[0].Subject.CommonName
}
