-- ITSM 动作模板引用化：草稿/发布状态 + 不可变版本快照。
-- 与 internal/itsm/assets/pg.go Init 自愈 DDL 双写，幂等可重跑。
-- 存量模板一律保持 draft / version=0，需在动作库页面显式「发布」后才能被流程引用。
-- 所有列 NOT NULL DEFAULT，避免 rows.Scan 直扫 NULL 导致整条查询失败。

ALTER TABLE itsm_action_templates
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft';
ALTER TABLE itsm_action_templates
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS itsm_action_template_versions (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    template_id TEXT NOT NULL,
    version     INTEGER NOT NULL,
    action_type TEXT NOT NULL,
    inputs      TEXT NOT NULL DEFAULT '{}',
    note        TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, template_id, version)
);
CREATE INDEX IF NOT EXISTS idx_itsm_atv_template
    ON itsm_action_template_versions(tenant_id, template_id, version DESC);
