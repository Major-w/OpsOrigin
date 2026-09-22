-- 026_knowledge.sql — 知识库模块全量表结构
--
-- 背景：知识库的表此前【只】存在于各 Store 启动时的自愈 DDL 里
-- （internal/knowledge/pgstore.go、social.go、precipitation.go），deploy/migrations
-- 中没有对应文件，导致全新库初始化时知识库表要等服务首次启动才创建，
-- 任何「先灌数据再起服务」的部署流程都会失败，且 schema 演进无版本记录。
--
-- 本文件与各 Store 的自愈 DDL 等价（CREATE TABLE IF NOT EXISTS + ADD COLUMN IF NOT EXISTS），
-- 全部幂等，可安全重复执行；服务启动时的自愈逻辑仍保留作为兜底。
-- 后续知识库表结构变更：既要在对应 Store 的自愈 DDL 里加，也要在本文件追加。
--
-- 可选扩展（缺失时对应能力自动降级，不影响基础功能）：
--   vector   —— 语义检索（pgvector）；缺失时降级为关键词检索
--   pg_trgm  —— 中文 ILIKE 走 GIN 索引；缺失时退化为顺序扫描
-- 因扩展需超级用户权限，凡依赖扩展的语句一律包在 DO 块中，失败仅告警不中断本文件。

-- ── knowledge_articles：知识文章 ──
CREATE TABLE IF NOT EXISTS knowledge_articles (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    title           TEXT,
    content         TEXT,
    category        TEXT,
    tags            TEXT DEFAULT '[]',
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_tenant ON knowledge_articles(tenant_id, category);

-- 内容管理：文档状态 / 版本号 / 浏览数 / 有效期 / 查看与编辑权限
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS views INT DEFAULT 0;
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS status TEXT DEFAULT 'draft';
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS version INT DEFAULT 1;
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS view_permission TEXT DEFAULT 'all';
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS edit_permission TEXT DEFAULT 'owner';

-- ── knowledge_chunks：RAG 分块表（块级检索单元） ──
-- 文章按 Markdown 标题层级切块，每块独立 embedding；附件抽文本后写入同表，
-- 用 source/attachment_id 与正文块区分（正文块 attachment_id IS NULL）。
CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id              TEXT PRIMARY KEY,
    article_id      TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    chunk_index     INT NOT NULL,
    heading         TEXT DEFAULT '',
    content         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_article ON knowledge_chunks(article_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_tenant ON knowledge_chunks(tenant_id);

-- 附件解析入库：source=article|attachment，attachment_id 指向 kb_attachments.id
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS source TEXT DEFAULT 'article';
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS attachment_id BIGINT;
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_attachment ON knowledge_chunks(attachment_id);

-- 向量维度标记：换 embedding 模型后旧维度向量自动失效，避免污染检索结果
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS embed_dim INT;
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_dim ON knowledge_chunks(embed_dim);

-- 全文检索列：英文/带空格词元走 tsvector（tsvector 为 PG 内置类型，无需扩展）
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS content_tsv tsvector;
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_tsv ON knowledge_chunks USING gin(content_tsv);

-- 存量回填：建列之前入库的块 content_tsv 为 NULL，不回填会检索不到
UPDATE knowledge_chunks
   SET content_tsv = to_tsvector('simple', coalesce(heading, '') || ' ' || content)
 WHERE content_tsv IS NULL;

-- 存量清理：早期切分器会给空/纯空白正文补一个「内容为空」的占位块，这类块没有任何
-- 信息量，却会被送去 embedding 并作为命中占据 top-k 名额，把有效片段挤出结果集。
-- 切分器已改为空内容返回 0 块（见 internal/knowledge/chunk.go chunkMarkdown），
-- 这里清掉历史遗留的空块；条件恒为幂等（清完再跑影响 0 行）。
DELETE FROM knowledge_chunks WHERE btrim(coalesce(content, '')) = '';

-- 依赖扩展的列与索引：全部包在 DO 块内，扩展不可用时降级而非中断
--
-- 关于 HNSW 索引的维度：embedding 列声明为裸 vector（允许混存任意维度），而 pgvector 的
-- HNSW 索引要求列有固定维度，否则 CREATE INDEX 直接报 "column does not have dimensions"，
-- 索引静默建不出来、余弦检索退化为全表扫描。因此这里先按库中已有向量的实际维度
-- 把列固化为 vector(d)，再建索引；库为空（或维度混杂）时跳过，等服务启动后的
-- 后台自愈循环在首批向量入库后补建（见 internal/knowledge/pgstore.go ensureHNSWIndex）。
DO $$
DECLARE
    v_dim      INT;
    v_variants INT;
    v_halfvec  BOOLEAN := false;
BEGIN
    CREATE EXTENSION IF NOT EXISTS vector;

    -- 文章级向量为历史结构，保留兼容
    ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS embedding vector;
    ALTER TABLE knowledge_chunks   ADD COLUMN IF NOT EXISTS embedding vector;

    -- 存量块补 embed_dim（该列晚于向量列引入，为 NULL 会被维度过滤漏掉）
    UPDATE knowledge_chunks
       SET embed_dim = vector_dims(embedding)
     WHERE embedding IS NOT NULL AND embed_dim IS NULL;

    -- halfvec 可用性（pgvector 0.7+）
    BEGIN
        PERFORM '[0]'::halfvec(1);
        v_halfvec := true;
    EXCEPTION WHEN OTHERS THEN
        v_halfvec := false;
    END;

    -- knowledge_chunks：按数据实际维度建 HNSW。
    -- 已存在同名索引时不重建（幂等复跑零代价）；维度变化由服务启动时的
    -- ensureHNSWIndex 负责 DROP + 重建。
    SELECT count(DISTINCT embed_dim), min(embed_dim)
      INTO v_variants, v_dim
      FROM knowledge_chunks WHERE embedding IS NOT NULL;
    IF COALESCE(v_variants, 0) = 1 AND COALESCE(v_dim, 0) > 0 THEN
        IF v_halfvec THEN
            IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'idx_knowledge_chunks_embedding_half') THEN
                EXECUTE format(
                    'CREATE INDEX idx_knowledge_chunks_embedding_half ON knowledge_chunks
                     USING hnsw ((embedding::halfvec(%s)) halfvec_cosine_ops)', v_dim);
                RAISE NOTICE 'knowledge: knowledge_chunks HNSW(halfvec %) 索引已建', v_dim;
            END IF;
        ELSIF v_dim <= 2000 THEN
            IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'idx_knowledge_chunks_embedding') THEN
                EXECUTE format('ALTER TABLE knowledge_chunks ALTER COLUMN embedding TYPE vector(%s)', v_dim);
                EXECUTE 'CREATE INDEX idx_knowledge_chunks_embedding ON knowledge_chunks USING hnsw (embedding vector_cosine_ops)';
                RAISE NOTICE 'knowledge: knowledge_chunks HNSW(vector %) 索引已建', v_dim;
            END IF;
        ELSE
            RAISE NOTICE 'knowledge: 跳过 chunks HNSW（% 维且无 halfvec 支持，需 pgvector 0.7+）', v_dim;
        END IF;
    ELSE
        RAISE NOTICE 'knowledge: 跳过 chunks HNSW（库中无向量或维度混杂，variants=%）', v_variants;
    END IF;

    -- knowledge_articles：历史文章级向量（该表无 embed_dim 列，直接按向量维度探测），
    -- 索引名保持 idx_knowledge_embedding* 兼容。
    SELECT count(DISTINCT vector_dims(embedding)), min(vector_dims(embedding))
      INTO v_variants, v_dim
      FROM knowledge_articles WHERE embedding IS NOT NULL;
    IF COALESCE(v_variants, 0) = 1 AND COALESCE(v_dim, 0) > 0 AND v_halfvec THEN
        IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'idx_knowledge_articles_embedding_half') THEN
            EXECUTE format(
                'CREATE INDEX idx_knowledge_articles_embedding_half ON knowledge_articles
                 USING hnsw ((embedding::halfvec(%s)) halfvec_cosine_ops)', v_dim);
            RAISE NOTICE 'knowledge: knowledge_articles HNSW(halfvec %) 索引已建', v_dim;
        END IF;
    END IF;
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'knowledge: pgvector unavailable (%), 语义检索降级为关键词检索', SQLERRM;
END $$;

-- pg_trgm：让 ILIKE '%中文%' 走 GIN 索引扫描而非全表扫描
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
    CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_trgm
        ON knowledge_chunks USING gin(content gin_trgm_ops);
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'knowledge: pg_trgm unavailable (%), 中文 ILIKE 仍为顺序扫描', SQLERRM;
END $$;

-- ── knowledge_categories：分类元数据 ──
CREATE TABLE IF NOT EXISTS knowledge_categories (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT DEFAULT '',
    sort_order      INT DEFAULT 0,
    color           TEXT DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE(tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_categories_tenant ON knowledge_categories(tenant_id);

-- ── knowledge_article_versions：文章版本快照 ──
CREATE TABLE IF NOT EXISTS knowledge_article_versions (
    id              TEXT PRIMARY KEY,
    article_id      TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    version         INT NOT NULL,
    title           TEXT NOT NULL,
    content         TEXT NOT NULL,
    category        TEXT DEFAULT '',
    tags            TEXT DEFAULT '[]',
    created_at      TIMESTAMPTZ NOT NULL,
    UNIQUE(article_id, version)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_versions_article ON knowledge_article_versions(article_id, version);

-- ── kb_attachments：文章附件（元数据在库，二进制在对象存储或回退 data） ──
CREATE TABLE IF NOT EXISTS kb_attachments (
    id              BIGSERIAL PRIMARY KEY,
    article_id      TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    file_name       TEXT NOT NULL,
    file_size       BIGINT NOT NULL,
    file_type       TEXT NOT NULL,
    uploaded_by     TEXT NOT NULL,
    object_key      TEXT,
    data            BYTEA,
    created_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_kb_attachments_article ON kb_attachments(article_id, tenant_id);

-- 附件解析入库状态：indexed=是否抽文本切片完成，index_error=失败原因，chunk_count=产出块数
ALTER TABLE kb_attachments ADD COLUMN IF NOT EXISTS indexed BOOLEAN DEFAULT false;
ALTER TABLE kb_attachments ADD COLUMN IF NOT EXISTS index_error TEXT;
ALTER TABLE kb_attachments ADD COLUMN IF NOT EXISTS chunk_count INT DEFAULT 0;

-- ── 互动数据：点赞 / 收藏 / 关注 / 更新提醒 / 评分 / 评论 ──
CREATE TABLE IF NOT EXISTS knowledge_likes (
    article_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT 'admin',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY(article_id, user_id)
);

CREATE TABLE IF NOT EXISTS knowledge_bookmarks (
    article_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT 'admin',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY(article_id, user_id)
);

CREATE TABLE IF NOT EXISTS knowledge_follows (
    article_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT 'admin',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY(article_id, user_id)
);

CREATE TABLE IF NOT EXISTS knowledge_follow_updates (
    article_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT 'admin',
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    read            BOOLEAN DEFAULT false,
    PRIMARY KEY(article_id, user_id)
);

CREATE TABLE IF NOT EXISTS knowledge_ratings (
    id              TEXT PRIMARY KEY,
    article_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT 'admin',
    rating          INT NOT NULL CHECK(rating BETWEEN 1 AND 5),
    comment         TEXT DEFAULT '',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS knowledge_comments (
    id              TEXT PRIMARY KEY,
    article_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT 'admin',
    content         TEXT NOT NULL,
    parent_id       TEXT DEFAULT '',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- ── knowledge_precipitation_rules：知识沉淀规则 ──
CREATE TABLE IF NOT EXISTS knowledge_precipitation_rules (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    source              TEXT NOT NULL DEFAULT 'manual',
    category            TEXT DEFAULT '',
    title_template      TEXT DEFAULT '',
    content_template    TEXT DEFAULT '',
    enabled             BOOLEAN DEFAULT true,
    auto_publish        BOOLEAN DEFAULT false,
    created_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ── knowledge_search_logs：AI 检索留痕（检索可观测 P1-2 / 零结果留痕 P1-4） ──
-- 每次 AI 知识库检索写一条：命中了哪几条路、召回几篇、耗时多少、是否零结果。
-- 零结果查询即「知识盲区清单」——用户真实问过但知识库里没有的问题。
-- 保留期 30 天，由 internal/knowledge/search_logs.go 的后台循环清理。
CREATE TABLE IF NOT EXISTS knowledge_search_logs (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    query           TEXT NOT NULL,
    mode            TEXT NOT NULL DEFAULT 'rrf',   -- rrf | fallback | cache
    categories      TEXT DEFAULT '',               -- 逗号分隔的 Agent 知识范围
    vec_hits        INT NOT NULL DEFAULT 0,        -- 块级向量路命中块数（融合前）
    kw_hits         INT NOT NULL DEFAULT 0,        -- 块级关键词路命中块数（融合前）
    hit_count       INT NOT NULL DEFAULT 0,        -- 融合后返回文章数
    zero_result     BOOLEAN NOT NULL DEFAULT false,
    latency_ms      DOUBLE PRECISION NOT NULL DEFAULT 0,
    top_score       DOUBLE PRECISION NOT NULL DEFAULT 0,
    agent_id        TEXT DEFAULT '',
    user_id         TEXT DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_search_logs_time ON knowledge_search_logs(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_search_logs_zero ON knowledge_search_logs(tenant_id, zero_result, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_search_logs_query ON knowledge_search_logs(tenant_id, query);

-- ── knowledge_article_reviews：文章状态流转 / 评审轨迹（P3-3） ──
-- 每次状态流转（草稿→待评审→发布→归档，含系统到期自动下架）写一条，
-- 驳回原因等评审备注留在这里（评审是可追溯的历史，不是文章表上的可变字段）。
CREATE TABLE IF NOT EXISTS knowledge_article_reviews (
    id              TEXT PRIMARY KEY,
    article_id      TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    from_status     TEXT NOT NULL,               -- draft | in_review | published | archived
    to_status       TEXT NOT NULL,
    note            TEXT DEFAULT '',
    actor           TEXT DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_reviews_article ON knowledge_article_reviews(article_id, tenant_id, created_at DESC);

-- ── 存量回填：view_permission 空串归一为 'all' ──
-- view_permission 的语义是 all(默认)，建表默认值同为 'all'，但写入路径历史上一律原样落库，
-- 于是大量「已发布」文章的该字段是空串。检索侧对可见性做 fail-closed 判定（只有公开可召回），
-- 空串会被当成"受限"，导致这批文章静默变成 AI 检索不可见（实测：published 文章仅 2/5 可召回）。
-- 显式归一为 'all'，与 knowledge/pgstore.go init() 的自愈 DDL 等价、幂等。
UPDATE knowledge_articles SET view_permission = 'all'
 WHERE COALESCE(view_permission, '') = '';
