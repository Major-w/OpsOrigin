-- OpsOrigin Core — PostgreSQL 初始迁移
-- 创建 CMDB + ITSM + Outbox 核心表

-- ============================================================
-- CMDB: CI 模型类型定义
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_ci_model_type (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    description TEXT,
    parent_id   TEXT REFERENCES cmdb_ci_model_type(id),
    schema      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmdb_ci_model_type_tenant ON cmdb_ci_model_type(tenant_id);

-- ============================================================
-- CMDB: CI 资产实例
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_ci (
    id          TEXT PRIMARY KEY,
    type_id     TEXT NOT NULL,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    name        TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'active',
    attributes  JSONB NOT NULL DEFAULT '{}',
    labels      JSONB NOT NULL DEFAULT '{}',
    channel     INT NOT NULL DEFAULT 100,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmdb_ci_tenant ON cmdb_ci(tenant_id);
CREATE INDEX idx_cmdb_ci_type ON cmdb_ci(type_id);
CREATE INDEX idx_cmdb_ci_status ON cmdb_ci(status);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE cmdb_ci ENABLE ROW LEVEL SECURITY;
CREATE POLICY cmdb_ci_tenant_isolation ON cmdb_ci
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- CMDB: CI 属性变更审计日志
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_ci_audit (
    id          BIGSERIAL PRIMARY KEY,
    ci_id       TEXT NOT NULL REFERENCES cmdb_ci(id),
    attr_name   TEXT NOT NULL,
    old_value   TEXT,
    new_value   TEXT,
    channel     INT NOT NULL DEFAULT 100,
    trace_id    TEXT NOT NULL DEFAULT 'unknown',
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmdb_ci_audit_ci ON cmdb_ci_audit(ci_id);
CREATE INDEX idx_cmdb_ci_audit_trace ON cmdb_ci_audit(trace_id);

-- ============================================================
-- CMDB: CI 关系（拓扑连线）
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_ci_relation (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL DEFAULT 'default',
    source_id     TEXT NOT NULL,
    target_id     TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    properties    JSONB DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cmdb_ci_relation_tenant ON cmdb_ci_relation(tenant_id);
CREATE INDEX idx_cmdb_ci_relation_source ON cmdb_ci_relation(source_id);
CREATE INDEX idx_cmdb_ci_relation_target ON cmdb_ci_relation(target_id);

-- ============================================================
-- Outbox: 异步消息表 (PG → Dgraph 同步)
-- ============================================================
CREATE TABLE IF NOT EXISTS cmdb_outbox (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL DEFAULT 'default',
    event_type   TEXT NOT NULL,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '',
    trace_id     TEXT NOT NULL DEFAULT 'unknown',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX idx_cmdb_outbox_unprocessed ON cmdb_outbox(processed_at) WHERE processed_at IS NULL;
CREATE INDEX idx_cmdb_outbox_created ON cmdb_outbox(created_at);

-- ============================================================
-- ITSM: 工单表
-- ============================================================
CREATE TABLE IF NOT EXISTS itsm_tickets (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL DEFAULT 'default',
    title           TEXT NOT NULL,
    description     TEXT,
    risk_level      TEXT NOT NULL DEFAULT 'low',
    status          TEXT NOT NULL DEFAULT 'draft',
    change_plan     TEXT,
    created_by      TEXT NOT NULL,
    created_by_type TEXT NOT NULL DEFAULT 'HUMAN',
    approved_by     TEXT,
    reject_reason   TEXT,
    webhook_url     TEXT,
    labels          JSONB DEFAULT '{}',
    trace_id        TEXT NOT NULL DEFAULT 'unknown',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approved_at     TIMESTAMPTZ,
    executed_at     TIMESTAMPTZ
);

CREATE INDEX idx_itsm_tickets_tenant ON itsm_tickets(tenant_id);
CREATE INDEX idx_itsm_tickets_status ON itsm_tickets(status);
CREATE INDEX idx_itsm_tickets_risk ON itsm_tickets(risk_level);

-- ============================================================
-- ITSM: 工单状态变更历史
-- ============================================================
CREATE TABLE IF NOT EXISTS itsm_ticket_events (
    id          BIGSERIAL PRIMARY KEY,
    ticket_id   TEXT NOT NULL REFERENCES itsm_tickets(id),
    from_status TEXT,
    to_status   TEXT NOT NULL,
    changed_by  TEXT NOT NULL,
    comment     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_itsm_ticket_events_ticket ON itsm_ticket_events(ticket_id);

-- ============================================================
-- 审计: 全栈操作审计日志
-- ============================================================
CREATE TABLE IF NOT EXISTS audit_events (
    id          TEXT PRIMARY KEY,
    trace_id    TEXT NOT NULL,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    event_type  TEXT NOT NULL,
    actor_type  TEXT NOT NULL,
    actor_id    TEXT NOT NULL,
    actor_ticket_id TEXT,
    target_type TEXT,
    target_id   TEXT,
    target_name TEXT,
    action      TEXT NOT NULL,
    request     JSONB,
    response    JSONB,
    success     BOOLEAN NOT NULL DEFAULT TRUE,
    error       TEXT,
    ip_address  TEXT,
    user_agent  TEXT,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_events_trace ON audit_events(trace_id);
CREATE INDEX idx_audit_events_tenant ON audit_events(tenant_id);
CREATE INDEX idx_audit_events_timestamp ON audit_events(timestamp DESC);
