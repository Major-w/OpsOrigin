package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// 传输类型常量。
const (
	TransportStdio = "stdio" // 本地子进程,通过 stdin/stdout 通信
	TransportSSE   = "sse"   // Server-Sent Events(旧式 HTTP 长连)
	TransportHTTP  = "http"  // Streamable HTTP(2025-03-26 起推荐)
)

// ServerConfig 描述一个需要接入的外部 MCP server。
//
// 三种传输各自所需字段:
//   - stdio: Command + Args + (可选)Env
//   - sse / http: URL + (可选)Headers(鉴权头,如 Authorization)
//
// 工具以 `mcp_<Name>_<tool>` 命名注入进程内 Registry,避免与内置/自定义工具冲突。
type ServerConfig struct {
	// Name 逻辑名,全局唯一,用作工具前缀与状态追踪键。
	// 留空时由 Manager 按传输+地址推导,但建议显式给定以便排查。
	Name string `json:"name"`
	// Transport 取值见 Transport* 常量,默认 stdio。
	Transport string `json:"transport"`
	// Disabled 为 true 时跳过该 server(默认启用,列出即接入)。
	Disabled bool `json:"disabled,omitempty"`
	// Command stdio 传输启动的命令(绝对路径或 PATH 内可寻址)。
	Command string `json:"command,omitempty"`
	// Args stdio 传输启动参数。
	Args []string `json:"args,omitempty"`
	// Env stdio 传输额外的环境变量,会与当前进程环境合并(同名覆盖)。
	Env map[string]string `json:"env,omitempty"`
	// URL sse / http 传输的服务端地址。
	URL string `json:"url,omitempty"`
	// Headers sse / http 传输的附加请求头(典型用法:鉴权 Bearer Token)。
	Headers map[string]string `json:"headers,omitempty"`
}

// Validate 校验配置自洽性。
func (c ServerConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("mcp server config: name is required")
	}
	switch c.Transport {
	case "", TransportStdio:
		if c.Command == "" {
			return fmt.Errorf("mcp server %q: stdio transport requires command", c.Name)
		}
	case TransportSSE, TransportHTTP:
		if c.URL == "" {
			return fmt.Errorf("mcp server %q: %s transport requires url", c.Name, c.Transport)
		}
	default:
		return fmt.Errorf("mcp server %q: unsupported transport %q", c.Name, c.Transport)
	}
	return nil
}

// LoadServersFromEnv 从环境变量加载外部 MCP server 配置列表。
//
// 优先级:
//  1. OPSORIGIN_MCP_CONFIG  — 指向一个 JSON 数组文件(适合多 server 的离线配置)。
//  2. OPSORIGIN_MCP_SERVERS — 直接内联 JSON 数组(适合容器/K8s 环境变量注入)。
//
// 两者皆为空时返回 (nil, nil),表示不接入任何外部 server(无副作用)。
func LoadServersFromEnv() ([]ServerConfig, error) {
	if p := os.Getenv("OPSORIGIN_MCP_CONFIG"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read OPSORIGIN_MCP_CONFIG %q: %w", p, err)
		}
		var cfgs []ServerConfig
		if err := json.Unmarshal(b, &cfgs); err != nil {
			return nil, fmt.Errorf("parse OPSORIGIN_MCP_CONFIG %q: %w", p, err)
		}
		return cfgs, nil
	}
	if s := os.Getenv("OPSORIGIN_MCP_SERVERS"); s != "" {
		var cfgs []ServerConfig
		if err := json.Unmarshal([]byte(s), &cfgs); err != nil {
			return nil, fmt.Errorf("parse OPSORIGIN_MCP_SERVERS: %w", err)
		}
		return cfgs, nil
	}
	return nil, nil
}
