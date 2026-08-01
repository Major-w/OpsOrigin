-- OpsOrigin Core — 采集任务框架（全局默认策略 + 采集任务）
--
-- 设计见竞品分析文档「六、采集架构设计（全局默认 + 纳管覆盖）」：
--   纳管层只建立通道，CMDB 作为统一调度者持有采集任务，
--   按频率触发、结果按 kind 路由（cmdb→CI / monitor→指标），采集器可插拔。

-- ============================================================
-- 平台级全局默认策略（单例行，兜底）
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_platform_policy (
    tenant_id                 TEXT PRIMARY KEY DEFAULT 'default',
    default_cmdb_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    default_monitor_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
    cmdb_default_frequency   TEXT    NOT NULL DEFAULT '1h',
    monitor_default_frequency TEXT   NOT NULL DEFAULT '5m',
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- 采集任务（由 CMDB 调度器按 frequency 触发）
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_collection_task (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL DEFAULT 'default',
    name          TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    target        TEXT NOT NULL,
    kind          TEXT NOT NULL,           -- cmdb | monitor
    frequency     TEXT NOT NULL DEFAULT '1h',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at   TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'idle', -- idle | running | success | failed | pending
    error         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmdb_collection_task_tenant  ON cmdb_collection_task(tenant_id);
CREATE INDEX idx_cmdb_collection_task_target  ON cmdb_collection_task(target);
CREATE INDEX idx_cmdb_collection_task_kind    ON cmdb_collection_task(kind);
CREATE INDEX idx_cmdb_collection_task_enabled ON cmdb_collection_task(enabled);
