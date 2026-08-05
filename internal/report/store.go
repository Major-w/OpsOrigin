package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// —— 内存实现(测试 / 轻量场景) ——

type MemoryDashboardStore struct {
	mu         sync.RWMutex
	dashboards map[string]*Dashboard
}

func NewMemoryDashboardStore() *MemoryDashboardStore {
	return &MemoryDashboardStore{dashboards: make(map[string]*Dashboard)}
}

func (s *MemoryDashboardStore) Create(ctx context.Context, d *Dashboard) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.ID = uuid.New().String()
	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now
	s.dashboards[d.ID] = d
	return nil
}

func (s *MemoryDashboardStore) Get(ctx context.Context, id, tenantID string) (*Dashboard, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.dashboards[id]
	if !ok || d.TenantID != tenantID {
		return nil, nil
	}
	return d, nil
}

func (s *MemoryDashboardStore) List(ctx context.Context, tenantID string) ([]*Dashboard, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Dashboard, 0, len(s.dashboards))
	for _, d := range s.dashboards {
		if d.TenantID != tenantID {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *MemoryDashboardStore) Update(ctx context.Context, d *Dashboard) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.dashboards[d.ID]
	if !ok || existing.TenantID != d.TenantID {
		return nil
	}
	d.CreatedAt = existing.CreatedAt
	d.UpdatedAt = time.Now()
	s.dashboards[d.ID] = d
	return nil
}

func (s *MemoryDashboardStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dashboards[id]
	if !ok || d.TenantID != tenantID {
		return nil
	}
	delete(s.dashboards, id)
	return nil
}

// —— PostgreSQL 实现 ——

type pgDashboardStore struct {
	db *sql.DB
}

func newPGDashboardStore(db *sql.DB) *pgDashboardStore {
	s := &pgDashboardStore{db: db}
	s.init(context.Background())
	return s
}

// InitSchema 创建看板表(幂等)。供装配期显式调用，也可由 newPGDashboardStore 自动执行。
func (s *pgDashboardStore) InitSchema(ctx context.Context) {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS report_dashboards (
			id          TEXT PRIMARY KEY,
			tenant_id   TEXT NOT NULL,
			name        TEXT NOT NULL,
			description TEXT DEFAULT '',
			widgets     JSONB NOT NULL DEFAULT '[]',
			created_at  TIMESTAMPTZ NOT NULL,
			updated_at  TIMESTAMPTZ NOT NULL,
			created_by  TEXT DEFAULT ''
		)`); err != nil {
		slog.Warn("report: create report_dashboards table failed", "err", err)
		return
	}
	// 存量表补列(幂等)：P0.5 起看板同时记录创建者展示名，列表页免回查用户表
	s.db.ExecContext(ctx, `ALTER TABLE report_dashboards ADD COLUMN IF NOT EXISTS created_by_name TEXT DEFAULT ''`)
	s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_report_dashboards_tenant ON report_dashboards(tenant_id)`)
}

func (s *pgDashboardStore) init(ctx context.Context) { s.InitSchema(ctx) }

func (s *pgDashboardStore) Create(ctx context.Context, d *Dashboard) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now
	widgets, err := json.Marshal(d.Widgets)
	if err != nil {
		return fmt.Errorf("report: marshal widgets failed: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO report_dashboards (id, tenant_id, name, description, widgets, created_at, updated_at, created_by, created_by_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		d.ID, d.TenantID, d.Name, d.Description, widgets, d.CreatedAt, d.UpdatedAt, d.CreatedBy, d.CreatedByName,
	)
	if err != nil {
		return fmt.Errorf("report: insert dashboard failed: %w", err)
	}
	return nil
}

func (s *pgDashboardStore) Get(ctx context.Context, id, tenantID string) (*Dashboard, error) {
	var d Dashboard
	var widgetsJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, description, widgets, created_at, updated_at, created_by, COALESCE(created_by_name, '')
		FROM report_dashboards WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&d.ID, &d.TenantID, &d.Name, &d.Description, &widgetsJSON, &d.CreatedAt, &d.UpdatedAt, &d.CreatedBy, &d.CreatedByName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("report: get dashboard failed: %w", err)
	}
	if len(widgetsJSON) > 0 {
		if err := json.Unmarshal(widgetsJSON, &d.Widgets); err != nil {
			return nil, fmt.Errorf("report: unmarshal widgets failed: %w", err)
		}
	}
	return &d, nil
}

func (s *pgDashboardStore) List(ctx context.Context, tenantID string) ([]*Dashboard, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, description, widgets, created_at, updated_at, created_by, COALESCE(created_by_name, '')
		FROM report_dashboards WHERE tenant_id = $1 ORDER BY updated_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("report: list dashboards failed: %w", err)
	}
	defer rows.Close()
	out := make([]*Dashboard, 0, 8)
	for rows.Next() {
		var d Dashboard
		var widgetsJSON []byte
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Name, &d.Description, &widgetsJSON, &d.CreatedAt, &d.UpdatedAt, &d.CreatedBy, &d.CreatedByName); err != nil {
			return nil, fmt.Errorf("report: scan dashboard failed: %w", err)
		}
		if len(widgetsJSON) > 0 {
			_ = json.Unmarshal(widgetsJSON, &d.Widgets)
		}
		out = append(out, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("report: iterate dashboards failed: %w", err)
	}
	return out, nil
}

func (s *pgDashboardStore) Update(ctx context.Context, d *Dashboard) error {
	widgets, err := json.Marshal(d.Widgets)
	if err != nil {
		return fmt.Errorf("report: marshal widgets failed: %w", err)
	}
	d.UpdatedAt = time.Now()
	res, err := s.db.ExecContext(ctx, `
		UPDATE report_dashboards
		SET name = $1, description = $2, widgets = $3, updated_at = $4
		WHERE id = $5 AND tenant_id = $6`,
		d.Name, d.Description, widgets, d.UpdatedAt, d.ID, d.TenantID)
	if err != nil {
		return fmt.Errorf("report: update dashboard failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("report: dashboard %q not found", d.ID)
	}
	return nil
}

func (s *pgDashboardStore) Delete(ctx context.Context, id, tenantID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM report_dashboards WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("report: delete dashboard failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("report: dashboard %q not found", id)
	}
	return nil
}
