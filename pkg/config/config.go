package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Core          CoreConfig      `json:"core"`
	Proxy         ProxyConfig     `json:"proxy"`
	Agent         AgentConfig     `json:"agent"`
	TLS           TLSConfig       `json:"tls"`
	Outbox        OutboxConfig    `json:"outbox"`
	CMDB          CMDBConfig      `json:"cmdb"`
	Auth          AuthConfig      `json:"auth"`
	LLM           LLMConfig       `json:"llm"`
	RedisAddr     string          `json:"redis_addr"`
	RedisPoolSize int             `json:"redis_pool_size"`
	LDAP          LDAPConfig      `json:"ldap"`
	SSO           SSOConfig       `json:"sso"`
	Tunnel        TunnelConfig    `json:"tunnel"`
	FileStore     FileStoreConfig `json:"file_store"` // §5 文件分发中心存储配置
	Postgres      PostgresConfig  `json:"postgres"`
	TaskQueue     TaskQueueConfig `json:"task_queue"`
	Upgrade       UpgradeConfig   `json:"upgrade"`
}

type LDAPConfig struct {
	Enabled    bool   `json:"enabled"`
	Addr       string `json:"addr"`
	BaseDN     string `json:"base_dn"`
	BindDN     string `json:"bind_dn"`
	BindPass   string `json:"bind_pass"`
	UserFilter string `json:"user_filter"`
}

type SSOConfig struct {
	Enabled      bool   `json:"enabled"`
	Provider     string `json:"provider"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURL  string `json:"redirect_url"`
	IssuerURL    string `json:"issuer_url"`
}

type CoreConfig struct {
	Port       int    `json:"port"`
	Addr       string `json:"addr"`
	GRPCListen string `json:"grpc_listen"`
	PublicURL  string `json:"public_url"` // Server 对外基址(如 http://host:8080),用于下发直连 Agent 的下载地址
	// VMAddr VictoriaMetrics 时序库地址(如 http://localhost:8428)。
	// 配置后监控指标写入 VM(高频 monitor), 未配置时回退内存 sink(仅演示/测试)。
	VMAddr string `json:"vm_addr"`
}

type ProxyConfig struct {
	ID         string `json:"id"`
	CoreAddr   string `json:"core_addr"`
	Listen     string `json:"listen"`
	GRPCListen string `json:"grpc_listen"`
	FileListen string `json:"file_listen"` // §5 HTTP 文件代理监听地址(留空则不启用)
}

// FileStoreConfig 是 §5 文件分发中心存储配置。
// Dir 为文件落盘目录(以 file_id 为文件名);TokenSecret 为 §6 短期下载令牌密钥,
// Server 与 Proxy 必须配置为同一值(Proxy 在边缘校验令牌后再向 Core 拉取)。
type FileStoreConfig struct {
	Dir         string `json:"dir"`
	TokenSecret string `json:"token_secret"`
}

type AgentConfig struct {
	ProxyAddr string `json:"proxy_addr"`
}

// TLSConfig 是全局 TLS 配置,影响下载链接的 scheme 判定。
type TLSConfig struct {
	Enabled  bool   `json:"enabled"`   // 是否全局启用 HTTPS
	CertFile string `json:"cert_file"` // 证书文件路径
	KeyFile  string `json:"key_file"`  // 私钥文件路径
}

type OutboxConfig struct {
	PollInterval time.Duration `json:"poll_interval"`
	BatchSize    int           `json:"batch_size"`
}

type CMDBConfig struct {
	PostgresDSN  string `json:"postgres_dsn"`
	DgraphTarget string `json:"dgraph_target"`
	DgraphAPIKey string `json:"dgraph_api_key,omitempty"`
}

type AuthConfig struct {
	JWTSecret string `json:"jwt_secret"`
}

// TunnelConfig 是 Proxy/Core 隧道通信的认证配置。
// 所有 Proxy 在注册到 Core 时必须携带与 Core 一致的 AuthSecret,
// 否则注册会被拒绝 (fail-closed), 防止未授权节点接入控制面。
type TunnelConfig struct {
	AuthSecret string `json:"auth_secret"` // 隧道注册共享密钥, 必填
	Insecure   bool   `json:"insecure"`    // 仅本地测试使用, 跳过校验并告警
}

type LLMConfig struct {
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	BaseURL string `json:"base_url"`
	Timeout int    `json:"timeout"`
	// EmbeddingModel/EmbeddingBaseURL 可选；留空时分别回退到 Model/BaseURL 的同名端点。
	EmbeddingModel   string `json:"embedding_model"`
	EmbeddingBaseURL string `json:"embedding_base_url"`
	// 生成参数(0 表示由引擎使用内置默认): GenTemperature 默认 0.7, GenMaxTokens 默认 1024, MaxInflight 默认 32。
	GenTemperature float64 `json:"gen_temperature"`
	GenMaxTokens   int     `json:"gen_max_tokens"`
	MaxInflight    int     `json:"max_inflight"`
	// DefaultSystemPrompt 未配置 Agent 级 system_prompt 时使用的全局默认提示词;
	// 留空则引擎回退到内置常量(与下方默认值一致)。运维可通过环境变量覆盖,实现提示词外置/版本化。
	DefaultSystemPrompt string `json:"default_system_prompt"`
	// Providers 多模型路由池(P3): 在默认 provider 之外追加的 LLM provider。
	// 留空=单 provider(默认配置), 行为与以往完全一致。
	Providers []LLMProviderConfig `json:"providers"`
}

// LLMProviderConfig 模型池中的一个附加 provider(多模型路由 P3)。
type LLMProviderConfig struct {
	Name    string `json:"name"`     // 池内唯一标识(Agent.model_name 可填此名)
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`    // 该 provider 的默认模型
	BaseURL string `json:"base_url"`
	Weight  int    `json:"weight"`   // 路由权重(越大越优先, 默认 50)
}

type PostgresConfig struct {
	MaxConns        int           `json:"max_conns"`         // OPSNEXUS_POSTGRES_MAX_CONNS, 默认 100 (F4-A 规模化)
	MaxIdleConns    int           `json:"max_idle_conns"`    // OPSNEXUS_POSTGRES_MAX_IDLE_CONNS, 默认 20 (F4-A 规模化)
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"` // OPSNEXUS_POSTGRES_CONN_MAX_LIFETIME, 默认 30m
}

type TaskQueueConfig struct {
	QueueSize int `json:"queue_size"` // OPSNEXUS_TASK_QUEUE_SIZE, 默认 1000
	Workers   int `json:"workers"`    // OPSNEXUS_TASK_QUEUE_WORKERS, 默认 4
}

type UpgradeConfig struct {
	DefaultBatchSize     int           `json:"default_batch_size"`     // OPSNEXUS_UPGRADE_DEFAULT_BATCH_SIZE, 默认 10
	DefaultBatchInterval time.Duration `json:"default_batch_interval"` // OPSNEXUS_UPGRADE_DEFAULT_BATCH_INTERVAL, 默认 60s
}

func Load() (*Config, error) {
	loadDotEnv()

	cfg := &Config{
		Core: CoreConfig{
			Port:       envInt("OPSNEXUS_CORE_PORT", 8080),
			GRPCListen: envStr("OPSNEXUS_CORE_GRPC_LISTEN", ":9091"),
			PublicURL:  envStr("OPSNEXUS_CORE_PUBLIC_URL", ""),
			VMAddr:     envStr("OPSNEXUS_CORE_VM_ADDR", ""),
		},
		Proxy: ProxyConfig{
			ID:         envStr("OPSNEXUS_PROXY_ID", "proxy-001"),
			CoreAddr:   envStr("OPSNEXUS_PROXY_CORE_ADDR", "localhost:8080"),
			Listen:     envStr("OPSNEXUS_PROXY_LISTEN", ":9090"),
			GRPCListen: envStr("OPSNEXUS_PROXY_GRPC_LISTEN", ":9092"),
			FileListen: envStr("OPSNEXUS_PROXY_FILE_LISTEN", ""),
		},
		Agent: AgentConfig{
			ProxyAddr: envStr("OPSNEXUS_AGENT_PROXY_ADDR", "localhost:9090"),
		},
		TLS: TLSConfig{
			Enabled:  envBool("OPSNEXUS_TLS_ENABLED", false),
			CertFile: envStr("OPSNEXUS_TLS_CERT", ""),
			KeyFile:  envStr("OPSNEXUS_TLS_KEY", ""),
		},
		Outbox: OutboxConfig{
			PollInterval: envDuration("OPSNEXUS_OUTBOX_POLL_INTERVAL", 1*time.Second),
			BatchSize:    envInt("OPSNEXUS_OUTBOX_BATCH_SIZE", 100),
		},
		CMDB: CMDBConfig{
			PostgresDSN:  envStr("OPSNEXUS_POSTGRES_DSN", "postgres://opsnexus:opsnexus@localhost:5432/opsnexus?sslmode=disable"),
			DgraphTarget: envStr("OPSNEXUS_DGRAPH_TARGET", "localhost:9080"),
			DgraphAPIKey: envStr("OPSNEXUS_DGRAPH_API_KEY", ""),
		},
		Auth: AuthConfig{
			JWTSecret: envStr("OPSNEXUS_JWT_SECRET", "opsnexus-dev-secret-do-not-use-in-production"),
		},
		LLM: LLMConfig{
			APIKey:  envStr("OPSNEXUS_LLM_API_KEY", ""),
			Model:   envStr("OPSNEXUS_LLM_MODEL", "deepseek-chat"),
			BaseURL: envStr("OPSNEXUS_LLM_BASE_URL", "https://api.deepseek.com"),
			Timeout: envInt("OPSNEXUS_LLM_TIMEOUT", 60),
			// 留空则回退到通用 OpenAI embeddings 端点；接 OpenAI/兼容服务时只需填 model。
			EmbeddingModel:   envStr("OPSNEXUS_LLM_EMBEDDING_MODEL", "text-embedding-3-small"),
			EmbeddingBaseURL: envStr("OPSNEXUS_LLM_EMBEDDING_BASE_URL", ""),
			// 生成参数(留空则引擎用内置默认:温度0.7/上限1024/并发32)
			GenTemperature: float64Env("OPSNEXUS_LLM_GEN_TEMPERATURE", 0),
			GenMaxTokens:   envInt("OPSNEXUS_LLM_GEN_MAX_TOKENS", 0),
			MaxInflight:    envInt("OPSNEXUS_LLM_MAX_INFLIGHT", 0),
			DefaultSystemPrompt: envStr("OPSNEXUS_LLM_DEFAULT_SYSTEM_PROMPT",
				"你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。"),
		},
		RedisAddr:     envStr("OPSNEXUS_REDIS_ADDR", "localhost:6379"),		RedisPoolSize: envInt("OPSNEXUS_REDIS_POOL_SIZE", 50),
		LDAP: LDAPConfig{
			Enabled:    envBool("OPSNEXUS_LDAP_ENABLED", false),
			Addr:       envStr("OPSNEXUS_LDAP_ADDR", "ldap://localhost:389"),
			BaseDN:     envStr("OPSNEXUS_LDAP_BASE_DN", "dc=example,dc=org"),
			BindDN:     envStr("OPSNEXUS_LDAP_BIND_DN", "cn=admin,dc=example,dc=org"),
			BindPass:   envStr("OPSNEXUS_LDAP_BIND_PASS", ""),
			UserFilter: envStr("OPSNEXUS_LDAP_USER_FILTER", "(uid=%s)"),
		},
		SSO: SSOConfig{
			Enabled:      envBool("OPSNEXUS_SSO_ENABLED", false),
			Provider:     envStr("OPSNEXUS_SSO_PROVIDER", "oidc"),
			ClientID:     envStr("OPSNEXUS_SSO_CLIENT_ID", ""),
			ClientSecret: envStr("OPSNEXUS_SSO_CLIENT_SECRET", ""),
			RedirectURL:  envStr("OPSNEXUS_SSO_REDIRECT_URL", "http://localhost:8080/api/v1/auth/callback"),
			IssuerURL:    envStr("OPSNEXUS_SSO_ISSUER_URL", ""),
		},
		Tunnel: TunnelConfig{
			AuthSecret: envStr("OPSNEXUS_TUNNEL_AUTH_SECRET", ""),
			Insecure:   envBool("OPSNEXUS_TUNNEL_INSECURE", false),
		},
		FileStore: FileStoreConfig{
			Dir:         envStr("OPSNEXUS_FILE_STORE_DIR", "./data/files"),
			TokenSecret: envStr("OPSNEXUS_FILE_TOKEN_SECRET", ""),
		},
	}

	cfg.Core.Addr = fmt.Sprintf(":%d", cfg.Core.Port)

	// Postgres 连接池配置
	cfg.Postgres = PostgresConfig{
		MaxConns:        envInt("OPSNEXUS_POSTGRES_MAX_CONNS", 100),
		MaxIdleConns:    envInt("OPSNEXUS_POSTGRES_MAX_IDLE_CONNS", 20),
		ConnMaxLifetime: envDuration("OPSNEXUS_POSTGRES_CONN_MAX_LIFETIME", 30*time.Minute),
	}

	// TaskQueue 配置
	cfg.TaskQueue = TaskQueueConfig{
		QueueSize: envInt("OPSNEXUS_TASK_QUEUE_SIZE", 1000),
		Workers:   envInt("OPSNEXUS_TASK_QUEUE_WORKERS", 4),
	}

	// Upgrade 配置
	cfg.Upgrade = UpgradeConfig{
		DefaultBatchSize:     envInt("OPSNEXUS_UPGRADE_DEFAULT_BATCH_SIZE", 10),
		DefaultBatchInterval: envDuration("OPSNEXUS_UPGRADE_DEFAULT_BATCH_INTERVAL", 60*time.Second),
	}

	// 多模型路由池(P3): OPSNEXUS_LLM_PROVIDERS 为 JSON 数组, 如
	// [{"name":"aliyun","api_key":"sk-xxx","model":"qwen-max","base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","weight":80}]
	if raw := os.Getenv("OPSNEXUS_LLM_PROVIDERS"); raw != "" {
		var ps []LLMProviderConfig
		if err := json.Unmarshal([]byte(raw), &ps); err != nil {
			slog.Warn("invalid OPSNEXUS_LLM_PROVIDERS json, ignored", "err", err)
		} else {
			cfg.LLM.Providers = ps
		}
	}

	return cfg, nil
}

func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func float64Env(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
