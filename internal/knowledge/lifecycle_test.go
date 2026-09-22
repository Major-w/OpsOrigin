package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// —— 状态机(P3-3) ——

// TestArticleTransitionMatrix 验证状态白名单与合法迁移表(含现网保留的反向操作)。
func TestArticleTransitionMatrix(t *testing.T) {
	for _, s := range ArticleStatuses {
		if !validArticleStatus(s) {
			t.Fatalf("whitelisted status %q must be valid", s)
		}
	}
	if validArticleStatus("deleted") || validArticleStatus("") {
		t.Fatal("unknown status must be rejected")
	}
	legal := [][2]string{
		{StatusDraft, StatusInReview}, {StatusDraft, StatusPublished}, {StatusDraft, StatusArchived},
		{StatusInReview, StatusPublished}, {StatusInReview, StatusDraft}, {StatusInReview, StatusArchived},
		{StatusPublished, StatusDraft}, {StatusPublished, StatusInReview}, {StatusPublished, StatusArchived},
		{StatusArchived, StatusDraft}, {StatusArchived, StatusPublished},
	}
	for _, p := range legal {
		if !canTransition(p[0], p[1]) {
			t.Fatalf("transition %s -> %s should be legal", p[0], p[1])
		}
	}
	illegal := [][2]string{{"deleted", StatusDraft}, {StatusDraft, "bogus"}}
	for _, p := range illegal {
		if canTransition(p[0], p[1]) {
			t.Fatalf("transition %s -> %s should be illegal", p[0], p[1])
		}
	}
}

// TestMemoryTransitionWritesReviewTrail 验证流转结果与评审轨迹(驳回原因可追溯)。
func TestMemoryTransitionWritesReviewTrail(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()
	a := &Article{Title: "磁盘满处理", Content: "du -sh", Status: StatusDraft, TenantID: "default"}
	if err := st.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 草稿 → 待评审 → 驳回(带原因) → 待评审 → 发布
	steps := []struct{ to, note, actor string }{
		{StatusInReview, "请评审", "alice"},
		{StatusDraft, "步骤缺少验证环节", "bob"},
		{StatusInReview, "已补充验证", "alice"},
		{StatusPublished, "通过", "bob"},
	}
	for _, s := range steps {
		got, err := st.Transition(ctx, a.ID, "default", s.to, s.note, s.actor)
		if err != nil {
			t.Fatalf("transition to %s: %v", s.to, err)
		}
		if got.Status != s.to {
			t.Fatalf("status = %q, want %q", got.Status, s.to)
		}
	}

	reviews, err := st.ListReviews(ctx, a.ID, "default", 10)
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(reviews) != len(steps) {
		t.Fatalf("expected %d review rows, got %d", len(steps), len(reviews))
	}
	// 最新在前: 首条应是 published, 且驳回原因留在了第 3 条(倒序第 3)。
	if reviews[0].To != StatusPublished || reviews[0].Actor != "bob" {
		t.Fatalf("unexpected newest review: %+v", reviews[0])
	}
	if reviews[2].To != StatusDraft || reviews[2].Note != "步骤缺少验证环节" {
		t.Fatalf("rejection reason must be preserved: %+v", reviews[2])
	}
}

// TestMemoryTransitionRejectsIllegal 验证未知状态与非法迁移被拒。
func TestMemoryTransitionRejectsIllegal(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()
	a := &Article{Title: "T", Status: StatusPublished, TenantID: "default"}
	_ = st.Create(ctx, a)

	if _, err := st.Transition(ctx, a.ID, "default", "bogus", "", "x"); err == nil ||
		!strings.Contains(err.Error(), "unknown article status") {
		t.Fatalf("unknown status must be rejected, got %v", err)
	}
	if _, err := st.Transition(ctx, a.ID, "other-tenant", StatusArchived, "", "x"); err != ErrNotFound {
		t.Fatalf("cross-tenant transition must be not-found, got %v", err)
	}
	// 幂等: 目标状态与当前一致时直接返回, 不产生轨迹。
	if _, err := st.Transition(ctx, a.ID, "default", StatusPublished, "", "x"); err != nil {
		t.Fatalf("same-status transition must be idempotent: %v", err)
	}
	reviews, _ := st.ListReviews(ctx, a.ID, "default", 10)
	if len(reviews) != 0 {
		t.Fatalf("idempotent transition must not write a trail, got %d", len(reviews))
	}
}

// TestPGTransitionUpdatesAndTrails 验证 pg 实现: 读文章 → 更新状态 → 写评审轨迹。
func TestPGTransitionUpdatesAndTrails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	expectGetArticle(mock, "a1", "default", StatusInReview)
	mock.ExpectExec(`UPDATE knowledge_articles SET status=\$1, updated_at=now\(\) WHERE id=\$2 AND tenant_id=\$3`).
		WithArgs(StatusPublished, "a1", "default").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO knowledge_article_reviews").
		WithArgs(sqlmock.AnyArg(), "a1", "default", StatusInReview, StatusPublished, "通过", "bob", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	s := &pgStore{db: db}
	a, err := s.Transition(context.Background(), "a1", "default", StatusPublished, "通过", "bob")
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if a == nil || a.Status != StatusPublished {
		t.Fatalf("unexpected article after transition: %+v", a)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestPGTransitionIllegalDoesNotWrite 验证非法迁移在读文章后即被拒, 不产生任何写入。
func TestPGTransitionIllegalDoesNotWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	expectGetArticle(mock, "a1", "default", StatusDraft)
	s := &pgStore{db: db}
	if _, err := s.Transition(context.Background(), "a1", "default", "bogus", "", "bob"); err == nil {
		t.Fatal("unknown status must be rejected before writing")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestPGArchiveExpired 验证到期自动下架: 扫描过期已发布 → 逐篇归档并留轨迹。
func TestPGArchiveExpired(t *testing.T) {
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

	s := &pgStore{db: db}
	n, err := s.ArchiveExpired(context.Background())
	if err != nil {
		t.Fatalf("archive expired: %v", err)
	}
	if n != 1 {
		t.Fatalf("archived = %d, want 1", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// —— 创建去重(P3-1) ——

// TestMemoryFindDuplicates 验证同名(忽略大小写/空白)与同正文被识别为重复候选。
func TestMemoryFindDuplicates(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()
	_ = st.Create(ctx, &Article{Title: "磁盘满处理", Content: "cleanup", Status: StatusPublished, TenantID: "default"})
	_ = st.Create(ctx, &Article{Title: "CPU 高排查", Content: "top", Status: StatusDraft, TenantID: "default"})

	dups, err := st.FindDuplicates(ctx, "default", "  磁盘满处理 ", "", "", 5)
	if err != nil {
		t.Fatalf("find duplicates: %v", err)
	}
	if len(dups) != 1 || dups[0].Reason != "title_same" || dups[0].Similarity != 1 {
		t.Fatalf("expected one exact-title duplicate, got %+v", dups)
	}
	// 同正文(标题不同)也要能识别。
	dups, err = st.FindDuplicates(ctx, "default", "别的标题", "cleanup", "", 5)
	if err != nil {
		t.Fatalf("find duplicates by content: %v", err)
	}
	if len(dups) != 1 || dups[0].Reason != "content_same" {
		t.Fatalf("expected content duplicate, got %+v", dups)
	}
	// excludeID 用于编辑态排除自身。
	dups, _ = st.FindDuplicates(ctx, "default", "磁盘满处理", "", dups[0].ID, 5)
	if len(dups) != 0 {
		t.Fatalf("excludeID must exclude the article itself, got %+v", dups)
	}
}

// TestPGFindDuplicatesTrgm 验证 pg_trgm 可用时走相似度查询, 并按判据标注原因。
func TestPGFindDuplicatesTrgm(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(`similarity\(a.title, \$2\) AS sim`).
		WithArgs("default", "磁盘满了怎么办", "du -sh", "", 5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "status", "category", "updated_at", "sim", "same_title", "same_content"}).
			AddRow("a1", "磁盘满处理", StatusPublished, "故障处理", now, 0.42, false, false).
			AddRow("a2", "磁盘满了怎么办", StatusDraft, "", now, 1.0, true, false))

	s := &pgStore{db: db, trgmEnabled: true}
	dups, err := s.FindDuplicates(context.Background(), "default", "磁盘满了怎么办", "du -sh", "", 5)
	if err != nil {
		t.Fatalf("find duplicates: %v", err)
	}
	if len(dups) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(dups))
	}
	if dups[0].Reason != "title_similar" || dups[0].Similarity != 0.42 {
		t.Fatalf("unexpected fuzzy candidate: %+v", dups[0])
	}
	if dups[1].Reason != "title_same" || dups[1].Similarity != 1 {
		t.Fatalf("unexpected exact candidate: %+v", dups[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// TestPGFindDuplicatesFallback 验证 pg_trgm 不可用时退化为「同名/同正文」精确去重(能力不整体失效)。
func TestPGFindDuplicatesFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock init: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(`lower\(btrim\(a.title\)\) = lower\(btrim\(\$2\)\)`).
		WithArgs("default", "磁盘满处理", "du -sh", "", 5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "status", "category", "updated_at", "sim", "same_title", "same_content"}).
			AddRow("a1", "磁盘满处理", StatusPublished, "故障处理", now, 0.0, true, false))

	s := &pgStore{db: db, trgmEnabled: false}
	dups, err := s.FindDuplicates(context.Background(), "default", "磁盘满处理", "du -sh", "", 5)
	if err != nil {
		t.Fatalf("find duplicates: %v", err)
	}
	if len(dups) != 1 || dups[0].Reason != "title_same" {
		t.Fatalf("unexpected fallback candidates: %+v", dups)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

// —— HTTP 入口 ——

// TestHandleCreateRejectsDuplicate 验证创建去重: 命中重复返回 409 + 候选清单,
// 带 force_duplicate 时才真正创建。
func TestHandleCreateRejectsDuplicate(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()
	_ = svc.CreateArticle(ctx, &Article{Title: "磁盘满处理", Content: "du -sh", Category: "故障处理"})

	body := `{"title":"磁盘满处理","content":"du -sh","category":"故障处理"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge", strings.NewReader(body))
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec := httptest.NewRecorder()
	svc.handleCreate(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Duplicates []*DuplicateCandidate `json:"duplicates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode 409 body: %v", err)
	}
	if len(payload.Duplicates) != 1 {
		t.Fatalf("409 body must list the duplicate candidate, got %+v", payload.Duplicates)
	}

	// 显式确认后放行。
	forceBody := `{"title":"磁盘满处理","content":"du -sh","force_duplicate":true}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/knowledge", strings.NewReader(forceBody))
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec = httptest.NewRecorder()
	svc.handleCreate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("forced create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCreateRejectsUnknownStatus 验证创建时的状态白名单。
func TestHandleCreateRejectsUnknownStatus(t *testing.T) {
	svc := NewService(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge",
		strings.NewReader(`{"title":"T","content":"C","status":"deleted"}`))
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec := httptest.NewRecorder()
	svc.handleCreate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleCheckDuplicates 验证编辑器预检端点。
func TestHandleCheckDuplicates(t *testing.T) {
	svc := NewService(nil)
	_ = svc.CreateArticle(context.Background(), &Article{Title: "磁盘满处理", Content: "du -sh"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/duplicates?title=磁盘满处理", nil)
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec := httptest.NewRecorder()
	svc.handleCheckDuplicates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload struct {
		Duplicates []*DuplicateCandidate `json:"duplicates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Duplicates) != 1 {
		t.Fatalf("expected 1 candidate, got %+v", payload.Duplicates)
	}
}

// TestHandleTransitionHTTP 验证转换端点: 非法状态 400 / 非法迁移 409 / 正常 200。
func TestHandleTransitionHTTP(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()
	a := &Article{Title: "T", Content: "C", Status: StatusDraft}
	if err := svc.CreateArticle(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/"+a.ID+"/transition", strings.NewReader(body))
		req.SetPathValue("id", a.ID)
		req = req.WithContext(withTenant(req.Context(), "default"))
		rec := httptest.NewRecorder()
		svc.handleTransition(rec, req)
		return rec
	}

	if rec := post(`{"to":"bogus"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown status = %d, want 400", rec.Code)
	}
	if rec := post(`{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body = %d, want 400", rec.Code)
	}
	if rec := post(`{"to":"in_review","note":"请评审"}`); rec.Code != http.StatusOK {
		t.Fatalf("draft->in_review = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var ok struct {
		Article     *Article `json:"article"`
		Retrievable bool     `json:"retrievable"`
	}
	if err := json.Unmarshal(post(`{"to":"archived"}`).Body.Bytes(), &ok); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok.Retrievable {
		t.Fatal("archived article must not be retrievable by AI")
	}

	// 轨迹可查。
	req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/"+a.ID+"/reviews", nil)
	req.SetPathValue("id", a.ID)
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec := httptest.NewRecorder()
	svc.handleListReviews(rec, req)
	var reviews struct {
		Items []*ArticleReview `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reviews); err != nil {
		t.Fatalf("decode reviews: %v", err)
	}
	if len(reviews.Items) != 2 {
		t.Fatalf("expected 2 review rows, got %d", len(reviews.Items))
	}
}

// TestUpdateRejectsIllegalStatusJump 验证编辑态改状态也受状态机约束。
func TestUpdateRejectsIllegalStatusJump(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()
	a := &Article{Title: "T", Content: "C", Status: StatusArchived}
	if err := svc.CreateArticle(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/knowledge/"+a.ID+"/",
		strings.NewReader(`{"title":"T","content":"C","status":"removed"}`))
	req.SetPathValue("id", a.ID)
	req = req.WithContext(withTenant(req.Context(), "default"))
	rec := httptest.NewRecorder()
	svc.handleUpdate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown status on update = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// —— 辅助 ——

// withTenant 注入租户标识(与中间件一致: 裸 key "tenant_id", 见 pkg/auth.TenantFromContext)。
func withTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, "tenant_id", tenant)
}

// expectGetArticle 让 sqlmock 期待一次 pgStore.Get(16 列)。
func expectGetArticle(mock sqlmock.Sqlmock, id, tenant, status string) {
	now := time.Now()
	mock.ExpectQuery(`SELECT id,tenant_id,title,content,category,tags,views,status,version,expires_at,view_permission,edit_permission,author_id,owner_group_ids,created_at,updated_at FROM knowledge_articles WHERE id=\$1 AND tenant_id=\$2`).
		WithArgs(id, tenant).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "content", "category", "tags",
			"views", "status", "version", "expires_at", "view_permission", "edit_permission", "author_id", "owner_group_ids", "created_at", "updated_at"}).
			AddRow(id, tenant, "磁盘满处理", "du -sh", "故障处理", []byte("[]"), 0, status, 1, nil, "all", "owner", "", []byte("{}"), now, now))
}
