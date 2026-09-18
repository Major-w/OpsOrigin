package agentic

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"
	"testing"
)

func TestGeneratePPTXValidZipAndXML(t *testing.T) {
	slides := []pptxSlide{
		{Title: "封面", Bullets: []string{"2026 年度汇报"}},
		{Title: "背景", Bullets: []string{"业务快速增长", "运维复杂度上升", "需要统一平台"}},
		{Title: "方案", Bullets: []string{"CMDB 统一建模", "AI 辅助排障", "自动化变更"}},
	}
	data, err := generatePPTX("OpsOrigin 介绍", slides)
	if err != nil {
		t.Fatalf("generatePPTX error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty output")
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}

	want := map[string]bool{
		"[Content_Types].xml":               true,
		"_rels/.rels":                       true,
		"ppt/presentation.xml":              true,
		"ppt/_rels/presentation.xml.rels":   true,
		"ppt/presProps.xml":                 true,
		"ppt/theme/theme1.xml":              true,
		"ppt/slideMasters/slideMaster1.xml": true,
		"ppt/slideLayouts/slideLayout1.xml": true,
		"ppt/slides/slide1.xml":             true,
		"ppt/slides/slide2.xml":             true,
		"ppt/slides/slide3.xml":             true,
		"docProps/core.xml":                 true,
		"docProps/app.xml":                  true,
	}
	seen := map[string]bool{}
	for _, f := range zr.File {
		seen[f.Name] = true
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			rc.Close()
			t.Fatalf("read %s: %v", f.Name, err)
		}
		rc.Close()
		// 每个 XML 部件必须是良构 XML
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".rels") {
			if err := xml.Unmarshal(buf.Bytes(), &struct{}{}); err != nil {
				t.Fatalf("xml not well-formed %s: %v\n---\n%s", f.Name, err, buf.String()[:min(200, buf.Len())])
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing part: %s", name)
		}
	}

	// presentation rels 应引用全部 3 页 slide
	var presRels bytes.Buffer
	for _, f := range zr.File {
		if f.Name == "ppt/_rels/presentation.xml.rels" {
			rc, _ := f.Open()
			presRels.ReadFrom(rc)
			rc.Close()
		}
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(presRels.String(), "slides/slide"+strconv.Itoa(i)+".xml") {
			t.Errorf("presentation rels missing slide%d", i)
		}
	}

	// slide1 应包含封面标题文本(转义正确)
	var s1 bytes.Buffer
	for _, f := range zr.File {
		if f.Name == "ppt/slides/slide1.xml" {
			rc, _ := f.Open()
			s1.ReadFrom(rc)
			rc.Close()
		}
	}
	if !strings.Contains(s1.String(), "封面") {
		t.Errorf("slide1 missing title text")
	}
}

func TestGeneratePPTXXMLEscape(t *testing.T) {
	slides := []pptxSlide{{Title: "A & B <C>", Bullets: []string{"x < y & z > w"}}}
	data, err := generatePPTX("", slides)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "ppt/slides/slide1.xml" {
			rc, _ := f.Open()
			var buf bytes.Buffer
			buf.ReadFrom(rc)
			rc.Close()
			if !strings.Contains(buf.String(), "&lt;") || !strings.Contains(buf.String(), "&amp;") {
				t.Errorf("special chars not escaped: %s", buf.String())
			}
			// 重新解析确认仍良构
			if err := xml.Unmarshal(buf.Bytes(), &struct{}{}); err != nil {
				t.Errorf("escaped xml invalid: %v", err)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
