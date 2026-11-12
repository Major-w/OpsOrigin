package user

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"mime/multipart"
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
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "department", "role", "status", "last_login", "password_changed_at", "created_at", "updated_at"}).
			AddRow("u-1", "default", "admin", hash, "admin@x.com", "", model.RoleAdmin, "active", nil, time.Now(), time.Now(), time.Now()))
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
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "department", "role", "status", "last_login", "password_changed_at", "created_at", "updated_at"}).
			AddRow("u-1", "default", "admin", hash, "", "", model.RoleAdmin, "active", nil, time.Now(), time.Now(), time.Now()))

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
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "department", "role", "status", "last_login", "password_changed_at", "created_at", "updated_at"}))

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
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "department", "role", "status", "last_login", "password_changed_at", "created_at", "updated_at"}).
			AddRow("u-1", "default", "admin", "hash", "", "", model.RoleAdmin, "active", nil, time.Now(), time.Now(), time.Now()).
			AddRow("u-2", "default", "ops", "hash", "", "", model.RoleOperator, "active", nil, time.Now(), time.Now(), time.Now()))

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

// ---- 用户批量导入 ----

func buildUsersImportBody(t *testing.T, csvContent string) (*strings.Reader, string) {
	t.Helper()
	var body strings.Builder
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "users.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(csvContent)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return strings.NewReader(body.String()), w.FormDataContentType()
}

func TestImportUsers(t *testing.T) {
	s, mock := newTestService(t)

	csvContent := "username,email,department,role,status,password\n" +
		"zhangsan,zhang@x.com,门店A,operator,active,\n" + // 无密码 → 自动生成
		"lisi,,门店B,,,\n" + // 角色空 → viewer
		"zhangsan,dup@x.com,门店A,,,\n" // 已存在 → skip

	// zhangsan: GetByUsername 未命中 → Create（INSERT 带 RETURNING 走 Query 路径）
	anyArgs := func() []driver.Value {
		return []driver.Value{sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()}
	}
	mock.ExpectQuery(`SELECT id, tenant_id, username, password, email, department, role, status`).
		WithArgs("zhangsan", "default").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO users`).WithArgs(anyArgs()...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("u-new"))
	// lisi: GetByUsername 未命中 → Create
	mock.ExpectQuery(`SELECT id, tenant_id, username, password, email, department, role, status`).
		WithArgs("lisi", "default").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO users`).WithArgs(anyArgs()...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("u-new2"))
	// zhangsan 第二次: 命中 → skip
	mock.ExpectQuery(`SELECT id, tenant_id, username, password, email, department, role, status`).
		WithArgs("zhangsan", "default").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "username", "password", "email", "department", "role", "status", "last_login", "password_changed_at", "created_at", "updated_at"}).
			AddRow("u-1", "default", "zhangsan", "hash", "", "门店A", model.RoleOperator, "active", nil, time.Now(), time.Now(), time.Now()))

	body, ctype := buildUsersImportBody(t, csvContent)
	req := httptest.NewRequest("POST", "/api/v1/users/import", body)
	req.Header.Set("Content-Type", ctype)
	ctx := principal.NewContext(req.Context(), principal.Principal{
		Type: principal.TypeHuman, ID: "u-1", Role: "admin", TenantID: "default",
	})
	rec := httptest.NewRecorder()
	s.handleImportUsers(rec, req.WithContext(ctx))

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Created  int `json:"created"`
		Skipped  int `json:"skipped"`
		Failed   int `json:"failed"`
		Accounts []struct {
			Username        string `json:"username"`
			InitialPassword string `json:"initial_password"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Created != 2 || resp.Skipped != 1 || resp.Failed != 0 {
		t.Fatalf("created/skipped/failed 应为 2/1/0，实际 %+v body=%s", resp, rec.Body.String())
	}
	if len(resp.Accounts) != 2 {
		t.Fatalf("应返回 2 个新账号，实际 %d", len(resp.Accounts))
	}
	for _, a := range resp.Accounts {
		if a.InitialPassword == "" {
			t.Fatalf("%s 无密码列应自动生成初始密码", a.Username)
		}
		if len(a.InitialPassword) != 12 {
			t.Fatalf("初始密码应 12 位，实际 %q", a.InitialPassword)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportUsersBadRole(t *testing.T) {
	s, _ := newTestService(t)
	csvContent := "username,email,department,role,status,password\nbad,,x,superadmin,,\n"
	body, ctype := buildUsersImportBody(t, csvContent)
	req := httptest.NewRequest("POST", "/api/v1/users/import", body)
	req.Header.Set("Content-Type", ctype)
	ctx := principal.NewContext(req.Context(), principal.Principal{
		Type: principal.TypeHuman, ID: "u-1", Role: "admin", TenantID: "default",
	})
	rec := httptest.NewRecorder()
	s.handleImportUsers(rec, req.WithContext(ctx))
	if !strings.Contains(rec.Body.String(), "非法") || !strings.Contains(rec.Body.String(), `"failed":1`) {
		t.Fatalf("非法角色应报错，实际 %s", rec.Body.String())
	}
}
