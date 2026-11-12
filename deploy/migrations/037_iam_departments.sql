-- OpsOrigin Core — 组织架构（部门树）表
-- AD/LDAP 同步自动镜像（source=ldap）+ 本地维护（source=local）。
-- 成员绑定经 users.department（部门名）关联，本表为组织维度实体（树形）。
-- 幂等可重跑；运行时 iam pgstore.init 亦有同款 DDL（双写）。

CREATE TABLE IF NOT EXISTS iam_departments (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    parent_id   TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'local',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_iam_departments_tenant ON iam_departments(tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_iam_departments_tenant_name ON iam_departments(tenant_id, name);
