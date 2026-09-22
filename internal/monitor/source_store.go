package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/opsorigin/core/internal/monitor/collect"
)

// 外部指标采集源持久化：monitor_sources 表（原纯内存 map，重启全丢）。
// db 为 nil 时退化为纯内存（无后端环境）。

func ensureSourcesTable(ctx context.Context, db *sql.DB) {
	if db == nil {
		return
	}
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS monitor_sources (
			id           TEXT PRIMARY KEY,
			tenant_id    TEXT NOT NULL,
			name         TEXT NOT NULL,
			type         TEXT NOT NULL,
			address      TEXT NOT NULL,
			port         INT  NOT NULL DEFAULT 0,
			path         TEXT NOT NULL DEFAULT '',
			keys         TEXT NOT NULL DEFAULT '[]',
			interval_sec INT  NOT NULL DEFAULT 30,
			ci_id        TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		slog.Warn("monitor: ensure monitor_sources table failed", "error", err)
	}
}

func saveSourceToDB(ctx context.Context, db *sql.DB, cfg collect.Config) error {
	if db == nil {
		return nil
	}
	keysJSON, err := json.Marshal(cfg.Keys)
	if err != nil {
		return fmt.Errorf("marshal keys: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO monitor_sources (id, tenant_id, name, type, address, port, path, keys, interval_sec, ci_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO UPDATE SET
			tenant_id=EXCLUDED.tenant_id, name=EXCLUDED.name, type=EXCLUDED.type,
			address=EXCLUDED.address, port=EXCLUDED.port, path=EXCLUDED.path,
			keys=EXCLUDED.keys, interval_sec=EXCLUDED.interval_sec, ci_id=EXCLUDED.ci_id`,
		cfg.ID, cfg.TenantID, cfg.Name, cfg.Type, cfg.Address, cfg.Port,
		cfg.Path, string(keysJSON), int(cfg.Interval.Seconds()), cfg.CIID, time.Now())
	if err != nil {
		return fmt.Errorf("save monitor source: %w", err)
	}
	return nil
}

func deleteSourceFromDB(ctx context.Context, db *sql.DB, id string) error {
	if db == nil {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM monitor_sources WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete monitor source: %w", err)
	}
	return nil
}

func loadSourcesFromDB(ctx context.Context, db *sql.DB) ([]collect.Config, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, tenant_id, name, type, address, port, path, keys, interval_sec, ci_id
		FROM monitor_sources`)
	if err != nil {
		return nil, fmt.Errorf("load monitor sources: %w", err)
	}
	defer rows.Close()
	var out []collect.Config
	for rows.Next() {
		var c collect.Config
		var keysJSON []byte
		var intervalSec int
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.Type, &c.Address, &c.Port,
			&c.Path, &keysJSON, &intervalSec, &c.CIID); err != nil {
			return nil, fmt.Errorf("scan monitor source: %w", err)
		}
		c.Interval = time.Duration(intervalSec) * time.Second
		if len(keysJSON) > 0 {
			_ = json.Unmarshal(keysJSON, &c.Keys)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load monitor sources rows: %w", err)
	}
	slog.Info("monitor: sources loaded from db", "count", len(out))
	return out, nil
}
