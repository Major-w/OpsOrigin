-- 022_cmdb_usage.sql
-- CMDB 数据消费分析：记录对 CMDB API 的每一次调用，用于分析
-- 「被调用了多少次 / 热点数据 / 调用错误频率」。
--
-- 写入方：internal/cmdb 的采集中间件（内存 channel 批量落库，不阻塞请求）。
-- 运行时由 Repository.EnsureUsageSchema 自愈建表（CREATE TABLE IF NOT EXISTS），
-- 本文件供全新环境按序初始化，与 016_platform_messages.sql 同模式。
--
-- 设计取舍：
--   route  —— 路由模板（Go 1.22 ServeMux 的 r.Pattern，如 "GET /api/v1/cmdb/ci/{id}"）。
--              天然低基数，适合按端点聚合热点；比裸 path 更适合做统计维度。
--   path   —— 原始请求路径，仅供错误样本定位，不做聚合维度（避免高基数膨胀）。
--   ci_id  —— 仅命中单实例端点（/cmdb/ci/{id} 等）时写入，用于「热点配置项」排行。
--   不落请求体/响应体，避免敏感信息与表膨胀。

CREATE TABLE IF NOT EXISTS cmdb_usage_event (
    id          BIGSERIAL   PRIMARY KEY,
    tenant_id   TEXT        NOT NULL DEFAULT 'default',
    user_id     TEXT        NOT NULL DEFAULT '',
    method      TEXT        NOT NULL DEFAULT 'GET',
    route       TEXT        NOT NULL DEFAULT '',
    path        TEXT        NOT NULL DEFAULT '',
    ci_id       TEXT        NOT NULL DEFAULT '',
    status_code INT         NOT NULL DEFAULT 200,
    latency_ms  INT         NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cmdb_usage_created
    ON cmdb_usage_event (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_cmdb_usage_route
    ON cmdb_usage_event (tenant_id, route, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_cmdb_usage_ci
    ON cmdb_usage_event (tenant_id, ci_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_cmdb_usage_status
    ON cmdb_usage_event (tenant_id, status_code, created_at DESC);
