package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

// 告警规则持久化：monitor_alert_rules 表（原纯内存 aggregator.rules，重启全丢）。
// 与 #720 数据源同源问题, 此处镜像 source_store.go 范式。
// db 为 nil 时退化为纯内存（无后端环境）。

func ensureRulesTable(ctx context.Context, db *sql.DB) {
	if db == nil {
		return
	}
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS monitor_alert_rules (
			id               TEXT PRIMARY KEY,
			tenant_id        TEXT NOT NULL,
			name             TEXT NOT NULL,
			metric_name      TEXT NOT NULL DEFAULT '',
			operator         TEXT NOT NULL DEFAULT '',
			threshold        DOUBLE PRECISION NOT NULL DEFAULT 0,
			duration_sec     INT NOT NULL DEFAULT 0,
			severity         TEXT NOT NULL DEFAULT 'warning',
			labels           JSONB NOT NULL DEFAULT '{}'::jsonb,
			enabled          BOOLEAN NOT NULL DEFAULT true,
			type             TEXT NOT NULL DEFAULT 'threshold',
			expr             TEXT NOT NULL DEFAULT '',
			eval_interval_sec INT NOT NULL DEFAULT 60,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		slog.Warn("monitor: ensure monitor_alert_rules table failed", "error", err)
	}
}

func saveRuleToDB(ctx context.Context, db *sql.DB, r *model.AlertRule) error {
	if db == nil {
		return nil
	}
	labelsJSON, err := json.Marshal(r.Labels)
	if err != nil {
		return fmt.Errorf("marshal rule labels: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO monitor_alert_rules
			(id, tenant_id, name, metric_name, operator, threshold, duration_sec, severity, labels, enabled, type, expr, eval_interval_sec, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (id) DO UPDATE SET
			tenant_id=EXCLUDED.tenant_id, name=EXCLUDED.name, metric_name=EXCLUDED.metric_name,
			operator=EXCLUDED.operator, threshold=EXCLUDED.threshold, duration_sec=EXCLUDED.duration_sec,
			severity=EXCLUDED.severity, labels=EXCLUDED.labels, enabled=EXCLUDED.enabled,
			type=EXCLUDED.type, expr=EXCLUDED.expr, eval_interval_sec=EXCLUDED.eval_interval_sec`,
		r.ID, r.TenantID, r.Name, r.MetricName, r.Operator, r.Threshold,
		int(r.Duration.Seconds()), string(r.Severity), string(labelsJSON), r.Enabled,
		r.Type, r.Expr, r.EvalIntervalSec, time.Now())
	if err != nil {
		return fmt.Errorf("save alert rule: %w", err)
	}
	return nil
}

func deleteRuleFromDB(ctx context.Context, db *sql.DB, id string) error {
	if db == nil {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM monitor_alert_rules WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete alert rule: %w", err)
	}
	return nil
}

func loadRulesFromDB(ctx context.Context, db *sql.DB) ([]*model.AlertRule, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, tenant_id, name, metric_name, operator, threshold, duration_sec,
		       severity, labels, enabled, type, expr, eval_interval_sec
		FROM monitor_alert_rules`)
	if err != nil {
		return nil, fmt.Errorf("load alert rules: %w", err)
	}
	defer rows.Close()
	var out []*model.AlertRule
	for rows.Next() {
		var r model.AlertRule
		var labelsJSON []byte
		var durSec int
		var sev string
		var typ, expr string
		var evalInt int
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &r.MetricName, &r.Operator,
			&r.Threshold, &durSec, &sev, &labelsJSON, &r.Enabled, &typ, &expr, &evalInt); err != nil {
			return nil, fmt.Errorf("scan alert rule: %w", err)
		}
		r.Duration = time.Duration(durSec) * time.Second
		r.Severity = model.AlertSeverity(sev)
		r.Type = typ
		r.Expr = expr
		r.EvalIntervalSec = evalInt
		if len(labelsJSON) > 0 {
			_ = json.Unmarshal(labelsJSON, &r.Labels)
		}
		if r.Labels == nil {
			r.Labels = map[string]string{}
		}
		out = append(out, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load alert rules rows: %w", err)
	}
	slog.Info("monitor: alert rules loaded from db", "count", len(out))
	return out, nil
}
