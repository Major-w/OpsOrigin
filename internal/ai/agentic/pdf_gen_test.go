package agentic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePDFWithChinese(t *testing.T) {
	font, err := loadCJKFont()
	if err != nil || font == nil {
		t.Skip("跳过: 本环境无可用 CJK 字体", err)
	}
	blocks := []pdfBlock{
		{Type: "heading", Level: 1, Text: "OpsOrigin 运维助手 — 中文 PDF 生成测试"},
		{Type: "paragraph", Text: "这是一段中文正文，用于验证纯 Go 手写的 PDF 是否正确内嵌了 CJK 字体并能在阅读器中正常渲染。"},
		{Type: "bullet", Text: "支持项目符号列表，例如：告警自动拉起、根因分析、复盘报告。"},
		{Type: "bullet", Text: "English mixed with 中文 should also wrap correctly."},
		{Type: "heading", Level: 2, Text: "二级标题示例"},
		{Type: "paragraph", Text: "Latin words like Kubernetes, Prometheus, Grafana are also supported alongside 中文混排。"},
	}
	data, pages, err := generatePDF(blocks)
	if err != nil {
		t.Fatalf("generatePDF: %v", err)
	}
	if !strings.HasPrefix(string(data), "%PDF-") {
		t.Fatalf("产物不是合法 PDF 头")
	}
	if !strings.Contains(string(data), "startxref") || !strings.HasSuffix(strings.TrimSpace(string(data)), "%%EOF") {
		t.Fatalf("PDF 缺少 xref/trailer")
	}
	out := filepath.Join(os.TempDir(), "opsorigin_pdf_test.pdf")
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	t.Logf("已写出 %s, 页数=%d, 字节=%d", out, pages, len(data))
}

func TestSubFontSliceTTC(t *testing.T) {
	// 验证 ttc 容器能被切出首个字体面(单文件 ttf 原样返回)。
	ttf, err := os.ReadFile("/System/Library/Fonts/Supplemental/Arial Unicode.ttf")
	if err != nil {
		t.Skip("无 Arial Unicode.ttf")
	}
	sub, err := subFontSlice(ttf)
	if err != nil {
		t.Fatalf("subFontSlice: %v", err)
	}
	if len(sub) != len(ttf) {
		t.Fatalf("单文件 ttf 应原样返回, got %d want %d", len(sub), len(ttf))
	}
}

// TestCJKFontCandidatesFallback 校验字体探测的健壮性:
//  1. OPSORIGIN_PDF_FONT 配错时不能"独占"候选列表, 必须仍能回退到默认探测(否则生产丢中文);
//  2. 选中的字体必须真正覆盖 CJK 字形(否则中文渲染成空白)。
func TestCJKFontCandidatesFallback(t *testing.T) {
	orig := os.Getenv("OPSORIGIN_PDF_FONT")
	defer os.Setenv("OPSORIGIN_PDF_FONT", orig)

	os.Setenv("OPSORIGIN_PDF_FONT", "/nonexistent/bogus-font.ttf")
	cands := cjkFontCandidates()
	if len(cands) < 2 {
		t.Fatalf("OPSORIGIN_PDF_FONT 不应独占候选列表: got %d, %v", len(cands), cands)
	}
	if cands[0] != "/nonexistent/bogus-font.ttf" {
		t.Fatalf("环境变量应作为优先项排首位: got %q", cands[0])
	}

	// 找得到的候选里至少含一个真实 CJK 字体(本机有字体时)。
	f, err := loadCJKFont()
	if err == nil && f != nil {
		if !coversCJK(f) {
			t.Fatalf("选中的字体不覆盖 CJK, 中文会渲染成空白")
		}
	} else {
		t.Skip("跳过: 本环境无可用 CJK 字体")
	}
}

func TestFileGenerateMimeAndSanitize(t *testing.T) {
	cases := map[string]string{
		"a.md":      "text/markdown; charset=utf-8",
		"data.csv":  "text/csv; charset=utf-8",
		"x.yaml":    "text/yaml; charset=utf-8",
		"q.sql":     "application/sql; charset=utf-8",
		"weird.xyz": "", // 未知扩展名: 映射为空, 由 handler 回落 text/plain
		"R.Go":      "text/x-go; charset=utf-8",
	}
	for name, want := range cases {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		got := textMimeByExt[ext]
		if got != want {
			t.Errorf("ext=%s mime=%q want %q", ext, got, want)
		}
	}
	if got := sanitizeFileName("a/b\\c:?.md"); got != "a_b_c__.md" {
		t.Errorf("sanitizeFileName=%q", got)
	}
}
