-- 016_platform_messages.sql
-- 站内消息(per-user 收件箱)。与 notification 模块的「外部渠道外发」互补:
-- 此表存的是归属到具体用户、带已读状态的站内消息,支撑顶栏未读角标与消息中心。
--
-- 收件人双标识说明:
--   user_id  — 鉴权链主体 ID(UUID),平台侧权威标识
--   username — 业务主体用户名,ITSM 的 assignee / created_by / @提及沿用它
-- 投递方拿到哪个填哪个,查询时两者任一命中即算本人消息。

CREATE TABLE IF NOT EXISTS platform_messages (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT        NOT NULL DEFAULT 'default',
    user_id     TEXT        NOT NULL DEFAULT '',
    username    TEXT        NOT NULL DEFAULT '',
    category    TEXT        NOT NULL DEFAULT 'system',
    level       TEXT        NOT NULL DEFAULT 'info',
    title       TEXT        NOT NULL DEFAULT '',
    content     TEXT        NOT NULL DEFAULT '',
    link        TEXT        NOT NULL DEFAULT '',
    ref_type    TEXT        NOT NULL DEFAULT '',
    ref_id      TEXT        NOT NULL DEFAULT '',
    actor       TEXT        NOT NULL DEFAULT '',
    is_read     BOOLEAN     NOT NULL DEFAULT FALSE,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_platform_messages_inbox
    ON platform_messages (tenant_id, user_id, is_read, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_platform_messages_inbox_name
    ON platform_messages (tenant_id, username, is_read, created_at DESC);
