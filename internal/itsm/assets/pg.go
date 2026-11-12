package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/itsm/form"
)

// PGStore 资产持久化（PostgreSQL）。
// 表结构由 Init 自愈 DDL 创建，并与 deploy/migrations/039_itsm_decision_action_assets.sql 双写。
// 所有列均为 NOT NULL DEFAULT，避免 rows.Scan 直扫 NULL 导致整条查询失败（ITSM 历史踩坑）。
type PGStore struct {
	db *sql.DB
}

func NewPGStore(db *sql.DB) *PGStore { return &PGStore{db: db} }

func (s *PGStore) Init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS itsm_decision_tables (
			id          TEXT PRIMARY KEY,
			tenant_id   TEXT NOT NULL DEFAULT 'default',
			name        TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'draft',
			version     INTEGER NOT NULL DEFAULT 0,
			table_json  TEXT NOT NULL DEFAULT '{}',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_itsm_dt_tenant ON itsm_decision_tables(tenant_id, name)`,
		`CREATE TABLE IF NOT EXISTS itsm_decision_table_versions (
			id         TEXT PRIMARY KEY,
			tenant_id  TEXT NOT NULL DEFAULT 'default',
			asset_id   TEXT NOT NULL,
			version    INTEGER NOT NULL,
			table_json TEXT NOT NULL DEFAULT '{}',
			note       TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (tenant_id, asset_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_itsm_dtv_asset ON itsm_decision_table_versions(tenant_id, asset_id, version DESC)`,
		`CREATE TABLE IF NOT EXISTS itsm_action_templates (
			id          TEXT PRIMARY KEY,
			tenant_id   TEXT NOT NULL DEFAULT 'default',
			name        TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			action_type TEXT NOT NULL,
			inputs      TEXT NOT NULL DEFAULT '{}',
			enabled     BOOLEAN NOT NULL DEFAULT TRUE,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_itsm_at_tenant ON itsm_action_templates(tenant_id, action_type)`,
		// 动作模板版本化（040 迁移同步）：存量表用幂等 ALTER 补列，不改既有数据。
		`ALTER TABLE itsm_action_templates ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft'`,
		`ALTER TABLE itsm_action_templates ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS itsm_action_template_versions (
			id          TEXT PRIMARY KEY,
			tenant_id   TEXT NOT NULL DEFAULT 'default',
			template_id TEXT NOT NULL,
			version     INTEGER NOT NULL,
			action_type TEXT NOT NULL,
			inputs      TEXT NOT NULL DEFAULT '{}',
			note        TEXT NOT NULL DEFAULT '',
			created_by  TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (tenant_id, template_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_itsm_atv_template ON itsm_action_template_versions(tenant_id, template_id, version DESC)`,
	}
	for i, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init asset DDL[%d]: %w", i, err)
		}
	}
	return nil
}

// --- 决策表资产 ---

func scanDecision(scanner interface{ Scan(...any) error }) (*DecisionTableAsset, error) {
	var a DecisionTableAsset
	var tableJSON string
	if err := scanner.Scan(&a.ID, &a.TenantID, &a.Name, &a.Description, &a.Status, &a.Version,
		&tableJSON, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if tableJSON != "" && tableJSON != "{}" {
		_ = json.Unmarshal([]byte(tableJSON), &a.Table)
	}
	return &a, nil
}

const decisionCols = `id, tenant_id, name, description, status, version, table_json, created_at, updated_at`

func (s *PGStore) ListDecisionTables(ctx context.Context, tenantID string) ([]*DecisionTableAsset, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+decisionCols+` FROM itsm_decision_tables WHERE tenant_id = $1 ORDER BY name, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DecisionTableAsset{}
	for rows.Next() {
		a, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) GetDecisionTable(ctx context.Context, tenantID, id string) (*DecisionTableAsset, error) {
	a, err := scanDecision(s.db.QueryRowContext(ctx,
		`SELECT `+decisionCols+` FROM itsm_decision_tables WHERE id = $1 AND tenant_id = $2`, id, tenantID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *PGStore) UpsertDecisionTable(ctx context.Context, tenantID string, a *DecisionTableAsset) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	tableJSON, err := json.Marshal(a.Table)
	if err != nil {
		return err
	}
	a.TenantID = tenantID
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO itsm_decision_tables (id, tenant_id, name, description, status, version, table_json)
		VALUES ($1, $2, $3, $4, 'draft', 0, $5)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			table_json = EXCLUDED.table_json,
			updated_at = NOW()
		WHERE itsm_decision_tables.tenant_id = EXCLUDED.tenant_id`,
		a.ID, tenantID, a.Name, a.Description, string(tableJSON))
	if err != nil {
		return err
	}
	// 冲突但租户不匹配时 RowsAffected=0，按未找到处理，避免跨租户改写。
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) DeleteDecisionTable(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM itsm_decision_tables WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM itsm_decision_table_versions WHERE asset_id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}

// Publish 以给定内容追加一个不可变版本，并在同一事务内把资产置为 published。
// 行锁（FOR UPDATE）保证并发发布版本号不重复；UNIQUE(tenant_id, asset_id, version) 兜底。
func (s *PGStore) Publish(ctx context.Context, tenantID, id string, table form.DecisionTable, note, createdBy string) (*DecisionTableVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRowContext(ctx,
		`SELECT version FROM itsm_decision_tables WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		id, tenantID).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	next := current + 1
	tableJSON, err := json.Marshal(table)
	if err != nil {
		return nil, err
	}
	v := &DecisionTableVersion{
		ID: uuid.New().String(), TenantID: tenantID, AssetID: id,
		Version: next, Table: table, Note: note, CreatedBy: createdBy, CreatedAt: time.Now(),
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO itsm_decision_table_versions (id, tenant_id, asset_id, version, table_json, note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		v.ID, tenantID, id, next, string(tableJSON), note, createdBy); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE itsm_decision_tables SET status = 'published', version = $1, table_json = $2, updated_at = NOW()
		WHERE id = $3 AND tenant_id = $4`, next, string(tableJSON), id, tenantID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return v, nil
}

func scanVersion(scanner interface{ Scan(...any) error }) (*DecisionTableVersion, error) {
	var v DecisionTableVersion
	var tableJSON string
	if err := scanner.Scan(&v.ID, &v.TenantID, &v.AssetID, &v.Version, &tableJSON,
		&v.Note, &v.CreatedBy, &v.CreatedAt); err != nil {
		return nil, err
	}
	if tableJSON != "" && tableJSON != "{}" {
		_ = json.Unmarshal([]byte(tableJSON), &v.Table)
	}
	return &v, nil
}

const versionCols = `id, tenant_id, asset_id, version, table_json, note, created_by, created_at`

func (s *PGStore) ListVersions(ctx context.Context, tenantID, assetID string) ([]*DecisionTableVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+versionCols+` FROM itsm_decision_table_versions WHERE tenant_id = $1 AND asset_id = $2 ORDER BY version DESC`,
		tenantID, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DecisionTableVersion{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PGStore) GetVersion(ctx context.Context, tenantID, assetID string, version int) (*DecisionTableVersion, error) {
	v, err := scanVersion(s.db.QueryRowContext(ctx,
		`SELECT `+versionCols+` FROM itsm_decision_table_versions
		 WHERE tenant_id = $1 AND asset_id = $2 AND version = $3`, tenantID, assetID, version))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return v, err
}

// ResolveDecisionTable 实现 form.DecisionTableResolver。
// version=0 跟随资产最新已发布版本；未发布/删除/版本缺失返回 (nil, nil) 供调用方容错跳过。
func (s *PGStore) ResolveDecisionTable(ctx context.Context, tenantID, assetID string, version int) (*form.DecisionTable, error) {
	a, err := s.GetDecisionTable(ctx, tenantID, assetID)
	if err != nil {
		return nil, nil
	}
	if a.Status != StatusPublished || a.Version < 1 {
		return nil, nil
	}
	if version == 0 {
		version = a.Version
	}
	v, err := s.GetVersion(ctx, tenantID, assetID, version)
	if err != nil || v == nil {
		return nil, nil
	}
	t := v.Table
	t.Name = a.Name
	t.AssetRef = nil
	return &t, nil
}

// --- 动作模板 ---

func scanTemplate(scanner interface{ Scan(...any) error }) (*ActionTemplate, error) {
	var t ActionTemplate
	var inputsJSON string
	if err := scanner.Scan(&t.ID, &t.TenantID, &t.Name, &t.Description, &t.ActionType,
		&inputsJSON, &t.Enabled, &t.Status, &t.Version, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Inputs = map[string]any{}
	if inputsJSON != "" && inputsJSON != "{}" {
		_ = json.Unmarshal([]byte(inputsJSON), &t.Inputs)
	}
	if t.Inputs == nil {
		t.Inputs = map[string]any{}
	}
	return &t, nil
}

const templateCols = `id, tenant_id, name, description, action_type, inputs, enabled, status, version, created_at, updated_at`

func scanTemplateVersion(scanner interface{ Scan(...any) error }) (*ActionTemplateVersion, error) {
	var v ActionTemplateVersion
	var inputsJSON string
	if err := scanner.Scan(&v.ID, &v.TenantID, &v.TemplateID, &v.Version, &v.ActionType,
		&inputsJSON, &v.Note, &v.CreatedBy, &v.CreatedAt); err != nil {
		return nil, err
	}
	v.Inputs = map[string]any{}
	if inputsJSON != "" && inputsJSON != "{}" {
		_ = json.Unmarshal([]byte(inputsJSON), &v.Inputs)
	}
	if v.Inputs == nil {
		v.Inputs = map[string]any{}
	}
	return &v, nil
}

const templateVersionCols = `id, tenant_id, template_id, version, action_type, inputs, note, created_by, created_at`

func (s *PGStore) ListActionTemplates(ctx context.Context, tenantID string) ([]*ActionTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+templateCols+` FROM itsm_action_templates WHERE tenant_id = $1 ORDER BY action_type, name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ActionTemplate{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) GetActionTemplate(ctx context.Context, tenantID, id string) (*ActionTemplate, error) {
	t, err := scanTemplate(s.db.QueryRowContext(ctx,
		`SELECT `+templateCols+` FROM itsm_action_templates WHERE id = $1 AND tenant_id = $2`, id, tenantID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *PGStore) UpsertActionTemplate(ctx context.Context, tenantID string, t *ActionTemplate) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.Inputs == nil {
		t.Inputs = map[string]any{}
	}
	inputsJSON, err := json.Marshal(t.Inputs)
	if err != nil {
		return err
	}
	t.TenantID = tenantID
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO itsm_action_templates (id, tenant_id, name, description, action_type, inputs, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			action_type = EXCLUDED.action_type,
			inputs = EXCLUDED.inputs,
			enabled = EXCLUDED.enabled,
			updated_at = NOW()
		WHERE itsm_action_templates.tenant_id = EXCLUDED.tenant_id`,
		t.ID, tenantID, t.Name, t.Description, t.ActionType, string(inputsJSON), t.Enabled)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) DeleteActionTemplate(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM itsm_action_templates WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM itsm_action_template_versions WHERE template_id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}

// PublishActionTemplate 以模板当前草稿内容追加不可变版本快照，并在同一事务内
// 递增版本号、置为 published。行锁 + UNIQUE(tenant_id, template_id, version) 防并发重号。
func (s *PGStore) PublishActionTemplate(ctx context.Context, tenantID, id, note, createdBy string) (*ActionTemplateVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var actionType, inputsJSON string
	var enabled bool
	var current int
	if err = tx.QueryRowContext(ctx,
		`SELECT action_type, inputs, enabled, version FROM itsm_action_templates
		 WHERE id = $1 AND tenant_id = $2 FOR UPDATE`, id, tenantID).
		Scan(&actionType, &inputsJSON, &enabled, &current); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !IsBuiltinActionType(actionType) {
		return nil, fmt.Errorf("action_type %q 不在内置动作白名单内", actionType)
	}
	if !enabled {
		return nil, fmt.Errorf("动作模板已停用，不能发布")
	}
	inputs := map[string]any{}
	if inputsJSON != "" && inputsJSON != "{}" {
		_ = json.Unmarshal([]byte(inputsJSON), &inputs)
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return nil, err
	}
	v := &ActionTemplateVersion{
		ID: uuid.New().String(), TenantID: tenantID, TemplateID: id,
		Version: current + 1, ActionType: actionType, Inputs: inputs,
		Note: note, CreatedBy: createdBy, CreatedAt: time.Now(),
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO itsm_action_template_versions
			(id, tenant_id, template_id, version, action_type, inputs, note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		v.ID, tenantID, id, v.Version, actionType, string(encoded), note, createdBy); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE itsm_action_templates SET status = 'published', version = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3`, v.Version, id, tenantID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *PGStore) ListActionTemplateVersions(ctx context.Context, tenantID, templateID string) ([]*ActionTemplateVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+templateVersionCols+` FROM itsm_action_template_versions
		 WHERE tenant_id = $1 AND template_id = $2 ORDER BY version DESC`, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ActionTemplateVersion{}
	for rows.Next() {
		v, err := scanTemplateVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PGStore) GetActionTemplateVersion(ctx context.Context, tenantID, templateID string, version int) (*ActionTemplateVersion, error) {
	v, err := scanTemplateVersion(s.db.QueryRowContext(ctx,
		`SELECT `+templateVersionCols+` FROM itsm_action_template_versions
		 WHERE tenant_id = $1 AND template_id = $2 AND version = $3`, tenantID, templateID, version))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *PGStore) ResolveActionTemplate(ctx context.Context, tenantID, templateID string, version int) (*ActionTemplateVersion, error) {
	t, err := s.GetActionTemplate(ctx, tenantID, templateID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !t.Enabled {
		return nil, fmt.Errorf("动作模板「%s」已停用", t.Name)
	}
	if t.Status != StatusPublished || t.Version < 1 {
		return nil, fmt.Errorf("动作模板「%s」尚未发布", t.Name)
	}
	if version == 0 {
		version = t.Version
	}
	v, err := s.GetActionTemplateVersion(ctx, tenantID, templateID, version)
	if err != nil || v == nil {
		return nil, fmt.Errorf("动作模板「%s」版本 v%d 不存在", t.Name, version)
	}
	return v, nil
}
