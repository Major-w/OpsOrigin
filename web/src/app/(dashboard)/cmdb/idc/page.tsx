
import { useEffect, useMemo, useState, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Select, Popconfirm } from 'antd'
import { UniversalForm } from '@/components/universal-form'
import { apiFetch, apiList } from '@/lib/utils'
import { Plus, Trash2, Building2, Server, RefreshCw, Rows3, Unplug, Gauge, ArrowLeftRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { getMessage } from '@/lib/message'

interface IDCRoom { id: string; name: string; location?: string; description?: string }
interface Rack { id: string; idc_id: string; name: string; row?: string; col?: string; max_units: number }
interface RackUnit { rack_id: string; unit: number; ci_id?: string; label?: string }
interface CIModel { id: string; name: string; type_id?: string; status?: string }

const DEVICE_TYPES = ['server', 'host', 'storage', 'firewall', 'load_balancer', 'router', 'switch', 'database', 'middleware', 'app']

// 连续 U 位推荐：返回满足设备高度的可用起始 U 位（升序），对齐业界 findAvailablePositions。
function findAvailablePositions(deviceUnit: number, maxUnits: number, occupied: Set<number>): number[] {
  const res: number[] = []
  let run = 0
  for (let pos = 1; pos <= maxUnits; pos++) {
    if (occupied.has(pos)) { run = 0; continue }
    run++
    if (run >= deviceUnit) res.push(pos - deviceUnit + 1)
  }
  return res
}

// ============ SVG 俯瞰平面图组件（d3-zoom 缩放/平移） ============
function FloorPlanSVG({ rows, cols, racks, rackDevices, devHeight, onRackClick }: {
  rows: string[]; cols: string[]; racks: any[];
  rackDevices: (id: string) => any[]; devHeight: (d: any) => number;
  onRackClick: (rack: any) => void;
}) {
  const svgRef = useRef<SVGSVGElement>(null)
  const gRef = useRef<SVGGElement>(null)

  // 布局常量
  const RACK_W = 64, RACK_H = 48, GAP = 8, HEADER = 32, LABEL_W = 40, PADDING = 20
  const svgW = cols.length * (RACK_W + GAP) + LABEL_W + PADDING * 2
  const svgH = rows.length * (RACK_H + GAP) + HEADER + PADDING * 2

  // 初始化 d3-zoom
  useEffect(() => {
    if (!svgRef.current || !gRef.current) return
    let cleanup: (() => void) | undefined
    import('d3-zoom').then(d3Zoom => {
      import('d3-selection').then(d3Selection => {
        const svg = d3Selection.select(svgRef.current!)
        const g = d3Selection.select(gRef.current!)
        const zoom = d3Zoom.zoom<SVGSVGElement, unknown>()
          .scaleExtent([0.3, 5])
          .on('zoom', (event) => {
            g.attr('transform', event.transform)
          })
        svg.call(zoom)
        // 初始适配
        const parent = svgRef.current!.parentElement
        if (parent) {
          const pw = parent.clientWidth
          const ph = Math.max(400, parent.clientHeight || 500)
          const sx = (pw - 40) / svgW, sy = (ph - 40) / svgH
          const s = Math.min(sx, sy, 1.5)
          const tx = (pw - svgW * s) / 2, ty = (ph - svgH * s) / 2
          svg.call(zoom.transform, d3Zoom.zoomIdentity.translate(tx, ty).scale(s))
        }
        cleanup = () => { svg.on('.zoom', null) }
      })
    })
    return () => { cleanup?.() }
  }, [rows.length, cols.length])

  // 计算机柜位置
  const rackMap = useMemo(() => {
    const m = new Map<string, any>()
    racks.forEach(rk => { if (rk.attributes?.row && rk.attributes?.col) m.set(`${rk.attributes.row}-${rk.attributes.col}`, rk) })
    return m
  }, [racks])

  return (
    <div className="w-full overflow-hidden bg-muted/30" style={{ minHeight: 400, cursor: 'grab' }}>
      <svg ref={svgRef} width="100%" height="100%" style={{ minHeight: 400, display: 'block' }}>
        <g ref={gRef}>
          {/* 房间背景 */}
          <rect x={PADDING} y={PADDING} width={cols.length * (RACK_W + GAP) + LABEL_W} height={rows.length * (RACK_H + GAP) + HEADER}
            rx={6} fill="var(--muted)" fillOpacity={0.3} stroke="var(--border)" strokeWidth={1.5} />
          {/* 网格线 */}
          {rows.map((_, ri) => (
            <line key={`hg-${ri}`} x1={PADDING} y1={PADDING + HEADER + ri * (RACK_H + GAP) - GAP / 2}
              x2={PADDING + cols.length * (RACK_W + GAP) + LABEL_W} y2={PADDING + HEADER + ri * (RACK_H + GAP) - GAP / 2}
              stroke="var(--border)" strokeWidth={0.5} strokeDasharray="4 4" opacity={0.4} />
          ))}
          {cols.map((_, ci) => (
            <line key={`vg-${ci}`} x1={PADDING + LABEL_W + ci * (RACK_W + GAP) - GAP / 2} y1={PADDING}
              x2={PADDING + LABEL_W + ci * (RACK_W + GAP) - GAP / 2} y2={PADDING + rows.length * (RACK_H + GAP) + HEADER}
              stroke="var(--border)" strokeWidth={0.5} strokeDasharray="4 4" opacity={0.4} />
          ))}
          {/* 列标题 */}
          {cols.map((col, ci) => (
            <text key={`ch-${ci}`} x={PADDING + LABEL_W + ci * (RACK_W + GAP) + RACK_W / 2} y={PADDING + HEADER - 8}
              textAnchor="middle" fontSize={10} fill="var(--muted-foreground)" fontWeight={600}>{col}</text>
          ))}
          {/* 行标签 */}
          {rows.map((row, ri) => (
            <text key={`rl-${ri}`} x={PADDING + LABEL_W - 8} y={PADDING + HEADER + ri * (RACK_H + GAP) + RACK_H / 2 + 4}
              textAnchor="end" fontSize={11} fill="var(--muted-foreground)" fontWeight={600}>{row}</text>
          ))}
          {/* 机柜 */}
          {rows.map((row, ri) =>
            cols.map((col, ci) => {
              const rk = rackMap.get(`${row}-${col}`)
              if (!rk) return null
              const devs = rackDevices(rk.id)
              const maxU = Number(rk.attributes?.max_units || 42)
              const usage = maxU > 0 ? Math.min(100, Math.round(devs.reduce((s: number, d: any) => s + devHeight(d), 0) / maxU * 100)) : 0
              const color = usage >= 80 ? '#dc2626' : usage > 0 ? '#d97706' : '#3b82f6'
              const x = PADDING + LABEL_W + ci * (RACK_W + GAP)
              const y = PADDING + HEADER + ri * (RACK_H + GAP)
              return (
                <g key={rk.id} onClick={() => onRackClick(rk)} style={{ cursor: 'pointer' }}>
                  <rect x={x} y={y} width={RACK_W} height={RACK_H} rx={4}
                    fill={color} fillOpacity={0.12} stroke={color} strokeWidth={1.5} />
                  {/* 使用率条 */}
                  <rect x={x + 4} y={y + RACK_H - 6} width={Math.max(0, (RACK_W - 8) * usage / 100)} height={4}
                    rx={2} fill={color} fillOpacity={0.6} />
                  {/* 机柜名 */}
                  <text x={x + RACK_W / 2} y={y + RACK_H / 2 - 2} textAnchor="middle" fontSize={10} fontWeight={600} fill={color}>
                    {rk.name || rk.id.slice(0, 6)}
                  </text>
                  {/* 设备数 + 使用率 */}
                  <text x={x + RACK_W / 2} y={y + RACK_H / 2 + 11} textAnchor="middle" fontSize={8} fill="var(--muted-foreground)">
                    {devs.length}台 · {usage}%
                  </text>
                  {/* hover 效果 */}
                  <rect x={x} y={y} width={RACK_W} height={RACK_H} rx={4} fill="transparent" className="hover:fill-white/10 dark:hover:fill-white/5 transition-colors" />
                </g>
              )
            })
          )}
        </g>
      </svg>
    </div>
  )
}

export default function CMDBIDCPage() {
  const navigate = useNavigate()
  const [rooms, setRooms] = useState<IDCRoom[]>([])
  const [selectedRoom, setSelectedRoom] = useState<IDCRoom | null>(null)
  const [racks, setRacks] = useState<Rack[]>([])
  const [selectedRack, setSelectedRack] = useState<Rack | null>(null)
  const [units, setUnits] = useState<Record<number, RackUnit>>({})
  const [cis, setCis] = useState<CIModel[]>([])
  // 2D 视图(模型驱动): rack CI + 设备按 rack_id/rack_unit 落位
  const [view2D, setView2D] = useState(false)
  const [panorama, setPanorama] = useState<{ racks: any[]; devices: any[] }>({ racks: [], devices: [] })
  const [panoRoom, setPanoRoom] = useState('')       // 选中的机房(idc_room CI)
  const [panoRack, setPanoRack] = useState<any>(null) // 选中的机柜(rack CI)
  const [panoFace, setPanoFace] = useState<'front' | 'rear'>('front') // 前后视图
  const [dragCI, setDragCI] = useState<{ id: string; name: string; type: string; height: number } | null>(null)
  const [recUnits, setRecUnits] = useState<number[]>([]) // 推荐可用连续 U 位(点击设备高亮)
  // 设备 hover 提示
  const [hoverDev, setHoverDev] = useState<{ dev: any; x: number; y: number } | null>(null)
  const [assigning, setAssigning] = useState(false)
  const loadPanorama = async () => {
    try {
      const data = await apiFetch<{ racks: any[]; devices: any[] }>('/cmdb/idc/panorama')
      setPanorama(data)
    } catch { setPanorama({ racks: [], devices: [] }) }
  }
  const rackDevices = (rackID: string) => panorama.devices.filter(d => d.attributes?.rack_id === rackID)
  const deviceColor = (t: string) =>
    t === 'server' || t === 'host' ? 'bg-sky-500' :
    t === 'database' || t === 'middleware' ? 'bg-violet-500' :
    t === 'storage' ? 'bg-amber-500' :
    t === 'firewall' || t === 'load_balancer' || t === 'router' || t === 'switch' ? 'bg-blue-500' : 'bg-slate-400'
  const roomRacks = () => panorama.racks.filter(r => !panoRoom || r.attributes?.room_id === panoRoom)
  const panoRooms = panorama.racks
    .map(r => r.attributes?.room_id).filter((v, i, a) => v && a.indexOf(v) === i)
  const roomName = (rid: string) => rooms.find(x => x.id === rid)?.name || rid.slice(0, 12)

  // 容量统计：总 U / 已占 U / 剩余 U / 占用率
  const panoStats = useMemo(() => {
    const rs = roomRacks()
    let totalU = 0, usedU = 0
    for (const rk of rs) totalU += Number(rk.attributes?.max_units || 0)
    for (const d of panorama.devices) usedU += Math.max(1, Number(d.attributes?.u_height || 1))
    return {
      rackCount: rs.length,
      devCount: panorama.devices.length,
      totalU, usedU, freeU: Math.max(0, totalU - usedU),
      usage: totalU > 0 ? Math.round(usedU / totalU * 100) : 0,
    }
  }, [panorama, panoRoom])

  // 未上架设备：全量 CI 中属于设备类型、且不在已落位集合里的
  const freeDevices = useMemo(() => {
    const racked = new Set(panorama.devices.map(d => d.id))
    return cis.filter(c => DEVICE_TYPES.includes(c.type_id || '') && !racked.has(c.id))
  }, [cis, panorama.devices])

  const [roomDialog, setRoomDialog] = useState(false)
  const [roomForm, setRoomForm] = useState({ name: '', location: '', description: '' })
  const [rackDialog, setRackDialog] = useState(false)
  const [rackForm, setRackForm] = useState({ name: '', row: '', col: '', max_units: 42 })
  const [unitDialog, setUnitDialog] = useState(false)
  const [unitForm, setUnitForm] = useState<{ unit: number; ci_id: string; label: string }>({ unit: 0, ci_id: '', label: '' })

  const loadRooms = async () => {
    try { setRooms(await apiList<IDCRoom>('/cmdb/idc')) } catch { setRooms([]) }
  }
  const loadRacks = async (idcId: string) => {
    try { setRacks(await apiList<Rack>(`/cmdb/idc/${idcId}/racks`)) } catch { setRacks([]) }
  }
  const loadUnits = async (rackId: string) => {
    try {
      const list = await apiList<RackUnit>(`/cmdb/racks/${rackId}/units`)
      const m: Record<number, RackUnit> = {}
      for (const u of list) m[u.unit] = u
      setUnits(m)
    } catch { setUnits({}) }
  }
  const loadCIs = async () => {
    try { setCis(await apiList<CIModel>('/cmdb/ci?limit=2000')) } catch { setCis([]) }
  }

  useEffect(() => { loadRooms(); loadCIs() }, [])

  const selectRoom = (room: IDCRoom | null) => {
    setSelectedRoom(room); setSelectedRack(null); setUnits({})
    if (room) loadRacks(room.id)
  }

  const selectRack = (rack: Rack | null) => {
    setSelectedRack(rack); setUnits({})
    if (rack) loadUnits(rack.id)
  }

  const createRoom = async () => {
    if (!roomForm.name.trim()) { getMessage().error('请输入机房名称'); return }
    try {
      await apiFetch('/cmdb/idc', { method: 'POST', body: JSON.stringify(roomForm) })
      getMessage().success('机房已创建'); setRoomDialog(false); setRoomForm({ name: '', location: '', description: '' }); loadRooms()
    } catch (e: any) { getMessage().error(e.message) }
  }

  // 删除确认（机房/机柜统一走 ConfirmDialog）
  const [deleteTarget, setDeleteTarget] = useState<{ kind: 'room'; item: IDCRoom } | { kind: 'rack'; item: Rack } | null>(null)
  const [deleteSubmitting, setDeleteSubmitting] = useState(false)

  const openDelete = (kind: 'room' | 'rack', item: IDCRoom | Rack) => setDeleteTarget({ kind, item: item as any })

  const confirmDelete = async () => {
    if (!deleteTarget) return
    const { kind, item } = deleteTarget
    setDeleteSubmitting(true)
    try {
      if (kind === 'room') {
        await apiFetch(`/cmdb/idc/${(item as IDCRoom).id}`, { method: 'DELETE' }); getMessage().success('已删除')
        if (selectedRoom?.id === (item as IDCRoom).id) selectRoom(null); loadRooms()
      } else {
        await apiFetch(`/cmdb/racks/${(item as Rack).id}`, { method: 'DELETE' }); getMessage().success('已删除')
        if (selectedRack?.id === (item as Rack).id) selectRack(null); loadRacks(selectedRoom!.id)
      }
      setDeleteTarget(null)
    } catch (e: any) { getMessage().error(e.message) } finally { setDeleteSubmitting(false) }
  }

  const createRack = async () => {
    if (!selectedRoom || !rackForm.name.trim()) { getMessage().error('请输入机柜编号'); return }
    try {
      await apiFetch(`/cmdb/idc/${selectedRoom.id}/racks`, { method: 'POST', body: JSON.stringify(rackForm) })
      getMessage().success('机柜已创建'); setRackDialog(false); setRackForm({ name: '', row: '', col: '', max_units: 42 }); loadRacks(selectedRoom.id)
    } catch (e: any) { getMessage().error(e.message) }
  }

  const openAssign = (unit: number) => {
    const cur = units[unit]
    setUnitForm({ unit, ci_id: cur?.ci_id || '', label: cur?.label || '' })
    setUnitDialog(true)
  }

  const assignUnit = async () => {
    if (!selectedRack) return
    try {
      await apiFetch(`/cmdb/racks/${selectedRack.id}/units`, {
        method: 'POST',
        body: JSON.stringify({ units: [{ unit: unitForm.unit, ci_id: unitForm.ci_id || undefined, label: unitForm.label || undefined }] }),
      })
      getMessage().success(`U 位 ${unitForm.unit} 已更新`); setUnitDialog(false); loadUnits(selectedRack.id); loadCIs()
    } catch (e: any) { getMessage().error(e.message) }
  }

  const releaseUnit = async (unit: number) => {
    if (!selectedRack) return
    try {
      await apiFetch(`/cmdb/racks/${selectedRack.id}/units`, { method: 'POST', body: JSON.stringify({ units: [{ unit, ci_id: '', label: '' }] }) })
      getMessage().success(`U 位 ${unit} 已释放`); loadUnits(selectedRack.id)
    } catch (e: any) { getMessage().error(e.message) }
  }

  const maxUnits = selectedRack?.max_units || 42
  const occupiedCIs = new Set(Object.values(units).filter(u => u.ci_id).map(u => u.ci_id))

  // ============ 2D 视图：多 U 落位 / 拖拽 / 健康联动 ============

  // 设备块高度（U 数）
  const devHeight = (d: any) => Math.max(1, Number(d.attributes?.u_height || 1))
  const devStart = (d: any) => Number(d.attributes?.rack_unit || 1)

  // 平面图按 row/col 真实定位（无坐标的追加到流式区）
  const roomGrid = useMemo(() => {
    const rs = roomRacks()
    const rows = [...new Set(rs.map(r => r.attributes?.row).filter(Boolean))].sort()
    const cols = [...new Set(rs.map(r => r.attributes?.col).filter(Boolean))].sort()
    const pos = rs.filter(r => r.attributes?.row && r.attributes?.col)
    const rest = rs.filter(r => !(r.attributes?.row && r.attributes?.col))
    return { rows, cols, pos, rest }
  }, [panorama, panoRoom])

  // 分配设备到机柜 U 位（支持多 U 区间；后端负责冲突检测与迁移清旧）
  const assignDeviceToRack = async (rackID: string, startUnit: number, height: number, ci: { id: string; name: string; type: string }) => {
    setAssigning(true)
    try {
      await apiFetch(`/cmdb/racks/${rackID}/units`, {
        method: 'POST',
        body: JSON.stringify({ units: [{ unit: startUnit, units: height, ci_id: ci.id }] }),
      })
      getMessage().success(`${ci.name} 已上架 U${startUnit}${height > 1 ? `-${startUnit + height - 1}` : ''}`)
      setRecUnits([]); setDragCI(null)
      await loadPanorama(); await loadCIs()
      if (selectedRack) loadUnits(selectedRack.id)
    } catch (e: any) { getMessage().error(e.message) } finally { setAssigning(false) }
  }

  const onDeviceDragStart = (ci: CIModel) => (e: React.DragEvent) => {
    const height = Math.max(1, Number((ci as any).attributes?.u_height || 1))
    setDragCI({ id: ci.id, name: ci.name, type: ci.type_id || '', height })
    e.dataTransfer.setData('text/plain', ci.id)
    e.dataTransfer.effectAllowed = 'copy'
  }

  const onUnitDrop = (rackID: string, dropUnit: number) => (e: React.DragEvent) => {
    e.preventDefault()
    const ci = dragCI
    if (!ci || assigning) return
    // 拖到 dropUnit，按设备高度向上取起始位（不越界）
    const start = Math.max(1, dropUnit - ci.height + 1)
    assignDeviceToRack(rackID, start, ci.height, ci)
  }

  // 点击设备 → 高亮推荐连续 U 位
  const highlightRecommend = (ci: CIModel, rack: any) => {
    const height = Math.max(1, Number((ci as any).attributes?.u_height || 1))
    const maxU = Number(rack.attributes?.max_units || 42)
    const occupied = new Set<number>()
    for (const d of rackDevices(rack.id)) {
      const s = devStart(d); const h = devHeight(d)
      for (let i = s; i < s + h; i++) occupied.add(i)
    }
    setRecUnits(findAvailablePositions(height, maxU, occupied))
  }

  // 机柜正视图网格（空 U 行 + 设备块重叠）— 2.5D 透视
  const rackElevation = (rack: any) => {
    const maxU = Number(rack.attributes?.max_units || 42)
    const devs = rackDevices(rack.id)
    const recSet = new Set(recUnits)
    return (
      <div className="perspective-[800px]">
        <div
          className="relative grid gap-[1px] origin-center"
          style={{
            gridTemplateRows: `repeat(${maxU}, 18px)`,
            transform: 'rotateX(8deg) rotateY(-2deg)',
            transformStyle: 'preserve-3d',
          }}
        >
          {/* 机柜框架 — 顶部横梁 */}
          <div
            className="absolute -top-3 left-0 right-0 h-3 rounded-t-md"
            style={{
              background: 'linear-gradient(180deg, #475569 0%, #334155 100%)',
              boxShadow: '0 -2px 6px rgba(0,0,0,0.3), inset 0 1px 0 rgba(255,255,255,0.1)',
              transform: 'translateZ(4px)',
            }}
          />
          {/* 机柜框架 — 底部横梁 */}
          <div
            className="absolute -bottom-3 left-0 right-0 h-3 rounded-b-md"
            style={{
              background: 'linear-gradient(180deg, #334155 0%, #1e293b 100%)',
              boxShadow: '0 4px 8px rgba(0,0,0,0.4), inset 0 1px 0 rgba(255,255,255,0.05)',
              transform: 'translateZ(4px)',
            }}
          />
          {/* 机柜框架 — 左侧立柱 */}
          <div
            className="absolute -left-2 top-0 bottom-0 w-2 rounded-l-md"
            style={{
              background: 'linear-gradient(90deg, #1e293b 0%, #334155 50%, #475569 100%)',
              boxShadow: '-3px 0 8px rgba(0,0,0,0.3)',
              transform: 'translateZ(6px)',
            }}
          />
          {/* 机柜框架 — 右侧立柱 */}
          <div
            className="absolute -right-2 top-0 bottom-0 w-2 rounded-r-md"
            style={{
              background: 'linear-gradient(90deg, #475569 0%, #334155 50%, #1e293b 100%)',
              boxShadow: '3px 0 8px rgba(0,0,0,0.3)',
              transform: 'translateZ(6px)',
            }}
          />
          {Array.from({ length: maxU }, (_, i) => maxU - i).map(u => {
            const rec = recSet.has(u)
            return (
              <div key={u}
                onDragOver={e => e.preventDefault()}
                onDrop={onUnitDrop(rack.id, u)}
                className={cn(
                  'flex items-center gap-0.5 px-1 text-[8px] transition-all duration-150',
                  rec
                    ? 'bg-blue-500/15 text-blue-600 dark:text-blue-400 cursor-copy border border-blue-400/40 shadow-[inset_0_0_8px_rgba(59,130,246,0.2)]'
                    : 'bg-white/40 dark:bg-white/[0.02] text-muted-foreground/50 border border-gray-200/60 dark:border-white/[0.06]'
                )}
                style={{
                  gridRow: maxU - u + 1,
                  borderRadius: '2px',
                }}>
                <span className="w-3 text-right shrink-0 tabular-nums">{u}</span>
                {rec && <span className="truncate text-[7px] font-medium">可放</span>}
              </div>
            )
          })}
          {devs.map(d => {
            const start = devStart(d); const h = devHeight(d)
            const topRow = maxU - start - h + 2
            const health = d.attributes?.health
            const crit = Number(d.attributes?.alert_critical || 0)
            const warn = Number(d.attributes?.alert_warning || 0)
            const down = health === 'down'
            const baseColor = deviceColor(d.type_id)
            // 根据设备类型生成 3D 渐变色板
            const gradients: Record<string, string> = {
              'bg-sky-500': 'linear-gradient(135deg, #38bdf8 0%, #0284c7 40%, #075985 100%)',
              'bg-violet-500': 'linear-gradient(135deg, #a78bfa 0%, #7c3aed 40%, #5b21b6 100%)',
              'bg-amber-500': 'linear-gradient(135deg, #fbbf24 0%, #d97706 40%, #92400e 100%)',
              'bg-blue-500': 'linear-gradient(135deg, #60a5fa 0%, #2563eb 40%, #1d4ed8 100%)',
              'bg-slate-400': 'linear-gradient(135deg, #94a3b8 0%, #64748b 40%, #475569 100%)',
            }
            const grad = gradients[baseColor] || gradients['bg-slate-400']
            const devIP = d.attributes?.ip_address || d.attributes?.ip || ''
            const devStatus = d.attributes?.status || d.attributes?.health || ''
            return (
              <div key={d.id}
                draggable={!assigning}
                onDragStart={e => { e.stopPropagation(); onDeviceDragStart({ id: d.id, name: d.name, type_id: d.type_id, attributes: { u_height: h } } as any)(e) }}
                onDragOver={e => e.preventDefault()}
                onDrop={onUnitDrop(rack.id, start)}
                onClick={(e) => { e.stopPropagation(); navigate(`/cmdb/instances/${d.id}`) }}
                onMouseEnter={e => { const r = e.currentTarget.getBoundingClientRect(); setHoverDev({ dev: d, x: r.left + r.width / 2, y: r.top - 8 }) }}
                onMouseLeave={() => setHoverDev(null)}
                className={cn(
                  'relative flex items-center gap-1 px-2 text-[9px] text-white cursor-pointer',
                  'transition-all duration-200 hover:brightness-110 hover:scale-[1.02] hover:z-10',
                  down && 'ring-2 ring-red-500 ring-offset-1 ring-offset-white dark:ring-offset-slate-900',
                  panoFace === 'rear' && 'opacity-80'
                )}
                style={{
                  gridRow: `${topRow} / span ${h}`,
                  background: grad,
                  borderRadius: '3px',
                  boxShadow: `0 1px 3px rgba(0,0,0,0.3), 0 ${h * 2}px ${h * 4}px rgba(0,0,0,0.15), inset 0 1px 0 rgba(255,255,255,0.2), inset 0 -1px 0 rgba(0,0,0,0.15)`,
                  transform: 'translateZ(2px)',
                  borderLeft: '1px solid rgba(255,255,255,0.15)',
                  borderRight: '1px solid rgba(0,0,0,0.1)',
                }}>
                {/* 左侧高光条 */}
                <div className="absolute left-0 top-0 bottom-0 w-[2px] rounded-l-sm" style={{ background: 'linear-gradient(180deg, rgba(255,255,255,0.25) 0%, rgba(255,255,255,0.05) 100%)' }} />
                <span className="truncate font-medium drop-shadow-sm">{d.name}</span>
                {crit > 0 && (
                  <span className="ml-auto h-2.5 w-2.5 rounded-full bg-red-500 ring-2 ring-white/80 shrink-0 shadow-lg animate-pulse" />
                )}
                {warn > 0 && crit === 0 && (
                  <span className="ml-auto h-2.5 w-2.5 rounded-full bg-amber-400 ring-2 ring-white/80 shrink-0 shadow-lg" />
                )}
              </div>
            )
           })}
        </div>
      </div>
    )
  }

  // 设备 hover 提示浮层
  const DeviceTooltip = () => {
    if (!hoverDev) return null
    const d = hoverDev.dev
    const h = devHeight(d)
    const start = devStart(d)
    return (
      <div
        className="fixed z-[9999] pointer-events-none px-3 py-2 rounded-lg shadow-xl border border-border/60 text-xs max-w-[260px]"
        style={{
          left: hoverDev.x,
          top: hoverDev.y,
          transform: 'translate(-50%, -100%)',
          background: 'var(--popover)',
          color: 'var(--popover-foreground)',
        }}
      >
        <p className="font-semibold text-sm mb-1 truncate">{d.name}</p>
        <div className="space-y-0.5 text-muted-foreground">
          <p>类型：<span className="text-foreground font-medium">{d.type_id}</span></p>
          {d.attributes?.ip_address && <p>IP：<span className="text-foreground font-mono">{d.attributes.ip_address}</span></p>}
          <p>位置：U{start}{h > 1 ? `-${start + h - 1}` : ''}</p>
          {d.attributes?.status && <p>状态：<span className="font-medium">{d.attributes.status}</span></p>}
          {d.attributes?.health && <p>健康：<span className="font-medium">{d.attributes.health}</span></p>}
        </div>
        <p className="text-[10px] text-muted-foreground mt-1.5 border-t border-border/40 pt-1">点击查看 CMDB 详情</p>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <DeviceTooltip />
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">机房机柜视图</h1>
          <p className="text-sm text-muted-foreground">{view2D ? '2.5D 效果图（管理/模型数据驱动 · 拖拽上架 · 多 U 落位 · 监控联动）' : 'IDC 机房 / 机柜 / U 位可视化管理与设备占用'}</p>
        </div>
        <div className="flex gap-2">
          <Button type={view2D ? 'primary' : 'default'} size="middle" onClick={() => { setView2D(true); loadPanorama() }}><Rows3 className="h-3.5 w-3.5 mr-1" />2D 视图</Button>
          <Button type={!view2D ? 'primary' : 'default'} size="middle" onClick={() => setView2D(false)}><Building2 className="h-3.5 w-3.5 mr-1" />管理</Button>
          {!view2D && <Button type="default" size="middle" onClick={() => { loadRooms(); if (selectedRoom) loadRacks(selectedRoom.id); if (selectedRack) loadUnits(selectedRack.id) }}><RefreshCw className="h-4 w-4" />刷新</Button>}
        </div>
      </div>

      {view2D ? (
        /* ============ 2D 效果图(模型驱动) ============ */
        <div className="space-y-4">
          {/* 机房选择 + 容量统计 */}
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-xs text-muted-foreground">机房:</span>
            <button onClick={() => { setPanoRoom(''); setPanoRack(null) }} className={cn('px-3 py-1 rounded-full text-xs border', panoRoom === '' ? 'border-blue-500 bg-blue-50 dark:bg-blue-500/10 text-blue-600' : 'border-gray-200 dark:border-gray-700')}>全部</button>
            {panoRooms.map(rid => (
              <button key={rid} onClick={() => { setPanoRoom(rid); setPanoRack(null) }} className={cn('px-3 py-1 rounded-full text-xs border', panoRoom === rid ? 'border-blue-500 bg-blue-50 dark:bg-blue-500/10 text-blue-600' : 'border-gray-200 dark:border-gray-700')}>{roomName(rid)}</button>
            ))}
          </div>

          {/* 容量统计卡片 — 2.5D 风格 */}
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3" style={{ perspective: '800px' }}>
            {[
              { label: '机柜', value: panoStats.rackCount, icon: '🏢', color: '#3b82f6' },
              { label: '设备', value: panoStats.devCount, icon: '🖥️', color: '#8b5cf6' },
              { label: '总 U 位', value: panoStats.totalU, icon: '📐', color: '#6366f1' },
              { label: '已占用 U', value: panoStats.usedU, icon: '📦', color: '#f59e0b' },
              { label: '剩余 U', value: panoStats.freeU, icon: '✅', color: '#22c55e' },
              { label: '占用率', value: `${panoStats.usage}%`, icon: '📊', color: panoStats.usage >= 80 ? '#ef4444' : '#3b82f6' },
            ].map(s => (
              <div key={s.label}
                className="group cursor-default"
                style={{
                  background: `linear-gradient(135deg, ${s.color}08 0%, ${s.color}04 50%, transparent 100%)`,
                  border: `1px solid ${s.color}20`,
                  borderRadius: 10,
                  padding: '0.75rem 0.875rem',
                  boxShadow: `0 2px 8px ${s.color}10, 0 4px 16px ${s.color}08, inset 0 1px 0 rgba(255,255,255,0.5)`,
                  transform: 'perspective(500px) rotateX(2deg)',
                  transition: 'all 0.2s ease',
                }}
                onMouseEnter={e => { (e.currentTarget as HTMLElement).style.transform = 'perspective(500px) rotateX(1deg) translateZ(3px)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLElement).style.transform = 'perspective(500px) rotateX(2deg)' }}
              >
                <div className="flex items-center gap-1.5 mb-1">
                  <span className="text-sm">{s.icon}</span>
                  <p className="text-[11px] font-medium" style={{ color: `${s.color}99` }}>{s.label}</p>
                </div>
                <p className="text-xl font-bold tabular-nums" style={{ color: s.color }}>{s.value}</p>
              </div>
            ))}
          </div>

          {/* 未上架设备面板 — 内联卡片 */}
          {freeDevices.length > 0 && (
            <Card>
              <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Gauge className="h-4 w-4" />未上架设备（{freeDevices.length}）
                </CardTitle>
                {panoRack && <span className="text-[10px] text-muted-foreground">拖拽到正视图上架</span>}
              </CardHeader>
              <CardContent className="p-3 pt-0">
                <div className="flex flex-wrap gap-1.5">
                  {freeDevices.map(ci => (
                    <div key={ci.id}
                      draggable={!!panoRack && !assigning}
                      onDragStart={onDeviceDragStart(ci)}
                      onClick={() => { panoRack && highlightRecommend(ci, panoRack) }}
                      title={panoRack ? '拖到机柜正视图 U 位上架，或点击高亮推荐空位' : '进入机柜正视图后，可拖拽上架'}
                      className={cn('border rounded px-2 py-1 text-[11px] flex items-center gap-1.5 cursor-grab active:cursor-grabbing transition-colors',
                        panoRack ? 'hover:border-blue-500 hover:bg-blue-50 dark:hover:bg-blue-500/10' : 'opacity-70 cursor-default')}>
                      <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', deviceColor(ci.type_id || ''))} />
                      <span className="font-medium truncate max-w-[120px]">{ci.name}</span>
                      <Tag className="text-[8px] ml-auto shrink-0">{ci.type_id}</Tag>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          )}

          {/* 平面图 / 机柜正视图 — 全宽 */}
          <div className="space-y-4">
              {panoRack ? (
                /* 机柜正视图: U 位从下到上, 多 U 设备跨行, 支持拖拽上架/迁移 */
                <Card style={{ boxShadow: '0 4px 16px rgba(0,0,0,0.08), 0 1px 4px rgba(0,0,0,0.05)', border: '1px solid rgba(0,0,0,0.06)' }}>
                  <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
                    <CardTitle className="text-sm flex items-center gap-2">
                      机柜 {panoRack.name || panoRack.id} 正视图
                      <Tag className="text-[9px]">{panoFace === 'front' ? '正面' : '背面'}</Tag>
                    </CardTitle>
                    <div className="flex items-center gap-2">
                      <Button type="default" size="middle" onClick={() => setPanoFace(f => f === 'front' ? 'rear' : 'front')}>
                        <ArrowLeftRight className="h-3 w-3 mr-1" />{panoFace === 'front' ? '背面' : '正面'}
                      </Button>
                      <Button type="default" size="middle" onClick={() => { setPanoRack(null); setRecUnits([]) }}>返回平面图</Button>
                    </div>
                  </CardHeader>
                  <CardContent>
                    <div className="flex gap-3 justify-center items-start py-4">
                      <div className="flex-1 max-w-[420px] rounded-lg p-3" style={{
                        background: 'linear-gradient(180deg, #1e293b 0%, #0f172a 100%)',
                        boxShadow: '0 8px 32px rgba(0,0,0,0.3), 0 2px 8px rgba(0,0,0,0.2), inset 0 1px 0 rgba(255,255,255,0.05)',
                        border: '1px solid rgba(255,255,255,0.05)',
                      }}>
                        {rackElevation(panoRack)}
                      </div>
                    </div>
                    <p className="text-[10px] text-muted-foreground mt-3 text-center">
                      {panoFace === 'front' ? '正面视图' : '背面视图（设备同构展示）'} · 拖拽「未上架设备」到空 U 位即可上架 · 点击设备可拖动迁移 · 蓝色高亮为推荐可放位置
                    </p>
                  </CardContent>
                </Card>
              ) : (
                /* ============ 机房 SVG 俯瞰平面图（d3-zoom 缩放/平移） ============ */
                <Card style={{ boxShadow: '0 4px 16px rgba(0,0,0,0.08), 0 1px 4px rgba(0,0,0,0.05)', border: '1px solid rgba(0,0,0,0.06)' }}>
                  <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
                    <CardTitle className="text-sm flex items-center gap-2">
                      <Building2 className="h-4 w-4" />机房平面图
                      <Tag className="text-[9px]">俯瞰 · 滚轮缩放 · 拖拽平移</Tag>
                    </CardTitle>
                  </CardHeader>
                  <CardContent className="p-0">
                    {roomGrid.pos.length === 0 && roomGrid.rest.length === 0 ? (
                      <p className="text-sm text-muted-foreground py-8 text-center">暂无机柜数据。可在「管理」视图创建机房/机柜（填写排、列、U 位总数），或录入 rack 配置项后自动成图。</p>
                    ) : (
                      <div className="space-y-3">
                        {/* SVG 平面图（d3-zoom） */}
                        {roomGrid.pos.length > 0 && (
                          <FloorPlanSVG
                            rows={roomGrid.rows}
                            cols={roomGrid.cols}
                            racks={roomGrid.pos}
                            rackDevices={rackDevices}
                            devHeight={devHeight}
                            onRackClick={setPanoRack}
                          />
                        )}
                        {/* 图例 */}
                        <div className="flex items-center gap-4 justify-center flex-wrap px-4 pb-2">
                          {[
                            { color: '#3b82f6', label: '空闲' },
                            { color: '#d97706', label: '部分占用' },
                            { color: '#dc2626', label: '高占用' },
                          ].map(l => (
                            <div key={l.label} className="flex items-center gap-1.5">
                              <div className="w-3 h-3 rounded-sm" style={{ background: l.color, opacity: 0.7 }} />
                              <span className="text-[10px] text-muted-foreground">{l.label}</span>
                            </div>
                          ))}
                        </div>
                        {/* 未设置排/列的机柜 */}
                        {roomGrid.rest.length > 0 && (
                          <div className="border-t border-border/60 pt-3 px-4 pb-3">
                            <p className="text-[10px] text-muted-foreground mb-2">未设置排/列的机柜：</p>
                            <div className="flex gap-2 flex-wrap">
                              {roomGrid.rest.map(rk => {
                                const devs = rackDevices(rk.id)
                                return (
                                  <button key={rk.id} onClick={() => setPanoRack(rk)} title={rk.name || rk.id}
                                    className="border rounded-lg px-3 py-1.5 text-center cursor-pointer hover:scale-105 hover:shadow-md transition-all active:scale-95"
                                    style={{
                                      background: 'linear-gradient(135deg, #f8fafc 0%, #e2e8f0 100%)',
                                      boxShadow: '0 2px 8px rgba(0,0,0,0.06), inset 0 1px 0 rgba(255,255,255,0.8)',
                                    }}>
                                    <Server className="h-3 w-3 mx-auto text-muted-foreground mb-0.5" />
                                    <p className="text-[10px] font-medium">{rk.name || rk.id.slice(0, 6)}</p>
                                    <p className="text-[8px] text-muted-foreground">{devs.length}台</p>
                                  </button>
                                )
                              })}
                            </div>
                          </div>
                        )}
                      </div>
                    )}
                  </CardContent>
                </Card>
              )}

              {/* 当前机柜设备列表（含健康/告警） */}
              {panoRack && (
                <Card>
                  <CardHeader className="py-3"><CardTitle className="text-sm">机柜内设备（{rackDevices(panoRack.id).length}）</CardTitle></CardHeader>
                  <CardContent className="p-3 space-y-2">
                    {rackDevices(panoRack.id).length === 0 ? (
                      <p className="text-xs text-muted-foreground text-center py-4">拖拽左侧未上架设备到此机柜</p>
                    ) : rackDevices(panoRack.id).map(d => {
                      const start = devStart(d); const h = devHeight(d)
                      const crit = Number(d.attributes?.alert_critical || 0)
                      const warn = Number(d.attributes?.alert_warning || 0)
                      const down = d.attributes?.health === 'down'
                      return (
                        <div key={d.id} className={cn('border rounded-md px-3 py-2 text-xs flex items-center gap-2', down && 'border-red-400 bg-red-50 dark:bg-red-500/10')}>
                          <span className={cn('h-2 w-2 rounded-full', deviceColor(d.type_id))} />
                          <span className="font-medium">{d.name}</span>
                           <Tag className="text-[9px]">{d.type_id}</Tag>
                          <span className="text-muted-foreground ml-auto">U{start}{h > 1 ? `-${start + h - 1}` : ''}</span>
                           {down && <Tag color="error" className="text-[9px]">宕机</Tag>}
                           {crit > 0 && <Tag color="error" className="text-[9px]">{crit} 严重</Tag>}
                           {warn > 0 && <Tag color="warning" className="text-[9px]">{warn} 警告</Tag>}
                        </div>
                      )
                    })}
                  </CardContent>
                </Card>
              )}
            </div>
            </div>

          ) : (
          <>
          <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
            {/* 新建机房 */}
        {/* 机房列表 */}
        <Card>
          <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
            <CardTitle className="text-sm flex items-center gap-2"><Building2 className="h-4 w-4" />机房（{rooms.length}）</CardTitle>
            <Button size="middle" type="default" onClick={() => setRoomDialog(true)}><Plus className="h-3 w-3" />新建机房</Button>
          </CardHeader>
          <CardContent className="p-0">
            {rooms.length === 0 ? <p className="px-4 py-6 text-sm text-muted-foreground text-center">暂无机房</p> : rooms.map(r => (
              <div key={r.id} onClick={() => selectRoom(r)} className={cn('px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors flex items-center justify-between', selectedRoom?.id === r.id && 'bg-accent border-l-2 border-l-blue-500')}>
                <div>
                  <p className="text-sm font-medium">{r.name}</p>
                  <p className="text-xs text-muted-foreground">{r.location || '未填位置'}{r.description ? ` · ${r.description}` : ''}</p>
                </div>
                <Button type="text" size="middle" className="h-6 w-6 text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={e => { e.stopPropagation(); openDelete('room', r) }}><Trash2 className="h-3 w-3" /></Button>
              </div>
            ))}
          </CardContent>
        </Card>

        {/* 机柜列表 */}
        <Card>
          <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
            <CardTitle className="text-sm flex items-center gap-2"><Server className="h-4 w-4" />机柜{selectedRoom ? ` · ${selectedRoom.name}` : ''}（{racks.length}）</CardTitle>
            {selectedRoom && <Button size="middle" type="default" onClick={() => setRackDialog(true)}><Plus className="h-3 w-3" />新建机柜</Button>}
          </CardHeader>
          <CardContent className="p-0">
            {!selectedRoom ? <p className="px-4 py-6 text-sm text-muted-foreground text-center">先选择一个机房</p> : racks.length === 0 ? <p className="px-4 py-6 text-sm text-muted-foreground text-center">暂无机柜</p> : racks.map(rk => (
              <div key={rk.id} onClick={() => selectRack(rk)} className={cn('px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors flex items-center justify-between', selectedRack?.id === rk.id && 'bg-accent border-l-2 border-l-blue-500')}>
                <div className="flex items-center gap-2">
                  <Rows3 className="h-4 w-4 text-muted-foreground" />
                  <div>
                    <p className="text-sm font-medium">{rk.name}</p>
                    <p className="text-xs text-muted-foreground">{rk.max_units}U{ rk.row ? ` · ${rk.row}` : ''}{rk.col ? ` · ${rk.col}` : ''}</p>
                  </div>
                </div>
                <Button type="text" size="middle" className="h-6 w-6 text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={e => { e.stopPropagation(); openDelete('rack', rk) }}><Trash2 className="h-3 w-3" /></Button>
              </div>
            ))}
          </CardContent>
        </Card>

        {/* U 位视图 */}
        <Card className="lg:col-span-1">
          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2">U 位视图{selectedRack ? ` · ${selectedRack.name}` : ''}</CardTitle></CardHeader>
          <CardContent className="p-3">
            {!selectedRack ? <p className="text-sm text-muted-foreground text-center py-8">选择机柜查看 U 位</p> : (
              <div className="flex gap-1">
                <div className="w-8 text-right text-[10px] text-muted-foreground space-y-0.5">
                  {Array.from({ length: maxUnits }, (_, i) => maxUnits - i).map(u => <div key={u} className="h-5 leading-5">{u}</div>)}
                </div>
                <div className="flex-1 space-y-0.5">
                  {Array.from({ length: maxUnits }, (_, i) => maxUnits - i).map(u => {
                    const occ = units[u]
                    const used = !!occ?.ci_id || !!occ?.label
                    return (
                      <div key={u} onClick={() => openAssign(u)} title={occ?.ci_id ? `${occ.label || occ.ci_id}（CI）` : occ?.label || '空闲'} className={cn('h-5 rounded-sm border text-[10px] flex items-center px-1.5 cursor-pointer transition-colors truncate', used ? 'border-blue-400 bg-blue-100 text-blue-800 dark:bg-blue-500/20 dark:text-blue-300' : 'border-dashed border-gray-300 dark:border-gray-600 text-muted-foreground hover:bg-accent')}>
                        {occ?.ci_id ? (occ.label || occ.ci_id.slice(0, 12)) : occ?.label || ''}
                      </div>
                    )
                  })}
                </div>
              </div>
            )}
            {selectedRack && Object.values(units).some(u => u.ci_id) && (
              <p className="text-[10px] text-muted-foreground mt-2">已占用 {occupiedCIs.size} 台设备 · 点击 U 位可分配/释放</p>
            )}
          </CardContent>
        </Card>
      </div>

      {/* 新建机房 */}
      <UniversalForm
        visible={roomDialog}
        title="新建机房"
        form={roomForm}
        fields={[
          { name: 'name', label: '机房名称 *', type: 'text', placeholder: '如：华东一区', rules: [{ required: true }] },
          { name: 'location', label: '位置', type: 'text', placeholder: '如：上海市青浆区' },
          { name: 'description', label: '描述', type: 'text' },
        ]}
        onCancel={() => setRoomDialog(false)}
        onSubmit={createRoom}
      />

      {/* 新建机柜 */}
      <UniversalForm
        visible={rackDialog}
        title={`新建机柜 · ${selectedRoom?.name || ''}`}
        form={rackForm}
        fields={[
          { name: 'name', label: '机柜编号 *', type: 'text', placeholder: 'R01', rules: [{ required: true }], grid: true, colspan: 1 },
          { name: 'max_units', label: 'U 位总数', type: 'number', grid: true, colspan: 1 },
          { name: 'row', label: '排', type: 'text', placeholder: 'A 排', grid: true, colspan: 1 },
          { name: 'col', label: '列', type: 'text', placeholder: '1', grid: true, colspan: 1 },
        ]}
        onCancel={() => setRackDialog(false)}
        onSubmit={createRack}
      />

      {/* U 位分配 */}
      <Modal open={unitDialog} onCancel={() => setUnitDialog(false)} title={`分配 U 位 ${unitForm.unit}`} footer={null}>
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">绑定 CI 资产（可选）</label>
            <Select value={unitForm.ci_id || undefined} onChange={v => setUnitForm(f => ({ ...f, ci_id: v || '' }))} className="w-full mt-1" options={[{ value: '', label: '（不绑定，仅填标签）' }, ...cis.map(c => ({ value: c.id, label: c.name }))]} />
          </div>
          <div><label className="text-sm font-medium">标签</label><Input value={unitForm.label} onChange={e => setUnitForm(f => ({ ...f, label: e.target.value }))} placeholder="设备名/备注" /></div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          {units[unitForm.unit]?.ci_id && <Button type="default" onClick={() => { releaseUnit(unitForm.unit); setUnitDialog(false) }}><Unplug className="h-3 w-3 mr-1" />释放</Button>}
          <Button type="default" onClick={() => setUnitDialog(false)}>取消</Button>
          <Button type="primary" onClick={assignUnit}>保存</Button>
        </div>
      </Modal>

      <Popconfirm
        title="确认删除"
        description={deleteTarget ? (deleteTarget.kind === 'room' ? `确认删除机房「${(deleteTarget.item as IDCRoom).name}」及其全部机柜？` : `确认删除机柜「${(deleteTarget.item as Rack).name}」及其 U 位占用？`) : ''}
        onConfirm={confirmDelete}
        open={!!deleteTarget}
        onCancel={() => setDeleteTarget(null)}
        okButtonProps={{ loading: deleteSubmitting }}
      />
      </>
      )}
    </div>
  )
}
