package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SnapshotCI / SnapshotRel 是快照内的紧凑 CI / 关系记录（去重字段，便于对比）。
type SnapshotCI struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	TypeID string         `json:"type_id"`
	Status string         `json:"status"`
	Attrs  map[string]any `json:"attrs"`
}

type SnapshotRel struct {
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
}

// SnapshotData 是快照载荷（全量 CI + 关系）。
type SnapshotData struct {
	CIs  []SnapshotCI  `json:"cis"`
	Rels []SnapshotRel `json:"rels"`
}

// Snapshot 是 CMDB 基线快照（捕获时刻的全量 CI + 关系，用于环境漂移对比）。
type Snapshot struct {
	ID        string       `json:"id"`
	TenantID  string       `json:"tenant_id"`
	Name      string       `json:"name"`
	CICount   int          `json:"ci_count"`
	RelCount  int          `json:"rel_count"`
	Data      SnapshotData `json:"data"`
	CreatedAt time.Time    `json:"created_at"`
}

// CreateSnapshot 写入一份基线快照（data 以 JSONB 落库）。
func (r *Repository) CreateSnapshot(ctx context.Context, snap *Snapshot) error {
	if snap.ID == "" {
		snap.ID = uuid.New().String()
	}
	dataJSON, err := json.Marshal(snap.Data)
	if err != nil {
		return fmt.Errorf("marshal snapshot data: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_snapshot (id, tenant_id, name, ci_count, rel_count, data, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		snap.ID, snap.TenantID, snap.Name, snap.CICount, snap.RelCount, dataJSON); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}

// ListSnapshots 按时间倒序列出某租户的快照。
func (r *Repository) ListSnapshots(ctx context.Context, tenantID string) ([]*Snapshot, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, ci_count, rel_count, data, created_at
		FROM cmdb_snapshot WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()

	var out []*Snapshot
	for rows.Next() {
		s := &Snapshot{}
		var dataJSON []byte
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Name, &s.CICount, &s.RelCount, &dataJSON, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		if dataJSON != nil {
			_ = json.Unmarshal(dataJSON, &s.Data)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSnapshot 取单份快照（含 data 载荷）。
func (r *Repository) GetSnapshot(ctx context.Context, id, tenantID string) (*Snapshot, error) {
	s := &Snapshot{}
	var dataJSON []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, ci_count, rel_count, data, created_at
		FROM cmdb_snapshot WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&s.ID, &s.TenantID, &s.Name, &s.CICount, &s.RelCount, &dataJSON, &s.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get snapshot: %w", err)
	}
	if dataJSON != nil {
		_ = json.Unmarshal(dataJSON, &s.Data)
	}
	return s, nil
}

// DeleteSnapshot 删除快照。
func (r *Repository) DeleteSnapshot(ctx context.Context, id, tenantID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_snapshot WHERE id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}
