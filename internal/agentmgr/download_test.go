package agentmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/token"
)

const testFileSecret = "test-file-secret"

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	s := NewService(nil)
	s.SetFileTokenSecret(testFileSecret)
	s.SetFileStoreDir(dir)
	s.SetServerBaseURL("http://srv:8080")
	if err := os.WriteFile(filepath.Join(dir, "f1.txt"), []byte("hello-opsorigin"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	return s, dir
}

func TestBuildFileURL(t *testing.T) {
	got := buildFileURL("http://srv:8080/api/v1/", "f1.txt", "tok123")
	want := "http://srv:8080/api/v1/files/f1.txt?token=tok123"
	if got != want {
		t.Fatalf("buildFileURL = %q, want %q", got, want)
	}
}

func TestHandleServeFile_Direct(t *testing.T) {
	s, _ := newTestService(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/files/{file_id}", s.handleServeFile)

	tok, err := token.Generate(testFileSecret, "f1.txt", "agent-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/f1.txt?token="+tok, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "hello-opsorigin" {
		t.Fatalf("body = %q", body)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatal("missing Accept-Ranges header (Range 续传不可用)")
	}
}

func TestHandleServeFile_MissingToken(t *testing.T) {
	s, _ := newTestService(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/files/{file_id}", s.handleServeFile)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/f1.txt", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleServeFile_ExpiredToken(t *testing.T) {
	s, _ := newTestService(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/files/{file_id}", s.handleServeFile)
	// 签发一个 1 秒有效令牌,睡眠越过有效期后即为过期。
	expTok, err := token.Generate(testFileSecret, "f1.txt", "agent-1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/f1.txt?token="+expTok, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (expired)", rec.Code)
	}
}

func TestHandleServeFile_WrongFile(t *testing.T) {
	s, _ := newTestService(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/files/{file_id}", s.handleServeFile)
	tok, _ := token.Generate(testFileSecret, "other.txt", "agent-1", time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/f1.txt?token="+tok, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (token/file mismatch)", rec.Code)
	}
}

func TestHandleServeFile_DirTraversal(t *testing.T) {
	s, _ := newTestService(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/files/{file_id}", s.handleServeFile)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/..%2f..%2fetc%2fpasswd?token=x", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("directory traversal should be rejected")
	}
}

func TestHandleServeFile_RateLimited(t *testing.T) {
	s, _ := newTestService(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/files/{file_id}", s.handleServeFile)
	tok, _ := token.Generate(testFileSecret, "f1.txt", "agent-rl", time.Minute)

	// 前 30 次允许(默认窗口上限),第 31 次被限流。
	limited := false
	for i := 0; i < 31; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/f1.txt?token="+tok, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("expected rate limit (429) after exhausting window")
	}
}

func TestHandleCreateDownloadTask_DirectTopology(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	h := &AgentHost{TenantID: "default", Type: AgentTypeAgent}
	if err := s.store.Register(ctx, h); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/agents/{id}/download-tasks", s.handleCreateDownloadTask)

	body := strings.NewReader(`{"file_id":"f1.txt","file_path":"/tmp/f1.txt","expected_checksum":"abc","checksum_algo":"sha256"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+h.ID+"/download-tasks", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var task TaskRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.FileURL == "" {
		t.Fatal("FileURL should be set")
	}
	if !strings.Contains(task.FileURL, "http://srv:8080/api/v1/files/f1.txt") {
		t.Fatalf("direct topology URL wrong: %q", task.FileURL)
	}
	if !strings.Contains(task.FileURL, "token=") {
		t.Fatal("download URL missing token")
	}
}

func TestHandleCreateDownloadTask_ProxyTopology(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	p := &ProxyInfo{TenantID: "default", Address: "10.0.0.9", Port: 9090}
	if err := s.store.RegisterProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	h := &AgentHost{TenantID: "default", Type: AgentTypeAgent, ProxyID: p.ID}
	if err := s.store.Register(ctx, h); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/agents/{id}/download-tasks", s.handleCreateDownloadTask)

	body := strings.NewReader(`{"file_id":"f1.txt","file_path":"/tmp/f1.txt"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+h.ID+"/download-tasks", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var task TaskRequest
	_ = json.Unmarshal(rec.Body.Bytes(), &task)
	if !strings.Contains(task.FileURL, "10.0.0.9:9090/api/v1/files/f1.txt") {
		t.Fatalf("proxy topology URL wrong: %q", task.FileURL)
	}
}
