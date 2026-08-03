package agentic

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strconv"
)

// ConversationStore 对话持久化接口。PG 实现落库；nil 时使用内存兜底。
type ConversationStore interface {
	Save(ctx context.Context, c *Conversation) error
	List(ctx context.Context, tenantID, agentID string, limit int) ([]*Conversation, error)
	Get(ctx context.Context, id string) (*Conversation, error)
}

// ——— PG 实现 ———

type pgConvStore struct {
	db *sql.DB
}

func NewPGConvStore(db *sql.DB) *pgConvStore {
	s := &pgConvStore{db: db}
	s.init(context.Background())
	return s
}

func (s *pgConvStore) init(ctx context.Context) {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_conversations (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			agent_id TEXT,
			title TEXT,
			messages TEXT DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`)
	if err != nil {
		slog.Warn("ai conversation store init DDL failed", "err", err)
		return
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_conversations_tenant ON ai_conversations(tenant_id, updated_at DESC)`)
}

func (s *pgConvStore) Save(ctx context.Context, c *Conversation) error {
	b, _ := json.Marshal(c.Messages)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_conversations (id, tenant_id, agent_id, title, messages, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET title=$4, messages=$5, updated_at=$7`,
		c.ID, c.TenantID, c.AgentID, c.Title, b, c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *pgConvStore) List(ctx context.Context, tenantID, agentID string, limit int) ([]*Conversation, error) {
	query := `SELECT id, tenant_id, agent_id, title, messages, created_at, updated_at FROM ai_conversations WHERE tenant_id=$1`
	args := []any{tenantID}
	if agentID != "" {
		query += ` AND agent_id=$2`
		args = append(args, agentID)
	}
	query += ` ORDER BY updated_at DESC`
	if limit > 0 {
		query += ` LIMIT $` + strconv.Itoa(len(args)+1)
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Conversation
	for rows.Next() {
		c := &Conversation{}
		var msgs []byte
		var agentID sql.NullString
		if err := rows.Scan(&c.ID, &c.TenantID, &agentID, &c.Title, &msgs, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if agentID.Valid {
			c.AgentID = agentID.String
		}
		_ = json.Unmarshal(msgs, &c.Messages)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *pgConvStore) Get(ctx context.Context, id string) (*Conversation, error) {
	c := &Conversation{}
	var msgs []byte
	var agentID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, agent_id, title, messages, created_at, updated_at
		FROM ai_conversations WHERE id=$1`, id).
		Scan(&c.ID, &c.TenantID, &agentID, &c.Title, &msgs, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if agentID.Valid {
		c.AgentID = agentID.String
	}
	_ = json.Unmarshal(msgs, &c.Messages)
	return c, nil
}
