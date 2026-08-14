package config

import (
	"strings"
	"testing"
)

func strongSecret(prefix string) string {
	// 64 字节, 非占位、非默认
	return prefix + "-" + strings.Repeat("a1b2c3d4", 7)
}

func TestValidateSecrets_DevelopmentSkips(t *testing.T) {
	cfg := &Config{Env: "development"}
	cfg.Auth.JWTSecret = "opsorigin-dev-secret-do-not-use-in-production"
	cfg.Tunnel.AuthSecret = "change-me-to-another-random-64-char-string"
	cfg.FileStore.TokenSecret = ""
	if err := cfg.ValidateSecrets(); err != nil {
		t.Fatalf("development 环境不应校验凭据: %v", err)
	}
}

func TestValidateSecrets_ProductionRejectsJWTDefaults(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"JWT 内置默认值", func(c *Config) { c.Auth.JWTSecret = "opsorigin-dev-secret-do-not-use-in-production" }},
		{"JWT change-me 占位", func(c *Config) { c.Auth.JWTSecret = "change-me-to-a-random-64-char-string" }},
		{"JWT 空", func(c *Config) { c.Auth.JWTSecret = "" }},
		{"JWT 过短", func(c *Config) { c.Auth.JWTSecret = "short-secret" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Env: "production"}
			cfg.Auth.JWTSecret = strongSecret("jwt")
			cfg.Tunnel.AuthSecret = strongSecret("tunnel")
			cfg.FileStore.TokenSecret = strongSecret("file")
			cfg.Auth.ServiceToken = strongSecret("svc")
			tc.mutate(cfg)
			err := cfg.ValidateSecrets()
			if err == nil {
				t.Fatalf("production 应拒绝弱 JWT 密钥, 但校验通过")
			}
			if !strings.Contains(err.Error(), "OPSORIGIN_JWT_SECRET") {
				t.Fatalf("错误信息应指明 JWT 密钥, got: %v", err)
			}
		})
	}
}

func TestValidateSecrets_ProductionRejectsWeakOptionalSecrets(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"Tunnel change-me 占位", func(c *Config) { c.Tunnel.AuthSecret = "change-me-to-another-random-64-char-string" }},
		{"Tunnel 过短", func(c *Config) { c.Tunnel.AuthSecret = "tiny" }},
		{"FileToken 占位", func(c *Config) { c.FileStore.TokenSecret = "change-me-file-token" }},
		{"ServiceToken 占位", func(c *Config) { c.Auth.ServiceToken = "change-me-internal-token" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Env: "production"}
			cfg.Auth.JWTSecret = strongSecret("jwt")
			cfg.Tunnel.AuthSecret = strongSecret("tunnel")
			cfg.FileStore.TokenSecret = strongSecret("file")
			cfg.Auth.ServiceToken = strongSecret("svc")
			tc.mutate(cfg)
			if err := cfg.ValidateSecrets(); err == nil {
				t.Fatalf("production 应拒绝非空但为占位/过短的通道密钥")
			}
		})
	}
}

func TestValidateSecrets_ProductionAllowsEmptyOptionalSecrets(t *testing.T) {
	// 空 = fail-closed (功能关闭), 生产环境允许不启用这些通道
	cfg := &Config{Env: "production"}
	cfg.Auth.JWTSecret = strongSecret("jwt")
	cfg.Tunnel.AuthSecret = ""
	cfg.FileStore.TokenSecret = ""
	cfg.Auth.ServiceToken = ""
	if err := cfg.ValidateSecrets(); err != nil {
		t.Fatalf("production + 空通道密钥(功能关闭)应通过: %v", err)
	}
}

func TestValidateSecrets_ProductionAcceptsStrong(t *testing.T) {
	cfg := &Config{Env: "production"}
	cfg.Auth.JWTSecret = strongSecret("jwt")
	cfg.Tunnel.AuthSecret = strongSecret("tunnel")
	cfg.FileStore.TokenSecret = strongSecret("file")
	cfg.Auth.ServiceToken = strongSecret("svc")
	if err := cfg.ValidateSecrets(); err != nil {
		t.Fatalf("production + 强密钥应通过: %v", err)
	}
}

func TestLoad_ProductionWeakSecretFails(t *testing.T) {
	t.Setenv("OPSORIGIN_ENV", "production")
	t.Setenv("OPSORIGIN_JWT_SECRET", "change-me-to-a-random-64-char-string")
	// 其余密钥给强值, 确保失败来自 JWT
	t.Setenv("OPSORIGIN_TUNNEL_AUTH_SECRET", strongSecret("tunnel"))
	t.Setenv("OPSORIGIN_FILE_TOKEN_SECRET", strongSecret("file"))
	t.Setenv("OPSORIGIN_SERVICE_TOKEN", strongSecret("svc"))

	_, err := Load()
	if err == nil {
		t.Fatal("production + change-me JWT 应返回错误")
	}
	if !strings.Contains(err.Error(), "OPSORIGIN_JWT_SECRET") {
		t.Fatalf("错误信息应指明具体密钥, got: %v", err)
	}
}

func TestLoad_ProductionStrongSecretsPasses(t *testing.T) {
	t.Setenv("OPSORIGIN_ENV", "production")
	t.Setenv("OPSORIGIN_JWT_SECRET", strongSecret("jwt"))
	t.Setenv("OPSORIGIN_TUNNEL_AUTH_SECRET", strongSecret("tunnel"))
	t.Setenv("OPSORIGIN_FILE_TOKEN_SECRET", strongSecret("file"))
	t.Setenv("OPSORIGIN_SERVICE_TOKEN", strongSecret("svc"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("production + 强密钥应通过 Load: %v", err)
	}
	if !cfg.IsProduction() {
		t.Fatal("IsProduction 应为 true")
	}
}
