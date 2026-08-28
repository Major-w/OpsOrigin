
// PageLayout —— 统一页面布局包装组件
// 提供一致的 padding、max-width、标题/副标题结构。
// 用法：
//   <PageLayout title="页面标题" subtitle="副标题描述">
//     ...页面内容
//   </PageLayout>
//
// 或自定义标题：
//   <PageLayout header={<CustomHeader />}>
//     ...页面内容
//   </PageLayout>

import { type ReactNode } from 'react'

interface PageLayoutProps {
  /** 页面标题 */
  title?: ReactNode
  /** 页面副标题 */
  subtitle?: ReactNode
  /** 标题栏右侧操作按钮 */
  actions?: ReactNode
  /** 自定义标题区域（覆盖 title/subtitle/actions） */
  header?: ReactNode
  /** 子内容 */
  children: ReactNode
  /** 外层容器 className 覆盖 */
  className?: string
  /** 内容区域 className */
  contentClassName?: string
  /** 是否限制最大宽度并居中（默认 true） */
  maxWidth?: boolean
}

export function PageLayout({
  title,
  subtitle,
  actions,
  header,
  children,
  className = '',
  contentClassName = '',
  maxWidth = true,
}: PageLayoutProps) {
  return (
    <div className={`p-6 space-y-4 ${maxWidth ? 'max-w-7xl mx-auto' : ''} ${className}`}>
      {header ?? (
        (title || actions) && (
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              {title && (
                <h1 className="text-2xl font-bold tracking-tight flex items-center gap-2">
                  {title}
                </h1>
              )}
              {subtitle && (
                <p className="mt-1 text-sm text-muted-foreground">
                  {subtitle}
                </p>
              )}
            </div>
            {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
          </div>
        )
      )}
      <div className={contentClassName}>
        {children}
      </div>
    </div>
  )
}
