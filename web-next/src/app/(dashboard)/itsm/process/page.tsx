'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { useITSMStore, type Process } from '@/stores/itsm'
import { ProcessVersionHistory } from '@/components/itsm/ProcessVersionHistory'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Plus, Pencil, Trash2, GitBranch, ChevronLeft, History } from 'lucide-react'

export default function ITSMProcessPage() {
  const router = useRouter()
  const { processes, fetchProcesses, createProcess, deleteProcess } = useITSMStore()
  const [showCreate, setShowCreate] = useState(false)
  const [name, setName] = useState('')
  const [desc, setDesc] = useState('')
  const [creating, setCreating] = useState(false)
  const [histId, setHistId] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Process | null>(null)

  useEffect(() => { fetchProcesses() }, [])

  const handleCreate = async () => {
    if (!name.trim()) return
    setCreating(true)
    const id = await createProcess({ name, description: desc, category: 'change', steps: [], enabled: true, version: 1 })
    setCreating(false)
    if (id) { setName(''); setDesc(''); setShowCreate(false); router.push(`/itsm/process/${id}`) }
  }

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between">
      <div>
        <h1 className="text-xl font-bold">ITSM 流程定义</h1>
        <p className="text-xs text-muted-foreground">BPMN 流程画布设计,支持拖拽连线编排审批/网关/定时器</p>
      </div>
      <Button size="sm" onClick={() => setShowCreate(!showCreate)}><Plus className="h-4 w-4 mr-1"/>{showCreate?'取消':'新建流程'}</Button>
    </div>

    {showCreate && <Card><CardContent className="p-4 space-y-3"><Input placeholder="流程名称" value={name} onChange={e=>setName(e.target.value)}/><Input placeholder="描述(可选)" value={desc} onChange={e=>setDesc(e.target.value)}/><Button onClick={handleCreate} disabled={creating||!name.trim()}>{creating?'创建中...':'创建并进入画布'}</Button></CardContent></Card>}

    <div className="grid gap-3">
      {processes.length === 0 ? <Card><CardContent className="py-10 text-center text-muted-foreground"><GitBranch className="h-10 w-10 mx-auto mb-2 opacity-30"/><p>暂无流程定义,点击上方「新建流程」创建</p></CardContent></Card> :
      processes.map(p => <Card key={p.id} className="cursor-pointer hover:border-primary/50" onClick={()=>router.push(`/itsm/process/${p.id}`)}><CardContent className="p-4 flex items-center justify-between">
        <div className="flex items-center gap-3 min-w-0">
          <GitBranch className="h-5 w-5 text-purple-500 shrink-0"/>
          <div className="min-w-0"><p className="font-medium truncate">{p.name}</p><p className="text-xs text-muted-foreground truncate">{p.description||'无描述'} · {p.steps?.length||0} 步骤</p></div>
          <Badge variant="outline" className="shrink-0 text-[10px]">v{p.version||1}</Badge>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <Badge variant={p.enabled?'success':'outline'} className="text-xs">{p.enabled?'启用':'禁用'}</Badge>
          <Button variant="ghost" size="icon" className="h-7 w-7" title="历史版本" onClick={e=>{e.stopPropagation();setHistId(p.id)}}><History className="h-3.5 w-3.5"/></Button>
          <Button variant="ghost" size="icon" className="h-7 w-7" onClick={e=>{e.stopPropagation();router.push(`/itsm/process/${p.id}`)}}><Pencil className="h-3.5 w-3.5"/></Button>
          <Button variant="ghost" size="icon" className="h-7 w-7 text-red-500" onClick={e=>{e.stopPropagation();setDeleteTarget(p)}}><Trash2 className="h-3.5 w-3.5"/></Button>
        </div>
      </CardContent></Card>)}
    </div>

    <ProcessVersionHistory processId={histId || ''} open={histId !== null} onClose={() => setHistId(null)} />
    <ConfirmDialog open={!!deleteTarget} onClose={() => setDeleteTarget(null)} onConfirm={() => { if (deleteTarget) deleteProcess(deleteTarget.id); setDeleteTarget(null) }} title="确认删除" description={`确定删除流程「${deleteTarget?.name || ''}」？`} />
  </div>
}
