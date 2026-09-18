// AI 文档生成工具 (xlsx_generate)。
//
// 让对话模型按需生成真实可下载的 .xlsx Excel 表格, 闭环「用户让 AI 出表/导出数据」诉求。
// 与 pptx_generate / docx_generate 同构: 纯 Go 手写最小合法 OOXML (ZIP 包), 零外部依赖,
// 不依赖 excelize / python openpyxl。产物可被 Excel / WPS / LibreOffice / Numbers 正常打开。
package agentic

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// xlsxSheet 单个工作表。
type xlsxSheet struct {
	Name    string   `json:"name"`
	Headers []string `json:"headers"`
	Rows    [][]any  `json:"rows"`
}

// xlsxGenerate 实现 MCP 工具 xlsx_generate。
func (ts *MCPToolSet) xlsxGenerate(ctx context.Context, args map[string]any) (any, error) {
	fileName, _ := args["file_name"].(string)
	fileName = strings.TrimSpace(fileName)

	var sheets []xlsxSheet
	if raw, ok := args["sheets"].([]any); ok {
		for _, it := range raw {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			s := xlsxSheet{}
			if n, ok := m["name"].(string); ok {
				s.Name = strings.TrimSpace(n)
			}
			if hs, ok := m["headers"].([]any); ok {
				for _, h := range hs {
					if s2, ok := h.(string); ok {
						s.Headers = append(s.Headers, s2)
					}
				}
			}
			if rs, ok := m["rows"].([]any); ok {
				for _, row := range rs {
					arr, ok := row.([]any)
					if !ok {
						continue
					}
					var cells []any
					for _, c := range arr {
						cells = append(cells, c)
					}
					s.Rows = append(s.Rows, cells)
				}
			}
			if s.Name == "" {
				s.Name = fmt.Sprintf("Sheet%d", len(sheets)+1)
			}
			sheets = append(sheets, s)
		}
	}

	// 便捷入口: 顶层直接传 headers/rows 时构造单 sheet。
	if len(sheets) == 0 {
		top := xlsxSheet{Name: "Sheet1"}
		if hs, ok := args["headers"].([]any); ok {
			for _, h := range hs {
				if s2, ok := h.(string); ok {
					top.Headers = append(top.Headers, s2)
				}
			}
		}
		if rs, ok := args["rows"].([]any); ok {
			for _, row := range rs {
				arr, ok := row.([]any)
				if !ok {
					continue
				}
				var cells []any
				for _, c := range arr {
					cells = append(cells, c)
				}
				top.Rows = append(top.Rows, cells)
			}
		}
		if len(top.Headers) > 0 || len(top.Rows) > 0 {
			sheets = append(sheets, top)
		}
	}

	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx_generate: 至少需要一个 sheet (sheets/headers/rows)")
	}

	data, err := generateXLSX(sheets)
	if err != nil {
		return nil, fmt.Errorf("xlsx_generate: %w", err)
	}

	if fileName == "" {
		fileName = "workbook-" + time.Now().Format("20060102-150405") + ".xlsx"
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".xlsx") {
		fileName += ".xlsx"
	}

	key := "ai-xlsx/" + uuid.NewString() + ".xlsx"
	ct := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	if ts.objStore == nil {
		return nil, fmt.Errorf("xlsx_generate: 未配置对象存储(MinIO), 无法生成可下载的表格")
	}
	if err := ts.objStore.Put(ctx, key, bytes.NewReader(data), int64(len(data)), ct); err != nil {
		return nil, fmt.Errorf("xlsx_generate: 对象存储写入失败: %w", err)
	}
	u, err := ts.objStore.PresignedURL(key, 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("xlsx_generate: 预签名失败: %w", err)
	}

	slog.Info("xlsx_generate ok", "sheets", len(sheets), "file", fileName)
	return map[string]any{
		"url":         u.String(),
		"file_name":   fileName,
		"sheet_count": len(sheets),
	}, nil
}

// generateXLSX 拼最小合法 .xlsx (OOXML ZIP), 返回文件字节。单元格用 inlineStr, 不依赖 sharedStrings。
func generateXLSX(sheets []xlsxSheet) ([]byte, error) {
	if len(sheets) == 0 {
		return nil, fmt.Errorf("empty workbook: need at least one sheet")
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

	// 1. [Content_Types].xml
	var ct strings.Builder
	ct.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	ct.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	ct.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	ct.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	ct.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	for i := 1; i <= len(sheets); i++ {
		fmt.Fprintf(&ct, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i)
	}
	ct.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	ct.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	ct.WriteString(`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`)
	ct.WriteString(`</Types>`)
	add("[Content_Types].xml", ct.String())

	// 2. _rels/.rels
	add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>`+
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>`+
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>`+
		`</Relationships>`)

	// 3. xl/workbook.xml + rels
	var wb strings.Builder
	wb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	wb.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	var wbRels strings.Builder
	wbRels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i, s := range sheets {
		rid := i + 1
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("Sheet%d", i+1)
		}
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(name), i+1, rid)
		fmt.Fprintf(&wbRels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, rid, i+1)
	}
	wb.WriteString(`</sheets></workbook>`)
	stylesRid := len(sheets) + 1
	fmt.Fprintf(&wbRels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, stylesRid)
	wbRels.WriteString(`</Relationships>`)
	add("xl/workbook.xml", wb.String())
	add("xl/_rels/workbook.xml.rels", wbRels.String())

	// 4. worksheets
	for i, s := range sheets {
		idx := i + 1
		var ws strings.Builder
		ws.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		ws.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheetData>`)
		row := 1
		if len(s.Headers) > 0 {
			var hcells []any
			for _, h := range s.Headers {
				hcells = append(hcells, h)
			}
			ws.WriteString(buildXLSXRow(row, hcells, true))
			row++
		}
		for _, r := range s.Rows {
			ws.WriteString(buildXLSXRow(row, r, false))
			row++
		}
		ws.WriteString(`</sheetData></worksheet>`)
		add(fmt.Sprintf("xl/worksheets/sheet%d.xml", idx), ws.String())
	}

	// 5. styles (含 bold 样式给表头)
	add("xl/styles.xml", xlsxStylesXML)

	// 6. docProps
	add("docProps/core.xml", fmt.Sprintf(xlsxCoreXML, now, now))
	add("docProps/app.xml", xlsxAppXML)

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("zip close: %w", err)
	}
	return buf.Bytes(), nil
}

// buildXLSXRow 生成一行 XML; header=true 时该单元格使用 bold 样式 (s="1")。
func buildXLSXRow(row int, cells []any, header bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<row r="%d">`, row)
	for c, v := range cells {
		ref := fmt.Sprintf("%s%d", colLetter(c), row)
		isStr, text := xlsxCellValue(v)
		if isStr {
			style := ""
			if header {
				style = ` s="1"`
			}
			fmt.Fprintf(&b, `<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xmlEscape(text))
		} else {
			style := ""
			if header {
				style = ` s="1"`
			}
			fmt.Fprintf(&b, `<c r="%s"%s><v>%s</v></c>`, ref, style, text)
		}
	}
	b.WriteString(`</row>`)
	return b.String()
}

// xlsxCellValue 区分字符串/数字, 返回 (isString, 文本)。整数 float64 去掉小数点。
func xlsxCellValue(v any) (bool, string) {
	switch t := v.(type) {
	case string:
		return true, t
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return false, strconv.FormatInt(int64(t), 10)
		}
		return false, strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return false, strconv.Itoa(t)
	case int64:
		return false, strconv.FormatInt(t, 10)
	case bool:
		return true, strconv.FormatBool(t)
	case nil:
		return true, ""
	default:
		return true, fmt.Sprintf("%v", t)
	}
}

// colLetter 0->A, 25->Z, 26->AA。
func colLetter(n int) string {
	s := ""
	for n >= 0 {
		s = string(rune('A'+n%26)) + s
		n = n/26 - 1
		if n < 0 {
			break
		}
	}
	return s
}

const xlsxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
	`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
	`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>` +
	`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
	`</styleSheet>`

const xlsxCoreXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
	`<dc:title>OpsOrigin AI Workbook</dc:title><dc:creator>OpsOrigin AI</dc:creator><cp:lastModifiedBy>OpsOrigin AI</cp:lastModifiedBy>` +
	`<dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>` +
	`</cp:coreProperties>`

const xlsxAppXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` +
	`<Application>OpsOrigin AI</Application></Properties>`
