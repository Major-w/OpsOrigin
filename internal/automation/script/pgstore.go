package script

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// PGStore 是 script.Store 的 Postgres 实现。
// 当 db==nil 时由 NewService 回退到 MemoryStore(无 DSN 的本地/调试场景)。
type PGStore struct {
	db *sql.DB
}

func NewPGStore(db *sql.DB) *PGStore {
	s := &PGStore{db: db}
	s.init(context.Background())
	return s
}

func (s *PGStore) init(ctx context.Context) {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS automation_scripts (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			content TEXT NOT NULL,
			description TEXT,
			input_params TEXT DEFAULT '[]',
			output_params TEXT DEFAULT '[]',
			timeout INTEGER DEFAULT 0,
			run_as TEXT,
			labels TEXT DEFAULT '{}',
			version INTEGER DEFAULT 1,
			public BOOLEAN DEFAULT false,
			approval_status TEXT DEFAULT 'draft',
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_scripts_tenant ON automation_scripts(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS automation_script_versions (
			id TEXT PRIMARY KEY,
			script_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			content TEXT NOT NULL,
			input_params TEXT DEFAULT '[]',
			output_params TEXT DEFAULT '[]',
			timeout INTEGER DEFAULT 0,
			run_as TEXT,
			change_note TEXT,
			created_by TEXT,
			public BOOLEAN DEFAULT false,
			approval_status TEXT DEFAULT 'draft',
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_script_versions_sid ON automation_script_versions(script_id, tenant_id)`,
		`CREATE TABLE IF NOT EXISTS automation_script_executions (
			id TEXT PRIMARY KEY,
			script_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			status TEXT NOT NULL,
			output TEXT,
			error TEXT,
			exit_code INTEGER DEFAULT 0,
			duration_ms BIGINT DEFAULT 0,
			params TEXT DEFAULT '{}',
			run_as TEXT,
			started_at TIMESTAMPTZ NOT NULL,
			finished_at TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_script_executions_sid ON automation_script_executions(script_id, tenant_id)`,
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st); err != nil {
			slog.Warn("automation script store init DDL failed", "err", err)
			return
		}
	}
}

func toJSON(v any) []byte {
	if v == nil {
		return []byte("null")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func (s *PGStore) Create(ctx context.Context, sc *Script) error {
	if sc.ID == "" {
		sc.ID = uuid.New().String()
	}
	if sc.TenantID == "" {
		sc.TenantID = "default"
	}
	now := time.Now()
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = now
	}
	sc.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_scripts
			(id, tenant_id, name, type, content, description, input_params, output_params, timeout, run_as, labels, version, public, approval_status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO NOTHING`,
		sc.ID, sc.TenantID, sc.Name, string(sc.Type), sc.Content, sc.Description,
		toJSON(sc.InputParams), toJSON(sc.OutputParams), sc.Timeout, sc.RunAs, toJSON(sc.Labels), sc.Version,
		sc.Public, sc.ApprovalStatus, sc.CreatedAt, sc.UpdatedAt,
	)
	return err
}

func (s *PGStore) Get(ctx context.Context, id, tenantID string) (*Script, error) {
	sc := &Script{}
	var inputParams, outputParams, labels []byte
	var typ string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, type, content, description, input_params, output_params, timeout, run_as, labels, version, public, approval_status, created_at, updated_at
		FROM automation_scripts WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&sc.ID, &sc.TenantID, &sc.Name, &typ, &sc.Content, &sc.Description, &inputParams, &outputParams, &sc.Timeout, &sc.RunAs, &labels, &sc.Version, &sc.Public, &sc.ApprovalStatus, &sc.CreatedAt, &sc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sc.Type = ScriptType(typ)
	_ = json.Unmarshal(inputParams, &sc.InputParams)
	_ = json.Unmarshal(outputParams, &sc.OutputParams)
	_ = json.Unmarshal(labels, &sc.Labels)
	return sc, nil
}

func (s *PGStore) List(ctx context.Context, tenantID string) ([]*Script, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, type, content, description, input_params, output_params, timeout, run_as, labels, version, public, approval_status, created_at, updated_at
		FROM automation_scripts WHERE tenant_id=$1 ORDER BY updated_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Script
	for rows.Next() {
		sc := &Script{}
		var inputParams, outputParams, labels []byte
		var typ string
		if err := rows.Scan(&sc.ID, &sc.TenantID, &sc.Name, &typ, &sc.Content, &sc.Description, &inputParams, &outputParams, &sc.Timeout, &sc.RunAs, &labels, &sc.Version, &sc.Public, &sc.ApprovalStatus, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
			return nil, err
		}
		sc.Type = ScriptType(typ)
		_ = json.Unmarshal(inputParams, &sc.InputParams)
		_ = json.Unmarshal(outputParams, &sc.OutputParams)
		_ = json.Unmarshal(labels, &sc.Labels)
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *PGStore) Update(ctx context.Context, sc *Script) error {
	// 获取当前版本号用于自增
	existing, err := s.Get(ctx, sc.ID, sc.TenantID)
	if err != nil || existing == nil {
		return err
	}
	sc.Version = existing.Version + 1
	sc.UpdatedAt = time.Now()
	_, err = s.db.ExecContext(ctx, `
		UPDATE automation_scripts
		SET name=$2, type=$3, content=$4, description=$5, input_params=$6, output_params=$7, timeout=$8, run_as=$9, labels=$10, version=$11, public=$12, approval_status=$13, updated_at=$14
		WHERE id=$1 AND tenant_id=$15`,
		sc.ID, sc.Name, string(sc.Type), sc.Content, sc.Description,
		toJSON(sc.InputParams), toJSON(sc.OutputParams), sc.Timeout, sc.RunAs, toJSON(sc.Labels), sc.Version,
		sc.Public, sc.ApprovalStatus, sc.UpdatedAt, sc.TenantID)
	if err != nil {
		return err
	}
	// 保存版本快照（与 MemoryStore 行为一致）
	sn := ScriptVersion{
		ScriptID:       sc.ID,
		TenantID:       sc.TenantID,
		Version:        sc.Version,
		Name:           sc.Name,
		Type:           sc.Type,
		Content:        sc.Content,
		InputParams:    sc.InputParams,
		OutputParams:   sc.OutputParams,
		Timeout:        sc.Timeout,
		RunAs:          sc.RunAs,
		CreatedAt:      time.Now(),
		Public:         sc.Public,
		ApprovalStatus: sc.ApprovalStatus,
	}
	if n, ok := ctx.Value(changeNoteCtxKey).(string); ok {
		sn.ChangeNote = n
	}
	if u, ok := ctx.Value(userCtxKey).(string); ok {
		sn.CreatedBy = u
	}
	_ = s.SaveVersion(ctx, &sn)
	return nil
}

func (s *PGStore) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM automation_scripts WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *PGStore) SaveVersion(ctx context.Context, v *ScriptVersion) error {
	if v.ID == "" {
		v.ID = uuid.New().String()
	}
	if v.TenantID == "" {
		v.TenantID = "default"
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_script_versions
			(id, script_id, tenant_id, version, name, type, content, input_params, output_params, timeout, run_as, change_note, created_by, public, approval_status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO NOTHING`,
		v.ID, v.ScriptID, v.TenantID, v.Version, v.Name, string(v.Type), v.Content,
		toJSON(v.InputParams), toJSON(v.OutputParams), v.Timeout, v.RunAs, v.ChangeNote, v.CreatedBy, v.Public, v.ApprovalStatus, v.CreatedAt)
	return err
}

func (s *PGStore) ListVersions(ctx context.Context, scriptID, tenantID string) ([]*ScriptVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, script_id, tenant_id, version, name, type, content, input_params, output_params, timeout, run_as, change_note, created_by, created_at
		FROM automation_script_versions WHERE script_id=$1 AND tenant_id=$2 ORDER BY version DESC`, scriptID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ScriptVersion
	for rows.Next() {
		v := &ScriptVersion{}
		var inputParams, outputParams []byte
		var typ string
		if err := rows.Scan(&v.ID, &v.ScriptID, &v.TenantID, &v.Version, &v.Name, &typ, &v.Content, &inputParams, &outputParams, &v.Timeout, &v.RunAs, &v.ChangeNote, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Type = ScriptType(typ)
		_ = json.Unmarshal(inputParams, &v.InputParams)
		_ = json.Unmarshal(outputParams, &v.OutputParams)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PGStore) GetVersion(ctx context.Context, scriptID, tenantID string, version int) (*ScriptVersion, error) {
	v := &ScriptVersion{}
	var inputParams, outputParams []byte
	var typ string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, script_id, tenant_id, version, name, type, content, input_params, output_params, timeout, run_as, change_note, created_by, created_at
		FROM automation_script_versions WHERE script_id=$1 AND tenant_id=$2 AND version=$3`, scriptID, tenantID, version).
		Scan(&v.ID, &v.ScriptID, &v.TenantID, &v.Version, &v.Name, &typ, &v.Content, &inputParams, &outputParams, &v.Timeout, &v.RunAs, &v.ChangeNote, &v.CreatedBy, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.Type = ScriptType(typ)
	_ = json.Unmarshal(inputParams, &v.InputParams)
	_ = json.Unmarshal(outputParams, &v.OutputParams)
	return v, nil
}

func (s *PGStore) SaveExecution(ctx context.Context, r *ExecutionResult) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	if r.TenantID == "" {
		r.TenantID = "default"
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_script_executions
			(id, script_id, tenant_id, status, output, error, exit_code, duration_ms, params, run_as, started_at, finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO NOTHING`,
		r.ID, r.ScriptID, r.TenantID, r.Status, r.Output, r.Error, r.ExitCode, r.Duration, toJSON(r.Params), r.RunAs, r.StartedAt, nullTime(r.FinishedAt))
	return err
}

func (s *PGStore) ListExecutions(ctx context.Context, scriptID, tenantID string) ([]*ExecutionResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, script_id, tenant_id, status, output, error, exit_code, duration_ms, params, run_as, started_at, finished_at
		FROM automation_script_executions WHERE script_id=$1 AND tenant_id=$2 ORDER BY started_at DESC`, scriptID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ExecutionResult
	for rows.Next() {
		r := &ExecutionResult{}
		var params []byte
		var finishedAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.ScriptID, &r.TenantID, &r.Status, &r.Output, &r.Error, &r.ExitCode, &r.Duration, &params, &r.RunAs, &r.StartedAt, &finishedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(params, &r.Params)
		if finishedAt.Valid {
			r.FinishedAt = &finishedAt.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
