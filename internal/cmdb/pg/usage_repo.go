package pg

import (
	"context"
	"fmt"
	"time"
)

// ——— CMDB 数据消费分析（usage analytics）———
//
// 记录维度与聚合口径见 deploy/migrations/022_cmdb_usage.sql。
// 所有查询均以 (tenant_id, created_at 下界) 为必备过滤条件，SQL 全部走 $N 占位符，
// 时间桶粒度经白名单映射（hour/day/week），不接受外部字符串拼接。

// UsageEvent 是一条待落库的 CMDB 调用记录（由采集中间件批量提交）。
type UsageEvent struct {
	TenantID   string
	UserID     string
	Method     string
	Route      string
	Path       string
	CIID       string
	StatusCode int
	LatencyMS  int
}

// EnsureUsageSchema 建表 + 索引（幂等）。与 022_cmdb_usage.sql 保持一致。
func (r *Repository) EnsureUsageSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cmdb_usage_event (
			id BIGSERIAL PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			user_id TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT 'GET',
			route TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			ci_id TEXT NOT NULL DEFAULT '',
			status_code INT NOT NULL DEFAULT 200,
			latency_ms INT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cmdb_usage_created ON cmdb_usage_event (tenant_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_cmdb_usage_route ON cmdb_usage_event (tenant_id, route, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_cmdb_usage_ci ON cmdb_usage_event (tenant_id, ci_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_cmdb_usage_status ON cmdb_usage_event (tenant_id, status_code, created_at DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensure cmdb_usage_event schema: %w", err)
		}
	}
	return nil
}

// RecordUsageBatch 批量写入调用记录。单事务 + 预编译语句，减少往返。
func (r *Repository) RecordUsageBatch(ctx context.Context, events []UsageEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin usage tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 提交成功后 Rollback 为空操作

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO cmdb_usage_event
		(tenant_id, user_id, method, route, path, ci_id, status_code, latency_ms, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())`)
	if err != nil {
		return fmt.Errorf("prepare usage insert: %w", err)
	}
	defer stmt.Close()

	for _, e := range events {
		if _, err := stmt.ExecContext(ctx,
			e.TenantID, e.UserID, e.Method, e.Route, e.Path, e.CIID, e.StatusCode, e.LatencyMS); err != nil {
			return fmt.Errorf("insert usage event: %w", err)
		}
	}
	return tx.Commit()
}

// PurgeUsageBefore 清理过期调用记录（保留窗口由调用方决定），返回删除行数。
func (r *Repository) PurgeUsageBefore(ctx context.Context, tenantID string, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM cmdb_usage_event WHERE tenant_id=$1 AND created_at < $2`, tenantID, before)
	if err != nil {
		return 0, fmt.Errorf("purge usage events: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// UsagePoint 是一个时间桶的调用量/错误量。
type UsagePoint struct {
	TS         time.Time `json:"ts"`
	Count      int       `json:"count"`
	ErrorCount int       `json:"error_count"`
}

// UsageBreakdown 是「按某维度」的计数（方法/状态码等）。
type UsageBreakdown struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// UsageSummary 是总览：总量、错误率、延迟分位、时间趋势与维度拆分。
type UsageSummary struct {
	WindowHours  int              `json:"window_hours"`
	TotalCalls   int              `json:"total_calls"`
	ErrorCalls   int              `json:"error_calls"`
	ErrorRate    float64          `json:"error_rate"`
	AvgLatencyMS float64          `json:"avg_latency_ms"`
	P95LatencyMS float64          `json:"p95_latency_ms"`
	CIHitCalls   int              `json:"ci_hit_calls"`
	DistinctCIs  int              `json:"distinct_cis"`
	Series       []UsagePoint     `json:"series"`
	ByMethod     []UsageBreakdown `json:"by_method"`
	ByStatus     []UsageBreakdown `json:"by_status"`
}

// UsageSummary 聚合指定时间窗口内的总览指标。
// bucket 取值 hour/day/week（其它值回落 hour）。
func (r *Repository) UsageSummary(ctx context.Context, tenantID string, since time.Time, bucket string) (*UsageSummary, error) {
	out := &UsageSummary{Series: []UsagePoint{}, ByMethod: []UsageBreakdown{}, ByStatus: []UsageBreakdown{}}

	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status_code >= 400),
		       COALESCE(AVG(latency_ms), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
		       COUNT(*) FILTER (WHERE ci_id <> ''),
		       COUNT(DISTINCT NULLIF(ci_id, ''))
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2`, tenantID, since,
	).Scan(&out.TotalCalls, &out.ErrorCalls, &out.AvgLatencyMS, &out.P95LatencyMS, &out.CIHitCalls, &out.DistinctCIs); err != nil {
		return nil, fmt.Errorf("usage summary: %w", err)
	}
	if out.TotalCalls > 0 {
		out.ErrorRate = float64(out.ErrorCalls) / float64(out.TotalCalls)
	}

	// bucket 经白名单映射，杜绝把外部输入拼进 SQL。
	trunc := "hour"
	switch bucket {
	case "day", "week":
		trunc = bucket
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT date_trunc($3, created_at) AS bucket,
		       COUNT(*),
		       COUNT(*) FILTER (WHERE status_code >= 400)
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2
		GROUP BY bucket ORDER BY bucket`, tenantID, since, trunc)
	if err != nil {
		return nil, fmt.Errorf("usage series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p UsagePoint
		if err := rows.Scan(&p.TS, &p.Count, &p.ErrorCount); err != nil {
			return nil, fmt.Errorf("scan usage series: %w", err)
		}
		out.Series = append(out.Series, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage series: %w", err)
	}

	if out.ByMethod, err = r.usageBreakdown(ctx, tenantID, since, "method"); err != nil {
		return nil, err
	}
	if out.ByStatus, err = r.usageBreakdown(ctx, tenantID, since, "status_code"); err != nil {
		return nil, err
	}
	return out, nil
}

// usageBreakdown 按白名单维度分组计数。dim 只允许 method / status_code。
func (r *Repository) usageBreakdown(ctx context.Context, tenantID string, since time.Time, dim string) ([]UsageBreakdown, error) {
	var col string
	switch dim {
	case "method":
		col = "method"
	case "status_code":
		col = "status_code::text"
	default:
		return nil, fmt.Errorf("unsupported usage dimension %q", dim)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+col+` AS label, COUNT(*) AS cnt
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2
		GROUP BY label ORDER BY cnt DESC LIMIT 12`, tenantID, since)
	if err != nil {
		return nil, fmt.Errorf("usage breakdown %s: %w", dim, err)
	}
	defer rows.Close()
	out := []UsageBreakdown{}
	for rows.Next() {
		var b UsageBreakdown
		if err := rows.Scan(&b.Label, &b.Count); err != nil {
			return nil, fmt.Errorf("scan usage breakdown: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UsageRouteHotspot 是「哪个接口被调得最多」。
type UsageRouteHotspot struct {
	Route        string  `json:"route"`
	Method       string  `json:"method"`
	Calls        int     `json:"calls"`
	Errors       int     `json:"errors"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
	Share        float64 `json:"share"`
}

// UsageCIHotspot 是「哪个配置项被读得最多」（热点数据）。
type UsageCIHotspot struct {
	CIID     string    `json:"ci_id"`
	Name     string    `json:"name"`
	TypeID   string    `json:"type_id"`
	Calls    int       `json:"calls"`
	Errors   int       `json:"errors"`
	LastSeen time.Time `json:"last_seen"`
}

// UsageUserHotspot 是「谁在用」（调用方排行）。
type UsageUserHotspot struct {
	UserID string `json:"user_id"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

// UsageHotspots 是热点数据集合：接口 / 配置项 / 调用方。
type UsageHotspots struct {
	Routes []UsageRouteHotspot `json:"routes"`
	CIs    []UsageCIHotspot    `json:"cis"`
	Users  []UsageUserHotspot  `json:"users"`
}

// UsageHotspots 聚合指定窗口内的热点排行（各取 TopN）。
func (r *Repository) UsageHotspots(ctx context.Context, tenantID string, since time.Time, limit int) (*UsageHotspots, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	out := &UsageHotspots{
		Routes: []UsageRouteHotspot{},
		CIs:    []UsageCIHotspot{},
		Users:  []UsageUserHotspot{},
	}

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cmdb_usage_event WHERE tenant_id=$1 AND created_at >= $2`,
		tenantID, since).Scan(&total); err != nil {
		return nil, fmt.Errorf("usage hotspots total: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT route, method, COUNT(*),
		       COUNT(*) FILTER (WHERE status_code >= 400),
		       COALESCE(AVG(latency_ms), 0)
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2 AND route <> ''
		GROUP BY route, method ORDER BY COUNT(*) DESC LIMIT $3`, tenantID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("usage route hotspots: %w", err)
	}
	for rows.Next() {
		var h UsageRouteHotspot
		if err := rows.Scan(&h.Route, &h.Method, &h.Calls, &h.Errors, &h.AvgLatencyMS); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan route hotspot: %w", err)
		}
		if total > 0 {
			h.Share = float64(h.Calls) / float64(total)
		}
		out.Routes = append(out.Routes, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate route hotspots: %w", err)
	}

	// 热点配置项：LEFT JOIN 取名称/模型，CI 已删除时仍保留调用计数。
	ciRows, err := r.db.QueryContext(ctx, `
		SELECT u.ci_id,
		       COALESCE(c.name, ''), COALESCE(c.type_id, ''),
		       COUNT(*) AS calls,
		       COUNT(*) FILTER (WHERE u.status_code >= 400),
		       MAX(u.created_at)
		FROM cmdb_usage_event u
		LEFT JOIN cmdb_ci c ON c.id = u.ci_id AND c.tenant_id = u.tenant_id
		WHERE u.tenant_id=$1 AND u.created_at >= $2 AND u.ci_id <> ''
		GROUP BY u.ci_id, c.name, c.type_id
		ORDER BY calls DESC LIMIT $3`, tenantID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("usage ci hotspots: %w", err)
	}
	defer ciRows.Close()
	for ciRows.Next() {
		var h UsageCIHotspot
		if err := ciRows.Scan(&h.CIID, &h.Name, &h.TypeID, &h.Calls, &h.Errors, &h.LastSeen); err != nil {
			return nil, fmt.Errorf("scan ci hotspot: %w", err)
		}
		out.CIs = append(out.CIs, h)
	}
	if err := ciRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ci hotspots: %w", err)
	}

	userRows, err := r.db.QueryContext(ctx, `
		SELECT user_id, COUNT(*), COUNT(*) FILTER (WHERE status_code >= 400)
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2 AND user_id <> ''
		GROUP BY user_id ORDER BY COUNT(*) DESC LIMIT $3`, tenantID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("usage user hotspots: %w", err)
	}
	defer userRows.Close()
	for userRows.Next() {
		var h UsageUserHotspot
		if err := userRows.Scan(&h.UserID, &h.Calls, &h.Errors); err != nil {
			return nil, fmt.Errorf("scan user hotspot: %w", err)
		}
		out.Users = append(out.Users, h)
	}
	return out, userRows.Err()
}

// UsageErrorGroup 是按 (route, method, status) 聚合的错误组。
type UsageErrorGroup struct {
	Route  string `json:"route"`
	Method string `json:"method"`
	Status int    `json:"status_code"`
	Count  int    `json:"count"`
}

// UsageErrorSample 是一条真实错误样本（用于定位具体请求）。
type UsageErrorSample struct {
	Route     string    `json:"route"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status_code"`
	UserID    string    `json:"user_id"`
	CIID      string    `json:"ci_id"`
	LatencyMS int       `json:"latency_ms"`
	At        time.Time `json:"at"`
}

// UsageErrors 是错误频率视图：趋势 + 分组 + 最近样本。
type UsageErrors struct {
	ErrorCalls int                `json:"error_calls"`
	Series     []UsagePoint       `json:"series"`
	ByRoute    []UsageErrorGroup  `json:"by_route"`
	ByStatus   []UsageBreakdown   `json:"by_status"`
	Recent     []UsageErrorSample `json:"recent"`
}

// UsageErrors 聚合指定窗口内的错误频率明细。
// bucket 取值 hour/day/week（其它值回落 hour）。
func (r *Repository) UsageErrors(ctx context.Context, tenantID string, since time.Time, bucket string, limit int) (*UsageErrors, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	out := &UsageErrors{
		Series:   []UsagePoint{},
		ByRoute:  []UsageErrorGroup{},
		ByStatus: []UsageBreakdown{},
		Recent:   []UsageErrorSample{},
	}

	trunc := "hour"
	switch bucket {
	case "day", "week":
		trunc = bucket
	}

	// 错误趋势：只取 status>=400；桶要补零由前端处理，这里只回有数据的桶。
	rows, err := r.db.QueryContext(ctx, `
		SELECT date_trunc($3, created_at) AS bucket, COUNT(*)
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2 AND status_code >= 400
		GROUP BY bucket ORDER BY bucket`, tenantID, since, trunc)
	if err != nil {
		return nil, fmt.Errorf("usage error series: %w", err)
	}
	total := 0
	for rows.Next() {
		var p UsagePoint
		if err := rows.Scan(&p.TS, &p.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan error series: %w", err)
		}
		p.ErrorCount = p.Count
		total += p.Count
		out.Series = append(out.Series, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate error series: %w", err)
	}
	out.ErrorCalls = total

	grpRows, err := r.db.QueryContext(ctx, `
		SELECT route, method, status_code, COUNT(*)
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2 AND status_code >= 400
		GROUP BY route, method, status_code
		ORDER BY COUNT(*) DESC LIMIT $3`, tenantID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("usage error groups: %w", err)
	}
	for grpRows.Next() {
		var g UsageErrorGroup
		if err := grpRows.Scan(&g.Route, &g.Method, &g.Status, &g.Count); err != nil {
			grpRows.Close()
			return nil, fmt.Errorf("scan error group: %w", err)
		}
		out.ByRoute = append(out.ByRoute, g)
	}
	grpRows.Close()
	if err := grpRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate error groups: %w", err)
	}

	// 按状态码拆分错误（区分 400/401/403/404/5xx 等语义）。
	if out.ByStatus, err = r.usageBreakdownFilteredErrors(ctx, tenantID, since); err != nil {
		return nil, err
	}

	sampleRows, err := r.db.QueryContext(ctx, `
		SELECT route, method, path, status_code, user_id, ci_id, latency_ms, created_at
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2 AND status_code >= 400
		ORDER BY created_at DESC LIMIT $3`, tenantID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("usage error samples: %w", err)
	}
	defer sampleRows.Close()
	for sampleRows.Next() {
		var s UsageErrorSample
		if err := sampleRows.Scan(&s.Route, &s.Method, &s.Path, &s.Status, &s.UserID, &s.CIID, &s.LatencyMS, &s.At); err != nil {
			return nil, fmt.Errorf("scan error sample: %w", err)
		}
		out.Recent = append(out.Recent, s)
	}
	return out, sampleRows.Err()
}

// usageBreakdownFilteredErrors 只统计错误状态码的分布。
func (r *Repository) usageBreakdownFilteredErrors(ctx context.Context, tenantID string, since time.Time) ([]UsageBreakdown, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT status_code::text AS label, COUNT(*) AS cnt
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2 AND status_code >= 400
		GROUP BY label ORDER BY cnt DESC`, tenantID, since)
	if err != nil {
		return nil, fmt.Errorf("usage error status breakdown: %w", err)
	}
	defer rows.Close()
	out := []UsageBreakdown{}
	for rows.Next() {
		var b UsageBreakdown
		if err := rows.Scan(&b.Label, &b.Count); err != nil {
			return nil, fmt.Errorf("scan error status breakdown: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// TopRoutesByStatus 返回状态码族（如 4xx / 5xx）占比，供前端告警阈值展示。
// 这里用一条聚合语句同时算总数、4xx、5xx，避免多次往返。
func (r *Repository) TopRoutesByStatus(ctx context.Context, tenantID string, since time.Time) (total, clientErr, serverErr int, err error) {
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status_code >= 400 AND status_code < 500),
		       COUNT(*) FILTER (WHERE status_code >= 500)
		FROM cmdb_usage_event
		WHERE tenant_id=$1 AND created_at >= $2`, tenantID, since,
	).Scan(&total, &clientErr, &serverErr); err != nil {
		return 0, 0, 0, fmt.Errorf("usage status family: %w", err)
	}
	return total, clientErr, serverErr, nil
}
