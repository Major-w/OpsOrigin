package message

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PGStore PostgreSQL 实现。建表走自愈 DDL(CREATE TABLE IF NOT EXISTS + ADD COLUMN
// IF NOT EXISTS),与仓库既有模块保持一致;同时在 deploy/migrations 留一份 SQL
// 供全新环境按序初始化。
type PGStore struct{ db *sql.DB }

func NewPGStore(db *sql.DB) *PGStore { return &PGStore{db: db} }

const messageCols = `id,tenant_id,user_id,username,category,level,title,content,link,ref_type,ref_id,actor,is_read,read_at,created_at`

// Init 建表 + 索引。返回错误由调用方决定是否回落内存实现。
func (s *PGStore) Init(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("message: nil db")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS platform_messages (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			user_id TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT 'system',
			level TEXT NOT NULL DEFAULT 'info',
			title TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL DEFAULT '',
			link TEXT NOT NULL DEFAULT '',
			ref_type TEXT NOT NULL DEFAULT '',
			ref_id TEXT NOT NULL DEFAULT '',
			actor TEXT NOT NULL DEFAULT '',
			is_read BOOLEAN NOT NULL DEFAULT FALSE,
			read_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_messages_inbox
			ON platform_messages(tenant_id, user_id, is_read, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_messages_inbox_name
			ON platform_messages(tenant_id, username, is_read, created_at DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("message: init schema: %w", err)
		}
	}
	return nil
}

func (s *PGStore) Insert(ctx context.Context, msgs ...*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // 提交成功后 Rollback 是 no-op

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO platform_messages(`+messageCols+`)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range msgs {
		if m == nil {
			continue
		}
		normalize(m)
		if _, err := stmt.ExecContext(ctx,
			m.ID, m.TenantID, m.UserID, m.Username, string(m.Category), string(m.Level),
			m.Title, m.Content, m.Link, m.RefType, m.RefID, m.Actor,
			m.Read, m.ReadAt, m.CreatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// recipientClause 生成「本人消息」的 SQL 条件。收件人双标识任一命中即可,
// 全部走 $N 占位,不拼接用户输入。
func recipientClause(r Recipient, args []any) (string, []any) {
	var parts []string
	if uid := strings.TrimSpace(r.UserID); uid != "" {
		args = append(args, uid)
		parts = append(parts, fmt.Sprintf("user_id = $%d", len(args)))
	}
	if name := strings.TrimSpace(r.Username); name != "" {
		args = append(args, name)
		parts = append(parts, fmt.Sprintf("LOWER(username) = LOWER($%d)", len(args)))
	}
	if len(parts) == 0 {
		// 收件人为空时返回恒假条件,绝不退化成「查全部人的消息」。
		return "FALSE", args
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func (s *PGStore) List(ctx context.Context, q ListQuery) ([]*Message, int, error) {
	q = q.normalized()

	args := []any{q.TenantID}
	where := "tenant_id = $1"

	clause, args := recipientClause(q.Recipient, args)
	where += " AND " + clause

	if q.Category != "" {
		args = append(args, q.Category)
		where += fmt.Sprintf(" AND category = $%d", len(args))
	}
	if q.OnlyUnread {
		where += " AND is_read = FALSE"
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_messages WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, q.Limit, q.Offset)
	query := `SELECT ` + messageCols + ` FROM platform_messages WHERE ` + where +
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]*Message, 0, q.Limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (s *PGStore) UnreadCount(ctx context.Context, tenantID string, r Recipient) (int, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	args := []any{tenantID}
	clause, args := recipientClause(r, args)
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM platform_messages WHERE tenant_id = $1 AND `+clause+` AND is_read = FALSE`,
		args...).Scan(&n)
	return n, err
}

func (s *PGStore) MarkRead(ctx context.Context, tenantID, id string, r Recipient) error {
	args := []any{tenantID, id, time.Now()}
	clause, args := recipientClause(r, args)
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET is_read = TRUE, read_at = $3
		 WHERE tenant_id = $1 AND id = $2 AND `+clause+` AND is_read = FALSE`, args...)
	return err
}

func (s *PGStore) MarkAllRead(ctx context.Context, tenantID string, r Recipient) (int, error) {
	args := []any{tenantID, time.Now()}
	clause, args := recipientClause(r, args)
	res, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET is_read = TRUE, read_at = $2
		 WHERE tenant_id = $1 AND `+clause+` AND is_read = FALSE`, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *PGStore) Delete(ctx context.Context, tenantID, id string, r Recipient) error {
	args := []any{tenantID, id}
	clause, args := recipientClause(r, args)
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM platform_messages WHERE tenant_id = $1 AND id = $2 AND `+clause, args...)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMessage(row rowScanner) (*Message, error) {
	var (
		m        Message
		category string
		level    string
		readAt   sql.NullTime
	)
	if err := row.Scan(&m.ID, &m.TenantID, &m.UserID, &m.Username, &category, &level,
		&m.Title, &m.Content, &m.Link, &m.RefType, &m.RefID, &m.Actor,
		&m.Read, &readAt, &m.CreatedAt); err != nil {
		return nil, err
	}
	m.Category = Category(category)
	m.Level = Level(level)
	if readAt.Valid {
		t := readAt.Time
		m.ReadAt = &t
	}
	return &m, nil
}
