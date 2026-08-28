
/**
 * CMDB 概览 —— 配置管理的统一入口。
 *
 * 排布顺序刻意贴合运维找资产的真实动线：
 *   1. 全局搜索：绝大多数进入 CMDB 的诉求就是「这台机器/这个应用在哪」，先给搜索框；
 *   2. 资源统计：搜不到时先看家底有什么；
 *   3. 拓扑视图：知道是谁之后，看它连着谁；
 *   4. 实例视图：需要精细维护属性时进列表；
 *   5. 机房机柜：物理定位收尾。
 *
 * 本页只做「入口 + 概要」，不重复实现各子页的完整能力，避免维护两份逻辑。
 */

import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useNavigate } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Input, Tag } from 'antd'
import { apiFetch, apiList, cn, formatTime } from '@/lib/utils'
import {
  Search, Boxes, Database, Share2, Server, BarChart3, Network, Building2,
  ArrowRight, RefreshCw, Layers, Radio, Package,
} from 'lucide-react'

interface Stats {
  total_models: number
  total_instances: number
  total_relations: number
  by_category: { category: string; models: number; instances: number }[]
  by_status: { status: string; count: number }[]
  by_type: { type_id: string; type_name: string; category: string; count: number }[]
}

interface CIItem {
  id: string
  name: string
  type_id: string
  status: string
  description?: string
  updated_at: string
}

interface IDCRoom { id: string; name: string; location?: string }

const STATUS_LABELS: Record<string, string> = { active: '正常', inactive: '停用', archived: '归档' }

export default function CMDBOverviewPage() {
  const navigate = useNavigate()
  const [stats, setStats] = useState<Stats | null>(null)
  const [recent, setRecent] = useState<CIItem[]>([])
  const [rooms, setRooms] = useState<IDCRoom[]>([])
  const [loading, setLoading] = useState(true)

  // 搜索：本页只做「快速命中」，输入后回车或点更多跳转全局搜索页
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<CIItem[] | null>(null)
  const [searching, setSearching] = useState(false)

  const load = async () => {
    setLoading(true)
    const [s, cis, idc] = await Promise.all([
      apiFetch<Stats>('/cmdb/stats').catch(() => null),
      apiList<CIItem>('/cmdb/ci?limit=200').catch(() => [] as CIItem[]),
      apiList<IDCRoom>('/cmdb/idc').catch(() => [] as IDCRoom[]),
    ])
    setStats(s)
    setRecent(
      [...cis]
        .sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
        .slice(0, 6)
    )
    setRooms(idc)
    setLoading(false)
  }

  useEffect(() => { load() }, [])

  const runSearch = async () => {
    const kw = q.trim()
    if (!kw) { setHits(null); return }
    setSearching(true)
    try {
      const items = await apiList<CIItem>('/cmdb/ci?limit=500')
      const lower = kw.toLowerCase()
      setHits(items.filter(i =>
        i.name?.toLowerCase().includes(lower) ||
        i.description?.toLowerCase().includes(lower) ||
        i.type_id?.toLowerCase().includes(lower)
      ).slice(0, 8))
    } catch { setHits([]) } finally { setSearching(false) }
  }

  const maxCat = Math.max(1, ...(stats?.by_category || []).map(c => c.instances))
  const activeCount = (stats?.by_status || []).find(s => s.status === 'active')?.count ?? 0

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">CMDB 配置管理</h1>
          <p className="text-sm text-muted-foreground">从搜索定位到关系穿透，再到物理位置，一站式查清任意资产</p>
        </div>
        <button onClick={load} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
        </button>
      </div>

      {/* 0. 快速操作：让概览从只读看板变成配置库入口 */}
      <div className="flex flex-wrap gap-3">
        <Link to="/cmdb/models">
          <Button type="default" className="h-10"><Package className="h-4 w-4" />新建模型</Button>
        </Link>
        <Link to="/cmdb/instances">
          <Button type="default" className="h-10"><Database className="h-4 w-4" />录入实例</Button>
        </Link>
        <Link to="/cmdb/auto-collect">
          <Button type="default" className="h-10"><Radio className="h-4 w-4" />配置自动采集</Button>
        </Link>
      </div>

      {/* 1. 全局搜索 */}
      <Card className="border-blue-200 dark:border-blue-500/20">
        <CardContent className="p-5 space-y-3">
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <Input
                className="pl-9 h-11"
                placeholder="搜索资产名称 / 描述 / 类型，例如 nginx、mysql、10.0.0.1 …"
                value={q}
                onChange={e => setQ(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && runSearch()}
              />
            </div>
            <Button className="h-11" onClick={runSearch} disabled={searching}>
              {searching ? '搜索中…' : '搜索'}
            </Button>
            <Link to="/cmdb/search">
              <Button type="default" className="h-11">高级检索</Button>
            </Link>
          </div>

          {hits !== null && (
            hits.length === 0 ? (
              <p className="text-sm text-muted-foreground px-1">没有命中「{q}」，换个关键词或去高级检索按类型筛选。</p>
            ) : (
              <div className="border rounded-lg divide-y">
                {hits.map(h => (
                  <button
                    key={h.id}
                    onClick={() => navigate(`/cmdb/instances?ci=${encodeURIComponent(h.id)}`)}
                    className="w-full flex items-center gap-3 px-3 py-2 text-left hover:bg-accent transition-colors"
                  >
                    <Server className="h-4 w-4 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium truncate flex-1">{h.name}</span>
                    <Tag className="text-[10px]">{h.type_id}</Tag>
                    <Tag color={h.status === 'active' ? 'success' : 'default'} className="text-[10px]">
                      {h.status}
                    </Tag>
                    <ArrowRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                  </button>
                ))}
              </div>
            )
          )}
        </CardContent>
      </Card>

      {/* 2. 资源统计 */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2"><BarChart3 className="h-4 w-4" />资源统计</h2>
          <Link to="/cmdb/stats" className="text-xs text-blue-600 dark:text-blue-400 hover:underline inline-flex items-center gap-0.5">
            完整统计<ArrowRight className="h-3 w-3" />
          </Link>
        </div>

        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          {[
            { n: stats?.total_models ?? 0, label: '资源模型', icon: Boxes, color: 'blue' as const },
            { n: stats?.total_instances ?? 0, label: '配置项实例', icon: Database, color: 'blue' as const },
            { n: stats?.total_relations ?? 0, label: '关联关系', icon: Share2, color: 'violet' as const },
            { n: activeCount, label: '在用资产', icon: Server, color: 'amber' as const },
          ].map(s => (
            <Card key={s.label}><CardContent className="p-4 flex items-center gap-3">
              <div className={cn('h-10 w-10 rounded-lg flex items-center justify-center',
                s.color === 'blue' && 'bg-blue-100 dark:bg-blue-500/20',
                s.color === 'blue' && 'bg-blue-100 dark:bg-blue-500/20',
                s.color === 'violet' && 'bg-violet-100 dark:bg-violet-500/20',
                s.color === 'amber' && 'bg-amber-100 dark:bg-amber-500/20')}>
                <s.icon className={cn('h-5 w-5',
                  s.color === 'blue' && 'text-blue-600 dark:text-blue-400',
                  s.color === 'blue' && 'text-blue-600 dark:text-blue-400',
                  s.color === 'violet' && 'text-violet-600 dark:text-violet-400',
                  s.color === 'amber' && 'text-amber-600 dark:text-amber-400')} />
              </div>
              <div><p className="text-2xl font-bold">{s.n}</p><p className="text-xs text-muted-foreground">{s.label}</p></div>
            </CardContent></Card>
          ))}
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm">按资源分类</CardTitle></CardHeader>
            <CardContent className="space-y-3">
              {!stats || stats.by_category.length === 0
                ? <p className="text-sm text-muted-foreground">暂无数据，先在「配置项设置 · 模型管理」建模，或开启自动采集。</p>
                : stats.by_category.slice(0, 6).map(c => (
                  <div key={c.category}>
                    <div className="flex justify-between text-xs mb-1">
                      <span className="font-medium">{c.category}</span>
                      <span className="text-muted-foreground">{c.models} 模型 · {c.instances} 实例</span>
                    </div>
                    <div className="h-2 rounded bg-muted overflow-hidden">
                      <div className="h-full rounded bg-blue-500" style={{ width: `${(c.instances / maxCat) * 100}%` }} />
                    </div>
                  </div>
                ))}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm">模型实例 Top</CardTitle></CardHeader>
            <CardContent className="space-y-2">
              {!stats || stats.by_type.length === 0
                ? <p className="text-sm text-muted-foreground">暂无数据</p>
                : stats.by_type.slice(0, 6).map(t => {
                  const maxType = Math.max(1, ...stats.by_type.map(x => x.count))
                  return (
                    <div key={t.type_id} className="flex items-center gap-2 text-xs">
                      <span className="w-32 truncate font-medium">{t.type_name}</span>
                      <div className="flex-1 h-2 rounded bg-muted overflow-hidden">
                        <div className="h-full rounded bg-blue-500" style={{ width: `${(t.count / maxType) * 100}%` }} />
                      </div>
                      <span className="w-8 text-right text-muted-foreground">{t.count}</span>
                    </div>
                  )
                })}
            </CardContent>
          </Card>
        </div>
      </section>

      {/* 3. 拓扑视图 */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2"><Network className="h-4 w-4" />拓扑视图</h2>
          <Link to="/cmdb/topology" className="text-xs text-blue-600 dark:text-blue-400 hover:underline inline-flex items-center gap-0.5">
            打开拓扑<ArrowRight className="h-3 w-3" />
          </Link>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Link to="/cmdb/topology" className="group">
            <Card className="h-full transition-all duration-300 hover:border-blue-300 dark:hover:border-blue-500/40 hover:shadow-md">
              <CardContent className="p-5 flex items-start gap-4">
                <div className="h-11 w-11 rounded-lg bg-violet-100 dark:bg-violet-500/20 flex items-center justify-center shrink-0">
                  <Network className="h-5 w-5 text-violet-600 dark:text-violet-400" />
                </div>
                <div className="min-w-0">
                  <p className="font-medium text-sm flex items-center gap-1">
                    关系图谱
                    <ArrowRight className="h-3.5 w-3.5 opacity-0 group-hover:opacity-100 group-hover:translate-x-0.5 transition-all" />
                  </p>
                  <p className="text-xs text-muted-foreground mt-1">
                    以任意 CI 为根展开 3 层依赖，支持拖拽布局与影响面分析，当前共 {stats?.total_relations ?? 0} 条关系。
                  </p>
                </div>
              </CardContent>
            </Card>
          </Link>
          <Link to="/cmdb/topology" className="group">
            <Card className="h-full transition-all duration-300 hover:border-blue-300 dark:hover:border-blue-500/40 hover:shadow-md">
              <CardContent className="p-5 flex items-start gap-4">
                <div className="h-11 w-11 rounded-lg bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
                  <Layers className="h-5 w-5 text-blue-600 dark:text-blue-400" />
                </div>
                <div className="min-w-0">
                  <p className="font-medium text-sm flex items-center gap-1">
                    应用架构
                    <ArrowRight className="h-3.5 w-3.5 opacity-0 group-hover:opacity-100 group-hover:translate-x-0.5 transition-all" />
                  </p>
                  <p className="text-xs text-muted-foreground mt-1">
                    以应用为中心按业务层 / 应用层 / 中间件 / 数据层 / 基础设施分层聚合上下游资产。
                  </p>
                </div>
              </CardContent>
            </Card>
          </Link>
        </div>
      </section>

      {/* 4. 实例视图 */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2"><Server className="h-4 w-4" />实例视图</h2>
          <Link to="/cmdb/instances" className="text-xs text-blue-600 dark:text-blue-400 hover:underline inline-flex items-center gap-0.5">
            全部实例<ArrowRight className="h-3 w-3" />
          </Link>
        </div>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm">最近变更</CardTitle></CardHeader>
          <CardContent className="p-0">
            {recent.length === 0 ? (
              <div className="px-4 py-6 text-sm text-muted-foreground">
                还没有配置项。可以到
                <Link to="/cmdb/auto-collect" className="text-blue-600 dark:text-blue-400 hover:underline mx-1 inline-flex items-center gap-0.5">
                  <Radio className="h-3 w-3" />自动采集
                </Link>
                开启发现与调度，让资产自己长出来。
              </div>
            ) : (
              <div className="divide-y">
                {recent.map(i => (
                  <Link key={i.id}
                    to={`/cmdb/instances?ci=${encodeURIComponent(i.id)}`}
                    className="flex items-center gap-3 px-4 py-2.5 hover:bg-accent transition-colors"
                  >
                    <span className="text-sm font-medium truncate flex-1">{i.name}</span>
                    <Tag className="text-[10px]">{i.type_id}</Tag>
                    <Tag color={i.status === 'active' ? 'success' : 'default'} className="text-[10px]">
                      {i.status}
                    </Tag>
                    <span className="text-[11px] text-muted-foreground w-36 text-right shrink-0">{formatTime(i.updated_at)}</span>
                  </Link>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      {/* 5. 机房机柜视图 */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2"><Building2 className="h-4 w-4" />机房机柜视图</h2>
          <Link to="/cmdb/idc" className="text-xs text-blue-600 dark:text-blue-400 hover:underline inline-flex items-center gap-0.5">
            打开机柜视图<ArrowRight className="h-3 w-3" />
          </Link>
        </div>
        <Card>
          <CardContent className="p-4">
            {rooms.length === 0 ? (
              <p className="text-sm text-muted-foreground">尚未登记机房。到「机房机柜」新建机房与机柜后，可把配置项挂到具体 U 位实现物理定位。</p>
            ) : (
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
                {rooms.slice(0, 8).map(r => (
                  <Link key={r.id} to="/cmdb/idc" className="group">
                    <div className="border rounded-lg p-3 transition-all duration-300 hover:border-blue-300 dark:hover:border-blue-500/40 hover:-translate-y-0.5">
                      <div className="flex items-center gap-2">
                        <Building2 className="h-4 w-4 text-muted-foreground" />
                        <span className="text-sm font-medium truncate">{r.name}</span>
                      </div>
                      <p className="text-[11px] text-muted-foreground mt-1 truncate">{r.location || '未填写位置'}</p>
                    </div>
                  </Link>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </section>
    </div>
  )
}
