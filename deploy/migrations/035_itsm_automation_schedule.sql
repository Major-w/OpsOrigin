-- 035: ITSM 自动化触发器定时调度配置（P3-1 定时触发流程）
-- 触发器表已有自愈 DDL 补列，此处双写保持 deploy/migrations 与代码同步。
ALTER TABLE itsm_automation_rules ADD COLUMN IF NOT EXISTS schedule TEXT NOT NULL DEFAULT '';
