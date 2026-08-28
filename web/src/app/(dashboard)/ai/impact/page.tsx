
import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tag } from 'antd'
import { Activity, ShieldAlert, ArrowRight, FileCheck2, Gauge, RefreshCw, RadarIcon } from 'lucide-react'
import { cn } from '@/lib/utils'
import { apiFetch } from '@/lib/utils'

interface WorkforceMetrics {
  inspections: { total: number; enabled: number; total_runs: number; success_runs: number; failed_runs: number; success_rate: number }
  findings: { high: number; medium: number; low: number; total: number }
  escalations: { total: number }
  tickets: { total: number; by_status: Record<string, number>; avg_resolution_minutes: number; by_assessment?: Record<string, number> } | null
}

const STATUS_LABELS: Record<string, string> = {
  draft: '草稿', pending_review: '待审批', approved: '已批准',
  executing: '执行中', succeeded: '已完成', failed: '失败', rolled_back: '已回滚',
}

function fmtMinutes(min: number) {
  if (min <= 0) return '—'
  if (min < 60) return `${min.toFixed(0)} 分钟`
  if (min < 24 * 60) return `${(min / 60).toFixed(1)} 小时`
  return `${(min / 1440).toFixed(1)} 天`
}

function fmtPct(v: number) {
  return `${(v * 100).toFixed(1)}%`
}

export default function ImpactPage() {
  const [metrics, setMetrics] = useState<WorkforceMetrics | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => { load() }, [])

  const load = async () => {
    setLoading(true)
    try {
      const m = await apiFetch<WorkforceMetrics>('/ai/workforce-metrics')
      setMetrics(m)
    } catch {
      setMetrics(null)
    }
    setLoading(false)
  }

  const ins = metrics?.inspections
  const findings = metrics?.findings
  const tickets = metrics?.tickets
  const resolvedCount = (tickets?.by_status?.succeeded || 0) + (tickets?.by_status?.rolled_back || 0)

  // 处置漏斗: 发现 → 转单 → 已处置
  const funnel = [
    { label: '累计发现', value: findings?.total || 0, color: 'bg-sky-500', icon: ShieldAlert },
    { label: '已转工单', value: metrics?.escalations?.total || 0, color: 'bg-amber-500', icon: ArrowRight },
    { label: '已处置', value: resolvedCount, color: 'bg-blue-500', icon: FileCheck2 },
  ]
  const funnelMax = Math.max(1, ...funnel.map(f => f.value))

  return (
    <div className="p-6 space-y-5">
      {/* 页头 */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Gauge className="h-6 w-6 text-primary" />
            运营效果
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            数字员工价值量化：巡检健康度、发现/转单/处置漏斗、工单处置时效
          </p>
        </div>
        <button
          onClick={load}
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
        >
          <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
        </button>
      </div>

      {loading ? (
        <div className="py-20 flex items-center justify-center"><Activity className="h-8 w-8 animate-spin text-muted-foreground" /></div>
      ) : (
        <div className="space-y-4">
          {/* 巡检健康度 */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            {[
              { label: '巡检任务', value: ins?.total || 0, sub: `${ins?.enabled || 0} 已启用`, Icon: RadarIcon, cls: 'text-primary' },
              { label: '运行总次数', value: ins?.total_runs || 0, sub: `${ins?.success_runs || 0} 成功 / ${ins?.failed_runs || 0} 失败`, Icon: Activity, cls: 'text-blue-500' },
              { label: '巡检成功率', value: fmtPct(ins?.success_rate || 0), sub: '最近全部运行', Icon: FileCheck2, cls: 'text-blue-500' },
              { label: '平均处置时长', value: fmtMinutes(tickets?.avg_resolution_minutes || 0), sub: `${tickets?.total || 0} 张 AI 巡检工单`, Icon: Gauge, cls: 'text-amber-500' },
            ].map(s => (
              <Card key={s.label}>
                <CardContent className="p-4">
                  <div className="flex items-center justify-between">
                    <div>
                      <div className="text-xs text-muted-foreground">{s.label}</div>
                      <div className="text-2xl font-bold mt-0.5">{s.value}</div>
                      <div className="text-[11px] text-muted-foreground mt-1">{s.sub}</div>
                    </div>
                    <s.Icon className={cn('h-8 w-8 opacity-20', s.cls)} />
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>

          {/* 发现分级 + 处置漏斗 */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-sm flex items-center gap-2"><ShieldAlert className="h-4 w-4" />发现分级</CardTitle></CardHeader>
              <CardContent>
                <div className="space-y-3">
                  {[
                    { label: '高危', value: findings?.high || 0, cls: 'bg-red-500', text: 'text-red-500' },
                    { label: '中危', value: findings?.medium || 0, cls: 'bg-amber-500', text: 'text-amber-500' },
                    { label: '低危', value: findings?.low || 0, cls: 'bg-sky-500', text: 'text-sky-500' },
                  ].map(s => (
                    <div key={s.label}>
                      <div className="flex justify-between text-xs mb-1">
                        <span className={s.text}>{s.label}</span>
                        <span className="text-muted-foreground">{s.value}</span>
                      </div>
                      <div className="h-2 rounded-full bg-muted overflow-hidden">
                        <div className={cn('h-full rounded-full transition-all duration-500', s.cls)}
                          style={{ width: `${findings?.total ? (s.value / (findings?.total || 1)) * 100 : 0}%` }} />
                      </div>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-sm flex items-center gap-2"><ArrowRight className="h-4 w-4" />处置漏斗（发现 → 转单 → 已处置）</CardTitle></CardHeader>
              <CardContent>
                <div className="space-y-3">
                  {funnel.map((f, i) => (
                    <div key={f.label}>
                      <div className="flex justify-between text-xs mb-1">
                        <span className="inline-flex items-center gap-1"><f.icon className="h-3 w-3" />{f.label}</span>
                        <span className="font-semibold">{f.value}</span>
                      </div>
                      <div className="h-3 rounded-md bg-muted overflow-hidden">
                        <div className={cn('h-full rounded-md transition-all duration-500', f.color)}
                          style={{ width: `${(f.value / funnelMax) * 100}%`, opacity: i === 0 ? 1 : 0.75 }} />
                      </div>
                    </div>
                  ))}
                  {tickets && (
                    <p className="text-[11px] text-muted-foreground pt-1">
                      处置率 {fmtPct(tickets.total ? resolvedCount / tickets.total : 0)} · 平均 {fmtMinutes(tickets.avg_resolution_minutes)}
                    </p>
                  )}
                </div>
              </CardContent>
            </Card>
          </div>

          {/* 工单状态分布 */}
          {tickets && tickets.total > 0 && (
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-sm flex items-center gap-2"><FileCheck2 className="h-4 w-4" />AI 巡检工单状态</CardTitle></CardHeader>
              <CardContent>
                <div className="flex flex-wrap gap-2">
                  {Object.entries(tickets.by_status).map(([st, n]) => (
                    <Tag key={st} color={st === 'succeeded' ? 'success' : st === 'failed' ? 'error' : 'default'}>
                      {STATUS_LABELS[st] || st}
                      <span className="opacity-80">{n}</span>
                    </Tag>
                  ))}
                </div>
                {(tickets.by_assessment?.confirmed || 0) + (tickets.by_assessment?.false_positive || 0) > 0 && (
                  <div className="mt-3 pt-3 border-t">
                    <div className="flex flex-wrap gap-2">
                      <Tag color="success">确认有效 {tickets.by_assessment?.confirmed || 0}</Tag>
                      <Tag color="error">误报 {tickets.by_assessment?.false_positive || 0}</Tag>
                      <span className="text-[11px] text-muted-foreground self-center">
                        AI 准确率 {fmtPct((tickets.by_assessment?.confirmed || 0) / ((tickets.by_assessment?.confirmed || 0) + (tickets.by_assessment?.false_positive || 0)))}
                      </span>
                    </div>
                  </div>
                )}
              </CardContent>
            </Card>
          )}
        </div>
      )}
    </div>
  )
}