-- OpsOrigin Core — P0 安全基线迁移 (§6 命令白名单 + §5.3 文件下载令牌)
-- 对齐 001_init.sql 约定: TEXT 主键 / tenant_id 多租户 / RLS 行级隔离 / JSONB / created_at timestamptz

-- ============================================================
-- 命令白名单 (§6 命令白名单硬需求)
--   服务端启用后,Agent 执行的任何系统命令必须命中 rules 之一,否则拒绝。
--   rules 为 JSONB,元素结构: {"type":"exact|prefix|regex|path","pattern":"..."}
--   enabled=false 表示该租户未启用(放行一切);enabled=true 且 rules 为空表示拒绝一切。
-- ============================================================
CREATE TABLE IF NOT EXISTS command_whitelist (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    description TEXT,
    enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    version     BIGINT NOT NULL DEFAULT 1,
    rules       JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_command_whitelist_tenant ON command_whitelist(tenant_id);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE command_whitelist ENABLE ROW LEVEL SECURITY;
CREATE POLICY command_whitelist_tenant_isolation ON command_whitelist
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- 文件下载短期令牌 (§5.3 / §6 下载令牌,建议 ≤10 分钟有效)
--   Server 生成下载地址时签发,嵌入 URL;Agent/Proxy 下载文件时校验。
--   token_hash 为 HMAC 摘要(Server 端不落明文),used 用于一次性/防重放。
-- ============================================================
CREATE TABLE IF NOT EXISTS download_tokens (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    file_id     TEXT NOT NULL,                       -- 文件标识(中心存储 object key 或 task id)
    agent_id    TEXT NOT NULL,                      -- 允许下载的 Agent/主机(防令牌盗用转售)
    token_hash  TEXT NOT NULL,                      -- HMAC-SHA256(token_secret, claims) 摘要
    algo        TEXT NOT NULL DEFAULT 'HS256',
    exp         TIMESTAMPTZ NOT NULL,              -- 过期时间(签发 ttl ≤ 10min)
    used        BOOLEAN NOT NULL DEFAULT FALSE,       -- 是否已被消费(可选一次性)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_download_tokens_tenant ON download_tokens(tenant_id);
CREATE INDEX idx_download_tokens_file ON download_tokens(file_id);
CREATE INDEX idx_download_tokens_exp ON download_tokens(exp);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE download_tokens ENABLE ROW LEVEL SECURITY;
CREATE POLICY download_tokens_tenant_isolation ON download_tokens
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);
