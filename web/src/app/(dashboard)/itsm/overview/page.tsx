
// ITSM 总览 —— 服务运营「管理驾驶舱」：
// SLA 达成仪表盘 + 工单趋势 + 状态/风险/渠道分布 + 人员效能。
// 需要按需自定义的报表请前往「自定义报表」。
import { useEffect, useState, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { RefreshCw, BarChart3 } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, message } from 'antd'
import { apiFetch } from '@/lib/utils'
import dynamic from '@/lib/dynamic'

const Charts = dynamic(() => import('@/components/charts').then(m => ({ default: m.TimeSeriesChart })), { ssr: false, loading: () => <div className="h-48 bg-muted animate-pulse rounded" /> })
const TimeSeriesChart = dynamic(() => import('@/components/charts').then(m => ({ default: m.TimeSeriesChart })), { ssr: false })
const GaugeChartView = dynamic(() => import('@/components/charts').then(m => ({ default: m.GaugeChartView })), { ssr: false })
const PieChartView = dynamic(() => import('@/components/charts').then(m => ({ default: m.PieChartView })), { ssr: false })
const BarChartView = dynamic(() => import('@/components/charts').then(m => ({ default: m.BarChartView })), { ssr: false })
import type { ITSMStats, SLAStats } from '@/stores/itsm/types'
import { STATUS_LABELS, RISK_LABELS, CHANNEL_LABELS } from '@/lib/itsm-constants'
import { getMessage } from '@/lib/message'
const fmtMs = (ms: number) => {
  if (!ms) return '-'
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  return `${(s / 3600).toFixed(1)}h`
}

export default function ITSMOverviewPage() {
  const [stats, setStats] = useState<ITSMStats | null>(null)
  const [sla, setSla] = useState<SLAStats | null>(null)
  const [loading, setLoading] = useState(true)

  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const [s, sl] = await Promise.all([
        apiFetch<ITSMStats>('/itsm/stats'),
        apiFetch<SLAStats>('/itsm/sla/stats'),
      ])
      setStats(s)
      setSla(sl)
    } catch { getMessage().error('加载统计数据失败') }
    setLoading(false)
  }, [])

  useEffect(() => { fetchAll() }, [fetchAll])

  const header = (
    <div className="flex items-center justify-between">
      <div>
        <h1 className="text-2xl font-bold">ITSM 总览</h1>
        <p className="text-sm text-muted-foreground">服务运营管理驾驶舱 · 数据实时</p>
      </div>
      <div className="flex gap-2">
        <Button onClick={fetchAll}>
          <RefreshCw className={`mr-1.5 h-4 w-4 ${loading ? 'animate-spin' : ''}`} />刷新
        </Button>
        <Link to="/itsm/reports">
          <Button size="middle"><BarChart3 className="mr-1.5 h-4 w-4" />自定义报表</Button>
        </Link>
      </div>
    </div>
  )

  if (loading) return <div className="p-6 space-y-4">{header}<div className="animate-pulse space-y-4">{[1,2,3].map(i=><div key={i} className="h-24 bg-muted rounded"/>)}</div></div>
  if (!stats) return <div className="p-6 space-y-4">{header}<div className="text-sm text-muted-foreground">暂无统计数据。</div></div>

  const statusData = Object.entries(stats.by_status || {}).map(([k, v]) => ({ name: STATUS_LABELS[k] || k, value: v }))
  const riskData = Object.entries(stats.by_risk || {}).map(([k, v]) => ({ name: RISK_LABELS[k] || k, value: v }))
  const creatorData = Object.entries(stats.by_creator || {}).map(([k, v]) => ({ name: k, value: v })).sort((a, b) => b.value - a.value).slice(0, 10)
  const channelData = Object.entries(stats.by_channel || {}).map(([k, v]) => ({ name: CHANNEL_LABELS[k] || k, value: v }))

  const compliance = sla ? Math.round((sla.compliance_rate || 0) * 100) : 0
  const trendX = (stats.trends || []).map(t => t.date.slice(5))
  const trendMetrics = [
    { name: '创建', data: (stats.trends || []).map(t => t.created), areaStyle: true },
    { name: '关闭', data: (stats.trends || []).map(t => t.closed), areaStyle: false },
  ]

  const kpis = [
    { label: '工单总数', value: stats.total },
    { label: '审批通过率', value: `${Math.round((stats.approval_rate || 0) * 100)}%` },
    { label: '平均审批耗时', value: fmtMs(stats.avg_approval_ms) },
    { label: '平均执行耗时', value: fmtMs(stats.avg_execution_ms) },
    { label: 'SLA 达成率', value: `${compliance}%` },
    { label: 'SLA 违约', value: sla?.sla_breached ?? 0 },
  ]

  return (
    <div className="p-6 space-y-4">
      {header}

      {/* KPI 卡片 */}
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
        {kpis.map(k => (
          <Card key={k.label}>
            <CardContent className="p-4">
              <div className="text-xs text-muted-foreground">{k.label}</div>
              <div className="text-2xl font-bold mt-1">{k.value}</div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* 趋势 + SLA */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <Card className="lg:col-span-2">
          <CardHeader className="py-3"><CardTitle className="text-sm">工单趋势（近 30 天）</CardTitle></CardHeader>
          <CardContent>
            <TimeSeriesChart xAxisData={trendX} metrics={trendMetrics} height={300} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm">SLA 达成率</CardTitle></CardHeader>
          <CardContent>
            <GaugeChartView value={compliance} title={`达标 ${sla?.sla_met ?? 0} · 违约 ${sla?.sla_breached ?? 0} · 预警 ${sla?.sla_warning ?? 0}`} height={300} />
          </CardContent>
        </Card>
      </div>

      {/* 分布 */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm">工单状态分布</CardTitle></CardHeader>
          <CardContent><PieChartView data={statusData} height={260} /></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm">风险等级分布</CardTitle></CardHeader>
          <CardContent><PieChartView data={riskData} height={260} roseType /></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm">来源渠道分布</CardTitle></CardHeader>
          <CardContent><PieChartView data={channelData} height={260} /></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm">人员效能 Top 10</CardTitle></CardHeader>
          <CardContent><BarChartView xAxisData={creatorData.map(d => d.name)} metrics={[{ name: '工单数', data: creatorData.map(d => d.value) }]} horizontal height={260} /></CardContent>
        </Card>
      </div>
    </div>
  )
}
