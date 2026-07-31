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

func (s *PGStore) Query(ctx context.Context, filter model.AuditFilter) ([]*model.AuditEvent, int, error) {
	// 条件构造: 返回 (whereSQL, args) 供数据查询与 COUNT 共用
	whereSQL, args := s.buildWhere(filter)

	// 总条数(分页用)
	var total int
	countArgs := append([]any{}, args...)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE 1=1`+whereSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit events: %w", err)
	}

	// 数据查询
	query := `SELECT id, trace_id, tenant_id, event_type, actor_type, actor_id,
	                 actor_display_name, actor_ticket_id, target_type, target_id, target_name,
	                 action, request, response, success, error, ip_address, user_agent, timestamp
	          FROM audit_events WHERE 1=1` + whereSQL + ` ORDER BY timestamp DESC`

	argIdx := len(args) + 1
	queryArgs := append([]any{}, args...)
	if filter.Limit > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, argIdx)
		queryArgs = append(queryArgs, filter.Limit)
		argIdx++
	} else {
		query += ` LIMIT 100`
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(` OFFSET $%d`, argIdx)
		queryArgs = append(queryArgs, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query audit events: %w", err)
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
			return nil, 0, fmt.Errorf("scan audit event: %w", err)
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
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if events == nil {
		events = []*model.AuditEvent{}
	}
	return events, total, nil
}

// buildWhere 构造 WHERE 条件片段(不含 WHERE 1=1)与参数列表。
func (s *PGStore) buildWhere(filter model.AuditFilter) (string, []any) {
	query := ""
	args := []any{}
	argIdx := 1

	add := func(cond string, v any) {
		query += fmt.Sprintf(` AND %s $%d`, cond, argIdx)
		args = append(args, v)
		argIdx++
	}

	if filter.TenantID != "" {
		add("tenant_id =", filter.TenantID)
	}
	if filter.TraceID != "" {
		add("trace_id =", filter.TraceID)
	}
	if filter.EventType != "" {
		add("event_type =", filter.EventType)
	}
	if filter.ActorType != "" {
		add("actor_type =", filter.ActorType)
	}
	if filter.ActorID != "" {
		// actor_id / display_name 模糊匹配(操作主体搜索)
		query += fmt.Sprintf(` AND (actor_id ILIKE $%d OR actor_display_name ILIKE $%d)`, argIdx, argIdx+1)
		like := "%" + filter.ActorID + "%"
		args = append(args, like, like)
		argIdx += 2
	}
	if filter.TargetType != "" {
		add("target_type =", filter.TargetType)
	}
	if filter.Keyword != "" {
		// 全局关键词: action / target_name / trace_id 模糊
		query += fmt.Sprintf(` AND (action ILIKE $%d OR target_name ILIKE $%d OR trace_id ILIKE $%d OR event_type ILIKE $%d)`, argIdx, argIdx+1, argIdx+2, argIdx+3)
		like := "%" + filter.Keyword + "%"
		args = append(args, like, like, like, like)
		argIdx += 4
	}
	if filter.Success != nil {
		add("success =", *filter.Success)
	}
	if !filter.From.IsZero() {
		add("timestamp >=", filter.From)
	}
	if !filter.To.IsZero() {
		add("timestamp <=", filter.To)
	}
	return query, args
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
