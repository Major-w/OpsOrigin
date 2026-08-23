-- 021_monitor_targets.sql
-- 监控拨测目标持久化：手动添加/发现的 MonitorTarget 落库，重启后恢复
-- （原为纯内存 map，重启全丢）。

CREATE TABLE IF NOT EXISTS monitor_targets (
    id           TEXT        PRIMARY KEY,
    tenant_id    TEXT        NOT NULL DEFAULT 'default',
    ci_id        TEXT        NOT NULL DEFAULT '',
    name         TEXT        NOT NULL,
    address      TEXT        NOT NULL,
    port         INT         NOT NULL DEFAULT 0,
    probes       JSONB       NOT NULL DEFAULT '[]',
    interval_sec INT         NOT NULL DEFAULT 60,
    timeout_sec  INT         NOT NULL DEFAULT 5,
    labels       JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_monitor_targets_ci ON monitor_targets (ci_id);
