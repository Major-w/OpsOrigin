
/**
 * 轻量力导向关系图（CI 实例详情「拓扑」页签、模型关系图等小图场景）。
 *
 * 渲染与交互统一走 ForceGraph（d3 算坐标 + React Flow 渲染），本文件只做数据投影：
 * 节点按 CI/模型类型着色，边标注关系类型。此前这里的原生 SVG 实现每个节点都往 window
 * 挂 mousemove/mouseup 且不移除，节点越多泄漏越严重，改由 React Flow 托管后消失。
 */

import { useMemo } from 'react'
import ForceGraph, { type ForceGraphEdge, type ForceGraphNode } from '@/components/graph/force-graph'
import { nodeTypeColor } from '@/lib/graph-colors'

export interface TopologyGraphNode {
  id: string
  name: string
  type: string
}

export interface TopologyGraphEdge {
  source: string
  target: string
  type: string
}

export interface TopologyGraphData {
  nodes: TopologyGraphNode[]
  edges: TopologyGraphEdge[]
}

interface Props {
  data: TopologyGraphData
  /** 兼容旧签名：ForceGraph 自适应容器宽度，此值仅作最小高度参考 */
  width?: number
  height?: number
  onNodeClick?: (id: string) => void
}

export default function TopologyGraph({ data, height = 320, onNodeClick }: Props) {
  const fgNodes = useMemo<ForceGraphNode[]>(
    () => (data.nodes ?? []).map(n => ({
      id: n.id,
      label: n.name,
      color: nodeTypeColor(n.type),
      sub: n.type,
      title: `${n.name}\n类型: ${n.type}`,
    })),
    [data.nodes],
  )

  const fgEdges = useMemo<ForceGraphEdge[]>(() => {
    const idSet = new Set((data.nodes ?? []).map(n => n.id))
    return (data.edges ?? [])
      .filter(e => e.source && e.target && idSet.has(e.source) && idSet.has(e.target))
      .map((e, i) => ({
        id: `t-${i}-${e.source}-${e.target}`,
        source: e.source,
        target: e.target,
        label: e.type,
      }))
  }, [data.nodes, data.edges])

  return (
    <ForceGraph
      nodes={fgNodes}
      edges={fgEdges}
      height={height}
      onNodeClick={onNodeClick}
      showMiniMap={false}
      emptyText="无拓扑数据"
    />
  )
}
