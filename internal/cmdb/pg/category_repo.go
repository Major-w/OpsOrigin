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

// migrateModelCategory 建 cmdb_model_category 表 + 种子内置 5 分类 + 存量模型分类名迁移。
// 幂等：表 IF NOT EXISTS、种子 ON CONFLICT(tenant_id,name) DO NOTHING、存量映射仅命中旧 key。
func (r *Repository) migrateModelCategory(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS cmdb_model_category (
			id         TEXT PRIMARY KEY,
			tenant_id  TEXT NOT NULL DEFAULT 'default',
			name       TEXT NOT NULL,
			builtin    BOOLEAN NOT NULL DEFAULT FALSE,
			sort_order INT  NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (tenant_id, name)
		)`); err != nil {
		return fmt.Errorf("create cmdb_model_category: %w", err)
	}

	// 种子：内置 5 分类（幂等）
	builtin := []struct {
		name  string
		order int
	}{
		{model.ModelCategoryBusiness, 1},
		{model.ModelCategoryApp, 2},
		{model.ModelCategoryMiddleware, 3},
		{model.ModelCategoryDatabase, 4},
		{model.ModelCategoryInfra, 5},
	}
	for _, b := range builtin {
		if _, err := r.db.ExecContext(ctx, `
			INSERT INTO cmdb_model_category (id, tenant_id, name, builtin, sort_order)
			VALUES ($1, $2, $3, TRUE, $4)
			ON CONFLICT (tenant_id, name) DO UPDATE SET builtin=TRUE, sort_order=EXCLUDED.sort_order`,
			uuid.New().String(), "default", b.name, b.order); err != nil {
			return fmt.Errorf("seed model category %q: %w", b.name, err)
		}
	}

	// 存量模型分类名迁移（旧 key → 新中文名），幂等
	remap := []struct{ old, new string }{
		{"platform", model.ModelCategoryInfra},
		{"infra", model.ModelCategoryInfra},
		{"app", model.ModelCategoryApp},
		{"org", model.ModelCategoryBusiness},
	}
	for _, m := range remap {
		if _, err := r.db.ExecContext(ctx,
			`UPDATE cmdb_ci_model_type SET category=$1 WHERE category=$2 AND tenant_id='default'`,
			m.new, m.old); err != nil {
			return fmt.Errorf("remap model category %q→%q: %w", m.old, m.new, err)
		}
	}
	// 空分类回填默认（基础设施资源）
	if _, err := r.db.ExecContext(ctx,
		`UPDATE cmdb_ci_model_type SET category=$1 WHERE category IS NULL OR category=''`,
		model.ModelCategoryInfra); err != nil {
		return fmt.Errorf("backfill model category: %w", err)
	}
	return nil
}

// ListModelCategories 列出租户下全部分类（内置在前，按 sort_order）。
func (r *Repository) ListModelCategories(ctx context.Context, tenantID string) ([]*model.ModelCategory, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, builtin, sort_order, created_at
		FROM cmdb_model_category WHERE tenant_id=$1 ORDER BY builtin DESC, sort_order ASC, created_at ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list model categories: %w", err)
	}
	defer rows.Close()
	out := make([]*model.ModelCategory, 0)
	for rows.Next() {
		c := &model.ModelCategory{}
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.Builtin, &c.SortOrder, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan model category: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateModelCategory 新增自定义分类（重名报错）。
func (r *Repository) CreateModelCategory(ctx context.Context, c *model.ModelCategory) error {
	c.ID = uuid.New().String()
	c.CreatedAt = time.Now()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cmdb_model_category (id, tenant_id, name, builtin, sort_order, created_at)
		VALUES ($1, $2, $3, FALSE, $4, $5)`,
		c.ID, c.TenantID, c.Name, c.SortOrder, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert model category: %w", err)
	}
	return nil
}

// GetModelCategoryByName 按名查分类。
func (r *Repository) GetModelCategoryByName(ctx context.Context, name, tenantID string) (*model.ModelCategory, error) {
	c := &model.ModelCategory{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, builtin, sort_order, created_at
		FROM cmdb_model_category WHERE name=$1 AND tenant_id=$2`, name, tenantID).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.Builtin, &c.SortOrder, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get model category by name: %w", err)
	}
	return c, nil
}

// CountCIModelTypesByCategory 统计某分类下模型数量（删除保护）。
func (r *Repository) CountCIModelTypesByCategory(ctx context.Context, categoryName, tenantID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM cmdb_ci_model_type WHERE category=$1 AND tenant_id=$2`,
		categoryName, tenantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count model types by category: %w", err)
	}
	return n, nil
}

// DeleteModelCategory 删除自定义分类；内置分类与有模型引用的分类拒绝删除。
func (r *Repository) DeleteModelCategory(ctx context.Context, id, tenantID string) error {
	c := &model.ModelCategory{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, builtin, sort_order, created_at
		FROM cmdb_model_category WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.Builtin, &c.SortOrder, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("category not found")
		}
		return fmt.Errorf("get model category: %w", err)
	}
	if c.Builtin {
		return errors.New("内置分类不可删除")
	}
	n, err := r.CountCIModelTypesByCategory(ctx, c.Name, tenantID)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("分类「%s」下仍有 %d 个模型，无法删除", c.Name, n)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_model_category WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return fmt.Errorf("delete model category: %w", err)
	}
	return nil
}
