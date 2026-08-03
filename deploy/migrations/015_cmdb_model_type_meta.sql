-- OpsOrigin Core — CMDB 模型类型扩展（分类/图标/二级分类/菜单显示/内置标记）
-- 对应 P0 建模层补齐：资源分类 + 图标 + 二级分类 + 在菜单显示 + 内置模型治理。
-- 运行时由 internal/cmdb/pg.Repository.Migrate 幂等执行；此处用于全新库/手动对齐。

ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS category     TEXT;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS sub_category TEXT;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS icon         TEXT;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS show_in_menu BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS builtin      BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS author       TEXT;

-- 存量模型回填默认分类（platform）
UPDATE cmdb_ci_model_type SET category = 'platform' WHERE category IS NULL OR category = '';
