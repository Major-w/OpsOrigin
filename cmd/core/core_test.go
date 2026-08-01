package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/ai"
	"github.com/opsorigin/core/internal/audit"
	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/itsm"
	"github.com/opsorigin/core/internal/monitor"
	"github.com/opsorigin/core/internal/user"
	"github.com/opsorigin/core/internal/ws"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
)

func newTestServer(t *testing.T) (*Server, *config.Config, string) {
	t.Helper()

	cfg := &config.Config{}
	cfg.Core.Port = 0
	cfg.Core.Addr = ":0"
	cfg.Auth.JWTSecret = "test-secret"
	cfg.LLM = config.LLMConfig{APIKey: "", Model: "dummy", BaseURL: "http://localhost:9999", Timeout: 1}

	authProv := auth.NewProvider(cfg.Auth.JWTSecret, nil, nil)
	auditSvc := audit.NewService(cfg, nil)
	wsHub := ws.NewHub()

	server := NewServer(cfg)
	server.WithMiddleware(authProv.Middleware)
	server.WithMiddleware(auditSvc.Middleware)
	server.WithMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/ws" {
				wsHub.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	token, err := authProv.GenerateToken("test-user", "default", "admin", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	return server, cfg, token
}

func buildHandler(server *Server) http.Handler {
	var handler http.Handler = server.mux
	for i := len(server.middlewares) - 1; i >= 0; i-- {
		handler = server.middlewares[i](handler)
	}
	return handler
}

func registerAllServices(server *Server, cfg *config.Config, authProv *auth.Provider) {
	auditSvc := audit.NewService(cfg, nil)
	server.WithMiddleware(auditSvc.Middleware)
	server.RegisterService(auditSvc)

	itsmSvc := itsm.NewService(cfg, nil)
	server.RegisterService(itsmSvc)

	monitorSvc := monitor.NewService(cfg, nil)
	server.RegisterService(monitorSvc)

	userSvc := user.NewService(nil, authProv)
	server.RegisterService(userSvc)
}

func registerAIService(server *Server, cfg *config.Config) {
	aiSvc := ai.NewService(cfg, nil, nil, nil)
	aiSvc.Start(nil)
	server.RegisterService(aiSvc)
}

func request(handler http.Handler, method, path, body, token string) *http.Response {
	var reqBody *strings.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	} else {
		reqBody = strings.NewReader("")
	}

	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Trace-ID", "test-trace-"+path)
	req.Header.Set("X-Principal-Type", "HUMAN")
	req.Header.Set("X-Principal-ID", "test-user")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w.Result()
}

func TestAuthLogin(t *testing.T) {
	handler := http.NewServeMux()
	authProv := auth.NewProvider("test-secret", nil, nil)
	userSvc := user.NewService(nil, authProv)
	userSvc.RegisterRoutes(handler)

	body := `{"username":"admin","password":"admin123"}`
	resp := request(handler, "POST", "/api/v1/auth/login", body, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var data struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if data.Token == "" {
		t.Fatal("expected non-empty token")
	}
	if data.Username != "admin" {
		t.Fatalf("expected admin, got %s", data.Username)
	}
	if data.Role != "admin" {
		t.Fatalf("expected admin role, got %s", data.Role)
	}
}

func TestAuthLoginInvalid(t *testing.T) {
	handler := http.NewServeMux()
	authProv := auth.NewProvider("test-secret", nil, nil)
	userSvc := user.NewService(nil, authProv)
	userSvc.RegisterRoutes(handler)

	body := `{"username":"admin","password":"wrong"}`
	resp := request(handler, "POST", "/api/v1/auth/login", body, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestCMDBListEmpty(t *testing.T) {
	server, _, token := newTestServer(t)
	pgRepo, _ := pg.NewRepository("")
	if pgRepo == nil {
		t.Skip("PG not available, skipping CMDB test")
	}
	dgClient, _ := dgraph.NewClient("")
	cmdbSvc := cmdb.NewService(&config.Config{}, pgRepo, dgClient)
	cmdbSvc.RegisterRoutes(server.mux)
	handler := buildHandler(server)

	resp := request(handler, "GET", "/api/v1/cmdb/ci", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var items []any
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
}

func TestITSMListEmpty(t *testing.T) {
	server, cfg, token := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := buildHandler(server)
	resp := request(handler, "GET", "/api/v1/itsm/tickets", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var items []any
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
}

func TestAIAgents(t *testing.T) {
	server, cfg, token := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	registerAIService(server, cfg)
	handler := buildHandler(server)
	resp := request(handler, "POST", "/api/v1/ai/agents", `{"name":"test-agent","model_name":"deepseek"}`, token)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 201 or 200, got %d", resp.StatusCode)
	}
}

func TestAITools(t *testing.T) {
	server, cfg, token := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	registerAIService(server, cfg)
	handler := buildHandler(server)
	resp := request(handler, "GET", "/api/v1/ai/tools", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var tools []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tools); err != nil {
		t.Fatal(err)
	}
}

func TestMonitorTargets(t *testing.T) {
	server, cfg, token := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := buildHandler(server)
	resp := request(handler, "GET", "/api/v1/monitor/targets", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUnauthenticatedRequest(t *testing.T) {
	server, cfg, _ := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := buildHandler(server)
	resp := request(handler, "GET", "/api/v1/itsm/tickets", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuditEvents(t *testing.T) {
	server, cfg, token := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := buildHandler(server)
	resp := request(handler, "GET", "/api/v1/audit/events", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMonitorProbe(t *testing.T) {
	server, cfg, token := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := buildHandler(server)
	body := `{"address":"10.0.0.1","port":80,"protocol":"tcp"}`
	resp := request(handler, "POST", "/api/v1/monitor/probe", body, token)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("unexpected 401")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	server, cfg, token := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := buildHandler(server)
	resp := request(handler, "GET", "/api/v1/monitor/alert-stats", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestITSMRiskLevels(t *testing.T) {
	server, cfg, token := newTestServer(t)
	registerAllServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := buildHandler(server)
	for _, risk := range []string{"low", "medium", "high", "critical"} {
		body := `{"title":"test-` + risk + `","description":"test","risk_level":"` + risk + `","change_plan":"test"}`
		resp := request(handler, "POST", "/api/v1/itsm/tickets", body, token)
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
			t.Fatalf("risk %s: expected 201 or 409, got %d", risk, resp.StatusCode)
		}
	}
}

func TestHealthCheck(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	resp := request(handler, "GET", "/api/v1/health", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var data map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if data["status"] != "ok" {
		t.Fatalf("expected ok, got %s", data["status"])
	}
}
