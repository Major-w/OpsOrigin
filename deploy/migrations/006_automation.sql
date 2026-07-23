-- 006: automation 模块持久化(script / workflow / scheduler)
-- 与 internal/automation/*/pgstore.go 的 init() DDL 保持一致;
-- 此处为可独立执行的迁移脚本(部署/CI 手动或自动 apply)。

CREATE TABLE IF NOT EXISTS automation_scripts (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	content TEXT NOT NULL,
	description TEXT,
	input_params TEXT DEFAULT '[]',
	output_params TEXT DEFAULT '[]',
	timeout INTEGER DEFAULT 0,
	run_as TEXT,
	labels TEXT DEFAULT '{}',
	version INTEGER DEFAULT 1,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_automation_scripts_tenant ON automation_scripts(tenant_id);

CREATE TABLE IF NOT EXISTS automation_script_versions (
	id TEXT PRIMARY KEY,
	script_id TEXT NOT NULL,
	tenant_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	content TEXT NOT NULL,
	input_params TEXT DEFAULT '[]',
	output_params TEXT DEFAULT '[]',
	timeout INTEGER DEFAULT 0,
	run_as TEXT,
	change_note TEXT,
	created_by TEXT,
	created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_automation_script_versions_sid ON automation_script_versions(script_id, tenant_id);

CREATE TABLE IF NOT EXISTS automation_script_executions (
	id TEXT PRIMARY KEY,
	script_id TEXT NOT NULL,
	tenant_id TEXT NOT NULL,
	status TEXT NOT NULL,
	output TEXT,
	error TEXT,
	exit_code INTEGER DEFAULT 0,
	duration_ms BIGINT DEFAULT 0,
	params TEXT DEFAULT '{}',
	run_as TEXT,
	started_at TIMESTAMPTZ NOT NULL,
	finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_automation_script_executions_sid ON automation_script_executions(script_id, tenant_id);

CREATE TABLE IF NOT EXISTS automation_workflows (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT,
	steps TEXT DEFAULT '[]',
	input_params TEXT DEFAULT '[]',
	labels TEXT DEFAULT '{}',
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_automation_workflows_tenant ON automation_workflows(tenant_id);

CREATE TABLE IF NOT EXISTS automation_workflow_executions (
	id TEXT PRIMARY KEY,
	workflow_id TEXT NOT NULL,
	tenant_id TEXT NOT NULL,
	status TEXT NOT NULL,
	agent_ids TEXT DEFAULT '[]',
	inputs TEXT DEFAULT '{}',
	step_results TEXT DEFAULT '{}',
	started_at TIMESTAMPTZ,
	finished_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL,
	pending_step_id TEXT,
	context TEXT DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_automation_wf_exec_wid ON automation_workflow_executions(workflow_id, tenant_id);

CREATE TABLE IF NOT EXISTS automation_workflow_approvals (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	execution_id TEXT NOT NULL,
	step_id TEXT NOT NULL,
	step_name TEXT,
	approvers TEXT DEFAULT '[]',
	message TEXT,
	status TEXT NOT NULL,
	decided_by TEXT,
	comment TEXT,
	created_at TIMESTAMPTZ NOT NULL,
	decided_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_automation_wf_appr_tid ON automation_workflow_approvals(tenant_id);

CREATE TABLE IF NOT EXISTS automation_scheduled_tasks (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	name TEXT NOT NULL,
	cron_expr TEXT NOT NULL,
	task_type TEXT NOT NULL,
	target_id TEXT NOT NULL,
	agent_ids TEXT DEFAULT '[]',
	inputs TEXT DEFAULT '{}',
	enabled BOOLEAN DEFAULT FALSE,
	labels TEXT DEFAULT '{}',
	last_run_at TIMESTAMPTZ,
	next_run_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_automation_sched_tasks_tenant ON automation_scheduled_tasks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_automation_sched_tasks_enabled ON automation_scheduled_tasks(enabled);
