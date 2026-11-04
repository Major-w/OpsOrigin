-- 031: ITSM 工单可读工单号 (TK-YYYY-NNNN)
-- 与 internal/itsm/store/pg.go 的 Init/migrate 自愈 DDL 保持双写一致。
ALTER TABLE itsm_tickets ADD COLUMN IF NOT EXISTS number TEXT;

CREATE TABLE IF NOT EXISTS itsm_ticket_counters (
    tenant_id TEXT    NOT NULL,
    year      INTEGER NOT NULL,
    seq       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, year)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_itsm_ticket_counters_ty
    ON itsm_ticket_counters(tenant_id, year);

-- 历史工单回填(按创建时间稳定排序)，并同步计数器避免与新号冲突。
-- 仅对未分配工单号的历史工单执行；幂等重跑安全。
WITH ranked AS (
    SELECT id, tenant_id,
           ROW_NUMBER() OVER (PARTITION BY tenant_id ORDER BY created_at ASC, id ASC) AS rn
    FROM itsm_tickets
    WHERE number IS NULL OR number = ''
)
UPDATE itsm_tickets t
SET number = 'TK-' || EXTRACT(YEAR FROM t.created_at)::int || '-' || LPAD(r.rn::text, 4, '0')
FROM ranked r
WHERE t.id = r.id;

-- 将计数器补齐到已存在历史工单的最大值，避免新工单号与回填号冲突。
INSERT INTO itsm_ticket_counters (tenant_id, year, seq)
SELECT tenant_id, EXTRACT(YEAR FROM created_at)::int AS y, COUNT(*) AS c
FROM itsm_tickets
WHERE number IS NOT NULL AND number <> ''
GROUP BY tenant_id, y
ON CONFLICT (tenant_id, year) DO UPDATE SET seq = GREATEST(itsm_ticket_counters.seq, EXCLUDED.seq);
