package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opsorigin/core/internal/ai/agentic"
)

func newFeedbackTestService() *Service {
	return &Service{feedbackStore: agentic.NewMemFeedbackStore()}
}

func withTenant(r *http.Request, tenant string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), "tenant_id", tenant))
}

func TestHandleSubmitFeedbackAndStats(t *testing.T) {
	s := newFeedbackTestService()

	body, _ := json.Marshal(map[string]any{"message_id": "m1", "rating": "up", "comment": "good"})
	req := withTenant(httptest.NewRequest(http.MethodPost, "/api/v1/ai/feedback", bytes.NewReader(body)), "t1")
	w := httptest.NewRecorder()
	s.handleSubmitFeedback(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["ok"] != true {
		t.Fatalf("ok not true: %v", resp)
	}

	// 同一消息改投 down -> upsert(不应新增)
	body2, _ := json.Marshal(map[string]any{"message_id": "m1", "rating": "down"})
	req2 := withTenant(httptest.NewRequest(http.MethodPost, "/api/v1/ai/feedback", bytes.NewReader(body2)), "t1")
	w2 := httptest.NewRecorder()
	s.handleSubmitFeedback(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}

	// stats: 应仅 1 条 down
	reqS := withTenant(httptest.NewRequest(http.MethodGet, "/api/v1/ai/feedback/stats", nil), "t1")
	wS := httptest.NewRecorder()
	s.handleFeedbackStats(wS, reqS)
	var st map[string]any
	json.Unmarshal(wS.Body.Bytes(), &st)
	if int(st["down"].(float64)) != 1 || int(st["up"].(float64)) != 0 {
		t.Fatalf("unexpected stats: %v", st)
	}

	// list 应返回 1 条
	reqL := withTenant(httptest.NewRequest(http.MethodGet, "/api/v1/ai/feedback", nil), "t1")
	wL := httptest.NewRecorder()
	s.handleListFeedback(wL, reqL)
	var lst map[string]any
	json.Unmarshal(wL.Body.Bytes(), &lst)
	feedbacks := lst["feedbacks"].([]any)
	if len(feedbacks) != 1 {
		t.Fatalf("want 1 feedback, got %d", len(feedbacks))
	}
}

func TestHandleSubmitFeedbackInvalidRating(t *testing.T) {
	s := newFeedbackTestService()
	body, _ := json.Marshal(map[string]any{"message_id": "m1", "rating": "maybe"})
	req := withTenant(httptest.NewRequest(http.MethodPost, "/api/v1/ai/feedback", bytes.NewReader(body)), "t1")
	w := httptest.NewRecorder()
	s.handleSubmitFeedback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandleSubmitFeedbackMissingTarget(t *testing.T) {
	s := newFeedbackTestService()
	body, _ := json.Marshal(map[string]any{"rating": "up"}) // 无 message_id 也无 conversation_id
	req := withTenant(httptest.NewRequest(http.MethodPost, "/api/v1/ai/feedback", bytes.NewReader(body)), "t1")
	w := httptest.NewRecorder()
	s.handleSubmitFeedback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandleEvalCases(t *testing.T) {
	s := newFeedbackTestService()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/eval/cases", nil)
	w := httptest.NewRecorder()
	s.handleEvalCases(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	cases := resp["cases"].([]any)
	if len(cases) == 0 {
		t.Fatal("eval cases should not be empty")
	}
}
