-- 020_cloud_credentials.sql
-- 云接入凭证持久化（替代内存存储：服务重启不丢凭证，云采集任务可持续拉取资源）。
-- SecretKey 由应用层 AES-GCM 加密后写入，密钥来自 OPSORIGIN_CRED_ENCRYPT_KEY。

CREATE TABLE IF NOT EXISTS cloud_credentials (
    provider    TEXT        NOT NULL,
    id          TEXT        NOT NULL,
    region      TEXT        NOT NULL DEFAULT '',
    access_key  TEXT        NOT NULL DEFAULT '',
    secret_key  TEXT        NOT NULL DEFAULT '',
    extra       JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, id)
);

CREATE INDEX IF NOT EXISTS idx_cloud_credentials_provider ON cloud_credentials (provider);
