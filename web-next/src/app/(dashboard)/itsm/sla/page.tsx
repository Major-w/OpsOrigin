'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Clock, Save } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'

interface SLAPolicy {
  id: string; name: string; risk_level: string
  response_hours: number; resolution_hours: number; escalation_hours: number
  escalation_to: string; enabled: boolean
}

const riskLabels: Record<string, string> = { critical: '紧急', high: '高', medium: '中', low: '低' }
const riskColors: Record<string, any> = { critical: 'destructive', high: 'default', medium: 'warning', low: 'success' }

export default function ITSMSLAPage() {
  const [policies, setPolicies] = useState<SLAPolicy[]>([])
  const [editing, setEditing] = useState<string | null>(null)
  const [form, setForm] = useState<Partial<SLAPolicy>>({})

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

  return <div className="p-6 space-y-4">
    <div><h1 className="text-xl font-bold">SLA 管理</h1><p className="text-xs text-muted-foreground">按风险等级定义响应/解决/升级时限</p></div>
    <div className="grid gap-3">
      {policies.map(p => {
        const isEdit = editing === p.id
        return <Card key={p.id}><CardContent className="p-4 flex items-center gap-4">
          <div className="shrink-0 w-12 text-center"><Badge variant={riskColors[p.risk_level]} className="text-xs">{riskLabels[p.risk_level]}</Badge></div>
          <div className="flex-1 min-w-0">{isEdit ? <Input value={form.name||''} onChange={e=>setForm({...form,name:e.target.value})} className="h-7 text-sm"/> : <p className="font-medium text-sm">{p.name}</p>}</div>
          <div className="flex gap-4 text-xs text-muted-foreground shrink-0">
            <span>响应 {isEdit ? <Input type="number" value={form.response_hours||0} onChange={e=>setForm({...form,response_hours:+e.target.value})} className="w-16 h-6 inline text-xs"/> : `${p.response_hours}h`}</span>
            <span>解决 {isEdit ? <Input type="number" value={form.resolution_hours||0} onChange={e=>setForm({...form,resolution_hours:+e.target.value})} className="w-16 h-6 inline text-xs"/> : `${p.resolution_hours}h`}</span>
            <span>升级 {isEdit ? <Input type="number" value={form.escalation_hours||0} onChange={e=>setForm({...form,escalation_hours:+e.target.value})} className="w-16 h-6 inline text-xs"/> : `${p.escalation_hours}h`}</span>
          </div>
          <div className="shrink-0 flex gap-1">
            {isEdit ? <Button size="sm" variant="outline" onClick={saveEdit}><Save className="h-3 w-3"/></Button> : <Button size="sm" variant="ghost" onClick={()=>startEdit(p)}><Clock className="h-3 w-3"/></Button>}
          </div>
        </CardContent></Card>
      })}
    </div>
  </div>
}
