package agentic

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
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
	// ListAll 返回该租户全部反馈(跨会话, 供训练数据集导出/分析)。
	ListAll(ctx context.Context, tenantID string) ([]*Feedback, error)
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
	// articleCache 文章降权聚合的进程内缓存(按租户)。SaveArticleFeedback 写入时按租户失效,
	// 避免高频检索路径每次都 GROUP BY 全表; 5 分钟兜底过期防漏失效。
	articleCacheMu sync.Mutex
	articleCache   map[string]articlePenaltyEntry
}

type articlePenaltyEntry struct {
	pen    map[string]int
	loaded time.Time
}

// NewPGFeedbackStore 创建 PG 反馈存储并自动建表/索引。
func NewPGFeedbackStore(db *sql.DB) *pgFeedbackStore {
	s := &pgFeedbackStore{db: db, articleCache: map[string]articlePenaltyEntry{}}
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
	// 文章级反馈(检索反馈闭环): 赞踩落到回答引用的具体文章, 供检索 rerank 降权。
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_article_feedback (
			tenant_id TEXT NOT NULL DEFAULT 'default',
			message_id TEXT NOT NULL DEFAULT '',
			article_id TEXT NOT NULL,
			rating TEXT NOT NULL CHECK (rating IN ('up','down')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (tenant_id, message_id, article_id)
		)`); err != nil {
		slog.Warn("ai article feedback DDL failed", "err", err)
		return
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_artfb ON ai_article_feedback(tenant_id, article_id, created_at DESC)`)
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

func (s *pgFeedbackStore) ListAll(ctx context.Context, tenantID string) ([]*Feedback, error) {
	return s.List(ctx, tenantID, "")
}

func (s *pgFeedbackStore) Stats(ctx context.Context, tenantID string) (up, down int, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN rating='up' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN rating='down' THEN 1 ELSE 0 END),0)
		FROM ai_message_feedback WHERE tenant_id=$1`, tenantID)
	err = row.Scan(&up, &down)
	return
}

// ——— 文章级反馈(检索反馈闭环) ———
//
// 消息级反馈(ai_message_feedback)只知道「这条回答好/不好」, 不知道是引用的哪篇知识拖了后腿。
// 文章级反馈把赞踩落到回答引用的具体文章上: 被反复点踩的文章在检索 rerank 中降权,
// 让「越用越准」成立。upsert 键为 (tenant, message_id, article) —— 同一条消息对同一篇文章
// 改判时覆盖, 不产生重复计票。

// SaveArticleFeedback 记录一条「回答引用了文章 X, 用户给了 up/down」。
func (s *pgFeedbackStore) SaveArticleFeedback(ctx context.Context, tenant, messageID, articleID, rating string) error {
	if tenant == "" {
		tenant = "default"
	}
	if articleID == "" || (rating != "up" && rating != "down") {
		return fmt.Errorf("article feedback: invalid article/rating")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_article_feedback (tenant_id, message_id, article_id, rating, created_at, updated_at)
		VALUES ($1,$2,$3,$4,now(),now())
		ON CONFLICT (tenant_id, message_id, article_id) DO UPDATE SET rating=$4, updated_at=now()`,
		tenant, messageID, articleID, rating); err != nil {
		return err
	}
	// 反馈进排序前先失效降权缓存, 否则改动要等 TTL 过期才生效
	s.articleCacheMu.Lock()
	delete(s.articleCache, tenant)
	s.articleCacheMu.Unlock()
	return nil
}

// ArticlePenalties 返回该租户近 90 天每篇文章的「净踩数」(踩-赞, 下限 0)。
// 只看净踩: 一篇被点踩 3 次又点赞 2 次的文章说明有争议而非纯粹无用, 不应重罚。
// 结果按租户缓存(由调用方通过 SaveArticleFeedback 失效), 避免每次检索都聚合全表。
func (s *pgFeedbackStore) ArticlePenalties(ctx context.Context, tenant string) (map[string]int, error) {
	if tenant == "" {
		tenant = "default"
	}
	s.articleCacheMu.Lock()
	if e, ok := s.articleCache[tenant]; ok && time.Since(e.loaded) < 5*time.Minute {
		s.articleCacheMu.Unlock()
		return e.pen, nil
	}
	s.articleCacheMu.Unlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT article_id, GREATEST(
			SUM(CASE WHEN rating='down' THEN 1 ELSE 0 END) - SUM(CASE WHEN rating='up' THEN 1 ELSE 0 END), 0)
		FROM ai_article_feedback
		WHERE tenant_id=$1 AND created_at > now() - interval '90 days'
		GROUP BY article_id`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pen := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		if n > 0 {
			pen[id] = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.articleCacheMu.Lock()
	s.articleCache[tenant] = articlePenaltyEntry{pen: pen, loaded: time.Now()}
	s.articleCacheMu.Unlock()
	return pen, nil
}

// ——— 内存实现(无 DB / 测试) ———

type memFeedbackStore struct {
	mu       sync.Mutex
	data     map[string]*Feedback // key: tenantID + "\x00" + targetKey
	articles map[string]articleVote
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

func (m *memFeedbackStore) ListAll(ctx context.Context, tenantID string) ([]*Feedback, error) {
	return m.List(ctx, tenantID, "")
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

// ——— 内存实现: 文章级反馈 ———

// SaveArticleFeedback 内存实现: key = tenant\0msg\0article, 同键覆盖(与 PG upsert 语义一致)。
func (m *memFeedbackStore) SaveArticleFeedback(ctx context.Context, tenant, messageID, articleID, rating string) error {
	if tenant == "" {
		tenant = "default"
	}
	if articleID == "" || (rating != "up" && rating != "down") {
		return fmt.Errorf("article feedback: invalid article/rating")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.articles == nil {
		m.articles = map[string]articleVote{}
	}
	m.articles[tenant+"\x00"+messageID+"\x00"+articleID] = articleVote{articleID: articleID, rating: rating, at: time.Now()}
	return nil
}

// ArticlePenalties 内存实现: 聚合近 90 天净踩数(不缓存, 内存路径本就为测试/降级)。
func (m *memFeedbackStore) ArticlePenalties(ctx context.Context, tenant string) (map[string]int, error) {
	if tenant == "" {
		tenant = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pen := map[string]int{}
	cutoff := time.Now().Add(-90 * 24 * time.Hour)
	prefix := tenant + "\x00"
	for key, v := range m.articles {
		// 键格式 tenant\x00msg\x00article: 必须按租户前缀过滤, 否则跨租户泄漏
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if v.at.Before(cutoff) {
			continue
		}
		if v.rating == "down" {
			pen[v.articleID]++
		} else {
			if pen[v.articleID] > 0 {
				pen[v.articleID]--
			}
		}
	}
	for id, n := range pen {
		if n <= 0 {
			delete(pen, id)
		}
	}
	return pen, nil
}

type articleVote struct {
	articleID string
	rating    string
	at        time.Time
}
