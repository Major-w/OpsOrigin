'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { apiFetch } from '@/lib/utils'
import { RefreshCw, Boxes, Database, Share2, Server, BarChart3 } from 'lucide-react'
import { cn } from '@/lib/utils'

interface Stats {
  total_models: number
  total_instances: number
  total_relations: number
  by_category: { category: string; models: number; instances: number }[]
  by_status: { status: string; count: number }[]
  by_type: { type_id: string; type_name: string; category: string; count: number }[]
  by_attr: { key: string; value: string; count: number }[]
}

const STATUS_LABELS: Record<string, string> = { active: '正常', inactive: '停用', archived: '归档' }

export default function CMDBStatsPage() {
  const [stats, setStats] = useState<Stats | null>(null)
  const [loading, setLoading] = useState(true)

  const load = async () => {
    setLoading(true)
    try { setStats(await apiFetch<Stats>('/cmdb/stats')) } catch { setStats(null) } finally { setLoading(false) }
  }

  useEffect(() => { load() }, [])

  const maxType = Math.max(1, ...(stats?.by_type || []).map(t => t.count))
  const maxAttr = Math.max(1, ...(stats?.by_attr || []).map(a => a.count))
  const maxCat = Math.max(1, ...(stats?.by_category || []).map(c => c.instances))

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">IT 资源统计</h1>
          <p className="text-sm text-muted-foreground">按模型分类 / 实例状态 / 模型类型 / 属性聚合的资源总览</p>
        </div>
        <button onClick={load} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
        </button>
      </div>

      {/* 总览卡片 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {[
          { n: stats?.total_models ?? 0, label: '资源模型', icon: Boxes, color: 'emerald' },
          { n: stats?.total_instances ?? 0, label: '配置项实例', icon: Database, color: 'blue' },
          { n: stats?.total_relations ?? 0, label: '关联关系', icon: Share2, color: 'violet' },
          { n: (stats?.by_status || []).reduce((s, x) => s + x.count, 0), label: '活跃资产', icon: Server, color: 'amber' },
        ].map(s => (
          <Card key={s.label}><CardContent className="p-4 flex items-center gap-3">
            <div className={cn('h-10 w-10 rounded-lg flex items-center justify-center',
              s.color === 'emerald' && 'bg-emerald-100 dark:bg-emerald-500/20',
              s.color === 'blue' && 'bg-blue-100 dark:bg-blue-500/20',
              s.color === 'violet' && 'bg-violet-100 dark:bg-violet-500/20',
              s.color === 'amber' && 'bg-amber-100 dark:bg-amber-500/20')}>
              <s.icon className={cn('h-5 w-5',
                s.color === 'emerald' && 'text-emerald-600 dark:text-emerald-400',
                s.color === 'blue' && 'text-blue-600 dark:text-blue-400',
                s.color === 'violet' && 'text-violet-600 dark:text-violet-400',
                s.color === 'amber' && 'text-amber-600 dark:text-amber-400')} />
            </div>
            <div><p className="text-2xl font-bold">{s.n}</p><p className="text-xs text-muted-foreground">{s.label}</p></div>
          </CardContent></Card>
        ))}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {/* 按分类 */}
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><BarChart3 className="h-4 w-4" />按资源分类</CardTitle></CardHeader>
          <CardContent className="space-y-3">
            {!stats || stats.by_category.length === 0 ? <p className="text-sm text-muted-foreground">暂无数据</p> : stats.by_category.map(c => (
              <div key={c.category}>
                <div className="flex justify-between text-xs mb-1">
                  <span className="font-medium">{c.category}</span>
                  <span className="text-muted-foreground">{c.models} 模型 · {c.instances} 实例</span>
                </div>
                <div className="h-2 rounded bg-muted overflow-hidden">
                  <div className="h-full rounded bg-emerald-500" style={{ width: `${(c.instances / maxCat) * 100}%` }} />
                </div>
              </div>
            ))}
          </CardContent>
        </Card>

        {/* 按状态 */}
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Server className="h-4 w-4" />按实例状态</CardTitle></CardHeader>
          <CardContent className="flex gap-3 flex-wrap">
            {!stats || stats.by_status.length === 0 ? <p className="text-sm text-muted-foreground">暂无数据</p> : stats.by_status.map(s => (
              <div key={s.status} className="flex-1 min-w-[120px] border rounded-lg p-3">
                <p className="text-2xl font-bold">{s.count}</p>
                <Badge variant={s.status === 'active' ? 'success' : 'secondary'}>{STATUS_LABELS[s.status] || s.status}</Badge>
              </div>
            ))}
          </CardContent>
        </Card>

        {/* 按模型类型 Top */}
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Database className="h-4 w-4" />模型实例 Top</CardTitle></CardHeader>
          <CardContent className="space-y-2">
            {!stats || stats.by_type.length === 0 ? <p className="text-sm text-muted-foreground">暂无数据</p> : stats.by_type.map(t => (
              <div key={t.type_id} className="flex items-center gap-2 text-xs">
                <span className="w-36 truncate font-medium">{t.type_name}</span>
                <div className="flex-1 h-2 rounded bg-muted overflow-hidden">
                  <div className="h-full rounded bg-blue-500" style={{ width: `${(t.count / maxType) * 100}%` }} />
                </div>
                <span className="w-8 text-right text-muted-foreground">{t.count}</span>
              </div>
            ))}
          </CardContent>
        </Card>

        {/* 按属性聚合 */}
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><BarChart3 className="h-4 w-4" />按属性聚合（Top 属性值）</CardTitle></CardHeader>
          <CardContent className="space-y-2">
            {!stats || stats.by_attr.length === 0 ? <p className="text-sm text-muted-foreground">暂无数据</p> : stats.by_attr.map(a => (
              <div key={a.key + a.value} className="flex items-center gap-2 text-xs">
                <Badge variant="outline" className="w-24 justify-center shrink-0 text-[10px]">{a.key}</Badge>
                <span className="w-40 truncate">{a.value}</span>
                <div className="flex-1 h-2 rounded bg-muted overflow-hidden">
                  <div className="h-full rounded bg-violet-500" style={{ width: `${(a.count / maxAttr) * 100}%` }} />
                </div>
                <span className="w-8 text-right text-muted-foreground">{a.count}</span>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
