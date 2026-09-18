// AI 文档生成工具 (pdf_generate)。
//
// 让对话模型按需生成真实可下载的 .pdf 文档(支持中文), 闭环「用户让 AI 写 PDF/报告」诉求。
// 与 pptx_generate / docx_generate / xlsx_generate 同构: 生成字节 → 对象存储(MinIO) → 预签名 URL。
//
// 生成方式: 纯 Go 手写最小合法 PDF。为支持中文, 运行时探测并内嵌一个 CJK TrueType 字体
// (CIDFontType2 + Identity-H 编码 + ToUnicode CMap), 字体字节经 Flate 压缩后作为 /FontFile2 流嵌入。
// 未探测到 CJK 字体时降级为内置 Helvetica(仅 ASCII, 非 ASCII 字符降级为 '?'), 保证仍可产出基本 PDF。
//
// 说明: 当前为"整字体嵌入"实现(健壮、零字体改写风险), 单文件体积约等于字体本身(~23MB)。
// 后续若需控制体积可做 TrueType 子集化(subset)——属可优化项, 不影响正确性。
package agentic

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// PDF 内容模型
// ---------------------------------------------------------------------------

// pdfBlock 文档的一个内容块。
type pdfBlock struct {
	Type  string // heading | paragraph | bullet | pagebreak
	Level int    // heading 级别 (1-3), 其余忽略
	Text  string
}

// pdfLine 排版后的一行(已落入某页)。
type pdfLine struct {
	text   string
	size   float64
	indent float64
	bold   bool
}

// pdfFont 解析后的 CJK 字体信息。
type pdfFont struct {
	raw       []byte          // 完整字体文件字节(用于嵌入)
	cmap      map[rune]uint16 // rune -> glyph index
	advance   map[uint16]int  // glyph index -> advance width (font units)
	units     int             // unitsPerEm
	ascender  int
	descender int
	bbox      [4]int // xMin,yMin,xMax,yMax
	subtype   string // 恒为 "CIDFontType2"
}

var (
	cjkFontOnce sync.Once
	cjkFont     *pdfFont
	cjkFontErr  error
)

// loadCJKFont 探测并解析一个 CJK TrueType 字体(单文件, 非 ttc 集合优先)。结果缓存。
func loadCJKFont() (*pdfFont, error) {
	cjkFontOnce.Do(func() {
		cjkFont, cjkFontErr = findAndParseCJKFont()
	})
	return cjkFont, cjkFontErr
}

// cjkFontCandidates 候选字体路径(环境变量可覆盖首项)。优先单个 .ttf 文件。
func cjkFontCandidates() []string {
	// 环境变量只是"优先项"而非"唯一项": 配错路径时仍能回退到默认探测, 不会彻底丢失中文。
	if p := os.Getenv("OPSORIGIN_PDF_FONT"); p != "" {
		return append([]string{p}, defaultCJKCandidates()...)
	}
	return defaultCJKCandidates()
}

// defaultCJKCandidates 默认候选(精确路径 + 目录扫描兜底)。
func defaultCJKCandidates() []string {
	return append([]string{
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		"/System/Library/Fonts/Supplemental/Songti.ttc",
		"/System/Library/Fonts/STHeiti Light.ttc",
		// Alpine(部署镜像, font-noto-cjk)
		"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/noto-cjk/NotoSansCJKsc-Regular.otf",
		// Debian/Ubuntu
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
		"/usr/share/fonts/truetype/arphic/uming.ttc",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", // 无中文, 仅作 ASCII 兜底
	}, scanFontDirs()...)
}

// scanFontDirs 兜底: 发行版字体目录布局各异(noto / noto-cjk / truetype/...),
// 无法穷举路径时递归扫描常见字体目录, 收集名字含 CJK/中文字体关键词的文件。
func scanFontDirs() []string {
	roots := []string{"/usr/share/fonts", "/usr/local/share/fonts", "/Library/Fonts", "/System/Library/Fonts"}
	var out []string
	seen := map[string]bool{}
	for _, root := range roots {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			low := strings.ToLower(info.Name())
			if !strings.HasSuffix(low, ".ttf") && !strings.HasSuffix(low, ".ttc") && !strings.HasSuffix(low, ".otf") {
				return nil
			}
			if !(strings.Contains(low, "cjk") || strings.Contains(low, "noto") ||
				strings.Contains(low, "hei") || strings.Contains(low, "song") ||
				strings.Contains(low, "ming") || strings.Contains(low, "wqy") ||
				strings.Contains(low, "droidsansfallback")) {
				return nil
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

func findAndParseCJKFont() (*pdfFont, error) {
	for _, p := range cjkFontCandidates() {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		sub, err := subFontSlice(raw)
		if err != nil {
			slog.Warn("pdf_generate: 字体容器解析失败, 跳过", "path", p, "err", err)
			continue
		}
		f, err := parseTrueType(sub)
		if err != nil {
			slog.Warn("pdf_generate: 解析字体失败, 跳过", "path", p, "err", err)
			continue
		}
		// 必须真正覆盖 CJK: 否则(如匹配到无中文的 Noto/DejaVu)会静默渲染成空白方块。
		if !coversCJK(f) {
			slog.Warn("pdf_generate: 字体不含中文字形, 跳过", "path", p)
			continue
		}
		f.raw = sub // 仅内嵌所用到的那个字体面(ttc 取首个 face), 而非整包
		slog.Info("pdf_generate: 已加载 CJK 字体", "path", p, "units", f.units, "glyphs", len(f.advance))
		return f, nil
	}
	return nil, fmt.Errorf("未找到可用的 CJK 字体(可设置 OPSORIGIN_PDF_FONT 指定 .ttf 路径)")
}

// coversCJK 用少量代表性汉字/中文标点探测字体是否覆盖 CJK(命中任一即认为可用)。
// 避免"解析成功但无中文"的字体被选中导致 PDF 中文变空白。
func coversCJK(f *pdfFont) bool {
	for _, r := range []rune{'中', '文', '运', '维', '报', '告', '，'} {
		if _, ok := f.cmap[r]; ok {
			return true
		}
	}
	return false
}

// subFontSlice 从字体数据取出第一个可用字体面:
//   - 单文件 TTF/OTF(TrueType 轮廓): 返回整段;
//   - 字体集合(.ttc, 标签 'ttcf'): 取首个字体的偏移切片(其内部表偏移相对自身起点, 是合法独立 TTF)。
func subFontSlice(raw []byte) ([]byte, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("字体数据过短")
	}
	if binary.BigEndian.Uint32(raw[0:4]) == 0x74746366 { // 'ttcf'
		if len(raw) < 16 {
			return nil, fmt.Errorf("ttc 头部过短")
		}
		num := binary.BigEndian.Uint32(raw[8:12])
		if num == 0 {
			return nil, fmt.Errorf("ttc 无字体面")
		}
		off := int(binary.BigEndian.Uint32(raw[12:16]))
		if off <= 0 || off >= len(raw) {
			return nil, fmt.Errorf("ttc 首字体偏移越界: %d", off)
		}
		return raw[off:], nil
	}
	return raw, nil
}

// parseTrueType 解析 TrueType 字体(单文件), 抽取 cmap/hmtx/head/hhea/maxp 必要信息。
// 不重写字体二进制, 仅用于内嵌与排版测量。
func parseTrueType(raw []byte) (*pdfFont, error) {
	if len(raw) < 12 {
		return nil, fmt.Errorf("字体数据过短")
	}
	r := bytes.NewReader(raw)
	var sfnt uint32
	if err := binary.Read(r, binary.BigEndian, &sfnt); err != nil {
		return nil, err
	}
	// 0x00010000 = TrueType; 0x4F54544F('OTTO') = CFF 轮廓(暂不支持, 跳过)
	if sfnt != 0x00010000 {
		return nil, fmt.Errorf("非 TrueType(轮廓)字体 (sfnt=0x%X)", sfnt)
	}
	numTables := binary.BigEndian.Uint16(raw[4:6])
	tables := make(map[string][4]int) // tag -> {offset, length}
	off := 12
	for i := 0; i < int(numTables); i++ {
		if off+16 > len(raw) {
			break
		}
		tag := string(raw[off : off+4])
		toff := int(binary.BigEndian.Uint32(raw[off+8 : off+12]))
		tlen := int(binary.BigEndian.Uint32(raw[off+12 : off+16]))
		tables[tag] = [4]int{toff, tlen, 0, 0}
		off += 16
	}
	need := []string{"cmap", "head", "hhea", "maxp", "hmtx"}
	for _, t := range need {
		if _, ok := tables[t]; !ok {
			return nil, fmt.Errorf("字体缺少必要表 %s", t)
		}
	}

	// head: unitsPerEm @18, fontBBox @36..42
	head := raw[tables["head"][0] : tables["head"][0]+tables["head"][1]]
	units := int(binary.BigEndian.Uint16(head[18:20]))
	bbox := [4]int{
		int(int16(binary.BigEndian.Uint16(head[36:38]))),
		int(int16(binary.BigEndian.Uint16(head[38:40]))),
		int(int16(binary.BigEndian.Uint16(head[40:42]))),
		int(int16(binary.BigEndian.Uint16(head[42:44]))),
	}

	// hhea: ascender @4, descender @6, numberOfHMetrics @34
	hhea := raw[tables["hhea"][0] : tables["hhea"][0]+tables["hhea"][1]]
	ascender := int(int16(binary.BigEndian.Uint16(hhea[4:6])))
	descender := int(int16(binary.BigEndian.Uint16(hhea[6:8])))
	numHMetrics := int(binary.BigEndian.Uint16(hhea[34:36]))

	// maxp: numGlyphs @4
	maxp := raw[tables["maxp"][0] : tables["maxp"][0]+tables["maxp"][1]]
	numGlyphs := int(binary.BigEndian.Uint16(maxp[4:6]))

	// hmtx: numberOfHMetrics 个 (advance uint16, lsb int16)
	hmtx := raw[tables["hmtx"][0] : tables["hmtx"][0]+tables["hmtx"][1]]
	advance := make(map[uint16]int, numGlyphs)
	var lastAdv int
	for i := 0; i < numHMetrics; i++ {
		o := i * 4
		if o+2 > len(hmtx) {
			break
		}
		a := int(binary.BigEndian.Uint16(hmtx[o : o+2]))
		advance[uint16(i)] = a
		lastAdv = a
	}
	for i := numHMetrics; i < numGlyphs; i++ {
		advance[uint16(i)] = lastAdv
	}

	// cmap: 选择 platform 0/3 的 format 4 或 12。
	cm, err := parseCmap(raw, tables["cmap"])
	if err != nil {
		return nil, err
	}

	return &pdfFont{
		cmap:     cm,
		advance:  advance,
		units:    units,
		ascender: ascender, descender: descender,
		bbox: bbox,
	}, nil
}

func parseCmap(raw []byte, t [4]int) (map[rune]uint16, error) {
	base := t[0]
	numSub := int(binary.BigEndian.Uint16(raw[base+2 : base+4]))
	type sub struct {
		plat, enc, off int
		fmt            int
	}
	subs := make([]sub, 0, numSub)
	for i := 0; i < numSub; i++ {
		o := base + 4 + i*8
		if o+8 > len(raw) {
			break
		}
		plat := int(binary.BigEndian.Uint16(raw[o : o+2]))
		enc := int(binary.BigEndian.Uint16(raw[o+2 : o+4]))
		off := int(binary.BigEndian.Uint32(raw[o+4 : o+8]))
		fo := base + off
		if fo+2 > len(raw) {
			continue
		}
		fmtN := int(binary.BigEndian.Uint16(raw[fo : fo+2]))
		subs = append(subs, sub{plat, enc, fo, fmtN})
	}
	// 优先 platform 0 或 3, format 12 > 4。
	var chosen *sub
	for i := range subs {
		s := &subs[i]
		if (s.plat == 0 || s.plat == 3) && s.fmt == 12 {
			chosen = s
			break
		}
	}
	if chosen == nil {
		for i := range subs {
			s := &subs[i]
			if (s.plat == 0 || s.plat == 3) && s.fmt == 4 {
				chosen = s
				break
			}
		}
	}
	if chosen == nil {
		for i := range subs {
			if subs[i].fmt == 4 || subs[i].fmt == 12 {
				chosen = &subs[i]
				break
			}
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("cmap 无可用子表")
	}
	if chosen.fmt == 4 {
		return parseCmapFormat4(raw, chosen.off)
	}
	return parseCmapFormat12(raw, chosen.off)
}

func parseCmapFormat4(raw []byte, off int) (map[rune]uint16, error) {
	segX2 := int(binary.BigEndian.Uint16(raw[off+6 : off+8]))
	segCount := segX2 / 2
	endP := off + 14
	startP := endP + segX2 + 2 // +reservedPad
	deltaP := startP + segX2
	roP := deltaP + segX2
	glyphP := roP + segX2

	cm := make(map[rune]uint16)
	for i := 0; i < segCount; i++ {
		endCode := binary.BigEndian.Uint16(raw[endP+i*2 : endP+i*2+2])
		startCode := binary.BigEndian.Uint16(raw[startP+i*2 : startP+i*2+2])
		delta := int(int16(binary.BigEndian.Uint16(raw[deltaP+i*2 : deltaP+i*2+2])))
		ro := int(binary.BigEndian.Uint16(raw[roP+i*2 : roP+i*2+2]))
		if startCode == 0xFFFF && endCode == 0xFFFF {
			continue
		}
		for c := int(startCode); c <= int(endCode); c++ {
			var gid uint16
			if ro == 0 {
				gid = uint16((c + delta) & 0xFFFF)
			} else {
				idx := ro/2 + (c - int(startCode)) - segCount + i
				oo := glyphP + idx*2
				if oo+2 > len(raw) {
					continue
				}
				g := binary.BigEndian.Uint16(raw[oo : oo+2])
				if g == 0 {
					gid = 0
				} else {
					gid = uint16((int(g) + delta) & 0xFFFF)
				}
			}
			cm[rune(c)] = gid
		}
	}
	return cm, nil
}

func parseCmapFormat12(raw []byte, off int) (map[rune]uint16, error) {
	nGroups := int(binary.BigEndian.Uint32(raw[off+12 : off+16]))
	gp := off + 16
	cm := make(map[rune]uint16)
	for i := 0; i < nGroups; i++ {
		o := gp + i*12
		if o+12 > len(raw) {
			break
		}
		start := binary.BigEndian.Uint32(raw[o : o+4])
		end := binary.BigEndian.Uint32(raw[o+4 : o+8])
		sgid := binary.BigEndian.Uint32(raw[o+8 : o+12])
		for c := int(start); c <= int(end); c++ {
			cm[rune(c)] = uint16(sgid + (uint32(c) - start))
		}
	}
	return cm, nil
}

// glyphWidthPt 返回某 rune 在给定字号下的宽度(pt)。
func (f *pdfFont) glyphWidthPt(r rune, fontSize float64) float64 {
	gid, ok := f.cmap[r]
	if !ok {
		gid = 0
	}
	w, ok := f.advance[gid]
	if !ok {
		w = f.units / 2
	}
	return float64(w) / float64(f.units) * fontSize
}

// ---------------------------------------------------------------------------
// 生成器
// ---------------------------------------------------------------------------

// pdfGenerate 实现 MCP 工具 pdf_generate。
func (ts *MCPToolSet) pdfGenerate(ctx context.Context, args map[string]any) (any, error) {
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	fileName, _ := args["file_name"].(string)
	fileName = strings.TrimSpace(fileName)

	var blocks []pdfBlock

	// 优先解析结构化 blocks: [{type, level?, text}]
	if raw, ok := args["blocks"].([]any); ok {
		for _, it := range raw {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			b := pdfBlock{}
			if tp, ok := m["type"].(string); ok {
				b.Type = strings.ToLower(strings.TrimSpace(tp))
			}
			if lv, ok := m["level"].(float64); ok {
				b.Level = int(lv)
			}
			if tx, ok := m["text"].(string); ok {
				b.Text = tx
			}
			if b.Type == "" {
				b.Type = "paragraph"
			}
			if b.Type == "pagebreak" || b.Text != "" || b.Type != "paragraph" {
				blocks = append(blocks, b)
			}
		}
	}

	// 退路: 单段纯文本 content (按 \n 拆段落)。
	if len(blocks) == 0 {
		if content, ok := args["content"].(string); ok {
			for _, line := range strings.Split(content, "\n") {
				blocks = append(blocks, pdfBlock{Type: "paragraph", Text: line})
			}
		}
	}

	if title == "" && len(blocks) == 0 {
		return nil, fmt.Errorf("pdf_generate: 至少需要 title 或 blocks/content")
	}

	if title != "" {
		blocks = append([]pdfBlock{{Type: "heading", Level: 1, Text: title}}, blocks...)
	}

	data, pageCount, err := generatePDF(blocks)
	if err != nil {
		return nil, fmt.Errorf("pdf_generate: %w", err)
	}

	if fileName == "" {
		fileName = "document-" + time.Now().Format("20060102-150405") + ".pdf"
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
		fileName += ".pdf"
	}

	key := "ai-pdf/" + uuid.NewString() + ".pdf"
	ct := "application/pdf"
	if ts.objStore == nil {
		return nil, fmt.Errorf("pdf_generate: 未配置对象存储(MinIO), 无法生成可下载的 PDF")
	}
	if err := ts.objStore.Put(ctx, key, bytes.NewReader(data), int64(len(data)), ct); err != nil {
		return nil, fmt.Errorf("pdf_generate: 对象存储写入失败: %w", err)
	}
	u, err := ts.objStore.PresignedURL(key, 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("pdf_generate: 预签名失败: %w", err)
	}

	slog.Info("pdf_generate ok", "title", title, "pages", pageCount, "file", fileName, "bytes", len(data))
	return map[string]any{
		"url":        u.String(),
		"file_name":  fileName,
		"page_count": pageCount,
		"title":      title,
	}, nil
}

// generatePDF 拼最小合法 PDF(内嵌 CJK 字体以支持中文), 返回字节与页数。
func generatePDF(blocks []pdfBlock) ([]byte, int, error) {
	font, ferr := loadCJKFont()
	useEmbed := ferr == nil && font != nil

	// 排版参数(A4: 595x842 pt)。
	const (
		pageW   = 595
		pageH   = 842
		marginX = 50
		marginT = 56
		marginB = 56
	)
	maxLineW := float64(pageW - 2*marginX)

	// 逐块逐行排版, 得到 (page -> lines).
	pages := [][]pdfLine{{}}
	y := float64(marginT)

	pushLine := func(ln pdfLine) {
		pages[len(pages)-1] = append(pages[len(pages)-1], ln)
	}
	newPage := func() {
		pages = append(pages, []pdfLine{})
		y = marginT
	}

	lineH := func(size float64) float64 { return size * 1.45 }
	need := func(size float64) {
		if y+lineH(size) > float64(pageH-marginB) {
			newPage()
		}
	}

	measure := func(s string, size float64) float64 {
		if useEmbed {
			var w float64
			for _, r := range s {
				w += font.glyphWidthPt(r, size)
			}
			return w
		}
		return float64(len([]rune(s))) * size * 0.5 // Helvetica 近似
	}

	for _, b := range blocks {
		switch b.Type {
		case "pagebreak":
			if len(pages[len(pages)-1]) > 0 {
				newPage()
			}
			continue
		case "heading":
			size := 16.0
			if b.Level == 2 {
				size = 14
			} else if b.Level >= 3 {
				size = 12.5
			} else {
				size = 18 // h1
			}
			need(size)
			for _, ln := range wrapText(b.Text, size, maxLineW, measure) {
				need(size)
				pushLine(pdfLine{text: ln, size: size, indent: 0, bold: true})
				y += lineH(size)
			}
			y += size * 0.3 // 段后距
		case "bullet":
			size := 11.0
			need(size)
			// 先排一个占位前缀, 再 wrap 正文并缩进。
			prefix := "•  "
			bodyW := maxLineW - measure(prefix, size)
			wrapped := wrapText(b.Text, size, bodyW, measure)
			for i, ln := range wrapped {
				need(size)
				txt := ln
				indent := 0.0
				if i == 0 {
					txt = prefix + ln
				} else {
					indent = measure(prefix, size)
				}
				pushLine(pdfLine{text: txt, size: size, indent: indent, bold: false})
				y += lineH(size)
			}
			y += size * 0.2
		default: // paragraph
			size := 11.0
			need(size)
			wrapped := wrapText(b.Text, size, maxLineW, measure)
			if len(wrapped) == 0 {
				wrapped = []string{""}
			}
			for _, ln := range wrapped {
				need(size)
				pushLine(pdfLine{text: ln, size: size, indent: 0, bold: false})
				y += lineH(size)
			}
			y += size * 0.35
		}
	}

	pageCount := len(pages)
	if pageCount == 0 {
		pageCount = 1
		pages = [][]pdfLine{{}}
	}

	// 收集实际用到的 glyph, 构建 /W 与 ToUnicode。
	usedGids := map[uint16]struct{}{0: {}}
	for _, pl := range pages {
		for _, ln := range pl {
			for _, r := range ln.text {
				if useEmbed {
					if g, ok := font.cmap[r]; ok {
						usedGids[g] = struct{}{}
					}
				}
			}
		}
	}

	// ---- 构建各 PDF 对象 ----
	var buf bytes.Buffer
	// PDF 文件头(二进制标记行提示传输链路按二进制处理)。
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	objOffsets := []int{}
	writeObj := func(body string) int {
		objOffsets = append(objOffsets, buf.Len())
		id := len(objOffsets)
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
		return id
	}

	// Catalog(1)
	catID := writeObj("<< /Type /Catalog /Pages 2 0 R >>")
	_ = catID

	// Pages(2) 引用稍后确定的 page 对象。先占位, 稍后补写 Kids。
	pagesObjIdx := len(objOffsets) // = index of obj 2
	_ = pagesObjIdx
	objOffsets = append(objOffsets, buf.Len())
	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /Pages /Kids [")
	pageIDs := make([]int, pageCount)
	contentIDs := make([]int, pageCount)
	// writeObj 调用顺序(在 catalog=1, pages=2, 各 page=3..2+pageCount 之后):
	//   CIDFontType2(3+pageCount) → FontDescriptor(4+pageCount) → FontFile2(5+pageCount)
	//   → ToUnicode(6+pageCount) → Type0(7+pageCount) → Contents(8+pageCount..)。
	fontType0ID := 7 + pageCount // Type0, 被 Page /Font /F1 引用
	cidFontID := 3 + pageCount   // CIDFontType2, 被 Type0 /DescendantFonts 引用
	fdID := 4 + pageCount        // FontDescriptor, 被 CIDFontType2 /FontDescriptor 引用
	contentStartID := 8 + pageCount

	for p := 0; p < pageCount; p++ {
		pid := 3 + p
		pageIDs[p] = pid
		contentIDs[p] = contentStartID + p
		if p > 0 {
			buf.WriteString(" ")
		}
		fmt.Fprintf(&buf, "%d 0 R", pid)
	}
	fmt.Fprintf(&buf, "] /Count %d >>\nendobj\n", pageCount)

	// Page 对象 (3 .. 2+pageCount)
	for p := 0; p < pageCount; p++ {
		body := fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			pageW, pageH, fontType0ID, contentIDs[p])
		writeObj(body)
	}

	// 字体对象。
	var fontBody string
	var usedGidList []uint16
	if useEmbed {
		for g := range usedGids {
			usedGidList = append(usedGidList, g)
		}
		sortUint16(usedGidList)
		wArray := buildWidthArray(font, usedGidList)
		// 压缩字体字节。
		var comp bytes.Buffer
		zw := zlib.NewWriter(&comp)
		if _, err := zw.Write(font.raw); err != nil {
			return nil, 0, fmt.Errorf("字体压缩失败: %w", err)
		}
		zw.Close()
		fontStream := comp.Bytes()

		toUni := buildToUnicode(font, usedGidList)

		// CIDFontType2
		writeObj(fmt.Sprintf(
			"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /OpsOriginCJK "+
				"/CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> "+
				"/FontDescriptor %d 0 R /DW %d /W [%s] >>",
			fdID, font.units/2, wArray))
		// FontDescriptor (含 FontFile2 二进制流)
		fdBody := fmt.Sprintf(
			"<< /Type /FontDescriptor /FontName /OpsOriginCJK /Flags 4 "+
				"/FontBBox [%d %d %d %d] /ItalicAngle 0 /Ascent %d /Descent %d /CapHeight %d /StemV 80 "+
				"/FontFile2 %d 0 R >>",
			font.bbox[0], font.bbox[1], font.bbox[2], font.bbox[3],
			font.ascender, font.descender, font.ascender,
			fdID+1) // FontFile2 流对象 ID = fdID+1
		writeObj(fdBody)
		// FontFile2 流对象。
		writeObj(fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n", len(fontStream)) +
			string(fontStream) + "\nendstream")
		// ToUnicode CMap 流对象(供文本提取/复制/搜索; 不引用也不影响显示)。
		toUniID := writeObj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(toUni), toUni))

		fontBody = fmt.Sprintf(
			"<< /Type /Font /Subtype /Type0 /BaseFont /OpsOriginCJK /Encoding /Identity-H "+
				"/DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", cidFontID, toUniID)
	} else {
		fontBody = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	}

	writeObj(fontBody) // Type0 或 Type1

	// Content 流对象。
	for p := 0; p < pageCount; p++ {
		stream := buildContentStream(pages[p], marginX, pageH, useEmbed, font)
		writeObj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}

	// ---- xref / trailer ----
	xrefOff := buf.Len()
	total := len(objOffsets) + 1 // 含对象 0
	fmt.Fprintf(&buf, "xref\n0 %d\n", total)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range objOffsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", total, xrefOff)

	return buf.Bytes(), pageCount, nil
}

// wrapText 按行宽折行: CJK 逐字断, 拉丁按空格断。
func wrapText(s string, size, maxW float64, measure func(string, float64) float64) []string {
	s = strings.TrimRight(s, " \t")
	if s == "" {
		return []string{""}
	}
	var lines []string
	var cur string
	flush := func() {
		lines = append(lines, cur)
		cur = ""
	}
	// 先按显式换行拆。
	for _, para := range strings.Split(s, "\n") {
		cur = ""
		words := strings.Fields(para)
		if len(words) == 0 {
			flush()
			continue
		}
		// 重新逐词处理, 支持 CJK 内联。
		for _, w := range words {
			candidate := cur
			if candidate != "" {
				candidate += " "
			}
			candidate += w
			if measure(candidate, size) <= maxW || cur == "" {
				cur = candidate
				continue
			}
			flush()
			cur = w
			// 若单个词仍超宽, 逐字符拆。
			for measure(cur, size) > maxW && len([]rune(cur)) > 1 {
				rs := []rune(cur)
				// 找到可断点。
				cut := len(rs)
				for i := 1; i < len(rs); i++ {
					if measure(string(rs[:i]), size) > maxW {
						cut = i - 1
						break
					}
				}
				if cut < 1 {
					cut = 1
				}
				flush()
				cur = string(rs[cut:])
			}
		}
		flush()
	}
	return lines
}

// buildContentStream 将一页的 lines 渲染为 PDF 内容流。
func buildContentStream(lines []pdfLine, marginX, pageH int, useEmbed bool, font *pdfFont) string {
	const topMargin = 56
	var b strings.Builder
	y := float64(pageH) - float64(topMargin)
	for _, ln := range lines {
		if ln.text == "" {
			y -= ln.size * 1.45
			continue
		}
		x := float64(marginX) + ln.indent
		y -= ln.size * 1.45
		b.WriteString("BT\n")
		fmt.Fprintf(&b, "/F1 %.1f Tf\n", ln.size)
		if ln.bold {
			b.WriteString("0.6 w\n2 Tr\n") // 描边模拟加粗
		} else {
			b.WriteString("0 Tr\n")
		}
		fmt.Fprintf(&b, "1 0 0 1 %.1f %.1f Tm\n", x, y)
		if useEmbed {
			b.WriteString("<")
			for _, r := range ln.text {
				gid := uint16(0)
				if g, ok := font.cmap[r]; ok {
					gid = g
				}
				fmt.Fprintf(&b, "%04X", gid)
			}
			b.WriteString("> Tj\n")
		} else {
			b.WriteString("(")
			b.WriteString(pdfLiteralEscape(ln.text))
			b.WriteString(") Tj\n")
		}
		b.WriteString("ET\n")
	}
	return b.String()
}

func pdfLiteralEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '(':
			b.WriteString("\\(")
		case ')':
			b.WriteString("\\)")
		case '\\':
			b.WriteString("\\\\")
		default:
			if r < 0x20 || r > 0x7E {
				b.WriteString("?") // Helvetica 降级仅 ASCII
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// buildWidthArray 为用到的 glyph 构造 /W 数组(连续段合并)。
func buildWidthArray(font *pdfFont, gids []uint16) string {
	var b strings.Builder
	i := 0
	for i < len(gids) {
		start := gids[i]
		j := i
		run := []int{}
		for j < len(gids) {
			if j > i && gids[j] != gids[j-1]+1 {
				break
			}
			w, ok := font.advance[gids[j]]
			if !ok {
				w = font.units / 2
			}
			run = append(run, w)
			j++
		}
		fmt.Fprintf(&b, "%d [", start)
		for k, w := range run {
			if k > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%d", w)
		}
		b.WriteString("] ")
		i = j
	}
	return b.String()
}

// buildToUnicode 构造 ToUnicode CMap(仅 BMP runes), 每 100 条分块。
func buildToUnicode(font *pdfFont, gids []uint16) string {
	// gid -> rune (取第一个映射到的 rune)。
	gidToRune := make(map[uint16]rune, len(font.cmap))
	for r, g := range font.cmap {
		if _, ok := gidToRune[g]; !ok {
			if r <= 0xFFFF {
				gidToRune[g] = r
			}
		}
	}

	var b strings.Builder
	b.WriteString("/CIDInit /ProcSet findresource begin\n")
	b.WriteString("20 dict begin\nbegincmap\n")
	b.WriteString("/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n")
	b.WriteString("/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n")
	b.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")

	type pair struct {
		gid uint16
		r   rune
	}
	var pairs []pair
	for _, g := range gids {
		if r, ok := gidToRune[g]; ok {
			pairs = append(pairs, pair{g, r})
		}
	}
	// 分块(每块最多 100 条 bfchar)。
	for start := 0; start < len(pairs); start += 100 {
		end := start + 100
		if end > len(pairs) {
			end = len(pairs)
		}
		fmt.Fprintf(&b, "%d beginbfchar\n", end-start)
		for _, p := range pairs[start:end] {
			fmt.Fprintf(&b, "<%04X> <%04X>\n", p.gid, p.r)
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString("endcmap\nCMapName currentdict end definefont pop\n")
	return b.String()
}

func sortUint16(s []uint16) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
