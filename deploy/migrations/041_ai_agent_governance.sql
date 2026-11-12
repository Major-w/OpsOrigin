-- AI 数字员工治理层 P0：独立身份 / 可见性与分享 / 审批策略持久化 / 运行轨迹。
-- 与 internal/ai/agentic/pgstore.go、agentic/trajectory_pg.go、internal/ai/agent_identity.go
-- 及 internal/ai/approval_policy_store.go 的自愈 DDL 双写，幂等可重跑。
-- 所有列一律 NOT NULL DEFAULT，避免 rows.Scan 直扫 NULL 导致整条查询失败。

-- 1) 数字员工身份：存量数据 owner_id=''、visibility='public'，保持历史「全租户可见」语义。
--    新建实例由 handler 显式写 owner_id + 默认 private。
ALTER TABLE ai_agents
    ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_agents
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'public';
CREATE INDEX IF NOT EXISTS idx_ai_agents_tenant_owner
    ON ai_agents(tenant_id, owner_id, visibility);

-- 2) 数字员工分享：目标用户 / 用户组，权限 view(仅使用与查看) / edit(可改配置)。
--    比 ai_conversation_shares 多带 tenant_id，便于按租户收口，杜绝跨租户匹配。
CREATE TABLE IF NOT EXISTS ai_agent_shares (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    agent_id    TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   TEXT NOT NULL,
    permission  TEXT NOT NULL DEFAULT 'view',
    shared_by   TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, agent_id, target_type, target_id)
);
CREATE INDEX IF NOT EXISTS idx_ai_agent_shares_agent
    ON ai_agent_shares(tenant_id, agent_id);

-- 3) 审批策略：从 Engine 内存 map 迁到持久化表。
--    agent_id='' 表示租户级全局默认；sensitive_tools 用 TEXT[]，空数组表示回落内置默认名单。
CREATE TABLE IF NOT EXISTS ai_approval_policies (
    tenant_id       TEXT NOT NULL DEFAULT 'default',
    agent_id        TEXT NOT NULL DEFAULT '',
    mode            TEXT NOT NULL DEFAULT 'sensitive_only',
    sensitive_tools TEXT[] NOT NULL DEFAULT '{}'::text[],
    ask_timeout_sec INTEGER NOT NULL DEFAULT 60,
    updated_by      TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, agent_id)
);

-- 4) 运行轨迹（append-only）：一次用户请求 = 一个 turn，data 为脱敏后的完整 JSON。
--    trace_id 对齐 W3C Trace Context；独立 turn_id 供按租户回放端点定位。
CREATE TABLE IF NOT EXISTS ai_trajectory_turns (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL DEFAULT 'default',
    conversation_id TEXT NOT NULL DEFAULT '',
    agent_id        TEXT NOT NULL DEFAULT '',
    user_id         TEXT NOT NULL DEFAULT '',
    trace_id        TEXT NOT NULL DEFAULT '',
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    data            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ai_trajectory_tenant_conv
    ON ai_trajectory_turns(tenant_id, conversation_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_ai_trajectory_tenant_created
    ON ai_trajectory_turns(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ai_trajectory_trace
    ON ai_trajectory_turns(tenant_id, trace_id);
