// Package docextract 提供零外部服务依赖的文档文本抽取能力。
//
// 用途: 知识库附件/网页导入、AI 对话附件解析 —— 把 PDF / Office(OOXML) / HTML / 纯文本
// 抽成模型与检索引擎可消费的纯文本。
//
// 设计取舍:
//  1. 纯 Go 实现, 不依赖 pandoc/LibreOffice 等外部进程 —— 解析必须同步返回, 不能让上传请求等子进程。
//  2. OOXML 本质是 zip + XML, 直接读部件即可, 无需引入重量级 Office 库。
//  3. PDF 用 pdfcpu 抽取内容流后再解析文本操作符; 扫描件(纯图片)抽不到文本, 需上层走 OCR。
package docextract

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// MaxTextBytes 抽取结果截断上限(防超长文档拖垮 embedding / 上下文窗口)。
const MaxTextBytes = 2 << 20

// Parseable 是否支持抽取该扩展名的文本。
func Parseable(name string) bool {
	return parseableExt[strings.ToLower(filepath.Ext(name))]
}

// parseableExt 可抽取文本的扩展名集合。
var parseableExt = map[string]bool{
	".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true,
	".md": true, ".markdown": true, ".txt": true, ".text": true, ".log": true,
	".csv": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".sql": true, ".html": true, ".htm": true, ".xml": true,
	".go": true, ".py": true, ".js": true, ".ts": true, ".java": true, ".sh": true, ".conf": true,
}

// ExtractText 按扩展名把文档二进制抽成纯文本。
// 返回 (text, err); err 非 nil 表示不支持该格式或抽取失败(调用方决定降级策略)。
func ExtractText(name string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".pdf":
		return ExtractPDFText(data)
	case ".docx":
		return ExtractDocxText(data)
	case ".xlsx":
		return ExtractXlsxText(data)
	case ".pptx":
		return ExtractPptxText(data)
	case ".html", ".htm", ".xml":
		return ExtractHTMLText(string(data)), nil
	default:
		if !parseableExt[ext] {
			return "", fmt.Errorf("暂不支持解析该格式(%s)", ext)
		}
		if !LooksLikeText(data) {
			return "", fmt.Errorf("文件内容非文本(%s), 无法抽取", ext)
		}
		return string(data), nil
	}
}

// LooksLikeText 粗判二进制: 采样前 8KB, UTF-8 合法且可打印字符占比 > 90% 视为文本。
func LooksLikeText(data []byte) bool {
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if !utf8.Valid(sample) {
		return false
	}
	printable, total := 0, 0
	for _, r := range string(sample) {
		if r == 0 {
			return false
		}
		total++
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			printable++
		}
	}
	if total == 0 {
		return false
	}
	return float64(printable)/float64(total) > 0.9
}

// —— PDF ——

// ExtractPDFText 用 pdfcpu 抽取每页内容流(自动解压 FlateDecode 等), 再从中解析可读文本。
// 扫描件(纯图片型 PDF)抽不到文本, 返回错误由上层降级。
func ExtractPDFText(data []byte) (string, error) {
	conf := pdfmodel.NewDefaultConfiguration()
	conf.ValidationMode = pdfmodel.ValidationRelaxed
	var contents []string
	err := pdfapi.ExtractContent(bytes.NewReader(data), nil, func(rd io.Reader, _ int) error {
		b, e := io.ReadAll(rd)
		if e != nil {
			return e
		}
		contents = append(contents, string(b))
		return nil
	}, conf)
	if err != nil {
		return "", fmt.Errorf("PDF 解析失败: %w", err)
	}
	text := extractTextFromPDFContent(contents)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("PDF 未提取到文本(可能为扫描件/图片型 PDF)")
	}
	return text, nil
}

var (
	reTJPDF      = regexp.MustCompile(`\[([^\]]*)\]\s*TJ`)
	reParenPDF   = regexp.MustCompile(`\(((?:\\.|[^()\\])*)\)`)
	reParenTjPDF = regexp.MustCompile(`\(((?:\\.|[^()\\])*)\)\s*Tj`)
	reHexTjPDF   = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*Tj`)
	reHexTJPDF   = regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
)

// extractTextFromPDFContent 从 pdfcpu 抽取的内容流中解析可读文本:
// 支持 (str) Tj、[(str) num ...] TJ 字距数组、<hex> 十六进制串及其组合。
func extractTextFromPDFContent(contents []string) string {
	var out strings.Builder
	for _, c := range contents {
		// 1) TJ 字距数组: 抽取其中所有 (str) 与 <hex>, 忽略数字间距
		for _, m := range reTJPDF.FindAllStringSubmatch(c, -1) {
			for _, p := range reParenPDF.FindAllStringSubmatch(m[1], -1) {
				out.WriteString(unescapePDFString(p[1]))
			}
			for _, h := range reHexTJPDF.FindAllStringSubmatch(m[1], -1) {
				out.WriteString(decodePDFHex(h[1]))
			}
			out.WriteString("\n")
		}
		// 2) 独立 (str) Tj
		for _, m := range reParenTjPDF.FindAllStringSubmatch(c, -1) {
			out.WriteString(unescapePDFString(m[1]))
			out.WriteString("\n")
		}
		// 3) 独立 <hex> Tj
		for _, m := range reHexTjPDF.FindAllStringSubmatch(c, -1) {
			out.WriteString(decodePDFHex(m[1]))
			out.WriteString("\n")
		}
	}
	return strings.TrimSpace(out.String())
}

func unescapePDFString(s string) string {
	return strings.NewReplacer(
		`\(`, "(", `\)`, ")", `\\`, "\\",
		`\n`, "\n", `\r`, "\r", `\t`, "\t",
	).Replace(s)
}

func decodePDFHex(s string) string {
	if len(s)%2 != 0 {
		s += "0"
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// —— DOCX ——

// ExtractDocxText 零依赖: docx 是 zip 包, 读取 word/document.xml 抽取 <w:t> 文本。
func ExtractDocxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("docx 解压失败(非有效 .docx): %w", err)
	}
	f := zipFileNamed(zr, "word/document.xml")
	if f == nil {
		return "", fmt.Errorf("docx 内未找到 word/document.xml")
	}
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return extractDocxTextFromXML(raw), nil
}

// extractDocxTextFromXML 流式解析 document.xml: 收集 <w:t> 文本, 段落(<w:p>)间换行。
func extractDocxTextFromXML(raw []byte) string {
	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var inText, inPara bool
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "t":
				inText = true
			case "p":
				inPara = true
			}
		case xml.EndElement:
			switch el.Name.Local {
			case "t":
				inText = false
			case "p":
				if inPara {
					sb.WriteString("\n")
				}
				inPara = false
			}
		case xml.CharData:
			if inText {
				sb.Write(el)
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// —— XLSX ——

// ExtractXlsxText 零依赖抽取 xlsx: 解析 xl/sharedStrings.xml + xl/worksheets/sheet*.xml,
// 每张表输出成 Markdown 表格(首行作表头), 便于切块后保持语义。
func ExtractXlsxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("xlsx 解压失败(非有效 .xlsx): %w", err)
	}
	shared := parseSharedStrings(zr)
	names := map[string]string{}
	if f := zipFileNamed(zr, "xl/workbook.xml"); f != nil {
		if b := readZipFile(f); len(b) > 0 {
			// <sheet name="Sheet1" sheetId="1" r:id="rId1"/>
			reSheet := regexp.MustCompile(`<sheet[^>]*name="([^"]*)"[^>]*>`)
			for i, m := range reSheet.FindAllStringSubmatch(string(b), -1) {
				names["xl/worksheets/sheet"+strconv.Itoa(i+1)+".xml"] = XMLUnescape(m[1])
			}
		}
	}
	var sheetNames []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetNames = append(sheetNames, f.Name)
		}
	}
	sort.Slice(sheetNames, func(i, j int) bool { return numInName(sheetNames[i]) < numInName(sheetNames[j]) })
	if len(sheetNames) == 0 {
		return "", fmt.Errorf("xlsx 内未找到工作表")
	}
	var sb strings.Builder
	for _, sn := range sheetNames {
		f := zipFileNamed(zr, sn)
		if f == nil {
			continue
		}
		rows := parseSheetRows(readZipFile(f), shared)
		if len(rows) == 0 {
			continue
		}
		title := names[sn]
		if title == "" {
			title = sn
		}
		sb.WriteString("## " + title + "\n")
		for i, row := range rows {
			sb.WriteString("| " + strings.Join(row, " | ") + " |\n")
			if i == 0 {
				sb.WriteString("|" + strings.Repeat(" --- |", len(row)) + "\n")
			}
		}
		sb.WriteString("\n")
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", fmt.Errorf("xlsx 未提取到文本")
	}
	return out, nil
}

// numInName 抽取名字末尾的序号(sheet1.xml → 1, slide2.xml → 2), 用于稳定排序。
func numInName(name string) int {
	m := regexp.MustCompile(`(\d+)\.xml$`).FindStringSubmatch(name)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// parseSharedStrings 解析 xl/sharedStrings.xml → 索引到字符串的映射。
func parseSharedStrings(zr *zip.Reader) []string {
	f := zipFileNamed(zr, "xl/sharedStrings.xml")
	if f == nil {
		return nil
	}
	raw := readZipFile(f)
	var out []string
	for _, m := range regexp.MustCompile(`(?s)<si>(.*?)</si>`).FindAllStringSubmatch(string(raw), -1) {
		var s strings.Builder
		for _, t := range regexp.MustCompile(`(?s)<t[^>]*>(.*?)</t>`).FindAllStringSubmatch(m[1], -1) {
			s.WriteString(XMLUnescape(t[1]))
		}
		out = append(out, s.String())
	}
	return out
}

var (
	reSheetRow = regexp.MustCompile(`(?s)<row[^>]*>(.*?)</row>`)
	reSheetC   = regexp.MustCompile(`(?s)<c\b([^>]*?)(?:/>|>(.*?)</c>)`)
	reCellV    = regexp.MustCompile(`(?s)<v>(.*?)</v>`)
	reCellIs   = regexp.MustCompile(`(?s)<is>(.*?)</is>`)
	reAnyT     = regexp.MustCompile(`(?s)<t[^>]*>(.*?)</t>`)
)

// parseSheetRows 解析单个工作表的行(每行为单元格字符串切片)。
func parseSheetRows(raw []byte, shared []string) [][]string {
	var rows [][]string
	for _, rm := range reSheetRow.FindAllStringSubmatch(string(raw), -1) {
		var row []string
		for _, cm := range reSheetC.FindAllStringSubmatch(rm[1], -1) {
			attr, inner := cm[1], cm[2]
			val := ""
			switch {
			case strings.Contains(attr, `t="s"`): // 共享字符串
				if m := reCellV.FindStringSubmatch(inner); len(m) > 1 {
					if idx, err := strconv.Atoi(strings.TrimSpace(m[1])); err == nil && idx >= 0 && idx < len(shared) {
						val = shared[idx]
					}
				}
			case strings.Contains(attr, `t="inlineStr"`):
				if m := reCellIs.FindStringSubmatch(inner); len(m) > 1 {
					for _, t := range reAnyT.FindAllStringSubmatch(m[1], -1) {
						val += XMLUnescape(t[1])
					}
				}
			default: // 数字/布尔
				if m := reCellV.FindStringSubmatch(inner); len(m) > 1 {
					val = strings.TrimSpace(XMLUnescape(m[1]))
				}
			}
			// 表格内换行会破坏 Markdown 表结构, 压成空格; 竖线需转义。
			val = strings.ReplaceAll(val, "\n", " ")
			val = strings.ReplaceAll(val, "|", "\\|")
			row = append(row, strings.TrimSpace(val))
		}
		for len(row) > 0 && row[len(row)-1] == "" {
			row = row[:len(row)-1]
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	return rows
}

// —— PPTX ——

// ExtractPptxText 零依赖抽取 pptx: 读取 ppt/slides/slideN.xml, 收集 <a:t> 文本。
func ExtractPptxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("pptx 解压失败(非有效 .pptx): %w", err)
	}
	var slides []string
	reSlide := regexp.MustCompile(`^ppt/slides/slide\d+\.xml$`)
	for _, f := range zr.File {
		if reSlide.MatchString(f.Name) {
			slides = append(slides, f.Name)
		}
	}
	sort.Slice(slides, func(i, j int) bool { return numInName(slides[i]) < numInName(slides[j]) })
	if len(slides) == 0 {
		return "", fmt.Errorf("pptx 内未找到幻灯片")
	}
	var sb strings.Builder
	for i, sn := range slides {
		f := zipFileNamed(zr, sn)
		if f == nil {
			continue
		}
		var texts []string
		for _, m := range regexp.MustCompile(`(?s)<a:t>(.*?)</a:t>`).FindAllStringSubmatch(string(readZipFile(f)), -1) {
			if t := strings.TrimSpace(XMLUnescape(m[1])); t != "" {
				texts = append(texts, t)
			}
		}
		if len(texts) == 0 {
			continue
		}
		sb.WriteString("## 第 " + strconv.Itoa(i+1) + " 页\n")
		for _, t := range texts {
			sb.WriteString("- " + t + "\n")
		}
		sb.WriteString("\n")
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", fmt.Errorf("pptx 未提取到文本")
	}
	return out, nil
}

// —— HTML / XML ——

// 注意: Go 的 RE2 不支持反向引用(\1), 故 script/style 各自一条。
var (
	reHTMLScript = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reHTMLStyle  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reHTMLTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	reHTMLBreak  = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/tr|/h[1-6])\s*/?>`)
	reHTMLSpace  = regexp.MustCompile(`[ \t\x{00a0}]+`) // \x{...} 而非 \uXXXX: Go RE2 不支持后者
	reHTMLLine   = regexp.MustCompile(`\n{3,}`)
)

// ExtractHTMLText 把 HTML/XML 转成可读纯文本: 去 script/style → 块级标签换行 → 去标签。
func ExtractHTMLText(s string) string {
	s = reHTMLScript.ReplaceAllString(s, "")
	s = reHTMLStyle.ReplaceAllString(s, "")
	s = reHTMLBreak.ReplaceAllString(s, "\n")
	s = reHTMLTag.ReplaceAllString(s, "")
	s = XMLUnescape(s)
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(reHTMLSpace.ReplaceAllString(ln, " "))
		if ln != "" {
			out = append(out, ln)
		}
	}
	return strings.TrimSpace(reHTMLLine.ReplaceAllString(strings.Join(out, "\n"), "\n\n"))
}

// XMLUnescape 还原常见 XML/HTML 实体(注意 &amp; 必须最后替换, 否则会二次解码)。
func XMLUnescape(s string) string {
	return strings.NewReplacer(
		"&lt;", "<", "&gt;", ">", "&quot;", "\"", "&apos;", "'", "&#39;", "'",
		"&nbsp;", " ", "&amp;", "&",
	).Replace(s)
}

func zipFileNamed(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func readZipFile(f *zip.File) []byte {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return b
}
