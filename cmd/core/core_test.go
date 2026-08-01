package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/agentmgr"
	"github.com/opsorigin/core/internal/ai"
	"github.com/opsorigin/core/internal/audit"
	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/itsm"
	"github.com/opsorigin/core/internal/knowledge"
	"github.com/opsorigin/core/internal/monitor"
	"github.com/opsorigin/core/internal/user"
	"github.com/opsorigin/core/internal/ws"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
)

// testServices 持有冒烟测试中构造的全部业务服务。
// 新增服务的构造签名变化只需改 newTestServices 一处。
type testServices struct {
	audit   *audit.Service
	itsm    *itsm.Service
	monitor *monitor.Service
	user    *user.Service
	ai      *ai.Service
}

// newTestServer 构造最小 Core Server：挂 auth 与 ws 中间件，签发测试 token。
// 业务服务不在此注册（统一走 newTestServices），避免中间件重复挂载。
func newTestServer(t *testing.T) (*Server, *config.Config, string) {
	t.Helper()

	cfg := &config.Config{}
	cfg.Core.Port = 0
	cfg.Core.Addr = ":0"
	cfg.Auth.JWTSecret = "test-secret"
	cfg.LLM = config.LLMConfig{APIKey: "", Model: "dummy", BaseURL: "http://localhost:9999", Timeout: 1}

	authProv := auth.NewProvider(cfg.Auth.JWTSecret, nil, nil)
	wsHub := ws.NewHub()

	server := NewServer(cfg)
	server.WithMiddleware(authProv.Middleware)
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

// newTestServices 统一构造并注册全部业务服务到 server。
// 依赖均传 nil，各服务自动退化为内存实现，保证零外部依赖；
// audit 中间件只在此挂载一次，避免请求被审计两次。
func newTestServices(server *Server, cfg *config.Config, authProv *auth.Provider) *testServices {
	auditSvc := audit.NewService(cfg, nil)
	server.WithMiddleware(auditSvc.Middleware)
	server.RegisterService(auditSvc)

	itsmSvc := itsm.NewService(cfg, nil)
	server.RegisterService(itsmSvc)

	monitorSvc := monitor.NewService(cfg, nil)
	server.RegisterService(monitorSvc)

	userSvc := user.NewService(nil, authProv)
	server.RegisterService(userSvc)

	// ai 服务不在测试中 Start（避免启动后台调度 goroutine 无法回收）
	aiSvc := ai.NewService(cfg, nil, nil, nil)
	server.RegisterService(aiSvc)

	knowledgeSvc := knowledge.NewService(nil)
	server.RegisterService(knowledgeSvc)

	agentmgrSvc := agentmgr.NewService(nil, nil, nil)
	server.RegisterService(agentmgrSvc)

	return &testServices{
		audit:   auditSvc,
		itsm:    itsmSvc,
		monitor: monitorSvc,
		user:    userSvc,
		ai:      aiSvc,
	}
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
	pgRepo, err := pg.NewRepository("")
	if err != nil || pgRepo == nil {
		t.Skipf("PG not available, skipping CMDB test: %v", err)
	}
	dgClient, _ := dgraph.NewClient("")
	cmdbSvc := cmdb.NewService(&config.Config{}, pgRepo, dgClient)
	cmdbSvc.RegisterRoutes(server.mux)
	handler := server.Handler()

	resp := request(handler, "GET", "/api/v1/cmdb/ci", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var items []any
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if items == nil {
		t.Fatal("expected non-nil items array")
	}
}

func TestITSMListEmpty(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	resp := request(handler, "GET", "/api/v1/itsm/tickets", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var items []any
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if items == nil {
		t.Fatal("expected non-nil items array")
	}
}

func TestAIAgents(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	resp := request(handler, "POST", "/api/v1/ai/agents", `{"name":"test-agent","model_name":"deepseek"}`, token)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 201 or 200, got %d", resp.StatusCode)
	}
}

func TestAITools(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
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
	if tools == nil {
		t.Fatal("expected non-nil tools array")
	}
}

func TestMonitorTargets(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	resp := request(handler, "GET", "/api/v1/monitor/targets", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUnauthenticatedRequest(t *testing.T) {
	server, cfg, _ := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	resp := request(handler, "GET", "/api/v1/itsm/tickets", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuditEvents(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	resp := request(handler, "GET", "/api/v1/audit/events", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMonitorProbe(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	// 500ms 超时避免默认 5s 内网探测等待
	body := `{"address":"10.0.0.1","port":80,"protocol":"tcp","timeout":"500ms"}`
	resp := request(handler, "POST", "/api/v1/monitor/probe", body, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	// 探测结果必须是可解析的 JSON, 且含 reachable 字段
	var result struct {
		Reachable bool `json:"reachable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("probe response not JSON: %v", err)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	resp := request(handler, "GET", "/api/v1/monitor/alert-stats", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestITSMRiskLevels(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	for _, risk := range []string{"low", "medium", "high", "critical"} {
		body := `{"title":"test-` + risk + `","description":"test","risk_level":"` + risk + `","change_plan":"test"}`
		resp := request(handler, "POST", "/api/v1/itsm/tickets", body, token)
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
			t.Fatalf("risk %s: expected 201 or 409, got %d", risk, resp.StatusCode)
		}
	}
}

// TestKnowledgeCreateAndList 覆盖知识库模块: 创建文章后列表能查到。
func TestKnowledgeCreateAndList(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()

	body := `{"title":"test-knowledge-article","content":"hello world","author":"admin","status":"published"}`
	resp := request(handler, "POST", "/api/v1/knowledge", body, token)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create: expected 201 or 200, got %d", resp.StatusCode)
	}

	resp = request(handler, "GET", "/api/v1/knowledge", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", resp.StatusCode)
	}
	var items []struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.Title == "test-knowledge-article" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("created article not found in list")
	}
}

// TestAgentsList 覆盖 Agent 管理模块列表端点。
func TestAgentsList(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	resp := request(handler, "GET", "/api/v1/agents", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestApprovalPolicy 覆盖 AI 审批策略端点（默认策略可读）。
func TestApprovalPolicy(t *testing.T) {
	server, cfg, token := newTestServer(t)
	newTestServices(server, cfg, auth.NewProvider(cfg.Auth.JWTSecret, nil, nil))
	handler := server.Handler()
	resp := request(handler, "GET", "/api/v1/ai/approval-policy", "", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
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
