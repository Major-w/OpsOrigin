package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Embedder 计算文本的向量表示,供 RAG 语义检索使用。
// 由 llm.Client 实现(OpenAI 兼容 /v1/embeddings 端点)。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// BatchEmbedder 是 Embedder 的可选扩展: 一次请求为多条文本生成向量。
// llm.Client 已实现(OpenAI 兼容 /v1/embeddings 的 input 数组形态)。
// 未实现时 embedRawBatch 自动回落到逐条 Embed, 行为不变。
type BatchEmbedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

type pgStore struct {
	db            *sql.DB
	embedder      Embedder
	vectorEnabled bool
	// 全文检索能力开关: tsvector(英文词元) 与 pg_trgm(中文 ILIKE 索引扫描), 建索引失败时各自降级。
	tsvEnabled  bool
	trgmEnabled bool
	// halfvecEnabled: pgvector 是否支持 halfvec(0.7+), 决定 HNSW 索引用半精度表达式还是原生列。
	halfvecEnabled bool
	// idxDims: 各表已建 HNSW 索引的维度。换 embedding 模型后维度变化时据此重建,
	// 否则索引表达式与检索表达式不匹配, 检索静默退化为全表余弦。
	idxDims map[string]int

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

	// P1 内容管理: 文档状态(draft/published) + 版本号 + 浏览数(兼容老库补列)。
	// 注意: 必须在 pgvector 分支之前执行 —— pgvector 不可用时 init 会提前 return,
	// 若补列迁移放在其后将被跳过, 导致 INSERT 带 status/views/version 列时报 42703。
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS views INT DEFAULT 0`)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS status TEXT DEFAULT 'draft'`)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS version INT DEFAULT 1`)
	// 知识有效期 + 查看/编辑权限(兼容老库补列)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS view_permission TEXT DEFAULT 'all'`)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS edit_permission TEXT DEFAULT 'owner'`)
	// —— 可见性归属列(权限感知检索的前置数据) ——
	// view_permission 决定"怎么判定", owner_group_ids 决定"判定给谁":
	//   owner → author_id 命中提问者; team → owner_group_ids 与提问者所属团队有交集。
	// owner_group_ids 用 NOT NULL DEFAULT '{}' 把 NULL 与空串两种"空态"在 schema 层消灭
	// (view_permission 的空串缺陷就是双态导致的静默失效)。PG 11+ 对已存在表加带默认值的
	// NOT NULL 列是 metadata-only, 不重写表。
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS author_id TEXT DEFAULT ''`)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS owner_group_ids TEXT[] NOT NULL DEFAULT '{}'::text[]`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_articles_scope ON knowledge_articles(tenant_id, view_permission)`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_articles_owner_groups ON knowledge_articles USING gin(owner_group_ids)`)
	// 双态兜底(与 deploy/migrations/027_knowledge_visibility.sql 等价): 上面的 ADD COLUMN IF NOT EXISTS
	// 在"该列已被以可空形态建出"时会整句跳过, NOT NULL 定义随之失效。先把 NULL 行归一为空数组,
	// 避免数组重叠谓词遇到 NULL 行时语义不可预期。无 NULL 行时为空更新。
	s.db.ExecContext(ctx, `UPDATE knowledge_articles SET owner_group_ids = '{}'::text[] WHERE owner_group_ids IS NULL`)
	s.db.ExecContext(ctx, `UPDATE knowledge_articles SET author_id = '' WHERE author_id IS NULL`)

	// —— P1-2/P1-4 检索可观测: 每次 AI 检索留痕(零结果查询 = 知识盲区清单) ——
	// 必须放在 pgvector 分支之前: 该分支不可用时 init 会提前 return, 放其后会导致表缺失、
	// 留痕写入报 42P01(与上面补列的注意事项同因)。
	// categories 存逗号分隔的分类绑定(Agent 知识库范围), 便于回溯"某次检索到底在哪个知识范围内跑空"。
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS knowledge_search_logs (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			query TEXT NOT NULL,
			mode TEXT NOT NULL DEFAULT 'rrf',
			categories TEXT DEFAULT '',
			vec_hits INT NOT NULL DEFAULT 0,
			kw_hits INT NOT NULL DEFAULT 0,
			hit_count INT NOT NULL DEFAULT 0,
			zero_result BOOLEAN NOT NULL DEFAULT false,
			latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
			top_score DOUBLE PRECISION NOT NULL DEFAULT 0,
			agent_id TEXT DEFAULT '',
			user_id TEXT DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("knowledge: create search logs table failed", "err", err)
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_search_logs_time ON knowledge_search_logs(tenant_id, created_at DESC)`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_search_logs_zero ON knowledge_search_logs(tenant_id, zero_result, created_at DESC)`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_search_logs_query ON knowledge_search_logs(tenant_id, query)`)

	// —— P3-3 评审轨迹: 状态流转留痕(谁在何时把文章从哪个状态改到哪个状态、驳回原因) ——
	// 同样放在 pgvector 分支之前(该分支不可用时 init 会提前 return)。
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS knowledge_article_reviews (
			id TEXT PRIMARY KEY,
			article_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			from_status TEXT NOT NULL,
			to_status TEXT NOT NULL,
			note TEXT DEFAULT '',
			actor TEXT DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("knowledge: create article reviews table failed", "err", err)
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_reviews_article ON knowledge_article_reviews(article_id, tenant_id, created_at DESC)`)

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
	// —— 附件解析入库: 块可来自文章正文或文章下的附件(兼容老库补列) ——
	// source: article|attachment; attachment_id: 来源附件(BIGSERIAL), 文章正文块为 NULL。
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS source TEXT DEFAULT 'article'`)
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS attachment_id BIGINT`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_attachment ON knowledge_chunks(attachment_id)`)
	// —— P1-2 向量维度标记: 换 embedding 模型后旧维度向量自动失效, 避免污染检索结果 ——
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS embed_dim INT`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_dim ON knowledge_chunks(embed_dim)`)
	// —— P0-1 全文检索: tsvector 列 + GIN 索引(英文/带空格词元) 与 pg_trgm(中文 ILIKE 加速) ——
	// 此前关键词路是裸 ILIKE '%q%': 前导通配符走不了 B-tree, 只能全表扫描。
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS content_tsv tsvector`)
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_tsv ON knowledge_chunks USING gin(content_tsv)`); err != nil {
		slog.Warn("knowledge: chunks tsvector GIN index unavailable", "err", err)
	} else {
		s.tsvEnabled = true
	}
	// pg_trgm 让 ILIKE '%中文%' 能走 GIN 索引扫描(中文 trigram 覆盖有限, 但对 3 字以上词语有效)。
	if _, err := s.db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm`); err != nil {
		slog.Warn("knowledge: pg_trgm unavailable, ILIKE stays sequential scan", "err", err)
	} else {
		if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_trgm ON knowledge_chunks USING gin(content gin_trgm_ops)`); err != nil {
			slog.Warn("knowledge: chunks trigram GIN index failed", "err", err)
		} else {
			s.trgmEnabled = true
		}
	}
	// 存量回填: 建列之前入库的块 content_tsv 为 NULL, 不回填会检索不到
	if res, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_chunks SET content_tsv = to_tsvector('simple', coalesce(heading,'') || ' ' || content)
		 WHERE content_tsv IS NULL`); err != nil {
		slog.Warn("knowledge: backfill content_tsv failed", "err", err)
	} else if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("knowledge: backfilled content_tsv for existing chunks", "rows", n)
	}
	// 存量回填: embed_dim 列晚于 embedding 列引入, 老数据为 NULL 会被维度过滤(embed_dim=$n)漏掉。
	if res, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_chunks SET embed_dim = vector_dims(embedding)
		 WHERE embedding IS NOT NULL AND embed_dim IS NULL`); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			slog.Info("knowledge: backfilled embed_dim for existing chunks", "rows", n)
		}
	}
	// 存量清理: 早期切分器会给空/纯空白正文补「内容为空」的占位块。这类块没有信息量,
	// 却会被送去 embedding 并作为命中占据 top-k 名额。切分器已改为空内容返回 0 块,
	// 这里顺手清掉历史遗留(幂等: 清完再跑影响 0 行), 免去存量环境手动跑迁移。
	if res, err := s.db.ExecContext(ctx,
		`DELETE FROM knowledge_chunks WHERE btrim(coalesce(content, '')) = ''`); err != nil {
		slog.Warn("knowledge: cleanup blank chunks failed", "err", err)
	} else if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("knowledge: removed blank placeholder chunks", "rows", n)
	}
	// 存量回填: view_permission 的语义是 all(默认), 但写入路径历史上一律原样落库,
	// 于是大量「已发布」文章的该字段是空串。检索侧对可见性是 fail-closed 判定
	// (只有公开可召回), 空串会被当成"受限"→ 这批文章静默变成 AI 不可见。
	// 归一为显式 'all', 使库内取值不再依赖读取侧兜底(幂等: 补完再跑影响 0 行)。
	if res, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_articles SET view_permission = 'all'
		 WHERE COALESCE(view_permission, '') = ''`); err != nil {
		slog.Warn("knowledge: backfill view_permission failed", "err", err)
	} else if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("knowledge: backfilled view_permission=all for blank articles", "rows", n)
	}
	// —— P2-2 内容哈希: 编辑文章时只有变化的块需要重新 embedding ——
	// 此前 syncChunks 一律「删光旧块 → 全文重新 embedding」, 改一个小节也要为整篇付费;
	// ReindexAll(force) 同样是纯全量重算。记录块内容哈希后, 未变化的块可直接复用旧向量。
	// 换 embedding 模型时必须绕过复用(force=true), 否则旧维度向量会被原样搬回来。
	s.db.ExecContext(ctx, `ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS content_hash TEXT`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_hash ON knowledge_chunks(article_id, tenant_id, content_hash)`)
	// HNSW 索引加速余弦检索。此前 embedding 是裸 vector 列, 原生 HNSW 因"无维度"与
	// "2000 维上限"两类错误从未建成, 检索一直全表余弦; 改为 halfvec 表达式索引(见该函数注释)。
	s.halfvecEnabled = s.probeHalfvec(ctx)
	s.ensureHNSWIndex(ctx, "knowledge_chunks")
	// 兼容旧结构(历史文章级向量), 保留不删。
	s.ensureHNSWIndex(ctx, "knowledge_articles")

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

	// —— 文章附件: 元数据在库, 二进制在对象存储(object_key)或回退 data(bytea) ——
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS kb_attachments (
			id BIGSERIAL PRIMARY KEY,
			article_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			file_size BIGINT NOT NULL,
			file_type TEXT NOT NULL,
			uploaded_by TEXT NOT NULL,
			object_key TEXT,
			data BYTEA,
			created_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("knowledge: create kb_attachments table failed", "err", err)
	}
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_kb_attachments_article ON kb_attachments(article_id, tenant_id)`)
	// —— 附件解析入库状态: indexed=是否抽文本切片完成, index_error=失败原因, chunk_count=产出块数 ——
	s.db.ExecContext(ctx, `ALTER TABLE kb_attachments ADD COLUMN IF NOT EXISTS indexed BOOLEAN DEFAULT false`)
	s.db.ExecContext(ctx, `ALTER TABLE kb_attachments ADD COLUMN IF NOT EXISTS index_error TEXT`)
	s.db.ExecContext(ctx, `ALTER TABLE kb_attachments ADD COLUMN IF NOT EXISTS chunk_count INT DEFAULT 0`)

	s.vectorEnabled = true
	slog.Info("knowledge: pgvector semantic search enabled (chunk-level)")
}

// ensureHNSWIndex 确保向量表上有可用的 HNSW 索引, 返回已建索引的维度(0 = 未建成)。
//
// 两道现实约束决定了这里必须走"半精度表达式索引", 而不是教科书式的原生写法:
//  1. 原生 `USING hnsw (embedding vector_cosine_ops)` 要求列有确定维度, 而 embedding 列
//     是裸 vector(允许混存维度) —— 直接建索引必报 "column does not have dimensions";
//  2. 即便先把列固化成 vector(D), 原生 HNSW 还有 2000 维硬上限, 而智谱 embedding-3
//     是 2048 维 —— 报 "column cannot have more than 2000 dimensions for hnsw index"。
//
// 此前这两类错误都只被 slog.Warn 吞掉, 于是索引从未真正生效, 向量检索一直退化为
// 全表余弦扫描(块数上千后延迟显著上升)。改用 `USING hnsw ((embedding::halfvec(D))
// halfvec_cosine_ops)` 后: halfvec 上限 4000 维、索引体积减半, 且与检索侧
// vectorOrderExpr 的表达式逐字一致, 能真正命中索引。
//
// 维度取自库中已有向量的实际维度(embed_dim, 有 B-tree 索引, 探测代价低)。
// 换 embedding 模型导致维度变化时按新维度重建索引, 避免索引与查询表达式不匹配而静默退化。
// 库为空时返回 0, 由后台自愈循环在首批向量入库后补建。table 只接受白名单内的表名。
func (s *pgStore) ensureHNSWIndex(ctx context.Context, table string) int {
	if !s.vectorEnabled {
		return 0
	}
	switch table { // 白名单: 仅这两张已知表带 embedding 列
	case "knowledge_chunks", "knowledge_articles":
	default:
		return 0
	}
	dim, ok := s.detectVectorDim(ctx, table)
	if !ok {
		return 0
	}
	if s.indexDimOf(table) == dim {
		return dim // 已按该维度建好, 无需重复 DDL
	}

	halfIdx := "idx_" + table + "_embedding_half"
	rawIdx := "idx_" + table + "_embedding"
	// 维度变化或首次建索引: 先清掉可能存在的旧索引, 避免留下与查询表达式不匹配的僵尸索引
	// (索引名与表名均为内部常量, 不接受外部输入)。
	s.db.ExecContext(ctx, `DROP INDEX IF EXISTS `+halfIdx)
	s.db.ExecContext(ctx, `DROP INDEX IF EXISTS `+rawIdx)

	if s.halfvecEnabled {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
			`CREATE INDEX %s ON %s USING hnsw ((embedding::halfvec(%d)) halfvec_cosine_ops)`,
			halfIdx, table, dim)); err != nil {
			slog.Warn("knowledge: halfvec HNSW index unavailable (向量检索走全表余弦)", "table", table, "dim", dim, "err", err)
			return 0
		}
		s.setIndexDim(table, dim)
		slog.Info("knowledge: halfvec HNSW index ready", "table", table, "dim", dim)
		return dim
	}

	// pgvector < 0.7 无 halfvec: 回退"固化维度 + 原生索引"(受 2000 维上限约束)
	if dim > 2000 {
		slog.Warn("knowledge: embedding dim exceeds 2000 and halfvec unavailable, 向量检索走全表余弦",
			"table", table, "dim", dim, "hint", "升级 pgvector 到 0.7+")
		return 0
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
		`ALTER TABLE %s ALTER COLUMN embedding TYPE vector(%d)`, table, dim)); err != nil {
		slog.Warn("knowledge: pin embedding dimension failed", "table", table, "dim", dim, "err", err)
		return 0
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
		`CREATE INDEX %s ON %s USING hnsw (embedding vector_cosine_ops)`, rawIdx, table)); err != nil {
		slog.Warn("knowledge: HNSW index unavailable (向量检索走全表余弦)", "table", table, "err", err)
		return 0
	}
	s.setIndexDim(table, dim)
	slog.Info("knowledge: native HNSW index ready", "table", table, "dim", dim)
	return dim
}

// probeHalfvec 探测 pgvector 是否支持 halfvec 类型(0.7+), 决定索引与检索表达式的形态。
func (s *pgStore) probeHalfvec(ctx context.Context) bool {
	var ok bool
	if err := s.db.QueryRowContext(ctx, `SELECT '[0]'::halfvec(1) IS NOT NULL`).Scan(&ok); err != nil {
		slog.Info("knowledge: halfvec unsupported (pgvector < 0.7), 索引回退原生 vector", "err", err)
		return false
	}
	return ok
}

// indexDimOf 返回某张表已建 HNSW 索引的维度(0 = 未建)。
func (s *pgStore) indexDimOf(table string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idxDims[table]
}

// setIndexDim 记录某张表已建 HNSW 索引的维度。
func (s *pgStore) setIndexDim(table string, dim int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idxDims == nil {
		s.idxDims = map[string]int{}
	}
	s.idxDims[table] = dim
}

// detectVectorDim 探测表内已有向量的维度; 维度唯一时返回 (dim, true)。
// 走 embed_dim(有索引)而非 vector_dims(embedding): 后者需全表扫描, 后台循环每轮调用会拖慢。
func (s *pgStore) detectVectorDim(ctx context.Context, table string) (int, bool) {
	var variants, dim sql.NullInt64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT count(DISTINCT embed_dim), min(embed_dim) FROM %s WHERE embedding IS NOT NULL`, table)).
		Scan(&variants, &dim); err != nil {
		return 0, false
	}
	if !variants.Valid || variants.Int64 == 0 || !dim.Valid || dim.Int64 <= 0 {
		// 库为空(或维度全空): 无从推断维度, 留给后台循环在数据入库后建
		return 0, false
	}
	if variants.Int64 > 1 {
		slog.Warn("knowledge: mixed embedding dimensions detected, HNSW index skipped (需重建向量)",
			"table", table, "variants", variants.Int64)
		return 0, false
	}
	return int(dim.Int64), true
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

// syncChunks 为文章同步分块: 取旧块向量缓存 → 删旧块 → Markdown 标题感知切块 → 分块 embedding 落库。
// 返回 (missingEmbedding, err): missingEmbedding=true 表示存在块向量缺失(embed 失败或不可用),
// 调用方可据此决定是否把文章加入异步重试队列(后台补算)。
// 无向量能力时仍落块(支撑关键词检索), embedding 留 NULL。
// 默认开启「内容哈希复用」: 未变化的块直接沿用旧向量, 只对新增/改动的块调用 embedding。
func (s *pgStore) syncChunks(ctx context.Context, a *Article) (missing bool, err error) {
	return s.syncChunksWithReuse(ctx, a, true)
}

// syncChunksWithReuse 是 syncChunks 的可控变体。
// reuse=false 时禁用内容哈希复用并重建全部向量 —— 换 embedding 模型后必须如此,
// 否则旧维度向量会被原样搬回, 检索端按维度过滤时表现为"文章存在但检索不到"。
func (s *pgStore) syncChunksWithReuse(ctx context.Context, a *Article, reuse bool) (missing bool, err error) {
	// 只清理「正文块」: 附件块(attachment_id 非空)由 syncAttachmentChunks 单独维护,
	// 否则编辑一次正文就会把附件抽取出来的知识块连带删掉。
	// 复用缓存必须在删除之前读取 —— 删完就没有旧的向量可比对了。
	var cached map[string]cachedVector
	if reuse {
		cached = s.loadReusableVectors(ctx, a.ID, a.TenantID)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM knowledge_chunks WHERE article_id=$1 AND tenant_id=$2 AND attachment_id IS NULL`, a.ID, a.TenantID); err != nil {
		return false, err
	}
	chunks, truncated := chunkMarkdown(a.Content)
	if truncated {
		// 静默丢内容会导致"文档里明明写了却检索不到", 必须留痕。
		slog.Warn("knowledge: article exceeded chunk limit, tail content dropped",
			"article", a.ID, "limit", chunkMaxBlocks)
	}
	// 空正文/纯空白正文切不出块: 旧块已在上面删掉, 这里直接返回, 避免落占位空块。
	if len(chunks) == 0 {
		return false, nil
	}
	return s.insertChunks(ctx, a.TenantID, a.ID, 0, "article", "", chunks, cached), nil
}

// cachedVector 是可复用的旧向量: 已序列化好的 vector 字面量 + 维度。
type cachedVector struct {
	literal string
	dim     int
}

// loadReusableVectors 读取文章现有正文块的内容哈希 → 向量映射, 供 syncChunks 复用。
// 只取「哈希非空 + 向量非空 + 维度已知」的块; 查不到时返回 nil(等价于不复用)。
func (s *pgStore) loadReusableVectors(ctx context.Context, articleID, tenantID string) map[string]cachedVector {
	if !s.vectorEnabled || s.embedder == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT content_hash, embedding::text, embed_dim
		 FROM knowledge_chunks
		 WHERE article_id=$1 AND tenant_id=$2 AND attachment_id IS NULL
		   AND content_hash IS NOT NULL AND embedding IS NOT NULL AND embed_dim IS NOT NULL`,
		articleID, tenantID)
	if err != nil {
		slog.Warn("knowledge: load reusable vectors failed", "article", articleID, "err", err)
		return nil
	}
	defer rows.Close()
	out := make(map[string]cachedVector)
	for rows.Next() {
		var hash, lit string
		var dim int
		if err := rows.Scan(&hash, &lit, &dim); err != nil {
			continue
		}
		out[hash] = cachedVector{literal: lit, dim: dim}
	}
	return out
}

// chunkContentHash 计算块的可复用性指纹: 嵌入文本(标题路径 + 正文)的 SHA-256。
// 用嵌入文本而非纯 content, 因为标题路径也参与向量计算, 标题变化同样需要重算。
func chunkContentHash(c *Chunk) string {
	sum := sha256.Sum256([]byte(chunkEmbedText(c)))
	return hex.EncodeToString(sum[:])
}

// embedRawBatch 批量计算向量。BatchEmbedder 可用时一次请求, 否则逐条调用 Embed。
// 返回的切片与 texts 等长; 单条失败时该位置为 nil(不中断整批)。
func (s *pgStore) embedRawBatch(ctx context.Context, texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	if len(texts) == 0 || !s.vectorEnabled || s.embedder == nil {
		return out
	}
	if be, ok := s.embedder.(BatchEmbedder); ok {
		vecs, err := be.EmbedBatch(ctx, texts)
		if err == nil && len(vecs) == len(texts) {
			return vecs
		}
		// 批量失败或返回条数不匹配(部分上游实现会丢条目) → 回落逐条, 保证不整批丢失。
		if err != nil {
			slog.Warn("knowledge: batch embed failed, falling back to per-item", "count", len(texts), "err", err)
		}
	}
	for i, t := range texts {
		v, err := s.embedRaw(ctx, t)
		if err != nil {
			continue
		}
		out[i] = v
	}
	return out
}

// insertChunks 批量写入块并计算 embedding。
// attID=0 表示文章正文块; attID>0 表示为某附件抽取的块(heading 前缀带上附件名便于溯源)。
// cached 非空时按内容哈希复用旧向量: 命中则完全不调用 embedding 接口, 只对新增/改动的块批量计算。
func (s *pgStore) insertChunks(ctx context.Context, tenantID, articleID string, attID int64, source, headingPrefix string, chunks []Chunk, cached map[string]cachedVector) (missing bool) {
	// —— 1) 预处理: 过滤空块、拼接 heading 前缀、计算内容哈希 ——
	type prepared struct {
		chunk   Chunk
		heading string
		hash    string
		// 复用的旧向量(命中时非空)
		reused *cachedVector
	}
	items := make([]prepared, 0, len(chunks))
	for i := range chunks {
		c := chunks[i]
		// 防御: 空内容块永远不该入库(既浪费一次 embedding, 又会占 top-k 名额挤掉有效片段)。
		// 切分器已保证不产出空块, 这里再兜一层, 避免未来新增调用方绕过该约定。
		if strings.TrimSpace(c.Content) == "" {
			continue
		}
		heading := c.Heading
		if headingPrefix != "" && heading != headingPrefix {
			if heading != "" {
				heading = headingPrefix + " › " + heading
			} else {
				heading = headingPrefix
			}
		}
		p := prepared{chunk: c, heading: heading, hash: chunkContentHash(&c)}
		if cv, ok := cached[p.hash]; ok && cv.literal != "" {
			hit := cv
			p.reused = &hit
		}
		items = append(items, p)
	}

	// —— 2) 收集需要新算向量的块, 批量 embedding ——
	var needIdx []int
	var needTexts []string
	if s.vectorEnabled && s.embedder != nil {
		for i := range items {
			if items[i].reused != nil {
				continue
			}
			needIdx = append(needIdx, i)
			needTexts = append(needTexts, chunkEmbedText(&items[i].chunk))
		}
	}
	vecs := s.embedRawBatch(ctx, needTexts)
	// vecOf: 块下标 → 新算出的向量(仅成功项); 未命中的块保持 nil, embedding 留 NULL 等待重试。
	vecOf := make(map[int][]float32, len(needIdx))
	for n, i := range needIdx {
		if n < len(vecs) && len(vecs[n]) > 0 {
			vecOf[i] = vecs[n]
			continue
		}
		slog.Warn("knowledge: chunk embed failed", "article", articleID, "chunk", items[i].chunk.Index)
		missing = true
	}
	if len(needIdx) > 0 {
		KnowledgeEmbedReusedChunks.Add(float64(len(items) - len(needIdx)))
		KnowledgeEmbedComputedChunks.Add(float64(len(needIdx)))
	}

	// —— 3) 落库 ——
	attKey := any(nil)
	if attID > 0 {
		attKey = attID
	}
	for i := range items {
		it := &items[i]
		var vc, dim any
		if it.reused != nil {
			vc = it.reused.literal
			dim = it.reused.dim
		} else if v, ok := vecOf[i]; ok {
			vc = vecLiteral(v)
			// 记录向量维度: 换 embedding 模型后旧维度块会被检索自动过滤, 不会与查询向量混算。
			dim = len(v)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO knowledge_chunks(id,article_id,tenant_id,chunk_index,heading,content,embedding,created_at,source,attachment_id,embed_dim,content_tsv,content_hash)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,to_tsvector('simple', $5 || ' ' || $6),$12) ON CONFLICT(id) DO NOTHING`,
			uuid.New().String(), articleID, tenantID, it.chunk.Index, it.heading, it.chunk.Content, vc, time.Now(), source, attKey, dim, it.hash); err != nil {
			slog.Warn("knowledge: insert chunk failed", "article", articleID, "chunk", it.chunk.Index, "err", err)
		}
	}
	return missing
}

// chunkIndexResult 是切块入库的结果: 块数 / 是否有块缺失 embedding / 是否触发块数上限被截断。
type chunkIndexResult struct {
	count     int
	missing   bool
	truncated bool
}

// syncAttachmentChunks 为一个附件重建知识块: 删旧块 → 切块 → 逐块 embedding。
// 与正文块同表同结构, 因此复用现有检索 SQL(按 article_id 聚合)即可被 AI 召回。
func (s *pgStore) syncAttachmentChunks(ctx context.Context, tenantID, articleID string, attID int64, title string, content string) (chunkIndexResult, error) {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM knowledge_chunks WHERE tenant_id=$1 AND attachment_id=$2`, tenantID, attID); err != nil {
		return chunkIndexResult{}, err
	}
	chunks, truncated := chunkMarkdown(content)
	if len(chunks) == 0 {
		return chunkIndexResult{}, nil
	}
	// 附件块同样按内容哈希复用: 重复导入同一份附件(或重试)不会重复付费。
	cached := s.loadReusableVectorsForAttachment(ctx, tenantID, attID)
	missing := s.insertChunks(ctx, tenantID, articleID, attID, "attachment", title, chunks, cached)
	return chunkIndexResult{count: len(chunks), missing: missing, truncated: truncated}, nil
}

// loadReusableVectorsForAttachment 读取某附件现有块的内容哈希 → 向量映射。
func (s *pgStore) loadReusableVectorsForAttachment(ctx context.Context, tenantID string, attID int64) map[string]cachedVector {
	if !s.vectorEnabled || s.embedder == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT content_hash, embedding::text, embed_dim
		 FROM knowledge_chunks
		 WHERE tenant_id=$1 AND attachment_id=$2
		   AND content_hash IS NOT NULL AND embedding IS NOT NULL AND embed_dim IS NOT NULL`,
		tenantID, attID)
	if err != nil {
		slog.Warn("knowledge: load reusable attachment vectors failed", "att", attID, "err", err)
		return nil
	}
	defer rows.Close()
	out := make(map[string]cachedVector)
	for rows.Next() {
		var hash, lit string
		var dim int
		if err := rows.Scan(&hash, &lit, &dim); err != nil {
			continue
		}
		out[hash] = cachedVector{literal: lit, dim: dim}
	}
	return out
}

// DeleteAttachmentChunks 删除某附件的全部知识块(附件删除时调用)。
func (s *pgStore) DeleteAttachmentChunks(ctx context.Context, tenantID string, attID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE tenant_id=$1 AND attachment_id=$2`, tenantID, attID)
	return err
}

// SetAttachmentIndexState 回写附件的解析入库状态(成功/失败原因/块数)。
func (s *pgStore) SetAttachmentIndexState(ctx context.Context, tenantID string, attID int64, indexed bool, errMsg string, chunkCount int) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE kb_attachments SET indexed=$1, index_error=$2, chunk_count=$3 WHERE id=$4 AND tenant_id=$5`,
		indexed, nullableText(errMsg), chunkCount, attID, tenantID); err != nil {
		slog.Warn("knowledge: update attachment index state failed", "att", attID, "err", err)
	}
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// GetChunk 按块 ID 读取知识块(供引用溯源跳转定位原文片段)。
// 同时校验块归属于给定文章与租户, 避免跨文章/跨租户越权读取; 返回 nil,nil 表示不存在。
func (s *pgStore) GetChunk(ctx context.Context, tenantID, articleID, chunkID string) (*ChunkRef, error) {
	c := &ChunkRef{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, heading, content FROM knowledge_chunks
		 WHERE id=$1 AND tenant_id=$2 AND article_id=$3`, chunkID, tenantID, articleID).
		Scan(&c.ID, &c.Heading, &c.Content)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListAttachmentChunks 列出某附件抽取出的知识块(供预览/溯源展示)。
func (s *pgStore) ListAttachmentChunks(ctx context.Context, tenantID string, attID int64, limit int) ([]ChunkRef, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, heading, content FROM knowledge_chunks WHERE tenant_id=$1 AND attachment_id=$2 ORDER BY chunk_index LIMIT $3`,
		tenantID, attID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChunkRef, 0)
	for rows.Next() {
		var c ChunkRef
		if err := rows.Scan(&c.ID, &c.Heading, &c.Content); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListArticleChunks 列出某篇文章正文切块(分块可视化用)。
// 只取正文块(attachment_id IS NULL); 附件块由 ListAttachmentChunks 单独承载。
// chunk_index 是切块顺序, 前端据此还原"文章被切成哪几段、每段多大"。
func (s *pgStore) ListArticleChunks(ctx context.Context, tenantID, articleID string, limit int) ([]ChunkRef, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, heading, content FROM knowledge_chunks
		 WHERE tenant_id=$1 AND article_id=$2 AND attachment_id IS NULL AND (source IS NULL OR source='article')
		 ORDER BY chunk_index LIMIT $3`,
		tenantID, articleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChunkRef, 0)
	for rows.Next() {
		var c ChunkRef
		if err := rows.Scan(&c.ID, &c.Heading, &c.Content); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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
	// 可见性留空时补文档默认值(空串会被检索侧的 fail-closed 谓词当成"受限"而永不召回)。
	a.normalizeVisibility()
	b, _ := json.Marshal(a.Tags)
	// 文章落库(块级向量在 syncChunks 中处理, 不再写 articles.embedding)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO knowledge_articles(id,tenant_id,title,content,category,tags,status,version,expires_at,view_permission,edit_permission,author_id,owner_group_ids,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT(id) DO NOTHING`,
		a.ID, a.TenantID, a.Title, a.Content, a.Category, b, a.Status, a.Version, a.ExpiresAt, a.ViewPermission, a.EditPermission, a.AuthorID, pq.Array(a.OwnerGroupIDs), a.CreatedAt, a.UpdatedAt); err != nil {
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
	err := s.db.QueryRowContext(ctx, `SELECT id,tenant_id,title,content,category,tags,views,status,version,expires_at,view_permission,edit_permission,author_id,owner_group_ids,created_at,updated_at FROM knowledge_articles WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.Views, &a.Status, &a.Version, &a.ExpiresAt, &a.ViewPermission, &a.EditPermission, &a.AuthorID, pq.Array(&a.OwnerGroupIDs), &a.CreatedAt, &a.UpdatedAt)
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
	q := `SELECT id,tenant_id,title,content,category,tags,views,status,version,expires_at,view_permission,edit_permission,author_id,owner_group_ids,created_at,updated_at FROM knowledge_articles WHERE tenant_id=$1`
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
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Title, &a.Content, &a.Category, &tags, &a.Views, &a.Status, &a.Version, &a.ExpiresAt, &a.ViewPermission, &a.EditPermission, &a.AuthorID, pq.Array(&a.OwnerGroupIDs), &a.CreatedAt, &a.UpdatedAt); err != nil {
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
	// 同上: 编辑保存时若前端没带可见性字段, 不能把已公开的文章改成"空=受限"。
	a.normalizeVisibility()
	b, _ := json.Marshal(a.Tags)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE knowledge_articles SET title=$2,content=$3,category=$4,tags=$5,status=$6,version=$7,expires_at=$8,view_permission=$9,edit_permission=$10,author_id=$11,owner_group_ids=$12,updated_at=$13 WHERE id=$1 AND tenant_id=$14`,
		a.ID, a.Title, a.Content, a.Category, b, a.Status, a.Version, a.ExpiresAt, a.ViewPermission, a.EditPermission, a.AuthorID, pq.Array(a.OwnerGroupIDs), a.UpdatedAt, a.TenantID); err != nil {
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

// unindexedArticleJoin / unindexedArticlePredicate 定义「需要重建索引的文章」口径。
//
// 口径 = 正文非空, 且以下任一成立:
//   - 完全没有正文块(c.id IS NULL) —— 从未分块, 例如升级前的存量文章;
//   - 存在正文块但缺向量(c.embedding IS NULL) —— 半索引: 分块成功但 embedding 失败。
//
// 修复前只判定 `c.id IS NULL`, 半索引文章(部分/全部块向量为空)既不会被入队重试,
// 也不计入未索引指标, 于是永久退化为只能被关键词命中且没有任何告警。
// 正文为空的文章切不出块属预期, 必须排除, 否则会被反复入队空转。
// 附件块(attachment_id 非空)由 syncAttachmentChunks 单独维护, 不参与本文口径。
const unindexedArticleJoin = `
	LEFT JOIN knowledge_chunks c
	  ON c.article_id = a.id AND c.tenant_id = a.tenant_id AND c.attachment_id IS NULL`

const unindexedArticlePredicate = `
	WHERE btrim(coalesce(a.content, '')) <> ''
	  AND (c.id IS NULL OR c.embedding IS NULL)`

// ReindexAll 为存量文章补建分块索引(知识库升级到块级 RAG 后的一次性回填)。
// force=true 时连已分块的文章一并重建 —— 更换 embedding 模型后旧维度向量会被检索自动过滤掉,
// 若不强制重建, 这些文章会长期退化成只有关键词检索, 且没有任何报错提示。
// force=false 时按 unindexedArticlePredicate 口径挑选(含半索引文章)。
// 单篇重算后若仍有块缺向量(embedding 服务抖动/限流), 该文章会进入异步重试队列。
// 仅 vectorEnabled 时有意义;逐条处理便于失败隔离。返回处理的文章数。
func (s *pgStore) ReindexAll(ctx context.Context, force bool) (int, error) {
	if !s.vectorEnabled {
		return 0, fmt.Errorf("vector search not available")
	}
	// force 模式去掉 JOIN/排除条件, 全表重建。
	where := unindexedArticleJoin + unindexedArticlePredicate
	if force {
		where = ""
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT a.id, a.tenant_id, a.title, a.content, a.category, a.tags, a.created_at, a.updated_at
		FROM knowledge_articles a`+where)
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
		missing, err := s.syncChunksWithReuse(ctx, a, !force)
		if err != nil {
			slog.Warn("knowledge: reindex chunks failed", "id", a.ID, "err", err)
			continue
		}
		if missing {
			// 半索引: 块已落库但向量缺失, 交给异步重试队列按指数退避补算, 不再静默丢弃。
			slog.Warn("knowledge: reindex left chunks without embedding, queued for retry", "id", a.ID)
			s.enqueueRetry(a.ID, a.TenantID)
		}
		n++
	}
	if force {
		// 换 embedding 模型后向量维度会变, 必须按新维度重建 HNSW 索引:
		// 索引表达式与检索表达式不匹配时会静默退化为全表余弦, 没有任何报错。
		s.ensureHNSWIndex(ctx, "knowledge_chunks")
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

// enqueueUnindexed 扫描「需重建索引」的文章(含半索引),补充进重试队列(幂等:已入队的不重复添加)。
// 作为"安全网"覆盖循环启动前/队列空窗期已存在的未索引或半索引文章。
func (s *pgStore) enqueueUnindexed(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT a.id, a.tenant_id FROM knowledge_articles a`+
		unindexedArticleJoin+unindexedArticlePredicate+`
		LIMIT 500`)
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

// refreshUnindexedMetric 刷新 knowledge_unindexed_articles 指标。
// 口径与 enqueueUnindexed / ReindexAll 一致(正文非空且无块或缺向量的文章数),
// 否则指标会低于实际待办量, 半索引问题在监控上看不出来。
// refreshUnindexedMetric 刷新「仍未进入语义检索的文章数」指标。
func (s *pgStore) refreshUnindexedMetric(ctx context.Context) {
	KnowledgeUnindexedArticles.Set(float64(s.countUnindexedArticles(ctx)))
}

// countUnindexedArticles 返回当前仍未完成索引的文章数(无正文块或块缺向量)。
// 与 refreshUnindexedMetric 同口径, 供检索可观测页与指标共用。
func (s *pgStore) countUnindexedArticles(ctx context.Context) int64 {
	var n int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT a.id) FROM knowledge_articles a`+
		unindexedArticleJoin+unindexedArticlePredicate).Scan(&n); err != nil {
		slog.Warn("knowledge: count unindexed failed", "err", err)
		return 0
	}
	return n
}

// RunReindexPass 执行一轮增量索引自愈:扫描未索引行入队 → 处理到期任务 → 刷新指标。
// 可经后台循环定时调用,也可在测试中同步调用。
func (s *pgStore) RunReindexPass(ctx context.Context) {
	if !s.vectorEnabled || s.embedder == nil {
		return
	}
	s.enqueueUnindexed(ctx)
	s.drainPending(ctx)
	// 空库启动时无数据可探测维度, HNSW 索引建不出来; 首批向量入库后在此补建。
	// 同时兼作换 embedding 模型后的索引重建探测(维度一致时 ensureHNSWIndex 直接返回, 无 DDL)。
	s.ensureHNSWIndex(ctx, "knowledge_chunks")
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

// —— 文章附件 ——

func (s *pgStore) AddAttachment(ctx context.Context, a *ArticleAttachment) error {
	a.CreatedAt = time.Now()
	return s.db.QueryRowContext(ctx, `
		INSERT INTO kb_attachments (article_id, tenant_id, file_name, file_size, file_type, uploaded_by, object_key, data, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		a.ArticleID, a.TenantID, a.FileName, a.FileSize, a.FileType, a.UploadedBy, a.ObjectKey, a.Data, a.CreatedAt,
	).Scan(&a.ID)
}

// attachmentSelectCols 附件查询列(含解析入库状态三列, 兼容老库补列前为 NULL/默认 false)。
const attachmentSelectCols = `id, article_id, tenant_id, file_name, file_size, file_type, uploaded_by, object_key, created_at,
		COALESCE(indexed,false), COALESCE(index_error,''), COALESCE(chunk_count,0)`

func (s *pgStore) ListAttachments(ctx context.Context, articleID, tenantID string) ([]*ArticleAttachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+attachmentSelectCols+`
		FROM kb_attachments WHERE article_id=$1 AND tenant_id=$2 ORDER BY created_at DESC`, articleID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ArticleAttachment, 0)
	for rows.Next() {
		a := &ArticleAttachment{}
		if err := rows.Scan(&a.ID, &a.ArticleID, &a.TenantID, &a.FileName, &a.FileSize, &a.FileType, &a.UploadedBy, &a.ObjectKey, &a.CreatedAt,
			&a.Indexed, &a.IndexError, &a.ChunkCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *pgStore) GetAttachment(ctx context.Context, aid int64, articleID, tenantID string) (*ArticleAttachment, error) {
	a := &ArticleAttachment{}
	err := s.db.QueryRowContext(ctx, `
		SELECT `+attachmentSelectCols+`, data
		FROM kb_attachments WHERE id=$1 AND article_id=$2 AND tenant_id=$3`, aid, articleID, tenantID).
		Scan(&a.ID, &a.ArticleID, &a.TenantID, &a.FileName, &a.FileSize, &a.FileType, &a.UploadedBy, &a.ObjectKey, &a.CreatedAt,
			&a.Indexed, &a.IndexError, &a.ChunkCount, &a.Data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

func (s *pgStore) DeleteAttachment(ctx context.Context, aid int64, articleID, tenantID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM kb_attachments WHERE id=$1 AND article_id=$2 AND tenant_id=$3`, aid, articleID, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// 连带删除该附件抽取出的知识块, 避免附件删了内容仍能被检索到。
	if err := s.DeleteAttachmentChunks(ctx, tenantID, aid); err != nil {
		slog.Warn("knowledge: delete attachment chunks failed", "att", aid, "err", err)
	}
	return nil
}

// reindexArticle 重建单篇文章的分块(供回滚后调用, 不改变版本号)。
func (s *pgStore) reindexArticle(ctx context.Context, a *Article) error {
	_, err := s.syncChunks(ctx, a)
	return err
}
