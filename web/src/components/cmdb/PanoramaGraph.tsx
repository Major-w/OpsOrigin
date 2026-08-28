
import { useEffect, useRef } from 'react'
import { forceSimulation, forceLink, forceManyBody, forceCenter } from 'd3-force'

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
  errorRate?: number
}

interface Props {
  nodes: PanoramaGraphNode[]
  edges: PanoramaGraphEdge[]
  width?: number
  height?: number
  onNodeClick?: (id: string) => void
}

// 健康度 → 颜色（critical 红 / warning 琥珀 / healthy 绿 / unknown 灰）。
const HEALTH_COLOR: Record<string, string> = {
  critical: '#ef4444',
  warning: '#f59e0b',
  healthy: '#3B82F6',
  unknown: '#94a3b8',
}

// 层级 → 边样式（纵向部署骨架实线 / 纵向逻辑依赖虚线 / 横向调用链青色）。
const LAYER_STYLE: Record<string, { stroke: string; dash: string; width: number }> = {
  'cmdb-structural': { stroke: 'rgba(148,163,184,0.55)', dash: '', width: 2 },
  'cmdb-logical': { stroke: 'rgba(99,102,241,0.6)', dash: '6 4', width: 1.8 },
  'callchain': { stroke: 'rgba(6,182,212,0.75)', dash: '', width: 1.8 },
}

export default function PanoramaGraph({ nodes, edges, width = 600, height = 400, onNodeClick }: Props) {
  const svgRef = useRef<SVGSVGElement>(null)

  useEffect(() => {
    if (!svgRef.current || nodes.length === 0) return

    const svg = svgRef.current
    const w = svg.clientWidth || width
    const h = svg.clientHeight || height

    // 复制节点供 d3 force 写入 x/y，避免污染外部状态。
    const simNodes = nodes.map(n => ({ ...n }))
    const simLinks = edges.map(e => ({ ...e }))

    const simulation = forceSimulation(simNodes as any)
      .force('link', forceLink(simLinks as any).id((d: any) => d.id).distance(140))
      .force('charge', forceManyBody().strength(-420))
      .force('center', forceCenter(w / 2, h / 2))

    const linkEls: SVGLineElement[] = []
    const nodeEls: SVGGElement[] = []

    simLinks.forEach(() => {
      const line = document.createElementNS('http://www.w3.org/2000/svg', 'line')
      const st = LAYER_STYLE[(simLinks as any)[0]?.layer] || LAYER_STYLE['cmdb-logical']
      line.setAttribute('stroke', st.stroke)
      line.setAttribute('stroke-width', String(st.width))
      if (st.dash) line.setAttribute('stroke-dasharray', st.dash)
      line.setAttribute('opacity', '0.7')
      svg.appendChild(line)
      linkEls.push(line)
    })

    simNodes.forEach((n) => {
      const g = document.createElementNS('http://www.w3.org/2000/svg', 'g')
      g.style.cursor = 'pointer'

      const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
      const hc = HEALTH_COLOR[(n as any).health] || HEALTH_COLOR.unknown
      circle.setAttribute('r', '9')
      circle.setAttribute('fill', hc)
      circle.setAttribute('stroke', 'rgba(255,255,255,0.85)')
      circle.setAttribute('stroke-width', '2')
      g.appendChild(circle)

      const text = document.createElementNS('http://www.w3.org/2000/svg', 'text')
      text.setAttribute('x', '14')
      text.setAttribute('y', '4')
      text.setAttribute('fill', 'currentColor')
      text.setAttribute('opacity', '0.8')
      text.setAttribute('font-size', '11')
      text.setAttribute('font-family', 'JetBrains Mono, monospace')
      text.textContent = (n as any).name
      g.appendChild(text)

      // 原生 tooltip：名称 + 健康度 + 告警 + 关键指标。
      const title = document.createElementNS('http://www.w3.org/2000/svg', 'title')
      const a = (n as any).alertSummary
      const m = (n as any).metrics
      let tip = `${(n as any).name}\n类型: ${(n as any).type}\n健康度: ${(n as any).health}`
      if (a) tip += `\n告警: critical ${a.critical} / warning ${a.warning}${a.rootCause ? ' / 根因' : ''}`
      if (m) {
        const keys = Object.keys(m).slice(0, 4)
        keys.forEach(k => { tip += `\n${k}: ${m[k]}` })
      }
      title.textContent = tip
      g.appendChild(title)

      if (onNodeClick) {
        g.addEventListener('click', () => onNodeClick((n as any).id))
      }

      svg.appendChild(g)
      nodeEls.push(g)

      // 拖拽。
      let dragging = false
      let dx = 0, dy = 0
      g.addEventListener('mousedown', (e) => {
        dragging = true
        const rect = svg.getBoundingClientRect()
        dx = e.clientX - rect.left - (n as any).x
        dy = e.clientY - rect.top - (n as any).y
        if (!(e as any).active) simulation.alphaTarget(0.3).restart()
        ;(n as any).fx = (n as any).x
        ;(n as any).fy = (n as any).y
      })
      window.addEventListener('mousemove', (e) => {
        if (!dragging) return
        const rect = svg.getBoundingClientRect()
        ;(n as any).fx = e.clientX - rect.left - dx
        ;(n as any).fy = e.clientY - rect.top - dy
      })
      window.addEventListener('mouseup', () => {
        if (!dragging) return
        dragging = false
        simulation.alphaTarget(0)
        ;(n as any).fx = null
        ;(n as any).fy = null
      })
    })

    simulation.on('tick', () => {
      linkEls.forEach((line, i) => {
        const d = simLinks[i] as any
        if (d.source && d.target) {
          line.setAttribute('x1', d.source.x)
          line.setAttribute('y1', d.source.y)
          line.setAttribute('x2', d.target.x)
          line.setAttribute('y2', d.target.y)
        }
      })
      nodeEls.forEach((g, i) => {
        const d = simNodes[i] as any
        g.setAttribute('transform', `translate(${d.x},${d.y})`)
      })
    })

    return () => { simulation.stop() }
  }, [nodes, edges, width, height, onNodeClick])

  return <svg ref={svgRef} width={width} height={height} className="w-full h-full" />
}
