package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/tunnel"
)

// TestAgentRegistryMetricsRelay 验证: Agent 经 TCP 隧道上报 MsgMetrics,
// AgentRegistry 读循环收到后触发 onMetrics 回调(纯转发路径)。
func TestAgentRegistryMetricsRelay(t *testing.T) {
	// 起一个 AgentRegistry 监听
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	lis.Close()

	reg := NewAgentRegistry()
	got := make(chan *tunnel.MetricsMessage, 1)
	reg.OnMetrics(func(agentID string, msg *tunnel.MetricsMessage) {
		if agentID == "" {
			t.Error("agent id empty")
		}
		got <- msg
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = reg.Serve(ctx, addr)
	}()

	// 等监听就绪
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 模拟 Agent 连接并上报
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ac := tunnel.NewConn(conn)

	msgData, err := tunnel.MarshalMessage(tunnel.MsgMetrics, tunnel.MetricsMessage{
		TaskID:      "builtin.monitor.basic",
		TaskType:    "monitor",
		Timestamp:   time.Now().UnixMilli(),
		IntervalSec: 5,
		Points: []tunnel.TunnelMetricPoint{
			{Name: "cpu.usage", Value: 12.5, Unit: "%", Labels: map[string]string{"host": "h1"}},
			{Name: "mem.percent", Value: 45.2, Unit: "%"},
		},
	}, "agent-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ac.SendRaw(msgData); err != nil {
		t.Fatal(err)
	}

	select {
	case mm := <-got:
		if mm.TaskID != "builtin.monitor.basic" || mm.TaskType != "monitor" {
			t.Fatalf("unexpected metrics message: %+v", mm)
		}
		if len(mm.Points) != 2 || mm.Points[0].Name != "cpu.usage" || mm.Points[0].Value != 12.5 {
			t.Fatalf("unexpected points: %+v", mm.Points)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metrics not relayed within timeout")
	}
}
