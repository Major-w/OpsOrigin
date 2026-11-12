package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/llm"
)

// M0 characterization: 钉死 4 个「完全绕过 Engine」的内容生成端点的当前 I/O 契约。
//
// 背景(2026-11-10 实测): presentation / clarify / workspace / evolution 四条链路直接调
// s.LLMClient().Chat, 不经过 Engine 的钩子链/轨迹/成本计量, 也不走模型注册表路由。
// D1 的 M1 要把它们收编进 Engine.Generate, 本文件先把"改造前"的成功/错误路径钉死,
// 确保重构零行为回归。生成类路径不套工具钩子, 仅做身份归属+轨迹+路由+成本。
//
// 当前零测试覆盖: 这 4 个 handler 此前没有任何 *_test.go 引用(grep 确认)。

// mockLLMClient 起一个确定性假 LLM 后端, 让模型固定返回 content(可被解析为 JSON)。
func mockLLMClient(t *testing.T, content string) *llm.Client {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, frameAnswer(content))
	}))
	t.Cleanup(upstream.Close)
	return llm.NewClient(llm.Config{
		APIKey: "test", Model: "test", BaseURL: upstream.URL, Timeout: 5e9,
	})
}

// newContentTestService 装配仅带 mock LLM 的 Service(不依赖 PG/Redis/Engine 钩子)。
func newContentTestService(t *testing.T, content string) *Service {
	t.Helper()
	return NewService(&config.Config{}, mockLLMClient(t, content), nil, nil)
}

// TestPresentationContract: POST /api/v1/ai/presentation 成功路径。
// 契约: 模型返回 outline JSON → 200 + {title, slides[>=1]}。
func TestPresentationContract(t *testing.T) {
	svc := newContentTestService(t, `{"title":"演示标题","slides":[{"title":"封面","bullets":["要点1","要点2"]},{"title":"章节","bullets":["要点3"]}]}`)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	h := wrapCtx(mux)

	body := `{"title":"演示标题","content":"这是一段用于生成演示文稿的测试内容，需要足够具体明确。"}`
	code, raw, _ := frameCall(t, h, http.MethodPost, "/api/v1/ai/presentation", body)
	if code != http.StatusOK {
		t.Fatalf("presentation status = %d, body = %q", code, raw)
	}
	var out struct {
		Title  string `json:"title"`
		Slides []any  `json:"slides"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("presentation 响应不是合法 JSON: %v, body=%q", err, raw)
	}
	if out.Title == "" || len(out.Slides) == 0 {
		t.Fatalf("presentation 契约破坏: title=%q slides=%d", out.Title, len(out.Slides))
	}
}

// TestPresentationEmptyContent: 空 content 必须 400(与改造前一致)。
func TestPresentationEmptyContent(t *testing.T) {
	svc := newContentTestService(t, `{"title":"x","slides":[]}`)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	h := wrapCtx(mux)
	code, _, _ := frameCall(t, h, http.MethodPost, "/api/v1/ai/presentation", `{"title":"t","content":"  "}`)
	if code != http.StatusBadRequest {
		t.Fatalf("presentation 空 content 期望 400, got %d", code)
	}
}

// TestClarifyContract: generateClarify 成功路径。
// 契约: 模型返回 {question, suggestions} → 非 nil 且字段非空。
func TestClarifyContract(t *testing.T) {
	svc := newContentTestService(t, `{"question":"您指的是哪个环境？","suggestions":["生产环境","测试环境"]}`)
	res, err := svc.generateClarify(context.Background(), "帮我查一下报错")
	if err != nil {
		t.Fatalf("clarify err = %v", err)
	}
	if res == nil {
		t.Fatal("clarify 期望非 nil 结果")
	}
	if strings.TrimSpace(res.Question) == "" || len(res.Suggestions) == 0 {
		t.Fatalf("clarify 契约破坏: question=%q suggestions=%v", res.Question, res.Suggestions)
	}
}

// TestEvolutionSummarizeContract: summarizeArticles 成功路径。
// 契约: 模型返回 [{title,content,score}] → 至少 1 条非空 title/content。
func TestEvolutionSummarizeContract(t *testing.T) {
	lc := mockLLMClient(t, `[{"title":"知识标题","content":"知识正文","score":0.8}]`)
	arts := summarizeArticles(context.Background(), lc, "raw source text")
	if len(arts) == 0 {
		t.Fatal("evolution summarize 期望至少 1 条")
	}
	for _, a := range arts {
		if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Content) == "" {
			t.Fatalf("evolution 契约破坏: title=%q content=%q", a.Title, a.Content)
		}
	}
}

// wrapCtx 注入与 sse_frame_contract_test.go 同款的租户/用户/角色上下文。
func wrapCtx(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, "tenant_id", "t-content")
		ctx = context.WithValue(ctx, "user_id", "u-content")
		ctx = context.WithValue(ctx, "role", "admin")
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
