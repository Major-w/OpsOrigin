
import { useEffect, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Timer, Plus, Trash2, Link2 } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { getMessage } from '@/lib/message'

export interface OLAPolicy {
  id: string; name: string; process_id?: string; step_id?: string
  duration_hours: number; escalate_to: string; notify_channel: string; enabled: boolean
}
interface ProcOpt { id: string; name: string }
interface StepOpt { id: string; name: string }

const channelLabels: Record<string, string> = { email: '邮件', wecom: '企业微信', dingtalk: '钉钉', feishu: '飞书' }

const emptyForm = (): Partial<OLAPolicy> => ({
  name: '', duration_hours: 4, escalate_to: '', notify_channel: 'wecom', enabled: true, process_id: '', step_id: '',
})

export function OLAPolicyPanel() {
  const [policies, setPolicies] = useState<OLAPolicy[]>([])
  const [processes, setProcesses] = useState<ProcOpt[]>([])
  const [steps, setSteps] = useState<StepOpt[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<Partial<OLAPolicy>>(emptyForm())

  useEffect(() => {
    apiList<OLAPolicy>('/itsm/ola/policies').then(setPolicies).catch(() => getMessage().error('加载 OLA 失败'))
    apiList<ProcOpt>('/itsm/processes').then(setProcesses).catch(() => {})
  }, [])

  const processName = (id?: string) => processes.find(p => p.id === id)?.name || ''
  const stepName = (id?: string) => steps.find(s => s.id === id)?.name || ''

  const loadSteps = async (processId?: string) => {
    if (!processId) { setSteps([]); return }
    try {
      const proc = await apiFetch<{ steps: StepOpt[] }>(`/itsm/processes/${processId}`)
      setSteps(proc.steps || [])
    } catch { setSteps([]) }
  }

  const openCreate = () => { setEditingId(null); setForm(emptyForm()); setSteps([]); setDialogOpen(true) }
  const openEdit = (p: OLAPolicy) => { setEditingId(p.id); setForm({ ...p }); loadSteps(p.process_id); setDialogOpen(true) }

  const onProcessChange = (pid: string) => { setForm(f => ({ ...f, process_id: pid, step_id: '' })); loadSteps(pid) }

  const save = async () => {
    if (!form.name) { getMessage().error('请填写 OLA 名称'); return }
    try {
      if (editingId) {
        const updated = await apiFetch<OLAPolicy>(`/itsm/ola/policies/${editingId}`, { method: 'PUT', body: JSON.stringify(form) })
        setPolicies(prev => prev.map(p => p.id === editingId ? updated : p))
        getMessage().success('OLA 已更新')
      } else {
        const created = await apiFetch<OLAPolicy>('/itsm/ola/policies', { method: 'POST', body: JSON.stringify(form) })
        setPolicies(prev => [...prev, created])
        getMessage().success('OLA 已创建')
      }
      setDialogOpen(false)
    } catch (e: any) { getMessage().error('保存失败: ' + e.message) }
  }

  const handleDelete = async (id: string) => {
    try {
      await apiFetch(`/itsm/ola/policies/${id}`, { method: 'DELETE' })
      setPolicies(prev => prev.filter(p => p.id !== id))
      getMessage().success('OLA 已删除')
    } catch (e: any) { getMessage().error('删除失败: ' + e.message) }
  }

  return <div className="space-y-4">
    <div className="flex items-center justify-between">
      <p className="text-xs text-muted-foreground">定义流程步骤的处理时限与超时升级策略；可关联到具体流程或流程步骤</p>
      <Button size="sm" onClick={openCreate}><Plus className="h-4 w-4" />新建 OLA</Button>
    </div>
    {policies.length === 0 && <Card><CardContent className="p-10 text-center text-sm text-muted-foreground">暂无 OLA 策略，点击「新建 OLA」定义内部处理时限</CardContent></Card>}
    <div className="grid gap-3">
      {policies.map(p => <Card key={p.id}><CardContent className="p-4 flex items-center gap-4">
        <div className="flex-1 min-w-0">
          <p className="font-medium text-sm">{p.name}</p>
          <p className="text-[11px] text-muted-foreground flex items-center gap-1 mt-0.5">
            <Link2 className="h-3 w-3" />
            {p.process_id
              ? `关联流程：${processName(p.process_id) || '(未知)'}${p.step_id ? ` / 步骤：${stepName(p.step_id) || p.step_id}` : ' / 全部步骤'}`
              : '全局默认（不限定流程）'}
          </p>
        </div>
        <div className="flex gap-4 text-xs text-muted-foreground shrink-0">
          <span>时限 {p.duration_hours}h</span>
          <span>升级 {p.escalate_to}</span>
          <span><Badge variant="outline" className="text-[10px]">{channelLabels[p.notify_channel] || p.notify_channel}</Badge></span>
        </div>
        <div className="shrink-0 flex gap-1">
          <Button size="sm" variant="ghost" onClick={()=>openEdit(p)}><Timer className="h-3 w-3"/></Button>
          <Button size="sm" variant="ghost" className="text-red-500" onClick={()=>handleDelete(p.id)}><Trash2 className="h-3 w-3"/></Button>
        </div>
      </CardContent></Card>)}
    </div>

    <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editingId ? '编辑 OLA 策略' : '新建 OLA 策略'}>
      <div className="space-y-3">
        <div><label className="text-sm font-medium">名称 *</label><Input value={form.name||''} onChange={e=>setForm({...form,name:e.target.value})} placeholder="如：一线处理时限" /></div>
        <div className="grid grid-cols-2 gap-2">
          <div><label className="text-sm font-medium">处理时限(h)</label><Input type="number" value={form.duration_hours||0} onChange={e=>setForm({...form,duration_hours:+e.target.value})} /></div>
          <div><label className="text-sm font-medium">通知渠道</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.notify_channel||'wecom'} onChange={e=>setForm({...form,notify_channel:e.target.value})}>
              <option value="email">邮件</option><option value="wecom">企业微信</option><option value="dingtalk">钉钉</option><option value="feishu">飞书</option>
            </select>
          </div>
        </div>
        <div><label className="text-sm font-medium">关联流程（可选）</label>
          <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.process_id||''} onChange={e=>onProcessChange(e.target.value)}>
            <option value="">不关联（全局默认）</option>
            {processes.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </div>
        <div><label className="text-sm font-medium">关联步骤（可选）</label>
          <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1 disabled:opacity-50" disabled={!form.process_id} value={form.step_id||''} onChange={e=>setForm({...form,step_id:e.target.value})}>
            <option value="">全部步骤（不限）</option>
            {steps.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          <p className="text-[11px] text-muted-foreground mt-1">选择流程后可限定到某个流程步骤；留空表示覆盖该流程所有步骤。</p>
        </div>
        <div><label className="text-sm font-medium">超时升级对象</label><Input value={form.escalate_to||''} onChange={e=>setForm({...form,escalate_to:e.target.value})} placeholder="角色 / 负责人 / 群组" /></div>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!form.enabled} onChange={e=>setForm({...form,enabled:e.target.checked})} /> 启用</label>
      </div>
      <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={save}>{editingId ? '保存' : '创建'}</Button></DialogFooter>
    </Dialog>
  </div>
}

export default OLAPolicyPanel
