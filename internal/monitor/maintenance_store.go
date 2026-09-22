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

// 维护期静默持久化：monitor_maintenance 表（原纯内存，重启全丢）。
// 镜像 rule_store.go 范式: 自愈 DDL + upsert + 启动 load。db 为 nil 时退化为纯内存。

func ensureMaintenanceTable(ctx context.Context, db *sql.DB) {
	if db == nil {
		return
	}
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS monitor_maintenance (
			id         TEXT PRIMARY KEY,
			tenant_id  TEXT NOT NULL,
			name       TEXT NOT NULL,
			start_at   TIMESTAMPTZ NOT NULL,
			end_at     TIMESTAMPTZ NOT NULL,
			enabled    BOOLEAN NOT NULL DEFAULT true,
			match      JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		slog.Warn("monitor: ensure monitor_maintenance table failed", "error", err)
	}
}

func saveMaintenanceToDB(ctx context.Context, db *sql.DB, m *model.MaintenanceWindow) error {
	if db == nil {
		return nil
	}
	matchJSON, err := json.Marshal(m.Match)
	if err != nil {
		return fmt.Errorf("marshal maintenance match: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO monitor_maintenance (id, tenant_id, name, start_at, end_at, enabled, match, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
			tenant_id=EXCLUDED.tenant_id, name=EXCLUDED.name, start_at=EXCLUDED.start_at,
			end_at=EXCLUDED.end_at, enabled=EXCLUDED.enabled, match=EXCLUDED.match`,
		m.ID, m.TenantID, m.Name, m.StartAt, m.EndAt, m.Enabled, string(matchJSON), time.Now())
	if err != nil {
		return fmt.Errorf("save maintenance window: %w", err)
	}
	return nil
}

func deleteMaintenanceFromDB(ctx context.Context, db *sql.DB, id string) error {
	if db == nil {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM monitor_maintenance WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete maintenance window: %w", err)
	}
	return nil
}

func loadMaintenanceFromDB(ctx context.Context, db *sql.DB) ([]*model.MaintenanceWindow, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, tenant_id, name, start_at, end_at, enabled, match
		FROM monitor_maintenance`)
	if err != nil {
		return nil, fmt.Errorf("load maintenance windows: %w", err)
	}
	defer rows.Close()
	var out []*model.MaintenanceWindow
	for rows.Next() {
		var m model.MaintenanceWindow
		var matchJSON []byte
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Name, &m.StartAt, &m.EndAt, &m.Enabled, &matchJSON); err != nil {
			return nil, fmt.Errorf("scan maintenance window: %w", err)
		}
		if len(matchJSON) > 0 {
			_ = json.Unmarshal(matchJSON, &m.Match)
		}
		out = append(out, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load maintenance windows rows: %w", err)
	}
	slog.Info("monitor: maintenance windows loaded from db", "count", len(out))
	return out, nil
}
