'use client'

import { useEffect, useState, useMemo, useCallback } from 'react'
import { useParams, useRouter } from 'next/navigation'
import {
  ReactFlow, Controls, Background, BackgroundVariant, MiniMap,
  type Node, type Edge, type Connection, type NodeProps,
  Handle, Position, ReactFlowProvider,
  MarkerType,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useITSMStore, type Process, type ProcessStep } from '@/stores/itsm'
import { useAgentStore } from '@/stores/agent'
import { useScriptStore } from '@/stores/script'
import { useNotificationStore } from '@/stores/notification'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { formatTime } from '@/lib/utils'
import {
  Plus, Trash2, Save, X, ChevronLeft, Loader2,
  PlayCircle, StopCircle, UserCheck, GitBranch,
  Clock, Bell, Server, FolderTree,
} from 'lucide-react'

// ---- types & meta ----
type ItStepType = ProcessStep['type']
type DepCondition = 'success' | 'failure' | 'always'

const META: Record<ItStepType, { label: string; icon: any; color: string }> = {
  start:    { label: '开始事件', icon: PlayCircle, color: '#10b981' },
  end:      { label: '结束事件', icon: StopCircle, color: '#ef4444' },
  user:     { label: '人工任务', icon: UserCheck, color: '#6366f1' },
  gateway:  { label: '排他网关', icon: GitBranch, color: '#8b5cf6' },
  timer:    { label: '定时器',   icon: Clock,     color: '#f97316' },
  notify:   { label: '通知',     icon: Bell,      color: '#ef4444' },
  service:  { label: '服务任务', icon: Server,    color: '#14b8a6' },
  parallel: { label: '并行网关', icon: FolderTree, color: '#06b6d4' },
  subflow:  { label: '子流程',   icon: FolderTree, color: '#8b5cf6' },
}

function newStep(type: ItStepType, idx: number): ProcessStep {
  return {
    id: `${type}_${Date.now().toString(36)}_${idx}`,
    name: META[type]?.label || type,
    type,
    position_x: 80 + (idx % 3) * 260,
    position_y: 60 + Math.floor(idx / 3) * 140,
    ...(type !== 'start' && type !== 'end' ? { depends_on: [] } : {}),
  }
}

function detectCycle(steps: ProcessStep[]): boolean {
  const idSet = new Set(steps.map(s => s.id))
  const succ = new Map<string, string[]>()
  for (const s of steps) {
    if (s.depends_on) for (const d of s.depends_on) {
      if (idSet.has(d.nodeId)) {
        if (!succ.has(d.nodeId)) succ.set(d.nodeId, [])
        succ.get(d.nodeId)!.push(s.id)
      }
    }
  }
  const WHITE = 0, GRAY = 1, BLACK = 2
  const color = new Map<string, number>()
  for (const s of steps) color.set(s.id, WHITE)
  function dfs(u: string): boolean {
    color.set(u, GRAY)
    for (const v of succ.get(u) || []) {
      const c = color.get(v)
      if (c === GRAY) return true
      if (c === WHITE && dfs(v)) return true
    }
    color.set(u, BLACK)
    return false
  }
  for (const s of steps) if (color.get(s.id) === WHITE && dfs(s.id)) return true
  return false
}

// ---- custom nodes ----
function BPMNStart({ data }: NodeProps) {
  return <div className="flex flex-col items-center">
    <div className="w-[100px] h-[52px] rounded-full border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm" style={{ borderColor: META.start.color }}>
      <PlayCircle className="h-5 w-5 mr-1.5" style={{ color: META.start.color }}/>
      <span className="text-xs font-semibold">{data.label}</span>
    </div>
    <Handle type="source" position={Position.Bottom} style={{ background: META.start.color, width: 10, height: 10 }}/>
  </div>
}
function BPMNEnd({ data }: NodeProps) {
  return <div className="flex flex-col items-center">
    <Handle type="target" position={Position.Top} style={{ background: '#ef4444', width: 10, height: 10 }}/>
    <div className="w-[100px] h-[52px] rounded-full border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm mt-1" style={{ borderColor: '#ef4444' }}>
      <StopCircle className="h-5 w-5 mr-1.5" style={{ color: '#ef4444' }}/>
      <span className="text-xs font-semibold">{data.label}</span>
    </div>
  </div>
}
function BPMNUser({ data }: NodeProps) {
  return <BPMNBox color={META.user.color} icon={<UserCheck className="h-3.5 w-3.5"/>} data={data}>
    {data.approvers?.length > 0 && <p className="text-[10px]">👥 {data.approvers.length} 审批人</p>}
  </BPMNBox>
}
function BPMNGateway({ data }: NodeProps) {
  return <div className="flex flex-col items-center">
    <Handle type="target" position={Position.Top} style={{ background: META.gateway.color, width: 8, height: 8 }}/>
    <div className="w-[60px] h-[60px] rotate-45 border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm mt-1" style={{ borderColor: META.gateway.color }}>
      <span className="-rotate-45 text-[10px] font-semibold">{data.label}</span>
    </div>
    <div className="absolute -bottom-1 left-0 right-0 flex gap-1 justify-center">
      <Handle type="source" position={Position.Bottom} id="on_success" style={{ background: '#10b981', width: 6, height: 6, position: 'relative', left: -8 }}/>
      <Handle type="source" position={Position.Bottom} id="on_failure" style={{ background: '#ef4444', width: 6, height: 6, position: 'relative', left: 8 }}/>
    </div>
  </div>
}
function BPMNTimer({ data }: NodeProps) {
  return <div className="flex flex-col items-center">
    <Handle type="target" position={Position.Top} style={{ background: META.timer.color, width: 10, height: 10 }}/>
    <div className="w-[100px] h-[52px] rounded-full border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm mt-1 gap-2" style={{ borderColor: META.timer.color }}>
      <Clock className="h-5 w-5" style={{ color: META.timer.color }}/>
      <span className="text-[10px] font-medium">{data.label}</span>
    </div>
    <Handle type="source" position={Position.Bottom} style={{ background: META.timer.color, width: 10, height: 10 }}/>
  </div>
}
function BPMNBox({ color, icon, data, children }: { color: string; icon: React.ReactNode; data: any; children?: React.ReactNode }) {
  return <div className="shadow-sm rounded-xl border-2 bg-white dark:bg-gray-800 min-w-[150px]" style={{ borderColor: color }}>
    <Handle type="target" position={Position.Top} style={{ background: color, width: 10, height: 10 }}/>
    <div className="flex items-center gap-2 px-3 py-2 rounded-t-xl text-white font-medium text-xs" style={{ backgroundColor: color }}>
      {icon}{data.label}
    </div>
    {children && <div className="px-3 py-1.5 text-[11px] text-muted-foreground">{children}</div>}
    <Handle type="source" position={Position.Bottom} style={{ background: color, width: 10, height: 10 }}/>
  </div>
}
function BPMNService({ data }: NodeProps) {
  return <BPMNBox color={META.service.color} icon={<Server className="h-3.5 w-3.5"/>} data={data}>
    {data.scriptName && <p className="text-[10px] truncate">{data.scriptName}</p>}
  </BPMNBox>
}
function BPMNNotify({ data }: NodeProps) {
  return <BPMNBox color={META.notify.color} icon={<Bell className="h-3.5 w-3.5"/>} data={data}>
    {data.notifyChannel && <p className="text-[10px] truncate">{data.notifyChannel}</p>}
  </BPMNBox>
}
function BPMNParallel({ data }: NodeProps) {
  return <BPMNBox color={META.parallel.color} icon={<FolderTree className="h-3.5 w-3.5"/>} data={data}>
    <p className="text-[10px]">并行节点</p>
  </BPMNBox>
}
function BPMNSubflow({ data }: NodeProps) {
  return <BPMNBox color={META.subflow.color} icon={<FolderTree className="h-3.5 w-3.5"/>} data={data}>
    {data.subflowName && <p className="text-[10px] truncate">{data.subflowName}</p>}
  </BPMNBox>
}

const nodeTypes: Record<string, any> = {
  start: BPMNStart, end: BPMNEnd, user: BPMNUser,
  gateway: BPMNGateway, timer: BPMNTimer, notify: BPMNNotify,
  service: BPMNService, parallel: BPMNParallel, subflow: BPMNSubflow,
}

// ---- step property panel ----
function StepPanel({ step, steps, scripts, channels, onChange }: {
  step: ProcessStep; steps: ProcessStep[]; scripts: any[]; channels: any[]
  onChange: (p: Partial<ProcessStep>) => void
}) {
  const other = steps.filter(s => s.id !== step.id)

  const removeDep = (nodeId: string) => {
    onChange({ depends_on: (step.depends_on || []).filter(d => d.nodeId !== nodeId) })
  }

  return <Card>
    <CardHeader className="py-3"><CardTitle className="text-sm">{META[step.type]?.label || step.type}</CardTitle></CardHeader>
    <CardContent className="space-y-2 text-sm">
      <div><label className="text-xs font-medium">名称</label><Input value={step.name} onChange={e=>onChange({name:e.target.value})} className="h-8 mt-1"/></div>
      {step.type === 'user' && <>
        <div><label className="text-xs font-medium">审批人 (逗号分隔)</label><Input value={(step.approvers||[]).join(',')} onChange={e=>onChange({approvers:e.target.value.split(',').map(s=>s.trim()).filter(Boolean)})} className="h-8 mt-1" placeholder="admin,manager"/></div>
        <div><label className="text-xs font-medium">审批消息</label><Input value={step.approval_msg||''} onChange={e=>onChange({approval_msg:e.target.value})} className="h-8 mt-1" placeholder="请审批此变更"/></div>
      </>}
      {step.type === 'gateway' && <div><label className="text-xs font-medium">条件表达式</label><Input value={step.condition||''} onChange={e=>onChange({condition:e.target.value})} className="h-8 mt-1" placeholder='risk == "high"'/></div>}
      {step.type === 'timer' && <div><label className="text-xs font-medium">超时时长</label><Input value={step.duration||''} onChange={e=>onChange({duration:e.target.value})} className="h-8 mt-1" placeholder="4h / 30m / 1d"/></div>}
      {step.type === 'notify' && <>
        <div><label className="text-xs font-medium">通知渠道</label><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={step.notify_channel||''} onChange={e=>onChange({notify_channel:e.target.value})}><option value="">选择渠道</option>{channels.filter((c:any)=>c.enabled).map((c:any)=><option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label className="text-xs font-medium">标题</label><Input value={step.notify_title||''} onChange={e=>onChange({notify_title:e.target.value})} className="h-8 mt-1"/></div>
        <div><label className="text-xs font-medium">内容</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1" value={step.notify_body||''} onChange={e=>onChange({notify_body:e.target.value})}/></div>
      </>}
      {step.type === 'service' && <div><label className="text-xs font-medium">关联脚本</label><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={step.script_id||''} onChange={e=>onChange({script_id:e.target.value})}><option value="">选择脚本</option>{scripts.map((s:any)=><option key={s.id} value={s.id}>{s.name}</option>)}</select></div>}
      {step.type === 'subflow' && <div><label className="text-xs font-medium">子流程ID</label><Input value={step.subflow_id||''} onChange={e=>onChange({subflow_id:e.target.value})} className="h-8 mt-1"/></div>}
      {(step.type === 'start' || step.type === 'end' || step.type === 'parallel') && <div className="text-xs text-muted-foreground bg-muted/30 rounded p-2">无需额外配置</div>}

      {step.type !== 'start' && <div className="pt-2 border-t space-y-1">
        <p className="text-[10px] font-medium text-muted-foreground">依赖上游 (depends_on)</p>
        {(step.depends_on||[]).length===0?<p className="text-[10px] text-muted-foreground">无,在画布上从上游拖到本节点</p>:
          (step.depends_on||[]).map(d=><div key={d.nodeId} className="flex items-center gap-1 text-[10px]"><Badge variant="outline" className="text-[10px]">{steps.find(s=>s.id===d.nodeId)?.name||d.nodeId}</Badge><span className="text-muted-foreground">({d.condition})</span><button onClick={()=>removeDep(d.nodeId)} className="text-red-400 hover:text-red-600 ml-1">x</button></div>)
        }
      </div>}
    </CardContent>
  </Card>
}

// ---- BPMN canvas ----
function BPMNCanvas({ process: proc, scripts, channels, onSave }: {
  proc: Process; scripts: any[]; channels: any[]
  onSave: (steps: ProcessStep[]) => Promise<void>
}) {
  const [steps, setSteps] = useState<ProcessStep[]>(() => {
    if (!proc.steps || proc.steps.length === 0) {
      const s = newStep('start', 0); s.position_x = 200; s.position_y = 300
      const e = newStep('end', 1);   e.position_x = 600; e.position_y = 300
      return [s, e]
    }
    return proc.steps
  })
  const [selId, setSelId] = useState<string | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const sel = steps.find(s => s.id === selId) || null

  const { nodes, edges } = useMemo(() => {
    const ns: Node[] = steps.map((s, i) => ({
      id: s.id, type: s.type, position: { x: s.position_x ?? 80 + (i%3)*260, y: s.position_y ?? 60 + Math.floor(i/3)*140 },
      data: {
        label: s.name,
        approvers: s.approvers, approval_msg: s.approval_msg,
        script_id: s.script_id, scriptName: scripts.find(sc=>sc.id===s.script_id)?.name,
        condition: s.condition, duration: s.duration,
        notifyChannel: channels.find((c:any)=>c.id===s.notify_channel)?.name || s.notify_channel,
        notify_title: s.notify_title, notify_body: s.notify_body,
        subflowName: s.subflow_id,
      },
    }))
    const es: Edge[] = []
    const idSet = new Set(steps.map(s => s.id))
    for (const s of steps) {
      if (!s.depends_on) continue
      for (const d of s.depends_on) {
        if (!idSet.has(d.nodeId)) continue
        const isFail = d.condition === 'failure'
        es.push({
          id: `${d.nodeId}->${s.id}-${d.condition}`, source: d.nodeId, target: s.id,
          type: 'smoothstep', animated: !isFail,
          style: { stroke: isFail ? '#ef4444' : '#10b981', strokeWidth: 2, strokeDasharray: isFail ? '5,3' : undefined },
          markerEnd: { type: MarkerType.ArrowClosed, color: isFail ? '#ef4444' : '#10b981' },
          label: isFail ? '不满足' : '满足',
          labelStyle: { fill: isFail ? '#ef4444' : '#10b981', fontSize: 9 },
          labelBgPadding: [3, 1] as [number, number],
          labelBgStyle: { fill: isFail ? '#fef2f2' : '#f0fdf4', fillOpacity: 0.9 },
        })
      }
    }
    return { nodes: ns, edges: es }
  }, [steps, scripts, channels])

  useEffect(() => { if (proc.steps?.length) setSteps(proc.steps); setDirty(false); setSelId(null) }, [proc.id])

  const update = (id: string, patch: Partial<ProcessStep>) => { setSteps(prev=>prev.map(s=>s.id===id?{...s,...patch}:s)); setDirty(true) }
  const add = (type: ItStepType) => { const s = newStep(type, steps.length); setSteps([...steps,s]); setSelId(s.id); setDirty(true) }
  const remove = (id: string) => { setSteps(prev=>prev.filter(s=>s.id!==id).map(s=>s.id===id?s:{...s,depends_on:(s.depends_on||[]).filter(d=>d.nodeId!==id)})); if(selId===id)setSelId(null); setDirty(true) }

  const onConnect = useCallback((conn: Connection) => {
    if (!conn.source || !conn.target) return
    let cond: DepCondition = 'success'
    if (conn.sourceHandle === 'on_failure') cond = 'failure'
    const tgt = steps.find(s=>s.id===conn.target)
    if (!tgt) return
    const deps = tgt.depends_on || []
    if (deps.some(d=>d.nodeId===conn.source)) return
    update(conn.target, { depends_on: [...deps, { nodeId: conn.source, condition: cond }] })
  }, [steps])

  const handleSave = async () => {
    const hasStart = steps.some(s=>s.type==='start')
    const hasEnd = steps.some(s=>s.type==='end')
    if (!hasStart || !hasEnd) { alert('流程必须包含开始和结束节点'); return }
    if (detectCycle(steps)) { alert('检测到环路，DAG不允许环路'); return }
    setSaving(true)
    await onSave(steps.map(s => ({ ...s, position_x: s.position_x, position_y: s.position_y })))
    setSaving(false); setDirty(false)
  }

  return <div className="flex gap-3" style={{ height: 680 }}>
    <div className="w-32 shrink-0 space-y-1">
      <p className="text-xs text-muted-foreground mb-2">BPMN 节点</p>
      {(Object.keys(META) as ItStepType[]).filter(t=>t!=='start'&&t!=='end').map(type=>{
        const m=META[type]; const I=m.icon
        return <button key={type} onClick={()=>add(type)} className="w-full flex items-center gap-2 px-2 py-2 text-xs rounded-md border border-dashed hover:border-solid hover:bg-accent"><I className="h-3 w-3 shrink-0"/>{m.label}</button>
      })}
      <div className="pt-2 mt-2 border-t text-[10px] text-muted-foreground space-y-1"><p>· 拖拽上游→下游连线</p><p>· 滚轮缩放</p><p>· 右键删除</p></div>
    </div>
    <div className="flex-1 border rounded-md overflow-hidden bg-muted/20">
      <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes}
        onConnect={onConnect}
        onNodeClick={(_e, node) => setSelId(node.id)}
        onNodeDragStop={(_e, node) => update(node.id, { position_x: node.position.x, position_y: node.position.y })}
        onPaneClick={() => setSelId(null)}
        onNodesDelete={nodes => nodes.forEach(n => { if (n.type !== 'start') remove(n.id) })}
        fitView deleteKeyCode={['Backspace','Delete']}
        defaultEdgeOptions={{ type: 'smoothstep', style: { strokeWidth: 2 } }}>
        <Controls position="bottom-right"/>
        <Background variant={BackgroundVariant.Dots} gap={20} size={1}/>
        <MiniMap position="bottom-left" nodeStrokeWidth={2} pannable zoomable style={{ height: 100 }}/>
      </ReactFlow>
    </div>
    <div className="w-60 shrink-0">
      {sel ? <div className="flex flex-col gap-2"><StepPanel step={sel} steps={steps} scripts={scripts} channels={channels} onChange={p=>update(sel.id,p)}/>{sel.type!=='start'&&<Button variant="destructive" size="sm" onClick={()=>remove(sel.id)}><Trash2 className="h-3.5 w-3.5 mr-1"/>删除</Button>}</div> :
      <Card className="h-full min-h-[200px] flex items-center justify-center"><CardContent className="text-center text-muted-foreground text-xs"><p>选中节点编辑属性</p></CardContent></Card>}
    </div>
    {dirty && <div className="fixed bottom-6 right-6 z-50 flex gap-2 bg-background border rounded-full shadow-lg px-4 py-2"><span className="text-sm text-amber-600 self-center">有未保存的修改</span><Button size="sm" onClick={handleSave} disabled={saving}>{saving?<Loader2 className="h-3 w-3 mr-1 animate-spin"/>:<Save className="h-3 w-3 mr-1"/>}保存</Button><Button size="sm" variant="outline" onClick={()=>{setSteps(proc.steps||[]);setDirty(false)}}><X className="h-3 w-3 mr-1"/>放弃</Button></div>}
  </div>
}

// ---- page ----
export default function ITSMProcessDetailPage() {
  const params = useParams(); const router = useRouter()
  const id = String(params.id || '')
  const isNew = id === 'new'

  const { processes, getProcess, createProcess, updateProcess, fetchProcesses } = useITSMStore()
  const { scripts, fetch: fetchScripts } = useScriptStore()
  const { channels, fetchChannels } = useNotificationStore()

  const [loading, setLoading] = useState(!isNew)
  const [proc, setProc] = useState<Process | null>(null)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    if (isNew) { setLoading(false); return }
    setLoading(true)
    Promise.all([fetchProcesses(), fetchScripts(), fetchChannels()]).then(() => setLoading(false))
  }, [id])

  useEffect(() => {
    if (isNew || loading) return
    const p = processes.find(p => p.id === id)
    if (p) setProc(p)
  }, [processes, id, loading])

  const handleCreate = async () => {
    if (!newName.trim()) return
    setCreating(true)
    const pid = await createProcess({ name: newName, category: 'change', steps: [], enabled: true, version: 1 })
    setCreating(false)
    if (pid) router.push(`/itsm/process/${pid}`)
  }

  const handleSave = async (steps: ProcessStep[]) => {
    if (!proc) return
    const ok = await updateProcess(proc.id, { ...proc, steps, version: (proc.version||1)+1 })
    if (ok) setProc({ ...proc, steps })
  }

  if (loading) return <div className="p-6 flex justify-center"><Loader2 className="h-8 w-8 animate-spin"/></div>
  if (isNew) return <div className="p-6 space-y-4"><div className="flex items-center gap-3"><Button variant="ghost" size="icon" onClick={()=>router.push('/itsm/process')}><ChevronLeft className="h-4 w-4"/></Button><h1 className="text-xl font-bold">新建 ITSM 流程</h1></div><Card><CardContent className="p-6 space-y-3 max-w-xl"><Input placeholder="流程名称" value={newName} onChange={e=>setNewName(e.target.value)}/><Button onClick={handleCreate} disabled={creating||!newName.trim()}>{creating?'创建中...':'创建并进入画布'}</Button></CardContent></Card></div>
  if (!proc) return <div className="p-6 text-center"><p className="text-muted-foreground">流程不存在</p><Button variant="outline" className="mt-3" onClick={()=>router.push('/itsm/process')}>返回列表</Button></div>

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between gap-3">
      <div className="flex items-center gap-3 min-w-0">
        <Button variant="ghost" size="icon" onClick={()=>router.push('/itsm/process')}><ChevronLeft className="h-4 w-4"/></Button>
        <div className="min-w-0"><h1 className="text-xl font-bold truncate">{proc.name}</h1><p className="text-xs text-muted-foreground">{proc.description||'无描述'} · {proc.steps?.length||0} 节点 · v{proc.version||1}</p></div>
      </div>
    </div>
    <ReactFlowProvider>
      <BPMNCanvas process={proc} scripts={scripts} channels={channels} onSave={handleSave}/>
    </ReactFlowProvider>
  </div>
}
