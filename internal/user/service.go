package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/user/model"
	"github.com/opsorigin/core/internal/user/store"
	"github.com/opsorigin/core/pkg/auth"
)

type Service struct {
	store    *store.PostgresStore
	authProv *auth.Provider
}

func NewService(db *sql.DB, authProv *auth.Provider) *Service {
	return &Service{
		store:    store.NewPostgresStore(db),
		authProv: authProv,
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleRefreshToken)
	mux.HandleFunc("GET /api/v1/users", s.handleListUsers)
	mux.HandleFunc("POST /api/v1/users", s.handleCreateUser)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}

	user, err := s.store.GetByUsername(context.Background(), req.Username, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	// 如果数据库不可用，使用默认admin用户
	if user == nil && s.store.IsNil() {
		if req.Username == "admin" && req.Password == "admin123" {
			user = &model.User{
				ID:       "admin-001",
				TenantID: "default",
				Username: "admin",
				Password: "$2a$10$z1zhCyKMAPr4k6.7Cjk77eVyoZEKxPCLMcXuJx5q9iiBxj22Fa1Li", // admin123
				Role:     model.RoleAdmin,
				Status:   "active",
			}
		}
	}

	if user == nil || !s.store.VerifyPassword(user, req.Password) || !user.IsActive() {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// 生成Token
	token, err := s.authProv.GenerateToken(user.ID, user.TenantID, string(user.Role), 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
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
	if err := s.store.CreateSession(context.Background(), session); err != nil {
		writeError(w, http.StatusInternalServerError, "session creation failed")
		return
	}

	// 更新最后登录时间
	s.store.UpdateLastLogin(context.Background(), user.ID, user.TenantID)

	// 获取用户权限
	permissions, _ := s.store.GetUserPermissions(context.Background(), string(user.Role))

	writeJSON(w, http.StatusOK, map[string]any{
		"token":         token,
		"refresh_token": refreshToken,
		"user_id":       user.ID,
		"username":      user.Username,
		"role":          user.Role,
		"tenant_id":     user.TenantID,
		"permissions":   permissions,
		"expires_at":    expiresAt,
	})
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := auth.TokenFromContext(r.Context())
	if token == "" {
		writeError(w, http.StatusBadRequest, "no token provided")
		return
	}

	// 撤销会话
	session, err := s.store.GetSessionByToken(context.Background(), token)
	if err == nil && session != nil {
		s.store.RevokeSession(context.Background(), session.ID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func (s *Service) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 获取会话
	session, err := s.store.GetSessionByRefreshToken(context.Background(), req.RefreshToken)
	if err != nil || session == nil {
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}

	// 检查是否过期
	if time.Now().After(session.ExpiresAt) {
		s.store.RevokeSession(context.Background(), session.ID)
		writeError(w, http.StatusUnauthorized, "refresh token expired")
		return
	}

	// 获取用户信息
	user, err := s.store.GetByID(context.Background(), session.UserID, session.TenantID)
	if err != nil || user == nil || !user.IsActive() {
		writeError(w, http.StatusUnauthorized, "user not found or inactive")
		return
	}

	// 撤销旧会话
	s.store.RevokeSession(context.Background(), session.ID)

	// 生成新Token
	newToken, err := s.authProv.GenerateToken(user.ID, user.TenantID, string(user.Role), 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
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
	if err := s.store.CreateSession(context.Background(), newSession); err != nil {
		writeError(w, http.StatusInternalServerError, "session creation failed")
		return
	}

	// 获取用户权限
	permissions, _ := s.store.GetUserPermissions(context.Background(), string(user.Role))

	writeJSON(w, http.StatusOK, map[string]any{
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
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	users, err := s.store.List(context.Background(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type userVO struct {
		ID        string  `json:"id"`
		Username  string  `json:"username"`
		Email     string  `json:"email,omitempty"`
		Role      string  `json:"role"`
		Status    string  `json:"status"`
		TenantID  string  `json:"tenant_id"`
		LastLogin *string `json:"last_login,omitempty"`
		CreatedAt string  `json:"created_at"`
		UpdatedAt string  `json:"updated_at"`
	}
	
	result := make([]userVO, 0)
	for _, u := range users {
		var lastLogin *string
		if u.LastLogin != nil {
			lastLoginStr := u.LastLogin.Format(time.RFC3339)
			lastLogin = &lastLoginStr
		}
		result = append(result, userVO{
			ID:        u.ID,
			Username:  u.Username,
			Email:     u.Email,
			Role:      string(u.Role),
			Status:    u.Status,
			TenantID:  u.TenantID,
			LastLogin: lastLogin,
			CreatedAt: u.CreatedAt.Format(time.RFC3339),
			UpdatedAt: u.UpdatedAt.Format(time.RFC3339),
		})
	}
	if result == nil {
		result = []userVO{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	role := auth.RoleFromContext(r.Context())
	if role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}

	u := &model.User{
		TenantID: tenantID,
		Username: req.Username,
		Password: req.Password,
		Email:    req.Email,
		Role:     model.Role(req.Role),
	}
	if u.Role == "" {
		u.Role = model.RoleViewer
	}

	if err := s.store.Create(context.Background(), u); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, u)
}

func (s *Service) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantIDFromContext(r.Context())
	
	user, err := s.store.GetByID(context.Background(), id, tenantID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Service) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := auth.RoleFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	
	// 只有admin可以更新其他用户，或者用户可以更新自己
	if role != "admin" && auth.UserIDFromContext(r.Context()) != id {
		writeError(w, http.StatusForbidden, "permission denied")
		return
	}

	user, err := s.store.GetByID(context.Background(), id, tenantID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Role     string `json:"role"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Username != "" {
		user.Username = req.Username
	}
	if req.Email != "" {
		user.Email = req.Email
	}
	if req.Role != "" && role == "admin" {
		user.Role = model.Role(req.Role)
	}
	if req.Status != "" && role == "admin" {
		user.Status = req.Status
	}

	if err := s.store.Update(context.Background(), user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Service) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := auth.RoleFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	
	if role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	// 不能删除自己
	if auth.UserIDFromContext(r.Context()) == id {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}

	if err := s.store.Delete(context.Background(), id, tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := auth.RoleFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	
	// 只有admin可以修改其他用户密码，或者用户可以修改自己密码
	if role != "admin" && auth.UserIDFromContext(r.Context()) != id {
		writeError(w, http.StatusForbidden, "permission denied")
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 如果是修改自己密码，需要验证旧密码
	if auth.UserIDFromContext(r.Context()) == id {
		user, err := s.store.GetByID(context.Background(), id, tenantID)
		if err != nil || user == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if !s.store.VerifyPassword(user, req.OldPassword) {
			writeError(w, http.StatusBadRequest, "old password incorrect")
			return
		}
	}

	if req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "new password required")
		return
	}

	if err := s.store.UpdatePassword(context.Background(), id, tenantID, req.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "password updated"})
}

func (s *Service) handleGetCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	
	user, err := s.store.GetByID(context.Background(), userID, tenantID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	// 获取用户权限
	permissions, _ := s.store.GetUserPermissions(context.Background(), string(user.Role))

	writeJSON(w, http.StatusOK, map[string]any{
		"user":        user,
		"permissions": permissions,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
