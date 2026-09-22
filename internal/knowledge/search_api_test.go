package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubKnowledgeSearcher 是 KnowledgeSearcher 的测试桩。
type stubKnowledgeSearcher struct {
	called     bool
	query      string
	limit      int
	categories []string
	items      []map[string]any
	err        error
}

func (s *stubKnowledgeSearcher) SearchForUI(_ context.Context, query string, limit int, categories []string) ([]map[string]any, error) {
	s.called = true
	s.query = query
	s.limit = limit
	s.categories = categories
	return s.items, s.err
}

// TestHandleKnowledgeSearchNotImplemented 未接 AI 引擎时给出明确 501(前端据此回退本地过滤)。
func TestHandleKnowledgeSearchNotImplemented(t *testing.T) {
	svc := NewService(nil)
	rec := httptest.NewRecorder()
	svc.handleKnowledgeSearch(rec, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/search?q=disk", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// TestHandleKnowledgeSearchRequiresQuery 空 query 必须拒绝, 不允许全表式空检索。
func TestHandleKnowledgeSearchRequiresQuery(t *testing.T) {
	svc := NewService(nil)
	svc.SetKnowledgeSearcher(&stubKnowledgeSearcher{})
	rec := httptest.NewRecorder()
	svc.handleKnowledgeSearch(rec, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/search", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestHandleKnowledgeSearchRuns 参数透传(limit 边界/cat 单值)与结果回传。
func TestHandleKnowledgeSearchRuns(t *testing.T) {
	svc := NewService(nil)
	stub := &stubKnowledgeSearcher{items: []map[string]any{
		{"id": "a1", "title": "磁盘扩容", "snippet": "清理步骤", "score": 0.87, "chunk_id": "k1"},
	}}
	svc.SetKnowledgeSearcher(stub)

	rec := httptest.NewRecorder()
	svc.handleKnowledgeSearch(rec, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/search?q=%E7%A3%81%E7%9B%98&limit=999&cat=%E8%BF%90%E7%BB%B4", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var res KnowledgeSearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Source != "semantic" || res.Total != 1 || len(res.Items) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !stub.called || stub.query != "磁盘" {
		t.Fatalf("query not forwarded: %q", stub.query)
	}
	// limit=999 必须被收敛到上限, 不允许单请求打爆引擎
	if stub.limit != kbSearchMaxLimit {
		t.Fatalf("limit = %d, want capped %d", stub.limit, kbSearchMaxLimit)
	}
	if len(stub.categories) != 1 || stub.categories[0] != "运维" {
		t.Fatalf("cat must be forwarded as single category, got %+v", stub.categories)
	}
}

// TestHandleKnowledgeSearchError 检索引擎报错 → 500 且带原因。
func TestHandleKnowledgeSearchError(t *testing.T) {
	svc := NewService(nil)
	svc.SetKnowledgeSearcher(&stubKnowledgeSearcher{err: errors.New("embedder down")})
	rec := httptest.NewRecorder()
	svc.handleKnowledgeSearch(rec, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/search?q=x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !contains(rec.Body.String(), "embedder down") {
		t.Fatalf("error reason must be surfaced, got %s", rec.Body.String())
	}
}
