package docextract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildZip 用给定 {文件名: 内容} 构造一个 zip 字节流(用于伪造最小 OOXML 包)。
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractXlsxText(t *testing.T) {
	// 最小 xlsx: 共享字符串 + 一张表(表头 + 两行数据, 含共享串索引与内联数字)。
	data := buildZip(t, map[string]string{
		"xl/workbook.xml": `<workbook><sheets>` +
			`<sheet name="资产清单" sheetId="1" r:id="rId1"/>` +
			`</sheets></workbook>`,
		"xl/sharedStrings.xml": `<sst><si><t>主机</t></si><si><t>web-01</t></si><si><t>db-02</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>` +
			`<row><c r="A1" t="s"><v>0</v></c><c r="B1" t="inlineStr"><is><t>编号</t></is></c></row>` +
			`<row><c r="A2" t="s"><v>1</v></c><c r="B2"><v>1001</v></c></row>` +
			`<row><c r="A3" t="s"><v>2</v></c><c r="B3"><v>1002</v></c></row>` +
			`</sheetData></worksheet>`,
	})
	got, err := ExtractXlsxText(data)
	if err != nil {
		t.Fatalf("ExtractXlsxText: %v", err)
	}
	for _, want := range []string{"## 资产清单", "主机", "web-01", "db-02", "1001", "1002", "| --- |"} {
		if !strings.Contains(got, want) {
			t.Errorf("输出缺少 %q:\n%s", want, got)
		}
	}
	// 表格结构: 3 行数据 + 表头分隔行
	if n := strings.Count(got, "|"); n < 12 {
		t.Errorf("表格单元格过少, 竖线数=%d\n%s", n, got)
	}
}

func TestExtractXlsxTextNotZip(t *testing.T) {
	if _, err := ExtractXlsxText([]byte("not a zip")); err == nil {
		t.Fatal("非 zip 数据应报错")
	}
}

func TestExtractPptxText(t *testing.T) {
	data := buildZip(t, map[string]string{
		"ppt/slides/slide1.xml": `<p:sld><p:sp><a:t>封面标题</a:t></p:sp></p:sld>`,
		"ppt/slides/slide2.xml": `<p:sld><p:sp><a:t>要点一</a:t><a:t>要点二</a:t></p:sp></p:sld>`,
	})
	got, err := ExtractPptxText(data)
	if err != nil {
		t.Fatalf("ExtractPptxText: %v", err)
	}
	// 幻灯片必须按编号顺序输出(slide1 先于 slide2), 否则跨页语义错乱。
	i1, i2 := strings.Index(got, "封面标题"), strings.Index(got, "要点一")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Errorf("幻灯片顺序错误:\n%s", got)
	}
	if !strings.Contains(got, "第 1 页") || !strings.Contains(got, "要点二") {
		t.Errorf("输出不完整:\n%s", got)
	}
}

func TestExtractHTMLText(t *testing.T) {
	html := `<html><head><title>告警处置</title>` +
		`<script>alert('xss')</script><style>body{color:red}</style></head>` +
		`<body><h1>磁盘满</h1><p>清理 &amp; 扩容</p><br><li>步骤一</li></body></html>`
	got := ExtractHTMLText(html)
	if strings.Contains(got, "alert") || strings.Contains(got, "color:red") {
		t.Errorf("script/style 未剥离: %q", got)
	}
	if !strings.Contains(got, "磁盘满") || !strings.Contains(got, "清理 & 扩容") {
		t.Errorf("正文或实体还原缺失: %q", got)
	}
	// &amp; 必须最后替换, 否则会被二次解码成 "&amp;"
	if strings.Contains(got, "&amp;") {
		t.Errorf("实体二次解码: %q", got)
	}
}

func TestExtractTextUnsupportedExt(t *testing.T) {
	if _, err := ExtractText("a.zip", []byte("PK\x03\x04")); err == nil {
		t.Fatal("不支持的扩展名应报错")
	}
	if !Parseable("a.pdf") || !Parseable("a.xlsx") || Parseable("a.zip") {
		t.Errorf("Parseable 判定错误")
	}
}

func TestLooksLikeText(t *testing.T) {
	if !LooksLikeText([]byte("hello 世界\n第二行")) {
		t.Error("纯文本应判定为文本")
	}
	if LooksLikeText([]byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe, 0x00, 0x10}) {
		t.Error("二进制应判定为非文本")
	}
}

func TestExtractTextPlainPassthrough(t *testing.T) {
	got, err := ExtractText("note.md", []byte("# 标题\n内容"))
	if err != nil {
		t.Fatalf("markdown 应作为纯文本通过: %v", err)
	}
	if got != "# 标题\n内容" {
		t.Errorf("纯文本被改写: %q", got)
	}
}
