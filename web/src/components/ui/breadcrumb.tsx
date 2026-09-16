
import { useMemo } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { ChevronRight, Home } from 'lucide-react'
import { cn } from '@/lib/utils'
import { t, useLocale } from '@/lib/i18n'

// 路由名称映射(使用 t() 实现国际化)
const ROUTE_KEYS: Record<string, string> = {
  dashboard: 'breadcrumb.dashboard',
  cmdb: 'nav.cmdb',
  models: 'breadcrumb.models',
  instances: 'breadcrumb.instances',
  search: 'breadcrumb.search',
  stats: 'breadcrumb.stats',
  topology: 'breadcrumb.topology',
  impact: 'breadcrumb.impact',
  quality: 'breadcrumb.quality',
  query: 'breadcrumb.query',
  idc: 'breadcrumb.idc',
  auto: 'breadcrumb.auto',
  job: 'breadcrumb.job',
  patch: 'breadcrumb.patch',
  inspection: 'breadcrumb.inspection',
  software: 'breadcrumb.software',
  vuln: 'breadcrumb.vuln',
  monitor: 'breadcrumb.monitor',
  panorama: 'breadcrumb.panorama',
  predict: 'breadcrumb.predict',
  targets: 'breadcrumb.targets',
  alerts: 'breadcrumb.alerts',
  collection: 'breadcrumb.collection',
  plugins: 'breadcrumb.plugins',
  itsm: 'nav.itsm',
  tickets: 'breadcrumb.tickets',
  process: 'breadcrumb.process',
  sla: 'breadcrumb.sla',
  ola: 'breadcrumb.ola',
  oncall: 'breadcrumb.oncall',
  change: 'breadcrumb.change',
  changeAssess: 'breadcrumb.changeAssess',
  knowledge: 'breadcrumb.knowledge',
  catalog: 'breadcrumb.catalog',
  portal: 'breadcrumb.portal',
  inbound: 'breadcrumb.inbound',
  deploy: 'breadcrumb.deploy',
  apps: 'breadcrumb.apps',
  environments: 'breadcrumb.environments',
  releases: 'breadcrumb.releases',
  ai: 'breadcrumb.ai',
  analytics: 'breadcrumb.analytics',
  teams: 'breadcrumb.teams',
  inspections: 'breadcrumb.inspections',
  mcp: 'breadcrumb.mcp',
  marketplace: 'breadcrumb.marketplace',
  scenes: 'breadcrumb.scenes',
  drills: 'breadcrumb.drills',
  settings: 'breadcrumb.settings',
  scripts: 'breadcrumb.scripts',
  resources: 'breadcrumb.resources',
  credentials: 'breadcrumb.credentials',
  system: 'breadcrumb.system',
  users: 'breadcrumb.users',
  roles: 'breadcrumb.roles',
  groups: 'breadcrumb.groups',
  auth: 'breadcrumb.auth',
  health: 'breadcrumb.health',
  notifications: 'breadcrumb.notifications',
  audit: 'breadcrumb.audit',
  lowcode: 'breadcrumb.lowcode',
  pages: 'breadcrumb.pages',
  guide: 'breadcrumb.guide',
  reports: 'breadcrumb.reports',
  schedules: 'breadcrumb.schedules',
  workflows: 'breadcrumb.workflows',
}

interface BreadcrumbItem {
  label: string
  path?: string
}

interface BreadcrumbProps {
  items?: BreadcrumbItem[]
  className?: string
}

export function Breadcrumb({ items: customItems, className }: BreadcrumbProps) {
  const location = useLocation()
  const locale = useLocale()

  const breadcrumbItems = useMemo(() => {
    if (customItems) return customItems

    const pathSegments = location.pathname.split('/').filter(Boolean)
    const items: BreadcrumbItem[] = []

    // 移除 (dashboard) 分组前缀
    const segments = pathSegments.filter(s => s !== '(dashboard)')

    segments.forEach((segment, index) => {
      const path = '/' + segments.slice(0, index + 1).join('/')
      const key = ROUTE_KEYS[segment]
      const name = key ? t(key) : segment
      items.push({ label: name, path: index < segments.length - 1 ? path : undefined })
    })

    return items
  }, [location.pathname, customItems, locale])

  return (
    <nav className={cn("flex items-center gap-1 text-xs text-muted-foreground", className)}>
      <Link to="/dashboard" className="hover:text-foreground transition-colors">
        <Home className="h-3.5 w-3.5" />
      </Link>
      {breadcrumbItems.map((item, index) => (
        <span key={index} className="flex items-center gap-1">
          <ChevronRight className="h-3 w-3" />
          {item.path ? (
            <Link to={item.path} className="hover:text-foreground transition-colors">
              {item.label}
            </Link>
          ) : (
            <span className="text-foreground font-medium">{item.label}</span>
          )}
        </span>
      ))}
    </nav>
  )
}
