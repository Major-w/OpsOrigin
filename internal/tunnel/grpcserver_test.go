package tunnel

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/opsorigin/core/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// startTestGRPCServer 启动 insecure gRPC tunnel server(随机端口), 返回地址。
func startTestGRPCServer(t *testing.T, onMetrics func(MetricsReport)) string {
	t.Helper()
	// 探测可用端口后释放, 交给 Start 内部 net.Listen 绑定
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()

	srv := NewGRPCServer(addr, "test-secret", true) // insecure=true 跳过 token
	srv.OnMetrics(onMetrics)
	go func() {
		_ = srv.Start(context.Background())
	}()
	t.Cleanup(func() {
		if srv.grpcServer != nil {
			srv.grpcServer.Stop()
		}
	})

	// 等待服务器就绪(TCP 可连)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("grpc server not ready within timeout")
	return ""
}

func TestGRPCMetricsReport(t *testing.T) {
	received := make(chan MetricsReport, 4)
	addr := startTestGRPCServer(t, func(r MetricsReport) { received <- r })

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewTunnelServiceClient(conn)
	stream, err := client.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// 1) 注册(首消息必须是 register)
	if err := stream.Send(&pb.TunnelMessage{
		MessageId: "m1",
		Payload: &pb.TunnelMessage_Register{
			Register: &pb.RegisterRequest{ProxyId: "agent-001", Hostname: "node1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil { // RegisterResp
		t.Fatal(err)
	}

	// 2) 上报监控指标批次
	batch := &pb.TunnelMessage_Metrics{
		Metrics: &pb.MetricBatch{
			TaskId:     "t-monitor-1",
			TaskType:   "monitor",
			Timestamp:  time.Now().Unix(),
			IntervalSec: 5,
			Points: []*pb.MetricPoint{
				{Name: "cpu.usage", Value: 0.62, Labels: map[string]string{"core": "0"}},
				{Name: "mem.used", Value: 8192, Unit: "MB"},
			},
		},
	}
	if err := stream.Send(&pb.TunnelMessage{MessageId: "m2", Payload: batch}); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-received:
		if r.AgentID != "agent-001" {
			t.Fatalf("expected agent-001, got %s", r.AgentID)
		}
		if r.TaskID != "t-monitor-1" || r.TaskType != "monitor" || r.IntervalSec != 5 {
			t.Fatalf("unexpected report meta: %+v", r)
		}
		if len(r.Points) != 2 {
			t.Fatalf("expected 2 points, got %d", len(r.Points))
		}
		if r.Points[0].Name != "cpu.usage" || r.Points[0].Value != 0.62 || r.Points[0].Labels["core"] != "0" {
			t.Fatalf("unexpected point: %+v", r.Points[0])
		}
		if r.Points[1].Unit != "MB" {
			t.Fatalf("unexpected unit: %+v", r.Points[1])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("onMetrics not called within timeout")
	}
}

func TestGRPCMetricsRequiresRegisterFirst(t *testing.T) {
	addr := startTestGRPCServer(t, nil)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewTunnelServiceClient(conn)
	stream, err := client.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 首消息直接发 metrics → 应被拒绝(服务端要求先 register, 会关闭流)
	if err := stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Metrics{Metrics: &pb.MetricBatch{TaskId: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("expected stream to be closed after non-register first message")
	}
}

func TestGRPCTaskListPushedAfterRegister(t *testing.T) {
	// 注入 taskListProvider: agent-002 返回 1 个自定义任务
	addr := startTestGRPCServerWithProvider(t, func(agentID string) []*pb.TaskDef {
		if agentID != "agent-002" {
			return nil
		}
		return []*pb.TaskDef{{
			TaskId: "custom.disk", Name: "磁盘巡检", Type: "cmdb",
			IntervalSec: 120, Script: "df -h", ScriptType: "shell",
		}}
	})

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewTunnelServiceClient(conn)
	stream, err := client.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Register{Register: &pb.RegisterRequest{ProxyId: "agent-002"}},
	}); err != nil {
		t.Fatal(err)
	}

	// 期待两条消息: RegisterResp 后跟 TaskSync
	foundTaskSync := false
	for i := 0; i < 3; i++ {
		msg, err := stream.Recv()
		if err != nil {
			break
		}
		if ts, ok := msg.Payload.(*pb.TunnelMessage_TaskSync); ok {
			foundTaskSync = true
			if len(ts.TaskSync.Tasks) != 1 || ts.TaskSync.Tasks[0].TaskId != "custom.disk" {
				t.Fatalf("unexpected task list: %+v", ts.TaskSync.Tasks)
			}
			if ts.TaskSync.Tasks[0].Script != "df -h" || ts.TaskSync.Tasks[0].IntervalSec != 120 {
				t.Fatalf("unexpected task def: %+v", ts.TaskSync.Tasks[0])
			}
			break
		}
	}
	if !foundTaskSync {
		t.Fatal("task sync not received after register")
	}
}

// startTestGRPCServerWithProvider 与 startTestGRPCServer 相同, 但注入任务清单 provider。
func startTestGRPCServerWithProvider(t *testing.T, provider func(agentID string) []*pb.TaskDef) string {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()

	srv := NewGRPCServer(addr, "test-secret", true)
	srv.SetTaskListProvider(provider)
	go func() {
		_ = srv.Start(context.Background())
	}()
	t.Cleanup(func() {
		if srv.grpcServer != nil {
			srv.grpcServer.Stop()
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("grpc server not ready within timeout")
	return ""
}

func TestGRPCPushTaskListLive(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()

	srv := NewGRPCServer(addr, "test-secret", true)
	go func() {
		_ = srv.Start(context.Background())
	}()
	t.Cleanup(func() {
		if srv.grpcServer != nil {
			srv.grpcServer.Stop()
		}
	})
	// 等 server 就绪
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := pb.NewTunnelServiceClient(conn)
	stream, err := client.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_Register{Register: &pb.RegisterRequest{ProxyId: "agent-live-1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}

	// 验证在线列表包含该 agent
	ids := srv.OnlineAgentIDs()
	found := false
	for _, id := range ids {
		if id == "agent-live-1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("agent not in online list: %v", ids)
	}

	// 主动推送任务清单
	tasks := []*pb.TaskDef{{
		TaskId: "custom.net", Name: "网络巡检", Type: "cmdb",
		IntervalSec: 300, Script: "ping -c1 1.1.1.1", ScriptType: "shell",
	}}
	if err := srv.PushTaskList("agent-live-1", tasks); err != nil {
		t.Fatalf("push task list: %v", err)
	}

	// Agent 侧收到 TaskSync
	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv task sync: %v", err)
	}
	ts, ok := msg.Payload.(*pb.TunnelMessage_TaskSync)
	if !ok {
		t.Fatalf("expected TaskSync, got %T", msg.Payload)
	}
	if len(ts.TaskSync.Tasks) != 1 || ts.TaskSync.Tasks[0].TaskId != "custom.net" {
		t.Fatalf("unexpected pushed tasks: %+v", ts.TaskSync.Tasks)
	}

	// 推送不存在的 agent 应报错
	if err := srv.PushTaskList("agent-nonexistent", tasks); err == nil {
		t.Fatal("expected error pushing to offline agent")
	}
}
