package sysconfig

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/ai"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/llm"

	"github.com/opsorigin/core/pkg/httputil"
)

// ConfigItem 描述一个可配置项的元数据。
type ConfigItem struct {
	Key             string `json:"key"`              // 环境变量名,如 OPSORIGIN_CORE_PORT
	Category        string `json:"category"`         // 分组: core/agent_ws/tls/database/...
	Label           string `json:"label"`            // 中文标签
	Description     string `json:"description"`      // 说明文字
	Type            string `json:"type"`             // string/int/bool/duration/secret
	CurrentValue    string `json:"current_value"`    // 当前值(敏感字段脱敏)
	DefaultValue    string `json:"default_value"`    // 默认值
	RequiresRestart bool   `json:"requires_restart"` // 修改后是否需要重启
	Sensitive       bool   `json:"sensitive"`        // 是否为敏感字段(密码/密钥)
}

// ConfigGroup 配置分组。
type ConfigGroup struct {
	Category string       `json:"category"`
	Label    string       `json:"label"`
	Items    []ConfigItem `json:"items"`
}

// UpdateRequest 更新配置请求。
type UpdateRequest struct {
	Updates map[string]string `json:"updates"` // key → value
}

// Service 是系统配置管理服务。
type Service struct {
	mu      sync.RWMutex
	cfg     *config.Config
	envPath string // .env 文件路径
}

// NewService 创建系统配置服务。
func NewService(cfg *config.Config) *Service {
	return &Service{
		cfg:     cfg,
		envPath: ".env",
	}
}

// Module 接口实现
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/system/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/v1/system/config", s.handleUpdateConfig)
	mux.HandleFunc("GET /api/v1/system/config/categories", s.handleGetCategories)
}

// buildConfigItems 构建所有配置项列表。
func (s *Service) buildConfigItems() []ConfigGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.cfg

	groups := []ConfigGroup{
		{
			Category: "core",
			Label:    "核心服务",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_CORE_PORT", Category: "core", Label: "主服务端口", Description: "Core HTTP API 监听端口", Type: "int", CurrentValue: fmt.Sprintf("%d", cfg.Core.Port), DefaultValue: "8080", RequiresRestart: true},
			},
		},
		{
			Category: "tls",
			Label:    "全局 TLS",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_TLS_ENABLED", Category: "tls", Label: "启用 HTTPS", Description: "全局启用 HTTPS,影响安装脚本中的下载链接 scheme", Type: "bool", CurrentValue: fmt.Sprintf("%t", cfg.TLS.Enabled), DefaultValue: "false", RequiresRestart: true},
				{Key: "OPSORIGIN_TLS_CERT", Category: "tls", Label: "TLS 证书路径", Description: "全局 TLS 证书文件路径", Type: "string", CurrentValue: cfg.TLS.CertFile, DefaultValue: "", RequiresRestart: true},
				{Key: "OPSORIGIN_TLS_KEY", Category: "tls", Label: "TLS 私钥路径", Description: "全局 TLS 私钥文件路径", Type: "string", CurrentValue: cfg.TLS.KeyFile, DefaultValue: "", RequiresRestart: true, Sensitive: true},
			},
		},
		{
			Category: "tunnel",
			Label:    "Proxy 隧道",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_PROXY_ID", Category: "tunnel", Label: "Proxy 节点 ID", Description: "默认 Proxy 节点标识", Type: "string", CurrentValue: cfg.Proxy.ID, DefaultValue: "proxy-001", RequiresRestart: true},
				{Key: "OPSORIGIN_PROXY_CORE_ADDR", Category: "tunnel", Label: "Proxy 连接地址", Description: "Proxy 连接 Core 的地址(host:port)", Type: "string", CurrentValue: cfg.Proxy.CoreAddr, DefaultValue: "localhost:8080", RequiresRestart: true},
				{Key: "OPSORIGIN_PROXY_LISTEN", Category: "tunnel", Label: "隧道监听地址", Description: "Core 端 tunnel server 监听地址", Type: "string", CurrentValue: cfg.Proxy.Listen, DefaultValue: ":9090", RequiresRestart: true},
				{Key: "OPSORIGIN_AGENT_PROXY_ADDR", Category: "tunnel", Label: "Agent Proxy 地址", Description: "Agent 连接 Proxy 的地址", Type: "string", CurrentValue: cfg.Agent.ProxyAddr, DefaultValue: "localhost:9090", RequiresRestart: true},
			},
		},
		{
			Category: "database",
			Label:    "数据存储",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_POSTGRES_DSN", Category: "database", Label: "PostgreSQL DSN", Description: "CMDB PostgreSQL 连接串", Type: "secret", CurrentValue: maskSensitive(cfg.CMDB.PostgresDSN), DefaultValue: "postgres://opsorigin:opsorigin@localhost:5432/opsorigin?sslmode=disable", RequiresRestart: true, Sensitive: true},
				{Key: "OPSORIGIN_DGRAPH_TARGET", Category: "database", Label: "Dgraph 地址", Description: "Dgraph gRPC 地址", Type: "string", CurrentValue: cfg.CMDB.DgraphTarget, DefaultValue: "localhost:9080", RequiresRestart: true},
				{Key: "OPSORIGIN_DGRAPH_API_KEY", Category: "database", Label: "Dgraph API Key", Description: "Dgraph API 密钥(可选)", Type: "secret", CurrentValue: maskSensitive(cfg.CMDB.DgraphAPIKey), DefaultValue: "", RequiresRestart: true, Sensitive: true},
				{Key: "OPSORIGIN_REDIS_ADDR", Category: "database", Label: "Redis 地址", Description: "Redis 服务器地址", Type: "string", CurrentValue: cfg.RedisAddr, DefaultValue: "localhost:6379", RequiresRestart: true},
			},
		},
		{
			Category: "auth",
			Label:    "认证安全",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_JWT_SECRET", Category: "auth", Label: "JWT 密钥", Description: "JWT Token 签名密钥,修改后所有已登录用户需重新登录", Type: "secret", CurrentValue: maskSensitive(cfg.Auth.JWTSecret), DefaultValue: "opsorigin-dev-secret-do-not-use-in-production", RequiresRestart: true, Sensitive: true},
				{Key: "OPSORIGIN_MULTI_TENANT", Category: "auth", Label: "多租户模式", Description: "关闭(默认)=单租户, 所有请求强制租户 default; 开启后按 JWT/请求头区分租户。建议部署初始化时设置, 热生效。", Type: "bool", CurrentValue: fmt.Sprintf("%t", cfg.MultiTenant), DefaultValue: "false", RequiresRestart: false},
			},
		},
		{
			Category: "llm",
			Label:    "AI 模型",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_LLM_API_KEY", Category: "llm", Label: "LLM API Key", Description: "大模型 API 密钥", Type: "secret", CurrentValue: maskSensitive(cfg.LLM.APIKey), DefaultValue: "", RequiresRestart: false, Sensitive: true},
				{Key: "OPSORIGIN_LLM_MODEL", Category: "llm", Label: "LLM 模型", Description: "大模型名称", Type: "string", CurrentValue: cfg.LLM.Model, DefaultValue: "deepseek-chat", RequiresRestart: false},
				{Key: "OPSORIGIN_LLM_BASE_URL", Category: "llm", Label: "LLM Base URL", Description: "大模型 API 基础地址", Type: "string", CurrentValue: cfg.LLM.BaseURL, DefaultValue: "https://api.deepseek.com", RequiresRestart: false},
				{Key: "OPSORIGIN_LLM_TIMEOUT", Category: "llm", Label: "LLM 超时(秒)", Description: "大模型请求超时时间", Type: "int", CurrentValue: fmt.Sprintf("%d", cfg.LLM.Timeout), DefaultValue: "60", RequiresRestart: false},
				{Key: "OPSORIGIN_LLM_PROVIDERS", Category: "llm", Label: "模型池 Providers (JSON)", Description: "多模型路由池 JSON 数组, 如 [{\"name\":\"aliyun\",\"api_key\":\"sk-xxx\",\"model\":\"qwen-max\",\"base_url\":\"https://dashscope.aliyuncs.com/compatible-mode/v1\",\"weight\":80}]。推荐在「智能体工作台→模型」页可视化维护。", Type: "json", CurrentValue: marshalProviders(cfg.LLM.Providers), DefaultValue: "[]", RequiresRestart: false},
			},
		},
		{
			Category: "outbox",
			Label:    "事件外发",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_OUTBOX_POLL_INTERVAL", Category: "outbox", Label: "轮询间隔", Description: "Outbox 事件轮询间隔", Type: "duration", CurrentValue: cfg.Outbox.PollInterval.String(), DefaultValue: "1s", RequiresRestart: true},
				{Key: "OPSORIGIN_OUTBOX_BATCH_SIZE", Category: "outbox", Label: "批量大小", Description: "Outbox 事件批量处理数量", Type: "int", CurrentValue: fmt.Sprintf("%d", cfg.Outbox.BatchSize), DefaultValue: "100", RequiresRestart: true},
			},
		},
		{
			Category: "ldap",
			Label:    "LDAP",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_LDAP_ENABLED", Category: "ldap", Label: "启用 LDAP", Description: "是否启用 LDAP 认证", Type: "bool", CurrentValue: fmt.Sprintf("%t", cfg.LDAP.Enabled), DefaultValue: "false", RequiresRestart: false},
				{Key: "OPSORIGIN_LDAP_ADDR", Category: "ldap", Label: "LDAP 地址", Description: "LDAP 服务器地址", Type: "string", CurrentValue: cfg.LDAP.Addr, DefaultValue: "ldap://localhost:389", RequiresRestart: false},
				{Key: "OPSORIGIN_LDAP_BASE_DN", Category: "ldap", Label: "Base DN", Description: "LDAP Base DN", Type: "string", CurrentValue: cfg.LDAP.BaseDN, DefaultValue: "dc=example,dc=org", RequiresRestart: false},
				{Key: "OPSORIGIN_LDAP_BIND_DN", Category: "ldap", Label: "Bind DN", Description: "LDAP 绑定 DN", Type: "string", CurrentValue: cfg.LDAP.BindDN, DefaultValue: "cn=admin,dc=example,dc=org", RequiresRestart: false},
				{Key: "OPSORIGIN_LDAP_BIND_PASS", Category: "ldap", Label: "Bind 密码", Description: "LDAP 绑定密码", Type: "secret", CurrentValue: maskSensitive(cfg.LDAP.BindPass), DefaultValue: "", RequiresRestart: false, Sensitive: true},
				{Key: "OPSORIGIN_LDAP_USER_FILTER", Category: "ldap", Label: "用户过滤器", Description: "LDAP 用户搜索过滤器", Type: "string", CurrentValue: cfg.LDAP.UserFilter, DefaultValue: "(uid=%s)", RequiresRestart: false},
			},
		},
		{
			Category: "sso",
			Label:    "SSO 单点登录",
			Items: []ConfigItem{
				{Key: "OPSORIGIN_SSO_ENABLED", Category: "sso", Label: "启用 SSO", Description: "是否启用 SSO 单点登录", Type: "bool", CurrentValue: fmt.Sprintf("%t", cfg.SSO.Enabled), DefaultValue: "false", RequiresRestart: false},
				{Key: "OPSORIGIN_SSO_PROVIDER", Category: "sso", Label: "SSO 提供商", Description: "SSO 提供商类型", Type: "string", CurrentValue: cfg.SSO.Provider, DefaultValue: "oidc", RequiresRestart: false},
				{Key: "OPSORIGIN_SSO_CLIENT_ID", Category: "sso", Label: "Client ID", Description: "SSO 客户端 ID", Type: "string", CurrentValue: cfg.SSO.ClientID, DefaultValue: "", RequiresRestart: false},
				{Key: "OPSORIGIN_SSO_CLIENT_SECRET", Category: "sso", Label: "Client Secret", Description: "SSO 客户端密钥", Type: "secret", CurrentValue: maskSensitive(cfg.SSO.ClientSecret), DefaultValue: "", RequiresRestart: false, Sensitive: true},
				{Key: "OPSORIGIN_SSO_REDIRECT_URL", Category: "sso", Label: "回调地址", Description: "SSO 登录回调地址", Type: "string", CurrentValue: cfg.SSO.RedirectURL, DefaultValue: "http://localhost:8080/api/v1/auth/callback", RequiresRestart: false},
				{Key: "OPSORIGIN_SSO_ISSUER_URL", Category: "sso", Label: "Issuer URL", Description: "SSO Issuer URL", Type: "string", CurrentValue: cfg.SSO.IssuerURL, DefaultValue: "", RequiresRestart: false},
			},
		},
	}
	return groups
}

// maskSensitive 对敏感字段脱敏(只显示前4位,其余用*代替)。
func maskSensitive(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + strings.Repeat("*", 12)
}

// handleGetConfig 返回所有配置项(分组)。
func (s *Service) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	groups := s.buildConfigItems()
	writeJSON(w, http.StatusOK, groups)
}

// handleGetCategories 返回所有配置分组名。
func (s *Service) handleGetCategories(w http.ResponseWriter, r *http.Request) {
	groups := s.buildConfigItems()
	categories := make([]map[string]string, 0, len(groups))
	for _, g := range groups {
		categories = append(categories, map[string]string{
			"category": g.Category,
			"label":    g.Label,
		})
	}
	writeJSON(w, http.StatusOK, categories)
}

// handleUpdateConfig 更新配置项并持久化到 .env 文件。
func (s *Service) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Updates) == 0 {
		writeError(w, http.StatusBadRequest, "no updates provided")
		return
	}

	// 获取所有已知 key 的合法集合
	knownKeys := make(map[string]bool)
	for _, g := range s.buildConfigItems() {
		for _, item := range g.Items {
			knownKeys[item.Key] = true
		}
	}

	// 验证所有 key 合法
	for key := range req.Updates {
		if !knownKeys[key] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown config key: %s", key))
			return
		}
	}

	// 更新内存中的配置
	s.mu.Lock()
	for key, value := range req.Updates {
		s.applyUpdate(key, value)
	}
	s.mu.Unlock()

	// 持久化到 .env 文件
	if err := s.persistToEnvFile(req.Updates); err != nil {
		slog.Warn("persist config to .env failed", "error", err)
		// 不返回错误,内存已更新,只是持久化失败
	}

	// 统计需要重启的项
	restartCount := 0
	for key := range req.Updates {
		for _, g := range s.buildConfigItems() {
			for _, item := range g.Items {
				if item.Key == key && item.RequiresRestart {
					restartCount++
				}
			}
		}
	}

	result := map[string]any{
		"updated":          len(req.Updates),
		"requires_restart": restartCount,
		"message":          "配置已更新,部分项需重启服务后生效",
	}
	if restartCount > 0 {
		result["message"] = fmt.Sprintf("已更新 %d 项配置,其中 %d 项需重启服务后生效", len(req.Updates), restartCount)
	}
	writeJSON(w, http.StatusOK, result)
}

// applyUpdate 将单个配置项更新应用到内存中的 Config。
func (s *Service) applyUpdate(key, value string) {
	cfg := s.cfg
	switch key {
	case "OPSORIGIN_CORE_PORT":
		cfg.Core.Port = atoiSafe(value, 8080)
	case "OPSORIGIN_TLS_ENABLED":
		cfg.TLS.Enabled = value == "true" || value == "1"
	case "OPSORIGIN_TLS_CERT":
		cfg.TLS.CertFile = value
	case "OPSORIGIN_TLS_KEY":
		cfg.TLS.KeyFile = value
	case "OPSORIGIN_PROXY_ID":
		cfg.Proxy.ID = value
	case "OPSORIGIN_PROXY_CORE_ADDR":
		cfg.Proxy.CoreAddr = value
	case "OPSORIGIN_PROXY_LISTEN":
		cfg.Proxy.Listen = value
	case "OPSORIGIN_AGENT_PROXY_ADDR":
		cfg.Agent.ProxyAddr = value
	case "OPSORIGIN_POSTGRES_DSN":
		cfg.CMDB.PostgresDSN = value
	case "OPSORIGIN_DGRAPH_TARGET":
		cfg.CMDB.DgraphTarget = value
	case "OPSORIGIN_DGRAPH_API_KEY":
		cfg.CMDB.DgraphAPIKey = value
	case "OPSORIGIN_REDIS_ADDR":
		cfg.RedisAddr = value
	case "OPSORIGIN_JWT_SECRET":
		cfg.Auth.JWTSecret = value
	case "OPSORIGIN_LLM_API_KEY":
		cfg.LLM.APIKey = value
		pushLLMUpdate(cfg)
	case "OPSORIGIN_LLM_MODEL":
		cfg.LLM.Model = value
		pushLLMUpdate(cfg)
	case "OPSORIGIN_LLM_BASE_URL":
		cfg.LLM.BaseURL = value
		pushLLMUpdate(cfg)
	case "OPSORIGIN_LLM_TIMEOUT":
		cfg.LLM.Timeout = atoiSafe(value, 60)
		pushLLMUpdate(cfg)
	case "OPSORIGIN_LLM_PROVIDERS":
		var ps []config.LLMProviderConfig
		if err := json.Unmarshal([]byte(value), &ps); err != nil {
			slog.Warn("invalid OPSORIGIN_LLM_PROVIDERS json, ignored", "err", err)
		} else {
			cfg.LLM.Providers = ps
			pushLLMUpdate(cfg)
		}
	case "OPSORIGIN_MULTI_TENANT":
		cfg.MultiTenant = value == "true" || value == "1"
		auth.SetMultiTenant(cfg.MultiTenant)
	case "OPSORIGIN_OUTBOX_POLL_INTERVAL":
		cfg.Outbox.PollInterval = parseDurationSafe(value)
	case "OPSORIGIN_OUTBOX_BATCH_SIZE":
		cfg.Outbox.BatchSize = atoiSafe(value, 100)
	case "OPSORIGIN_LDAP_ENABLED":
		cfg.LDAP.Enabled = value == "true" || value == "1"
	case "OPSORIGIN_LDAP_ADDR":
		cfg.LDAP.Addr = value
	case "OPSORIGIN_LDAP_BASE_DN":
		cfg.LDAP.BaseDN = value
	case "OPSORIGIN_LDAP_BIND_DN":
		cfg.LDAP.BindDN = value
	case "OPSORIGIN_LDAP_BIND_PASS":
		cfg.LDAP.BindPass = value
	case "OPSORIGIN_LDAP_USER_FILTER":
		cfg.LDAP.UserFilter = value
	case "OPSORIGIN_SSO_ENABLED":
		cfg.SSO.Enabled = value == "true" || value == "1"
	case "OPSORIGIN_SSO_PROVIDER":
		cfg.SSO.Provider = value
	case "OPSORIGIN_SSO_CLIENT_ID":
		cfg.SSO.ClientID = value
	case "OPSORIGIN_SSO_CLIENT_SECRET":
		cfg.SSO.ClientSecret = value
	case "OPSORIGIN_SSO_REDIRECT_URL":
		cfg.SSO.RedirectURL = value
	case "OPSORIGIN_SSO_ISSUER_URL":
		cfg.SSO.IssuerURL = value
	}
}

func pushLLMUpdate(cfg *config.Config) {
	if ai.LLMConfigHook != nil {
		ai.LLMConfigHook(llm.Config{
			APIKey:  cfg.LLM.APIKey,
			Model:   cfg.LLM.Model,
			BaseURL: cfg.LLM.BaseURL,
			Timeout: time.Duration(cfg.LLM.Timeout) * time.Second,
			// 语义检索 embedding 端点随配置一起热替换, 避免改 LLM 配置后向量化失效。
			EmbeddingModel:   cfg.LLM.EmbeddingModel,
			EmbeddingBaseURL: cfg.LLM.EmbeddingBaseURL,
		})
	}
}

// marshalProviders 将模型池 provider 序列化为 JSON 字符串(供 sysconfig 展示/回写)。
func marshalProviders(ps []config.LLMProviderConfig) string {
	if len(ps) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ps)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// persistToEnvFile 将更新的配置项写入 .env 文件(保留已有项,更新或追加)。
func (s *Service) persistToEnvFile(updates map[string]string) error {
	// 读取现有 .env 内容
	lines := []string{}
	if content, err := os.ReadFile(s.envPath); err == nil {
		lines = strings.Split(string(content), "\n")
	}

	// 构建 key → line index 映射
	keyLines := make(map[string]int)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			keyLines[strings.TrimSpace(parts[0])] = i
		}
	}

	// 更新已有行或追加新行
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := updates[key]
		line := fmt.Sprintf("%s=%s", key, value)
		if idx, ok := keyLines[key]; ok {
			lines[idx] = line
		} else {
			if len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "# 全局参数修改自动写入", line)
		}
	}

	// 写回 .env
	content := strings.Join(lines, "\n")
	return os.WriteFile(s.envPath, []byte(content), 0644)
}

// atoiSafe 安全的字符串转 int,失败返回默认值。
func atoiSafe(s string, defaultVal int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return defaultVal
	}
	return n
}

// parseDurationSafe 安全的 duration 解析,失败返回 1s。
func parseDurationSafe(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Second
	}
	return d
}

// writeJSON 写入 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	v = httputil.EmptySliceIfNil(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError 写入错误响应。
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
