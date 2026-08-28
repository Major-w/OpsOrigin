
import { useEffect, useRef, useState, useCallback } from 'react'
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

const MIN_ZOOM = 0.2
const MAX_ZOOM = 3

export default function TopologyGraph({ data, width = 600, height = 400 }: Props) {
  const svgRef = useRef<SVGSVGElement>(null)
  const contentRef = useRef<SVGGElement>(null)
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  // 用 ref 镜像 zoom/pan, 供事件回调读取实时值(避免闭包陈旧)
  const viewRef = useRef({ zoom: 1, pan: { x: 0, y: 0 } })
  viewRef.current = { zoom, pan }

  const zoomAt = useCallback((nextZoom: number, cx: number, cy: number) => {
    const z = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, nextZoom))
    setZoom(prev => {
      const old = prev
      // 以鼠标位置为锚点: 保持鼠标下的图坐标不动
      setPan(p => ({
        x: cx - (cx - p.x) * (z / old),
        y: cy - (cy - p.y) * (z / old),
      }))
      return z
    })
  }, [])

  // 空白拖拽平移
  const onBgMouseDown = (e: React.MouseEvent) => {
    // 只在点击空白处(非节点)时平移
    if ((e.target as Element).tagName === 'circle' || (e.target as Element).tagName === 'text') return
    const startX = e.clientX - viewRef.current.pan.x
    const startY = e.clientY - viewRef.current.pan.y
    const onMove = (ev: MouseEvent) => {
      setPan({ x: ev.clientX - startX, y: ev.clientY - startY })
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }

  useEffect(() => {
    if (!svgRef.current || !contentRef.current || !data.nodes.length) return

    const svg = svgRef.current
    const content = contentRef.current
    // 清理上一轮(或重渲染)挂载的节点/连线, 避免重复累积
    while (content.firstChild) content.removeChild(content.firstChild)

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
      content.appendChild(line)
      links.push(line)
    })

    data.nodes.forEach((n) => {
      const g = document.createElementNS('http://www.w3.org/2000/svg', 'g')
      g.style.cursor = 'grab'

      const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
      circle.setAttribute('r', '8')
      circle.setAttribute('fill', n.type === 'service' ? '#60A5FA' : n.type === 'host' ? '#6366f1' : '#f59e0b')
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

      content.appendChild(g)
      groups.push(g)

      let dragging = false
      let dx = 0, dy = 0

      g.addEventListener('mousedown', (e) => {
        dragging = true
        const rect = svg.getBoundingClientRect()
        const z = viewRef.current.zoom
        // 图坐标 = (screen - pan) / zoom
        const gx = (e.clientX - rect.left - viewRef.current.pan.x) / z
        const gy = (e.clientY - rect.top - viewRef.current.pan.y) / z
        dx = gx - (n as any).x
        dy = gy - (n as any).y
        g.style.cursor = 'grabbing'
        if (!(e as any).active) simulation.alphaTarget(0.3).restart()
        ;(n as any).fx = (n as any).x
        ;(n as any).fy = (n as any).y
        e.stopPropagation()
      })

      window.addEventListener('mousemove', (e) => {
        if (!dragging) return
        const rect = svg.getBoundingClientRect()
        const z = viewRef.current.zoom
        ;(n as any).fx = (e.clientX - rect.left - viewRef.current.pan.x) / z - dx
        ;(n as any).fy = (e.clientY - rect.top - viewRef.current.pan.y) / z - dy
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

  const resetView = () => { setZoom(1); setPan({ x: 0, y: 0 }) }

  return (
    <div className="relative w-full h-full">
      <div className="absolute right-2 top-2 z-10 flex items-center gap-1 rounded-md border border-border bg-card/90 p-1 backdrop-blur">
        <button
          type="button"
          title="缩小"
          className="h-6 w-6 rounded text-xs leading-none hover:bg-muted"
          onClick={() => {
            const svg = svgRef.current
            if (!svg) return
            const rect = svg.getBoundingClientRect()
            zoomAt(viewRef.current.zoom - 0.2, rect.width / 2, rect.height / 2)
          }}
        >－</button>
        <span className="w-10 text-center text-[11px] text-muted-foreground">{Math.round(zoom * 100)}%</span>
        <button
          type="button"
          title="放大"
          className="h-6 w-6 rounded text-xs leading-none hover:bg-muted"
          onClick={() => {
            const svg = svgRef.current
            if (!svg) return
            const rect = svg.getBoundingClientRect()
            zoomAt(viewRef.current.zoom + 0.2, rect.width / 2, rect.height / 2)
          }}
        >＋</button>
        <button
          type="button"
          title="重置视图"
          className="ml-1 h-6 rounded px-2 text-[11px] hover:bg-muted"
          onClick={resetView}
        >重置</button>
      </div>
      <svg
        ref={svgRef}
        width={width}
        height={height}
        className="w-full h-full cursor-grab active:cursor-grabbing"
        style={{ touchAction: 'none' }}
        onMouseDown={onBgMouseDown}
        onWheel={(e) => {
          e.preventDefault()
          const rect = svgRef.current!.getBoundingClientRect()
          const cx = e.clientX - rect.left
          const cy = e.clientY - rect.top
          const delta = e.deltaY < 0 ? 1.1 : 1 / 1.1
          zoomAt(viewRef.current.zoom * delta, cx, cy)
        }}
      >
        <g ref={contentRef} transform={`translate(${pan.x},${pan.y}) scale(${zoom})`} />
      </svg>
    </div>
  )
}
