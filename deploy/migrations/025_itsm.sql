-- 025_itsm.sql — ITSM 模块全量表结构
--
-- 背景：ITSM 的 14 张表此前【只】存在于各 Store 启动时的自愈 DDL 里，
-- deploy/migrations 中没有对应文件，导致全新库初始化时 ITSM 表要等服务首次启动才创建，
-- 任何「先灌数据再起服务」的部署流程都会失败，且 schema 演进无版本记录。
--
-- 本文件与各 Store 的自愈 DDL 等价（CREATE TABLE IF NOT EXISTS + ADD COLUMN IF NOT EXISTS），
-- 全部幂等，可安全重复执行；服务启动时的自愈逻辑仍保留作为兜底。
-- 后续 ITSM 表结构变更：既要在对应 Store 的自愈 DDL 里加，也要在本文件追加。

-- itsm_tickets
CREATE TABLE IF NOT EXISTS itsm_tickets (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    risk_level      TEXT NOT NULL,
    status          TEXT NOT NULL,
    change_plan     TEXT,
    rollback_plan   TEXT,
    created_by      TEXT,
    created_by_type TEXT,
    approved_by     TEXT,
    reject_reason   TEXT,
    completed_by    TEXT,
    rolled_back_by  TEXT,
    rollback_reason TEXT,
    ci_ids          TEXT DEFAULT '[]',
    webhook_url     TEXT,
    labels          TEXT DEFAULT '{}',
    trace_id        TEXT,
    scheduled_at    TIMESTAMPTZ,
    deadline_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    approved_at     TIMESTAMPTZ,
    executed_at     TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    rolled_back_at  TIMESTAMPTZ,
    sla_warning_notified BOOLEAN DEFAULT FALSE,
    sla_breached        BOOLEAN DEFAULT FALSE,
    sla_breached_at     TIMESTAMPTZ,
    sla_escalation_level INTEGER DEFAULT 0,
    custom_fields       TEXT DEFAULT '{}'
    );

-- itsm_comments
CREATE TABLE IF NOT EXISTS itsm_comments (
    id         BIGSERIAL PRIMARY KEY,
    ticket_id  TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    author     TEXT NOT NULL,
    content    TEXT NOT NULL,
    type       TEXT DEFAULT 'normal',
    mentions   JSONB DEFAULT '[]'::jsonb,
    edited     BOOLEAN DEFAULT FALSE,
    edited_at  TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
    );

-- itsm_subtasks
CREATE TABLE IF NOT EXISTS itsm_subtasks (
    id          BIGSERIAL PRIMARY KEY,
    ticket_id   TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    title       TEXT NOT NULL,
    description TEXT,
    assignee    TEXT,
    status      TEXT NOT NULL DEFAULT 'pending',
    due_at      TIMESTAMPTZ,
    created_by  TEXT,
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    meta        TEXT DEFAULT '{}',
    detail      TEXT,
    viewed_at   TIMESTAMPTZ,
    started_at  TIMESTAMPTZ,
    updated_by  TEXT,
    close_with_parent BOOLEAN NOT NULL DEFAULT TRUE
    );

-- itsm_attachments
CREATE TABLE IF NOT EXISTS itsm_attachments (
    id          BIGSERIAL PRIMARY KEY,
    ticket_id   TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    file_name   TEXT NOT NULL,
    file_size   BIGINT NOT NULL,
    file_type   TEXT NOT NULL,
    uploaded_by TEXT NOT NULL,
    data        BYTEA,
    object_key  TEXT,
    created_at  TIMESTAMPTZ NOT NULL
    );

-- itsm_changes
CREATE TABLE IF NOT EXISTS itsm_changes (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    ticket_id     TEXT,
    title         TEXT NOT NULL,
    description   TEXT,
    risk_level    TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    change_type   TEXT,
    impact        TEXT,
    urgency       TEXT,
    cab_members   TEXT DEFAULT '[]',
    approvals     TEXT DEFAULT '[]',
    plan          TEXT,
    rollback_plan TEXT,
    created_by    TEXT,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    scheduled_at  TIMESTAMPTZ,
    window_start  TIMESTAMPTZ,
    window_end    TIMESTAMPTZ
    );

-- itsm_form_schemas
CREATE TABLE IF NOT EXISTS itsm_form_schemas (
    tenant_id TEXT NOT NULL,
    scope     TEXT NOT NULL,
    version   INTEGER NOT NULL DEFAULT 1,
    fields    JSONB NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, scope)
    );

-- itsm_form_dicts
CREATE TABLE IF NOT EXISTS itsm_form_dicts (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    fields TEXT DEFAULT '[]',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
    );

-- itsm_processes
CREATE TABLE IF NOT EXISTS itsm_processes (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    category TEXT DEFAULT '',
    steps JSONB DEFAULT '[]',
    labels JSONB DEFAULT '{}',
    enabled BOOLEAN DEFAULT TRUE,
    version INT DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );

-- itsm_process_instances
CREATE TABLE IF NOT EXISTS itsm_process_instances (
    id TEXT PRIMARY KEY,
    process_id TEXT NOT NULL,
    ticket_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL DEFAULT 'default',
    current_step TEXT DEFAULT '',
    status TEXT DEFAULT 'running',
    step_results JSONB DEFAULT '{}',
    process_snapshot JSONB DEFAULT '[]',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );

-- itsm_catalog
CREATE TABLE IF NOT EXISTS itsm_catalog (
    id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL, description TEXT DEFAULT '',
    category TEXT DEFAULT '', process_id TEXT DEFAULT '',
    form_scope TEXT DEFAULT '',
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
    );

-- itsm_sla_policies
CREATE TABLE IF NOT EXISTS itsm_sla_policies (
    id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL, risk_level TEXT NOT NULL,
    process_id TEXT DEFAULT '',
    response_hours INT DEFAULT 0, resolution_hours INT DEFAULT 0,
    escalation_hours INT DEFAULT 0, escalation_to TEXT DEFAULT '',
    escalation_levels JSONB DEFAULT '[]'::jsonb,
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
    );

-- itsm_ola_policies
CREATE TABLE IF NOT EXISTS itsm_ola_policies (
    id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL, process_id TEXT DEFAULT '', step_id TEXT DEFAULT '',
    duration_hours INT DEFAULT 24, escalate_to TEXT DEFAULT '',
    notify_channel TEXT DEFAULT 'email', enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
    );

-- oncall_shifts
CREATE TABLE IF NOT EXISTS oncall_shifts (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, user_id TEXT, start_time TIMESTAMPTZ, end_time TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL);

-- oncall_rotations
CREATE TABLE IF NOT EXISTS oncall_rotations (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT, members TEXT DEFAULT '[]', cycle_days INTEGER DEFAULT 7, labels TEXT DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL);

-- 自愈演进补齐的列（与 Store 内 ADD COLUMN IF NOT EXISTS 对齐）
ALTER TABLE itsm_comments ADD COLUMN IF NOT EXISTS type TEXT DEFAULT 'normal';
ALTER TABLE itsm_comments ADD COLUMN IF NOT EXISTS mentions JSONB DEFAULT '[]'::jsonb;
ALTER TABLE itsm_comments ADD COLUMN IF NOT EXISTS edited BOOLEAN DEFAULT FALSE;
ALTER TABLE itsm_comments ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ;
ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS meta TEXT DEFAULT '{}';
ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS detail TEXT;
ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS viewed_at TIMESTAMPTZ;
ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS updated_by TEXT;
ALTER TABLE itsm_subtasks ADD COLUMN IF NOT EXISTS close_with_parent BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE itsm_attachments ADD COLUMN IF NOT EXISTS object_key TEXT;
ALTER TABLE itsm_changes ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ;
ALTER TABLE itsm_changes ADD COLUMN IF NOT EXISTS window_start TIMESTAMPTZ;
ALTER TABLE itsm_changes ADD COLUMN IF NOT EXISTS window_end TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS rollback_plan TEXT;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS completed_by TEXT;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS rolled_back_by TEXT;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS rollback_reason TEXT;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS ci_ids TEXT DEFAULT '[]';
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS deadline_at TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS rolled_back_at TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS sla_warning_notified BOOLEAN DEFAULT FALSE;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS sla_breached BOOLEAN DEFAULT FALSE;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS sla_breached_at TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS sla_escalation_level INTEGER DEFAULT 0;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS process_id TEXT;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS custom_fields TEXT DEFAULT '{}';
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS assignee TEXT;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS assigned_at TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS satisfaction INTEGER DEFAULT 0;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS on_hold BOOLEAN DEFAULT FALSE;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS hold_reason TEXT;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS hold_until_at TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS related_ticket_ids TEXT DEFAULT '[]';
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS catalog_id TEXT DEFAULT '';
ALTER TABLE itsm_processes ADD COLUMN IF NOT EXISTS form JSONB DEFAULT '{}'::jsonb;
ALTER TABLE itsm_processes ADD COLUMN IF NOT EXISTS sla_policy_id TEXT DEFAULT '';
ALTER TABLE itsm_processes ADD COLUMN IF NOT EXISTS notify_config JSONB DEFAULT '{}'::jsonb;
ALTER TABLE itsm_process_instances ADD COLUMN IF NOT EXISTS process_snapshot JSONB DEFAULT '[]';
ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS form_scope TEXT DEFAULT '';
ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS audience JSONB DEFAULT '{}'::jsonb;
ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS parent_id TEXT DEFAULT '';
ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS level INTEGER DEFAULT 1;
ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS sort_order INTEGER DEFAULT 0;
ALTER TABLE itsm_catalog ADD COLUMN IF NOT EXISTS kind TEXT DEFAULT 'service';
ALTER TABLE itsm_sla_policies ADD COLUMN IF NOT EXISTS process_id TEXT DEFAULT '';
ALTER TABLE itsm_sla_policies ADD COLUMN IF NOT EXISTS escalation_levels JSONB DEFAULT '[]'::jsonb;

-- 索引
CREATE INDEX IF NOT EXISTS idx_itsm_tickets_tenant ON itsm_tickets(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_itsm_comments_ticket ON itsm_comments(ticket_id, tenant_id);
CREATE INDEX IF NOT EXISTS idx_itsm_subtasks_ticket ON itsm_subtasks(ticket_id, tenant_id);
CREATE INDEX IF NOT EXISTS idx_itsm_attachments_ticket ON itsm_attachments(ticket_id, tenant_id);
CREATE INDEX IF NOT EXISTS idx_itsm_changes_tenant ON itsm_changes(tenant_id);
CREATE INDEX IF NOT EXISTS idx_process_instances_ticket ON itsm_process_instances(ticket_id);
CREATE INDEX IF NOT EXISTS idx_process_instances_tenant ON itsm_process_instances(tenant_id);
CREATE INDEX IF NOT EXISTS idx_oncall_shifts_tenant ON oncall_shifts(tenant_id, start_time DESC);
CREATE INDEX IF NOT EXISTS idx_oncall_rotations_tenant ON oncall_rotations(tenant_id);

-- 2026-09-12 补：hold_at / hold_seconds 曾缺失（列没建、Store 的 Update 也没写），
-- 导致挂起时长从未落库、SLA 挂起补偿在 PG 部署下从未生效。
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS hold_at TIMESTAMPTZ;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS hold_seconds INTEGER DEFAULT 0;
-- 2026-09-12 补：approval_chain / approval_status 曾缺失，多级审批链(顺序/并行)在
-- PG 部署下读回恒为 nil，审批退化成单人即过、投票记录全丢。
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS approval_chain TEXT;
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS approval_status TEXT;
