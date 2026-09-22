-- 028_monitor_sources.sql
-- 外部指标采集源持久化：Zabbix Agent / Prometheus exporter 数据源落库，
-- 重启后由 service.Start 从 DB 恢复并重新调度（原为纯内存 map，重启全丢）。
-- 与 internal/monitor/source_store.go 的 ensureSourcesTable(自愈 DDL) 双写。

CREATE TABLE IF NOT EXISTS monitor_sources (
    id           TEXT        PRIMARY KEY,
    tenant_id    TEXT        NOT NULL DEFAULT 'default',
    name         TEXT        NOT NULL,
    type         TEXT        NOT NULL,
    address      TEXT        NOT NULL,
    port         INT         NOT NULL DEFAULT 0,
    path         TEXT        NOT NULL DEFAULT '',
    keys         JSONB       NOT NULL DEFAULT '[]',
    interval_sec INT         NOT NULL DEFAULT 30,
    ci_id        TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_monitor_sources_tenant ON monitor_sources (tenant_id);
