-- 024_vuln_lifecycle.sql
-- 漏洞扫描闭环改造:扫描覆盖率/失败诊断 + 发现项生命周期(状态/首末次出现/去重键)。
--
-- 说明:本仓无迁移 runner,运行时由 internal/vuln/store.PGStore.Init 自愈执行等价 DDL;
-- 本文件作为版本化留档,供人工/CI 环境预先执行。全部语句幂等。
--
-- 背景(修复前的三处缺陷):
--   1. 扫描失败被吞成「已完成 0 漏洞」——现在 vuln_scans 记录 targets/failed_targets/error_detail,
--      status 增加 failed / partial,无法再与「扫了但干净」混淆。
--   2. 漏洞库从不更新却宣称在同步——现在 db_updated_at 记录 Agent 端漏洞库实际时间。
--   3. 每条漏洞都显示「已修复」——现在 vuln_findings.status 为真实处置状态。
--   + 支持 daily/weekly 计划任务:schedule / next_run_at / last_run_at / scan_options。

-- ---------- vuln_scans ----------
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS schedule TEXT NOT NULL DEFAULT 'once';
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS targets INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS failed_targets INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS error_detail TEXT;
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS agent_id TEXT;
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS db_updated_at TIMESTAMPTZ;
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS next_run_at TIMESTAMPTZ;
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS last_run_at TIMESTAMPTZ;
-- scan_options 保存创建时的执行参数,供计划任务按原参数重跑。刻意不落仓库密码。
ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS scan_options TEXT;

CREATE INDEX IF NOT EXISTS idx_vuln_scans_tenant ON vuln_scans(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_vuln_scans_due ON vuln_scans(next_run_at)
    WHERE schedule IS NOT NULL AND schedule <> 'once';

-- ---------- vuln_findings ----------
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS cvss DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS remediation TEXT;
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS host TEXT;
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS dedupe_key TEXT;
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'open';
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS note TEXT;
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS ignore_until TIMESTAMPTZ;
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS occurrences INTEGER NOT NULL DEFAULT 1;
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS first_seen TIMESTAMPTZ;
ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ;

-- 老数据回填生命周期字段(仅对未回填过的行执行)。
UPDATE vuln_findings SET
    dedupe_key  = md5(tenant_id || '|' || coalesce(cve,'') || '|' || coalesce(affected_package,'') || '|' || coalesce(installed_version,'') || '|' || coalesce(host,'')),
    first_seen  = COALESCE(first_seen, created_at),
    last_seen   = COALESCE(last_seen, created_at),
    status      = COALESCE(NULLIF(status,''), 'open'),
    occurrences = CASE WHEN occurrences IS NULL OR occurrences < 1 THEN 1 ELSE occurrences END
 WHERE dedupe_key IS NULL;

-- 去重:老模型「每次扫描一行快照」会在同一 dedupe_key 上产生多行,只保留最新一条。
DELETE FROM vuln_findings WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (PARTITION BY tenant_id, dedupe_key ORDER BY created_at DESC) AS rn
        FROM vuln_findings WHERE dedupe_key IS NOT NULL
    ) t WHERE t.rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_vuln_findings_dedupe
    ON vuln_findings(tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_vuln_findings_scan ON vuln_findings(scan_id);
CREATE INDEX IF NOT EXISTS idx_vuln_findings_tenant ON vuln_findings(tenant_id);
CREATE INDEX IF NOT EXISTS idx_vuln_findings_tenant_status ON vuln_findings(tenant_id, status);
