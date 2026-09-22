-- 告警规则持久化(与 #720 数据源同源问题修复, 镜像 source_store 范式)。
-- 自愈 DDL 见 internal/monitor/rule_store.go ensureRulesTable, 此处为可审计的显式迁移。
CREATE TABLE IF NOT EXISTS monitor_alert_rules (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL,
    name              TEXT NOT NULL,
    metric_name       TEXT NOT NULL DEFAULT '',
    operator          TEXT NOT NULL DEFAULT '',
    threshold         DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_sec      INT NOT NULL DEFAULT 0,
    severity          TEXT NOT NULL DEFAULT 'warning',
    labels            JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    type              TEXT NOT NULL DEFAULT 'threshold',
    expr              TEXT NOT NULL DEFAULT '',
    eval_interval_sec INT NOT NULL DEFAULT 60,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_monitor_alert_rules_tenant ON monitor_alert_rules (tenant_id);
