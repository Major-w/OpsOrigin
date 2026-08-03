'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Package, Plus, Trash2 } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { useITSMStore } from '@/stores/itsm'

interface ServiceItem { id: string; name: string; description: string; category: string; process_id?: string; enabled: boolean }

export default function ITSMCatalogPage() {
  const { processes, fetchProcesses } = useITSMStore()
  const [items, setItems] = useState<ServiceItem[]>([])
  const [showAdd, setShowAdd] = useState(false)
  const [name, setName] = useState(''); const [desc, setDesc] = useState(''); const [cat, setCat] = useState('change')
  const [pid, setPid] = useState('')
  const [editing, setEditing] = useState<string | null>(null)

  const fetch = () => apiList<ServiceItem>('/itsm/catalog').then(setItems).catch(()=>toast.error('加载失败'))

  useEffect(() => { fetch(); fetchProcesses() }, [])

  const create = async () => {
    if (!name.trim()) return
    await apiFetch('/itsm/catalog', { method: 'POST', body: JSON.stringify({ name, description: desc, category: cat, process_id: pid }) })
    setName(''); setDesc(''); setPid(''); setShowAdd(false); fetch(); toast.success('服务项已创建')
  }

  const remove = async (id: string) => {
    if (!confirm('确认删除？')) return
    await apiFetch(`/itsm/catalog/${id}`, { method: 'DELETE' })
    fetch(); toast.success('已删除')
  }

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between"><div><h1 className="text-xl font-bold">服务目录</h1><p className="text-xs text-muted-foreground">定义 ITSM 可申请的标准化服务</p></div><Button size="sm" onClick={()=>setShowAdd(!showAdd)}><Plus className="h-4 w-4 mr-1"/>{showAdd?'取消':'新增'}</Button></div>

    {showAdd && <Card><CardContent className="p-4 space-y-3"><Input placeholder="服务名称" value={name} onChange={e=>setName(e.target.value)}/><Input placeholder="描述" value={desc} onChange={e=>setDesc(e.target.value)}/><div className="grid grid-cols-2 gap-2"><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs" value={cat} onChange={e=>setCat(e.target.value)}><option value="change">变更</option><option value="incident">事件</option><option value="request">请求</option><option value="problem">问题</option></select><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs" value={pid} onChange={e=>setPid(e.target.value)}><option value="">无流程模板</option>{processes.filter(p=>p.enabled).map(p=><option key={p.id} value={p.id}>{p.name}</option>)}</select></div><Button onClick={create} disabled={!name.trim()}>创建</Button></CardContent></Card>}

    <div className="grid gap-3 md:grid-cols-2">
      {items.length === 0 ? <Card className="col-span-2"><CardContent className="py-10 text-center text-muted-foreground"><Package className="h-10 w-10 mx-auto mb-2 opacity-30"/><p>暂无服务项</p></CardContent></Card> :
      items.map(it => <Card key={it.id}><CardContent className="p-4 flex items-start gap-3"><Package className="h-5 w-5 text-blue-500 shrink-0 mt-0.5"/><div className="flex-1 min-w-0"><p className="font-medium text-sm">{it.name}</p><p className="text-xs text-muted-foreground truncate">{it.description||'无描述'}</p><div className="flex gap-2 mt-1"><Badge variant="outline" className="text-[10px]">{it.category}</Badge>{it.process_id && <Badge variant="secondary" className="text-[10px]">有流程</Badge>}</div></div><Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" onClick={()=>remove(it.id)}><Trash2 className="h-3.5 w-3.5 text-red-400"/></Button></CardContent></Card>)}
    </div>
  </div>
}
