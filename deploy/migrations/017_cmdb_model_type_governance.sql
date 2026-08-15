-- OpsOrigin Core — CMDB 模型类型治理字段（方案 C 分层 Taxonomy 打底）
-- 对应 internal/cmdb/model/ci.go 中 CIModelType 新增字段：
--   abstract / source / discovered_by / confidence / evidence / version / suggested_collectors
-- 运行时由 internal/cmdb/pg.Repository.Migrate 幂等执行；此处用于全新库/手动对齐。

ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS abstract             BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS source               TEXT;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS discovered_by        TEXT;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS confidence           DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS evidence            JSONB;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS version             INTEGER NOT NULL DEFAULT 1;
ALTER TABLE cmdb_ci_model_type ADD COLUMN IF NOT EXISTS suggested_collectors JSONB;

-- 存量大类（database/middleware）标记为抽象父类，禁止直接实例化（仅作继承基类）。
-- 具体引擎子类由 EnsureDiscoveryModelTypes 启动时种子补齐；存量大类 CI 已在库中的不受影响。
UPDATE cmdb_ci_model_type SET abstract = TRUE WHERE id IN ('database', 'middleware') AND abstract IS DISTINCT FROM TRUE;
