package agentic

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/opsorigin/core/internal/ai/model"
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

	// TokenRecordsSince 返回从 cutoff 起至今的 token 用量记录(按时间升序)。
	TokenRecordsSince(ctx context.Context, cutoff time.Time) ([]model.TokenRecord, error)
	// SaveTokenRecord 写入一条 token 用量记录(供 /ai/token-stats 持久化统计)。
	SaveTokenRecord(ctx context.Context, r *model.TokenRecord) error

	// ——— 自定义 MCP 工具 (PRD 5.2.2) ———
	CreateCustomTool(ctx context.Context, t *CustomTool) error
	UpdateCustomTool(ctx context.Context, t *CustomTool) error
	DeleteCustomTool(ctx context.Context, id, tenantID string) error
	ListCustomTools(ctx context.Context, tenantID string) ([]*CustomTool, error)

	// ——— 主动巡检 Agent (P0) ———
	CreateInspection(ctx context.Context, in *Inspection) error
	GetInspection(ctx context.Context, id, tenantID string) (*Inspection, error)
	ListInspections(ctx context.Context, tenantID string) ([]*Inspection, error)
	UpdateInspection(ctx context.Context, in *Inspection) error
	DeleteInspection(ctx context.Context, id, tenantID string) error
	CreateInspectionRun(ctx context.Context, run *InspectionRun) error
	UpdateInspectionRun(ctx context.Context, run *InspectionRun) error
	ListInspectionRuns(ctx context.Context, inspectionID, tenantID string) ([]*InspectionRun, error)
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

	// KnowledgeCategories 绑定的知识库分类列表, 经 extra JSONB 持久化。
	KnowledgeCategories []string `json:"knowledge_categories,omitempty"`
	// Tools 允许使用的 MCP 工具白名单(空=全部), 经 extra JSONB 持久化。
	Tools []string `json:"tools,omitempty"`
	// Skills 该 Agent 的可复用能力定义, 经 extra JSONB 持久化。
	Skills []model.AgentSkill `json:"skills,omitempty"`
}

// encodeExtra 将 KnowledgeCategories/Tools/Skills 编码进 extra JSONB(合并保留其他字段)。
func (a *AgentRecord) encodeExtra() []byte {
	base := map[string]any{}
	if len(a.Extra) > 0 {
		_ = json.Unmarshal(a.Extra, &base)
	}
	base["knowledge_categories"] = a.KnowledgeCategories
	base["tools"] = a.Tools
	if a.Skills != nil {
		base["skills"] = a.Skills
	}
	b, _ := json.Marshal(base)
	return b
}

// decodeExtra 从 extra JSONB 解析 KnowledgeCategories/Tools/Skills(幂等)。
func (a *AgentRecord) decodeExtra() {
	if len(a.Extra) == 0 {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(a.Extra, &m); err != nil {
		return
	}
	if cats, ok := m["knowledge_categories"].([]any); ok {
		a.KnowledgeCategories = nil
		for _, c := range cats {
			if s, ok := c.(string); ok && s != "" {
				a.KnowledgeCategories = append(a.KnowledgeCategories, s)
			}
		}
	}
	if tools, ok := m["tools"].([]any); ok {
		a.Tools = nil
		for _, t := range tools {
			if s, ok := t.(string); ok && s != "" {
				a.Tools = append(a.Tools, s)
			}
		}
	}
	if raw, ok := m["skills"]; ok {
		if b, err := json.Marshal(raw); err == nil {
			var skills []model.AgentSkill
			if json.Unmarshal(b, &skills) == nil {
				a.Skills = skills
			}
		}
	}
}

type TeamRecord struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenant_id"`
	Name      string          `json:"name"`
	Members   json.RawMessage `json:"members"` // JSON array of member configs
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
	s.db.Exec(`CREATE TABLE IF NOT EXISTS ai_token_records (
		id BIGSERIAL PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT 'default',
		agent_id TEXT DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		prompt_tokens INT NOT NULL DEFAULT 0,
		completion_tokens INT NOT NULL DEFAULT 0,
		total_tokens INT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_token_records_created ON ai_token_records(created_at)`)
	// 自定义 MCP 工具(PRD 5.2.2): 用户注册的 HTTP/CLI 工具定义
	s.db.Exec(`CREATE TABLE IF NOT EXISTS ai_custom_tools (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, description TEXT DEFAULT '', type TEXT DEFAULT 'http',
		enabled BOOLEAN DEFAULT TRUE, parameters JSONB DEFAULT '[]',
		http_method TEXT DEFAULT 'GET', http_url TEXT DEFAULT '', http_headers JSONB DEFAULT '{}',
		timeout_sec INT DEFAULT 30, cli_command TEXT DEFAULT '',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(),
		UNIQUE(tenant_id, name)
	)`)
	// 主动巡检 Agent(P0): 巡检任务定义与运行记录(JSONB 持久化整对象, 与 ai_teams 同构但更紧凑)。
	s.db.Exec(`CREATE TABLE IF NOT EXISTS ai_inspections (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, def JSONB NOT NULL DEFAULT '{}',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS ai_inspection_runs (
		id TEXT PRIMARY KEY, inspection_id TEXT NOT NULL, tenant_id TEXT NOT NULL DEFAULT 'default',
		run JSONB NOT NULL DEFAULT '{}', started_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_inspections_tenant ON ai_inspections(tenant_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_inspection_runs_insp ON ai_inspection_runs(inspection_id)`)
}

// ——— 自定义 MCP 工具持久化 (PRD 5.2.2) ———

func (s *PGStore) CreateCustomTool(ctx context.Context, t *CustomTool) error {
	t.CreatedAt = time.Now()
	t.UpdatedAt = t.CreatedAt
	if t.TenantID == "" {
		t.TenantID = "default"
	}
	params, _ := json.Marshal(t.Parameters)
	headers, _ := json.Marshal(t.HTTPHeaders)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_custom_tools(id,tenant_id,name,description,type,enabled,parameters,http_method,http_url,http_headers,timeout_sec,cli_command,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		t.ID, t.TenantID, t.Name, t.Description, t.Type, t.Enabled, params, t.HTTPMethod, t.HTTPURL, headers, t.TimeoutSec, t.CLICommand, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *PGStore) UpdateCustomTool(ctx context.Context, t *CustomTool) error {
	t.UpdatedAt = time.Now()
	params, _ := json.Marshal(t.Parameters)
	headers, _ := json.Marshal(t.HTTPHeaders)
	_, err := s.db.ExecContext(ctx,
		`UPDATE ai_custom_tools SET name=$1,description=$2,type=$3,enabled=$4,parameters=$5,http_method=$6,http_url=$7,http_headers=$8,timeout_sec=$9,cli_command=$10,updated_at=$11 WHERE id=$12 AND tenant_id=$13`,
		t.Name, t.Description, t.Type, t.Enabled, params, t.HTTPMethod, t.HTTPURL, headers, t.TimeoutSec, t.CLICommand, t.UpdatedAt, t.ID, t.TenantID)
	return err
}

func (s *PGStore) DeleteCustomTool(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ai_custom_tools WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *PGStore) ListCustomTools(ctx context.Context, tenantID string) ([]*CustomTool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,description,type,enabled,parameters,http_method,http_url,http_headers,timeout_sec,cli_command,created_at,updated_at FROM ai_custom_tools WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CustomTool
	for rows.Next() {
		t := &CustomTool{}
		var params, headers []byte
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.Description, &t.Type, &t.Enabled, &params, &t.HTTPMethod, &t.HTTPURL, &headers, &t.TimeoutSec, &t.CLICommand, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if len(params) > 0 {
			_ = json.Unmarshal(params, &t.Parameters)
		}
		if len(headers) > 0 {
			_ = json.Unmarshal(headers, &t.HTTPHeaders)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ——— 主动巡检持久化 (P0) ———
// 采用 def/run JSONB 整对象存储: 巡检定义字段演进频繁(周期/范围/风险策略),
// 避免每次加字段都要改表; 检索维度仅 tenant/inspection_id, 已建索引。

func (s *PGStore) CreateInspection(ctx context.Context, in *Inspection) error {
	if in.TenantID == "" {
		in.TenantID = "default"
	}
	def, _ := json.Marshal(in)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_inspections(id,tenant_id,name,def,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, def=EXCLUDED.def, updated_at=EXCLUDED.updated_at`,
		in.ID, in.TenantID, in.Name, def, in.CreatedAt, in.UpdatedAt)
	return err
}

func (s *PGStore) GetInspection(ctx context.Context, id, tenantID string) (*Inspection, error) {
	var def []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT def FROM ai_inspections WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan(&def)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	in := &Inspection{}
	if err := json.Unmarshal(def, in); err != nil {
		return nil, err
	}
	return in, nil
}

func (s *PGStore) ListInspections(ctx context.Context, tenantID string) ([]*Inspection, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if tenantID == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT def FROM ai_inspections ORDER BY created_at`)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT def FROM ai_inspections WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Inspection
	for rows.Next() {
		var def []byte
		if err := rows.Scan(&def); err != nil {
			return nil, err
		}
		in := &Inspection{}
		if json.Unmarshal(def, in) != nil {
			continue
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateInspection(ctx context.Context, in *Inspection) error {
	in.UpdatedAt = time.Now()
	def, _ := json.Marshal(in)
	_, err := s.db.ExecContext(ctx,
		`UPDATE ai_inspections SET name=$1, def=$2, updated_at=$3 WHERE id=$4 AND tenant_id=$5`,
		in.Name, def, in.UpdatedAt, in.ID, in.TenantID)
	return err
}

func (s *PGStore) DeleteInspection(ctx context.Context, id, tenantID string) error {
	// 先删运行记录再删定义, 避免遗留孤儿数据。
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ai_inspection_runs WHERE inspection_id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM ai_inspections WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *PGStore) CreateInspectionRun(ctx context.Context, run *InspectionRun) error {
	if run.TenantID == "" {
		run.TenantID = "default"
	}
	body, _ := json.Marshal(run)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_inspection_runs(id,inspection_id,tenant_id,run,started_at) VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (id) DO UPDATE SET run=EXCLUDED.run`,
		run.ID, run.InspectionID, run.TenantID, body, run.StartedAt)
	return err
}

func (s *PGStore) ListInspectionRuns(ctx context.Context, inspectionID, tenantID string) ([]*InspectionRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT run FROM ai_inspection_runs WHERE inspection_id=$1 AND tenant_id=$2 ORDER BY started_at DESC LIMIT 50`,
		inspectionID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InspectionRun
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		r := &InspectionRun{}
		if err := json.Unmarshal(body, r); err != nil {
			slog.Warn("skip inspection run with invalid payload", "inspection_id", inspectionID, "err", err)
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateInspectionRun(ctx context.Context, run *InspectionRun) error {
	body, _ := json.Marshal(run)
	_, err := s.db.ExecContext(ctx,
		`UPDATE ai_inspection_runs SET run=$1 WHERE id=$2 AND tenant_id=$3`,
		body, run.ID, run.TenantID)
	return err
}

func (s *PGStore) CreateAgent(ctx context.Context, a *AgentRecord) error {
	a.CreatedAt = time.Now()
	a.UpdatedAt = a.CreatedAt
	if a.TenantID == "" {
		a.TenantID = "default"
	}
	if a.Status == "" {
		a.Status = "active"
	}
	extra := a.encodeExtra()
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
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err == nil {
		a.decodeExtra()
	}
	return a, err
}

func (s *PGStore) ListAgents(ctx context.Context, tenantID string) ([]*AgentRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,model_name,system_prompt,status,extra,created_at,updated_at FROM ai_agents WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AgentRecord
	for rows.Next() {
		a := &AgentRecord{}
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Name, &a.ModelName, &a.SystemPrompt, &a.Status, &a.Extra, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.decodeExtra()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateAgent(ctx context.Context, a *AgentRecord) error {
	a.UpdatedAt = time.Now()
	extra := a.encodeExtra()
	_, err := s.db.ExecContext(ctx,
		`UPDATE ai_agents SET name=$1,model_name=$2,system_prompt=$3,status=$4,extra=$5,updated_at=$6 WHERE id=$7 AND tenant_id=$8`,
		a.Name, a.ModelName, a.SystemPrompt, a.Status, extra, a.UpdatedAt, a.ID, a.TenantID)
	return err
}

func (s *PGStore) DeleteAgent(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ai_agents WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *PGStore) CreateTeam(ctx context.Context, t *TeamRecord) error {
	t.CreatedAt = time.Now()
	t.UpdatedAt = t.CreatedAt
	if t.TenantID == "" {
		t.TenantID = "default"
	}
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
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *PGStore) ListTeams(ctx context.Context, tenantID string) ([]*TeamRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,members,mode,prompt,created_at,updated_at FROM ai_teams WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
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

// TokenRecordsSince 返回从 cutoff 起至今的 token 用量记录(按时间升序)。
func (s *PGStore) TokenRecordsSince(ctx context.Context, cutoff time.Time) ([]model.TokenRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT created_at, agent_id, model, prompt_tokens, completion_tokens, total_tokens
		 FROM ai_token_records WHERE created_at >= $1 ORDER BY created_at`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TokenRecord
	for rows.Next() {
		r := model.TokenRecord{}
		if err := rows.Scan(&r.Time, &r.AgentID, &r.Model, &r.Prompt, &r.Completion, &r.Total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveTokenRecord 写入一条 token 用量记录。
func (s *PGStore) SaveTokenRecord(ctx context.Context, r *model.TokenRecord) error {
	if r.Time.IsZero() {
		r.Time = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_token_records(tenant_id, agent_id, model, prompt_tokens, completion_tokens, total_tokens, created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`,
		"default", r.AgentID, r.Model, r.Prompt, r.Completion, r.Total, r.Time)
	return err
}
