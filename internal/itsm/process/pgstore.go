package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, p *Process) error
	Get(ctx context.Context, id, tenantID string) (*Process, error)
	List(ctx context.Context, tenantID string) ([]*Process, error)
	Update(ctx context.Context, p *Process) error
	Delete(ctx context.Context, id, tenantID string) error
	// 流程实例持久化（工单绑定流程后自动创建实例，驱动流转）
	CreateInstance(ctx context.Context, inst *ProcessInstance) error
	GetInstanceByTicket(ctx context.Context, ticketID, tenantID string) (*ProcessInstance, error)
	UpdateInstance(ctx context.Context, inst *ProcessInstance) error
}

type PGStore struct{ db *sql.DB }

func NewPGStore(db *sql.DB) *PGStore {
	s := &PGStore{db: db}
	s.migrate()
	s.migrateInstances()
	return s
}

func (s *PGStore) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_processes (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL,
		description TEXT DEFAULT '',
		category TEXT DEFAULT '',
		steps JSONB DEFAULT '[]',
		labels JSONB DEFAULT '{}',
		enabled BOOLEAN DEFAULT TRUE,
		version INT DEFAULT 1,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
}

func (s *PGStore) Create(ctx context.Context, p *Process) error {
	p.ID = uuid.New().String()
	p.CreatedAt = time.Now()
	p.UpdatedAt = p.CreatedAt
	if p.TenantID == "" { p.TenantID = "default" }
	if p.Version == 0 { p.Version = 1 }
	stepsJSON, _ := json.Marshal(p.Steps)
	labelsJSON, _ := json.Marshal(p.Labels)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO itsm_processes(id,tenant_id,name,description,category,steps,labels,enabled,version,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		p.ID, p.TenantID, p.Name, p.Description, p.Category, stepsJSON, labelsJSON, p.Enabled, p.Version, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *PGStore) Get(ctx context.Context, id, tenantID string) (*Process, error) {
	var stepsJSON, labelsJSON []byte
	p := &Process{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,description,category,steps,labels,enabled,version,created_at,updated_at
		 FROM itsm_processes WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&p.ID, &p.TenantID, &p.Name, &p.Description, &p.Category, &stepsJSON, &labelsJSON, &p.Enabled, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal(stepsJSON, &p.Steps)
	json.Unmarshal(labelsJSON, &p.Labels)
	return p, nil
}

func (s *PGStore) List(ctx context.Context, tenantID string) ([]*Process, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,description,category,steps,labels,enabled,version,created_at,updated_at
		 FROM itsm_processes WHERE tenant_id=$1 ORDER BY updated_at DESC`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*Process
	for rows.Next() {
		var stepsJSON, labelsJSON []byte
		p := &Process{}
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.Description, &p.Category, &stepsJSON, &labelsJSON, &p.Enabled, &p.Version, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal(stepsJSON, &p.Steps)
		json.Unmarshal(labelsJSON, &p.Labels)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PGStore) Update(ctx context.Context, p *Process) error {
	p.UpdatedAt = time.Now()
	stepsJSON, _ := json.Marshal(p.Steps)
	labelsJSON, _ := json.Marshal(p.Labels)
	res, err := s.db.ExecContext(ctx,
		`UPDATE itsm_processes SET name=$1,description=$2,category=$3,steps=$4,labels=$5,enabled=$6,version=$7,updated_at=$8
		 WHERE id=$9 AND tenant_id=$10`,
		p.Name, p.Description, p.Category, stepsJSON, labelsJSON, p.Enabled, p.Version, p.UpdatedAt, p.ID, p.TenantID)
	if err != nil { return err }
	n, _ := res.RowsAffected()
	if n == 0 { return fmt.Errorf("process not found") }
	return nil
}

func (s *PGStore) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM itsm_processes WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}
