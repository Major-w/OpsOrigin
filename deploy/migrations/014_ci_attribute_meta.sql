-- P1-1: 字段级数据来源 / 置信度（attribute_meta）
--
-- 设计要点（与用户确认）：
--   一个 CI 的不同字段来源往往不同（hostname 来自 Agent、public_ip 来自云 API、
--   cpu 来自监控、owner 来自人工），因此来源/置信度必须做到「字段级」。
--   采用「平行存储」而非把元数据塞进 attributes 同一个 map：
--     · 不污染业务属性，不干扰 validateAttributes 校验、前端展示、Dgraph 同步；
--     · attributes 单体 JSON 读写零改动，Dgraph 拓扑只吃整体 attributes，不受影响；
--     · attribute_meta 键与 attributes 一一对应，按字段记录 source/confidence/last_seen_at。
--
-- 采集路径（cmdb/collection）会按采集任务推导 source（cloud_api / agent）逐字段写入；
-- 人工编辑路径按 ChannelWeight 兜底标记为 manual（置信度 1.0）。

ALTER TABLE cmdb_ci ADD COLUMN IF NOT EXISTS attribute_meta JSONB NOT NULL DEFAULT '{}'::jsonb;

-- GIN 索引便于按来源/置信度做筛选（如"列出所有人工维护的字段""低置信度字段巡检"）。
CREATE INDEX IF NOT EXISTS idx_cmdb_ci_attr_meta ON cmdb_ci USING gin (attribute_meta);
