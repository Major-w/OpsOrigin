
import { Fragment, useEffect, useRef } from 'react'
import { renderMarkdown } from '@/lib/ai-render'
import dynamic from '@/lib/dynamic'

const MermaidBlock = dynamic(() => import('@/components/mermaid-block').then(m => ({ default: m.MermaidBlock })), { ssr: false, loading: () => <div className="h-8" /> })

/**
 * AI 消息内容渲染: 支持 ```mermaid 代码块渲染为图表,
 * 其余 Markdown 走轻量 renderMarkdown(与悬浮助手行为一致)。
 *
 * 代码块: 渲染后 effect 注入「头部栏」(语言名 + 复制按钮, Claude/ChatGPT 风格):
 *   ┌─────────────────┐
 *   │ python     复制 │
 *   ├─────────────────┤
 *   │ code...         │
 *   └─────────────────┘
 * dangerouslySetInnerHTML 在流式更新时整体重建 DOM, effect 每次渲染后重跑,
 * 已注入的块(ai-code-wrap)跳过, 不会重复。语言名取自 renderMarkdown 的 data-lang 标签。
 */
export function AiMarkdown({ content }: { content: string }) {
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const root = rootRef.current
    if (!root) return
    for (const pre of Array.from(root.querySelectorAll('pre'))) {
      if (pre.parentElement?.classList.contains('ai-code-wrap')) continue
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
    }
  })

  if (!content) return null
  // 按 ```mermaid 块切分: 偶数段为普通 markdown, 奇数段为 mermaid 图
  const parts = content.split(/```mermaid\s*\n?([\s\S]*?)```/g)
  if (parts.length === 1) {
    return <div ref={rootRef} dangerouslySetInnerHTML={{ __html: renderMarkdown(content) }} />
  }
  return (
    <div ref={rootRef}>
      {parts.map((part, i) =>
        i % 2 === 1 ? (
          <MermaidBlock key={i} chart={part.replace(/\n$/, '')} />
        ) : part ? (
          <Fragment key={i}>
            <div dangerouslySetInnerHTML={{ __html: renderMarkdown(part) }} />
          </Fragment>
        ) : null,
      )}
    </div>
  )
}
