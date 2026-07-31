package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Embedder 计算文本的向量表示,供 RAG 语义检索使用。
// 由 llm.Client 实现(OpenAI 兼容 /v1/embeddings 端点)。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type pgStore struct {
	db            *sql.DB
	embedder      Embedder
	vectorEnabled bool

	// —— P3-1 增量索引自愈:embedding 失败的文章进入重试队列,后台循环带退避重试 ——
	mu       sync.Mutex
	pending  []reindexTask // 待(重)计算 embedding 的文章队列
	stopCh   chan struct{}  // 关闭后台循环的信号(由 StartReindexLoop 创建)
}

// reindexTask 是待补算 embedding 的文章任务,带重试次数与下次尝试时间(退避)。
type reindexTask struct {
	id          string
	tenantID    string
	attempts    int
	nextAttempt time.Time
}

func newPGStore(db *sql.DB) *pgStore {
	s := &pgStore{db: db}
	s.init(context.Background())
	return s
}

func (s *pgStore) init(ctx context.Context) {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS knowledge_articles (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, title TEXT, content TEXT,
			category TEXT, tags TEXT DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`); err != nil {
		slog.Warn("knowledge pg init failed", "err", err)
		return
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_tenant ON knowledge_articles(tenant_id, category)`)

	// —— 尝试启用 pgvector 语义检索(可选,失败则降级为 ILIKE 关键词匹配) ——
	if _, err := s.db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		slog.Warn("knowledge: pgvector extension unavailable, falling back to ILIKE search", "err", err)
		s.vectorEnabled = false
		return
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS embedding vector`); err != nil {
		slog.Warn("knowledge: add embedding column failed, falling back to ILIKE", "err", err)
		s.vectorEnabled = false
		return
	}
	// HNSW 索引加速余弦检索;创建失败(老版本 pgvector)不致命,仍走全表余弦。
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_embedding ON knowledge_articles USING hnsw (embedding vector_cosine_ops)`)
	s.vectorEnabled = true
	slog.Info("knowledge: pgvector semantic search enabled")
}

// SetEmbedder 注入向量生成器;仅在 pgvector 可用时才会写入 embedding。
func (s *pgStore) SetEmbedder(e Embedder) { s.embedder = e }

// VectorEnabled 报告语义检索是否可用(用于运维/调试)。
func (s *pgStore) VectorEnabled() bool { return s.vectorEnabled }

// embedRaw 实际调用 embedder 计算向量;向量不可用或 embedder 为空时返回 (nil, nil)。
func (s *pgStore) embedRaw(ctx context.Context, a *Article) ([]float32, error) {
	if s.embedder == nil || !s.vectorEnabled {
		return nil, nil
	}
	return s.embedder.Embed(ctx, a.Title+"\n"+a.Content)
}

// embed 供 Create/Update 同步调用:
//   - 成功返回向量;
//   - 向量不可用(无 embedder / pgvector 未启用)返回 nil,上层按 NULL 落库并回退 ILIKE 关键词检索;
//   - embedder 调用出错时把文章加入异步重试队列并返回 nil —— 不再"静默写 NULL 后永久脱离语义检索"(P3-1 修复)。
func (s *pgStore) embed(ctx context.Context, a *Article) []float32 {
	v, err := s.embedRaw(ctx, a)
	if err != nil {
		slog.Warn("knowledge: embed failed, queued for async reindex", "id", a.ID, "err", err)
		s.enqueueRetry(a.ID, a.TenantID)
		return nil
	}
	return v
}

func vecLiteral(v []float32) any {
	if len(v) == 0 {
		return nil
	}
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatFloat(float64(x), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (s *pgStore) Create(ctx context.Context, a *Article) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	if a.TenantID == "" {
		a.TenantID = "default"
	}
	now := time.Now()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	b, _ := json.Marshal(a.Tags)
	emb := s.embed(ctx, a)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO knowledge_articles(id,tenant_id,title,content,category,tags,embedding,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(id) DO NOTHING`,
		a.ID, a.TenantID, a.Title, a.Content, a.Category, b, vecLiteral(emb), a.CreatedAt, a.UpdatedAt)
	return err
}

func (s *pgStore) Get(ctx context.Context, id, tenantID string) (*Article, error) {
	a := &Article{}
	var tags []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,tenant_id,title,content,category,tags,created_at,updated_at FROM knowledge_articles WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(tags, &a.Tags)
	return a, nil
}

func (s *pgStore) List(ctx context.Context, tenantID, category string) ([]*Article, error) {
	q := `SELECT id,tenant_id,title,content,category,tags,created_at,updated_at FROM knowledge_articles WHERE tenant_id=$1`
	args := []any{tenantID}
	if category != "" {
		q += ` AND category=$2`
		args = append(args, category)
	}
	q += ` ORDER BY updated_at DESC LIMIT 200`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Article
	for rows.Next() {
		a := &Article{}
		var tags []byte
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(tags, &a.Tags)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *pgStore) Update(ctx context.Context, a *Article) error {
	a.UpdatedAt = time.Now()
	b, _ := json.Marshal(a.Tags)
	emb := s.embed(ctx, a)
	_, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_articles SET title=$2,content=$3,category=$4,tags=$5,embedding=$6,updated_at=$7 WHERE id=$1 AND tenant_id=$8`,
		a.ID, a.Title, a.Content, a.Category, b, vecLiteral(emb), a.UpdatedAt, a.TenantID)
	return err
}

// IncrementViews 仅递增浏览数,不触碰 embedding(避免每次 GET 都调用 embedding API)。
func (s *pgStore) IncrementViews(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE knowledge_articles SET views = views + 1 WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *pgStore) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_articles WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ReindexAll 为存量文章补算 embedding(知识库开启语义检索后的一次性回填)。
// 仅 vectorEnabled 且有 embedder 时有效;逐条计算便于失败隔离。
func (s *pgStore) ReindexAll(ctx context.Context) (int, error) {
	if !s.vectorEnabled || s.embedder == nil {
		return 0, fmt.Errorf("vector search not available")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,tenant_id,title,content,category,tags,created_at,updated_at FROM knowledge_articles WHERE embedding IS NULL`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		a := &Article{}
		var tags []byte
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return n, err
		}
		_ = json.Unmarshal(tags, &a.Tags)
		emb := s.embed(ctx, a)
		if len(emb) == 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE knowledge_articles SET embedding=$2 WHERE id=$1 AND tenant_id=$3`, a.ID, vecLiteral(emb), a.TenantID); err != nil {
			slog.Warn("knowledge: reindex row failed", "id", a.ID, "err", err)
			continue
		}
		n++
	}
	return n, rows.Err()
}

// ——— P3-1: 增量索引自愈 ———
// 原实现 embedding 失败仅 warn 写 NULL、静默退出语义检索,且除启动时一次性 ReindexAll 外无任何重试。
// 现改为:失败文章进入重试队列,后台循环扫描 embedding IS NULL 行、带指数退避重试,并暴露未索引指标。

// enqueueRetry 将单篇文章加入异步重试队列(线程安全)。空 id 忽略。
func (s *pgStore) enqueueRetry(id, tenantID string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, reindexTask{id: id, tenantID: tenantID, attempts: 0, nextAttempt: time.Now()})
}

// PendingReindexCount 返回当前待重试队列长度(观测/测试用)。
func (s *pgStore) PendingReindexCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// enqueueUnindexed 扫描全表 embedding IS NULL 的行,补充进重试队列(幂等:已入队的不重复添加)。
// 作为"安全网"覆盖循环启动前/队列空窗期已存在的未索引文章。
func (s *pgStore) enqueueUnindexed(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, tenant_id FROM knowledge_articles WHERE embedding IS NULL LIMIT 500`)
	if err != nil {
		slog.Warn("knowledge: scan unindexed failed", "err", err)
		return
	}
	defer rows.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]bool, len(s.pending))
	for _, t := range s.pending {
		seen[t.id] = true
	}
	for rows.Next() {
		var id, tid string
		if err := rows.Scan(&id, &tid); err != nil {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		s.pending = append(s.pending, reindexTask{id: id, tenantID: tid, attempts: 0, nextAttempt: time.Now()})
	}
}

// drainPending 处理重试队列中"已到下次尝试时间"的任务:拉取文章→重新 embed→写回;
// 失败(embed 错误或写回失败)按指数退避(2^attempts*30s,上限 30min)重新入队,避免对 embedding 服务持续施压。
func (s *pgStore) drainPending(ctx context.Context) {
	s.mu.Lock()
	now := time.Now()
	var due, remaining []reindexTask
	for _, t := range s.pending {
		if t.nextAttempt.After(now) {
			remaining = append(remaining, t)
		} else {
			due = append(due, t)
		}
	}
	s.pending = remaining
	s.mu.Unlock()

	for _, t := range due {
		a, err := s.Get(ctx, t.id, t.tenantID)
		if err != nil || a == nil {
			continue // 文章已删除或暂不可读,丢弃该任务
		}
		v, eerr := s.embedRaw(ctx, a)
		if eerr != nil || len(v) == 0 {
			t.attempts++
			t.nextAttempt = time.Now().Add(backoffForAttempt(t.attempts))
			s.mu.Lock()
			s.pending = append(s.pending, t)
			s.mu.Unlock()
			slog.Warn("knowledge: async reindex failed, will retry", "id", t.id, "attempts", t.attempts, "next", t.nextAttempt)
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE knowledge_articles SET embedding=$2 WHERE id=$1 AND tenant_id=$3`, t.id, vecLiteral(v), t.tenantID); err != nil {
			slog.Warn("knowledge: async reindex update failed", "id", t.id, "err", err)
			t.attempts++
			t.nextAttempt = time.Now().Add(30 * time.Second)
			s.mu.Lock()
			s.pending = append(s.pending, t)
			s.mu.Unlock()
			continue
		}
		slog.Info("knowledge: async reindex ok", "id", t.id)
	}
}

// backoffForAttempt 返回第 n 次失败后的退避时长(2^n * 30s,上限 30min)。
func backoffForAttempt(attempts int) time.Duration {
	b := time.Duration(1<<uint(min(attempts, 6))) * 30 * time.Second
	if b > 30*time.Minute {
		b = 30 * time.Minute
	}
	return b
}

// refreshUnindexedMetric 刷新 knowledge_unindexed_articles 指标(当前 embedding IS NULL 的文章数)。
func (s *pgStore) refreshUnindexedMetric(ctx context.Context) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_articles WHERE embedding IS NULL`).Scan(&n); err != nil {
		slog.Warn("knowledge: count unindexed failed", "err", err)
		return
	}
	KnowledgeUnindexedArticles.Set(float64(n))
}

// RunReindexPass 执行一轮增量索引自愈:扫描未索引行入队 → 处理到期任务 → 刷新指标。
// 可经后台循环定时调用,也可在测试中同步调用。
func (s *pgStore) RunReindexPass(ctx context.Context) {
	if !s.vectorEnabled || s.embedder == nil {
		return
	}
	s.enqueueUnindexed(ctx)
	s.drainPending(ctx)
	s.refreshUnindexedMetric(ctx)
}

// StartReindexLoop 启动后台重索引循环,按 interval 周期执行 RunReindexPass。
// 仅在向量检索可用且有 embedder 时生效;否则静默不启动。传入的 ctx 取消即停止循环。
func (s *pgStore) StartReindexLoop(ctx context.Context, interval time.Duration) {
	if !s.vectorEnabled || s.embedder == nil {
		slog.Info("knowledge reindex loop not started (vector search unavailable or no embedder)")
		return
	}
	s.mu.Lock()
	if s.stopCh != nil {
		s.mu.Unlock()
		return // 已启动
	}
	s.stopCh = make(chan struct{})
	stop := s.stopCh
	s.mu.Unlock()

	slog.Info("knowledge reindex loop started", "interval", interval.String())
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		s.RunReindexPass(ctx) // 立即先跑一轮
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				s.RunReindexPass(ctx)
			}
		}
	}()
}

// StopReindexLoop 停止后台重索引循环(幂等)。
func (s *pgStore) StopReindexLoop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopCh != nil {
		close(s.stopCh)
		s.stopCh = nil
	}
}
