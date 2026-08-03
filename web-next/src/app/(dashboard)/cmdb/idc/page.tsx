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
          <p className="text-sm text-muted-foreground">IDC 机房 / 机柜 / U 位可视化管理与设备占用</p>
        </div>
        <Button variant="outline" onClick={() => { loadRooms(); if (selectedRoom) loadRacks(selectedRoom.id); if (selectedRack) loadUnits(selectedRack.id) }}><RefreshCw className="h-4 w-4" />刷新</Button>
      </div>

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
    </div>
  )
}
