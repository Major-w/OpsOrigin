package agentic

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TestGeneratedPDFRoundTripCJK 独立校验: 解析生成的 PDF 文件字节,
// 读出实际写入的 ToUnicode CMap + 内容流的 <GID> Tj, 把 GID 还原成 Unicode,
// 断言原始中文可被正确还原(证明 Identity-H + CIDFontType2 + ToUnicode 链路正确, 中文可渲染/复制/搜索)。
func TestGeneratedPDFRoundTripCJK(t *testing.T) {
	font, err := loadCJKFont()
	if err != nil || font == nil {
		t.Skip("跳过: 本环境无可用 CJK 字体")
	}
	headingText := "运维助手 PDF 中文渲染校验"
	paraText := "告警自动拉起 / 根因分析 RCA / 复盘报告，含 English 与数字 123。"
	want := headingText + paraText
	blocks := []pdfBlock{
		{Type: "heading", Level: 1, Text: headingText},
		{Type: "paragraph", Text: paraText},
	}
	data, _, err := generatePDF(blocks)
	if err != nil {
		t.Fatalf("generatePDF: %v", err)
	}

	doc := string(data)
	gidToRune := parseToUnicodeFromPDF(t, doc)
	if len(gidToRune) == 0 {
		t.Fatalf("未从 PDF 解析出 ToUnicode 映射")
	}

	// 提取所有内容流的 <hex> Tj。
	re := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*Tj`)
	var got strings.Builder
	for _, m := range re.FindAllStringSubmatch(doc, -1) {
		hex := m[1]
		for i := 0; i+4 <= len(hex); i += 4 {
			var gid uint16
			fmt.Sscanf(hex[i:i+4], "%X", &gid)
			if r, ok := gidToRune[gid]; ok {
				got.WriteRune(r)
			} else {
				got.WriteRune('�') // 缺映射
			}
		}
	}
	recovered := got.String()
	t.Logf("还原文本: %s", recovered)
	if !strings.Contains(recovered, want) {
		t.Fatalf("中文还原失败:\n  期望包含: %s\n  实际还原: %s", want, recovered)
	}
}

// parseToUnicodeFromPDF 从 PDF 文档文本中定位 ToUnicode CMap 流, 解析 bfchar 得到 GID→rune。
func parseToUnicodeFromPDF(t *testing.T, doc string) map[uint16]rune {
	out := map[uint16]rune{}
	// 找到所有 stream...endstream。
	reStream := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	for _, sm := range reStream.FindAllStringSubmatch(doc, -1) {
		body := sm[1]
		if !strings.Contains(body, "beginbfchar") && !strings.Contains(body, "beginbfrange") {
			continue
		}
		// 解析 bfchar: <gid> <uni>
		reBF := regexp.MustCompile(`<([0-9A-Fa-f]{1,4})>\s+<([0-9A-Fa-f]{1,4})>`)
		for _, bm := range reBF.FindAllStringSubmatch(body, -1) {
			var gid, uni uint16
			fmt.Sscanf(bm[1], "%X", &gid)
			fmt.Sscanf(bm[2], "%X", &uni)
			out[gid] = rune(uni)
		}
	}
	return out
}
