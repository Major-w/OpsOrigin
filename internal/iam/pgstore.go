package iam

import (
	"context"
	"database/sql"
	"encoding/json"
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
		`CREATE TABLE IF NOT EXISTS iam_roles (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT, display_name TEXT,
			description TEXT, permissions TEXT DEFAULT '[]', is_built_in BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_iam_roles_tenant ON iam_roles(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS iam_groups (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT, description TEXT,
			user_ids TEXT DEFAULT '[]', created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_iam_groups_tenant ON iam_groups(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS iam_user_roles (
			user_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			role_id TEXT NOT NULL,
			PRIMARY KEY (user_id, tenant_id, role_id))`,
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st); err != nil {
			slog.Warn("iam pg store init DDL failed", "err", err)
			return
		}
	}
}

func (s *pgStore) CreateRole(ctx context.Context, r *Role) error {
	if r.ID == "" { r.ID = uuid.New().String() }
	if r.TenantID == "" { r.TenantID = "default" }
	now := time.Now()
	if r.CreatedAt.IsZero() { r.CreatedAt = now }
	r.UpdatedAt = now
	b, _ := json.Marshal(r.Permissions)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO iam_roles(id,tenant_id,name,display_name,description,permissions,is_built_in,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(id) DO NOTHING`,
		r.ID, r.TenantID, r.Name, r.DisplayName, r.Description, b, r.IsBuiltIn, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *pgStore) GetRole(ctx context.Context, id, tenantID string) (*Role, error) {
	r := &Role{}
	var perms []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,display_name,description,permissions,is_built_in,created_at,updated_at FROM iam_roles WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&r.ID, &r.TenantID, &r.Name, &r.DisplayName, &r.Description, &perms, &r.IsBuiltIn, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	_ = json.Unmarshal(perms, &r.Permissions)
	return r, nil
}

func (s *pgStore) GetRoleByName(ctx context.Context, name, tenantID string) (*Role, error) {
	r := &Role{}
	var perms []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,display_name,description,permissions,is_built_in,created_at,updated_at FROM iam_roles WHERE name=$1 AND tenant_id=$2`, name, tenantID).
		Scan(&r.ID, &r.TenantID, &r.Name, &r.DisplayName, &r.Description, &perms, &r.IsBuiltIn, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	_ = json.Unmarshal(perms, &r.Permissions)
	return r, nil
}

func (s *pgStore) ListRoles(ctx context.Context, tenantID string) ([]*Role, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,display_name,description,permissions,is_built_in,created_at,updated_at FROM iam_roles WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*Role
	for rows.Next() {
		r := &Role{}
		var perms []byte
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &r.DisplayName, &r.Description, &perms, &r.IsBuiltIn, &r.CreatedAt, &r.UpdatedAt); err != nil { return nil, err }
		_ = json.Unmarshal(perms, &r.Permissions)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *pgStore) UpdateRole(ctx context.Context, r *Role) error {
	r.UpdatedAt = time.Now()
	b, _ := json.Marshal(r.Permissions)
	_, err := s.db.ExecContext(ctx,
		`UPDATE iam_roles SET name=$2,display_name=$3,description=$4,permissions=$5,is_built_in=$6,updated_at=$7 WHERE id=$1 AND tenant_id=$8`,
		r.ID, r.Name, r.DisplayName, r.Description, b, r.IsBuiltIn, r.UpdatedAt, r.TenantID)
	return err
}

func (s *pgStore) DeleteRole(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM iam_roles WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *pgStore) CreateGroup(ctx context.Context, g *Group) error {
	if g.ID == "" { g.ID = uuid.New().String() }
	if g.TenantID == "" { g.TenantID = "default" }
	now := time.Now()
	if g.CreatedAt.IsZero() { g.CreatedAt = now }
	g.UpdatedAt = now
	b, _ := json.Marshal(g.UserIDs)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO iam_groups(id,tenant_id,name,description,user_ids,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`,
		g.ID, g.TenantID, g.Name, g.Description, b, g.CreatedAt, g.UpdatedAt)
	return err
}

func (s *pgStore) GetGroup(ctx context.Context, id, tenantID string) (*Group, error) {
	g := &Group{}
	var uids []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,description,user_ids,created_at,updated_at FROM iam_groups WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&g.ID, &g.TenantID, &g.Name, &g.Description, &uids, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	_ = json.Unmarshal(uids, &g.UserIDs)
	return g, nil
}

func (s *pgStore) ListGroups(ctx context.Context, tenantID string) ([]*Group, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,description,user_ids,created_at,updated_at FROM iam_groups WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		g := &Group{}
		var uids []byte
		if err := rows.Scan(&g.ID, &g.TenantID, &g.Name, &g.Description, &uids, &g.CreatedAt, &g.UpdatedAt); err != nil { return nil, err }
		_ = json.Unmarshal(uids, &g.UserIDs)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *pgStore) UpdateGroup(ctx context.Context, g *Group) error {
	g.UpdatedAt = time.Now()
	b, _ := json.Marshal(g.UserIDs)
	_, err := s.db.ExecContext(ctx,
		`UPDATE iam_groups SET name=$2,description=$3,user_ids=$4,updated_at=$5 WHERE id=$1 AND tenant_id=$6`,
		g.ID, g.Name, g.Description, b, g.UpdatedAt, g.TenantID)
	return err
}

func (s *pgStore) DeleteGroup(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM iam_groups WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *pgStore) SetUserRoles(ctx context.Context, userID, tenantID string, roleIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM iam_user_roles WHERE user_id=$1 AND tenant_id=$2`, userID, tenantID); err != nil { return err }
	for _, rid := range roleIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO iam_user_roles(user_id,tenant_id,role_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, userID, tenantID, rid); err != nil { return err }
	}
	return tx.Commit()
}

func (s *pgStore) GetUserRoles(ctx context.Context, userID, tenantID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role_id FROM iam_user_roles WHERE user_id=$1 AND tenant_id=$2`, userID, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []string
	for rows.Next() {
		var rid string
		if err := rows.Scan(&rid); err != nil { return nil, err }
		out = append(out, rid)
	}
	return out, rows.Err()
}
