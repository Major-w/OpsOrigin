package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Core      CoreConfig   `json:"core"`
	Proxy     ProxyConfig  `json:"proxy"`
	Agent     AgentConfig  `json:"agent"`
	AgentWS   AgentWSConfig `json:"agent_ws"`
	TLS       TLSConfig    `json:"tls"`
	Outbox    OutboxConfig `json:"outbox"`
	CMDB      CMDBConfig   `json:"cmdb"`
	Auth      AuthConfig   `json:"auth"`
	LLM       LLMConfig    `json:"llm"`
	RedisAddr string       `json:"redis_addr"`
	LDAP      LDAPConfig   `json:"ldap"`
	SSO       SSOConfig    `json:"sso"`
}

type LDAPConfig struct {
	Enabled  bool   `json:"enabled"`
	Addr     string `json:"addr"`
	BaseDN   string `json:"base_dn"`
	BindDN   string `json:"bind_dn"`
	BindPass string `json:"bind_pass"`
	UserFilter string `json:"user_filter"`
}

type SSOConfig struct {
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider"`
	ClientID    string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURL string `json:"redirect_url"`
	IssuerURL   string `json:"issuer_url"`
}

type CoreConfig struct {
	Port       int    `json:"port"`
	Addr       string `json:"addr"`
	GRPCListen string `json:"grpc_listen"`
}

type ProxyConfig struct {
	ID         string `json:"id"`
	CoreAddr   string `json:"core_addr"`
	Listen     string `json:"listen"`
	GRPCListen string `json:"grpc_listen"`
}

type AgentConfig struct {
	ProxyAddr string `json:"proxy_addr"`
}

// AgentWSConfig 是 Agent WebSocket 长连接服务的配置。
// Agent 通信走独立端口(默认 5588),与主服务 8080 隔离,避免主服务故障影响 Agent 数据上报。
type AgentWSConfig struct {
	Port     int    `json:"port"`      // WebSocket 监听端口,默认 5588
	Enabled  bool   `json:"enabled"`   // 是否启用 Agent WebSocket 长连接
	UseTLS   bool   `json:"use_tls"`   // 是否启用 TLS(WSS)
	CertFile string `json:"cert_file"` // TLS 证书文件路径
	KeyFile  string `json:"key_file"`  // TLS 私钥文件路径
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

type LLMConfig struct {
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	BaseURL string `json:"base_url"`
	Timeout int    `json:"timeout"`
}

func Load() (*Config, error) {
	loadDotEnv()

	cfg := &Config{
		Core: CoreConfig{
			Port:       envInt("OPSNEXUS_CORE_PORT", 8080),
			GRPCListen: envStr("OPSNEXUS_CORE_GRPC_LISTEN", ":9091"),
		},
		Proxy: ProxyConfig{
			ID:         envStr("OPSNEXUS_PROXY_ID", "proxy-001"),
			CoreAddr:   envStr("OPSNEXUS_PROXY_CORE_ADDR", "localhost:8080"),
			Listen:     envStr("OPSNEXUS_PROXY_LISTEN", ":9090"),
			GRPCListen: envStr("OPSNEXUS_PROXY_GRPC_LISTEN", ":9092"),
		},
		Agent: AgentConfig{
			ProxyAddr: envStr("OPSNEXUS_AGENT_PROXY_ADDR", "localhost:9090"),
		},
		AgentWS: AgentWSConfig{
			Port:     envInt("OPSNEXUS_AGENT_WS_PORT", 5588),
			Enabled:  envBool("OPSNEXUS_AGENT_WS_ENABLED", true),
			UseTLS:   envBool("OPSNEXUS_AGENT_WS_TLS", false),
			CertFile: envStr("OPSNEXUS_AGENT_WS_CERT", ""),
			KeyFile:  envStr("OPSNEXUS_AGENT_WS_KEY", ""),
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
		},
		RedisAddr: envStr("OPSNEXUS_REDIS_ADDR", "localhost:6379"),
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
	}

	cfg.Core.Addr = fmt.Sprintf(":%d", cfg.Core.Port)

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

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
