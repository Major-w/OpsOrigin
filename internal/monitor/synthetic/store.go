package synthetic

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

// 拨测目标持久化：monitor_targets 表（原纯内存 map，重启全丢）。
// db 为 nil 时退化为纯内存（无后端环境）。

func saveTargetToDB(db *sql.DB, t *model.MonitorTarget) error {
	probesJSON, err := json.Marshal(t.Probes)
	if err != nil {
		return fmt.Errorf("marshal probes: %w", err)
	}
	labelsJSON, err := json.Marshal(t.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	_, err = db.Exec(`
		INSERT INTO monitor_targets (id, tenant_id, ci_id, name, address, port, probes, interval_sec, timeout_sec, labels, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, $11)
		ON CONFLICT (id) DO UPDATE SET
			tenant_id=EXCLUDED.tenant_id, ci_id=EXCLUDED.ci_id, name=EXCLUDED.name,
			address=EXCLUDED.address, port=EXCLUDED.port, probes=EXCLUDED.probes,
			interval_sec=EXCLUDED.interval_sec, timeout_sec=EXCLUDED.timeout_sec, labels=EXCLUDED.labels`,
		t.ID, t.TenantID, t.CIID, t.Name, t.Address, t.Port, probesJSON,
		int(t.Interval.Seconds()), int(t.Timeout.Seconds()), labelsJSON, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("save monitor target: %w", err)
	}
	return nil
}

func deleteTargetFromDB(db *sql.DB, id string) error {
	if _, err := db.Exec(`DELETE FROM monitor_targets WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete monitor target: %w", err)
	}
	return nil
}

func loadTargetsFromDB(ctx context.Context, db *sql.DB) ([]*model.MonitorTarget, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, tenant_id, ci_id, name, address, port, probes, interval_sec, timeout_sec, labels, created_at
		FROM monitor_targets`)
	if err != nil {
		return nil, fmt.Errorf("load monitor targets: %w", err)
	}
	defer rows.Close()
	var out []*model.MonitorTarget
	for rows.Next() {
		t := &model.MonitorTarget{}
		var probesJSON, labelsJSON []byte
		var intervalSec, timeoutSec int
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CIID, &t.Name, &t.Address, &t.Port,
			&probesJSON, &intervalSec, &timeoutSec, &labelsJSON, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan monitor target: %w", err)
		}
		t.Interval = time.Duration(intervalSec) * time.Second
		t.Timeout = time.Duration(timeoutSec) * time.Second
		t.Status = model.TargetUnknown
		if len(probesJSON) > 0 {
			_ = json.Unmarshal(probesJSON, &t.Probes)
		}
		if len(labelsJSON) > 0 {
			_ = json.Unmarshal(labelsJSON, &t.Labels)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load monitor targets rows: %w", err)
	}
	slog.Info("monitor: targets loaded from db", "count", len(out))
	return out, nil
}
