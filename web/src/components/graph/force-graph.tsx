/**
 * 通用力导向图（d3-force 只算坐标，React Flow 负责渲染与交互）。
 *
 * 背景：此前 TopologyGraph / PanoramaGraph 各自用 d3-force + 原生 SVG `createElementNS`
 * 手绘图元，带来三个问题——图元 appendChild 后不清理（数据刷新叠加）、每个节点挂 window
 * 监听不移除（泄漏）、以及拖拽/缩放逻辑重复实现且与主拓扑页交互体验不一致。
 * 本组件统一收敛：d3 只产出坐标，渲染交给 React Flow，天然获得 minimap / 框选 / 平移缩放 /
 * 节点拖拽，且颜色统一走 lib/graph-colors.ts。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  forceCenter, forceCollide, forceLink, forceManyBody, forceSimulation,
  type SimulationLinkDatum, type SimulationNodeDatum,
} from 'd3-force'
import {
  Background, BackgroundVariant, Controls, MiniMap, ReactFlow, ReactFlowProvider,
  useNodesState,
  type Edge, type Node, type NodeProps, type NodeTypes,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { cn } from '@/lib/utils'
import { GRAPH_DEFAULT_COLOR, alpha } from '@/lib/graph-colors'

export interface ForceGraphNode {
  id: string
  label: string
  /** 分类色，缺省走 GRAPH_DEFAULT_COLOR */
  color?: string
  /** 副标题（如类型 / IP） */
  sub?: string
  /** 右上角徽标（如告警数） */
  badge?: string
  /** 原生 tooltip 全文 */
  title?: string
  /** 淡化（非聚焦范围） */
  dim?: boolean
}

export interface ForceGraphEdge {
  id: string
  source: string
  target: string
  color?: string
  dashed?: boolean
  width?: number
  label?: string
  dim?: boolean
}

interface FGData extends Record<string, unknown> {
  label: string
  color: string
  sub?: string
  badge?: string
  title?: string
  dim?: boolean
  ring?: boolean
}

type FGNode = Node<FGData, 'fg'>

function ForceNodeCard({ data }: NodeProps<FGNode>) {
  return (
    <div
      title={data.title}
      className={cn(
        'flex items-center gap-2 rounded-md border bg-card px-2.5 py-1.5 text-xs shadow-sm transition-opacity',
        data.dim && 'opacity-25',
        data.ring && 'ring-2 ring-primary',
      )}
      style={{ borderColor: data.color, maxWidth: 190 }}
    >
      <span className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: data.color }} />
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium leading-tight">{data.label}</span>
        {data.sub && <span className="block truncate text-[10px] leading-tight text-muted-foreground">{data.sub}</span>}
      </span>
      {data.badge && (
        <span className="shrink-0 rounded bg-muted px-1 text-[10px] text-muted-foreground">{data.badge}</span>
      )}
    </div>
  )
}

const nodeTypes: NodeTypes = { fg: ForceNodeCard }

type SimNode = SimulationNodeDatum & { id: string }
type SimLink = SimulationLinkDatum<SimNode>

/** 同步跑完力导向迭代，一次性产出坐标（避免 tick 回调逐帧 setState 造成抖动）。 */
function computeLayout(
  nodes: ForceGraphNode[],
  edges: ForceGraphEdge[],
  w: number,
  h: number,
): Record<string, { x: number; y: number }> {
  if (nodes.length === 0) return {}
  const cx = w / 2
  const cy = h / 2
  if (nodes.length === 1) return { [nodes[0].id]: { x: Math.round(cx - 60), y: Math.round(cy - 20) } }

  // 圆形播种 + 固定顺序 → 同一份数据每次得到同一张图（刷新不再乱跳）
  const r = Math.min(w, h) * 0.3
  const simNodes: SimNode[] = nodes.map((n, i) => {
    const a = (Math.PI * 2 * i) / nodes.length
    return { id: n.id, x: cx + Math.cos(a) * r, y: cy + Math.sin(a) * r }
  })
  const idSet = new Set(nodes.map(n => n.id))
  const simLinks: SimLink[] = edges
    .filter(e => idSet.has(e.source) && idSet.has(e.target) && e.source !== e.target)
    .map(e => ({ source: e.source, target: e.target }))

  const sim = forceSimulation<SimNode>(simNodes)
    .force('link', forceLink<SimNode, SimLink>(simLinks).id(d => d.id).distance(130).strength(0.35))
    .force('charge', forceManyBody().strength(-420))
    .force('collide', forceCollide(34))
    .force('center', forceCenter(cx, cy))
    .stop()

  const ticks = nodes.length > 200 ? 120 : 300
  for (let i = 0; i < ticks; i++) sim.tick()
  sim.stop()

  const out: Record<string, { x: number; y: number }> = {}
  for (const n of simNodes) out[n.id] = { x: Math.round(n.x || 0), y: Math.round(n.y || 0) }
  return out
}

interface Props {
  nodes: ForceGraphNode[]
  edges: ForceGraphEdge[]
  className?: string
  height?: number | string
  emptyText?: string
  onNodeClick?: (id: string) => void
  /** 框选结果变化（Shift + 拖拽框选） */
  onSelectionChange?: (ids: string[]) => void
  /** 搜索命中项，命中节点描边并参与"上一个/下一个"跳转 */
  matchIds?: Set<string>
  /** 平滑聚焦到该节点 */
  focusId?: string | null
  showMiniMap?: boolean
}

function ForceGraphInner({
  nodes, edges, className, height = '100%', emptyText = '暂无拓扑数据',
  onNodeClick, onSelectionChange, matchIds, focusId, showMiniMap = true,
}: Props) {
  const wrapRef = useRef<HTMLDivElement>(null)
  const [size, setSize] = useState({ w: 900, h: 560 })
  const [positions, setPositions] = useState<Record<string, { x: number; y: number }>>({})
  const overridesRef = useRef<Record<string, { x: number; y: number }>>({})
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<FGNode>([])
  const rfRef = useRef<any>(null)

  // 容器尺寸：力导向需要一个初始画布尺寸做中心与半径参考
  useEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const measure = () => {
      const rect = el.getBoundingClientRect()
      if (rect.width > 0 && rect.height > 0) setSize({ w: rect.width, h: rect.height })
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const nodeSig = useMemo(() => nodes.map(n => n.id).join('|'), [nodes])
  const edgeSig = useMemo(() => edges.map(e => `${e.source}>${e.target}`).join('|'), [edges])
  const matchSig = useMemo(() => (matchIds ? [...matchIds].sort().join('|') : ''), [matchIds])

  // 布局重算：仅当节点/边集合或画布尺寸变化时触发，避免每次 props 抖动都重排
  useEffect(() => {
    if (nodes.length === 0) { setPositions({}); return }
    overridesRef.current = {}
    setPositions(computeLayout(nodes, edges, size.w, size.h))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodeSig, edgeSig, size.w, size.h])

  useEffect(() => {
    const match = matchSig ? new Set(matchSig.split('|')) : null
    setRfNodes(nodes.map(n => ({
      id: n.id,
      type: 'fg' as const,
      position: overridesRef.current[n.id] || positions[n.id] || { x: 0, y: 0 },
      data: {
        label: n.label,
        color: n.color || GRAPH_DEFAULT_COLOR,
        sub: n.sub,
        badge: n.badge,
        title: n.title,
        dim: n.dim,
        ring: !!match?.has(n.id),
      },
    })))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodes, positions, matchSig, setRfNodes])

  const rfEdges = useMemo<Edge[]>(() => edges.map(e => {
    const color = e.color || GRAPH_DEFAULT_COLOR
    const a = e.dim ? 0.12 : 0.7
    return {
      id: e.id,
      source: e.source,
      target: e.target,
      type: 'default',
      style: {
        stroke: alpha(color, a),
        strokeWidth: e.width ?? 1.6,
        strokeDasharray: e.dashed ? '6 4' : undefined,
      },
      label: e.dim ? undefined : e.label,
      labelStyle: { fill: alpha(color, 0.9), fontSize: 10 },
      labelBgStyle: { fill: 'var(--background)', fillOpacity: 0.85 },
      labelBgPadding: [4, 2] as [number, number],
      labelBgBorderRadius: 4,
    }
  }), [edges])

  // 布局完成后适配视口
  useEffect(() => {
    if (rfNodes.length === 0) return
    const t = setTimeout(() => { try { rfRef.current?.fitView({ padding: 0.15, duration: 0, maxZoom: 1.2 }) } catch {} }, 80)
    return () => clearTimeout(t)
  }, [positions, nodes.length])

  // 聚焦：平滑居中到目标节点
  useEffect(() => {
    if (!focusId) return
    const p = overridesRef.current[focusId] || positions[focusId]
    if (!p) return
    try { rfRef.current?.setCenter(p.x + 90, p.y + 20, { zoom: 1.15, duration: 400 }) } catch {}
  }, [focusId, positions])

  const handleNodeClick = useCallback((_e: unknown, node: Node) => {
    onNodeClick?.(node.id)
  }, [onNodeClick])

  const handleNodeDragStop = useCallback((_e: unknown, node: Node) => {
    overridesRef.current[node.id] = { x: node.position.x, y: node.position.y }
  }, [])

  const handleSelectionChange = useCallback(({ nodes: sel }: { nodes: Node[] }) => {
    onSelectionChange?.(sel.map(n => n.id))
  }, [onSelectionChange])

  return (
    <div ref={wrapRef} className={cn('relative w-full', className)} style={{ height }}>
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onNodeClick={handleNodeClick}
        onNodeDragStop={handleNodeDragStop}
        onSelectionChange={handleSelectionChange}
        onInit={i => {
          rfRef.current = i
          setTimeout(() => { try { i.fitView({ padding: 0.15, duration: 0, maxZoom: 1.2 }) } catch {} }, 160)
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
        {showMiniMap && (
          <MiniMap
            pannable
            zoomable
            position="bottom-right"
            nodeStrokeWidth={2}
            nodeColor={n => (n.data as FGData | undefined)?.color || GRAPH_DEFAULT_COLOR}
            maskColor="rgba(148,163,184,0.08)"
          />
        )}
      </ReactFlow>
      {rfNodes.length === 0 && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
          <p className="text-sm text-muted-foreground">{emptyText}</p>
        </div>
      )}
      <div className="pointer-events-none absolute left-2 top-2 rounded bg-card/80 px-1.5 py-0.5 text-[10px] text-muted-foreground">
        Shift + 拖拽框选
      </div>
    </div>
  )
}

export default function ForceGraph(props: Props) {
  return (
    <ReactFlowProvider>
      <ForceGraphInner {...props} />
    </ReactFlowProvider>
  )
}
