package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/audit/model"
)

type PGStore struct {
	db *sql.DB
}

func NewPGStore(db *sql.DB) *PGStore {
	return &PGStore{db: db}
}

func (s *PGStore) Init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS audit_events (
			id              TEXT PRIMARY KEY,
			trace_id        TEXT NOT NULL,
			tenant_id       TEXT NOT NULL,
			event_type      TEXT NOT NULL,
			actor_type      TEXT NOT NULL,
			actor_id        TEXT,
			actor_display_name TEXT,
			actor_ticket_id TEXT,
			target_type     TEXT,
			target_id       TEXT,
			target_name     TEXT,
			action          TEXT,
			request         TEXT,
			response        TEXT,
			success         BOOLEAN DEFAULT TRUE,
			error           TEXT,
			ip_address      TEXT,
			user_agent      TEXT,
			timestamp       TIMESTAMPTZ NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("init audit_events table: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_audit_events_tenant ON audit_events(tenant_id, event_type)`)
	if err != nil {
		return fmt.Errorf("init audit_events index: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_audit_events_trace ON audit_events(trace_id)`)
	if err != nil {
		return fmt.Errorf("init audit_events trace index: %w", err)
	}
	return nil
}

func (s *PGStore) Record(ctx context.Context, event *model.AuditEvent) error {
	event.ID = uuid.New().String()
	event.Timestamp = time.Now()

	reqJSON, _ := json.Marshal(event.Request)
	respJSON, _ := json.Marshal(event.Response)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_events
			(id, trace_id, tenant_id, event_type, actor_type, actor_id,
			 actor_display_name, actor_ticket_id, target_type, target_id, target_name,
			 action, request, response, success, error, ip_address, user_agent, timestamp)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		event.ID, event.TraceID, event.TenantID, event.EventType, event.Actor.Type,
		event.Actor.ID, event.Actor.DisplayName, event.Actor.TicketID,
		event.Target.Type, event.Target.ID, event.Target.Name,
		event.Action, string(reqJSON), string(respJSON),
		event.Success, nullable(event.Error),
		event.IPAddress, event.UserAgent, event.Timestamp)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func (s *PGStore) Query(ctx context.Context, filter model.AuditFilter) ([]*model.AuditEvent, error) {
	query := `SELECT id, trace_id, tenant_id, event_type, actor_type, actor_id,
	                 actor_display_name, actor_ticket_id, target_type, target_id, target_name,
	                 action, request, response, success, error, ip_address, user_agent, timestamp
	          FROM audit_events WHERE 1=1`
	args := []any{}
	argIdx := 1

	if filter.TenantID != "" {
		query += fmt.Sprintf(` AND tenant_id = $%d`, argIdx)
		args = append(args, filter.TenantID)
		argIdx++
	}
	if filter.TraceID != "" {
		query += fmt.Sprintf(` AND trace_id = $%d`, argIdx)
		args = append(args, filter.TraceID)
		argIdx++
	}
	if filter.EventType != "" {
		query += fmt.Sprintf(` AND event_type = $%d`, argIdx)
		args = append(args, filter.EventType)
		argIdx++
	}
	if filter.ActorType != "" {
		query += fmt.Sprintf(` AND actor_type = $%d`, argIdx)
		args = append(args, filter.ActorType)
		argIdx++
	}
	if !filter.From.IsZero() {
		query += fmt.Sprintf(` AND timestamp >= $%d`, argIdx)
		args = append(args, filter.From)
		argIdx++
	}
	if !filter.To.IsZero() {
		query += fmt.Sprintf(` AND timestamp <= $%d`, argIdx)
		args = append(args, filter.To)
		argIdx++
	}

	query += ` ORDER BY timestamp DESC`

	if filter.Limit > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, argIdx)
		args = append(args, filter.Limit)
		argIdx++
	} else {
		query += ` LIMIT 100`
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(` OFFSET $%d`, argIdx)
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()

	var events []*model.AuditEvent
	for rows.Next() {
		e := &model.AuditEvent{}
		var actorID, actorDisplay, actorTicket, targetType, targetID, targetName sql.NullString
		var action, reqJSON, respJSON, errStr, ipAddr, userAgent sql.NullString

		if err := rows.Scan(
			&e.ID, &e.TraceID, &e.TenantID, &e.EventType, &e.Actor.Type,
			&actorID, &actorDisplay, &actorTicket,
			&targetType, &targetID, &targetName,
			&action, &reqJSON, &respJSON,
			&e.Success, &errStr, &ipAddr, &userAgent, &e.Timestamp); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}

		e.Actor.ID = nullableStr(actorID)
		e.Actor.DisplayName = nullableStr(actorDisplay)
		e.Actor.TicketID = nullableStr(actorTicket)
		e.Target.Type = nullableStr(targetType)
		e.Target.ID = nullableStr(targetID)
		e.Target.Name = nullableStr(targetName)
		e.Action = nullableStr(action)
		e.Error = nullableStr(errStr)
		e.IPAddress = nullableStr(ipAddr)
		e.UserAgent = nullableStr(userAgent)

		if reqJSON.Valid {
			json.Unmarshal([]byte(reqJSON.String), &e.Request)
		}
		if respJSON.Valid {
			json.Unmarshal([]byte(respJSON.String), &e.Response)
		}

		events = append(events, e)
	}
	return events, rows.Err()
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
