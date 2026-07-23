-- 009: Agent 管理持久化(agentmgr)

CREATE TABLE IF NOT EXISTS agent_hosts (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT,
	type TEXT,
	address TEXT,
	port INTEGER DEFAULT 0,
	os_type TEXT,
	os_version TEXT,
	auth_type TEXT,
	ssh_user TEXT,
	status TEXT DEFAULT 'offline',
	proxy_id TEXT,
	install_method TEXT,
	version TEXT,
	cpu TEXT,
	memory TEXT,
	disk TEXT,
	hostname TEXT,
	labels TEXT DEFAULT '{}',
	environment TEXT,
	capabilities TEXT DEFAULT '[]',
	ips TEXT DEFAULT '[]',
	last_seen TIMESTAMPTZ,
	last_heartbeat TIMESTAMPTZ,
	first_seen TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_hosts_tenant ON agent_hosts(tenant_id);

CREATE TABLE IF NOT EXISTS agent_proxies (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT,
	address TEXT,
	port INTEGER DEFAULT 0,
	status TEXT DEFAULT 'offline',
	agent_count INTEGER DEFAULT 0,
	version TEXT,
	last_seen TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_proxies_tenant ON agent_proxies(tenant_id);

CREATE TABLE IF NOT EXISTS agent_tasks (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	action TEXT,
	script TEXT,
	params TEXT DEFAULT '{}',
	timeout INTEGER DEFAULT 0,
	run_as TEXT,
	status TEXT DEFAULT 'pending',
	output TEXT,
	exit_code INTEGER,
	duration_ms BIGINT DEFAULT 0,
	error TEXT,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_agent ON agent_tasks(agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_tenant ON agent_tasks(tenant_id);
