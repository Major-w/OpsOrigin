'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Plus, Trash2, Clock } from 'lucide-react'

const DEFAULT_SLAS = [
  { id: '1', name: 'Critical 变更', riskLevel: 'critical', deadlineHours: 4, escalationHours: 1, description: '数据库变更/安全事件' },
  { id: '2', name: 'High 变更', riskLevel: 'high', deadlineHours: 24, escalationHours: 4, description: '核心配置修改/应用发布' },
  { id: '3', name: 'Medium 变更', riskLevel: 'medium', deadlineHours: 48, escalationHours: 12, description: '单机重启/日志清理' },
  { id: '4', name: 'Low 变更', riskLevel: 'low', deadlineHours: 72, escalationHours: 24, description: '只读查询/文档更新' },
]

const riskLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危' }

export default function ITSMSLAPage() {
  const [slas, setSlas] = useState(DEFAULT_SLAS)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ name: '', riskLevel: 'low', deadlineHours: 72, escalationHours: 24, description: '' })

  const handleCreate = () => {
    setSlas([...slas, { id: Date.now().toString(), ...form }])
    setDialogOpen(false); setForm({ name: '', riskLevel: 'low', deadlineHours: 72, escalationHours: 24, description: '' })
  }

  const riskBadge: Record<string, 'destructive' | 'warning' | 'secondary' | 'success'> = { critical: 'destructive', high: 'warning', medium: 'secondary', low: 'success' }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">SLA 管理</h1><p className="text-sm text-muted-foreground">服务等级协议与超时策略</p></div>
        <Button onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4" />新建策略</Button>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {slas.map(sla => (
          <Card key={sla.id}>
            <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Clock className="h-4 w-4 text-emerald-400" />{sla.name}</CardTitle><p className="text-xs text-muted-foreground">{sla.description}</p></CardHeader>
            <CardContent>
              <div className="grid grid-cols-3 gap-2 text-xs">
                <div><span className="text-muted-foreground">风险:</span> <Badge variant={riskBadge[sla.riskLevel] || 'secondary'}>{riskLabels[sla.riskLevel] || sla.riskLevel}</Badge></div>
                <div><span className="text-muted-foreground">截止:</span> <span className="font-medium">{sla.deadlineHours}h</span></div>
                <div><span className="text-muted-foreground">升级:</span> <span className="font-medium">{sla.escalationHours}h</span></div>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建 SLA 策略">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">名称</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="低风险变更" /></div>
          <div><label className="text-sm font-medium">风险等级</label><select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.riskLevel} onChange={e => setForm(f => ({ ...f, riskLevel: e.target.value }))}>
            <option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="critical">Critical</option>
          </select></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">截止时间(小时)</label><Input type="number" value={form.deadlineHours} onChange={e => setForm(f => ({ ...f, deadlineHours: parseInt(e.target.value) || 72 }))} /></div>
            <div><label className="text-sm font-medium">升级时间(小时)</label><Input type="number" value={form.escalationHours} onChange={e => setForm(f => ({ ...f, escalationHours: parseInt(e.target.value) || 24 }))} /></div>
          </div>
          <div><label className="text-sm font-medium">描述</label><Input value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleCreate} disabled={!form.name}>创建</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
