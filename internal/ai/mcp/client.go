package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcptypes "github.com/mark3labs/mcp-go/mcp"
)

// ExternalClient 封装与一个外部 MCP server 的连接生命周期。
//
// 设计要点:
//   - 懒连接:首次 ListTools/CallTool 时才建连(ensureConnected),避免无谓启动子进程。
//   - 自愈:连接级错误会将 cli 置 nil,下次调用自动重连;Manager 另有监督 goroutine 做主动健康探测。
//   - 线程安全:所有对 cli 的访问都经 mu 保护,因为 Manager 监督 goroutine 与引擎调用路径会并发使用。
type ExternalClient struct {
	cfg ServerConfig

	mu  sync.Mutex
	cli *client.Client
}

// connect 建立传输层并完成 MCP 握手(Initialize)。
// 调用方需持有 mu;本方法不会重复连接(已连接则直接返回)。
func (e *ExternalClient) connect(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cli != nil {
		return nil
	}

	var (
		c   *client.Client
		err error
	)
	switch e.cfg.Transport {
	case "", TransportStdio:
		env := os.Environ()
		for k, v := range e.cfg.Env {
			env = append(env, k+"="+v)
		}
		// NewStdioMCPClient 会自行启动子进程(stdio 不需要再调 Start)。
		c, err = client.NewStdioMCPClient(e.cfg.Command, env, e.cfg.Args...)
	case TransportSSE:
		c, err = client.NewSSEMCPClient(e.cfg.URL, transport.WithHeaders(e.cfg.Headers))
	case TransportHTTP:
		c, err = client.NewStreamableHttpClient(e.cfg.URL, transport.WithHTTPHeaders(e.cfg.Headers))
	default:
		return fmt.Errorf("unsupported transport %q for server %s", e.cfg.Transport, e.cfg.Name)
	}
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	// sse / http 传输构造器不会自动启动,需要显式 Start;stdio 已自动启动。
	if e.cfg.Transport != TransportStdio {
		if err = c.Start(ctx); err != nil {
			_ = c.Close()
			return fmt.Errorf("start transport: %w", err)
		}
	}

	initReq := mcptypes.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcptypes.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcptypes.Implementation{
		Name:    "opsorigin-ai",
		Version: "1.0.0",
	}
	if _, err = c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return fmt.Errorf("initialize handshake: %w", err)
	}

	e.cli = c
	slog.Info("external mcp client connected", "server", e.cfg.Name, "transport", e.cfg.Transport)
	return nil
}

// disconnect 关闭连接并将 cli 置 nil,触发下次调用的重连。
func (e *ExternalClient) disconnect() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cli != nil {
		_ = e.cli.Close()
		e.cli = nil
	}
}

// ensureConnected 在需要时建立连接,并确保现有连接仍可 ping 通。
// 返回错误表示当前无法连通。
func (e *ExternalClient) ensureConnected(ctx context.Context) error {
	e.mu.Lock()
	cli := e.cli
	e.mu.Unlock()

	if cli != nil {
		if err := cli.Ping(ctx); err == nil {
			return nil
		}
		// 现有连接已失效,标记断开后重连。
		e.mu.Lock()
		if e.cli != nil {
			_ = e.cli.Close()
			e.cli = nil
		}
		e.mu.Unlock()
	}
	return e.connect(ctx)
}

// ListTools 列出远端 server 暴露的全部工具。
func (e *ExternalClient) ListTools(ctx context.Context) ([]mcptypes.Tool, error) {
	if err := e.ensureConnected(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	cli := e.cli
	e.mu.Unlock()
	if cli == nil {
		return nil, fmt.Errorf("not connected")
	}

	res, err := cli.ListTools(ctx, mcptypes.ListToolsRequest{})
	if err != nil {
		// 连接级错误:标记断开,下一次调用自愈。
		e.mu.Lock()
		if e.cli != nil {
			_ = e.cli.Close()
			e.cli = nil
		}
		e.mu.Unlock()
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return res.Tools, nil
}

// CallTool 调用远端工具,返回其文本结果(或结构化内容)。
func (e *ExternalClient) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	if err := e.ensureConnected(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	cli := e.cli
	e.mu.Unlock()
	if cli == nil {
		return nil, fmt.Errorf("not connected")
	}

	req := mcptypes.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	res, err := cli.CallTool(ctx, req)
	if err != nil {
		e.mu.Lock()
		if e.cli != nil {
			_ = e.cli.Close()
			e.cli = nil
		}
		e.mu.Unlock()
		return nil, fmt.Errorf("call remote tool %s: %w", name, err)
	}
	return extractToolResult(res)
}

// Ping 探测连接存活。
func (e *ExternalClient) Ping(ctx context.Context) error {
	e.mu.Lock()
	cli := e.cli
	e.mu.Unlock()
	if cli == nil {
		return fmt.Errorf("not connected")
	}
	return cli.Ping(ctx)
}

// extractToolResult 把远端 CallToolResult 归一为本系统统一的 (any, error)。
// 优先返回结构化内容;否则拼接文本片段。远端业务错误(IsError)映射为 Go error。
func extractToolResult(res *mcptypes.CallToolResult) (any, error) {
	if res == nil {
		return nil, fmt.Errorf("empty tool result")
	}
	if res.IsError {
		return nil, fmt.Errorf("remote tool error: %s", textOfContent(res.Content))
	}
	if res.StructuredContent != nil {
		return res.StructuredContent, nil
	}
	return textOfContent(res.Content), nil
}

// textOfContent 拼接 Content 列表中的文本片段。
func textOfContent(content []mcptypes.Content) string {
	var sb strings.Builder
	for _, c := range content {
		if t, ok := c.(mcptypes.TextContent); ok {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

// convertSchema 将远端 JSON Schema(InputSchema.Properties/Required)转换为本系统
// 的本地 ParamSchema,供前端渲染参数表单与引擎做参数校验。
func convertSchema(tool mcptypes.Tool) map[string]ParamSchema {
	out := make(map[string]ParamSchema, len(tool.InputSchema.Properties))
	for name, raw := range tool.InputSchema.Properties {
		ps := ParamSchema{Type: "string"} // JSON Schema 缺省按字符串兜底
		if m, ok := raw.(map[string]any); ok {
			if t, ok := m["type"].(string); ok && t != "" {
				ps.Type = t
			}
			if d, ok := m["description"].(string); ok {
				ps.Description = d
			}
			if def, ok := m["default"]; ok {
				ps.Default = def
			}
			if enumRaw, ok := m["enum"].([]any); ok {
				for _, e := range enumRaw {
					if s, ok := e.(string); ok {
						ps.Enum = append(ps.Enum, s)
					}
				}
			}
		}
		out[name] = ps
	}
	for _, r := range tool.InputSchema.Required {
		if p, ok := out[r]; ok {
			p.Required = true
			out[r] = p
		}
	}
	return out
}

// backoffFor 计算下一次重连的退避间隔(指数增长,封顶 60s)。
func backoffFor(attempt int) time.Duration {
	if attempt <= 0 {
		return time.Second
	}
	d := time.Duration(1<<uint(attempt-1)) * time.Second
	if d > 60*time.Second {
		return 60 * time.Second
	}
	return d
}
