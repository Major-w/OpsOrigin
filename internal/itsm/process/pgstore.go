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
	// ListRunningInstances 返回所有 running 状态的实例(供定时器/扫描器轮询)。
	ListRunningInstances(ctx context.Context) ([]*ProcessInstance, error)
	// 流程定义版本快照(每次 Update 前归档旧定义, 支持版本列表/回看/差异对比)
	SaveVersion(ctx context.Context, v *ProcessVersion) error
	ListVersions(ctx context.Context, processID, tenantID string) ([]*ProcessVersion, error)
	GetVersion(ctx context.Context, processID, tenantID string, version int) (*ProcessVersion, error)
}

type PGStore struct{ db *sql.DB }

func NewPGStore(db *sql.DB) *PGStore {
	s := &PGStore{db: db}
	if db != nil {
		s.migrate()
		s.migrateInstances()
		s.migrateVersions()
	}
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
	// 流程级主表单 / SLA 关联 / 通知策略(向导式流程定义: 表单→编排→高级设置)
	s.db.Exec(`ALTER TABLE itsm_processes ADD COLUMN IF NOT EXISTS form JSONB DEFAULT '{}'::jsonb`)
	s.db.Exec(`ALTER TABLE itsm_processes ADD COLUMN IF NOT EXISTS sla_policy_id TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE itsm_processes ADD COLUMN IF NOT EXISTS notify_config JSONB DEFAULT '{}'::jsonb`)
}

func (s *PGStore) Create(ctx context.Context, p *Process) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	p.ID = uuid.New().String()
	p.CreatedAt = time.Now()
	p.UpdatedAt = p.CreatedAt
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	if p.Version == 0 {
		p.Version = 1
	}
	stepsJSON, _ := json.Marshal(p.Steps)
	labelsJSON, _ := json.Marshal(p.Labels)
	formJSON := p.Form
	if len(formJSON) == 0 {
		formJSON = []byte("{}")
	}
	notifyJSON := p.NotifyConfig
	if len(notifyJSON) == 0 {
		notifyJSON = []byte("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO itsm_processes(id,tenant_id,name,description,category,steps,labels,enabled,version,form,sla_policy_id,notify_config,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		p.ID, p.TenantID, p.Name, p.Description, p.Category, stepsJSON, labelsJSON, p.Enabled, p.Version, formJSON, p.SlaPolicyID, notifyJSON, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *PGStore) Get(ctx context.Context, id, tenantID string) (*Process, error) {
	if s.db == nil {
		return nil, nil
	}
	var stepsJSON, labelsJSON, formJSON, notifyJSON []byte
	p := &Process{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,description,category,steps,labels,enabled,version,form,sla_policy_id,notify_config,created_at,updated_at
		 FROM itsm_processes WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&p.ID, &p.TenantID, &p.Name, &p.Description, &p.Category, &stepsJSON, &labelsJSON, &p.Enabled, &p.Version, &formJSON, &p.SlaPolicyID, &notifyJSON, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal(stepsJSON, &p.Steps)
	json.Unmarshal(labelsJSON, &p.Labels)
	if len(formJSON) > 0 && string(formJSON) != "null" {
		p.Form = formJSON
	}
	if len(notifyJSON) > 0 && string(notifyJSON) != "null" {
		p.NotifyConfig = notifyJSON
	}
	return p, nil
}

func (s *PGStore) List(ctx context.Context, tenantID string) ([]*Process, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,description,category,steps,labels,enabled,version,form,sla_policy_id,notify_config,created_at,updated_at
		 FROM itsm_processes WHERE tenant_id=$1 ORDER BY updated_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Process
	for rows.Next() {
		var stepsJSON, labelsJSON, formJSON, notifyJSON []byte
		p := &Process{}
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.Description, &p.Category, &stepsJSON, &labelsJSON, &p.Enabled, &p.Version, &formJSON, &p.SlaPolicyID, &notifyJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal(stepsJSON, &p.Steps)
		json.Unmarshal(labelsJSON, &p.Labels)
		if len(formJSON) > 0 && string(formJSON) != "null" {
			p.Form = formJSON
		}
		if len(notifyJSON) > 0 && string(notifyJSON) != "null" {
			p.NotifyConfig = notifyJSON
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PGStore) Update(ctx context.Context, p *Process) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	p.UpdatedAt = time.Now()
	stepsJSON, _ := json.Marshal(p.Steps)
	labelsJSON, _ := json.Marshal(p.Labels)
	formJSON := p.Form
	if len(formJSON) == 0 {
		formJSON = []byte("{}")
	}
	notifyJSON := p.NotifyConfig
	if len(notifyJSON) == 0 {
		notifyJSON = []byte("{}")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE itsm_processes SET name=$1,description=$2,category=$3,steps=$4,labels=$5,enabled=$6,version=$7,form=$8,sla_policy_id=$9,notify_config=$10,updated_at=$11
		 WHERE id=$12 AND tenant_id=$13`,
		p.Name, p.Description, p.Category, stepsJSON, labelsJSON, p.Enabled, p.Version, formJSON, p.SlaPolicyID, notifyJSON, p.UpdatedAt, p.ID, p.TenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("process not found")
	}
	return nil
}

func (s *PGStore) Delete(ctx context.Context, id, tenantID string) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM itsm_processes WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}
