
import { useEffect, useRef, useState } from 'react'
import mermaid from 'mermaid'

// 动态按需初始化(避免多实例重复加载)
let mermaidInit = false
function ensureInit() {
  if (mermaidInit) return
  mermaid.initialize({
    startOnLoad: false,
    theme: document.documentElement.classList.contains('dark') ? 'dark' : 'default',
    securityLevel: 'loose',
    fontFamily: 'inherit',
  })
  mermaidInit = true
}

interface MermaidBlockProps {
  chart: string
}

/**
 * Mermaid 图渲染(架构图/拓扑图/流程图)。
 * 用于 MarkdownViewer 与 AI 对话产物中的 ```mermaid 代码块。
 */
export function MermaidBlock({ chart }: MermaidBlockProps) {
  const ref = useRef<HTMLDivElement>(null)
  const [svg, setSvg] = useState('')
  const [err, setErr] = useState('')
  const [id] = useState(() => `mmd-${Math.random().toString(36).slice(2, 9)}`)

  useEffect(() => {
    let alive = true
    ensureInit()
    const render = async () => {
      try {
        const { svg: s } = await mermaid.render(id, chart)
        if (alive) { setSvg(s); setErr('') }
      } catch (e: any) {
        if (alive) { setErr(e?.message || 'mermaid 渲染失败'); setSvg('') }
      }
    }
    render()
    return () => { alive = false }
  }, [id, chart])

  if (err) {
    return (
      <div className="my-2 rounded-md border border-red-200 dark:border-red-500/30 bg-red-50 dark:bg-red-500/10 p-2">
        <p className="text-[11px] text-red-600 dark:text-red-400">图渲染失败：{err}</p>
        <pre className="mt-1 text-[11px] whitespace-pre-wrap text-muted-foreground">{chart}</pre>
      </div>
    )
  }
  if (!svg) {
    return <div className="my-2 text-xs text-muted-foreground">图加载中…</div>
  }
  return (
    <div className="my-2 overflow-x-auto" dangerouslySetInnerHTML={{ __html: svg }} ref={ref} />
  )
}
