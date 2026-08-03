'use client'

import { useState } from 'react'
import Link from 'next/link'
import { usePathname, useRouter } from 'next/navigation'
import { cn } from '@/lib/utils'
import { Server, TicketCheck, Activity, Bot, Shield, LayoutDashboard, Hexagon, LogOut, User, Terminal, Workflow, Clock, Search, BookOpen, Calendar, GitBranch, Package, Bell, Key, Blocks, Users, Settings, Sun, Moon, Scan, Bug, Download, PackagePlus, Network, AlertTriangle, History, FileText, Rocket, AppWindow, FileCheck2, Cloud, Play, Sparkles, Database, Radio, BarChart3, Building2, Radar, MessagesSquare, Gauge, FlaskConical, TrendingUp, type LucideIcon } from 'lucide-react'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { ScrollToTop } from '@/components/ui/scroll-to-top'
import AIAssistant from '@/components/ai-assistant'

interface NavItem {
  to: string
  label: string
  icon: LucideIcon
}

interface NavSubGroup {
  label: string
  items: NavItem[]
}

interface NavGroup {
  label: string
  icon: LucideIcon
  items: (NavItem | NavSubGroup)[]
}

const navGroups: (NavItem | NavGroup)[] = [
  { to: '/dashboard', label: '仪表盘', icon: LayoutDashboard },
  {
    label: '基础功能管理', icon: Settings, items: [
      { to: '/resources', label: '资源纳管', icon: Server },
      { to: '/credentials', label: '凭证管理', icon: Key },
      { to: '/lowcode', label: '低代码编排', icon: Blocks },
      { to: '/scripts', label: '脚本管理', icon: Terminal },
      { to: '/workflows', label: '作业流程', icon: GitBranch },
    ]
  },
  {
    label: '自动化运维', icon: Workflow, items: [
      { to: '/auto', label: '运维总览', icon: Workflow },
      { to: '/auto/job', label: '作业中心', icon: Play },
      { to: '/schedules', label: '定时任务', icon: Clock },
      { to: '/auto/inspection', label: '自动化巡检', icon: Scan },
      { to: '/auto/vuln-scan', label: '漏洞扫描', icon: Bug },
      { to: '/auto/patch', label: '补丁安装', icon: Download },
      { to: '/auto/software', label: '文件分发', icon: PackagePlus },
      { to: '/auto/collect', label: '数据采集', icon: Database },
    ]
  },
  {
    label: '应用发布', icon: Rocket, items: [
      { to: '/deploy/apps', label: '应用管理', icon: AppWindow },
      { to: '/deploy/releases', label: '发布单', icon: FileCheck2 },
      { to: '/deploy/environments', label: '环境管理', icon: Cloud },
      { to: '/deploy/history', label: '发布历史', icon: History },
    ]
  },
  {
    label: 'CMDB', icon: Server, items: [
      { to: '/cmdb/models', label: '模型管理', icon: Package },
      { to: '/cmdb/instances', label: '实例管理', icon: Server },
      { to: '/cmdb/relations', label: '关系管理', icon: GitBranch },
      { to: '/cmdb/discovery', label: '自动发现', icon: Scan },
      { to: '/cmdb/collection', label: '采集任务', icon: Radio },
      { to: '/cmdb/stats', label: '资源统计', icon: BarChart3 },
      { to: '/cmdb/idc', label: '机房机柜', icon: Building2 },
      { to: '/cmdb/app-arch', label: '应用架构', icon: Network },
      { to: '/cmdb/subscriptions', label: '消息订阅', icon: Bell },
      { to: '/cmdb/topology', label: '拓扑可视化', icon: Network },
      { to: '/cmdb/impact', label: '影响分析', icon: AlertTriangle },
      { to: '/cmdb/change-assess', label: '变更评估', icon: FileText },
      { to: '/cmdb/history', label: '版本追溯', icon: History },
    ]
  },
  {
    label: 'ITSM', icon: TicketCheck, items: [
      { to: '/itsm/overview', label: '总览', icon: LayoutDashboard },
      { to: '/itsm/tickets', label: '工单列表', icon: TicketCheck },
      {
        label: '服务管理', items: [
          { to: '/itsm/process', label: '流程定义', icon: GitBranch },
          { to: '/itsm/catalog', label: '服务目录定义', icon: Package },
          { to: '/itsm/sla', label: 'SLA 管理', icon: Clock },
        ]
      },
      { to: '/itsm/oncall', label: '值班管理', icon: Calendar },
    ]
  },
  {
    label: '监控中心', icon: Activity, items: [
      { to: '/monitor', label: '监控总览', icon: Activity },
      { to: '/monitor/alerts', label: '告警中心', icon: AlertTriangle },
      { to: '/monitor/targets', label: '监控配置', icon: Settings },
      { to: '/monitor/predict', label: '趋势预测', icon: TrendingUp },
    ]
  },
  {
    label: 'AI 助手', icon: Bot, items: [
      { to: '/ai', label: 'AI 对话', icon: MessagesSquare },
      { to: '/ai/scenes', label: '场景包', icon: Package },
      { to: '/ai/digital-employees', label: '数字员工', icon: Sparkles },
      { to: '/ai/inspections', label: '主动巡检', icon: Radar },
      { to: '/ai/teams', label: '智能体团队', icon: Users },
      { to: '/ai/impact', label: '运营效果', icon: Gauge },
      { to: '/ai/drills', label: '故障演练', icon: FlaskConical },
      { to: '/ai/analytics', label: '使用分析', icon: BarChart3 },
      { to: '/ai/settings', label: 'AI 设置', icon: Settings },
    ]
  },
  {
    label: '知识库', icon: BookOpen, items: [
      { to: '/knowledge', label: '文章列表', icon: BookOpen },
      { to: '/knowledge/new', label: 'AI 写作', icon: Sparkles },
    ]
  },
  {
    label: '系统管理', icon: Users, items: [
      { to: '/system/users', label: '用户管理', icon: Users },
      { to: '/system/groups', label: '用户组管理', icon: Users },
      { to: '/system/roles', label: '角色管理', icon: Shield },
      { to: '/system/auth', label: '认证设置', icon: Key },
      { to: '/system/notifications', label: '消息通知设置', icon: Bell },
      { to: '/system/settings', label: '全局参数', icon: Settings },
      { to: '/audit', label: '审计日志', icon: Shield },
    ]
  },
]

function isNavItem(v: NavItem | NavSubGroup): v is NavItem {
  return 'to' in v
}

function isNavSubGroup(v: NavItem | NavSubGroup): v is NavSubGroup {
  return 'items' in v && !('to' in v)
}

function isNavGroup(v: NavItem | NavGroup): v is NavGroup {
  return 'items' in v
}

/** 路由命中判定：要求完整路径段匹配，避免 /ai 被 /aixxx 之类误命中。 */
function pathMatches(pathname: string, to: string): boolean {
  return pathname === to || pathname.startsWith(to + '/')
}

/**
 * 在一组同级路由中选出唯一应高亮的项：命中且路径最长者胜出。
 * 解决父路由(如 /ai)与其子路由(如 /ai/inspections)同时高亮的问题。
 */
function activeRoute(pathname: string, routes: string[]): string | null {
  let best: string | null = null
  for (const r of routes) {
    if (pathMatches(pathname, r) && (best === null || r.length > best.length)) best = r
  }
  return best
}

/** 展开分组下的全部叶子路由(含二级子分组)。 */
function leafRoutes(group: NavGroup): string[] {
  return group.items.flatMap(i => (isNavItem(i) ? [i.to] : i.items.map(s => s.to)))
}

function NavGroupComponent({ group, collapsed, toggle }: { group: NavGroup; collapsed: boolean; toggle: () => void }) {
  const pathname = usePathname()
  // 组内唯一高亮项：按最长匹配选出，父子路由不再同时点亮
  const activeLeaf = activeRoute(pathname, leafRoutes(group))
  const isActive = activeLeaf !== null
  const Icon = group.icon
  const { theme } = useThemeStore()
  const [openSubs, setOpenSubs] = useState<Record<string, boolean>>({})

  const toggleSub = (label: string) => {
    setOpenSubs(prev => ({ ...prev, [label]: !prev[label] }))
  }

  return (
    <div>
      <button
        onClick={toggle}
        className={cn(
          'group flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-all duration-200 w-full text-left',
          isActive
            ? 'bg-emerald-50 text-emerald-600 border border-emerald-200 dark:bg-emerald-500/5 dark:text-emerald-400 dark:border-emerald-500/10'
            : theme === 'dark' ? 'text-[#94A3B8] hover:text-white hover:bg-white/[0.03] border border-transparent' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent'
        )}
      >
        <Icon className={cn('h-4 w-4', isActive && 'text-emerald-500 dark:text-emerald-400')} strokeWidth={1.5} />
        <span className="text-xs font-medium tracking-wide flex-1">{group.label}</span>
        <svg className={cn('h-3 w-3 transition-transform', !collapsed && 'rotate-90')} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path d="M9 5l7 7-7 7" /></svg>
      </button>
      {!collapsed && (
        <div className="ml-4 mt-0.5 space-y-0.5">
          {group.items.map((item, idx) => {
            if (isNavItem(item)) {
              const active = item.to === activeLeaf
              const Icon = item.icon
              return (
                <Link key={item.to} href={item.to}
                  className={cn(
                    'group flex items-center gap-2.5 px-3 py-1.5 rounded-md text-xs transition-all duration-200',
                    active
                      ? 'bg-emerald-50 text-emerald-600 border border-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-400 dark:border-emerald-500/20'
                      : theme === 'dark' ? 'text-[#94A3B8] hover:text-white hover:bg-white/[0.03] border border-transparent' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent'
                  )}>
                  <Icon className={cn('h-3.5 w-3.5', active && 'text-emerald-500 dark:text-emerald-400')} strokeWidth={1.5} />
                  <span className="font-medium tracking-wide">{item.label}</span>
                </Link>
              )
            }
            const isSubActive = item.items.some(sub => sub.to === activeLeaf)
            return (
              <div key={item.label}>
                <button
                  onClick={() => toggleSub(item.label)}
                  className={cn(
                    'group flex items-center gap-2.5 px-3 py-1.5 rounded-md text-xs transition-all duration-200 w-full text-left',
                    isSubActive
                      ? 'text-emerald-600 dark:text-emerald-400'
                      : theme === 'dark' ? 'text-[#94A3B8] hover:text-white' : 'text-gray-600 hover:text-gray-900'
                  )}
                >
                  <span className="font-medium tracking-wide flex-1">{item.label}</span>
                  <svg className={cn('h-3 w-3 transition-transform', openSubs[item.label] && 'rotate-90')} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path d="M9 5l7 7-7 7" /></svg>
                </button>
                {openSubs[item.label] && (
                  <div className="ml-3 mt-0.5 space-y-0.5">
                    {item.items.map(sub => {
                      const active = sub.to === activeLeaf
                      const Icon = sub.icon
                      return (
                        <Link key={sub.to} href={sub.to}
                          className={cn(
                            'group flex items-center gap-2.5 px-3 py-1.5 rounded-md text-xs transition-all duration-200',
                            active
                              ? 'bg-emerald-50 text-emerald-600 border border-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-400 dark:border-emerald-500/20'
                              : theme === 'dark' ? 'text-[#94A3B8] hover:text-white hover:bg-white/[0.03] border border-transparent' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent'
                          )}>
                          <Icon className={cn('h-3.5 w-3.5', active && 'text-emerald-500 dark:text-emerald-400')} strokeWidth={1.5} />
                          <span className="font-medium tracking-wide">{sub.label}</span>
                        </Link>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

export function Layout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname()
  const router = useRouter()
  const { user, logout } = useAuthStore()
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({ 'CMDB': true, 'ITSM': true })
  const { theme, toggle } = useThemeStore()

  const toggleGroup = (label: string) => {
    setCollapsed(prev => ({ ...prev, [label]: !prev[label] }))
  }

  return (
    <div className={cn("flex h-screen overflow-hidden", theme === 'dark' ? 'bg-[#0B0F19]' : 'bg-gray-50')}>
      <div className="fixed inset-0 pointer-events-none opacity-[0.03] bg-[url('data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIzMDAiIGhlaWdodD0iMzAwIj48ZmlsdGVyIGlkPSJmIj48ZmVUdXJidWxlbmNlIHR5cGU9ImZyYWN0YWxOb2lzZSIgYmFzZUZyZXF1ZW5jeT0iLjc0IiBudW1PY3RhdmVzPSIzIi8+PC9maWx0ZXI+PHJlY3Qgd2lkdGg9IjEwMCUiIGhlaWdodD0iMTAwJSIgZmlsdGVyPSJ1cmwoI2YpIiBvcGFjaXR5PSIwLjA0Ii8+PC9zdmc+')]" />

      <aside className={cn("relative z-10 w-60 flex flex-col border-r backdrop-blur-2xl overflow-y-auto", theme === 'dark' ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.8)]' : 'border-gray-200 bg-white/80')}>
        <div className={cn("h-14 flex items-center gap-2.5 px-5 border-b sticky top-0 backdrop-blur-2xl", theme === 'dark' ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.95)]' : 'border-gray-200 bg-gray-50/95')}>
          <div className="relative flex items-center justify-center w-7 h-7">
            <Hexagon className="w-7 h-7 text-emerald-400" strokeWidth={1.5} />
            <span className={cn("absolute text-[8px] font-bold font-mono", theme === 'dark' ? 'text-white' : 'text-[#0B0F19]')}>ON</span>
          </div>
          <div className="flex-1">
            <span className={cn("text-sm font-semibold tracking-tight", theme === 'dark' ? 'text-white' : 'text-gray-900')}>OpsOrigin</span>
            <p className="text-[10px] text-muted-foreground tracking-wider uppercase">Control Plane</p>
          </div>
          <button onClick={toggle} className={cn("p-1.5 rounded-md transition-colors", theme === 'dark' ? 'text-gray-400 hover:text-white hover:bg-white/10' : 'text-gray-500 hover:text-gray-900 hover:bg-gray-200')}>
            {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </button>
        </div>

        <nav className="flex-1 p-2 space-y-0.5 pt-4">
          {navGroups.map((item) => {
            if (isNavGroup(item)) {
              return (
                <NavGroupComponent
                  key={item.label}
                  group={item}
                  collapsed={!!collapsed[item.label]}
                  toggle={() => toggleGroup(item.label)}
                />
              )
            }
            const navItem = item as NavItem
            const isActive = navItem.to === '/dashboard' ? pathname === '/dashboard' : pathMatches(pathname, navItem.to)
            const Icon = navItem.icon
            return (
              <Link
                key={navItem.to}
                href={navItem.to}
                className={cn(
                  'group flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-all duration-200',
                  isActive
                    ? 'bg-emerald-50 text-emerald-600 border border-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-400 dark:border-emerald-500/20'
                    : theme === 'dark' ? 'text-[#94A3B8] hover:text-white hover:bg-white/[0.03] border border-transparent' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent'
                )}
              >
                <Icon className={cn('h-4 w-4', isActive && 'text-emerald-500 dark:text-emerald-400')} strokeWidth={1.5} />
                <span className="text-xs font-medium tracking-wide">{navItem.label}</span>
                {isActive && <div className="ml-auto w-1 h-1 rounded-full bg-emerald-500 dark:bg-emerald-400 animate-pulse-soft" />}
              </Link>
            )
          })}
        </nav>

        <div className={cn("px-4 py-3 border-t space-y-2 sticky bottom-0 backdrop-blur-2xl", theme === 'dark' ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.95)]' : 'border-gray-200 bg-white/95')}>
          {user && (
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 min-w-0">
                <div className="w-6 h-6 rounded-full bg-emerald-100 dark:bg-emerald-500/20 flex items-center justify-center shrink-0">
                  <User className="w-3 h-3 text-emerald-600 dark:text-emerald-400" />
                </div>
                <div className="min-w-0">
                  <p className={cn("text-xs font-medium truncate", theme === 'dark' ? 'text-white' : 'text-gray-900')}>{user.username}</p>
                  <p className="text-[10px] text-muted-foreground/60 font-mono uppercase">{user.role}</p>
                </div>
              </div>
              <button onClick={() => { logout(); router.push('/login') }} className="text-muted-foreground/40 hover:text-red-500 transition-colors p-1">
                <LogOut className="w-3.5 h-3.5" />
              </button>
            </div>
          )}
          <div className="flex items-center gap-2">
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-500 dark:bg-emerald-400 animate-pulse-soft" />
            <span className="text-[11px] text-muted-foreground font-mono tracking-tight">System Nominal</span>
          </div>
          <p className="text-[10px] text-muted-foreground/50 font-mono tracking-tight">v1.0.0 · build 2026.07</p>
        </div>
      </aside>

      <main className={cn("relative z-10 flex-1 overflow-auto", theme === 'dark' ? '' : 'bg-gray-50')} data-scroll-container>
        <div className="min-h-full">{children}</div>
      </main>
      <ScrollToTop />
      <AIAssistant />
    </div>
  )
}
