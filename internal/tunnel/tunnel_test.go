package tunnel

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// TestMarshalUnmarshalMessage tests message serialization round-trip
func TestMarshalUnmarshalMessage(t *testing.T) {
	payload := RegisterRequest{
		ProxyID:  "proxy-001",
		Version:  "1.0.0",
		Hostname: "node1",
		Token:    "secret",
	}

	data, err := MarshalMessage(MsgRegister, payload, "proxy-001", "trace-123")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	msg, err := UnmarshalMessage(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.Type != MsgRegister {
		t.Errorf("expected type %s, got %s", MsgRegister, msg.Type)
	}
	if msg.ProxyID != "proxy-001" {
		t.Errorf("expected proxy_id proxy-001, got %s", msg.ProxyID)
	}
	if msg.TraceID != "trace-123" {
		t.Errorf("expected trace_id trace-123, got %s", msg.TraceID)
	}

	var reg RegisterRequest
	if err := json.Unmarshal(msg.Payload, &reg); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if reg.ProxyID != "proxy-001" {
		t.Errorf("expected payload proxy_id proxy-001, got %s", reg.ProxyID)
	}
	if reg.Hostname != "node1" {
		t.Errorf("expected hostname node1, got %s", reg.Hostname)
	}
}

// TestConnSendReceive tests TCP connection message framing
func TestConnSendReceive(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	conn := NewConn(server)
	go func() {
		msg := &TunnelMessage{
			MessageID: "msg-1",
			Type:      MsgHeartbeat,
			Payload:   json.RawMessage(`{"timestamp":1234567890}`),
		}
		if err := conn.Send(msg); err != nil {
			t.Errorf("send: %v", err)
		}
	}()

	clientConn := NewConn(client)
	received, err := clientConn.Receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if received.MessageID != "msg-1" {
		t.Errorf("expected message_id msg-1, got %s", received.MessageID)
	}
	if received.Type != MsgHeartbeat {
		t.Errorf("expected type %s, got %s", MsgHeartbeat, received.Type)
	}
}

// TestConnMessageSizeLimit tests the 10MB message size limit
func TestConnMessageSizeLimit(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		// Send oversized payload (simulate by writing raw header with large size)
		header := make([]byte, 4)
		header[0] = 0x01 // 16MB > 10MB limit
		header[1] = 0x00
		header[2] = 0x00
		header[3] = 0x00
		server.Write(header)
	}()

	clientConn := NewConn(client)
	_, err := clientConn.Receive()
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
	if !containsString(err.Error(), "message too large") {
		t.Errorf("expected 'message too large' error, got: %v", err)
	}
}

// TestServerVerifyToken tests authentication logic
func TestServerVerifyToken(t *testing.T) {
	tests := []struct {
		name       string
		authToken  string
		token      string
		insecure   bool
		wantAccept bool
	}{
		{"valid token", "secret", "secret", false, true},
		{"invalid token", "secret", "wrong", false, false},
		{"empty token rejected", "secret", "", false, false},
		{"no auth configured rejected", "", "any", false, false},
		{"insecure mode accepts all", "secret", "wrong", true, true},
		{"insecure mode empty token", "", "", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{authToken: tt.authToken, insecure: tt.insecure}
			got := s.verifyToken(tt.token)
			if got != tt.wantAccept {
				t.Errorf("verifyToken(%q) = %v, want %v", tt.token, got, tt.wantAccept)
			}
		})
	}
}

// TestServerHasProxy tests proxy session lookup
func TestServerHasProxy(t *testing.T) {
	s := NewServer("127.0.0.1:0", "secret", true)

	if s.HasProxy("nonexistent") {
		t.Error("expected false for nonexistent proxy")
	}

	// Simulate registered proxy
	session := &ProxySession{ID: "sess-1", ProxyID: "proxy-1"}
	s.proxies.Store("proxy-1", session)

	if !s.HasProxy("proxy-1") {
		t.Error("expected true for registered proxy")
	}
}

// TestServerClientRegistration tests full registration flow
func TestServerClientRegistration(t *testing.T) {
	// Start server
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()

	srv := NewServer(addr, "test-token", true)
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()
	go func() {
		_ = srv.Start(srvCtx)
	}()
	defer func() {
		if srv.listener != nil {
			srv.listener.Close()
		}
	}()

	// Wait for server ready
	time.Sleep(100 * time.Millisecond)

	// Connect client
	client := NewClient(addr, "proxy-test", "host1", []string{"cmd"}, "test-token")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = client.Start(ctx)
	}()

	// Wait for registration
	time.Sleep(200 * time.Millisecond)

	if !srv.HasProxy("proxy-test") {
		t.Error("proxy not registered")
	}

	cancel()
}

// TestClientSendToAgent tests agent command routing through client
func TestClientSendToAgent(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Setup client connection
	c := &Client{
		conn:      NewConn(client),
		connected: true,
		proxyID:   "proxy-1",
	}

	// Start handleSession to process responses
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = c.handleSession(ctx)
	}()

	// Simulate server responding to agent command
	go func() {
		serverConn := NewConn(server)
		msg, err := serverConn.Receive()
		if err != nil {
			return
		}

		if msg.Type != MsgAgentCommand {
			return
		}

		// Extract command ID from the request
		var cmd AgentCommand
		if err := json.Unmarshal(msg.Payload, &cmd); err != nil {
			return
		}

		// Send response with matching command ID
		resp := AgentResponse{
			CommandID: cmd.CommandID,
			AgentID:   "agent-1",
			Success:   true,
			Output:    []byte("ok"),
		}
		respData, _ := MarshalMessage(MsgAgentResp, resp, "", "")
		serverConn.SendRaw(respData)
	}()

	result, err := c.SendToAgent(ctx, "agent-1", "exec", []byte("ls"))
	if err != nil {
		t.Fatalf("send to agent: %v", err)
	}

	if !result.Success {
		t.Error("expected success")
	}
	if string(result.Output) != "ok" {
		t.Errorf("expected output 'ok', got '%s'", string(result.Output))
	}
}

// TestAgentClientSendMetrics tests metrics reporting
func TestAgentClientSendMetrics(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	agent := &AgentClient{
		conn:      NewConn(client),
		connected: true,
		agentID:   "agent-1",
	}

	// Capture sent message
	go func() {
		serverConn := NewConn(server)
		msg, err := serverConn.Receive()
		if err != nil {
			t.Errorf("server receive: %v", err)
			return
		}

		if msg.Type != MsgMetrics {
			t.Errorf("expected %s, got %s", MsgMetrics, msg.Type)
			return
		}

		var metrics MetricsMessage
		if err := json.Unmarshal(msg.Payload, &metrics); err != nil {
			t.Errorf("unmarshal metrics: %v", err)
			return
		}

		if metrics.TaskID != "task-1" {
			t.Errorf("expected task_id task-1, got %s", metrics.TaskID)
		}
		if len(metrics.Points) != 2 {
			t.Errorf("expected 2 points, got %d", len(metrics.Points))
		}
	}()

	points := []TunnelMetricPoint{
		{Name: "cpu.usage", Value: 0.65, Unit: "percent"},
		{Name: "mem.used", Value: 1024, Unit: "MB"},
	}

	err := agent.SendMetrics("task-1", "monitor", time.Now(), 60, points)
	if err != nil {
		t.Fatalf("send metrics: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
}

// TestAgentClientSendMetricsNotConnected tests error handling when not connected
func TestAgentClientSendMetricsNotConnected(t *testing.T) {
	agent := &AgentClient{
		conn:    nil,
		agentID: "agent-1",
	}

	points := []TunnelMetricPoint{{Name: "test", Value: 1.0}}
	err := agent.SendMetrics("task-1", "monitor", time.Now(), 60, points)
	if err == nil {
		t.Error("expected error when not connected")
	}
	if !containsString(err.Error(), "not connected") {
		t.Errorf("expected 'not connected' error, got: %v", err)
	}
}

// TestClientHeartbeat tests heartbeat sending
func TestClientHeartbeat(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	c := &Client{
		conn:      NewConn(client),
		connected: true,
		proxyID:   "proxy-1",
	}

	// Capture heartbeat
	go func() {
		serverConn := NewConn(server)
		msg, err := serverConn.Receive()
		if err != nil {
			t.Errorf("server receive: %v", err)
			return
		}

		if msg.Type != MsgHeartbeat {
			t.Errorf("expected %s, got %s", MsgHeartbeat, msg.Type)
			return
		}

		var hb Heartbeat
		if err := json.Unmarshal(msg.Payload, &hb); err != nil {
			t.Errorf("unmarshal heartbeat: %v", err)
			return
		}

		if hb.Timestamp == 0 {
			t.Error("expected non-zero timestamp")
		}
	}()

	c.sendHeartbeat()
	time.Sleep(100 * time.Millisecond)
}

// TestAgentClientExecuteCommand tests command execution with action handler
func TestAgentClientExecuteCommand(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	agent := &AgentClient{
		conn:    NewConn(client),
		agentID: "agent-1",
	}

	// Setup action handler
	agent.OnAction(func(action string, payload []byte) ([]byte, error) {
		if action != "test-action" {
			t.Errorf("expected action 'test-action', got '%s'", action)
		}
		return []byte("result"), nil
	})

	// Capture response
	go func() {
		serverConn := NewConn(server)
		msg, err := serverConn.Receive()
		if err != nil {
			t.Errorf("server receive: %v", err)
			return
		}

		if msg.Type != MsgAgentResp {
			t.Errorf("expected %s, got %s", MsgAgentResp, msg.Type)
			return
		}

		var resp AgentResponse
		if err := json.Unmarshal(msg.Payload, &resp); err != nil {
			t.Errorf("unmarshal response: %v", err)
			return
		}

		if !resp.Success {
			t.Error("expected success")
		}
		if string(resp.Output) != "result" {
			t.Errorf("expected output 'result', got '%s'", string(resp.Output))
		}
	}()

	cmd := AgentCommand{
		CommandID: "cmd-1",
		AgentID:   "agent-1",
		Action:    "test-action",
		Payload:   []byte("data"),
	}

	agent.executeCommand(cmd)
	time.Sleep(100 * time.Millisecond)
}

// containsString checks if substr is in s
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
