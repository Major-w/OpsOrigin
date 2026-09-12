
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Segmented, Table, Empty, Tooltip as AntTooltip } from 'antd'
import { apiFetch, formatTime } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { getMessage } from '@/lib/message'
import { ErrorState } from '@/components/ui/error-state'
import { TimeSeriesChart, BarChartView, PieChartView } from '@/components/charts/echarts'
import {
  Activity, AlertTriangle, Gauge, BarChart3, RefreshCw, Flame, Users, Server,
  ShieldAlert, Info,
} from 'lucide-react'

// ——— 数据消费分析 ———
// 回答三个问题：CMDB 的 API 被调用了多少次（总量/趋势）、哪些数据是热点
// （接口/配置项/调用方 TopN）、调用错误频率如何（错误趋势 + 分组 + 真实样本）。
// 数据源：后端采集中间件落库的 cmdb_usage_event（见 022_cmdb_usage.sql）。

interface UsagePoint { ts: string; count: number; error_count: number }
interface UsageBreakdown { label: string; count: number }

interface UsageSummary {
  window_hours: number
  total_calls: number
  error_calls: number
  error_rate: number
  avg_latency_ms: number
  p95_latency_ms: number
  ci_hit_calls: number
  distinct_cis: number
  series: UsagePoint[]
  by_method: UsageBreakdown[]
  by_status: UsageBreakdown[]
}

interface RouteHotspot {
  route: string; method: string; calls: number; errors: number
  avg_latency_ms: number; share: number
}
interface CIHotspot {
  ci_id: string; name: string; type_id: string; calls: number; errors: number; last_seen: string
}
interface UserHotspot { user_id: string; calls: number; errors: number }
interface UsageHotspots { routes: RouteHotspot[]; cis: CIHotspot[]; users: UserHotspot[] }

interface ErrorGroup { route: string; method: string; status_code: number; count: number }
interface ErrorSample {
  route: string; method: string; path: string; status_code: number
  user_id: string; ci_id: string; latency_ms: number; at: string
}
interface UsageErrors {
  error_calls: number
  series: UsagePoint[]
  by_route: ErrorGroup[]
  by_status: UsageBreakdown[]
  recent: ErrorSample[]
}
interface CollectorStats { enqueued: number; dropped: number; flushed: number; last_error?: string }

const WINDOWS = [
  { label: '近 1 小时', value: 1 },
  { label: '近 6 小时', value: 6 },
  { label: '近 24 小时', value: 24 },
  { label: '近 7 天', value: 168 },
  { label: '近 30 天', value: 720 },
]

/** 时间桶 → 图表 X 轴标签（按窗口粒度自适应：日内显示时:分，跨天显示月-日）。 */
function bucketLabel(ts: string, hours: number): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const pad = (n: number) => String(n).padStart(2, '0')
  if (hours <= 48) return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:00`
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** 路由模板 → 可读的接口名（把 ServeMux 的 {id} 保留为 :id，前缀去掉）。 */
function routeLabel(route: string): string {
  if (!route) return '(未匹配路由)'
  return route.replace('/api/v1', '')
}

const fmtInt = (n: number) => (n ?? 0).toLocaleString('zh-CN')
const fmtPct = (v: number) => `${((v ?? 0) * 100).toFixed(v >= 0.1 ? 1 : 2)}%`
const fmtMs = (v: number) => `${(v ?? 0).toFixed((v ?? 0) >= 100 ? 0 : 1)} ms`

export default function CMDBUsagePage() {
  const canRead = useCan('cmdb:read')
  const [hours, setHours] = useState(24)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [summary, setSummary] = useState<UsageSummary | null>(null)
  const [hotspots, setHotspots] = useState<UsageHotspots | null>(null)
  const [errors, setErrors] = useState<UsageErrors | null>(null)
  const [collector, setCollector] = useState<CollectorStats | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const q = `?hours=${hours}`
      const [s, h, e, c] = await Promise.all([
        apiFetch<UsageSummary>('/cmdb/usage/summary' + q),
        apiFetch<UsageHotspots>('/cmdb/usage/hotspots' + q + '&limit=10'),
        apiFetch<UsageErrors>('/cmdb/usage/errors' + q + '&limit=20'),
        apiFetch<CollectorStats>('/cmdb/usage/status').catch(() => null),
      ])
      setSummary(s)
      setHotspots(h)
      setErrors(e)
      setCollector(c)
    } catch (err: any) {
      setError(err?.message || '加载调用分析失败')
    } finally {
      setLoading(false)
    }
  }, [hours])

  useEffect(() => { if (canRead) load() }, [canRead, load])

  // 趋势图：调用量 + 错误量双序列（同轴，便于直接看出错误占比）。
  const trendOption = useMemo(() => {
    const series = summary?.series || []
    return {
      xAxisData: series.map(p => bucketLabel(p.ts, hours)),
      metrics: [
        { name: '调用量', data: series.map(p => p.count), areaStyle: true, color: '#3B82F6' },
        { name: '错误量', data: series.map(p => p.error_count), color: '#ef4444' },
      ],
    }
  }, [summary, hours])

  const errorTrendOption = useMemo(() => {
    const series = errors?.series || []
    return {
      xAxisData: series.map(p => bucketLabel(p.ts, hours)),
      metrics: [{ name: '错误数', data: series.map(p => p.count), areaStyle: true, color: '#ef4444' }],
    }
  }, [errors, hours])

  const routeBarOption = useMemo(() => {
    const rows = (hotspots?.routes || []).slice(0, 8).reverse()
    return {
      xAxisData: rows.map(r => routeLabel(r.route)),
      metrics: [
        { name: '调用量', data: rows.map(r => r.calls) },
        { name: '错误数', data: rows.map(r => r.errors), color: '#ef4444' },
      ],
    }
  }, [hotspots])

  const statusPieData = useMemo(
    () => (summary?.by_status || []).map(b => ({ name: `${b.label}`, value: b.count })),
    [summary],
  )
  const methodPieData = useMemo(
    () => (summary?.by_method || []).map(b => ({ name: b.label, value: b.count })),
    [summary],
  )

  if (!canRead) {
    return (
      <div className="p-6">
        <Card><CardContent className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <ShieldAlert className="h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">无访问权限</p>
          <p className="text-xs text-muted-foreground">当前账号缺少「查看 CMDB 资源（cmdb:read）」权限，无法查看数据消费分析。</p>
        </CardContent></Card>
      </div>
    )
  }

  const kpis = [
    { label: '调用总量', value: fmtInt(summary?.total_calls || 0), sub: `窗口内 ${hours}h`, icon: Activity, cls: 'text-blue-500' },
    { label: '错误调用', value: fmtInt(summary?.error_calls || 0), sub: `错误率 ${fmtPct(summary?.error_rate || 0)}`, icon: AlertTriangle, cls: (summary?.error_rate || 0) > 0.05 ? 'text-red-500' : 'text-amber-500' },
    { label: '平均延迟', value: fmtMs(summary?.avg_latency_ms || 0), sub: `P95 ${fmtMs(summary?.p95_latency_ms || 0)}`, icon: Gauge, cls: 'text-violet-500' },
    { label: '被访问配置项', value: fmtInt(summary?.distinct_cis || 0), sub: `命中实例调用 ${fmtInt(summary?.ci_hit_calls || 0)}`, icon: Server, cls: 'text-emerald-500' },
  ]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold">CMDB 数据消费分析</h1>
          <p className="text-sm text-muted-foreground">
            统计 CMDB 接口被调用的次数、热点数据（接口 / 配置项 / 调用方）与调用错误频率，用于评估 API 使用健康度与数据热度
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Segmented
            size="small"
            value={hours}
            onChange={v => setHours(Number(v))}
            options={WINDOWS}
          />
          <Button type="default" onClick={load} loading={loading}>
            <RefreshCw className="h-3.5 w-3.5 mr-1" />刷新
          </Button>
        </div>
      </div>

      {collector && (collector.dropped > 0 || collector.last_error) && (
        <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 dark:bg-amber-500/10 dark:border-amber-500/30 px-3 py-2 text-xs">
          <Info className="h-3.5 w-3.5 mt-0.5 shrink-0 text-amber-600" />
          <span className="text-muted-foreground">
            采集器存在写入压力：已丢弃 <b className="text-amber-600">{fmtInt(collector.dropped)}</b> 条埋点
            （已入库 {fmtInt(collector.flushed)} 条）。
            {collector.last_error ? ` 最近错误：${collector.last_error}` : ''}
            若持续增长，建议调大采集缓冲或降低调用频率。
          </span>
        </div>
      )}

      {error ? (
        <Card><CardContent><ErrorState message={error} onRetry={load} /></CardContent></Card>
      ) : (
        <>
          {/* KPI */}
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
            {kpis.map(k => {
              const Icon = k.icon
              return (
                <Card key={k.label}>
                  <CardContent className="flex items-center gap-3 py-4">
                    <div className="rounded-lg bg-muted p-2.5">
                      <Icon className={`h-5 w-5 ${k.cls}`} />
                    </div>
                    <div className="min-w-0">
                      <div className="text-xs text-muted-foreground">{k.label}</div>
                      <div className="text-xl font-bold leading-tight truncate">{k.value}</div>
                      <div className="text-[11px] text-muted-foreground truncate">{k.sub}</div>
                    </div>
                  </CardContent>
                </Card>
              )
            })}
          </div>

          {/* 调用趋势 */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm flex items-center gap-2">
                <Activity className="h-4 w-4 text-blue-500" />调用量趋势
                <span className="text-xs font-normal text-muted-foreground">
                  （{hours <= 48 ? '按小时' : '按天'}聚合；红点为错误调用）
                </span>
              </CardTitle>
            </CardHeader>
            <CardContent>
              {(summary?.series?.length || 0) === 0 ? (
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该窗口内暂无调用记录（埋点自采集器启用后开始累计）" />
              ) : (
                <TimeSeriesChart {...trendOption} height={300} showDataZoom={false} yAxisLabel="次数" />
              )}
            </CardContent>
          </Card>

          {/* 接口热点 + 状态码/方法分布 */}
          <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
            <Card className="lg:col-span-2">
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Flame className="h-4 w-4 text-orange-500" />接口热点 Top 8
                  <span className="text-xs font-normal text-muted-foreground">（按调用量排序）</span>
                </CardTitle>
              </CardHeader>
              <CardContent>
                {routeBarOption.xAxisData.length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无接口调用数据" />
                ) : (
                  <BarChartView {...routeBarOption} height={280} horizontal />
                )}
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <BarChart3 className="h-4 w-4 text-cyan-500" />响应状态分布
                </CardTitle>
              </CardHeader>
              <CardContent>
                {statusPieData.length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" />
                ) : (
                  <PieChartView data={statusPieData} height={280} />
                )}
              </CardContent>
            </Card>
          </div>

          {/* 热点配置项 + 调用方 */}
          <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
            <Card className="lg:col-span-2">
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Flame className="h-4 w-4 text-red-500" />热点配置项 Top 10
                  <span className="text-xs font-normal text-muted-foreground">（实例详情被读取次数）</span>
                </CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                <Table
                  rowKey="ci_id"
                  size="small"
                  loading={loading}
                  dataSource={hotspots?.cis || []}
                  pagination={false}
                  locale={{ emptyText: <Empty description="暂无单实例访问记录" /> }}
                  columns={[
                    {
                      title: '配置项', dataIndex: 'name', key: 'name',
                      render: (v: string, row: CIHotspot) => (
                        <div className="min-w-0">
                          <div className="font-medium truncate">{v || '(已删除实例)'}</div>
                          <div className="text-[11px] text-muted-foreground truncate">{row.type_id || row.ci_id}</div>
                        </div>
                      ),
                    },
                    { title: '调用次数', dataIndex: 'calls', key: 'calls', width: 100, render: (v: number) => <Tag color="blue">{fmtInt(v)}</Tag> },
                    {
                      title: '错误', dataIndex: 'errors', key: 'errors', width: 80,
                      render: (v: number) => (v > 0 ? <Tag color="red">{v}</Tag> : <span className="text-muted-foreground text-xs">0</span>),
                    },
                    {
                      title: '最近访问', dataIndex: 'last_seen', key: 'last_seen', width: 160,
                      render: (v: string) => <span className="text-xs text-muted-foreground">{v ? formatTime(v) : '—'}</span>,
                    },
                  ]}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Users className="h-4 w-4 text-indigo-500" />调用方排行
                </CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                <Table
                  rowKey="user_id"
                  size="small"
                  loading={loading}
                  dataSource={hotspots?.users || []}
                  pagination={false}
                  locale={{ emptyText: <Empty description="暂无数据" /> }}
                  columns={[
                    { title: '调用方', dataIndex: 'user_id', key: 'user_id', ellipsis: true, render: (v: string) => <span className="text-xs">{v || '—'}</span> },
                    { title: '调用', dataIndex: 'calls', key: 'calls', width: 80, render: (v: number) => fmtInt(v) },
                    {
                      title: '错误', dataIndex: 'errors', key: 'errors', width: 64,
                      render: (v: number) => (v > 0 ? <span className="text-red-500 text-xs">{v}</span> : <span className="text-xs text-muted-foreground">0</span>),
                    },
                  ]}
                />
              </CardContent>
            </Card>
          </div>

          {/* 错误频率 */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm flex items-center gap-2">
                <AlertTriangle className="h-4 w-4 text-red-500" />调用错误频率
                <span className="text-xs font-normal text-muted-foreground">
                  （窗口内共 {fmtInt(errors?.error_calls || 0)} 次失败调用）
                </span>
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
                <div className="lg:col-span-2">
                  {(errors?.series?.length || 0) === 0 ? (
                    <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该窗口内没有失败调用 🎉" />
                  ) : (
                    <TimeSeriesChart {...errorTrendOption} height={240} showDataZoom={false} yAxisLabel="错误数" />
                  )}
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-2">错误状态码分布</p>
                  {(errors?.by_status?.length || 0) === 0 ? (
                    <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="无" />
                  ) : (
                    <div className="space-y-1.5">
                      {errors!.by_status.map(b => (
                        <div key={b.label} className="flex items-center justify-between text-xs">
                          <Tag color={b.label.startsWith('5') ? 'red' : b.label.startsWith('4') ? 'orange' : 'default'}>
                            HTTP {b.label}
                          </Tag>
                          <span className="font-medium">{fmtInt(b.count)}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>

              <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                <div>
                  <p className="text-xs text-muted-foreground mb-2">错误接口分组（Top 20）</p>
                  <Table
                    rowKey={r => `${r.method}-${r.route}-${r.status_code}`}
                    size="small"
                    loading={loading}
                    dataSource={errors?.by_route || []}
                    pagination={{ pageSize: 8, hideOnSinglePage: true }}
                    locale={{ emptyText: <Empty description="无错误" /> }}
                    columns={[
                      {
                        title: '接口', dataIndex: 'route', key: 'route',
                        render: (v: string, row: ErrorGroup) => (
                          <span className="text-xs">
                            <Tag>{row.method}</Tag>
                            <span className="font-mono">{routeLabel(v)}</span>
                          </span>
                        ),
                      },
                      {
                        title: '状态', dataIndex: 'status_code', key: 'status_code', width: 90,
                        render: (v: number) => <Tag color={v >= 500 ? 'red' : 'orange'}>{v}</Tag>,
                      },
                      { title: '次数', dataIndex: 'count', key: 'count', width: 80, render: (v: number) => <b>{fmtInt(v)}</b> },
                    ]}
                  />
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-2">最近错误样本</p>
                  <Table
                    rowKey={r => `${r.at}-${r.path}-${r.status_code}-${r.latency_ms}`}
                    size="small"
                    loading={loading}
                    dataSource={errors?.recent || []}
                    pagination={{ pageSize: 8, hideOnSinglePage: true }}
                    locale={{ emptyText: <Empty description="无错误" /> }}
                    columns={[
                      {
                        title: '请求', key: 'req',
                        render: (_: any, row: ErrorSample) => (
                          <div className="min-w-0">
                            <div className="text-xs">
                              <Tag color={row.status_code >= 500 ? 'red' : 'orange'}>{row.status_code}</Tag>
                              <span className="font-mono">{row.method} {routeLabel(row.route || row.path)}</span>
                            </div>
                            <div className="text-[11px] text-muted-foreground truncate">
                              {row.user_id ? `调用方 ${row.user_id}` : '内部调用'}
                              {row.ci_id ? ` · CI ${row.ci_id}` : ''}
                            </div>
                          </div>
                        ),
                      },
                      {
                        title: '耗时', dataIndex: 'latency_ms', key: 'latency_ms', width: 84,
                        render: (v: number) => (
                          <AntTooltip title={`${v} ms`}>
                            <span className={`text-xs ${v >= 3000 ? 'text-red-500' : v >= 1000 ? 'text-amber-500' : 'text-muted-foreground'}`}>
                              {v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v}ms`}
                            </span>
                          </AntTooltip>
                        ),
                      },
                      {
                        title: '时间', dataIndex: 'at', key: 'at', width: 150,
                        render: (v: string) => <span className="text-[11px] text-muted-foreground">{formatTime(v)}</span>,
                      },
                    ]}
                  />
                </div>
              </div>
            </CardContent>
          </Card>

          {/* 方法分布（补充维度，占位保持信息完整） */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm flex items-center gap-2">
                <BarChart3 className="h-4 w-4 text-slate-500" />请求方法分布
              </CardTitle>
            </CardHeader>
            <CardContent>
              {methodPieData.length === 0 ? (
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" />
              ) : (
                <div className="flex flex-wrap items-center gap-3">
                  {methodPieData.map(m => (
                    <div key={m.name} className="flex items-center gap-2 rounded-md border px-3 py-1.5">
                      <Tag color={m.name === 'GET' ? 'blue' : m.name === 'POST' ? 'green' : m.name === 'DELETE' ? 'red' : 'default'}>{m.name}</Tag>
                      <span className="text-sm font-medium">{fmtInt(m.value)}</span>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  )
}
