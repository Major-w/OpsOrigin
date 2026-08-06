'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Timer, Save, Plus, Trash2 } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { Dialog, DialogFooter } from '@/components/ui/dialog'

interface OLAPolicy {
  id: string; name: string; process_id?: string; step_id?: string
  duration_hours: number; escalate_to: string; notify_channel: string; enabled: boolean
}

const channelLabels: Record<string, string> = { email: '邮件', wecom: '企业微信', dingtalk: '钉钉', feishu: '飞书' }

export default function ITSMOLAPage() {
  const [policies, setPolicies] = useState<OLAPolicy[]>([])
  const [editing, setEditing] = useState<string | null>(null)
  const [form, setForm] = useState<Partial<OLAPolicy>>({})
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm, setCreateForm] = useState<Partial<OLAPolicy>>({ name: '', duration_hours: 4, escalate_to: '', notify_channel: 'wecom', enabled: true })

  useEffect(() => {
    apiList<OLAPolicy>('/itsm/ola/policies').then(setPolicies).catch(() => toast.error('加载 OLA 失败'))
  }, [])

  const startEdit = (p: OLAPolicy) => { setEditing(p.id); setForm({ ...p }) }
  const saveEdit = async () => {
    if (!editing) return
    try {
      const updated = await apiFetch<OLAPolicy>(`/itsm/ola/policies/${editing}`, { method: 'PUT', body: JSON.stringify(form) })
      setPolicies(prev => prev.map(p => p.id === editing ? updated : p))
      setEditing(null); toast.success('OLA 已更新')
    } catch (e: any) { toast.error('保存失败: ' + e.message) }
  }

  const handleCreate = async () => {
    if (!createForm.name) { toast.error('请填写 OLA 名称'); return }
    try {
      const created = await apiFetch<OLAPolicy>('/itsm/ola/policies', { method: 'POST', body: JSON.stringify(createForm) })
      setPolicies(prev => [...prev, created])
      setCreateOpen(false)
      setCreateForm({ name: '', duration_hours: 4, escalate_to: '', notify_channel: 'wecom', enabled: true })
      toast.success('OLA 已创建')
    } catch (e: any) { toast.error('创建失败: ' + e.message) }
  }

  const handleDelete = async (id: string) => {
    try {
      await apiFetch(`/itsm/ola/policies/${id}`, { method: 'DELETE' })
      setPolicies(prev => prev.filter(p => p.id !== id))
      toast.success('OLA 已删除')
    } catch (e: any) { toast.error('删除失败: ' + e.message) }
  }

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between">
      <div><h1 className="text-xl font-bold flex items-center gap-2"><Timer className="h-5 w-5" />OLA 管理</h1><p className="text-xs text-muted-foreground">定义流程步骤的处理时限与超时升级策略</p></div>
      <Button size="sm" onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4" />新建 OLA</Button>
    </div>
    <div className="grid gap-3">
      {policies.map(p => {
        const isEdit = editing === p.id
        return <Card key={p.id}><CardContent className="p-4 flex items-center gap-4">
          <div className="flex-1 min-w-0">{isEdit ? <Input value={form.name||''} onChange={e=>setForm({...form,name:e.target.value})} className="h-7 text-sm"/> : <p className="font-medium text-sm">{p.name}</p>}</div>
          <div className="flex gap-4 text-xs text-muted-foreground shrink-0">
            <span>时限 {isEdit ? <Input type="number" value={form.duration_hours||0} onChange={e=>setForm({...form,duration_hours:+e.target.value})} className="w-16 h-6 inline text-xs"/> : `${p.duration_hours}h`}</span>
            <span>升级 {isEdit ? <Input value={form.escalate_to||''} onChange={e=>setForm({...form,escalate_to:e.target.value})} className="w-28 h-6 inline text-xs"/> : p.escalate_to}</span>
            <span>渠道 {isEdit
              ? <select value={form.notify_channel||'wecom'} onChange={e=>setForm({...form,notify_channel:e.target.value})} className="h-6 text-xs ml-1"><option value="email">邮件</option><option value="wecom">企业微信</option><option value="dingtalk">钉钉</option><option value="feishu">飞书</option></select>
              : <Badge variant="outline" className="text-[10px] ml-1">{channelLabels[p.notify_channel] || p.notify_channel}</Badge>}</span>
          </div>
          <div className="shrink-0 flex gap-1">
            {isEdit ? <Button size="sm" variant="outline" onClick={saveEdit}><Save className="h-3 w-3"/></Button> : <Button size="sm" variant="ghost" onClick={()=>startEdit(p)}><Timer className="h-3 w-3"/></Button>}
            <Button size="sm" variant="ghost" className="text-red-500" onClick={()=>handleDelete(p.id)}><Trash2 className="h-3 w-3"/></Button>
          </div>
        </CardContent></Card>
      })}
    </div>

    <Dialog open={createOpen} onClose={() => setCreateOpen(false)} title="新建 OLA 策略">
      <div className="space-y-3">
        <div><label className="text-sm font-medium">名称 *</label><Input value={createForm.name||''} onChange={e=>setCreateForm({...createForm,name:e.target.value})} placeholder="如：一线处理时限" /></div>
        <div className="grid grid-cols-2 gap-2">
          <div><label className="text-sm font-medium">处理时限(h)</label><Input type="number" value={createForm.duration_hours||0} onChange={e=>setCreateForm({...createForm,duration_hours:+e.target.value})} /></div>
          <div><label className="text-sm font-medium">通知渠道</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={createForm.notify_channel||'wecom'} onChange={e=>setCreateForm({...createForm,notify_channel:e.target.value})}>
              <option value="email">邮件</option><option value="wecom">企业微信</option><option value="dingtalk">钉钉</option><option value="feishu">飞书</option>
            </select>
          </div>
        </div>
        <div><label className="text-sm font-medium">超时升级对象</label><Input value={createForm.escalate_to||''} onChange={e=>setCreateForm({...createForm,escalate_to:e.target.value})} placeholder="角色 / 负责人 / 群组" /></div>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!createForm.enabled} onChange={e=>setCreateForm({...createForm,enabled:e.target.checked})} /> 启用</label>
      </div>
      <DialogFooter><Button variant="outline" onClick={() => setCreateOpen(false)}>取消</Button><Button onClick={handleCreate}>创建</Button></DialogFooter>
    </Dialog>
  </div>
}
