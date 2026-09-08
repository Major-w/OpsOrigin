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

type PresentationOutline = { title: string; slides: { title: string; bullets: string[] }[] }

// 请求后端 LLM 将文档重编为演示大纲(标题 + 每页要点), 取代旧的按标题机械分页。
// 走 apiFetchRaw 自动附带 Authorization 头 —— 此前裸 fetch 缺 token, 后端直接返回
// 401 "missing authorization header"。
async function fetchPresentationOutline(md: string, fallbackName: string): Promise<PresentationOutline> {
  const res = await apiFetchRaw('/ai/presentation', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title: titleFromMd(md, fallbackName), content: md }),
  })
  if (!res.ok) {
    const b = await res.json().catch(() => ({} as { error?: string }))
    throw new Error(b?.error || `HTTP ${res.status}`)
  }
  return await res.json()
}

async function exportPptx(md: string, fallbackName: string): Promise<void> {
  await checkFuse(md)
  // AI 重编演示大纲(LLM 提炼章节 → 每页要点), 空结果抛 EMPTY(由调用方静默)
  const outline = await fetchPresentationOutline(md, fallbackName)
  if (!outline?.slides?.length) throw new Error('EMPTY')
  const docTitle = outline.title?.trim() || titleFromMd(md, fallbackName)

  const PptxGenJS = (await import('pptxgenjs')).default
  const pptx = new PptxGenJS()
  pptx.layout = 'LAYOUT_WIDE'
  const INK = '1A1A1A', MUTED = '555555', ACCENT = '3B82F6'

  const rect = (slide: any, x: number, y: number, w: number, h: number) =>
    slide.addShape((pptx as any).ShapeType?.rect ?? 'rect', { x, y, w, h, fill: { color: ACCENT }, line: { color: ACCENT } })

  // 封面
  const cover = pptx.addSlide()
  cover.addText(docTitle, { x: 0.6, y: 2.2, w: 11.8, h: 1.2, fontSize: 30, bold: true, color: INK })
  cover.addText(new Date().toLocaleDateString('zh-CN'), { x: 0.62, y: 3.4, w: 5, h: 0.4, fontSize: 13, color: MUTED })
  rect(cover, 0.62, 2.05, 0.5, 0.06)

  for (const s of outline.slides) {
    const slide = pptx.addSlide()
    if (s.title) {
      slide.addText(s.title, { x: 0.6, y: 0.4, w: 11.8, h: 0.9, fontSize: 22, bold: true, color: INK })
      rect(slide, 0.62, 1.28, 0.4, 0.05)
    }
    const bullets = (s.bullets || []).map((text: string) => ({ text, options: { bullet: true, breakLine: true } } as Bullet))
    if (bullets.length) {
      slide.addText(bullets as any, { x: 0.7, y: 1.5, w: 11.6, h: 4.6, fontSize: 14, color: MUTED, valign: 'top', lineSpacingMultiple: 1.2 })
    }
  }

  await pptx.writeFile({ fileName: `${sanitizeName(docTitle, fallbackName)}.pptx` })
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

/** 导出 markdown 为 docx/pptx。返回是否真正发起(防重未过则 false)。
 *  - docx: 浏览器内确定性转换, >2MB 或失败时 fallback 服务端 pandoc 通道
 *  - pptx: AI 重编演示文稿(LLM 提炼大纲), 失败不降级机械转储, 直接提示 */
export async function exportDocFile(md: string, fallbackName: string, fmt: 'docx' | 'pptx', key = fallbackName): Promise<boolean> {
  if (!md.trim()) { getMessage().warning('内容为空，无可导出'); return false }
  const k = `${key}:${fmt}`
  if (exporting.has(k)) return false
  exporting.add(k)
  try {
    if (fmt === 'docx') {
      try {
        await exportDocx(md, fallbackName)
      } catch (e: any) {
        // 保险丝(>2MB)或前端转换异常 → 服务端 pandoc 通道兜底
        const note = e?.message === 'TOO_LARGE' ? '文档超过 2MB，改走服务端导出…' : '前端转换失败，尝试服务端导出…'
        await exportViaServer(md, fallbackName, 'docx', note)
      }
    } else {
      getMessage().info('AI 正在将内容重编为演示文稿，请稍候…')
      await exportPptx(md, fallbackName)
    }
    return true
  } catch (e: any) {
    if (e?.message === 'EMPTY') return false // 空大纲已在上层静默/提示过
    if (e?.message === 'TOO_LARGE') getMessage().warning('文档过大，AI 演示生成仅支持 2MB 以内的文档')
    else getMessage().error(`生成失败: ${e?.message || '未知错误'}`)
    return false
  } finally {
    exporting.delete(k)
  }
}
