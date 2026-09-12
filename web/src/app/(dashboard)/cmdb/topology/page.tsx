
import { useEffect, useState, useRef, useMemo, useCallback } from 'react'
import { useCMDBStore, type TopologyNode, type TopologyEdge, type TopologyResult } from '@/stores/cmdb'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Tabs, Segmented, Modal, AutoComplete, Select } from 'antd'
import { AppArchPanel } from '@/components/cmdb/app-arch-panel'
import { DependencyMatrix } from '@/components/cmdb/dependency-matrix'
import { TopologyDiff } from '@/components/cmdb/topology-diff'
import {
  Maximize, RefreshCw, Network, AlertTriangle, Layers, Search, GitBranch, EyeOff, RotateCcw,
  Server, Database, Cloud, Globe, Boxes, Shield, Link2, Download, ChevronUp, ChevronDown,
  FileSpreadsheet, FileCode, ArrowUpFromLine, ArrowDownToLine, GitCompareArrows,
} from 'lucide-react'
import { cn, formatTime, apiFetch } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { getMessage } from '@/lib/message'
import { toPng } from 'html-to-image'
import { RELATION_COLORS, RELATION_LABELS, GRAPH_DEFAULT_COLOR, HEALTH_COLOR, alpha } from '@/lib/graph-colors'
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

// 关系类型中文名统一由 @/lib/graph-colors 提供（含后端在用的 hosts / runs_on 等）

// hex(#rrggbb) -> rgba 的工具已收编到 @/lib/graph-colors（全景图/力导向图共用）。

// ============ 分层布局：以根为锚做 BFS 深度分列，同一深度一行 ============
// 相比力导向"蜘蛛网"，纵向分层的读法更像业务架构图：越靠近根越靠上，下游依赖逐层展开。
const LAYER = { cardW: 184, cardH: 62, rowGap: 132, colGap: 30, padX: 88, padTop: 120, padBottom: 104 }
/** 单层最多并排多少个节点，超出折行（防止画布被拉成超宽条） */
const MAX_PER_ROW = 6
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

  // —— 多行分层：同深度一行；单层节点超过 MAX_PER_ROW 时折行 ——
  // 关键：不折行的话，节点多的那一层会把画布拉成超宽条，fitView 只能把整图缩到很小、
  // 文字全部不可读。折行后画布宽度有上界，默认缩放下即可读。
  const perRow = Math.max(1, Math.min(maxCols, MAX_PER_ROW))
  const W = LAYER.padX * 2 + perRow * (LAYER.cardW + LAYER.colGap) - LAYER.colGap
  const rows: LayerRow[] = []
  let visualRow = 0
  rowsRaw.forEach(([d, list]) => {
    for (let i = 0; i < list.length; i += perRow) {
      const chunk = list.slice(i, i + perRow)
      const rowW = chunk.length * (LAYER.cardW + LAYER.colGap) - LAYER.colGap
      const xStart = (W - rowW) / 2
      const y = LAYER.padTop + visualRow * LAYER.rowGap
      chunk.forEach((n, j) => {
        positions[n.id] = { x: Math.round(xStart + j * (LAYER.cardW + LAYER.colGap)), y: Math.round(y) }
      })
      const orphan = d > maxReached
      const base = d === 0 ? '根' : orphan ? '孤立节点' : `第 ${d} 层`
      rows.push({ depth: d, y, label: i === 0 ? base : `${base}（续）`, orphan })
      visualRow++
    }
  })
  const H = LAYER.padTop + visualRow * LAYER.rowGap + LAYER.padBottom
  return { positions, size: { w: W, h: H }, rows }
}

// ============ 星形(放射)布局：以根为圆心，按 BFS 深度分环放置 ============
// 适合"中心辐射"型拓扑（一个核心 + 多圈外围），阅读焦点天然落在根上，
// 跨环连线呈放射状，与分层布局（纵向业务架构图）形成互补。
const RADIAL = { cardW: 184, cardH: 62, ringGap: 210, centerPad: 120, maxDepth: 6 }
function layoutRadial(
  nodes: TopologyNode[],
  rootId: string,
  edges: TopologyEdge[],
): { positions: Record<string, { x: number; y: number }>; size: { w: number; h: number }; rows: LayerRow[] } {
  const positions: Record<string, { x: number; y: number }> = {}
  if (nodes.length === 0) return { positions, size: { w: 960, h: 600 }, rows: [] }
  if (nodes.length === 1) {
    positions[nodes[0].id] = { x: RADIAL.centerPad + RADIAL.cardW / 2, y: RADIAL.centerPad + RADIAL.cardH / 2 }
    return { positions, size: { w: RADIAL.centerPad * 2 + RADIAL.cardW, h: RADIAL.centerPad * 2 + RADIAL.cardH }, rows: [] }
  }

  const idSet = new Set(nodes.map(n => n.id))
  const adj = new Map<string, string[]>()
  nodes.forEach(n => adj.set(n.id, []))
  for (const e of edges) {
    if (!idSet.has(e.source_id) || !idSet.has(e.target_id) || e.source_id === e.target_id) continue
    adj.get(e.source_id)?.push(e.target_id)
    adj.get(e.target_id)?.push(e.source_id)
  }

  // BFS 求每个节点到根的距离（无向遍历，上下游都可见）
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

  // 分组：与根连通者按深度入环；孤立节点落在最外环（maxReached+1，封顶 maxDepth）
  const groups = new Map<number, TopologyNode[]>()
  nodes.forEach(n => {
    let d = depth.has(n.id) ? depth.get(n.id)! : Math.min(maxReached + 1, RADIAL.maxDepth)
    if (d > RADIAL.maxDepth) d = RADIAL.maxDepth
    if (!groups.has(d)) groups.set(d, [])
    groups.get(d)!.push(n)
  })
  // 层内按"关联度大者靠上(角度起点 -90°)"稳定排序，降低连线交叉感
  const deg = new Map<string, number>()
  nodes.forEach(n => deg.set(n.id, 0))
  edges.forEach(e => {
    if (deg.has(e.source_id)) deg.set(e.source_id, deg.get(e.source_id)! + 1)
    if (deg.has(e.target_id)) deg.set(e.target_id, deg.get(e.target_id)! + 1)
  })
  groups.forEach(list => list.sort((a, b) => (deg.get(b.id) ?? 0) - (deg.get(a.id) ?? 0) || a.name.localeCompare(b.name, 'zh')))

  // 根放圆心；其余每层一个半径，角度均分（从正上方 -90° 起）
  const cx = 0
  const cy = 0
  if (rootId && idSet.has(rootId)) positions[rootId] = { x: cx, y: cy }

  const ringKeys = [...groups.keys()].filter(d => d >= 1).sort((a, b) => a - b)
  for (const d of ringKeys) {
    const list = groups.get(d)!
    const radius = d * RADIAL.ringGap
    const count = list.length
    list.forEach((n, i) => {
      const angle = (Math.PI * 2 * i) / count - Math.PI / 2
      positions[n.id] = {
        x: Math.round(cx + radius * Math.cos(angle)),
        y: Math.round(cy + radius * Math.sin(angle)),
      }
    })
  }

  // 计算包围盒并平移到正坐标（卡片中心对齐到节点坐标）
  const xs = Object.values(positions).map(p => p.x)
  const ys = Object.values(positions).map(p => p.y)
  const minX = Math.min(...xs) - RADIAL.cardW / 2
  const maxX = Math.max(...xs) + RADIAL.cardW / 2
  const minY = Math.min(...ys) - RADIAL.cardH / 2
  const maxY = Math.max(...ys) + RADIAL.cardH / 2
  const offX = RADIAL.centerPad - minX
  const offY = RADIAL.centerPad - minY
  const norm: Record<string, { x: number; y: number }> = {}
  for (const [id, p] of Object.entries(positions)) {
    norm[id] = { x: Math.round(p.x + offX), y: Math.round(p.y + offY) }
  }
  return {
    positions: norm,
    size: { w: Math.round(maxX - minX + RADIAL.centerPad * 2), h: Math.round(maxY - minY + RADIAL.centerPad * 2) },
    rows: [],
  }
}

// ============ 正交层次布局：分层网格 + 重心法层内排序，边走正交折线 ============
// 与分层的区别：分层按「关联度」排层内顺序，正交用重心法(barycenter)迭代——
// 每层节点按其在上一层邻居的平均列号重排，显著减少连线交叉，配合 smoothstep 正交折线
// 读起来接近架构图的直角走线，而不是一堆斜拉的贝塞尔。
const ORTHO = { cardW: 184, cardH: 62, rowGap: 132, colGap: 30, padX: 88, padTop: 120, padBottom: 104, maxPerRow: 6, rounds: 4 }
function layoutOrthogonal(
  nodes: TopologyNode[],
  rootId: string,
  edges: TopologyEdge[],
): { positions: Record<string, { x: number; y: number }>; size: { w: number; h: number }; rows: LayerRow[] } {
  const empty = { positions: {} as Record<string, { x: number; y: number }>, size: { w: 960, h: 600 }, rows: [] as LayerRow[] }
  if (nodes.length === 0) return empty
  if (nodes.length === 1) {
    return {
      positions: { [nodes[0].id]: { x: Math.round(480 - ORTHO.cardW / 2), y: ORTHO.padTop } },
      size: { w: 960, h: ORTHO.padTop + ORTHO.cardH + ORTHO.padBottom },
      rows: [{ depth: 0, y: ORTHO.padTop, label: '根', orphan: false }],
    }
  }

  const idSet = new Set(nodes.map(n => n.id))
  const adj = new Map<string, Set<string>>()
  nodes.forEach(n => adj.set(n.id, new Set()))
  for (const e of edges) {
    if (!idSet.has(e.source_id) || !idSet.has(e.target_id) || e.source_id === e.target_id) continue
    adj.get(e.source_id)!.add(e.target_id)
    adj.get(e.target_id)!.add(e.source_id)
  }

  // BFS 深度（无向）
  const depth = new Map<string, number>()
  if (rootId && idSet.has(rootId)) {
    depth.set(rootId, 0)
    const q = [rootId]
    while (q.length) {
      const cur = q.shift()!
      for (const nx of adj.get(cur) ?? []) {
        if (depth.has(nx)) continue
        depth.set(nx, (depth.get(cur) ?? 0) + 1)
        q.push(nx)
      }
    }
  }
  let maxReached = 0
  depth.forEach(d => { if (d > maxReached) maxReached = d })
  const MAX_DEPTH = 6
  const groups = new Map<number, TopologyNode[]>()
  nodes.forEach(n => {
    let d = depth.has(n.id) ? Math.min(depth.get(n.id)!, MAX_DEPTH) : Math.min(maxReached + 1, MAX_DEPTH)
    if (!groups.has(d)) groups.set(d, [])
    groups.get(d)!.push(n)
  })
  const deg = new Map<string, number>()
  nodes.forEach(n => deg.set(n.id, adj.get(n.id)?.size ?? 0))
  groups.forEach(list => list.sort((a, b) => (deg.get(b.id) ?? 0) - (deg.get(a.id) ?? 0) || a.name.localeCompare(b.name, 'zh')))

  // 重心法：双向扫描若干轮，每层按邻居在相邻层的平均序号重排
  const keys = [...groups.keys()].sort((a, b) => a - b)
  const colOf = new Map<string, number>()
  const reindex = () => { groups.forEach(list => list.forEach((n, i) => colOf.set(n.id, i))) }
  reindex()
  for (let r = 0; r < ORTHO.rounds; r++) {
    const down = r % 2 === 0
    const seq = down ? keys : [...keys].reverse()
    for (const d of seq) {
      const list = groups.get(d)!
      const refDepth = down ? d - 1 : d + 1
      const ref = groups.get(refDepth)
      if (!ref) continue
      const bary = new Map<string, number>()
      list.forEach((n, i) => {
        const ns = [...(adj.get(n.id) ?? [])].filter(x => (depth.get(x) ?? 0) === refDepth)
        const avg = ns.length ? ns.reduce((s, x) => s + (colOf.get(x) ?? 0), 0) / ns.length : Number.NaN
        bary.set(n.id, Number.isNaN(avg) ? i : avg)
      })
      list.sort((a, b) => (bary.get(a.id) ?? 0) - (bary.get(b.id) ?? 0) || a.name.localeCompare(b.name, 'zh'))
      reindex()
    }
  }

  const maxCols = Math.max(...keys.map(d => groups.get(d)!.length))
  const perRow = Math.max(1, Math.min(maxCols, ORTHO.maxPerRow))
  const W = ORTHO.padX * 2 + perRow * (ORTHO.cardW + ORTHO.colGap) - ORTHO.colGap
  const positions: Record<string, { x: number; y: number }> = {}
  const rows: LayerRow[] = []
  let visualRow = 0
  for (const d of keys) {
    const list = groups.get(d)!
    for (let i = 0; i < list.length; i += perRow) {
      const chunk = list.slice(i, i + perRow)
      const rowW = chunk.length * (ORTHO.cardW + ORTHO.colGap) - ORTHO.colGap
      const xStart = (W - rowW) / 2
      const y = ORTHO.padTop + visualRow * ORTHO.rowGap
      chunk.forEach((n, j) => {
        positions[n.id] = { x: Math.round(xStart + j * (ORTHO.cardW + ORTHO.colGap)), y: Math.round(y) }
      })
      const orphan = d > maxReached
      const base = d === 0 ? '根' : orphan ? '孤立节点' : `第 ${d} 层`
      rows.push({ depth: d, y, label: i === 0 ? base : `${base}（续）`, orphan })
      visualRow++
    }
  }
  return { positions, size: { w: W, h: ORTHO.padTop + visualRow * ORTHO.rowGap + ORTHO.padBottom }, rows }
}

const layoutFnOf = (mode: 'layered' | 'radial' | 'ortho') =>
  mode === 'radial' ? layoutRadial : mode === 'ortho' ? layoutOrthogonal : layoutLayered

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
  connectable?: boolean
  /** 画布内搜索命中 */
  isMatch?: boolean
}
type CMDBRFNode = Node<CMDBNodeData>
type CMDBRFGroupNode = Node<CMDBGroupData>
/** 画布上的节点可能是 CI 卡片，也可能是折叠后的类型团 */
type CMDBAnyNode = CMDBRFNode | CMDBRFGroupNode

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
      {/* 连线端点：上下两向；连线模式下显形供拖拽 */}
      <Handle type="target" position={Position.Top} style={{ opacity: data.connectable ? 0.9 : 0, width: 8, height: 8 }} />
      <Handle type="source" position={Position.Bottom} style={{ opacity: data.connectable ? 0.9 : 0, width: 8, height: 8 }} />

      <div
        className={cn(
          'relative flex items-center gap-2.5 rounded-xl border bg-background px-2.5 py-2 pr-3',
          'w-[184px] h-[62px] shadow-sm transition-[box-shadow,border-color] duration-150',
          meta.border,
          // 状态层次：选中 > 影响源 > 受影响 > 根 > 默认
          data.isSelected && 'ring-2 ring-blue-500 ring-offset-2 border-blue-400',
          // 搜索命中：用琥珀色描边，与「选中/影响」的蓝/红/黄语义区分
          !data.isSelected && !data.isSource && data.isMatch && 'ring-2 ring-amber-500 ring-offset-1',
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

// ============ 分组折叠节点：把同一类型（或同一业务）的一组 CI 收成一个可展开的团 ============
type CMDBGroupData = {
  label: string
  count: number
  expanded: boolean
  isDimmed: boolean
}
type CMDBGroupNode = Node<CMDBGroupData>

function CMDBGroupCard({ data }: NodeProps<CMDBGroupNode>) {
  const meta = TYPE_META[data.label] || DEFAULT_META
  const Icon = meta.icon
  return (
    <div className={cn('select-none transition-opacity', data.isDimmed && 'opacity-25')}>
      <div
        className={cn(
          'flex items-center gap-2 rounded-xl border-2 border-dashed bg-muted/40 px-2.5 py-2',
          'w-[184px] h-[62px] cursor-pointer hover:border-solid hover:bg-muted/70',
          meta.border,
        )}
      >
        <div className={cn('flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border', meta.bg, meta.border)}>
          <Icon className={cn('h-5 w-5', meta.color)} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-[13px] font-semibold leading-tight">{data.label}</div>
          <div className="mt-0.5 text-[11px] text-muted-foreground">{data.count} 个 · 点击展开</div>
        </div>
      </div>
      <Handle type="target" position={Position.Top} style={{ opacity: 0, width: 8, height: 8 }} />
      <Handle type="source" position={Position.Bottom} style={{ opacity: 0, width: 8, height: 8 }} />
    </div>
  )
}

const nodeTypes = { cmdb: CMDBNode, cmdbGroup: CMDBGroupCard }

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
  const { items, topology, impactResult, modelTypes, fetch, search, fetchTopology, fetchSubgraph, fetchImpact, fetchModelTypes } = useCMDBStore()
  const [selectedCI, setSelectedCI] = useState<string>('')
  const [searchQ, setSearchQ] = useState('')
  const [selectedNode, setSelectedNode] = useState<TopologyNode | null>(null)
  const [impactLoading, setImpactLoading] = useState(false)
  // 视图模式：关系图谱（以任意 CI 为根展开）/ 应用架构（按业务分层聚合）/ 依赖矩阵（N×N）
  const [view, setView] = useState<'graph' | 'app' | 'matrix' | 'diff'>('graph')
  // 影响范围画布高亮开关（清除时保留左侧分析结果卡片）
  const [highlightAffected, setHighlightAffected] = useState(false)
  // 关系类型显隐过滤(空=全显)
  const [hiddenRelTypes, setHiddenRelTypes] = useState<Set<string>>(new Set())
  // 只看关联节点：隐藏无任何关系的孤立节点（大图降噪）
  const [hideIsolated, setHideIsolated] = useState(false)
  // 布局模式：分层(layered) / 星形(radial) / 正交(ortho)。
  // 星形以根为圆心按深度分环，适合中心辐射型拓扑；正交用重心法排层内顺序 + 折线走边，交叉最少。
  const [layoutMode, setLayoutMode] = useState<'layered' | 'radial' | 'ortho'>('layered')
  // 连线模式：开启后节点端点显形，可拖拽建立关系
  const [connectMode, setConnectMode] = useState(false)
  const [connectPair, setConnectPair] = useState<{ source: string; target: string } | null>(null)
  const [relType, setRelType] = useState('')
  const [connecting, setConnecting] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  // ---- P1 大图能力 ----
  // 按类型分组折叠：把同类型 CI 收成一个可展开的团，节点量大时先看清"有哪些类、类之间怎么连"
  const [groupByType, setGroupByType] = useState(false)
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())
  // 画布内搜索：命中节点描边，回车逐个跳转
  const [canvasSearch, setCanvasSearch] = useState('')
  const [hitCursor, setHitCursor] = useState(0)
  const [jumpTick, setJumpTick] = useState(0)
  // 框选结果（Shift + 拖拽）：支持批量隐藏
  const [selectedIds, setSelectedIds] = useState<string[]>([])
  const [hiddenNodeIds, setHiddenNodeIds] = useState<Set<string>>(new Set())
  // 路径聚焦：只保留 A→B 最短路径及其直连邻域
  const [pathModal, setPathModal] = useState(false)
  const [pathFrom, setPathFrom] = useState('')
  const [pathTo, setPathTo] = useState('')
  const [pathIds, setPathIds] = useState<string[] | null>(null)
  const [pathLoading, setPathLoading] = useState(false)
  // 展开深度（后端 1..5，默认 3）。原先后端写死 3，大图看不到更远、小图又嫌噪。
  const [depth, setDepth] = useState(3)
  // 数据源：单根邻域(root) / 条件子图(subgraph)。
  // root 只能回答"以某个 CI 为中心的三层邻域"，subgraph 才回答"某业务/某环境里谁依赖谁"。
  const [scopeMode, setScopeMode] = useState<'root' | 'subgraph'>('root')
  const [subgraph, setSubgraph] = useState<TopologyResult | null>(null)
  const [subLoading, setSubLoading] = useState(false)
  const [subModelId, setSubModelId] = useState('')
  const [subEnv, setSubEnv] = useState('')
  const [subBusiness, setSubBusiness] = useState('')
  // 依赖方向：按边的 source→target 做有向遍历，区分"我依赖的(out)"与"依赖我的(in)"。
  // 拓扑 BFS 是无向的，图上"层"只是跳数，不开这个开关分不清上下游。
  const [directionMode, setDirectionMode] = useState<'off' | 'out' | 'in'>('off')

  // ---- React Flow 受控节点/画布状态 ----
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<CMDBAnyNode>([])
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

  // 根节点候选：默认拉前 200 条本地过滤；一旦输入关键字就下推到服务端搜索。
  // 原先只拉 200 条，租户一大，想找的 CI 根本不在候选列表里。
  const firstSearchRef = useRef(true)
  useEffect(() => {
    if (firstSearchRef.current) { firstSearchRef.current = false; return }
    const q = searchQ.trim()
    const t = setTimeout(() => {
      if (!q) { fetch({ limit: 200 }); return }
      search(q)
    }, 300)
    return () => clearTimeout(t)
  }, [searchQ, fetch, search])

  useEffect(() => {
    loadInitial()
    if (modelTypes.length === 0) fetchModelTypes()
  }, [loadInitial])

  // 首次加载后默认选中第一个 CI
  useEffect(() => {
    if (!selectedCI && items.length > 0) setSelectedCI(items[0].id)
  }, [items, selectedCI])

  // 布局模式用 ref 同步：若 loadTopology 直接依赖 layoutMode，切布局会让 useCallback 重建，
  // 进而触发下面的 useEffect 再发一次拓扑请求 —— 而 switchLayout 已经本地重算过坐标，
  // 那次请求纯属浪费，还会把用户的拖拽微调一起清掉。
  const layoutModeRef = useRef(layoutMode)
  useEffect(() => { layoutModeRef.current = layoutMode }, [layoutMode])

  // 布局/行元数据(泳道渲染用)——layoutRef 存坐标供节点重建, state 保证泳道层随布局刷新
  const [layoutMeta, setLayoutMeta] = useState<{ size: { w: number; h: number }; rows: LayerRow[] }>({ size: { w: 960, h: 600 }, rows: [] })
  // 加载拓扑：BFS 分层布局出全部节点坐标
  const loadTopology = useCallback(async (ciId: string) => {
    if (!ciId) return
    const result = await fetchTopology(ciId, depth)
    if (result && (result.nodes ?? []).length > 0) {
      dragOverridesRef.current = {}
      layoutRef.current = layoutFnOf(layoutModeRef.current)(result.nodes ?? [], ciId, result.edges ?? [])
      setLayoutMeta({ size: { ...layoutRef.current.size }, rows: layoutRef.current.rows })
      setSelectedNode(null)
      setHighlightAffected(false)
      setHoverEdgeId(null)
    }
  }, [fetchTopology, depth])

  // 条件子图：按模型/环境/业务筛选出一个节点集及其内部关系。
  // 这是依赖矩阵真正该吃的数据 —— 否则矩阵只是"某个根的邻居之间谁和谁有关系"。
  const loadSubgraph = useCallback(async () => {
    setSubLoading(true)
    try {
      const r = await fetchSubgraph({
        model_id: subModelId || undefined,
        env: subEnv || undefined,
        business: subBusiness || undefined,
        depth,
        limit: 200,
      })
      if (r) {
        setSubgraph({ nodes: r.nodes ?? [], edges: r.edges ?? [], truncated: r.truncated })
        const root = (r.nodes ?? [])[0]?.id ?? ''
        if (root) {
          layoutRef.current = layoutFnOf(layoutModeRef.current)(r.nodes ?? [], root, r.edges ?? [])
          setLayoutMeta({ size: { ...layoutRef.current.size }, rows: layoutRef.current.rows })
        }
        setSelectedNode(null)
        setHighlightAffected(false)
      }
    } finally {
      setSubLoading(false)
    }
  }, [fetchSubgraph, subModelId, subEnv, subBusiness, depth])

  // 切换布局：用已加载的拓扑就地重算坐标（不清空拖拽微调之外无需重新请求），并强制节点重建回新位
  const switchLayout = (mode: 'layered' | 'radial' | 'ortho') => {
    setLayoutMode(mode)
    if (topology && (topology.nodes ?? []).length > 0) {
      dragOverridesRef.current = {}
      layoutRef.current = layoutFnOf(mode)(topology.nodes ?? [], selectedCI, topology.edges ?? [])
      setLayoutMeta({ size: { ...layoutRef.current.size }, rows: layoutRef.current.rows })
    }
    setResetTick(t => t + 1)
  }

  useEffect(() => {
    if (scopeMode === 'subgraph') return
    if (selectedCI) loadTopology(selectedCI)
  }, [selectedCI, loadTopology, scopeMode])

  // 当前生效的数据源：单根邻域或条件子图。下面的渲染管线全部走它，两种模式行为一致。
  const activeTopology = scopeMode === 'subgraph' ? subgraph : topology

  // 后端现已保证 nodes/edges 为数组，此处再兜一层：任何历史/异常返回都不应让整页崩掉
  const topoNodes = activeTopology?.nodes ?? []
  const topoEdges = activeTopology?.edges ?? []

  // 有向邻接：out 表示"我依赖谁"(source→target)，in 表示"谁依赖我"(target→source)
  const dirAdj = useMemo(() => {
    const out = new Map<string, string[]>()
    const inn = new Map<string, string[]>()
    for (const e of topoEdges) {
      if (!out.has(e.source_id)) out.set(e.source_id, [])
      if (!inn.has(e.target_id)) inn.set(e.target_id, [])
      out.get(e.source_id)!.push(e.target_id)
      inn.get(e.target_id)!.push(e.source_id)
    }
    return { out, in: inn }
  }, [topoEdges])

  // 方向模式下的命中集：从根/选中节点做有向 BFS
  const dirSet = useMemo(() => {
    if (directionMode === 'off') return null
    const anchor = selectedNode?.id || selectedCI
    if (!anchor) return null
    const adj = directionMode === 'out' ? dirAdj.out : dirAdj.in
    const seen = new Set<string>([anchor])
    const queue = [anchor]
    while (queue.length > 0) {
      const cur = queue.shift()!
      for (const nb of adj.get(cur) ?? []) {
        if (!seen.has(nb)) { seen.add(nb); queue.push(nb) }
      }
    }
    return seen
  }, [directionMode, dirAdj, selectedNode, selectedCI])

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
    for (const e of topoEdges) {
      if (!m.has(e.source_id)) m.set(e.source_id, new Set())
      if (!m.has(e.target_id)) m.set(e.target_id, new Set())
      m.get(e.source_id)!.add(e.target_id)
      m.get(e.target_id)!.add(e.source_id)
    }
    return m
  }, [topoEdges])
  const focusNeighbors = focusId ? neighborMap.get(focusId) : undefined

  const ciById = useMemo(() => new Map(items.map(c => [c.id, c])), [items])

  // ---- P1：可见节点（排除手动隐藏的 / 孤立节点）----
  const baseNodes = useMemo(() => {
    let list = topoNodes.filter(n => !hiddenNodeIds.has(n.id))
    if (hideIsolated) list = list.filter(n => n.id === selectedCI || (neighborMap.get(n.id)?.size ?? 0) > 0)
    return list
  }, [topoNodes, hiddenNodeIds, hideIsolated, neighborMap, selectedCI])

  // ---- P1：按类型分组折叠 ----
  // 折叠时一组成员只画一个"团"节点，边随之归并到团级别，先看清类型间关系再下钻。
  const collapsedGroups = useMemo(() => {
    if (!groupByType) return [] as { key: string; type: string; members: TopologyNode[] }[]
    const m = new Map<string, TopologyNode[]>()
    for (const n of baseNodes) {
      if (expandedGroups.has(n.type)) continue
      if (!m.has(n.type)) m.set(n.type, [])
      m.get(n.type)!.push(n)
    }
    return [...m.entries()]
      .filter(([, members]) => members.length > 1)
      .map(([type, members]) => ({ key: `grp:${type}`, type, members }))
      .sort((a, b) => b.members.length - a.members.length)
  }, [groupByType, baseNodes, expandedGroups])

  const collapsedTypes = useMemo(() => new Set(collapsedGroups.map(g => g.type)), [collapsedGroups])

  // 节点 id -> 画布上实际渲染的 id（折叠时为团 id）
  const renderIdOf = useMemo(() => {
    const m = new Map<string, string>()
    if (!groupByType) return m
    for (const n of baseNodes) m.set(n.id, collapsedTypes.has(n.type) ? `grp:${n.type}` : n.id)
    return m
  }, [groupByType, baseNodes, collapsedTypes])

  // ---- P1：画布内搜索命中 ----
  const matchIds = useMemo(() => {
    const q = canvasSearch.trim().toLowerCase()
    if (!q) return null
    const s = new Set<string>()
    for (const n of baseNodes) {
      if (n.name.toLowerCase().includes(q) || n.type.toLowerCase().includes(q)) s.add(n.id)
    }
    return s
  }, [canvasSearch, baseNodes])

  // ---- P1：路径聚焦（A→B 最短路径 + 其直连邻域）----
  const pathSet = useMemo(() => {
    if (!pathIds || pathIds.length === 0) return null
    const s = new Set(pathIds)
    for (const id of pathIds) for (const nb of neighborMap.get(id) ?? []) s.add(nb)
    return s
  }, [pathIds, neighborMap])

  useEffect(() => {
    if (!activeTopology) return
    const pos = layoutRef.current.positions
    const next: CMDBAnyNode[] = []

    // 折叠团：位置取组内首个成员的布局位（拖拽后按团 id 记 override）
    for (const g of collapsedGroups) {
      const p = dragOverridesRef.current[g.key] || pos[g.members[0].id] || { x: 80, y: 80 }
      next.push({
        id: g.key,
        type: 'cmdbGroup' as const,
        position: p,
        data: { label: g.type, count: g.members.length, expanded: false, isDimmed: !!pathSet && g.members.some(m => !pathSet.has(m.id)) },
      })
    }

    for (const n of baseNodes) {
      if (collapsedTypes.has(n.type)) continue
      const p = dragOverridesRef.current[n.id] || pos[n.id] || { x: 80, y: 80 }
      const ci = ciById.get(n.id)
      const isRoot = n.id === selectedCI
      const isSelected = n.id === selectedNode?.id
      const isSource = impactIdSet ? n.id === impactResult?.source_ci : false
      const isAffected = impactIdSet ? impactIdSet.has(n.id) && !isSource : false
      // 淡化优先级：路径聚焦 > 依赖方向 > 影响高亮 > 邻域聚焦
      const isDimmed = pathSet
        ? !pathSet.has(n.id)
        : dirSet
          ? !dirSet.has(n.id)
          : impactIdSet
            ? !impactIdSet.has(n.id)
            : focusId !== null
              ? n.id !== focusId && !focusNeighbors?.has(n.id)
              : false
      next.push({
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
          connectable: connectMode,
          isMatch: !!matchIds?.has(n.id),
        },
      })
    }
    setRfNodes(next)
  }, [activeTopology, selectedCI, selectedNode, impactIdSet, impactOn, focusId, focusNeighbors, ciById, resetTick,
    connectMode, hideIsolated, neighborMap, setRfNodes, baseNodes, collapsedGroups, collapsedTypes, matchIds, pathSet, dirSet])

  // 布局或数据变化后自动适配视口：禁用动画(duration:0)避免 LayerBands 同步滞后
  useEffect(() => {
    if (!activeTopology || topoNodes.length === 0) return
    const t = setTimeout(() => { rfRef.current?.fitView({ padding: 0.1, duration: 0, maxZoom: 1.2 }) }, 120)
    return () => clearTimeout(t)
  }, [activeTopology, topoNodes.length, layoutMeta.size, resetTick, hideIsolated])

  // 影响分析
  const runImpact = async () => {
    if (!selectedCI) return
    setImpactLoading(true)
    await fetchImpact(selectedCI)
    setHighlightAffected(true)
    setImpactLoading(false)
  }
  const toggleHighlight = () => setHighlightAffected(v => !v)

  // ---- P1：路径聚焦（A→B 最短依赖路径，其余淡出）----
  const runPathFocus = async () => {
    if (!pathFrom || !pathTo || pathFrom === pathTo) {
      getMessage().warning('请选择两个不同的 CI')
      return
    }
    setPathLoading(true)
    try {
      const r = await apiFetch<{ found: boolean; path: { id: string }[] }>(
        `/cmdb/path?from=${encodeURIComponent(pathFrom)}&to=${encodeURIComponent(pathTo)}`,
      )
      if (!r?.found || !r.path?.length) {
        setPathIds(null)
        getMessage().warning('这两个 CI 之间没有可达的依赖路径')
      } else {
        setPathIds(r.path.map(p => p.id))
        // 路径聚焦与分组折叠互斥：折叠后路径节点会被收进团里，看不出链路
        setGroupByType(false)
        setExpandedGroups(new Set())
        setSelectedNode(null)
        setHighlightAffected(false)
        getMessage().success(`已聚焦路径（${r.path.length} 个节点）`)
      }
      setPathModal(false)
    } catch (e: any) {
      getMessage().error('路径查询失败: ' + e.message)
    } finally {
      setPathLoading(false)
    }
  }
  const clearPathFocus = () => { setPathIds(null); setHitCursor(0) }

  // ---- P1：画布内搜索：命中描边 + 逐个跳转居中 ----
  const jumpToMatch = (dir: 1 | -1) => {
    if (!matchIds || matchIds.size === 0) return
    const arr = [...matchIds]
    const next = (hitCursor + dir + arr.length) % arr.length
    setHitCursor(next)
    const id = arr[next]
    const p = dragOverridesRef.current[id] || layoutRef.current.positions[id]
    if (p) { try { rfRef.current?.setCenter(p.x + 92, p.y + 31, { zoom: 1.2, duration: 350 }) } catch {} }
  }

  // ---- P1：框选批量操作（Shift + 拖拽框选）----
  const hideSelected = () => {
    if (selectedIds.length === 0) return
    setHiddenNodeIds(prev => {
      const n = new Set(prev)
      selectedIds.forEach(id => n.add(id))
      return n
    })
    getMessage().success(`已隐藏 ${selectedIds.length} 个节点`)
    setSelectedIds([])
    setResetTick(t => t + 1)
  }
  const restoreHidden = () => {
    setHiddenNodeIds(new Set())
    setResetTick(t => t + 1)
  }

  // 还原视图：清除拖拽微调 → 重建回分层位 → 自动适配视口
  const resetView = () => {
    dragOverridesRef.current = {}
    setResetTick(t => t + 1)
  }

  // ---- 导出：PNG 之外补 CSV 关系清单与 Mermaid，便于归档和贴进文档 ----
  const downloadText = (filename: string, content: string, mime: string) => {
    const blob = new Blob([content], { type: mime })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
    URL.revokeObjectURL(url)
  }

  const exportCsv = () => {
    if (topoNodes.length === 0) { getMessage().warning('当前没有可导出的拓扑'); return }
    const nameOf = (id: string) => {
      const n = topoNodes.find(x => x.id === id)
      return n?.name || id
    }
    const rows = [['source_id', 'source_name', 'relation_type', 'target_id', 'target_name']]
    for (const e of topoEdges) {
      rows.push([e.source_id, nameOf(e.source_id), e.relation_type, e.target_id, nameOf(e.target_id)])
    }
    const csv = rows.map(r => r.map(v => `"${String(v).replace(/"/g, '""')}"`).join(',')).join('\n')
    // BOM 保证 Excel 打开不乱码
    downloadText(`topology-relations-${Date.now()}.csv`, '\uFEFF' + csv, 'text/csv;charset=utf-8')
    getMessage().success(`已导出 ${topoEdges.length} 条关系`)
  }

  const exportMermaid = () => {
    if (topoNodes.length === 0) { getMessage().warning('当前没有可导出的拓扑'); return }
    // 节点 id 带连字符，Mermaid 里必须加引号；名称里的引号换成中文引号避免破坏语法
    const safe = (s: string) => String(s || '').replace(/"/g, '“')
    const lines = ['graph LR']
    for (const n of topoNodes) {
      lines.push(`  n_${n.id.replace(/-/g, '_')}["${safe(n.name)}<br/>${safe(n.type)}"]`)
    }
    for (const e of topoEdges) {
      const label = RELATION_LABELS[e.relation_type] || e.relation_type
      lines.push(`  n_${e.source_id.replace(/-/g, '_')} -->|"${safe(label)}"| n_${e.target_id.replace(/-/g, '_')}`)
    }
    downloadText(`topology-${Date.now()}.mmd`, lines.join('\n'), 'text/plain;charset=utf-8')
    getMessage().success('已导出 Mermaid 图')
  }

  const onNodeClick = useCallback((_e: any, node: Node) => {
    // 折叠团：点击即展开该类型
    if (node.id.startsWith('grp:')) {
      const type = node.id.slice(4)
      setExpandedGroups(prev => { const n = new Set(prev); n.add(type); return n })
      setResetTick(t => t + 1)
      return
    }
    const n = topoNodes.find(x => x.id === node.id)
    if (n) setSelectedNode(n)
  }, [topoNodes])

  const onNodeDoubleClick = useCallback((_e: any, node: Node) => {
    setSelectedCI(node.id)
  }, [])

  // 拖拽连线建关系：弹窗选择关系类型后落库
  const onConnect = useCallback((c: { source: string; target: string }) => {
    if (!c.source || !c.target || c.source === c.target) return
    setRelType('')
    setConnectPair({ source: c.source, target: c.target })
  }, [])

  const confirmConnect = useCallback(async () => {
    if (!connectPair || !relType) {
      getMessage().warning('请选择关系类型')
      return
    }
    setConnecting(true)
    try {
      const ok = await useCMDBStore.getState().createRelation({
        source_id: connectPair.source,
        target_id: connectPair.target,
        relation_type: relType,
      })
      if (ok) {
        getMessage().success('关系已建立')
        setConnectPair(null)
        setConnectMode(false)
        if (selectedCI) loadTopology(selectedCI)
      } else {
        getMessage().error('建立关系失败')
      }
    } catch (e: any) {
      getMessage().error('建立关系失败: ' + e.message)
    } finally {
      setConnecting(false)
    }
  }, [connectPair, relType, selectedCI, loadTopology])

  // 导出拓扑为 PNG（截取 React Flow viewport，按当前缩放铺满画布）
  const exportPng = useCallback(async () => {
    const vp = document.querySelector('.react-flow__viewport') as HTMLElement | null
    if (!vp) {
      getMessage().error('画布未就绪')
      return
    }
    setExporting(true)
    try {
      const dataUrl = await toPng(vp, { backgroundColor: '#ffffff', pixelRatio: 2, cacheBust: true })
      const a = document.createElement('a')
      a.href = dataUrl
      a.download = `cmdb-topology-${selectedCI || 'all'}-${Date.now()}.png`
      a.click()
      getMessage().success('已导出 PNG')
    } catch (e: any) {
      getMessage().error('导出失败: ' + e.message)
    } finally {
      setExporting(false)
    }
  }, [selectedCI])

  const onPaneClick = useCallback(() => {
    setSelectedNode(null)
    setHoverEdgeId(null)
  }, [])

  const onNodeDragStop = useCallback((_e: any, node: Node) => {
    dragOverridesRef.current[node.id] = { x: node.position.x, y: node.position.y }
  }, [])

  // 框选（Shift + 拖拽）：结果用于批量隐藏
  const onSelectionChange = useCallback(({ nodes: sel }: { nodes: Node[] }) => {
    setSelectedIds(sel.map(n => n.id))
  }, [])

  // ---- 边：按 聚焦 > 影响 > 默认 三档强调，颜色统一带透明度淡化（箭头同色同步） ----
  const visibleEdges = useMemo(
    () => (topology?.edges ?? []).filter(e => e.source_id !== e.target_id && !hiddenRelTypes.has(e.relation_type)),
    [topology, hiddenRelTypes],
  )

  // 边密集时默认不铺标签（互相叠压会让全图不可读），悬停/聚焦某条边仍会显示
  const denseGraph = visibleEdges.length > 14

  // 路径上的连续 pair（用于把最短路径本身画成最亮的主干）
  const pathEdgeKeys = useMemo(() => {
    if (!pathIds || pathIds.length < 2) return null
    const s = new Set<string>()
    for (let i = 0; i < pathIds.length - 1; i++) {
      s.add(`${pathIds[i]}->${pathIds[i + 1]}`)
      s.add(`${pathIds[i + 1]}->${pathIds[i]}`)
    }
    return s
  }, [pathIds])

  // 折叠后边归并到团级别（同一对团之间的多条边合成一条，标注条数）
  const displayEdges = useMemo(() => {
    const out: { source_id: string; target_id: string; relation_type: string; count: number }[] = []
    const idx = new Map<string, number>()
    for (const e of visibleEdges) {
      const s = renderIdOf.get(e.source_id) ?? e.source_id
      const t = renderIdOf.get(e.target_id) ?? e.target_id
      if (s === t) continue
      const k = `${s}->${t}`
      const at = idx.get(k)
      if (at !== undefined) { out[at].count++; continue }
      idx.set(k, out.length)
      out.push({ source_id: s, target_id: t, relation_type: e.relation_type, count: 1 })
    }
    return out
  }, [visibleEdges, renderIdOf])

  const rfEdges = useMemo<Edge[]>(() => {
    const list = displayEdges
    return list.map((e, i) => {
      const color = RELATION_COLORS[e.relation_type] || GRAPH_DEFAULT_COLOR
      const relLabel = (RELATION_LABELS[e.relation_type] || e.relation_type) + (e.count > 1 ? ` ×${e.count}` : '')
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
      let showLabel = !denseGraph
      if (pathSet) {
        // 路径聚焦：最短路径主干最亮，其余几乎隐去
        if (pathEdgeKeys?.has(pairKey)) { level = 1; width = 3; showLabel = true }
        else { level = 0.08; width = 1; showLabel = false }
      } else if (isHover) {
        level = 1; width = 2.6; showLabel = true
      } else if (focusId !== null) {
        if (isFocusEdge) { level = 1; width = 2.4; showLabel = true }
        else if (isNeighborEdge) { level = 0.6; width = 1.4; showLabel = true }
        else { level = 0.12; width = 1; showLabel = false }
      } else if (impactIdSet) {
        if (bothHit) { level = 1; width = 2.6; showLabel = true }
        else if (inHit) { level = 0.6; width = 1.4; showLabel = true }
        else { level = 0.12; width = 1; showLabel = false }
      } else if (dirSet) {
        // 依赖方向：只提亮落在有向可达集内的边，其余淡出
        if (dirSet.has(e.source_id) && dirSet.has(e.target_id)) { level = 1; width = 2.4; showLabel = true }
        else { level = 0.08; width = 1; showLabel = false }
      }
      if (level < 0.25) showLabel = false
      const stroke = alpha(color, level)
      return {
        id: `e-${i}`,
        source: e.source_id,
        target: e.target_id,
        // 正交布局：边走直角折线，配合重心法排序显著降低交叉感
        type: layoutMode === 'ortho' ? 'smoothstep' : 'default',
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
  }, [displayEdges, focusId, focusNeighbors, impactIdSet, hoverEdgeId, pathSet, pathEdgeKeys, layoutMode, denseGraph, dirSet])

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
    for (const n of topoNodes) stats[n.type] = (stats[n.type] || 0) + 1
    return stats
  }, [topology])

  const relStats = useMemo(() => {
    const stats: Record<string, number> = {}
    for (const e of topoEdges) stats[e.relation_type] = (stats[e.relation_type] || 0) + 1
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
          <p className="text-sm text-muted-foreground">关系图谱以任意 CI 为根按依赖深度展开（分层 / 星形 / 正交三种布局）；依赖矩阵用 N×N 回答"谁和谁有关系"；应用架构按业务分层聚合上下游资产</p>
        </div>
        {view === 'graph' && (
          <div className="flex items-center gap-2">
            <Select
              className="w-32" size="middle"
              value={scopeMode}
              onChange={(v) => { setScopeMode(v); if (v === 'subgraph' && !subgraph) loadSubgraph() }}
              options={[
                { value: 'root', label: '单根邻域' },
                { value: 'subgraph', label: '条件子图' },
              ]}
            />
            <Select
              className="w-24" size="middle"
              value={depth} onChange={setDepth}
              options={[1, 2, 3, 4, 5].map(d => ({ value: d, label: `深度 ${d}` }))}
            />
            <Button type="default" size="middle" onClick={() => (scopeMode === 'subgraph' ? loadSubgraph() : selectedCI && loadTopology(selectedCI))}>
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

      <Tabs activeKey={view} onChange={v => setView(v as 'graph' | 'app' | 'matrix' | 'diff')} items={[
        { key: 'graph', label: <><Network className="h-3.5 w-3.5 mr-1" />关系图谱</>, children: null },
        { key: 'matrix', label: <><Boxes className="h-3.5 w-3.5 mr-1" />依赖矩阵</>, children: null },
        { key: 'app', label: <><Layers className="h-3.5 w-3.5 mr-1" />应用架构</>, children: null },
        { key: 'diff', label: <><GitCompareArrows className="h-3.5 w-3.5 mr-1" />变更对比</>, children: null },
      ]} />

      {/* 条件子图筛选：单根邻域只能看"某个 CI 周围三层"，这里按模型/环境/业务整片取 */}
      {scopeMode === 'subgraph' && (
        <div className="mt-3 flex flex-wrap items-end gap-3 rounded-md border p-3">
          <div>
            <p className="mb-1 text-xs text-muted-foreground">模型类型</p>
            <Select
              className="w-52" allowClear showSearch optionFilterProp="label" placeholder="全部模型"
              value={subModelId || undefined} onChange={v => setSubModelId(v || '')}
              options={modelTypes.map(t => ({ value: t.id, label: `${t.name || t.id} (${t.id})` }))}
            />
          </div>
          <div>
            <p className="mb-1 text-xs text-muted-foreground">环境</p>
            <Input className="w-36" placeholder="如 prod" value={subEnv} onChange={e => setSubEnv(e.target.value)} />
          </div>
          <div>
            <p className="mb-1 text-xs text-muted-foreground">业务</p>
            <Input className="w-36" placeholder="如 交易" value={subBusiness} onChange={e => setSubBusiness(e.target.value)} />
          </div>
          <Button type="primary" loading={subLoading} onClick={loadSubgraph}>加载子图</Button>
          {subgraph && (
            <span className="text-xs text-muted-foreground">
              {subgraph.nodes?.length ?? 0} 节点 / {subgraph.edges?.length ?? 0} 关系
              {subgraph.truncated && '（已按上限截断，可加筛选条件缩小范围）'}
            </span>
          )}
        </div>
      )}

      {view === 'graph' && (
        <div className="space-y-4 mt-4">
          {topology?.truncated && (
            <div className="text-xs text-amber-600 bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/20 rounded-md px-3 py-2 flex items-center gap-2">
              <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
              <span>拓扑节点规模过大，已自动收缩仅展示部分节点（当前根节点连接极其密集）。可换一个根节点查看局部拓扑。</span>
            </div>
          )}
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
              {topoNodes.length > 0 && (
                <Card>
                  <CardHeader className="py-3">
                    <CardTitle className="text-sm">图谱统计</CardTitle>
                  </CardHeader>
                  <CardContent className="space-y-3">
                    <div>
                      <p className="text-xs text-muted-foreground mb-1.5">节点类型 ({topoNodes.length})</p>
                      <div className="flex flex-wrap gap-1">
                        {Object.entries(typeStats).map(([type, count]) => (
                          <Tag key={type} className="text-[10px]">{type}: {count}</Tag>
                        ))}
                      </div>
                    </div>
                    <div>
                      <p className="text-xs text-muted-foreground mb-1.5">关系类型 ({topoEdges.length})</p>
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
                      拓扑画布 {activeTopology && `(${topoNodes.length} 节点, ${visibleEdges.length}/${topoEdges.length} 关系)`}
                      {hiddenRelTypes.size > 0 && <span className="text-amber-500 text-[10px] ml-1">已过滤 {hiddenRelTypes.size} 类</span>}
                    </CardTitle>
                    <div className="flex items-center gap-1">
                      <span className="text-xs text-muted-foreground mr-0.5">布局</span>
                      <Segmented
                        size="small"
                        value={layoutMode}
                        onChange={v => switchLayout(v as 'layered' | 'radial' | 'ortho')}
                        options={[
                          { label: '分层', value: 'layered' },
                          { label: '星形', value: 'radial' },
                          { label: '正交', value: 'ortho' },
                        ]}
                      />
                      <Button type="text" size="middle" className="h-7 text-xs" onClick={resetView} title="重置视图(清除拖拽微调)">
                        <Maximize className="h-3.5 w-3.5 mr-1" />还原视图
                      </Button>
                      <Button
                        type={hideIsolated ? 'primary' : 'text'} size="middle" className="h-7 text-xs"
                        onClick={() => setHideIsolated(v => !v)}
                        title="隐藏没有任何关系的孤立节点，便于看清主干拓扑"
                      >
                        <Layers className="h-3.5 w-3.5 mr-1" />只看关联
                      </Button>
                      <Button
                        type={connectMode ? 'primary' : 'text'} size="middle" className="h-7 text-xs"
                        onClick={() => setConnectMode(v => !v)} title="开启后拖拽节点端点即可建立关系"
                      >
                        <Link2 className="h-3.5 w-3.5 mr-1" />{connectMode ? '连线中' : '连线'}
                      </Button>
                      <Button type="text" size="middle" className="h-7 text-xs" loading={exporting} onClick={exportPng} title="导出当前拓扑为 PNG">
                        <Download className="h-3.5 w-3.5 mr-1" />PNG
                      </Button>
                      <Button type="text" size="middle" className="h-7 text-xs" onClick={exportCsv} title="导出关系清单 CSV（Excel 可直接打开）">
                        <FileSpreadsheet className="h-3.5 w-3.5 mr-1" />CSV
                      </Button>
                      <Button type="text" size="middle" className="h-7 text-xs" onClick={exportMermaid} title="导出为 Mermaid 图，可贴进文档/知识库">
                        <FileCode className="h-3.5 w-3.5 mr-1" />Mermaid
                      </Button>
                    </div>
                  </div>
                </CardHeader>
                <CardContent>
                  {/* P1 画布工具条：搜索定位 / 分组折叠 / 路径聚焦 / 批量操作 */}
                  <div className="mb-2 flex flex-wrap items-center gap-2 text-xs">
                    <div className="relative">
                      <Input
                        className="h-7 w-52 pl-7 pr-14 text-xs"
                        placeholder="画布内搜索节点…"
                        value={canvasSearch}
                        onChange={e => { setCanvasSearch(e.target.value); setHitCursor(0) }}
                        onPressEnter={() => jumpToMatch(1)}
                      />
                      <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                      {matchIds && (
                        <span className="absolute right-1.5 top-1/2 flex -translate-y-1/2 items-center gap-0.5">
                          <span className="text-[10px] text-muted-foreground">{Math.min(hitCursor + 1, matchIds.size)}/{matchIds.size}</span>
                          <button type="button" className="rounded px-0.5 hover:bg-muted" title="上一个" onClick={() => jumpToMatch(-1)}>
                            <ChevronUp className="h-3 w-3" />
                          </button>
                          <button type="button" className="rounded px-0.5 hover:bg-muted" title="下一个" onClick={() => jumpToMatch(1)}>
                            <ChevronDown className="h-3 w-3" />
                          </button>
                        </span>
                      )}
                    </div>

                    {/* 依赖方向：BFS 是无向的，图上"层"只是跳数，开这个才分得清上下游 */}
                    <Segmented
                      size="small"
                      value={directionMode}
                      onChange={v => setDirectionMode(v as 'off' | 'out' | 'in')}
                      options={[
                        { label: '不分方向', value: 'off' },
                        { label: '我依赖的', value: 'out' },
                        { label: '依赖我的', value: 'in' },
                      ]}
                    />

                    <Button
                      type={groupByType ? 'primary' : 'text'} size="middle" className="h-7 text-xs"
                      onClick={() => { setGroupByType(v => !v); setExpandedGroups(new Set()); setResetTick(t => t + 1) }}
                      title="把同类型 CI 收成一个可点击展开的团，节点量大时先看清类型间关系"
                    >
                      <Boxes className="h-3.5 w-3.5 mr-1" />按类型折叠
                    </Button>

                    {pathIds ? (
                      <Button type="primary" size="middle" className="h-7 text-xs" onClick={clearPathFocus}>
                        <GitBranch className="h-3.5 w-3.5 mr-1" />退出路径聚焦（{pathIds.length} 节点）
                      </Button>
                    ) : (
                      <Button
                        type="text" size="middle" className="h-7 text-xs"
                        onClick={() => { setPathFrom(selectedCI); setPathTo(''); setPathModal(true) }}
                        title="查询任意两个 CI 之间的最短依赖路径，画布只保留该链路"
                      >
                        <GitBranch className="h-3.5 w-3.5 mr-1" />路径聚焦
                      </Button>
                    )}

                    {hiddenNodeIds.size > 0 && (
                      <Button type="text" size="middle" className="h-7 text-xs" onClick={restoreHidden}>
                        <RotateCcw className="h-3.5 w-3.5 mr-1" />恢复 {hiddenNodeIds.size} 个隐藏节点
                      </Button>
                    )}

                    {selectedIds.length > 0 && (
                      <span className="ml-auto flex items-center gap-1 rounded-md border bg-muted/50 px-2 py-1">
                        <span className="text-muted-foreground">已选 {selectedIds.length}</span>
                        <Button type="text" size="middle" className="h-6 text-xs" onClick={hideSelected}>
                          <EyeOff className="h-3.5 w-3.5 mr-1" />隐藏
                        </Button>
                        <Button type="text" size="middle" className="h-6 text-xs" onClick={() => setSelectedIds([])}>取消</Button>
                      </span>
                    )}
                    {!selectedIds.length && <span className="ml-auto text-[10px] text-muted-foreground">Shift + 拖拽框选可批量操作</span>}
                  </div>

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
                      onSelectionChange={onSelectionChange}
                      onConnect={onConnect}
                      onInit={i => {
                        rfRef.current = i
                        // Provider 初始化后兜底 fit 一次, 避免初次 mount 的 0x0 视口残留
                        setTimeout(() => { try { i.fitView({ padding: 0.1, duration: 0, maxZoom: 1.2 }) } catch {} }, 200)
                      }}
                      nodesConnectable={connectMode}
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
                    {layoutMode === 'layered' && <LayerBands rows={layoutMeta.rows} totalW={layoutMeta.size.w} />}
                    {(!topology || topoNodes.length === 0) && (
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

      {view === 'matrix' && (
        <div className="mt-4">
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm flex items-center gap-2">
                <Boxes className="h-4 w-4" />依赖矩阵
                {activeTopology && (
                  <span className="text-[11px] font-normal text-muted-foreground">
                    {scopeMode === 'subgraph' ? '基于条件子图' : '基于当前根节点'}：{topoNodes.length} 节点 / {visibleEdges.length} 关系
                  </span>
                )}
              </CardTitle>
            </CardHeader>
            <CardContent>
              {!activeTopology || topoNodes.length === 0 ? (
                <p className="py-12 text-center text-sm text-muted-foreground">
                  {scopeMode === 'subgraph'
                    ? '当前筛选条件下没有 CI，试试放宽模型/环境/业务条件'
                    : selectedCI ? '该 CI 无拓扑关系' : '请选择根节点'}
                </p>
              ) : (
                <DependencyMatrix
                  nodes={topoNodes.map(n => ({ id: n.id, name: n.name, type: n.type }))}
                  edges={visibleEdges.map(e => ({ source_id: e.source_id, target_id: e.target_id, relation_type: e.relation_type }))}
                  onCellClick={(source) => {
                    const n = topoNodes.find(x => x.id === source)
                    if (n) { setSelectedNode(n); setView('graph') }
                  }}
                />
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {view === 'app' && (
        <div className="mt-4">
          <AppArchPanel defaultAppId={selectedCI} />
        </div>
      )}

      {/* 时间维度：两个基线快照之间的拓扑变更（新增/消失/变更三色） */}
      {view === 'diff' && <TopologyDiff />}

      {/* 连线建关系：选择关系类型后落库 */}
      <Modal
        open={!!connectPair}
        title="建立关系"
        onCancel={() => setConnectPair(null)}
        onOk={confirmConnect}
        confirmLoading={connecting}
        okText="建立"
        cancelText="取消"
      >
        <p className="text-sm text-muted-foreground mb-3">
          从 <span className="font-mono">{connectPair?.source}</span> 到 <span className="font-mono">{connectPair?.target}</span>
        </p>
        <p className="text-xs text-muted-foreground mb-1">关系类型</p>
        <AutoComplete
          className="w-full"
          value={relType}
          onChange={setRelType}
          placeholder="选择或输入关系类型"
          options={Object.entries(RELATION_LABELS).map(([k, v]) => ({ value: k, label: `${v} (${k})` }))}
          filterOption={(input, option) => String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())}
        />
      </Modal>

      {/* P1 路径聚焦：选两个 CI，画布只保留其间最短依赖路径 */}
      <Modal
        open={pathModal}
        title="路径聚焦"
        onCancel={() => setPathModal(false)}
        onOk={runPathFocus}
        confirmLoading={pathLoading}
        okText="聚焦"
        cancelText="取消"
      >
        <p className="text-sm text-muted-foreground mb-3">
          选择两个 CI，画布只保留它们之间的最短依赖路径及其直连邻域，其余淡出。
        </p>
        <div className="space-y-3">
          <div>
            <p className="text-xs text-muted-foreground mb-1">起点</p>
            <Select
              className="w-full" showSearch optionFilterProp="label" placeholder="选择起点 CI"
              value={pathFrom || undefined} onChange={setPathFrom}
              options={items.map(c => ({ value: c.id, label: `${c.name} (${c.type_id})` }))}
            />
          </div>
          <div>
            <p className="text-xs text-muted-foreground mb-1">终点</p>
            <Select
              className="w-full" showSearch optionFilterProp="label" placeholder="选择终点 CI"
              value={pathTo || undefined} onChange={setPathTo}
              options={items.map(c => ({ value: c.id, label: `${c.name} (${c.type_id})` }))}
            />
          </div>
        </div>
      </Modal>
    </div>
  )
}
