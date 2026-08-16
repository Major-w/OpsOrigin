package mcp

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	mcptypes "github.com/mark3labs/mcp-go/mcp"
)

// 工具命名安全字符:非字母数字下划线统一替换为下划线,避免破坏 Registry key 语义。
var reNonWord = regexp.MustCompile(`[^A-Za-z0-9_]`)

func sanitizeName(s string) string {
	return reNonWord.ReplaceAllString(s, "_")
}

// 健康探测间隔与重连参数。
const (
	healthCheckInterval = 30 * time.Second
	maxReconnectBackoff = 60 * time.Second
)

// ServerStatus 是一个外部 MCP server 的运行时状态快照。
type ServerStatus struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"tool_count"`
	Error     string `json:"error,omitempty"`
}

// Manager 负责外部 MCP server 的接入、工具 proxy 与生命周期管理。
//
// 它把每个远端 server 暴露的工具以 `mcp_<server>_<tool>` 命名 proxy 进进程内
// Registry —— 由于 buildToolDefs 在 agent.Tools 为空时视全部注册表工具可用,
// 这些外部工具注入后天然对模型可见,无需额外放行。
type Manager struct {
	reg *Registry

	mu      sync.RWMutex
	servers map[string]*ExternalClient
	status  map[string]*ServerStatus
	prefix  map[string]string // name -> 工具前缀(用于增删)

	stopCh    chan struct{}
	closeOnce sync.Once
}

// NewManager 创建 Manager 并绑定目标 Registry。
func NewManager(reg *Registry) *Manager {
	return &Manager{
		reg:     reg,
		servers: make(map[string]*ExternalClient),
		status:  make(map[string]*ServerStatus),
		prefix:  make(map[string]string),
		stopCh:  make(chan struct{}),
	}
}

// Start 根据配置接入所有(未禁用)外部 server,每个 server 一个监督 goroutine。
// cfgs 为空时直接返回,无副作用。
func (m *Manager) Start(ctx context.Context, cfgs []ServerConfig) error {
	for _, cfg := range cfgs {
		if cfg.Disabled {
			slog.Info("external mcp server disabled, skip", "name", cfg.Name)
			continue
		}
		if cfg.Transport == "" {
			cfg.Transport = TransportStdio
		}
		if err := cfg.Validate(); err != nil {
			slog.Error("invalid external mcp server config, skip", "name", cfg.Name, "error", err)
			continue
		}
		if cfg.Name == "" {
			cfg.Name = sanitizeName(cfg.Command + cfg.URL)
		}

		ec := &ExternalClient{cfg: cfg}
		prefix := "mcp_" + sanitizeName(cfg.Name) + "_"

		m.mu.Lock()
		m.servers[cfg.Name] = ec
		m.prefix[cfg.Name] = prefix
		m.status[cfg.Name] = &ServerStatus{
			Name:      cfg.Name,
			Transport: cfg.Transport,
			Enabled:   true,
		}
		m.mu.Unlock()

		go m.supervise(ctx, cfg.Name)
	}
	return nil
}

// supervise 监督单个 server:连接 → 拉取工具 → 健康探测 → 断线重连(指数退避)。
// 该 goroutine 随 ctx 取消或 Stop 而退出。
func (m *Manager) supervise(ctx context.Context, name string) {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		default:
		}

		ec := m.getClient(name)
		if ec == nil {
			return
		}

		if err := ec.connect(ctx); err != nil {
			attempt++
			backoff := backoffFor(attempt)
			m.setStatus(name, false, 0, err.Error())
			slog.Warn("external mcp connect failed, retry later",
				"server", name, "error", err, "attempt", attempt, "backoff", backoff)
			if !m.sleepWithCancel(backoff) {
				return
			}
			continue
		}

		tools, err := ec.ListTools(ctx)
		if err != nil {
			attempt++
			backoff := backoffFor(attempt)
			m.setStatus(name, false, 0, err.Error())
			ec.disconnect()
			slog.Warn("external mcp list tools failed, retry later",
				"server", name, "error", err, "attempt", attempt, "backoff", backoff)
			if !m.sleepWithCancel(backoff) {
				return
			}
			continue
		}

		// 连接成功后重置退避计数,并刷新工具(可能相对上次有增减)。
		attempt = 0
		m.registerTools(name, ec, tools)
		m.setStatus(name, true, len(tools), "")
		slog.Info("external mcp server ready", "server", name, "tool_count", len(tools))

		// —— 健康探测循环,直到连接失效 ——
		ticker := time.NewTicker(healthCheckInterval)
		healthy := true
		for healthy {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-m.stopCh:
				ticker.Stop()
				return
			case <-ticker.C:
				if err := ec.Ping(ctx); err != nil {
					slog.Warn("external mcp ping failed, reconnecting",
						"server", name, "error", err)
					m.setStatus(name, false, 0, err.Error())
					m.unregisterTools(name)
					ec.disconnect()
					healthy = false
				}
			}
		}
		ticker.Stop()
		// 退出健康循环 → 回到顶部重连(attempt 已在成功时归零,首次退避 1s)。
	}
}

// registerTools 把远端工具 proxy 为本地 ToolDefinition 注入 Registry。
// 先按前缀清理旧工具,保证重连后工具集与远端一致(支持远端工具增删)。
func (m *Manager) registerTools(name string, ec *ExternalClient, tools []mcptypes.Tool) {
	m.unregisterTools(name)
	prefix := m.getPrefix(name)

	for _, t := range tools {
		remoteName := t.GetName()
		localName := prefix + sanitizeName(remoteName)
		params := convertSchema(t)
		desc := t.Description
		if desc == "" {
			desc = "外部 MCP 工具 " + remoteName + " (来自 server " + name + ")"
		}
		// 捕获 remoteName 到闭包,handler 调用时使用远端原始工具名。
		rn := remoteName
		m.reg.Register(ToolDefinition{
			Name:        localName,
			Description: desc,
			Parameters:  params,
			Handler: func(ctx context.Context, input map[string]any) (any, error) {
				return ec.CallTool(ctx, rn, input)
			},
		})
	}
}

// unregisterTools 卸载该 server 前缀下的全部本地 proxy 工具。
func (m *Manager) unregisterTools(name string) {
	prefix := m.getPrefix(name)
	if prefix == "" {
		return
	}
	for _, t := range m.reg.List() {
		if strings.HasPrefix(t.Name, prefix) {
			m.reg.Unregister(t.Name)
		}
	}
}

// Status 返回所有 server 的运行时状态快照。
func (m *Manager) Status() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServerStatus, 0, len(m.status))
	for _, s := range m.status {
		out = append(out, *s)
	}
	return out
}

// Stop 停止全部监督 goroutine 并断开所有连接(优雅退出)。
func (m *Manager) Stop() {
	m.closeOnce.Do(func() {
		close(m.stopCh)
	})
	m.mu.RLock()
	servers := make([]*ExternalClient, 0, len(m.servers))
	for _, ec := range m.servers {
		servers = append(servers, ec)
	}
	m.mu.RUnlock()
	for _, ec := range servers {
		ec.disconnect()
	}
}

// getClient / getPrefix / setStatus 是带锁的内部访问器。
func (m *Manager) getClient(name string) *ExternalClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}

func (m *Manager) getPrefix(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.prefix[name]
}

func (m *Manager) setStatus(name string, connected bool, toolCount int, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.status[name]; ok {
		s.Connected = connected
		s.ToolCount = toolCount
		s.Error = errMsg
	}
}

// sleepWithCancel 在退避等待期间可被 ctx 取消或 Stop 提前唤醒。
// 返回 false 表示应终止监督 goroutine。
func (m *Manager) sleepWithCancel(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-m.stopCh:
		return false
	}
}
