-- OpsOrigin Core — CMDB 模型级关系声明
-- 在 cmdb_ci_model_type 上新增 relations JSONB 列，存放该模型声明的关联定义
-- （名称 / 目标模型 / 基数 1:1|1:N|M:N / 反向名）。与 schema 同构，按模型持久化。
-- 默认空数组，保证旧行与 SELECT 兼容。

ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS relations JSONB NOT NULL DEFAULT '[]';
