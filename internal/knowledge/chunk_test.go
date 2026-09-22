package knowledge

import (
	"strings"
	"testing"
)

// TestTokenEstimateCJKvsLatin 验证 token 估算: 中文按 1 token/字, 拉丁按 4 字符/token。
func TestTokenEstimateCJKvsLatin(t *testing.T) {
	cjk := tokenEstimate("磁盘空间不足时的清理步骤")
	if cjk < 10 || cjk > 14 {
		t.Fatalf("CJK token estimate = %d, want ~12", cjk)
	}
	latin := tokenEstimate(strings.Repeat("abcdefgh", 50)) // 400 字符
	if latin < 90 || latin > 110 {
		t.Fatalf("latin token estimate = %d, want ~100", latin)
	}
}

// TestChunkMarkdownTruncated 验证超过块数上限时返回 truncated=true(不再静默丢内容)。
func TestChunkMarkdownTruncated(t *testing.T) {
	var b strings.Builder
	for i := 0; i < chunkMaxBlocks+5; i++ {
		b.WriteString("## 章节")
		b.WriteString(string(rune('A' + i%26)))
		b.WriteString("\n\n内容")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString("\n\n")
	}
	chunks, truncated := chunkMarkdown(b.String())
	if !truncated {
		t.Fatalf("expected truncated=true when exceeding %d blocks, got %d chunks", chunkMaxBlocks, len(chunks))
	}
	if len(chunks) != chunkMaxBlocks {
		t.Fatalf("chunks = %d, want exactly %d", len(chunks), chunkMaxBlocks)
	}
}

// TestChunkMarkdownShortNotTruncated 验证普通文档不会误报截断。
func TestChunkMarkdownShortNotTruncated(t *testing.T) {
	_, truncated := chunkMarkdown("# 标题\n\n内容一\n\n## 子标题\n\n内容二")
	if truncated {
		t.Fatalf("short doc must not report truncated")
	}
}

// TestChunkMarkdownTableKeepsHeader 验证超长表格按行分段后每段都保留表头,
// 否则表体脱离列名后 LLM 无法理解列含义。
func TestChunkMarkdownTableKeepsHeader(t *testing.T) {
	var b strings.Builder
	b.WriteString("| 参数名 | 说明 | 默认值 |\n| --- | --- | --- |\n")
	for i := 0; i < 120; i++ {
		b.WriteString("| param")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(" | 这是该参数的中文说明文字 | 默认值ABC |\n")
	}
	chunks, _ := chunkMarkdown(b.String())
	if len(chunks) < 2 {
		t.Fatalf("long table should split into >=2 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if !strings.HasPrefix(strings.TrimSpace(c.Content), "| 参数名") {
			t.Fatalf("chunk %d lost table header: %q", i, firstLine(c.Content))
		}
		if !strings.Contains(c.Content, "| --- |") {
			t.Fatalf("chunk %d lost table separator: %q", i, firstLine(c.Content))
		}
	}
	// 表格不应与正文混在一块
	if chunks[0].Kind != "table" {
		t.Fatalf("table chunk kind = %q, want table", chunks[0].Kind)
	}
}

// TestChunkMarkdownTableNotMergedWithText 验证表格与前后正文分成不同块。
func TestChunkMarkdownTableNotMergedWithText(t *testing.T) {
	content := "# 配置说明\n\n下面是参数表。\n\n| 名称 | 值 |\n| --- | --- |\n| a | 1 |\n| b | 2 |\n\n表格结束后的补充说明。"
	chunks, _ := chunkMarkdown(content)
	var kinds []string
	for _, c := range chunks {
		kinds = append(kinds, c.Kind)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "table") {
		t.Fatalf("expected a table chunk, kinds = %s", joined)
	}
	if len(chunks) < 2 {
		t.Fatalf("table should be separated from prose, got %d chunk(s)", len(chunks))
	}
}

// TestChunkMarkdownRuneSafeLongCJK 验证超长中文按 rune 切分, 不产生半个汉字(UTF-8 非法序列)。
func TestChunkMarkdownRuneSafeLongCJK(t *testing.T) {
	long := strings.Repeat("运维知识库分片存储的检索效果验证文本。", 200)
	chunks, _ := chunkMarkdown(long)
	if len(chunks) < 2 {
		t.Fatalf("expected >=2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if !validUTF8Runes(c.Content) {
			t.Fatalf("chunk contains invalid UTF-8 (byte-level split): %q", firstLine(c.Content))
		}
		if tokenEstimate(c.Content) > chunkMaxTokens*2 {
			t.Fatalf("chunk too long: %d tokens", tokenEstimate(c.Content))
		}
	}
}

// TestSplitByTokensOverlap 验证滑窗切分相邻段有重叠(保持跨块语义连续)。
func TestSplitByTokensOverlap(t *testing.T) {
	s := strings.Repeat("这是用于验证重叠切分的中文句子内容。", 120)
	parts := splitByTokens(s)
	if len(parts) < 2 {
		t.Fatalf("expected >=2 parts, got %d", len(parts))
	}
	// 前一段的尾部应与后一段的头部存在公共子串(重叠区)
	tail := tailRunesForTest(parts[0], 20)
	if !strings.Contains(parts[1], tail) {
		t.Fatalf("no overlap between parts: tail=%q not in next part head", tail)
	}
}

// TestChunkMarkdownEmptyProducesNoChunks 验证空正文不产出"内容为空"的占位块。
//
// 回归背景: 早期实现会给空内容补一个空块「避免上层空转」, 后果是空块被送去 embedding
// (白花一次调用)并作为语义无意义的命中占据 top-k 名额, 把真正有信息量的片段挤出结果集。
func TestChunkMarkdownEmptyProducesNoChunks(t *testing.T) {
	chunks, truncated := chunkMarkdown("")
	if len(chunks) != 0 {
		t.Fatalf("empty content must yield 0 chunks, got %d: %+v", len(chunks), chunks)
	}
	if truncated {
		t.Fatalf("empty content must not report truncated")
	}
}

// TestChunkMarkdownWhitespaceOnlyProducesNoChunks 验证纯空白正文同样不产出块。
func TestChunkMarkdownWhitespaceOnlyProducesNoChunks(t *testing.T) {
	for _, in := range []string{"   ", "\n\n\n", " \t \n  \n\t", "# \n\n   \n"} {
		chunks, _ := chunkMarkdown(in)
		for _, c := range chunks {
			if strings.TrimSpace(c.Content) == "" {
				t.Fatalf("input %q produced empty chunk %+v", in, c)
			}
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func validUTF8Runes(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

func tailRunesForTest(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
