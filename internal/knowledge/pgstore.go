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
	mu      sync.Mutex
	pending []reindexTask // 待(重)计算 embedding 的文章队列
	stopCh  chan struct{} // 关闭后台循环的信号(由 StartReindexLoop 创建)
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
	// P1 内容管理: 文档状态(draft/published) + 版本号 + 浏览数(兼容老库补列)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS views INT DEFAULT 0`)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS status TEXT DEFAULT 'draft'`)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS version INT DEFAULT 1`)
	// —— RAG 分块表: 块级向量检索(2026-07-31 P0 增强) ——
	// 文章按 Markdown 标题层级切成块, 每块独立 embedding, 检索命中"段落级"而非整篇。
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS knowledge_chunks (
			id TEXT PRIMARY KEY,
			article_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			chunk_index INT NOT NULL,
			heading TEXT DEFAULT '',
			content TEXT NOT NULL,
			embedding vector,
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("knowledge: create chunks table failed, falling back to ILIKE", "err", err)
		s.vectorEnabled = false
		return
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_article ON knowledge_chunks(article_id)`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_tenant ON knowledge_chunks(tenant_id)`)
	// HNSW 索引加速余弦检索;创建失败(老版本 pgvector)不致命,仍走全表余弦。
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_embedding ON knowledge_chunks USING hnsw (embedding vector_cosine_ops)`)
	// 兼容旧索引(历史文章级向量), 保留不删。
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_embedding ON knowledge_articles USING hnsw (embedding vector_cosine_ops)`)

	// —— P1 内容管理: 分类元数据 + 文章版本快照 ——
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS knowledge_categories (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			sort_order INT DEFAULT 0,
			color TEXT DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			UNIQUE(tenant_id, name)
		)`); err != nil {
		slog.Warn("knowledge: create categories table failed", "err", err)
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_categories_tenant ON knowledge_categories(tenant_id)`)
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS knowledge_article_versions (
			id TEXT PRIMARY KEY,
			article_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			version INT NOT NULL,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			category TEXT DEFAULT '',
			tags TEXT DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL,
			UNIQUE(article_id, version)
		)`); err != nil {
		slog.Warn("knowledge: create versions table failed", "err", err)
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_versions_article ON knowledge_article_versions(article_id, version)`)

	s.vectorEnabled = true
	slog.Info("knowledge: pgvector semantic search enabled (chunk-level)")
}

// SetEmbedder 注入向量生成器;仅在 pgvector 可用时才会写入 embedding。
func (s *pgStore) SetEmbedder(e Embedder) { s.embedder = e }

// VectorEnabled 报告语义检索是否可用(用于运维/调试)。
func (s *pgStore) VectorEnabled() bool { return s.vectorEnabled }

// embedRaw 实际调用 embedder 计算文本向量;向量不可用或 embedder 为空时返回 (nil, nil)。
// 供块级 embedding 使用(每块独立向量)。
func (s *pgStore) embedRaw(ctx context.Context, text string) ([]float32, error) {
	if s.embedder == nil || !s.vectorEnabled {
		return nil, nil
	}
	return s.embedder.Embed(ctx, text)
}

// syncChunks 为文章同步分块: 删旧块 → Markdown 标题感知切块 → 逐块 embedding 落库。
// 返回 (missingEmbedding, err): missingEmbedding=true 表示存在块向量缺失(embed 失败或不可用),
// 调用方可据此决定是否把文章加入异步重试队列(后台补算)。
// 无向量能力时仍落块(支撑关键词检索), embedding 留 NULL。
func (s *pgStore) syncChunks(ctx context.Context, a *Article) (missing bool, err error) {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE article_id=$1 AND tenant_id=$2`, a.ID, a.TenantID); err != nil {
		return false, err
	}
	chunks := chunkMarkdown(a.Content)
	for _, c := range chunks {
		vc := any(nil)
		if s.vectorEnabled && s.embedder != nil {
			v, err := s.embedRaw(ctx, chunkEmbedText(&c))
			if err != nil {
				slog.Warn("knowledge: chunk embed failed", "article", a.ID, "chunk", c.Index, "err", err)
				missing = true
			} else {
				vc = vecLiteral(v)
			}
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO knowledge_chunks(id,article_id,tenant_id,chunk_index,heading,content,embedding,created_at)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING`,
			uuid.New().String(), a.ID, a.TenantID, c.Index, c.Heading, c.Content, vc, time.Now()); err != nil {
			slog.Warn("knowledge: insert chunk failed", "article", a.ID, "chunk", c.Index, "err", err)
		}
	}
	return missing, nil
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
	if a.Status == "" {
		a.Status = "draft"
	}
	if a.Version <= 0 {
		a.Version = 1
	}
	b, _ := json.Marshal(a.Tags)
	// 文章落库(块级向量在 syncChunks 中处理, 不再写 articles.embedding)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO knowledge_articles(id,tenant_id,title,content,category,tags,status,version,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO NOTHING`,
		a.ID, a.TenantID, a.Title, a.Content, a.Category, b, a.Status, a.Version, a.CreatedAt, a.UpdatedAt); err != nil {
		return err
	}
	// 同步分块; 块向量缺失(embed 失败)时入异步重试队列补算
	if missing, _ := s.syncChunks(ctx, a); missing {
		s.enqueueRetry(a.ID, a.TenantID)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id, tenantID string) (*Article, error) {
	a := &Article{}
	var tags []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,tenant_id,title,content,category,tags,views,status,version,created_at,updated_at FROM knowledge_articles WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.Views, &a.Status, &a.Version, &a.CreatedAt, &a.UpdatedAt)
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
	q := `SELECT id,tenant_id,title,content,category,tags,views,status,version,created_at,updated_at FROM knowledge_articles WHERE tenant_id=$1`
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
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.Views, &a.Status, &a.Version, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(tags, &a.Tags)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *pgStore) Update(ctx context.Context, a *Article) error {
	// 取当前版本与内容(用于快照与版本递增)
	var curVersion int
	var curTitle, curContent, curCategory string
	var curTags []byte
	s.db.QueryRowContext(ctx,
		`SELECT version,title,content,category,tags FROM knowledge_articles WHERE id=$1 AND tenant_id=$2`,
		a.ID, a.TenantID).Scan(&curVersion, &curTitle, &curContent, &curCategory, &curTags)
	if curVersion <= 0 {
		curVersion = 1
	}
	// 保存旧内容为历史版本快照(编辑历史可回滚)
	if err := s.saveVersionSnapshot(ctx, a.ID, a.TenantID, curVersion, curTitle, curContent, curCategory, curTags); err != nil {
		slog.Warn("knowledge: save version snapshot failed", "article", a.ID, "err", err)
	}
	a.Version = curVersion + 1
	a.UpdatedAt = time.Now()
	if a.Status == "" {
		a.Status = "draft"
	}
	b, _ := json.Marshal(a.Tags)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_articles SET title=$2,content=$3,category=$4,tags=$5,status=$6,version=$7,updated_at=$8 WHERE id=$1 AND tenant_id=$9`,
		a.ID, a.Title, a.Content, a.Category, b, a.Status, a.Version, a.UpdatedAt, a.TenantID); err != nil {
		return err
	}
	// 同步分块; 块向量缺失时入异步重试队列补算
	if missing, _ := s.syncChunks(ctx, a); missing {
		s.enqueueRetry(a.ID, a.TenantID)
	}
	return nil
}

// saveVersionSnapshot 把一版内容写入历史版本表。
func (s *pgStore) saveVersionSnapshot(ctx context.Context, articleID, tenantID string, version int, title, content, category string, tags []byte) error {
	if version <= 0 || content == "" {
		return nil
	}
	if len(tags) == 0 {
		tags = []byte("[]")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO knowledge_article_versions(id,article_id,tenant_id,version,title,content,category,tags,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(article_id, version) DO NOTHING`,
		uuid.New().String(), articleID, tenantID, version, title, content, category, string(tags), time.Now())
	return err
}

// IncrementViews 仅递增浏览数,不触碰 embedding(避免每次 GET 都调用 embedding API)。
func (s *pgStore) IncrementViews(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE knowledge_articles SET views = views + 1 WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *pgStore) Delete(ctx context.Context, id, tenantID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_articles WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE article_id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ReindexAll 为存量文章补建分块索引(知识库升级到块级 RAG 后的一次性回填)。
// 仅 vectorEnabled 时有意义;逐条处理便于失败隔离。返回处理的文章数。
func (s *pgStore) ReindexAll(ctx context.Context) (int, error) {
	if !s.vectorEnabled {
		return 0, fmt.Errorf("vector search not available")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.tenant_id, a.title, a.content, a.category, a.tags, a.created_at, a.updated_at
		FROM knowledge_articles a
		LEFT JOIN knowledge_chunks c ON c.article_id = a.id AND c.tenant_id = a.tenant_id
		WHERE c.id IS NULL`)
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
		if _, err := s.syncChunks(ctx, a); err != nil {
			slog.Warn("knowledge: reindex chunks failed", "id", a.ID, "err", err)
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

// enqueueUnindexed 扫描无分块的文章,补充进重试队列(幂等:已入队的不重复添加)。
// 作为"安全网"覆盖循环启动前/队列空窗期已存在的未分块文章。
func (s *pgStore) enqueueUnindexed(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.tenant_id FROM knowledge_articles a
		LEFT JOIN knowledge_chunks c ON c.article_id = a.id AND c.tenant_id = a.tenant_id
		WHERE c.id IS NULL LIMIT 500`)
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

// drainPending 处理重试队列中"已到下次尝试时间"的任务:拉取文章→重新分块并 embedding;
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
		missing, err := s.syncChunks(ctx, a)
		if err != nil || missing {
			t.attempts++
			t.nextAttempt = now.Add(backoffForAttempt(t.attempts))
			s.mu.Lock()
			s.pending = append(s.pending, t)
			s.mu.Unlock()
			slog.Warn("knowledge: async reindex failed, will retry", "id", t.id, "attempts", t.attempts, "err", err)
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

// refreshUnindexedMetric 刷新 knowledge_unindexed_articles 指标(当前无分块的文章数)。
func (s *pgStore) refreshUnindexedMetric(ctx context.Context) {
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM knowledge_articles a
		LEFT JOIN knowledge_chunks c ON c.article_id = a.id AND c.tenant_id = a.tenant_id
		WHERE c.id IS NULL`).Scan(&n); err != nil {
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

// ——— P1 内容管理: 分类元数据 ———

func (s *pgStore) ListCategories(ctx context.Context, tenantID string) ([]*Category, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,description,sort_order,color,created_at FROM knowledge_categories
		 WHERE tenant_id=$1 ORDER BY sort_order, name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Category
	for rows.Next() {
		c := &Category{}
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.Description, &c.SortOrder, &c.Color, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []*Category{}
	}
	return out, rows.Err()
}

func (s *pgStore) CreateCategory(ctx context.Context, c *Category) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	if c.TenantID == "" {
		c.TenantID = "default"
	}
	c.CreatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO knowledge_categories(id,tenant_id,name,description,sort_order,color,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,name) DO UPDATE SET description=EXCLUDED.description,sort_order=EXCLUDED.sort_order,color=EXCLUDED.color`,
		c.ID, c.TenantID, c.Name, c.Description, c.SortOrder, c.Color, c.CreatedAt)
	return err
}

func (s *pgStore) UpdateCategory(ctx context.Context, c *Category) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_categories SET name=$2,description=$3,sort_order=$4,color=$5 WHERE id=$1 AND tenant_id=$6`,
		c.ID, c.Name, c.Description, c.SortOrder, c.Color, c.TenantID)
	return err
}

func (s *pgStore) DeleteCategory(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_categories WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// ——— P1 内容管理: 文章版本 ———

func (s *pgStore) ListVersions(ctx context.Context, articleID, tenantID string) ([]*ArticleVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,article_id,tenant_id,version,title,content,category,tags,created_at
		 FROM knowledge_article_versions WHERE article_id=$1 AND tenant_id=$2 ORDER BY version DESC`,
		articleID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ArticleVersion
	for rows.Next() {
		v := &ArticleVersion{}
		var tags string
		if err := rows.Scan(&v.ID, &v.ArticleID, &v.TenantID, &v.Version, &v.Title, &v.Content, &v.Category, &tags, &v.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &v.Tags)
		out = append(out, v)
	}
	if out == nil {
		out = []*ArticleVersion{}
	}
	return out, rows.Err()
}

// RollbackToVersion 回滚文章到指定历史版本: 当前内容先存为新快照, 再以目标版本内容覆盖文章, 版本号+1。
func (s *pgStore) RollbackToVersion(ctx context.Context, articleID, tenantID string, version int) error {
	cur, err := s.Get(ctx, articleID, tenantID)
	if err != nil || cur == nil {
		return fmt.Errorf("article not found")
	}
	// 目标版本内容
	var v ArticleVersion
	var tags string
	err = s.db.QueryRowContext(ctx,
		`SELECT id,article_id,tenant_id,version,title,content,category,tags FROM knowledge_article_versions
		 WHERE article_id=$1 AND tenant_id=$2 AND version=$3`,
		articleID, tenantID, version).
		Scan(&v.ID, &v.ArticleID, &v.TenantID, &v.Version, &v.Title, &v.Content, &v.Category, &tags)
	if err != nil {
		return fmt.Errorf("version %d not found", version)
	}
	_ = json.Unmarshal([]byte(tags), &v.Tags)
	// 当前内容入历史
	cb, _ := json.Marshal(cur.Tags)
	_ = s.saveVersionSnapshot(ctx, articleID, tenantID, cur.Version, cur.Title, cur.Content, cur.Category, cb)
	// 覆盖文章
	cur.Title = v.Title
	cur.Content = v.Content
	cur.Category = v.Category
	cur.Tags = v.Tags
	cur.Version++
	cur.UpdatedAt = time.Now()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_articles SET title=$2,content=$3,category=$4,tags=$5,version=$6,updated_at=$7 WHERE id=$1 AND tenant_id=$8`,
		articleID, cur.Title, cur.Content, cur.Category, string(cb), cur.Version, cur.UpdatedAt, tenantID); err != nil {
		return err
	}
	// 回滚后重建分块索引
	return s.reindexArticle(ctx, cur)
}

// reindexArticle 重建单篇文章的分块(供回滚后调用, 不改变版本号)。
func (s *pgStore) reindexArticle(ctx context.Context, a *Article) error {
	_, err := s.syncChunks(ctx, a)
	return err
}
