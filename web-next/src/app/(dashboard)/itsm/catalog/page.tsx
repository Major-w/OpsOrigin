'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Plus, Package, GitBranch, ExternalLink, Edit2, Trash2 } from 'lucide-react'
import { useThemeStore } from '@/stores/theme'
import { cn } from '@/lib/utils'

interface ServiceItem {
  id: string
  name: string
  category: string
  risk: 'low' | 'medium' | 'high' | 'critical'
  description: string
  approvalRequired: boolean
  processId?: string
  processName?: string
  status: 'active' | 'inactive'
  slaTarget?: string
}

interface ServiceForm {
  name: string
  category: string
  risk: 'low' | 'medium' | 'high' | 'critical'
  description: string
  processId: string
  slaTarget: string
}

const MOCK_PROCESSES = [
  { id: 'p1', name: '服务器重启流程', steps: ['提交申请', '主管审批', '执行重启', '验证确认'] },
  { id: 'p2', name: '数据库配置变更', steps: ['提交申请', 'DBA审核', '窗口确认', '执行变更', '验证确认'] },
  { id: 'p3', name: '应用发布流程', steps: ['提交申请', '技术评审', '测试验证', '灰度发布', '全量发布'] },
  { id: 'p4', name: '网络设备变更', steps: ['提交申请', '网络审核', '变更执行', '验证确认'] },
  { id: 'p5', name: 'SSL证书续期', steps: ['检测过期', '申请证书', '部署更新', '验证确认'] },
]

const CATEGORIES = ['基础设施', '数据库', '网络', '安全', '应用', '中间件', '存储']
const RISK_LEVELS = [
  { value: 'low', label: '低', color: 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400' },
  { value: 'medium', label: '中', color: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-500/20 dark:text-yellow-400' },
  { value: 'high', label: '高', color: 'bg-orange-100 text-orange-700 dark:bg-orange-500/20 dark:text-orange-400' },
  { value: 'critical', label: '极高', color: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400' },
]

const DEFAULT_ITEMS: ServiceItem[] = [
  { id: '1', name: '服务器重启', category: '基础设施', risk: 'medium', description: '按计划重启生产服务器，需提前通知业务方', approvalRequired: true, processId: 'p1', processName: '服务器重启流程', status: 'active', slaTarget: '30分钟内完成' },
  { id: '2', name: '数据库配置修改', category: '数据库', risk: 'high', description: '修改数据库参数，需DBA审核后窗口期执行', approvalRequired: true, processId: 'p2', processName: '数据库配置变更', status: 'active', slaTarget: '2小时内完成' },
  { id: '3', name: 'DNS记录更新', category: '网络', risk: 'low', description: '域名解析记录增删改', approvalRequired: false, processId: 'p4', processName: '网络设备变更', status: 'active' },
  { id: '4', name: 'SSL证书续期', category: '安全', risk: 'low', description: '更新即将过期的SSL/TLS证书', approvalRequired: false, processId: 'p5', processName: 'SSL证书续期', status: 'active' },
  { id: '5', name: '应用发布', category: '应用', risk: 'high', description: '生产环境应用版本发布，含灰度策略', approvalRequired: true, processId: 'p3', processName: '应用发布流程', status: 'active', slaTarget: '1小时内完成' },
  { id: '6', name: '中间件配置变更', category: '中间件', risk: 'medium', description: '修改中间件配置参数', approvalRequired: true, status: 'inactive' },
]

export default function ITSMCatalogPage() {
  const [items, setItems] = useState<ServiceItem[]>(DEFAULT_ITEMS)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editItem, setEditItem] = useState<ServiceItem | null>(null)
  const [form, setForm] = useState<ServiceForm>({ name: '', category: '基础设施', risk: 'low', description: '', processId: '', slaTarget: '' })
  const [filter, setFilter] = useState('')
  const { theme } = useThemeStore()

  const openCreate = () => {
    setEditItem(null)
    setForm({ name: '', category: '基础设施', risk: 'low', description: '', processId: '', slaTarget: '' })
    setDialogOpen(true)
  }

  const openEdit = (item: ServiceItem) => {
    setEditItem(item)
    setForm({ name: item.name, category: item.category, risk: item.risk, description: item.description, processId: item.processId || '', slaTarget: item.slaTarget || '' })
    setDialogOpen(true)
  }

  const handleSave = () => {
    const process = MOCK_PROCESSES.find(p => p.id === form.processId)
    const base: Omit<ServiceItem, 'id' | 'status'> = {
      name: form.name,
      category: form.category,
      risk: form.risk,
      description: form.description,
      approvalRequired: form.risk === 'high' || form.risk === 'critical',
      processId: form.processId || undefined,
      processName: process?.name,
      slaTarget: form.slaTarget || undefined
    }

    if (editItem) {
      setItems(prev => prev.map(i => i.id === editItem.id ? { ...i, ...base } : i))
    } else {
      setItems(prev => [...prev, { id: Date.now().toString(), ...base, status: 'active' }])
    }
    setDialogOpen(false)
  }

  const handleDelete = (id: string) => {
    if (!confirm('确定删除该服务项？')) return
    setItems(prev => prev.filter(i => i.id !== id))
  }

  const toggleStatus = (id: string) => {
    setItems(prev => prev.map(i => i.id === id ? { ...i, status: i.status === 'active' ? 'inactive' : 'active' } : i))
  }

  const filtered = items.filter(i =>
    !filter || i.name.toLowerCase().includes(filter.toLowerCase()) || i.category.toLowerCase().includes(filter.toLowerCase())
  )

  const riskInfo = (r: string) => RISK_LEVELS.find(rl => rl.value === r)

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">服务目录定义</h1>
          <p className="text-sm text-muted-foreground">定义发布的流程服务，关联到已设计好的流程</p>
        </div>
        <Button onClick={openCreate}><Plus className="h-4 w-4" />新建服务项</Button>
      </div>

      <div className="flex gap-3">
        <Input placeholder="搜索服务项..." value={filter} onChange={e => setFilter(e.target.value)} className="max-w-sm" />
        <div className="flex gap-1">
          {CATEGORIES.map(c => (
            <button key={c} className={cn("px-2 py-1 rounded text-[10px] border transition-colors", filter === c ? "border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600" : "border-gray-200 dark:border-gray-700 hover:border-gray-300")} onClick={() => setFilter(filter === c ? '' : c)}>{c}</button>
          ))}
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {filtered.map(item => (
          <Card key={item.id} className={cn("group hover:shadow-md transition-all", item.status === 'inactive' && "opacity-60")}>
            <CardHeader className="py-3">
              <div className="flex items-start justify-between">
                <div className="flex-1 min-w-0">
                  <CardTitle className="text-sm flex items-center gap-2">
                    <Package className="h-4 w-4 text-emerald-400 shrink-0" />
                    <span className="truncate">{item.name}</span>
                  </CardTitle>
                  <CardDescription className="text-xs mt-1 line-clamp-2">{item.description}</CardDescription>
                </div>
                <div className="flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity shrink-0 ml-2">
                  <Button variant="ghost" size="sm" className="h-6 w-6 p-0" onClick={() => openEdit(item)}><Edit2 className="h-3 w-3" /></Button>
                  <Button variant="ghost" size="sm" className="h-6 w-6 p-0 text-destructive" onClick={() => handleDelete(item.id)}><Trash2 className="h-3 w-3" /></Button>
                </div>
              </div>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="flex gap-1.5 flex-wrap">
                <Badge variant="outline" className="text-[10px]">{item.category}</Badge>
                <Badge variant="secondary" className={cn("text-[10px]", riskInfo(item.risk)?.color)}>{riskInfo(item.risk)?.label}风险</Badge>
                {item.approvalRequired && <Badge variant="warning" className="text-[10px]">需审批</Badge>}
                <Badge variant={item.status === 'active' ? 'default' : 'outline'} className="text-[10px]">{item.status === 'active' ? '启用' : '停用'}</Badge>
              </div>

              {/* 关联流程 */}
              {item.processId ? (
                <div className="flex items-center gap-2 p-2 rounded-md bg-gray-50 dark:bg-gray-800/50 border border-gray-100 dark:border-gray-800">
                  <GitBranch className="h-3.5 w-3.5 text-emerald-500 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <p className="text-[11px] font-medium truncate">{item.processName}</p>
                    <div className="flex gap-1 mt-0.5">
                      {MOCK_PROCESSES.find(p => p.id === item.processId)?.steps.slice(0, 3).map((step, i) => (
                        <span key={i} className="text-[9px] text-muted-foreground">{step}{i < 2 ? ' →' : ''}</span>
                      ))}
                    </div>
                  </div>
                </div>
              ) : (
                <div className="flex items-center gap-2 p-2 rounded-md border border-dashed border-gray-200 dark:border-gray-700 text-xs text-muted-foreground">
                  <GitBranch className="h-3.5 w-3.5" />
                  <span>未关联流程</span>
                </div>
              )}

              {item.slaTarget && (
                <div className="text-[11px] text-muted-foreground">SLA: {item.slaTarget}</div>
              )}
            </CardContent>
          </Card>
        ))}
      </div>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editItem ? '编辑服务项' : '新建服务项'}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="服务项名称" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">分类</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.category} onChange={e => setForm(f => ({ ...f, category: e.target.value }))}>
                {CATEGORIES.map(c => <option key={c} value={c}>{c}</option>)}
              </select>
            </div>
            <div><label className="text-sm font-medium">风险等级</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.risk} onChange={e => setForm(f => ({ ...f, risk: e.target.value as any }))}>
                {RISK_LEVELS.map(r => <option key={r.value} value={r.value}>{r.label}</option>)}
              </select>
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">关联流程</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.processId} onChange={e => setForm(f => ({ ...f, processId: e.target.value }))}>
              <option value="">不关联流程</option>
              {MOCK_PROCESSES.map(p => <option key={p.id} value={p.id}>{p.name} ({p.steps.length} 步)</option>)}
            </select>
          </div>
          <div><label className="text-sm font-medium">SLA 目标</label><Input value={form.slaTarget} onChange={e => setForm(f => ({ ...f, slaTarget: e.target.value }))} placeholder="例: 30分钟内完成" /></div>
          <div><label className="text-sm font-medium">描述</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSave} disabled={!form.name}>保存</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
