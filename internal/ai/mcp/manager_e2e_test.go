package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestManagerE2E 端到端验证:用 mark3labs 的 server 包起一个内存 MCP server
// (Streamable HTTP 传输),再经本包 Manager 把其工具 proxy 进 Registry,
// 最后通过 Registry.Execute 真实调用验证链路打通。
func TestManagerE2E(t *testing.T) {
	// 1) 构造远端 MCP server,暴露一个 echo 工具。
	srv := server.NewMCPServer("demo-server", "1.0.0")
	srv.AddTool(mcp.Tool{
		Name:        "echo",
		Description: "echo back the given message",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"msg": map[string]any{"type": "string", "description": "message to echo"},
			},
			Required: []string{"msg"},
		},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msg := req.GetString("msg", "")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "echo:" + msg},
			},
		}, nil
	})

	// 2) 用 httptest 把 server 暴露为 Streamable HTTP 端点。
	sh := server.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(http.HandlerFunc(sh.ServeHTTP))
	defer ts.Close()

	// 3) 创建本地 Registry 与 Manager,接入该 http 传输的 server。
	reg := NewRegistry()
	mgr := NewManager(reg)
	if err := mgr.Start(context.Background(), []ServerConfig{
		{Name: "demo", Transport: TransportHTTP, URL: ts.URL},
	}); err != nil {
		t.Fatalf("manager start: %v", err)
	}
	defer mgr.Stop()

	// 4) 轮询等待工具被 proxy 进 Registry(监督 goroutine 异步完成连接+列工具)。
	toolName := "mcp_demo_echo"
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, ok := reg.Get(toolName); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for proxied tool %q; status=%+v", toolName, mgr.Status())
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 5) 通过 Registry 真实调用远端工具。
	out, err := reg.Execute(context.Background(), toolName, map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("execute proxied tool: %v", err)
	}
	text, ok := out.(string)
	if !ok || !strings.Contains(text, "echo:hello") {
		t.Fatalf("unexpected tool result: %#v", out)
	}

	// 6) 校验参数 schema 已正确转换(必填字段 msg 应标记为 Required)。
	td, ok := reg.Get(toolName)
	if !ok {
		t.Fatalf("tool %q missing after execute", toolName)
	}
	msgSchema, ok := td.Parameters["msg"]
	if !ok {
		t.Fatalf("parameter msg missing in proxied schema: %+v", td.Parameters)
	}
	if !msgSchema.Required {
		t.Fatalf("expected msg to be required, got %+v", td.Parameters)
	}
	if msgSchema.Type != "string" {
		t.Fatalf("expected msg type string, got %q", msgSchema.Type)
	}

	// 7) 状态应显示已连接且工具数为 1。
	st := mgr.Status()
	if len(st) != 1 {
		t.Fatalf("expected 1 server status, got %d: %+v", len(st), st)
	}
	if !st[0].Connected || st[0].ToolCount != 1 {
		t.Fatalf("unexpected server status: %+v", st[0])
	}
}
