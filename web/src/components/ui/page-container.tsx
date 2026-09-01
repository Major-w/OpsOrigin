import type { ReactNode } from 'react'
import { Breadcrumb } from 'antd'
import type { BreadcrumbItemType } from 'antd/es/breadcrumb/Breadcrumb'
import { useLocation, useNavigate } from 'react-router-dom'

const ROUTE_NAMES: Record<string, string> = {
  dashboard: '仪表盘',
  cmdb: 'CMDB',
  resources: '资源纳管',
  scripts: '脚本管理',
  workflows: '作业流程',
  deploy: '发布管理',
  monitor: '监控告警',
  itsm: '工单管理',
  knowledge: '知识库',
  system: '系统设置',
  credentials: '凭证管理',
  schedules: '定时任务',
  ai: 'AI 中心',
  auto: '自动化',
  lowcode: '低代码',
  audit: '审计日志',
  reports: '报表统计',
  guide: '使用指南',
}

function getBreadcrumbs(pathname: string): BreadcrumbItemType[] {
  const parts = pathname.split('/').filter(Boolean)
  const items: BreadcrumbItemType[] = []
  let path = ''

  for (const part of parts) {
    path += `/${part}`
    const name = ROUTE_NAMES[part] || part
    items.push({ title: name, href: path })
  }

  return items
}

export interface PageContainerProps {
  children: ReactNode
  title?: string
  subtitle?: string
  extra?: ReactNode
  header?: ReactNode
}

export default function PageContainer({
  children,
  title,
  subtitle,
  extra,
  header,
}: PageContainerProps) {
  const location = useLocation()
  const navigate = useNavigate()
  const breadcrumbItems = getBreadcrumbs(location.pathname)

  return (
    <div className="px-6 py-4">
      {(title || header) && (
        <div className="mb-4">
          {header || (
            <div className="flex items-center justify-between">
              <div>
                {breadcrumbItems.length > 0 && (
                  <Breadcrumb
                    className="mb-2 text-xs text-muted-foreground"
                    items={breadcrumbItems.map((item) => ({
                      title: item.title,
                      onClick: (e: React.MouseEvent) => {
                        e.preventDefault()
                        navigate(item.href!)
                      },
                    }))}
                  />
                )}
                <h1 className="text-xl font-semibold text-foreground">{title}</h1>
                {subtitle && <p className="text-sm text-muted-foreground mt-1">{subtitle}</p>}
              </div>
              {extra && <div>{extra}</div>}
            </div>
          )}
        </div>
      )}
      {children}
    </div>
  )
}
