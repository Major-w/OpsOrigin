
import { Fragment } from 'react'
import { renderMarkdown } from '@/lib/ai-render'
import dynamic from '@/lib/dynamic'

const MermaidBlock = dynamic(() => import('@/components/mermaid-block').then(m => ({ default: m.MermaidBlock })), { ssr: false, loading: () => <div className="h-8 bg-muted animate-pulse rounded my-2" /> })

/**
 * AI 消息内容渲染: 支持 ```mermaid 代码块渲染为图表,
 * 其余 Markdown 走轻量 renderMarkdown(与悬浮助手行为一致)。
 */
export function AiMarkdown({ content }: { content: string }) {
  if (!content) return null
  // 按 ```mermaid 块切分: 偶数段为普通 markdown, 奇数段为 mermaid 图
  const parts = content.split(/```mermaid\s*\n?([\s\S]*?)```/g)
  if (parts.length === 1) {
    return <div dangerouslySetInnerHTML={{ __html: renderMarkdown(content) }} />
  }
  return (
    <div>
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
