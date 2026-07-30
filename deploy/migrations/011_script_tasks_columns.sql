-- 011_script_tasks_columns.sql
-- 补全 Go 代码已引用但早期迁移遗漏的列,对齐 DB schema 与代码层。
-- 影响表:automation_scripts, automation_script_versions, agent_tasks

-- automation_scripts: 公共脚本标记与审批状态（Go model 有此字段）
ALTER TABLE automation_scripts ADD COLUMN IF NOT EXISTS public BOOLEAN DEFAULT false;
ALTER TABLE automation_scripts ADD COLUMN IF NOT EXISTS approval_status TEXT DEFAULT 'draft';

-- automation_script_versions: 版本快照同步保留公共/审批标记
ALTER TABLE automation_script_versions ADD COLUMN IF NOT EXISTS public BOOLEAN DEFAULT false;
ALTER TABLE automation_script_versions ADD COLUMN IF NOT EXISTS approval_status TEXT DEFAULT 'draft';

-- agent_tasks: 文件分发字段（taskqueue BulkCreate/processTask 引用）
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS file_id TEXT;
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS file_url TEXT;
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS file_path TEXT;
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS expected_checksum TEXT;
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS checksum_algo TEXT;
