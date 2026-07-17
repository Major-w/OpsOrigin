-- OpsOrigin Core — 版本追溯 + 自动发现 迁移

-- ============================================================
-- CMDB: CI 版本历史（全量快照对比）
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_ci_version (
    id          TEXT PRIMARY KEY,
    ci_id       TEXT NOT NULL REFERENCES cmdb_ci(id) ON DELETE CASCADE,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    version     INT NOT NULL,
    name        TEXT NOT NULL,
    type_id     TEXT NOT NULL,
    status      TEXT NOT NULL,
    attributes  JSONB NOT NULL DEFAULT '{}',
    labels      JSONB NOT NULL DEFAULT '{}',
    operator    TEXT NOT NULL DEFAULT 'system',
    change_type TEXT NOT NULL DEFAULT 'update',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmdb_ci_version_ci ON cmdb_ci_version(ci_id);
CREATE INDEX idx_cmdb_ci_version_tenant ON cmdb_ci_version(tenant_id);
CREATE INDEX idx_cmdb_ci_version_created ON cmdb_ci_version(created_at DESC);

-- ============================================================
-- CMDB: 自动发现任务
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_discovery_task (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    type        TEXT NOT NULL,
    target      TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'idle',
    config      JSONB NOT NULL DEFAULT '{}',
    found_count INT NOT NULL DEFAULT 0,
    last_run_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmdb_discovery_task_tenant ON cmdb_discovery_task(tenant_id);

-- ============================================================
-- CMDB: 自动发现结果（已发现的资产）
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_discovery_result (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES cmdb_discovery_task(id) ON DELETE CASCADE,
    tenant_id     TEXT NOT NULL DEFAULT 'default',
    ci_name       TEXT NOT NULL,
    ci_type       TEXT NOT NULL,
    ip_address    TEXT,
    attributes    JSONB NOT NULL DEFAULT '{}',
    source        TEXT NOT NULL,
    synced        BOOLEAN NOT NULL DEFAULT FALSE,
    synced_ci_id  TEXT,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmdb_discovery_result_task ON cmdb_discovery_result(task_id);
CREATE INDEX idx_cmdb_discovery_result_tenant ON cmdb_discovery_result(tenant_id);
CREATE INDEX idx_cmdb_discovery_result_synced ON cmdb_discovery_result(synced);
