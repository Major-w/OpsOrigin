-- Feature 3 漏洞扫描：vuln_scans + vuln_findings 两表 + 索引。
-- 注：vuln PGStore.Init 已自建表，本迁移为显式/可审计版本。

CREATE TABLE IF NOT EXISTS vuln_scans (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL,
    name             TEXT NOT NULL,
    type             TEXT NOT NULL,
    demo_mode        BOOLEAN NOT NULL DEFAULT FALSE,
    status           TEXT NOT NULL,
    severity_counts  TEXT DEFAULT '{}',
    total_findings   INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL,
    completed_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_vuln_scans_tenant ON vuln_scans(tenant_id, status);

CREATE TABLE IF NOT EXISTS vuln_findings (
    id                TEXT PRIMARY KEY,
    scan_id           TEXT NOT NULL,
    tenant_id         TEXT NOT NULL,
    cve               TEXT,
    title             TEXT,
    description       TEXT,
    severity          TEXT,
    affected_package  TEXT,
    installed_version TEXT,
    fixed_version     TEXT,
    reference         TEXT,
    rank              INTEGER NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_vuln_findings_scan ON vuln_findings(scan_id);
CREATE INDEX IF NOT EXISTS idx_vuln_findings_tenant ON vuln_findings(tenant_id);
