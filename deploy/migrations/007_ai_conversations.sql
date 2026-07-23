-- 007: AI 对话历史持久化
-- 与 internal/ai/agentic/convstore.go 的 init() DDL 保持一致。

CREATE TABLE IF NOT EXISTS ai_conversations (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	agent_id TEXT,
	title TEXT,
	messages TEXT DEFAULT '[]',
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ai_conversations_tenant ON ai_conversations(tenant_id, updated_at DESC);
