
import { Fragment, useEffect, useRef, useState } from 'react'
import { renderMarkdown, renderExploreContent, renderMarkdownCached, renderExploreContentCached } from '@/lib/ai-render'
import { highlightCode, highlightAllCode } from '@/lib/code-highlight'
import { bindImageLightbox } from '@/components/ai/image-lightbox'
import dynamic from '@/lib/dynamic'

const MermaidBlock = dynamic(() => import('@/components/mermaid-block').then(m => ({ default: m.MermaidBlock })), { ssr: false, loading: () => <div className="h-8" /> })

interface AiMarkdownProps {
  content: string
  /** 流式输出中: 追加闪烁光标(CSS .ai-streaming), 不重高已处理块避免闪烁 */
  streaming?: boolean
  /** 引用内联标记: 正文 [n] 在范围内时转为可点击上标, 跳转 cite-{citationAnchor}-{n} */
  citationIds?: string[]
  citationAnchor?: string
}

/** 流式期间 mermaid 文本每帧都在增长, 直接渲染会让 MermaidBlock 每帧重画整张图(重且闪)。
 *  流式时 400ms 防抖 + 占位骨架; 流结束立即渲染最终图。 */
function DebouncedMermaid({ chart, streaming }: { chart: string; streaming?: boolean }) {
  const [render, setRender] = useState(streaming ? '' : chart)
  useEffect(() => {
    if (!streaming) { setRender(chart); return }
    const t = setTimeout(() => setRender(chart), 400)
    return () => clearTimeout(t)
  }, [chart, streaming])
  if (!render.trim()) {
    return (
      <div className="my-2 h-16 rounded-lg border border-dashed border-gray-300 dark:border-gray-600 flex items-center justify-center text-[11px] text-muted-foreground">
        图表生成中…
      </div>
    )
  }
  return <MermaidBlock chart={render} />
}

/**
 * AI 消息内容渲染(标准 Markdown 路径): 支持 ```mermaid 代码块渲染为图表,
 * 其余 Markdown 走轻量 renderMarkdown(与悬浮助手行为一致)。
 *
 * 代码块: effect 注入「头部栏」(语言名 + 复制按钮, Claude/ChatGPT 风格)并就近语法高亮:
 *   ┌─────────────────┐
 *   │ python     复制 │
 *   ├─────────────────┤
 *   │ code...         │
 *   └─────────────────┘
 * dangerouslySetInnerHTML 在流式更新时整体重建 DOM, effect 每次渲染后重跑,
 * 已注入的块(ai-code-wrap / data-hl)跳过, 不会重复。语言名取自 renderMarkdown 的 data-lang 标签。
 */
export function AiMarkdown({ content, streaming, citationIds, citationAnchor }: AiMarkdownProps) {
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const root = rootRef.current
    if (!root) return
    // Markdown 内嵌图片点击 → 页内预览(effect 无依赖, 每次渲染重绑, 开销可忽略)
    const unbindImg = bindImageLightbox(root)
    for (const pre of Array.from(root.querySelectorAll('pre'))) {
      const wrapEl = pre.parentElement
      if (wrapEl?.classList.contains('ai-code-wrap')) {
        // 已注入的块跳过; 但流式期间建的块当时内容还在长、无法决定是否折叠, 流结束后补做
        if (wrapEl.dataset.fold === '-' && !streaming) applyCodeFold(wrapEl, pre)
        continue
      }
      // 语言名: renderMarkdown 在 pre 内生成的 data-lang 标签 span(text-[9px] uppercase)
      const langSpan = pre.querySelector('span.uppercase')
      const lang = langSpan?.textContent?.trim() || ''
      if (langSpan) langSpan.remove()

      const wrap = document.createElement('div')
      wrap.className = 'ai-code-wrap my-2 rounded-lg overflow-hidden border border-gray-700/40'
      pre.parentNode?.insertBefore(wrap, pre)
      // pre 原自带 rounded/my-2, 收编进 wrap 后改贴边
      pre.classList.remove('my-2', 'rounded')
      pre.classList.add('rounded-none', 'my-0')
      wrap.appendChild(pre)

      // 头部栏
      const header = document.createElement('div')
      header.className = 'flex items-center justify-between bg-gray-800 px-3 py-1 text-[10px] text-gray-400 border-b border-gray-700/40'
      const langEl = document.createElement('span')
      langEl.textContent = lang || '代码'
      langEl.className = 'font-mono uppercase tracking-wide select-none'
      header.appendChild(langEl)
      const btn = document.createElement('button')
      btn.type = 'button'
      btn.textContent = '复制'
      btn.className = 'px-1.5 py-0.5 rounded text-[10px] leading-none text-gray-300 hover:text-white hover:bg-gray-700 transition-colors'
      btn.onclick = () => {
        const code = pre.querySelector('code')?.textContent ?? pre.textContent ?? ''
        navigator.clipboard.writeText(code).then(() => {
          btn.textContent = '已复制'
          setTimeout(() => { btn.textContent = '复制' }, 1500)
        }).catch(() => {})
      }
      header.appendChild(btn)
      wrap.insertBefore(header, pre)

      // 语法高亮(仅处理未高亮的块; data-lang 已取出, 直接用于着色)
      if (!pre.dataset.hl) {
        const code = pre.querySelector('code')
        if (code) {
          code.innerHTML = highlightCode(code.textContent || '', lang)
          pre.dataset.hl = '1'
        }
      }

      // 折叠决策: 流式期间代码还在增长, 推迟到流结束再定(fold='-' 表示待定)
      if (streaming) wrap.dataset.fold = '-'
      else applyCodeFold(wrap, pre)
    }
    // 引用内联标记: 正文 [n] → 可点击上标
    if (citationIds && citationIds.length && citationAnchor && shouldLinkify(root, citationAnchor, citationIds.length, streaming)) {
      linkifyInlineCitations(root, citationAnchor, citationIds)
    }
    return unbindImg
  })

  if (!content) return null
  // 流式消息内容每帧都变, 走无缓存版本(否则会挤爆缓存); 稳定消息走缓存, 列表重渲染时零成本
  const render = streaming ? renderMarkdown : renderMarkdownCached
  // 按 ```mermaid 块切分: 偶数段为普通 markdown, 奇数段为 mermaid 图
  const parts = content.split(/```mermaid\s*\n?([\s\S]*?)```/g)
  if (parts.length === 1) {
    return <div ref={rootRef} className={streaming ? 'ai-streaming' : undefined} dangerouslySetInnerHTML={{ __html: render(content) }} />
  }
  return (
    <div ref={rootRef} className={streaming ? 'ai-streaming' : undefined}>
      {parts.map((part, i) =>
        i % 2 === 1 ? (
          <DebouncedMermaid key={i} chart={part.replace(/\n$/, '')} streaming={streaming} />
        ) : part ? (
          <Fragment key={i}>
            <div dangerouslySetInnerHTML={{ __html: render(part) }} />
          </Fragment>
        ) : null,
      )}
    </div>
  )
}

/**
 * 自主/探索模式渲染: 结构化标签(plan/step/report/工具调用)走卡片式 renderExploreContent,
 * 代码块同样就近语法高亮。流式期间追加闪烁光标。
 */
export function ExploreMarkdown({ content, streaming, citationIds, citationAnchor }: AiMarkdownProps) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (ref.current) {
      highlightAllCode(ref.current)
      if (citationIds && citationIds.length && citationAnchor && shouldLinkify(ref.current, citationAnchor, citationIds.length, streaming)) {
        linkifyInlineCitations(ref.current, citationAnchor, citationIds)
      }
      return bindImageLightbox(ref.current)
    }
  })
  if (!content) return null
  return (
    <div
      ref={ref}
      className={streaming ? 'ai-streaming' : undefined}
      dangerouslySetInnerHTML={{ __html: (streaming ? renderExploreContent : renderExploreContentCached)(content) }}
    />
  )
}

/** 非流式消息内容恒定: 引用内联化做过一次即可, 避免每帧重复遍历 DOM(流式期间内容在变, 需每帧重做) */
function shouldLinkify(el: HTMLElement, anchor: string, n: number, streaming?: boolean) {
  const key = `${anchor}:${n}`
  if (streaming) return true
  if (el.dataset.citeLink === key) return false
  el.dataset.citeLink = key
  return true
}

/** 将容器内正文 [n] 标记(在 citationIds 范围内)转为可点击上标, 跳转 cite-{anchor}-{n}。
 *  跳过 code/pre/a 等元素内部, 避免破坏代码与已有链接。 */
function linkifyInlineCitations(root: HTMLElement, anchor: string, ids: string[]) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const p = node.parentElement
      if (!p) return NodeFilter.FILTER_REJECT
      if (p.closest('pre, code, a, button, sup')) return NodeFilter.FILTER_REJECT
      return NodeFilter.FILTER_ACCEPT
    },
  })
  const targets: Text[] = []
  let n = walker.nextNode()
  while (n) {
    targets.push(n as Text)
    n = walker.nextNode()
  }
  const re = /\[(\d+)\]/g
  for (const node of targets) {
    const text = node.nodeValue || ''
    if (!re.test(text)) continue
    re.lastIndex = 0
    let m: RegExpExecArray | null
    let last = 0
    let changed = false
    const frag = document.createDocumentFragment()
    while ((m = re.exec(text))) {
      const idx = parseInt(m[1], 10)
      if (idx < 1 || idx > ids.length) continue
      changed = true
      if (m.index > last) frag.appendChild(document.createTextNode(text.slice(last, m.index)))
      const sup = document.createElement('sup')
      sup.className = 'ai-cite-ref'
      const a = document.createElement('a')
      a.href = `#cite-${anchor}-${idx}`
      a.dataset.target = `cite-${anchor}-${idx}`
      a.title = ids[idx - 1] || ''
      a.textContent = `[${idx}]`
      a.onclick = (e) => {
        e.preventDefault()
        scrollToCite(anchor, idx)
      }
      sup.appendChild(a)
      frag.appendChild(sup)
      last = m.index + m[0].length
    }
    if (changed) {
      if (last < text.length) frag.appendChild(document.createTextNode(text.slice(last)))
      node.parentNode?.replaceChild(frag, node)
    }
  }
}

/** 超过该行数的代码块默认收起, 避免长脚本占满整屏 */
const FOLD_LINES = 18

/** 超长代码块折叠: 底部加「展开全部 (N 行)/收起」。幂等 —— wrap.dataset.fold 标记是否已处理。 */
function applyCodeFold(wrap: HTMLElement, pre: HTMLElement) {
  const lineCount = (pre.querySelector('code')?.textContent || '').split('\n').length
  if (lineCount <= FOLD_LINES) { wrap.dataset.fold = '0'; return }
  wrap.dataset.fold = '1'
  pre.style.maxHeight = '18rem'
  pre.style.overflow = 'auto'
  const footer = document.createElement('div')
  footer.className = 'bg-gray-800 border-t border-gray-700/40'
  const more = document.createElement('button')
  more.type = 'button'
  more.className = 'w-full py-1 text-[10px] text-gray-300 hover:text-white transition-colors'
  more.textContent = `展开全部 (${lineCount} 行)`
  let expanded = false
  more.onclick = () => {
    expanded = !expanded
    pre.style.maxHeight = expanded ? '' : '18rem'
    more.textContent = expanded ? '收起' : `展开全部 (${lineCount} 行)`
  }
  footer.appendChild(more)
  wrap.appendChild(footer)
}

export function scrollToCite(anchor: string, idx: number) {
  const details = document.getElementById(`cites-${anchor}`)
  if (details && !details.hasAttribute('open')) details.setAttribute('open', '')
  const el = document.getElementById(`cite-${anchor}-${idx}`)
  if (el) {
    el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    el.classList.add('ai-cite-flash')
    setTimeout(() => el.classList.remove('ai-cite-flash'), 1200)
  }
}
