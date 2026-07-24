'use client'

import { useEffect, useState, useRef } from 'react'
import { useParams, useRouter } from 'next/navigation'
import {
  useWorkflowStore,
  type Workflow,
  type WorkflowStep,
  type StepType,
  type StepApproval,
  type WorkflowExecution,
} from '@/stores/workflow'
import { useAgentStore } from '@/stores/agent'
import { useScriptStore } from '@/stores/script'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatTime } from '@/lib/utils'
import {
  Plus, Trash2, Play, GitBranch, Workflow as WorkflowIcon,
  Code, CheckSquare, Split, Clock, Save, X, Flag, Check, Ban,
  ChevronLeft, Loader2, Pencil,
} from 'lucide-react'

// ---------- helpers ----------
const NODE_META: Record<StepType, { label: string; icon: any; color: string }> = {
  script:    { label: '脚本节点',  icon: Code,      color: 'border-blue-500 bg-blue-50 dark:bg-blue-950/30' },
  approval:  { label: '人工审批',  icon: CheckSquare, color: 'border-amber-500 bg-amber-50 dark:bg-amber-950/30' },
  condition: { label: '分支网关',  icon: GitBranch, color: 'border-purple-500 bg-purple-50 dark:bg-purple-950/30' },
  parallel:  { label: '并行网关',  icon: Split,     color: 'border-cyan-500 bg-cyan-50 dark:bg-cyan-950/30' },
  wait:      { label: '等待节点',  icon: Clock,     color: 'border-slate-500 bg-slate-50 dark:bg-slate-950/30' },
}

function newStep(type: StepType, idx: number): WorkflowStep {
  return {
    id: `${type}_${Date.now().toString(36)}_${idx}`,
    name: NODE_META[type].label,
    type,
    position: { x: 80 + (idx % 4) * 200, y: 60 + Math.floor(idx / 4) * 120 },
    ...(type === 'script' ? { timeout: 300 } : {}),
    ...(type === 'wait' ? { wait_seconds: 10 } : {}),
    ...(type === 'approval' ? { approvers: [] } : {}),
  }
}

// ---------- main page ----------
export default function WorkflowDetailPage() {
  const params = useParams()
  const router = useRouter()
  const workflowId = String(params.id || '')
  const isNew = workflowId === 'new' || !workflowId

  const {
    workflows, executions, approvals,
    fetch, create, update, execute,
    fetchExecutions, fetchApprovals, approve, reject,
  } = useWorkflowStore()
  const { agents } = useAgentStore()
  const { scripts, fetch: fetchScripts } = useScriptStore()

  const [loading, setLoading] = useState(!isNew)
  const [viewWF, setViewWF] = useState<Workflow | null>(null)
  const [mainTab, setMainTab] = useState('canvas')
  const [execDialog, setExecDialog] = useState(false)
  const [execAgents, setExecAgents] = useState<string[]>([])
  const [execInputs, setExecInputs] = useState<Record<string, string>>({})
  // 新建流程表单
  const [createForm, setCreateForm] = useState({ name: '', description: '' })
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    if (isNew) {
      setLoading(false)
      return
    }
    setLoading(true)
    Promise.all([fetch(), fetchScripts(), fetchApprovals()]).then(() => setLoading(false))
  }, [workflowId, isNew])

  // 找到当前流程
  useEffect(() => {
    if (isNew || loading) return
    const wf = workflows.find(w => w.id === workflowId)
    if (wf) {
      setViewWF(wf)
      // 自动加载执行历史
      fetchExecutions(wf.id)
    }
  }, [workflows, workflowId, isNew, loading])

  // 新建流程创建
  const handleCreate = async () => {
    if (!createForm.name.trim()) return
    setCreating(true)
    const id = await create({
      name: createForm.name,
      description: createForm.description,
      steps: [],
      input_params: [],
    })
    setCreating(false)
    if (id) {
      router.push(`/workflows/${id}`)
    }
  }

  const handleExecute = async () => {
    if (!viewWF) return
    await execute(viewWF.id, execAgents, execInputs)
    setExecDialog(false)
    await fetchExecutions(viewWF.id)
    await fetchApprovals()
  }

  // 编辑画布保存回调
  const handleCanvasSave = async (steps: WorkflowStep[]) => {
    if (!viewWF) return
    const ok = await update(viewWF.id, { ...viewWF, steps })
    if (ok) {
      setViewWF({ ...viewWF, steps })
    }
  }

  if (loading) {
    return (
      <div className="p-6 flex items-center justify-center min-h-[400px]">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    )
  }

  // 新建流程页面
  if (isNew) {
    return (
      <div className="p-6 space-y-4">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="icon" onClick={() => router.push('/workflows')} title="返回流程列表">
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <WorkflowIcon className="h-5 w-5 text-emerald-400" />
          <div>
            <h1 className="text-xl font-bold">新建作业流程</h1>
            <p className="text-xs text-muted-foreground">创建后将进入编排画布</p>
          </div>
        </div>

        <Card>
          <CardContent className="p-6">
            <div className="space-y-4 max-w-xl">
              <div>
                <label className="text-sm font-medium">流程名称 *</label>
                <Input
                  value={createForm.name}
                  onChange={e => setCreateForm(f => ({ ...f, name: e.target.value }))}
                  placeholder="例:发布上线流程"
                  className="mt-1"
                />
              </div>
              <div>
                <label className="text-sm font-medium">流程描述</label>
                <textarea
                  className="flex h-24 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1"
                  value={createForm.description}
                  onChange={e => setCreateForm(f => ({ ...f, description: e.target.value }))}
                  placeholder="流程说明..."
                />
              </div>
              <div className="flex gap-2">
                <Button variant="outline" onClick={() => router.push('/workflows')}>取消</Button>
                <Button onClick={handleCreate} disabled={!createForm.name.trim() || creating}>
                  {creating ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Plus className="h-4 w-4 mr-1" />}
                  创建并进入编排
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    )
  }

  if (!viewWF) {
    return (
      <div className="p-6 space-y-4">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="icon" onClick={() => router.push('/workflows')} title="返回流程列表">
            <ChevronLeft className="h-4 w-4" />
          </Button>
        </div>
        <Card>
          <CardContent className="py-12 text-center text-muted-foreground">
            <WorkflowIcon className="h-10 w-10 mx-auto mb-3 opacity-30" />
            <p className="text-sm">未找到该流程</p>
            <Button variant="outline" size="sm" className="mt-3" onClick={() => router.push('/workflows')}>返回流程列表</Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  const pendingApprovals = approvals.filter(a => a.status === 'pending')

  return (
    <div className="p-6 space-y-4">
      {/* 顶部导航 */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3 min-w-0">
          <Button variant="ghost" size="icon" onClick={() => router.push('/workflows')} title="返回流程列表">
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <WorkflowIcon className="h-5 w-5 text-emerald-400 shrink-0" />
          <div className="min-w-0">
            <h1 className="text-xl font-bold truncate">{viewWF.name}</h1>
            <p className="text-xs text-muted-foreground">{viewWF.description || '无描述'} · {viewWF.steps?.length || 0} 步骤</p>
          </div>
        </div>
        <div className="flex gap-2 shrink-0">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setMainTab('approvals')}
            className={mainTab === 'approvals' ? 'border-amber-500 text-amber-600' : ''}
          >
            <CheckSquare className="h-3.5 w-3.5 mr-1" />
            待审批 ({pendingApprovals.length})
          </Button>
          <Button size="sm" onClick={() => { setExecDialog(true); setExecAgents([]); setExecInputs({}) }}>
            <Play className="h-3.5 w-3.5 mr-1" />执行
          </Button>
        </div>
      </div>

      <Card>
        <CardContent className="p-4">
          <Tabs value={mainTab} onValueChange={setMainTab}>
            <TabsList>
              <TabsTrigger value="canvas"><WorkflowIcon className="h-3 w-3 mr-1" />编排画布</TabsTrigger>
              <TabsTrigger value="execs">执行历史 ({executions.length})</TabsTrigger>
              <TabsTrigger value="approvals">审批 ({pendingApprovals.length})</TabsTrigger>
            </TabsList>

            <TabsContent value="canvas" className="mt-3">
              <CanvasEditor
                workflow={viewWF}
                scripts={scripts}
                onSave={handleCanvasSave}
              />
            </TabsContent>

            <TabsContent value="execs" className="mt-3">
              <ExecutionsPanel executions={executions} />
            </TabsContent>

            <TabsContent value="approvals" className="mt-3">
              <ApprovalsPanel
                approvals={pendingApprovals}
                onApprove={(id, c) => approve(id, c)}
                onReject={(id, c) => reject(id, c)}
              />
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>

      {/* 执行对话框 */}
      {execDialog && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setExecDialog(false)} />
          <div className="relative z-50 w-full max-w-lg bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg max-h-[85vh] overflow-y-auto">
            <h3 className="text-lg font-semibold mb-4 flex items-center gap-2">
              <Play className="h-5 w-5 text-emerald-500" />执行流程
            </h3>
            <ExecDialog
              workflow={viewWF}
              agents={agents}
              execAgents={execAgents}
              setExecAgents={setExecAgents}
              execInputs={execInputs}
              setExecInputs={setExecInputs}
              onConfirm={handleExecute}
              onCancel={() => setExecDialog(false)}
            />
          </div>
        </div>
      )}
    </div>
  )
}

// ---------- 执行对话框 ----------
function ExecDialog({ workflow, agents, execAgents, setExecAgents, execInputs, setExecInputs, onConfirm, onCancel }: any) {
  const inputParams: any[] = workflow?.input_params || []
  return (
    <div className="space-y-4">
      {inputParams.length > 0 && (
        <div>
          <label className="text-sm font-medium">输入参数</label>
          <div className="space-y-2 mt-1">
            {inputParams.map((p: any) => (
              <div key={p.name} className="flex items-center gap-2">
                <span className="text-sm w-28 truncate">{p.name}{p.required && <span className="text-red-500">*</span>}</span>
                <Input value={execInputs[p.name] ?? p.default ?? ''}
                  onChange={e => setExecInputs({ ...execInputs, [p.name]: e.target.value })}
                  placeholder={p.description || `输入${p.name}`} />
              </div>
            ))}
          </div>
        </div>
      )}
      <div>
        <label className="text-sm font-medium">执行目标 (Agent)</label>
        <p className="text-xs text-muted-foreground mb-2">选择要执行此流程的主机</p>
        <div className="space-y-1 max-h-60 overflow-auto border rounded-md p-2">
          {agents.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-4">暂无可用 Agent</p>
          ) : agents.map((a: any) => (
            <label key={a.id} className="flex items-center gap-2 text-sm py-1 cursor-pointer hover:bg-muted/50 px-2 rounded">
              <input type="checkbox" checked={execAgents.includes(a.id)} onChange={e => {
                if (e.target.checked) setExecAgents([...execAgents, a.id])
                else setExecAgents(execAgents.filter((id: string) => id !== a.id))
              }} />
              <span>{a.name}</span>
              <Badge variant="outline" className="text-xs">{a.address}</Badge>
            </label>
          ))}
        </div>
      </div>
      <div className="flex justify-end gap-2 pt-2 border-t">
        <Button variant="outline" onClick={onCancel}>取消</Button>
        <Button onClick={onConfirm} disabled={execAgents.length === 0}>立即执行</Button>
      </div>
    </div>
  )
}

// ---------- 画布编辑器（重写：拖拽连线 + 条件构建器）----------

function ConnectingLine({ connecting, steps }: { connecting: { fromId: string; kind: string; mx: number; my: number }; steps: WorkflowStep[] }) {
  const from = steps.find(s => s.id === connecting.fromId)
  if (!from) return null
  const fx = (from.position?.x ?? 0) + 88
  const fy = (from.position?.y ?? 0) + 50
  const color = connecting.kind === 'on_success' ? '#10b981' : '#ef4444'
  return (
    <path
      d={`M ${fx} ${fy} Q ${(fx + connecting.mx) / 2} ${fy} ${connecting.mx} ${connecting.my}`}
      stroke={color}
      strokeWidth={2}
      fill="none"
      strokeDasharray="5,3"
    />
  )
}

function StaticLine({ from, to, color }: { from: WorkflowStep; to?: WorkflowStep; color: string }) {
  if (!to) return null
  const fx = (from.position?.x ?? 0) + 88
  const fy = (from.position?.y ?? 0) + 50
  const tx = (to.position?.x ?? 0) + 88
  const ty = (to.position?.y ?? 0)
  const midY = (fy + ty) / 2
  const marker = color === '#10b981' ? 'url(#arrowGreen)' : 'url(#arrowRed)'
  return (
    <path
      d={`M ${fx} ${fy} C ${fx} ${midY}, ${tx} ${midY}, ${tx} ${ty}`}
      stroke={color}
      strokeWidth={2}
      fill="none"
      markerEnd={marker}
    />
  )
}

function CanvasNode({ step, isSelected, scripts, onMouseDown, onClick, onMouseUp, onDelete, onStartConnect }: {
  step: WorkflowStep
  isSelected: boolean
  scripts: any[]
  onMouseDown: (e: React.MouseEvent) => void
  onClick: (e: React.MouseEvent) => void
  onMouseUp: (e: React.MouseEvent) => void
  onDelete: () => void
  onStartConnect: (e: React.MouseEvent, kind: 'on_success' | 'on_failure') => void
}) {
  const meta = NODE_META[step.type]
  const Icon = meta.icon
  const borderClass = `${meta.color}${isSelected ? ' ring-2 ring-primary' : ''}`
  const x = step.position?.x ?? 0
  const y = step.position?.y ?? 0

  return (
    <div
      className={`absolute w-44 rounded-md border-2 ${borderClass} cursor-move bg-background shadow-sm`}
      style={{ left: x, top: y }}
      onMouseDown={onMouseDown}
      onClick={onClick}
      onMouseUp={onMouseUp}
    >
      <div className="flex items-center gap-2 px-2 py-1.5 border-b border-current/10">
        <Icon className="h-3 w-3 shrink-0" />
        <span className="text-xs font-medium truncate flex-1">{step.name}</span>
        <button onClick={e => { e.stopPropagation(); onDelete() }} className="text-red-500 hover:bg-red-100 rounded p-0.5">
          <Trash2 className="h-2.5 w-2.5" />
        </button>
      </div>
      <div className="px-2 py-1 text-[10px] text-muted-foreground">
        {step.type === 'script' && step.script_id && (
          <p>{scripts.find(s => s.id === step.script_id)?.name?.slice(0, 14) || step.script_id.slice(0, 8)}</p>
        )}
        {step.type === 'wait' && <p>等待 {step.wait_seconds || 0}s</p>}
        {step.type === 'approval' && <p>审批人: {step.approvers?.length || 0}</p>}
        {step.type === 'condition' && <p className="truncate">{step.condition || '未设置条件'}</p>}
        {step.type === 'parallel' && <p>并行 {step.parallel_steps?.length || 0} 步</p>}
      </div>
      <div className="flex border-t border-current/10">
        <div className="flex-1 flex items-center justify-center py-1 gap-3">
          <div className="w-3 h-3 rounded-full bg-emerald-500 cursor-crosshair hover:scale-125"
            onMouseDown={e => onStartConnect(e, 'on_success')} title="成功跳转" />
          <div className="w-3 h-3 rounded-full bg-red-500 cursor-crosshair hover:scale-125"
            onMouseDown={e => onStartConnect(e, 'on_failure')} title="失败跳转" />
        </div>
      </div>
    </div>
  )
}

function CanvasEditor({ workflow, scripts, onSave }: {
  workflow: Workflow
  scripts: any[]
  onSave: (steps: WorkflowStep[]) => Promise<void>
}) {
  const [steps, setSteps] = useState<WorkflowStep[]>(workflow.steps || [])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [connecting, setConnecting] = useState<{ fromId: string; kind: string; mx: number; my: number } | null>(null)
  const canvasRef = useRef<HTMLDivElement>(null)
  const dragRef = useRef<{ id: string; ox: number; oy: number } | null>(null)

  useEffect(() => {
    setSteps(workflow.steps || [])
    setDirty(false)
    setSelectedId(null)
    setConnecting(null)
  }, [workflow.id, workflow.steps])

  const updateStep = (id: string, patch: Partial<WorkflowStep>) => {
    setSteps(prev => prev.map(s => s.id === id ? { ...s, ...patch } : s))
    setDirty(true)
  }

  const addStep = (type: StepType) => {
    const s = newStep(type, steps.length)
    setSteps([...steps, s])
    setSelectedId(s.id)
    setDirty(true)
  }

  const deleteStep = (id: string) => {
    setSteps(prev => prev.filter(s => s.id !== id).map(s => ({
      ...s,
      on_success: s.on_success === id ? '' : s.on_success,
      on_failure: s.on_failure === id ? '' : s.on_failure,
    })))
    if (selectedId === id) setSelectedId(null)
    setDirty(true)
  }

  const startConnect = (e: React.MouseEvent, fromId: string, kind: string) => {
    e.stopPropagation()
    e.preventDefault()
    const canvas = canvasRef.current
    if (!canvas) return
    const rect = canvas.getBoundingClientRect()
    setConnecting({ fromId, kind, mx: e.clientX - rect.left, my: e.clientY - rect.top })
  }

  const handleCanvasMouseMove = (e: React.MouseEvent) => {
    if (!connecting || !canvasRef.current) return
    const rect = canvasRef.current.getBoundingClientRect()
    setConnecting({ ...connecting, mx: e.clientX - rect.left, my: e.clientY - rect.top })
  }

  const dropOnNode = (toId: string) => {
    if (!connecting || connecting.fromId === toId) { setConnecting(null); return }
    updateStep(connecting.fromId, { [connecting.kind]: toId })
    setConnecting(null)
  }

  const handleCanvasMouseUp = () => {
    if (connecting) setConnecting(null)
  }

  const onNodeMouseDown = (e: React.MouseEvent, step: WorkflowStep) => {
    if (connecting) return
    const canvas = canvasRef.current
    if (!canvas) return
    const rect = canvas.getBoundingClientRect()
    dragRef.current = {
      id: step.id,
      ox: e.clientX - rect.left - (step.position?.x ?? 0),
      oy: e.clientY - rect.top - (step.position?.y ?? 0),
    }
    setSelectedId(step.id)
    e.preventDefault()
  }

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!dragRef.current || !canvasRef.current) return
      const rect = canvasRef.current.getBoundingClientRect()
      setSteps(prev => prev.map(s =>
        s.id === dragRef.current!.id
          ? { ...s, position: { x: Math.max(0, e.clientX - rect.left - dragRef.current.ox), y: Math.max(0, e.clientY - rect.top - dragRef.current.oy) } }
          : s
      ))
    }
    const onUp = () => { dragRef.current = null }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
  }, [])

  const handleSave = async () => {
    setSaving(true)
    await onSave(steps)
    setSaving(false)
    setDirty(false)
  }

  const selectedStep = steps.find(s => s.id === selectedId) || null

  return (
    <div className="flex gap-3">
      {/* 节点类型面板 */}
      <div className="w-32 shrink-0 space-y-1">
        <p className="text-xs text-muted-foreground mb-2">点击添加</p>
        {(Object.keys(NODE_META) as StepType[]).map(type => {
          const meta = NODE_META[type]; const IconEl = meta.icon
          return (
            <button key={type} onClick={() => addStep(type)}
              className="w-full flex items-center gap-2 px-2 py-2 text-xs rounded-md border border-dashed hover:border-solid hover:bg-accent">
              <IconEl className="h-3 w-3 shrink-0" />{meta.label}
            </button>
          )
        })}
        <div className="pt-2 mt-2 border-t text-[10px] text-muted-foreground space-y-1">
          <p>· 拖拽节点移动</p>
          <p>· <span className="text-emerald-500">●</span>拖到目标=成功</p>
          <p>· <span className="text-red-500">●</span>拖到目标=失败</p>
        </div>
      </div>

      {/* 画布 */}
      <div ref={canvasRef}
        className="relative flex-1 h-[520px] border rounded-md bg-muted/20 overflow-hidden"
        onClick={() => { setSelectedId(null); if (connecting) setConnecting(null) }}
        onMouseMove={handleCanvasMouseMove}
        onMouseUp={handleCanvasMouseUp}
      >
        <svg className="absolute inset-0 pointer-events-none" style={{ width: '100%', height: '100%' }}>
          <defs>
            <marker id="arrowGreen" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
              <path d="M0,0 L0,6 L7,3 z" fill="#10b981" />
            </marker>
            <marker id="arrowRed" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
              <path d="M0,0 L0,6 L7,3 z" fill="#ef4444" />
            </marker>
          </defs>
          {steps.map(s => (
            <g key={s.id}>
              {s.on_success && <StaticLine from={s} to={steps.find(t => t.id === s.on_success)} color="#10b981" />}
              {s.on_failure && <StaticLine from={s} to={steps.find(t => t.id === s.on_failure)} color="#ef4444" />}
            </g>
          ))}
          {connecting && <ConnectingLine connecting={connecting} steps={steps} />}
        </svg>

        {steps.length === 0 ? (
          <div className="absolute inset-0 flex items-center justify-center text-muted-foreground text-sm">
            <div className="text-center">
              <WorkflowIcon className="h-8 w-8 mx-auto mb-2 opacity-30" />
              <p>从左侧添加节点开始编排</p>
            </div>
          </div>
        ) : (
          steps.map(step => (
            <CanvasNode
              key={step.id}
              step={step}
              isSelected={step.id === selectedId}
              scripts={scripts}
              onMouseDown={e => onNodeMouseDown(e, step)}
              onClick={e => { e.stopPropagation(); setSelectedId(step.id) }}
              onMouseUp={e => { e.stopPropagation(); if (connecting) dropOnNode(step.id) }}
              onDelete={() => deleteStep(step.id)}
              onStartConnect={(e, kind) => startConnect(e, step.id, kind)}
            />
          ))
        )}

        {connecting && (
          <div className="absolute top-2 left-1/2 -translate-x-1/2 bg-primary text-primary-foreground text-xs px-3 py-1 rounded-full shadow z-10">
            拖拽到目标节点释放以完成连线
          </div>
        )}
      </div>

      {/* 属性面板 */}
      <div className="w-64 shrink-0">
        {selectedStep ? (
          <StepPanel step={selectedStep} steps={steps} scripts={scripts}
            onChange={patch => updateStep(selectedStep.id, patch)} />
        ) : (
          <Card className="h-full min-h-[200px] flex items-center justify-center">
            <CardContent className="text-center text-muted-foreground text-xs">
              <p>选中节点查看属性</p>
            </CardContent>
          </Card>
        )}
      </div>

      {/* 保存按钮 */}
      {dirty && (
        <div className="fixed bottom-6 right-6 z-50 flex gap-2 bg-background border rounded-full shadow-lg px-4 py-2">
          <span className="text-sm text-amber-600 self-center">有未保存的修改</span>
          <Button size="sm" onClick={handleSave} disabled={saving}>
            {saving ? <Loader2 className="h-3 w-3 mr-1 animate-spin" /> : <Save className="h-3 w-3 mr-1" />}
            保存
          </Button>
          <Button size="sm" variant="outline" onClick={() => { setSteps(workflow.steps || []); setDirty(false) }}>
            <X className="h-3 w-3 mr-1" />放弃
          </Button>
        </div>
      )}
    </div>
  )
}

// ---------- 步骤属性面板 ----------
function StepPanel({ step, steps, scripts, onChange }: {
  step: WorkflowStep
  steps: WorkflowStep[]
  scripts: any[]
  onChange: (p: Partial<WorkflowStep>) => void
}) {
  const meta = NODE_META[step.type]
  const IconEl = meta.icon
  const otherSteps = steps.filter(s => s.id !== step.id)
  const ctxVars = steps.filter(s => s.id !== step.id).map(s => ({ id: s.id, name: s.name }))

  return (
    <Card>
      <CardHeader className="py-3">
        <div className="flex items-center gap-2">
          <IconEl className="h-4 w-4" />
          <CardTitle className="text-sm">{meta.label} 属性</CardTitle>
        </div>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <div>
          <label className="text-xs font-medium">节点名称</label>
          <Input value={step.name} onChange={e => onChange({ name: e.target.value })} className="h-8 mt-1" />
        </div>
        <div>
          <label className="text-xs font-medium">超时(秒)</label>
          <Input type="number" value={step.timeout || 0}
            onChange={e => onChange({ timeout: parseInt(e.target.value) || 0 })} className="h-8 mt-1" />
        </div>

        {step.type === 'script' && (
          <div>
            <label className="text-xs font-medium">关联脚本</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
              value={step.script_id || ''} onChange={e => onChange({ script_id: e.target.value })}>
              <option value="">选择脚本</option>
              {scripts.map((s: any) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </div>
        )}

        {step.type === 'condition' && (
          <div className="space-y-2">
            <label className="text-xs font-medium">条件表达式</label>
            <p className="text-[10px] text-muted-foreground">支持 == != &gt;= &lt;= &gt; &lt; =~ &amp;&amp; ||</p>
            <details className="text-[10px]">
              <summary className="cursor-pointer text-muted-foreground">插入变量</summary>
              <div className="mt-1 space-y-0.5">
                {ctxVars.map(v => (
                  <button key={v.id} onClick={() => onChange({ condition: (step.condition || '') + ' ' + v.name })}
                    className="block text-blue-500 hover:underline text-left">{v.name}</button>
                ))}
              </div>
            </details>
            <textarea
              className="flex h-16 w-full rounded-md border border-input bg-background px-2 py-1 text-xs font-mono"
              value={step.condition || ''}
              onChange={e => onChange({ condition: e.target.value })}
              placeholder="exit_code == 0"
            />
          </div>
        )}

        {step.type === 'wait' && (
          <div>
            <label className="text-xs font-medium">等待秒数</label>
            <Input type="number" value={step.wait_seconds || 0}
              onChange={e => onChange({ wait_seconds: parseInt(e.target.value) || 0 })} className="h-8 mt-1" />
          </div>
        )}

        {step.type === 'approval' && (
          <div>
            <label className="text-xs font-medium">审批消息</label>
            <Input value={step.approval_msg || ''} onChange={e => onChange({ approval_msg: e.target.value })}
              placeholder="请审批本次操作" className="h-8 mt-1" />
          </div>
        )}

        {step.type === 'parallel' && (
          <div className="text-xs text-muted-foreground bg-muted/30 rounded p-2">
            并行节点下所有 on_success 指向的节点将同时执行
          </div>
        )}

        <div className="pt-2 border-t space-y-2">
          <p className="text-xs font-medium">跳转配置</p>
          <div>
            <label className="text-[10px] text-muted-foreground">成功 →</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
              value={step.on_success || ''} onChange={e => onChange({ on_success: e.target.value })}>
              <option value="">无</option>
              {otherSteps.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </div>
          <div>
            <label className="text-[10px] text-muted-foreground">失败 →</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
              value={step.on_failure || ''} onChange={e => onChange({ on_failure: e.target.value })}>
              <option value="">无</option>
              {otherSteps.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

// ---------- 执行历史面板 ----------
function ExecutionsPanel({ executions }: { executions: WorkflowExecution[] }) {
  const [expanded, setExpanded] = useState<string | null>(null)
  if (executions.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground">
        <Play className="h-8 w-8 mx-auto mb-2 opacity-30" />
        <p className="text-sm">无执行记录</p>
        <p className="text-xs mt-1">点击右上角「执行」按钮开始流程</p>
      </div>
    )
  }
  return (
    <div className="space-y-2">
      {executions.map(e => {
        const isOpen = expanded === e.id
        const results = Object.values(e.step_results || {}) as any[]
        const succeeded = results.filter(r => r.status === 'succeeded').length
        const failed = results.filter(r => r.status === 'failed').length
        const waiting = results.filter(r => r.status === 'waiting').length
        return (
          <div key={e.id} className="border rounded-md">
            <button className="w-full px-3 py-2 flex items-center justify-between hover:bg-accent text-left"
              onClick={() => setExpanded(isOpen ? null : e.id)}>
              <div className="flex items-center gap-2">
                <Badge variant={
                  e.status === 'succeeded' ? 'success' :
                  e.status === 'failed' ? 'destructive' :
                  e.status === 'waiting' ? 'outline' : 'secondary'
                } className="text-xs">{e.status}</Badge>
                <span className="text-xs font-mono">{e.id.slice(0, 8)}</span>
                {e.pending_step_id && <Badge variant="outline" className="text-xs">等待审批: {e.pending_step_id.slice(0, 8)}</Badge>}
              </div>
              <div className="flex items-center gap-3 text-xs text-muted-foreground">
                <span>{(e.agent_ids || []).length} 主机</span>
                <span>{results.length} 步骤</span>
                {succeeded > 0 && <span className="text-emerald-600">{succeeded} 成功</span>}
                {failed > 0 && <span className="text-red-600">{failed} 失败</span>}
                {waiting > 0 && <span className="text-amber-600">{waiting} 等待</span>}
                <span>{formatTime(e.created_at)}</span>
              </div>
            </button>
            {isOpen && (
              <div className="border-t bg-muted/20 p-3 space-y-1.5">
                {results.length === 0 ? (
                  <p className="text-xs text-muted-foreground">无步骤结果</p>
                ) : results.map(r => (
                  <div key={r.step_id} className="text-xs border rounded p-2 bg-background">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <Badge variant={
                          r.status === 'succeeded' ? 'success' :
                          r.status === 'failed' ? 'destructive' :
                          r.status === 'waiting' ? 'outline' : 'secondary'
                        } className="text-[10px]">{r.status}</Badge>
                        <span className="font-medium">{r.step_name}</span>
                      </div>
                      <span className="text-muted-foreground">{r.duration_ms}ms</span>
                    </div>
                    {r.error && <p className="text-red-600 mt-1 font-mono text-[10px] break-all">{r.error}</p>}
                    {r.output && Object.keys(r.output).length > 0 && (
                      <pre className="mt-1 text-[10px] text-muted-foreground bg-muted/30 rounded p-1 overflow-auto max-h-32">
                        {JSON.stringify(r.output, null, 2)}
                      </pre>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

// ---------- 审批面板 ----------
function ApprovalsPanel({ approvals, onApprove, onReject }: {
  approvals: StepApproval[]
  onApprove: (id: string, comment: string) => Promise<boolean>
  onReject: (id: string, comment: string) => Promise<boolean>
}) {
  const [commentMap, setCommentMap] = useState<Record<string, string>>({})
  if (approvals.length === 0) {
    return (
      <div className="text-center py-8 text-muted-foreground">
        <CheckSquare className="h-8 w-8 mx-auto mb-2 opacity-30" />
        <p className="text-sm">暂无待审批任务</p>
      </div>
    )
  }
  return (
    <div className="space-y-3">
      {approvals.map(a => (
        <div key={a.id} className="border rounded-md p-3 bg-amber-50/50 dark:bg-amber-950/10">
          <div className="flex items-start justify-between">
            <div>
              <div className="flex items-center gap-2">
                <Flag className="h-3 w-3 text-amber-600" />
                <span className="text-sm font-medium">{a.step_name}</span>
                <Badge variant="outline" className="text-[10px]">{a.status}</Badge>
              </div>
              <p className="text-xs text-muted-foreground mt-1">
                审批人: {(a.approvers || []).join(', ') || '无'} ·
                创建于 {formatTime(a.created_at)}
              </p>
              {a.message && <p className="text-sm mt-2">{a.message}</p>}
            </div>
            <span className="text-xs text-muted-foreground font-mono">{a.id.slice(0, 8)}</span>
          </div>
          <div className="mt-2">
            <Input
              placeholder="审批意见 (可选)"
              value={commentMap[a.id] || ''}
              onChange={e => setCommentMap({ ...commentMap, [a.id]: e.target.value })}
              className="h-8 text-xs"
            />
          </div>
          <div className="flex gap-2 mt-2 justify-end">
            <Button size="sm" variant="outline"
              onClick={async () => { const ok = await onReject(a.id, commentMap[a.id] || ''); if (ok) setCommentMap({ ...commentMap, [a.id]: '' }) }}>
              <Ban className="h-3 w-3 mr-1" />驳回
            </Button>
            <Button size="sm"
              onClick={async () => { const ok = await onApprove(a.id, commentMap[a.id] || ''); if (ok) setCommentMap({ ...commentMap, [a.id]: '' }) }}>
              <Check className="h-3 w-3 mr-1" />通过
            </Button>
          </div>
        </div>
      ))}
    </div>
  )
}
