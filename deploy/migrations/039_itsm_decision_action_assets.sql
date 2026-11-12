-- ITSM 配置资产：决策表资产（跨表单复用 / 草稿·发布 / 不可变版本）+ 动作模板。
-- 与 internal/itsm/assets/pg.go Init 自愈 DDL 双写，幂等可重跑。
-- 所有列 NOT NULL DEFAULT，避免 rows.Scan 直扫 NULL 导致整条查询失败。

CREATE TABLE IF NOT EXISTS itsm_decision_tables (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'draft',
    version     INTEGER NOT NULL DEFAULT 0,
    table_json  TEXT NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_itsm_dt_tenant ON itsm_decision_tables(tenant_id, name);

CREATE TABLE IF NOT EXISTS itsm_decision_table_versions (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL DEFAULT 'default',
    asset_id   TEXT NOT NULL,
    version    INTEGER NOT NULL,
    table_json TEXT NOT NULL DEFAULT '{}',
    note       TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, asset_id, version)
);
CREATE INDEX IF NOT EXISTS idx_itsm_dtv_asset ON itsm_decision_table_versions(tenant_id, asset_id, version DESC);

CREATE TABLE IF NOT EXISTS itsm_action_templates (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    action_type TEXT NOT NULL,
    inputs      TEXT NOT NULL DEFAULT '{}',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_itsm_at_tenant ON itsm_action_templates(tenant_id, action_type);
