package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// 可见性归属(迭代一 / 批次1)回归测试。
//
// 背景: knowledge_articles 此前只有 view_permission(all/team/owner) 而**没有任何归属列**,
// 于是 team/owner 两种可见性在检索侧无法判定归属, 只能 fail-closed 一律不召回
// —— 作者以为「发给团队了」, 实际对 AI 永久不可见。本批次补齐两个判定依据:
//   author_id        → owner 判定(提问者 = 作者)
//   owner_group_ids  → team 判定(提问者所属团队与该数组有交集即可见, 多值以支持跨团队 SOP)
//
// 这里锁四类不变量:
//  1. 归一: trim / 去空串 / 去重 / 稳定排序, 且**永不返回 nil**(pq.Array(nil) 会写 SQL NULL
//     撞 NOT NULL 约束, 而 pq.Array([]string{}) 才是 '{}');
//  2. 归属只在 team 语义下存在(可见范围切回 all/owner 必须清空, 否则残留旧团队造成越权);
//  3. team 可见但无归属 = 对任何人都不成立 → 写入时必须报错而非静默不可见;
//  4. schema 层用 NOT NULL DEFAULT '{}' 消灭 NULL/空串双态(view_permission 空串缺陷同因)。

func TestNormalizeOwnerGroupIDs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil 必须变成非 nil 空切片", nil, []string{}},
		{"空切片保持非 nil", []string{}, []string{}},
		{"去空串与纯空白", []string{"", "  ", "\t"}, []string{}},
		{"trim 前后空白", []string{" ops ", "\tnet\n"}, []string{"net", "ops"}},
		{"去重", []string{"ops", "ops", "net", "ops"}, []string{"net", "ops"}},
		{"稳定升序", []string{"net", "db", "ops"}, []string{"db", "net", "ops"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeOwnerGroupIDs(tc.in)
			if got == nil {
				t.Fatalf("normalizeOwnerGroupIDs(%v) = nil; 必须返回非 nil 切片(pq.Array(nil) 写 SQL NULL 撞 NOT NULL)", tc.in)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("normalizeOwnerGroupIDs(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeOwnershipOnlyKeepsGroupsForTeam 锁「归属只在 team 语义下存在」。
// 若 all/owner 下保留归属, 用户把可见范围 team → all → team 来回切换时会浮现上一轮残留的
// 团队归属: 编辑器里看不到, 却已对那批团队生效。
func TestNormalizeOwnershipOnlyKeepsGroupsForTeam(t *testing.T) {
	cases := []struct {
		name     string
		in       Article
		wantView string
		want     []string
	}{
		{"team 保留归属", Article{ViewPermission: ViewPermissionTeam, OwnerGroupIDs: []string{"ops", "net"}},
			ViewPermissionTeam, []string{"net", "ops"}},
		{"all 清空归属", Article{ViewPermission: ViewPermissionAll, OwnerGroupIDs: []string{"ops"}},
			ViewPermissionAll, []string{}},
		{"owner 清空归属", Article{ViewPermission: ViewPermissionOwner, OwnerGroupIDs: []string{"ops"}},
			ViewPermissionOwner, []string{}},
		{"留空(归一为 all)也清空归属", Article{OwnerGroupIDs: []string{"ops"}},
			ViewPermissionAll, []string{}},
		{"team 空归属保持非 nil", Article{ViewPermission: ViewPermissionTeam},
			ViewPermissionTeam, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.in
			a.normalizeVisibility()
			if a.ViewPermission != tc.wantView {
				t.Fatalf("view_permission = %q, want %q", a.ViewPermission, tc.wantView)
			}
			if a.OwnerGroupIDs == nil {
				t.Fatalf("OwnerGroupIDs 为 nil; 必须非 nil")
			}
			if !reflect.DeepEqual(a.OwnerGroupIDs, tc.want) {
				t.Errorf("OwnerGroupIDs = %v, want %v", a.OwnerGroupIDs, tc.want)
			}
		})
	}
}

func TestValidateOwnership(t *testing.T) {
	cases := []struct {
		name    string
		in      Article
		wantErr bool
	}{
		{"team 有归属", Article{ViewPermission: ViewPermissionTeam, OwnerGroupIDs: []string{"ops"}}, false},
		{"team 无归属", Article{ViewPermission: ViewPermissionTeam}, true},
		{"team 归属经归一后为空", Article{ViewPermission: ViewPermissionTeam, OwnerGroupIDs: []string{" ", ""}}, true},
		{"all 无归属", Article{ViewPermission: ViewPermissionAll}, false},
		{"owner 无归属", Article{ViewPermission: ViewPermissionOwner}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.in
			// 与 handler 一致: 先归一(留空 view 语义是 all), 再校验。
			a.normalizeVisibility()
			err := validateOwnership(&a)
			if tc.wantErr && err == nil {
				t.Fatalf("期望报错(team 无归属 = 对任何人都不成立), 实际通过")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("不应报错, got %v", err)
			}
		})
	}
}

// TestOwnerGroupIDsColumnIsNotNullWithEmptyArrayDefault 是 schema 契约守卫。
//
// owner_group_ids 必须是 NOT NULL DEFAULT '{}'::text[]: 允许 NULL 就意味着"空"有 NULL 与
// 空数组两种表示, 数组重叠谓词遇到 NULL 行语义不可预期 —— 与 view_permission 空串缺陷同因。
// 同时锁住自愈 DDL 与迁移文件双写一致(只改一处会导致全新建库与升级库结构不同)。
func TestOwnerGroupIDsColumnIsNotNullWithEmptyArrayDefault(t *testing.T) {
	const wantDDL = `owner_group_ids TEXT[] NOT NULL DEFAULT '{}'::text[]`

	src, err := os.ReadFile("pgstore.go")
	if err != nil {
		t.Fatalf("read pgstore.go: %v", err)
	}
	if !strings.Contains(string(src), wantDDL) {
		t.Errorf("pgstore.go 自愈 DDL 缺少 %q;\n"+
			"可空列会让「空归属」出现 NULL 与空数组两种表示, 数组重叠谓词对 NULL 行结果不可预期", wantDDL)
	}
	// 不可写成可空形态(即同一句里没有 NOT NULL)。
	if strings.Contains(string(src), "ADD COLUMN IF NOT EXISTS owner_group_ids TEXT[]`") {
		t.Errorf("pgstore.go 把 owner_group_ids 建成了可空列; 必须 NOT NULL DEFAULT '{}'::text[]")
	}

	mig, err := os.ReadFile("../../deploy/migrations/027_knowledge_visibility.sql")
	if err != nil {
		t.Fatalf("read 027_knowledge_visibility.sql: %v", err)
	}
	if !strings.Contains(string(mig), wantDDL) {
		t.Errorf("027_knowledge_visibility.sql 缺少 %q(与自愈 DDL 必须双写一致)", wantDDL)
	}
}

// TestPgStoreCreatePersistsOwnership 锁写入侧: INSERT 必须带上 author_id 与 owner_group_ids,
// 且数组以 pq 编码写入(非 nil → '{}' 而非 NULL)。
func TestPgStoreCreatePersistsOwnership(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	s := &pgStore{db: db, vectorEnabled: false}

	// 15 个参数按 INSERT 列序: ... 第 12 个 author_id, 第 13 个 owner_group_ids。
	mock.ExpectExec("INSERT INTO knowledge_articles").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), "u-owner", `{"net","ops"}`, sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(0, 0))

	a := &Article{
		TenantID: "default", Title: "跨团队 SOP", Content: "  ",
		ViewPermission: ViewPermissionTeam, AuthorID: "u-owner",
		OwnerGroupIDs: []string{"ops", "net", "ops"},
	}
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if want := []string{"net", "ops"}; !reflect.DeepEqual(a.OwnerGroupIDs, want) {
		t.Errorf("落库前应归一为 %v, got %v", want, a.OwnerGroupIDs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPgStoreGetReadsOwnershipArrays 锁读取侧: 数组列正确解析, 且历史 NULL 行不炸。
func TestPgStoreGetReadsOwnershipArrays(t *testing.T) {
	selectSQL := `SELECT id,tenant_id,title,content,category,tags,views,status,version,expires_at,view_permission,edit_permission,author_id,owner_group_ids,created_at,updated_at FROM knowledge_articles WHERE id=\$1 AND tenant_id=\$2`
	cols := []string{"id", "tenant_id", "title", "content", "category", "tags",
		"views", "status", "version", "expires_at", "view_permission", "edit_permission",
		"author_id", "owner_group_ids", "created_at", "updated_at"}

	t.Run("正常数组", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close()
		s := &pgStore{db: db, vectorEnabled: false}
		now := time.Now()
		mock.ExpectQuery(selectSQL).WithArgs("a1", "default").
			WillReturnRows(sqlmock.NewRows(cols).
				AddRow("a1", "default", "T", "C", "cat", []byte("[]"), 0, "published", 1, nil,
					"team", "owner", "u-owner", []byte("{net,ops}"), now, now))

		a, err := s.Get(context.Background(), "a1", "default")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if a.AuthorID != "u-owner" {
			t.Errorf("author_id = %q, want %q", a.AuthorID, "u-owner")
		}
		if want := []string{"net", "ops"}; !reflect.DeepEqual(a.OwnerGroupIDs, want) {
			t.Errorf("owner_group_ids = %v, want %v", a.OwnerGroupIDs, want)
		}
	})

	t.Run("NULL 数组不炸", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close()
		s := &pgStore{db: db, vectorEnabled: false}
		now := time.Now()
		// 升级库若该列曾被以可空形态建出, 历史行会是 NULL。读取侧必须容忍(不能整条查询失败)。
		mock.ExpectQuery(selectSQL).WithArgs("a2", "default").
			WillReturnRows(sqlmock.NewRows(cols).
				AddRow("a2", "default", "T", "C", "cat", []byte("[]"), 0, "published", 1, nil,
					"all", "owner", "", nil, now, now))

		a, err := s.Get(context.Background(), "a2", "default")
		if err != nil {
			t.Fatalf("NULL owner_group_ids 不应让整条查询失败: %v", err)
		}
		if len(a.OwnerGroupIDs) != 0 {
			t.Errorf("NULL 数组应解析为空, got %v", a.OwnerGroupIDs)
		}
	})
}

// newOwnershipTestContext 注入 handler 所需的租户与用户身份(与中间件一致: 裸 key)。
func newOwnershipTestContext(tenant, user string) context.Context {
	ctx := context.WithValue(context.Background(), "tenant_id", tenant)
	return context.WithValue(ctx, "user_id", user)
}

func postArticle(t *testing.T, svc *Service, ctx context.Context, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge", strings.NewReader(body))
	rec := httptest.NewRecorder()
	svc.handleCreate(rec, req.WithContext(ctx))
	return rec
}

// TestHandleCreateOwnership 覆盖创建接口的三条归属契约:
//   - team 可见无归属 → 400(否则该文章对任何人都不成立, 却是静默的);
//   - 正常创建 → author_id = 当前登录用户(owner 判定的唯一依据);
//   - 非 team 可见 → 归属被清空(前端残留选择不得造成越权)。
func TestHandleCreateOwnership(t *testing.T) {
	ctx := newOwnershipTestContext("default", "u-owner")

	t.Run("team 无归属被拒", func(t *testing.T) {
		svc := NewService(nil)
		rec := postArticle(t, svc, ctx, `{"title":"团队 SOP","content":"x","view_permission":"team"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "归属团队") {
			t.Errorf("错误文案应说明缺少归属团队, got %s", rec.Body.String())
		}
	})

	t.Run("team 有归属创建成功且记录作者", func(t *testing.T) {
		svc := NewService(nil)
		rec := postArticle(t, svc, ctx,
			`{"title":"跨团队 SOP","content":"x","view_permission":"team","owner_group_ids":["ops","net","ops"]}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
		}
		var got Article
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.AuthorID != "u-owner" {
			t.Errorf("author_id = %q, want %q(必须写入当前登录用户)", got.AuthorID, "u-owner")
		}
		if want := []string{"net", "ops"}; !reflect.DeepEqual(got.OwnerGroupIDs, want) {
			t.Errorf("owner_group_ids = %v, want %v", got.OwnerGroupIDs, want)
		}
		// 落库后读取一致(内存 store 走同一归一逻辑)
		stored, _ := svc.store.Get(context.Background(), got.ID, "default")
		if stored == nil || stored.AuthorID != "u-owner" {
			t.Errorf("落库后 author_id 应保留, got %+v", stored)
		}
	})

	t.Run("非 team 可见清空归属", func(t *testing.T) {
		svc := NewService(nil)
		rec := postArticle(t, svc, ctx,
			`{"title":"公开文章","content":"x","view_permission":"all","owner_group_ids":["ops"]}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
		}
		var got Article
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(got.OwnerGroupIDs) != 0 {
			t.Errorf("all 可见时归属必须清空(防 team→all→team 残留越权), got %v", got.OwnerGroupIDs)
		}
	})
}

// TestHandleUpdateOwnership 覆盖编辑接口: 作者不可被改写, 未带归属时保留原值, 归属清空后被拒。
func TestHandleUpdateOwnership(t *testing.T) {
	ctx := newOwnershipTestContext("default", "u-other")

	svc := NewService(nil)
	stored := &Article{
		TenantID: "default", Title: "旧标题", Content: "x",
		ViewPermission: ViewPermissionTeam, EditPermission: EditPermissionOwner,
		AuthorID: "u-owner", OwnerGroupIDs: []string{"ops"},
	}
	if err := svc.store.Create(ctx, stored); err != nil {
		t.Fatalf("seed: %v", err)
	}

	put := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/knowledge/"+stored.ID, strings.NewReader(body))
		req.SetPathValue("id", stored.ID)
		rec := httptest.NewRecorder()
		svc.handleUpdate(rec, req.WithContext(ctx))
		return rec
	}

	// 1) 不带归属地编辑 team 文章 → 保留原归属, 不应被清空也不应被拒。
	rec := put(`{"title":"新标题","content":"y","view_permission":"team","owner_group_ids":["ops"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got Article
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.AuthorID != "u-owner" {
		t.Errorf("author_id = %q, want 原值 %q(不得被编辑者改写)", got.AuthorID, "u-owner")
	}
	if want := []string{"ops"}; !reflect.DeepEqual(got.OwnerGroupIDs, want) {
		t.Errorf("owner_group_ids = %v, want %v", got.OwnerGroupIDs, want)
	}

	// 2) 显式清空归属 → 400(team 无归属等于对所有人不可见)。
	rec = put(`{"title":"新标题","content":"y","view_permission":"team","owner_group_ids":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("清空归属后 status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
