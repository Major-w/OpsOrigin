package iam

// 部门（组织架构）PG 实现：树形实体 + 成员计数。
// 成员绑定关系经 users.department（部门名）关联——这里是只读聚合，
// 不构成对 user 模块的写依赖（跨模块统计，同库直查并注明）。

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// deptMemberCounts 按部门名统计成员数（users.department = 部门名）。
func (s *pgStore) deptMemberCounts(ctx context.Context, tenantID string) map[string]int {
	out := map[string]int{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT department, COUNT(*) FROM users WHERE tenant_id=$1 AND department <> '' GROUP BY department`, tenantID)
	if err != nil {
		return out // 统计失败不阻断列表（计数留空）
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			continue
		}
		out[name] = n
	}
	return out
}

func (s *pgStore) CreateDepartment(ctx context.Context, d *Department) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	if d.TenantID == "" {
		d.TenantID = "default"
	}
	if d.Source == "" {
		d.Source = "local"
	}
	now := time.Now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO iam_departments(id,tenant_id,name,parent_id,description,source,created_at,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(tenant_id,name) DO NOTHING`,
		d.ID, d.TenantID, d.Name, d.ParentID, d.Description, d.Source, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return err
	}
	if n, aerr := res.RowsAffected(); aerr == nil && n == 0 {
		return fmt.Errorf("部门 %q 已存在", d.Name)
	}
	return nil
}

func (s *pgStore) GetDepartment(ctx context.Context, id, tenantID string) (*Department, error) {
	d := &Department{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,parent_id,description,source,created_at,updated_at FROM iam_departments WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&d.ID, &d.TenantID, &d.Name, &d.ParentID, &d.Description, &d.Source, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *pgStore) ListDepartments(ctx context.Context, tenantID string) ([]*Department, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,parent_id,description,source,created_at,updated_at FROM iam_departments WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := s.deptMemberCounts(ctx, tenantID)
	var out []*Department
	for rows.Next() {
		d := &Department{}
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Name, &d.ParentID, &d.Description, &d.Source, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.MemberCount = counts[d.Name]
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *pgStore) UpdateDepartment(ctx context.Context, d *Department) error {
	d.UpdatedAt = time.Now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE iam_departments SET name=$2,parent_id=$3,description=$4,updated_at=$5 WHERE id=$1 AND tenant_id=$6`,
		d.ID, d.Name, d.ParentID, d.Description, d.UpdatedAt, d.TenantID)
	if err != nil {
		return err
	}
	if n, aerr := res.RowsAffected(); aerr == nil && n == 0 {
		return fmt.Errorf("部门不存在")
	}
	return nil
}

func (s *pgStore) DeleteDepartment(ctx context.Context, id, tenantID string) error {
	var children int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM iam_departments WHERE tenant_id=$1 AND parent_id=$2`, tenantID, id).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return fmt.Errorf("存在子部门，无法删除")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM iam_departments WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// EnsureDepartmentUnder 按名+父幂等建档（批量导入用）：同名已存在时校验父一致，
// 不一致报错（同名在租户内唯一，防路径导入把同名部门错误挂到两处）。
func (s *pgStore) EnsureDepartmentUnder(ctx context.Context, tenantID, name, parentID, source string) (string, bool, error) {
	var id, existingParent string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, parent_id FROM iam_departments WHERE tenant_id=$1 AND name=$2`, tenantID, name).Scan(&id, &existingParent)
	if err == nil {
		if existingParent != parentID {
			return "", false, fmt.Errorf("部门 %q 已存在但上级不一致（已有挂载与导入路径冲突）", name)
		}
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}
	d := &Department{TenantID: tenantID, Name: name, ParentID: parentID, Source: source}
	if err := s.CreateDepartment(ctx, d); err != nil {
		// 并发建档撞唯一键 → 回读校验
		if e2 := s.db.QueryRowContext(ctx,
			`SELECT id, parent_id FROM iam_departments WHERE tenant_id=$1 AND name=$2`, tenantID, name).Scan(&id, &existingParent); e2 == nil && existingParent == parentID {
			return id, false, nil
		}
		return "", false, err
	}
	return d.ID, true, nil
}

// EnsureDepartment 按名幂等建档（AD 镜像/本地引用归档共用）：存在直接返回。
func (s *pgStore) EnsureDepartment(ctx context.Context, tenantID, name, source string) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM iam_departments WHERE tenant_id=$1 AND name=$2`, tenantID, name).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}
	d := &Department{TenantID: tenantID, Name: name, Source: source}
	if err := s.CreateDepartment(ctx, d); err != nil {
		// 并发建档撞唯一键 → 回读
		if e2 := s.db.QueryRowContext(ctx,
			`SELECT id FROM iam_departments WHERE tenant_id=$1 AND name=$2`, tenantID, name).Scan(&id); e2 == nil {
			return id, false, nil
		}
		return "", false, err
	}
	return d.ID, true, nil
}
