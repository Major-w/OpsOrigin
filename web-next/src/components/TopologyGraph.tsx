'use client'

import { useEffect, useRef } from 'react'
import { forceSimulation, forceLink, forceManyBody, forceCenter } from 'd3-force'

interface Node {
  id: string
  name: string
  type: string
}

interface Edge {
  source: string
  target: string
  type: string
}

interface TopologyData {
  nodes: Node[]
  edges: Edge[]
}

interface Props {
  data: TopologyData
  width?: number
  height?: number
}

export default function TopologyGraph({ data, width = 600, height = 400 }: Props) {
  const svgRef = useRef<SVGSVGElement>(null)

  useEffect(() => {
    if (!svgRef.current || !data.nodes.length) return

    const svg = svgRef.current
    const w = svg.clientWidth || width
    const h = svg.clientHeight || height

    const simulation = forceSimulation(data.nodes as any)
      .force('link', forceLink(data.edges).id((d: any) => d.id).distance(120))
      .force('charge', forceManyBody().strength(-300))
      .force('center', forceCenter(w / 2, h / 2))

    const links: SVGLineElement[] = []
    const groups: SVGGElement[] = []

    data.edges.forEach(() => {
      const line = document.createElementNS('http://www.w3.org/2000/svg', 'line')
      line.setAttribute('stroke', 'rgba(148,163,184,0.2)')
      line.setAttribute('stroke-width', '1.5')
      svg.appendChild(line)
      links.push(line)
    })

    data.nodes.forEach((n) => {
      const g = document.createElementNS('http://www.w3.org/2000/svg', 'g')
      g.style.cursor = 'grab'

      const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
      circle.setAttribute('r', '8')
      circle.setAttribute('fill', n.type === 'service' ? '#34d399' : n.type === 'host' ? '#6366f1' : '#f59e0b')
      circle.setAttribute('stroke', 'rgba(255,255,255,0.1)')
      circle.setAttribute('stroke-width', '2')
      g.appendChild(circle)

      const text = document.createElementNS('http://www.w3.org/2000/svg', 'text')
      text.setAttribute('x', '14')
      text.setAttribute('y', '4')
      text.setAttribute('fill', '#94A3B8')
      text.setAttribute('font-size', '11')
      text.setAttribute('font-family', 'JetBrains Mono, monospace')
      text.textContent = n.name
      g.appendChild(text)

      svg.appendChild(g)
      groups.push(g)

      let dragging = false
      let dx = 0, dy = 0

      g.addEventListener('mousedown', (e) => {
        dragging = true
        const rect = svg.getBoundingClientRect()
        dx = e.clientX - rect.left - (n as any).x
        dy = e.clientY - rect.top - (n as any).y
        g.style.cursor = 'grabbing'
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
        g.style.cursor = 'grab'
        simulation.alphaTarget(0)
        ;(n as any).fx = null
        ;(n as any).fy = null
      })
    })

    simulation.on('tick', () => {
      links.forEach((line, i) => {
        const d = data.edges[i] as any
        if (d.source && d.target) {
          line.setAttribute('x1', d.source.x)
          line.setAttribute('y1', d.source.y)
          line.setAttribute('x2', d.target.x)
          line.setAttribute('y2', d.target.y)
        }
      })
      groups.forEach((g, i) => {
        const d = data.nodes[i] as any
        g.setAttribute('transform', `translate(${d.x},${d.y})`)
      })
    })

    return () => { simulation.stop() }
  }, [data, width, height])

  return <svg ref={svgRef} width={width} height={height} className="w-full h-full" />
}
