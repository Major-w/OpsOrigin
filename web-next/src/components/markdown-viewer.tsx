'use client'

import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { cn } from '@/lib/utils'

interface MarkdownViewerProps {
  content: string
  className?: string
}

/**
 * Markdown 渲染组件(主题自适应)
 * - remark-gfm: 表格 / 任务列表 / 删除线 / 自动链接
 * - 样式由 globals.css 的 .markdown-body 统一提供(dark/light 用 CSS 变量)
 */
export function MarkdownViewer({ content, className }: MarkdownViewerProps) {
  return (
    <div className={cn('markdown-body', className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{content || '*（空内容）*'}</ReactMarkdown>
    </div>
  )
}
