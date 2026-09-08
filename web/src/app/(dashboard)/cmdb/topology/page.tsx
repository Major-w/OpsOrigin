
import { useEffect, useState, useRef, useMemo, useCallback } from 'react'
import { useCMDBStore, type TopologyNode, type TopologyEdge } from '@/stores/cmdb'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Tabs } from 'antd'
import { AppArchPanel } from '@/components/cmdb/app-arch-panel'
import {
  Maximize, RefreshCw, Network, AlertTriangle, Layers,
  Server, Database, Cloud, Globe, Boxes, Shield,
} from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { RELATION_COLORS, GRAPH_DEFAULT_COLOR, HEALTH_COLOR } from '@/lib/graph-colors'
import { ErrorState } from '@/components/ui/error-state'
import {
  ReactFlow, ReactFlowProvider, Background, BackgroundVariant, Controls, MiniMap, Handle, Position, MarkerType,
  useNodesState, useStore,
  type Node, type Edge, type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'

// ============ CI 类型 -> 图标/颜色映射（图标块 + 卡片主色，class 体系随主题） ============
const TYPE_META: Record<string, { icon: any; color: string; bg: string; border: string }> = {
  server: { icon: Server, color: 'text-blue-600', bg: 'bg-blue-50 dark:bg-blue-500/10', border: 'border-blue-300 dark:border-blue-500/30' },
  'web-server': { icon: Globe, color: 'text-cyan-600', bg: 'bg-cyan-50 dark:bg-cyan-500/10', border: 'border-cyan-300 dark:border-cyan-500/30' },
  database: { icon: Database, color: 'text-purple-600', bg: 'bg-purple-50 dark:bg-purple-500/10', border: 'border-purple-300 dark:border-purple-500/30' },
  application: { icon: Boxes, color: 'text-teal-600', bg: 'bg-teal-50 dark:bg-teal-500/10', border: 'border-teal-300 dark:border-teal-500/30' },
  loadbalancer: { icon: Layers, color: 'text-amber-600', bg: 'bg-amber-50 dark:bg-amber-500/10', border: 'border-amber-300 dark:border-amber-500/30' },
  service: { icon: Cloud, color: 'text-blue-600', bg: 'bg-blue-50 dark:bg-blue-500/10', border: 'border-blue-300 dark:border-blue-500/30' },
  switch: { icon: Network, color: 'text-orange-600', bg: 'bg-orange-50 dark:bg-orange-500/10', border: 'border-orange-300 dark:border-orange-500/30' },
  firewall: { icon: Shield, color: 'text-red-600', bg: 'bg-red-50 dark:bg-red-500/10', border: 'border-red-300 dark:border-red-500/30' },
  storage: { icon: Boxes, color: 'text-indigo-600', bg: 'bg-indigo-50 dark:bg-indigo-500/10', border: 'border-indigo-300 dark:border-indigo-500/30' },
}
const DEFAULT_META = { icon: Server, color: 'text-gray-600', bg: 'bg-gray-50 dark:bg-gray-500/10', border: 'border-gray-300 dark:border-gray-500/30' }

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

// hex(#rrggbb) -> rgba，用于让连线/箭头/标签随强调程度统一淡化（SVG marker 不继承父 opacity）
function alpha(hex: string, a: number): string {
  const m = (hex || '').replace('#', '')
  const full = m.length === 3 ? m.split('').map(c => c + c).join('') : m
  const r = parseInt(full.slice(0, 2), 16)
  const g = parseInt(full.slice(2, 4), 16)
  const b = parseInt(full.slice(4, 6), 16)
  return `rgba(${r},${g},${b},${a})`
}

// ============ 分层布局：以根为锚做 BFS 深度分列，同一深度一行 ============
// 相比力导向"蜘蛛网"，纵向分层的读法更像业务架构图：越靠近根越靠上，下游依赖逐层展开。
const LAYER = { cardW: 184, cardH: 62, rowGap: 132, colGap: 30, padX: 88, padTop: 120, padBottom: 104 }
type LayerRow = { depth: number; y: number; label: string; orphan: boolean }
function layoutLayered(
  nodes: TopologyNode[],
  rootId: string,
  edges: TopologyEdge[],
): { positions: Record<string, { x: number; y: number }>; size: { w: number; h: number }; rows: LayerRow[] } {
  const positions: Record<string, { x: number; y: number }> = {}
  if (nodes.length === 0) return { positions, size: { w: 960, h: 600 }, rows: [] }
  // —— 单节点：纯居中 ——
  if (nodes.length === 1) {
    positions[nodes[0].id] = { x: 400, y: LAYER.padTop }
    return {
      positions,
      size: { w: 800, h: LAYER.padTop + LAYER.padBottom + 80 },
      rows: [{ depth: 0, y: LAYER.padTop, label: '根', orphan: false }],
    }
  }

  const idSet = new Set(nodes.map(n => n.id))
  const adj = new Map<string, string[]>()
  nodes.forEach(n => adj.set(n.id, []))
  for (const e of edges) {
    if (!idSet.has(e.source_id) || !idSet.has(e.target_id) || e.source_id === e.target_id) continue
    adj.get(e.source_id)?.push(e.target_id)
    adj.get(e.target_id)?.push(e.source_id)
  }

  // BFS 求每个节点到根的距离（无向遍历——上游依赖与下游调用都可见）
  const depth = new Map<string, number>()
  if (rootId && idSet.has(rootId)) {
    depth.set(rootId, 0)
    const q = [rootId]
    while (q.length) {
      const cur = q.shift()!
      const nextD = (depth.get(cur) ?? 0) + 1
      for (const nx of adj.get(cur) ?? []) {
        if (depth.has(nx)) continue
        depth.set(nx, nextD)
        q.push(nx)
      }
    }
  }
  let maxReached = 0
  depth.forEach(d => { if (d > maxReached) maxReached = d })
  // 无关系的孤立节点落到最底层(或第二行)不挡主体
  const MAX_DEPTH = 6
  const groups = new Map<number, TopologyNode[]>()
  nodes.forEach(n => {
    let d = depth.has(n.id) ? depth.get(n.id)! : Math.min(maxReached + 1, MAX_DEPTH)
    if (d > MAX_DEPTH) d = MAX_DEPTH
    if (!groups.has(d)) groups.set(d, [])
    groups.get(d)!.push(n)
  })
  // 层内按"关联度大者居中偏前 + 名称稳定排序"，降低连线交叉感
  const deg = new Map<string, number>()
  nodes.forEach(n => deg.set(n.id, 0))
  edges.forEach(e => {
    if (deg.has(e.source_id)) deg.set(e.source_id, deg.get(e.source_id)! + 1)
    if (deg.has(e.target_id)) deg.set(e.target_id, deg.get(e.target_id)! + 1)
  })
  groups.forEach(list => list.sort((a, b) => (deg.get(b.id) ?? 0) - (deg.get(a.id) ?? 0) || a.name.localeCompare(b.name, 'zh')))

  const rowsRaw = [...groups.entries()].sort((a, b) => a[0] - b[0])
  const totalNodes = nodes.length
  const maxCols = Math.max(...rowsRaw.map(([, list]) => list.length))

  // —— 小图特化（≤3 节点）：强制 root 在左、其余同级并排，水平间距一个卡片宽 —— 
  // 直白的"主-从"关系, 比"分层上下"更清晰, 根除重叠问题
  if (totalNodes <= 3) {
    const ordered: TopologyNode[] = []
    if (rootId && idSet.has(rootId)) ordered.push(nodes.find(n => n.id === rootId)!)
    const rest = nodes.filter(n => n.id !== rootId)
    rest.forEach(n => ordered.push(n))
    const gap = 76
    const totalW = ordered.length * LAYER.cardW + (ordered.length - 1) * gap
    const startX = 480 - totalW / 2
    const y = LAYER.padTop
    ordered.forEach((n, i) => {
      positions[n.id] = { x: Math.round(startX + i * (LAYER.cardW + gap)), y }
    })
    return {
      positions,
      size: { w: 960, h: LAYER.padTop + LAYER.cardH + LAYER.padBottom },
      rows: [{ depth: 0, y, label: '根', orphan: false }],
    }
  }

  // —— 多行分层：同深度一行，行内均分 ——
  const W = LAYER.padX * 2 + maxCols * (LAYER.cardW + LAYER.colGap) - LAYER.colGap
  const H = LAYER.padTop + rowsRaw.length * LAYER.rowGap + LAYER.padBottom
  const rows: LayerRow[] = []
  rowsRaw.forEach(([d, list], rowIdx) => {
    const rowW = list.length * (LAYER.cardW + LAYER.colGap) - LAYER.colGap
    const xStart = (W - rowW) / 2
    const y = LAYER.padTop + rowIdx * LAYER.rowGap
    list.forEach((n, i) => {
      positions[n.id] = { x: Math.round(xStart + i * (LAYER.cardW + LAYER.colGap)), y: Math.round(y) }
    })
    const orphan = d > maxReached
    const label = d === 0 ? '根' : orphan ? '孤立节点' : `第 ${d} 层`
    rows.push({ depth: d, y, label, orphan })
  })
  return { positions, size: { w: W, h: H }, rows }
}

// ============ React Flow 自定义节点：横版信息卡片 ============
type CMDBNodeData = {
  name: string
  type: string
  status?: string
  isRoot: boolean
  isSelected: boolean
  isSource: boolean
  isAffected: boolean
  isDimmed: boolean
}
type CMDBRFNode = Node<CMDBNodeData>

function CMDBNode({ data }: NodeProps<CMDBRFNode>) {
  const meta = TYPE_META[data.type] || DEFAULT_META
  const Icon = meta.icon
  const dim = data.isDimmed
  return (
    <div
      className={cn(
        'relative select-none transition-opacity duration-150 cursor-move',
        dim && 'opacity-25',
        !dim && data.isSelected && 'z-[5]',
      )}
      title={data.isRoot ? `${data.name}（当前根节点，双击可换根）` : `${data.name}（双击以该节点为根重绘）`}
    >
      {/* 连线端点：上下两向，视觉隐藏仅作锚点 */}
      <Handle type="target" position={Position.Top} style={{ opacity: 0, width: 6, height: 6 }} />
      <Handle type="source" position={Position.Bottom} style={{ opacity: 0, width: 6, height: 6 }} />

      <div
        className={cn(
          'relative flex items-center gap-2.5 rounded-xl border bg-background px-2.5 py-2 pr-3',
          'w-[184px] h-[62px] shadow-sm transition-[box-shadow,border-color] duration-150',
          meta.border,
          // 状态层次：选中 > 影响源 > 受影响 > 根 > 默认
          data.isSelected && 'ring-2 ring-blue-500 ring-offset-2 border-blue-400',
          !data.isSelected && data.isSource && 'ring-2 ring-red-500 ring-offset-2 border-red-400',
          !data.isSelected && !data.isSource && data.isAffected && 'ring-2 ring-amber-400 ring-offset-2 border-amber-300',
          !data.isSelected && data.isRoot && 'ring-2 ring-blue-500/70 ring-offset-2',
          !dim && !data.isSelected && !data.isSource && !data.isAffected && 'hover:border-blue-400/70 hover:shadow-md',
        )}
      >
        <div className={cn('h-10 w-10 shrink-0 rounded-lg border flex items-center justify-center', meta.bg, meta.border)}>
          <Icon className={cn('h-5 w-5', meta.color)} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-[13.5px] font-semibold leading-tight truncate">{data.name}</div>
          <div className="text-[11px] text-muted-foreground truncate mt-0.5">{data.type}</div>
        </div>
        {data.status && (
          <span
            title={`状态: ${data.status}`}
            className={cn(
              'absolute -right-1 -top-1 h-3 w-3 rounded-full border-2 border-background',
              data.status === 'active' ? 'bg-emerald-500' : 'bg-amber-500',
            )}
          />
        )}
        {data.isRoot && (
          <span className="absolute -top-[9px] left-1/2 -translate-x-1/2 rounded-full bg-blue-600 px-1.5 text-[10px] leading-[15px] text-white shadow-sm">
            根
          </span>
        )}
      </div>
    </div>
  )
}

const nodeTypes = { cmdb: CMDBNode }

// ============ 分层泳道覆盖层：行间虚线 + 左侧层级标尺，随 React Flow 视口同步 ============
// 多行分层图才有意义；单行小图(≤3 节点)不画，避免跟节点角标重叠干扰
function LayerBands({ rows, totalW }: { rows: LayerRow[]; totalW: number }) {
  const transform = useStore(s => s.transform)
  const [tx, ty, zoom] = transform
  if (rows.length < 2) return null
  const showLabel = zoom >= 0.45
  // 行卡片底部到下一行卡片顶的中线(行间空隙中心)
  const gapY = (r: LayerRow) => r.y + LAYER.cardH + (LAYER.rowGap - LAYER.cardH) / 2
  const cardMidY = (r: LayerRow) => r.y + LAYER.cardH / 2
  return (
    <svg
      width="100%"
      height="100%"
      className="pointer-events-none absolute inset-0 z-[1] overflow-hidden"
      aria-hidden
    >
      {/* 行间分隔虚线(跨全宽, 落在卡片间隙不压节点) */}
      {rows.slice(0, -1).map((r, i) => {
        const sy = gapY(r) * zoom + ty
        return (
          <line
            key={`lane-${i}`}
            x1={tx}
            y1={sy}
            x2={totalW * zoom + tx}
            y2={sy}
            stroke="var(--border)"
            strokeWidth={1}
            strokeDasharray="2 8"
            opacity={0.75}
          />
        )
      })}
      {/* 左侧层级标尺 */}
      {showLabel && rows.map((r, i) => {
        const cx = 14 * zoom + tx
        const cy = cardMidY(r) * zoom + ty
        const isRoot = r.depth === 0
        const tw = r.label.length * (isRoot ? 11.5 : 10.5) * zoom + 18 * zoom
        const th = 20 * zoom
        return (
          <g key={`chip-${i}`}>
            <rect
              x={cx - tw / 2}
              y={cy - th / 2}
              width={tw}
              height={th}
              rx={th / 2}
              fill={isRoot ? 'var(--primary)' : 'var(--background)'}
              stroke={isRoot ? 'none' : 'var(--border)'}
              strokeWidth={1}
            />
            <text
              x={cx}
              y={cy + 3.6 * zoom}
              textAnchor="middle"
              fontSize={11 * zoom}
              fontWeight={isRoot ? 600 : 400}
              fill={isRoot ? 'white' : 'var(--muted-foreground)'}
            >
              {r.label}
            </text>
          </g>
        )
      })}
    </svg>
  )
}

export default function TopologyPage() {
  const canRead = useCan('cmdb:read')
  const { items, topology, impactResult, fetch, fetchTopology, fetchImpact } = useCMDBStore()
  const [selectedCI, setSelectedCI] = useState<string>('')
  const [searchQ, setSearchQ] = useState('')
  const [selectedNode, setSelectedNode] = useState<TopologyNode | null>(null)
  const [impactLoading, setImpactLoading] = useState(false)
  // 视图模式：关系图谱（以任意 CI 为根展开）/ 应用架构（按业务分层聚合）
  const [view, setView] = useState<'graph' | 'app'>('graph')
  // 影响范围画布高亮开关（清除时保留左侧分析结果卡片）
  const [highlightAffected, setHighlightAffected] = useState(false)
  // 关系类型显隐过滤(空=全显)
  const [hiddenRelTypes, setHiddenRelTypes] = useState<Set<string>>(new Set())
  const [loadError, setLoadError] = useState<string | null>(null)

  // ---- React Flow 受控节点/画布状态 ----
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<CMDBRFNode>([])
  const rfRef = useRef<any>(null)
  // 还原视图 tick：清除拖拽覆盖后触发一次重建(节点弹回分层位置)与视口适配
  const [resetTick, setResetTick] = useState(0)
  const layoutRef = useRef<{ positions: Record<string, { x: number; y: number }>; size: { w: number; h: number }; rows: LayerRow[] }>({ positions: {}, size: { w: 960, h: 600 }, rows: [] })
  // 用户拖拽微调过的位置（布局重算/换根时保留；点"还原"则清除）
  const dragOverridesRef = useRef<Record<string, { x: number; y: number }>>({})
  // hover 单边聚焦
  const [hoverEdgeId, setHoverEdgeId] = useState<string | null>(null)

  const loadInitial = useCallback(async () => {
    setLoadError(null)
    try {
      await fetch({ limit: 200 })
    } catch (e: any) {
      setLoadError(e.message || '加载实例列表失败')
    }
  }, [fetch])

  useEffect(() => {
    loadInitial()
  }, [loadInitial])

  // 首次加载后默认选中第一个 CI
  useEffect(() => {
    if (!selectedCI && items.length > 0) setSelectedCI(items[0].id)
  }, [items, selectedCI])

  // 布局/行元数据(泳道渲染用)——layoutRef 存坐标供节点重建, state 保证泳道层随布局刷新
  const [layoutMeta, setLayoutMeta] = useState<{ size: { w: number; h: number }; rows: LayerRow[] }>({ size: { w: 960, h: 600 }, rows: [] })
  // 加载拓扑：BFS 分层布局出全部节点坐标
  const loadTopology = useCallback(async (ciId: string) => {
    if (!ciId) return
    const result = await fetchTopology(ciId)
    if (result && (result.nodes ?? []).length > 0) {
      dragOverridesRef.current = {}
      layoutRef.current = layoutLayered(result.nodes ?? [], ciId, result.edges ?? [])
      setLayoutMeta({ size: { ...layoutRef.current.size }, rows: layoutRef.current.rows })
      setSelectedNode(null)
      setHighlightAffected(false)
      setHoverEdgeId(null)
    }
  }, [fetchTopology])

  useEffect(() => {
    if (selectedCI) loadTopology(selectedCI)
  }, [selectedCI, loadTopology])

  // 布局/数据变化后重建 RF 节点（保留拖拽覆盖）
  const impactIdSet = useMemo(() => {
    if (!highlightAffected || !impactResult) return null
    return new Set([impactResult.source_ci, ...(impactResult.affected_ci || [])])
  }, [highlightAffected, impactResult])
  const impactOn = !!impactIdSet
  // 节点聚焦(单击选中)：影响高亮开启时优先展示影响范围，聚焦随之让位
  const focusId = !impactOn && selectedNode ? selectedNode.id : null

  const neighborMap = useMemo(() => {
    const m = new Map<string, Set<string>>()
    for (const e of topology?.edges ?? []) {
      if (!m.has(e.source_id)) m.set(e.source_id, new Set())
      if (!m.has(e.target_id)) m.set(e.target_id, new Set())
      m.get(e.source_id)!.add(e.target_id)
      m.get(e.target_id)!.add(e.source_id)
    }
    return m
  }, [topology])
  const focusNeighbors = focusId ? neighborMap.get(focusId) : undefined

  const ciById = useMemo(() => new Map(items.map(c => [c.id, c])), [items])

  useEffect(() => {
    if (!topology) return
    const pos = layoutRef.current.positions
    const nodes = (topology.nodes ?? []).map<TopologyNode>(n => n)
    const next: CMDBRFNode[] = nodes.map(n => {
      const p = dragOverridesRef.current[n.id] || pos[n.id] || { x: 80, y: 80 }
      const ci = ciById.get(n.id)
      const isRoot = n.id === selectedCI
      const isSelected = n.id === selectedNode?.id
      const isSource = impactIdSet ? n.id === impactResult?.source_ci : false
      const isAffected = impactIdSet ? impactIdSet.has(n.id) && !isSource : false
      const isDimmed = impactIdSet
        ? !impactIdSet.has(n.id)
        : focusId !== null
          ? n.id !== focusId && !focusNeighbors?.has(n.id)
          : false
      return {
        id: n.id,
        type: 'cmdb' as const,
        position: p,
        data: {
          name: n.name,
          type: n.type,
          status: ci?.status,
          isRoot,
          isSelected,
          isSource,
          isAffected,
          isDimmed,
        },
      }
    })
    setRfNodes(next)
  }, [topology, selectedCI, selectedNode, impactIdSet, impactOn, focusId, focusNeighbors, ciById, resetTick, setRfNodes])

  // 布局或数据变化后自动适配视口：禁用动画(duration:0)避免 LayerBands 同步滞后
  useEffect(() => {
    if (!topology || topology.nodes.length === 0) return
    const t = setTimeout(() => { rfRef.current?.fitView({ padding: 0.1, duration: 0, maxZoom: 1.2 }) }, 120)
    return () => clearTimeout(t)
  }, [topology, layoutMeta.size, resetTick])

  // 影响分析
  const runImpact = async () => {
    if (!selectedCI) return
    setImpactLoading(true)
    await fetchImpact(selectedCI)
    setHighlightAffected(true)
    setImpactLoading(false)
  }
  const toggleHighlight = () => setHighlightAffected(v => !v)

  // 还原视图：清除拖拽微调 → 重建回分层位 → 自动适配视口
  const resetView = () => {
    dragOverridesRef.current = {}
    setResetTick(t => t + 1)
  }

  const onNodeClick = useCallback((_e: any, node: Node) => {
    const n = (topology?.nodes ?? []).find(x => x.id === node.id)
    if (n) setSelectedNode(n)
  }, [topology])

  const onNodeDoubleClick = useCallback((_e: any, node: Node) => {
    setSelectedCI(node.id)
  }, [])

  const onPaneClick = useCallback(() => {
    setSelectedNode(null)
    setHoverEdgeId(null)
  }, [])

  const onNodeDragStop = useCallback((_e: any, node: Node) => {
    dragOverridesRef.current[node.id] = { x: node.position.x, y: node.position.y }
  }, [])

  // ---- 边：按 聚焦 > 影响 > 默认 三档强调，颜色统一带透明度淡化（箭头同色同步） ----
  const visibleEdges = useMemo(
    () => (topology?.edges ?? []).filter(e => e.source_id !== e.target_id && !hiddenRelTypes.has(e.relation_type)),
    [topology, hiddenRelTypes],
  )

  const rfEdges = useMemo<Edge[]>(() => {
    const list = visibleEdges
    return list.map((e, i) => {
      const color = RELATION_COLORS[e.relation_type] || GRAPH_DEFAULT_COLOR
      const relLabel = RELATION_LABELS[e.relation_type] || e.relation_type
      const pairKey = `${e.source_id}->${e.target_id}`
      const isHover = hoverEdgeId === pairKey
      // 节点聚焦：与选中节点直连的边最亮；二跳(邻居之间)弱提亮；其余几乎隐去
      const isFocusEdge = focusId !== null && (e.source_id === focusId || e.target_id === focusId)
      const isNeighborEdge = focusId !== null && !isFocusEdge && !!focusNeighbors
        && focusNeighbors.has(e.source_id) && focusNeighbors.has(e.target_id)
      // 影响高亮
      const bothHit = impactIdSet ? impactIdSet.has(e.source_id) && impactIdSet.has(e.target_id) : false
      const inHit = impactIdSet ? impactIdSet.has(e.source_id) || impactIdSet.has(e.target_id) : false

      let level = 0.7 // 默认常显(比旧版"0.45 若有若无"更有存在感)
      let width = 1.6
      let showLabel = true
      if (isHover) {
        level = 1; width = 2.6; showLabel = true
      } else if (focusId !== null) {
        if (isFocusEdge) { level = 1; width = 2.4; showLabel = true }
        else if (isNeighborEdge) { level = 0.6; width = 1.4; showLabel = true }
        else { level = 0.12; width = 1; showLabel = false }
      } else if (impactIdSet) {
        if (bothHit) { level = 1; width = 2.6; showLabel = true }
        else if (inHit) { level = 0.6; width = 1.4; showLabel = true }
        else { level = 0.12; width = 1; showLabel = false }
      }
      if (level < 0.25) showLabel = false
      const stroke = alpha(color, level)
      return {
        id: `e-${i}`,
        source: e.source_id,
        target: e.target_id,
        type: 'default',
        style: { stroke, strokeWidth: width, transition: 'stroke 0.15s' },
        markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: stroke },
        label: showLabel ? relLabel : undefined,
        labelStyle: {
          fill: level > 0.9 ? color : alpha(color, 0.78),
          fontSize: level > 0.9 ? 11 : 10,
          fontWeight: level > 0.9 ? 600 : 400,
        },
        labelBgPadding: [4, 2] as [number, number],
        labelBgBorderRadius: 4,
        labelBgStyle: { fill: 'var(--background)', fillOpacity: 0.85 },
      }
    })
  }, [visibleEdges, focusId, focusNeighbors, impactIdSet, hoverEdgeId])

  // 聚焦交互：hover 单条关系 → 提亮该 pair 的所有边并显示关系名
  const onEdgeMouseEnter = useCallback((_e: any, edge: Edge) => setHoverEdgeId(`${edge.source}->${edge.target}`), [])
  const onEdgeMouseLeave = useCallback(() => setHoverEdgeId(null), [])

  // ---- 派生：左侧列表 / 详情 / 统计 ----
  const filteredCIs = useMemo(() =>
    items.filter(ci =>
      ci.name.toLowerCase().includes(searchQ.toLowerCase()) ||
      ci.type_id.toLowerCase().includes(searchQ.toLowerCase())
    ), [items, searchQ])

  const selectedCIDetail = useMemo(() =>
    selectedNode ? items.find(ci => ci.id === selectedNode.id) : null
  , [selectedNode, items])

  const typeStats = useMemo(() => {
    const stats: Record<string, number> = {}
    for (const n of topology?.nodes ?? []) stats[n.type] = (stats[n.type] || 0) + 1
    return stats
  }, [topology])

  const relStats = useMemo(() => {
    const stats: Record<string, number> = {}
    for (const e of topology?.edges ?? []) stats[e.relation_type] = (stats[e.relation_type] || 0) + 1
    return stats
  }, [topology])

  if (!canRead) {
    return (
      <div className="p-6">
        <Card><CardContent className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <Network className="h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">无访问权限</p>
          <p className="text-xs text-muted-foreground">当前账号缺少「查看 CMDB 资源（cmdb:read）」权限，无法查看拓扑可视化。</p>
        </CardContent></Card>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      {loadError && items.length === 0 && <ErrorState message={`拓扑数据加载失败：${loadError}`} onRetry={loadInitial} />}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">拓扑可视化</h1>
          <p className="text-sm text-muted-foreground">关系图谱以任意 CI 为根按依赖深度分层展开；应用架构按业务分层聚合上下游资产</p>
        </div>
        {view === 'graph' && (
          <div className="flex items-center gap-2">
            <Button type="default" size="middle" onClick={() => selectedCI && loadTopology(selectedCI)}>
              <RefreshCw className="h-3.5 w-3.5 mr-1" />刷新
            </Button>
            <Button type="default" size="middle" onClick={runImpact} disabled={!selectedCI || impactLoading}>
              <AlertTriangle className="h-3.5 w-3.5 mr-1" />{impactLoading ? '分析中...' : '影响分析'}
            </Button>
            {impactResult && (
              <Button type={highlightAffected ? 'primary' : 'default'} size="middle" onClick={toggleHighlight}>
                {highlightAffected ? '取消高亮' : '画布高亮'}
              </Button>
            )}
          </div>
        )}
      </div>

      <Tabs activeKey={view} onChange={v => setView(v as 'graph' | 'app')} items={[
        { key: 'graph', label: <><Network className="h-3.5 w-3.5 mr-1" />关系图谱</>, children: null },
        { key: 'app', label: <><Layers className="h-3.5 w-3.5 mr-1" />应用架构</>, children: null },
      ]} />

      {view === 'graph' && (
        <div className="space-y-4 mt-4">
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
                            selectedCI === ci.id && "bg-blue-500/10 border border-blue-500/30"
                          )}
                          onClick={() => setSelectedCI(ci.id)}
                        >
                          <Icon className={cn("h-3.5 w-3.5 shrink-0", meta.color)} />
                          <span className="truncate flex-1">{ci.name}</span>
                          <Tag className="text-[9px]">{ci.type_id}</Tag>
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
                          <Tag key={type} className="text-[10px]">{type}: {count}</Tag>
                        ))}
                      </div>
                    </div>
                    <div>
                      <p className="text-xs text-muted-foreground mb-1.5">关系类型 ({(topology.edges ?? []).length})</p>
                      <div className="flex flex-wrap gap-1">
                        {Object.entries(relStats).map(([type, count]) => (
                          <Tag key={type} className="text-[10px]">
                            <span style={{ color: RELATION_COLORS[type] || GRAPH_DEFAULT_COLOR }}>●</span>
                            {RELATION_LABELS[type] || type}: {count}
                          </Tag>
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
                      <Tag color={impactResult.affected_ci.length > 0 ? 'warning' : undefined} className="ml-1">
                        {impactResult.affected_ci.length}
                      </Tag>
                    </div>
                    {impactResult.has_cycle && (
                      <Tag color="error" className="text-[10px]">检测到环路</Tag>
                    )}
                    {impactResult.affected_ci.length > 0 && (
                      <div className="space-y-1 max-h-40 overflow-y-auto">
                        {impactResult.affected_ci.map(id => {
                          const ci = items.find(c => c.id === id)
                          return (
                            <div key={id} className="text-xs flex items-center gap-1.5 p-1 rounded bg-amber-50 dark:bg-amber-500/10">
                              <span className="w-1 h-1 rounded-full bg-amber-500" />
                              <span className="truncate">{ci?.name || id.slice(0, 8)}</span>
                              {ci && <Tag className="text-[9px]">{ci.type_id}</Tag>}
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </CardContent>
                </Card>
              )}
            </div>

            {/* 右侧:图谱画布(React Flow) */}
            <div className="lg:col-span-3">
              <Card>
                <CardHeader className="py-3">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm">
                      拓扑画布 {topology && `(${topology.nodes.length} 节点, ${visibleEdges.length}/${(topology.edges ?? []).length} 关系)`}
                      {hiddenRelTypes.size > 0 && <span className="text-amber-500 text-[10px] ml-1">已过滤 {hiddenRelTypes.size} 类</span>}
                    </CardTitle>
                    <div className="flex items-center gap-1">
                      <Button type="text" size="middle" className="h-7 text-xs" onClick={resetView} title="重置视图(清除拖拽微调)">
                        <Maximize className="h-3.5 w-3.5 mr-1" />还原视图
                      </Button>
                    </div>
                  </div>
                </CardHeader>
                <CardContent>
                  <div className="relative h-[620px] border rounded-md overflow-hidden bg-muted/20">
                    <ReactFlowProvider>
                    <ReactFlow
                      nodes={rfNodes}
                      edges={rfEdges}
                      nodeTypes={nodeTypes}
                      onNodesChange={onNodesChange}
                      onNodeClick={onNodeClick}
                      onNodeDoubleClick={onNodeDoubleClick}
                      onNodeDragStop={onNodeDragStop}
                      onEdgeMouseEnter={onEdgeMouseEnter}
                      onEdgeMouseLeave={onEdgeMouseLeave}
                      onPaneClick={onPaneClick}
                      onInit={i => {
                        rfRef.current = i
                        // Provider 初始化后兜底 fit 一次, 避免初次 mount 的 0x0 视口残留
                        setTimeout(() => { try { i.fitView({ padding: 0.1, duration: 0, maxZoom: 1.2 }) } catch {} }, 200)
                      }}
                      nodesConnectable={false}
                      deleteKeyCode={null}
                      fitView
                      minZoom={0.15}
                      maxZoom={2.5}
                      proOptions={{ hideAttribution: true }}
                    >
                      <Background variant={BackgroundVariant.Dots} gap={26} size={1.5} color="var(--border)" style={{ opacity: 0.45 }} />
                      <Controls showInteractive={false} position="bottom-left" />
                      <MiniMap
                        pannable
                        zoomable
                        position="bottom-right"
                        nodeStrokeWidth={2}
                        nodeColor={n => {
                          const d = (n.data || {}) as Partial<CMDBNodeData>
                          if (d.isRoot) return HEALTH_COLOR.healthy
                          if (d.isSource) return HEALTH_COLOR.critical
                          if (d.isAffected) return HEALTH_COLOR.warning
                          return '#94a3b8'
                        }}
                        maskColor="rgba(148,163,184,0.08)"
                      />
                    </ReactFlow>
                    <LayerBands rows={layoutMeta.rows} totalW={layoutMeta.size.w} />
                    {(!topology || topology.nodes.length === 0) && (
                      <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
                        <div className="text-center">
                          <Network className="h-12 w-12 mx-auto mb-3 text-muted-foreground/30" />
                          <p className="text-sm text-muted-foreground">
                            {selectedCI ? '该 CI 无拓扑关系' : '请选择根节点'}
                          </p>
                        </div>
                      </div>
                    )}
                    </ReactFlowProvider>
                  </div>

                  {/* 图例(点击切换该关系类型显隐) */}
                  <div className="mt-3 flex flex-wrap gap-3 text-xs">
                    <span className="text-muted-foreground self-center">关系图例(点击显隐):</span>
                    {Object.entries(RELATION_COLORS).map(([type, color]) => {
                      const hidden = hiddenRelTypes.has(type)
                      return (
                        <button
                          key={type}
                          type="button"
                          className={cn(
                            'flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors',
                            hidden ? 'opacity-40 line-through hover:opacity-80' : 'hover:bg-muted',
                          )}
                          onClick={() => setHiddenRelTypes(prev => {
                            const next = new Set(prev)
                            if (next.has(type)) next.delete(type)
                            else next.add(type)
                            return next
                          })}
                        >
                          <span style={{ color: hidden ? 'currentColor' : color }} className="text-base leading-none">●</span>
                          <span className="text-muted-foreground">{RELATION_LABELS[type] || type}</span>
                        </button>
                      )
                    })}
                    {hiddenRelTypes.size > 0 && (
                      <button
                        type="button"
                        className="text-blue-500 hover:underline"
                        onClick={() => setHiddenRelTypes(new Set())}
                      >全部显示</button>
                    )}
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
                  <div className="flex items-center gap-1">
                    <Button type="default" size="middle" className="h-6 text-xs" onClick={() => setSelectedCI(selectedNode.id)}>
                      以该节点为根重绘
                    </Button>
                    <Button type="text" size="middle" className="h-6 w-6" onClick={() => setSelectedNode(null)}>×</Button>
                  </div>
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
                    <Tag>{selectedCIDetail.type_id}</Tag>
                  </div>
                  <div>
                    <p className="text-xs text-muted-foreground">状态</p>
                    <Tag color={selectedCIDetail.status === 'active' ? 'success' : undefined}>
                      {selectedCIDetail.status}
                    </Tag>
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
                        <Tag key={k} className="text-[10px]">{k}={v}</Tag>
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
        </div>
      )}

      {view === 'app' && (
        <div className="mt-4">
          <AppArchPanel />
        </div>
      )}
    </div>
  )
}
