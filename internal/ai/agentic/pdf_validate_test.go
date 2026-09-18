package agentic

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// TestGeneratedPDFIsValid 用 pdfcpu 校验手写 PDF 结构合法性(对象/xref/内嵌字体/流)。
func TestGeneratedPDFIsValid(t *testing.T) {
	font, err := loadCJKFont()
	if err != nil || font == nil {
		t.Skip("跳过: 本环境无可用 CJK 字体")
	}
	blocks := []pdfBlock{
		{Type: "heading", Level: 1, Text: "中文 PDF 结构校验"},
		{Type: "paragraph", Text: "包含中文、English、数字123与标点，验证 Identity-H 编码与 ToUnicode 映射。"},
		{Type: "bullet", Text: "告警自动拉起 / 根因分析 RCA / 复盘报告。"},
	}
	data, _, err := generatePDF(blocks)
	if err != nil {
		t.Fatalf("generatePDF: %v", err)
	}
	conf := model.NewDefaultConfiguration()
	if err := api.Validate(bytes.NewReader(data), conf); err != nil {
		t.Fatalf("pdfcpu Validate 失败: %v", err)
	}
	out := filepath.Join(os.TempDir(), "opsorigin_pdf_valid.pdf")
	_ = os.WriteFile(out, data, 0o644)
	t.Logf("PDF 校验通过, 已写出 %s (%d 字节)", out, len(data))
}
