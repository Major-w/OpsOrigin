package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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

// embed 计算文章向量;向量不可用时返回 nil(上层按 NULL 落库,检索回退 ILIKE)。
func (s *pgStore) embed(ctx context.Context, a *Article) []float32 {
	if s.embedder == nil || !s.vectorEnabled {
		return nil
	}
	v, err := s.embedder.Embed(ctx, a.Title+"\n"+a.Content)
	if err != nil {
		slog.Warn("knowledge: embed failed", "id", a.ID, "err", err)
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
