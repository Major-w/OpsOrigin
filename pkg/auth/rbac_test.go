package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// withRole 把角色写入请求 context，模拟认证中间件注入的声明。
func withRole(req *http.Request, role string) *http.Request {
	if role == "" {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), "role", role))
}

// TestAutoPermissionMiddleware_CMDB 验证 CMDB 各路由被正确 RBAC 管制：
//   - viewer 仅可读，写/删均 403
//   - operator 可读写数据，但模型管理(model-type)仅 admin 可写
//   - admin 全部放行
//   - 未携带角色 → 401
//
// 重点覆盖此前 model-type→cmdb:admin 专属映射因路径前缀截断而失效的缺陷。
func TestAutoPermissionMiddleware_CMDB(t *testing.T) {
	m := NewRBACMiddleware(&Provider{})

	reach := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := m.AutoPermissionMiddleware(reach)

	cases := []struct {
		name   string
		role   string
		method string
		path   string
		want   int
	}{
		// 无角色 → 401
		{"no role unauthorized", "", "GET", "/api/v1/cmdb/ci", http.StatusUnauthorized},

		// viewer：只读
		{"viewer read ci list", "viewer", "GET", "/api/v1/cmdb/ci", http.StatusOK},
		{"viewer read ci by id", "viewer", "GET", "/api/v1/cmdb/ci/mt-123", http.StatusOK},
		{"viewer read search", "viewer", "GET", "/api/v1/cmdb/search", http.StatusOK},
		{"viewer read model-type", "viewer", "GET", "/api/v1/cmdb/model-type", http.StatusOK},
		{"viewer read collection-tasks", "viewer", "GET", "/api/v1/cmdb/collection-tasks", http.StatusOK},
		{"viewer create ci forbidden", "viewer", "POST", "/api/v1/cmdb/ci", http.StatusForbidden},
		{"viewer delete ci forbidden", "viewer", "DELETE", "/api/v1/cmdb/ci/mt-123", http.StatusForbidden},

		// operator：可读写数据，但删除(cmdb:delete)与模型管理(cmdb:admin)需 admin
		{"operator create ci", "operator", "POST", "/api/v1/cmdb/ci", http.StatusOK},
		{"operator update ci", "operator", "PUT", "/api/v1/cmdb/ci/mt-123", http.StatusOK},
		{"operator delete ci forbidden", "operator", "DELETE", "/api/v1/cmdb/ci/mt-123", http.StatusForbidden},
		{"operator relation write", "operator", "POST", "/api/v1/cmdb/relation", http.StatusOK},
		{"operator onboard", "operator", "POST", "/api/v1/cmdb/onboard", http.StatusOK},
		{"operator run collection task", "operator", "POST", "/api/v1/cmdb/collection-tasks/t1/run", http.StatusOK},
		// 关键修复点：model-type 写操作必须 admin，operator 应被拒绝
		{"operator create model-type forbidden", "operator", "POST", "/api/v1/cmdb/model-type", http.StatusForbidden},
		{"operator update model-type forbidden", "operator", "PUT", "/api/v1/cmdb/model-type/mt1", http.StatusForbidden},
		{"operator delete model-type forbidden", "operator", "DELETE", "/api/v1/cmdb/model-type/mt1", http.StatusForbidden},

		// admin：全部放行（含 model-type 管理与删除）
		{"admin create model-type", "admin", "POST", "/api/v1/cmdb/model-type", http.StatusOK},
		{"admin delete model-type", "admin", "DELETE", "/api/v1/cmdb/model-type/mt1", http.StatusOK},
		{"admin delete ci", "admin", "DELETE", "/api/v1/cmdb/ci/mt-123", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			req = withRole(req, c.role)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("method=%s path=%s role=%q: got %d want %d", c.method, c.path, c.role, rec.Code, c.want)
			}
		})
	}
}

// TestResolvePathPermission 单测前缀解析：更长前缀优先。
func TestResolvePathPermission(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
		ok     bool
	}{
		{"GET", "/api/v1/cmdb/ci", PermissionCMDBRead, true},
		{"POST", "/api/v1/cmdb/ci", PermissionCMDBWrite, true},
		{"POST", "/api/v1/cmdb/model-type", PermissionCMDBAdmin, true},
		{"DELETE", "/api/v1/cmdb/model-type/mt1", PermissionCMDBAdmin, true},
		{"POST", "/api/v1/cmdb/ci/abc", PermissionCMDBWrite, true},
		{"GET", "/api/v1/unknown/xyz", "", false},
	}
	for _, c := range cases {
		got, ok := resolvePathPermission(c.method, c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("resolvePathPermission(%s,%s) = (%q,%v), want (%q,%v)", c.method, c.path, got, ok, c.want, c.ok)
		}
	}
}

// withTenant 把 tenant 写入 context，模拟认证中间件注入的租户声明。
func withTenant(req *http.Request, tenant string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), "tenant_id", tenant))
}

// TestAutoPermissionMiddleware_NewModules 覆盖此前未登记权限映射的模块:
// 修复前这些前缀未出现在 PathPermissionMapping 中, 中间件只认证不鉴权,
// 任意已登录用户(含 viewer)都能增删改; 本用例锁定修复后的门控语义。
func TestAutoPermissionMiddleware_NewModules(t *testing.T) {
	m := NewRBACMiddleware(&Provider{})
	reach := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := m.AutoPermissionMiddleware(reach)

	cases := []struct {
		name   string
		role   string
		method string
		path   string
		want   int
	}{
		// 知识库:viewer 只读, 写拒绝; 删除属 manage, operator 亦拒绝
		{"viewer read knowledge", RoleViewer, "GET", "/api/v1/knowledge/abc", http.StatusOK},
		{"viewer write knowledge forbidden", RoleViewer, "POST", "/api/v1/knowledge", http.StatusForbidden},
		{"viewer update knowledge forbidden", RoleViewer, "PUT", "/api/v1/knowledge/abc", http.StatusForbidden},
		{"viewer delete knowledge forbidden", RoleViewer, "DELETE", "/api/v1/knowledge/abc", http.StatusForbidden},
		{"viewer reindex forbidden", RoleViewer, "POST", "/api/v1/knowledge/reindex", http.StatusForbidden},
		{"operator write knowledge", RoleOperator, "POST", "/api/v1/knowledge", http.StatusOK},
		{"operator delete knowledge forbidden", RoleOperator, "DELETE", "/api/v1/knowledge/abc", http.StatusForbidden},
		{"admin delete knowledge", RoleAdmin, "DELETE", "/api/v1/knowledge/abc", http.StatusOK},

		// AI 助手:viewer 只读, 会话写入拒绝
		{"viewer read ai conversations", RoleViewer, "GET", "/api/v1/ai/conversations", http.StatusOK},
		{"viewer save conversation forbidden", RoleViewer, "POST", "/api/v1/ai/conversations", http.StatusForbidden},
		{"operator chat", RoleOperator, "POST", "/api/v1/ai/chat", http.StatusOK},

		// AI 执行类动作:提升到 ai:tool:exec, viewer 拒绝
		{"viewer execute tool forbidden", RoleViewer, "POST", "/api/v1/ai/tools/cmdb_query/execute", http.StatusForbidden},
		{"operator execute tool", RoleOperator, "POST", "/api/v1/ai/tools/cmdb_query/execute", http.StatusOK},

		// 脚本:执行属 script:exec(高危), viewer 拒绝
		{"viewer read scripts", RoleViewer, "GET", "/api/v1/scripts", http.StatusOK},
		{"viewer write script forbidden", RoleViewer, "POST", "/api/v1/scripts", http.StatusForbidden},
		{"viewer execute script forbidden", RoleViewer, "POST", "/api/v1/scripts/s1/execute", http.StatusForbidden},
		{"operator execute script", RoleOperator, "POST", "/api/v1/scripts/s1/execute", http.StatusOK},

		// 工作流:执行属 workflow:exec
		{"viewer execute workflow forbidden", RoleViewer, "POST", "/api/v1/workflows/w1/execute", http.StatusForbidden},
		{"operator execute workflow", RoleOperator, "POST", "/api/v1/workflows/w1/execute", http.StatusOK},
		{"operator cancel execution", RoleOperator, "POST", "/api/v1/workflows/executions/e1/cancel", http.StatusOK},

		// 定时任务
		{"viewer read schedules", RoleViewer, "GET", "/api/v1/schedules", http.StatusOK},
		{"viewer create schedule forbidden", RoleViewer, "POST", "/api/v1/schedules", http.StatusForbidden},
		{"operator create schedule", RoleOperator, "POST", "/api/v1/schedules", http.StatusOK},

		// 入站渠道配置(属 automation 域)
		{"viewer read inbound channels", RoleViewer, "GET", "/api/v1/automation/inbound/channels", http.StatusOK},
		{"viewer upsert inbound channel forbidden", RoleViewer, "POST", "/api/v1/automation/inbound/channels", http.StatusForbidden},

		// 报表
		{"viewer read dashboards", RoleViewer, "GET", "/api/v1/report/dashboards", http.StatusOK},
		{"viewer create dashboard forbidden", RoleViewer, "POST", "/api/v1/report/dashboards", http.StatusForbidden},
		{"viewer custom query forbidden", RoleViewer, "POST", "/api/v1/report/query", http.StatusForbidden},

		// 低代码
		{"viewer read lowcode pages", RoleViewer, "GET", "/api/v1/lowcode/pages", http.StatusOK},
		{"viewer create lowcode page forbidden", RoleViewer, "POST", "/api/v1/lowcode/pages", http.StatusForbidden},

		// 发布管理
		{"viewer read deploy apps", RoleViewer, "GET", "/api/v1/deploy/apps", http.StatusOK},
		{"viewer create deploy app forbidden", RoleViewer, "POST", "/api/v1/deploy/apps", http.StatusForbidden},
		{"viewer deploy release forbidden", RoleViewer, "POST", "/api/v1/deploy/releases/r1/deploy", http.StatusForbidden},
		{"operator deploy release", RoleOperator, "POST", "/api/v1/deploy/releases/r1/deploy", http.StatusOK},

		// 凭据库:默认不授予 viewer
		{"viewer read credentials forbidden", RoleViewer, "GET", "/api/v1/keys", http.StatusForbidden},
		{"operator read credentials", RoleOperator, "GET", "/api/v1/keys", http.StatusOK},
		{"viewer delete credential forbidden", RoleViewer, "DELETE", "/api/v1/keys/k1", http.StatusForbidden},

		// 云资源纳管:与 CMDB 权限同级
		{"viewer read cloud providers", RoleViewer, "GET", "/api/v1/cloud/providers", http.StatusOK},
		{"viewer create cloud credential forbidden", RoleViewer, "POST", "/api/v1/cloud/credentials", http.StatusForbidden},
		{"operator create cloud credential", RoleOperator, "POST", "/api/v1/cloud/credentials", http.StatusOK},
		{"operator delete cloud credential forbidden", RoleOperator, "DELETE", "/api/v1/cloud/credentials/c1", http.StatusForbidden},

		// 全景视图 / 值班表 / 系统配置
		{"viewer read panorama", RoleViewer, "GET", "/api/v1/panorama", http.StatusOK},
		{"viewer read oncall", RoleViewer, "GET", "/api/v1/oncall", http.StatusOK},
		{"viewer write oncall forbidden", RoleViewer, "POST", "/api/v1/oncall", http.StatusForbidden},
		{"viewer read system status", RoleViewer, "GET", "/api/v1/system/status", http.StatusOK},
		{"operator read system config forbidden", RoleOperator, "GET", "/api/v1/system/config", http.StatusForbidden},
		{"admin read system config", RoleAdmin, "GET", "/api/v1/system/config", http.StatusOK},

		// Agent 文件:与 Agent 管理同级(viewer 可读, 不可写)
		{"viewer read files", RoleViewer, "GET", "/api/v1/files", http.StatusOK},
		{"viewer upload file forbidden", RoleViewer, "POST", "/api/v1/files/upload", http.StatusForbidden},
		{"operator upload file", RoleOperator, "POST", "/api/v1/files/upload", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			req = withRole(req, c.role)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("method=%s path=%s role=%q: got %d want %d", c.method, c.path, c.role, rec.Code, c.want)
			}
		})
	}
}

// TestResolveActionPermission 验证动作级映射(段通配)优先于前缀匹配,
// 且方法或段数不匹配时必须回落到前缀规则。
func TestResolveActionPermission(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
	}{
		// {id}/execute 形态
		{"POST", "/api/v1/scripts/s1/execute", PermissionScriptExec},
		{"POST", "/api/v1/workflows/w1/execute", PermissionWorkflowExec},
		{"POST", "/api/v1/workflows/executions/e1/cancel", PermissionWorkflowExec},
		{"POST", "/api/v1/ai/tools/cmdb_query/execute", PermissionAIToolExec},
		{"POST", "/api/v1/ai/remediate/a1/execute", PermissionAIToolExec},
		{"POST", "/api/v1/ai/runbooks/rb1/execute", PermissionAIToolExec},
		{"POST", "/api/v1/deploy/releases/r1/deploy", PermissionDeployWrite},
		{"POST", "/api/v1/deploy/releases/r1/rollback", PermissionDeployWrite},
		{"POST", "/api/v1/scripts/s1/approve", PermissionScriptWrite},
		// 方法不匹配 → 回落前缀(script:read)
		{"GET", "/api/v1/scripts/s1/execute", PermissionScriptRead},
		// 段数不符 → 回落前缀(script:write)
		{"POST", "/api/v1/scripts/s1", PermissionScriptWrite},
		// 模块路径不符 → 回落前缀
		{"POST", "/api/v1/ai/tools/custom", PermissionAIWrite},
	}
	for _, c := range cases {
		got, ok := resolvePathPermission(c.method, c.path)
		if !ok || got != c.want {
			t.Errorf("resolvePathPermission(%s,%s) = (%q,%v), want %q", c.method, c.path, got, ok, c.want)
		}
	}
}

// TestAllPermissionsGrantedToAdmin 保证每个声明的权限点都授予了内置 admin 角色,
// 避免新增权限点后角色编辑页展示缺口。
func TestAllPermissionsGrantedToAdmin(t *testing.T) {
	granted := make(map[string]struct{}, len(RolePermissions[RoleAdmin]))
	for _, p := range RolePermissions[RoleAdmin] {
		granted[p] = struct{}{}
	}
	if len(AllPermissions) != len(granted) {
		// 仅提示:RolePermissions 可能包含 AllPermissions 未收录的点
		t.Logf("AllPermissions=%d, admin 授权=%d", len(AllPermissions), len(granted))
	}
	for _, p := range AllPermissions {
		if _, ok := granted[p]; !ok {
			t.Errorf("权限点 %q 未授予内置 admin 角色", p)
		}
	}
}

// TestBackfillModulePermissions 验证存量自定义角色按等价能力补齐新模块权限点:
// 写角色补读写、只读角色补只读、具备 ai:tool:exec 的角色补脚本/工作流执行;
// 反复调用必须幂等。
func TestBackfillModulePermissions(t *testing.T) {
	has := func(list []string, p string) bool {
		for _, x := range list {
			if x == p {
				return true
			}
		}
		return false
	}

	// 写入型角色:补齐读写
	writer := []string{PermissionCMDBRead, PermissionCMDBWrite}
	got, changed := BackfillModulePermissions(writer)
	if !changed {
		t.Fatal("写角色应触发补齐")
	}
	for _, p := range []string{
		PermissionKnowledgeRead, PermissionKnowledgeWrite, PermissionKnowledgeManage,
		PermissionReportRead, PermissionReportWrite, PermissionCredentialRead,
	} {
		if !has(got, p) {
			t.Errorf("写角色应补齐 %q", p)
		}
	}
	if has(got, PermissionScriptExec) {
		t.Error("写角色未持有 ai:tool:exec, 不应补齐 script:exec")
	}
	if _, again := BackfillModulePermissions(got); again {
		t.Error("补齐必须幂等: 第二次调用不应再产生变化")
	}

	// 只读角色:只补只读, 不补写与凭据
	got, changed = BackfillModulePermissions([]string{PermissionCMDBRead})
	if !changed {
		t.Fatal("只读角色应触发补齐")
	}
	if !has(got, PermissionKnowledgeRead) || !has(got, PermissionAIRead) {
		t.Error("只读角色应补齐只读权限点")
	}
	if has(got, PermissionKnowledgeWrite) || has(got, PermissionCredentialRead) {
		t.Error("只读角色不应补齐写权限与凭据查看")
	}

	// 工具执行角色:补脚本/工作流执行
	got, _ = BackfillModulePermissions([]string{PermissionCMDBRead, PermissionAIToolExec})
	if !has(got, PermissionScriptExec) || !has(got, PermissionWorkflowExec) {
		t.Error("持有 ai:tool:exec 的角色应补齐 script:exec 与 workflow:exec")
	}

	// 空权限角色:无兄弟权限可参照, 不补齐(保持最小权限)
	if _, changed = BackfillModulePermissions(nil); changed {
		t.Error("无任何权限的角色不应被自动补权")
	}
}

// 以 resolver 返回的权限为准(而非写死的 RolePermissions),使角色管理页的编辑实时生效。
// 这里用一个内存 map 模拟 IAM 角色库,无需 PG。
func TestAutoPermissionMiddleware_Resolver(t *testing.T) {
	// "unknown tenant falls back" 子用例依赖多租户语义(未带租户时应回落写死映射),
	// 单租户模式会强制 "default" 使该路径不可达,故在此启用多租户。
	prev := MultiTenantEnabled()
	SetMultiTenant(true)
	defer SetMultiTenant(prev)

	// 模拟 IAM 角色库:operator 被收窄为仅 cmdb:read(不再有 cmdb:write)
	iamRoles := map[string][]string{
		"operator": {PermissionCMDBRead},
		"editor":   {PermissionCMDBRead, PermissionCMDBWrite, PermissionCMDBDelete},
	}
	m := NewRBACMiddleware(&Provider{})
	m.SetPermissionResolver(func(tenantID, role string) []string {
		if tenantID != "default" {
			return nil
		}
		return iamRoles[role]
	})

	reach := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := m.AutoPermissionMiddleware(reach)

	cases := []struct {
		name   string
		role   string
		method string
		path   string
		want   int
	}{
		// resolver 把 operator 收窄为只读:写/删均 403(即便写死映射里 operator 可写)
		{"resolver operator read ok", "operator", "GET", "/api/v1/cmdb/ci", http.StatusOK},
		{"resolver operator write forbidden", "operator", "POST", "/api/v1/cmdb/ci", http.StatusForbidden},
		{"resolver operator delete forbidden", "operator", "DELETE", "/api/v1/cmdb/ci/x", http.StatusForbidden},
		// editor 由 resolver 授予 cmdb:delete:可删
		{"resolver editor delete ok", "editor", "DELETE", "/api/v1/cmdb/ci/x", http.StatusOK},
		// 未知租户:resolver 返回 nil → 回落写死映射(operator 可写)
		{"unknown tenant falls back", "operator", "POST", "/api/v1/cmdb/ci", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			req = withRole(req, c.role)
			if c.name != "unknown tenant falls back" {
				req = withTenant(req, "default")
			}
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("method=%s path=%s role=%q: got %d want %d", c.method, c.path, c.role, rec.Code, c.want)
			}
		})
	}
}
