// ============================================================================
// 纯前端文档导出: markdown → docx / pptx（浏览器内确定性转换，零服务器负载）
// - docx:  marked → HTML → DOMParser 遍历 → docx 包原生对象 → Packer.toBlob
// - pptx:  marked.lexer 分词 → h1/h2 分页 → pptxgenjs 原生可编辑文本框
// 保险丝: 内容 > 2MB 拒绝前端转换（提示走服务端通道）。
// 动态 import 三个依赖，不进主 bundle，首次点导出才加载。
// ============================================================================
import { getMessage } from '@/lib/message'
import { apiFetchRaw } from '@/lib/utils'

const MAX_MD_BYTES = 2 * 1024 * 1024

function decodeEntities(s: string): string {
  const el = document.createElement('textarea')
  el.innerHTML = s
  return el.value
}

function sanitizeName(title: string, fallback: string): string {
  const t = (title || '').replace(/[\\/:*?"<>|#\n\r]/g, '_').trim().slice(0, 40)
  return t || fallback
}

function titleFromMd(md: string, fallback: string): string {
  const h1 = md.match(/^#\s+(.+)$/m)?.[1]
  return sanitizeName(decodeEntities((h1 || '').trim()), fallback)
}

async function checkFuse(md: string) {
  if (new Blob([md]).size > MAX_MD_BYTES) {
    // 抛出后由 exportDocFile 自动 fallback 服务端 pandoc 通道, 无需用户手动切换
    throw new Error('TOO_LARGE')
  }
}

function triggerDownload(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

// ---------------------------------------------------------------------------
// docx
// ---------------------------------------------------------------------------

type InlineStyle = { bold?: boolean; italic?: boolean; code?: boolean }

async function exportDocx(md: string, fallbackName: string): Promise<void> {
  await checkFuse(md)
  const [{ marked }, DOCX] = await Promise.all([import('marked'), import('docx')])
  const {
    Document, Packer, Paragraph, TextRun, HeadingLevel, Table, TableRow, TableCell,
    WidthType, AlignmentType, BorderStyle, ShadingType,
  } = DOCX
  const html = await marked.parse(md, { async: true })
  const dom = new DOMParser().parseFromString(html, 'text/html')

  const inlineRuns = (node: Node, st: InlineStyle = {}): any[] => {
    const runs: any[] = []
    node.childNodes.forEach((child) => {
      if (child.nodeType === Node.TEXT_NODE) {
        const text = child.textContent || ''
        if (text) {
          runs.push(new TextRun({
            text,
            bold: st.bold,
            italics: st.italic,
            font: st.code ? 'Courier New' : undefined,
            shading: st.code ? { type: ShadingType.CLEAR, fill: 'F2F2F2' } : undefined,
          }))
        }
        return
      }
      const el = child as HTMLElement
      const tag = el.tagName.toLowerCase()
      const next: InlineStyle = {
        bold: st.bold || tag === 'strong' || tag === 'b',
        italic: st.italic || tag === 'em' || tag === 'i',
        code: st.code || tag === 'code',
      }
      if (tag === 'br') { runs.push(new TextRun({ break: 1 })); return }
      if (tag === 'a') {
        runs.push(new TextRun({ text: el.textContent || '', bold: next.bold, italics: next.italic, color: '3B82F6', underline: {} }))
        return
      }
      runs.push(...inlineRuns(el, next))
    })
    return runs
  }

  const headingMap: Record<string, (typeof HeadingLevel)[keyof typeof HeadingLevel]> = {
    h1: HeadingLevel.HEADING_1, h2: HeadingLevel.HEADING_2, h3: HeadingLevel.HEADING_3,
    h4: HeadingLevel.HEADING_4, h5: HeadingLevel.HEADING_5, h6: HeadingLevel.HEADING_6,
  }
  const border = { style: BorderStyle.SINGLE, size: 1, color: 'D4D4D8' }
  const tableBorders = { top: border, bottom: border, left: border, right: border, insideHorizontal: border, insideVertical: border }

  const children: any[] = []
  const walk = (parent: Element, listCtx?: { ordered: boolean; indent: number }) => {
    let olIdx = 0
    Array.from(parent.children).forEach((el) => {
      const tag = el.tagName.toLowerCase()
      switch (tag) {
        case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6':
          children.push(new Paragraph({ heading: headingMap[tag], children: inlineRuns(el) }))
          break
        case 'p':
          children.push(new Paragraph({
            children: inlineRuns(el),
            spacing: { after: 120 },
            alignment: (el as HTMLElement).style?.textAlign === 'center' ? AlignmentType.CENTER : undefined,
          }))
          break
        case 'ul': case 'ol':
          walk(el, { ordered: tag === 'ol', indent: (listCtx?.indent ?? -1) + 1 })
          break
        case 'li': {
          olIdx += 1
          const prefix = listCtx?.ordered ? `${olIdx}. ` : undefined
          const runs = prefix ? [new TextRun({ text: prefix }), ...inlineRuns(el)] : inlineRuns(el)
          children.push(new Paragraph({
            children: runs,
            bullet: !listCtx?.ordered ? { level: Math.max(listCtx?.indent ?? 0, 0) } : undefined,
            indent: listCtx?.ordered ? { left: 720 * ((listCtx.indent ?? 0) + 1), hanging: 360 } : undefined,
            spacing: { after: 60 },
          }))
          // 嵌套列表
          Array.from(el.children).forEach((sub) => {
            const subTag = sub.tagName.toLowerCase()
            if (subTag === 'ul' || subTag === 'ol') walk(sub, { ordered: subTag === 'ol', indent: (listCtx?.indent ?? -1) + 1 })
          })
          break
        }
        case 'blockquote': {
          Array.from(el.children).forEach((p) => {
            children.push(new Paragraph({
              children: inlineRuns(p).map((r: any) => r),
              indent: { left: 480 },
              spacing: { after: 120 },
              border: { left: { style: BorderStyle.SINGLE, size: 12, color: 'A1A1AA' } },
            }))
          })
          break
        }
        case 'pre': {
          const code = el.querySelector('code')?.textContent || el.textContent || ''
          code.split('\n').forEach((line) => {
            children.push(new Paragraph({
              children: [new TextRun({ text: line || ' ', font: 'Courier New', size: 18, shading: { type: ShadingType.CLEAR, fill: 'F4F4F5' } })],
              spacing: { after: 0 },
            }))
          })
          children.push(new Paragraph({ text: '', spacing: { after: 120 } }))
          break
        }
        case 'table': {
          const rows = Array.from(el.querySelectorAll('tr'))
          if (!rows.length) break
          const docxRows = rows.map((tr, ri) => new TableRow({
            children: Array.from(tr.children).map((cell) => new TableCell({
              children: [new Paragraph({ children: inlineRuns(cell), alignment: AlignmentType.LEFT })],
              shading: ri === 0 ? { type: ShadingType.CLEAR, fill: 'F2F2F2' } : undefined,
            })),
          }))
          children.push(new Table({ rows: docxRows, width: { size: 100, type: WidthType.PERCENTAGE }, borders: tableBorders }))
          children.push(new Paragraph({ text: '', spacing: { after: 120 } }))
          break
        }
        case 'hr':
          children.push(new Paragraph({ thematicBreak: true }))
          break
        default:
          // div 等容器递归
          if (el.children.length) walk(el, listCtx)
          else children.push(new Paragraph({ children: inlineRuns(el), spacing: { after: 120 } }))
      }
    })
  }
  walk(dom.body)

  const doc = new Document({
    sections: [{ properties: {}, children }],
  })
  const blob = await Packer.toBlob(doc)
  triggerDownload(blob, `${titleFromMd(md, fallbackName)}.docx`)
}

// ---------------------------------------------------------------------------
// pptx
// ---------------------------------------------------------------------------

type Bullet = { text: string; options: { bullet?: boolean | number; indentLevel?: number; bold?: boolean; fontSize?: number; breakLine?: boolean; fontFace?: string } }

async function exportPptx(md: string, fallbackName: string): Promise<void> {
  await checkFuse(md)
  const { marked } = await import('marked')
  const PptxGenJS = (await import('pptxgenjs')).default
  const tokens = marked.lexer(md)

  const plain = (s: string) => decodeEntities((s || '').replace(/[*_`~]/g, '').trim())

  // 分页: h1/h2 开新页; 无任何标题时整篇一页
  type SlideSpec = { title: string; bullets: Bullet[] }
  const slides: SlideSpec[] = []
  let cur: SlideSpec | null = null
  let docTitle = ''
  const push = () => { if (cur && (cur.title || cur.bullets.length)) slides.push(cur) }
  const ensure = (title: string) => { push(); cur = { title, bullets: [] } }

  for (const tk of tokens as any[]) {
    if (tk.type === 'heading') {
      if (tk.depth <= 2) {
        const t = plain(tk.text)
        if (!docTitle && tk.depth === 1) docTitle = t
        ensure(t)
      } else {
        if (!cur) ensure('')
        cur!.bullets.push({ text: plain(tk.text), options: { bullet: true, bold: true, breakLine: true } })
      }
    } else if (tk.type === 'list') {
      if (!cur) ensure('')
      for (const item of tk.items as any[]) {
        cur!.bullets.push({ text: plain(item.text), options: { bullet: true, breakLine: true } })
        for (const sub of (item.tokens || []) as any[]) {
          if (sub.type === 'list') {
            for (const subItem of sub.items as any[]) {
              cur!.bullets.push({ text: plain(subItem.text), options: { bullet: true, indentLevel: 1, breakLine: true } })
            }
          }
        }
      }
    } else if (tk.type === 'paragraph' || tk.type === 'text') {
      if (!cur) ensure('')
      const t = plain(tk.text)
      if (t) cur!.bullets.push({ text: t, options: { breakLine: true } })
    } else if (tk.type === 'table') {
      if (!cur) ensure('')
      // marked 表格 token: header/rows 均为 cell token 数组(取 .text), 兼容纯字符串形态
      const cellText = (c: any) => plain(typeof c === 'string' ? c : (c?.text ?? ''))
      const rows = [tk.header, ...(tk.rows || [])] as any[][]
      for (const row of rows) {
        if (!row) continue
        cur!.bullets.push({ text: row.map(cellText).join('  |  '), options: { bullet: false, breakLine: true } })
      }
    } else if (tk.type === 'code') {
      if (!cur) ensure('')
      plain(tk.text).split('\n').forEach((line) => {
        cur!.bullets.push({ text: line || ' ', options: { bullet: false, fontFace: 'Consolas', breakLine: true } })
      })
    }
    // space/hr 忽略
  }
  push()

  if (!slides.length) { getMessage().warning('没有可转换为幻灯片的内容'); throw new Error('EMPTY') }

  const pptx = new PptxGenJS()
  pptx.layout = 'LAYOUT_WIDE'
  const INK = '1A1A1A', MUTED = '555555', ACCENT = '3B82F6'

  const rect = (slide: any, x: number, y: number, w: number, h: number) =>
    slide.addShape((pptx as any).ShapeType?.rect ?? 'rect', { x, y, w, h, fill: { color: ACCENT }, line: { color: ACCENT } })

  // 封面
  const cover = pptx.addSlide()
  cover.addText(docTitle || fallbackName, { x: 0.6, y: 2.2, w: 11.8, h: 1.2, fontSize: 30, bold: true, color: INK })
  cover.addText(new Date().toLocaleDateString('zh-CN'), { x: 0.62, y: 3.4, w: 5, h: 0.4, fontSize: 13, color: MUTED })
  rect(cover, 0.62, 2.05, 0.5, 0.06)

  for (const s of slides) {
    const slide = pptx.addSlide()
    if (s.title) {
      slide.addText(s.title, { x: 0.6, y: 0.4, w: 11.8, h: 0.9, fontSize: 22, bold: true, color: INK })
      rect(slide, 0.62, 1.28, 0.4, 0.05)
    }
    if (s.bullets.length) {
      slide.addText(s.bullets as any, { x: 0.7, y: 1.5, w: 11.6, h: 4.6, fontSize: 14, color: MUTED, valign: 'top', lineSpacingMultiple: 1.2 })
    }
  }

  await pptx.writeFile({ fileName: `${titleFromMd(md, fallbackName)}.pptx` })
}

// ---------------------------------------------------------------------------
// 统一入口(带 loading/防重/错误提示)
// ---------------------------------------------------------------------------

const exporting = new Set<string>()

/** 服务端确定性导出通道(POST /api/v1/ai/export, 后端 pandoc): 前端保险丝触发或转换失败时的 fallback。 */
async function exportViaServer(md: string, fallbackName: string, fmt: 'docx' | 'pptx', note: string) {
  getMessage().info(note)
  const res = await apiFetchRaw('/api/v1/ai/export', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ format: fmt, title: titleFromMd(md, fallbackName), content: md }),
  })
  if (res.status === 501) {
    getMessage().warning('服务端未安装 pandoc，服务端导出不可用（部署镜像需包含 pandoc）')
    throw new Error('SERVER_UNAVAILABLE')
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}) as { error?: string })
    throw new Error(body.error || `HTTP ${res.status}`)
  }
  triggerDownload(await res.blob(), `${titleFromMd(md, fallbackName)}.${fmt}`)
}

/** 导出 markdown 为 docx/pptx。返回是否真正发起(防重未过则 false)。前端保险丝触发或转换失败时自动 fallback 服务端通道。 */
export async function exportDocFile(md: string, fallbackName: string, fmt: 'docx' | 'pptx', key = fallbackName): Promise<boolean> {
  if (!md.trim()) { getMessage().warning('内容为空，无可导出'); return false }
  const k = `${key}:${fmt}`
  if (exporting.has(k)) return false
  exporting.add(k)
  try {
    if (fmt === 'docx') await exportDocx(md, fallbackName)
    else await exportPptx(md, fallbackName)
    return true
  } catch (e: any) {
    // 保险丝(>2MB)或前端转换异常 → 服务端 pandoc 通道兜底
    const note = e?.message === 'TOO_LARGE' ? '文档超过 2MB，改走服务端导出…' : '前端转换失败，尝试服务端导出…'
    try {
      await exportViaServer(md, fallbackName, fmt, note)
      return true
    } catch (se: any) {
      if (e?.message !== 'TOO_LARGE' && e?.message !== 'EMPTY') console.error('[doc-export]', e)
      if (se?.message !== 'SERVER_UNAVAILABLE') getMessage().error(`导出失败: ${se?.message || '未知错误'}`)
      return false
    }
  } finally {
    exporting.delete(k)
  }
}
