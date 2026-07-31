package agentic

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/opsorigin/core/pkg/llm"
)

// TeamConvStore 团队多轮对话持久化接口。PG 实现落库;nil 时引擎仅内存/无历史。
type TeamConvStore interface {
	// Append 把若干条消息(通常为一轮 user+assistant)追加到某团队的对话历史。
	Append(ctx context.Context, teamID, tenantID string, msgs []llm.Message) error
	// Recent 返回某团队最近 limit 条消息(按时间升序,可直接喂给 LLM)。
	Recent(ctx context.Context, teamID string, limit int) ([]llm.Message, error)
}

type pgTeamConvStore struct{ db *sql.DB }

func NewPGTeamConvStore(db *sql.DB) *pgTeamConvStore {
	s := &pgTeamConvStore{db: db}
	s.init(context.Background())
	return s
}

func (s *pgTeamConvStore) init(ctx context.Context) {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_team_conversations (
			id BIGSERIAL PRIMARY KEY,
			team_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`)
	if err != nil {
		slog.Warn("ai team conversation store init DDL failed", "err", err)
		return
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_team_conv_team ON ai_team_conversations(team_id, created_at)`)
}

func (s *pgTeamConvStore) Append(ctx context.Context, teamID, tenantID string, msgs []llm.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	for _, m := range msgs {
		if m.Role == "" {
			continue
		}
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO ai_team_conversations(team_id, tenant_id, role, content, created_at) VALUES($1,$2,$3,$4,$5)`,
			teamID, tenantID, m.Role, m.Content, time.Now())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *pgTeamConvStore) Recent(ctx context.Context, teamID string, limit int) ([]llm.Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT role, content FROM ai_team_conversations WHERE team_id=$1 ORDER BY created_at DESC LIMIT $2`,
		teamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 倒序读出后反转,得到时间升序(LLM 期望的历史顺序)。
	var rev []llm.Message
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return nil, err
		}
		rev = append(rev, llm.Message{Role: role, Content: content})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]llm.Message, len(rev))
	for i, m := range rev {
		out[len(rev)-1-i] = m
	}
	return out, nil
}

// SetTeamConvStore 注入团队对话存储(启动时由 service 装配;nil 则无跨轮记忆)。
func (e *Engine) SetTeamConvStore(s TeamConvStore) { e.teamConvStore = s }

// LoadTeamHistory 返回某团队最近的对话历史(升序),供 RunTeamChat 跨轮记忆使用。
func (e *Engine) LoadTeamHistory(teamID string, limit int) []llm.Message {
	if e.teamConvStore == nil {
		return nil
	}
	msgs, err := e.teamConvStore.Recent(context.Background(), teamID, limit)
	if err != nil {
		slog.Warn("load team history failed", "team", teamID, "err", err)
		return nil
	}
	return msgs
}

// AppendTeamTurn 持久化一轮团队对话(user 提问 + assistant 最终答复)。
func (e *Engine) AppendTeamTurn(teamID, tenantID, userMsg, final string) {
	if e.teamConvStore == nil || final == "" {
		return
	}
	msgs := []llm.Message{
		{Role: "user", Content: userMsg},
		{Role: "assistant", Content: final},
	}
	if err := e.teamConvStore.Append(context.Background(), teamID, tenantID, msgs); err != nil {
		slog.Warn("append team turn failed", "team", teamID, "err", err)
	}
}
