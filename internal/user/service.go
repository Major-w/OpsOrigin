package user

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/user/model"
	"github.com/opsorigin/core/internal/user/store"
	"github.com/opsorigin/core/pkg/auth"
	"golang.org/x/crypto/bcrypt"

	"github.com/opsorigin/core/pkg/httputil"
)

type Service struct {
	store    *store.PostgresStore
	authProv *auth.Provider
}

// defaultAdminCreds 返回数据库不可用时的兜底管理员凭据。
// 可通过环境变量覆盖（OPSORIGIN_DEFAULT_ADMIN_USERNAME / OPSORIGIN_DEFAULT_ADMIN_PASSWORD），
// 默认 admin/admin123（仅本地开发与单租户演示使用，公网部署必须通过环境变量修改）。
func defaultAdminCreds() (username, password string) {
	username = os.Getenv("OPSORIGIN_DEFAULT_ADMIN_USERNAME")
	if username == "" {
		username = "admin"
	}
	password = os.Getenv("OPSORIGIN_DEFAULT_ADMIN_PASSWORD")
	if password == "" {
		password = "admin123"
	}
	return username, password
}

func NewService(db *sql.DB, authProv *auth.Provider) *Service {
	return &Service{
		store:    store.NewPostgresStore(db),
		authProv: authProv,
	}
}

// InitSchema 存储自愈 DDL（department 列等，与 deploy/migrations 双写）。
// 由 cmd/core/main.go 在装配时显式调用（不放进 NewService，避免测试用 sqlmock
// 的严格期望被 DDL 打乱）；失败仅告警，不阻断启动。
func (s *Service) InitSchema(ctx context.Context) {
	if err := s.store.Init(ctx); err != nil {
		slog.Warn("user store init (schema ensure) failed", "error", err)
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleRefreshToken)
	mux.HandleFunc("GET /api/v1/users", s.handleListUsers)
	mux.HandleFunc("POST /api/v1/users", s.handleCreateUser)
	mux.HandleFunc("POST /api/v1/users/import", s.handleImportUsers)
	mux.HandleFunc("GET /api/v1/users/password-policy", s.handleGetPasswordPolicy)
	mux.HandleFunc("GET /api/v1/users/{id}", s.handleGetUser)
	mux.HandleFunc("PUT /api/v1/users/{id}", s.handleUpdateUser)
	mux.HandleFunc("DELETE /api/v1/users/{id}", s.handleDeleteUser)
	mux.HandleFunc("PUT /api/v1/users/{id}/password", s.handleChangePassword)
	mux.HandleFunc("GET /api/v1/users/me", s.handleGetCurrentUser)
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}

	user, err := s.store.GetByUsername(r.Context(), req.Username, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "database error")
		return
	}

	// 如果数据库不可用，使用默认admin用户（凭据可由环境变量覆盖）
	if user == nil && s.store.IsNil() {
		defUser, defPass := defaultAdminCreds()
		if req.Username == defUser && req.Password == defPass {
			hash, err := bcrypt.GenerateFromPassword([]byte(defPass), bcrypt.DefaultCost)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "password hashing failed")
				return
			}
			user = &model.User{
				ID:       "admin-001",
				TenantID: "default",
				Username: defUser,
				Password: string(hash),
				Role:     model.RoleAdmin,
				Status:   "active",
			}
		}
	}

	if user == nil || !s.store.VerifyPassword(user, req.Password) || !user.IsActive() {
		// 本地账号未命中（不存在 / 密码不符 / 被禁用）→ 尝试 LDAP/AD 域认证。
		// 本地命中时优先本地（管理员兜底不受影响）；LDAP 未启用则维持原 401。
		ldapUser := s.ldapLogin(r.Context(), tenantID, req.Username, req.Password)
		if ldapUser == nil {
			httputil.WriteError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		user = ldapUser
	}

	// 生成Token
	token, err := s.authProv.GenerateToken(user.ID, user.TenantID, string(user.Role), 24*time.Hour)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "token generation failed")
		return
	}

	// 生成Refresh Token
	refreshToken := uuid.New().String()
	expiresAt := time.Now().Add(7 * 24 * time.Hour) // 7天过期

	// 创建会话
	session := &store.Session{
		UserID:       user.ID,
		TenantID:     user.TenantID,
		Token:        token,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	}
	if err := s.store.CreateSession(r.Context(), session); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "session creation failed")
		return
	}

	// 更新最后登录时间
	s.store.UpdateLastLogin(r.Context(), user.ID, user.TenantID)

	// 获取用户权限
	permissions, _ := s.store.GetUserPermissions(r.Context(), string(user.Role))

	// 密码有效期检查：过期时登录放行但标记，前端强制引导改密
	passwordExpired := CurrentPolicy().PasswordExpired(
		func() time.Time {
			if user.PasswordChangedAt != nil {
				return *user.PasswordChangedAt
			}
			return time.Time{}
		}(), time.Now())

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"token":            token,
		"refresh_token":    refreshToken,
		"user_id":          user.ID,
		"username":         user.Username,
		"role":             user.Role,
		"tenant_id":        user.TenantID,
		"permissions":      permissions,
		"expires_at":       expiresAt,
		"password_expired": passwordExpired,
	})
}

// ldapLogin 尝试 LDAP/AD 域认证并返回平台用户（未配置 LDAP 或认证失败返回 nil）。
// 语义：
//   - 本地已有同名且 active 的账号 → 同步邮箱后直接使用（管理员可预建账号并调角色）；
//   - 本地账号被禁用 → 拒绝（不允许通过域认证绕过本地禁用）；
//   - 本地不存在 → 首登自动建档：role=operator、随机本地密码（防止域账号被本地密码路径绕过）。
func (s *Service) ldapLogin(ctx context.Context, tenantID, username, password string) *model.User {
	lp := s.authProv.LDAP()
	if lp == nil || username == "" || password == "" {
		return nil
	}
	info, err := lp.Authenticate(username, password)
	if err != nil {
		return nil
	}

	u, err := s.store.GetByUsername(ctx, username, tenantID)
	if err != nil {
		return nil
	}
	if u != nil {
		if !u.IsActive() {
			return nil // 本地禁用优先于域认证
		}
		changed := false
		if info.Email != "" && info.Email != u.Email {
			u.Email = info.Email
			changed = true
		}
		if info.Department != "" && info.Department != u.Department {
			u.Department = info.Department
			changed = true
		}
		if changed {
			if err := s.store.Update(ctx, u); err != nil {
				slog.Warn("ldap login: sync profile failed", "username", username, "error", err)
			}
		}
		return u
	}

	// 首登自动建档
	newUser := &model.User{
		TenantID:   tenantID,
		Username:   username,
		Email:      info.Email,
		Department: info.Department,
		Role:       model.RoleOperator,
		Status:     "active",
		Password:   uuid.NewString(), // 不可预测：域用户不应能走本地密码路径
	}
	if err := s.store.Create(ctx, newUser); err != nil {
		// 并发建档撞唯一键（ON CONFLICT DO NOTHING 无 RETURNING 行）→ 回读
		u, gerr := s.store.GetByUsername(ctx, username, tenantID)
		if gerr != nil || u == nil {
			slog.Warn("ldap login: provision failed", "username", username, "error", err)
			return nil
		}
		return u
	}
	return newUser
}

// EnsureUser 目录同步建档（实现 ldapsync.UserWriter）：按用户名 Upsert，
// 不存在建档（operator/active/随机本地密码），存在同步邮箱与部门。返回 (id, created, err)。
func (s *Service) EnsureUser(ctx context.Context, tenantID, username, email, department string) (string, bool, error) {
	u, err := s.store.GetByUsername(ctx, username, tenantID)
	if err != nil {
		return "", false, err
	}
	if u != nil {
		changed := false
		if email != "" && email != u.Email {
			u.Email = email
			changed = true
		}
		if department != "" && department != u.Department {
			u.Department = department
			changed = true
		}
		if changed {
			if err := s.store.Update(ctx, u); err != nil {
				return u.ID, false, fmt.Errorf("update profile: %w", err)
			}
		}
		return u.ID, false, nil
	}
	newUser := &model.User{
		TenantID:   tenantID,
		Username:   username,
		Email:      email,
		Department: department,
		Role:       model.RoleOperator,
		Status:     "active",
		Password:   uuid.NewString(), // 目录账号不可预测本地密码
	}
	if err := s.store.Create(ctx, newUser); err != nil {
		// 并发撞唯一键（ON CONFLICT DO NOTHING 无 RETURNING 行）→ 回读
		if u2, gerr := s.store.GetByUsername(ctx, username, tenantID); gerr == nil && u2 != nil {
			return u2.ID, false, nil
		}
		return "", false, err
	}
	return newUser.ID, true, nil
}

// ListUsernameIDMap 本地 username → user ID 映射（实现 ldapsync.UserWriter）。
func (s *Service) ListUsernameIDMap(ctx context.Context, tenantID string) (map[string]string, error) {
	users, err := s.store.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(users))
	for _, u := range users {
		m[u.Username] = u.ID
	}
	return m, nil
}

// GetUserAttr 按用户 ID 取单个属性（ITSM 提单人属性派单 submitter_attr 联动通道）。
// 返回 (值, 是否有值, err)；用户不存在/属性不支持时 has=false。
func (s *Service) GetUserAttr(ctx context.Context, tenantID, userID, attr string) (string, bool, error) {
	u, err := s.store.GetByID(ctx, userID, tenantID)
	if err != nil {
		return "", false, err
	}
	if u == nil {
		return "", false, nil
	}
	switch strings.TrimSpace(attr) {
	case "department":
		return u.Department, u.Department != "", nil
	case "email":
		return u.Email, u.Email != "", nil
	default:
		return "", false, nil
	}
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := auth.TokenFromContext(r.Context())
	if token == "" {
		httputil.WriteError(w, http.StatusBadRequest, "no token provided")
		return
	}

	// 撤销会话
	session, err := s.store.GetSessionByToken(r.Context(), token)
	if err == nil && session != nil {
		s.store.RevokeSession(r.Context(), session.ID)
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func (s *Service) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 获取会话
	session, err := s.store.GetSessionByRefreshToken(r.Context(), req.RefreshToken)
	if err != nil || session == nil {
		httputil.WriteError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}

	// 检查是否过期
	if time.Now().After(session.ExpiresAt) {
		s.store.RevokeSession(r.Context(), session.ID)
		httputil.WriteError(w, http.StatusUnauthorized, "refresh token expired")
		return
	}

	// 获取用户信息
	user, err := s.store.GetByID(r.Context(), session.UserID, session.TenantID)
	if err != nil || user == nil || !user.IsActive() {
		httputil.WriteError(w, http.StatusUnauthorized, "user not found or inactive")
		return
	}

	// 撤销旧会话
	s.store.RevokeSession(r.Context(), session.ID)

	// 生成新Token
	newToken, err := s.authProv.GenerateToken(user.ID, user.TenantID, string(user.Role), 24*time.Hour)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "token generation failed")
		return
	}

	// 生成新Refresh Token
	newRefreshToken := uuid.New().String()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	// 创建新会话
	newSession := &store.Session{
		UserID:       user.ID,
		TenantID:     user.TenantID,
		Token:        newToken,
		RefreshToken: newRefreshToken,
		ExpiresAt:    expiresAt,
	}
	if err := s.store.CreateSession(r.Context(), newSession); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "session creation failed")
		return
	}

	// 获取用户权限
	permissions, _ := s.store.GetUserPermissions(r.Context(), string(user.Role))

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"token":         newToken,
		"refresh_token": newRefreshToken,
		"user_id":       user.ID,
		"username":      user.Username,
		"role":          user.Role,
		"tenant_id":     user.TenantID,
		"permissions":   permissions,
		"expires_at":    expiresAt,
	})
}

func (s *Service) handleListUsers(w http.ResponseWriter, r *http.Request) {
	role := auth.RoleFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	if role != "admin" {
		httputil.WriteError(w, http.StatusForbidden, "admin only")
		return
	}

	users, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type userVO struct {
		ID         string  `json:"id"`
		Username   string  `json:"username"`
		Email      string  `json:"email,omitempty"`
		Department string  `json:"department,omitempty"`
		Role       string  `json:"role"`
		Status     string  `json:"status"`
		TenantID   string  `json:"tenant_id"`
		LastLogin  *string `json:"last_login,omitempty"`
		CreatedAt  string  `json:"created_at"`
		UpdatedAt  string  `json:"updated_at"`
	}

	result := make([]userVO, 0)
	for _, u := range users {
		var lastLogin *string
		if u.LastLogin != nil {
			lastLoginStr := u.LastLogin.Format(time.RFC3339)
			lastLogin = &lastLoginStr
		}
		result = append(result, userVO{
			ID:         u.ID,
			Username:   u.Username,
			Email:      u.Email,
			Department: u.Department,
			Role:       string(u.Role),
			Status:     u.Status,
			TenantID:   u.TenantID,
			LastLogin:  lastLogin,
			CreatedAt:  u.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  u.UpdatedAt.Format(time.RFC3339),
		})
	}
	if result == nil {
		result = []userVO{}
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	role := auth.RoleFromContext(r.Context())
	if role != "admin" {
		httputil.WriteError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		Email      string `json:"email"`
		Department string `json:"department"`
		Role       string `json:"role"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		httputil.WriteError(w, http.StatusBadRequest, "username and password required")
		return
	}
	if err := ValidatePassword(req.Password); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}

	u := &model.User{
		TenantID:   tenantID,
		Username:   req.Username,
		Password:   req.Password,
		Email:      req.Email,
		Department: req.Department,
		Role:       model.Role(req.Role),
	}
	if u.Role == "" {
		u.Role = model.RoleViewer
	}

	if err := s.store.Create(r.Context(), u); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, u)
}

// randomPassword 生成 12 位随机初始密码（大小写+数字，crypto/rand）。
func randomPassword() string {
	const charset = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 12)
	buf := make([]byte, 12)
	if _, err := crand.Read(buf); err != nil {
		return "Ops@" + uuid.NewString()[:8] // 兜底：仍保证不可预测
	}
	for i := range b {
		b[i] = charset[int(buf[i])%len(charset)]
	}
	return string(b)
}

// handleImportUsers 批量导入用户（CSV multipart，admin only）。
// 列：username,email,department,role,status,password。password 为空时自动生成
// 随机初始密码并在结果 accounts 中返回（仅本次响应可见，提示管理员分发）。
// 已存在用户名跳过（不覆盖），角色非法行报错。
func (s *Service) handleImportUsers(w http.ResponseWriter, r *http.Request) {
	if auth.RoleFromContext(r.Context()) != "admin" {
		httputil.WriteError(w, http.StatusForbidden, "admin only")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to parse form")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to parse CSV: "+err.Error())
		return
	}

	type importError struct {
		Row     int    `json:"row"`
		Message string `json:"message"`
	}
	type importAccount struct {
		Username        string `json:"username"`
		InitialPassword string `json:"initial_password,omitempty"` // 仅自动生成时返回
	}
	resp := struct {
		Created  int             `json:"created"`
		Skipped  int             `json:"skipped"`
		Failed   int             `json:"failed"`
		Errors   []importError   `json:"errors,omitempty"`
		Accounts []importAccount `json:"accounts,omitempty"` // 新建账号（含自动生成的初始密码）
	}{Errors: []importError{}, Accounts: []importAccount{}}

	validRoles := map[string]bool{string(model.RoleAdmin): true, string(model.RoleOperator): true, string(model.RoleViewer): true}

	for i, row := range records {
		if i == 0 && strings.EqualFold(strings.TrimSpace(row[0]), "username") {
			continue // 表头
		}
		if len(row) < 1 {
			resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: "insufficient columns"})
			resp.Failed++
			continue
		}
		username := strings.TrimSpace(row[0])
		if username == "" {
			resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: "username 为空"})
			resp.Failed++
			continue
		}
		email, department := "", ""
		if len(row) > 1 {
			email = strings.TrimSpace(row[1])
		}
		if len(row) > 2 {
			department = strings.TrimSpace(row[2])
		}
		roleStr := ""
		if len(row) > 3 {
			roleStr = strings.TrimSpace(row[3])
		}
		if roleStr != "" && !validRoles[roleStr] {
			resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: fmt.Sprintf("角色 %q 非法（admin/operator/viewer）", roleStr)})
			resp.Failed++
			continue
		}
		if roleStr == "" {
			roleStr = string(model.RoleViewer)
		}
		status := "active"
		if len(row) > 4 && strings.TrimSpace(row[4]) != "" {
			status = strings.TrimSpace(row[4])
		}
		password := ""
		if len(row) > 5 {
			password = strings.TrimSpace(row[5])
		}
		generated := ""
		if password == "" {
			generated = randomPassword()
			password = generated
		} else if err := ValidatePassword(password); err != nil {
			// 手工填写的密码受密码策略约束；自动生成的 12 位随机串默认满足策略
			resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: err.Error()})
			resp.Failed++
			continue
		}

		// 已存在跳过（不覆盖已有账号）
		if existing, gerr := s.store.GetByUsername(r.Context(), username, tenantID); gerr == nil && existing != nil {
			resp.Skipped++
			continue
		}

		u := &model.User{
			TenantID:   tenantID,
			Username:   username,
			Password:   password,
			Email:      email,
			Department: department,
			Role:       model.Role(roleStr),
			Status:     status,
		}
		if err := s.store.Create(r.Context(), u); err != nil {
			resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: err.Error()})
			resp.Failed++
			continue
		}
		resp.Created++
		resp.Accounts = append(resp.Accounts, importAccount{Username: username, InitialPassword: generated})
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantIDFromContext(r.Context())

	user, err := s.store.GetByID(r.Context(), id, tenantID)
	if err != nil || user == nil {
		httputil.WriteError(w, http.StatusNotFound, "user not found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, user)
}

func (s *Service) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := auth.RoleFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())

	// 只有admin可以更新其他用户，或者用户可以更新自己
	if role != "admin" && auth.UserIDFromContext(r.Context()) != id {
		httputil.WriteError(w, http.StatusForbidden, "permission denied")
		return
	}

	user, err := s.store.GetByID(r.Context(), id, tenantID)
	if err != nil || user == nil {
		httputil.WriteError(w, http.StatusNotFound, "user not found")
		return
	}

	var req struct {
		Username   string  `json:"username"`
		Email      string  `json:"email"`
		Department *string `json:"department"` // 指针语义：未传=不动，传空串=清空
		Role       string  `json:"role"`
		Status     string  `json:"status"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username != "" {
		user.Username = req.Username
	}
	if req.Email != "" {
		user.Email = req.Email
	}
	if req.Department != nil {
		user.Department = strings.TrimSpace(*req.Department)
	}
	if req.Role != "" && role == "admin" {
		user.Role = model.Role(req.Role)
	}
	if req.Status != "" && role == "admin" {
		user.Status = req.Status
	}

	if err := s.store.Update(r.Context(), user); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, user)
}

func (s *Service) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := auth.RoleFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())

	if role != "admin" {
		httputil.WriteError(w, http.StatusForbidden, "admin only")
		return
	}

	// 不能删除自己
	if auth.UserIDFromContext(r.Context()) == id {
		httputil.WriteError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}

	if err := s.store.Delete(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := auth.RoleFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())

	// 只有admin可以修改其他用户密码，或者用户可以修改自己密码
	if role != "admin" && auth.UserIDFromContext(r.Context()) != id {
		httputil.WriteError(w, http.StatusForbidden, "permission denied")
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 如果是修改自己密码，需要验证旧密码
	if auth.UserIDFromContext(r.Context()) == id {
		user, err := s.store.GetByID(r.Context(), id, tenantID)
		if err != nil || user == nil {
			httputil.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		if !s.store.VerifyPassword(user, req.OldPassword) {
			httputil.WriteError(w, http.StatusBadRequest, "old password incorrect")
			return
		}
	}

	if req.NewPassword == "" {
		httputil.WriteError(w, http.StatusBadRequest, "new password required")
		return
	}
	// 密码策略校验（管理员重置他人密码同样受约束）
	if err := ValidatePassword(req.NewPassword); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.UpdatePassword(r.Context(), id, tenantID, req.NewPassword); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "password updated"})
}

// handleGetPasswordPolicy 返回当前密码策略（前端表单提示/校验用，任意已登录用户可读）。
func (s *Service) handleGetPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	p := CurrentPolicy()
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"min_len":      p.MinLen,
		"min_classes":  p.MinClasses,
		"max_age_days": p.MaxAgeDays,
	})
}

func (s *Service) handleGetCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())

	user, err := s.store.GetByID(r.Context(), userID, tenantID)
	if err != nil || user == nil {
		httputil.WriteError(w, http.StatusNotFound, "user not found")
		return
	}

	// 获取用户权限
	permissions, _ := s.store.GetUserPermissions(r.Context(), string(user.Role))

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"user":        user,
		"permissions": permissions,
	})
}
