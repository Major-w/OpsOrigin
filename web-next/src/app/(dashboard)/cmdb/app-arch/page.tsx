'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { apiFetch, apiList } from '@/lib/utils'
import { Network, RefreshCw, ArrowDown } from 'lucide-react'
import { cn } from '@/lib/utils'

interface AppTopoNode { id: string; name: string; type_id: string; category: string; layer: string }
interface AppTopoEdge { source: string; target: string; type: string }
interface TopoLayer { name: string; nodes: string[] }
interface AppTopology { app_ci_id: string; app_name: string; layers: TopoLayer[]; nodes: AppTopoNode[]; edges: AppTopoEdge[]; total_ci: number }
interface CIModel { id: string; name: string; type_id: string }

const LAYER_META: Record<string, { label: string; color: string; border: string }> = {
  business: { label: '业务层', color: 'bg-emerald-500', border: 'border-emerald-300 dark:border-emerald-600' },
  app: { label: '应用层', color: 'bg-blue-500', border: 'border-blue-300 dark:border-blue-600' },
  middleware: { label: '中间件层', color: 'bg-violet-500', border: 'border-violet-300 dark:border-violet-600' },
  database: { label: '数据层', color: 'bg-amber-500', border: 'border-amber-300 dark:border-amber-600' },
  infra: { label: '基础设施层', color: 'bg-slate-500', border: 'border-slate-300 dark:border-slate-600' },
  other: { label: '其它', color: 'bg-gray-400', border: 'border-gray-300 dark:border-gray-600' },
}

export default function AppArchPage() {
  const [cis, setCis] = useState<CIModel[]>([])
  const [appId, setAppId] = useState('')
  const [depth, setDepth] = useState(2)
  const [topo, setTopo] = useState<AppTopology | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    apiList<CIModel>('/cmdb/ci?limit=2000').then(setCis).catch(() => setCis([]))
  }, [])

  const load = async () => {
    if (!appId) return
    setLoading(true)
    try {
      setTopo(await apiFetch<AppTopology>(`/cmdb/app-topology?app_ci_id=${encodeURIComponent(appId)}&depth=${depth}`))
    } catch (e: any) { setTopo(null); alert(e.message) } finally { setLoading(false) }
  }

  const nodeById = (id: string) => topo?.nodes.find(n => n.id === id)
  const layerNodes = (layer: TopoLayer) => layer.nodes.map(id => nodeById(id)).filter(Boolean) as AppTopoNode[]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">应用架构拓扑</h1>
          <p className="text-sm text-muted-foreground">以应用为中心的业务/分层架构视图，自动按关系聚合上下游资产</p>
        </div>
        <div className="flex gap-2 items-center">
          <select value={appId} onChange={e => setAppId(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 py-1.5 text-sm w-64">
            <option value="">选择应用（CI 资产）…</option>
            {cis.map(c => <option key={c.id} value={c.id}>{c.name} ({c.type_id})</option>)}
          </select>
          <select value={depth} onChange={e => setDepth(Number(e.target.value))} className="h-9 rounded-md border border-input bg-background px-2 py-1.5 text-sm">
            <option value={1}>1 层</option>
            <option value={2}>2 层</option>
            <option value={3}>3 层</option>
          </select>
          <Button onClick={load} disabled={!appId || loading}><Network className="h-4 w-4" />生成视图</Button>
          {topo && <Button variant="outline" onClick={load}><RefreshCw className="h-4 w-4" /></Button>}
        </div>
      </div>

      {!topo ? (
        <Card className="text-center py-20"><CardContent>
          <Network className="h-12 w-12 mx-auto mb-3 opacity-30" />
          <p className="text-muted-foreground text-sm">选择应用并点击「生成视图」，按层展示其关联的中间件、数据库与基础设施</p>
        </CardContent></Card>
      ) : (
        <Card>
          <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
            <CardTitle className="text-sm flex items-center gap-2"><Network className="h-4 w-4" />{topo.app_name} · 共 {topo.total_ci} 个资产</CardTitle>
            <Badge variant="outline">{topo.edges.length} 条关系</Badge>
          </CardHeader>
          <CardContent>
            {/* 分层列布局 */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6 gap-3">
              {topo.layers.map(layer => {
                const meta = LAYER_META[layer.name] || LAYER_META.other
                const nodes = layerNodes(layer)
                return (
                  <div key={layer.name} className={cn('border rounded-lg p-3', meta.border)}>
                    <div className="flex items-center gap-2 mb-2">
                      <span className={cn('h-2.5 w-2.5 rounded-full', meta.color)} />
                      <span className="text-xs font-medium">{meta.label}</span>
                      <Badge variant="secondary" className="text-[9px]">{nodes.length}</Badge>
                    </div>
                    <div className="space-y-1.5">
                      {nodes.map(n => (
                        <div key={n.id} className={cn('border rounded-md px-2 py-1.5 text-xs bg-card', n.id === topo.app_ci_id ? 'border-blue-500 ring-1 ring-blue-500/30' : '')}>
                          <p className="font-medium truncate">{n.name}</p>
                          <p className="text-[10px] text-muted-foreground truncate">{n.type_id}{n.category ? ` · ${n.category}` : ''}</p>
                        </div>
                      ))}
                      {nodes.length === 0 && <p className="text-[10px] text-muted-foreground">（空）</p>}
                    </div>
                  </div>
                )
              })}
            </div>
            {/* 关系线（文本描述） */}
            {topo.edges.length > 0 && (
              <div className="mt-4">
                <p className="text-xs font-medium mb-2 flex items-center gap-1"><ArrowDown className="h-3 w-3" />关联明细</p>
                <div className="flex flex-wrap gap-1.5">
                  {topo.edges.map((e, i) => (
                    <Badge key={i} variant="outline" className="text-[10px]">{nodeById(e.source)?.name || e.source.slice(0, 6)} → {nodeById(e.target)?.name || e.target.slice(0, 6)} <span className="text-muted-foreground ml-1">{e.type}</span></Badge>
                  ))}
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  )
}
