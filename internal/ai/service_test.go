package ai

import "testing"

type errString string

func (e errString) Error() string { return string(e) }

// TestChatFriendlyErrorRateLimit 覆盖 429/访问量过大/rate limit 中文与英文匹配, 以及原有分支不被误伤。
func TestChatFriendlyErrorRateLimit(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"llm: API error [429]: 您的使用量已超出频率限制", "模型请求频率过高或当前繁忙，请稍候重试"},
		{"429 该模型当前访问量过大", "模型请求频率过高或当前繁忙，请稍候重试"},
		{"rate limit exceeded for tool execution", "模型请求频率过高或当前繁忙，请稍候重试"},
		{"llm: API error [401]: invalid key", "LLM API Key 无效或未配置，请在「设置 → 模型」中填写正确的 API Key"},
		{"connection refused", "LLM 服务连接失败，请检查 Base URL 和网络"},
		{"some unknown error", "some unknown error"},
	}
	for _, c := range cases {
		if got := chatFriendlyError(c.raw); got != c.want {
			t.Errorf("chatFriendlyError(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// TestChatErrorStatus 验证限流错误映射为 429, 其余底层故障映射为 502。
func TestChatErrorStatus(t *testing.T) {
	if s := chatErrorStatus(errString("llm: API error [429]: x")); s != 429 {
		t.Errorf("chatErrorStatus(429) = %d, want 429", s)
	}
	if s := chatErrorStatus(errString("429 访问量过大")); s != 429 {
		t.Errorf("chatErrorStatus(cn) = %d, want 429", s)
	}
	if s := chatErrorStatus(errString("boom")); s != 502 {
		t.Errorf("chatErrorStatus(other) = %d, want 502", s)
	}
}

// TestStripEmptyCitationSection 验证无引用标注时剥离尾部空"## 引用来源"段, 有引用时不改动。
func TestStripEmptyCitationSection(t *testing.T) {
	withRef := "结论[^a1]。\n\n## 引用来源\n- [a1] 标题"
	if got := stripEmptyCitationSection(withRef); got != withRef {
		t.Errorf("with refs changed: %q", got)
	}
	if got := stripEmptyCitationSection("这是回答。\n\n## 引用来源\n无"); got != "这是回答。" {
		t.Errorf("empty tail = %q, want %q", got, "这是回答。")
	}
	plain := "普通回答没有引用。"
	if got := stripEmptyCitationSection(plain); got != plain {
		t.Errorf("plain changed: %q", got)
	}
}
