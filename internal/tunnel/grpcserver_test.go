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
