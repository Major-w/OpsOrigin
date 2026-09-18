// AI 文档生成工具 (docx_generate)。
//
// 让对话模型按需生成真实可下载的 .docx Word 文档, 闭环「用户让 AI 写文档/报告/方案」诉求。
// 与 pptx_generate 同构: 纯 Go 手写最小合法 OOXML (ZIP 包), 零外部依赖, 不依赖 python-docx。
// 产物可被 Word / WPS / LibreOffice / Pages 正常打开。
package agentic

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// docxSection 文档的一个章节(含标题 + 段落/要点)。
type docxSection struct {
	Heading    string   `json:"heading"`
	Paragraphs []string `json:"paragraphs"`
	Bullets    []string `json:"bullets"`
}

// docxGenerate 实现 MCP 工具 docx_generate。
func (ts *MCPToolSet) docxGenerate(ctx context.Context, args map[string]any) (any, error) {
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	fileName, _ := args["file_name"].(string)
	fileName = strings.TrimSpace(fileName)

	var sections []docxSection
	if raw, ok := args["sections"].([]any); ok {
		for _, it := range raw {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			s := docxSection{}
			if h, ok := m["heading"].(string); ok {
				s.Heading = strings.TrimSpace(h)
			}
			if ps, ok := m["paragraphs"].([]any); ok {
				for _, p := range ps {
					if s2, ok := p.(string); ok {
						s2 = strings.TrimSpace(s2)
						if s2 != "" {
							s.Paragraphs = append(s.Paragraphs, s2)
						}
					}
				}
			}
			if bs, ok := m["bullets"].([]any); ok {
				for _, b := range bs {
					if s2, ok := b.(string); ok {
						s2 = strings.TrimSpace(s2)
						if s2 != "" {
							s.Bullets = append(s.Bullets, s2)
						}
					}
				}
			}
			if s.Heading != "" || len(s.Paragraphs) > 0 || len(s.Bullets) > 0 {
				sections = append(sections, s)
			}
		}
	}

	if title == "" && len(sections) == 0 {
		return nil, fmt.Errorf("docx_generate: 至少需要 title 或至少一个 sections")
	}

	data, err := generateDOCX(title, sections)
	if err != nil {
		return nil, fmt.Errorf("docx_generate: %w", err)
	}

	if fileName == "" {
		fileName = "document-" + time.Now().Format("20060102-150405") + ".docx"
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".docx") {
		fileName += ".docx"
	}

	key := "ai-docx/" + uuid.NewString() + ".docx"
	ct := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if ts.objStore == nil {
		return nil, fmt.Errorf("docx_generate: 未配置对象存储(MinIO), 无法生成可下载的文档")
	}
	if err := ts.objStore.Put(ctx, key, bytes.NewReader(data), int64(len(data)), ct); err != nil {
		return nil, fmt.Errorf("docx_generate: 对象存储写入失败: %w", err)
	}
	u, err := ts.objStore.PresignedURL(key, 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("docx_generate: 预签名失败: %w", err)
	}

	slog.Info("docx_generate ok", "title", title, "sections", len(sections), "file", fileName)
	return map[string]any{
		"url":           u.String(),
		"file_name":     fileName,
		"section_count": len(sections),
		"title":         title,
	}, nil
}

// generateDOCX 拼最小合法 .docx (OOXML ZIP), 返回文件字节。
func generateDOCX(docTitle string, sections []docxSection) ([]byte, error) {
	if docTitle == "" && len(sections) == 0 {
		return nil, fmt.Errorf("empty document: need title or sections")
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	var body strings.Builder
	body.WriteString(`<w:body>`)

	// 文档主标题
	if docTitle != "" {
		body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Title"/></w:pPr><w:r><w:t xml:space="preserve">`)
		body.WriteString(xmlEscape(docTitle))
		body.WriteString(`</w:t></w:r></w:p>`)
	}

	for i, s := range sections {
		if s.Heading != "" {
			body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t xml:space="preserve">`)
			body.WriteString(xmlEscape(s.Heading))
			body.WriteString(`</w:t></w:r></w:p>`)
		}
		for _, p := range s.Paragraphs {
			body.WriteString(`<w:p><w:r><w:t xml:space="preserve">`)
			body.WriteString(xmlEscape(p))
			body.WriteString(`</w:t></w:r></w:p>`)
		}
		for _, b := range s.Bullets {
			body.WriteString(`<w:p><w:pPr><w:ind w:left="420" w:hanging="280"/></w:pPr><w:r><w:t xml:space="preserve">`)
			body.WriteString(xmlEscape("• " + b))
			body.WriteString(`</w:t></w:r></w:p>`)
		}
		_ = i
	}

	// 末尾 sectPr(页面 A4 纵向)。
	body.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
	body.WriteString(`</w:body>`)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			return
		}
		_, _ = w.Write([]byte(content))
	}

	add("[Content_Types].xml", docxContentTypes)
	add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>`+
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>`+
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>`+
		`</Relationships>`)
	add("word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`+
		body.String()+`</w:document>`)
	add("word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`+
		`</Relationships>`)
	add("word/styles.xml", docxStylesXML)
	add("docProps/core.xml", fmt.Sprintf(docxCoreXML, xmlEscape(docTitle), now, now))
	add("docProps/app.xml", docxAppXML)

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("zip close: %w", err)
	}
	return buf.Bytes(), nil
}

const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
	`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
	`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
	`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` +
	`</Types>`

// docxStylesXML 定义常用段落样式(标题/正文), 使 w:pStyle 引用有效, 避免 Word 报错。
const docxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="宋体"/><w:sz w:val="22"/></w:rPr></w:rPrDefault>` +
	`<w:pPrDefault><w:pPr><w:spacing w:after="160" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:pPr><w:spacing w:before="240" w:after="120"/></w:pPr><w:rPr><w:b/><w:sz w:val="44"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="32"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:spacing w:before="200" w:after="100"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:sz w:val="26"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:pPr><w:ind w:left="420"/></w:pPr></w:style>` +
	`</w:styles>`

const docxCoreXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
	`<dc:title>%s</dc:title><dc:creator>OpsOrigin AI</dc:creator><cp:lastModifiedBy>OpsOrigin AI</cp:lastModifiedBy>` +
	`<dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>` +
	`</cp:coreProperties>`

const docxAppXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` +
	`<Application>OpsOrigin AI</Application></Properties>`
