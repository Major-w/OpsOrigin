import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Modal, Input, InputNumber, Select, Popconfirm, Segmented, Empty } from 'antd'
import {
  Building2, Server, Plus, Trash2, RefreshCw, Search, Grid3x3, Box,
  AlertTriangle, Network, Unplug, Cpu,
} from 'lucide-react'
import { apiFetch, apiList, cn } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { IsometricRoom, type IsoRack } from './isometric-room'
import { RackUnitView, type RackDevice } from './rack-unit-view'

// ============================================================================
// 机房机柜工作台
// 左：机房列表；中：平面图 / 3D 机房；右：容量与告警统计。
// 点击机柜 → 机柜 U 位视图（42→1，可绑定/解绑设备）。
// ============================================================================

interface IDCRoom { id: string; name: string; location?: string; description?: string }
interface RackRow { id: string; name: string; row: string; col: string; maxUnits: number }
interface CISlim { id: string; name: string; type_id?: string; status?: string }

const DEVICE_TYPES = ['server', 'host', 'storage', 'firewall', 'load_balancer', 'router', 'switch', 'database', 'middleware', 'app']
const NET_TYPES = ['switch', 'router', 'firewall', 'load_balancer']

export default function IdcWorkspace() {
  const navigate = useNavigate()
  const [rooms, setRooms] = useState<IDCRoom[]>([])
  const [roomId, setRoomId] = useState('')
  const [panorama, setPanorama] = useState<{ racks: any[]; devices: any[] }>({ racks: [], devices: [] })
  const [cis, setCis] = useState<CISlim[]>([])
  const [loading, setLoading] = useState(false)
  const [view, setView] = useState<'plan' | '3d'>('plan')
  const [keyword, setKeyword] = useState('')
  const [activeRack, setActiveRack] = useState<RackRow | null>(null)
  const [isoSelected, setIsoSelected] = useState<string | undefined>()

  // 弹窗
  const [roomOpen, setRoomOpen] = useState(false)
  const [roomForm, setRoomForm] = useState({ name: '', location: '', description: '' })
  const [rackOpen, setRackOpen] = useState(false)
  const [rackForm, setRackForm] = useState({ name: '', row: 'A', col: '01', max_units: 42 })
  const [bindUnit, setBindUnit] = useState<{ rack: RackRow; unit: number } | null>(null)
  const [bindCI, setBindCI] = useState('')
  const [bindHeight, setBindHeight] = useState(1)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [rs, pano] = await Promise.all([
        apiList<IDCRoom>('/cmdb/idc').catch(() => [] as IDCRoom[]),
        apiFetch<{ racks: any[]; devices: any[] }>('/cmdb/idc/panorama').catch(() => ({ racks: [], devices: [] })),
      ])
      setRooms(rs)
      setPanorama(pano || { racks: [], devices: [] })
    } finally { setLoading(false) }
  }, [])

  useEffect(() => {
    load()
    apiList<CISlim>('/cmdb/ci?limit=2000').then(setCis).catch(() => setCis([]))
  }, [load])

  const currentRoom = rooms.find(r => r.id === roomId) || null

  /** 全景机柜 → 规范化行 */
  const roomRacks: RackRow[] = useMemo(() => {
    const list = panorama.racks.filter(r => !roomId || r.attributes?.room_id === roomId)
    return list.map(r => ({
      id: r.id,
      name: r.name || r.id.slice(0, 8),
      row: String(r.attributes?.row || ''),
      col: String(r.attributes?.col || ''),
      maxUnits: Number(r.attributes?.max_units || 42),
    }))
  }, [panorama.racks, roomId])

  const devicesOf = useCallback(
    (rackID: string) => panorama.devices.filter(d => d.attributes?.rack_id === rackID),
    [panorama.devices],
  )

  /** 机柜 → 等距视图所需的聚合数据 */
  const isoRacks: IsoRack[] = useMemo(() => {
    const rows = roomRacks.filter(r => keyword.trim() === '' || r.name.toLowerCase().includes(keyword.trim().toLowerCase()))
    return rows.map((r, i) => {
      const devs = devicesOf(r.id)
      const used = devs.reduce((s, d) => s + Math.max(1, Number(d.attributes?.u_height || 1)), 0)
      let critical = 0, warning = 0
      for (const d of devs) {
        critical += Number(d.attributes?.alert_critical || 0)
        warning += Number(d.attributes?.alert_warning || 0)
      }
      // row/col 缺失时按顺序自动排布，保证 3D 视图有网格
      const hasPos = r.row !== '' && r.col !== ''
      return {
        id: r.id,
        name: r.name,
        maxUnits: r.maxUnits,
        usedUnits: used,
        critical,
        warning,
        deviceCount: devs.length,
        row: hasPos ? Number(r.row.replace(/\D/g, '') || 0) : Math.floor(i / 6),
        col: hasPos ? Number(r.col.replace(/\D/g, '') || 0) : i % 6,
        online: true,
      }
    })
  }, [roomRacks, devicesOf, keyword])

  /** 右侧统计 */
  const stats = useMemo(() => {
    const devs = roomRacks.flatMap(r => devicesOf(r.id))
    const totalU = roomRacks.reduce((s, r) => s + r.maxUnits, 0)
    const usedU = devs.reduce((s, d) => s + Math.max(1, Number(d.attributes?.u_height || 1)), 0)
    let critical = 0, warning = 0
    for (const d of devs) {
      critical += Number(d.attributes?.alert_critical || 0)
      warning += Number(d.attributes?.alert_warning || 0)
    }
    const abnormalRacks = roomRacks.filter(r => devicesOf(r.id).some(d => Number(d.attributes?.alert_critical || 0) > 0)).length
    const netDevices = devs.filter(d => NET_TYPES.includes(d.type_id || '')).length
    return {
      rackCount: roomRacks.length,
      devCount: devs.length,
      totalU, usedU,
      freeU: Math.max(0, totalU - usedU),
      usage: totalU > 0 ? Math.round(usedU / totalU * 100) : 0,
      critical, warning,
      normal: Math.max(0, devs.length - abnormalRacks),
      abnormalRacks, netDevices,
    }
  }, [roomRacks, devicesOf])

  /** 未上架设备（用于 U 位绑定） */
  const freeDevices = useMemo(() => {
    const racked = new Set(panorama.devices.map(d => d.id))
    return cis.filter(c => DEVICE_TYPES.includes(c.type_id || '') && !racked.has(c.id))
  }, [cis, panorama.devices])

  const activeRackDevices: RackDevice[] = useMemo(() => {
    if (!activeRack) return []
    return devicesOf(activeRack.id).map(d => ({
      id: d.id,
      name: d.name || d.id.slice(0, 8),
      typeId: d.type_id,
      ip: d.attributes?.ip_address,
      startUnit: Number(d.attributes?.rack_unit || 1),
      height: Math.max(1, Number(d.attributes?.u_height || 1)),
      critical: Number(d.attributes?.alert_critical || 0),
      warning: Number(d.attributes?.alert_warning || 0),
      status: d.attributes?.health || d.status,
    }))
  }, [activeRack, devicesOf])

  // ---- 操作 ----
  const createRoom = async () => {
    if (!roomForm.name.trim()) { getMessage().error('请填写机房名称'); return }
    setSaving(true)
    try {
      await apiFetch('/cmdb/idc', { method: 'POST', body: JSON.stringify(roomForm) })
      getMessage().success('机房已创建')
      setRoomOpen(false); setRoomForm({ name: '', location: '', description: '' })
      await load()
    } catch (e: any) { getMessage().error('创建失败：' + (e?.message || '')) } finally { setSaving(false) }
  }

  const deleteRoom = async (id: string) => {
    try {
      await apiFetch(`/cmdb/idc/${id}`, { method: 'DELETE' })
      getMessage().success('已删除')
      if (roomId === id) setRoomId('')
      await load()
    } catch (e: any) { getMessage().error('删除失败：' + (e?.message || '')) }
  }

  const createRack = async () => {
    if (!roomId) { getMessage().error('请先选择机房'); return }
    if (!rackForm.name.trim()) { getMessage().error('请填写机柜名称'); return }
    setSaving(true)
    try {
      await apiFetch(`/cmdb/idc/${roomId}/racks`, { method: 'POST', body: JSON.stringify(rackForm) })
      getMessage().success('机柜已创建')
      setRackOpen(false)
      await load()
    } catch (e: any) { getMessage().error('创建失败：' + (e?.message || '')) } finally { setSaving(false) }
  }

  const deleteRack = async (id: string) => {
    try {
      await apiFetch(`/cmdb/racks/${id}`, { method: 'DELETE' })
      getMessage().success('机柜已删除')
      if (activeRack?.id === id) setActiveRack(null)
      await load()
    } catch (e: any) { getMessage().error('删除失败：' + (e?.message || '')) }
  }

  const bindDevice = async () => {
    if (!bindUnit) return
    if (!bindCI) { getMessage().error('请选择要绑定的设备'); return }
    setSaving(true)
    try {
      await apiFetch(`/cmdb/racks/${bindUnit.rack.id}/units`, {
        method: 'POST',
        body: JSON.stringify({ units: [{ unit: bindUnit.unit, units: bindHeight, ci_id: bindCI }] }),
      })
      getMessage().success('已绑定到该 U 位')
      setBindUnit(null); setBindCI(''); setBindHeight(1)
      await load()
    } catch (e: any) { getMessage().error('绑定失败：' + (e?.message || '')) } finally { setSaving(false) }
  }

  const unbindDevice = async (dev: RackDevice) => {
    if (!activeRack) return
    try {
      await apiFetch(`/cmdb/racks/${activeRack.id}/units`, {
        method: 'POST',
        body: JSON.stringify({ units: [{ unit: dev.startUnit, units: dev.height, ci_id: '' }] }),
      })
      getMessage().success('已解绑')
      await load()
    } catch (e: any) { getMessage().error('解绑失败：' + (e?.message || '')) }
  }

  return (
    <div className="flex h-[calc(100vh-116px)] min-h-[600px] gap-3">
      {/* ---------- 左：机房列表 ---------- */}
      <aside className="flex w-[196px] shrink-0 flex-col rounded-lg border border-border bg-card">
        <div className="flex items-center justify-between border-b border-border px-3 py-2">
          <span className="flex items-center gap-1.5 text-xs font-medium"><Building2 className="h-3.5 w-3.5" />机房</span>
          <Button size="small" type="text" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setRoomOpen(true)} />
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
          <button
            onClick={() => setRoomId('')}
            className={cn('mb-1 w-full rounded-md px-2 py-1.5 text-left text-xs transition-colors',
              roomId === '' ? 'bg-blue-500/15 text-blue-600 dark:text-blue-300' : 'hover:bg-muted')}
          >全部机房（{rooms.length}）</button>
          {rooms.map(r => (
            <div key={r.id} className={cn('group mb-1 flex items-center gap-1 rounded-md px-2 py-1.5 transition-colors',
              roomId === r.id ? 'bg-blue-500/15' : 'hover:bg-muted')}>
              <button onClick={() => setRoomId(r.id)} className="min-w-0 flex-1 text-left">
                <span className={cn('block truncate text-xs', roomId === r.id && 'font-medium text-blue-600 dark:text-blue-300')}>{r.name}</span>
                {r.location && <span className="block truncate text-[10px] text-muted-foreground">{r.location}</span>}
              </button>
              <Popconfirm title="删除该机房及其机柜？" onConfirm={() => deleteRoom(r.id)}>
                <Trash2 className="h-3.5 w-3.5 shrink-0 cursor-pointer text-muted-foreground opacity-0 transition-opacity hover:text-red-500 group-hover:opacity-100" />
              </Popconfirm>
            </div>
          ))}
          {!rooms.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="text-[11px] text-muted-foreground">暂无机房</span>} />}
        </div>
      </aside>

      {/* ---------- 中：视图 ---------- */}
      <section className="flex min-w-0 flex-1 flex-col rounded-lg border border-border bg-card">
        <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
          <span className="text-xs font-medium">{currentRoom ? currentRoom.name : '全部机房'}</span>
          <span className="text-[11px] text-muted-foreground">{stats.rackCount} 个机柜 · {stats.devCount} 台设备</span>
          <div className="ml-auto flex items-center gap-2">
            <Input
              size="small" allowClear prefix={<Search className="h-3.5 w-3.5 text-muted-foreground" />}
              placeholder="搜索机柜" value={keyword} onChange={e => setKeyword(e.target.value)} style={{ width: 150 }}
            />
            <Segmented
              size="small" value={view} onChange={v => setView(v as 'plan' | '3d')}
              options={[
                { label: <span className="flex items-center gap-1 text-xs"><Grid3x3 className="h-3.5 w-3.5" />平面图</span>, value: 'plan' },
                { label: <span className="flex items-center gap-1 text-xs"><Box className="h-3.5 w-3.5" />3D 机房</span>, value: '3d' },
              ]}
            />
            <Button size="small" icon={<Plus className="h-3.5 w-3.5" />} disabled={!roomId} onClick={() => setRackOpen(true)}>新建机柜</Button>
            <Button size="small" icon={<RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />} onClick={load} />
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-auto p-3">
          {view === '3d' ? (
            <IsometricRoom
              racks={isoRacks}
              selectedId={isoSelected}
              onSelect={id => {
                setIsoSelected(id)
                const r = roomRacks.find(x => x.id === id)
                if (r) setActiveRack(r)
              }}
            />
          ) : (
            <PlanGrid
              racks={roomRacks.filter(r => keyword.trim() === '' || r.name.toLowerCase().includes(keyword.trim().toLowerCase()))}
              devicesOf={devicesOf}
              onOpen={setActiveRack}
              onDelete={deleteRack}
            />
          )}
        </div>
      </section>

      {/* ---------- 右：统计 ---------- */}
      <aside className="hidden w-[228px] shrink-0 flex-col gap-3 overflow-y-auto xl:flex">
        <div className="grid grid-cols-2 gap-2">
          <MiniCard icon={Cpu} label="U 位异常机柜" value={stats.abnormalRacks} tone={stats.abnormalRacks ? 'warn' : 'ok'} />
          <MiniCard icon={AlertTriangle} label="告警总数" value={stats.critical + stats.warning} tone={stats.critical ? 'danger' : stats.warning ? 'warn' : 'ok'} />
        </div>

        <div className="rounded-lg border border-border bg-card p-3">
          <p className="text-xs font-medium">告警分级</p>
          <div className="mt-2 grid grid-cols-3 gap-2 text-center">
            <Level label="紧急" value={stats.critical} tone="text-red-500" />
            <Level label="告警" value={stats.warning} tone="text-amber-500" />
            <Level label="正常" value={stats.normal} tone="text-emerald-500" />
          </div>
        </div>

        <div className="grid grid-cols-2 gap-2">
          <MiniCard icon={Server} label="机柜总数" value={stats.rackCount} tone="info" />
          <MiniCard icon={Unplug} label="设备总数" value={stats.devCount} tone="info" />
        </div>

        <div className="rounded-lg border border-border bg-card p-3">
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1.5 text-xs font-medium"><Network className="h-3.5 w-3.5" />网络设备</span>
            <span className="text-sm font-semibold tabular-nums">{stats.netDevices}</span>
          </div>
          <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted">
            <div className="h-full rounded-full bg-blue-500" style={{ width: `${stats.devCount ? Math.round(stats.netDevices / stats.devCount * 100) : 0}%` }} />
          </div>
          <p className="mt-1 text-[10px] text-muted-foreground">占全部设备 {stats.devCount ? Math.round(stats.netDevices / stats.devCount * 100) : 0}%</p>
        </div>

        <div className="rounded-lg border border-border bg-card p-3">
          <p className="text-xs font-medium">机房容量</p>
          <div className="mt-2 flex items-center gap-3">
            <CapacityRing value={stats.usage} />
            <div className="space-y-1 text-[11px]">
              <CapRow dot="bg-blue-500" label="已使用" value={stats.usedU} />
              <CapRow dot="bg-muted-foreground/40" label="未使用" value={stats.freeU} />
              <CapRow dot="bg-slate-400" label="总 U 数" value={stats.totalU} />
            </div>
          </div>
        </div>

        <div className="rounded-lg border border-border bg-card p-3">
          <p className="text-xs font-medium">机房概况</p>
          <div className="mt-2 space-y-1.5 text-[11px]">
            <Row k="机房名称" v={currentRoom?.name || '全部'} />
            <Row k="机房位置" v={currentRoom?.location || '-'} />
            <Row k="机柜数量" v={String(stats.rackCount)} />
            <Row k="设备数量" v={String(stats.devCount)} />
            <Row k="备注" v={currentRoom?.description || '-'} />
          </div>
        </div>
      </aside>

      {/* ---------- 弹窗 ---------- */}
      <Modal title="新建机房" open={roomOpen} onCancel={() => setRoomOpen(false)} onOk={createRoom} confirmLoading={saving} destroyOnHidden>
        <div className="space-y-3 py-1">
          <Field label="机房名称"><Input value={roomForm.name} onChange={e => setRoomForm(f => ({ ...f, name: e.target.value }))} placeholder="如：广州天河机房" /></Field>
          <Field label="位置"><Input value={roomForm.location} onChange={e => setRoomForm(f => ({ ...f, location: e.target.value }))} placeholder="如：广州市天河区XX路1号3层" /></Field>
          <Field label="备注"><Input.TextArea rows={2} value={roomForm.description} onChange={e => setRoomForm(f => ({ ...f, description: e.target.value }))} /></Field>
        </div>
      </Modal>

      <Modal title={`新建机柜 · ${currentRoom?.name || ''}`} open={rackOpen} onCancel={() => setRackOpen(false)} onOk={createRack} confirmLoading={saving} destroyOnHidden>
        <div className="grid grid-cols-2 gap-3 py-1">
          <Field label="机柜名称"><Input value={rackForm.name} onChange={e => setRackForm(f => ({ ...f, name: e.target.value }))} placeholder="如：HS-C3" /></Field>
          <Field label="U 位总数"><InputNumber min={1} max={100} className="w-full" value={rackForm.max_units} onChange={v => setRackForm(f => ({ ...f, max_units: Number(v) || 42 }))} /></Field>
          <Field label="行"><Input value={rackForm.row} onChange={e => setRackForm(f => ({ ...f, row: e.target.value }))} placeholder="A" /></Field>
          <Field label="列"><Input value={rackForm.col} onChange={e => setRackForm(f => ({ ...f, col: e.target.value }))} placeholder="01" /></Field>
        </div>
      </Modal>

      <Modal
        title={`机柜视图 · ${activeRack?.name || ''}`}
        open={!!activeRack}
        onCancel={() => { setActiveRack(null); setIsoSelected(undefined) }}
        footer={null}
        width={980}
        destroyOnHidden
      >
        {activeRack && (
          <RackUnitView
            rackName={activeRack.name}
            maxUnits={activeRack.maxUnits}
            devices={activeRackDevices}
            onBind={unit => { setBindUnit({ rack: activeRack, unit }); setBindCI(''); setBindHeight(1) }}
            onUnbind={unbindDevice}
            onOpenDevice={dev => navigate(`/cmdb/instances/${dev.id}`)}
          />
        )}
      </Modal>

      <Modal
        title={bindUnit ? `绑定设备到 U${bindUnit.unit}` : ''}
        open={!!bindUnit}
        onCancel={() => setBindUnit(null)}
        onOk={bindDevice}
        confirmLoading={saving}
        destroyOnHidden
      >
        <div className="space-y-3 py-1">
          <Field label="选择设备">
            <Select
              className="w-full" showSearch optionFilterProp="label" value={bindCI || undefined}
              onChange={setBindCI} placeholder={`未上架设备（${freeDevices.length}）`}
              options={freeDevices.map(c => ({ value: c.id, label: `${c.name}${c.type_id ? ` · ${c.type_id}` : ''}` }))}
              notFoundContent="没有未上架的设备"
            />
          </Field>
          <Field label="占用 U 数">
            <InputNumber min={1} max={activeRack?.maxUnits || 42} className="w-full" value={bindHeight} onChange={v => setBindHeight(Number(v) || 1)} />
          </Field>
          <p className="text-[11px] text-muted-foreground">将从 U{bindUnit?.unit} 起连续占用；与既有设备冲突时后端会拒绝。</p>
        </div>
      </Modal>
    </div>
  )
}

// ---------------- 平面图 ----------------
function PlanGrid({ racks, devicesOf, onOpen, onDelete }: {
  racks: RackRow[]
  devicesOf: (id: string) => any[]
  onOpen: (r: RackRow) => void
  onDelete: (id: string) => void
}) {
  const { rows, cols } = useMemo(() => {
    const rs = new Set<string>(), cs = new Set<string>()
    racks.forEach(r => { if (r.row) rs.add(r.row); if (r.col) cs.add(r.col) })
    return { rows: [...rs].sort(), cols: [...cs].sort() }
  }, [racks])

  if (!racks.length) {
    return <Empty className="mt-16" description={<span className="text-xs text-muted-foreground">该机房暂无可见机柜，点击右上「新建机柜」开始规划</span>} />
  }

  const positioned = rows.length > 0 && cols.length > 0
  return (
    <div className="min-w-fit">
      {positioned ? (
        <div className="inline-grid gap-1.5" style={{ gridTemplateColumns: `28px repeat(${cols.length}, 84px)` }}>
          <span />
          {cols.map(c => <span key={c} className="text-center text-[10px] font-medium text-muted-foreground">{c}</span>)}
          {rows.map(r => (
            <div key={r} className="contents">
              <span className="self-center text-right text-[10px] font-medium text-muted-foreground">{r}</span>
              {cols.map(c => {
                const rack = racks.find(x => x.row === r && x.col === c)
                return rack
                  ? <RackTile key={rack.id} rack={rack} devices={devicesOf(rack.id)} onOpen={onOpen} onDelete={onDelete} />
                  : <div key={`${r}-${c}`} className="h-[72px] rounded border border-dashed border-border/60" />
              })}
            </div>
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(84px,1fr))] gap-1.5">
          {racks.map(rack => <RackTile key={rack.id} rack={rack} devices={devicesOf(rack.id)} onOpen={onOpen} onDelete={onDelete} />)}
        </div>
      )}
    </div>
  )
}

function RackTile({ rack, devices, onOpen, onDelete }: {
  rack: RackRow
  devices: any[]
  onOpen: (r: RackRow) => void
  onDelete: (id: string) => void
}) {
  const used = devices.reduce((s, d) => s + Math.max(1, Number(d.attributes?.u_height || 1)), 0)
  const usage = rack.maxUnits > 0 ? Math.min(100, Math.round(used / rack.maxUnits * 100)) : 0
  const critical = devices.reduce((s, d) => s + Number(d.attributes?.alert_critical || 0), 0)
  const warning = devices.reduce((s, d) => s + Number(d.attributes?.alert_warning || 0), 0)
  const tone = critical > 0 ? 'border-red-400' : warning > 0 ? 'border-amber-400' : 'border-slate-600/70'

  return (
    <div className={cn('group relative flex h-[72px] flex-col items-center justify-center rounded border bg-slate-800/90 transition-all hover:border-blue-400 hover:shadow-md', tone)}>
      <button onClick={() => onOpen(rack)} className="flex h-full w-full flex-col items-center justify-center gap-1 px-1">
        {/* 机柜图形 */}
        <span className="flex h-8 w-6 flex-col justify-between rounded-sm bg-slate-950 p-[2px] ring-1 ring-inset ring-slate-600/60">
          {[0, 1, 2].map(i => <span key={i} className="block h-[3px] rounded-[1px] bg-slate-700" />)}
        </span>
        <span className="w-full truncate text-center text-[10px] font-medium text-slate-200">{rack.name}</span>
        <span className="text-[9px] text-slate-400">{usage}%</span>
        {(critical > 0 || warning > 0) && (
          <span className={cn('absolute right-0.5 top-0.5 rounded-full px-1 text-[9px] font-medium',
            critical > 0 ? 'bg-red-500 text-white' : 'bg-amber-500 text-white')}>
            {critical > 0 ? critical : warning}
          </span>
        )}
      </button>
      <Popconfirm title="删除该机柜？" onConfirm={() => onDelete(rack.id)}>
        <Trash2 className="absolute bottom-0.5 right-0.5 h-3 w-3 cursor-pointer text-slate-500 opacity-0 transition-opacity hover:text-red-400 group-hover:opacity-100" />
      </Popconfirm>
    </div>
  )
}

// ---------------- 小组件 ----------------
function MiniCard({ icon: Icon, label, value, tone = 'info' }: {
  icon: typeof Server; label: string; value: number; tone?: 'info' | 'ok' | 'warn' | 'danger'
}) {
  const color = tone === 'danger' ? 'text-red-500' : tone === 'warn' ? 'text-amber-500' : tone === 'ok' ? 'text-emerald-500' : 'text-blue-500'
  return (
    <div className="rounded-lg border border-border bg-card p-2.5">
      <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
        <Icon className={cn('h-3 w-3', color)} />{label}
      </div>
      <p className={cn('mt-1 text-xl font-semibold leading-none tabular-nums', color)}>{value}</p>
    </div>
  )
}

function Level({ label, value, tone }: { label: string; value: number; tone: string }) {
  return (
    <div>
      <p className={cn('text-base font-semibold leading-none tabular-nums', value > 0 ? tone : 'text-muted-foreground')}>{value}</p>
      <p className="mt-1 text-[10px] text-muted-foreground">{label}</p>
    </div>
  )
}

function CapRow({ dot, label, value }: { dot: string; label: string; value: number }) {
  return (
    <div className="flex items-center gap-1.5">
      <span className={cn('h-1.5 w-1.5 rounded-full', dot)} />
      <span className="text-muted-foreground">{label}</span>
      <span className="ml-auto font-medium tabular-nums">{value.toLocaleString()}</span>
    </div>
  )
}

function CapacityRing({ value }: { value: number }) {
  const r = 26, c = 2 * Math.PI * r
  const v = Math.max(0, Math.min(100, value))
  return (
    <div className="relative h-[64px] w-[64px] shrink-0">
      <svg viewBox="0 0 64 64" className="h-full w-full -rotate-90">
        <circle cx="32" cy="32" r={r} fill="none" stroke="var(--muted)" strokeWidth="6" />
        <circle cx="32" cy="32" r={r} fill="none" stroke="#3b82f6" strokeWidth="6" strokeLinecap="round"
          strokeDasharray={`${(v / 100) * c} ${c}`} />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="text-[13px] font-bold leading-none tabular-nums">{v}%</span>
        <span className="text-[9px] text-muted-foreground">占用</span>
      </div>
    </div>
  )
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-start justify-between gap-2">
      <span className="shrink-0 text-muted-foreground">{k}</span>
      <span className="truncate text-right font-medium" title={v}>{v}</span>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">{label}</label>
      {children}
    </div>
  )
}
