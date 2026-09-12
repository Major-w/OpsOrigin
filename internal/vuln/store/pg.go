package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/vuln/model"
)

// PGStore Postgres 实现；Init 自建/自愈 vuln_scans / vuln_findings 两表。
// 本仓无迁移 runner,统一「自愈 DDL + deploy/migrations 留一份 SQL」。
type PGStore struct {
	db *sql.DB
}

// NewPGStore 构造 PG 存储。
func NewPGStore(db *sql.DB) *PGStore {
	return &PGStore{db: db}
}

// scanCols 扫描列清单(与 scanScanRow 顺序一致)。
const scanCols = `id, tenant_id, name, type, schedule, demo_mode, status, severity_counts,
	total_findings, targets, failed_targets, error_detail, agent_id, db_updated_at,
	scan_options, created_at, next_run_at, last_run_at, completed_at`

// findingCols 发现项列清单(与 scanFindingRow 顺序一致)。
const findingCols = `id, scan_id, tenant_id, cve, title, description, severity, cvss,
	affected_package, installed_version, fixed_version, reference, remediation, rank,
	host, dedupe_key, status, note, ignore_until, occurrences, first_seen, last_seen, created_at`

// Init 建表 + 列自愈 + 索引(幂等)。
func (s *PGStore) Init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS vuln_scans (
			id               TEXT PRIMARY KEY,
			tenant_id        TEXT NOT NULL,
			name             TEXT NOT NULL,
			type             TEXT NOT NULL,
			schedule         TEXT NOT NULL DEFAULT 'once',
			demo_mode        BOOLEAN NOT NULL DEFAULT FALSE,
			status           TEXT NOT NULL,
			severity_counts  TEXT DEFAULT '{}',
			total_findings   INTEGER NOT NULL DEFAULT 0,
			targets          INTEGER NOT NULL DEFAULT 0,
			failed_targets   INTEGER NOT NULL DEFAULT 0,
			error_detail     TEXT,
			agent_id         TEXT,
			db_updated_at    TIMESTAMPTZ,
			created_at       TIMESTAMPTZ NOT NULL,
			next_run_at      TIMESTAMPTZ,
			last_run_at      TIMESTAMPTZ,
			completed_at     TIMESTAMPTZ
		)`,
		// 历史库列自愈(新增字段一律走 IF NOT EXISTS,老库平滑升级)。
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS schedule TEXT NOT NULL DEFAULT 'once'`,
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS targets INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS failed_targets INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS error_detail TEXT`,
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS agent_id TEXT`,
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS db_updated_at TIMESTAMPTZ`,
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS scan_options TEXT`,
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS next_run_at TIMESTAMPTZ`,
		`ALTER TABLE vuln_scans ADD COLUMN IF NOT EXISTS last_run_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS idx_vuln_scans_tenant ON vuln_scans(tenant_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_vuln_scans_due ON vuln_scans(next_run_at)
			WHERE schedule IS NOT NULL AND schedule <> 'once'`,

		`CREATE TABLE IF NOT EXISTS vuln_findings (
			id                TEXT PRIMARY KEY,
			scan_id           TEXT NOT NULL,
			tenant_id         TEXT NOT NULL,
			cve               TEXT,
			title             TEXT,
			description       TEXT,
			severity          TEXT,
			cvss              DOUBLE PRECISION NOT NULL DEFAULT 0,
			affected_package  TEXT,
			installed_version TEXT,
			fixed_version     TEXT,
			reference         TEXT,
			remediation       TEXT,
			rank              INTEGER NOT NULL DEFAULT 0,
			host              TEXT,
			dedupe_key        TEXT,
			status            TEXT NOT NULL DEFAULT 'open',
			note              TEXT,
			ignore_until      TIMESTAMPTZ,
			occurrences       INTEGER NOT NULL DEFAULT 1,
			first_seen        TIMESTAMPTZ,
			last_seen         TIMESTAMPTZ,
			created_at        TIMESTAMPTZ NOT NULL
		)`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS cvss DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS remediation TEXT`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS host TEXT`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS dedupe_key TEXT`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'open'`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS note TEXT`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS ignore_until TIMESTAMPTZ`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS occurrences INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS first_seen TIMESTAMPTZ`,
		`ALTER TABLE vuln_findings ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ`,
		// 老数据回填生命周期字段(仅在 dedupe_key 为空时执行一次)。
		`UPDATE vuln_findings SET
			dedupe_key  = md5(tenant_id || '|' || coalesce(cve,'') || '|' || coalesce(affected_package,'') || '|' || coalesce(installed_version,'') || '|' || coalesce(host,'')),
			first_seen  = COALESCE(first_seen, created_at),
			last_seen   = COALESCE(last_seen, created_at),
			status      = COALESCE(NULLIF(status,''), 'open'),
			occurrences = CASE WHEN occurrences IS NULL OR occurrences < 1 THEN 1 ELSE occurrences END
		 WHERE dedupe_key IS NULL`,
		// 去重:同一 (tenant_id, dedupe_key) 只保留最新一条(老快照模型会产出重复)。
		`DELETE FROM vuln_findings WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY tenant_id, dedupe_key ORDER BY created_at DESC) AS rn
				FROM vuln_findings WHERE dedupe_key IS NOT NULL
			) t WHERE t.rn > 1
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_vuln_findings_dedupe
			ON vuln_findings(tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_vuln_findings_scan ON vuln_findings(scan_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vuln_findings_tenant ON vuln_findings(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_vuln_findings_tenant_status ON vuln_findings(tenant_id, status)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("init vuln schema: %w (stmt: %s)", err, firstLine(q))
		}
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	if len(s) > 90 {
		return s[:90]
	}
	return s
}

// ---------- 扫描 ----------

func (s *PGStore) CreateScan(ctx context.Context, scan *model.Scan) error {
	scan.ID = uuid.New().String()
	now := time.Now()
	scan.CreatedAt = now
	if scan.SeverityCounts == nil {
		scan.SeverityCounts = map[string]int{}
	}
	if scan.Schedule == "" {
		scan.Schedule = model.ScheduleOnce
	}
	if scan.Status == "" {
		scan.Status = model.ScanStatusRunning
	}
	countsJSON, _ := json.Marshal(scan.SeverityCounts)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vuln_scans
			(id, tenant_id, name, type, schedule, demo_mode, status, severity_counts,
			 total_findings, targets, failed_targets, error_detail, agent_id, db_updated_at,
			 created_at, next_run_at, last_run_at, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		scan.ID, scan.TenantID, scan.Name, string(scan.Type), string(scan.Schedule),
		scan.DemoMode, string(scan.Status), string(countsJSON),
		scan.TotalFindings, scan.Targets, scan.FailedTargets, nullable(scan.ErrorDetail),
		nullable(scan.AgentID), scan.DBUpdatedAt,
		scan.CreatedAt, scan.NextRunAt, scan.LastRunAt, nullableTime(scan.CompletedAt))
	if err != nil {
		return fmt.Errorf("insert scan: %w", err)
	}
	return nil
}

func (s *PGStore) UpdateScan(ctx context.Context, scan *model.Scan) error {
	countsJSON, _ := json.Marshal(scan.SeverityCounts)
	result, err := s.db.ExecContext(ctx, `
		UPDATE vuln_scans SET
			name=$1, type=$2, schedule=$3, demo_mode=$4, status=$5,
			severity_counts=$6, total_findings=$7, targets=$8, failed_targets=$9,
			error_detail=$10, agent_id=$11, db_updated_at=$12, scan_options=$13,
			next_run_at=$14, last_run_at=$15, completed_at=$16
		WHERE id=$17 AND tenant_id=$18`,
		scan.Name, string(scan.Type), string(scan.Schedule), scan.DemoMode, string(scan.Status),
		string(countsJSON), scan.TotalFindings, scan.Targets, scan.FailedTargets,
		nullable(scan.ErrorDetail), nullable(scan.AgentID), scan.DBUpdatedAt, nullable(scan.Options),
		scan.NextRunAt, scan.LastRunAt, nullableTime(scan.CompletedAt),
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

// scanScanRow 从一行结果读取 Scan(列顺序与 scanCols 一致)。
func scanScanRow(sc *model.Scan, row interface{ Scan(dest ...any) error }) error {
	var countsJSON, errDetail, agentID, options sql.NullString
	var dbUpdatedAt, nextRunAt, lastRunAt, completedAt sql.NullTime
	if err := row.Scan(&sc.ID, &sc.TenantID, &sc.Name, &sc.Type, &sc.Schedule, &sc.DemoMode,
		&sc.Status, &countsJSON, &sc.TotalFindings, &sc.Targets, &sc.FailedTargets,
		&errDetail, &agentID, &dbUpdatedAt, &options, &sc.CreatedAt, &nextRunAt, &lastRunAt, &completedAt); err != nil {
		return err
	}
	sc.ErrorDetail = nullableStr(errDetail)
	sc.AgentID = nullableStr(agentID)
	sc.Options = nullableStr(options)
	if dbUpdatedAt.Valid {
		sc.DBUpdatedAt = &dbUpdatedAt.Time
	}
	if nextRunAt.Valid {
		sc.NextRunAt = &nextRunAt.Time
	}
	if lastRunAt.Valid {
		sc.LastRunAt = &lastRunAt.Time
	}
	if completedAt.Valid {
		sc.CompletedAt = &completedAt.Time
	}
	if countsJSON.Valid && countsJSON.String != "" {
		json.Unmarshal([]byte(countsJSON.String), &sc.SeverityCounts)
	}
	return nil
}

func (s *PGStore) GetScan(ctx context.Context, id, tenantID string) (*model.Scan, error) {
	sc := &model.Scan{}
	err := scanScanRow(sc, s.db.QueryRowContext(ctx,
		`SELECT `+scanCols+` FROM vuln_scans WHERE id = $1 AND tenant_id = $2`, id, tenantID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query scan: %w", err)
	}
	return sc, nil
}

func (s *PGStore) ListScans(ctx context.Context, tenantID string, f ScanFilter) ([]*model.Scan, int, error) {
	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	// status/type 为白名单值(调用方已校验),仅作等值比较且值走 $N。
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.Type != "" {
		args = append(args, f.Type)
		where = append(where, fmt.Sprintf("type = $%d", len(args)))
	}
	cond := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vuln_scans WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count scans: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	q := fmt.Sprintf(`SELECT %s FROM vuln_scans WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		scanCols, cond, len(args)-1, len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list scans: %w", err)
	}
	defer rows.Close()
	out := []*model.Scan{}
	for rows.Next() {
		sc := &model.Scan{}
		if err := scanScanRow(sc, rows); err != nil {
			return nil, 0, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, sc)
	}
	return out, total, rows.Err()
}

// ListDueScans 跨租户取到期计划扫描(仅调度器调用,无用户上下文)。
func (s *PGStore) ListDueScans(ctx context.Context, now time.Time) ([]*model.Scan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+scanCols+` FROM vuln_scans
		WHERE schedule IS NOT NULL AND schedule <> 'once'
		  AND status <> 'running'
		  AND next_run_at IS NOT NULL AND next_run_at <= $1
		ORDER BY next_run_at ASC LIMIT 50`, now)
	if err != nil {
		return nil, fmt.Errorf("list due scans: %w", err)
	}
	defer rows.Close()
	var out []*model.Scan
	for rows.Next() {
		sc := &model.Scan{}
		if err := scanScanRow(sc, rows); err != nil {
			return nil, fmt.Errorf("scan due row: %w", err)
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
	// 仅删除「未被其他扫描引用」的发现项(去重模型下同一实例归属最近一次扫描)。
	s.db.ExecContext(ctx, `DELETE FROM vuln_findings WHERE scan_id = $1 AND tenant_id = $2`, id, tenantID)
	return nil
}

// ---------- 发现项 ----------

// UpsertFindings 以 (tenant_id, dedupe_key) 为唯一实例批量写入(单事务)。
func (s *PGStore) UpsertFindings(ctx context.Context, findings []*model.Finding) error {
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
		now := time.Now()
		if f.CreatedAt.IsZero() {
			f.CreatedAt = now
		}
		if f.FirstSeen.IsZero() {
			f.FirstSeen = now
		}
		if f.LastSeen.IsZero() {
			f.LastSeen = now
		}
		if f.Status == "" {
			f.Status = model.FindingOpen
		}
		if f.Occurrences < 1 {
			f.Occurrences = 1
		}
		if f.DedupeKey == "" {
			// 兜底:调用方未算键时用 id 保证唯一(不与其他实例合并)。
			f.DedupeKey = f.ID
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO vuln_findings
				(id, scan_id, tenant_id, cve, title, description, severity, cvss,
				 affected_package, installed_version, fixed_version, reference, remediation, rank,
				 host, dedupe_key, status, note, ignore_until, occurrences, first_seen, last_seen, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
			ON CONFLICT (tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO UPDATE SET
				scan_id           = EXCLUDED.scan_id,
				title             = EXCLUDED.title,
				description       = EXCLUDED.description,
				severity          = EXCLUDED.severity,
				cvss              = EXCLUDED.cvss,
				fixed_version     = EXCLUDED.fixed_version,
				reference         = EXCLUDED.reference,
				remediation       = EXCLUDED.remediation,
				rank              = EXCLUDED.rank,
				occurrences       = vuln_findings.occurrences + 1,
				last_seen         = EXCLUDED.last_seen,
				status            = CASE
					WHEN vuln_findings.status IN ('ignored','false_positive')
					     AND (vuln_findings.ignore_until IS NULL OR vuln_findings.ignore_until > NOW())
					  THEN vuln_findings.status
					ELSE 'open' END`,
			f.ID, f.ScanID, f.TenantID, nullable(f.CVE), nullable(f.Title),
			nullable(f.Description), string(f.Severity), f.CVSS,
			nullable(f.Affected), nullable(f.Installed), nullable(f.Fixed), nullable(f.Reference),
			nullable(f.Remediation), f.Rank,
			nullable(f.Host), f.DedupeKey, string(f.Status), nullable(f.Note),
			f.IgnoreUntil, f.Occurrences, f.FirstSeen, f.LastSeen, f.CreatedAt)
		if err != nil {
			return fmt.Errorf("upsert finding: %w", err)
		}
	}
	return tx.Commit()
}

func scanFindingRow(f *model.Finding, row interface{ Scan(dest ...any) error }) error {
	var cve, title, desc, aff, inst, fixed, ref, rem, host, dkey, note sql.NullString
	var sev, status string
	var ignoreUntil, firstSeen, lastSeen sql.NullTime
	if err := row.Scan(&f.ID, &f.ScanID, &f.TenantID, &cve, &title, &desc, &sev, &f.CVSS,
		&aff, &inst, &fixed, &ref, &rem, &f.Rank, &host, &dkey, &status, &note,
		&ignoreUntil, &f.Occurrences, &firstSeen, &lastSeen, &f.CreatedAt); err != nil {
		return err
	}
	f.CVE = nullableStr(cve)
	f.Title = nullableStr(title)
	f.Description = nullableStr(desc)
	f.Severity = model.Severity(sev)
	f.Affected = nullableStr(aff)
	f.Installed = nullableStr(inst)
	f.Fixed = nullableStr(fixed)
	f.Reference = nullableStr(ref)
	f.Remediation = nullableStr(rem)
	f.Host = nullableStr(host)
	f.DedupeKey = nullableStr(dkey)
	f.Status = model.FindingStatus(status)
	f.Note = nullableStr(note)
	if ignoreUntil.Valid {
		f.IgnoreUntil = &ignoreUntil.Time
	}
	if firstSeen.Valid {
		f.FirstSeen = firstSeen.Time
	}
	if lastSeen.Valid {
		f.LastSeen = lastSeen.Time
	}
	return nil
}

// listFindings 统一过滤 + 分页查询。scanID 为空表示不限扫描。
func (s *PGStore) listFindings(ctx context.Context, scanID, tenantID string, fl FindingFilter) ([]*model.Finding, int, error) {
	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	if scanID != "" {
		args = append(args, scanID)
		where = append(where, fmt.Sprintf("scan_id = $%d", len(args)))
	}
	// severity/status 仅接受白名单值;host/search 走参数化,search 用单参数转义通配。
	if fl.Severity != "" {
		args = append(args, fl.Severity)
		where = append(where, fmt.Sprintf("severity = $%d", len(args)))
	}
	if fl.Status != "" {
		args = append(args, fl.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if fl.Host != "" {
		args = append(args, fl.Host)
		where = append(where, fmt.Sprintf("host = $%d", len(args)))
	}
	if fl.Search != "" {
		args = append(args, "%"+escapeLike(fl.Search)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(cve ILIKE $%d ESCAPE '\\' OR affected_package ILIKE $%d ESCAPE '\\' OR title ILIKE $%d ESCAPE '\\')", n, n, n))
	}
	cond := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vuln_findings WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count findings: %w", err)
	}

	limit := fl.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	offset := fl.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	q := fmt.Sprintf(`SELECT %s FROM vuln_findings WHERE %s
		ORDER BY rank DESC, cvss DESC, last_seen DESC NULLS LAST
		LIMIT $%d OFFSET $%d`, findingCols, cond, len(args)-1, len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list findings: %w", err)
	}
	defer rows.Close()
	out := []*model.Finding{}
	for rows.Next() {
		f := &model.Finding{}
		if err := scanFindingRow(f, rows); err != nil {
			return nil, 0, fmt.Errorf("scan finding: %w", err)
		}
		out = append(out, f)
	}
	return out, total, rows.Err()
}

// escapeLike 转义 LIKE 通配符,避免用户输入 %/_ 变成通配。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (s *PGStore) ListFindings(ctx context.Context, scanID, tenantID string, fl FindingFilter) ([]*model.Finding, int, error) {
	return s.listFindings(ctx, scanID, tenantID, fl)
}

func (s *PGStore) ListFindingsAll(ctx context.Context, tenantID string, fl FindingFilter) ([]*model.Finding, int, error) {
	return s.listFindings(ctx, "", tenantID, fl)
}

func (s *PGStore) GetFinding(ctx context.Context, id, tenantID string) (*model.Finding, error) {
	f := &model.Finding{}
	err := scanFindingRow(f, s.db.QueryRowContext(ctx,
		`SELECT `+findingCols+` FROM vuln_findings WHERE id = $1 AND tenant_id = $2`, id, tenantID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query finding: %w", err)
	}
	return f, nil
}

func (s *PGStore) UpdateFinding(ctx context.Context, id, tenantID string, upd FindingUpdate) (*model.Finding, error) {
	sets := []string{}
	args := []any{}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if upd.Status != nil {
		add("status", string(*upd.Status))
	}
	if upd.Note != nil {
		add("note", nullable(*upd.Note))
	}
	if upd.ClearIgnore {
		sets = append(sets, "ignore_until = NULL")
	} else if upd.IgnoreUntil != nil {
		add("ignore_until", *upd.IgnoreUntil)
	}
	if len(sets) == 0 {
		return s.GetFinding(ctx, id, tenantID)
	}
	args = append(args, id, tenantID)
	q := fmt.Sprintf(`UPDATE vuln_findings SET %s WHERE id = $%d AND tenant_id = $%d`,
		strings.Join(sets, ", "), len(args)-1, len(args))
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("update finding: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetFinding(ctx, id, tenantID)
}

// CloseMissedFindings 复扫验证:hosts 范围内容 last_seen 早于 before 的 open 项置为 fixed。
func (s *PGStore) CloseMissedFindings(ctx context.Context, tenantID string, hosts []string, before time.Time) (int, error) {
	if len(hosts) == 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE vuln_findings SET status = 'fixed'
		WHERE tenant_id = $1 AND status = 'open'
		  AND host = ANY($2::text[])
		  AND last_seen IS NOT NULL AND last_seen < $3`,
		tenantID, hosts, before)
	if err != nil {
		return 0, fmt.Errorf("close missed findings: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *PGStore) GetStats(ctx context.Context, tenantID string) (*model.VulnStats, error) {
	stats := &model.VulnStats{
		BySeverity:      map[string]int{},
		ByStatus:        map[string]int{},
		ByFindingStatus: map[string]int{},
		OpenBySeverity:  map[string]int{},
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vuln_scans WHERE tenant_id = $1`, tenantID).Scan(&stats.TotalScans); err != nil {
		return nil, fmt.Errorf("count scans: %w", err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vuln_findings WHERE tenant_id = $1`, tenantID).Scan(&stats.TotalFindings); err != nil {
		return nil, fmt.Errorf("count findings: %w", err)
	}
	// 最近一轮扫描的覆盖率(用于提示结论可信度)。
	var tt, ft sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT targets, failed_targets FROM vuln_scans
		WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 1`, tenantID).Scan(&tt, &ft); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("latest scan coverage: %w", err)
	}
	stats.ScannedTargets = int(tt.Int64)
	stats.FailedTargets = int(ft.Int64)

	if err := s.groupCount(ctx, `
		SELECT severity, COUNT(*) FROM vuln_findings WHERE tenant_id = $1 GROUP BY severity`,
		tenantID, stats.BySeverity); err != nil {
		return nil, err
	}
	if err := s.groupCount(ctx, `
		SELECT status, COUNT(*) FROM vuln_scans WHERE tenant_id = $1 GROUP BY status`,
		tenantID, stats.ByStatus); err != nil {
		return nil, err
	}
	if err := s.groupCount(ctx, `
		SELECT status, COUNT(*) FROM vuln_findings WHERE tenant_id = $1 GROUP BY status`,
		tenantID, stats.ByFindingStatus); err != nil {
		return nil, err
	}
	if err := s.groupCount(ctx, `
		SELECT severity, COUNT(*) FROM vuln_findings WHERE tenant_id = $1 AND status = 'open' GROUP BY severity`,
		tenantID, stats.OpenBySeverity); err != nil {
		return nil, err
	}
	stats.OpenFindings = stats.ByFindingStatus[string(model.FindingOpen)]
	return stats, nil
}

// groupCount 执行 `<key,count>` 聚合查询并写入 map。
func (s *PGStore) groupCount(ctx context.Context, q, tenantID string, dst map[string]int) error {
	rows, err := s.db.QueryContext(ctx, q, tenantID)
	if err != nil {
		return fmt.Errorf("group count: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k sql.NullString
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return fmt.Errorf("scan group: %w", err)
		}
		dst[nullableStr(k)] = n
	}
	return rows.Err()
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
