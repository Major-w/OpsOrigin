package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
)

// TestStripThinkTags 验证思维链标记清洗: 部分兼容端点(如 GLM)会把 <think> 混入正文,
// 或只留下孤立的 </think>, 需剥离后再呈现给用户。
func TestStripThinkTags(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"无标记", "你好，我是助手", "你好，我是助手"},
		{"孤立结束标签", "答案在这里\n</think>", "答案在这里\n"},
		{"完整思维块", "前面<think>推理过程</think>后面", "前面后面"},
		{"未闭合思维块", "答案<think>未结束的推理", "答案"},
		{"多段", "A<think>x</think>B<think>y</think>C", "ABC"},
	}
	for _, c := range cases {
		if got := stripThinkTags(c.in); got != c.want {
			t.Errorf("%s: stripThinkTags(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestThinkFilterStreaming 验证流式增量过滤: 标签可能被切在两个 chunk 之间,
// 且未闭合的思维链残留必须在 flush 时丢弃, 不能泄漏给用户。
// 新版同时回吐 (clean, think): think 段送往 event: thinking(ThinkingBlock 折叠展示),
// clean 段送往 event: token(答案正文)。
func TestThinkFilterStreaming(t *testing.T) {
	// 1) 标签被切分到多个 chunk
	f := newThinkFilter()
	var clean, think string
	open := thinkTagOpen
	close := thinkTagClose
	for _, chunk := range []string{"答案一", open[:6], open[6:] + "推理中", close[:6], close[6:] + "答案二"} {
		c, th := f.feed(chunk)
		clean += c
		think += th
	}
	c, th := f.flush()
	clean += c
	think += th
	if clean != "答案一答案二" {
		t.Fatalf("split tags clean: got %q, want %q", clean, "答案一答案二")
	}
	if think != "推理中" {
		t.Fatalf("split tags think: got %q, want %q", think, "推理中")
	}

	// 2) 孤立结束标签(起始在 reasoning_content 中)
	f = newThinkFilter()
	clean, think = "", ""
	c, _ = f.feed("正文内容")
	clean += c
	c, _ = f.feed("</think>")
	clean += c
	c, _ = f.feed("后续")
	clean += c
	c, th = f.flush()
	clean += c
	think += th
	if clean != "正文内容后续" {
		t.Fatalf("orphan close tag: got %q, want %q", clean, "正文内容后续")
	}

	// 3) 思维链未闭合: 残留必须在 flush 时丢弃
	f = newThinkFilter()
	clean, think = "", ""
	c, _ = f.feed("可见内容")
	clean += c
	c, _ = f.feed("<think>未闭合推理...")
	clean += c
	c, th = f.flush()
	clean += c
	think += th
	if clean != "可见内容" {
		t.Fatalf("unclosed think clean: got %q, want %q", clean, "可见内容")
	}
	if think != "" {
		t.Fatalf("unclosed think must drop, got %q", think)
	}

	// 4) 普通文本不应被吞尾(尾部不足一个标签长度时保留缓冲, flush 后完整输出)
	f = newThinkFilter()
	clean, think = "", ""
	c, _ = f.feed("abcdefghij")
	clean += c
	c, th = f.flush()
	clean += c
	think += th
	if clean != "abcdefghij" {
		t.Fatalf("plain text: got %q, want %q", clean, "abcdefghij")
	}

	// 5) 中文 chunk 边界不能切出非法 UTF-8(P0: thinkTagMaxLen 需与字符边界对齐)
	f = newThinkFilter()
	clean, think = "", ""
	msg := "你好世界"
	// 在"世"字中间切断(chunk 边界落在多字节字符内部), 验证不会输出半截字符导致乱码
	for _, chunk := range []string{msg[:7], msg[7:]} {
		c, _ = f.feed(chunk)
		clean += c
	}
	c, th = f.flush()
	clean += c
	think += th
	if clean != "你好世界" {
		t.Fatalf("utf8 boundary: got %q, want %q", clean, "你好世界")
	}
}

// TestCnCoTFilterStreaming 验证中文「分步推理分析…再给出最终答案」双段结构被折叠为 thinking。
func TestCnCoTFilterStreaming(t *testing.T) {
	// 1) 完整双段, 分隔符在 chunk 边界之后才出现 → 前段折叠为 thinking, 后段为 clean(增量 emit 不重复)
	f := newCnCoTFilter()
	var clean, think string
	chunks := []string{
		"先进行分步推理分析\n\n",
		"用户问的是通用运维知识, 我无法查询实时系统。\n",
		"我的知识来自预训练语料。\n",
		"再给出最终答案\n\n",
		"Linux 常用命令包括 ls、cd、ps 等。",
	}
	for _, chunk := range chunks {
		c, th := f.feed(chunk)
		clean += c
		think += th
	}
	c, th := f.flush()
	clean += c
	think += th
	if think == "" {
		t.Fatalf("cnCoT: expect non-empty thinking, got empty")
	}
	if !strings.Contains(think, "用户问的是通用运维知识") || !strings.Contains(think, "预训练语料") {
		t.Fatalf("cnCoT thinking should contain the reasoning part, got %q", think)
	}
	if clean != "Linux 常用命令包括 ls、cd、ps 等。" {
		t.Fatalf("cnCoT clean: got %q, want %q", clean, "Linux 常用命令包括 ls、cd、ps 等。")
	}

	// 2) 普通回答(不以推理前缀开头)应原样透传, 不进入提取模式
	f = newCnCoTFilter()
	clean, think = "", ""
	c, th = f.feed("Linux 常用命令有 ls、cd、ps。")
	clean += c
	think += th
	c, th = f.flush()
	clean += c
	think += th
	if clean != "Linux 常用命令有 ls、cd、ps。" {
		t.Fatalf("cnCoT plain pass-through: got %q, want %q", clean, "Linux 常用命令有 ls、cd、ps。")
	}
	if think != "" {
		t.Fatalf("cnCoT plain must not produce thinking, got %q", think)
	}

	// 3) 分隔符跨 chunk 切分(「最终答案」被切成两半)仍能识别
	f = newCnCoTFilter()
	clean, think = "", ""
	c, th = f.feed("分步推理分析\n前因后果。")
	clean += c
	think += th
	c, th = f.feed("最") // 「最终答案」的前一字残片
	clean += c
	think += th
	c, th = f.feed("终答案\n\n这是答案正文。")
	clean += c
	think += th
	c, th = f.flush()
	clean += c
	think += th
	if !strings.Contains(think, "前因后果") {
		t.Fatalf("cnCoT cross-chunk think: got %q", think)
	}
	if clean != "这是答案正文。" {
		t.Fatalf("cnCoT cross-chunk clean: got %q, want %q", clean, "这是答案正文。")
	}

	// 4) 复现截图场景: 原生推理通道把完整中文双段写进 reasoning_content,
	//    其中「再给出最终答案」后的 bullets 必须拆出来作为 clean 答案, 不泡在 thinking 里。
	f = newCnCoTFilter()
	clean, think = "", ""
	chunks = []string{
		"先进行分步推理分析\n\n",
		"用户询问我是否具备高等数学知识。这是一个关于我知识库覆盖范围的问题。",
		"我的知识来源于大规模文本数据集的预训练, 包含数学文献、教科书和学术文章。",
		"再给出最终答案\n\n",
		"是的, 我具备处理高等数学知识的能力。包括但不限于:\n",
		"• 微积分: 极限、导数、积分、多元微积分等。\n",
		"• 线性代数: 矩阵运算、向量空间、特征值与特征向量等。",
	}
	for _, chunk := range chunks {
		c, th := f.feed(chunk)
		clean += c
		think += th
	}
	c, th = f.flush()
	clean += c
	think += th
	if !strings.Contains(think, "高等数学知识") || strings.Contains(think, "是的, 我具备") {
		t.Fatalf("cnCoT screenshot thinking: got %q", think)
	}
	if !strings.HasPrefix(clean, "是的, 我具备") {
		t.Fatalf("cnCoT screenshot clean: got %q", clean)
	}
}

// TestBuildToolDefsDefaultAssistantNoTools 验证默认助手(agentID=="")不暴露任何工具,
// 仅纯对话/推理(产品意图: 默认助手不关联工具, 避免未选数字员工时盲目发起工具调用)。
func TestBuildToolDefsDefaultAssistantNoTools(t *testing.T) {
	reg := mcp.NewRegistry()
	for _, name := range []string{
		"cmdb_query", "knowledge_search", "script_list", "script_detail", "script_executions",
		"ticket_create", "script_run", "script_generate",
	} {
		reg.Register(mcp.ToolDefinition{
			Name:        name,
			Description: "x",
			Parameters:  map[string]mcp.ParamSchema{},
			Handler:     func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
		})
	}
	e := NewEngine(reg, nil, nil, nil)

	defs := e.buildToolDefs("") // 默认助手
	if len(defs) != 0 {
		t.Fatalf("默认助手不应暴露任何工具, got %v", toolNames(defs))
	}
}

// TestToolFeedbackContentErrorHint 验证工具报错/空结果时回填内容携带反重试提示(retry:false + hint),
// 从反馈环源头打断 error→retry 与 空结果→换词重搜 两类死循环; 正常非空结果原样回填。
func TestToolFeedbackContentErrorHint(t *testing.T) {
	// 1) execErr=true: execToolChecked 已写入 hint/retry
	errResult := map[string]any{"error": "db connection refused", "retry": false, "hint": "工具执行失败, 请勿重复重试同一工具; 请直接告知用户该能力暂不可用或检查相关后端是否就绪。"}
	c1 := toolFeedbackContent(errResult, true)
	if !strings.Contains(c1, "请勿重复重试") {
		t.Fatalf("execErr result should carry anti-retry hint, got: %s", c1)
	}

	// 2) execErr=false 但结果内含 error 字段(handler 把错误包进结果): 应补注 hint
	inMapErr := map[string]any{"error": "validation failed"}
	c2 := toolFeedbackContent(inMapErr, false)
	if !strings.Contains(c2, "请勿重复重试") {
		t.Fatalf("result-with-error-field should be annotated with anti-retry hint, got: %s", c2)
	}

	// 3) 空结果 count:0: 应注入"空结果反重试"提示(retry:false + 工具返回为空), 而非错误提示。
	//    这是打断 空结果→换词重搜→仍为空 死循环的关键: 明确告诉模型"无数据≠失败, 不要重试"。
	empty := map[string]any{"count": 0, "items": []any{}}
	c3 := toolFeedbackContent(empty, false)
	if !strings.Contains(c3, "工具返回为空") {
		t.Fatalf("empty result (count:0) should carry empty-result anti-retry hint, got: %s", c3)
	}
	if strings.Contains(c3, "工具执行失败") {
		t.Fatalf("empty result must NOT be annotated with the error hint, got: %s", c3)
	}

	// 4) 正常非空结果: 不注入任何提示, 原样回填。
	normal := map[string]any{"count": 2, "items": []any{"a", "b"}}
	c4 := toolFeedbackContent(normal, false)
	if strings.Contains(c4, "retry") || strings.Contains(c4, "hint") {
		t.Fatalf("normal non-empty result must NOT be annotated, got: %s", c4)
	}
}
