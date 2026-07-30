-- OpsOrigin Core — 用户管理表
-- 创建用户表和相关索引

-- ============================================================
-- 用户表
-- ============================================================
CREATE TABLE IF NOT EXISTS users (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    username    TEXT NOT NULL,
    password    TEXT NOT NULL,
    email       TEXT,
    role        TEXT NOT NULL DEFAULT 'viewer',
    status      TEXT NOT NULL DEFAULT 'active',
    last_login  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, username)
);

CREATE INDEX idx_users_tenant ON users(tenant_id);
CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
CREATE POLICY users_tenant_isolation ON users
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- 用户会话表（用于Refresh Token和Token黑名单）
-- ============================================================
CREATE TABLE IF NOT EXISTS user_sessions (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id   TEXT NOT NULL DEFAULT 'default',
    token       TEXT NOT NULL,
    refresh_token TEXT,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX idx_user_sessions_user ON user_sessions(user_id);
CREATE INDEX idx_user_sessions_token ON user_sessions(token);
CREATE INDEX idx_user_sessions_refresh ON user_sessions(refresh_token);

-- 行级安全策略 (RLS) — 多租户隔离
ALTER TABLE user_sessions ENABLE ROW LEVEL SECURITY;
CREATE POLICY user_sessions_tenant_isolation ON user_sessions
    USING (tenant_id = current_setting('app.tenant_id')::TEXT);

-- ============================================================
-- 权限表（用于RBAC）
-- ============================================================
CREATE TABLE IF NOT EXISTS permissions (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- 角色权限关联表
-- ============================================================
CREATE TABLE IF NOT EXISTS role_permissions (
    role        TEXT NOT NULL,
    permission_id TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role, permission_id)
);

CREATE INDEX idx_role_permissions_role ON role_permissions(role);

-- ============================================================
-- 初始化默认权限
-- ============================================================
INSERT INTO permissions (id, name, description) VALUES
    ('perm_cmdb_read', 'cmdb:read', '读取CMDB数据'),
    ('perm_cmdb_write', 'cmdb:write', '修改CMDB数据'),
    ('perm_cmdb_admin', 'cmdb:admin', '管理CMDB模型和配置'),
    ('perm_monitor_read', 'monitor:read', '读取监控数据'),
    ('perm_monitor_write', 'monitor:write', '配置监控规则'),
    ('perm_itsm_read', 'itsm:read', '读取工单数据'),
    ('perm_itsm_write', 'itsm:write', '创建和处理工单'),
    ('perm_itsm_approve', 'itsm:approve', '审批工单'),
    ('perm_user_read', 'user:read', '读取用户信息'),
    ('perm_user_write', 'user:write', '管理用户'),
    ('perm_agent_read', 'agent:read', '读取Agent信息'),
    ('perm_agent_write', 'agent:write', '管理Agent'),
    ('perm_audit_read', 'audit:read', '读取审计日志'),
    ('perm_system_admin', 'system:admin', '系统管理')
ON CONFLICT (name) DO NOTHING;

-- ============================================================
-- 初始化默认角色权限
-- ============================================================
-- admin角色：所有权限
INSERT INTO role_permissions (role, permission_id)
SELECT 'admin', id FROM permissions
ON CONFLICT DO NOTHING;

-- operator角色：大部分权限，除了系统管理
INSERT INTO role_permissions (role, permission_id)
SELECT 'operator', id FROM permissions WHERE name != 'system:admin'
ON CONFLICT DO NOTHING;

-- viewer角色：只读权限
INSERT INTO role_permissions (role, permission_id)
SELECT 'viewer', id FROM permissions WHERE name LIKE '%:read'
ON CONFLICT DO NOTHING;

-- ============================================================
-- 初始化默认管理员用户（密码: admin123，使用bcrypt hash）
-- ============================================================
INSERT INTO users (id, tenant_id, username, password, email, role, status)
VALUES ('user-admin-001', 'default', 'admin', '$2a$10$z1zhCyKMAPr4k6.7Cjk77eVyoZEKxPCLMcXuJx5q9iiBxj22Fa1Li', 'admin@example.com', 'admin', 'active')
ON CONFLICT (tenant_id, username) DO NOTHING;