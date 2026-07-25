package agentic

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type Store interface {
	CreateAgent(ctx context.Context, a *AgentRecord) error
	GetAgent(ctx context.Context, id, tenantID string) (*AgentRecord, error)
	ListAgents(ctx context.Context, tenantID string) ([]*AgentRecord, error)
	UpdateAgent(ctx context.Context, a *AgentRecord) error
	DeleteAgent(ctx context.Context, id, tenantID string) error

	CreateTeam(ctx context.Context, t *TeamRecord) error
	GetTeam(ctx context.Context, id, tenantID string) (*TeamRecord, error)
	ListTeams(ctx context.Context, tenantID string) ([]*TeamRecord, error)
	UpdateTeam(ctx context.Context, t *TeamRecord) error
	DeleteTeam(ctx context.Context, id, tenantID string) error
}

type AgentRecord struct {
	ID           string          `json:"id"`
	TenantID     string          `json:"tenant_id"`
	Name         string          `json:"name"`
	ModelName    string          `json:"model_name"`
	SystemPrompt string          `json:"system_prompt"`
	Status       string          `json:"status"`
	Extra        json.RawMessage `json:"extra,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type TeamRecord struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenant_id"`
	Name      string          `json:"name"`
	Members   json.RawMessage `json:"members"`    // JSON array of member configs
	Mode      string          `json:"mode"`
	Prompt    string          `json:"prompt,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type PGStore struct{ db *sql.DB }

func NewPGStore(db *sql.DB) *PGStore {
	s := &PGStore{db: db}
	s.migrate()
	return s
}

func (s *PGStore) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS ai_agents (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, model_name TEXT DEFAULT 'deepseek-chat',
		system_prompt TEXT DEFAULT '', status TEXT DEFAULT 'active',
		extra JSONB DEFAULT '{}', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS ai_teams (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, members JSONB DEFAULT '[]',
		mode TEXT DEFAULT 'supervisor', prompt TEXT DEFAULT '',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
}

func (s *PGStore) CreateAgent(ctx context.Context, a *AgentRecord) error {
	a.CreatedAt = time.Now(); a.UpdatedAt = a.CreatedAt
	if a.TenantID == "" { a.TenantID = "default" }
	if a.Status == "" { a.Status = "active" }
	extra, _ := json.Marshal(a.Extra)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_agents(id,tenant_id,name,model_name,system_prompt,status,extra,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		a.ID, a.TenantID, a.Name, a.ModelName, a.SystemPrompt, a.Status, extra, a.CreatedAt, a.UpdatedAt)
	return err
}

func (s *PGStore) GetAgent(ctx context.Context, id, tenantID string) (*AgentRecord, error) {
	a := &AgentRecord{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,model_name,system_prompt,status,extra,created_at,updated_at FROM ai_agents WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.Name, &a.ModelName, &a.SystemPrompt, &a.Status, &a.Extra, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	return a, err
}

func (s *PGStore) ListAgents(ctx context.Context, tenantID string) ([]*AgentRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,model_name,system_prompt,status,extra,created_at,updated_at FROM ai_agents WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*AgentRecord
	for rows.Next() {
		a := &AgentRecord{}
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Name, &a.ModelName, &a.SystemPrompt, &a.Status, &a.Extra, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateAgent(ctx context.Context, a *AgentRecord) error {
	a.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE ai_agents SET name=$1,model_name=$2,system_prompt=$3,status=$4,updated_at=$5 WHERE id=$6 AND tenant_id=$7`,
		a.Name, a.ModelName, a.SystemPrompt, a.Status, a.UpdatedAt, a.ID, a.TenantID)
	return err
}

func (s *PGStore) DeleteAgent(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ai_agents WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *PGStore) CreateTeam(ctx context.Context, t *TeamRecord) error {
	t.CreatedAt = time.Now(); t.UpdatedAt = t.CreatedAt
	if t.TenantID == "" { t.TenantID = "default" }
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_teams(id,tenant_id,name,members,mode,prompt,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		t.ID, t.TenantID, t.Name, t.Members, t.Mode, t.Prompt, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *PGStore) GetTeam(ctx context.Context, id, tenantID string) (*TeamRecord, error) {
	t := &TeamRecord{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,members,mode,prompt,created_at,updated_at FROM ai_teams WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&t.ID, &t.TenantID, &t.Name, &t.Members, &t.Mode, &t.Prompt, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	return t, err
}

func (s *PGStore) ListTeams(ctx context.Context, tenantID string) ([]*TeamRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,members,mode,prompt,created_at,updated_at FROM ai_teams WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []*TeamRecord
	for rows.Next() {
		t := &TeamRecord{}
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.Members, &t.Mode, &t.Prompt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateTeam(ctx context.Context, t *TeamRecord) error {
	t.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE ai_teams SET name=$1,members=$2,mode=$3,prompt=$4,updated_at=$5 WHERE id=$6 AND tenant_id=$7`,
		t.Name, t.Members, t.Mode, t.Prompt, t.UpdatedAt, t.ID, t.TenantID)
	return err
}

func (s *PGStore) DeleteTeam(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ai_teams WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}
