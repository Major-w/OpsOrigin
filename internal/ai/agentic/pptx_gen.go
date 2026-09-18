// AI 文档生成工具 (pptx_generate)。
//
// 设计目标: 让对话模型按需生成真实可下载的 .pptx 演示文稿, 闭环「用户让 AI 写 PPT」诉求。
// 复用 image_generate 的「生成 → 对象存储(MinIO) → 预签名 URL → 模型在回复里贴链接」通道。
//
// 生成方式: 纯 Go 手写最小合法 OOXML (一个 ZIP 包, 内含 presentation/slideMaster/slideLayout/
// theme/slides 等标准部件), 零外部依赖, 不依赖 python-pptx 或任何侧车进程。产物可被
// PowerPoint / WPS / LibreOffice / Keynote 正常打开。
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

// pptxSlide 单页内容。
type pptxSlide struct {
	Title   string   `json:"title"`
	Bullets []string `json:"bullets"`
}

// pptxGenerate 实现 MCP 工具 pptx_generate。
// 失败返回 error, 模型收到后可调整内容重试。
func (ts *MCPToolSet) pptxGenerate(ctx context.Context, args map[string]any) (any, error) {
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	subtitle, _ := args["subtitle"].(string)
	subtitle = strings.TrimSpace(subtitle)
	fileName, _ := args["file_name"].(string)
	fileName = strings.TrimSpace(fileName)

	var slides []pptxSlide
	if raw, ok := args["slides"].([]any); ok {
		for _, it := range raw {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			s := pptxSlide{}
			if t, ok := m["title"].(string); ok {
				s.Title = strings.TrimSpace(t)
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
			if s.Title != "" || len(s.Bullets) > 0 {
				slides = append(slides, s)
			}
		}
	}

	if title == "" && len(slides) == 0 {
		return nil, fmt.Errorf("pptx_generate: 至少需要 title 或至少一页 slides")
	}

	// 组装最终页列表: 有 deck 标题时作为封面页, 其后接内容页。
	final := make([]pptxSlide, 0, len(slides)+1)
	if title != "" {
		cover := pptxSlide{Title: title}
		if subtitle != "" {
			cover.Bullets = []string{subtitle}
		}
		final = append(final, cover)
	}
	final = append(final, slides...)

	data, err := generatePPTX(title, final)
	if err != nil {
		return nil, fmt.Errorf("pptx_generate: %w", err)
	}

	if fileName == "" {
		fileName = "presentation-" + time.Now().Format("20060102-150405") + ".pptx"
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".pptx") {
		fileName += ".pptx"
	}

	key := "ai-pptx/" + uuid.NewString() + ".pptx"
	ct := "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	if ts.objStore == nil {
		return nil, fmt.Errorf("pptx_generate: 未配置对象存储(MinIO), 无法生成可下载的 PPT")
	}
	if err := ts.objStore.Put(ctx, key, bytes.NewReader(data), int64(len(data)), ct); err != nil {
		return nil, fmt.Errorf("pptx_generate: 对象存储写入失败: %w", err)
	}
	u, err := ts.objStore.PresignedURL(key, 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("pptx_generate: 预签名失败: %w", err)
	}

	slog.Info("pptx_generate ok", "title", title, "slides", len(final), "file", fileName)
	return map[string]any{
		"url":         u.String(),
		"file_name":   fileName,
		"slide_count": len(final),
		"title":       title,
	}, nil
}

// generatePPTX 拼最小合法 .pptx (OOXML ZIP), 返回文件字节。
func generatePPTX(deckTitle string, slides []pptxSlide) ([]byte, error) {
	n := len(slides)
	if n == 0 {
		return nil, fmt.Errorf("no slides")
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			return
		}
		_, _ = w.Write([]byte(content))
	}

	// 1. [Content_Types].xml (动态: 每页一个 override)
	add("[Content_Types].xml", buildContentTypes(n))

	// 2. _rels/.rels
	add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>`+
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>`+
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>`+
		`</Relationships>`)

	// 3. ppt/presentation.xml + rels
	add("ppt/presentation.xml", buildPresentation(n))
	add("ppt/_rels/presentation.xml.rels", buildPresentationRels(n))

	// 4. presProps + theme + slideMaster + slideLayout
	add("ppt/presProps.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<p:presentationPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`)
	add("ppt/theme/theme1.xml", themeXML)
	add("ppt/slideMasters/slideMaster1.xml", slideMasterXML)
	add("ppt/slideMasters/_rels/slideMaster1.xml.rels",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>`+
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>`+
			`</Relationships>`)
	add("ppt/slideLayouts/slideLayout1.xml", slideLayoutXML)
	add("ppt/slideLayouts/_rels/slideLayout1.xml.rels",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>`+
			`</Relationships>`)

	// 5. slides
	for i, s := range slides {
		idx := i + 1
		add(fmt.Sprintf("ppt/slides/slide%d.xml", idx), buildSlideXML(s))
		add(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", idx),
			`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
				`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>`+
				`</Relationships>`)
	}

	// 6. docProps
	add("docProps/core.xml", fmt.Sprintf(coreXML, xmlEscape(deckTitle), now, now))
	add("docProps/app.xml", appXML)

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("zip close: %w", err)
	}
	return buf.Bytes(), nil
}

func buildContentTypes(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>`)
	b.WriteString(`<Override PartName="/ppt/presProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"/>`)
	b.WriteString(`<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>`)
	b.WriteString(`<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>`)
	b.WriteString(`<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i)
	}
	b.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	b.WriteString(`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`)
	b.WriteString(`</Types>`)
	return b.String()
}

func buildPresentation(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">`)
	b.WriteString(`<p:sldIdLst>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<p:sldId id="%d" r:id="rId%d"/>`, 255+i, i)
	}
	b.WriteString(`</p:sldIdLst>`)
	b.WriteString(`<p:sldSz cx="9144000" cy="6858000" type="screen16x9"/>`)
	b.WriteString(`<p:notesSz cx="6858000" cy="9144000"/>`)
	b.WriteString(`</p:presentation>`)
	return b.String()
}

func buildPresentationRels(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, i, i)
	}
	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`, n+1)
	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/presProps" Target="presProps.xml"/>`, n+2)
	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/>`, n+3)
	b.WriteString(`</Relationships>`)
	return b.String()
}

func buildSlideXML(s pptxSlide) string {
	var body strings.Builder
	if len(s.Bullets) == 0 {
		body.WriteString(`<a:p><a:endParaRPr lang="en-US"/></a:p>`)
	} else {
		for _, bl := range s.Bullets {
			fmt.Fprintf(&body, `<a:p><a:pPr marL="285750" indent="-285750"><a:buFont typeface="Arial"/><a:buChar char="&#8226;"/></a:pPr><a:r><a:rPr lang="en-US"/><a:t>%s</a:t></a:r></a:p>`, xmlEscape(bl))
		}
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
		`<p:cSld>` +
		`<p:spTree>` +
		`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
		`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
		// 标题
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title"/><p:cNvSpPr><p:ph type="title"/></p:cNvSpPr><p:nvPr/></p:nvSpPr>` +
		`<p:spPr><a:xfrm><a:off x="838200" y="365125"/><a:ext cx="7464150" cy="1325563"/></a:xfrm></p:spPr>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/>` +
		fmt.Sprintf(`<a:p><a:r><a:rPr lang="en-US" b="1" sz="4400"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:rPr><a:t>%s</a:t></a:r></a:p>`, xmlEscape(s.Title)) +
		`</p:txBody></p:sp>` +
		// 正文
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Body"/><p:cNvSpPr><p:ph type="body" idx="1"/></p:cNvSpPr><p:nvPr/></p:nvSpPr>` +
		`<p:spPr><a:xfrm><a:off x="838200" y="1825625"/><a:ext cx="7464150" cy="4351338"/></a:xfrm></p:spPr>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/>` + body.String() + `</p:txBody></p:sp>` +
		`</p:spTree>` +
		`</p:cSld>` +
		`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>` +
		`</p:sld>`
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// 静态部件: theme / slideMaster / slideLayout / docProps。
const themeXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Office Theme">` +
	`<a:themeElements>` +
	`<a:clrScheme name="Office"><a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>` +
	`<a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2>` +
	`<a:accent1><a:srgbClr val="4472C4"/></a:accent1><a:accent2><a:srgbClr val="ED7D31"/></a:accent2><a:accent3><a:srgbClr val="A5A5A5"/></a:accent3>` +
	`<a:accent4><a:srgbClr val="FFC000"/></a:accent4><a:accent5><a:srgbClr val="5B9BD5"/></a:accent5><a:accent6><a:srgbClr val="70AD47"/></a:accent6>` +
	`<a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme>` +
	`<a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>` +
	`<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>` +
	`<a:fmtScheme name="Office">` +
	`<a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst>` +
	`<a:lnStyleLst><a:ln><a:noFill/></a:ln></a:lnStyleLst>` +
	`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>` +
	`<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst>` +
	`</a:fmtScheme>` +
	`</a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`

const slideMasterXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">` +
	`<p:cSld>` +
	`<p:bg><p:bgPr><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>` +
	`<p:spTree>` +
	`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm/></p:grpSpPr>` +
	`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title Placeholder 1"/><p:cNvSpPr><p:ph type="title"/></p:cNvSpPr><p:nvPr/></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>` +
	`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Text Placeholder 2"/><p:cNvSpPr><p:ph type="body" idx="1"/></p:cNvSpPr><p:nvPr/></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>` +
	`</p:spTree></p:cSld>` +
	`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
	`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>` +
	`<p:txStyles><p:titleStyle><a:lvl1pPr algn="l"><a:defRPr sz="4400"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mj-lt"/></a:defRPr></a:lvl1pPr></p:titleStyle>` +
	`<p:bodyStyle><a:lvl1pPr><a:defRPr sz="1800"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/></a:defRPr></a:lvl1pPr></p:bodyStyle>` +
	`<p:otherStyle><a:lvl1pPr><a:defRPr sz="1800"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:defRPr></a:lvl1pPr></p:otherStyle></p:txStyles>` +
	`</p:sldMaster>`

const slideLayoutXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="title">` +
	`<p:cSld name="Title Slide">` +
	`<p:spTree>` +
	`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm/></p:grpSpPr>` +
	`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title 1"/><p:cNvSpPr><p:ph type="title" idx="1"/></p:cNvSpPr><p:nvPr/></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>` +
	`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Body 1"/><p:cNvSpPr><p:ph type="body" idx="1"/></p:cNvSpPr><p:nvPr/></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>` +
	`</p:spTree></p:cSld>` +
	`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>` +
	`</p:sldLayout>`

const coreXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
	`<dc:title>%s</dc:title><dc:creator>OpsOrigin AI</dc:creator><cp:lastModifiedBy>OpsOrigin AI</cp:lastModifiedBy>` +
	`<dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>` +
	`</cp:coreProperties>`

const appXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` +
	`<Application>OpsOrigin AI</Application></Properties>`
