-- 维护期静默(变更窗口抑制误报): 在 [start_at, end_at] 内且命中可选匹配条件的告警被抑制通知。
CREATE TABLE IF NOT EXISTS monitor_maintenance (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    name       TEXT NOT NULL,
    start_at   TIMESTAMPTZ NOT NULL,
    end_at     TIMESTAMPTZ NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    match      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_monitor_maintenance_tenant ON monitor_maintenance(tenant_id);
