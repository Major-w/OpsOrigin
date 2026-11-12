-- 用户密码策略：有效期检查基准（密码最后修改时间）。
-- 与 internal/user/store/postgres.go Init 自愈 DDL 双写，幂等可重跑。
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ NULL;
-- 老数据按创建时间起算（不过期策略下无影响；开启有效期后老密码按 created_at 计）
UPDATE users SET password_changed_at = created_at WHERE password_changed_at IS NULL;
