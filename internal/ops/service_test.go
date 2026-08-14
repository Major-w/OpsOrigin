package ops

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opsorigin/core/pkg/config"
)

// 注意: handleStop/handleRestart 会向进程发送 SIGTERM/SIGHUP,
// 会杀死测试进程, 因此这里只测不发信号的 health/status 路径。

func TestHealth_WithNilDB_Degraded(t *testing.T) {
	s := NewService(&config.Config{}, nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest("GET", "/api/v1/ops/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("db=nil 时 postgresql=unavailable 应返回 503, got %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["status"] != "degraded" {
		t.Errorf("overall 应为 degraded, got %v", body["status"])
	}
	modules, ok := body["modules"].(map[string]any)
	if !ok {
		t.Fatalf("modules 字段缺失: %v", body)
	}
	if modules["postgresql"] != "unavailable" {
		t.Errorf("postgresql 模块状态应为 unavailable: %v", modules)
	}
	if modules["core"] != "running" || modules["api"] != "running" {
		t.Errorf("core/api 应 running: %v", modules)
	}
	if body["version"] == "" {
		t.Error("version 字段缺失")
	}
}

func TestStatus_WithNilDB(t *testing.T) {
	s := NewService(&config.Config{}, nil)
	rec := httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest("GET", "/api/v1/ops/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status 应 200, got %d", rec.Code)
	}
	var hs HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &hs); err != nil {
		t.Fatalf("json: %v", err)
	}
	if hs.Status != "running" {
		t.Errorf("status 应为 running: %v", hs.Status)
	}
	if hs.Modules["postgresql"] != "unavailable" {
		t.Errorf("postgresql 应为 unavailable: %v", hs.Modules)
	}
	if hs.Uptime == "" {
		t.Error("uptime 缺失")
	}
}

func TestRoutesRegistered(t *testing.T) {
	s := NewService(&config.Config{}, nil)
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	// 路由注册后应能直接 serve, 不存在 panic
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ops/health", nil))
	if rec.Code == http.StatusNotFound {
		t.Fatal("health 路由未注册")
	}
}
