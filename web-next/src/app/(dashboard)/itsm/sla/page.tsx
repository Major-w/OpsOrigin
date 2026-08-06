'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Clock, Save, Plus, Trash2 } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { Dialog, DialogFooter } from '@/components/ui/dialog'

interface SLAPolicy {
  id: string; name: string; risk_level: string
  response_hours: number; resolution_hours: number; escalation_hours: number
  escalation_to: string; enabled: boolean; process_id?: string
}

const riskLabels: Record<string, string> = { critical: '紧急', high: '高', medium: '中', low: '低', custom: '自定义' }
const riskColors: Record<string, any> = { critical: 'destructive', high: 'default', medium: 'warning', low: 'success', custom: 'secondary' }

export default function ITSMSLAPage() {
  const [policies, setPolicies] = useState<SLAPolicy[]>([])
  const [editing, setEditing] = useState<string | null>(null)
  const [form, setForm] = useState<Partial<SLAPolicy>>({})
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm, setCreateForm] = useState<Partial<SLAPolicy>>({ risk_level: 'custom', enabled: true, response_hours: 24, resolution_hours: 48, escalation_hours: 36 })

  useEffect(() => {
    apiList<SLAPolicy>('/itsm/sla/policies').then(setPolicies).catch(() => toast.error('加载SLA失败'))
  }, [])

  const startEdit = (p: SLAPolicy) => { setEditing(p.id); setForm({ ...p }) }
  const saveEdit = async () => {
    if (!editing) return
    try {
      await apiFetch(`/itsm/sla/policies/${editing}`, { method: 'PUT', body: JSON.stringify(form) })
      setPolicies(prev => prev.map(p => p.id === editing ? { ...p, ...form } as SLAPolicy : p))
      setEditing(null); toast.success('SLA 已更新')
    } catch (e: any) { toast.error('保存失败: ' + e.message) }
  }

  const handleCreate = async () => {
    if (!createForm.name) { toast.error('请填写 SLA 名称'); return }
    try {
      const created = await apiFetch<SLAPolicy>('/itsm/sla/policies', { method: 'POST', body: JSON.stringify(createForm) })
      setPolicies(prev => [...prev, created])
      setCreateOpen(false)
      setCreateForm({ risk_level: 'custom', enabled: true, response_hours: 24, resolution_hours: 48, escalation_hours: 36 })
      toast.success('SLA 已创建')
    } catch (e: any) { toast.error('创建失败: ' + e.message) }
  }

  const handleDelete = async (id: string) => {
    try {
      await apiFetch(`/itsm/sla/policies/${id}`, { method: 'DELETE' })
      setPolicies(prev => prev.filter(p => p.id !== id))
      toast.success('SLA 已删除')
    } catch (e: any) { toast.error('删除失败: ' + e.message) }
  }

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between">
      <div><h1 className="text-xl font-bold">SLA 管理</h1><p className="text-xs text-muted-foreground">按风险等级或流程定义响应/解决/升级时限</p></div>
      <Button size="sm" onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4" />新建 SLA</Button>
    </div>
    <div className="grid gap-3">
      {policies.map(p => {
        const isEdit = editing === p.id
        return <Card key={p.id}><CardContent className="p-4 flex items-center gap-4">
          <div className="shrink-0 w-12 text-center"><Badge variant={riskColors[p.risk_level] || 'secondary'} className="text-xs">{riskLabels[p.risk_level] || p.risk_level}</Badge></div>
          <div className="flex-1 min-w-0">{isEdit ? <Input value={form.name||''} onChange={e=>setForm({...form,name:e.target.value})} className="h-7 text-sm"/> : <p className="font-medium text-sm">{p.name}</p>}</div>
          <div className="flex gap-4 text-xs text-muted-foreground shrink-0">
            <span>响应 {isEdit ? <Input type="number" value={form.response_hours||0} onChange={e=>setForm({...form,response_hours:+e.target.value})} className="w-16 h-6 inline text-xs"/> : `${p.response_hours}h`}</span>
            <span>解决 {isEdit ? <Input type="number" value={form.resolution_hours||0} onChange={e=>setForm({...form,resolution_hours:+e.target.value})} className="w-16 h-6 inline text-xs"/> : `${p.resolution_hours}h`}</span>
            <span>升级 {isEdit ? <Input type="number" value={form.escalation_hours||0} onChange={e=>setForm({...form,escalation_hours:+e.target.value})} className="w-16 h-6 inline text-xs"/> : `${p.escalation_hours}h`}</span>
          </div>
          <div className="shrink-0 flex gap-1">
            {isEdit ? <Button size="sm" variant="outline" onClick={saveEdit}><Save className="h-3 w-3"/></Button> : <Button size="sm" variant="ghost" onClick={()=>startEdit(p)}><Clock className="h-3 w-3"/></Button>}
            <Button size="sm" variant="ghost" className="text-red-500" onClick={()=>handleDelete(p.id)}><Trash2 className="h-3 w-3"/></Button>
          </div>
        </CardContent></Card>
      })}
    </div>

    <Dialog open={createOpen} onClose={() => setCreateOpen(false)} title="新建 SLA 策略">
      <div className="space-y-3">
        <div><label className="text-sm font-medium">名称 *</label><Input value={createForm.name||''} onChange={e=>setCreateForm({...createForm,name:e.target.value})} placeholder="如：核心交易系统 SLA" /></div>
        <div><label className="text-sm font-medium">风险等级</label>
          <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={createForm.risk_level||'custom'} onChange={e=>setCreateForm({...createForm,risk_level:e.target.value})}>
            <option value="custom">自定义</option><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="critical">紧急</option>
          </select>
        </div>
        <div className="grid grid-cols-3 gap-2">
          <div><label className="text-sm font-medium">响应(h)</label><Input type="number" value={createForm.response_hours||0} onChange={e=>setCreateForm({...createForm,response_hours:+e.target.value})} /></div>
          <div><label className="text-sm font-medium">解决(h)</label><Input type="number" value={createForm.resolution_hours||0} onChange={e=>setCreateForm({...createForm,resolution_hours:+e.target.value})} /></div>
          <div><label className="text-sm font-medium">升级(h)</label><Input type="number" value={createForm.escalation_hours||0} onChange={e=>setCreateForm({...createForm,escalation_hours:+e.target.value})} /></div>
        </div>
        <div><label className="text-sm font-medium">升级通知对象</label><Input value={createForm.escalation_to||''} onChange={e=>setCreateForm({...createForm,escalation_to:e.target.value})} placeholder="角色 / 负责人 / 群组" /></div>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!createForm.enabled} onChange={e=>setCreateForm({...createForm,enabled:e.target.checked})} /> 启用</label>
      </div>
      <DialogFooter><Button variant="outline" onClick={() => setCreateOpen(false)}>取消</Button><Button onClick={handleCreate}>创建</Button></DialogFooter>
    </Dialog>
  </div>
}
