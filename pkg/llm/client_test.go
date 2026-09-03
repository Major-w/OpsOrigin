package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChatRetry 验证可重试错误(5xx)会按配置次数重试并最终成功。
func TestChatRetry(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{Model: "m", BaseURL: srv.URL, MaxRetries: 3, Timeout: 5 * time.Second})
	resp, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "x"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Fatalf("got content %q", resp.Choices[0].Message.Content)
	}
	if hits != 3 {
		t.Fatalf("expected exactly 3 requests (2 failures + 1 success), got %d", hits)
	}
}

// TestChatNoRetryOn4xx 验证 4xx(非 429)不重试, 立即返回。
func TestChatNoRetryOn4xx(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(Config{Model: "m", BaseURL: srv.URL, MaxRetries: 3, Timeout: 5 * time.Second})
	_, err := c.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if hits != 1 {
		t.Fatalf("expected no retry on 4xx, got %d hits", hits)
	}
}

// TestJoinEndpoint 锁定「版本感知」端点拼接行为: 标准厂商追加 /v1,
// 自带版本段的厂商(如智谱 /api/paas/v4)不再重复追加, 避免 /v4/v1/... 404。
func TestJoinEndpoint(t *testing.T) {
	cases := []struct{ base, path, want string }{
		// 标准 OpenAI 兼容: 无版本段 → 追加 /v1
		{"https://api.deepseek.com", "/v1/chat/completions", "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.deepseek.com/", "/v1/chat/completions", "https://api.deepseek.com/v1/chat/completions"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "/v1/chat/completions", "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"},
		// 自带版本段: 智谱 v4 → 只拼 /chat/completions
		{"https://open.bigmodel.cn/api/paas/v4", "/v1/chat/completions", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"https://open.bigmodel.cn/api/paas/v4/", "/v1/chat/completions", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"https://open.bigmodel.cn/api/paas/v4", "/v1/embeddings", "https://open.bigmodel.cn/api/paas/v4/embeddings"},
	}
	for _, tc := range cases {
		if got := joinEndpoint(tc.base, tc.path); got != tc.want {
			t.Errorf("joinEndpoint(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}

// TestIsRetryable 验证重试判定矩阵。
func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"429", &apiError{Status: 429}, true},
		{"500", &apiError{Status: 500}, true},
		{"502", &apiError{Status: 502}, true},
		{"400", &apiError{Status: 400}, false},
		{"401", &apiError{Status: 401}, false},
		{"ctx canceled", context.Canceled, false},
		{"ctx deadline", context.DeadlineExceeded, false},
		{"conn refused", errors.New("dial tcp: connection refused"), true},
		{"net dns timeout", &net.DNSError{IsTimeout: true, Err: "timeout"}, true},
		{"plain error", errors.New("something odd"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryable(tc.err); got != tc.want {
				t.Fatalf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestBackoffDur 验证退避时长非负、随次数增长且封顶。
func TestBackoffDur(t *testing.T) {
	var prev time.Duration
	for attempt := 1; attempt <= 6; attempt++ {
		d := backoffDur(attempt, nil)
		if d < 0 {
			t.Fatalf("negative backoff for attempt %d: %v", attempt, d)
		}
		if attempt > 1 && d < prev {
			t.Fatalf("backoff decreased: attempt %d = %v < prev %v", attempt, d, prev)
		}
		prev = d
	}
	if backoffDur(1, nil) < 200*time.Millisecond {
		t.Fatalf("attempt1 backoff too small: %v", backoffDur(1, nil))
	}
	if backoffDur(10, nil) > 8*time.Second {
		t.Fatalf("backoff not capped: %v", backoffDur(10, nil))
	}
}

// TestReadSSEParsing 验证逐行 SSE 解析: 普通内容片段 + 跨 index 聚合的 tool_calls + [DONE] 处理。
func TestReadSSEParsing(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"你好"}}]}`,
		`data: {"choices":[{"delta":{"content":"世界"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"cmdb_query","arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`data: [DONE]`,
		``,
	}, "\n")

	ch := make(chan StreamChunk, 64)
	go func() {
		// 直接调用内部解析器: 用一个已关闭的 reader 来源。
		c := NewClient(Config{Model: "m", BaseURL: "http://x", Timeout: time.Second})
		c.readSSE(context.Background(), io.NopCloser(strings.NewReader(body)), ch)
	}()

	var got []StreamChunk
	for sc := range ch {
		got = append(got, sc)
	}

	// 前两个 content chunk
	if len(got) < 2 || got[0].Content != "你好" || got[1].Content != "世界" {
		t.Fatalf("unexpected content chunks: %+v", got)
	}
	// 末片携带聚合后的 tool_calls
	last := got[len(got)-1]
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].Function.Name != "cmdb_query" {
		t.Fatalf("expected aggregated tool call, got %+v", last)
	}
	if last.ToolCalls[0].Function.Arguments != `{"q":"x"}` {
		t.Fatalf("tool args not concatenated: %q", last.ToolCalls[0].Function.Arguments)
	}
}

// TestReadSSENonSSEFallback 验证非 SSE(纯 JSON)响应回退为单个 chunk。
func TestReadSSENonSSEFallback(t *testing.T) {
	body := `{"choices":[{"message":{"content":"plain"}}]}`
	ch := make(chan StreamChunk, 8)
	c := NewClient(Config{Model: "m", BaseURL: "http://x", Timeout: time.Second})
	go c.readSSE(context.Background(), io.NopCloser(strings.NewReader(body)), ch)

	got := <-ch
	if got.Content != "plain" || got.FinishReason != "stop" {
		t.Fatalf("unexpected fallback chunk: %+v", got)
	}
}
