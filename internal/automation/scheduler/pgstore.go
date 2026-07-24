package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// PGStore 是 scheduler.Store 的 Postgres 实现。
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
		`CREATE TABLE IF NOT EXISTS automation_scheduled_tasks (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			cron_expr TEXT NOT NULL,
			task_type TEXT NOT NULL,
			target_id TEXT NOT NULL,
			agent_ids TEXT DEFAULT '[]',
			inputs TEXT DEFAULT '{}',
			enabled BOOLEAN DEFAULT FALSE,
			labels TEXT DEFAULT '{}',
			max_retries INTEGER DEFAULT 0,
			retry_delay_secs INTEGER DEFAULT 60,
			retry_count INTEGER DEFAULT 0,
			last_error TEXT DEFAULT '',
			alert_on_failure BOOLEAN DEFAULT FALSE,
			last_run_at TIMESTAMPTZ,
			next_run_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_sched_tasks_tenant ON automation_scheduled_tasks(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_sched_tasks_enabled ON automation_scheduled_tasks(enabled)`,
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st); err != nil {
			slog.Warn("automation scheduler store init DDL failed", "err", err)
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

func (s *PGStore) Create(ctx context.Context, t *ScheduledTask) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.TenantID == "" {
		t.TenantID = "default"
	}
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_scheduled_tasks
			(id, tenant_id, name, cron_expr, task_type, target_id, agent_ids, inputs, enabled, labels, max_retries, retry_delay_secs, retry_count, last_error, alert_on_failure, last_run_at, next_run_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (id) DO NOTHING`,
		t.ID, t.TenantID, t.Name, t.CronExpr, string(t.TaskType), t.TargetID, toJSON(t.AgentIDs), toJSON(t.Inputs), t.Enabled, toJSON(t.Labels),
		t.MaxRetries, t.RetryDelaySecs, t.RetryCount, t.LastError, t.AlertOnFailure,
		nullTime(t.LastRunAt), nullTime(t.NextRunAt), t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *PGStore) Get(ctx context.Context, id, tenantID string) (*ScheduledTask, error) {
	t := &ScheduledTask{}
	var agentIDs, inputs, labels []byte
	var taskType string
	var lastRunAt, nextRunAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, cron_expr, task_type, target_id, agent_ids, inputs, enabled, labels, max_retries, retry_delay_secs, retry_count, last_error, alert_on_failure, last_run_at, next_run_at, created_at, updated_at
		FROM automation_scheduled_tasks WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&t.ID, &t.TenantID, &t.Name, &t.CronExpr, &taskType, &t.TargetID, &agentIDs, &inputs, &t.Enabled, &labels,
			&t.MaxRetries, &t.RetryDelaySecs, &t.RetryCount, &t.LastError, &t.AlertOnFailure,
			&lastRunAt, &nextRunAt, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.TaskType = TaskType(taskType)
	_ = json.Unmarshal(agentIDs, &t.AgentIDs)
	_ = json.Unmarshal(inputs, &t.Inputs)
	_ = json.Unmarshal(labels, &t.Labels)
	if lastRunAt.Valid {
		t.LastRunAt = &lastRunAt.Time
	}
	if nextRunAt.Valid {
		t.NextRunAt = &nextRunAt.Time
	}
	return t, nil
}

// List 当 tenantID=="" 时返回全部(供 scheduler.Start 跨租户扫描定时任务)。
func (s *PGStore) List(ctx context.Context, tenantID string) ([]*ScheduledTask, error) {
	query := `SELECT id, tenant_id, name, cron_expr, task_type, target_id, agent_ids, inputs, enabled, labels, max_retries, retry_delay_secs, retry_count, last_error, alert_on_failure, last_run_at, next_run_at, created_at, updated_at
		FROM automation_scheduled_tasks`
	var args []any
	if tenantID != "" {
		query += ` WHERE tenant_id=$1`
		args = append(args, tenantID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ScheduledTask
	for rows.Next() {
		t := &ScheduledTask{}
		var agentIDs, inputs, labels []byte
		var taskType string
		var lastRunAt, nextRunAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.CronExpr, &taskType, &t.TargetID, &agentIDs, &inputs, &t.Enabled, &labels,
			&t.MaxRetries, &t.RetryDelaySecs, &t.RetryCount, &t.LastError, &t.AlertOnFailure,
			&lastRunAt, &nextRunAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.TaskType = TaskType(taskType)
		_ = json.Unmarshal(agentIDs, &t.AgentIDs)
		_ = json.Unmarshal(inputs, &t.Inputs)
		_ = json.Unmarshal(labels, &t.Labels)
		if lastRunAt.Valid {
			t.LastRunAt = &lastRunAt.Time
		}
		if nextRunAt.Valid {
			t.NextRunAt = &nextRunAt.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) Update(ctx context.Context, t *ScheduledTask) error {
	t.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE automation_scheduled_tasks
		SET name=$2, cron_expr=$3, task_type=$4, target_id=$5, agent_ids=$6, inputs=$7, enabled=$8, labels=$9,
		    max_retries=$10, retry_delay_secs=$11, retry_count=$12, last_error=$13, alert_on_failure=$14,
		    last_run_at=$15, next_run_at=$16, updated_at=$17
		WHERE id=$1 AND tenant_id=$18`,
		t.ID, t.Name, t.CronExpr, string(t.TaskType), t.TargetID, toJSON(t.AgentIDs), toJSON(t.Inputs), t.Enabled, toJSON(t.Labels),
		t.MaxRetries, t.RetryDelaySecs, t.RetryCount, t.LastError, t.AlertOnFailure,
		nullTime(t.LastRunAt), nullTime(t.NextRunAt), t.UpdatedAt, t.TenantID)
	return err
}

func (s *PGStore) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM automation_scheduled_tasks WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}
