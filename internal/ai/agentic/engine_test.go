package agentic

import (
	"strings"
	"testing"
)

func TestParseCoT(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantThink string
		wantAns   string
	}{
		{
			name:      "full tags",
			input:     "<think>先推理一下</think><answer>最终答案</answer>",
			wantThink: "先推理一下",
			wantAns:   "最终答案",
		},
		{
			name:      "no answer tag falls back to tail",
			input:     "<think>只在思考</think>剩下的就是答案",
			wantThink: "只在思考",
			wantAns:   "剩下的就是答案",
		},
		{
			name:      "no tags returns whole as answer",
			input:     "没有任何标签的内容",
			wantThink: "",
			wantAns:   "没有任何标签的内容",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			think, ans := parseCoT(c.input)
			if strings.TrimSpace(think) != c.wantThink {
				t.Errorf("thinking: got %q want %q", think, c.wantThink)
			}
			if strings.TrimSpace(ans) != c.wantAns {
				t.Errorf("answer: got %q want %q", ans, c.wantAns)
			}
		})
	}
}

func TestTrunc(t *testing.T) {
	if got := trunc("hello世界", 5); got != "hello" {
		t.Errorf("trunc short: got %q", got)
	}
	if got := trunc("hi", 10); got != "hi" {
		t.Errorf("trunc no-op: got %q", got)
	}
}

func TestParseNumbered(t *testing.T) {
	in := "1. [小明] 负责网络\n2、[小红] 负责存储\n3: [小刚] 负责计算\n随便一句废话\n4. 无成员名的子任务"
	got := parseNumbered(in)
	// 仅编号行被解析(保留 [成员名] 前缀,函数只剥离编号标记); 非编号行被忽略。
	if len(got) != 4 {
		t.Fatalf("expected 4 items, got %d: %#v", len(got), got)
	}
	want := []string{"[小明] 负责网络", "[小红] 负责存储", "[小刚] 负责计算", "无成员名的子任务"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestWithPersonaPrefix(t *testing.T) {
	got := withPersonaPrefix("严谨的架构师")
	if !strings.Contains(got, "严谨的架构师") {
		t.Errorf("persona not included: %q", got)
	}
}
