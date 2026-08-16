'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { GitBranch, Plus, Trash2, Play, Network } from 'lucide-react'
import { cn } from '@/lib/utils'

interface ModelInfo { id: string; name: string; category: string; parent_id?: string }
interface ChainHop { relation: string; direction: string }
interface LayerResult { depth: number; type_id: string; cis: { id: string; name: string; type_id: string; status: string }[] }

const RELATION_TYPES = ['depends_on', 'hosts', 'connects', 'contains']

export default function CMDBChainQueryPage() {
  const [models, setModels] = useState<ModelInfo[]>([])
  const [startType, setStartType] = useState('')
  const [hops, setHops] = useState<ChainHop[]>([])
  const [layers, setLayers] = useState<LayerResult[] | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    apiList<ModelInfo>('/cmdb/model-type?limit=1000')
      .then(list => { setModels(list); if (list.length) setStartType(list[0].id) })
      .catch(() => setModels([]))
  }, [])

  const run = async () => {
    if (!startType) return
    if (!hops.length) { toast.error('请至少添加一跳关系'); return }
    setLoading(true)
    try {
      const path = hops.map(h => ({ relation: h.relation, direction: h.direction }))
      const data = await apiFetch<{ layers: LayerResult[] }>('/cmdb/query/chain', {
        method: 'POST', body: JSON.stringify({ start_type: startType, path, max_depth: hops.length }),
      })
      setLayers(data.layers)
    } catch (e: any) { toast.error('查询失败: ' + e.message) }
    finally { setLoading(false) }
  }

  const layerColor = (d: number) => ['bg-emerald-500', 'bg-sky-500', 'bg-violet-500', 'bg-amber-500', 'bg-rose-500'][d % 5]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2"><GitBranch className="h-6 w-6" />综合查询</h1>
          <p className="text-sm text-muted-foreground">跨模型关联查询：从起点模型出发，沿关系链分层展开</p>
        </div>
      </div>

      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm">查询构建</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-xs text-muted-foreground">起点模型:</span>
            <select className="h-8 rounded-md border border-input bg-background px-2 text-xs" value={startType} onChange={e => setStartType(e.target.value)}>
              {models.filter(m => !m.parent_id).map(m => <option key={m.id} value={m.id}>{m.name}（{m.id}）</option>)}
            </select>
          </div>

          {/* 关系链构建 */}
          <div className="space-y-2">
            {hops.map((h, i) => (
              <div key={i} className="flex items-center gap-2">
                <span className="text-xs text-muted-foreground w-14">第 {i + 1} 跳</span>
                <select className="h-8 rounded-md border border-input bg-background px-2 text-xs" value={h.relation}
                  onChange={e => setHops(hs => hs.map((x, j) => j === i ? { ...x, relation: e.target.value } : x))}>
                  {RELATION_TYPES.map(r => <option key={r} value={r}>{r}</option>)}
                </select>
                <select className="h-8 rounded-md border border-input bg-background px-2 text-xs" value={h.direction}
                  onChange={e => setHops(hs => hs.map((x, j) => j === i ? { ...x, direction: e.target.value } : x))}>
                  <option value="out">out（本层 → 下一层）</option>
                  <option value="in">in（下一层 → 本层）</option>
                </select>
                <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setHops(hs => hs.filter((_, j) => j !== i))}><Trash2 className="h-3 w-3" /></Button>
              </div>
            ))}
            {hops.length < 5 && (
              <Button variant="outline" size="sm" onClick={() => setHops(hs => [...hs, { relation: 'depends_on', direction: 'out' }])}><Plus className="h-3 w-3 mr-1" />添加关系跳</Button>
            )}
          </div>

          <div className="flex items-center gap-2">
            <Button onClick={run} disabled={loading}><Play className="h-3.5 w-3.5 mr-1" />{loading ? '查询中...' : '执行查询'}</Button>
            <span className="text-xs text-muted-foreground">示例：业务服务 → depends_on → 应用 → hosts → 服务器（机柜）</span>
          </div>
        </CardContent>
      </Card>

      {layers && (
        <div className="space-y-4">
          <div className="flex items-center gap-3">
            {layers.map((l, i) => (
              <div key={i} className="flex items-center gap-3">
                <div className="text-center">
                  <span className={cn('inline-block h-2 w-14 rounded', layerColor(i))} />
                  <p className="text-[10px] text-muted-foreground mt-0.5">{l.type_id} · {l.cis.length}</p>
                </div>
                {i < layers.length - 1 && <Network className="h-3 w-3 text-muted-foreground" />}
              </div>
            ))}
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
            {layers.map((l, i) => (
              <Card key={i}>
                <CardHeader className="py-3">
                  <CardTitle className="text-sm flex items-center gap-2">
                    <span className={cn('h-2 w-2 rounded-full', layerColor(i))} />
                    第 {i} 层 · {l.type_id}（{l.cis.length}）
                  </CardTitle>
                </CardHeader>
                <CardContent className="p-0 max-h-80 overflow-y-auto">
                  {l.cis.length === 0 ? <p className="px-4 py-4 text-xs text-muted-foreground">无结果</p> : l.cis.map(ci => (
                    <div key={ci.id} className="px-4 py-2 border-b last:border-0 flex items-center gap-2 text-xs">
                      <span className="font-medium truncate">{ci.name || ci.id}</span>
                      <Badge variant="outline" className="text-[9px] ml-auto">{ci.type_id}</Badge>
                      <Badge variant={ci.status === 'active' ? 'secondary' : 'outline'} className="text-[9px]">{ci.status}</Badge>
                    </div>
                  ))}
                </CardContent>
              </Card>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
