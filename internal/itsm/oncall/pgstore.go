package oncall

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
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS oncall_shifts (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, user_id TEXT, start_time TIMESTAMPTZ, end_time TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_oncall_shifts_tenant ON oncall_shifts(tenant_id, start_time DESC)`,
		`CREATE TABLE IF NOT EXISTS oncall_rotations (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT, members TEXT DEFAULT '[]', cycle_days INTEGER DEFAULT 7, labels TEXT DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_oncall_rotations_tenant ON oncall_rotations(tenant_id)`,
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st); err != nil { slog.Warn("oncall pg init failed", "err", err); return }
	}
}

func (s *pgStore) AddShift(ctx context.Context, sh *Shift) error {
	if sh.ID == "" { sh.ID = uuid.New().String() }
	if sh.TenantID == "" { sh.TenantID = "default" }
	if sh.CreatedAt.IsZero() { sh.CreatedAt = time.Now() }
	_, err := s.db.ExecContext(ctx, `INSERT INTO oncall_shifts(id,tenant_id,user_id,start_time,end_time,created_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`,
		sh.ID, sh.TenantID, sh.UserID, sh.StartTime, sh.EndTime, sh.CreatedAt)
	return err
}

func (s *pgStore) ListShifts(ctx context.Context, tenantID string) ([]*Shift, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,user_id,start_time,end_time,created_at FROM oncall_shifts WHERE tenant_id=$1 ORDER BY start_time DESC LIMIT 100`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*Shift
	for rows.Next() {
		sh := &Shift{}
		if err := rows.Scan(&sh.ID, &sh.TenantID, &sh.UserID, &sh.StartTime, &sh.EndTime, &sh.CreatedAt); err != nil { return nil, err }
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *pgStore) AddRotation(ctx context.Context, r *Rotation) error {
	if r.ID == "" { r.ID = uuid.New().String() }
	if r.TenantID == "" { r.TenantID = "default" }
	if r.CreatedAt.IsZero() { r.CreatedAt = time.Now() }
	if r.UpdatedAt.IsZero() { r.UpdatedAt = r.CreatedAt }
	b, _ := json.Marshal(r.Members)
	l, _ := json.Marshal(r.Labels)
	_, err := s.db.ExecContext(ctx, `INSERT INTO oncall_rotations(id,tenant_id,name,members,cycle_days,labels,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING`,
		r.ID, r.TenantID, r.Name, b, r.CycleDays, l, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *pgStore) ListRotations(ctx context.Context, tenantID string) ([]*Rotation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,name,members,cycle_days,labels,created_at,updated_at FROM oncall_rotations WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*Rotation
	for rows.Next() {
		r := &Rotation{}; var membs, labels []byte
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &membs, &r.CycleDays, &labels, &r.CreatedAt, &r.UpdatedAt); err != nil { return nil, err }
		_ = json.Unmarshal(membs, &r.Members)
		_ = json.Unmarshal(labels, &r.Labels)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *pgStore) DeleteRotation(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oncall_rotations WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}
