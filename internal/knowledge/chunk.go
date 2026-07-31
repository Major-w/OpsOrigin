package knowledge

import "strings"

// Chunk 是文章切分后的检索单元(块)。
// Heading 记录该块所属的 Markdown 标题路径(如 "1.2 安装"), 用于引用溯源到段落级。
type Chunk struct {
	Index   int    `json:"index"`
	Heading string `json:"heading,omitempty"`
	Content string `json:"content"`
}

// 分块参数: 中文场景按字符估算, 约 1500 字符 ≈ 500~700 token; 重叠 120 字符保持跨块语义连续。
const (
	chunkMaxLen    = 1500
	chunkOverlap   = 120
	chunkMaxBlocks = 64 // 单篇文章分块上限, 防异常长文打爆 embedding 请求
)

// chunkMarkdown 按 Markdown 标题层级感知切块:
//   - 以 # ~ #### 标题作为块边界(保留标题路径到 Heading);
//   - 标题之间的正文独立成块(去掉首尾空行, 压缩连续空行);
//   - 单个块超过 chunkMaxLen 时按段落/固定窗口二次切分并带重叠;
//   - 代码块(``` 包裹)整体保留, 内部不按标题切, 防止把代码拦腰截断。
// 纯文本(无任何标题)整体切分, 超长按窗口切。
func chunkMarkdown(content string) []Chunk {
	lines := strings.Split(content, "\n")
	var chunks []Chunk
	var buf strings.Builder
	var heading string
	var headingStack []string
	inCode := false

	flush := func() {
		text := strings.TrimSpace(buf.String())
		buf.Reset()
		if text == "" {
			return
		}
		// 超长块二次切分(按段落/窗口, 带重叠)
		if len(text) > chunkMaxLen {
			for _, sub := range splitLong(text) {
				chunks = append(chunks, Chunk{Index: len(chunks), Heading: heading, Content: sub})
			}
			return
		}
		chunks = append(chunks, Chunk{Index: len(chunks), Heading: heading, Content: text})
	}

	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		// 代码块状态切换(不按标题切分代码块内部)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			buf.WriteString(ln)
			buf.WriteString("\n")
			continue
		}
		if !inCode && isHeading(trimmed) {
			flush()
			// 更新标题路径栈
			level := headingLevel(trimmed)
			title := strings.TrimSpace(trimmed[level:])
			headingStack = headingStack[:min(level-1, len(headingStack))]
			headingStack = append(headingStack, title)
			heading = strings.Join(headingStack, " / ")
			buf.WriteString(ln)
			buf.WriteString("\n")
			continue
		}
		buf.WriteString(ln)
		buf.WriteString("\n")
	}
	flush()

	// 兜底: 完全没切出块(空/纯空白)时, 给一个空块避免上层空转
	if len(chunks) == 0 {
		chunks = append(chunks, Chunk{Index: 0, Content: strings.TrimSpace(content)})
	}
	// 上限保护
	if len(chunks) > chunkMaxBlocks {
		chunks = chunks[:chunkMaxBlocks]
	}
	return chunks
}

// isHeading 判断是否为 1~4 级 Markdown 标题。
func isHeading(line string) bool {
	if line == "" || line[0] != '#' {
		return false
	}
	lv := headingLevel(line)
	return lv >= 1 && lv <= 4 && len(line) > lv
}

// headingLevel 返回标题级别(连续 # 个数); 非标题返回 0。
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n
}

// splitLong 将超长文本按段落/固定窗口切分(带重叠)。
// 优先按空行分段; 单段仍超长则按 chunkMaxLen 固定窗口切, 相邻窗口重叠 chunkOverlap。
func splitLong(text string) []string {
	paras := splitParagraphs(text)
	var out []string
	var cur strings.Builder
	for _, p := range paras {
		if cur.Len() > 0 && cur.Len()+len(p) > chunkMaxLen {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
		cur.WriteString(p)
		cur.WriteString("\n\n")
		if cur.Len() > chunkMaxLen*2 { // 防御: 单段落本身超长
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	// 固定窗口兜底(极端: 无空行的超长文本)
	var fixed []string
	for _, s := range out {
		if len(s) <= chunkMaxLen {
			fixed = append(fixed, s)
			continue
		}
		for i := 0; i < len(s); i += chunkMaxLen - chunkOverlap {
			end := min(i+chunkMaxLen, len(s))
			fixed = append(fixed, s[i:end])
			if end == len(s) {
				break
			}
		}
	}
	if len(fixed) == 0 {
		fixed = []string{strings.TrimSpace(text)}
	}
	return fixed
}

// splitParagraphs 按空行切分段落, 保留段落内部换行。
func splitParagraphs(text string) []string {
	var out []string
	var cur strings.Builder
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) == "" {
			if cur.Len() > 0 {
				out = append(out, strings.TrimSpace(cur.String()))
				cur.Reset()
			}
			continue
		}
		cur.WriteString(ln)
		cur.WriteString("\n")
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	if len(out) == 0 {
		out = []string{strings.TrimSpace(text)}
	}
	return out
}

// chunkEmbedText 生成块的 embedding 文本(标题路径加权在前, 提升标题相关查询命中)。
func chunkEmbedText(c *Chunk) string {
	if c.Heading != "" {
		return c.Heading + "\n" + c.Content
	}
	return c.Content
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
