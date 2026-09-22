import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Input, Segmented, Switch, Table, Tag, Empty, Tooltip as AntTooltip } from 'antd'
import { apiFetch, formatTime } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { getMessage } from '@/lib/message'
import { ErrorState } from '@/components/ui/error-state'
import { TimeSeriesChart } from '@/components/charts/echarts'
import { HEALTH_COLOR } from '@/lib/graph-colors'
import {
  Activity, AlertTriangle, Gauge, RefreshCw, SearchX, FileWarning, ShieldAlert,
  ArrowRight, Layers, Inbox, Target,
} from 'lucide-react'

// ——— 知识库检索可观测(P1-2 / P1-4) ———
// 回答三个此前无法回答的问题:
//   1. 知识库到底被用上了没有(检索量/命中率/各召回路贡献);
//   2. 回答不好是「知识库里没有」还是「检索没召回」(零结果率 + 零结果查询清单);
//   3. 检索慢在哪里、兜底路径是否长期在用(延迟 P95 / fallback 占比)。
// 数据源: 每次 AI 检索写入的 knowledge_search_logs(internal/knowledge/search_logs.go)。

interface SearchQueryStat {
  query: string
  count: number
  avg_hits: number
  last_at: string
}

interface SearchDailyStat {
  day: string
  total: number
  zero: number
  avg_latency_ms: number
}

interface SearchStats {
  days: number
  total: number
  zero_results: number
  zero_rate: number
  avg_hit_count: number
  avg_latency_ms: number
  p95_latency_ms: number
  mode_stats: Record<string, number>
  vector_routes: number
  keyword_routes: number
  fallback_routes: number
  unindexed_articles: number
  top_queries: SearchQueryStat[]
  zero_queries: SearchQueryStat[]
  series: SearchDailyStat[]
}

interface SearchLog {
  id: string
  query: string
  mode: string
  categories?: string[]
  vec_hits: number
  kw_hits: number
  hit_count: number
  zero_result: boolean
  latency_ms: number
  top_score: number
  agent_id?: string
  user_id?: string
  created_at: string
}

// RAG 效果评估(P1-1): 与后端 internal/knowledge.RAGEvalReport 字段对齐。
interface RAGEvalCaseResult {
  query: string
  expected: string[]
  retrieved: string[]
  rank: number
  hit: boolean
  recall: number
  latency_ms: number
  zero_result: boolean
}

interface RAGEvalReport {
  k: number
  cases: number
  hits: number
  hit_rate: number
  recall_at_1: number
  recall_at_3: number
  recall_at_k: number
  mrr: number
  zero_rate: number
  avg_latency_ms: number
  source: string
  details: RAGEvalCaseResult[]
}

const WINDOWS = [
  { label: '近 7 天', value: 7 },
  { label: '近 14 天', value: 14 },
  { label: '近 30 天', value: 30 },
]

const MODE_LABEL: Record<string, string> = {
  rrf: '块级融合',
  fallback: '文章级兜底',
  cache: '结果缓存',
}

const EVAL_K_OPTIONS = [
  { label: 'k=3', value: 3 },
  { label: 'k=5', value: 5 },
  { label: 'k=10', value: 10 },
]

const EVAL_SAMPLE_OPTIONS = [
  { label: '抽样 10 篇', value: 10 },
  { label: '抽样 20 篇', value: 20 },
  { label: '抽样 50 篇', value: 50 },
]

const LOG_PAGE_SIZE = 20

const fmtInt = (n: number) => (n ?? 0).toLocaleString('zh-CN')
const fmtPct = (v: number) => `${((v ?? 0) * 100).toFixed((v ?? 0) >= 0.1 ? 1 : 2)}%`
const fmtMs = (v: number) => `${(v ?? 0).toFixed((v ?? 0) >= 100 ? 0 : 1)} ms`

export default function KnowledgeInsightsPage() {
  const navigate = useNavigate()
  const canManage = useCan('knowledge:manage')
  const [searchParams, setSearchParams] = useSearchParams()

  // 筛选条件 URL 参数化(F1.3): 刷新/分享/外链直达都保留窗口与过滤条件
  const [days, setDays] = useState(() => {
    const d = Number(searchParams.get('days'))
    return WINDOWS.some(w => w.value === d) ? d : 7
  })
  const [zeroOnly, setZeroOnly] = useState(() => searchParams.get('zero') === '1')
  const [logQ, setLogQ] = useState(() => searchParams.get('q') || '')
  const [page, setPage] = useState(() => Math.max(1, Number(searchParams.get('page')) || 1))

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [stats, setStats] = useState<SearchStats | null>(null)
  const [logs, setLogs] = useState<SearchLog[]>([])
  const [logTotal, setLogTotal] = useState(0)
  const [reindexing, setReindexing] = useState(false)
  // RAG 效果评估(P1-1): 按需触发(一次评估会跑 N 次真实检索, 不该在页面加载时自动跑)
  const [evalK, setEvalK] = useState(5)
  const [evalSample, setEvalSample] = useState(20)
  const [evaluating, setEvaluating] = useState(false)
  const [evalReport, setEvalReport] = useState<RAGEvalReport | null>(null)
  const [evalError, setEvalError] = useState<string | null>(null)

  useEffect(() => {
    const p = new URLSearchParams()
    if (days !== 7) p.set('days', String(days))
    if (zeroOnly) p.set('zero', '1')
    if (logQ.trim()) p.set('q', logQ.trim())
    if (page > 1) p.set('page', String(page))
    setSearchParams(p, { replace: true })
  }, [days, zeroOnly, logQ, page, setSearchParams])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const logParams = new URLSearchParams({
        limit: String(LOG_PAGE_SIZE),
        offset: String((page - 1) * LOG_PAGE_SIZE),
      })
      if (zeroOnly) logParams.set('zero_only', '1')
      if (logQ.trim()) logParams.set('q', logQ.trim())
      const [s, l] = await Promise.all([
        apiFetch<SearchStats>(`/knowledge/search-stats?days=${days}`),
        apiFetch<{ items: SearchLog[]; total: number }>(`/knowledge/search-logs?${logParams.toString()}`),
      ])
      setStats(s)
      setLogs(l.items || [])
      setLogTotal(l.total || 0)
    } catch (err: any) {
      setError(err?.message || '加载检索可观测数据失败')
    } finally {
      setLoading(false)
    }
  }, [days, zeroOnly, logQ, page])

  useEffect(() => {
    if (!canManage) return
    // 关键词输入防抖: 避免每敲一个字就打一次接口
    const timer = setTimeout(load, logQ ? 300 : 0)
    return () => clearTimeout(timer)
  }, [canManage, load, logQ])

  const trendOption = useMemo(() => {
    const series = stats?.series || []
    return {
      xAxisData: series.map(p => p.day),
      metrics: [
        { name: '检索量', data: series.map(p => p.total), areaStyle: true, color: HEALTH_COLOR.healthy },
        { name: '零结果', data: series.map(p => p.zero), color: HEALTH_COLOR.critical },
      ],
    }
  }, [stats])

  // 重建索引: 兜底路有量通常意味着分块索引缺失(未 reindex / embedding 曾失败)。
  // 提交防重 + toast + 数据刷新(F2.2/F2.3)。
  // RAG 效果评估: 提交防重 + toast + 结果落到本地状态(F2.2)。
  const runEval = async () => {
    if (evaluating) return
    setEvaluating(true)
    setEvalError(null)
    try {
      const report = await apiFetch<RAGEvalReport>('/knowledge/rag-eval', {
        method: 'POST',
        body: JSON.stringify({ k: evalK, sample: evalSample }),
      })
      setEvalReport(report)
      getMessage().success(`评估完成：${report.cases} 条用例，命中率 ${fmtPct(report.hit_rate)}`)
    } catch (err: any) {
      const msg = err?.message || '评估失败'
      setEvalError(msg)
      getMessage().error(msg)
    } finally {
      setEvaluating(false)
    }
  }

  // 重建索引: 兜底路有量通常意味着分块索引缺失(未 reindex / embedding 曾失败)。
  const runReindex = async () => {
    if (reindexing) return
    setReindexing(true)
    try {
      const res = await apiFetch<{ reindexed: number }>('/knowledge/reindex', { method: 'POST' })
      getMessage().success(`已重建 ${res?.reindexed ?? 0} 篇文章的索引`)
      await load()
    } catch (err: any) {
      getMessage().error(err?.message || '重建索引失败')
    } finally {
      setReindexing(false)
    }
  }

  if (!canManage) {
    return (
      <div className="p-6">
        <Card><CardContent className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <ShieldAlert className="h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">无访问权限</p>
          <p className="text-xs text-muted-foreground">
            检索留痕包含用户原始提问与召回详情，当前账号缺少「管理知识库（knowledge:manage）」权限。
          </p>
        </CardContent></Card>
      </div>
    )
  }

  const kpis = [
    {
      label: '检索次数', value: fmtInt(stats?.total || 0),
      sub: `窗口 ${days} 天 · 平均召回 ${(stats?.avg_hit_count || 0).toFixed(1)} 篇`,
      icon: Activity, cls: 'text-blue-500',
    },
    {
      label: '零结果率', value: fmtPct(stats?.zero_rate || 0),
      sub: `${fmtInt(stats?.zero_results || 0)} 次无召回（知识盲区）`,
      icon: SearchX, cls: (stats?.zero_rate || 0) > 0.2 ? 'text-red-500' : 'text-amber-500',
    },
    {
      label: '平均延迟', value: fmtMs(stats?.avg_latency_ms || 0),
      sub: `P95 ${fmtMs(stats?.p95_latency_ms || 0)}（含 query 向量化）`,
      icon: Gauge, cls: 'text-violet-500',
    },
    {
      label: '未完成索引', value: fmtInt(stats?.unindexed_articles || 0),
      sub: '无正文块或缺向量的文章',
      icon: FileWarning, cls: (stats?.unindexed_articles || 0) > 0 ? 'text-amber-500' : 'text-emerald-500',
    },
  ]

  const zeroColumns = [
    {
      title: '用户问过但知识库答不出的问题', dataIndex: 'query', key: 'query', ellipsis: true,
      render: (v: string) => <span className="text-sm">{v}</span>,
    },
    { title: '出现次数', dataIndex: 'count', key: 'count', width: 100 },
    {
      title: '最近出现', dataIndex: 'last_at', key: 'last_at', width: 170,
      render: (v: string) => <span className="text-xs text-muted-foreground">{formatTime(v)}</span>,
    },
    {
      title: '操作', key: 'action', width: 200,
      render: (_: unknown, row: SearchQueryStat) => (
        <div className="flex items-center gap-1">
          <Button size="small" type="link" onClick={() => navigate(`/knowledge/new?title=${encodeURIComponent(row.query)}`)}>
            沉淀为知识
          </Button>
          {/* 反查链路: 这句话为什么没召回 —— 进测试台逐路看归因, 而不是猜 */}
          <Button
            size="small" type="link"
            onClick={() => navigate(`/knowledge/retrieval?q=${encodeURIComponent(row.query)}`)}
          >
            测试台复现<ArrowRight className="ml-1 h-3 w-3" />
          </Button>
        </div>
      ),
    },
  ]

  const topColumns = [
    { title: '查询', dataIndex: 'query', key: 'query', ellipsis: true },
    { title: '命中次数', dataIndex: 'count', key: 'count', width: 100 },
    {
      title: '平均召回', dataIndex: 'avg_hits', key: 'avg_hits', width: 100,
      render: (v: number) => `≤ ${(v || 0).toFixed(1)} 篇`,
    },
  ]

  const logColumns = [
    {
      title: '时间', dataIndex: 'created_at', key: 'created_at', width: 170,
      render: (v: string) => <span className="text-xs text-muted-foreground">{formatTime(v)}</span>,
    },
    { title: '查询', dataIndex: 'query', key: 'query', ellipsis: true },
    {
      title: '路径', dataIndex: 'mode', key: 'mode', width: 110,
      render: (v: string) => <Tag>{MODE_LABEL[v] || v}</Tag>,
    },
    {
      title: '各路命中', key: 'routes', width: 130,
      render: (_: unknown, row: SearchLog) => (
        <span className="text-xs text-muted-foreground">向量 {row.vec_hits} · 关键词 {row.kw_hits}</span>
      ),
    },
    { title: '召回', dataIndex: 'hit_count', key: 'hit_count', width: 70 },
    {
      title: '延迟', dataIndex: 'latency_ms', key: 'latency_ms', width: 90,
      render: (v: number) => <span className="text-xs">{fmtMs(v)}</span>,
    },
    {
      title: '结果', dataIndex: 'zero_result', key: 'zero_result', width: 90,
      render: (v: boolean) => (v ? <Tag color="red">零结果</Tag> : <Tag color="green">命中</Tag>),
    },
    {
      title: '来源', key: 'source', width: 150, ellipsis: true,
      render: (_: unknown, row: SearchLog) => (
        <AntTooltip title={`Agent: ${row.agent_id || '-'} / 用户: ${row.user_id || '-'}`}>
          <span className="text-xs text-muted-foreground">{row.user_id || row.agent_id || '系统'}</span>
        </AntTooltip>
      ),
    },
  ]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold">知识库检索可观测</h1>
          <p className="text-sm text-muted-foreground">
            AI 每次检索知识库都会留痕：命中了几条召回路径、召回多少篇、耗时多少、是否零结果。零结果查询就是最该补的知识。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Segmented size="small" value={days} onChange={v => { setDays(Number(v)); setPage(1) }} options={WINDOWS} />
          <Button type="default" onClick={load} loading={loading}>
            <RefreshCw className="h-3.5 w-3.5 mr-1" />刷新
          </Button>
        </div>
      </div>

      {error ? (
        <ErrorState message={`加载检索可观测数据失败：${error}`} onRetry={load} />
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
            {kpis.map(k => (
              <Card key={k.label}>
                <CardContent className="flex items-start justify-between gap-3 pt-6">
                  <div className="min-w-0">
                    <p className="text-xs text-muted-foreground">{k.label}</p>
                    <p className="mt-1 text-2xl font-semibold">{loading && !stats ? '—' : k.value}</p>
                    <p className="mt-1 text-xs text-muted-foreground truncate">{k.sub}</p>
                  </div>
                  <k.icon className={`h-5 w-5 shrink-0 ${k.cls}`} />
                </CardContent>
              </Card>
            ))}
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
            <Card className="lg:col-span-2">
              <CardHeader><CardTitle className="text-base">检索量与零结果趋势</CardTitle></CardHeader>
              <CardContent>
                {(stats?.series || []).length === 0 ? (
                  <Empty description={loading ? '加载中…' : '窗口内还没有检索记录'} />
                ) : (
                  <TimeSeriesChart xAxisData={trendOption.xAxisData} metrics={trendOption.metrics} height={280} showDataZoom={false} />
                )}
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle className="text-base flex items-center gap-2">
                  <Layers className="h-4 w-4" />召回路径贡献
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                {[
                  { name: '块级向量路（语义）', value: stats?.vector_routes || 0 },
                  { name: '块级关键词路（tsvector/ILIKE）', value: stats?.keyword_routes || 0 },
                  { name: '文章级兜底（未索引存量）', value: stats?.fallback_routes || 0 },
                ].map(r => (
                  <div key={r.name} className="flex items-center justify-between gap-2 text-sm">
                    <span className="text-muted-foreground truncate">{r.name}</span>
                    <span className="shrink-0 font-medium">
                      {fmtInt(r.value)}
                      <span className="ml-1 text-xs text-muted-foreground">
                        {stats?.total ? `(${fmtPct(r.value / stats.total)})` : ''}
                      </span>
                    </span>
                  </div>
                ))}
                <p className="pt-1 text-xs text-muted-foreground">
                  兜底路长期有量 = 分块索引没建好（未索引文章 {fmtInt(stats?.unindexed_articles || 0)} 篇），应执行一次重建索引。
                </p>
                {(stats?.fallback_routes || 0) > 0 && (
                  <Button size="small" loading={reindexing} disabled={reindexing} onClick={runReindex}>
                    重建索引
                  </Button>
                )}
              </CardContent>
            </Card>
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle className="text-base flex items-center gap-2">
                  <SearchX className="h-4 w-4" />零结果查询 TOP（知识盲区）
                </CardTitle>
              </CardHeader>
              <CardContent>
                <Table
                  size="small"
                  rowKey="query"
                  columns={zeroColumns}
                  dataSource={stats?.zero_queries || []}
                  loading={loading && !stats}
                  pagination={false}
                  locale={{ emptyText: <Empty description="窗口内没有零结果查询，检索覆盖良好" /> }}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle className="text-base flex items-center gap-2">
                  <Activity className="h-4 w-4" />命中查询 TOP
                </CardTitle>
              </CardHeader>
              <CardContent>
                <Table
                  size="small"
                  rowKey="query"
                  columns={topColumns}
                  dataSource={stats?.top_queries || []}
                  loading={loading && !stats}
                  pagination={false}
                  locale={{ emptyText: <Empty description={loading ? '加载中…' : '窗口内还没有命中记录'} /> }}
                />
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-3 flex-wrap">
              <CardTitle className="text-base flex items-center gap-2">
                <Inbox className="h-4 w-4" />最近检索留痕
              </CardTitle>
              <div className="flex items-center gap-3">
                <div className="flex items-center gap-2">
                  <Switch
                    size="small"
                    checked={zeroOnly}
                    onChange={v => { setZeroOnly(v); setPage(1) }}
                  />
                  <span className="text-xs text-muted-foreground">只看零结果</span>
                </div>
                <Input.Search
                  allowClear
                  size="small"
                  style={{ width: 220 }}
                  placeholder="按提问内容过滤"
                  value={logQ}
                  onChange={e => { setLogQ(e.target.value); setPage(1) }}
                />
              </div>
            </CardHeader>
            <CardContent>
              <Table
                size="small"
                rowKey="id"
                columns={logColumns}
                dataSource={logs}
                loading={loading}
                scroll={{ x: 900 }}
                pagination={{
                  current: page,
                  pageSize: LOG_PAGE_SIZE,
                  total: logTotal,
                  showSizeChanger: false,
                  onChange: setPage,
                  showTotal: total => `共 ${total} 条`,
                }}
                locale={{
                  emptyText: <Empty
                    description={zeroOnly ? '窗口内没有零结果检索' : '窗口内还没有检索留痕'}
                  />,
                }}
              />
              <p className="mt-2 text-xs text-muted-foreground">
                留痕保留 30 天，由后台循环自动清理（{fmtInt(logTotal)} 条符合当前过滤条件）。
              </p>
            </CardContent>
          </Card>

          {/* RAG 效果评估(P1-1): 跑在真实检索路径上的命中率/MRR, 用于回答"检索到底准不准" */}
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-3 flex-wrap">
              <CardTitle className="text-base flex items-center gap-2">
                <Target className="h-4 w-4" />RAG 效果评估
              </CardTitle>
              <div className="flex items-center gap-2">
                <Segmented size="small" value={evalK} onChange={v => setEvalK(Number(v))} options={EVAL_K_OPTIONS} />
                <Segmented size="small" value={evalSample} onChange={v => setEvalSample(Number(v))} options={EVAL_SAMPLE_OPTIONS} />
                <Button type="primary" size="small" loading={evaluating} disabled={evaluating} onClick={runEval}>
                  运行评估
                </Button>
              </div>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-xs text-muted-foreground">
                从已发布文章里随机抽样，用「标题」当查询回打知识库，统计期望文章是否被召回到 top-k。
                抽样评估的绝对分数偏乐观（标题与正文高度重合），真正有意义的是它的变化趋势：
                调整分块、更换 embedding 模型、加入 rerank 后，这个分数应当上升。
              </p>
              {evalError && (
                <p className="text-xs text-destructive">
                  {evalError.includes('501') || evalError.includes('engine')
                    ? '当前未接入 AI 检索引擎（或向量检索未启用），评估不可用。'
                    : evalError}
                </p>
              )}
              {evalReport ? (
                <>
                  <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
                    {[
                      { label: '命中率', value: fmtPct(evalReport.hit_rate) },
                      { label: `Recall@1`, value: fmtPct(evalReport.recall_at_1) },
                      { label: `Recall@${evalReport.k}`, value: fmtPct(evalReport.recall_at_k) },
                      { label: 'MRR', value: (evalReport.mrr || 0).toFixed(3) },
                      { label: '零结果率', value: fmtPct(evalReport.zero_rate) },
                    ].map(m => (
                      <div key={m.label} className="rounded-md border p-3">
                        <p className="text-xs text-muted-foreground">{m.label}</p>
                        <p className="mt-1 text-lg font-semibold">{m.value}</p>
                      </div>
                    ))}
                  </div>
                  <p className="text-xs text-muted-foreground">
                    用例 {evalReport.cases} 条（{evalReport.source === 'sampled' ? '按标题自动抽样' : '调用方指定'}），
                    命中 {evalReport.hits} 条，平均检索耗时 {fmtMs(evalReport.avg_latency_ms)}。明细按「未命中优先」排序。
                  </p>
                  <Table
                    size="small"
                    rowKey={r => `${r.query}`}
                    dataSource={evalReport.details}
                    pagination={{ pageSize: 10, hideOnSinglePage: true }}
                    columns={[
                      { title: '查询（标题）', dataIndex: 'query', key: 'query', ellipsis: true },
                      {
                        title: '排名', dataIndex: 'rank', key: 'rank', width: 80,
                        render: (v: number) => (v > 0 ? `第 ${v} 位` : <Tag color="red">未召回</Tag>),
                      },
                      {
                        title: '召回结果', key: 'retrieved', width: 260, ellipsis: true,
                        render: (_: unknown, r: RAGEvalCaseResult) => (
                          <span className="text-xs text-muted-foreground">
                            {r.retrieved.length ? r.retrieved.join(' → ') : '（零结果）'}
                          </span>
                        ),
                      },
                      {
                        title: '延迟', dataIndex: 'latency_ms', key: 'latency_ms', width: 90,
                        render: (v: number) => <span className="text-xs">{fmtMs(v)}</span>,
                      },
                    ]}
                  />
                </>
              ) : (
                <Empty description={evaluating ? '正在跑评估（会执行多次真实检索）…' : '尚未运行评估'} />
              )}
            </CardContent>
          </Card>

          {(stats?.zero_results || 0) > 0 && (            <Card>
              <CardContent className="flex items-start gap-3 pt-6">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                <p className="text-xs text-muted-foreground">
                  零结果不等于知识缺失：也可能是提问与文档用词差异过大（如口语化问法）。建议优先处理「出现次数高 + 确实该有答案」的查询，
                  直接把问题沉淀成新文档（点「沉淀为知识」按钮可带标题跳转新建页）。
                </p>
              </CardContent>
            </Card>
          )}
        </>
      )}
    </div>
  )
}
