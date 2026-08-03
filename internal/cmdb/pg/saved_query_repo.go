package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// CreateSavedQuery 保存一条查询策略。
func (r *Repository) CreateSavedQuery(ctx context.Context, q *model.SavedQuery) error {
	if q.ID == "" {
		q.ID = uuid.New().String()
	}
	now := time.Now()
	q.CreatedAt = now
	q.UpdatedAt = now

	queryJSON, err := json.Marshal(q.Query)
	if err != nil {
		return fmt.Errorf("marshal query: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO cmdb_saved_query (id, tenant_id, name, type_id, query, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		q.ID, q.TenantID, q.Name, nullIfEmpty(q.TypeID), queryJSON, q.CreatedAt, q.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert saved query: %w", err)
	}
	return nil
}

// GetSavedQuery 按 ID + 租户读取查询策略。
func (r *Repository) GetSavedQuery(ctx context.Context, id, tenantID string) (*model.SavedQuery, error) {
	q := &model.SavedQuery{}
	var queryJSON []byte
	var typeID sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, type_id, query, created_at, updated_at
		FROM cmdb_saved_query WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&q.ID, &q.TenantID, &q.Name, &typeID, &queryJSON, &q.CreatedAt, &q.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get saved query: %w", err)
	}
	q.TypeID = typeID.String
	if queryJSON != nil {
		json.Unmarshal(queryJSON, &q.Query)
	}
	if q.Query == nil {
		q.Query = map[string]any{}
	}
	return q, nil
}

// ListSavedQueries 列出某租户全部查询策略。
func (r *Repository) ListSavedQueries(ctx context.Context, tenantID string) ([]*model.SavedQuery, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, type_id, query, created_at, updated_at
		FROM cmdb_saved_query WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list saved queries: %w", err)
	}
	defer rows.Close()

	var out []*model.SavedQuery
	for rows.Next() {
		q := &model.SavedQuery{}
		var queryJSON []byte
		var typeID sql.NullString
		if err := rows.Scan(&q.ID, &q.TenantID, &q.Name, &typeID, &queryJSON, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan saved query: %w", err)
		}
		q.TypeID = typeID.String
		if queryJSON != nil {
			json.Unmarshal(queryJSON, &q.Query)
		}
		if q.Query == nil {
			q.Query = map[string]any{}
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// DeleteSavedQuery 删除查询策略。
func (r *Repository) DeleteSavedQuery(ctx context.Context, id, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cmdb_saved_query WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete saved query: %w", err)
	}
	return nil
}

// ListCIBySavedQuery 按保存查询的结构化条件解析命中的 CI 列表（动态目标/我想查共用）。
// 支持 type_id 限定、关键字 ILIKE（name/description）、状态精确匹配、属性精确匹配。
// 解析时包含归档 CI（采集动态目标可能需要命中已归档资产，由调用方自行过滤）。
func (r *Repository) ListCIBySavedQuery(ctx context.Context, tenantID string, f *model.SavedQueryFilter) ([]*model.CIModel, error) {
	query := `SELECT id, type_id, tenant_id, name, description, status, attributes, labels, channel, created_at, updated_at, attribute_meta
		FROM cmdb_ci WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if f == nil {
		query += ` ORDER BY created_at DESC LIMIT 100`
		return scanCIsSafe(ctx, r, query, args...)
	}

	if f.TypeID != "" {
		query += fmt.Sprintf(` AND type_id = $%d`, argIdx)
		args = append(args, f.TypeID)
		argIdx++
	}
	if f.Status != "" {
		query += fmt.Sprintf(` AND status = $%d`, argIdx)
		args = append(args, f.Status)
		argIdx++
	}
	if f.Q != "" {
		query += fmt.Sprintf(` AND (name ILIKE $%d OR description ILIKE $%d)`, argIdx, argIdx+1)
		like := "%" + f.Q + "%"
		args = append(args, like, like)
		argIdx += 2
	}
	for k, v := range f.Attrs {
		// 属性精确匹配：attributes->>'k' = v（绑定参数，避免注入）
		query += fmt.Sprintf(` AND attributes->>$%d = $%d`, argIdx, argIdx+1)
		args = append(args, k, v)
		argIdx += 2
	}
	query += ` ORDER BY created_at DESC LIMIT 500`

	return scanCIsSafe(ctx, r, query, args...)
}

// scanCIsSafe 执行已拼装的 CI 查询并扫描。
func scanCIsSafe(ctx context.Context, r *Repository, query string, args ...any) ([]*model.CIModel, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ci by saved query: %w", err)
	}
	defer rows.Close()
	var cis []*model.CIModel
	for rows.Next() {
		ci, err := scanCI(rows)
		if err != nil {
			return nil, err
		}
		cis = append(cis, ci)
	}
	return cis, rows.Err()
}
