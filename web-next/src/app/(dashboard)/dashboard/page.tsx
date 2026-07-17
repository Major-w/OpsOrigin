'use client'

import { useEffect, useState, useRef } from 'react'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Server, TicketCheck, Activity, Bot, Cpu, AlertTriangle, Wifi, HardDrive,
  Package, GitBranch, Clock, Shield, TrendingUp, TrendingDown, ArrowUpRight,
  Layers, Search, BarChart3, Zap, CheckCircle, XCircle, Timer, Users, Workflow
} from 'lucide-react'
import { useWebSocket } from '@/lib/useWebSocket'
import { apiFetch } from '@/lib/utils'

function AnimatedCounter({ value, label }: { value: number; label: string }) {
  const [display, setDisplay] = useState(0)
  const ref = useRef<HTMLDivElement>(null)
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) { setVisible(true); observer.disconnect() }
    }, { threshold: 0.1 })
    if (ref.current) observer.observe(ref.current)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    if (!visible) return
    const duration = 1200
    const steps = 30
    const increment = value / steps
    let current = 0
    const timer = setInterval(() => {
      current += increment
      if (current >= value) { setDisplay(value); clearInterval(timer) }
      else setDisplay(Math.round(current))
    }, duration / steps)
    return () => clearInterval(timer)
  }, [visible, value])

  return (
    <div ref={ref}>
      <div className="metric-value text-3xl sm:text-4xl text-gray-900 dark:text-white">{display}</div>
      <div className="text-[11px] text-muted-foreground/60 mt-1 font-mono tracking-tight">{label}</div>
    </div>
  )
}

function MetricCard({ icon: Icon, title, value, label, trend, trendUp, color = 'emerald' }: {
  icon: any; title: string; value: number; label: string; trend?: string; trendUp?: boolean; color?: string
}) {
  return (
    <Card className="hover:border-emerald-500/20 transition-all duration-200">
      <CardContent className="p-4">
        <div className="flex items-start justify-between">
          <div>
            <p className="text-xs text-muted-foreground font-medium">{title}</p>
            <p className="metric-value text-2xl text-gray-900 dark:text-white mt-1">{value}</p>
            <p className="text-[11px] text-muted-foreground/60 mt-0.5">{label}</p>
          </div>
          <div className={`w-10 h-10 rounded-lg flex items-center justify-center bg-${color}-50 dark:bg-${color}-500/10`}>
            <Icon className={`w-5 h-5 text-${color}-500 dark:text-${color}-400`} />
          </div>
        </div>
        {trend && (
          <div className="flex items-center gap-1 mt-3 text-[11px]">
            {trendUp ? (
              <TrendingUp className="w-3 h-3 text-emerald-500" />
            ) : (
              <TrendingDown className="w-3 h-3 text-red-500" />
            )}
            <span className={trendUp ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'}>{trend}</span>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function StatusDot({ status }: { status: string }) {
  const colors: Record<string, string> = { nominal: 'bg-emerald-400', warning: 'bg-amber-400', critical: 'bg-red-400' }
  return (
    <span className={`inline-block w-1.5 h-1.5 rounded-full ${colors[status] || 'bg-emerald-400'} ${status === 'nominal' ? 'animate-pulse-ring' : status === 'critical' ? 'animate-pulse-soft' : ''}`} />
  )
}

function DataFlowBar() {
  return (
    <div className="relative h-px w-full bg-gray-200 dark:bg-[rgba(255,255,255,0.04)] overflow-hidden">
      <div className="absolute inset-0 w-1/3 bg-gradient-to-r from-transparent via-emerald-400/40 to-transparent animate-[data-flow_2s_linear_infinite]" />
    </div>
  )
}

function BarChart({ data }: { data: { label: string; value: number; color?: string }[] }) {
  const max = Math.max(...data.map(d => d.value), 1)
  return (
    <div className="flex items-end gap-2 h-32">
      {data.map((d, i) => (
        <div key={i} className="flex-1 flex flex-col items-center gap-1">
          <span className="text-[10px] text-muted-foreground font-mono">{d.value}</span>
          <div
            className={`w-full rounded-t transition-all duration-500 ${d.color || 'bg-emerald-400 dark:bg-emerald-500'}`}
            style={{ height: `${(d.value / max) * 100}%`, minHeight: '4px' }}
          />
          <span className="text-[10px] text-muted-foreground/60 truncate w-full text-center">{d.label}</span>
        </div>
      ))}
    </div>
  )
}

export default function DashboardPage() {
  const [time, setTime] = useState(new Date())
  const [activeTab, setActiveTab] = useState('overview')

  // Core metrics
  const [metrics, setMetrics] = useState({
    cmdb: 0, targets: 0, alerts: 0, agents: 0,
    tickets: 0, openTickets: 0, sla: 98.5,
    deployments: 0, successRate: 0,
  })

  const [alerts, setAlerts] = useState<{ id: string; ci: string; severity: string; message: string; time: string }[]>([
    { id: 'ALT-001', ci: 'prod-db-01', severity: 'critical', message: 'Connection pool exhausted (>90%)', time: '2m ago' },
    { id: 'ALT-002', ci: 'api-gateway-02', severity: 'warning', message: 'P99 latency spike 320ms', time: '5m ago' },
    { id: 'ALT-003', ci: 'redis-cluster', severity: 'info', message: 'Failover completed', time: '12m ago' },
  ])

  const [traces, setTraces] = useState<{ trace: string; action: string; actor: string; status: string }[]>([
    { trace: '7a3f...b1e2', action: 'cmdb.ci.update', actor: 'AI_AGENT', status: 'success' },
    { trace: '9c1d...f4a8', action: 'itsm.ticket.approve', actor: 'HUMAN', status: 'success' },
    { trace: '4e5f...2c7d', action: 'monitor.probe.run', actor: 'AI_AGENT', status: 'success' },
  ])

  useEffect(() => { const t = setInterval(() => setTime(new Date()), 1000); return () => clearInterval(t) }, [])

  useEffect(() => {
    Promise.allSettled([
      apiFetch<any[]>('/cmdb/ci').then(d => setMetrics(m => ({ ...m, cmdb: d.length }))).catch(() => {}),
      apiFetch<any[]>('/monitor/targets').then(d => setMetrics(m => ({ ...m, targets: d.length }))).catch(() => {}),
      apiFetch<any[]>('/monitor/alerts').then(d => {
        setMetrics(m => ({ ...m, alerts: d.length }))
        setAlerts(d.slice(0, 5).map((a: any) => ({ id: a.id, ci: a.ci, severity: a.severity || 'info', message: a.message, time: 'now' })))
      }).catch(() => {}),
      apiFetch<any[]>('/ai/agents').then(d => setMetrics(m => ({ ...m, agents: d.length }))).catch(() => {}),
      apiFetch<any[]>('/itsm/tickets').then(d => setMetrics(m => ({ ...m, tickets: d.length, openTickets: d.filter((t: any) => t.status === 'open').length }))).catch(() => {}),
    ])
  }, [])

  useWebSocket({
    onEvent: (data) => {
      if (data.type === 'alert') {
        setAlerts(prev => [{ id: data.payload.id, ci: data.payload.ci, severity: data.payload.severity, message: data.payload.message, time: 'just now' }, ...prev].slice(0, 10))
        setMetrics(m => ({ ...m, alerts: m.alerts + 1 }))
      }
      if (data.type === 'audit') {
        setTraces(prev => [{ trace: data.payload.trace_id?.slice(0, 8) + '...', action: data.payload.action, actor: data.payload.actor, status: data.payload.success ? 'success' : 'failed' }, ...prev].slice(0, 10))
      }
    },
  })

  return (
    <div className="p-6 space-y-6 max-w-7xl mx-auto">
      {/* Header */}
      <div className="flex items-center justify-between animate-fade-in-up">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-xl font-semibold text-gray-900 dark:text-white tracking-tight">Dashboard</h1>
            <Badge variant="success" pulse>System Nominal</Badge>
          </div>
          <p className="text-sm text-muted-foreground/60 mt-1 font-mono tracking-tight">
            {time.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' })}
            {' · '}
            {time.toLocaleTimeString('zh-CN', { hour12: false })}
            {' UTC+8'}
          </p>
        </div>
        <div className="flex items-center gap-4 text-xs text-muted-foreground/50 font-mono tracking-tight">
          <span>uptime 14d 7h</span>
          <span className="w-px h-3 bg-gray-200 dark:bg-[rgba(255,255,255,0.06)]" />
          <span>sessions 3</span>
        </div>
      </div>

      <DataFlowBar />

      {/* Tabs */}
      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList className="bg-gray-100 dark:bg-[rgba(255,255,255,0.04)] p-1">
          <TabsTrigger value="overview">汇总</TabsTrigger>
          <TabsTrigger value="cmdb"><Server className="h-3 w-3 mr-1" />CMDB</TabsTrigger>
          <TabsTrigger value="itsm"><TicketCheck className="h-3 w-3 mr-1" />ITSM</TabsTrigger>
          <TabsTrigger value="release"><GitBranch className="h-3 w-3 mr-1" />应用发布</TabsTrigger>
          <TabsTrigger value="monitor"><Activity className="h-3 w-3 mr-1" />监控</TabsTrigger>
          <TabsTrigger value="automation"><Workflow className="h-3 w-3 mr-1" />自动化运维</TabsTrigger>
        </TabsList>

        {/* ============ 汇总页签 ============ */}
        <TabsContent value="overview" className="mt-6 space-y-6">
          {/* Top KPI */}
          <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
            <MetricCard icon={Server} title="CMDB 资产" value={metrics.cmdb} label="已录入资产" trend="+12% 较上月" trendUp color="violet" />
            <MetricCard icon={Wifi} title="监控目标" value={metrics.targets} label="在线目标" trend="稳定" trendUp color="emerald" />
            <MetricCard icon={AlertTriangle} title="活跃告警" value={metrics.alerts} label="待处理告警" trend={metrics.alerts > 0 ? `${metrics.alerts} 条待处理` : '无告警'} trendUp={metrics.alerts === 0} color="amber" />
            <MetricCard icon={Bot} title="AI Agents" value={metrics.agents} label="运行中" trend="+2 本周新增" trendUp color="blue" />
          </div>

          {/* Second row: ITSM + Deploy */}
          <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
            <MetricCard icon={TicketCheck} title="ITSM 工单" value={metrics.tickets} label="总工单" trend={`${metrics.openTickets} 个待处理`} trendUp={metrics.openTickets === 0} color="cyan" />
            <MetricCard icon={Shield} title="SLA 达标率" value={Math.round(metrics.sla)} label="%" trend="目标 99%" trendUp={metrics.sla >= 99} color="emerald" />
            <MetricCard icon={GitBranch} title="应用发布" value={metrics.deployments} label="本月发布" trend={`${metrics.successRate}% 成功率`} trendUp color="pink" />
            <MetricCard icon={Zap} title="自动化率" value={87} label="%" trend="+5% 较上月" trendUp color="orange" />
          </div>

          {/* Middle: System Health + Alerts */}
          <div className="grid gap-4 lg:grid-cols-3">
            {/* System Health */}
            <Card className="lg:col-span-2">
              <CardHeader className="py-3">
                <div className="flex items-center justify-between">
                  <CardTitle className="flex items-center gap-2">
                    <Cpu className="w-3.5 h-3.5 text-muted-foreground/60" strokeWidth={1.5} />
                    系统健康
                  </CardTitle>
                  <span className="text-[11px] text-muted-foreground/50 font-mono">updated 12s ago</span>
                </div>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                  {[
                    { label: 'API Latency', value: '2.4ms', status: 'nominal' as const },
                    { label: 'DB Connections', value: '12/50', status: 'nominal' as const },
                    { label: 'Dgraph QPS', value: '1.2k', status: 'nominal' as const },
                    { label: 'Memory Usage', value: '342MB', status: 'nominal' as const },
                  ].map(m => (
                    <div key={m.label} className="p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                      <div className="text-[11px] text-muted-foreground/60 font-mono tracking-tight">{m.label}</div>
                      <div className="flex items-center gap-2 mt-1.5">
                        <span className="metric-value text-lg text-gray-900 dark:text-white">{m.value}</span>
                        <StatusDot status={m.status} />
                      </div>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>

            {/* Recent Alerts */}
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="flex items-center gap-2">
                  <AlertTriangle className="w-3.5 h-3.5 text-muted-foreground/60" strokeWidth={1.5} />
                  最近告警
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                {alerts.map((alert, i) => (
                  <div key={alert.id} className={`flex items-start gap-3 p-2.5 rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.04)] bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] animate-slide-in-right stagger-${i + 1}`}>
                    <div className={`mt-1 w-2 h-2 rounded-full flex-shrink-0 ${
                      alert.severity === 'critical' ? 'bg-red-400 animate-pulse-soft' :
                      alert.severity === 'warning' ? 'bg-amber-400' : 'bg-emerald-400'
                    }`} />
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-xs font-mono font-medium text-gray-900 dark:text-white">{alert.ci}</span>
                        <Badge variant={alert.severity === 'critical' ? 'destructive' : alert.severity === 'warning' ? 'warning' : 'default'}>{alert.severity}</Badge>
                      </div>
                      <p className="text-[11px] text-muted-foreground/70 mt-0.5 truncate">{alert.message}</p>
                      <p className="text-[10px] text-muted-foreground/40 font-mono mt-0.5">{alert.time}</p>
                    </div>
                  </div>
                ))}
              </CardContent>
            </Card>
          </div>

          {/* Bottom: Trace + Pipeline */}
          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="flex items-center gap-2">
                  <Activity className="w-3.5 h-3.5 text-muted-foreground/60" strokeWidth={1.5} />
                  操作流水
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="space-y-1">
                  <div className="grid grid-cols-[2fr_1.5fr_1fr_1fr] gap-2 text-[10px] text-muted-foreground/40 font-mono uppercase tracking-wider px-2 pb-2 border-b border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                    <span>Trace ID</span><span>Action</span><span>Actor</span><span>Status</span>
                  </div>
                  {traces.map((e, i) => (
                    <div key={e.trace} className={`grid grid-cols-[2fr_1.5fr_1fr_1fr] gap-2 items-center px-2 py-2 rounded-md text-xs border-b border-gray-50 dark:border-[rgba(255,255,255,0.02)] hover:bg-gray-50 dark:hover:bg-[rgba(255,255,255,0.02)] transition-colors animate-slide-in-right stagger-${i + 1}`}>
                      <span className="font-mono text-emerald-500/80 dark:text-emerald-400/80 text-[11px] truncate">{e.trace}</span>
                      <span className="font-mono text-[11px] text-gray-700 dark:text-white/70 truncate">{e.action}</span>
                      <Badge variant="default">{e.actor}</Badge>
                      <span className="text-emerald-500/80 dark:text-emerald-400/80 font-mono text-[11px]">{e.status}</span>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="py-3">
                <CardTitle className="flex items-center gap-2">
                  <HardDrive className="w-3.5 h-3.5 text-muted-foreground/60" strokeWidth={1.5} />
                  自愈管道
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="relative">
                  {[
                    { step: 'Monitor', status: 'active', desc: '指标采集正常' },
                    { step: 'Detect', status: 'active', desc: '异常检测待命中' },
                    { step: 'Analyze', status: 'idle', desc: 'AI 分析就绪' },
                    { step: 'Execute', status: 'idle', desc: '执行器待命中' },
                    { step: 'Verify', status: 'idle', desc: '自愈验证已就绪' },
                  ].map((p, i) => (
                    <div key={p.step} className="flex items-center gap-4 py-3 group animate-fade-in-up" style={{ animationDelay: `${i * 0.08}s` }}>
                      <div className="flex flex-col items-center">
                        <div className={`w-3 h-3 rounded-full border-2 transition-all duration-500 ${
                          p.status === 'active'
                            ? 'border-emerald-400 bg-emerald-400/20 shadow-[0_0_10px_rgba(52,211,153,0.3)] animate-pulse-ring'
                            : 'border-gray-300 dark:border-[rgba(255,255,255,0.15)] bg-transparent'
                        }`} />
                        {i < 4 && <div className={`w-px h-5 ${p.status === 'active' ? 'bg-emerald-400/30' : 'bg-gray-200 dark:bg-[rgba(255,255,255,0.06)]'}`} />}
                      </div>
                      <div className="flex-1">
                        <div className="flex items-center gap-2">
                          <span className={`text-sm font-medium ${p.status === 'active' ? 'text-gray-900 dark:text-white' : 'text-muted-foreground/60'}`}>{p.step}</span>
                          {p.status === 'active' && <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse-soft" />}
                        </div>
                        <p className="text-[11px] text-muted-foreground/50 font-mono mt-0.5">{p.desc}</p>
                      </div>
                      <Badge variant={p.status === 'active' ? 'success' : 'secondary'} className="text-[10px]">{p.status}</Badge>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          </div>
        </TabsContent>

        {/* ============ CMDB 页签 ============ */}
        <TabsContent value="cmdb" className="mt-6 space-y-6">
          <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
            <MetricCard icon={Server} title="资产总数" value={metrics.cmdb} label="已录入" color="violet" />
            <MetricCard icon={Layers} title="模型数量" value={12} label="已定义模型" color="blue" />
            <MetricCard icon={Search} title="今日查询" value={342} label="次搜索" color="emerald" />
            <MetricCard icon={TrendingUp} title="资产变更" value={28} label="本周变更" color="amber" />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">资产类型分布</CardTitle>
              </CardHeader>
              <CardContent>
                <BarChart data={[
                  { label: '服务器', value: 156, color: 'bg-violet-400' },
                  { label: '数据库', value: 42, color: 'bg-blue-400' },
                  { label: '网络设备', value: 89, color: 'bg-emerald-400' },
                  { label: '中间件', value: 67, color: 'bg-amber-400' },
                  { label: '应用服务', value: 124, color: 'bg-pink-400' },
                  { label: '存储', value: 31, color: 'bg-cyan-400' },
                ]} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">最近资产变更</CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                {[
                  { time: '10:32', action: '新增', target: 'prod-web-03', type: '服务器' },
                  { time: '09:15', action: '更新', target: 'redis-cluster', type: '中间件' },
                  { time: '08:47', action: '下线', target: 'test-db-02', type: '数据库' },
                  { time: '昨天', action: '新增', target: 'k8s-node-12', type: '服务器' },
                ].map((item, i) => (
                  <div key={i} className="flex items-center justify-between p-2 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                    <div className="flex items-center gap-3">
                      <Badge variant={item.action === '新增' ? 'success' : item.action === '下线' ? 'destructive' : 'secondary'}>{item.action}</Badge>
                      <span className="text-xs font-mono text-gray-900 dark:text-white">{item.target}</span>
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="text-[10px] text-muted-foreground">{item.type}</span>
                      <span className="text-[10px] text-muted-foreground/50 font-mono">{item.time}</span>
                    </div>
                  </div>
                ))}
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">拓扑关系概览</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-3 gap-4 text-center">
                {[
                  { label: '物理关系', value: 234, icon: Server },
                  { label: '依赖关系', value: 567, icon: GitBranch },
                  { label: '网络关系', value: 189, icon: Wifi },
                ].map((item, i) => (
                  <div key={i} className="p-4 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                    <item.icon className="w-6 h-6 mx-auto text-violet-500 dark:text-violet-400 mb-2" />
                    <p className="metric-value text-xl text-gray-900 dark:text-white">{item.value}</p>
                    <p className="text-[11px] text-muted-foreground mt-1">{item.label}</p>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        {/* ============ ITSM 页签 ============ */}
        <TabsContent value="itsm" className="mt-6 space-y-6">
          <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
            <MetricCard icon={TicketCheck} title="总工单" value={metrics.tickets} label="累计工单" color="cyan" />
            <MetricCard icon={Clock} title="待处理" value={metrics.openTickets} label="待处理工单" color="amber" />
            <MetricCard icon={Shield} title="SLA 达标率" value={Math.round(metrics.sla)} label="%" color="emerald" />
            <MetricCard icon={Users} title="处理人" value={8} label="在线处理人" color="blue" />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">工单状态分布</CardTitle>
              </CardHeader>
              <CardContent>
                <BarChart data={[
                  { label: '待处理', value: 12, color: 'bg-amber-400' },
                  { label: '处理中', value: 8, color: 'bg-blue-400' },
                  { label: '已完成', value: 45, color: 'bg-emerald-400' },
                  { label: '已关闭', value: 156, color: 'bg-gray-400' },
                ]} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">工单类型统计</CardTitle>
              </CardHeader>
              <CardContent>
                <BarChart data={[
                  { label: '故障', value: 23, color: 'bg-red-400' },
                  { label: '请求', value: 67, color: 'bg-blue-400' },
                  { label: '变更', value: 34, color: 'bg-amber-400' },
                  { label: '咨询', value: 45, color: 'bg-emerald-400' },
                ]} />
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">最近工单</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              {[
                { id: 'TK-001', title: '数据库连接池告警', status: 'open', priority: 'P1', assignee: '张三', time: '10:32' },
                { id: 'TK-002', title: 'API 网关性能优化', status: 'processing', priority: 'P2', assignee: '李四', time: '09:15' },
                { id: 'TK-003', title: '服务器扩容申请', status: 'open', priority: 'P3', assignee: '王五', time: '昨天' },
                { id: 'TK-004', title: 'SSL 证书更新', status: 'closed', priority: 'P2', assignee: '赵六', time: '昨天' },
              ].map((item, i) => (
                <div key={i} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                  <div className="flex items-center gap-3">
                    <Badge variant={item.status === 'open' ? 'destructive' : item.status === 'processing' ? 'warning' : 'success'}>
                      {item.status === 'open' ? '待处理' : item.status === 'processing' ? '处理中' : '已完成'}
                    </Badge>
                    <div>
                      <span className="text-xs font-mono text-emerald-500 dark:text-emerald-400">{item.id}</span>
                      <span className="text-xs text-gray-900 dark:text-white ml-2">{item.title}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-4">
                    <Badge variant="outline">{item.priority}</Badge>
                    <span className="text-[11px] text-muted-foreground">{item.assignee}</span>
                    <span className="text-[10px] text-muted-foreground/50 font-mono">{item.time}</span>
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>
        </TabsContent>

        {/* ============ 应用发布页签 ============ */}
        <TabsContent value="release" className="mt-6 space-y-6">
          <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
            <MetricCard icon={GitBranch} title="本月发布" value={24} label="次发布" color="pink" />
            <MetricCard icon={CheckCircle} title="成功率" value={96} label="%" color="emerald" />
            <MetricCard icon={Timer} title="平均耗时" value={12} label="分钟" color="blue" />
            <MetricCard icon={Package} title="应用数" value={18} label="个应用" color="violet" />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">发布趋势 (近7天)</CardTitle>
              </CardHeader>
              <CardContent>
                <BarChart data={[
                  { label: '周一', value: 5, color: 'bg-pink-400' },
                  { label: '周二', value: 3, color: 'bg-pink-400' },
                  { label: '周三', value: 4, color: 'bg-pink-400' },
                  { label: '周四', value: 6, color: 'bg-pink-400' },
                  { label: '周五', value: 2, color: 'bg-pink-400' },
                  { label: '周六', value: 1, color: 'bg-pink-400' },
                  { label: '周日', value: 3, color: 'bg-pink-400' },
                ]} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">发布环境分布</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 gap-4">
                  {[
                    { env: '生产环境', count: 12, color: 'text-red-500 dark:text-red-400' },
                    { env: '预发环境', count: 8, color: 'text-amber-500 dark:text-amber-400' },
                    { env: '测试环境', count: 45, color: 'text-blue-500 dark:text-blue-400' },
                    { env: '开发环境', count: 120, color: 'text-emerald-500 dark:text-emerald-400' },
                  ].map((item, i) => (
                    <div key={i} className="p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                      <p className="text-xs text-muted-foreground">{item.env}</p>
                      <p className={`metric-value text-2xl ${item.color} mt-1`}>{item.count}</p>
                      <p className="text-[10px] text-muted-foreground/60 mt-0.5">次发布</p>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">最近发布记录</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              {[
                { id: 'REL-001', app: 'user-service', version: 'v2.3.1', env: '生产', status: 'success', time: '10:32', duration: '8min' },
                { id: 'REL-002', app: 'api-gateway', version: 'v1.8.0', env: '预发', status: 'running', time: '09:45', duration: '进行中' },
                { id: 'REL-003', app: 'order-service', version: 'v3.1.0', env: '生产', status: 'success', time: '昨天', duration: '12min' },
                { id: 'REL-004', app: 'payment-service', version: 'v2.0.3', env: '生产', status: 'failed', time: '昨天', duration: '5min' },
              ].map((item, i) => (
                <div key={i} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                  <div className="flex items-center gap-3">
                    <Badge variant={item.status === 'success' ? 'success' : item.status === 'running' ? 'warning' : 'destructive'}>
                      {item.status === 'success' ? '成功' : item.status === 'running' ? '发布中' : '失败'}
                    </Badge>
                    <div>
                      <span className="text-xs font-mono text-pink-500 dark:text-pink-400">{item.id}</span>
                      <span className="text-xs text-gray-900 dark:text-white ml-2">{item.app}</span>
                      <Badge variant="outline" className="ml-2 text-[10px]">{item.version}</Badge>
                    </div>
                  </div>
                  <div className="flex items-center gap-4">
                    <Badge variant="secondary">{item.env}</Badge>
                    <span className="text-[11px] text-muted-foreground">{item.duration}</span>
                    <span className="text-[10px] text-muted-foreground/50 font-mono">{item.time}</span>
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>
        </TabsContent>

        {/* ============ 监控页签 ============ */}
        <TabsContent value="monitor" className="mt-6 space-y-6">
          <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
            <MetricCard icon={Wifi} title="监控目标" value={metrics.targets} label="在线目标" color="emerald" />
            <MetricCard icon={AlertTriangle} title="活跃告警" value={metrics.alerts} label="待处理" color="amber" />
            <MetricCard icon={Activity} title="探测次数" value={15600} label="今日探测" color="blue" />
            <MetricCard icon={TrendingUp} title="可用性" value={99} label="%" color="emerald" />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">告警趋势 (近7天)</CardTitle>
              </CardHeader>
              <CardContent>
                <BarChart data={[
                  { label: '周一', value: 12, color: 'bg-amber-400' },
                  { label: '周二', value: 8, color: 'bg-amber-400' },
                  { label: '周三', value: 15, color: 'bg-red-400' },
                  { label: '周四', value: 6, color: 'bg-amber-400' },
                  { label: '周五', value: 10, color: 'bg-amber-400' },
                  { label: '周六', value: 3, color: 'bg-emerald-400' },
                  { label: '周日', value: 5, color: 'bg-emerald-400' },
                ]} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">告警级别分布</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-3 gap-4">
                  {[
                    { level: '严重', count: 3, color: 'text-red-500 dark:text-red-400', bg: 'bg-red-50 dark:bg-red-500/10' },
                    { level: '警告', count: 8, color: 'text-amber-500 dark:text-amber-400', bg: 'bg-amber-50 dark:bg-amber-500/10' },
                    { level: '通知', count: 15, color: 'text-blue-500 dark:text-blue-400', bg: 'bg-blue-50 dark:bg-blue-500/10' },
                  ].map((item, i) => (
                    <div key={i} className={`p-4 rounded-lg text-center ${item.bg}`}>
                      <p className={`metric-value text-3xl ${item.color}`}>{item.count}</p>
                      <p className="text-xs text-muted-foreground mt-1">{item.level}</p>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">最近告警</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              {alerts.map((alert, i) => (
                <div key={alert.id} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                  <div className="flex items-center gap-3">
                    <div className={`w-2 h-2 rounded-full ${
                      alert.severity === 'critical' ? 'bg-red-400 animate-pulse-soft' :
                      alert.severity === 'warning' ? 'bg-amber-400' : 'bg-emerald-400'
                    }`} />
                    <div>
                      <span className="text-xs font-mono font-medium text-gray-900 dark:text-white">{alert.ci}</span>
                      <span className="text-xs text-muted-foreground ml-2">{alert.message}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge variant={alert.severity === 'critical' ? 'destructive' : alert.severity === 'warning' ? 'warning' : 'default'}>{alert.severity}</Badge>
                    <span className="text-[10px] text-muted-foreground/50 font-mono">{alert.time}</span>
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">监控目标状态</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                {[
                  { label: '服务器', total: 45, online: 43 },
                  { label: '数据库', total: 12, online: 12 },
                  { label: '网络设备', total: 28, online: 27 },
                  { label: '中间件', total: 35, online: 34 },
                ].map((item, i) => (
                  <div key={i} className="p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                    <p className="text-xs text-muted-foreground">{item.label}</p>
                    <div className="flex items-baseline gap-1 mt-1">
                      <span className="metric-value text-xl text-emerald-500 dark:text-emerald-400">{item.online}</span>
                      <span className="text-xs text-muted-foreground">/ {item.total}</span>
                    </div>
                    <div className="mt-2 h-1.5 bg-gray-200 dark:bg-gray-700 rounded-full overflow-hidden">
                      <div className="h-full bg-emerald-400 rounded-full" style={{ width: `${(item.online / item.total) * 100}%` }} />
                    </div>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        {/* ============ 自动化运维页签 ============ */}
        <TabsContent value="automation" className="mt-6 space-y-6">
          <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
            <MetricCard icon={Workflow} title="定时任务" value={12} label="个任务" color="blue" />
            <MetricCard icon={Zap} title="自动化巡检" value={8} label="今日完成" color="emerald" />
            <MetricCard icon={Shield} title="漏洞扫描" value={3} label="个待处理" color="amber" />
            <MetricCard icon={Package} title="补丁安装" value={5} label="待安装" color="violet" />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">巡检任务状态</CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                {[
                  { name: '每日系统巡检', status: 'success', time: '08:00', targets: 45 },
                  { name: '安全合规巡检', status: 'success', time: '22:00', targets: 32 },
                  { name: '网络连通性巡检', status: 'success', time: '07:30', targets: 50 },
                  { name: '性能基线巡检', status: 'partial', time: '06:00', targets: 28 },
                ].map((item, i) => (
                  <div key={i} className="flex items-center justify-between p-2 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                    <div className="flex items-center gap-2">
                      <Badge variant={item.status === 'success' ? 'success' : 'warning'} className="text-[10px]">
                        {item.status === 'success' ? '成功' : '部分通过'}
                      </Badge>
                      <span className="text-xs text-gray-900 dark:text-white">{item.name}</span>
                    </div>
                    <div className="flex items-center gap-3 text-xs text-muted-foreground">
                      <span>{item.targets} 台</span>
                      <span className="font-mono">{item.time}</span>
                    </div>
                  </div>
                ))}
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">漏洞扫描概览</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 gap-4">
                  {[
                    { level: '严重', count: 3, color: 'text-red-500 dark:text-red-400', bg: 'bg-red-50 dark:bg-red-500/10' },
                    { level: '高危', count: 8, color: 'text-orange-500 dark:text-orange-400', bg: 'bg-orange-50 dark:bg-orange-500/10' },
                    { level: '中危', count: 16, color: 'text-amber-500 dark:text-amber-400', bg: 'bg-amber-50 dark:bg-amber-500/10' },
                    { level: '低危', count: 31, color: 'text-emerald-500 dark:text-emerald-400', bg: 'bg-emerald-50 dark:bg-emerald-500/10' },
                  ].map((item, i) => (
                    <div key={i} className={`p-3 rounded-lg text-center ${item.bg}`}>
                      <p className={`metric-value text-2xl ${item.color}`}>{item.count}</p>
                      <p className="text-xs text-muted-foreground mt-1">{item.level}</p>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">补丁安装进度</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {[
                  { name: '安全补丁批量安装', progress: 100, targets: 28, completed: 28 },
                  { name: '内核补丁紧急安装', progress: 60, targets: 5, completed: 3 },
                  { name: 'OpenSSL 安全补丁', progress: 0, targets: 45, completed: 0 },
                ].map((item, i) => (
                  <div key={i} className="space-y-1">
                    <div className="flex items-center justify-between text-xs">
                      <span className="text-gray-900 dark:text-white">{item.name}</span>
                      <span className="text-muted-foreground">{item.completed}/{item.targets}</span>
                    </div>
                    <div className="h-1.5 bg-gray-200 dark:bg-gray-700 rounded-full overflow-hidden">
                      <div className={`h-full rounded-full transition-all ${item.progress === 100 ? 'bg-emerald-400' : item.progress > 0 ? 'bg-blue-400' : 'bg-gray-300'}`} style={{ width: `${item.progress}%` }} />
                    </div>
                  </div>
                ))}
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">软件部署统计</CardTitle>
              </CardHeader>
              <CardContent>
                <BarChart data={[
                  { label: 'Nginx', value: 12, color: 'bg-blue-400' },
                  { label: 'Redis', value: 6, color: 'bg-red-400' },
                  { label: 'MySQL', value: 3, color: 'bg-blue-500' },
                  { label: 'Kafka', value: 0, color: 'bg-gray-400' },
                  { label: 'ES', value: 0, color: 'bg-amber-400' },
                ]} />
              </CardContent>
            </Card>
          </div>
        </TabsContent>
      </Tabs>

      {/* Footer */}
      <div className="flex items-center justify-between pt-2 border-t border-gray-100 dark:border-[rgba(255,255,255,0.04)] text-[10px] text-muted-foreground/30 font-mono">
        <span>All systems operational</span>
        <span>Cluster: prod-cn-east-1 · Zone: A</span>
      </div>
    </div>
  )
}
