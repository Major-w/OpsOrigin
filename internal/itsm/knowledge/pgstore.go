package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type pgStore struct{ db *sql.DB }

func newPGStore(db *sql.DB) *pgStore {
	s := &pgStore{db: db}
	s.init(context.Background())
	return s
}

func (s *pgStore) init(ctx context.Context) {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS knowledge_articles (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, title TEXT, content TEXT,
			category TEXT, tags TEXT DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`)
	if err != nil { slog.Warn("knowledge pg init failed", "err", err); return }
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_tenant ON knowledge_articles(tenant_id, category)`)
}

func (s *pgStore) Create(ctx context.Context, a *Article) error {
	if a.ID == "" { a.ID = uuid.New().String() }
	if a.TenantID == "" { a.TenantID = "default" }
	now := time.Now(); if a.CreatedAt.IsZero() { a.CreatedAt = now }
	a.UpdatedAt = now
	b, _ := json.Marshal(a.Tags)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO knowledge_articles(id,tenant_id,title,content,category,tags,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING`,
		a.ID, a.TenantID, a.Title, a.Content, a.Category, b, a.CreatedAt, a.UpdatedAt)
	return err
}

func (s *pgStore) Get(ctx context.Context, id, tenantID string) (*Article, error) {
	a := &Article{}; var tags []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,tenant_id,title,content,category,tags,created_at,updated_at FROM knowledge_articles WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	_ = json.Unmarshal(tags, &a.Tags)
	return a, nil
}

func (s *pgStore) List(ctx context.Context, tenantID, category string) ([]*Article, error) {
	q := `SELECT id,tenant_id,title,content,category,tags,created_at,updated_at FROM knowledge_articles WHERE tenant_id=$1`
	args := []any{tenantID}
	if category != "" { q += ` AND category=$2`; args = append(args, category) }
	q += ` ORDER BY updated_at DESC LIMIT 200`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*Article
	for rows.Next() {
		a := &Article{}; var tags []byte
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.CreatedAt, &a.UpdatedAt); err != nil { return nil, err }
		_ = json.Unmarshal(tags, &a.Tags)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *pgStore) Update(ctx context.Context, a *Article) error {
	a.UpdatedAt = time.Now(); b, _ := json.Marshal(a.Tags)
	_, err := s.db.ExecContext(ctx, `UPDATE knowledge_articles SET title=$2,content=$3,category=$4,tags=$5,updated_at=$6 WHERE id=$1 AND tenant_id=$7`,
		a.ID, a.Title, a.Content, a.Category, b, a.UpdatedAt, a.TenantID)
	return err
}

func (s *pgStore) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_articles WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}
