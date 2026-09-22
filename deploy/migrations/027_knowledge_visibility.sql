-- 027_knowledge_visibility.sql — 知识库可见性归属列（权限感知检索的前置数据）
--
-- 背景：knowledge_articles 只有 view_permission（all/team/owner），但**没有任何归属列**
-- —— 既没有作者列，也没有团队列。于是 team / owner 两种可见性在检索侧根本无法判定归属，
-- 只能 fail-closed（一律不召回），导致「团队可见」的知识对 AI 永久不可见：
-- 作者以为发给了团队，实际谁都检索不到。
--
-- 本文件补齐两个判定依据（与 internal/knowledge/pgstore.go init() 的自愈 DDL 等价、幂等）：
--   author_id        —— owner 可见性的唯一判定依据（提问者 = 作者）
--   owner_group_ids  —— team 可见性的判定依据（提问者所属团队与该数组有交集即可见）
--                       多值数组以支持「一篇 SOP 同时给运维组和网络组」。
--
-- 设计要点：owner_group_ids 用 `NOT NULL DEFAULT '{}'::text[]` 把 NULL 与空串两种"空态"
-- 在 schema 层消灭 —— view_permission 此前的缺陷正是空串被 fail-closed 判定当成"受限"，
-- 使已发布文章静默退出检索（见 026 末尾的存量回填）。
-- PG 11+ 对已存在表加带默认值的 NOT NULL 列是 metadata-only，不重写表。
--
-- 存量 author_id 不回填：没有可靠来源可以推断历史文章的作者，且"回填给谁"本身就是越权决策。
-- 未回填的效果是新语义下这些文章仍 AI 不可见 —— 与变更前行为一致，零回归。

ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS author_id TEXT DEFAULT '';
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS owner_group_ids TEXT[] NOT NULL DEFAULT '{}'::text[];

-- 兜底：若该列曾被以可空形态建出（ADD COLUMN IF NOT EXISTS 会跳过上面的 NOT NULL 定义），
-- 先把 NULL 行归一为空数组，避免数组重叠谓词遇到 NULL 行为不可预期。
UPDATE knowledge_articles SET owner_group_ids = '{}'::text[] WHERE owner_group_ids IS NULL;
UPDATE knowledge_articles SET author_id = '' WHERE author_id IS NULL;

-- 可见性过滤的复合索引：检索谓词恒定按 (tenant_id, view_permission) 收窄。
CREATE INDEX IF NOT EXISTS idx_knowledge_articles_scope
    ON knowledge_articles(tenant_id, view_permission);

-- team 归属的数组重叠匹配（owner_group_ids && $n::text[]）走 GIN。
CREATE INDEX IF NOT EXISTS idx_knowledge_articles_owner_groups
    ON knowledge_articles USING gin(owner_group_ids);
