-- OpsOrigin Core — P1 文件分发中心存储表 (§5 文件分发)
-- 对齐 004 约定: TEXT 主键 / tenant_id 多租户 / RLS 行级隔离 / created_at timestamptz
--
-- 说明: 记录文件实体的元数据。实际字节落盘于 FileStore.Dir(以 file_id 为文件名),
--       本表提供原始文件名、校验和、大小等,供 §6 下载令牌签发与审计使用。
--       文件内容本身不入库,避免大对象撑爆数据库。

CREATE TABLE IF NOT EXISTS files (
    file_id        TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL DEFAULT 'default',
    original_name  TEXT NOT NULL,
    stored_path    TEXT NOT NULL,                 -- 服务器本地绝对路径(FileStore.Dir/file_id)
    size_bytes     BIGINT NOT NULL DEFAULT 0,
    checksum       TEXT,                          -- 文件校验和(sha256 / md5)
    checksum_algo TEXT NOT NULL DEFAULT 'sha256',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_files_tenant ON files(tenant_id);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE files ENABLE ROW LEVEL SECURITY;
CREATE POLICY files_tenant_isolation ON files
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);
