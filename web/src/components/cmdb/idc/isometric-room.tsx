import { useMemo, useState } from 'react'
import { cn } from '@/lib/utils'

// ============================================================================
// 机房等距（isometric）立体视图
// 纯 SVG 绘制：地板 + 两面围墙 + 机柜立体盒子（三个可见面），无需 3D 引擎。
// 机柜按告警/容量着色，点击选中后在柜顶浮出统计卡。
// ============================================================================

export interface IsoRack {
  id: string
  name: string
  maxUnits: number
  usedUnits: number
  critical: number
  warning: number
  deviceCount: number
  row: number
  col: number
  online: boolean
}

interface Props {
  racks: IsoRack[]
  selectedId?: string
  onSelect?: (id: string) => void
  className?: string
}

// 等距投影参数（半个格子的屏幕位移）
const UX = 40
const UY = 21
const RACK_H = 46 // 机柜高度（像素，向上抬升）
const HALF_W = 0.33 // 沿 u 方向的半宽（格）
const HALF_D = 0.33 // 沿 v 方向的半深（格）

function floorPt(x: number, y: number, lift = 0) {
  return { X: (x - y) * UX, Y: (x + y) * UY - lift }
}

function poly(pts: { X: number; Y: number }[]) {
  return pts.map(p => `${p.X.toFixed(1)},${p.Y.toFixed(1)}`).join(' ')
}

/** 机柜配色：告警优先（紧急红 / 告警橙），其次按容量，空闲为深灰蓝 */
function rackTone(r: IsoRack) {
  if (r.critical > 0) return { face: '#7f1d1d', side: '#450a0a', top: '#b91c1c', edge: '#f87171' }
  if (r.warning > 0) return { face: '#78350f', side: '#431407', top: '#b45309', edge: '#fbbf24' }
  if (!r.online) return { face: '#1e293b', side: '#0f172a', top: '#334155', edge: '#64748b' }
  return { face: '#1f2b3d', side: '#131c2b', top: '#334155', edge: '#7dd3fc' }
}

export function IsometricRoom({ racks, selectedId, onSelect, className }: Props) {
  const [hover, setHover] = useState<string | null>(null)

  const layout = useMemo(() => {
    if (!racks.length) return null
    const maxRow = Math.max(...racks.map(r => r.row))
    const maxCol = Math.max(...racks.map(r => r.col))
    // 四角包围盒：等距投影后在 4 个极值点取范围
    const corners = [
      floorPt(-0.5, -0.5), floorPt(maxCol + 0.5, -0.5),
      floorPt(-0.5, maxRow + 0.5), floorPt(maxCol + 0.5, maxRow + 0.5),
    ]
    const xs = corners.map(p => p.X), ys = corners.map(p => p.Y)
    const minX = Math.min(...xs) - 24
    const maxX = Math.max(...xs) + 24
    const minY = Math.min(...ys) - RACK_H - 44 // 留出机柜高度与浮层空间
    const maxY = Math.max(...ys) + 30
    return { minX, minY, w: maxX - minX, h: maxY - minY, maxRow, maxCol }
  }, [racks])

  if (!layout || !racks.length) {
    return <div className={cn('flex h-full min-h-[320px] items-center justify-center text-xs text-muted-foreground', className)}>暂无机柜数据</div>
  }

  const { minX, minY, w, h, maxRow, maxCol } = layout
  const viewBox = `${minX} ${minY} ${w} ${h}`
  const selected = racks.find(r => r.id === selectedId)

  return (
    <div className={cn('relative h-full min-h-[360px] w-full overflow-hidden rounded-md bg-gradient-to-b from-slate-900/95 to-slate-950', className)}>
      <svg viewBox={viewBox} className="h-full w-full" style={{ display: 'block' }}>
        <defs>
          <linearGradient id="isoFloor" x1="0" y1="0" x2="0.6" y2="1">
            <stop offset="0%" stopColor="#cbd5e1" stopOpacity="0.95" />
            <stop offset="100%" stopColor="#94a3b8" stopOpacity="0.75" />
          </linearGradient>
          <linearGradient id="isoWallL" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#e2e8f0" />
            <stop offset="100%" stopColor="#b8c4d4" />
          </linearGradient>
          <linearGradient id="isoWallR" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#f1f5f9" />
            <stop offset="100%" stopColor="#c3cddb" />
          </linearGradient>
          <filter id="isoGlow" x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="4" result="b" />
            <feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
          </filter>
        </defs>

        {/* 地板 */}
        <polygon
          points={poly([floorPt(-0.5, -0.5), floorPt(maxCol + 0.5, -0.5), floorPt(maxCol + 0.5, maxRow + 0.5), floorPt(-0.5, maxRow + 0.5)])}
          fill="url(#isoFloor)" stroke="#64748b" strokeWidth="1"
        />
        {/* 地砖网格 */}
        {Array.from({ length: maxCol + 2 }).map((_, i) => (
          <line key={`gx${i}`} {...lineBetween(floorPt(i - 0.5, -0.5), floorPt(i - 0.5, maxRow + 0.5))} stroke="#94a3b8" strokeOpacity="0.35" strokeWidth="0.6" />
        ))}
        {Array.from({ length: maxRow + 2 }).map((_, i) => (
          <line key={`gy${i}`} {...lineBetween(floorPt(-0.5, i - 0.5), floorPt(maxCol + 0.5, i - 0.5))} stroke="#94a3b8" strokeOpacity="0.35" strokeWidth="0.6" />
        ))}

        {/* 左后 / 右后 两面墙 */}
        <polygon
          points={poly([floorPt(-0.5, -0.5, 0), floorPt(maxCol + 0.5, -0.5, 0), floorPt(maxCol + 0.5, -0.5, 52), floorPt(-0.5, -0.5, 52)])}
          fill="url(#isoWallR)" stroke="#64748b" strokeWidth="0.8"
        />
        <polygon
          points={poly([floorPt(-0.5, -0.5, 0), floorPt(-0.5, maxRow + 0.5, 0), floorPt(-0.5, maxRow + 0.5, 52), floorPt(-0.5, -0.5, 52)])}
          fill="url(#isoWallL)" stroke="#64748b" strokeWidth="0.8"
        />

        {/* 机柜：按 row+col 排序保证遮挡关系正确（远的先画） */}
        {[...racks].sort((a, b) => (a.row + a.col) - (b.row + b.col)).map(r => {
          const tone = rackTone(r)
          const isSel = r.id === selectedId
          const isHover = r.id === hover
          const base = {
            a: floorPt(r.col - HALF_W, r.row - HALF_D),
            b: floorPt(r.col + HALF_W, r.row - HALF_D),
            c: floorPt(r.col + HALF_W, r.row + HALF_D),
            d: floorPt(r.col - HALF_W, r.row + HALF_D),
          }
          const top = {
            a: floorPt(r.col - HALF_W, r.row - HALF_D, RACK_H),
            b: floorPt(r.col + HALF_W, r.row - HALF_D, RACK_H),
            c: floorPt(r.col + HALF_W, r.row + HALF_D, RACK_H),
            d: floorPt(r.col - HALF_W, r.row + HALF_D, RACK_H),
          }
          const usage = r.maxUnits > 0 ? Math.min(100, Math.round((r.usedUnits / r.maxUnits) * 100)) : 0
          return (
            <g
              key={r.id}
              style={{ cursor: 'pointer' }}
              onMouseEnter={() => setHover(r.id)}
              onMouseLeave={() => setHover(h => (h === r.id ? null : h))}
              onClick={() => onSelect?.(r.id)}
              filter={isSel ? 'url(#isoGlow)' : undefined}
            >
              {/* 右侧面（正对观察者） */}
              <polygon points={poly([base.c, base.d, top.d, top.c])} fill={tone.face} stroke={isSel ? '#22d3ee' : tone.edge} strokeWidth={isSel ? 1.6 : 0.5} strokeOpacity={isSel ? 1 : 0.35} />
              {/* 左侧面 */}
              <polygon points={poly([base.d, base.a, top.a, top.d])} fill={tone.side} stroke={isSel ? '#22d3ee' : tone.edge} strokeWidth={isSel ? 1.6 : 0.5} strokeOpacity={isSel ? 1 : 0.3} />
              {/* 顶面 */}
              <polygon points={poly([top.a, top.b, top.c, top.d])} fill={tone.top} stroke={isSel ? '#22d3ee' : tone.edge} strokeWidth={isSel ? 1.6 : 0.5} strokeOpacity={isSel ? 1 : 0.4} />
              {/* 正面 U 位刻度条纹 + 占用条 */}
              <line {...lineBetween(floorPt(r.col + HALF_W, r.row + HALF_D, RACK_H * (1 - usage / 100)), floorPt(r.col - HALF_W, r.row + HALF_D, RACK_H * (1 - usage / 100)))} stroke="#38bdf8" strokeOpacity="0.85" strokeWidth="2" />
              {[0.25, 0.5, 0.75].map(k => (
                <line key={k} {...lineBetween(floorPt(r.col + HALF_W, r.row + HALF_D, RACK_H * k), floorPt(r.col - HALF_W, r.row + HALF_D, RACK_H * k))} stroke="#94a3b8" strokeOpacity="0.18" strokeWidth="0.6" />
              ))}
              {/* 告警指示灯 */}
              {(r.critical > 0 || r.warning > 0) && (
                <circle cx={top.c.X} cy={top.c.Y - 4} r="3" fill={r.critical > 0 ? '#ef4444' : '#f59e0b'}>
                  <animate attributeName="opacity" values="1;0.25;1" dur="1.6s" repeatCount="indefinite" />
                </circle>
              )}
              {/* hover 机柜名 */}
              {isHover && !isSel && (
                <text x={top.a.X + 6} y={top.a.Y - 6} fontSize="9" fill="#e2e8f0" textAnchor="middle">{r.name}</text>
              )}
            </g>
          )
        })}
      </svg>

      {/* 选中机柜的浮层统计卡（贴右上，带引线到柜顶） */}
      {selected && (
        <div className="pointer-events-none absolute right-4 top-4 w-[190px] rounded-lg border border-cyan-400/40 bg-slate-950/85 p-2.5 backdrop-blur">
          <div className="flex items-center justify-between">
            <span className="text-[12px] font-semibold text-cyan-300">{selected.name}</span>
            <span className="text-[10px] text-slate-400">U {selected.usedUnits}/{selected.maxUnits}</span>
          </div>
          <div className="mt-2 space-y-1 text-[11px]">
            {[
              { k: '紧急', v: selected.critical, c: '#ef4444' },
              { k: '告警', v: selected.warning, c: '#f59e0b' },
              { k: '设备', v: selected.deviceCount, c: '#38bdf8' },
            ].map(it => (
              <div key={it.k} className="flex items-center justify-between">
                <span className="text-slate-300">{it.k}</span>
                <span className="font-medium tabular-nums" style={{ color: it.v > 0 ? it.c : '#94a3b8' }}>{it.v}</span>
              </div>
            ))}
          </div>
          <div className="mt-2 h-1.5 overflow-hidden rounded bg-white/10">
            <div className="h-full rounded" style={{ width: `${selected.maxUnits ? Math.round(selected.usedUnits / selected.maxUnits * 100) : 0}%`, background: 'linear-gradient(90deg,#22d3ee,#38bdf8)' }} />
          </div>
        </div>
      )}
    </div>
  )
}

function lineBetween(p: { X: number; Y: number }, q: { X: number; Y: number }) {
  return { x1: p.X, y1: p.Y, x2: q.X, y2: q.Y }
}
