package keystore

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type pgStore struct {
	db *sql.DB
}

func newPGStore(db *sql.DB) *pgStore {
	s := &pgStore{db: db}
	s.init(context.Background())
	return s
}

func (s *pgStore) init(ctx context.Context) {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS keystore_entries (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT, type TEXT,
			content TEXT NOT NULL, fingerprint TEXT, size INTEGER DEFAULT 0, algorithm TEXT,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_keystore_entries_tenant ON keystore_entries(tenant_id, type)`,
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st); err != nil {
			slog.Warn("keystore pg store init DDL failed", "err", err)
			return
		}
	}
}

func (s *pgStore) Create(ctx context.Context, k *KeyEntry) error {
	if k.ID == "" {
		k.ID = uuid.New().String()
	}
	if k.TenantID == "" {
		k.TenantID = "default"
	}
	now := time.Now()
	if k.CreatedAt.IsZero() {
		k.CreatedAt = now
	}
	k.UpdatedAt = now
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO keystore_entries(id,tenant_id,name,type,content,fingerprint,size,algorithm,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO NOTHING`,
		k.ID, k.TenantID, k.Name, string(k.Type), k.Content, k.Fingerprint, k.Size, k.Algorithm, k.CreatedAt, k.UpdatedAt)
	return err
}

func (s *pgStore) Get(ctx context.Context, id, tenantID string) (*KeyEntry, error) {
	k := &KeyEntry{}
	var typ string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,type,content,fingerprint,size,algorithm,created_at,updated_at FROM keystore_entries WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&k.ID, &k.TenantID, &k.Name, &typ, &k.Content, &k.Fingerprint, &k.Size, &k.Algorithm, &k.CreatedAt, &k.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k.Type = KeyType(typ)
	return k, nil
}

func (s *pgStore) List(ctx context.Context, tenantID string, keyType KeyType) ([]*KeyEntry, error) {
	query := `SELECT id,tenant_id,name,type,content,fingerprint,size,algorithm,created_at,updated_at FROM keystore_entries WHERE tenant_id=$1`
	args := []any{tenantID}
	if keyType != "" {
		query += ` AND type=$2`
		args = append(args, string(keyType))
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KeyEntry
	for rows.Next() {
		k := &KeyEntry{}
		var typ string
		if err := rows.Scan(&k.ID, &k.TenantID, &k.Name, &typ, &k.Content, &k.Fingerprint, &k.Size, &k.Algorithm, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		k.Type = KeyType(typ)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *pgStore) Update(ctx context.Context, k *KeyEntry) error {
	k.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE keystore_entries SET name=$2,type=$3,content=$4,fingerprint=$5,size=$6,algorithm=$7,updated_at=$8 WHERE id=$1 AND tenant_id=$9`,
		k.ID, k.Name, string(k.Type), k.Content, k.Fingerprint, k.Size, k.Algorithm, k.UpdatedAt, k.TenantID)
	return err
}

func (s *pgStore) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM keystore_entries WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *pgStore) BatchDelete(ctx context.Context, tenantID string, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM keystore_entries WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
