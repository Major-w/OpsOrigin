// 任务工作台: 目标驱动的任务级 Agent(T1 骨架 + T2 产物面板 + T3 规划轮/权限审批 + T4 插话/暂停恢复/断线重连)。
// 左栏任务列表 / 中栏执行时间线(可编辑 checklist + 审批卡 + 插话气泡 + 流式输出 + 插话输入) / 右栏产物面板(doc_write 落盘, 预览/下载/沉淀知识库)。
// SSE 客户端走 fetch 流式解析(EventSource 无法带 Authorization 头), 与对话流同模式。
import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Input, Modal, Select, Tag } from 'antd'
import { Loader2, Play, Plus, Square, Pause, ListTodo, FileText, CircleCheck, CircleX, Ban, Download, BookPlus, ShieldAlert, Sparkles, X, MessageSquare, Users, Gauge, Clock, FileDown, Presentation } from 'lucide-react'
import { apiFetch, apiFetchRaw, cn } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { exportDocFile } from '@/lib/doc-export'
import { AiMarkdown } from '@/components/ai-markdown'

interface TaskStep { id: string; title: string; status: string; note?: string; output?: string }
interface AITask {
  id: string; title: string; goal: string; status: string; permission_mode: string; team_id?: string
  steps: TaskStep[]; result_summary: string; error?: string
  steers?: { id: string; message: string; created_at: string }[]
  priority?: number
  conversation_id: string; created_at: string; updated_at: string
}
interface TaskQueueState {
  max_concurrent: number; running: number
  running_ids: string[]
  queued: { id: string; title: string; priority: number; position: number; status: string }[]
}
interface TaskArtifact {
  id: string; conversation_id: string; file_name: string; file_type: string
  file_size: number; created_at: string
}
interface TaskApproval {
  call_id: string; kind: string; tool?: string; question?: string
  args?: Record<string, any>; status: string; comment?: string
}

const STATUS_META: Record<string, { label: string; color: string }> = {
  planning: { label: '待启动', color: 'default' },
  queued: { label: '排队中', color: 'orange' },
  running: { label: '执行中', color: 'processing' },
  waiting_approval: { label: '待确认', color: 'warning' },
  paused: { label: '已暂停', color: 'warning' },
  completed: { label: '已完成', color: 'success' },
  failed: { label: '失败', color: 'error' },
  cancelled: { label: '已取消', color: 'default' },
}

// T6: 调度优先级(数值小者优先出队)
const PRIORITY_META: Record<number, { label: string; color: string }> = {
  1: { label: '高', color: 'red' },
  5: { label: '普通', color: 'default' },
  9: { label: '低', color: 'default' },
}

const STEP_ICON: Record<string, typeof CircleCheck> = {
  done: CircleCheck, running: Loader2, failed: CircleX, skipped: Ban,
}

/** fetch 流式解析 SSE(EventSource 无法带鉴权头)。返回关闭函数。 */
function connectTaskStream(taskId: string, onEvent: (type: string, data: any) => void): () => void {
  const ctrl = new AbortController()
  ;(async () => {
    try {
      const resp = await apiFetchRaw(`/ai/tasks/${taskId}/events`, { signal: ctrl.signal })
      if (!resp.ok || !resp.body) return
      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        // SSE 帧以空行分隔; 逐帧解析 event:/data:
        const frames = buf.split('\n\n')
        buf = frames.pop() || ''
        for (const frame of frames) {
          let evType = 'message'
          let data = ''
          for (const line of frame.split('\n')) {
            if (line.startsWith('event:')) evType = line.slice(6).trim()
            else if (line.startsWith('data:')) data += line.slice(5).trim()
          }
          if (data) {
            try { onEvent(evType, JSON.parse(data)) } catch { /* keepalive 注释等 */ }
          }
        }
      }
    } catch { /* 断开/取消: 静默, 快照机制兜底 */ }
  })()
  return () => ctrl.abort()
}

export default function AITasksPage() {
  const [tasks, setTasks] = useState<AITask[]>([])
  const [active, setActive] = useState<AITask | null>(null)
  const [outputs, setOutputs] = useState<Record<number, string>>({}) // 步骤序号 → 累计输出
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ goal: '', permission_mode: 'standard', team_id: '', priority: 5, auto_start: true })
  // T5: 协作团队选项(任务步骤可派发给多智能体团队并行执行)
  const [teams, setTeams] = useState<{ id: string; name: string; mode: string }[]>([])
  // T6: 多任务调度状态(并发上限/运行中/等待队列)
  const [queue, setQueue] = useState<TaskQueueState | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [artifacts, setArtifacts] = useState<TaskArtifact[]>([])
  const [preview, setPreview] = useState<{ name: string; type: string; content: string } | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [exportingArt, setExportingArt] = useState<string | null>(null) // `${artifactId}:${fmt}` 导出 loading/防重
  // T3: 规划轮草稿(planning 态可编辑) + 审批卡(待批工具/确认) + 各审批单批注
  const [draftSteps, setDraftSteps] = useState<TaskStep[]>([])
  const [planLoading, setPlanLoading] = useState(false)
  const [approvals, setApprovals] = useState<TaskApproval[]>([])
  const [apComments, setApComments] = useState<Record<string, string>>({})
  const closeStreamRef = useRef<(() => void) | null>(null)
  const streamBoxRef = useRef<HTMLDivElement>(null)
  // T4: 运行中插话输入框状态
  const [steerText, setSteerText] = useState('')
  const [steering, setSteering] = useState(false)

  const loadTasks = useCallback(async (selectFirst = false) => {
    try {
      const list = await apiFetch<AITask[]>('/ai/tasks')
      setTasks(list || [])
      if (selectFirst && list?.length && !active) setActive(list[0])
    } catch (e: any) {
      getMessage().error(e?.message || '加载任务列表失败')
    }
  }, [active])

  const loadArtifacts = useCallback(async (taskId: string) => {
    try {
      const list = await apiFetch<TaskArtifact[]>(`/ai/tasks/${taskId}/artifacts`)
      setArtifacts(list || [])
    } catch { /* 产物加载失败不打断主流程 */ }
  }, [])

  const loadApprovals = useCallback(async (taskId: string) => {
    try {
      const list = await apiFetch<TaskApproval[]>(`/ai/tasks/${taskId}/approvals`)
      setApprovals(list || [])
    } catch { /* 审批单加载失败不打断主流程 */ }
  }, [])

  const loadTeams = useCallback(async () => {
    try {
      const list = await apiFetch<{ id: string; name: string; mode: string }[]>('/ai/teams')
      setTeams(list || [])
    } catch { /* 团队加载失败不打断主流程 */ }
  }, [])

  const loadQueue = useCallback(async () => {
    try {
      setQueue(await apiFetch<TaskQueueState>('/ai/tasks/queue'))
    } catch { /* 调度状态加载失败不打断主流程 */ }
  }, [])

  const openArtifact = async (a: TaskArtifact) => {
    if (!active?.conversation_id) return
    setPreviewing(true)
    try {
      const resp = await apiFetchRaw(`/ai/conversations/${active.conversation_id}/artifacts/${a.id}`)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const text = await resp.text()
      setPreview({ name: a.file_name, type: a.file_type, content: text })
    } catch (e: any) {
      getMessage().error(e?.message || '读取产物失败')
    } finally { setPreviewing(false) }
  }

  const downloadArtifact = async (a: TaskArtifact) => {
    if (!active?.conversation_id) return
    try {
      const resp = await apiFetchRaw(`/ai/conversations/${active.conversation_id}/artifacts/${a.id}`)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const blob = await resp.blob()
      const url = URL.createObjectURL(blob)
      const el = document.createElement('a')
      el.href = url; el.download = a.file_name; el.click()
      URL.revokeObjectURL(url)
    } catch (e: any) {
      getMessage().error(e?.message || '下载失败')
    }
  }

  const toKnowledge = async (a: TaskArtifact) => {
    // 借助模型链路外的直接通道: 任务产物 → 知识库草稿。前端不直接调 doc_to_knowledge(模型工具),
    // 这里提示用户在对话/任务中让模型执行, 保持权限链一致。
    getMessage().info(`请在任务执行或对话中让 AI 执行 doc_to_knowledge 沉淀「${a.file_name}」, 草稿生成后到知识库审核发布`)
  }

  // 产物导出 docx/pptx: 仅对 markdown 产物, 内容从预览缓存/重新拉取(纯前端转换)
  const exportArtifactAs = async (a: TaskArtifact, fmt: 'docx' | 'pptx', cachedContent?: string) => {
    const k = `${a.id}:${fmt}`
    if (exportingArt) return
    setExportingArt(k)
    try {
      let md = cachedContent
      if (!md) {
        if (!active?.conversation_id) throw new Error('任务会话缺失')
        const resp = await apiFetchRaw(`/ai/conversations/${active.conversation_id}/artifacts/${a.id}`)
        if (!resp.ok) throw new Error(`读取产物失败(${resp.status})`)
        md = await resp.text()
      }
      await exportDocFile(md, a.file_name.replace(/\.md$/i, ''), fmt, `art:${a.id}`)
    } catch (e: any) {
      if (e?.message !== 'TOO_LARGE') getMessage().error(e?.message || '导出失败')
    } finally {
      setExportingArt(null)
    }
  }

  // 轮询刷新列表(10s); SSE 只挂在选中任务上
  useEffect(() => { loadTasks(true); loadTeams(); loadQueue(); const t = setInterval(() => { loadTasks(); loadQueue() }, 10000); return () => clearInterval(t) }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // 选中任务 → 建立事件流 + 加载产物/审批单
  useEffect(() => {
    closeStreamRef.current?.()
    closeStreamRef.current = null
    setOutputs({})
    setArtifacts([])
    setApprovals([])
    setApComments({})
    setDraftSteps(active?.steps || [])
    setPlanLoading(false)
    if (!active) return
    loadArtifacts(active.id)
    loadApprovals(active.id)
    closeStreamRef.current = connectTaskStream(active.id, (type, data) => {
      if (type === 'snapshot') {
        setActive((prev) => (prev && prev.id === data.id ? { ...data } : prev))
        // T4: 轨迹补放——用快照中各步骤已落库的输出填充, 断线重连无需重放 token
        const snapOut: Record<number, string> = {}
        ;(data.steps || []).forEach((s: TaskStep, i: number) => { if (s.output) snapOut[i] = s.output })
        if (Object.keys(snapOut).length) setOutputs((prev) => ({ ...prev, ...snapOut }))
      } else if (type === 'token') {
        setOutputs((prev) => ({ ...prev, [data.index]: (prev[data.index] || '') + data.delta }))
      } else if (type === 'artifact') {
        loadArtifacts(data.task_id || active.id) // 模型 doc_write 落盘 → 右栏即时刷新
      } else if (type === 'step') {
        setActive((prev) => {
          if (!prev || !prev.steps || data.index >= prev.steps.length) return prev
          const steps = [...prev.steps]; steps[data.index] = data.step
          return { ...prev, steps }
        })
      } else if (type === 'plan') {
        // T3 规划轮产出: 刷新任务步骤 + 编辑草稿
        setActive((prev) => (prev && prev.id === data.task_id ? { ...prev, steps: data.steps } : prev))
        setDraftSteps(data.steps || [])
        setPlanLoading(false)
        getMessage().success('AI 计划已生成, 可编辑后启动')
      } else if (type === 'plan_failed') {
        setPlanLoading(false)
        getMessage().error(data?.error || 'AI 生成计划失败, 请重试或手动编辑步骤')
      } else if (type === 'approval_required') {
        setApprovals((prev) => (prev.some((a) => a.call_id === data.call_id) ? prev : [...prev, data]))
      } else if (type === 'approval_resolved') {
        setApprovals((prev) => prev.filter((a) => a.call_id !== data.call_id))
      } else if (type === 'status') {
        setActive((prev) => (prev && prev.id === data.task_id ? { ...prev, status: data.status } : prev))
        if (data.status === 'running') setPlanLoading(false)
        if (data.status === 'completed') loadTasks()
      } else if (type === 'failed') {
        setActive((prev) => (prev && prev.id === data.task_id ? { ...prev, status: 'failed', error: data.error } : prev))
        loadTasks()
      } else if (type === 'cancelled') {
        setActive((prev) => (prev && prev.id === data.task_id ? { ...prev, status: 'cancelled' } : prev))
        loadTasks()
      } else if (type === 'steer') {
        // T4: 用户插话气泡即时展示
        setActive((prev) => prev ? { ...prev, steers: [...(prev.steers || []), data] } : prev)
      } else if (type === 'paused') {
        setActive((prev) => (prev && prev.id === data.task_id ? { ...prev, status: 'paused' } : prev))
        loadTasks(); loadQueue()
      } else if (type === 'queued') {
        // T6: 任务进入等待队列(并发已满)或优先级变更 → 刷新调度面板与任务状态
        setActive((prev) => (prev && prev.id === data.task_id ? { ...prev, status: 'queued' } : prev))
        loadTasks(); loadQueue()
      } else if (type === 'done' || type === 'failed') {
        loadTasks(); loadQueue()
      }
    })
    return () => { closeStreamRef.current?.(); closeStreamRef.current = null }
  }, [active?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  // 输出区自动滚动
  useEffect(() => { streamBoxRef.current?.scrollTo({ top: streamBoxRef.current.scrollHeight }) }, [outputs])

  const createTask = async () => {
    if (!form.goal.trim() || submitting) return
    setSubmitting(true)
    try {
      const t = await apiFetch<AITask>('/ai/tasks', {
        method: 'POST',
        body: JSON.stringify({ goal: form.goal.trim(), permission_mode: form.permission_mode, team_id: form.team_id, priority: form.priority, auto_start: form.auto_start }),
      })
      getMessage().success('任务已创建')
      setCreating(false)
      setForm({ goal: '', permission_mode: 'standard', team_id: '', priority: 5, auto_start: true })
      setTasks((prev) => [t, ...prev])
      setActive(t)
    } catch (e: any) {
      getMessage().error(e?.message || '创建失败')
    } finally { setSubmitting(false) }
  }

  const startTask = async (id: string) => {
    try {
      // planning 态启动前先落步骤草稿(后端仅 planning 态接受编辑)
      if (active?.id === id && active.status === 'planning' && draftSteps.length > 0) {
        await apiFetch(`/ai/tasks/${id}/plan`, {
          method: 'PUT',
          body: JSON.stringify({ steps: draftSteps.filter((s) => s.title.trim()).map((s) => ({ title: s.title.trim() })) }),
        })
      }
      const t = await apiFetch<AITask>(`/ai/tasks/${id}/start`, { method: 'POST' })
      setActive(t); loadTasks()
    } catch (e: any) { getMessage().error(e?.message || '启动失败') }
  }

  // T3: AI 生成计划(异步, 结果经 SSE plan 事件回推)
  const genPlan = async () => {
    if (!active || planLoading) return
    setPlanLoading(true)
    try {
      await apiFetch(`/ai/tasks/${active.id}/plan`, { method: 'POST' })
      getMessage().info('AI 正在拆解任务步骤, 完成后自动刷新')
    } catch (e: any) {
      getMessage().error(e?.message || '发起规划失败')
      setPlanLoading(false)
    }
  }

  // T3: 批复审批单(批准继续执行 / 拒绝并把理由回注给 AI)
  const resolveApproval = async (callId: string, action: 'approve' | 'reject') => {
    if (!active) return
    try {
      await apiFetch(`/ai/tasks/${active.id}/approvals/${callId}`, {
        method: 'POST',
        body: JSON.stringify({ action, comment: apComments[callId] || '' }),
      })
      setApprovals((prev) => prev.filter((a) => a.call_id !== callId))
      getMessage().success(action === 'approve' ? '已批准, 任务继续执行' : '已拒绝, AI 将调整方案')
    } catch (e: any) {
      getMessage().error(e?.message || '批复失败')
    }
  }

  const cancelTask = async (id: string) => {
    try {
      const t = await apiFetch<AITask>(`/ai/tasks/${id}/cancel`, { method: 'POST' })
      setActive(t); loadTasks()
    } catch (e: any) { getMessage().error(e?.message || '取消失败') }
  }

  // T4: 暂停(保留进度)/恢复(续跑)/插话(steer)
  const pauseTask = async (id: string) => {
    try {
      const t = await apiFetch<AITask>(`/ai/tasks/${id}/pause`, { method: 'POST' })
      setActive(t); loadTasks()
    } catch (e: any) { getMessage().error(e?.message || '暂停失败') }
  }
  const resumeTask = async (id: string) => {
    try {
      const t = await apiFetch<AITask>(`/ai/tasks/${id}/resume`, { method: 'POST' })
      setActive(t); loadTasks()
    } catch (e: any) { getMessage().error(e?.message || '恢复失败') }
  }
  const steerTask = async () => {
    if (!active || !steerText.trim() || steering) return
    setSteering(true)
    try {
      const ent = await apiFetch<{ id: string; message: string; created_at: string }>(`/ai/tasks/${active.id}/steer`, {
        method: 'POST', body: JSON.stringify({ message: steerText.trim() }),
      })
      setActive((prev) => prev ? { ...prev, steers: [...(prev.steers || []), ent] } : prev)
      setSteerText('')
      getMessage().success('已插话, AI 将在下一步参考你的指令(暂停后恢复也可立即生效)')
    } catch (e: any) { getMessage().error(e?.message || '插话失败') }
    finally { setSteering(false) }
  }

  const runningTasks = tasks.filter((t) => t.status === 'running').length
  const liveStep = active?.steps.findIndex((s) => s.status === 'running') ?? -1
  const streamable = active && (active.status === 'running' || active.status === 'planning' || liveStep >= 0 ||
    (active.status === 'completed' && Object.keys(outputs).length > 0))

  return (
    <div className="h-[calc(100vh-120px)] min-h-[520px] flex gap-3">
      {/* 左栏: 任务列表 */}
      <div className="w-72 shrink-0 flex flex-col gap-2 border border-gray-200 dark:border-gray-700/50 rounded-xl p-2.5">
        <div className="flex items-center justify-between px-1">
          <span className="text-sm font-semibold flex items-center gap-1.5"><ListTodo className="h-4 w-4" />任务</span>
          {runningTasks > 0 && <Tag color="processing">{runningTasks} 个执行中</Tag>}
        </div>
        {/* T6: 调度面板——并发占用 / 等待队列(并发满时新任务排队, 按优先级出队) */}
        {queue && (
          <div className="mx-1 mb-0.5 rounded-lg border border-gray-200 dark:border-gray-700/50 px-2 py-1.5 space-y-1 bg-gray-50/60 dark:bg-gray-800/30">
            <div className="flex items-center justify-between text-[11px]">
              <span className="flex items-center gap-1 text-muted-foreground">
                <Gauge className="h-3 w-3" />并发
              </span>
              <span className="font-medium">
                {queue.running}/{queue.max_concurrent}
                {queue.queued.length > 0 && <span className="text-orange-500 ml-1.5">排队 {queue.queued.length}</span>}
              </span>
            </div>
            {queue.queued.length > 0 && (
              <div className="space-y-0.5">
                {queue.queued.slice(0, 3).map((q) => (
                  <div key={q.id} className="flex items-center gap-1 text-[10px] text-muted-foreground">
                    <Clock className="h-2.5 w-2.5 shrink-0" />
                    <span className="text-orange-500 shrink-0">#{q.position}</span>
                    <span className="truncate flex-1">{q.title || q.id.slice(0, 8)}</span>
                    <Tag color={PRIORITY_META[q.priority]?.color} className="!m-0 !px-1 !text-[9px] !leading-3">
                      {PRIORITY_META[q.priority]?.label || '普通'}
                    </Tag>
                  </div>
                ))}
                {queue.queued.length > 3 && <p className="text-[10px] text-muted-foreground pl-3.5">…还有 {queue.queued.length - 3} 个</p>}
              </div>
            )}
          </div>
        )}
        <Button size="small" onClick={() => setCreating(true)} className="w-full">
          <Plus className="h-3.5 w-3.5 mr-1" />新建任务
        </Button>
        <div className="flex-1 overflow-y-auto space-y-1 min-h-0">
          {tasks.length === 0 && <p className="text-xs text-muted-foreground text-center py-6">暂无任务, 点击「新建任务」开始</p>}
          {tasks.map((t) => {
            const meta = STATUS_META[t.status] || STATUS_META.planning
            return (
              <button
                key={t.id}
                onClick={() => setActive(t)}
                className={cn(
                  'w-full text-left p-2.5 rounded-lg transition-colors',
                  active?.id === t.id ? 'bg-blue-50 dark:bg-blue-500/10 ring-1 ring-blue-200 dark:ring-blue-500/30' : 'hover:bg-gray-100 dark:hover:bg-gray-800',
                )}
              >
                <p className="text-sm font-medium truncate">{t.title}</p>
                <div className="flex items-center gap-1.5 mt-1">
                  <Tag color={meta.color} className="!text-[10px] !leading-4 !px-1.5 !mr-0">{meta.label}</Tag>
                  <span className="text-[10px] text-muted-foreground">{t.permission_mode}</span>
                  <span className="text-[10px] text-muted-foreground ml-auto">{new Date(t.updated_at).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })}</span>
                </div>
              </button>
            )
          })}
        </div>
      </div>

      {/* 中栏: 执行时间线 */}
      <div className="flex-1 min-w-0 flex flex-col border border-gray-200 dark:border-gray-700/50 rounded-xl p-4 gap-3">
        {!active ? (
          <div className="flex-1 flex items-center justify-center text-sm text-muted-foreground">选择或创建一个任务</div>
        ) : (
          <>
            <div className="flex items-start gap-2">
              <div className="flex-1 min-w-0">
                <p className="text-base font-semibold truncate">{active.title}</p>
                <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{active.goal}</p>
              </div>
              <Tag color={(STATUS_META[active.status] || STATUS_META.planning).color}>
                {(STATUS_META[active.status] || STATUS_META.planning).label}
              </Tag>
              {active.team_id && (
                <Tag color="purple" title="协作团队: 步骤派发给多智能体团队并行执行">
                  <Users className="h-3 w-3 mr-1 inline" />协作团队
                </Tag>
              )}
              {active.status === 'planning' && (
                <>
                  <Button size="small" onClick={genPlan} loading={planLoading}>
                    <Sparkles className="h-3 w-3 mr-1" />AI 生成计划
                  </Button>
                  <Button size="small" type="primary" onClick={() => startTask(active.id)}><Play className="h-3 w-3 mr-1" />启动</Button>
                </>
              )}
              {(active.status === 'running' || active.status === 'waiting_approval') && (
                <>
                  <Button size="small" onClick={() => pauseTask(active.id)}><Pause className="h-3 w-3 mr-1" />暂停</Button>
                  <Button size="small" danger onClick={() => cancelTask(active.id)}><Square className="h-3 w-3 mr-1" />取消</Button>
                </>
              )}
              {active.status === 'paused' && (
                <Button size="small" type="primary" onClick={() => resumeTask(active.id)}><Play className="h-3 w-3 mr-1" />恢复</Button>
              )}
              {active.status === 'queued' && (
                <Button size="small" danger onClick={() => cancelTask(active.id)}><Square className="h-3 w-3 mr-1" />取消排队</Button>
              )}
            </div>

            {/* 步骤 checklist: planning 态可编辑(T3), 其余态只读展示 */}
            {active.status === 'planning' ? (
              <div className="space-y-1.5">
                {draftSteps.map((s, i) => (
                  <div key={s.id || i} className="flex items-center gap-1.5">
                    <span className="text-[10px] w-5 text-right text-muted-foreground shrink-0">{i + 1}.</span>
                    <Input
                      size="small"
                      value={s.title}
                      placeholder="步骤标题"
                      onChange={(e) => setDraftSteps((prev) => prev.map((x, j) => (j === i ? { ...x, title: e.target.value } : x)))}
                    />
                    <button
                      onClick={() => setDraftSteps((prev) => prev.filter((_, j) => j !== i))}
                      className="p-1 rounded text-muted-foreground hover:text-red-500 shrink-0"
                      title="删除步骤"
                    >
                      <X className="h-3.5 w-3.5" />
                    </button>
                  </div>
                ))}
                <Button size="small" className="mt-1" onClick={() => setDraftSteps((prev) => [...prev, { id: `draft-${Date.now()}`, title: '', status: 'pending' }])}>
                  <Plus className="h-3 w-3 mr-1" />添加步骤
                </Button>
              </div>
            ) : (
            <div className="space-y-1">
              {active.steps.map((s, i) => {
                const Icon = STEP_ICON[s.status]
                return (
                  <div key={s.id || i} className="flex items-center gap-2 text-sm px-2 py-1.5 rounded-md hover:bg-gray-50 dark:hover:bg-gray-800/60">
                    {Icon ? (
                      <Icon className={cn('h-4 w-4 shrink-0', s.status === 'running' && 'animate-spin text-blue-500', s.status === 'done' && 'text-emerald-500', s.status === 'failed' && 'text-red-500')} />
                    ) : (
                      <span className="h-4 w-4 shrink-0 rounded-full border border-gray-300 dark:border-gray-600" />
                    )}
                    <span className={cn('truncate', s.status === 'done' && 'text-muted-foreground line-through')}>{s.title}</span>
                    {s.note && s.status === 'failed' && <span className="text-[10px] text-red-500 truncate">{s.note}</span>}
                    {s.note && s.status !== 'failed' && <span className="text-[10px] text-muted-foreground truncate" title={s.note}>{s.note}</span>}
                  </div>
                )
              })}
            </div>
            )}

            {/* 审批卡(T3): 工具闸门拦截 / AI 主动确认, 批准继续 / 拒绝回注理由 */}
            {approvals.length > 0 && (
              <div className="space-y-2">
                {approvals.map((ap) => (
                  <div key={ap.call_id} className="border border-amber-300 dark:border-amber-500/40 bg-amber-50 dark:bg-amber-500/10 rounded-lg p-3 space-y-2">
                    <p className="text-xs font-semibold flex items-center gap-1.5 text-amber-700 dark:text-amber-400">
                      <ShieldAlert className="h-3.5 w-3.5 shrink-0" />
                      {ap.kind === 'confirm' ? 'AI 请求你的确认' : `AI 请求执行「${ap.tool}」(需审批)`}
                    </p>
                    {ap.question && <p className="text-xs whitespace-pre-wrap">{ap.question}</p>}
                    {ap.tool && ap.args && Object.keys(ap.args).length > 0 && (
                      <pre className="text-[10px] bg-white/60 dark:bg-black/20 rounded p-2 overflow-x-auto max-h-32">{JSON.stringify(ap.args, null, 2)}</pre>
                    )}
                    <Input
                      size="small"
                      placeholder="批注(可选, 拒绝时会转达给 AI)"
                      value={apComments[ap.call_id] || ''}
                      onChange={(e) => setApComments((prev) => ({ ...prev, [ap.call_id]: e.target.value }))}
                    />
                    <div className="flex gap-2">
                      <Button size="small" type="primary" onClick={() => resolveApproval(ap.call_id, 'approve')}>批准</Button>
                      <Button size="small" danger onClick={() => resolveApproval(ap.call_id, 'reject')}>拒绝</Button>
                    </div>
                  </div>
                ))}
              </div>
            )}

            {/* 流式输出 */}
            <div ref={streamBoxRef} className="flex-1 overflow-y-auto min-h-0 space-y-3 border-t border-gray-200 dark:border-gray-700/50 pt-3">
              {active.error && <div className="text-xs text-red-500 bg-red-50 dark:bg-red-500/10 rounded-md p-2">{active.error}</div>}
              {/* T4: 插话气泡(用户运行中指令) */}
              {(active.steers || []).length > 0 && (
                <div className="space-y-1.5">
                  {(active.steers || []).map((st) => (
                    <div key={st.id} className="flex justify-end">
                      <div className="max-w-[80%] flex items-start gap-1.5 bg-blue-500 text-white rounded-lg px-2.5 py-1.5 text-xs">
                        <MessageSquare className="h-3 w-3 mt-0.5 shrink-0 opacity-80" />
                        <span className="whitespace-pre-wrap break-words">{st.message}</span>
                      </div>
                    </div>
                  ))}
                </div>
              )}
              {Object.keys(outputs).length === 0 && !streamable && active.result_summary && (
                <p className="text-sm whitespace-pre-wrap text-muted-foreground">{active.result_summary}</p>
              )}
              {Object.keys(outputs).length === 0 && !streamable && !active.result_summary && (
                <p className="text-xs text-muted-foreground">
                  {active.status === 'planning'
                    ? '任务待启动。可手动编辑步骤, 或点击「AI 生成计划」让 AI 拆解目标, 然后「启动」。'
                    : '暂无执行输出。'}
                </p>
              )}
              {Object.entries(outputs).map(([idx, text]) => (
                <div key={idx}>
                  <p className="text-[10px] text-muted-foreground mb-1">
                    步骤 {Number(idx) + 1}{Number(idx) === liveStep && <Loader2 className="inline h-3 w-3 ml-1 animate-spin" />}
                  </p>
                  <pre className="text-xs whitespace-pre-wrap font-sans leading-relaxed">{text}</pre>
                </div>
              ))}
            </div>

            {/* T4: 运行中插话输入框 */}
            {(active.status === 'running' || active.status === 'paused' || active.status === 'waiting_approval') && !active.team_id && (
              <div className="flex items-end gap-2 border-t border-gray-200 dark:border-gray-700/50 pt-2.5">
                <MessageSquare className="h-3.5 w-3.5 text-muted-foreground shrink-0 mb-2" />
                <Input.TextArea
                  autoSize={{ minRows: 1, maxRows: 3 }}
                  value={steerText}
                  onChange={(e) => setSteerText(e.target.value)}
                  onPressEnter={(e) => { if (!e.shiftKey) { e.preventDefault(); steerTask() } }}
                  placeholder="插话: 给执行中的任务下达新指令(回车发送, Shift+回车换行)"
                />
                <Button size="small" type="primary" loading={steering} onClick={steerTask} disabled={!steerText.trim()}>发送</Button>
              </div>
            )}
          </>
        )}
      </div>

      {/* 右栏: 产物面板(模型 doc_write 落盘 → 实时刷新; 支持预览/下载/沉淀知识库) */}
      <div className="w-64 shrink-0 flex flex-col border border-gray-200 dark:border-gray-700/50 rounded-xl p-2.5 gap-2">
        <span className="text-sm font-semibold flex items-center gap-1.5 px-1"><FileText className="h-4 w-4" />产物</span>
        <div className="flex-1 overflow-y-auto space-y-1 min-h-0">
          {artifacts.length === 0 && (
            <div className="h-full flex items-center justify-center text-xs text-muted-foreground text-center px-2">
              暂无产物。任务执行中 AI 产出文档/报告后会自动出现在这里。
            </div>
          )}
          {artifacts.map((a) => (
            <div key={a.id} className="group flex items-center gap-1.5 p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800">
              <button onClick={() => openArtifact(a)} className="flex-1 min-w-0 text-left" title={previewing ? '读取中…' : '点击预览'}>
                <p className="text-xs font-medium truncate">{a.file_name}</p>
                <p className="text-[10px] text-muted-foreground">{(a.file_size / 1024).toFixed(1)} KB · {new Date(a.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</p>
              </button>
              {(a.file_type === 'text/markdown' || a.file_name.endsWith('.md')) && (
                <>
                  <button onClick={() => exportArtifactAs(a, 'docx')} disabled={!!exportingArt} className="p-1 rounded text-muted-foreground hover:text-blue-500 opacity-0 group-hover:opacity-100 transition-opacity disabled:opacity-50" title="导出 Word (.docx)">
                    {exportingArt === `${a.id}:docx` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <FileDown className="h-3.5 w-3.5" />}
                  </button>
                  <button onClick={() => exportArtifactAs(a, 'pptx')} disabled={!!exportingArt} className="p-1 rounded text-muted-foreground hover:text-blue-500 opacity-0 group-hover:opacity-100 transition-opacity disabled:opacity-50" title="AI 重编生成演示文稿 (.pptx)">
                    {exportingArt === `${a.id}:pptx` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Presentation className="h-3.5 w-3.5" />}
                  </button>
                </>
              )}
              <button onClick={() => downloadArtifact(a)} className="p-1 rounded text-muted-foreground hover:text-blue-500 opacity-0 group-hover:opacity-100 transition-opacity" title="下载">
                <Download className="h-3.5 w-3.5" />
              </button>
              <button onClick={() => toKnowledge(a)} className="p-1 rounded text-muted-foreground hover:text-emerald-500 opacity-0 group-hover:opacity-100 transition-opacity" title="沉淀为知识库草稿(在任务/对话中让 AI 执行)">
                <BookPlus className="h-3.5 w-3.5" />
              </button>
            </div>
          ))}
        </div>
      </div>

      {/* 产物预览 */}
      <Modal
        title={preview?.name}
        open={!!preview}
        onCancel={() => setPreview(null)}
        width={720}
        footer={[
          (preview?.type === 'text/markdown' || preview?.name.endsWith('.md')) && (() => {
            const a = artifacts.find((x) => x.file_name === preview?.name)
            return a ? [
              <Button key="dl-docx" size="small" loading={exportingArt === `${a.id}:docx`} disabled={!!exportingArt} onClick={() => exportArtifactAs(a, 'docx', preview.content)}>
                <FileDown className="h-3 w-3 mr-1" />导出 docx
              </Button>,
              <Button key="dl-pptx" size="small" loading={exportingArt === `${a.id}:pptx`} disabled={!!exportingArt} onClick={() => exportArtifactAs(a, 'pptx', preview.content)}>
                <Presentation className="h-3 w-3 mr-1" />AI 生成演示
              </Button>,
            ] : null
          })(),
          <Button key="dl" size="small" onClick={() => { const a = artifacts.find((x) => x.file_name === preview?.name); if (a) downloadArtifact(a) }}>
            <Download className="h-3 w-3 mr-1" />下载
          </Button>,
          <Button key="close" size="small" type="primary" onClick={() => setPreview(null)}>关闭</Button>,
        ].flat().filter(Boolean) as any}
      >
        {preview && (preview.type === 'text/markdown' || preview.name.endsWith('.md')) ? (
          <div className="max-h-[60vh] overflow-y-auto"><AiMarkdown content={preview.content} /></div>
        ) : (
          <pre className="text-xs whitespace-pre-wrap max-h-[60vh] overflow-y-auto">{preview?.content}</pre>
        )}
      </Modal>

      {/* 新建任务 */}
      <Modal
        title="新建任务"
        open={creating}
        onCancel={() => setCreating(false)}
        onOk={createTask}
        confirmLoading={submitting}
        okText="创建"
        cancelText="取消"
      >
        <div className="space-y-3 pt-2">
          <div>
            <p className="text-sm mb-1.5">任务目标</p>
            <Input.TextArea
              rows={4}
              value={form.goal}
              onChange={(e) => setForm((f) => ({ ...f, goal: e.target.value }))}
              placeholder="描述要完成的任务, 例如: 梳理生产环境全部 MySQL 实例的版本分布, 输出巡检报告"
            />
          </div>
          <div className="flex items-center gap-3">
            <div className="flex-1">
              <p className="text-sm mb-1.5">权限模式</p>
              <Select
                className="w-full"
                value={form.permission_mode}
                onChange={(v) => setForm((f) => ({ ...f, permission_mode: v }))}
                options={[
                  { value: 'readonly', label: '只读(仅查询, 不写任何数据)' },
                  { value: 'standard', label: '标准确认(推荐, 写操作需确认)' },
                  { value: 'auto', label: '自动执行(仅不可逆操作需确认)' },
                  { value: 'full_auto', label: '完全托管(需管理员权限)' },
                ]}
              />
            </div>
            <div className="flex-1">
              <p className="text-sm mb-1.5">协作团队 <span className="text-muted-foreground">(可选)</span></p>
              <Select
                className="w-full"
                allowClear
                placeholder={teams.length ? '选择团队(多智能体并行)' : '暂无可用的团队'}
                value={form.team_id || undefined}
                onChange={(v) => setForm((f) => ({ ...f, team_id: v || '' }))}
                options={teams.map((tm) => ({ value: tm.id, label: `${tm.name}${tm.mode ? `（${tm.mode}）` : ''}` }))}
              />
            </div>
            <div className="flex-1">
              <p className="text-sm mb-1.5">优先级 <span className="text-muted-foreground">(并发满时排队序)</span></p>
              <Select
                className="w-full"
                value={form.priority}
                onChange={(v) => setForm((f) => ({ ...f, priority: v }))}
                options={[
                  { value: 1, label: '高(优先出队)' },
                  { value: 5, label: '普通' },
                  { value: 9, label: '低(让位给其他任务)' },
                ]}
              />
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm cursor-pointer">
            <input type="checkbox" checked={form.auto_start} onChange={(e) => setForm((f) => ({ ...f, auto_start: e.target.checked }))} />
            创建后自动规划并启动(AI 拆解步骤后直接执行)
          </label>
          <p className="text-[11px] text-muted-foreground">
            流程: 创建 → AI 规划步骤(可手动编辑) → 启动执行。写操作按所选权限模式判定: 只读模式拒绝写入; 标准模式下任务产物写入自动放行、跨域/高危操作会暂停等你批复。
          </p>
        </div>
      </Modal>
    </div>
  )
}
