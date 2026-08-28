
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { cn } from '@/lib/utils'
import dynamic from '@/lib/dynamic'

const MermaidBlock = dynamic(() => import('@/components/mermaid-block').then(m => ({ default: m.MermaidBlock })), { ssr: false, loading: () => <div className="h-8 bg-muted animate-pulse rounded my-2" /> })

interface MarkdownViewerProps {
  content: string
  className?: string
}

/**
 * Markdown 渲染组件(主题自适应)
 * - remark-gfm: 表格 / 任务列表 / 删除线 / 自动链接
 * - ```mermaid 代码块渲染为图表(架构图/拓扑图/流程图)
 * - 样式由 globals.css 的 .markdown-body 统一提供(dark/light 用 CSS 变量)
 */
export function MarkdownViewer({ content, className }: MarkdownViewerProps) {
  return (
    <div className={cn('markdown-body', className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          code({ className, children, ...props }: any) {
            const match = /language-(\w+)/.exec(className || '')
            if (match && match[1] === 'mermaid') {
              return <MermaidBlock chart={String(children).replace(/\n$/, '')} />
            }
            return (
              <code className={className} {...props}>
                {children}
              </code>
            )
          },
        }}
      >
        {content || '*（空内容）*'}
      </ReactMarkdown>
    </div>
  )
}
