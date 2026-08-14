package user

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"

	"github.com/opsorigin/core/internal/user/model"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/principal"
)

func newTestService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	prov := auth.NewProvider("test-secret-0123456789abcdef-test-secret", nil, nil)
	return NewService(db, prov), mock
}

func adminHash(t *testing.T) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

func TestLogin_Success(t *testing.T) {
	s, mock := newTestService(t)
	hash := adminHash(t)

	mock.ExpectQuery(`FROM users WHERE username = \$1 AND tenant_id = \$2`).
		WithArgs("admin", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "role", "status", "last_login", "created_at", "updated_at"}).
			AddRow("u-1", "default", "admin", hash, "admin@x.com", model.RoleAdmin, "active", nil, time.Now(), time.Now()))
	mock.ExpectExec(`INSERT INTO user_sessions`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE users\s+SET last_login`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`FROM permissions p`).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("cmdb:read"))

	rec := httptest.NewRecorder()
	body := `{"username":"admin","password":"admin123"}`
	s.handleLogin(rec, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("登录应成功, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp["token"] == "" || resp["refresh_token"] == "" {
		t.Fatalf("应返回 token 与 refresh_token: %v", resp)
	}
	if resp["username"] != "admin" || resp["role"] != string(model.RoleAdmin) {
		t.Fatalf("用户信息不符: %v", resp)
	}
	perms, ok := resp["permissions"].([]any)
	if !ok || len(perms) != 1 {
		t.Fatalf("permissions 不符: %v", resp["permissions"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock 期望未满足: %v", err)
	}
}

func TestLogin_InvalidPassword(t *testing.T) {
	s, mock := newTestService(t)
	hash := adminHash(t)

	mock.ExpectQuery(`FROM users WHERE username = \$1 AND tenant_id = \$2`).
		WithArgs("admin", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "role", "status", "last_login", "created_at", "updated_at"}).
			AddRow("u-1", "default", "admin", hash, "", model.RoleAdmin, "active", nil, time.Now(), time.Now()))

	rec := httptest.NewRecorder()
	s.handleLogin(rec, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong"}`)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401, got %d", rec.Code)
	}
}

func TestLogin_UserNotFound(t *testing.T) {
	s, mock := newTestService(t)

	mock.ExpectQuery(`FROM users WHERE username = \$1 AND tenant_id = \$2`).
		WithArgs("ghost", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "role", "status", "last_login", "created_at", "updated_at"}))

	rec := httptest.NewRecorder()
	s.handleLogin(rec, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"ghost","password":"x"}`)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未找到用户应 401, got %d", rec.Code)
	}
}

func TestLogin_BadBody(t *testing.T) {
	s, _ := newTestService(t)
	rec := httptest.NewRecorder()
	s.handleLogin(rec, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{invalid`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏请求应 400, got %d", rec.Code)
	}
}

func TestListUsers(t *testing.T) {
	s, mock := newTestService(t)

	mock.ExpectQuery(`FROM users\s+WHERE tenant_id = \$1`).
		WithArgs("default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "role", "status", "last_login", "created_at", "updated_at"}).
			AddRow("u-1", "default", "admin", "hash", "", model.RoleAdmin, "active", nil, time.Now(), time.Now()).
			AddRow("u-2", "default", "ops", "hash", "", model.RoleOperator, "active", nil, time.Now(), time.Now()))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	// 注入 admin 主体(真实链路由 auth 中间件完成)
	ctx := principal.NewContext(req.Context(), principal.Principal{
		Type: principal.TypeHuman, ID: "u-1", Role: "admin", TenantID: "default",
	})
	s.handleListUsers(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("列表应 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"admin"`) || !strings.Contains(rec.Body.String(), `"ops"`) {
		t.Fatalf("列表应包含用户: %s", rec.Body.String())
	}
}
