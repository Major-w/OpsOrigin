package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/vuln/model"
)

// PGStore Postgres 实现；Init 自建 vuln_scans / vuln_findings 两表。
type PGStore struct {
	db *sql.DB
}

// NewPGStore 构造 PG 存储。
func NewPGStore(db *sql.DB) *PGStore {
	return &PGStore{db: db}
}

// Init 建表 + 索引（幂等）。
func (s *PGStore) Init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
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
		)`)
	if err != nil {
		return fmt.Errorf("init vuln_scans table: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_vuln_scans_tenant ON vuln_scans(tenant_id, status)`)
	if err != nil {
		return fmt.Errorf("init vuln_scans index: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
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
		)`)
	if err != nil {
		return fmt.Errorf("init vuln_findings table: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_vuln_findings_scan ON vuln_findings(scan_id)`)
	if err != nil {
		return fmt.Errorf("init vuln_findings scan index: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_vuln_findings_tenant ON vuln_findings(tenant_id)`)
	if err != nil {
		return fmt.Errorf("init vuln_findings tenant index: %w", err)
	}
	return nil
}

func (s *PGStore) CreateScan(ctx context.Context, scan *model.Scan) error {
	scan.ID = uuid.New().String()
	now := time.Now()
	scan.CreatedAt = now
	if scan.SeverityCounts == nil {
		scan.SeverityCounts = map[string]int{}
	}
	countsJSON, _ := json.Marshal(scan.SeverityCounts)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vuln_scans
			(id, tenant_id, name, type, demo_mode, status, severity_counts,
			 total_findings, created_at, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		scan.ID, scan.TenantID, scan.Name, string(scan.Type),
		scan.DemoMode, string(scan.Status), string(countsJSON),
		scan.TotalFindings, scan.CreatedAt, nullableTime(scan.CompletedAt))
	if err != nil {
		return fmt.Errorf("insert scan: %w", err)
	}
	return nil
}

func (s *PGStore) UpdateScan(ctx context.Context, scan *model.Scan) error {
	countsJSON, _ := json.Marshal(scan.SeverityCounts)
	result, err := s.db.ExecContext(ctx, `
		UPDATE vuln_scans SET
			name=$1, type=$2, demo_mode=$3, status=$4,
			severity_counts=$5, total_findings=$6, completed_at=$7
		WHERE id=$8 AND tenant_id=$9`,
		scan.Name, string(scan.Type), scan.DemoMode, string(scan.Status),
		string(countsJSON), scan.TotalFindings, nullableTime(scan.CompletedAt),
		scan.ID, scan.TenantID)
	if err != nil {
		return fmt.Errorf("update scan: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) GetScan(ctx context.Context, id, tenantID string) (*model.Scan, error) {
	sc := &model.Scan{}
	var countsJSON sql.NullString
	var completedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, type, demo_mode, status, severity_counts,
		       total_findings, created_at, completed_at
		FROM vuln_scans WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(
		&sc.ID, &sc.TenantID, &sc.Name, &sc.Type, &sc.DemoMode,
		&sc.Status, &countsJSON, &sc.TotalFindings, &sc.CreatedAt, &completedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query scan: %w", err)
	}
	if completedAt.Valid {
		sc.CompletedAt = &completedAt.Time
	}
	if countsJSON.Valid && countsJSON.String != "" {
		json.Unmarshal([]byte(countsJSON.String), &sc.SeverityCounts)
	}
	return sc, nil
}

func (s *PGStore) ListScans(ctx context.Context, tenantID string) ([]*model.Scan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, type, demo_mode, status, severity_counts,
		       total_findings, created_at, completed_at
		FROM vuln_scans WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}
	defer rows.Close()
	var out []*model.Scan
	for rows.Next() {
		sc := &model.Scan{}
		var countsJSON sql.NullString
		var completedAt sql.NullTime
		if err := rows.Scan(&sc.ID, &sc.TenantID, &sc.Name, &sc.Type, &sc.DemoMode,
			&sc.Status, &countsJSON, &sc.TotalFindings, &sc.CreatedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan scan: %w", err)
		}
		if completedAt.Valid {
			sc.CompletedAt = &completedAt.Time
		}
		if countsJSON.Valid && countsJSON.String != "" {
			json.Unmarshal([]byte(countsJSON.String), &sc.SeverityCounts)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *PGStore) DeleteScan(ctx context.Context, id, tenantID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM vuln_scans WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete scan: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	// 级联清理该扫描的发现项。
	s.db.ExecContext(ctx, `DELETE FROM vuln_findings WHERE scan_id = $1`, id)
	return nil
}

func (s *PGStore) CreateFindings(ctx context.Context, findings []*model.Finding) error {
	if len(findings) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	for _, f := range findings {
		if f.ID == "" {
			f.ID = uuid.New().String()
		}
		if f.CreatedAt.IsZero() {
			f.CreatedAt = time.Now()
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO vuln_findings
				(id, scan_id, tenant_id, cve, title, description, severity,
				 affected_package, installed_version, fixed_version, reference, rank, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			f.ID, f.ScanID, f.TenantID, nullable(f.CVE), nullable(f.Title),
			nullable(f.Description), string(f.Severity), nullable(f.Affected),
			nullable(f.Installed), nullable(f.Fixed), nullable(f.Reference),
			f.Rank, f.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert finding: %w", err)
		}
	}
	return tx.Commit()
}

func (s *PGStore) ListFindings(ctx context.Context, scanID, tenantID string) ([]*model.Finding, error) {
	return s.listFindings(ctx, `scan_id = $1 AND tenant_id = $2`, scanID, tenantID)
}

func (s *PGStore) ListFindingsAll(ctx context.Context, tenantID string) ([]*model.Finding, error) {
	return s.listFindings(ctx, `tenant_id = $1`, tenantID)
}

func (s *PGStore) listFindings(ctx context.Context, where string, args ...any) ([]*model.Finding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, scan_id, tenant_id, cve, title, description, severity,
		       affected_package, installed_version, fixed_version, reference, rank, created_at
		FROM vuln_findings WHERE `+where+` ORDER BY created_at ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}
	defer rows.Close()
	var out []*model.Finding
	for rows.Next() {
		f := &model.Finding{}
		var cve, title, desc, aff, inst, fixed, ref sql.NullString
		var sev string
		if err := rows.Scan(&f.ID, &f.ScanID, &f.TenantID, &cve, &title, &desc,
			&sev, &aff, &inst, &fixed, &ref, &f.Rank, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		f.CVE = nullableStr(cve)
		f.Title = nullableStr(title)
		f.Description = nullableStr(desc)
		f.Severity = model.Severity(sev)
		f.Affected = nullableStr(aff)
		f.Installed = nullableStr(inst)
		f.Fixed = nullableStr(fixed)
		f.Reference = nullableStr(ref)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *PGStore) GetStats(ctx context.Context, tenantID string) (*model.VulnStats, error) {
	stats := &model.VulnStats{
		BySeverity: map[string]int{},
		ByStatus:   map[string]int{},
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vuln_scans WHERE tenant_id = $1`, tenantID).Scan(&stats.TotalScans); err != nil {
		return nil, fmt.Errorf("count scans: %w", err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vuln_findings WHERE tenant_id = $1`, tenantID).Scan(&stats.TotalFindings); err != nil {
		return nil, fmt.Errorf("count findings: %w", err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT severity, COUNT(*) FROM vuln_findings WHERE tenant_id = $1 GROUP BY severity`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("group findings by severity: %w", err)
	}
	for rows.Next() {
		var sev string
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan severity group: %w", err)
		}
		stats.BySeverity[sev] = n
	}
	rows.Close()
	srows, err := s.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM vuln_scans WHERE tenant_id = $1 GROUP BY status`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("group scans by status: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var st string
		var n int
		if err := srows.Scan(&st, &n); err != nil {
			return nil, fmt.Errorf("scan status group: %w", err)
		}
		stats.ByStatus[st] = n
	}
	return stats, nil
}

// --- nullable 助手 (对齐 itsm) ---

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func nullableTime(t *time.Time) *time.Time {
	return t
}
