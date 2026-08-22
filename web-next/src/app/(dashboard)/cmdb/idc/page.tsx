'use client'

import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, apiList } from '@/lib/utils'
import { Plus, Trash2, Building2, Server, RefreshCw, Rows3, Unplug, Gauge, ArrowLeftRight, PanelLeft, PanelRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'

interface IDCRoom { id: string; name: string; location?: string; description?: string }
interface Rack { id: string; idc_id: string; name: string; row?: string; col?: string; max_units: number }
interface RackUnit { rack_id: string; unit: number; ci_id?: string; label?: string }
interface CIModel { id: string; name: string; type_id?: string; status?: string }

const DEVICE_TYPES = ['server', 'virtual_machine', 'storage', 'firewall', 'load_balancer', 'router', 'switch', 'database', 'middleware', 'app']

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

export default function CMDBIDCPage() {
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
  const [assigning, setAssigning] = useState(false)
  const loadPanorama = async () => {
    try {
      const data = await apiFetch<{ racks: any[]; devices: any[] }>('/cmdb/idc/panorama')
      setPanorama(data)
    } catch { setPanorama({ racks: [], devices: [] }) }
  }
  const rackDevices = (rackID: string) => panorama.devices.filter(d => d.attributes?.rack_id === rackID)
  const deviceColor = (t: string) =>
    t === 'server' || t === 'virtual_machine' ? 'bg-sky-500' :
    t === 'database' || t === 'middleware' ? 'bg-violet-500' :
    t === 'storage' ? 'bg-amber-500' :
    t === 'firewall' || t === 'load_balancer' || t === 'router' || t === 'switch' ? 'bg-emerald-500' : 'bg-slate-400'
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
    if (!roomForm.name.trim()) { toast.error('请输入机房名称'); return }
    try {
      await apiFetch('/cmdb/idc', { method: 'POST', body: JSON.stringify(roomForm) })
      toast.success('机房已创建'); setRoomDialog(false); setRoomForm({ name: '', location: '', description: '' }); loadRooms()
    } catch (e: any) { toast.error(e.message) }
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
        await apiFetch(`/cmdb/idc/${(item as IDCRoom).id}`, { method: 'DELETE' }); toast.success('已删除')
        if (selectedRoom?.id === (item as IDCRoom).id) selectRoom(null); loadRooms()
      } else {
        await apiFetch(`/cmdb/racks/${(item as Rack).id}`, { method: 'DELETE' }); toast.success('已删除')
        if (selectedRack?.id === (item as Rack).id) selectRack(null); loadRacks(selectedRoom!.id)
      }
      setDeleteTarget(null)
    } catch (e: any) { toast.error(e.message) } finally { setDeleteSubmitting(false) }
  }

  const createRack = async () => {
    if (!selectedRoom || !rackForm.name.trim()) { toast.error('请输入机柜编号'); return }
    try {
      await apiFetch(`/cmdb/idc/${selectedRoom.id}/racks`, { method: 'POST', body: JSON.stringify(rackForm) })
      toast.success('机柜已创建'); setRackDialog(false); setRackForm({ name: '', row: '', col: '', max_units: 42 }); loadRacks(selectedRoom.id)
    } catch (e: any) { toast.error(e.message) }
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
      toast.success(`U 位 ${unitForm.unit} 已更新`); setUnitDialog(false); loadUnits(selectedRack.id); loadCIs()
    } catch (e: any) { toast.error(e.message) }
  }

  const releaseUnit = async (unit: number) => {
    if (!selectedRack) return
    try {
      await apiFetch(`/cmdb/racks/${selectedRack.id}/units`, { method: 'POST', body: JSON.stringify({ units: [{ unit, ci_id: '', label: '' }] }) })
      toast.success(`U 位 ${unit} 已释放`); loadUnits(selectedRack.id)
    } catch (e: any) { toast.error(e.message) }
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
      toast.success(`${ci.name} 已上架 U${startUnit}${height > 1 ? `-${startUnit + height - 1}` : ''}`)
      setRecUnits([]); setDragCI(null)
      await loadPanorama(); await loadCIs()
      if (selectedRack) loadUnits(selectedRack.id)
    } catch (e: any) { toast.error(e.message) } finally { setAssigning(false) }
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

  // 机柜正视图网格（空 U 行 + 设备块重叠）
  const rackElevation = (rack: any) => {
    const maxU = Number(rack.attributes?.max_units || 42)
    const devs = rackDevices(rack.id)
    const recSet = new Set(recUnits)
    return (
      <div className="relative grid gap-[2px]" style={{ gridTemplateRows: `repeat(${maxU}, 20px)` }}>
        {Array.from({ length: maxU }, (_, i) => maxU - i).map(u => {
          const rec = recSet.has(u)
          return (
            <div key={u}
              onDragOver={e => e.preventDefault()}
              onDrop={onUnitDrop(rack.id, u)}
              className={cn('flex items-center gap-1 px-1.5 rounded-sm border text-[9px] transition-colors',
                rec ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-500/15 text-emerald-600 cursor-copy' : 'border-dashed border-gray-200 dark:border-gray-700 text-muted-foreground')}
              style={{ gridRow: maxU - u + 1 }}>
              <span className="w-5 text-right shrink-0">{u}</span>
              {rec && <span className="truncate">可放</span>}
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
          return (
            <div key={d.id}
              draggable={!assigning}
              onDragStart={e => onDeviceDragStart({ id: d.id, name: d.name, type_id: d.type_id, attributes: { u_height: h } } as any)(e)}
              onDragOver={e => e.preventDefault()}
              onDrop={onUnitDrop(rack.id, start)}
              title={`${d.name} · ${d.type_id} · U${start}${h > 1 ? `-${start + h - 1}` : ''}${down ? ' · 宕机' : ''}${crit ? ` · ${crit} 条严重告警` : ''}`}
              className={cn('relative flex items-center gap-1 px-1.5 rounded-sm text-[9px] text-white cursor-grab active:cursor-grabbing border',
                deviceColor(d.type_id),
                down && 'ring-2 ring-red-500',
                panoFace === 'rear' && 'opacity-80')}
              style={{ gridRow: `${topRow} / span ${h}` }}>
              <span className="truncate font-medium">{d.name}</span>
              {crit > 0 && <span className="ml-auto h-2 w-2 rounded-full bg-red-600 ring-1 ring-white shrink-0" />}
              {warn > 0 && crit === 0 && <span className="ml-auto h-2 w-2 rounded-full bg-amber-400 ring-1 ring-white shrink-0" />}
            </div>
          )
        })}
      </div>
    )
  }

  // 右侧 PDU 竖条（纯视觉外设）
  const pduBar = (side: 'L' | 'R') => {
    const maxU = panoRack ? Number(panoRack.attributes?.max_units || 42) : 42
    return (
      <div className="flex flex-col items-center gap-[2px] w-7" title={`PDU-${side}`}>
        {side === 'L' ? <PanelLeft className="h-3 w-3 text-muted-foreground mb-0.5" /> : <PanelRight className="h-3 w-3 text-muted-foreground mb-0.5" />}
        {Array.from({ length: maxU }, (_, i) => (
          <div key={i} className={cn('w-5 h-5 rounded-sm border border-dashed', side === 'L' ? 'border-gray-200 dark:border-gray-700' : 'border-gray-200 dark:border-gray-700')} style={{ height: 20 }} />
        ))}
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">机房机柜视图</h1>
          <p className="text-sm text-muted-foreground">{view2D ? '2D 效果图（管理/模型数据驱动 · 拖拽上架 · 多 U 落位 · 监控联动）' : 'IDC 机房 / 机柜 / U 位可视化管理与设备占用'}</p>
        </div>
        <div className="flex gap-2">
          <Button variant={view2D ? 'default' : 'outline'} size="sm" onClick={() => { setView2D(true); loadPanorama() }}><Rows3 className="h-3.5 w-3.5 mr-1" />2D 视图</Button>
          <Button variant={!view2D ? 'default' : 'outline'} size="sm" onClick={() => setView2D(false)}><Building2 className="h-3.5 w-3.5 mr-1" />管理</Button>
          {!view2D && <Button variant="outline" size="sm" onClick={() => { loadRooms(); if (selectedRoom) loadRacks(selectedRoom.id); if (selectedRack) loadUnits(selectedRack.id) }}><RefreshCw className="h-4 w-4" />刷新</Button>}
        </div>
      </div>

      {view2D ? (
        /* ============ 2D 效果图(模型驱动) ============ */
        <div className="space-y-4">
          {/* 机房选择 + 容量统计 */}
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-xs text-muted-foreground">机房:</span>
            <button onClick={() => { setPanoRoom(''); setPanoRack(null) }} className={cn('px-3 py-1 rounded-full text-xs border', panoRoom === '' ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600' : 'border-gray-200 dark:border-gray-700')}>全部</button>
            {panoRooms.map(rid => (
              <button key={rid} onClick={() => { setPanoRoom(rid); setPanoRack(null) }} className={cn('px-3 py-1 rounded-full text-xs border', panoRoom === rid ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600' : 'border-gray-200 dark:border-gray-700')}>{roomName(rid)}</button>
            ))}
          </div>

          {/* 容量统计卡片 */}
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
            {[
              { label: '机柜', value: panoStats.rackCount },
              { label: '设备', value: panoStats.devCount },
              { label: '总 U 位', value: panoStats.totalU },
              { label: '已占用 U', value: panoStats.usedU },
              { label: '剩余 U', value: panoStats.freeU },
              { label: '占用率', value: `${panoStats.usage}%` },
            ].map(s => (
              <div key={s.label} style={{ background: 'var(--color-background-secondary)', borderRadius: 'var(--border-radius-md)', padding: '0.75rem 1rem' }}>
                <p style={{ fontSize: 12, color: 'var(--color-text-secondary)', margin: 0 }}>{s.label}</p>
                <p style={{ fontSize: 20, fontWeight: 500, margin: 0 }}>{s.value}</p>
              </div>
            ))}
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-4 gap-4">
            {/* 未上架设备（可拖拽上架） */}
            <Card className="lg:col-span-1 h-fit">
              <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Gauge className="h-4 w-4" />未上架设备（{freeDevices.length}）</CardTitle></CardHeader>
              <CardContent className="p-3 max-h-[480px] overflow-y-auto space-y-1">
                {freeDevices.length === 0 ? (
                  <p className="text-xs text-muted-foreground text-center py-6">全部设备已上架</p>
                ) : freeDevices.map(ci => (
                  <div key={ci.id}
                    draggable={!!panoRack && !assigning}
                    onDragStart={onDeviceDragStart(ci)}
                    onClick={() => panoRack && highlightRecommend(ci, panoRack)}
                    title={panoRack ? '拖到机柜正视图 U 位上架，或点击高亮推荐空位' : '进入机柜正视图后，可拖拽上架'}
                    className={cn('border rounded-md px-2.5 py-1.5 text-xs flex items-center gap-2 cursor-grab active:cursor-grabbing transition-colors',
                      panoRack ? 'hover:border-emerald-500 hover:bg-emerald-50 dark:hover:bg-emerald-500/10' : 'opacity-70 cursor-default')}>
                    <span className={cn('h-2 w-2 rounded-full shrink-0', deviceColor(ci.type_id || ''))} />
                    <span className="font-medium truncate">{ci.name}</span>
                    <Badge variant="outline" className="text-[9px] ml-auto shrink-0">{ci.type_id}</Badge>
                  </div>
                ))}
              </CardContent>
            </Card>

            {/* 平面图 / 机柜正视图 */}
            <div className="lg:col-span-3 space-y-4">
              {panoRack ? (
                /* 机柜正视图: U 位从下到上, 多 U 设备跨行, 支持拖拽上架/迁移 */
                <Card>
                  <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
                    <CardTitle className="text-sm flex items-center gap-2">
                      机柜 {panoRack.name || panoRack.id} 正视图
                      <Badge variant="outline" className="text-[9px]">{panoFace === 'front' ? '正面' : '背面'}</Badge>
                    </CardTitle>
                    <div className="flex items-center gap-2">
                      <Button variant="outline" size="sm" onClick={() => setPanoFace(f => f === 'front' ? 'rear' : 'front')}>
                        <ArrowLeftRight className="h-3 w-3 mr-1" />{panoFace === 'front' ? '背面' : '正面'}
                      </Button>
                      <Button variant="outline" size="sm" onClick={() => { setPanoRack(null); setRecUnits([]) }}>返回平面图</Button>
                    </div>
                  </CardHeader>
                  <CardContent>
                    <div className="flex gap-2 justify-center">
                      {pduBar('L')}
                      <div className="flex-1 max-w-[420px]">{rackElevation(panoRack)}</div>
                      {pduBar('R')}
                    </div>
                    <p className="text-[10px] text-muted-foreground mt-3 text-center">
                      {panoFace === 'front' ? '正面视图' : '背面视图（设备同构展示）'} · 拖拽「未上架设备」到空 U 位即可上架 · 点击设备可拖动迁移 · 绿格为点击推荐的可放位置
                    </p>
                  </CardContent>
                </Card>
              ) : (
                /* 机房平面图: 机柜按 row/col 网格定位 */
                <Card>
                  <CardHeader className="py-3"><CardTitle className="text-sm">机房平面图（机柜位置 · 按排/列定位）</CardTitle></CardHeader>
                  <CardContent>
                    {roomGrid.pos.length === 0 && roomGrid.rest.length === 0 ? (
                      <p className="text-sm text-muted-foreground py-8 text-center">暂无机柜数据。可在「管理」视图创建机房/机柜（填写排、列、U 位总数），或录入 rack 配置项后自动成图。</p>
                    ) : (
                      <div className="space-y-4">
                        {roomGrid.pos.length > 0 && (
                          <div className="grid gap-2" style={{ gridTemplateColumns: `repeat(${roomGrid.cols.length}, minmax(56px, 1fr))` }}>
                            {roomGrid.pos.map(rk => {
                              const devs = rackDevices(rk.id)
                              const maxU = Number(rk.attributes?.max_units || 42)
                              const usage = maxU > 0 ? Math.min(100, Math.round(devs.reduce((s, d) => s + devHeight(d), 0) / maxU * 100)) : 0
                              return (
                                <button key={rk.id} onClick={() => setPanoRack(rk)} title={`${rk.name || rk.id} · ${devs.length} 台设备 · ${usage}%`}
                                  style={{ gridColumn: roomGrid.cols.indexOf(rk.attributes?.col) + 1, gridRow: roomGrid.rows.indexOf(rk.attributes?.row) + 1 }}
                                  className={cn('border rounded p-1.5 text-center hover:border-emerald-500 transition-colors', usage >= 80 ? 'bg-red-50 border-red-300 dark:bg-red-500/10' : usage > 0 ? 'bg-amber-50 border-amber-300 dark:bg-amber-500/10' : 'bg-muted/30')}>
                                  <Rows3 className="h-4 w-4 mx-auto text-muted-foreground" />
                                  <p className="text-[9px] font-medium truncate mt-0.5">{rk.name || rk.id.slice(0, 8)}</p>
                                  <p className="text-[8px] text-muted-foreground">{rk.attributes?.row}{rk.attributes?.col ? `-${rk.attributes.col}` : ''} · {devs.length}台{usage > 0 ? ` ${usage}%` : ''}</p>
                                </button>
                              )
                            })}
                          </div>
                        )}
                        {roomGrid.rest.length > 0 && (
                          <div className="border-t border-border/60 pt-3">
                            <p className="text-[10px] text-muted-foreground mb-2">未设置排/列的机柜：</p>
                            <div className="grid gap-2" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(56px, 1fr))' }}>
                              {roomGrid.rest.map(rk => (
                                <button key={rk.id} onClick={() => setPanoRack(rk)} title={rk.name || rk.id}
                                  className="border rounded p-1.5 text-center hover:border-emerald-500 transition-colors bg-muted/30">
                                  <Rows3 className="h-4 w-4 mx-auto text-muted-foreground" />
                                  <p className="text-[9px] font-medium truncate mt-0.5">{rk.name || rk.id.slice(0, 8)}</p>
                                </button>
                              ))}
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
                          <Badge variant="outline" className="text-[9px]">{d.type_id}</Badge>
                          <span className="text-muted-foreground ml-auto">U{start}{h > 1 ? `-${start + h - 1}` : ''}</span>
                          {down && <Badge className="text-[9px] bg-red-500">宕机</Badge>}
                          {crit > 0 && <Badge className="text-[9px] bg-red-500">{crit} 严重</Badge>}
                          {warn > 0 && <Badge className="text-[9px] bg-amber-500">{warn} 警告</Badge>}
                        </div>
                      )
                    })}
                  </CardContent>
                </Card>
              )}
            </div>
          </div>
        </div>
      ) : (
      <>
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* 机房列表 */}
        <Card>
          <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
            <CardTitle className="text-sm flex items-center gap-2"><Building2 className="h-4 w-4" />机房（{rooms.length}）</CardTitle>
            <Button size="sm" variant="outline" onClick={() => setRoomDialog(true)}><Plus className="h-3 w-3" />新建机房</Button>
          </CardHeader>
          <CardContent className="p-0">
            {rooms.length === 0 ? <p className="px-4 py-6 text-sm text-muted-foreground text-center">暂无机房</p> : rooms.map(r => (
              <div key={r.id} onClick={() => selectRoom(r)} className={cn('px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors flex items-center justify-between', selectedRoom?.id === r.id && 'bg-accent border-l-2 border-l-emerald-500')}>
                <div>
                  <p className="text-sm font-medium">{r.name}</p>
                  <p className="text-xs text-muted-foreground">{r.location || '未填位置'}{r.description ? ` · ${r.description}` : ''}</p>
                </div>
                <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); openDelete('room', r) }}><Trash2 className="h-3 w-3" /></Button>
              </div>
            ))}
          </CardContent>
        </Card>

        {/* 机柜列表 */}
        <Card>
          <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
            <CardTitle className="text-sm flex items-center gap-2"><Server className="h-4 w-4" />机柜{selectedRoom ? ` · ${selectedRoom.name}` : ''}（{racks.length}）</CardTitle>
            {selectedRoom && <Button size="sm" variant="outline" onClick={() => setRackDialog(true)}><Plus className="h-3 w-3" />新建机柜</Button>}
          </CardHeader>
          <CardContent className="p-0">
            {!selectedRoom ? <p className="px-4 py-6 text-sm text-muted-foreground text-center">先选择一个机房</p> : racks.length === 0 ? <p className="px-4 py-6 text-sm text-muted-foreground text-center">暂无机柜</p> : racks.map(rk => (
              <div key={rk.id} onClick={() => selectRack(rk)} className={cn('px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors flex items-center justify-between', selectedRack?.id === rk.id && 'bg-accent border-l-2 border-l-emerald-500')}>
                <div className="flex items-center gap-2">
                  <Rows3 className="h-4 w-4 text-muted-foreground" />
                  <div>
                    <p className="text-sm font-medium">{rk.name}</p>
                    <p className="text-xs text-muted-foreground">{rk.max_units}U{ rk.row ? ` · ${rk.row}` : ''}{rk.col ? ` · ${rk.col}` : ''}</p>
                  </div>
                </div>
                <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); openDelete('rack', rk) }}><Trash2 className="h-3 w-3" /></Button>
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
                      <div key={u} onClick={() => openAssign(u)} title={occ?.ci_id ? `${occ.label || occ.ci_id}（CI）` : occ?.label || '空闲'} className={cn('h-5 rounded-sm border text-[10px] flex items-center px-1.5 cursor-pointer transition-colors truncate', used ? 'border-emerald-400 bg-emerald-100 text-emerald-800 dark:bg-emerald-500/20 dark:text-emerald-300' : 'border-dashed border-gray-300 dark:border-gray-600 text-muted-foreground hover:bg-accent')}>
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
      <Dialog open={roomDialog} onClose={() => setRoomDialog(false)} title="新建机房">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">机房名称 *</label><Input value={roomForm.name} onChange={e => setRoomForm(f => ({ ...f, name: e.target.value }))} placeholder="如：华东一区" /></div>
          <div><label className="text-sm font-medium">位置</label><Input value={roomForm.location} onChange={e => setRoomForm(f => ({ ...f, location: e.target.value }))} placeholder="如：上海市青浦区" /></div>
          <div><label className="text-sm font-medium">描述</label><Input value={roomForm.description} onChange={e => setRoomForm(f => ({ ...f, description: e.target.value }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setRoomDialog(false)}>取消</Button><Button onClick={createRoom} disabled={!roomForm.name.trim()}>创建</Button></DialogFooter>
      </Dialog>

      {/* 新建机柜 */}
      <Dialog open={rackDialog} onClose={() => setRackDialog(false)} title={`新建机柜 · ${selectedRoom?.name || ''}`}>
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">机柜编号 *</label><Input value={rackForm.name} onChange={e => setRackForm(f => ({ ...f, name: e.target.value }))} placeholder="R01" /></div>
            <div><label className="text-sm font-medium">U 位总数</label><Input type="number" value={rackForm.max_units} onChange={e => setRackForm(f => ({ ...f, max_units: Number(e.target.value) || 42 }))} /></div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">排</label><Input value={rackForm.row} onChange={e => setRackForm(f => ({ ...f, row: e.target.value }))} placeholder="A 排" /></div>
            <div><label className="text-sm font-medium">列</label><Input value={rackForm.col} onChange={e => setRackForm(f => ({ ...f, col: e.target.value }))} placeholder="1" /></div>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setRackDialog(false)}>取消</Button><Button onClick={createRack} disabled={!rackForm.name.trim()}>创建</Button></DialogFooter>
      </Dialog>

      {/* U 位分配 */}
      <Dialog open={unitDialog} onClose={() => setUnitDialog(false)} title={`分配 U 位 ${unitForm.unit}`}>
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">绑定 CI 资产（可选）</label>
            <select value={unitForm.ci_id} onChange={e => setUnitForm(f => ({ ...f, ci_id: e.target.value }))} className="w-full mt-1 rounded-md border border-input bg-background px-2 py-1.5 text-sm">
              <option value="">（不绑定，仅填标签）</option>
              {cis.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </div>
          <div><label className="text-sm font-medium">标签</label><Input value={unitForm.label} onChange={e => setUnitForm(f => ({ ...f, label: e.target.value }))} placeholder="设备名/备注" /></div>
        </div>
        <DialogFooter>
          {units[unitForm.unit]?.ci_id && <Button variant="outline" onClick={() => { releaseUnit(unitForm.unit); setUnitDialog(false) }}><Unplug className="h-3 w-3 mr-1" />释放</Button>}
          <Button variant="outline" onClick={() => setUnitDialog(false)}>取消</Button>
          <Button onClick={assignUnit}>保存</Button>
        </DialogFooter>
      </Dialog>

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title="确认删除"
        description={deleteTarget ? (deleteTarget.kind === 'room' ? `确认删除机房「${(deleteTarget.item as IDCRoom).name}」及其全部机柜？` : `确认删除机柜「${(deleteTarget.item as Rack).name}」及其 U 位占用？`) : ''}
        confirmLabel="删除"
        loading={deleteSubmitting}
      />
      </>
      )}
    </div>
  )
}
