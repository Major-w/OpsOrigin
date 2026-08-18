package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/model"
)

type PGStore struct {
	db *sql.DB
}

func NewPGStore(db *sql.DB) *PGStore {
	return &PGStore{db: db}
}

func (s *PGStore) Init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS itsm_tickets (
			id              TEXT PRIMARY KEY,
			tenant_id       TEXT NOT NULL,
			title           TEXT NOT NULL,
			description     TEXT,
			risk_level      TEXT NOT NULL,
			status          TEXT NOT NULL,
			change_plan     TEXT,
			rollback_plan   TEXT,
			created_by      TEXT,
			created_by_type TEXT,
			approved_by     TEXT,
			reject_reason   TEXT,
			completed_by    TEXT,
			rolled_back_by  TEXT,
			rollback_reason TEXT,
			ci_ids          TEXT DEFAULT '[]',
			webhook_url     TEXT,
			labels          TEXT DEFAULT '{}',
			trace_id        TEXT,
			scheduled_at    TIMESTAMPTZ,
			deadline_at     TIMESTAMPTZ,
			created_at      TIMESTAMPTZ NOT NULL,
			updated_at      TIMESTAMPTZ NOT NULL,
			approved_at     TIMESTAMPTZ,
			executed_at     TIMESTAMPTZ,
			completed_at    TIMESTAMPTZ,
			rolled_back_at  TIMESTAMPTZ,
			sla_warning_notified BOOLEAN DEFAULT FALSE,
			sla_breached        BOOLEAN DEFAULT FALSE,
			sla_breached_at     TIMESTAMPTZ,
			sla_escalation_level INTEGER DEFAULT 0,
			custom_fields       TEXT DEFAULT '{}'
		)`)
	if err != nil {
		return fmt.Errorf("init itsm_tickets table: %w", err)
	}
	s.migrate(ctx)
	s.initDictTable(ctx)
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_itsm_tickets_tenant ON itsm_tickets(tenant_id, status)`)
	if err != nil {
		return fmt.Errorf("init itsm_tickets index: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS itsm_comments (
			id         BIGSERIAL PRIMARY KEY,
			ticket_id  TEXT NOT NULL,
			tenant_id  TEXT NOT NULL,
			author     TEXT NOT NULL,
			content    TEXT NOT NULL,
			type       TEXT DEFAULT 'normal',
			mentions   JSONB DEFAULT '[]'::jsonb,
			edited     BOOLEAN DEFAULT FALSE,
			edited_at  TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("init itsm_comments table: %w", err)
	}
	// 向后兼容: 旧表可能缺少新列
	for _, col := range []string{
		`ALTER TABLE itsm_comments ADD COLUMN IF NOT EXISTS type TEXT DEFAULT 'normal'`,
		`ALTER TABLE itsm_comments ADD COLUMN IF NOT EXISTS mentions JSONB DEFAULT '[]'::jsonb`,
		`ALTER TABLE itsm_comments ADD COLUMN IF NOT EXISTS edited BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE itsm_comments ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ`,
	} {
		s.db.ExecContext(ctx, col)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_itsm_comments_ticket ON itsm_comments(ticket_id, tenant_id)`)
	if err != nil {
		return fmt.Errorf("init itsm_comments index: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS itsm_subtasks (
			id          BIGSERIAL PRIMARY KEY,
			ticket_id   TEXT NOT NULL,
			tenant_id   TEXT NOT NULL,
			title       TEXT NOT NULL,
			description TEXT,
			assignee    TEXT,
			status      TEXT NOT NULL DEFAULT 'pending',
			due_at      TIMESTAMPTZ,
			created_by  TEXT,
			created_at  TIMESTAMPTZ NOT NULL,
			updated_at  TIMESTAMPTZ NOT NULL,
			completed_at TIMESTAMPTZ,
			meta        TEXT DEFAULT '{}',
			detail      TEXT,
			viewed_at   TIMESTAMPTZ,
			started_at  TIMESTAMPTZ,
			updated_by  TEXT,
			close_with_parent BOOLEAN NOT NULL DEFAULT TRUE
		)`)
	// 自愈: 老库补列(派发式子任务增强)。
	s.db.Exec(`ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS meta TEXT DEFAULT '{}'`)
	s.db.Exec(`ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS detail TEXT`)
	s.db.Exec(`ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS viewed_at TIMESTAMPTZ`)
	s.db.Exec(`ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ`)
	s.db.Exec(`ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS updated_by TEXT`)
	s.db.Exec(`ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS close_with_parent BOOLEAN NOT NULL DEFAULT TRUE`)
	if err != nil {
		return fmt.Errorf("init itsm_subtasks table: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_itsm_subtasks_ticket ON itsm_subtasks(ticket_id, tenant_id)`)
	if err != nil {
		return fmt.Errorf("init itsm_subtasks index: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS itsm_attachments (
			id          BIGSERIAL PRIMARY KEY,
			ticket_id   TEXT NOT NULL,
			tenant_id   TEXT NOT NULL,
			file_name   TEXT NOT NULL,
			file_size   BIGINT NOT NULL,
			file_type   TEXT NOT NULL,
			uploaded_by TEXT NOT NULL,
			data        BYTEA,
			object_key  TEXT,
			created_at  TIMESTAMPTZ NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("init itsm_attachments table: %w", err)
	}
	// 存量库补列(幂等): 对象存储迁移后二进制走 object_key, data 保留历史数据。
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE itsm_attachments ADD COLUMN IF NOT EXISTS object_key TEXT`); err != nil {
		return fmt.Errorf("migrate itsm_attachments.object_key: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_itsm_attachments_ticket ON itsm_attachments(ticket_id, tenant_id)`)
	if err != nil {
		return fmt.Errorf("init itsm_attachments index: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS itsm_changes (
			id            TEXT PRIMARY KEY,
			tenant_id     TEXT NOT NULL,
			ticket_id     TEXT,
			title         TEXT NOT NULL,
			description   TEXT,
			risk_level    TEXT NOT NULL,
			status        TEXT NOT NULL DEFAULT 'pending',
			change_type   TEXT,
			impact        TEXT,
			urgency       TEXT,
			cab_members   TEXT DEFAULT '[]',
			approvals     TEXT DEFAULT '[]',
			plan          TEXT,
			rollback_plan TEXT,
			created_by    TEXT,
			created_at    TIMESTAMPTZ NOT NULL,
			updated_at    TIMESTAMPTZ NOT NULL,
			scheduled_at  TIMESTAMPTZ,
			window_start  TIMESTAMPTZ,
			window_end    TIMESTAMPTZ
		)`)
	s.db.Exec(`ALTER TABLE itsm_changes ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ`)
	s.db.Exec(`ALTER TABLE itsm_changes ADD COLUMN IF NOT EXISTS window_start TIMESTAMPTZ`)
	s.db.Exec(`ALTER TABLE itsm_changes ADD COLUMN IF NOT EXISTS window_end TIMESTAMPTZ`)
	if err != nil {
		return fmt.Errorf("init itsm_changes table: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_itsm_changes_tenant ON itsm_changes(tenant_id)`)
	if err != nil {
		return fmt.Errorf("init itsm_changes index: %w", err)
	}

	// 可配置表单 schema 表（动态表单定义）
	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS itsm_form_schemas (
			tenant_id TEXT NOT NULL,
			scope     TEXT NOT NULL,
			version   INTEGER NOT NULL DEFAULT 1,
			fields    JSONB NOT NULL DEFAULT '[]',
			updated_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (tenant_id, scope)
		)`)
	if err != nil {
		return fmt.Errorf("init itsm_form_schemas table: %w", err)
	}

	return nil
}

func (s *PGStore) migrate(ctx context.Context) {
	migrations := []string{
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS rollback_plan TEXT`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS completed_by TEXT`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS rolled_back_by TEXT`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS rollback_reason TEXT`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS ci_ids TEXT DEFAULT '[]'`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS deadline_at TIMESTAMPTZ`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS rolled_back_at TIMESTAMPTZ`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS sla_warning_notified BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS sla_breached BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS sla_breached_at TIMESTAMPTZ`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS sla_escalation_level INTEGER DEFAULT 0`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS process_id TEXT`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS custom_fields TEXT DEFAULT '{}'`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS assignee TEXT`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS assigned_at TIMESTAMPTZ`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS satisfaction INTEGER DEFAULT 0`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS on_hold BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS hold_reason TEXT`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS hold_until_at TIMESTAMPTZ`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS related_ticket_ids TEXT DEFAULT '[]'`,
		`ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS catalog_id TEXT DEFAULT ''`,
	}
	for _, m := range migrations {
		s.db.ExecContext(ctx, m)
	}
}

func (s *PGStore) Create(ctx context.Context, ticket *model.Ticket) error {
	ticket.ID = uuid.New().String()
	now := time.Now()
	ticket.CreatedAt = now
	ticket.UpdatedAt = now

	labelsJSON, _ := json.Marshal(ticket.Labels)
	ciIDsJSON, _ := json.Marshal(ticket.CIIDs)
	customFieldsJSON, _ := json.Marshal(ticket.CustomFields)
	relatedJSON, _ := json.Marshal(ticket.RelatedTicketIDs)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO itsm_tickets
			(id, tenant_id, title, description, risk_level, status, change_plan,
			 rollback_plan, created_by, created_by_type, approved_by, reject_reason,
			 completed_by, rolled_back_by, rollback_reason, ci_ids, webhook_url,
			 labels, trace_id, scheduled_at, deadline_at, created_at, updated_at,
			 approved_at, executed_at, completed_at, rolled_back_at, process_id, custom_fields,
			 assignee, assigned_at, satisfaction,
			 on_hold, hold_reason, hold_until_at, related_ticket_ids, catalog_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37)`,
		ticket.ID, ticket.TenantID, ticket.Title, nullable(ticket.Description),
		string(ticket.RiskLevel), string(ticket.Status), nullable(ticket.ChangePlan),
		nullable(ticket.RollbackPlan), ticket.CreatedBy, ticket.CreatedByType,
		nullable(ticket.ApprovedBy), nullable(ticket.RejectReason),
		nullable(ticket.CompletedBy), nullable(ticket.RolledBackBy),
		nullable(ticket.RollbackReason), string(ciIDsJSON),
		nullable(ticket.WebhookURL), string(labelsJSON), ticket.TraceID,
		nullableTime(ticket.ScheduledAt), nullableTime(ticket.DeadlineAt),
		ticket.CreatedAt, ticket.UpdatedAt,
		nullableTime(ticket.ApprovedAt), nullableTime(ticket.ExecutedAt),
		nullableTime(ticket.CompletedAt), nullableTime(ticket.RolledBackAt),
		nullable(ticket.ProcessID), string(customFieldsJSON),
		nullable(ticket.Assignee), nullableTime(ticket.AssignedAt), ticket.Satisfaction,
		ticket.OnHold, nullable(ticket.HoldReason), nullableTime(ticket.HoldUntilAt), string(relatedJSON), nullable(ticket.CatalogID))
	if err != nil {
		return fmt.Errorf("insert ticket: %w", err)
	}
	return nil
}

func (s *PGStore) Get(ctx context.Context, id, tenantID string) (*model.Ticket, error) {
	t := &model.Ticket{}
	var desc, changePlan, rollbackPlan, approvedBy, rejectReason, webhookURL sql.NullString
	var completedBy, rolledBackBy, rollbackReason sql.NullString
	var labelsJSON, ciIDsJSON sql.NullString
	var customFieldsJSON sql.NullString
	var processID sql.NullString
	var catalogID sql.NullString
	var assignee sql.NullString
	var assignedAt sql.NullTime
	var satisfaction sql.NullInt32
	var approvedAt, executedAt, completedAt, rolledBackAt sql.NullTime
	var scheduledAt, deadlineAt sql.NullTime
	var holdReason sql.NullString
	var holdUntilAt sql.NullTime
	var relatedJSON sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, title, description, risk_level, status, change_plan,
		       rollback_plan, created_by, created_by_type, approved_by, reject_reason,
		       completed_by, rolled_back_by, rollback_reason, ci_ids, webhook_url,
		       labels, trace_id, scheduled_at, deadline_at, created_at, updated_at,
		       approved_at, executed_at, completed_at, rolled_back_at, process_id, custom_fields,
		       assignee, assigned_at, satisfaction,
		       on_hold, hold_reason, hold_until_at, related_ticket_ids, catalog_id
		FROM itsm_tickets WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(
		&t.ID, &t.TenantID, &t.Title, &desc, &t.RiskLevel, &t.Status,
		&changePlan, &rollbackPlan, &t.CreatedBy, &t.CreatedByType,
		&approvedBy, &rejectReason, &completedBy, &rolledBackBy, &rollbackReason,
		&ciIDsJSON, &webhookURL, &labelsJSON, &t.TraceID,
		&scheduledAt, &deadlineAt, &t.CreatedAt, &t.UpdatedAt,
		&approvedAt, &executedAt, &completedAt, &rolledBackAt, &processID, &customFieldsJSON,
		&assignee, &assignedAt, &satisfaction,
		&t.OnHold, &holdReason, &holdUntilAt, &relatedJSON, &catalogID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query ticket: %w", err)
	}

	t.ProcessID = nullableStr(processID)
	t.CatalogID = nullableStr(catalogID)
	t.Description = nullableStr(desc)
	t.ChangePlan = nullableStr(changePlan)
	t.RollbackPlan = nullableStr(rollbackPlan)
	t.HoldReason = nullableStr(holdReason)
	setTimePtr(&t.HoldUntilAt, holdUntilAt)
	if len(relatedJSON.String) > 0 && relatedJSON.String != "[]" {
		json.Unmarshal([]byte(relatedJSON.String), &t.RelatedTicketIDs)
	}
	t.ApprovedBy = nullableStr(approvedBy)
	t.RejectReason = nullableStr(rejectReason)
	t.CompletedBy = nullableStr(completedBy)
	t.RolledBackBy = nullableStr(rolledBackBy)
	t.RollbackReason = nullableStr(rollbackReason)
	t.WebhookURL = nullableStr(webhookURL)
	t.Assignee = nullableStr(assignee)
	setTimePtr(&t.AssignedAt, assignedAt)
	if satisfaction.Valid {
		t.Satisfaction = int(satisfaction.Int32)
	}
	setTimePtr(&t.ApprovedAt, approvedAt)
	setTimePtr(&t.ExecutedAt, executedAt)
	setTimePtr(&t.CompletedAt, completedAt)
	setTimePtr(&t.RolledBackAt, rolledBackAt)
	setTimePtr(&t.ScheduledAt, scheduledAt)
	setTimePtr(&t.DeadlineAt, deadlineAt)
	if labelsJSON.Valid {
		json.Unmarshal([]byte(labelsJSON.String), &t.Labels)
	}
	if ciIDsJSON.Valid && ciIDsJSON.String != "" {
		json.Unmarshal([]byte(ciIDsJSON.String), &t.CIIDs)
	}
	if customFieldsJSON.Valid && customFieldsJSON.String != "" && customFieldsJSON.String != "{}" {
		json.Unmarshal([]byte(customFieldsJSON.String), &t.CustomFields)
	}
	return t, nil
}

func setTimePtr(dst **time.Time, src sql.NullTime) {
	if src.Valid {
		*dst = &src.Time
	}
}

func (s *PGStore) Update(ctx context.Context, ticket *model.Ticket) error {
	ticket.UpdatedAt = time.Now()

	labelsJSON, _ := json.Marshal(ticket.Labels)
	ciIDsJSON, _ := json.Marshal(ticket.CIIDs)
	customFieldsJSON, _ := json.Marshal(ticket.CustomFields)
	relatedJSON, _ := json.Marshal(ticket.RelatedTicketIDs)

	result, err := s.db.ExecContext(ctx, `
		UPDATE itsm_tickets SET
			title=$1, description=$2, risk_level=$3, status=$4, change_plan=$5,
			rollback_plan=$6, created_by=$7, created_by_type=$8, approved_by=$9,
			reject_reason=$10, completed_by=$11, rolled_back_by=$12,
			rollback_reason=$13, ci_ids=$14, webhook_url=$15, labels=$16,
			trace_id=$17, updated_at=$18, approved_at=$19, executed_at=$20,
			completed_at=$21, rolled_back_at=$22, scheduled_at=$23, deadline_at=$24,
			process_id=$25, custom_fields=$26, assignee=$27, assigned_at=$28, satisfaction=$29,
			on_hold=$30, hold_reason=$31, hold_until_at=$32, related_ticket_ids=$33
		WHERE id=$34 AND tenant_id=$35`,
		ticket.Title, nullable(ticket.Description), string(ticket.RiskLevel),
		string(ticket.Status), nullable(ticket.ChangePlan),
		nullable(ticket.RollbackPlan), ticket.CreatedBy, ticket.CreatedByType,
		nullable(ticket.ApprovedBy), nullable(ticket.RejectReason),
		nullable(ticket.CompletedBy), nullable(ticket.RolledBackBy),
		nullable(ticket.RollbackReason), string(ciIDsJSON),
		nullable(ticket.WebhookURL), string(labelsJSON), ticket.TraceID,
		ticket.UpdatedAt, nullableTime(ticket.ApprovedAt), nullableTime(ticket.ExecutedAt),
		nullableTime(ticket.CompletedAt), nullableTime(ticket.RolledBackAt),
		nullableTime(ticket.ScheduledAt), nullableTime(ticket.DeadlineAt),
		nullable(ticket.ProcessID), string(customFieldsJSON),
		nullable(ticket.Assignee), nullableTime(ticket.AssignedAt), ticket.Satisfaction,
		ticket.OnHold, nullable(ticket.HoldReason), nullableTime(ticket.HoldUntilAt), string(relatedJSON),
		ticket.ID, ticket.TenantID)
	if err != nil {
		return fmt.Errorf("update ticket: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) List(ctx context.Context, tenantID string, status model.TicketStatus) ([]*model.Ticket, error) {
	query := `SELECT id, tenant_id, title, description, risk_level, status, change_plan,
	                  rollback_plan, created_by, created_by_type, approved_by, reject_reason,
	                  completed_by, rolled_back_by, rollback_reason, ci_ids, webhook_url,
	                  labels, trace_id, scheduled_at, deadline_at, created_at, updated_at,
	                  approved_at, executed_at, completed_at, rolled_back_at, process_id, custom_fields,
	                  assignee, assigned_at, satisfaction,
	                  on_hold, hold_reason, hold_until_at, related_ticket_ids
	           FROM itsm_tickets WHERE tenant_id = $1`
	args := []any{tenantID}

	if status != "" {
		query += ` AND status = $2`
		args = append(args, string(status))
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()

	var tickets []*model.Ticket
	for rows.Next() {
		t := &model.Ticket{}
		var desc, changePlan, rollbackPlan, approvedBy, rejectReason, webhookURL sql.NullString
		var completedBy, rolledBackBy, rollbackReason sql.NullString
		var labelsJSON, ciIDsJSON sql.NullString
		var customFieldsJSON sql.NullString
		var processID sql.NullString
		var assignee sql.NullString
		var assignedAt sql.NullTime
		var satisfaction sql.NullInt32
		var approvedAt, executedAt, completedAt, rolledBackAt sql.NullTime
		var scheduledAt, deadlineAt sql.NullTime
		var holdReason sql.NullString
		var holdUntilAt sql.NullTime
		var relatedJSON sql.NullString

		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.Title, &desc, &t.RiskLevel, &t.Status,
			&changePlan, &rollbackPlan, &t.CreatedBy, &t.CreatedByType,
			&approvedBy, &rejectReason, &completedBy, &rolledBackBy, &rollbackReason,
			&ciIDsJSON, &webhookURL, &labelsJSON, &t.TraceID,
			&scheduledAt, &deadlineAt, &t.CreatedAt, &t.UpdatedAt,
			&approvedAt, &executedAt, &completedAt, &rolledBackAt, &processID, &customFieldsJSON,
			&assignee, &assignedAt, &satisfaction,
			&t.OnHold, &holdReason, &holdUntilAt, &relatedJSON); err != nil {
			return nil, fmt.Errorf("scan ticket: %w", err)
		}

		t.ProcessID = nullableStr(processID)
		t.Description = nullableStr(desc)
		t.ChangePlan = nullableStr(changePlan)
		t.RollbackPlan = nullableStr(rollbackPlan)
		t.ApprovedBy = nullableStr(approvedBy)
		t.RejectReason = nullableStr(rejectReason)
		t.CompletedBy = nullableStr(completedBy)
		t.RolledBackBy = nullableStr(rolledBackBy)
		t.RollbackReason = nullableStr(rollbackReason)
		t.WebhookURL = nullableStr(webhookURL)
		t.Assignee = nullableStr(assignee)
		setTimePtr(&t.AssignedAt, assignedAt)
		if satisfaction.Valid {
			t.Satisfaction = int(satisfaction.Int32)
		}
		setTimePtr(&t.ApprovedAt, approvedAt)
		setTimePtr(&t.ExecutedAt, executedAt)
		setTimePtr(&t.CompletedAt, completedAt)
		setTimePtr(&t.RolledBackAt, rolledBackAt)
		setTimePtr(&t.ScheduledAt, scheduledAt)
		setTimePtr(&t.DeadlineAt, deadlineAt)
		if labelsJSON.Valid {
			json.Unmarshal([]byte(labelsJSON.String), &t.Labels)
		}
		if ciIDsJSON.Valid && ciIDsJSON.String != "" {
			json.Unmarshal([]byte(ciIDsJSON.String), &t.CIIDs)
		}
		if customFieldsJSON.Valid && customFieldsJSON.String != "" && customFieldsJSON.String != "{}" {
			json.Unmarshal([]byte(customFieldsJSON.String), &t.CustomFields)
		}
		t.HoldReason = nullableStr(holdReason)
		setTimePtr(&t.HoldUntilAt, holdUntilAt)
		if len(relatedJSON.String) > 0 && relatedJSON.String != "[]" {
			json.Unmarshal([]byte(relatedJSON.String), &t.RelatedTicketIDs)
		}
		tickets = append(tickets, t)
	}
	return tickets, rows.Err()
}

func (s *PGStore) AddEvent(ctx context.Context, event *model.TicketEvent) error {
	event.CreatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO itsm_ticket_events (ticket_id, from_status, to_status, changed_by, comment, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		event.TicketID, nullableStrPtr(event.FromStatus),
		string(event.ToStatus), event.ChangedBy,
		nullable(event.Comment), event.CreatedAt)
	return err
}

func nullableStrPtr(s model.TicketStatus) *string {
	if s == "" {
		return nil
	}
	str := string(s)
	return &str
}

func (s *PGStore) GetEvents(ctx context.Context, ticketID, tenantID string) ([]*model.TicketEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.ticket_id, e.from_status, e.to_status, e.changed_by, e.comment, e.created_at
		FROM itsm_ticket_events e
		JOIN itsm_tickets t ON e.ticket_id = t.id
		WHERE e.ticket_id = $1 AND t.tenant_id = $2
		ORDER BY e.created_at ASC`, ticketID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query ticket events: %w", err)
	}
	defer rows.Close()

	var events []*model.TicketEvent
	for rows.Next() {
		e := &model.TicketEvent{}
		var fromStatus sql.NullString
		var comment sql.NullString
		if err := rows.Scan(&e.ID, &e.TicketID, &fromStatus, &e.ToStatus,
			&e.ChangedBy, &comment, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if fromStatus.Valid {
			e.FromStatus = model.TicketStatus(fromStatus.String)
		}
		if comment.Valid {
			e.Comment = comment.String
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *PGStore) GetStats(ctx context.Context, tenantID string) (*model.ITSMStats, error) {
	stats := &model.ITSMStats{
		ByStatus:  make(map[string]int),
		ByRisk:    make(map[string]int),
		ByCreator: make(map[string]int),
		ByChannel: make(map[string]int),
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT status, risk_level, created_by, created_by_type, approved_at, created_at, executed_at, completed_at
		FROM itsm_tickets WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	var approvedCount int
	var totalApprovalMs int64
	var approvedWithAt int
	var totalExecutionMs int64
	var executedWithAt int
	type dayAgg struct {
		created int
		closed  int
	}
	days := map[string]*dayAgg{}
	var dayOrder []string

	for rows.Next() {
		var status, risk, createdBy, createdByType string
		var approvedAt, createdAt, executedAt, completedAt sql.NullTime
		if err := rows.Scan(&status, &risk, &createdBy, &createdByType, &approvedAt, &createdAt, &executedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan stats row: %w", err)
		}
		stats.Total++
		stats.ByStatus[status]++
		stats.ByRisk[risk]++
		if createdBy != "" {
			stats.ByCreator[createdBy]++
		}
		ch := createdByType
		if ch == "" {
			ch = "manual"
		}
		stats.ByChannel[ch]++
		if status == "approved" {
			approvedCount++
		}
		// 趋势: 创建按 created_at, 关闭按 completed_at(仅终态且有关闭时间)
		if createdAt.Valid {
			day := createdAt.Time.Format("2006-01-02")
			if _, ok := days[day]; !ok {
				days[day] = &dayAgg{}
				dayOrder = append(dayOrder, day)
			}
			days[day].created++
		}
		if completedAt.Valid {
			cd := completedAt.Time.Format("2006-01-02")
			if _, ok := days[cd]; !ok {
				days[cd] = &dayAgg{}
				dayOrder = append(dayOrder, cd)
			}
			days[cd].closed++
		}
		if approvedAt.Valid && createdAt.Valid && createdAt.Time.Before(approvedAt.Time) {
			totalApprovalMs += approvedAt.Time.Sub(createdAt.Time).Milliseconds()
			approvedWithAt++
		}
		if executedAt.Valid && approvedAt.Valid && executedAt.Time.After(approvedAt.Time) {
			totalExecutionMs += executedAt.Time.Sub(approvedAt.Time).Milliseconds()
			executedWithAt++
		}
	}

	if stats.Total > 0 {
		stats.ApprovalRate = float64(approvedCount) / float64(stats.Total)
	}
	if approvedWithAt > 0 {
		stats.AvgApprovalMs = totalApprovalMs / int64(approvedWithAt)
	}
	if executedWithAt > 0 {
		stats.AvgExecutionMs = totalExecutionMs / int64(executedWithAt)
	}
	// 趋势排序(按日期升序, 最多取最近 30 天)
	stats.Trends = make([]model.TrendPoint, 0, len(dayOrder))
	for _, d := range dayOrder {
		stats.Trends = append(stats.Trends, model.TrendPoint{Date: d, Created: days[d].created, Closed: days[d].closed})
	}
	if len(stats.Trends) > 30 {
		stats.Trends = stats.Trends[len(stats.Trends)-30:]
	}
	return stats, rows.Err()
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func nullableTime(t *time.Time) *time.Time {
	return t
}

func (s *PGStore) AddComment(ctx context.Context, comment *model.Comment) error {
	comment.CreatedAt = time.Now()
	if comment.Type == "" {
		comment.Type = "normal"
	}
	mentionsJSON, _ := json.Marshal(comment.Mentions)
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO itsm_comments (ticket_id, tenant_id, author, content, type, mentions, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		comment.TicketID, comment.TenantID, comment.Author, comment.Content, comment.Type, mentionsJSON, comment.CreatedAt).Scan(&comment.ID)
	return err
}

func (s *PGStore) ListComments(ctx context.Context, ticketID, tenantID string) ([]*model.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ticket_id, tenant_id, author, content, type, mentions, edited, edited_at, created_at
		FROM itsm_comments WHERE ticket_id = $1 AND tenant_id = $2
		ORDER BY created_at ASC`, ticketID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var comments []*model.Comment
	for rows.Next() {
		c := &model.Comment{}
		var mentionsJSON []byte
		if err := rows.Scan(&c.ID, &c.TicketID, &c.TenantID, &c.Author, &c.Content, &c.Type, &mentionsJSON, &c.Edited, &c.EditedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		if len(mentionsJSON) > 0 {
			json.Unmarshal(mentionsJSON, &c.Mentions)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

func (s *PGStore) DeleteComment(ctx context.Context, id int64, ticketID, tenantID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM itsm_comments WHERE id = $1 AND ticket_id = $2 AND tenant_id = $3`, id, ticketID, tenantID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) EditComment(ctx context.Context, id int64, ticketID, tenantID, content string) error {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE itsm_comments SET content=$1, edited=TRUE, edited_at=$2
		WHERE id=$3 AND ticket_id=$4 AND tenant_id=$5`, content, now, id, ticketID, tenantID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) CreateSubtask(ctx context.Context, st *model.Subtask) error {
	now := time.Now()
	st.CreatedAt = now
	st.UpdatedAt = now
	if st.Status == "" {
		st.Status = model.SubtaskPending
	}
	if st.Status == model.SubtaskDone {
		st.CompletedAt = &now
	}
	metaJSON, _ := json.Marshal(st.Meta)
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO itsm_subtasks (ticket_id, tenant_id, title, description, assignee, status, due_at, created_by, created_at, updated_at, completed_at, meta,
			detail, viewed_at, started_at, updated_by, close_with_parent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`,
		st.TicketID, st.TenantID, st.Title, nullable(st.Description), nullable(st.Assignee),
		string(st.Status), nullableTime(st.DueAt), st.CreatedBy, st.CreatedAt, st.UpdatedAt, nullableTime(st.CompletedAt), string(metaJSON),
		nullable(st.Detail), nullableTime(st.ViewedAt), nullableTime(st.StartedAt), nullable(st.UpdatedBy), st.CloseWithParent).Scan(&st.ID)
	return err
}

// subtaskColumns 子任务查询列, Get/List 共用以保证 scanSubtask 顺序一致。
const subtaskColumns = `id, ticket_id, tenant_id, title, description, assignee, status, due_at, created_by,
	created_at, updated_at, completed_at, meta, detail, viewed_at, started_at, updated_by, close_with_parent`

// scanSubtask 按 subtaskColumns 顺序读取一行子任务。
func scanSubtask(sc interface{ Scan(...any) error }) (*model.Subtask, error) {
	st := &model.Subtask{}
	var description, assignee, createdBy, detail, updatedBy sql.NullString
	var dueAt, completedAt, viewedAt, startedAt sql.NullTime
	var metaJSON sql.NullString
	var closeWithParent sql.NullBool
	if err := sc.Scan(&st.ID, &st.TicketID, &st.TenantID, &st.Title,
		&description, &assignee, &st.Status, &dueAt, &createdBy,
		&st.CreatedAt, &st.UpdatedAt, &completedAt, &metaJSON,
		&detail, &viewedAt, &startedAt, &updatedBy, &closeWithParent); err != nil {
		return nil, err
	}
	st.Description = nullableStr(description)
	st.Assignee = nullableStr(assignee)
	st.CreatedBy = nullableStr(createdBy)
	st.Detail = nullableStr(detail)
	st.UpdatedBy = nullableStr(updatedBy)
	setTimePtr(&st.DueAt, dueAt)
	setTimePtr(&st.CompletedAt, completedAt)
	setTimePtr(&st.ViewedAt, viewedAt)
	setTimePtr(&st.StartedAt, startedAt)
	// 老数据 NULL 视为 true, 保持既有级联关闭语义。
	st.CloseWithParent = !closeWithParent.Valid || closeWithParent.Bool
	if metaJSON.Valid && metaJSON.String != "" && metaJSON.String != "{}" {
		json.Unmarshal([]byte(metaJSON.String), &st.Meta)
	}
	return st, nil
}

func (s *PGStore) GetSubtask(ctx context.Context, id int64, ticketID, tenantID string) (*model.Subtask, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+subtaskColumns+`
		FROM itsm_subtasks WHERE id = $1 AND ticket_id = $2 AND tenant_id = $3`, id, ticketID, tenantID)
	st, err := scanSubtask(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return st, err
}

func (s *PGStore) ListSubtasks(ctx context.Context, ticketID, tenantID string) ([]*model.Subtask, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+subtaskColumns+`
		FROM itsm_subtasks WHERE ticket_id = $1 AND tenant_id = $2
		ORDER BY created_at ASC`, ticketID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Subtask
	for rows.Next() {
		st, err := scanSubtask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateSubtask(ctx context.Context, st *model.Subtask) error {
	st.UpdatedAt = time.Now()
	if st.Status == model.SubtaskDone && st.CompletedAt == nil {
		now := st.UpdatedAt
		st.CompletedAt = &now
	} else if st.Status != model.SubtaskDone {
		st.CompletedAt = nil
	}
	// 进入处理中且未记开始时间时补齐(手动置 in_progress 或首次查看认领)。
	if st.Status == model.SubtaskInProgress && st.StartedAt == nil {
		now := st.UpdatedAt
		st.StartedAt = &now
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE itsm_subtasks SET title=$1, description=$2, assignee=$3, status=$4,
			due_at=$5, updated_at=$6, completed_at=$7,
			detail=$8, viewed_at=$9, started_at=$10, updated_by=$11, close_with_parent=$12
		WHERE id=$13 AND ticket_id=$14 AND tenant_id=$15`,
		st.Title, nullable(st.Description), nullable(st.Assignee), string(st.Status),
		nullableTime(st.DueAt), st.UpdatedAt, nullableTime(st.CompletedAt),
		nullable(st.Detail), nullableTime(st.ViewedAt), nullableTime(st.StartedAt),
		nullable(st.UpdatedBy), st.CloseWithParent,
		st.ID, st.TicketID, st.TenantID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) DeleteSubtask(ctx context.Context, id int64, ticketID, tenantID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM itsm_subtasks WHERE id = $1 AND ticket_id = $2 AND tenant_id = $3`, id, ticketID, tenantID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) AddAttachment(ctx context.Context, att *model.Attachment) error {
	att.CreatedAt = time.Now()
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO itsm_attachments (ticket_id, tenant_id, file_name, file_size, file_type, uploaded_by, data, object_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		att.TicketID, att.TenantID, att.FileName, att.FileSize, att.FileType, att.UploadedBy, att.Data, att.ObjectKey, att.CreatedAt).Scan(&att.ID)
	return err
}

func (s *PGStore) ListAttachments(ctx context.Context, ticketID, tenantID string) ([]*model.Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ticket_id, tenant_id, file_name, file_size, file_type, uploaded_by, created_at
		FROM itsm_attachments WHERE ticket_id = $1 AND tenant_id = $2
		ORDER BY created_at ASC`, ticketID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var atts []*model.Attachment
	for rows.Next() {
		a := &model.Attachment{}
		if err := rows.Scan(&a.ID, &a.TicketID, &a.TenantID, &a.FileName, &a.FileSize, &a.FileType, &a.UploadedBy, &a.CreatedAt); err != nil {
			return nil, err
		}
		atts = append(atts, a)
	}
	return atts, rows.Err()
}

func (s *PGStore) GetAttachment(ctx context.Context, id int64, ticketID, tenantID string) (*model.Attachment, error) {
	a := &model.Attachment{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, ticket_id, tenant_id, file_name, file_size, file_type, uploaded_by, data, object_key, created_at
		FROM itsm_attachments WHERE id = $1 AND ticket_id = $2 AND tenant_id = $3`, id, ticketID, tenantID).Scan(
		&a.ID, &a.TicketID, &a.TenantID, &a.FileName, &a.FileSize, &a.FileType, &a.UploadedBy, &a.Data, &a.ObjectKey, &a.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

func (s *PGStore) DeleteAttachment(ctx context.Context, id int64, ticketID, tenantID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM itsm_attachments WHERE id = $1 AND ticket_id = $2 AND tenant_id = $3`, id, ticketID, tenantID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) ListActiveTicketsWithDeadline(ctx context.Context, tenantID string) ([]*model.Ticket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, title, description, risk_level, status, change_plan, rollback_plan,
			created_by, created_by_type, approved_by, reject_reason, completed_by, rolled_back_by,
			rollback_reason, ci_ids, webhook_url, labels, trace_id, scheduled_at, deadline_at,
			created_at, updated_at, approved_at, executed_at, completed_at, rolled_back_at,
			sla_warning_notified, sla_breached, sla_breached_at, sla_escalation_level, custom_fields,
			process_id, assignee
		FROM itsm_tickets
		WHERE tenant_id = $1 AND status IN ('draft','pending_review','approved','executing')
		  AND deadline_at IS NOT NULL AND COALESCE(on_hold, false) = false`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.Ticket
	for rows.Next() {
		t := &model.Ticket{}
		var ciIDsJSON, labelsJSON, customFieldsJSON []byte
		var processID, assignee, changePlan, rollbackPlan sql.NullString
		var scheduledAt, deadlineAt, approvedAt, executedAt, completedAt, rolledBackAt, slaBreachedAt sql.NullTime
		err := rows.Scan(
			&t.ID, &t.TenantID, &t.Title, &t.Description, &t.RiskLevel, &t.Status,
			&changePlan, &rollbackPlan, &t.CreatedBy, &t.CreatedByType,
			&t.ApprovedBy, &t.RejectReason, &t.CompletedBy, &t.RolledBackBy,
			&t.RollbackReason, &ciIDsJSON, &t.WebhookURL, &labelsJSON, &t.TraceID,
			&scheduledAt, &deadlineAt, &t.CreatedAt, &t.UpdatedAt,
			&approvedAt, &executedAt, &completedAt, &rolledBackAt,
			&t.SLAWarningNotified, &t.SLABreached, &slaBreachedAt, &t.SLAEscalationLevel,
			&customFieldsJSON, &processID, &assignee,
		)
		if err != nil {
			return nil, err
		}
		t.ChangePlan = nullableStr(changePlan)
		t.RollbackPlan = nullableStr(rollbackPlan)
		if scheduledAt.Valid {
			t.ScheduledAt = &scheduledAt.Time
		}
		if deadlineAt.Valid {
			t.DeadlineAt = &deadlineAt.Time
		}
		if approvedAt.Valid {
			t.ApprovedAt = &approvedAt.Time
		}
		if executedAt.Valid {
			t.ExecutedAt = &executedAt.Time
		}
		if completedAt.Valid {
			t.CompletedAt = &completedAt.Time
		}
		if rolledBackAt.Valid {
			t.RolledBackAt = &rolledBackAt.Time
		}
		if slaBreachedAt.Valid {
			t.SLABreachedAt = &slaBreachedAt.Time
		}
		if ciIDsJSON != nil {
			json.Unmarshal(ciIDsJSON, &t.CIIDs)
		}
		if labelsJSON != nil {
			json.Unmarshal(labelsJSON, &t.Labels)
		}
		if len(customFieldsJSON) > 0 && string(customFieldsJSON) != "{}" {
			json.Unmarshal(customFieldsJSON, &t.CustomFields)
		}
		t.ProcessID = nullableStr(processID)
		t.Assignee = nullableStr(assignee)
		result = append(result, t)
	}
	return result, nil
}

func (s *PGStore) ListOnHoldTicketsPastDue(ctx context.Context, tenantID string) ([]*model.Ticket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, title, description, risk_level, status, change_plan, rollback_plan,
			created_by, created_by_type, approved_by, reject_reason, completed_by, rolled_back_by,
			rollback_reason, ci_ids, webhook_url, labels, trace_id, scheduled_at, deadline_at,
			created_at, updated_at, approved_at, executed_at, completed_at, rolled_back_at,
			sla_warning_notified, sla_breached, sla_breached_at, sla_escalation_level, custom_fields,
			process_id, assignee, on_hold, hold_reason, hold_until_at
		FROM itsm_tickets
		WHERE tenant_id = $1 AND COALESCE(on_hold, false) = true
		  AND hold_until_at IS NOT NULL AND hold_until_at <= NOW()`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.Ticket
	for rows.Next() {
		t := &model.Ticket{}
		var ciIDsJSON, labelsJSON, customFieldsJSON []byte
		var slaWarningNotified, slaBreached bool
		var slaBreachedAt, deadlineAt, holdUntilAt sql.NullTime
		var slaEscalationLevel sql.NullInt64
		var processID, assignee, changePlan, rollbackPlan sql.NullString
		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.Title, &t.Description, &t.RiskLevel, &t.Status,
			&changePlan, &rollbackPlan, &t.CreatedBy, &t.CreatedByType, &t.ApprovedBy,
			&t.RejectReason, &t.CompletedBy, &t.RolledBackBy, &t.RollbackReason,
			&ciIDsJSON, &t.WebhookURL, &labelsJSON, &t.TraceID,
			&t.ScheduledAt, &deadlineAt, &t.CreatedAt, &t.UpdatedAt, &t.ApprovedAt,
			&t.ExecutedAt, &t.CompletedAt, &t.RolledBackAt,
			&slaWarningNotified, &slaBreached, &slaBreachedAt, &slaEscalationLevel,
			&customFieldsJSON, &processID, &assignee, &t.OnHold, &t.HoldReason, &holdUntilAt,
		); err != nil {
			return nil, err
		}
		t.ChangePlan = nullableStr(changePlan)
		t.RollbackPlan = nullableStr(rollbackPlan)
		t.SLAWarningNotified = slaWarningNotified
		t.SLABreached = slaBreached
		if slaBreachedAt.Valid {
			t.SLABreachedAt = &slaBreachedAt.Time
		}
		if deadlineAt.Valid {
			t.DeadlineAt = &deadlineAt.Time
		}
		if holdUntilAt.Valid {
			t.HoldUntilAt = &holdUntilAt.Time
		}
		if ciIDsJSON != nil {
			json.Unmarshal(ciIDsJSON, &t.CIIDs)
		}
		if labelsJSON != nil {
			json.Unmarshal(labelsJSON, &t.Labels)
		}
		if customFieldsJSON != nil {
			json.Unmarshal(customFieldsJSON, &t.CustomFields)
		}
		t.ProcessID = nullableStr(processID)
		t.Assignee = nullableStr(assignee)
		result = append(result, t)
	}
	return result, nil
}

// ListTenantIDs 返回存在工单的所有租户, 供 SLA 检查器等跨租户任务遍历。
func (s *PGStore) ListTenantIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT tenant_id FROM itsm_tickets WHERE tenant_id <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			return nil, err
		}
		out = append(out, tid)
	}
	if len(out) == 0 {
		out = []string{"default"}
	}
	return out, rows.Err()
}

func (s *PGStore) GetSLAStats(ctx context.Context, tenantID string) (*model.SLAStats, error) {
	stats := &model.SLAStats{}
	// Total active with deadline
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM itsm_tickets
		WHERE tenant_id = $1 AND deadline_at IS NOT NULL AND status IN ('draft','pending_review','approved','executing')`,
		tenantID).Scan(&stats.TotalActive)
	// SLA breached
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM itsm_tickets
		WHERE tenant_id = $1 AND sla_breached = true`,
		tenantID).Scan(&stats.SLABreached)
	// SLA met (completed without breach)
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM itsm_tickets
		WHERE tenant_id = $1 AND deadline_at IS NOT NULL AND sla_breached = false
		AND status IN ('succeeded','rejected','failed','rolled_back')`,
		tenantID).Scan(&stats.SLAMet)
	// SLA warning
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM itsm_tickets
		WHERE tenant_id = $1 AND sla_warning_notified = true AND sla_breached = false
		AND status IN ('draft','pending_review','approved','executing')`,
		tenantID).Scan(&stats.SLAWarning)
	total := stats.SLAMet + stats.SLABreached
	if total > 0 {
		stats.ComplianceRate = float64(stats.SLAMet) / float64(total)
	}
	// 平均响应/解决耗时(此前恒 0, 顺手补齐): 已批准工单的批准耗时 / 已完成工单的完成耗时
	var respMs, resMs sql.NullFloat64
	s.db.QueryRowContext(ctx, `
		SELECT AVG(EXTRACT(EPOCH FROM (approved_at - created_at)) * 1000) FROM itsm_tickets
		WHERE tenant_id = $1 AND approved_at IS NOT NULL`, tenantID).Scan(&respMs)
	s.db.QueryRowContext(ctx, `
		SELECT AVG(EXTRACT(EPOCH FROM (completed_at - created_at)) * 1000) FROM itsm_tickets
		WHERE tenant_id = $1 AND completed_at IS NOT NULL`, tenantID).Scan(&resMs)
	if respMs.Valid {
		stats.AvgResponseMs = int64(respMs.Float64)
	}
	if resMs.Valid {
		stats.AvgResolutionMs = int64(resMs.Float64)
	}
	return stats, nil
}

func (s *PGStore) CreateChange(ctx context.Context, change *model.ChangeRequest) error {
	change.ID = uuid.New().String()
	now := time.Now()
	change.CreatedAt = now
	change.UpdatedAt = now
	if change.Status == "" {
		change.Status = model.ChangeStatusPending
	}
	cabMembersJSON, _ := json.Marshal(change.CABMembers)
	approvalsJSON, _ := json.Marshal(change.Approvals)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO itsm_changes
			(id, tenant_id, ticket_id, title, description, risk_level, status,
			 change_type, impact, urgency, cab_members, approvals, plan,
			 rollback_plan, created_by, created_at, updated_at,
			 scheduled_at, window_start, window_end)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		change.ID, change.TenantID, nullable(change.TicketID),
		change.Title, nullable(change.Description), string(change.RiskLevel),
		string(change.Status), nullable(change.ChangeType),
		nullable(change.Impact), nullable(change.Urgency),
		string(cabMembersJSON), string(approvalsJSON),
		nullable(change.Plan), nullable(change.RollbackPlan),
		nullable(change.CreatedBy), change.CreatedAt, change.UpdatedAt,
		nullableTime(change.ScheduledAt), nullableTime(change.WindowStart), nullableTime(change.WindowEnd))
	if err != nil {
		return fmt.Errorf("insert change: %w", err)
	}
	return nil
}

func (s *PGStore) GetChange(ctx context.Context, id, tenantID string) (*model.ChangeRequest, error) {
	c := &model.ChangeRequest{}
	var desc, changeType, impact, urgency, plan, rollbackPlan, createdBy, ticketID sql.NullString
	var cabMembersJSON, approvalsJSON []byte
	var scheduledAt, windowStart, windowEnd sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, ticket_id, title, description, risk_level, status,
		       change_type, impact, urgency, cab_members, approvals, plan,
		       rollback_plan, created_by, created_at, updated_at,
		       scheduled_at, window_start, window_end
		FROM itsm_changes WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(
		&c.ID, &c.TenantID, &ticketID, &c.Title, &desc, &c.RiskLevel, &c.Status,
		&changeType, &impact, &urgency, &cabMembersJSON, &approvalsJSON,
		&plan, &rollbackPlan, &createdBy, &c.CreatedAt, &c.UpdatedAt,
		&scheduledAt, &windowStart, &windowEnd)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query change: %w", err)
	}
	c.TicketID = nullableStr(ticketID)
	c.Description = nullableStr(desc)
	c.ChangeType = nullableStr(changeType)
	c.Impact = nullableStr(impact)
	c.Urgency = nullableStr(urgency)
	c.Plan = nullableStr(plan)
	c.RollbackPlan = nullableStr(rollbackPlan)
	c.CreatedBy = nullableStr(createdBy)
	setTimePtr(&c.ScheduledAt, scheduledAt)
	setTimePtr(&c.WindowStart, windowStart)
	setTimePtr(&c.WindowEnd, windowEnd)
	if cabMembersJSON != nil {
		json.Unmarshal(cabMembersJSON, &c.CABMembers)
	}
	if approvalsJSON != nil {
		json.Unmarshal(approvalsJSON, &c.Approvals)
	}
	return c, nil
}

func (s *PGStore) ListChanges(ctx context.Context, tenantID string) ([]*model.ChangeRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, ticket_id, title, description, risk_level, status,
		       change_type, impact, urgency, cab_members, approvals, plan,
		       rollback_plan, created_by, created_at, updated_at,
		       scheduled_at, window_start, window_end
		FROM itsm_changes WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list changes: %w", err)
	}
	defer rows.Close()
	var result []*model.ChangeRequest
	for rows.Next() {
		c := &model.ChangeRequest{}
		var desc, changeType, impact, urgency, plan, rollbackPlan, createdBy, ticketID sql.NullString
		var cabMembersJSON, approvalsJSON []byte
		var scheduledAt, windowStart, windowEnd sql.NullTime
		if err := rows.Scan(
			&c.ID, &c.TenantID, &ticketID, &c.Title, &desc, &c.RiskLevel, &c.Status,
			&changeType, &impact, &urgency, &cabMembersJSON, &approvalsJSON,
			&plan, &rollbackPlan, &createdBy, &c.CreatedAt, &c.UpdatedAt,
			&scheduledAt, &windowStart, &windowEnd); err != nil {
			return nil, fmt.Errorf("scan change: %w", err)
		}
		c.TicketID = nullableStr(ticketID)
		c.Description = nullableStr(desc)
		c.ChangeType = nullableStr(changeType)
		c.Impact = nullableStr(impact)
		c.Urgency = nullableStr(urgency)
		c.Plan = nullableStr(plan)
		c.RollbackPlan = nullableStr(rollbackPlan)
		c.CreatedBy = nullableStr(createdBy)
		setTimePtr(&c.ScheduledAt, scheduledAt)
		setTimePtr(&c.WindowStart, windowStart)
		setTimePtr(&c.WindowEnd, windowEnd)
		if cabMembersJSON != nil {
			json.Unmarshal(cabMembersJSON, &c.CABMembers)
		}
		if approvalsJSON != nil {
			json.Unmarshal(approvalsJSON, &c.Approvals)
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *PGStore) UpdateChange(ctx context.Context, change *model.ChangeRequest) error {
	change.UpdatedAt = time.Now()
	cabMembersJSON, _ := json.Marshal(change.CABMembers)
	approvalsJSON, _ := json.Marshal(change.Approvals)
	result, err := s.db.ExecContext(ctx, `
		UPDATE itsm_changes SET
			ticket_id=$1, title=$2, description=$3, risk_level=$4, status=$5,
			change_type=$6, impact=$7, urgency=$8, cab_members=$9, approvals=$10,
			plan=$11, rollback_plan=$12, created_by=$13, updated_at=$14,
			scheduled_at=$15, window_start=$16, window_end=$17
		WHERE id=$18 AND tenant_id=$19`,
		nullable(change.TicketID), change.Title, nullable(change.Description),
		string(change.RiskLevel), string(change.Status),
		nullable(change.ChangeType), nullable(change.Impact), nullable(change.Urgency),
		string(cabMembersJSON), string(approvalsJSON),
		nullable(change.Plan), nullable(change.RollbackPlan),
		nullable(change.CreatedBy), change.UpdatedAt,
		nullableTime(change.ScheduledAt), nullableTime(change.WindowStart), nullableTime(change.WindowEnd),
		change.ID, change.TenantID)
	if err != nil {
		return fmt.Errorf("update change: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Form schema methods (postgres) ---

func (s *PGStore) GetFormSchema(ctx context.Context, tenantID, scope string) (*form.FormSchema, error) {
	var fieldsJSON []byte
	var version int
	err := s.db.QueryRowContext(ctx, `
		SELECT version, fields FROM itsm_form_schemas WHERE tenant_id = $1 AND scope = $2`,
		tenantID, scope).Scan(&version, &fieldsJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query form schema: %w", err)
	}
	schema := &form.FormSchema{Scope: scope, Version: version}
	if len(fieldsJSON) > 0 {
		if err := json.Unmarshal(fieldsJSON, &schema.Fields); err != nil {
			return nil, fmt.Errorf("unmarshal form schema fields: %w", err)
		}
	}
	return schema, nil
}

func (s *PGStore) UpsertFormSchema(ctx context.Context, tenantID, scope string, schema form.FormSchema) error {
	fieldsJSON, err := json.Marshal(schema.Fields)
	if err != nil {
		return fmt.Errorf("marshal form schema fields: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO itsm_form_schemas (tenant_id, scope, version, fields, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (tenant_id, scope)
		DO UPDATE SET version = EXCLUDED.version, fields = EXCLUDED.fields, updated_at = NOW()`,
		tenantID, scope, schema.Version, string(fieldsJSON))
	if err != nil {
		return fmt.Errorf("upsert form schema: %w", err)
	}
	return nil
}

func (s *PGStore) ListFormScopes(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT scope FROM itsm_form_schemas WHERE tenant_id = $1 ORDER BY scope`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list form scopes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sc string
		if err := rows.Scan(&sc); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// --- 数据字典(PG) ---

func (s *PGStore) initDictTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS itsm_form_dicts (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			fields TEXT DEFAULT '[]',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`)
	return err
}

func (s *PGStore) GetFieldDict(ctx context.Context, tenantID, id string) (*form.FieldDict, error) {
	var d form.FieldDict
	var fieldsJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, description, fields FROM itsm_form_dicts WHERE id = $1 AND tenant_id = $2`,
		id, tenantID).Scan(&d.ID, &d.TenantID, &d.Name, &d.Description, &fieldsJSON)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(fieldsJSON), &d.Fields)
	return &d, nil
}

func (s *PGStore) ListFieldDicts(ctx context.Context, tenantID string) ([]*form.FieldDict, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, description, fields FROM itsm_form_dicts WHERE tenant_id = $1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*form.FieldDict
	for rows.Next() {
		var d form.FieldDict
		var fieldsJSON string
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Name, &d.Description, &fieldsJSON); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(fieldsJSON), &d.Fields)
		out = append(out, &d)
	}
	return out, rows.Err()
}

func (s *PGStore) UpsertFieldDict(ctx context.Context, tenantID string, dict *form.FieldDict) error {
	if dict.ID == "" {
		dict.ID = uuid.New().String()
	}
	dict.TenantID = tenantID
	fieldsJSON, _ := json.Marshal(dict.Fields)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO itsm_form_dicts (id, tenant_id, name, description, fields, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description,
			fields = EXCLUDED.fields, updated_at = NOW()`,
		dict.ID, tenantID, dict.Name, dict.Description, string(fieldsJSON))
	return err
}

func (s *PGStore) DeleteFieldDict(ctx context.Context, tenantID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM itsm_form_dicts WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
