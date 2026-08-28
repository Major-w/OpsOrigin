
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMonitorStore } from '@/stores/monitor'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag } from 'antd'
import { formatTime } from '@/lib/utils'
import { useWebSocket } from '@/lib/useWebSocket'
import { Activity, AlertTriangle, BellRing, Settings2, Cable, ArrowRight, Loader2 } from 'lucide-react'

const severityColor: Record<string, string> = { critical: 'red', high: 'red', warning: 'orange', info: 'default' }
const severityLabel: Record<string, string> = { critical: '严重', high: '严重', warning: '警告', info: '提示' }

export default function MonitorOverviewPage() {
  const store = useMonitorStore()
  const { targets, alerts, rules, alertStats, loading } = store
  const [scrolling, setScrolling] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)

  const refresh = () => {
    store.fetchTargets()
    store.fetchAlerts()
    store.fetchAlertStats()
    store.fetchRules()
  }

  useEffect(() => { refresh() }, [])

  useWebSocket({
    onEvent: (data: any) => {
      if (data.type === 'alert') refresh()
    },
  })

  useEffect(() => {
    if (!scrolling || alerts.length <= 1) return
    const timer = setInterval(() => {
      if (scrollRef.current) {
        const el = scrollRef.current
        const maxScroll = el.scrollHeight - el.clientHeight
        if (el.scrollTop >= maxScroll - 4) el.scrollTo({ top: 0, behavior: 'smooth' })
        else el.scrollBy({ top: 60, behavior: 'smooth' })
      }
    }, 2000)
    return () => clearInterval(timer)
  }, [scrolling, alerts.length])

  const shortcuts = [
    { to: '/monitor/alerts', icon: AlertTriangle, title: '告警中心', desc: '处理活跃告警、查看处理历史轨迹', accent: 'text-red-500' },
    { to: '/monitor/targets', icon: Settings2, title: '监控配置', desc: '采集目标、手动探测、指标与告警规则', accent: 'text-blue-500' },
  ]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2"><Activity className="h-6 w-6 text-blue-500" />监控总览</h1>
          <p className="text-sm text-muted-foreground">主动探测与智能告警 — 值班第一眼</p>
        </div>
        <Button onClick={refresh} disabled={loading}>
          {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin mr-1" /> : <Activity className="h-3.5 w-3.5 mr-1" />}刷新
        </Button>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">监控目标</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{targets.length}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">活跃告警</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{alertStats.active_alerts || 0}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">严重告警</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-destructive">{alertStats.critical || 0}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">告警规则</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{rules.length}</span></CardContent>
        </Card>
      </div>

      <div className="grid md:grid-cols-2 gap-3">
        {shortcuts.map(s => {
          const Icon = s.icon
          return (
            <Link key={s.to} to={s.to} className="group">
              <Card className="hover:shadow-md transition-all hover:border-blue-300 dark:hover:border-blue-500/40">
                <CardContent className="p-4 flex items-center gap-4">
                  <div className={`h-10 w-10 rounded-lg bg-gray-100 dark:bg-white/[0.04] flex items-center justify-center ${s.accent} group-hover:scale-110 transition-transform`}>
                    <Icon className="h-5 w-5" />
                  </div>
                  <div className="flex-1">
                    <div className="font-medium">{s.title}</div>
                    <div className="text-xs text-muted-foreground">{s.desc}</div>
                  </div>
                  <ArrowRight className="h-4 w-4 text-muted-foreground group-hover:translate-x-1 transition-transform" />
                </CardContent>
              </Card>
            </Link>
          )
        })}
      </div>

      <Card>
        <CardHeader className="pb-2 flex-row items-center justify-between">
          <CardTitle className="text-base flex items-center gap-2"><BellRing className="h-4 w-4 text-red-500" />活跃告警滚动</CardTitle>
          <label className="flex items-center gap-1.5 text-xs text-muted-foreground cursor-pointer">
            <input type="checkbox" checked={scrolling} onChange={e => setScrolling(e.target.checked)} className="accent-blue-500" />
            自动滚动
          </label>
        </CardHeader>
        <CardContent className="p-0">
          {alerts.length === 0 ? (
            <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无活跃告警 🎉</p>
          ) : (
            <div
              ref={scrollRef}
              onMouseEnter={() => setScrolling(false)}
              onMouseLeave={() => setScrolling(true)}
              className="max-h-72 overflow-y-auto"
            >
              <table className="w-full text-sm">
                <thead><tr className="border-b text-left text-muted-foreground sticky top-0 bg-background">
                  <th className="pb-2 px-4 font-medium">严重度</th>
                  <th className="pb-2 px-4 font-medium">标题</th>
                  <th className="pb-2 px-4 font-medium hidden md:table-cell">规则</th>
                  <th className="pb-2 px-4 font-medium hidden md:table-cell">源 CI</th>
                  <th className="pb-2 px-4 font-medium">触发时间</th>
                </tr></thead>
                <tbody>
                  {alerts.map(a => (
                    <tr key={a.id} className="border-b last:border-0">
                      <td className="py-2 px-4"><Tag color={severityColor[a.severity] || 'default'} className="text-xs">{severityLabel[a.severity] || a.severity}</Tag></td>
                      <td className="py-2 px-4 max-w-xs truncate">{a.title}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs hidden md:table-cell">{a.rule_name}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs hidden md:table-cell">{a.source_ci_id || '-'}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs whitespace-nowrap">{formatTime(a.triggered_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      <div className="flex items-start gap-2 text-xs p-3 rounded-lg bg-blue-50 dark:bg-blue-500/5 border border-blue-200 dark:border-blue-500/10">
        <Cable className="h-4 w-4 text-blue-500 shrink-0 mt-0.5" />
        <div className="text-blue-600 dark:text-blue-400">
          <span className="font-medium">规划中的能力：</span>
          采集插件库、多步骤服务拨测、第三方告警接入、告警转义、告警收敛/压缩、通知升级策略 — 将在告警中心与监控配置页逐步开放。
        </div>
      </div>
    </div>
  )
}
