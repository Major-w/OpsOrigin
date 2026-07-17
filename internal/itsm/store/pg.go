package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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
			rolled_back_at  TIMESTAMPTZ
		)`)
	if err != nil {
		return fmt.Errorf("init itsm_tickets table: %w", err)
	}
	s.migrate(ctx)
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_itsm_tickets_tenant ON itsm_tickets(tenant_id, status)`)
	if err != nil {
		return fmt.Errorf("init itsm_tickets index: %w", err)
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

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO itsm_tickets
			(id, tenant_id, title, description, risk_level, status, change_plan,
			 rollback_plan, created_by, created_by_type, approved_by, reject_reason,
			 completed_by, rolled_back_by, rollback_reason, ci_ids, webhook_url,
			 labels, trace_id, scheduled_at, deadline_at, created_at, updated_at,
			 approved_at, executed_at, completed_at, rolled_back_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`,
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
		nullableTime(ticket.CompletedAt), nullableTime(ticket.RolledBackAt))
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
	var approvedAt, executedAt, completedAt, rolledBackAt sql.NullTime
	var scheduledAt, deadlineAt sql.NullTime

	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, title, description, risk_level, status, change_plan,
		       rollback_plan, created_by, created_by_type, approved_by, reject_reason,
		       completed_by, rolled_back_by, rollback_reason, ci_ids, webhook_url,
		       labels, trace_id, scheduled_at, deadline_at, created_at, updated_at,
		       approved_at, executed_at, completed_at, rolled_back_at
		FROM itsm_tickets WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(
		&t.ID, &t.TenantID, &t.Title, &desc, &t.RiskLevel, &t.Status,
		&changePlan, &rollbackPlan, &t.CreatedBy, &t.CreatedByType,
		&approvedBy, &rejectReason, &completedBy, &rolledBackBy, &rollbackReason,
		&ciIDsJSON, &webhookURL, &labelsJSON, &t.TraceID,
		&scheduledAt, &deadlineAt, &t.CreatedAt, &t.UpdatedAt,
		&approvedAt, &executedAt, &completedAt, &rolledBackAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query ticket: %w", err)
	}

	t.Description = nullableStr(desc)
	t.ChangePlan = nullableStr(changePlan)
	t.RollbackPlan = nullableStr(rollbackPlan)
	t.ApprovedBy = nullableStr(approvedBy)
	t.RejectReason = nullableStr(rejectReason)
	t.CompletedBy = nullableStr(completedBy)
	t.RolledBackBy = nullableStr(rolledBackBy)
	t.RollbackReason = nullableStr(rollbackReason)
	t.WebhookURL = nullableStr(webhookURL)
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

	result, err := s.db.ExecContext(ctx, `
		UPDATE itsm_tickets SET
			title=$1, description=$2, risk_level=$3, status=$4, change_plan=$5,
			rollback_plan=$6, created_by=$7, created_by_type=$8, approved_by=$9,
			reject_reason=$10, completed_by=$11, rolled_back_by=$12,
			rollback_reason=$13, ci_ids=$14, webhook_url=$15, labels=$16,
			trace_id=$17, updated_at=$18, approved_at=$19, executed_at=$20,
			completed_at=$21, rolled_back_at=$22, scheduled_at=$23, deadline_at=$24
		WHERE id=$25 AND tenant_id=$26`,
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
	                  approved_at, executed_at, completed_at, rolled_back_at
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
		var approvedAt, executedAt, completedAt, rolledBackAt sql.NullTime
		var scheduledAt, deadlineAt sql.NullTime

		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.Title, &desc, &t.RiskLevel, &t.Status,
			&changePlan, &rollbackPlan, &t.CreatedBy, &t.CreatedByType,
			&approvedBy, &rejectReason, &completedBy, &rolledBackBy, &rollbackReason,
			&ciIDsJSON, &webhookURL, &labelsJSON, &t.TraceID,
			&scheduledAt, &deadlineAt, &t.CreatedAt, &t.UpdatedAt,
			&approvedAt, &executedAt, &completedAt, &rolledBackAt); err != nil {
			return nil, fmt.Errorf("scan ticket: %w", err)
		}

		t.Description = nullableStr(desc)
		t.ChangePlan = nullableStr(changePlan)
		t.RollbackPlan = nullableStr(rollbackPlan)
		t.ApprovedBy = nullableStr(approvedBy)
		t.RejectReason = nullableStr(rejectReason)
		t.CompletedBy = nullableStr(completedBy)
		t.RolledBackBy = nullableStr(rolledBackBy)
		t.RollbackReason = nullableStr(rollbackReason)
		t.WebhookURL = nullableStr(webhookURL)
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
		ByStatus: make(map[string]int),
		ByRisk:   make(map[string]int),
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT status, risk_level, approved_at, created_at, executed_at
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

	for rows.Next() {
		var status, risk string
		var approvedAt, createdAt time.Time
		var executedAt sql.NullTime
		if err := rows.Scan(&status, &risk, &approvedAt, &createdAt, &executedAt); err != nil {
			return nil, fmt.Errorf("scan stats row: %w", err)
		}
		stats.Total++
		stats.ByStatus[status]++
		stats.ByRisk[risk]++
		if status == "approved" {
			approvedCount++
		}
		var zero time.Time
		if approvedAt != zero && createdAt.Before(approvedAt) {
			totalApprovalMs += approvedAt.Sub(createdAt).Milliseconds()
			approvedWithAt++
		}
		if executedAt.Valid && approvedAt != zero && executedAt.Time.After(approvedAt) {
			totalExecutionMs += executedAt.Time.Sub(approvedAt).Milliseconds()
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
