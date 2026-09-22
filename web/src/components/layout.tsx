
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useNavigate } from 'react-router-dom'
import usePathname from '@/lib/use-pathname'
import { cn } from '@/lib/utils'
import { Server, TicketCheck, Activity, FlaskConical, Bot, Shield, LayoutDashboard, Hexagon, LogOut, User, Terminal, Workflow, Clock, Search, BookOpen, Calendar, GitBranch, Package, Bell, Key, Blocks, Users, Settings, Sun, Moon, Scan, Bug, Download, PackagePlus, Network, AlertTriangle, History, FileText, Rocket, AppWindow, FileCheck2, Cloud, Play, Sparkles, Database, Radio, BarChart3, Building2, Camera, TrendingUp, Store, Cpu, Webhook, Mail, MessageSquare, FolderOpen, ShieldAlert, ShieldCheck, PanelLeftClose, PanelLeftOpen, type LucideIcon } from 'lucide-react'
import { useAuthStore } from '@/stores/auth'
import { useAccess } from '@/access'
import { MessageBell } from '@/components/message-bell'
import { useThemeStore } from '@/stores/theme'
import { ScrollToTop } from '@/components/ui/scroll-to-top'
import { Breadcrumb } from '@/components/ui/breadcrumb'
import LocaleSwitcher from '@/components/ui/locale-switcher'
import AIAssistant from '@/components/ai-assistant'
import PureMode from '@/components/pure-mode'
import { getAINavItems } from '@/components/ai-nav'
import { t, useLocale } from '@/lib/i18n'
import { useMemo } from 'react'

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
  // 三级子菜单默认收起；但当前路由命中该子组时自动展开，确保能暴露当前所处位置。
  const [openSubs, setOpenSubs] = useState<Record<string, boolean>>(() =>
    group.items.reduce<Record<string, boolean>>((acc, item) => {
      if (!isNavItem(item) && item.items.some(sub => pathMatches(pathname, sub.to))) {
        acc[item.label] = true
      }
      return acc
    }, {})
  )
  // 顶层分组：命中当前路由时强制展开，否则尊重用户的折叠状态。
  const expanded = isActive || !collapsed

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
            ? 'bg-blue-50 text-blue-600 border border-blue-200 dark:bg-blue-500/5 dark:text-blue-400 dark:border-blue-500/10'
            : theme === 'dark' ? 'text-[var(--secondary-foreground)] hover:text-white hover:bg-white/[0.03] border border-transparent' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent'
        )}
      >
        <Icon className={cn('h-4 w-4', isActive && 'text-blue-500 dark:text-blue-400')} strokeWidth={1.5} />
        <span className="text-xs font-medium tracking-wide flex-1">{group.label}</span>
        <svg className={cn('h-3 w-3 transition-transform', expanded && 'rotate-90')} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path d="M9 5l7 7-7 7" /></svg>
      </button>
      {expanded && (
        <div className="ml-4 mt-0.5 space-y-0.5">
          {group.items.map((item, idx) => {
            if (isNavItem(item)) {
              const active = item.to === activeLeaf
              const Icon = item.icon
              return (
                <Link key={item.to} to={item.to}
                  className={cn(
                    'group flex items-center gap-2.5 px-3 py-1.5 rounded-md text-xs transition-all duration-200',
                    active
                      ? 'bg-blue-50 text-blue-600 border border-blue-200 dark:bg-blue-500/10 dark:text-blue-400 dark:border-blue-500/20'
                      : theme === 'dark' ? 'text-[var(--secondary-foreground)] hover:text-white hover:bg-white/[0.03] border border-transparent' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent'
                  )}>
                  <Icon className={cn('h-3.5 w-3.5', active && 'text-blue-500 dark:text-blue-400')} strokeWidth={1.5} />
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
                      ? 'text-blue-600 dark:text-blue-400'
                      : theme === 'dark' ? 'text-[var(--secondary-foreground)] hover:text-white' : 'text-gray-600 hover:text-gray-900'
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
                        <Link key={sub.to} to={sub.to}
                          className={cn(
                            'group flex items-center gap-2.5 px-3 py-1.5 rounded-md text-xs transition-all duration-200',
                            active
                              ? 'bg-blue-50 text-blue-600 border border-blue-200 dark:bg-blue-500/10 dark:text-blue-400 dark:border-blue-500/20'
                              : theme === 'dark' ? 'text-[var(--secondary-foreground)] hover:text-white hover:bg-white/[0.03] border border-transparent' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent'
                          )}>
                          <Icon className={cn('h-3.5 w-3.5', active && 'text-blue-500 dark:text-blue-400')} strokeWidth={1.5} />
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
  const navigate = useNavigate()
  const { user, logout } = useAuthStore()
  const { theme, toggle } = useThemeStore()
  const locale = useLocale()
  const { canKnowledgeManage } = useAccess()
  const navGroups: (NavItem | NavGroup)[] = useMemo(() => [
    { to: '/dashboard', label: t('dashboard.title'), icon: LayoutDashboard },
    { to: '/reports', label: t('reports.title'), icon: BarChart3 },
    {
      label: t('nav.basicManagement'), icon: Settings, items: [
        { to: '/resources', label: t('resources.title'), icon: Server },
        { to: '/credentials', label: t('credentials.title'), icon: Key },
        { to: '/lowcode', label: t('lowcode.title'), icon: Blocks },
        { to: '/scripts', label: t('scripts.title'), icon: Terminal },
        { to: '/workflows', label: t('workflows.title'), icon: GitBranch },
      ]
    },
    {
      label: 'ITSM', icon: TicketCheck, items: [
        { to: '/itsm/overview', label: t('itsm.overview'), icon: LayoutDashboard },
        { to: '/itsm/portal', label: t('itsm.portal'), icon: Store },
        { to: '/itsm/tickets', label: t('itsm.tickets'), icon: TicketCheck },
        { to: '/itsm/change', label: t('itsm.change'), icon: Calendar },
        { to: '/itsm/change-assess', label: t('itsm.changeAssess'), icon: FileText },
        { to: '/itsm/reports', label: t('itsm.reports'), icon: BarChart3 },
        {
          label: t('nav.serviceManagement'), items: [
            { to: '/itsm/process', label: t('itsm.process'), icon: GitBranch },
            { to: '/itsm/catalog', label: t('itsm.catalog'), icon: Package },
            { to: '/itsm/sla-ola', label: t('itsm.slaOla'), icon: Clock },
            { to: '/itsm/sla-breached', label: t('itsm.slaBreached'), icon: AlertTriangle },
            { to: '/itsm/form-dicts', label: t('itsm.formDicts'), icon: Database },
          ]
        },
        { to: '/itsm/oncall', label: t('itsm.oncall'), icon: Calendar },
        { to: '/itsm/oncall/calendar', label: t('itsm.oncallCalendar'), icon: Calendar },
        { to: '/itsm/inbound', label: t('itsm.inbound'), icon: Webhook },
      ]
    },
    {
      label: t('knowledge.title'), icon: BookOpen, items: [
        { to: '/knowledge', label: t('knowledge.articleList'), icon: BookOpen },
        { to: '/knowledge/categories', label: t('knowledge.categories'), icon: FolderOpen },
        // 检索可观测(P1-2/P1-4): 零结果查询 = 知识盲区清单, 收敛到 knowledge:manage
        ...(canKnowledgeManage
          ? [
              { to: '/knowledge/insights', label: t('knowledge.insights'), icon: Activity },
              // 检索测试台(迭代一): 真实检索路径的逐步归因
              { to: '/knowledge/retrieval', label: t('knowledge.retrievalLab'), icon: FlaskConical },
            ]
          : []),
      ]
    },
    {
      label: t('cmdb.title'), icon: Server, items: [
        { to: '/cmdb', label: t('cmdb.overview'), icon: LayoutDashboard },
        { to: '/cmdb/instances', label: t('cmdb.instances'), icon: Server },
        { to: '/cmdb/stats', label: t('cmdb.stats'), icon: BarChart3 },
        {
          label: t('nav.relationsAndDeps'), icon: Network, items: [
            { to: '/cmdb/topology', label: t('cmdb.topology'), icon: Network },
            { to: '/cmdb/impact', label: t('cmdb.impact'), icon: AlertTriangle },
            { to: '/cmdb/snapshots', label: t('cmdb.snapshots'), icon: Camera },
          ]
        },
        { to: '/cmdb/idc', label: t('cmdb.idc'), icon: Building2 },
        {
          label: t('nav.dataGovernance'), icon: ShieldCheck, items: [
            { to: '/cmdb/quality', label: t('cmdb.quality'), icon: ShieldAlert },
            { to: '/cmdb/data-policy', label: t('cmdb.dataPolicy'), icon: ShieldCheck },
            { to: '/cmdb/usage', label: t('cmdb.usage'), icon: Activity },
          ]
        },
        {
          label: t('nav.ciSettings'), icon: Package, items: [
            { to: '/cmdb/models', label: t('cmdb.models'), icon: Package },
            { to: '/cmdb/auto-collect', label: t('cmdb.autoCollect'), icon: Radio },
            { to: '/cmdb/subscriptions', label: t('cmdb.subscriptions'), icon: Bell },
            { to: '/cmdb/history', label: t('cmdb.history'), icon: History },
          ]
        },
        {
          label: t('nav.advancedSearch'), icon: Search, items: [
            { to: '/cmdb/search', label: t('cmdb.search'), icon: Search },
            { to: '/cmdb/query', label: t('cmdb.query'), icon: GitBranch },
          ]
        },
      ]
    },
    {
      label: t('monitor.title'), icon: Activity, items: [
        { to: '/monitor', label: t('monitor.overview'), icon: Activity },
        { to: '/monitor/alerts', label: t('monitor.center'), icon: AlertTriangle },
        { to: '/monitor/collection', icon: Radio, label: t('monitor.collection') },
        { to: '/monitor/plugins', label: t('monitor.plugins'), icon: Blocks },
        { to: '/monitor/targets', label: t('monitor.configuration'), icon: Settings },
        { to: '/monitor/sources', label: t('monitor.sources'), icon: Database },
        { to: '/monitor/ingest', label: t('monitor.ingest'), icon: Webhook },
        { to: '/monitor/predict', label: t('monitor.predict'), icon: TrendingUp },
        { to: '/monitor/panorama', label: t('monitor.panorama'), icon: Activity },
      ]
    },
    {
      label: t('auto.title'), icon: Workflow, items: [
        { to: '/auto', label: t('auto.overview'), icon: Workflow },
        { to: '/auto/job', label: t('auto.job'), icon: Play },
        { to: '/schedules', label: t('schedules.title'), icon: Clock },
        { to: '/auto/inspection', label: t('auto.inspection'), icon: Scan },
        { to: '/auto/vuln-scan', label: t('auto.vulnScan'), icon: Bug },
        { to: '/auto/patch', label: t('auto.patch'), icon: Download },
        { to: '/auto/software', label: t('auto.software'), icon: PackagePlus },
      ]
    },
    {
      label: t('nav.deploy'), icon: Rocket, items: [
        { to: '/deploy/apps', label: t('deploy.apps'), icon: AppWindow },
        { to: '/deploy/releases', label: t('deploy.releases'), icon: FileCheck2 },
        { to: '/deploy/environments', label: t('deploy.environments'), icon: Cloud },
        { to: '/deploy/history', label: t('deploy.history'), icon: History },
      ]
    },
    {
      label: t('ai.assistant'), icon: Bot, items: getAINavItems(),
    },
    {
      label: t('system.title'), icon: Users, items: [
        { to: '/system/users', label: t('system.users'), icon: Users },
        { to: '/system/groups', label: t('system.groups'), icon: Users },
        { to: '/system/roles', label: t('system.roles'), icon: Shield },
        { to: '/system/auth', label: t('system.auth'), icon: Key },
        { to: '/system/api-keys', label: t('system.apiKeys'), icon: Key },
        { to: '/system/notifications', label: t('system.notifications'), icon: Bell },
        { to: '/system/settings', label: t('system.settings'), icon: Settings },
        { to: '/system/health', label: t('system.health'), icon: Activity },
        { to: '/system/collection-policy', label: t('system.collectionPolicy'), icon: Database },
        { to: '/audit', label: t('audit.title'), icon: Shield },
      ]
    },
  ], [locale, canKnowledgeManage])

  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {}
    for (const g of navGroups) if (isNavGroup(g)) init[g.label] = true
    return init
  })
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(() => {
    if (typeof window === 'undefined') return false
    try { return localStorage.getItem('opsorigin_sidebar_collapsed') === '1' } catch { return false }
  })
  const applySidebarCollapsed = (v: boolean) => {
    setSidebarCollapsed(v)
    try { localStorage.setItem('opsorigin_sidebar_collapsed', v ? '1' : '0') } catch { /* ignore */ }
  }
  const [pureMode, setPureMode] = useState<boolean>(() => {
    if (typeof window === 'undefined') return false
    try {
      if (localStorage.getItem('opsorigin_pure_default') === '1') return true
      return localStorage.getItem('opsorigin_pure_mode') === '1'
    } catch {
      return false
    }
  })
  const applyPure = (v: boolean) => {
    setPureMode(v)
    try { localStorage.setItem('opsorigin_pure_mode', v ? '1' : '0') } catch { /* ignore */ }
  }

  const toggleGroup = (label: string) => {
    setCollapsed(prev => ({ ...prev, [label]: !prev[label] }))
  }

  if (pureMode) {
    return <PureMode onExitPure={() => applyPure(false)} />
  }

  return (
    <div className={cn("flex h-screen overflow-hidden", theme === 'dark' ? 'bg-[var(--background)]' : 'bg-gray-50')}>
      <div className="fixed inset-0 pointer-events-none opacity-[0.03] bg-[url('data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIzMDAiIGhlaWdodD0iMzAwIj48ZmlsdGVyIGlkPSJmIj48ZmVUdXJidWxlbmNlIHR5cGU9ImZyYWN0YWxOb2lzZSIgYmFzZUZyZXF1ZW5jeT0iLjc0IiBudW1PY3RhdmVzPSIzIi8+PC9maWx0ZXI+PHJlY3Qgd2lkdGg9IjEwMCUiIGhlaWdodD0iMTAwJSIgZmlsdGVyPSJ1cmwoI2YpIiBvcGFjaXR5PSIwLjA0Ii8+PC9zdmc+')]" />

      <aside className={cn("relative z-10 flex flex-col border-r backdrop-blur-2xl overflow-y-auto sticky top-0 h-screen shrink-0 transition-[width] duration-200", sidebarCollapsed ? 'w-14' : 'w-60', theme === 'dark' ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.8)]' : 'border-gray-200 bg-white/80')}>
        <div className={cn("h-14 flex items-center gap-2.5 border-b sticky top-0 backdrop-blur-2xl", sidebarCollapsed ? 'px-2 justify-center' : 'px-5', theme === 'dark' ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.95)]' : 'border-gray-200 bg-gray-50/95')}>
          {sidebarCollapsed ? (
            <button onClick={() => applySidebarCollapsed(false)} className={cn("p-1.5 rounded-md transition-colors", theme === 'dark' ? 'text-gray-400 hover:text-white hover:bg-white/10' : 'text-gray-500 hover:text-gray-900 hover:bg-gray-200')} title="展开菜单">
              <PanelLeftOpen className="h-4 w-4" />
            </button>
          ) : (
            <>
              <div className="relative flex items-center justify-center w-7 h-7 shrink-0">
                <Hexagon className="w-7 h-7 text-blue-400" strokeWidth={1.5} />
                <span className={cn("absolute text-[8px] font-bold font-mono", theme === 'dark' ? 'text-white' : 'text-[var(--primary-foreground)]')}>ON</span>
              </div>
              <div className="flex-1 min-w-0">
                <span className={cn("block text-sm font-semibold tracking-tight truncate", theme === 'dark' ? 'text-white' : 'text-gray-900')}>OpsOrigin</span>
                <p className="text-[10px] text-muted-foreground tracking-wider uppercase truncate">AI-Native Ops</p>
              </div>
              <button onClick={() => applySidebarCollapsed(true)} className={cn("p-1.5 rounded-md transition-colors shrink-0", theme === 'dark' ? 'text-gray-400 hover:text-white hover:bg-white/10' : 'text-gray-500 hover:text-gray-900 hover:bg-gray-200')} title="收起菜单">
                <PanelLeftClose className="h-4 w-4" />
              </button>
            </>
          )}
        </div>

        <nav className={cn("flex-1 space-y-0.5 pt-4", sidebarCollapsed ? 'px-1.5' : 'p-2')}>
          {navGroups.map((item) => {
            if (sidebarCollapsed) {
              // 图标窄条模式: 顶层项直接跳转; 分组点击先展开侧栏再选
              const Icon = isNavGroup(item) ? item.icon : (item as NavItem).icon
              const isActive = isNavGroup(item)
                ? leafRoutes(item).some(r => pathMatches(pathname, r))
                : (() => { const to = (item as NavItem).to; return to === '/dashboard' ? pathname === '/dashboard' : pathMatches(pathname, to) })()
              return (
                <button
                  key={isNavGroup(item) ? item.label : (item as NavItem).to}
                  onClick={() => applySidebarCollapsed(false)}
                  title={isNavGroup(item) ? item.label : (item as NavItem).label}
                  className={cn(
                    'flex items-center justify-center w-full py-2 rounded-lg transition-all duration-200',
                    isActive
                      ? 'bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-400'
                      : theme === 'dark' ? 'text-[var(--secondary-foreground)] hover:text-white hover:bg-white/[0.06]' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100'
                  )}
                >
                  <Icon className="h-4 w-4" strokeWidth={1.5} />
                </button>
              )
            }
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
              <Link key={navItem.to}
                to={navItem.to}
                className={cn(
                  'group flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-all duration-200',
                  isActive
                    ? 'bg-blue-50 text-blue-600 border border-blue-200 dark:bg-blue-500/10 dark:text-blue-400 dark:border-blue-500/20'
                    : theme === 'dark' ? 'text-[var(--secondary-foreground)] hover:text-white hover:bg-white/[0.03] border border-transparent' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent'
                )}
              >
                <Icon className={cn('h-4 w-4', isActive && 'text-blue-500 dark:text-blue-400')} strokeWidth={1.5} />
                <span className="text-xs font-medium tracking-wide">{navItem.label}</span>
                {isActive && <div className="ml-auto w-1 h-1 rounded-full bg-blue-500 dark:bg-blue-400 animate-pulse-soft" />}
              </Link>
            )
          })}
        </nav>

        <div className={cn("border-t space-y-2 sticky bottom-0 backdrop-blur-2xl", sidebarCollapsed ? 'px-1.5 py-3' : 'px-4 py-3', theme === 'dark' ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.95)]' : 'border-gray-200 bg-white/95')}>
          {user && (
            <div className={cn("flex items-center", sidebarCollapsed ? 'flex-col gap-1.5' : 'justify-between')}>
              <div className={cn("flex items-center gap-2", sidebarCollapsed && 'flex-col')} title={sidebarCollapsed ? `${user.username} (${user.role})` : undefined}>
                <div className="w-6 h-6 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
                  <User className="w-3 h-3 text-blue-600 dark:text-blue-400" />
                </div>
                {!sidebarCollapsed && (
                  <div className="min-w-0">
                    <p className={cn("text-xs font-medium truncate", theme === 'dark' ? 'text-white' : 'text-gray-900')}>{user.username}</p>
                    <p className="text-[10px] text-muted-foreground/60 font-mono uppercase">{user.role}</p>
                  </div>
                )}
              </div>
              <div className={cn("flex items-center", sidebarCollapsed ? 'flex-col gap-1.5' : 'gap-1')}>
                <MessageBell />
                <button onClick={toggle} className={cn("p-1 rounded-md transition-colors", theme === 'dark' ? 'text-gray-400 hover:text-white hover:bg-white/10' : 'text-gray-500 hover:text-gray-900 hover:bg-gray-200')} title={theme === 'dark' ? '切换亮色' : '切换暗色'}>
                  {theme === 'dark' ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
                </button>
                <LocaleSwitcher />
                <button onClick={() => { logout(); navigate('/login') }} className="text-muted-foreground/40 hover:text-red-500 transition-colors p-1" title="退出登录">
                  <LogOut className="w-3.5 h-3.5" />
                </button>
              </div>
            </div>
          )}
          {sidebarCollapsed ? (
            <button
              onClick={() => applyPure(true)}
              className="w-full flex items-center justify-center py-1.5 rounded-lg text-white bg-blue-500 hover:bg-blue-600 transition-all duration-200 shadow-sm"
              title="切换到纯净模式（AI 工作界面）"
            >
              <Sparkles className="h-3.5 w-3.5" strokeWidth={1.5} />
            </button>
          ) : (
            <button
              onClick={() => applyPure(true)}
              className="w-full flex items-center justify-center gap-1.5 py-1.5 rounded-lg text-[11px] font-semibold text-white bg-blue-500 hover:bg-blue-600 transition-all duration-200 shadow-sm"
              title="切换到纯净模式（AI 工作界面）"
            >
              <Sparkles className="h-3.5 w-3.5" strokeWidth={1.5} /> 纯净模式
            </button>
          )}
          {!sidebarCollapsed && (
            <p className="text-[10px] text-muted-foreground/50 font-mono tracking-tight">
              v{__APP_VERSION__} · {__BUILD_TIME__}
            </p>
          )}
        </div>
      </aside>

      <main className={cn("relative z-10 flex-1 overflow-auto", theme === 'dark' ? '' : 'bg-gray-50')} data-scroll-container>
        <div className="min-h-full">
          <div className={cn("px-6 py-3 border-b sticky top-0 z-20 backdrop-blur-xl", theme === 'dark' ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.8)]' : 'border-gray-200 bg-gray-50/80')}>
            <Breadcrumb />
          </div>
          {children}
        </div>
        <ScrollToTop />
      </main>
      <AIAssistant />
    </div>
  )
}
