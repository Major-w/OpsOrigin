'use client'

import { useEffect, useState, useMemo, useCallback } from 'react'
import { useCMDBStore } from '@/stores/cmdb'
import { apiFetch, cn } from '@/lib/utils'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import PanoramaGraph, { type PanoramaGraphNode, type PanoramaGraphEdge } from '@/components/cmdb/PanoramaGraph'
import {
  Activity, RefreshCw, Search, Network, AlertTriangle, Server, Database, Globe, Boxes,
} from 'lucide-react'

// 对齐后端 PanoramaResponse。
interface PanoramaAlert {
  id: string
  ciId: string
  severity: string
  rootCause: boolean
  rootCauseCi?: string
  message: string
}
interface PanoramaSummary {
  total: number
  critical: number
  warning: number
  healthy: number
  unknown: number
}
interface PanoramaResp {
  nodes: PanoramaGraphNode[]
  edges: PanoramaGraphEdge[]
  alerts: PanoramaAlert[]
  summary: PanoramaSummary
}

// 类型 → 图标（leaf 引擎映射回分类图标）。
const TYPE_ICON: Record<string, any> = {
  mysql: Database, postgresql: Database, oracle: Database,
  redis: Database, mongodb: Database,
  nginx: Globe, elasticsearch: Boxes, kafka: Boxes, zookeeper: Boxes, rabbitmq: Boxes,
}
const HEALTH_BADGE: Record<string, 'destructive' | 'warning' | 'success' | 'secondary'> = {
  critical: 'destructive', warning: 'warning', healthy: 'success', unknown: 'secondary',
}
const HEALTH_COLOR: Record<string, string> = {
  critical: '#ef4444', warning: '#f59e0b', healthy: '#22c55e', unknown: '#94a3b8',
}

export default function PanoramaPage() {
  const { items, fetch } = useCMDBStore()
  const [scope, setScope] = useState('')
  const [searchQ, setSearchQ] = useState('')
  const [layers, setLayers] = useState({ cmdb: true, callchain: true })
  const [data, setData] = useState<PanoramaResp | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [selectedNode, setSelectedNode] = useState<string | null>(null)

  useEffect(() => {
    fetch({ limit: 500 })
  }, [fetch])

  useEffect(() => {
    if (!scope && items.length > 0) setScope(items[0].id)
  }, [items, scope])

  const loadPanorama = useCallback(async () => {
    if (!scope) return
    setLoading(true)
    setError('')
    try {
      const layerParam = [
        layers.cmdb ? 'cmdb' : '',
        layers.callchain ? 'callchain' : '',
      ].filter(Boolean).join(',')
      const resp = await apiFetch<PanoramaResp>(
        `/panorama?scope=${encodeURIComponent(scope)}&layers=${layerParam}`
      )
      setData(resp)
    } catch (e: any) {
      setError(e?.message || '加载全景失败')
    } finally {
      setLoading(false)
    }
  }, [scope, layers])

  useEffect(() => {
    if (scope) loadPanorama()
  }, [scope, loadPanorama])

  const filteredCIs = useMemo(() =>
    items.filter(ci =>
      ci.name.toLowerCase().includes(searchQ.toLowerCase()) ||
      ci.type_id.toLowerCase().includes(searchQ.toLowerCase())
    ), [items, searchQ])

  const selectedDetail = useMemo(() =>
    selectedNode ? data?.nodes.find(n => n.id === selectedNode) || null : null
  , [selectedNode, data])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Activity className="h-6 w-6 text-cyan-500" />业务可观测全景拓扑
          </h1>
          <p className="text-sm text-muted-foreground">一张图融合 CMDB 拓扑（纵向）+ 横向调用链 + 监控健康度与告警 rollup</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={loadPanorama} disabled={!scope || loading}>
            <RefreshCw className={cn('h-3.5 w-3.5 mr-1', loading && 'animate-spin')} />刷新
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-4 gap-4">
        {/* 左栏：根节点选择 + 图层 + 汇总 + 告警 */}
        <div className="lg:col-span-1 space-y-4">
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">根节点 (scope)</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="relative">
                <Input className="h-8 text-xs pl-8" placeholder="搜索 CI..." value={searchQ}
                  onChange={e => setSearchQ(e.target.value)} />
                <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
              </div>
              <div className="max-h-[260px] overflow-y-auto space-y-1 border rounded-md p-2">
                {filteredCIs.length === 0 ? (
                  <p className="text-xs text-muted-foreground text-center py-4">暂无 CI</p>
                ) : filteredCIs.map(ci => {
                  const Icon = TYPE_ICON[ci.type_id] || Server
                  return (
                    <button key={ci.id}
                      className={cn('w-full text-left px-2 py-1.5 rounded text-xs flex items-center gap-2 hover:bg-muted/50',
                        scope === ci.id && 'bg-cyan-500/10 border border-cyan-500/30')}
                      onClick={() => setScope(ci.id)}>
                      <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                      <span className="truncate flex-1">{ci.name}</span>
                      <Badge variant="outline" className="text-[9px]">{ci.type_id}</Badge>
                    </button>
                  )
                })}
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm flex items-center gap-2"><Network className="h-4 w-4" />图层</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              <label className="flex items-center gap-2 text-xs cursor-pointer">
                <input type="checkbox" checked={layers.cmdb} onChange={e => setLayers(l => ({ ...l, cmdb: e.target.checked }))} />
                <span className="w-3 h-0.5 rounded" style={{ background: 'rgba(99,102,241,0.6)' }} />
                CMDB 拓扑（纵向骨架/依赖）
              </label>
              <label className="flex items-center gap-2 text-xs cursor-pointer">
                <input type="checkbox" checked={layers.callchain} onChange={e => setLayers(l => ({ ...l, callchain: e.target.checked }))} />
                <span className="w-3 h-0.5 rounded" style={{ background: 'rgba(6,182,212,0.75)' }} />
                横向调用链（callchain）
              </label>
            </CardContent>
          </Card>

          {data && (
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">健康度汇总</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 gap-2 text-center">
                  <div className="rounded-md bg-muted/30 py-2">
                    <div className="text-lg font-bold">{data.summary.total}</div>
                    <div className="text-[10px] text-muted-foreground">节点</div>
                  </div>
                  <div className="rounded-md py-2" style={{ background: 'rgba(239,68,68,0.1)' }}>
                    <div className="text-lg font-bold text-red-500">{data.summary.critical}</div>
                    <div className="text-[10px] text-muted-foreground">critical</div>
                  </div>
                  <div className="rounded-md py-2" style={{ background: 'rgba(245,158,11,0.1)' }}>
                    <div className="text-lg font-bold text-amber-500">{data.summary.warning}</div>
                    <div className="text-[10px] text-muted-foreground">warning</div>
                  </div>
                  <div className="rounded-md py-2" style={{ background: 'rgba(34,197,94,0.1)' }}>
                    <div className="text-lg font-bold text-green-500">{data.summary.healthy}</div>
                    <div className="text-[10px] text-muted-foreground">healthy</div>
                  </div>
                </div>
              </CardContent>
            </Card>
          )}

          {data && data.alerts.length > 0 && (
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm flex items-center gap-2">
                  <AlertTriangle className="h-4 w-4 text-amber-500" />活跃告警 ({data.alerts.length})
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-1.5 max-h-[240px] overflow-y-auto">
                {data.alerts.map(a => (
                  <div key={a.id} className="text-xs border rounded p-2"
                    style={{ borderColor: a.severity === 'critical' ? 'rgba(239,68,68,0.4)' : 'rgba(245,158,11,0.4)' }}>
                    <div className="flex items-center gap-1.5">
                      <Badge variant={HEALTH_BADGE[a.severity] || 'secondary'} className="text-[9px]">{a.severity}</Badge>
                      {a.rootCause && <Badge variant="destructive" className="text-[9px]">根因</Badge>}
                    </div>
                    <p className="mt-1 text-muted-foreground truncate">{a.message}</p>
                  </div>
                ))}
              </CardContent>
            </Card>
          )}
        </div>

        {/* 右栏：全景画布 */}
        <div className="lg:col-span-3">
          <Card>
            <CardHeader className="py-3">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm">
                  全景画布 {data && `(${data.nodes.length} 节点, ${data.edges.length} 边)`}
                </CardTitle>
                <div className="flex items-center gap-3 text-xs">
                  {Object.entries(HEALTH_COLOR).map(([h, c]) => (
                    <div key={h} className="flex items-center gap-1">
                      <span className="w-2.5 h-2.5 rounded-full" style={{ background: c }} />
                      <span className="text-muted-foreground">{h}</span>
                    </div>
                  ))}
                </div>
              </div>
            </CardHeader>
            <CardContent>
              <div className="relative w-full h-[600px] border rounded-md overflow-hidden bg-muted/20">
                {error ? (
                  <div className="absolute inset-0 flex items-center justify-center">
                    <p className="text-sm text-red-500">{error}</p>
                  </div>
                ) : !data || data.nodes.length === 0 ? (
                  <div className="absolute inset-0 flex items-center justify-center">
                    <div className="text-center">
                      <Activity className="h-12 w-12 mx-auto mb-3 text-muted-foreground/30" />
                      <p className="text-sm text-muted-foreground">{scope ? '该范围无全景数据' : '请选择根节点'}</p>
                    </div>
                  </div>
                ) : (
                  <PanoramaGraph nodes={data.nodes} edges={data.edges} onNodeClick={setSelectedNode} />
                )}
              </div>
            </CardContent>
          </Card>

          {selectedDetail && (
            <Card className="mt-4">
              <CardHeader className="py-3">
                <CardTitle className="text-sm flex items-center gap-2">
                  节点详情 - {selectedDetail.name}
                  <Badge variant={HEALTH_BADGE[selectedDetail.health] || 'secondary'} className="text-[9px]">{selectedDetail.health}</Badge>
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
                  <div><p className="text-xs text-muted-foreground">ID</p><p className="font-mono text-xs">{selectedDetail.id}</p></div>
                  <div><p className="text-xs text-muted-foreground">类型</p><Badge variant="outline">{selectedDetail.type}</Badge></div>
                  <div><p className="text-xs text-muted-foreground">监控状态</p><Badge variant="secondary">{selectedDetail.status || 'unknown'}</Badge></div>
                  <div>
                    <p className="text-xs text-muted-foreground">告警</p>
                    <Badge variant={selectedDetail.alertSummary?.critical ? 'destructive' : selectedDetail.alertSummary?.warning ? 'warning' : 'secondary'} className="text-[10px]">
                      C {selectedDetail.alertSummary?.critical || 0} / W {selectedDetail.alertSummary?.warning || 0}
                    </Badge>
                  </div>
                </div>
                {selectedDetail.metrics && Object.keys(selectedDetail.metrics).length > 0 && (
                  <div className="mt-3 flex flex-wrap gap-2">
                    {Object.entries(selectedDetail.metrics).map(([k, v]) => (
                      <div key={k} className="text-xs border rounded px-2 py-1">
                        <span className="text-muted-foreground">{k}:</span>
                        <span className="font-mono ml-1">{String(v)}</span>
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          )}
        </div>
      </div>
    </div>
  )
}
