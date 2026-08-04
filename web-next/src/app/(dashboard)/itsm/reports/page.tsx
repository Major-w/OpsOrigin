'use client'

// 报表看板 —— 三门户的「管理驾驶舱」形态：
// SLA 达成仪表盘 + 工单趋势 + 状态/风险分布 + 人员效能 + 来源渠道分布。
import { useEffect, useState, useCallback } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { apiFetch } from '@/lib/utils'
import { TimeSeriesChart, GaugeChartView, PieChartView, BarChartView } from '@/components/charts'
import type { ITSMStats, SLAStats } from '@/stores/itsm'

const statusLabels: Record<string, string> = {
  draft: '草稿', pending_review: '待审批', approved: '已审批', rejected: '已驳回',
  executing: '执行中', succeeded: '已完成', failed: '已失败', rolled_back: '已回滚',
  closed: '已关闭', withdrawn: '已撤回', reopened: '已重开',
}
const riskLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危' }
const channelLabels: Record<string, string> = { manual: '人工创建', mail: '邮件入站', system: '系统', catalog: '服务目录', ai: 'AI 巡检' }

const fmtMs = (ms: number) => {
  if (!ms) return '-'
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  return `${(s / 3600).toFixed(1)}h`
}

export default function ITSMReportsPage() {
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
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => { fetchAll() }, [fetchAll])

  if (loading) return <div className="p-6 text-sm text-muted-foreground">加载报表数据…</div>
  if (!stats) return <div className="p-6 text-sm text-muted-foreground">暂无统计数据。</div>

  const statusData = Object.entries(stats.by_status || {}).map(([k, v]) => ({ name: statusLabels[k] || k, value: v }))
  const riskData = Object.entries(stats.by_risk || {}).map(([k, v]) => ({ name: riskLabels[k] || k, value: v }))
  const creatorData = Object.entries(stats.by_creator || {}).map(([k, v]) => ({ name: k, value: v })).sort((a, b) => b.value - a.value).slice(0, 10)
  const channelData = Object.entries(stats.by_channel || {}).map(([k, v]) => ({ name: channelLabels[k] || k, value: v }))

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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">报表看板</h1>
          <p className="text-sm text-muted-foreground">服务运营管理驾驶舱 · 数据实时</p>
        </div>
      </div>

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
