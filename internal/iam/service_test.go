package iam

import (
	"context"
	"testing"
)

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

// TestGetAggregatedPermissions 验证前端权限感知的数据来源:
//   - 主角色决定基础权限(operator 含 cmdb:write 但不含 cmdb:delete)
//   - 已分配的 IAM 角色权限与主角色并集(viewer + 带 cmdb:delete 的自定义角色 => 同时拥有两者)
func TestGetAggregatedPermissions(t *testing.T) {
	s := NewService(nil) // 内存存储,seed admin/operator/viewer
	ctx := context.Background()

	// operator 主角色:可读写数据,但删除收紧后不应含 cmdb:delete
	op := s.GetAggregatedPermissions(ctx, "default", "u-op", "operator")
	if !contains(op, "cmdb:write") {
		t.Error("operator 应拥有 cmdb:write")
	}
	if contains(op, "cmdb:delete") {
		t.Error("operator 不应拥有 cmdb:delete(删除已收紧为仅 admin)")
	}

	// 新建一个仅含 cmdb:delete 的自定义角色并分配给某 viewer
	custom := &Role{TenantID: "default", Name: "deleter", DisplayName: "Deleter", Permissions: []string{"cmdb:delete"}}
	if err := s.store.CreateRole(ctx, custom); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if err := s.store.SetUserRoles(ctx, "u-viewer", "default", []string{custom.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	ag := s.GetAggregatedPermissions(ctx, "default", "u-viewer", "viewer")
	if !contains(ag, "cmdb:read") {
		t.Error("viewer 主角色应拥有 cmdb:read")
	}
	if !contains(ag, "cmdb:delete") {
		t.Error("分配 deleter 角色后应并入 cmdb:delete")
	}
}
