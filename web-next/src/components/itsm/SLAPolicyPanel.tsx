'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Clock, Plus, Trash2, Link2 } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { Dialog, DialogFooter } from '@/components/ui/dialog'

export interface SLAPolicy {
  id: string; name: string; risk_level: string
  response_hours: number; resolution_hours: number; escalation_hours: number
  escalation_to: string; enabled: boolean; process_id?: string
}
interface ProcOpt { id: string; name: string }

const riskLabels: Record<string, string> = { critical: '紧急', high: '高', medium: '中', low: '低', custom: '自定义' }
const riskColors: Record<string, any> = { critical: 'destructive', high: 'default', medium: 'warning', low: 'success', custom: 'secondary' }

const emptyForm = (): Partial<SLAPolicy> => ({
  risk_level: 'custom', enabled: true, response_hours: 24, resolution_hours: 48, escalation_hours: 36, process_id: '',
})

export function SLAPolicyPanel() {
  const [policies, setPolicies] = useState<SLAPolicy[]>([])
  const [processes, setProcesses] = useState<ProcOpt[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<Partial<SLAPolicy>>(emptyForm())

  useEffect(() => {
    apiList<SLAPolicy>('/itsm/sla/policies').then(setPolicies).catch(() => toast.error('加载SLA失败'))
    apiList<ProcOpt>('/itsm/processes').then(setProcesses).catch(() => {})
  }, [])

  const processName = (id?: string) => processes.find(p => p.id === id)?.name || ''

  const openCreate = () => { setEditingId(null); setForm(emptyForm()); setDialogOpen(true) }
  const openEdit = (p: SLAPolicy) => { setEditingId(p.id); setForm({ ...p }); setDialogOpen(true) }

  const save = async () => {
    if (!form.name) { toast.error('请填写 SLA 名称'); return }
    try {
      if (editingId) {
        const updated = await apiFetch<SLAPolicy>(`/itsm/sla/policies/${editingId}`, { method: 'PUT', body: JSON.stringify(form) })
        setPolicies(prev => prev.map(p => p.id === editingId ? updated : p))
        toast.success('SLA 已更新')
      } else {
        const created = await apiFetch<SLAPolicy>('/itsm/sla/policies', { method: 'POST', body: JSON.stringify(form) })
        setPolicies(prev => [...prev, created])
        toast.success('SLA 已创建')
      }
      setDialogOpen(false)
    } catch (e: any) { toast.error('保存失败: ' + e.message) }
  }

  const handleDelete = async (id: string) => {
    try {
      await apiFetch(`/itsm/sla/policies/${id}`, { method: 'DELETE' })
      setPolicies(prev => prev.filter(p => p.id !== id))
      toast.success('SLA 已删除')
    } catch (e: any) { toast.error('删除失败: ' + e.message) }
  }

  return <div className="space-y-4">
    <div className="flex items-center justify-between">
      <p className="text-xs text-muted-foreground">按风险等级或流程定义响应/解决/升级时限；关联流程后优先于风险级匹配</p>
      <Button size="sm" onClick={openCreate}><Plus className="h-4 w-4" />新建 SLA</Button>
    </div>
    {policies.length === 0 && <Card><CardContent className="p-10 text-center text-sm text-muted-foreground">暂无 SLA 策略，点击「新建 SLA」定义服务级别目标</CardContent></Card>}
    <div className="grid gap-3">
      {policies.map(p => <Card key={p.id}><CardContent className="p-4 flex items-center gap-4">
        <div className="shrink-0 w-12 text-center"><Badge variant={riskColors[p.risk_level] || 'secondary'} className="text-xs">{riskLabels[p.risk_level] || p.risk_level}</Badge></div>
        <div className="flex-1 min-w-0">
          <p className="font-medium text-sm">{p.name}</p>
          <p className="text-[11px] text-muted-foreground flex items-center gap-1 mt-0.5">
            <Link2 className="h-3 w-3" />{p.process_id ? `关联流程：${processName(p.process_id) || '(未知)'}` : '全局默认（按风险级匹配）'}
          </p>
        </div>
        <div className="flex gap-4 text-xs text-muted-foreground shrink-0">
          <span>响应 {p.response_hours}h</span>
          <span>解决 {p.resolution_hours}h</span>
          <span>升级 {p.escalation_hours}h</span>
        </div>
        <div className="shrink-0 flex gap-1">
          <Button size="sm" variant="ghost" onClick={()=>openEdit(p)}><Clock className="h-3 w-3"/></Button>
          <Button size="sm" variant="ghost" className="text-red-500" onClick={()=>handleDelete(p.id)}><Trash2 className="h-3 w-3"/></Button>
        </div>
      </CardContent></Card>)}
    </div>

    <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editingId ? '编辑 SLA 策略' : '新建 SLA 策略'}>
      <div className="space-y-3">
        <div><label className="text-sm font-medium">名称 *</label><Input value={form.name||''} onChange={e=>setForm({...form,name:e.target.value})} placeholder="如：核心交易系统 SLA" /></div>
        <div><label className="text-sm font-medium">风险等级</label>
          <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.risk_level||'custom'} onChange={e=>setForm({...form,risk_level:e.target.value})}>
            <option value="custom">自定义</option><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="critical">紧急</option>
          </select>
        </div>
        <div><label className="text-sm font-medium">关联流程（可选）</label>
          <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.process_id||''} onChange={e=>setForm({...form,process_id:e.target.value})}>
            <option value="">不关联（全局默认，按风险级匹配）</option>
            {processes.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
          <p className="text-[11px] text-muted-foreground mt-1">关联流程后，该流程的工单将优先匹配此 SLA，而非风险级默认策略。</p>
        </div>
        <div className="grid grid-cols-3 gap-2">
          <div><label className="text-sm font-medium">响应(h)</label><Input type="number" value={form.response_hours||0} onChange={e=>setForm({...form,response_hours:+e.target.value})} /></div>
          <div><label className="text-sm font-medium">解决(h)</label><Input type="number" value={form.resolution_hours||0} onChange={e=>setForm({...form,resolution_hours:+e.target.value})} /></div>
          <div><label className="text-sm font-medium">升级(h)</label><Input type="number" value={form.escalation_hours||0} onChange={e=>setForm({...form,escalation_hours:+e.target.value})} /></div>
        </div>
        <div><label className="text-sm font-medium">升级通知对象</label><Input value={form.escalation_to||''} onChange={e=>setForm({...form,escalation_to:e.target.value})} placeholder="角色 / 负责人 / 群组" /></div>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!form.enabled} onChange={e=>setForm({...form,enabled:e.target.checked})} /> 启用</label>
      </div>
      <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={save}>{editingId ? '保存' : '创建'}</Button></DialogFooter>
    </Dialog>
  </div>
}

export default SLAPolicyPanel
