
/**
 * 全景可观测拓扑图。
 *
 * 渲染交给通用 ForceGraph（d3 算坐标 + React Flow 渲染与交互），本文件只负责
 * 把后端 PanoramaResponse 投影成图元：节点按健康度着色，边按「每条边自己的 layer」
 * 决定线型（结构实线 / 逻辑虚线 / 调用链青色带 QPS·延迟·错误率）。
 */

import { useMemo } from 'react'
import ForceGraph, { type ForceGraphEdge, type ForceGraphNode } from '@/components/graph/force-graph'
import { HEALTH_COLOR, panoramaLayerStyle } from '@/lib/graph-colors'

// 全景图节点（对齐后端 PanoramaNode 投影）。
export interface PanoramaGraphNode {
  id: string
  name: string
  type: string
  health: string // critical | warning | healthy | unknown
  status?: string
  alertSummary?: { critical: number; warning: number; info: number; rootCause: boolean }
  metrics?: Record<string, any>
}

// 全景图边（对齐后端 PanoramaEdge 投影）。
export interface PanoramaGraphEdge {
  source: string
  target: string
  layer: string // cmdb-structural | cmdb-logical | callchain
  relationType: string
  health?: string
  latencyMs?: number
  errorRate?: number
  qps?: number
}

interface Props {
  nodes: PanoramaGraphNode[]
  edges: PanoramaGraphEdge[]
  onNodeClick?: (id: string) => void
}

/** 节点 tooltip：名称 + 类型 + 健康度 + 告警 + 关键指标。 */
function nodeTitle(n: PanoramaGraphNode): string {
  const a = n.alertSummary
  let tip = `${n.name}\n类型: ${n.type}\n健康度: ${n.health}`
  if (n.status) tip += `\n状态: ${n.status}`
  if (a) tip += `\n告警: critical ${a.critical} / warning ${a.warning}${a.rootCause ? ' / 根因' : ''}`
  if (n.metrics) {
    for (const k of Object.keys(n.metrics).slice(0, 4)) tip += `\n${k}: ${n.metrics[k]}`
  }
  return tip
}

/** 边 tooltip：调用链边额外带 QPS / 延迟 / 错误率。 */
function edgeLabel(e: PanoramaGraphEdge): string {
  if (e.layer !== 'callchain') return e.relationType
  const parts = [e.relationType]
  if (typeof e.qps === 'number') parts.push(`${e.qps.toFixed(1)} qps`)
  if (typeof e.latencyMs === 'number') parts.push(`${e.latencyMs.toFixed(0)} ms`)
  if (typeof e.errorRate === 'number') parts.push(`err ${(e.errorRate * 100).toFixed(1)}%`)
  return parts.join(' · ')
}

export default function PanoramaGraph({ nodes, edges, onNodeClick }: Props) {
  const fgNodes = useMemo<ForceGraphNode[]>(() => nodes.map(n => {
    const a = n.alertSummary
    const total = a ? a.critical + a.warning : 0
    return {
      id: n.id,
      label: n.name,
      color: HEALTH_COLOR[n.health] || HEALTH_COLOR.unknown,
      sub: n.type,
      badge: total > 0 ? String(total) : undefined,
      title: nodeTitle(n),
    }
  }), [nodes])

  const fgEdges = useMemo<ForceGraphEdge[]>(() => edges
    .filter(e => e.source && e.target && e.source !== e.target)
    .map((e, i) => {
      // 关键：按当前这条边的 layer 取样式（历史 bug 是所有边复用第一条边的 layer）
      const st = panoramaLayerStyle(e.layer)
      return {
        id: `p-${i}-${e.source}-${e.target}`,
        source: e.source,
        target: e.target,
        color: st.color,
        dashed: st.dashed,
        width: st.width,
        label: edgeLabel(e),
      }
    }), [edges])

  return (
    <ForceGraph
      nodes={fgNodes}
      edges={fgEdges}
      onNodeClick={onNodeClick}
      emptyText="无全景数据"
    />
  )
}
