package report

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/principal"
)

// newOwnershipService 构造一个只用内存存储、注册了测试数据集的 service。
func newOwnershipService(t *testing.T) *Service {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(testDataset(&stubResolver{})); err != nil {
		t.Fatalf("register dataset: %v", err)
	}
	return &Service{reg: reg, store: NewMemoryDashboardStore()}
}

// withPrincipal 把可信主体塞进请求上下文，模拟鉴权中间件的产物。
func withPrincipal(r *http.Request, pr principal.Principal) *http.Request {
	return r.WithContext(principal.NewContext(r.Context(), pr))
}

func createDashboard(t *testing.T, svc *Service, body string, decorate func(*http.Request) *http.Request) *Dashboard {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/report/dashboards", strings.NewReader(body))
	if decorate != nil {
		req = decorate(req)
	}
	rec := httptest.NewRecorder()
	svc.handleCreateDashboard(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dashboard: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got Dashboard
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &got
}

// 归属信息必须来自可信上下文：请求体里伪造的 createdBy / tenantId 一律被服务端覆盖。
func TestCreateDashboardOwnershipComesFromTrustedContext(t *testing.T) {
	svc := newOwnershipService(t)
	body := `{"name":"我的看板","widgets":[],"createdBy":"u-attacker","createdByName":"管理员","tenantId":"tenant-victim"}`

	got := createDashboard(t, svc, body, func(r *http.Request) *http.Request {
		return withPrincipal(r, principal.Principal{
			Type:        principal.TypeHuman,
			ID:          "u-alice",
			DisplayName: "爱丽丝",
			TenantID:    "tenant-a",
			Role:        "operator",
		})
	})

	if got.CreatedBy != "u-alice" {
		t.Errorf("CreatedBy = %q, 期望取可信上下文 u-alice(伪造值必须被覆盖)", got.CreatedBy)
	}
	if got.CreatedByName != "爱丽丝" {
		t.Errorf("CreatedByName = %q, 期望 爱丽丝", got.CreatedByName)
	}
	if got.TenantID != "tenant-a" {
		t.Errorf("TenantID = %q, 期望 tenant-a(请求体租户必须被忽略)", got.TenantID)
	}
}

// 展示名缺失时回落到用户 ID，不能出现空归属。
func TestCreateDashboardFallsBackToUserIDWhenNoDisplayName(t *testing.T) {
	svc := newOwnershipService(t)
	got := createDashboard(t, svc, `{"name":"无名看板","widgets":[]}`, func(r *http.Request) *http.Request {
		return withPrincipal(r, principal.Principal{Type: principal.TypeHuman, ID: "u-bob", TenantID: "tenant-a"})
	})
	if got.CreatedBy != "u-bob" || got.CreatedByName != "u-bob" {
		t.Errorf("归属回落错误: CreatedBy=%q CreatedByName=%q, 期望均为 u-bob", got.CreatedBy, got.CreatedByName)
	}
}

// 无主体场景(内部任务)回落 system + default，避免孤儿看板。
func TestCreateDashboardWithoutPrincipalFallsBackToSystem(t *testing.T) {
	svc := newOwnershipService(t)
	got := createDashboard(t, svc, `{"name":"系统看板","widgets":[]}`, nil)
	if got.CreatedBy != "system" {
		t.Errorf("CreatedBy = %q, 期望 system", got.CreatedBy)
	}
	if got.TenantID != "default" {
		t.Errorf("TenantID = %q, 期望 default", got.TenantID)
	}
}

// 兼容旧式裸 key 上下文(尚未迁移的调用方)。
func TestContextHelpersFallBackToLegacyKeys(t *testing.T) {
	ctx := context.WithValue(context.Background(), "user_id", "u-legacy") //nolint:staticcheck // 兼容历史裸 key
	ctx = context.WithValue(ctx, "tenant_id", "tenant-legacy")            //nolint:staticcheck // 兼容历史裸 key
	if u := userFromCtx(ctx); u != "u-legacy" {
		t.Errorf("userFromCtx = %q, 期望 u-legacy", u)
	}
	if tn := auth.TenantFromContext(ctx); tn != "tenant-legacy" {
		t.Errorf("TenantFromContext = %q, 期望 tenant-legacy", tn)
	}
}

// 更新看板不得改写创建者归属。
func TestUpdateDashboardPreservesCreator(t *testing.T) {
	svc := newOwnershipService(t)
	created := createDashboard(t, svc, `{"name":"原看板","widgets":[]}`, func(r *http.Request) *http.Request {
		return withPrincipal(r, principal.Principal{Type: principal.TypeHuman, ID: "u-alice", DisplayName: "爱丽丝", TenantID: "tenant-a"})
	})

	body := `{"name":"改名看板","widgets":[],"createdBy":"u-mallory","createdByName":"入侵者"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/report/dashboards/"+created.ID, strings.NewReader(body))
	req.SetPathValue("id", created.ID)
	req = withPrincipal(req, principal.Principal{Type: principal.TypeHuman, ID: "u-bob", DisplayName: "鲍勃", TenantID: "tenant-a"})
	rec := httptest.NewRecorder()
	svc.handleUpdateDashboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update dashboard: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got Dashboard
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.CreatedBy != "u-alice" || got.CreatedByName != "爱丽丝" {
		t.Errorf("更新后创建者被改写: CreatedBy=%q CreatedByName=%q", got.CreatedBy, got.CreatedByName)
	}
	if got.Name != "改名看板" {
		t.Errorf("Name = %q, 期望改名生效", got.Name)
	}
}
