-- 032: 流程定义新增「工单编号组成规则」列 number_rule (JSONB)。
-- 与 internal/itsm/process/pgstore.go 的 migrate() 自愈 DDL 双写：
--   ALTER TABLE itsm_processes ADD COLUMN IF NOT EXISTS number_rule JSONB DEFAULT '{}'::jsonb
-- 规则结构见 process.TicketNumberRule：{ enabled, prefix, date_format, separator, seq_width, seq_scope }。
-- 为空/未启用时工单号回退全局默认 TK-YYYY-NNNN。

ALTER TABLE itsm_processes ADD COLUMN IF NOT EXISTS number_rule JSONB DEFAULT '{}'::jsonb;
