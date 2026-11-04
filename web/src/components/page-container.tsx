import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface PageContainerProps {
  title: ReactNode
  subtitle?: ReactNode
  extra?: ReactNode
  children: ReactNode
  /** 内容区间距（space-y-*），默认 space-y-6 */
  className?: string
}

/**
 * 统一页面骨架：页头（标题/副标题/操作区）+ 内容区。
 * 面包屑由 layout 顶栏统一渲染，此处不再重复。
 */
export function PageContainer({ title, subtitle, extra, children, className }: PageContainerProps) {
  return (
    <div className={cn('p-6', className || 'space-y-6')}>
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold truncate">{title}</h1>
          {subtitle && <p className="text-sm text-muted-foreground">{subtitle}</p>}
        </div>
        {extra && <div className="shrink-0 flex items-center gap-2">{extra}</div>}
      </div>
      {children}
    </div>
  )
}
