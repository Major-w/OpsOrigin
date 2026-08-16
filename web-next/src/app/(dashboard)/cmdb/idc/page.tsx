'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, apiList, apiBase } from '@/lib/utils'
import { Plus, Trash2, Building2, Server, RefreshCw, Rows3, Unplug } from 'lucide-react'
import { cn } from '@/lib/utils'
import { toast } from '@/stores/toast'

interface IDCRoom { id: string; name: string; location?: string; description?: string }
interface Rack { id: string; idc_id: string; name: string; row?: string; col?: string; max_units: number }
interface RackUnit { rack_id: string; unit: number; ci_id?: string; label?: string }
interface CIModel { id: string; name: string }

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

  const deleteRoom = async (room: IDCRoom) => {
    if (!confirm(`确认删除机房「${room.name}」及其全部机柜？`)) return
    try { await apiFetch(`/cmdb/idc/${room.id}`, { method: 'DELETE' }); toast.success('已删除'); if (selectedRoom?.id === room.id) selectRoom(null); loadRooms() }
    catch (e: any) { toast.error(e.message) }
  }

  const createRack = async () => {
    if (!selectedRoom || !rackForm.name.trim()) { toast.error('请输入机柜编号'); return }
    try {
      await apiFetch(`/cmdb/idc/${selectedRoom.id}/racks`, { method: 'POST', body: JSON.stringify(rackForm) })
      toast.success('机柜已创建'); setRackDialog(false); setRackForm({ name: '', row: '', col: '', max_units: 42 }); loadRacks(selectedRoom.id)
    } catch (e: any) { toast.error(e.message) }
  }

  const deleteRack = async (rack: Rack) => {
    if (!confirm(`确认删除机柜「${rack.name}」及其 U 位占用？`)) return
    try { await apiFetch(`/cmdb/racks/${rack.id}`, { method: 'DELETE' }); toast.success('已删除'); if (selectedRack?.id === rack.id) selectRack(null); loadRacks(selectedRoom!.id) }
    catch (e: any) { toast.error(e.message) }
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

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">机房机柜视图</h1>
          <p className="text-sm text-muted-foreground">{view2D ? '2D 效果图（模型驱动：录入 rack 与设备 rack_id/rack_unit 自动成图）' : 'IDC 机房 / 机柜 / U 位可视化管理与设备占用'}</p>
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
          {/* 机房选择 */}
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-xs text-muted-foreground">机房:</span>
            <button onClick={() => { setPanoRoom(''); setPanoRack(null) }} className={cn('px-3 py-1 rounded-full text-xs border', panoRoom === '' ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600' : 'border-gray-200 dark:border-gray-700')}>全部</button>
            {panoRooms.map(rid => (
              <button key={rid} onClick={() => { setPanoRoom(rid); setPanoRack(null) }} className={cn('px-3 py-1 rounded-full text-xs border', panoRoom === rid ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600' : 'border-gray-200 dark:border-gray-700')}>{rid.slice(0, 12)}</button>
            ))}
            <span className="text-xs text-muted-foreground ml-auto">机柜 {roomRacks().length} · 设备 {panorama.devices.length}</span>
          </div>

          {panoRack ? (
            /* 机柜正视图: U 位从下到上 */
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
              <Card className="lg:col-span-1">
                <CardHeader className="py-3"><CardTitle className="text-sm">机柜 {panoRack.name || panoRack.id} 正视图</CardTitle></CardHeader>
                <CardContent>
                  <div className="space-y-0.5">
                    {(() => {
                      const maxU = Number(panoRack.attributes?.max_units || 42)
                      const devs = rackDevices(panoRack.id)
                      const unitOf = (d: any) => Number(d.attributes?.rack_unit || 0)
                      return Array.from({ length: maxU }, (_, i) => maxU - i).map(u => {
                        const d = devs.find(x => unitOf(x) === u)
                        return (
                          <div key={u} className={cn('flex items-center gap-2 px-2 py-0.5 rounded text-[10px] border', d ? 'border-transparent text-white' : 'border-dashed border-gray-200 dark:border-gray-700 text-muted-foreground')}>
                            <span className="w-6 text-right shrink-0">{u}</span>
                            <div className={cn('flex-1 rounded px-1.5 py-0.5 truncate', d ? deviceColor(d.type_id) : 'bg-muted/40')}>
                              {d ? `${d.name} · ${d.type_id}` : '空'}
                            </div>
                          </div>
                        )
                      })
                    })()}
                  </div>
                  <Button variant="outline" size="sm" className="mt-3" onClick={() => setPanoRack(null)}>返回平面图</Button>
                </CardContent>
              </Card>
              <div className="lg:col-span-2 space-y-2">
                <p className="text-xs text-muted-foreground">该机柜内设备（{rackDevices(panoRack.id).length}）</p>
                {rackDevices(panoRack.id).map(d => (
                  <div key={d.id} className="border rounded-md px-3 py-2 text-xs flex items-center gap-2">
                    <span className={cn('h-2 w-2 rounded-full', deviceColor(d.type_id))} />
                    <span className="font-medium">{d.name}</span>
                    <Badge variant="outline" className="text-[9px]">{d.type_id}</Badge>
                    <span className="text-muted-foreground ml-auto">U{d.attributes?.rack_unit}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            /* 机房平面图: 机柜按 row/col 网格 */
            <Card>
              <CardHeader className="py-3"><CardTitle className="text-sm">机房平面图（机柜位置）</CardTitle></CardHeader>
              <CardContent>
                {roomRacks().length === 0 ? (
                  <p className="text-sm text-muted-foreground py-8 text-center">暂无机柜数据。请在模型管理中录入 rack 配置项（属性：room_id / row / col / max_units），并为设备设置 rack_id / rack_unit 后自动成图。</p>
                ) : (
                  <div className="grid gap-2" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(56px, 1fr))' }}>
                    {roomRacks().map(rk => {
                      const devs = rackDevices(rk.id)
                      const usage = rk.attributes?.max_units ? Math.round(devs.length / Number(rk.attributes.max_units) * 100) : 0
                      return (
                        <button key={rk.id} onClick={() => setPanoRack(rk)} title={`${rk.name || rk.id} · ${devs.length} 台设备`}
                          className={cn('border rounded p-1.5 text-center hover:border-emerald-500 transition-colors', usage >= 80 ? 'bg-red-50 border-red-300 dark:bg-red-500/10' : usage > 0 ? 'bg-amber-50 border-amber-300 dark:bg-amber-500/10' : 'bg-muted/30')}>
                          <Rows3 className="h-4 w-4 mx-auto text-muted-foreground" />
                          <p className="text-[9px] font-medium truncate mt-0.5">{rk.name || rk.id.slice(0, 8)}</p>
                          <p className="text-[8px] text-muted-foreground">{devs.length}台{usage > 0 ? ` ${usage}%` : ''}</p>
                        </button>
                      )
                    })}
                  </div>
                )}
              </CardContent>
            </Card>
          )}
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
                <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); deleteRoom(r) }}><Trash2 className="h-3 w-3" /></Button>
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
                <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); deleteRack(rk) }}><Trash2 className="h-3 w-3" /></Button>
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
      </>
      )}
    </div>
  )
}
