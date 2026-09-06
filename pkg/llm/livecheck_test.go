//go:build livecheck

// 临时真实连通性检查: 用项目 .env 中的配置对真实 LLM 端点发起一次调用。
// 仅用于人工验证 key/端点是否可用, 不属于常规测试, 验证完即删除。
// 运行: go test -tags livecheck -run TestLiveEndpoint ./pkg/llm/ -v
package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func loadEnvFile(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("../../.env")
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}

func TestLiveEndpoint(t *testing.T) {
	env := loadEnvFile(t)
	key := env["OPSORIGIN_LLM_API_KEY"]
	base := env["OPSORIGIN_LLM_BASE_URL"]
	model := env["OPSORIGIN_LLM_MODEL"]
	embModel := env["OPSORIGIN_LLM_EMBEDDING_MODEL"]
	if key == "" || base == "" {
		t.Fatalf("missing LLM config in .env")
	}
	t.Logf("BASE_URL = %s", base)
	t.Logf("MODEL    = %s (embedding: %s)", model, embModel)
	t.Logf("API_KEY  = %s...%s (len=%d)", key[:4], key[len(key)-4:], len(key))

	// 1) 验证端点拼接结果(纯函数, 不联网)
	t.Logf("joinEndpoint(chat) = %s", joinEndpoint(base, "/chat/completions"))
	t.Logf("joinEndpoint(emb)  = %s", joinEndpoint(base, "/embeddings"))

	cfg := Config{APIKey: key, Model: model, BaseURL: base, Timeout: 60 * time.Second, EmbeddingModel: embModel}
	c := NewClient(cfg)

	// 2) 真实 Chat 调用
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := c.Chat(ctx, ChatRequest{
		Model:    model,
		Messages: []Message{{Role: "user", Content: "只回答一个数字: 1+1=?"}},
	})
	if err != nil {
		t.Fatalf("REAL CHAT CALL FAILED: %v", err)
	}
	t.Logf("CHAT OK 耗时=%.1fs choices=%d", time.Since(start).Seconds(), len(resp.Choices))
	if len(resp.Choices) == 0 {
		t.Fatalf("chat returned 0 choices (empty response)")
	}
	m := resp.Choices[0].Message
	t.Logf("  finish_reason = %q", resp.Choices[0].FinishReason)
	t.Logf("  content       = %q", truncateForLog(m.Content, 120))
	t.Logf("  reasoning     = %q", truncateForLog(m.ReasoningContent, 80))
	if resp.Usage != nil {
		t.Logf("  tokens        = prompt=%d completion=%d total=%d",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}
	if strings.TrimSpace(m.Content) == "" {
		t.Errorf("WARNING: content 为空(推理模型可能只吐 reasoning_content, 或 max_tokens 不足)")
	}

	// 3) 真实 Embedding 调用
	start = time.Now()
	vec, err := c.Embed(ctx, "连通性检查")
	if err != nil {
		t.Errorf("REAL EMBED CALL FAILED: %v", err)
	} else {
		t.Logf("EMBED OK 耗时=%.1fs 维度=%d 前3值=%v", time.Since(start).Seconds(), len(vec), vec[:min(3, len(vec))])
	}
}

func truncateForLog(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
