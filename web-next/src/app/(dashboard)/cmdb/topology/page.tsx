'use client'

import { useEffect, useState, useRef, useMemo, useCallback } from 'react'
import { useCMDBStore, type TopologyNode } from '@/stores/cmdb'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AppArchPanel } from '@/components/cmdb/app-arch-panel'
import {
  ZoomIn, ZoomOut, Maximize, RefreshCw, Network, AlertTriangle,
  Server, Database, Cloud, Globe, Boxes, Shield, Layers,
} from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'

// CI 类型 -> 图标 + 颜色映射
const TYPE_META: Record<string, { icon: any; color: string; bg: string; border: string }> = {
  server: { icon: Server, color: 'text-blue-600', bg: 'bg-blue-50 dark:bg-blue-500/10', border: 'border-blue-300 dark:border-blue-500/30' },
  'web-server': { icon: Globe, color: 'text-cyan-600', bg: 'bg-cyan-50 dark:bg-cyan-500/10', border: 'border-cyan-300 dark:border-cyan-500/30' },
  database: { icon: Database, color: 'text-purple-600', bg: 'bg-purple-50 dark:bg-purple-500/10', border: 'border-purple-300 dark:border-purple-500/30' },
  application: { icon: Boxes, color: 'text-teal-600', bg: 'bg-teal-50 dark:bg-teal-500/10', border: 'border-teal-300 dark:border-teal-500/30' },
  loadbalancer: { icon: Layers, color: 'text-amber-600', bg: 'bg-amber-50 dark:bg-amber-500/10', border: 'border-amber-300 dark:border-amber-500/30' },
  service: { icon: Cloud, color: 'text-emerald-600', bg: 'bg-emerald-50 dark:bg-emerald-500/10', border: 'border-emerald-300 dark:border-emerald-500/30' },
  switch: { icon: Network, color: 'text-orange-600', bg: 'bg-orange-50 dark:bg-orange-500/10', border: 'border-orange-300 dark:border-orange-500/30' },
  firewall: { icon: Shield, color: 'text-red-600', bg: 'bg-red-50 dark:bg-red-500/10', border: 'border-red-300 dark:border-red-500/30' },
  storage: { icon: Boxes, color: 'text-indigo-600', bg: 'bg-indigo-50 dark:bg-indigo-500/10', border: 'border-indigo-300 dark:border-indigo-500/30' },
}
const DEFAULT_META = { icon: Server, color: 'text-gray-600', bg: 'bg-gray-50 dark:bg-gray-500/10', border: 'border-gray-300 dark:border-gray-500/30' }

// 关系类型 -> 颜色映射
const RELATION_COLORS: Record<string, string> = {
  depends_on: '#ef4444',
  connects_to: '#3b82f6',
  contains: '#10b981',
  backup_of: '#f59e0b',
  deployed_on: '#8b5cf6',
  calls: '#06b6d4',
  belongs_to: '#ec4899',
  part_of: '#6366f1',
}

const RELATION_LABELS: Record<string, string> = {
  depends_on: '依赖于',
  connects_to: '连接到',
  contains: '包含',
  backup_of: '备份',
  deployed_on: '部署于',
  calls: '调用',
  belongs_to: '隶属于',
  part_of: '组成部分',
}

// 节点位置布局:环形布局
function layoutNodes(nodes: TopologyNode[], rootId: string): Record<string, { x: number; y: number }> {
  const positions: Record<string, { x: number; y: number }> = {}
  if (nodes.length === 0) return positions

  // 根节点居中
  positions[rootId] = { x: 500, y: 350 }

  // 其余节点环形排列
  const others = nodes.filter(n => n.id !== rootId)
  const radius = 220
  others.forEach((node, i) => {
    const angle = (i / others.length) * 2 * Math.PI - Math.PI / 2
    positions[node.id] = {
      x: 500 + radius * Math.cos(angle),
      y: 350 + radius * Math.sin(angle),
    }
  })
  return positions
}

export default function TopologyPage() {
  const { items, topology, impactResult, fetch, fetchTopology, fetchImpact } = useCMDBStore()
  const [selectedCI, setSelectedCI] = useState<string>('')
  const [searchQ, setSearchQ] = useState('')
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [selectedNode, setSelectedNode] = useState<TopologyNode | null>(null)
  const [impactLoading, setImpactLoading] = useState(false)
  // 视图模式：关系图谱（以任意 CI 为根）/ 应用架构（按业务分层）——同一份关系数据的两种呈现
  const [view, setView] = useState<'graph' | 'app'>('graph')

  // 节点位置(可拖拽)
  const [positions, setPositions] = useState<Record<string, { x: number; y: number }>>({})
  const draggingRef = useRef<string | null>(null)
  const dragOffsetRef = useRef({ x: 0, y: 0 })
  const canvasRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    fetch({ limit: 200 })
  }, [])

  // 首次加载后默认选中第一个 CI
  useEffect(() => {
    if (!selectedCI && items.length > 0) {
      setSelectedCI(items[0].id)
    }
  }, [items, selectedCI])

  // 加载拓扑
  const loadTopology = useCallback(async (ciId: string) => {
    if (!ciId) return
    const result = await fetchTopology(ciId)
    if (result && result.nodes.length > 0) {
      setPositions(layoutNodes(result.nodes, ciId))
      setSelectedNode(null)
    }
  }, [fetchTopology])

  useEffect(() => {
    if (selectedCI) loadTopology(selectedCI)
  }, [selectedCI, loadTopology])

  // 影响分析
  const runImpact = async () => {
    if (!selectedCI) return
    setImpactLoading(true)
    await fetchImpact(selectedCI)
    setImpactLoading(false)
  }

  // 节点拖拽
  const onNodeMouseDown = (e: React.MouseEvent, nodeId: string) => {
    e.stopPropagation()
    draggingRef.current = nodeId
    const rect = canvasRef.current?.getBoundingClientRect()
    if (rect) {
      const pos = positions[nodeId]
      // 考虑 zoom 和 pan
      const screenX = (pos.x * zoom) + pan.x + rect.left
      const screenY = (pos.y * zoom) + pan.y + rect.top
      dragOffsetRef.current = { x: e.clientX - screenX, y: e.clientY - screenY }
    }
  }

  useEffect(() => {
    const onMouseMove = (e: MouseEvent) => {
      if (!draggingRef.current || !canvasRef.current) return
      const rect = canvasRef.current.getBoundingClientRect()
      const x = (e.clientX - rect.left - pan.x - dragOffsetRef.current.x) / zoom
      const y = (e.clientY - rect.top - pan.y - dragOffsetRef.current.y) / zoom
      setPositions(prev => ({
        ...prev,
        [draggingRef.current!]: { x, y },
      }))
    }
    const onMouseUp = () => { draggingRef.current = null }
    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseup', onMouseUp)
    return () => {
      window.removeEventListener('mousemove', onMouseMove)
      window.removeEventListener('mouseup', onMouseUp)
    }
  }, [zoom, pan])

  // 画布拖拽(平移)
  const onCanvasMouseDown = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget || (e.target as HTMLElement).dataset.canvas === 'true') {
      const startX = e.clientX - pan.x
      const startY = e.clientY - pan.y
      const onMove = (e: MouseEvent) => setPan({ x: e.clientX - startX, y: e.clientY - startY })
      const onUp = () => {
        window.removeEventListener('mousemove', onMove)
        window.removeEventListener('mouseup', onUp)
      }
      window.addEventListener('mousemove', onMove)
      window.addEventListener('mouseup', onUp)
    }
  }

  const resetView = () => {
    setZoom(1)
    setPan({ x: 0, y: 0 })
    if (topology && selectedCI) {
      setPositions(layoutNodes(topology.nodes, selectedCI))
    }
  }

  // 筛选 CI 列表
  const filteredCIs = useMemo(() =>
    items.filter(ci =>
      ci.name.toLowerCase().includes(searchQ.toLowerCase()) ||
      ci.type_id.toLowerCase().includes(searchQ.toLowerCase())
    ), [items, searchQ])

  // 节点详情:从 items 中查找完整 CI 信息
  const selectedCIDetail = useMemo(() =>
    selectedNode ? items.find(ci => ci.id === selectedNode.id) : null
  , [selectedNode, items])

  // 统计
  const typeStats = useMemo(() => {
    const stats: Record<string, number> = {}
    topology?.nodes.forEach(n => {
      stats[n.type] = (stats[n.type] || 0) + 1
    })
    return stats
  }, [topology])

  const relStats = useMemo(() => {
    const stats: Record<string, number> = {}
    topology?.edges.forEach(e => {
      stats[e.relation_type] = (stats[e.relation_type] || 0) + 1
    })
    return stats
  }, [topology])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">拓扑可视化</h1>
          <p className="text-sm text-muted-foreground">关系图谱以任意 CI 为根展开 3 层关系；应用架构按业务分层聚合上下游资产</p>
        </div>
        {view === 'graph' && (
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => selectedCI && loadTopology(selectedCI)}>
              <RefreshCw className="h-3.5 w-3.5 mr-1" />刷新
            </Button>
            <Button variant="outline" size="sm" onClick={runImpact} disabled={!selectedCI || impactLoading}>
              <AlertTriangle className="h-3.5 w-3.5 mr-1" />{impactLoading ? '分析中...' : '影响分析'}
            </Button>
          </div>
        )}
      </div>

      <Tabs value={view} onValueChange={v => setView(v as 'graph' | 'app')}>
        <TabsList>
          <TabsTrigger value="graph"><Network className="h-3.5 w-3.5 mr-1" />关系图谱</TabsTrigger>
          <TabsTrigger value="app"><Layers className="h-3.5 w-3.5 mr-1" />应用架构</TabsTrigger>
        </TabsList>

        <TabsContent value="graph" className="space-y-4 mt-4">
      <div className="grid grid-cols-1 lg:grid-cols-4 gap-4">
        {/* 左侧:CI 选择器 */}
        <div className="lg:col-span-1 space-y-4">
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">选择根节点</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="relative">
                <Input
                  className="h-8 text-xs pl-8"
                  placeholder="搜索 CI..."
                  value={searchQ}
                  onChange={e => setSearchQ(e.target.value)}
                />
                <span className="absolute left-2.5 top-1/2 -translate-y-1/2 text-xs text-muted-foreground">🔍</span>
              </div>
              <div className="max-h-[400px] overflow-y-auto space-y-1 border rounded-md p-2">
                {filteredCIs.length === 0 ? (
                  <p className="text-xs text-muted-foreground text-center py-4">暂无 CI</p>
                ) : filteredCIs.map(ci => {
                  const meta = TYPE_META[ci.type_id] || DEFAULT_META
                  const Icon = meta.icon
                  return (
                    <button
                      key={ci.id}
                      className={cn(
                        "w-full text-left px-2 py-1.5 rounded text-xs flex items-center gap-2 hover:bg-muted/50",
                        selectedCI === ci.id && "bg-emerald-500/10 border border-emerald-500/30"
                      )}
                      onClick={() => setSelectedCI(ci.id)}
                    >
                      <Icon className={cn("h-3.5 w-3.5 shrink-0", meta.color)} />
                      <span className="truncate flex-1">{ci.name}</span>
                      <Badge variant="outline" className="text-[9px]">{ci.type_id}</Badge>
                    </button>
                  )
                })}
              </div>
            </CardContent>
          </Card>

          {/* 统计 */}
          {topology && topology.nodes.length > 0 && (
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">图谱统计</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">节点类型 ({topology.nodes.length})</p>
                  <div className="flex flex-wrap gap-1">
                    {Object.entries(typeStats).map(([type, count]) => (
                      <Badge key={type} variant="secondary" className="text-[10px]">{type}: {count}</Badge>
                    ))}
                  </div>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">关系类型 ({topology.edges.length})</p>
                  <div className="flex flex-wrap gap-1">
                    {Object.entries(relStats).map(([type, count]) => (
                      <Badge key={type} variant="outline" className="text-[10px]">
                        <span style={{ color: RELATION_COLORS[type] || '#666' }}>●</span>
                        {RELATION_LABELS[type] || type}: {count}
                      </Badge>
                    ))}
                  </div>
                </div>
              </CardContent>
            </Card>
          )}

          {/* 影响分析结果 */}
          {impactResult && (
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm flex items-center gap-2">
                  <AlertTriangle className="h-4 w-4 text-amber-500" />影响分析
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                <div className="text-xs">
                  <span className="text-muted-foreground">根 CI:</span>
                  <span className="font-medium ml-1">{items.find(c => c.id === impactResult.source_ci)?.name || impactResult.source_ci.slice(0, 8)}</span>
                </div>
                <div className="text-xs">
                  <span className="text-muted-foreground">影响深度:</span>
                  <span className="font-medium ml-1">{impactResult.depth}</span>
                </div>
                <div className="text-xs">
                  <span className="text-muted-foreground">受影响 CI:</span>
                  <Badge variant={impactResult.affected_ci.length > 0 ? 'warning' : 'secondary'} className="ml-1">
                    {impactResult.affected_ci.length}
                  </Badge>
                </div>
                {impactResult.has_cycle && (
                  <Badge variant="destructive" className="text-[10px]">检测到环路</Badge>
                )}
                {impactResult.affected_ci.length > 0 && (
                  <div className="space-y-1 max-h-40 overflow-y-auto">
                    {impactResult.affected_ci.map(id => {
                      const ci = items.find(c => c.id === id)
                      return (
                        <div key={id} className="text-xs flex items-center gap-1.5 p-1 rounded bg-amber-50 dark:bg-amber-500/10">
                          <span className="w-1 h-1 rounded-full bg-amber-500" />
                          <span className="truncate">{ci?.name || id.slice(0, 8)}</span>
                          {ci && <Badge variant="outline" className="text-[9px]">{ci.type_id}</Badge>}
                        </div>
                      )
                    })}
                  </div>
                )}
              </CardContent>
            </Card>
          )}
        </div>

        {/* 右侧:图谱画布 */}
        <div className="lg:col-span-3">
          <Card>
            <CardHeader className="py-3">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm">
                  拓扑画布 {topology && `(${topology.nodes.length} 节点, ${topology.edges.length} 关系)`}
                </CardTitle>
                <div className="flex items-center gap-1">
                  <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setZoom(z => Math.min(z + 0.2, 3))} title="放大">
                    <ZoomIn className="h-3.5 w-3.5" />
                  </Button>
                  <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setZoom(z => Math.max(z - 0.2, 0.3))} title="缩小">
                    <ZoomOut className="h-3.5 w-3.5" />
                  </Button>
                  <Button variant="ghost" size="icon" className="h-7 w-7" onClick={resetView} title="重置视图">
                    <Maximize className="h-3.5 w-3.5" />
                  </Button>
                  <span className="text-xs text-muted-foreground ml-2 w-10">{Math.round(zoom * 100)}%</span>
                </div>
              </div>
            </CardHeader>
            <CardContent>
              <div
                ref={canvasRef}
                data-canvas="true"
                className="relative w-full h-[600px] border rounded-md overflow-hidden bg-muted/20 cursor-grab active:cursor-grabbing"
                onMouseDown={onCanvasMouseDown}
                style={{
                  backgroundImage: 'radial-gradient(circle, rgba(128,128,128,0.15) 1px, transparent 1px)',
                  backgroundSize: `${20 * zoom}px ${20 * zoom}px`,
                  backgroundPosition: `${pan.x}px ${pan.y}px`,
                }}
              >
                {!topology || topology.nodes.length === 0 ? (
                  <div className="absolute inset-0 flex items-center justify-center">
                    <div className="text-center">
                      <Network className="h-12 w-12 mx-auto mb-3 text-muted-foreground/30" />
                      <p className="text-sm text-muted-foreground">
                        {selectedCI ? '该 CI 无拓扑关系' : '请选择根节点'}
                      </p>
                    </div>
                  </div>
                ) : (
                  <>
                    {/* SVG 连线层 */}
                    <svg
                      className="absolute inset-0 w-full h-full pointer-events-none"
                      style={{ transform: `scale(${zoom}) translate(${pan.x / zoom}px, ${pan.y / zoom}px)`, transformOrigin: '0 0' }}
                    >
                      <defs>
                        <marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto" markerUnits="strokeWidth">
                          <path d="M0,0 L0,6 L9,3 z" fill="currentColor" />
                        </marker>
                      </defs>
                      {topology.edges.map((edge, i) => {
                        const src = positions[edge.source_id]
                        const tgt = positions[edge.target_id]
                        if (!src || !tgt) return null
                        const color = RELATION_COLORS[edge.relation_type] || '#666'
                        // 贝塞尔曲线控制点
                        const mx = (src.x + tgt.x) / 2
                        const my = (src.y + tgt.y) / 2
                        const dx = tgt.x - src.x
                        const dy = tgt.y - src.y
                        const offset = 30
                        const cx = mx - dy / Math.sqrt(dx * dx + dy * dy) * offset
                        const cy = my + dx / Math.sqrt(dx * dx + dy * dy) * offset
                        return (
                          <g key={i}>
                            <path
                              d={`M ${src.x} ${src.y} Q ${cx} ${cy} ${tgt.x} ${tgt.y}`}
                              fill="none"
                              stroke={color}
                              strokeWidth={1.5}
                              markerEnd="url(#arrow)"
                              opacity={0.7}
                            />
                            {/* 关系标签 */}
                            <text
                              x={cx}
                              y={cy}
                              fill={color}
                              fontSize="10"
                              textAnchor="middle"
                              dy="0.3em"
                              className="select-none"
                            >
                              {RELATION_LABELS[edge.relation_type] || edge.relation_type}
                            </text>
                          </g>
                        )
                      })}
                    </svg>

                    {/* 节点层 */}
                    {topology.nodes.map(node => {
                      const pos = positions[node.id]
                      if (!pos) return null
                      const meta = TYPE_META[node.type] || DEFAULT_META
                      const Icon = meta.icon
                      const isRoot = node.id === selectedCI
                      const isSelected = selectedNode?.id === node.id
                      return (
                        <div
                          key={node.id}
                          className={cn(
                            "absolute flex flex-col items-center gap-1 cursor-move select-none",
                            "transition-shadow",
                            isRoot && "ring-2 ring-emerald-500 ring-offset-2 rounded-lg",
                          )}
                          style={{
                            left: `${pos.x * zoom + pan.x}px`,
                            top: `${pos.y * zoom + pan.y}px`,
                            transform: 'translate(-50%, -50%)',
                          }}
                          onMouseDown={(e) => onNodeMouseDown(e, node.id)}
                          onClick={(e) => { e.stopPropagation(); setSelectedNode(node) }}
                        >
                          <div className={cn(
                            "w-12 h-12 rounded-lg flex items-center justify-center border-2",
                            meta.bg, meta.border,
                            isSelected && "ring-2 ring-emerald-400 ring-offset-1",
                          )}>
                            <Icon className={cn("h-5 w-5", meta.color)} />
                          </div>
                          <div className="text-xs font-medium text-center max-w-[80px] truncate">
                            {node.name}
                          </div>
                          <Badge variant="outline" className="text-[9px]">{node.type}</Badge>
                        </div>
                      )
                    })}
                  </>
                )}
              </div>

              {/* 图例 */}
              <div className="mt-3 flex flex-wrap gap-3 text-xs">
                {Object.entries(RELATION_COLORS).map(([type, color]) => (
                  <div key={type} className="flex items-center gap-1">
                    <span style={{ color }} className="text-base leading-none">●</span>
                    <span className="text-muted-foreground">{RELATION_LABELS[type] || type}</span>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </div>
      </div>

      {/* 节点详情面板 */}
      {selectedNode && selectedCIDetail && (
        <Card>
          <CardHeader className="py-3">
            <div className="flex items-center justify-between">
              <CardTitle className="text-sm">节点详情 - {selectedNode.name}</CardTitle>
              <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => setSelectedNode(null)}>×</Button>
            </div>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
              <div>
                <p className="text-xs text-muted-foreground">ID</p>
                <p className="font-mono text-xs">{selectedCIDetail.id}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">类型</p>
                <Badge variant="outline">{selectedCIDetail.type_id}</Badge>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">状态</p>
                <Badge variant={selectedCIDetail.status === 'active' ? 'success' : 'secondary'}>
                  {selectedCIDetail.status}
                </Badge>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">描述</p>
                <p className="text-xs">{selectedCIDetail.description || '-'}</p>
              </div>
            </div>
            {selectedCIDetail.attributes && Object.keys(selectedCIDetail.attributes).length > 0 && (
              <div className="mt-4">
                <p className="text-xs text-muted-foreground mb-2">属性</p>
                <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
                  {Object.entries(selectedCIDetail.attributes).map(([k, v]) => (
                    <div key={k} className="text-xs border rounded p-2">
                      <span className="text-muted-foreground">{k}:</span>
                      <span className="font-mono ml-1">{String(v)}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
            {selectedCIDetail.labels && Object.keys(selectedCIDetail.labels).length > 0 && (
              <div className="mt-3">
                <p className="text-xs text-muted-foreground mb-1.5">标签</p>
                <div className="flex flex-wrap gap-1">
                  {Object.entries(selectedCIDetail.labels).map(([k, v]) => (
                    <Badge key={k} variant="secondary" className="text-[10px]">{k}={v}</Badge>
                  ))}
                </div>
              </div>
            )}
            <p className="text-[10px] text-muted-foreground/60 mt-3">
              创建于 {formatTime(selectedCIDetail.created_at)} · 更新于 {formatTime(selectedCIDetail.updated_at)}
            </p>
          </CardContent>
        </Card>
      )}
        </TabsContent>

        <TabsContent value="app" className="mt-4">
          <AppArchPanel />
        </TabsContent>
      </Tabs>
    </div>
  )
}
