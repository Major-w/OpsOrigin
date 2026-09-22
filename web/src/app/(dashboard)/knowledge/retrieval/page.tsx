import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  AutoComplete, Button, Drawer, Empty, Input, Segmented, Select, Table, Tag, Tooltip as AntTooltip,
} from 'antd'
import { apiFetch } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { useIAMStore } from '@/stores/iam'
import { getMessage } from '@/lib/message'
import { ErrorState } from '@/components/ui/error-state'
import {
  AlertTriangle, ArrowRight, FileText, Inbox, Layers, Search, ShieldAlert, SlidersHorizontal,
  Sparkles, Terminal, Users,
} from 'lucide-react'

// ——— 检索测试台(迭代一 · 批次 4) ———
// 检索是"沉默"的：一次提问没命中，用户只看到「知识库里没有相关内容」。但真实原因可能是四件
// 互斥的事 —— 库里没有 / 分块切碎了 / 向量模型不敏感 / 有内容但提问者无权看见。
// 本页把一次真实检索完整摊开：每条召回路径各命中什么、RRF 与 rerank 怎么算出来、
// ACL 挡掉多少候选、最终进 system prompt 的是哪几段文字。
//
// 数据来源是生产同一条检索路径(searchKnowledgeExplained)，不是另写一套 —— 否则测的是
// "另一套检索"，对线上零解释力。诊断请求不写留痕、不进缓存(后端强制 NoLog + BypassCache)。

interface RetrievalRouteHit {
  rank: number
  article_id: string
  title: string
  chunk_id: string
  heading: string
  source: string
  chunk_index: number
  distance: number
  preview: string
}

interface RetrievalRoute {
  route: string
  count: number
  skipped?: string
  items: RetrievalRouteHit[]
}

interface RetrievalKeyword {
  tsvector: RetrievalRoute
  ilike: RetrievalRoute
}

interface RetrievalFusionRow {
  rank: number
  article_id: string
  title: string
  chunk_id: string
  rrf_score: number
  sim_part: number
  kw_part: number
  title_part: number
  final_score: number
  best_distance: number
  kw_hit: boolean
  title_hit: boolean
  match_count: number
}

interface RetrievalPreviewReport {
  query: string
  mode: string
  route: string
  latency_ms: number
  scoped: boolean
  scope_key: string
  vector: RetrievalRoute
  keyword: RetrievalKeyword
  fusion: RetrievalFusionRow[]
  items: Record<string, unknown>[]
  filtered_by_acl: number
  degraded: Record<string, boolean> | null
  prompt_preview: string
  prompt_items: Record<string, unknown>[]
  warnings: string[] | null
}

interface Category {
  id: string
  name: string
  slug?: string
}

interface HistoryEntry {
  query: string
  k: number
  mode: string
  route: string
  hits: number
  filtered: number
  latency: number
  at: string
}

const MODE_OPTIONS = [
  { label: '双路融合', value: 'rrf' },
  { label: '仅向量', value: 'vector' },
  { label: '仅关键词', value: 'keyword' },
  { label: '文章级兜底', value: 'fallback' },
]

const K_OPTIONS = [
  { label: 'k=3', value: 3 },
  { label: 'k=5', value: 5 },
  { label: 'k=10', value: 10 },
  { label: 'k=20', value: 20 },
]

const ROUTE_LABEL: Record<string, string> = {
  rrf: '双路融合',
  fallback: '文章级兜底',
  cache: '结果缓存',
}

const DEGRADED_LABEL: Record<string, string> = {
  halfvec: 'halfvec 表达式不可用',
  tsvector: 'tsvector 全文检索不可用',
  trgm: 'pg_trgm 索引不可用',
}

const fmtMs = (v: number) => `${(v ?? 0).toFixed((v ?? 0) >= 100 ? 0 : 1)} ms`

// 距离 → 相似度展示口径: 与后端 rerankBonus 一致(相似度 = 1 − 距离/2, 距离 -1 = 无向量命中)。
const similarity = (dist: number) => (dist < 0 ? null : Math.max(0, 1 - dist / 2))

export default function KnowledgeRetrievalPage() {
  const navigate = useNavigate()
  const canManage = useCan('knowledge:manage')
  const [searchParams, setSearchParams] = useSearchParams()
  const { groups, fetchGroups } = useIAMStore()

  // 筛选条件 URL 参数化(F1.3): 从检索可观测页「在测试台复现」可直接深链过来
  const initialQ = searchParams.get('q') || ''
  const [query, setQuery] = useState(initialQ)
  const [k, setK] = useState(() => {
    const v = Number(searchParams.get('k'))
    return K_OPTIONS.some(o => o.value === v) ? v : 5
  })
  const [mode, setMode] = useState(() => {
    const v = searchParams.get('mode') || 'rrf'
    return MODE_OPTIONS.some(o => o.value === v) ? v : 'rrf'
  })
  const [categories, setCategories] = useState<string[]>(() => {
    const raw = searchParams.get('cats')
    return raw ? raw.split(',').map(s => s.trim()).filter(Boolean) : []
  })
  const [scopeUser, setScopeUser] = useState(() => searchParams.get('user') || '')

  const [catOptions, setCatOptions] = useState<Category[]>([])
  const [report, setReport] = useState<RetrievalPreviewReport | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [promptOpen, setPromptOpen] = useState(false)
  const [history, setHistory] = useState<HistoryEntry[]>([])

  const autoRan = useRef(false)

  // 分类与团队只在首次进入时拉一次; 团队列表用于「以身份检索」的候选用户(组内成员)。
  useEffect(() => {
    apiFetch<Category[]>('/knowledge/categories')
      .then(cs => setCatOptions(Array.isArray(cs) ? cs : []))
      .catch(() => setCatOptions([]))
  }, [])
  useEffect(() => {
    if (groups.length === 0) void fetchGroups()
  }, [groups.length, fetchGroups])

  useEffect(() => {
    const p = new URLSearchParams()
    if (query.trim()) p.set('q', query.trim())
    if (k !== 5) p.set('k', String(k))
    if (mode !== 'rrf') p.set('mode', mode)
    if (categories.length) p.set('cats', categories.join(','))
    if (scopeUser.trim()) p.set('user', scopeUser.trim())
    setSearchParams(p, { replace: true })
  }, [query, k, mode, categories, scopeUser, setSearchParams])

  // 「以身份检索」的候选用户来自各团队成员的并集(IAM 无用户列表端点)。
  // 输入框允许任意键入 —— 目标是验证任意账号的可见范围, 不该被候选集限制。
  const userOptions = useMemo(() => {
    const seen = new Map<string, string[]>()
    for (const g of groups) {
      for (const uid of g.user_ids || []) {
        const names = seen.get(uid) || []
        names.push(g.name)
        seen.set(uid, names)
      }
    }
    return Array.from(seen.entries()).map(([uid, names]) => ({
      value: uid,
      label: `${uid}（${names.join('、')}）`,
    }))
  }, [groups])

  const runSearch = useCallback(async () => {
    const q = query.trim()
    if (!q) return
    setSubmitting(true)
    setError(null)
    try {
      const rep = await apiFetch<RetrievalPreviewReport>('/knowledge/retrieval-preview', {
        method: 'POST',
        body: JSON.stringify({
          query: q,
          k,
          mode,
          categories,
          scope_user_id: scopeUser.trim(),
        }),
      })
      setReport(rep)
      setHistory(prev => [{
        query: q, k, mode,
        route: rep.route,
        hits: rep.items?.length || 0,
        filtered: rep.filtered_by_acl || 0,
        latency: rep.latency_ms || 0,
        at: new Date().toLocaleTimeString('zh-CN'),
      }, ...prev].slice(0, 20))
      if (!rep.items?.length) {
        getMessage().warning('零结果 —— 请对照下方归因判断是「内容缺失」还是「权限挡住」')
      }
    } catch (err: unknown) {
      const msg = (err as { message?: string })?.message || '检索失败'
      setError(msg)
      setReport(null)
      getMessage().error(msg)
    } finally {
      setSubmitting(false)
    }
  }, [query, k, mode, categories, scopeUser])

  // 深链直达时自动跑一次(?q=...) —— 否则「在测试台复现」还要用户再点一次检索。
  useEffect(() => {
    if (autoRan.current || !initialQ) return
    autoRan.current = true
    void runSearch()
  }, [initialQ, runSearch])

  const openChunk = (articleID: string, chunkID: string) => {
    if (!articleID) return
    navigate(`/knowledge/${articleID}${chunkID ? `?chunk=${encodeURIComponent(chunkID)}` : ''}`)
  }

  if (!canManage) {
    return (
      <div className="p-6">
        <Card><CardContent className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <ShieldAlert className="h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">无访问权限</p>
          <p className="text-xs text-muted-foreground">
            检索测试台可以把检索归因到「指定身份」，属于敏感能力，需要「管理知识库（knowledge:manage）」权限。
          </p>
        </CardContent></Card>
      </div>
    )
  }

  // 单路召回的命中列表(向量 / tsvector / ILIKE 三处复用)
  const renderRoute = (route?: RetrievalRoute, distLabel = '距离') => {
    if (!route || (route.items?.length || 0) === 0) {
      return (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={route?.skipped ? <span className="text-xs">{route.skipped}</span> : '本次无命中'}
        />
      )
    }
    return (
      <div className="space-y-2">
        {(route.items || []).map(it => (
          <div
            key={`${it.chunk_id}-${it.rank}`}
            className="cursor-pointer rounded-md border p-2 text-xs transition-colors hover:bg-accent"
            onClick={() => openChunk(it.article_id, it.chunk_id)}
            title="点击跳原文并高亮该块"
          >
            <div className="flex items-center justify-between gap-2">
              <span className="truncate font-medium">#{it.rank} {it.title || '(无标题)'}</span>
              {it.distance >= 0 ? (
                <AntTooltip title={`相似度 ${((similarity(it.distance) ?? 0) * 100).toFixed(1)}%（越小越近）`}>
                  <Tag color="blue">{distLabel} {it.distance.toFixed(3)}</Tag>
                </AntTooltip>
              ) : (
                <Tag>无向量距离</Tag>
              )}
            </div>
            {it.heading && <p className="mt-1 truncate text-muted-foreground">{it.heading}</p>}
            <p className="mt-1 line-clamp-3 text-muted-foreground">{it.preview}</p>
            <p className="mt-1 text-[10px] text-muted-foreground">{it.source === 'attachment' ? '附件块' : '正文块'} #{it.chunk_index}</p>
          </div>
        ))}
      </div>
    )
  }

  // 分数分解条: 直观看出"这篇为什么排第一" —— 是靠名次, 还是靠相似度/关键词/标题加成。
  const ScoreBar = ({ row }: { row: RetrievalFusionRow }) => {
    const total = row.rrf_score + row.sim_part + row.kw_part + row.title_part
    if (total <= 0) return null
    const pct = (v: number) => `${Math.max(0, (v / total) * 100)}%`
    const seg = [
      { v: row.rrf_score, label: '名次', cls: 'bg-muted-foreground' },
      { v: row.sim_part, label: '相似度', cls: 'bg-sky-500' },
      { v: row.kw_part, label: '关键词', cls: 'bg-violet-500' },
      { v: row.title_part, label: '标题', cls: 'bg-amber-500' },
    ].filter(s => s.v > 0)
    return (
      <AntTooltip title={seg.map(s => `${s.label} ${s.v.toFixed(3)}`).join(' · ')}>
        <div className="flex h-2 w-full overflow-hidden rounded-full bg-muted">
          {seg.map(s => <div key={s.label} className={s.cls} style={{ width: pct(s.v) }} />)}
        </div>
      </AntTooltip>
    )
  }

  const degradedKeys = Object.entries(report?.degraded || {}).filter(([, v]) => v).map(([kk]) => kk)
  const kwCount = (report?.keyword?.tsvector?.count || 0) + (report?.keyword?.ilike?.count || 0)

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold">知识库检索测试台</h1>
          <p className="text-sm text-muted-foreground">
            用真实检索路径回打一次知识库，把「向量路 / 关键词路 / 融合排序 / 最终进 prompt 的片段」全部摊开。
            查不到时先看这里：是内容没有，还是权限挡住了。
          </p>
        </div>
      </div>

      <Card>
        <CardContent className="space-y-3 pt-6">
          <div className="flex items-center gap-2 flex-wrap">
            <Input
              allowClear
              size="middle"
              style={{ minWidth: 260, flex: 1 }}
              placeholder="例如：磁盘满了怎么处理 / 数据库连接数超限"
              prefix={<Search className="h-3.5 w-3.5 text-muted-foreground" />}
              value={query}
              onChange={e => setQuery(e.target.value)}
              onPressEnter={() => !submitting && runSearch()}
            />
            <Button type="primary" loading={submitting} disabled={submitting || !query.trim()} onClick={runSearch}>
              {submitting ? '检索中…' : '检索'}
            </Button>
          </div>
          <div className="flex items-center gap-3 flex-wrap">
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground"><SlidersHorizontal className="mr-1 inline h-3 w-3" />返回条数</span>
              <Segmented size="small" value={k} onChange={v => setK(Number(v))} options={K_OPTIONS} />
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground">召回模式</span>
              <Segmented size="small" value={mode} onChange={v => setMode(String(v))} options={MODE_OPTIONS} />
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground"><Layers className="mr-1 inline h-3 w-3" />知识分类</span>
              <Select
                mode="multiple"
                allowClear
                size="small"
                style={{ minWidth: 200, maxWidth: 320 }}
                placeholder="不限"
                value={categories}
                onChange={v => setCategories(v || [])}
                options={catOptions.map(c => ({ label: c.name, value: c.name }))}
                optionFilterProp="label"
                maxTagCount="responsive"
              />
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground">
                <Users className="mr-1 inline h-3 w-3" />以身份检索
              </span>
              <AutoComplete
                size="small"
                style={{ minWidth: 240 }}
                placeholder="留空 = 以本人身份"
                value={scopeUser}
                onChange={v => setScopeUser(v || '')}
                options={userOptions}
                filterOption={(input, opt) => String(opt?.value || '').includes(input)}
                allowClear
              />
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            「以身份检索」用于验证可见范围：同一句话换个人问，召回结果应当不同。
            仅按所选用户的团队归属计算可见性，租户恒为当前租户。诊断请求不写检索留痕、不进结果缓存。
          </p>
        </CardContent>
      </Card>

      {error ? (
        <ErrorState message={`检索失败：${error}`} onRetry={runSearch} />
      ) : !report ? (
        <Card>
          <CardContent className="py-16">
            <Empty
              description={
                <span className="text-xs">
                  {submitting ? '正在跑真实检索…' : '输入一个问题后点「检索」，这里会展示各路召回与排序归因'}
                </span>
              }
            />
          </CardContent>
        </Card>
      ) : (
        <>
          {/* 状态条: 一次看全本次检索的基本事实 */}
          <Card>
            <CardContent className="flex flex-wrap items-center gap-x-4 gap-y-2 pt-6 text-xs">
              <Tag color={report.scoped ? 'blue' : 'orange'}>
                {report.scoped ? '按身份过滤' : '仅公开（未解析出身份）'}
              </Tag>
              <span className="text-muted-foreground">路由 <b className="text-foreground">{ROUTE_LABEL[report.route] || report.route}</b></span>
              <span className="text-muted-foreground">模式 <b className="text-foreground">{report.mode}</b></span>
              <span className="text-muted-foreground">延迟 <b className="text-foreground">{fmtMs(report.latency_ms)}</b></span>
              <span className="text-muted-foreground">向量命中 <b className="text-foreground">{report.vector?.count || 0}</b></span>
              <span className="text-muted-foreground">
                关键词命中 <b className="text-foreground">{kwCount}</b>
                <span className="ml-1">（tsvector {report.keyword?.tsvector?.count || 0} / ILIKE {report.keyword?.ilike?.count || 0}）</span>
              </span>
              <span className="text-muted-foreground">融合后 <b className="text-foreground">{report.items?.length || 0}</b> 篇</span>
              <span className="text-muted-foreground">ACL 过滤 <b className="text-foreground">{report.filtered_by_acl || 0}</b></span>
              <span className="text-muted-foreground">可见范围哈希 <b className="text-foreground">{report.scope_key || '-'}</b></span>
              {degradedKeys.length > 0 && (
                <Tag color="orange">
                  降级：{degradedKeys.map(dd => DEGRADED_LABEL[dd] || dd).join('、')}
                </Tag>
              )}
            </CardContent>
          </Card>

          {(report.warnings || []).length > 0 && (
            <Card>
              <CardContent className="space-y-1 pt-6">
                {(report.warnings || []).map((wn, i) => (
                  <div key={i} className="flex items-start gap-2">
                    <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-500" />
                    <p className="text-xs text-muted-foreground">{wn}</p>
                  </div>
                ))}
              </CardContent>
            </Card>
          )}

          {/* 三栏对比: 两条召回路径各自的原始命中 + 融合后的最终结果 */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Sparkles className="h-4 w-4" />向量路（语义）
                  <Tag>{report.vector?.count || 0}</Tag>
                </CardTitle>
              </CardHeader>
              <CardContent className="max-h-[520px] overflow-y-auto">
                {renderRoute(report.vector)}
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Terminal className="h-4 w-4" />关键词路
                  <Tag>tsvector {report.keyword?.tsvector?.count || 0}</Tag>
                  <Tag>ILIKE {report.keyword?.ilike?.count || 0}</Tag>
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-3 max-h-[520px] overflow-y-auto">
                <p className="text-[11px] text-muted-foreground">tsvector（词元匹配，英文/带空格词元）</p>
                {renderRoute(report.keyword?.tsvector)}
                <p className="text-[11px] text-muted-foreground">ILIKE（中文子串匹配）</p>
                {renderRoute(report.keyword?.ilike)}
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Inbox className="h-4 w-4" />融合后 top-{k}
                  <Tag color="green">{report.items?.length || 0}</Tag>
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-2 max-h-[520px] overflow-y-auto">
                {(report.fusion || []).length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="text-xs">本次零结果</span>} />
                ) : (
                  (report.fusion || []).map(row => (
                    <div
                      key={row.article_id}
                      className="cursor-pointer rounded-md border p-2 text-xs transition-colors hover:bg-accent"
                      onClick={() => openChunk(row.article_id, row.chunk_id)}
                      title="点击跳原文并高亮该块"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="truncate font-medium">#{row.rank} {row.title || '(无标题)'}</span>
                        <span className="shrink-0 text-muted-foreground">{row.final_score.toFixed(3)}</span>
                      </div>
                      <div className="mt-1"><ScoreBar row={row} /></div>
                      <p className="mt-1 text-[10px] text-muted-foreground">
                        名次 {row.rrf_score.toFixed(3)} · 相似度 {row.sim_part.toFixed(3)} · 关键词 {row.kw_part.toFixed(3)} · 标题 {row.title_part.toFixed(3)}
                        {row.match_count > 1 && ` · 命中 ${row.match_count} 块`}
                      </p>
                    </div>
                  ))
                )}
              </CardContent>
            </Card>
          </div>

          {/* 融合明细表: 全字段, 便于对照口径 */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base flex items-center gap-2">
                <FileText className="h-4 w-4" />融合明细（最终分 = RRF 名次分 + 0.6×相似度 + 0.25×关键词 + 0.2×标题）
              </CardTitle>
            </CardHeader>
            <CardContent>
              <Table
                size="small"
                rowKey="article_id"
                dataSource={report.fusion || []}
                pagination={false}
                scroll={{ x: 900 }}
                locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="本次零结果" /> }}
                columns={[
                  { title: '排名', dataIndex: 'rank', key: 'rank', width: 60 },
                  {
                    title: '文章', dataIndex: 'title', key: 'title', ellipsis: true,
                    render: (v: string, row: RetrievalFusionRow) => (
                      <Button type="link" size="small" className="px-0" onClick={() => openChunk(row.article_id, row.chunk_id)}>
                        {v || row.article_id}
                      </Button>
                    ),
                  },
                  { title: '名次分', dataIndex: 'rrf_score', key: 'rrf_score', width: 90, render: (v: number) => v.toFixed(3) },
                  {
                    title: '相似度 0.6×', dataIndex: 'sim_part', key: 'sim_part', width: 110,
                    render: (v: number, row: RetrievalFusionRow) => (
                      <AntTooltip title={row.best_distance < 0 ? '无向量命中' : `余弦距离 ${row.best_distance.toFixed(3)}`}>
                        <span>{v.toFixed(3)}</span>
                      </AntTooltip>
                    ),
                  },
                  { title: '关键词 0.25×', dataIndex: 'kw_part', key: 'kw_part', width: 110, render: (v: number) => v.toFixed(3) },
                  { title: '标题 0.2×', dataIndex: 'title_part', key: 'title_part', width: 100, render: (v: number) => v.toFixed(3) },
                  { title: '最终分', dataIndex: 'final_score', key: 'final_score', width: 90, render: (v: number) => <b>{v.toFixed(3)}</b> },
                  { title: '命中块', dataIndex: 'match_count', key: 'match_count', width: 80 },
                ]}
              />
            </CardContent>
          </Card>

          {/* 注入 prompt 预览: 回答"模型到底读到了什么" */}
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-3 flex-wrap pb-2">
              <CardTitle className="text-base flex items-center gap-2">
                <Terminal className="h-4 w-4" />注入 system prompt 的片段
              </CardTitle>
              <Button size="small" onClick={() => setPromptOpen(true)} disabled={!report.prompt_preview}>
                展开全文
              </Button>
            </CardHeader>
            <CardContent>
              <p className="text-xs text-muted-foreground">
                与线上 RAG 注入**同参数**（top-3 / 每段 600 字）跑出来的真实文本，含 <code>[文章ID]</code> 锚点与首块相邻块上下文。
                这里为空 = 模型这次没读到任何知识库内容。
              </p>
              {report.prompt_preview ? (
                <pre className="mt-2 max-h-64 overflow-auto rounded-md border bg-muted/40 p-3 text-[11px] leading-relaxed whitespace-pre-wrap">
                  {report.prompt_preview}
                </pre>
              ) : (
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="text-xs">本次没有片段进入 prompt</span>} className="mt-2" />
              )}
            </CardContent>
          </Card>

          {/* 零结果时的行动指引: 把"没有结果"翻译成下一步做什么 */}
          {(report.items?.length || 0) === 0 && (
            <Card>
              <CardContent className="flex items-start gap-3 pt-6">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                <div className="space-y-2 text-xs text-muted-foreground">
                  <p>
                    {(report.filtered_by_acl || 0) > 0
                      ? `本次有 ${report.filtered_by_acl} 个候选块因可见性被过滤 —— 库里大概率有内容，先核对归属团队/可见范围，而不是急着补文档。`
                      : '本次未见可见性过滤，更可能是内容缺失、分块把答案切碎，或提问用词与文档差异过大。'}
                  </p>
                  <div className="flex items-center gap-2">
                    <Button size="small" onClick={() => navigate(`/knowledge/new?title=${encodeURIComponent(query.trim())}`)}>
                      沉淀为知识<ArrowRight className="ml-1 h-3 w-3" />
                    </Button>
                    <Button size="small" onClick={() => navigate('/knowledge/insights')}>
                      看零结果清单
                    </Button>
                  </div>
                </div>
              </CardContent>
            </Card>
          )}

          {/* 本次会话内的检索记录(刻意不入库: 人工探测写进留痕会污染线上零结果率基线) */}
          {history.length > 0 && (
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-base">本次会话检索记录</CardTitle>
              </CardHeader>
              <CardContent>
                <Table
                  size="small"
                  rowKey={(r: HistoryEntry) => `${r.at}-${r.query}`}
                  dataSource={history}
                  pagination={false}
                  locale={{ emptyText: '暂无' }}
                  columns={[
                    { title: '时间', dataIndex: 'at', key: 'at', width: 100 },
                    {
                      title: '查询', dataIndex: 'query', key: 'query', ellipsis: true,
                      render: (v: string) => (
                        <Button type="link" size="small" className="px-0" onClick={() => setQuery(v)}>{v}</Button>
                      ),
                    },
                    { title: '模式', dataIndex: 'mode', key: 'mode', width: 100, render: (v: string) => ROUTE_LABEL[v] || v },
                    { title: 'k', dataIndex: 'k', key: 'k', width: 60 },
                    { title: '召回', dataIndex: 'hits', key: 'hits', width: 70 },
                    { title: 'ACL 过滤', dataIndex: 'filtered', key: 'filtered', width: 90 },
                    { title: '延迟', dataIndex: 'latency', key: 'latency', width: 90, render: (v: number) => fmtMs(v) },
                  ]}
                />
                <p className="mt-2 text-xs text-muted-foreground">
                  测试台请求不写知识库检索留痕，也不进结果缓存 —— 否则人工探测会混进「线上零结果率」这类基线。
                </p>
              </CardContent>
            </Card>
          )}
        </>
      )}

      <Drawer
        title="注入 system prompt 的完整片段"
        placement="right"
        width={720}
        open={promptOpen}
        onClose={() => setPromptOpen(false)}
      >
        <pre className="whitespace-pre-wrap text-xs leading-relaxed">{report?.prompt_preview || '（空）'}</pre>
      </Drawer>
    </div>
  )
}
