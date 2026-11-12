-- OpsOrigin Core — 用户部门字段
-- ITSM 提单人属性派单（submitter_attr 数据联动）依赖：提单人 → 部门(门店) → 门店IT。
-- 幂等可重跑；运行时 user store.Init 亦有同款自愈 DDL（双写）。

ALTER TABLE users ADD COLUMN IF NOT EXISTS department TEXT NOT NULL DEFAULT '';
