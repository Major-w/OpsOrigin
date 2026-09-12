-- 023_api_keys.sql
-- API Key 管理：密钥绑定到用户，鉴权时以该用户身份通过，并「继承用户当前权限」。
--
-- 继承语义：Key 上不冗余任何权限点，鉴权命中后用 user_id 实时回查
-- users.role 作为主角色，再由 IAM 富化器聚合出最终权限集合 —— 因此用户
-- 角色/用户组变更后，其名下所有 Key 的权限自动跟随，无需重建。
--
-- 安全取舍：
--   key_hash    —— 只存 SHA-256 摘要，明文仅在创建响应中返回一次，落库不可逆。
--   key_prefix  —— 可读前缀（osk_ 前 12 位），列表页用于辨认是哪个 Key。
--   expires_at  —— NULL 表示永不过期；吊销用 revoked_at（NULL=有效）。
--   last_used_at / use_count —— 用量统计，便于发现长期闲置的密钥并回收。
--
-- 运行时由 internal/apikey 的 Store.EnsureSchema 自愈建表；本文件供全新库初始化。

CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT        PRIMARY KEY,
    tenant_id    TEXT        NOT NULL DEFAULT 'default',
    user_id      TEXT        NOT NULL,
    name         TEXT        NOT NULL DEFAULT '',
    key_prefix   TEXT        NOT NULL DEFAULT '',
    key_hash     TEXT        NOT NULL UNIQUE,
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    use_count    BIGINT      NOT NULL DEFAULT 0,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_keys_user
    ON api_keys (tenant_id, user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash
    ON api_keys (key_hash);
