// 服务时间（服务窗口）管理：定义工作时间（每周时段 + 时区 + 节假日），
// 供 SLA 策略与服务目录绑定 —— SLA 解决时限按窗口内业务时间推进。
import { useEffect, useMemo, useState } from 'react'
import { Clock, Plus, Pencil, Trash2, RefreshCw, CalendarOff, CalendarClock } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { Button, Input, Modal, Select, Switch, Tag, Tooltip } from 'antd'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { cn } from '@/lib/utils'

interface DayWindow { enabled: boolean; start: string; end: string }
interface SvcWindow {
  id: string
  name: string
  description?: string
  timezone: string
  weekly: DayWindow[]
  holidays?: string[]
  updated_at?: string
}

const WEEK_LABELS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

const TIMEZONES = [
  'Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Singapore', 'Asia/Tokyo',
  'UTC', 'Europe/London', 'Europe/Berlin', 'America/New_York', 'America/Los_Angeles',
]

/** 半小时步进的时间选项（00:00 ~ 24:00） */
const TIME_OPTIONS = (() => {
  const out: { value: string; label: string }[] = []
  for (let m = 0; m <= 24 * 60; m += 30) {
    const v = `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
    out.push({ value: v, label: v })
  }
  return out
})()

/** 窗口模板 */
const TEMPLATES: { name: string; build: () => DayWindow[] }[] = [
  {
    name: '5x8 工作日（09:00-18:00）',
    build: () => Array.from({ length: 7 }, (_, i) => (i >= 1 && i <= 5 ? { enabled: true, start: '09:00', end: '18:00' } : { enabled: false, start: '09:00', end: '18:00' })),
  },
  {
    name: '6x10（08:00-18:00，周一~周六）',
    build: () => Array.from({ length: 7 }, (_, i) => (i >= 1 && i <= 6 ? { enabled: true, start: '08:00', end: '18:00' } : { enabled: false, start: '08:00', end: '18:00' })),
  },
  {
    name: '7x24 全天候',
    build: () => Array.from({ length: 7 }, () => ({ enabled: true, start: '00:00', end: '24:00' })),
  },
]

const emptyWindow = (): SvcWindow => ({
  id: '', name: '', description: '', timezone: 'Asia/Shanghai',
  weekly: TEMPLATES[0].build(), holidays: [],
})

export default function ITSMServiceHoursPage() {
  const [windows, setWindows] = useState<SvcWindow[]>([])
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<SvcWindow>(emptyWindow())
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<SvcWindow | null>(null)
  // 试算预览
  const [pvHours, setPvHours] = useState(4)
  const [pvResult, setPvResult] = useState<string | null>(null)
  const [pvLoading, setPvLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    setLoadError(null)
    try {
      const data = await apiList<SvcWindow>('/itsm/service-windows')
      setWindows(data || [])
    } catch (e: any) {
      setLoadError(e.message || '加载服务时间失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const openCreate = (tpl?: typeof TEMPLATES[number]) => {
    setEditingId(null)
    const w = emptyWindow()
    if (tpl) w.weekly = tpl.build()
    setForm(w)
    setPvResult(null)
    setDialogOpen(true)
  }

  // 从值班班次导入：按周几聚合值班排班的时间段（当天最早开始 ~ 最晚结束），
  // 预填进新建窗口表单 —— 值班排班口径直接转化为 SLA 计算口径。
  const importFromShifts = async () => {
    try {
      const shifts = await apiList<{ start_time: string; end_time: string; user_name?: string }>('/oncall')
      if (!shifts.length) { getMessage().info('暂无值班班次记录，请先在「值班管理」中排班'); return }
      const agg = new Map<number, { start: string; end: string; count: number }>()
      const fmt = (d: Date) => `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
      for (const s of shifts) {
        const st = new Date(s.start_time), en = new Date(s.end_time)
        if (isNaN(st.getTime()) || isNaN(en.getTime())) continue
        const day = st.getDay()
        const sv = fmt(st), ev = fmt(en)
        const cur = agg.get(day)
        agg.set(day, cur
          ? { start: sv < cur.start ? sv : cur.start, end: ev > cur.end ? ev : cur.end, count: cur.count + 1 }
          : { start: sv, end: ev, count: 1 })
      }
      if (agg.size === 0) { getMessage().info('值班班次时间无法解析，请检查排班时间'); return }
      const weekly = Array.from({ length: 7 }, (_, i) => {
        const hit = agg.get(i)
        return hit ? { enabled: true, start: hit.start, end: hit.end } : { enabled: false, start: '09:00', end: '18:00' }
      })
      setEditingId(null)
      setForm({ ...emptyWindow(), name: '值班班次窗口', description: `由 ${shifts.length} 条值班班次聚合生成`, weekly })
      setPvResult(null)
      setDialogOpen(true)
      getMessage().success(`已按 ${agg.size} 天的值班时段预填窗口，可调整后保存`)
    } catch (e: any) { getMessage().error('读取值班班次失败: ' + e.message) }
  }

  const openEdit = (w: SvcWindow) => {
    setEditingId(w.id)
    setForm({
      ...w,
      weekly: w.weekly?.length === 7 ? w.weekly.map(d => ({ ...d })) : TEMPLATES[0].build(),
      holidays: [...(w.holidays || [])],
    })
    setPvResult(null)
    setDialogOpen(true)
  }

  const patchDay = (idx: number, patch: Partial<DayWindow>) => {
    setForm(f => ({ ...f, weekly: f.weekly.map((d, i) => (i === idx ? { ...d, ...patch } : d)) }))
  }

  const save = async () => {
    if (!form.name.trim()) { getMessage().error('请填写窗口名称'); return }
    if (!form.weekly.some(d => d.enabled)) { getMessage().error('至少启用一天的服务时段（或选择 7x24 模板）'); return }
    for (const [i, d] of form.weekly.entries()) {
      if (d.enabled && d.end <= d.start) { getMessage().error(`${WEEK_LABELS[i]}：结束时间必须晚于开始时间`); return }
    }
    setSaving(true)
    try {
      const body = {
        id: editingId || undefined,
        name: form.name.trim(),
        description: form.description || '',
        timezone: form.timezone,
        weekly: form.weekly,
        holidays: form.holidays || [],
      }
      await apiFetch('/itsm/service-windows', { method: 'POST', body: JSON.stringify(body) })
      getMessage().success(editingId ? '更新成功' : '创建成功')
      setDialogOpen(false)
      load()
    } catch (e: any) {
      getMessage().error(`保存失败: ${e.message}`)
    } finally {
      setSaving(false)
    }
  }

  const preview = async () => {
    // 未保存的新窗口先保存才能试算（preview 按 ID 查询）
    if (!editingId) { getMessage().info('请先保存窗口后再试算'); return }
    setPvLoading(true)
    try {
      const res = await apiFetch<{ deadline: string; hours: number }>(
        `/itsm/service-windows/${editingId}/preview?hours=${pvHours}`)
      setPvResult(new Date(res.deadline).toLocaleString('zh-CN'))
    } catch (e: any) {
      getMessage().error(`试算失败: ${e.message}`)
    } finally {
      setPvLoading(false)
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await apiFetch(`/itsm/service-windows/${deleteTarget.id}`, { method: 'DELETE' })
      getMessage().success('删除成功')
      load()
    } catch (e: any) {
      getMessage().error(`删除失败: ${e.message}`)
    }
    setDeleteTarget(null)
  }

  const enabledDays = useMemo(() => form.weekly.filter(d => d.enabled).length, [form.weekly])

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">服务时间</h1>
          <p className="text-sm text-muted-foreground mt-1">定义服务窗口（工作时间）；SLA 策略与服务目录绑定后，工单时限按窗口内业务时间推进</p>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={importFromShifts}>
            <CalendarClock className="w-4 h-4 mr-1" />从值班班次导入
          </Button>
          <Button type="primary" onClick={() => openCreate()}>
            <Plus className="w-4 h-4 mr-1" />新建窗口
          </Button>
        </div>
      </div>

      {loadError ? (
        <ErrorState message={`加载服务时间失败: ${loadError}`} onRetry={load} />
      ) : loading ? (
        <div className="text-center py-12 text-muted-foreground">加载中...</div>
      ) : windows.length === 0 ? (
        <div className="border rounded-lg py-12 text-center">
          <Clock className="w-12 h-12 mx-auto text-muted-foreground mb-4" />
          <p className="text-muted-foreground">暂无服务窗口。未绑定的 SLA 默认按 7x24 墙钟计时。</p>
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {windows.map(w => (
            <div key={w.id} className="border rounded-lg p-4 hover:shadow-md transition-shadow bg-card">
              <div className="flex items-start justify-between">
                <div className="min-w-0">
                  <h3 className="text-base font-semibold truncate">{w.name}</h3>
                  <p className="text-xs text-muted-foreground mt-0.5">时区：{w.timezone}</p>
                </div>
                <div className="flex gap-1 shrink-0">
                  <Button type="text" size="small" onClick={() => openEdit(w)} aria-label="编辑"><Pencil className="w-3.5 h-3.5" /></Button>
                  <Button type="text" size="small" danger onClick={() => setDeleteTarget(w)} aria-label="删除"><Trash2 className="w-3.5 h-3.5" /></Button>
                </div>
              </div>
              {w.description && <p className="text-xs text-muted-foreground mt-1 line-clamp-1">{w.description}</p>}
              <div className="mt-3 flex gap-1">
                {(w.weekly || []).map((d, i) => (
                  <Tooltip key={i} title={d.enabled ? `${d.start} ~ ${d.end}` : '休息'}>
                    <div className={cn(
                      'flex-1 rounded text-center py-1.5 text-[10px] border',
                      d.enabled ? 'bg-primary/10 border-primary/30' : 'text-muted-foreground border-dashed',
                    )}>
                      <div>{WEEK_LABELS[i]}</div>
                      <div className="mt-0.5">{d.enabled ? d.start : '—'}</div>
                    </div>
                  </Tooltip>
                ))}
              </div>
              {!!w.holidays?.length && (
                <div className="mt-2 flex items-center gap-1 text-xs text-muted-foreground">
                  <CalendarOff className="w-3 h-3" />节假日排除 {w.holidays.length} 天
                  <Tooltip title={w.holidays.join('、')}><span className="cursor-help underline decoration-dotted">查看</span></Tooltip>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      <Modal
        open={dialogOpen}
        onCancel={() => setDialogOpen(false)}
        title={editingId ? '编辑服务窗口' : '新建服务窗口'}
        width={640}
        footer={null}
      >
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">窗口名称 <span className="text-red-500">*</span></label>
              <Input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} placeholder="如: 总部 5x8 服务时间" />
            </div>
            <div>
              <label className="text-sm font-medium">时区</label>
              <Select className="w-full" value={form.timezone} onChange={v => setForm({ ...form, timezone: v })}
                options={TIMEZONES.map(t => ({ value: t, label: t }))} showSearch />
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">描述</label>
            <Input value={form.description || ''} onChange={e => setForm({ ...form, description: e.target.value })} placeholder="窗口用途说明（可选）" />
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium">每周时段 <span className="text-muted-foreground font-normal text-xs">（已启用 {enabledDays} 天）</span></label>
              <Select placeholder="套用模板" style={{ width: 220 }} value={null}
                onChange={(name) => { const t = TEMPLATES.find(x => x.name === name); if (t) setForm(f => ({ ...f, weekly: t.build() })) }}
                options={TEMPLATES.map(t => ({ value: t.name, label: t.name }))} />
            </div>
            <div className="space-y-1.5">
              {form.weekly.map((d, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Switch size="small" checked={d.enabled} onChange={v => patchDay(i, { enabled: v })} />
                  <span className={cn('w-10 text-sm', !d.enabled && 'text-muted-foreground')}>{WEEK_LABELS[i]}</span>
                  {d.enabled ? (
                    <>
                      <Select size="small" className="w-28" value={d.start} onChange={v => patchDay(i, { start: v })} options={TIME_OPTIONS} />
                      <span className="text-muted-foreground">~</span>
                      <Select size="small" className="w-28" value={d.end} onChange={v => patchDay(i, { end: v })} options={TIME_OPTIONS} />
                    </>
                  ) : (
                    <span className="text-xs text-muted-foreground">休息日</span>
                  )}
                </div>
              ))}
            </div>
          </div>

          <div>
            <label className="text-sm font-medium">节假日排除</label>
            <div className="flex gap-2 mt-1">
              <Input
                className="flex-1"
                placeholder="日期，如 2026-10-01（回车添加）"
                onKeyDown={e => {
                  const v = (e.target as HTMLInputElement).value.trim()
                  if (e.key === 'Enter' && v) {
                    if (!/^\d{4}-\d{2}-\d{2}$/.test(v)) { getMessage().error('日期格式应为 YYYY-MM-DD'); return }
                    if (!form.holidays?.includes(v)) setForm(f => ({ ...f, holidays: [...(f.holidays || []), v] }))
                    ;(e.target as HTMLInputElement).value = ''
                  }
                }}
              />
            </div>
            {!!form.holidays?.length && (
              <div className="flex flex-wrap gap-1 mt-2">
                {form.holidays.map(h => (
                  <Tag key={h} closable onClose={() => setForm(f => ({ ...f, holidays: f.holidays?.filter(x => x !== h) }))}>{h}</Tag>
                ))}
              </div>
            )}
          </div>

          <div className="border rounded-md p-3 bg-muted/20">
            <label className="text-sm font-medium">试算（按当前窗口推进业务时间）</label>
            <div className="flex items-center gap-2 mt-2">
              <span className="text-xs text-muted-foreground">从现在起</span>
              <Input type="number" className="w-24" min={1} value={pvHours} onChange={e => setPvHours(+e.target.value || 1)} />
              <span className="text-xs text-muted-foreground">小时 →</span>
              <Button size="small" onClick={preview} loading={pvLoading} disabled={!editingId}>试算</Button>
              {pvResult && <Tag color="blue">截止：{pvResult}</Tag>}
              {!editingId && <span className="text-xs text-muted-foreground">（新窗口保存后可试算）</span>}
            </div>
          </div>

          <div className="flex justify-end gap-2">
            <Button onClick={() => setDialogOpen(false)}>取消</Button>
            <Button type="primary" onClick={save} disabled={saving}>{saving ? '保存中...' : '保存'}</Button>
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={`删除服务窗口「${deleteTarget?.name || ''}」`}
        description="绑定此窗口的 SLA 策略与服务目录将回退为 7x24 墙钟计时。确定删除？"
        confirmLabel="删除"
      />
    </div>
  )
}
