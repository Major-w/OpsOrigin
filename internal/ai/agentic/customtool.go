package agentic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/ai/mcp"
)

// ============================================================================
// 自定义 MCP 工具注册 (PRD 5.2.2)
//
// 用户在界面注册两类自定义工具, 动态注入 LLM Function Calling:
//   - type=http: 调用外部 REST API(URL 模板支持 {param} 占位符, 其余参数进请求体/查询串);
//   - type=cli:  在服务端执行本地命令(命令模板支持 {param} 替换)。
//
// 安全边界:
//   - cli 工具在服务端 shell 执行, 风险极高, 仅建议管理员在可信环境启用;
//   - 工具名全局唯一(租户内), 参数 Schema 可校验;
//   - http 工具默认 30s 超时, 响应体截断(1MB)防撑爆上下文。
// ============================================================================

// CustomToolParam 自定义工具的一个参数定义。
type CustomToolParam struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // string | number | boolean
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Enum        []string `json:"enum,omitempty"`
}

// CustomTool 一个用户自定义注册的 MCP 工具(持久化于 ai_custom_tools 表)。
type CustomTool struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"` // 工具名(租户内唯一, LLM function calling 名称)
	Description string `json:"description"`
	Type        string `json:"type"` // "http" | "cli"
	Enabled     bool   `json:"enabled"`

	// 参数 Schema(渲染给 LLM)
	Parameters []CustomToolParam `json:"parameters"`

	// —— HTTP 工具配置 ——
	HTTPMethod  string            `json:"http_method,omitempty"` // GET/POST/PUT/DELETE/PATCH
	HTTPURL     string            `json:"http_url,omitempty"`    // 支持 {param} 占位符
	HTTPHeaders map[string]string `json:"http_headers,omitempty"`
	TimeoutSec  int               `json:"timeout_sec,omitempty"` // 默认 30

	// —— CLI 工具配置 ——
	CLICommand string `json:"cli_command,omitempty"` // shell 模板, 支持 {param}

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// toolParamMap 转换为 mcp.ParamSchema 映射(供 registry 与 LLM ToolDef 使用)。
func (t *CustomTool) toolParamMap() map[string]mcp.ParamSchema {
	out := make(map[string]mcp.ParamSchema, len(t.Parameters))
	for _, p := range t.Parameters {
		ps := mcp.ParamSchema{Type: p.Type, Description: p.Description, Required: p.Required}
		if len(p.Enum) > 0 {
			ps.Enum = p.Enum
		}
		out[p.Name] = ps
	}
	return out
}

// toToolDefinition 将自定义工具转换为 mcp.ToolDefinition(含执行 handler)。
func (t *CustomTool) toToolDefinition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  t.toolParamMap(),
		Handler:     t.handler(),
	}
}

// handler 按工具类型生成执行函数。
func (t *CustomTool) handler() mcp.ToolHandler {
	if t.Type == "cli" {
		return t.cliHandler()
	}
	return t.httpHandler()
}

// httpHandler 调用外部 REST API。
// URL 模板中的 {param} 由参数替换; 非 GET 请求参数作为 JSON body 发送。
func (t *CustomTool) httpHandler() mcp.ToolHandler {
	method := strings.ToUpper(strings.TrimSpace(t.HTTPMethod))
	if method == "" {
		method = "GET"
	}
	timeout := time.Duration(t.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return func(ctx context.Context, input map[string]any) (any, error) {
		u := t.HTTPURL
		for k, v := range input {
			u = strings.ReplaceAll(u, "{"+k+"}", url.QueryEscape(fmt.Sprint(v)))
		}
		var body io.Reader
		if method != "GET" && method != "HEAD" {
			if b, err := json.Marshal(input); err == nil {
				body = bytes.NewReader(b)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return map[string]any{"error": "invalid request: " + err.Error()}, nil
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range t.HTTPHeaders {
			req.Header.Set(k, v)
		}
		client := &http.Client{Timeout: timeout}
		resp, err := client.Do(req)
		if err != nil {
			return map[string]any{"error": "http request failed: " + err.Error()}, nil
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB 截断
		var result any
		if json.Unmarshal(data, &result) == nil {
			return map[string]any{"status": resp.StatusCode, "data": result}, nil
		}
		return map[string]any{"status": resp.StatusCode, "data": string(data)}, nil
	}
}

// cliHandler 在服务端执行本地命令(命令模板中的 {param} 由参数替换)。
// ⚠️ 安全提示: shell 执行, 仅应由管理员在可信环境创建。
func (t *CustomTool) cliHandler() mcp.ToolHandler {
	timeout := time.Duration(t.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return func(ctx context.Context, input map[string]any) (any, error) {
		cmdStr := t.CLICommand
		for k, v := range input {
			cmdStr = strings.ReplaceAll(cmdStr, "{"+k+"}", fmt.Sprint(v))
		}
		ctx2, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		out, err := exec.CommandContext(ctx2, "sh", "-c", cmdStr).CombinedOutput()
		result := map[string]any{"output": string(out)}
		if err != nil {
			result["error"] = err.Error()
		}
		return result, nil
	}
}
