package agentic

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

func assertValidXML(t *testing.T, data []byte, label string) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("%s: XML 不合法: %v", label, err)
		}
	}
}

func readZipMember(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}
			defer rc.Close()
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rc)
			return buf.Bytes()
		}
	}
	t.Fatalf("zip 缺少成员: %s", name)
	return nil
}

func TestGenerateDOCX_ValidZip(t *testing.T) {
	sections := []docxSection{
		{Heading: "第一章 概述", Paragraphs: []string{"这是一段正文, 介绍背景。", "第二段落含特殊符号 & < > \" 引号。"}, Bullets: []string{"要点一", "要点二 & 符号"}},
		{Heading: "第二章 结论", Paragraphs: []string{"总结内容。"}},
	}
	data, err := generateDOCX("测试文档 & <标题>", sections)
	if err != nil {
		t.Fatalf("generateDOCX: %v", err)
	}

	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/_rels/document.xml.rels", "word/styles.xml", "docProps/core.xml", "docProps/app.xml"} {
		b := readZipMember(t, data, name)
		if len(b) == 0 {
			t.Errorf("空成员: %s", name)
		}
		assertValidXML(t, b, name)
	}

	doc := readZipMember(t, data, "word/document.xml")
	if !strings.Contains(string(doc), `w:pStyle w:val="Heading1"`) {
		t.Errorf("document 未含 Heading1 样式")
	}
	if !strings.Contains(string(doc), `&amp;`) {
		t.Errorf("document 未转义特殊字符 &")
	}
	if !strings.Contains(string(doc), `&lt;`) {
		t.Errorf("document 未转义 <")
	}
}

func TestGenerateXLSX_ValidZip(t *testing.T) {
	sheets := []xlsxSheet{
		{
			Name:    "人员清单",
			Headers: []string{"姓名", "年龄", "部门"},
			Rows: [][]any{
				{"张三", 28, "研发"},
				{"李四 & 王五", 35, "市场"},
			},
		},
		{
			Name:    "汇总",
			Headers: []string{"指标", "值"},
			Rows:    [][]any{{"总数", 2}},
		},
	}
	data, err := generateXLSX(sheets)
	if err != nil {
		t.Fatalf("generateXLSX: %v", err)
	}

	for _, name := range []string{"[Content_Types].xml", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/styles.xml", "docProps/core.xml"} {
		b := readZipMember(t, data, name)
		assertValidXML(t, b, name)
	}

	wb := readZipMember(t, data, "xl/workbook.xml")
	if !strings.Contains(string(wb), `name="人员清单"`) || !strings.Contains(string(wb), `name="汇总"`) {
		t.Errorf("workbook 未含两个 sheet 名")
	}

	s1 := readZipMember(t, data, "xl/worksheets/sheet1.xml")
	if !strings.Contains(string(s1), `t="inlineStr"`) {
		t.Errorf("sheet1 未用 inlineStr")
	}
	if !strings.Contains(string(s1), `<v>28</v>`) {
		t.Errorf("sheet1 数值单元格未正确写入 (28)")
	}
	if !strings.Contains(string(s1), `&amp;`) {
		t.Errorf("sheet1 未转义 & (李四 & 王五)")
	}
	// 表头应使用 bold 样式 s="1"
	if !strings.Contains(string(s1), `s="1"`) {
		t.Errorf("sheet1 表头未使用 bold 样式")
	}
}

func TestColLetter(t *testing.T) {
	cases := map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"}
	for in, want := range cases {
		if got := colLetter(in); got != want {
			t.Errorf("colLetter(%d)=%s, want %s", in, got, want)
		}
	}
}

func TestGenerateDOCX_EmptyRejected(t *testing.T) {
	if _, err := generateDOCX("", nil); err == nil {
		t.Errorf("空文档应报错")
	}
}

func TestGenerateXLSX_EmptyRejected(t *testing.T) {
	if _, err := generateXLSX(nil); err == nil {
		t.Errorf("空工作簿应报错")
	}
}
