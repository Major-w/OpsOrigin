package agentic

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Feedback 表示用户对一条 AI 消息的评分(点赞/点踩)与可选评论。
// MessageID 由前端对每条 assistant 回复生成并透传; 为空时退化为按 conversation_id 计(整轮反馈)。
type Feedback struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id"`
	Rating         string    `json:"rating"` // "up" | "down"
	Comment        string    `json:"comment"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// FeedbackStore 消息反馈持久化接口。PG 实现落库; 内存实现用于无 DB 或测试场景。
type FeedbackStore interface {
	Save(ctx context.Context, f *Feedback) error
	List(ctx context.Context, tenantID, conversationID string) ([]*Feedback, error)
	Stats(ctx context.Context, tenantID string) (up, down int, err error)
}

// feedbackTargetKey 计算 upsert 唯一键: 优先 message_id, 退化 conversation_id, 最后 anon。
func feedbackTargetKey(messageID, conversationID string) string {
	if messageID != "" {
		return "msg:" + messageID
	}
	if conversationID != "" {
		return "conv:" + conversationID
	}
	return "anon"
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ——— PG 实现 ———

type pgFeedbackStore struct {
	db *sql.DB
}

// NewPGFeedbackStore 创建 PG 反馈存储并自动建表/索引。
func NewPGFeedbackStore(db *sql.DB) *pgFeedbackStore {
	s := &pgFeedbackStore{db: db}
	s.init(context.Background())
	return s
}

func (s *pgFeedbackStore) init(ctx context.Context) {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_message_feedback (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			conversation_id TEXT,
			message_id TEXT,
			target_key TEXT NOT NULL,
			rating TEXT NOT NULL,
			comment TEXT DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("ai feedback store init DDL failed", "err", err)
		return
	}
	s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_ai_fb_target ON ai_message_feedback(tenant_id, target_key)`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_fb_tenant ON ai_message_feedback(tenant_id, created_at DESC)`)
}

func (s *pgFeedbackStore) Save(ctx context.Context, f *Feedback) error {
	if f.TenantID == "" {
		f.TenantID = "default"
	}
	now := time.Now()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_message_feedback (id, tenant_id, conversation_id, message_id, target_key, rating, comment, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (tenant_id, target_key) DO UPDATE SET rating=$6, comment=$7, updated_at=$9`,
		f.ID, f.TenantID, nullStr(f.ConversationID), nullStr(f.MessageID),
		feedbackTargetKey(f.MessageID, f.ConversationID), f.Rating, f.Comment, f.CreatedAt, f.UpdatedAt)
	return err
}

func (s *pgFeedbackStore) List(ctx context.Context, tenantID, conversationID string) ([]*Feedback, error) {
	query := `SELECT id, tenant_id, conversation_id, message_id, rating, comment, created_at, updated_at
		FROM ai_message_feedback WHERE tenant_id=$1`
	args := []any{tenantID}
	if conversationID != "" {
		query += ` AND conversation_id=$2`
		args = append(args, conversationID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Feedback
	for rows.Next() {
		f := &Feedback{}
		var convID, msgID sql.NullString
		if err := rows.Scan(&f.ID, &f.TenantID, &convID, &msgID, &f.Rating, &f.Comment, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		if convID.Valid {
			f.ConversationID = convID.String
		}
		if msgID.Valid {
			f.MessageID = msgID.String
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *pgFeedbackStore) Stats(ctx context.Context, tenantID string) (up, down int, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN rating='up' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN rating='down' THEN 1 ELSE 0 END),0)
		FROM ai_message_feedback WHERE tenant_id=$1`, tenantID)
	err = row.Scan(&up, &down)
	return
}

// ——— 内存实现(无 DB / 测试) ———

type memFeedbackStore struct {
	mu   sync.Mutex
	data map[string]*Feedback // key: tenantID + "\x00" + targetKey
}

// NewMemFeedbackStore 创建内存反馈存储(无 DB 或测试场景)。
func NewMemFeedbackStore() *memFeedbackStore {
	return &memFeedbackStore{data: map[string]*Feedback{}}
}

func (m *memFeedbackStore) Save(ctx context.Context, f *Feedback) error {
	if f.TenantID == "" {
		f.TenantID = "default"
	}
	now := time.Now()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	key := f.TenantID + "\x00" + feedbackTargetKey(f.MessageID, f.ConversationID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.data[key]; ok {
		f.ID = old.ID
		f.CreatedAt = old.CreatedAt
	}
	m.data[key] = f
	return nil
}

func (m *memFeedbackStore) List(ctx context.Context, tenantID, conversationID string) ([]*Feedback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Feedback
	for _, f := range m.data {
		if f.TenantID != tenantID {
			continue
		}
		if conversationID != "" && f.ConversationID != conversationID {
			continue
		}
		cp := *f
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *memFeedbackStore) Stats(ctx context.Context, tenantID string) (up, down int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.data {
		if f.TenantID != tenantID {
			continue
		}
		if f.Rating == "up" {
			up++
		} else if f.Rating == "down" {
			down++
		}
	}
	return
}
