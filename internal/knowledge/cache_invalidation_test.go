package knowledge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// —— 检索缓存失效(可见性修复的配套) ——
//
// AI 侧对检索结果有 5 分钟 TTL 缓存(searchCache + 引擎级 toolCache 两条独立路径)。
// 知识库一旦发生「改变可召回集合」的写操作而不同步失效, 已下架/已收紧可见性/已删除的内容
// 仍会在窗口内被召回并进入模型上下文 —— fail-closed 的可见性过滤被开了个时间窗。
// 因此这里逐个锁住各写操作是否触发回调。

// countingInvalidator 返回一个计数闭包, 用于断言回调被调用了几次。
func countingInvalidator() (*int, func()) {
	n := 0
	return &n, func() { n++ }
}

// TestCacheInvalidatedOnArticleWrites 覆盖创建 / 编辑 / 状态流转 / 删除四条写路径。
func TestCacheInvalidatedOnArticleWrites(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()
	got, hook := countingInvalidator()
	svc.SetCacheInvalidator(hook)

	// 1) 内部创建(CreateArticle: ITSM 沉淀 / AI 主动进化走这条路)
	a := &Article{Title: "磁盘满处理", Content: "du -sh", Category: "故障处理", Status: StatusPublished}
	if err := svc.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}
	if *got != 1 {
		t.Fatalf("CreateArticle 应触发 1 次失效, 实际 %d", *got)
	}

	// 2) HTTP 创建
	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge",
		strings.NewReader(`{"title":"CPU 高排查","content":"top","force_duplicate":true}`))
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec := httptest.NewRecorder()
	svc.handleCreate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("handleCreate status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if *got != 2 {
		t.Fatalf("handleCreate 应触发失效, 累计应为 2, 实际 %d", *got)
	}

	// 3) 编辑保存
	req = httptest.NewRequest(http.MethodPut, "/api/v1/knowledge/"+a.ID,
		strings.NewReader(`{"title":"磁盘满处理(已修订)","content":"du -sh /var"}`))
	req.SetPathValue("id", a.ID)
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec = httptest.NewRecorder()
	svc.handleUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleUpdate status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if *got != 3 {
		t.Fatalf("handleUpdate 应触发失效, 累计应为 3, 实际 %d", *got)
	}

	// 4) 状态流转(发布 → 归档: 可召回集合收窄)
	if _, err := svc.TransitionArticle(ctx, a.ID, "default", StatusArchived, "过期下架", "tester"); err != nil {
		t.Fatalf("TransitionArticle: %v", err)
	}
	if *got != 4 {
		t.Fatalf("TransitionArticle 应触发失效, 累计应为 4, 实际 %d", *got)
	}

	// 5) 删除
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/knowledge/"+a.ID, nil)
	req.SetPathValue("id", a.ID)
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec = httptest.NewRecorder()
	svc.handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleDelete status = %d", rec.Code)
	}
	if *got != 5 {
		t.Fatalf("handleDelete 应触发失效, 累计应为 5, 实际 %d", *got)
	}
}

// TestCacheInvalidatedOnExpiryArchive 验证到期自动下架也清缓存。
func TestCacheInvalidatedOnExpiryArchive(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT id, tenant_id FROM knowledge_articles").
		WithArgs(StatusPublished).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).AddRow("a1", "default"))
	expectGetArticle(mock, "a1", "default", StatusPublished)
	mock.ExpectExec(`UPDATE knowledge_articles SET status=\$1, updated_at=now\(\)`).
		WithArgs(StatusArchived, "a1", "default").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO knowledge_article_reviews").
		WithArgs(sqlmock.AnyArg(), "a1", "default", StatusPublished, StatusArchived,
			"知识有效期已过, 系统自动下架", "system", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	svc := NewService(nil)
	got, hook := countingInvalidator()
	svc.SetCacheInvalidator(hook)

	svc.archiveExpired(&pgStore{db: db}, context.Background())
	if *got != 1 {
		t.Fatalf("到期下架应触发 1 次失效, 实际 %d", *got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestCacheInvalidatorNilSafe 未注入回调时(内存模式 / 单测装配)不得 panic。
func TestCacheInvalidatorNilSafe(t *testing.T) {
	svc := NewService(nil)
	svc.invalidateSearchCache()
	if err := svc.CreateArticle(context.Background(), &Article{Title: "T", Content: "C"}); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}
	if _, err := svc.TransitionArticle(context.Background(), "missing-id", "default", StatusArchived, "", "t"); err == nil {
		t.Log("article 不存在时流转报错属预期")
	}
}
