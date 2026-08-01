-- OpsOrigin Core — 采集任务通道字段（方案 X：CMDB 统一调度）
--
-- 采集任务新增 channel 列，区分两种采集通道：
--   - server : CMDB 服务端主动拉取（如云资源 / 云监控，由 CMDB 调度器直接执行采集器）
--   - agent  : 经 Agent 通道推送上报（如主机 / 容器，由 Agent 本地采集并 push，
--              调度权仍在 CMDB，任务的「下发投影」由 agentmgr 承接到 Agent）
-- 存量任务（Phase 1/2 仅云）一律回退为 server。

ALTER TABLE cmdb_collection_task
    ADD COLUMN IF NOT EXISTS channel TEXT NOT NULL DEFAULT 'server';

CREATE INDEX IF NOT EXISTS idx_cmdb_collection_task_channel
    ON cmdb_collection_task(channel);
