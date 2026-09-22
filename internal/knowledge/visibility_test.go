package knowledge

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// 可见性字段归一化回归测试。
//
// 背景(实测故障): view_permission 的语义是 all(默认), 建表默认值同为 'all',
// 但 Create/Update 曾经原样落库, 于是大量「已发布」文章的该字段是空串;
// 检索侧对可见性做 fail-closed 判定(只有公开可召回), 空串被判成"受限"
// → 这批文章静默变成 AI 检索不可见(库中 published 文章仅 2/5 可召回)。
//
// 因此这里锁两条:
//  1. 写入路径必须把留空补成显式默认值(而不是依赖检索侧兜底);
//  2. 显式声明的取值("team"/"owner")不得被覆盖。

func TestNormalizeVisibilityDefaults(t *testing.T) {
	cases := []struct {
		name     string
		in       Article
		wantView string
		wantEdit string
	}{
		{"全部留空", Article{}, ViewPermissionAll, EditPermissionOwner},
		{"view 纯空白", Article{ViewPermission: "  "}, ViewPermissionAll, EditPermissionOwner},
		{"edit 纯空白", Article{EditPermission: "\t"}, ViewPermissionAll, EditPermissionOwner},
		{"显式 team 不覆盖", Article{ViewPermission: "team"}, ViewPermissionTeam, EditPermissionOwner},
		{"显式 owner 不覆盖", Article{ViewPermission: "owner", EditPermission: "all"},
			ViewPermissionOwner, EditPermissionAll},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.in
			a.normalizeVisibility()
			if a.ViewPermission != tc.wantView {
				t.Errorf("view_permission = %q, want %q", a.ViewPermission, tc.wantView)
			}
			if a.EditPermission != tc.wantEdit {
				t.Errorf("edit_permission = %q, want %q", a.EditPermission, tc.wantEdit)
			}
		})
	}
}

// TestMemoryStoreCreateNormalizesVisibility 锁内存 store 的写入归一。
func TestMemoryStoreCreateNormalizesVisibility(t *testing.T) {
	s := NewMemoryStore()
	a := &Article{Title: "空可见性", Content: "x", TenantID: "default"}
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.ViewPermission != ViewPermissionAll {
		t.Fatalf("create 后 view_permission = %q, want %q", a.ViewPermission, ViewPermissionAll)
	}
	// 编辑保存时同理: 前端未带该字段也不能把公开文章变成"空=受限"
	a.ViewPermission = ""
	if err := s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	if a.ViewPermission != ViewPermissionAll {
		t.Fatalf("update 后 view_permission = %q, want %q", a.ViewPermission, ViewPermissionAll)
	}
	got, _ := s.Get(context.Background(), a.ID, "default")
	if got == nil || got.ViewPermission != ViewPermissionAll {
		t.Fatalf("落库后应为 all, got %+v", got)
	}
}

// TestPgStoreCreateNormalizesVisibility 锁 PG store 的写入归一。
// 正文留空 → syncChunks 只做一次 DELETE(不会调用 embedder), 故期望集很小。
func TestPgStoreCreateNormalizesVisibility(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	embedCalls := 0
	emb := &stubEmbedder{fn: func(ctx context.Context, text string) ([]float32, error) {
		embedCalls++
		return []float32{0.1}, nil
	}}
	s := &pgStore{db: db, embedder: emb, vectorEnabled: true}

	mock.ExpectExec("INSERT INTO knowledge_articles").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM knowledge_chunks").
		WillReturnResult(sqlmock.NewResult(0, 0))

	a := &Article{TenantID: "default", Title: "空可见性", Content: "  "}
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.ViewPermission != ViewPermissionAll {
		t.Fatalf("create 后 view_permission = %q, want %q", a.ViewPermission, ViewPermissionAll)
	}
	if embedCalls != 0 {
		t.Fatalf("空正文不应触发 embedding, got %d 次", embedCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
