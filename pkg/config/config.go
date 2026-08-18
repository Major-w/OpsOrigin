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
	S3            S3Config        `json:"s3"`         // 对象存储(MinIO/COS/OSS/S3): ITSM 附件等二进制落对象存储
	Postgres      PostgresConfig  `json:"postgres"`
	TaskQueue     TaskQueueConfig `json:"task_queue"`
	Upgrade       UpgradeConfig   `json:"upgrade"`
	// Env 运行环境: development | production (OPSORIGIN_ENV, 默认 development)。
	// production 下启动时强制校验凭据强度, 默认/占位密钥直接拒绝启动 (fail-fast)。
	Env string `json:"env"`
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

// S3Config 是对象存储配置(S3 协议兼容: MinIO/腾讯云COS/阿里OSS/亚马逊S3 通用)。
// Endpoint 为空时关闭对象存储, 附件等二进制回退数据库存储。
type S3Config struct {
	Endpoint  string `json:"endpoint"`   // 如 minio:9000 或 s3.xxx.com
	AccessKey string `json:"access_key"` // AccessKey / MINIO_ROOT_USER
	SecretKey string `json:"secret_key"` // SecretKey / MINIO_ROOT_PASSWORD
	Bucket    string `json:"bucket"`     // 桶名(不存在自动创建)
	UseSSL    bool   `json:"use_ssl"`    // 走 HTTPS(云端对象存储一般为 true)
	Region    string `json:"region"`     // 区域(自建 MinIO 填 us-east-1 即可)
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
	// ServiceToken 内部服务间调用的共享密钥。仅当请求携带 X-Internal-Token
	// 且与此值一致时,才采信 X-Principal-* 断言头(代表某用户/AI 主体执行)。
	// 留空 = 关闭断言头通道(fail-closed),浏览器请求一律从 JWT 派生主体。
	ServiceToken string `json:"service_token,omitempty"`
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
	Name    string `json:"name"` // 池内唯一标识(Agent.model_name 可填此名)
	APIKey  string `json:"api_key"`
	Model   string `json:"model"` // 该 provider 的默认模型
	BaseURL string `json:"base_url"`
	Weight  int    `json:"weight"` // 路由权重(越大越优先, 默认 50)
}

type PostgresConfig struct {
	MaxConns        int           `json:"max_conns"`         // OPSORIGIN_POSTGRES_MAX_CONNS, 默认 100 (F4-A 规模化)
	MaxIdleConns    int           `json:"max_idle_conns"`    // OPSORIGIN_POSTGRES_MAX_IDLE_CONNS, 默认 20 (F4-A 规模化)
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"` // OPSORIGIN_POSTGRES_CONN_MAX_LIFETIME, 默认 30m
}

type TaskQueueConfig struct {
	QueueSize int `json:"queue_size"` // OPSORIGIN_TASK_QUEUE_SIZE, 默认 1000
	Workers   int `json:"workers"`    // OPSORIGIN_TASK_QUEUE_WORKERS, 默认 4
}

type UpgradeConfig struct {
	DefaultBatchSize     int           `json:"default_batch_size"`     // OPSORIGIN_UPGRADE_DEFAULT_BATCH_SIZE, 默认 10
	DefaultBatchInterval time.Duration `json:"default_batch_interval"` // OPSORIGIN_UPGRADE_DEFAULT_BATCH_INTERVAL, 默认 60s
}

func Load() (*Config, error) {
	loadDotEnv()

	cfg := &Config{
		Core: CoreConfig{
			Port:       envInt("OPSORIGIN_CORE_PORT", 8080),
			GRPCListen: envStr("OPSORIGIN_CORE_GRPC_LISTEN", ":9091"),
			PublicURL:  envStr("OPSORIGIN_CORE_PUBLIC_URL", ""),
			VMAddr:     envStr("OPSORIGIN_CORE_VM_ADDR", ""),
		},
		Proxy: ProxyConfig{
			ID:         envStr("OPSORIGIN_PROXY_ID", "proxy-001"),
			CoreAddr:   envStr("OPSORIGIN_PROXY_CORE_ADDR", "localhost:8080"),
			Listen:     envStr("OPSORIGIN_PROXY_LISTEN", ":9090"),
			GRPCListen: envStr("OPSORIGIN_PROXY_GRPC_LISTEN", ":9092"),
			FileListen: envStr("OPSORIGIN_PROXY_FILE_LISTEN", ""),
		},
		Agent: AgentConfig{
			ProxyAddr: envStr("OPSORIGIN_AGENT_PROXY_ADDR", "localhost:9090"),
		},
		TLS: TLSConfig{
			Enabled:  envBool("OPSORIGIN_TLS_ENABLED", false),
			CertFile: envStr("OPSORIGIN_TLS_CERT", ""),
			KeyFile:  envStr("OPSORIGIN_TLS_KEY", ""),
		},
		Outbox: OutboxConfig{
			PollInterval: envDuration("OPSORIGIN_OUTBOX_POLL_INTERVAL", 1*time.Second),
			BatchSize:    envInt("OPSORIGIN_OUTBOX_BATCH_SIZE", 100),
		},
		CMDB: CMDBConfig{
			PostgresDSN:  envStr("OPSORIGIN_POSTGRES_DSN", "postgres://opsorigin:opsorigin@localhost:5432/opsorigin?sslmode=disable"),
			DgraphTarget: envStr("OPSORIGIN_DGRAPH_TARGET", "localhost:9080"),
			DgraphAPIKey: envStr("OPSORIGIN_DGRAPH_API_KEY", ""),
		},
		Auth: AuthConfig{
			JWTSecret:    envStr("OPSORIGIN_JWT_SECRET", "opsorigin-dev-secret-do-not-use-in-production"),
			ServiceToken: envStr("OPSORIGIN_SERVICE_TOKEN", ""),
		},
		LLM: LLMConfig{
			APIKey:  envStr("OPSORIGIN_LLM_API_KEY", ""),
			Model:   envStr("OPSORIGIN_LLM_MODEL", "deepseek-chat"),
			BaseURL: envStr("OPSORIGIN_LLM_BASE_URL", "https://api.deepseek.com"),
			Timeout: envInt("OPSORIGIN_LLM_TIMEOUT", 60),
			// 留空则回退到通用 OpenAI embeddings 端点；接 OpenAI/兼容服务时只需填 model。
			EmbeddingModel:   envStr("OPSORIGIN_LLM_EMBEDDING_MODEL", "text-embedding-3-small"),
			EmbeddingBaseURL: envStr("OPSORIGIN_LLM_EMBEDDING_BASE_URL", ""),
			// 生成参数(留空则引擎用内置默认:温度0.7/上限1024/并发32)
			GenTemperature: float64Env("OPSORIGIN_LLM_GEN_TEMPERATURE", 0),
			GenMaxTokens:   envInt("OPSORIGIN_LLM_GEN_MAX_TOKENS", 0),
			MaxInflight:    envInt("OPSORIGIN_LLM_MAX_INFLIGHT", 0),
			DefaultSystemPrompt: envStr("OPSORIGIN_LLM_DEFAULT_SYSTEM_PROMPT",
				"你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。"),
		},
		RedisAddr: envStr("OPSORIGIN_REDIS_ADDR", "localhost:6379"), RedisPoolSize: envInt("OPSORIGIN_REDIS_POOL_SIZE", 50),
		LDAP: LDAPConfig{
			Enabled:    envBool("OPSORIGIN_LDAP_ENABLED", false),
			Addr:       envStr("OPSORIGIN_LDAP_ADDR", "ldap://localhost:389"),
			BaseDN:     envStr("OPSORIGIN_LDAP_BASE_DN", "dc=example,dc=org"),
			BindDN:     envStr("OPSORIGIN_LDAP_BIND_DN", "cn=admin,dc=example,dc=org"),
			BindPass:   envStr("OPSORIGIN_LDAP_BIND_PASS", ""),
			UserFilter: envStr("OPSORIGIN_LDAP_USER_FILTER", "(uid=%s)"),
		},
		SSO: SSOConfig{
			Enabled:      envBool("OPSORIGIN_SSO_ENABLED", false),
			Provider:     envStr("OPSORIGIN_SSO_PROVIDER", "oidc"),
			ClientID:     envStr("OPSORIGIN_SSO_CLIENT_ID", ""),
			ClientSecret: envStr("OPSORIGIN_SSO_CLIENT_SECRET", ""),
			RedirectURL:  envStr("OPSORIGIN_SSO_REDIRECT_URL", "http://localhost:8080/api/v1/auth/callback"),
			IssuerURL:    envStr("OPSORIGIN_SSO_ISSUER_URL", ""),
		},
		Tunnel: TunnelConfig{
			AuthSecret: envStr("OPSORIGIN_TUNNEL_AUTH_SECRET", ""),
			Insecure:   envBool("OPSORIGIN_TUNNEL_INSECURE", false),
		},
		FileStore: FileStoreConfig{
			Dir:         envStr("OPSORIGIN_FILE_STORE_DIR", "./data/files"),
			TokenSecret: envStr("OPSORIGIN_FILE_TOKEN_SECRET", ""),
		},
		S3: S3Config{
			Endpoint:  envStr("OPSORIGIN_S3_ENDPOINT", ""),
			AccessKey: envStr("OPSORIGIN_S3_ACCESS_KEY", ""),
			SecretKey: envStr("OPSORIGIN_S3_SECRET_KEY", ""),
			Bucket:    envStr("OPSORIGIN_S3_BUCKET", "opsorigin"),
			UseSSL:    envBool("OPSORIGIN_S3_USE_SSL", false),
			Region:    envStr("OPSORIGIN_S3_REGION", "us-east-1"),
		},
	}

	cfg.Core.Addr = fmt.Sprintf(":%d", cfg.Core.Port)

	// Postgres 连接池配置
	cfg.Postgres = PostgresConfig{
		MaxConns:        envInt("OPSORIGIN_POSTGRES_MAX_CONNS", 100),
		MaxIdleConns:    envInt("OPSORIGIN_POSTGRES_MAX_IDLE_CONNS", 20),
		ConnMaxLifetime: envDuration("OPSORIGIN_POSTGRES_CONN_MAX_LIFETIME", 30*time.Minute),
	}

	// TaskQueue 配置
	cfg.TaskQueue = TaskQueueConfig{
		QueueSize: envInt("OPSORIGIN_TASK_QUEUE_SIZE", 1000),
		Workers:   envInt("OPSORIGIN_TASK_QUEUE_WORKERS", 4),
	}

	// Upgrade 配置
	cfg.Upgrade = UpgradeConfig{
		DefaultBatchSize:     envInt("OPSORIGIN_UPGRADE_DEFAULT_BATCH_SIZE", 10),
		DefaultBatchInterval: envDuration("OPSORIGIN_UPGRADE_DEFAULT_BATCH_INTERVAL", 60*time.Second),
	}

	// 多模型路由池(P3): OPSORIGIN_LLM_PROVIDERS 为 JSON 数组, 如
	// [{"name":"aliyun","api_key":"sk-xxx","model":"qwen-max","base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","weight":80}]
	if raw := os.Getenv("OPSORIGIN_LLM_PROVIDERS"); raw != "" {
		var ps []LLMProviderConfig
		if err := json.Unmarshal([]byte(raw), &ps); err != nil {
			slog.Warn("invalid OPSORIGIN_LLM_PROVIDERS json, ignored", "err", err)
		} else {
			cfg.LLM.Providers = ps
		}
	}

	cfg.Env = envStr("OPSORIGIN_ENV", "development")

	// 生产环境凭据强度校验 (fail-fast): 默认/占位/弱密钥直接拒绝启动。
	if err := cfg.ValidateSecrets(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// IsProduction 报告当前是否为生产环境 (OPSORIGIN_ENV=production)。
func (c *Config) IsProduction() bool { return c.Env == "production" }

// ValidateSecrets 校验凭据强度, 仅在生产环境生效。
// 命中默认/占位/过短密钥时返回错误, 由调用方 (main) 以非零退出终止启动,
// 防止带默认密钥上线后 JWT 可被伪造、隧道可被未授权节点接入。
//
// 校验策略分两档:
//   - 必填主密钥 (OPSORIGIN_JWT_SECRET): 认证系统依赖, 空/占位/过短一律拒绝;
//   - 可选通道密钥 (隧道/内部令牌/文件令牌): 空 = fail-closed (功能关闭, 安全),
//     但一旦非空就必须是强随机密钥 —— 用可预测的占位密钥比不启用更危险。
func (c *Config) ValidateSecrets() error {
	if !c.IsProduction() {
		return nil
	}
	if isWeakSecret(c.Auth.JWTSecret) {
		return fmt.Errorf(
			"production 环境必须显式配置强随机 JWT 密钥(至少 32 字节): OPSORIGIN_JWT_SECRET")
	}
	type secret struct{ name, value string }
	optional := []secret{
		{"OPSORIGIN_TUNNEL_AUTH_SECRET", c.Tunnel.AuthSecret},
		{"OPSORIGIN_SERVICE_TOKEN", c.Auth.ServiceToken},
		{"OPSORIGIN_FILE_TOKEN_SECRET", c.FileStore.TokenSecret},
	}
	for _, s := range optional {
		if s.value != "" && isWeakSecret(s.value) {
			return fmt.Errorf(
				"production 环境禁止使用默认/占位/过短密钥: %s 必须显式配置强随机密钥(至少 32 字节)", s.name)
		}
	}
	return nil
}

// isWeakSecret 判定一个密钥是否可被当作"未配置": 空、占位(change-me*)、
// 内置默认(opsorigin-dev-* / do-not-use-in-production)或长度不足 32 字节。
func isWeakSecret(v string) bool {
	lower := strings.ToLower(v)
	return v == "" ||
		strings.HasPrefix(lower, "change-me") ||
		strings.Contains(lower, "do-not-use-in-production") ||
		len(v) < 32
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
