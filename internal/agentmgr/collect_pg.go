package agentmgr

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"
)

// ——— 采集任务持久化 (CollectStore 的 PG 实现) ———

// LoadCollectTasks 从 collect_tasks 表加载全部采集任务。
func (s *pgStore) LoadCollectTasks(ctx context.Context) ([]*CollectTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,type,script,script_type,requirements,interval_sec,target_agents,enabled,timeout_sec,last_run_at,created_at,updated_at
		 FROM collect_tasks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CollectTask
	for rows.Next() {
		t := &CollectTask{}
		var reqs, targets []byte
		var lastRun sql.NullTime
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.Type, &t.Script, &t.ScriptType,
			&reqs, &t.IntervalSec, &targets, &t.Enabled, &t.TimeoutSec, &lastRun, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal(reqs, &t.Requirements)
		json.Unmarshal(targets, &t.TargetAgents)
		if lastRun.Valid {
			t.LastRunAt = &lastRun.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveCollectTask 保存(INSERT ON CONFLICT UPDATE)采集任务。
func (s *pgStore) SaveCollectTask(ctx context.Context, t *CollectTask) error {
	reqs, _ := json.Marshal(t.Requirements)
	targets, _ := json.Marshal(t.TargetAgents)
	var lastRun any
	if t.LastRunAt != nil {
		lastRun = *t.LastRunAt
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO collect_tasks(id,tenant_id,name,type,script,script_type,requirements,interval_sec,target_agents,enabled,timeout_sec,last_run_at,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 ON CONFLICT (id) DO UPDATE SET
		   name=$3,type=$4,script=$5,script_type=$6,requirements=$7,interval_sec=$8,
		   target_agents=$9,enabled=$10,timeout_sec=$11,last_run_at=$12,updated_at=$14`,
		t.ID, t.TenantID, t.Name, string(t.Type), t.Script, t.ScriptType, reqs, t.IntervalSec,
		targets, t.Enabled, t.TimeoutSec, lastRun, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		slog.Warn("collect: save task to pg failed", "id", t.ID, "err", err)
	}
	return err
}

// DeleteCollectTask 删除采集任务。
func (s *pgStore) DeleteCollectTask(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM collect_tasks WHERE id=$1`, id)
	return err
}

// ——— 采集指标持久化 ———

// InsertCollectMetrics 批量写入采集指标点。
func (s *pgStore) InsertCollectMetrics(ctx context.Context, tenantID, agentID, taskID string, points []MetricPoint, at time.Time) error {
	if len(points) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO collect_metrics(tenant_id,agent_id,collect_task_id,name,value,unit,collected_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range points {
		if _, err := stmt.ExecContext(ctx, tenantID, agentID, taskID, p.Name, p.Value, p.Unit, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QueryCollectMetrics 查询采集指标(按 agent/名称过滤, 时间倒序, 上限 limit)。
func (s *pgStore) QueryCollectMetrics(ctx context.Context, tenantID, agentID, name string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query := `SELECT agent_id, collect_task_id, name, value, unit, collected_at FROM collect_metrics WHERE tenant_id=$1`
	args := []any{tenantID}
	argIdx := 2
	if agentID != "" {
		query += " AND agent_id = $2"
		args = append(args, agentID)
		argIdx++
	}
	if name != "" {
		query += " AND name = $3"
		args = append(args, name)
		argIdx++
	}
	query += " ORDER BY collected_at DESC LIMIT $4"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var agentID2, taskID, name2, unit string
		var value float64
		var at time.Time
		if err := rows.Scan(&agentID2, &taskID, &name2, &value, &unit, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"agent_id": agentID2, "collect_task_id": taskID, "name": name2,
			"value": value, "unit": unit, "collected_at": at,
		})
	}
	return out, rows.Err()
}
