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

	// onHeartbeat 在会话注册成功及每次收到隧道心跳时触发,
	// 供 agentmgr 回写在线状态与系统信息(Redis/DB)。直连 Agent 场景 proxyID 即 agentID。
	onHeartbeat func(info AgentHeartbeatInfo)

	// onDisconnect 在会话断开时触发,供 agentmgr 立即标记离线。
	onDisconnect func(agentID string)

	// resolveAgentID 在 gRPC 直连 Agent 注册时,将入站 ID 解析为应复用的规范记录 ID,
	// 同时执行 boot_id 碰撞检测(同一 host_id 在不同 boot_id 上报时判定为克隆冲突)。
	// 返回(规范 host_id, 是否需要 RESET_REGISTRATION)。为 nil 时(未注入)直接采用入站 ID。
	resolveAgentID func(ctx context.Context, tenantID, incomingID, hostname string, ips []string, bootID string, forceNew bool) (string, bool)

	// onMetrics 在收到 Agent 指标批量上报(MetricBatch)时触发,
	// 供上层(agentmgr/ingest)做批量缓冲 + 按任务类型分流存储(PG/VM)。
	onMetrics func(report MetricsReport)

	// taskListProvider 在 Agent 注册成功后调用, 返回该 Agent 应执行的任务清单(TaskDef)。
	// 由 agentmgr 注入(基于 collect_tasks 过滤 target_agents)。为 nil 时不下发。
	taskListProvider func(agentID string) []*pb.TaskDef
}

// MetricPoint 一个被上报的指标点(内部表示, 与 pb 层解耦)。
type MetricPoint struct {
	Name   string
	Value  float64
	Unit   string
	Labels map[string]string
}

// MetricsReport 一次指标批量上报。TaskType 供分流: cmdb(低频→PG) | monitor(高频→VM)。
type MetricsReport struct {
	AgentID     string
	TaskID      string
	TaskType    string
	Timestamp   time.Time
	IntervalSec int64
	Points      []MetricPoint
}

// AgentHeartbeatInfo 携带 Agent 注册/心跳时的身份与系统信息,
// 由 gRPC 隧道上报,供 agentmgr 回写 agent_hosts(在线状态 + OS/IP/CPU/Memory)。
type AgentHeartbeatInfo struct {
	AgentID      string
	Hostname     string
	OS           string
	OSVersion    string
	Version      string
	CPU          string
	Memory       string
	IPs          []string
	Capabilities []string
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

// OnHeartbeat 注册心跳回调:会话注册成功时和每次收到隧道心跳时触发。
func (s *GRPCServer) OnHeartbeat(handler func(info AgentHeartbeatInfo)) {
	s.onHeartbeat = handler
}

// OnDisconnect 注册断连回调:会话结束(流关闭/出错)时触发。
func (s *GRPCServer) OnDisconnect(handler func(agentID string)) {
	s.onDisconnect = handler
}

// OnMetrics 注册指标上报回调:收到 Agent 的 MetricBatch 时触发。
// 由上层(agentmgr/ingest)接入批量缓冲与存储分流, 不阻塞 gRPC 读循环。
func (s *GRPCServer) OnMetrics(handler func(report MetricsReport)) {
	s.onMetrics = handler
}

// SetTaskListProvider 注入任务清单构造器: Agent 注册成功后下发任务清单(TaskDef 列表)。
func (s *GRPCServer) SetTaskListProvider(fn func(agentID string) []*pb.TaskDef) {
	s.taskListProvider = fn
}

// PushTaskList 主动向指定在线 Agent 推送最新任务清单(TaskSync)。
// 供上层(agentmgr)在 collect_tasks 变更时增量同步, 不依赖 Agent 重连。
// Agent 不在线或推送失败时返回 error(调用方决定是否日志告警)。
func (s *GRPCServer) PushTaskList(agentID string, tasks []*pb.TaskDef) error {
	v, ok := s.proxies.Load(agentID)
	if !ok {
		return fmt.Errorf("agent %s not connected", agentID)
	}
	sess, ok := v.(*GRPCProxySession)
	if !ok || sess.Stream == nil {
		return fmt.Errorf("agent %s session invalid", agentID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.Stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_TaskSync{
			TaskSync: &pb.TaskList{Tasks: tasks},
		},
	})
}

// OnlineAgentIDs 返回当前在线(已建立 gRPC 会话)的 Agent ID 列表。
// 供上层在全局任务变更时对所有在线 Agent 推送。
func (s *GRPCServer) OnlineAgentIDs() []string {
	ids := make([]string, 0, 16)
	s.proxies.Range(func(k, _ any) bool {
		ids = append(ids, k.(string))
		return true
	})
	return ids
}

// SetAgentIDResolver 注入指纹去重解析器(由 agentmgr 在 SetGRPCTunnel 时调用)。
// 入站 gRPC Agent 注册时,据此将入站 ID 解析为应复用的规范记录 ID,实现跨重启/跨通道去重。
func (s *GRPCServer) SetAgentIDResolver(fn func(ctx context.Context, tenantID, incomingID, hostname string, ips []string, bootID string, forceNew bool) (string, bool)) {
	s.resolveAgentID = fn
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

	// 直接从 oneof 断言取注册包。此前用 extractPayload(json 序列化 oneof wrapper)
	// 会得到 {"Register":{...}} 外层结构,反序列化到 RegisterRequest 全为零值,
	// 导致会话以空 proxy_id 注册、HasProxy(agentID) 永远 false。
	regPayload, ok := msg.Payload.(*pb.TunnelMessage_Register)
	if !ok || regPayload.Register == nil {
		return status.Errorf(codes.InvalidArgument, "first message must be register")
	}
	reg := regPayload.Register
	if reg.ProxyId == "" {
		return status.Errorf(codes.InvalidArgument, "register missing proxy_id")
	}

	// 指纹去重 + boot_id 克隆冲突检测(Phase 2):
	// 将入站 ID 解析为应复用的规范记录 ID;若同一 host_id 在短时间内来自不同 boot_id
	// → 克隆冲突,要求 Agent 执行 RESET_REGISTRATION(追加随机盐后以新身份重注册)。
	agentID := reg.ProxyId
	var resetFlag bool
	if s.resolveAgentID != nil {
		agentID, resetFlag = s.resolveAgentID(stream.Context(), "default",
			reg.ProxyId, reg.Hostname, reg.Ips, reg.BootId, reg.ForceNew)
	}

	// §3.1 从客户端证书 CN 解析身份(若启用 mTLS)。
	cn := clientCertCN(stream.Context())

	sessionID := fmt.Sprintf("grpc-%s", uuid.New().String())

	// 先发送 RegisterResp,再决定是否建立会话:克隆冲突时在应答中置 Reset=true、
	// 通知 Agent 旋转身份,随后直接关闭流(不存储 session、不记录心跳),Agent 收到后
	// 追加随机盐重新计算指纹,以全新 host_id 重注册。
	regResp := &pb.RegisterResponse{
		Accepted:    !resetFlag,
		SessionId:   sessionID,
		Reset_:      resetFlag,
		ResetReason: "clone_collision",
	}
	if resetFlag {
		regResp.Error = "boot_id mismatch: clone detected, please re-register with a new identity"
	}
	if err := stream.Send(&pb.TunnelMessage{
		MessageId: msg.MessageId,
		TraceId:   msg.TraceId,
		Payload:   &pb.TunnelMessage_RegisterResp{RegisterResp: regResp},
	}); err != nil {
		return fmt.Errorf("send register response: %w", err)
	}
	if resetFlag {
		slog.Warn("grpc registration reset due to clone collision",
			"proxy_id", reg.ProxyId, "agent_id", agentID, "hostname", reg.Hostname)
		return nil // Agent 将旋转身份后重连,不在此建立会话
	}

	session := &GRPCProxySession{
		ID:           sessionID,
		ProxyID:      agentID,
		CertCN:       cn,
		Stream:       stream,
		Hostname:     reg.Hostname,
		Capabilities: reg.Capabilities,
		ConnectedAt:  time.Now(),
	}

	s.proxies.Store(agentID, session)
	s.sessions.Store(sessionID, session)

	// T13: gRPC 连接指标打点
	metrics.AgentmgrGrpcConnections.WithLabelValues(agentID, "default").Inc()

	slog.Info("proxy registered via gRPC",
		"proxy_id", reg.ProxyId,
		"agent_id", agentID,
		"cert_cn", cn,
		"hostname", reg.Hostname,
		"session_id", sessionID)

	// 注册即视为一次心跳:立刻回写在线状态与系统信息,避免等 30s 首个隧道心跳。
	if s.onHeartbeat != nil {
		s.onHeartbeat(AgentHeartbeatInfo{
			AgentID:      agentID,
			Hostname:     reg.Hostname,
			OS:           reg.Os,
			OSVersion:    reg.OsVersion,
			Version:      reg.Version,
			CPU:          reg.Cpu,
			Memory:       reg.Memory,
			IPs:          reg.Ips,
			Capabilities: reg.Capabilities,
		})
	}

	// 注册成功后下发任务清单(自定义/内置任务定义, Agent 本地调度执行)。
	if s.taskListProvider != nil {
		if tasks := s.taskListProvider(agentID); len(tasks) > 0 {
			if err := stream.Send(&pb.TunnelMessage{
				Payload: &pb.TunnelMessage_TaskSync{
					TaskSync: &pb.TaskList{Tasks: tasks},
				},
			}); err != nil {
				slog.Warn("grpc send task list failed", "agent_id", agentID, "error", err)
			} else {
				slog.Info("grpc task list pushed", "agent_id", agentID, "tasks", len(tasks))
			}
		}
	}

	// read loop
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			slog.Info("proxy stream closed", "agent_id", agentID)
			break
		}
		if err != nil {
			slog.Warn("proxy stream error", "agent_id", agentID, "error", err)
			break
		}

		s.handleMessage(session, msg)
	}

	s.proxies.Delete(agentID)
	s.sessions.Delete(sessionID)

	// T13: gRPC 连接断开打点
	metrics.AgentmgrGrpcConnections.WithLabelValues(agentID, "default").Dec()

	// 会话断开:通知上层立即标记离线。
	if s.onDisconnect != nil {
		s.onDisconnect(agentID)
	}

	slog.Info("proxy disconnected via gRPC", "agent_id", agentID)
	return nil
}

func (s *GRPCServer) handleMessage(session *GRPCProxySession, msg *pb.TunnelMessage) {
	switch p := msg.Payload.(type) {
	case *pb.TunnelMessage_Heartbeat:
		slog.Debug("gRPC heartbeat from proxy",
			"proxy_id", session.ProxyID,
			"active_agents", p.Heartbeat.ActiveAgents)
		// 回写在线状态(Redis TTL 续期),否则 UI 列表永远显示 offline。
		// 仅带身份字段;OS/IP 等系统信息已在注册时上报,此处不覆盖。
		if s.onHeartbeat != nil {
			s.onHeartbeat(AgentHeartbeatInfo{AgentID: session.ProxyID, Hostname: session.Hostname})
		}

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

	case *pb.TunnelMessage_Metrics:
		// 监控/CMDB 指标批量上报: 转内部 MetricsReport 交给上层缓冲与分流。
		if s.onMetrics == nil || p.Metrics == nil {
			return
		}
		mb := p.Metrics
		pts := make([]MetricPoint, 0, len(mb.Points))
		for _, pt := range mb.Points {
			pts = append(pts, MetricPoint{
				Name:   pt.Name,
				Value:  pt.Value,
				Unit:   pt.Unit,
				Labels: pt.Labels,
			})
		}
		ts := time.Now()
		if mb.Timestamp > 0 {
			ts = time.Unix(mb.Timestamp, 0)
		}
		s.onMetrics(MetricsReport{
			AgentID:     session.ProxyID,
			TaskID:      mb.TaskId,
			TaskType:    mb.TaskType,
			Timestamp:   ts,
			IntervalSec: mb.IntervalSec,
			Points:      pts,
		})
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
