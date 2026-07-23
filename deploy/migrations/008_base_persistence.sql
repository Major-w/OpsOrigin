-- 008: 基础功能持久化(notification / iam / keystore)

CREATE TABLE IF NOT EXISTS notification_channels (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	config TEXT DEFAULT '{}',
	enabled BOOLEAN DEFAULT TRUE,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notification_channels_tenant ON notification_channels(tenant_id);

CREATE TABLE IF NOT EXISTS notification_policies (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT NOT NULL,
	event TEXT NOT NULL,
	channel_ids TEXT DEFAULT '[]',
	template TEXT,
	quiet_hours TEXT,
	enabled BOOLEAN DEFAULT TRUE,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notification_policies_tenant ON notification_policies(tenant_id);

CREATE TABLE IF NOT EXISTS notification_history (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	channel_id TEXT,
	event TEXT,
	title TEXT,
	content TEXT,
	status TEXT NOT NULL,
	error TEXT,
	created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notification_history_tenant ON notification_history(tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS iam_roles (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT NOT NULL,
	display_name TEXT,
	description TEXT,
	permissions TEXT DEFAULT '[]',
	is_built_in BOOLEAN DEFAULT FALSE,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_iam_roles_tenant ON iam_roles(tenant_id);

CREATE TABLE IF NOT EXISTS iam_groups (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT,
	user_ids TEXT DEFAULT '[]',
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_iam_groups_tenant ON iam_groups(tenant_id);

CREATE TABLE IF NOT EXISTS iam_user_roles (
	user_id TEXT NOT NULL,
	tenant_id TEXT NOT NULL,
	role_id TEXT NOT NULL,
	PRIMARY KEY (user_id, tenant_id, role_id)
);

CREATE TABLE IF NOT EXISTS keystore_entries (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	content TEXT NOT NULL,
	fingerprint TEXT,
	size INTEGER DEFAULT 0,
	algorithm TEXT,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_keystore_entries_tenant ON keystore_entries(tenant_id, type);
