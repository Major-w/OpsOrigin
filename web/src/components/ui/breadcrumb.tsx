
import { useMemo } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { ChevronRight, Home } from 'lucide-react'
import { cn } from '@/lib/utils'

// 路由名称映射
const ROUTE_NAMES: Record<string, string> = {
  dashboard: '仪表盘',
  cmdb: 'CMDB',
  models: '模型管理',
  instances: '实例管理',
  search: '高级搜索',
  stats: '资源统计',
  topology: '拓扑视图',
  impact: '影响分析',
  quality: '数据质量',
  query: '高级查询',
  idc: '机房机柜',
  auto: '自动化运维',
  job: '任务管理',
  patch: '补丁管理',
  inspection: '巡检管理',
  software: '软件管理',
  vuln: '漏洞扫描',
  monitor: '监控中心',
  panorama: '全景监控',
  predict: '智能预测',
  targets: '监控对象',
  alerts: '告警管理',
  collection: '采集管理',
  plugins: '插件管理',
  itsm: 'ITSM',
  tickets: '工单管理',
  process: '流程管理',
  sla: 'SLA 管理',
  ola: 'OLA 管理',
  oncall: '值班管理',
  change: '变更管理',
  changeAssess: '变更评估',
  knowledge: '知识库',
  catalog: '服务目录',
  portal: '服务门户',
  inbound: '工单接入',
  deploy: '应用发布',
  apps: '应用管理',
  environments: '环境管理',
  releases: '发布管理',
  ai: 'AI 中心',
  analytics: '智能分析',
  teams: '团队管理',
  inspections: '智能巡检',
  mcp: 'MCP 管理',
  marketplace: '技能市场',
  scenes: '场景编排',
  drills: '演练管理',
  settings: 'AI 设置',
  scripts: '脚本管理',
  resources: '资源管理',
  credentials: '凭证管理',
  system: '系统管理',
  users: '用户管理',
  roles: '角色管理',
  groups: '分组管理',
  auth: '认证配置',
  health: '系统健康',
  notifications: '通知管理',
  audit: '审计日志',
  lowcode: '低代码',
  pages: '页面管理',
  guide: '使用指南',
  reports: '报表中心',
  schedules: '定时任务',
  workflows: '工作流',
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

  const breadcrumbItems = useMemo(() => {
    if (customItems) return customItems

    const pathSegments = location.pathname.split('/').filter(Boolean)
    const items: BreadcrumbItem[] = []

    // 移除 (dashboard) 分组前缀
    const segments = pathSegments.filter(s => s !== '(dashboard)')

    segments.forEach((segment, index) => {
      const path = '/' + segments.slice(0, index + 1).join('/')
      const name = ROUTE_NAMES[segment] || segment
      items.push({ label: name, path: index < segments.length - 1 ? path : undefined })
    })

    return items
  }, [location.pathname, customItems])

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
