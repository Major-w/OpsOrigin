package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// CreateCollectionTask 新建一条采集任务。
func (r *Repository) CreateCollectionTask(ctx context.Context, t *model.CollectionTask) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Status == "" {
		t.Status = "idle"
	}
	if t.Channel == "" {
		t.Channel = "server"
	}
	if t.TargetMode == "" {
		t.TargetMode = "static"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_collection_task
			(id, tenant_id, name, resource_type, target, kind, channel, frequency, enabled, target_mode, target_query, last_run_at, status, error, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		t.ID, t.TenantID, t.Name, t.ResourceType, t.Target, t.Kind, t.Channel, t.Frequency, t.Enabled,
		t.TargetMode, nullIfEmpty(t.TargetQuery),
		nullTime(t.LastRunAt), t.Status, nullString(t.Error), t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert collection task: %w", err)
	}
	return nil
}

// GetCollectionTask 按 ID + 租户读取采集任务。
func (r *Repository) GetCollectionTask(ctx context.Context, id, tenantID string) (*model.CollectionTask, error) {
	t := &model.CollectionTask{}
	var lastRun sql.NullTime
	var errStr sql.NullString
	var targetMode, targetQuery sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, resource_type, target, kind, channel, frequency, enabled, target_mode, target_query, last_run_at, status, error, created_at, updated_at
		FROM cmdb_collection_task WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&t.ID, &t.TenantID, &t.Name, &t.ResourceType, &t.Target, &t.Kind, &t.Channel, &t.Frequency,
			&t.Enabled, &targetMode, &targetQuery, &lastRun, &t.Status, &errStr, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get collection task: %w", err)
	}
	if lastRun.Valid {
		t.LastRunAt = &lastRun.Time
	}
	t.TargetMode = targetMode.String
	if t.TargetMode == "" {
		t.TargetMode = "static"
	}
	t.TargetQuery = targetQuery.String
	t.Error = errStr.String
	return t, nil
}

// ListCollectionTasks 列出某租户全部采集任务。
func (r *Repository) ListCollectionTasks(ctx context.Context, tenantID string) ([]*model.CollectionTask, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, resource_type, target, kind, channel, frequency, enabled, target_mode, target_query, last_run_at, status, error, created_at, updated_at
		FROM cmdb_collection_task WHERE tenant_id=$1 ORDER BY created_at ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list collection tasks: %w", err)
	}
	defer rows.Close()
	return scanCollectionTasks(rows)
}

// ListAllEnabledCollectionTasks 列出全部租户中启用且未删除的采集任务（供调度器扫描）。
func (r *Repository) ListAllEnabledCollectionTasks(ctx context.Context) ([]*model.CollectionTask, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, resource_type, target, kind, channel, frequency, enabled, target_mode, target_query, last_run_at, status, error, created_at, updated_at
		FROM cmdb_collection_task WHERE enabled=TRUE ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list enabled collection tasks: %w", err)
	}
	defer rows.Close()
	return scanCollectionTasks(rows)
}

// UpdateCollectionTask 更新启停 / 频率 / 状态 / 上次运行时间 / 错误信息。
func (r *Repository) UpdateCollectionTask(ctx context.Context, t *model.CollectionTask) error {
	t.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx, `
		UPDATE cmdb_collection_task
		SET name=$2, resource_type=$3, target=$4, kind=$5, channel=$6, frequency=$7, enabled=$8,
		    target_mode=$9, target_query=$10, last_run_at=$11, status=$12, error=$13, updated_at=$14
		WHERE id=$1 AND tenant_id=$15`,
		t.ID, t.Name, t.ResourceType, t.Target, t.Kind, t.Channel, t.Frequency, t.Enabled,
		t.TargetMode, nullIfEmpty(t.TargetQuery),
		nullTime(t.LastRunAt), t.Status, nullString(t.Error), t.UpdatedAt, t.TenantID)
	if err != nil {
		return fmt.Errorf("update collection task: %w", err)
	}
	return nil
}

// TouchCollectionTask 仅更新调度相关字段（上次运行时间 / 状态 / 错误），供调度器调用。
func (r *Repository) TouchCollectionTask(ctx context.Context, id string, lastRun *time.Time, status, errMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE cmdb_collection_task SET last_run_at=$2, status=$3, error=$4, updated_at=NOW() WHERE id=$1`,
		id, nullTime(lastRun), status, nullString(errMsg))
	if err != nil {
		return fmt.Errorf("touch collection task: %w", err)
	}
	return nil
}

// DeleteCollectionTask 删除采集任务。
func (r *Repository) DeleteCollectionTask(ctx context.Context, id, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_collection_task WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete collection task: %w", err)
	}
	return nil
}

// GetPlatformPolicy 读取平台全局默认策略；不存在时返回内置默认值（不写库）。
func (r *Repository) GetPlatformPolicy(ctx context.Context, tenantID string) (*model.PlatformPolicy, error) {
	p := &model.PlatformPolicy{}
	err := r.db.QueryRowContext(ctx, `
		SELECT tenant_id, default_cmdb_enabled, default_monitor_enabled, cmdb_default_frequency, monitor_default_frequency, updated_at
		FROM cmdb_platform_policy WHERE tenant_id=$1`, tenantID).
		Scan(&p.TenantID, &p.DefaultCMDBEnabled, &p.DefaultMonitorEnabled, &p.CMDBDefaultFrequency, &p.MonitorDefaultFrequency, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return defaultPlatformPolicy(tenantID), nil
		}
		return nil, fmt.Errorf("get platform policy: %w", err)
	}
	return p, nil
}

// SavePlatformPolicy 写入（upsert）平台全局默认策略。
func (r *Repository) SavePlatformPolicy(ctx context.Context, p *model.PlatformPolicy) error {
	p.UpdatedAt = time.Now()
	if p.CMDBDefaultFrequency == "" {
		p.CMDBDefaultFrequency = "1h"
	}
	if p.MonitorDefaultFrequency == "" {
		p.MonitorDefaultFrequency = "5m"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_platform_policy
			(tenant_id, default_cmdb_enabled, default_monitor_enabled, cmdb_default_frequency, monitor_default_frequency, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id) DO UPDATE SET
			default_cmdb_enabled=EXCLUDED.default_cmdb_enabled,
			default_monitor_enabled=EXCLUDED.default_monitor_enabled,
			cmdb_default_frequency=EXCLUDED.cmdb_default_frequency,
			monitor_default_frequency=EXCLUDED.monitor_default_frequency,
			updated_at=EXCLUDED.updated_at`,
		p.TenantID, p.DefaultCMDBEnabled, p.DefaultMonitorEnabled, p.CMDBDefaultFrequency, p.MonitorDefaultFrequency, p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save platform policy: %w", err)
	}
	return nil
}

func defaultPlatformPolicy(tenantID string) *model.PlatformPolicy {
	return &model.PlatformPolicy{
		TenantID:                tenantID,
		DefaultCMDBEnabled:      true,
		DefaultMonitorEnabled:   false,
		CMDBDefaultFrequency:    "1h",
		MonitorDefaultFrequency: "5m",
	}
}

func scanCollectionTasks(rows *sql.Rows) ([]*model.CollectionTask, error) {
	out := make([]*model.CollectionTask, 0)
	for rows.Next() {
		t := &model.CollectionTask{}
		var lastRun sql.NullTime
		var errStr sql.NullString
		var targetMode, targetQuery sql.NullString
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.ResourceType, &t.Target, &t.Kind, &t.Channel,
			&t.Frequency, &t.Enabled, &targetMode, &targetQuery, &lastRun, &t.Status, &errStr, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan collection task: %w", err)
		}
		if lastRun.Valid {
			t.LastRunAt = &lastRun.Time
		}
		t.TargetMode = targetMode.String
		if t.TargetMode == "" {
			t.TargetMode = "static"
		}
		t.TargetQuery = targetQuery.String
		t.Error = errStr.String
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collection tasks: %w", err)
	}
	return out, nil
}

// nullTime 把 *time.Time 转为可空 SQL 参数。
func nullTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}

// nullString 把 string 转为可空 SQL 参数（空串视为 NULL）。
func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
