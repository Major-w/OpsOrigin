package knowledge

import (
	"strings"
	"unicode"
)

// Chunk 是文章切分后的检索单元(块)。
// Heading 记录该块所属的 Markdown 标题路径(如 "1.2 安装"), 用于引用溯源到段落级。
// Kind 记录块的内容形态(文本/代码/表格/列表), 供切分与回传时按形态保护结构。
type Chunk struct {
	Index   int    `json:"index"`
	Heading string `json:"heading,omitempty"`
	Content string `json:"content"`
	Kind    string `json:"kind,omitempty"`
}

// 分块参数: 按 token 估算而非字节/字符数 —— 中英混排下字符数会严重失真
// (英文 1500 字符仅约 350 token, 中文 1500 字符已近 700 token)。
const (
	chunkMaxTokens    = 600 // 单块 token 预算(中文 1 字≈1 token, 其他 4 字符≈1 token)
	chunkOverlapRunes = 80  // 相邻块重叠的 rune 数, 保持跨块语义连续
	chunkMaxBlocks    = 64  // 单篇文章分块上限, 防异常长文打爆 embedding 请求
)

// blockKind 标记当前累积块的内容形态, 用于决定切分策略与结构保护。
type blockKind int

const (
	kindText blockKind = iota
	kindCode
	kindTable
	kindList
)

func (k blockKind) String() string {
	switch k {
	case kindCode:
		return "code"
	case kindTable:
		return "table"
	case kindList:
		return "list"
	default:
		return "text"
	}
}

// chunkMarkdown 按 Markdown 标题层级感知切块:
//   - 以 # ~ #### 标题作为块边界(保留标题路径到 Heading);
//   - 标题之间的正文独立成块(去掉首尾空行, 压缩连续空行);
//   - 单个块超过 chunkMaxTokens 时按段落/token 窗口二次切分并带重叠;
//   - 代码块(``` 包裹)整体保留, 内部不按标题切, 防止把代码拦腰截断;
//   - 表格(| 行)与连续列表整体成块; 超长表格按行分段并重复表头, 避免表体脱离表头。
//
// 第二个返回值表示是否触发了 chunkMaxBlocks 上限而被截断(调用方应告警, 避免静默丢内容)。
// 纯文本(无任何标题)整体切分, 超长按窗口切。
func chunkMarkdown(content string) ([]Chunk, bool) {
	lines := strings.Split(content, "\n")
	var chunks []Chunk
	var buf strings.Builder
	var heading string
	var headingStack []string
	inCode := false
	curKind := kindText

	flush := func() {
		text := strings.TrimSpace(buf.String())
		buf.Reset()
		if text == "" {
			curKind = kindText
			return
		}
		// 超长块二次切分(按形态: 表格保表头, 其余按段落/token 窗口, 带重叠)
		if tokenEstimate(text) > chunkMaxTokens {
			for _, sub := range splitLong(text, curKind) {
				chunks = append(chunks, Chunk{Index: len(chunks), Heading: heading, Content: sub, Kind: curKind.String()})
			}
			curKind = kindText
			return
		}
		chunks = append(chunks, Chunk{Index: len(chunks), Heading: heading, Content: text, Kind: curKind.String()})
		curKind = kindText
	}

	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		// 代码块状态切换(不按标题切分代码块内部)
		if strings.HasPrefix(trimmed, "```") {
			// 代码围栏不做形态切换 flush: 说明文字与其代码块同属一段语义上下文,
			// 强行拆开会让代码块脱离说明, 检索命中时只剩代码没有上下文。
			// 仅当块以代码开头时标记为 code 形态(影响超长时的切分策略)。
			if !inCode && strings.TrimSpace(buf.String()) == "" {
				curKind = kindCode
			}
			inCode = !inCode
			buf.WriteString(ln)
			buf.WriteString("\n")
			continue
		}
		if inCode {
			buf.WriteString(ln)
			buf.WriteString("\n")
			continue
		}
		if isHeading(trimmed) {
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
		// 形态切换(文本 ↔ 表格 ↔ 列表): 不同形态不混在一块, 防止表格被正文打断
		k := lineKind(trimmed, curKind)
		if k != curKind && strings.TrimSpace(buf.String()) != "" {
			flush()
		}
		if k != kindText || curKind == kindText {
			curKind = k
		}
		buf.WriteString(ln)
		buf.WriteString("\n")
	}
	flush()

	// 空/纯空白内容返回 0 块, 不产出占位空块。
	//
	// 历史实现会给空内容补一个内容为空的块「避免上层空转」, 但代价是真实的:
	// 空块会被送去 embedding(白花一次 API 调用), 成功拿到向量后又会作为语义上
	// 无意义的命中占据 top-k 名额, 把真正有信息量的片段挤出结果集。
	// 调用方需按 len(chunks)==0 短路(见 syncChunks / syncAttachmentChunks)。
	//
	// 上限保护: 超出部分丢弃, 但必须让调用方感知(返回 truncated=true)
	truncated := false
	if len(chunks) > chunkMaxBlocks {
		chunks = chunks[:chunkMaxBlocks]
		truncated = true
	}
	return chunks, truncated
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

// lineKind 判定单行的内容形态。
// 表格的分隔行(|---|---|)在已有表格上下文时归为表格, 否则视为普通文本。
func lineKind(trimmed string, cur blockKind) blockKind {
	if trimmed == "" {
		return cur
	}
	if strings.HasPrefix(trimmed, "|") {
		return kindTable
	}
	if cur == kindTable && isTableSeparator(trimmed) {
		return kindTable
	}
	if isListLine(trimmed) {
		return kindList
	}
	return kindText
}

// isTableSeparator 判断是否为 Markdown 表格分隔行, 形如 |---|:---:|---|。
func isTableSeparator(s string) bool {
	if !strings.Contains(s, "-") {
		return false
	}
	for _, r := range s {
		switch r {
		case '-', ':', '|', ' ':
		default:
			return false
		}
	}
	return true
}

// isListLine 判断是否为无序/有序列表项。
func isListLine(s string) bool {
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "+ ") {
		return true
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) {
		return false
	}
	return (s[i] == '.' || s[i] == ')') && i+1 < len(s) && s[i+1] == ' '
}

// splitLong 将超长文本切分为 token 预算内的多段。
// 表格走 splitTable(分段并重复表头), 其余先按段落聚合再按 token 窗口切。
func splitLong(text string, kind blockKind) []string {
	if kind == kindTable {
		if parts := splitTable(text); len(parts) > 0 {
			return parts
		}
	}
	paras := splitParagraphs(text)
	var out []string
	var cur strings.Builder
	for _, p := range paras {
		if cur.Len() > 0 && tokenEstimate(cur.String())+tokenEstimate(p) > chunkMaxTokens {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
		cur.WriteString(p)
		cur.WriteString("\n\n")
		if tokenEstimate(cur.String()) > chunkMaxTokens*2 { // 防御: 单段落本身超长
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	// token 窗口兜底(极端: 无空行的超长文本)
	var fixed []string
	for _, s := range out {
		if tokenEstimate(s) <= chunkMaxTokens {
			fixed = append(fixed, s)
			continue
		}
		fixed = append(fixed, splitByTokens(s)...)
	}
	if len(fixed) == 0 {
		fixed = []string{strings.TrimSpace(text)}
	}
	return fixed
}

// splitTable 将超长 Markdown 表格按数据行分段, 每段都带上表头与分隔行。
// 表头与表体分离会让 LLM 无法理解列含义, 因此必须重复表头。
func splitTable(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return nil
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "|") || !isTableSeparator(strings.TrimSpace(lines[1])) {
		return nil
	}
	header := lines[0]
	sep := lines[1]
	body := lines[2:]
	var out []string
	var cur []string
	budget := tokenEstimate(header) + tokenEstimate(sep)
	for _, ln := range body {
		t := tokenEstimate(ln)
		if len(cur) > 0 && budget+t > chunkMaxTokens {
			out = append(out, strings.TrimSpace(strings.Join(append([]string{header, sep}, cur...), "\n")))
			cur = nil
			budget = tokenEstimate(header) + tokenEstimate(sep)
		}
		cur = append(cur, ln)
		budget += t
	}
	if len(cur) > 0 {
		out = append(out, strings.TrimSpace(strings.Join(append([]string{header, sep}, cur...), "\n")))
	}
	return out
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

// splitByTokens 按 token 预算滑窗切分, 相邻窗口重叠 chunkOverlapRunes 个 rune。
// 必须按 rune 而非 byte 切分: 中文一个字 3 字节, 按 byte 切会切出半个字导致乱码。
func splitByTokens(s string) []string {
	runes := []rune(s)
	var out []string
	start := 0
	for start < len(runes) {
		end := start
		tok := 0.0
		for end < len(runes) && tok < float64(chunkMaxTokens) {
			tok += runeTokenWeight(runes[end])
			end++
		}
		if seg := strings.TrimSpace(string(runes[start:end])); seg != "" {
			out = append(out, seg)
		}
		if end >= len(runes) {
			break
		}
		// 下一段回退 chunkOverlapRunes 个 rune 作为重叠; 回退到起点则强制前进防死循环
		next := end - chunkOverlapRunes
		if next <= start {
			next = end
		}
		start = next
	}
	if len(out) == 0 {
		out = []string{strings.TrimSpace(s)}
	}
	return out
}

// tokenEstimate 粗估文本的 token 数: CJK 按 1 token/字, 其余按 4 字符/token。
func tokenEstimate(s string) int {
	t := 0.0
	for _, r := range s {
		t += runeTokenWeight(r)
	}
	return int(t + 0.5)
}

// runeTokenWeight 单个 rune 的 token 权重。
func runeTokenWeight(r rune) float64 {
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
		return 1.0
	}
	if r == '\n' || r == '\t' {
		return 0.1
	}
	return 0.25
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
