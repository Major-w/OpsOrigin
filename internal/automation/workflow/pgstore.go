package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// PGStore 是 workflow.Store 的 Postgres 实现。
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
		`CREATE TABLE IF NOT EXISTS automation_workflows (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			steps TEXT DEFAULT '[]',
			input_params TEXT DEFAULT '[]',
			labels TEXT DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_workflows_tenant ON automation_workflows(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS automation_workflow_executions (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			status TEXT NOT NULL,
			agent_ids TEXT DEFAULT '[]',
			inputs TEXT DEFAULT '{}',
			step_results TEXT DEFAULT '{}',
			started_at TIMESTAMPTZ,
			finished_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL,
			pending_step_id TEXT,
			context TEXT DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_wf_exec_wid ON automation_workflow_executions(workflow_id, tenant_id)`,
		`CREATE TABLE IF NOT EXISTS automation_workflow_approvals (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			step_id TEXT NOT NULL,
			step_name TEXT,
			approvers TEXT DEFAULT '[]',
			message TEXT,
			status TEXT NOT NULL,
			decided_by TEXT,
			comment TEXT,
			created_at TIMESTAMPTZ NOT NULL,
			decided_at TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_wf_appr_tid ON automation_workflow_approvals(tenant_id)`,
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st); err != nil {
			slog.Warn("automation workflow store init DDL failed", "err", err)
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

func (s *PGStore) Create(ctx context.Context, w *Workflow) error {
	if w.ID == "" {
		w.ID = uuid.New().String()
	}
	if w.TenantID == "" {
		w.TenantID = "default"
	}
	now := time.Now()
	if w.CreatedAt.IsZero() {
		w.CreatedAt = now
	}
	w.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_workflows
			(id, tenant_id, name, description, steps, input_params, labels, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO NOTHING`,
		w.ID, w.TenantID, w.Name, w.Description, toJSON(w.Steps), toJSON(w.InputParams), toJSON(w.Labels), w.CreatedAt, w.UpdatedAt)
	return err
}

func (s *PGStore) Get(ctx context.Context, id, tenantID string) (*Workflow, error) {
	w := &Workflow{}
	var steps, inputParams, labels []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, description, steps, input_params, labels, created_at, updated_at
		FROM automation_workflows WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&w.ID, &w.TenantID, &w.Name, &w.Description, &steps, &inputParams, &labels, &w.CreatedAt, &w.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(steps, &w.Steps)
	_ = json.Unmarshal(inputParams, &w.InputParams)
	_ = json.Unmarshal(labels, &w.Labels)
	return w, nil
}

func (s *PGStore) List(ctx context.Context, tenantID string) ([]*Workflow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, description, steps, input_params, labels, created_at, updated_at
		FROM automation_workflows WHERE tenant_id=$1 ORDER BY updated_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Workflow
	for rows.Next() {
		w := &Workflow{}
		var steps, inputParams, labels []byte
		if err := rows.Scan(&w.ID, &w.TenantID, &w.Name, &w.Description, &steps, &inputParams, &labels, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(steps, &w.Steps)
		_ = json.Unmarshal(inputParams, &w.InputParams)
		_ = json.Unmarshal(labels, &w.Labels)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *PGStore) Update(ctx context.Context, w *Workflow) error {
	w.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE automation_workflows
		SET name=$2, description=$3, steps=$4, input_params=$5, labels=$6, updated_at=$7
		WHERE id=$1 AND tenant_id=$8`,
		w.ID, w.Name, w.Description, toJSON(w.Steps), toJSON(w.InputParams), toJSON(w.Labels), w.UpdatedAt, w.TenantID)
	return err
}

func (s *PGStore) Delete(ctx context.Context, id, tenantID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 先删审批(引用 execution_id), 再删执行, 最后删工作流定义。
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM automation_workflow_approvals
		WHERE execution_id IN (SELECT id FROM automation_workflow_executions WHERE workflow_id=$1 AND tenant_id=$2)`, id, tenantID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM automation_workflow_executions WHERE workflow_id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM automation_workflows WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PGStore) CreateExecution(ctx context.Context, e *WorkflowExecution) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	if e.TenantID == "" {
		e.TenantID = "default"
	}
	now := time.Now()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_workflow_executions
			(id, workflow_id, tenant_id, status, agent_ids, inputs, step_results, started_at, finished_at, created_at, pending_step_id, context)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO NOTHING`,
		e.ID, e.WorkflowID, e.TenantID, string(e.Status), toJSON(e.AgentIDs), toJSON(e.Inputs), toJSON(e.StepResults),
		nullTime(e.StartedAt), nullTime(e.FinishedAt), e.CreatedAt, e.PendingStepID, toJSON(e.Context))
	return err
}

func (s *PGStore) GetExecution(ctx context.Context, id, tenantID string) (*WorkflowExecution, error) {
	e := &WorkflowExecution{}
	var agentIDs, inputs, stepResults, ctxCol []byte
	var status string
	var startedAt, finishedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, workflow_id, tenant_id, status, agent_ids, inputs, step_results, started_at, finished_at, created_at, pending_step_id, context
		FROM automation_workflow_executions WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&e.ID, &e.WorkflowID, &e.TenantID, &status, &agentIDs, &inputs, &stepResults, &startedAt, &finishedAt, &e.CreatedAt, &e.PendingStepID, &ctxCol)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.Status = ExecutionStatus(status)
	_ = json.Unmarshal(agentIDs, &e.AgentIDs)
	_ = json.Unmarshal(inputs, &e.Inputs)
	_ = json.Unmarshal(stepResults, &e.StepResults)
	if startedAt.Valid {
		e.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		e.FinishedAt = &finishedAt.Time
	}
	_ = json.Unmarshal(ctxCol, &e.Context)
	return e, nil
}

func (s *PGStore) ListExecutions(ctx context.Context, workflowID, tenantID string) ([]*WorkflowExecution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workflow_id, tenant_id, status, agent_ids, inputs, step_results, started_at, finished_at, created_at, pending_step_id, context
		FROM automation_workflow_executions WHERE workflow_id=$1 AND tenant_id=$2 ORDER BY created_at DESC`, workflowID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkflowExecution
	for rows.Next() {
		e := &WorkflowExecution{}
		var agentIDs, inputs, stepResults, ctxCol []byte
		var status string
		var startedAt, finishedAt sql.NullTime
		if err := rows.Scan(&e.ID, &e.WorkflowID, &e.TenantID, &status, &agentIDs, &inputs, &stepResults, &startedAt, &finishedAt, &e.CreatedAt, &e.PendingStepID, &ctxCol); err != nil {
			return nil, err
		}
		e.Status = ExecutionStatus(status)
		_ = json.Unmarshal(agentIDs, &e.AgentIDs)
		_ = json.Unmarshal(inputs, &e.Inputs)
		_ = json.Unmarshal(stepResults, &e.StepResults)
		if startedAt.Valid {
			e.StartedAt = &startedAt.Time
		}
		if finishedAt.Valid {
			e.FinishedAt = &finishedAt.Time
		}
		_ = json.Unmarshal(ctxCol, &e.Context)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateExecution(ctx context.Context, e *WorkflowExecution) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE automation_workflow_executions
		SET status=$2, agent_ids=$3, inputs=$4, step_results=$5, started_at=$6, finished_at=$7, pending_step_id=$8, context=$9
		WHERE id=$1 AND tenant_id=$10`,
		e.ID, string(e.Status), toJSON(e.AgentIDs), toJSON(e.Inputs), toJSON(e.StepResults),
		nullTime(e.StartedAt), nullTime(e.FinishedAt), e.PendingStepID, toJSON(e.Context), e.TenantID)
	return err
}

func (s *PGStore) CreateApproval(ctx context.Context, a *StepApproval) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	if a.TenantID == "" {
		a.TenantID = "default"
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_workflow_approvals
			(id, tenant_id, execution_id, step_id, step_name, approvers, message, status, decided_by, comment, created_at, decided_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO NOTHING`,
		a.ID, a.TenantID, a.ExecutionID, a.StepID, a.StepName, toJSON(a.Approvers), a.Message, string(a.Status), a.DecidedBy, a.Comment, a.CreatedAt, nullTime(a.DecidedAt))
	return err
}

func (s *PGStore) ListApprovals(ctx context.Context, tenantID string) ([]*StepApproval, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, execution_id, step_id, step_name, approvers, message, status, decided_by, comment, created_at, decided_at
		FROM automation_workflow_approvals WHERE tenant_id=$1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*StepApproval
	for rows.Next() {
		a := &StepApproval{}
		var approvers []byte
		var status string
		var decidedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.TenantID, &a.ExecutionID, &a.StepID, &a.StepName, &approvers, &a.Message, &status, &a.DecidedBy, &a.Comment, &a.CreatedAt, &decidedAt); err != nil {
			return nil, err
		}
		a.Status = ApprovalStatus(status)
		_ = json.Unmarshal(approvers, &a.Approvers)
		if decidedAt.Valid {
			a.DecidedAt = &decidedAt.Time
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) GetApproval(ctx context.Context, id, tenantID string) (*StepApproval, error) {
	a := &StepApproval{}
	var approvers []byte
	var status string
	var decidedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, execution_id, step_id, step_name, approvers, message, status, decided_by, comment, created_at, decided_at
		FROM automation_workflow_approvals WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.ExecutionID, &a.StepID, &a.StepName, &approvers, &a.Message, &status, &a.DecidedBy, &a.Comment, &a.CreatedAt, &decidedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Status = ApprovalStatus(status)
	_ = json.Unmarshal(approvers, &a.Approvers)
	if decidedAt.Valid {
		a.DecidedAt = &decidedAt.Time
	}
	return a, nil
}

func (s *PGStore) UpdateApproval(ctx context.Context, a *StepApproval) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE automation_workflow_approvals
		SET execution_id=$2, step_id=$3, step_name=$4, approvers=$5, message=$6, status=$7, decided_by=$8, comment=$9, decided_at=$10
		WHERE id=$1 AND tenant_id=$11`,
		a.ID, a.ExecutionID, a.StepID, a.StepName, toJSON(a.Approvers), a.Message, string(a.Status), a.DecidedBy, a.Comment, nullTime(a.DecidedAt), a.TenantID)
	return err
}
