package apikey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound 表示目标 Key 不存在（或不属于该用户，避免探测他人 Key）。
var ErrNotFound = errors.New("api key not found")

// Store 是 api_keys 表的 PG 持久化。建表走自愈 DDL（CREATE TABLE IF NOT EXISTS），
// 与 deploy/migrations/023_api_keys.sql 保持一致。
//
// 注意：db 为 nil 时 Available() 返回 false，Service 侧降级为 501，
// 不影响 Core 在无 PG 的 stub 模式下启动。
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Available() bool { return s != nil && s.db != nil }

// EnsureSchema 建表 + 索引（幂等）。
func (s *Store) EnsureSchema(ctx context.Context) error {
	if !s.Available() {
		return fmt.Errorf("apikey: nil db")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			user_id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			key_prefix TEXT NOT NULL DEFAULT '',
			key_hash TEXT NOT NULL UNIQUE,
			expires_at TIMESTAMPTZ,
			last_used_at TIMESTAMPTZ,
			use_count BIGINT NOT NULL DEFAULT 0,
			revoked_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys (tenant_id, user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys (key_hash)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apikey: ensure schema: %w", err)
		}
	}
	return nil
}

// listCols 统一列表/详情字段。username 通过 LEFT JOIN users 取，可能为空。
const listCols = `k.id, k.tenant_id, k.user_id, COALESCE(u.username, ''),
	k.name, k.key_prefix, k.expires_at, k.last_used_at, k.use_count, k.revoked_at, k.created_at`

func scanKey(sc interface {
	Scan(dest ...any) error
}) (*Key, error) {
	var k Key
	var expires, lastUsed, revoked sql.NullTime
	if err := sc.Scan(&k.ID, &k.TenantID, &k.UserID, &k.Username, &k.Name, &k.KeyPrefix,
		&expires, &lastUsed, &k.UseCount, &revoked, &k.CreatedAt); err != nil {
		return nil, err
	}
	if expires.Valid {
		t := expires.Time
		k.ExpiresAt = &t
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		k.LastUsedAt = &t
	}
	if revoked.Valid {
		t := revoked.Time
		k.RevokedAt = &t
	}
	return &k, nil
}

// Create 写入一条 Key（只落摘要与展示前缀）。
func (s *Store) Create(ctx context.Context, k *Key, raw string) error {
	if !s.Available() {
		return fmt.Errorf("apikey: nil db")
	}
	if k.KeyPrefix == "" {
		k.KeyPrefix = displayPrefix(raw)
	}
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, tenant_id, user_id, name, key_prefix, key_hash, expires_at, use_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8)`,
		k.ID, k.TenantID, k.UserID, k.Name, k.KeyPrefix, hashKey(raw), k.ExpiresAt, k.CreatedAt); err != nil {
		return fmt.Errorf("apikey: insert: %w", err)
	}
	return nil
}

// ListByUser 列出某用户全部 Key（含已吊销/过期，前端按状态展示）。
func (s *Store) ListByUser(ctx context.Context, tenantID, userID string, limit int) ([]*Key, error) {
	if !s.Available() {
		return nil, fmt.Errorf("apikey: nil db")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+listCols+`
		FROM api_keys k LEFT JOIN users u ON u.id = k.user_id AND u.tenant_id = k.tenant_id
		WHERE k.tenant_id=$1 AND k.user_id=$2
		ORDER BY k.created_at DESC LIMIT $3`, tenantID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("apikey: list by user: %w", err)
	}
	return collectKeys(rows)
}

// ListByTenant 列出全租户 Key（仅管理员使用）。
func (s *Store) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*Key, error) {
	if !s.Available() {
		return nil, fmt.Errorf("apikey: nil db")
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+listCols+`
		FROM api_keys k LEFT JOIN users u ON u.id = k.user_id AND u.tenant_id = k.tenant_id
		WHERE k.tenant_id=$1
		ORDER BY k.created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("apikey: list by tenant: %w", err)
	}
	return collectKeys(rows)
}

func collectKeys(rows *sql.Rows) ([]*Key, error) {
	defer rows.Close()
	out := []*Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("apikey: scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetByHash 按摘要查 Key（鉴权路径唯一入口，不带租户过滤：
// key_hash 全局唯一，且解析时尚未知租户）。
func (s *Store) GetByHash(ctx context.Context, raw string) (*Key, error) {
	if !s.Available() {
		return nil, fmt.Errorf("apikey: nil db")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, name, key_prefix, expires_at, last_used_at, use_count, revoked_at, created_at
		FROM api_keys WHERE key_hash=$1`, hashKey(raw))
	var k Key
	var expires, lastUsed, revoked sql.NullTime
	err := row.Scan(&k.ID, &k.TenantID, &k.UserID, &k.Name, &k.KeyPrefix,
		&expires, &lastUsed, &k.UseCount, &revoked, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("apikey: get by hash: %w", err)
	}
	if expires.Valid {
		t := expires.Time
		k.ExpiresAt = &t
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		k.LastUsedAt = &t
	}
	if revoked.Valid {
		t := revoked.Time
		k.RevokedAt = &t
	}
	return &k, nil
}

// GetByID 按主键查 Key。userID 非空时限定归属（防止越权查看/轮换他人 Key）；
// 管理员可传空串表示不限归属。
func (s *Store) GetByID(ctx context.Context, tenantID, id, userID string) (*Key, error) {
	if !s.Available() {
		return nil, fmt.Errorf("apikey: nil db")
	}
	q := `SELECT ` + listCols + `
		FROM api_keys k LEFT JOIN users u ON u.id = k.user_id AND u.tenant_id = k.tenant_id
		WHERE k.id=$1 AND k.tenant_id=$2`
	args := []any{id, tenantID}
	if userID != "" {
		q += ` AND k.user_id=$3`
		args = append(args, userID)
	}
	k, err := scanKey(s.db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("apikey: get by id: %w", err)
	}
	return k, nil
}

// Revoke 吊销一条 Key。userID 非空时限定归属（普通用户只能吊销自己的）；
// 管理员传空串表示不限归属。返回是否实际命中行。
func (s *Store) Revoke(ctx context.Context, tenantID, id, userID string) (bool, error) {
	if !s.Available() {
		return false, fmt.Errorf("apikey: nil db")
	}
	q := `UPDATE api_keys SET revoked_at=NOW()
		WHERE id=$1 AND tenant_id=$2 AND revoked_at IS NULL`
	args := []any{id, tenantID}
	if userID != "" {
		q += ` AND user_id=$3`
		args = append(args, userID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return false, fmt.Errorf("apikey: revoke: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// TouchUsage 累加用量计数（按 id 去重后逐条自增）。
func (s *Store) TouchUsage(ctx context.Context, ids []string) error {
	if !s.Available() || len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apikey: begin usage tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 提交后为空操作
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE api_keys SET last_used_at=NOW(), use_count=use_count+1 WHERE id=$1`)
	if err != nil {
		return fmt.Errorf("apikey: prepare usage: %w", err)
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("apikey: touch usage: %w", err)
		}
	}
	return tx.Commit()
}
