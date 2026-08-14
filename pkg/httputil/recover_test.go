package httputil

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecoverHandler_Passthrough(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "1"})
	})
	h := RecoverHandler(next, testLogger())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("正常请求应透传 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok":"1"`) {
		t.Fatalf("正常请求应透传 body, got %s", rec.Body.String())
	}
}

func TestRecoverHandler_PanicReturns500(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	h := RecoverHandler(next, testLogger())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应返回 500, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应应为 JSON: %v", err)
	}
	if body["error"] == "" {
		t.Fatalf("响应应包含 error 字段, got %v", body)
	}
}

func TestRecoverHandler_PanicAfterWriteKeepsStatus(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"partial": "1"})
		panic("late boom")
	})
	h := RecoverHandler(next, testLogger())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/stream", nil))

	// 已开始写出后 panic: 不崩溃、不覆盖已写状态码
	if rec.Code != http.StatusOK {
		t.Fatalf("已写响应后的 panic 不应改写状态码, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"partial":"1"`) {
		t.Fatalf("已写 body 应保留, got %s", rec.Body.String())
	}
}
