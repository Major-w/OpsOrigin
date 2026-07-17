package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/user/model"
	"golang.org/x/crypto/bcrypt"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Create(ctx context.Context, user *model.User) error {
	if s.db == nil {
		return nil
	}
	
	if user.ID == "" {
		user.ID = uuid.New().String()
	}
	if user.TenantID == "" {
		user.TenantID = "default"
	}
	if user.Status == "" {
		user.Status = "active"
	}
	if user.Role == "" {
		user.Role = model.RoleViewer
	}
	
	// Hash密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user.Password = string(hashedPassword)
	
	now := time.Now()
	user.CreatedAt = now
	user.UpdatedAt = now
	
	query := `
		INSERT INTO users (id, tenant_id, username, password, email, role, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, username) DO NOTHING
		RETURNING id`
	
	return s.db.QueryRowContext(ctx, query,
		user.ID, user.TenantID, user.Username, user.Password,
		user.Email, user.Role, user.Status, user.CreatedAt, user.UpdatedAt,
	).Scan(&user.ID)
}

func (s *PostgresStore) GetByID(ctx context.Context, id, tenantID string) (*model.User, error) {
	if s.db == nil {
		return nil, nil
	}
	
	query := `
		SELECT id, tenant_id, username, password, email, role, status, last_login, created_at, updated_at
		FROM users
		WHERE id = $1 AND tenant_id = $2`
	
	user := &model.User{}
	err := s.db.QueryRowContext(ctx, query, id, tenantID).Scan(
		&user.ID, &user.TenantID, &user.Username, &user.Password,
		&user.Email, &user.Role, &user.Status, &user.LastLogin,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *PostgresStore) GetByUsername(ctx context.Context, username, tenantID string) (*model.User, error) {
	if s.db == nil {
		return nil, nil
	}
	
	query := `
		SELECT id, tenant_id, username, password, email, role, status, last_login, created_at, updated_at
		FROM users
		WHERE username = $1 AND tenant_id = $2`
	
	user := &model.User{}
	err := s.db.QueryRowContext(ctx, query, username, tenantID).Scan(
		&user.ID, &user.TenantID, &user.Username, &user.Password,
		&user.Email, &user.Role, &user.Status, &user.LastLogin,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *PostgresStore) GetByEmail(ctx context.Context, email, tenantID string) (*model.User, error) {
	if s.db == nil {
		return nil, nil
	}
	
	query := `
		SELECT id, tenant_id, username, password, email, role, status, last_login, created_at, updated_at
		FROM users
		WHERE email = $1 AND tenant_id = $2`
	
	user := &model.User{}
	err := s.db.QueryRowContext(ctx, query, email, tenantID).Scan(
		&user.ID, &user.TenantID, &user.Username, &user.Password,
		&user.Email, &user.Role, &user.Status, &user.LastLogin,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

// IsNil 检查数据库连接是否为空
func (s *PostgresStore) IsNil() bool {
	return s.db == nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string) ([]*model.User, error) {
	if s.db == nil {
		return nil, nil
	}
	
	query := `
		SELECT id, tenant_id, username, password, email, role, status, last_login, created_at, updated_at
		FROM users
		WHERE tenant_id = $1
		ORDER BY created_at DESC`
	
	rows, err := s.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var users []*model.User
	for rows.Next() {
		user := &model.User{}
		err := rows.Scan(
			&user.ID, &user.TenantID, &user.Username, &user.Password,
			&user.Email, &user.Role, &user.Status, &user.LastLogin,
			&user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func (s *PostgresStore) Update(ctx context.Context, user *model.User) error {
	if s.db == nil {
		return nil
	}
	
	user.UpdatedAt = time.Now()
	
	query := `
		UPDATE users
		SET username = $1, email = $2, role = $3, status = $4, updated_at = $5
		WHERE id = $6 AND tenant_id = $7`
	
	_, err := s.db.ExecContext(ctx, query,
		user.Username, user.Email, user.Role, user.Status, user.UpdatedAt,
		user.ID, user.TenantID,
	)
	return err
}

func (s *PostgresStore) UpdatePassword(ctx context.Context, userID, tenantID, newPassword string) error {
	if s.db == nil {
		return nil
	}
	
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	
	query := `
		UPDATE users
		SET password = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4`
	
	_, err = s.db.ExecContext(ctx, query, string(hashedPassword), time.Now(), userID, tenantID)
	return err
}

func (s *PostgresStore) Delete(ctx context.Context, id, tenantID string) error {
	if s.db == nil {
		return nil
	}
	
	query := `DELETE FROM users WHERE id = $1 AND tenant_id = $2`
	_, err := s.db.ExecContext(ctx, query, id, tenantID)
	return err
}

func (s *PostgresStore) VerifyPassword(user *model.User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	return err == nil
}

func (s *PostgresStore) UpdateLastLogin(ctx context.Context, userID, tenantID string) error {
	if s.db == nil {
		return nil
	}
	
	query := `
		UPDATE users
		SET last_login = $1
		WHERE id = $2 AND tenant_id = $3`
	
	_, err := s.db.ExecContext(ctx, query, time.Now(), userID, tenantID)
	return err
}

// 会话管理
func (s *PostgresStore) CreateSession(ctx context.Context, session *Session) error {
	if s.db == nil {
		return nil
	}
	
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	if session.TenantID == "" {
		session.TenantID = "default"
	}
	
	query := `
		INSERT INTO user_sessions (id, user_id, tenant_id, token, refresh_token, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	
	_, err := s.db.ExecContext(ctx, query,
		session.ID, session.UserID, session.TenantID, session.Token,
		session.RefreshToken, session.ExpiresAt, session.CreatedAt,
	)
	return err
}

func (s *PostgresStore) GetSessionByToken(ctx context.Context, token string) (*Session, error) {
	if s.db == nil {
		return nil, nil
	}
	
	query := `
		SELECT id, user_id, tenant_id, token, refresh_token, expires_at, created_at, revoked_at
		FROM user_sessions
		WHERE token = $1 AND revoked_at IS NULL`
	
	session := &Session{}
	err := s.db.QueryRowContext(ctx, query, token).Scan(
		&session.ID, &session.UserID, &session.TenantID, &session.Token,
		&session.RefreshToken, &session.ExpiresAt, &session.CreatedAt, &session.RevokedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *PostgresStore) GetSessionByRefreshToken(ctx context.Context, refreshToken string) (*Session, error) {
	if s.db == nil {
		return nil, nil
	}
	
	query := `
		SELECT id, user_id, tenant_id, token, refresh_token, expires_at, created_at, revoked_at
		FROM user_sessions
		WHERE refresh_token = $1 AND revoked_at IS NULL`
	
	session := &Session{}
	err := s.db.QueryRowContext(ctx, query, refreshToken).Scan(
		&session.ID, &session.UserID, &session.TenantID, &session.Token,
		&session.RefreshToken, &session.ExpiresAt, &session.CreatedAt, &session.RevokedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *PostgresStore) RevokeSession(ctx context.Context, sessionID string) error {
	if s.db == nil {
		return nil
	}
	
	query := `
		UPDATE user_sessions
		SET revoked_at = $1
		WHERE id = $2`
	
	_, err := s.db.ExecContext(ctx, query, time.Now(), sessionID)
	return err
}

func (s *PostgresStore) RevokeAllUserSessions(ctx context.Context, userID string) error {
	if s.db == nil {
		return nil
	}
	
	query := `
		UPDATE user_sessions
		SET revoked_at = $1
		WHERE user_id = $2 AND revoked_at IS NULL`
	
	_, err := s.db.ExecContext(ctx, query, time.Now(), userID)
	return err
}

// 权限管理
func (s *PostgresStore) GetUserPermissions(ctx context.Context, role string) ([]string, error) {
	if s.db == nil {
		return nil, nil
	}
	
	query := `
		SELECT p.name
		FROM permissions p
		INNER JOIN role_permissions rp ON p.id = rp.permission_id
		WHERE rp.role = $1`
	
	rows, err := s.db.QueryContext(ctx, query, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var permissions []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		permissions = append(permissions, name)
	}
	return permissions, nil
}

// Session 会话结构体
type Session struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	TenantID     string     `json:"tenant_id"`
	Token        string     `json:"token"`
	RefreshToken string     `json:"refresh_token"`
	ExpiresAt    time.Time  `json:"expires_at"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}