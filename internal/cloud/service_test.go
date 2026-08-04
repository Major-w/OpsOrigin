package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Fakes（内存实现，不触真实云 / 不依赖 cmdb / pg）
// ---------------------------------------------------------------------------

// fakeProvider 是内存 fake Provider，ListResources 直接返回预设资源。
type fakeProvider struct {
	name      string
	resources []Resource
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) ListResources(ctx context.Context, cred Credential, region string, kinds []ResourceType) ([]Resource, error) {
	return f.resources, nil
}

func (f *fakeProvider) GetResource(ctx context.Context, cred Credential, region, kind, id string) (*Resource, error) {
	return nil, nil
}

func (f *fakeProvider) CollectMetrics(ctx context.Context, cred Credential, region string, resources []Resource) ([]Metric, error) {
	return nil, nil
}

func (f *fakeProvider) SupportedVersions() []ProviderVersion {
	return nil
}

// fakeCMDBWriter 记录 SyncResources 调用并返回预设结果。
type fakeCMDBWriter struct {
	mu            sync.Mutex
	calls         int
	lastTenant    string
	lastResources []Resource
	result        SyncResult
}

func (f *fakeCMDBWriter) SyncResources(ctx context.Context, tenantID string, resources []Resource) SyncResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastTenant = tenantID
	f.lastResources = resources
	return f.result
}

const fakeProviderName = "faketest"

var fakeOnce sync.Once

// registerFakeProvider 在全局注册表注册一个带预设资源的内存 fake Provider。
func registerFakeProvider() {
	fakeOnce.Do(func() {
		Register(&fakeProvider{
			name: fakeProviderName,
			resources: []Resource{
				{Provider: fakeProviderName, Type: TypeVM, ID: "i-1", Name: "vm-1", Region: "cn-hangzhou", Status: "running", PublicIP: "1.2.3.4", PrivateIP: "10.0.0.1"},
				{Provider: fakeProviderName, Type: TypeDatabase, ID: "db-1", Name: "rds-1", Region: "cn-hangzhou", Status: "available"},
			},
		})
	})
}

// newTestService 构造测试用 Service（内存凭证存储 + 注入的 CMDBWriter）。
func newTestService(cmdb CMDBWriter) *Service {
	return NewService(NewMemStore(), cmdb)
}

// doRequest 用 httptest 起一个 mux 并派发请求，返回 recorder。
func doRequest(t *testing.T, s *Service, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// Handler 测试
// ---------------------------------------------------------------------------

// TestHandleListProviders GET /api/v1/cloud/providers 返回 200 且含已注册 provider。
func TestHandleListProviders(t *testing.T) {
	registerFakeProvider()
	s := newTestService(&fakeCMDBWriter{})

	rec := doRequest(t, s, http.MethodGet, "/api/v1/cloud/providers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Providers []struct {
			Name  string   `json:"name"`
			Types []string `json:"types"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}

	found := false
	for _, p := range resp.Providers {
		if p.Name == fakeProviderName {
			found = true
		}
		// MVP 全局支持 vm/database/load_balancer
		if len(p.Types) != 3 {
			t.Errorf("provider %q types = %v, want 3 types", p.Name, p.Types)
		}
	}
	if !found {
		t.Errorf("providers response missing %q: %+v", fakeProviderName, resp.Providers)
	}
}

// TestHandleListResources POST /api/v1/cloud/resources/list 返回 Resource[]。
func TestHandleListResources(t *testing.T) {
	registerFakeProvider()
	s := newTestService(&fakeCMDBWriter{})

	body := map[string]any{
		"provider": fakeProviderName,
		"region":   "cn-hangzhou",
		"types":    []string{"vm", "database"},
		"credential": map[string]string{
			"access_key": "ak",
			"secret_key": "sk",
		},
	}

	rec := doRequest(t, s, http.MethodPost, "/api/v1/cloud/resources/list", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Resources []Resource `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if len(resp.Resources) != 2 {
		t.Fatalf("got %d resources, want 2; body=%s", len(resp.Resources), rec.Body.String())
	}
	if resp.Resources[0].Provider != fakeProviderName {
		t.Errorf("resource[0].Provider = %q, want %q", resp.Resources[0].Provider, fakeProviderName)
	}
	if resp.Resources[0].Type != TypeVM {
		t.Errorf("resource[0].Type = %q, want %q", resp.Resources[0].Type, TypeVM)
	}
}

// TestHandleListResourcesUnknownProvider 未知 provider 应返回 400。
func TestHandleListResourcesUnknownProvider(t *testing.T) {
	registerFakeProvider()
	s := newTestService(&fakeCMDBWriter{})

	body := map[string]any{
		"provider":   "does-not-exist",
		"credential": map[string]string{"access_key": "ak"},
	}
	rec := doRequest(t, s, http.MethodPost, "/api/v1/cloud/resources/list", body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unknown provider", rec.Code)
	}
}

// TestHandleSyncResources POST /api/v1/cloud/resources/sync 调通 fake CMDBWriter。
func TestHandleSyncResources(t *testing.T) {
	registerFakeProvider()
	fw := &fakeCMDBWriter{result: SyncResult{Created: 2, Updated: 1, Skipped: 0}}
	s := newTestService(fw)

	syncBody := map[string]any{
		"provider": fakeProviderName,
		"resources": []map[string]any{
			{"provider": fakeProviderName, "type": "vm", "id": "i-x", "name": "vm-x", "region": "cn-hangzhou", "status": "running"},
			{"provider": fakeProviderName, "type": "database", "id": "db-x", "name": "db-x", "region": "cn-hangzhou", "status": "available"},
		},
		"credential": map[string]string{"access_key": "ak", "secret_key": "sk"},
	}

	rec := doRequest(t, s, http.MethodPost, "/api/v1/cloud/resources/sync", syncBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var got SyncResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if got.Created != 2 || got.Updated != 1 {
		t.Errorf("SyncResult = %+v, want created=2 updated=1", got)
	}
	// 验证 handler 确实把请求透传给了 CMDBWriter
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.calls != 1 {
		t.Errorf("CMDBWriter.SyncResources called %d times, want 1", fw.calls)
	}
	if len(fw.lastResources) != 2 {
		t.Errorf("CMDBWriter received %d resources, want 2", len(fw.lastResources))
	}
	if fw.lastTenant != "default" {
		t.Errorf("CMDBWriter tenant = %q, want %q", fw.lastTenant, "default")
	}
}

// TestHandleCreateCredentialNoSecret 凭证创建响应体不得包含 secret_key。
func TestHandleCreateCredentialNoSecret(t *testing.T) {
	registerFakeProvider()
	s := newTestService(&fakeCMDBWriter{})

	body := map[string]any{
		"provider":   fakeProviderName,
		"region":     "cn-hangzhou",
		"access_key": "ak-secret",
		"secret_key": "TOPSECRET-VALUE",
	}
	rec := doRequest(t, s, http.MethodPost, "/api/v1/cloud/credentials", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	// 安全断言：响应体不应出现 secret_key 字段
	if _, ok := raw["secret_key"]; ok {
		t.Errorf("credential response MUST NOT contain 'secret_key' field; body=%v", raw)
	}
	if hs, ok := raw["has_secret"].(bool); !ok || !hs {
		t.Errorf("has_secret should be true, got %v", raw["has_secret"])
	}

	// 验证 secret 已正确落库（安全关注的是“响应回显”，存储仍需保留）
	stored, err := s.store.Get(fakeProviderName, idOf(Credential{Provider: fakeProviderName, AccessKey: "ak-secret"}))
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored == nil {
		t.Fatal("credential was not stored")
	}
	if stored.SecretKey != "TOPSECRET-VALUE" {
		t.Errorf("stored SecretKey = %q, want %q", stored.SecretKey, "TOPSECRET-VALUE")
	}
}

// TestHandleListCredentialsNoSecret GET 凭证列表同样不应回显 secret_key。
func TestHandleListCredentialsNoSecret(t *testing.T) {
	registerFakeProvider()
	s := newTestService(&fakeCMDBWriter{})

	// 先创建一条
	create := map[string]any{
		"provider":   fakeProviderName,
		"access_key": "ak-list",
		"secret_key": "SECRETLIST",
	}
	recC := doRequest(t, s, http.MethodPost, "/api/v1/cloud/credentials", create)
	if recC.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", recC.Code, recC.Body.String())
	}

	rec := doRequest(t, s, http.MethodGet, "/api/v1/cloud/credentials", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Credentials []map[string]any `json:"credentials"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	for _, c := range resp.Credentials {
		if _, ok := c["secret_key"]; ok {
			t.Errorf("listed credential MUST NOT contain 'secret_key'; got %v", c)
		}
	}
}
