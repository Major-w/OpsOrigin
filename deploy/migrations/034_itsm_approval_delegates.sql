-- 034: 审批委托/代理人（对标 ServiceNow Delegate）
-- principal 在 [start_at, end_at) 生效期内把自己的审批权委托给 delegate；
-- 追加式语义：表决名册 = 原审批人 + 在期代理人，原审批人仍可投票。
CREATE TABLE IF NOT EXISTS itsm_approval_delegates (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL DEFAULT 'default',
    principal TEXT NOT NULL,
    delegate TEXT NOT NULL,
    start_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    end_at TIMESTAMPTZ,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    note TEXT DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_itsm_delegates_tenant ON itsm_approval_delegates(tenant_id);
