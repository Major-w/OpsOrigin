
import { useEffect, useState, useRef, useCallback, useMemo, type ChangeEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTicketStore } from '@/stores/itsm/tickets'
import { useProcessStore } from '@/stores/itsm/processes'
import type { Ticket, TicketEvent, ITSMStats, ProcessInstance } from '@/stores/itsm/types'
import { DynamicForm, withBuiltinFields, BUILTIN_KEYS, type FormSchema } from '@/components/itsm/DynamicForm'
import { FormBuilder } from '@/components/itsm/FormBuilder'
import ProcessFlowView from '@/components/itsm/ProcessFlowView'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Tabs, message } from 'antd'
import { Switch, SwitchField } from '@/components/ui/switch'
import { formatTime, apiFetch, apiList } from '@/lib/utils'
import { useCan, useCanAny } from '@/stores/permissions'
import { useAuthStore } from '@/stores/auth'
import { useUserStore } from '@/stores/user'
import { useWebSocket } from '@/lib/useWebSocket'
import { Plus, Check, X, Play, RotateCcw, Clock, FileText, Activity, ChevronRight, Paperclip, MessageSquare, Upload, Download, Trash2, FileUp, FileDown, BookOpen, BookOpenCheck, Send, UserPlus, CheckCircle2, ListChecks, Bell, Link2 } from 'lucide-react'
import { RISK_LABELS, STATUS_LABELS, RISK_BADGE_VARIANT, parseFormSchema, applyStepFieldPerms } from '@/lib/itsm-constants'
const SLA_BY_RISK: Record<string, string> = { critical: '4小时', high: '24小时', medium: '48小时', low: '72小时' }

interface Comment {
  id: number
  ticket_id: string
  tenant_id: string
  author: string
  content: string
  created_at: string
}

interface Attachment {
  id: number
  ticket_id: string
  tenant_id: string
  file_name: string
  file_size: number
  file_type: string
  uploaded_by: string
  created_at: string
}

interface Subtask {
  id: number
  ticket_id: string
  tenant_id: string
  title: string
  description?: string
  assignee?: string
  status: 'pending' | 'in_progress' | 'done' | 'blocked'
  due_at?: string
  created_by?: string
  created_at: string
  updated_at?: string
  completed_at?: string | null
  // 派发任务增强字段
  detail?: string                 // 处理情况：处理人回填的执行过程/结果
  viewed_at?: string | null       // 首次查看时间（已认领）
  started_at?: string | null      // 进入处理中时间
  updated_by?: string             // 最后更新处理情况的人
  close_with_parent?: boolean     // 是否随主单关闭而自动关闭（默认 true）
  meta?: Record<string, any>      // 主单字段同步快照
}

const subtaskLabels: Record<string, string> = {
  pending: '待处理', in_progress: '处理中', done: '已完成', blocked: '受阻',
}
const subtaskVariants: Record<string, 'secondary' | 'default' | 'success' | 'destructive'> = {
  pending: 'secondary', in_progress: 'default', done: 'success', blocked: 'destructive',
}

function statusVariant(s: string): 'success' | 'error' | 'default' {
  if (s === 'approved' || s === 'succeeded') return 'success'
  if (s === 'rejected' || s === 'failed') return 'error'
  if (s === 'executing') return 'default'
  return 'default'
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}

export default function ITSMPage() {
  const navigate = useNavigate()
  // 当前流程节点允许展示的操作按钮 key；undefined = 未绑定流程 / 节点未配置 → 沿用默认全部显示（向后兼容）。
  const [currentStepActions, setCurrentStepActions] = useState<string[] | undefined>(undefined)
  const { tickets, stats, loading, fetch, create, approve, reject, execute, complete, rollback, submit, assign, close, withdraw, reopen, hold, resume, remind, relate, getTicket, getEvents, fetchStats } = useTicketStore()
  const { processes, fetchProcesses, getFormSchema, upsertFormSchema, getFormOptions, getFormOptionSources, getTicketProcess, updateTicketCustomFields } = useProcessStore()
  const canWrite = useCan('itsm:write')
  const canApprove = useCanAny(['itsm:approve', 'itsm:write'])
  const me = useAuthStore(s => s.user)
  const users = useUserStore(s => s.users)
  // 按钮可见性：未绑定流程或节点未配置 actions → 全部显示（向后兼容）；否则仅显示节点配置的按钮。
  const stepAllows = useCallback((key: string) => currentStepActions === undefined || currentStepActions.includes(key), [currentStepActions])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [myOnly, setMyOnly] = useState(false)
  const [form, setForm] = useState({ title: '', description: '', risk_level: 'low', change_plan: '', rollback_plan: '', created_by: '', process_id: '' })
  // 可配置表单：自定义字段值与创建用 schema
  const [customFields, setCustomFields] = useState<Record<string, any>>({})
  const [createSchema, setCreateSchema] = useState<FormSchema | null>(null)
  const [knowledgeRecs, setKnowledgeRecs] = useState<any[]>([])
  const [selectedTicket, setSelectedTicket] = useState<Ticket | null>(null)
  const [selectedSchema, setSelectedSchema] = useState<FormSchema | null>(null)
  // 流程步骤级表单：流程只有一张主表单，节点按 form_fields 权限决定「哪些字段在本节点可见/可编辑」。
  // stepScope 仅作为保存时回传的作用域（兼容旧版节点独立表单）；stepName 用于标题展示；
  // stepEditable=true 表示当前节点存在可编辑字段，才渲染保存按钮。
  const [stepScope, setStepScope] = useState('')
  const [stepName, setStepName] = useState('')
  const [stepEditable, setStepEditable] = useState(false)
  const [stepFields, setStepFields] = useState<Record<string, any>>({})
  const [stepSaving, setStepSaving] = useState(false)
  const [ticketEvents, setTicketEvents] = useState<TicketEvent[]>([])
  const [processInst, setProcessInst] = useState<ProcessInstance | null>(null)
  const [detailTab, setDetailTab] = useState('detail')
  const [approveDialog, setApproveDialog] = useState(false)
  const [approveComment, setApproveComment] = useState('')
  const [rejectDialog, setRejectDialog] = useState(false)
  const [rejectReason, setRejectReason] = useState('')
  const [completeDialog, setCompleteDialog] = useState(false)
  const [completeComment, setCompleteComment] = useState('')
  const [completeAssessment, setCompleteAssessment] = useState<'confirmed' | 'false_positive' | undefined>(undefined)
  const [rollbackDialog, setRollbackDialog] = useState(false)
  const [rollbackReason, setRollbackReason] = useState('')
  // 闭环操作: 送审(draft) / 指派(非终态) / 关闭(succeeded)
  const [assignDialog, setAssignDialog] = useState(false)
  const [assigneeInput, setAssigneeInput] = useState('')
  const [closeDialog, setCloseDialog] = useState(false)
  const [closeComment, setCloseComment] = useState('')
  const [closeSatisfaction, setCloseSatisfaction] = useState(0)
  const [ciIdInput, setCiIdInput] = useState('')
  const [ciIds, setCiIds] = useState<string[]>([])

  // Comments
  const [comments, setComments] = useState<Comment[]>([])
  const [commentContent, setCommentContent] = useState('')
  const [commentLoading, setCommentLoading] = useState(false)

  // Attachments
  const [attachments, setAttachments] = useState<Attachment[]>([])
  const [uploading, setUploading] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Subtasks
  const [subtasks, setSubtasks] = useState<Subtask[]>([])
  const [subtaskDialog, setSubtaskDialog] = useState(false)
  const [subtaskTitle, setSubtaskTitle] = useState('')
  const [subtaskAssignee, setSubtaskAssignee] = useState('')
  const [subtaskDesc, setSubtaskDesc] = useState('')
  const [subtaskDue, setSubtaskDue] = useState('')
  const [subtaskSaving, setSubtaskSaving] = useState(false)
  // 子任务增强：字段映射(sync_fields) + 随主单关闭开关(close_with_parent)
  const [subtaskSync, setSubtaskSync] = useState<string[]>([])
  const [subtaskCloseWithParent, setSubtaskCloseWithParent] = useState(true)
  // 子任务处理详情弹窗：查看即认领，回填处理情况
  const [subtaskDetail, setSubtaskDetail] = useState<Subtask | null>(null)
  const [subtaskDetailOpen, setSubtaskDetailOpen] = useState(false)
  const [subtaskDetailSaving, setSubtaskDetailSaving] = useState(false)

  // Import
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState<{ total: number; success: number; errors: { row: number; message: string }[] } | null>(null)
  const [importDialog, setImportDialog] = useState(false)
  const importFileRef = useRef<HTMLInputElement>(null)

  // 表单配置（管理端可配自定义字段 schema）
  const [configDialog, setConfigDialog] = useState(false)
  const [configJson, setConfigJson] = useState('')
  const [configSaving, setConfigSaving] = useState(false)
  // 可视化配置 / 专家 JSON 双模式
  const [configMode, setConfigMode] = useState<'visual' | 'json'>('visual')
  const [configSchema, setConfigSchema] = useState<FormSchema>({ version: 1, scope: 'ticket:default', fields: [] })
  // 动态选项来源清单（cmdb:<type> / iam:user|role|group）
  const [optionSources, setOptionSources] = useState<string[]>([])
  // 当前创建用作用域（catalog:<id> / process:<pid> / ticket:default），由来源或所选流程决定
  const [createScope, setCreateScope] = useState('ticket:default')
  const initialScopeRef = useRef('ticket:default')
  // 配置 Dialog 的 scope 选择（ticket 默认 / catalog 目录项 / process 流程 / process_step 流程步骤）
  const [configScopeType, setConfigScopeType] = useState<'ticket' | 'catalog' | 'process' | 'process_step'>('ticket')

  // WebSocket 实时更新
  useWebSocket({
    onEvent: (data) => {
      if (data.type === 'ticket.updated' && data.ticket) {
        // 刷新工单列表
        fetch()
        // 如果当前选中的工单被更新，刷新详情
        if (selectedTicket && data.ticket.id === selectedTicket.id) {
          getTicket(data.ticket.id).then(updated => {
            setSelectedTicket(updated)
          })
        }
      } else if (data.type === 'ola_breached') {
        message.error(`OLA 超时: 工单 ${data.ticket_id} — ${data.policy || ''}`)
        fetch()
      } else if (data.type === 'ola_warning') {
        message.warning(`OLA 预警: 工单 ${data.ticket_id} — ${data.policy || ''}`)
      } else if (data.type === 'hold_resumed') {
        message.success(`工单 ${data.ticket_id} 已自动恢复`)
        fetch()
      }
    },
  })
  const [configScopeId, setConfigScopeId] = useState('')
  const [configStepId, setConfigStepId] = useState('')

  const fetchComments = useCallback(async (ticketId: string) => {
    try {
      const data = await apiList<Comment>(`/itsm/tickets/${ticketId}/comments`)
      setComments(data || [])
    } catch { setComments([]); message.error('加载评论失败') }
  }, [])

  const fetchAttachments = useCallback(async (ticketId: string) => {
    try {
      const data = await apiList<Attachment>(`/itsm/tickets/${ticketId}/attachments`)
      setAttachments(data || [])
    } catch { setAttachments([]); message.error('加载附件失败') }
  }, [])

  const fetchSubtasks = useCallback(async (ticketId: string) => {
    try {
      const data = await apiList<Subtask>(`/itsm/tickets/${ticketId}/subtasks`)
      setSubtasks(data || [])
    } catch { setSubtasks([]); message.error('加载子任务失败') }
  }, [])

  useEffect(() => { fetch(); fetchStats(); fetchProcesses() }, [])

  // 评论/子任务的「用户」下拉：确保用户列表已加载（评论 @ 与子任务处理人共用）。
  useEffect(() => {
    const st = useUserStore.getState()
    if (st.users.length === 0) st.fetch()
  }, [])

  // 深链 ?id=<ticket>：从服务门户提交申请后「查看工单」跳进来，直接展开该工单详情。
  // 服务申请本身已收敛到服务门户内的抽屉表单完成，不再通过跳本页创建。
  useEffect(() => {
    try {
      const tid = new URLSearchParams(window.location.search).get('id')
      if (!tid) return
      ;(async () => {
        try {
          await fetchProcesses() // 先备好流程定义，详情才能按主表单+节点权限渲染
          const t = await getTicket(tid)
          if (t) await handleSelectTicket(t)
        } catch { message.error('工单不存在或无权访问') }
      })()
    } catch { /* ignore */ }
  }, [])

  // 知识推荐：监听标题变化, 匹配知识库文章
  useEffect(() => {
    if (!form.title || form.title.length < 2) { setKnowledgeRecs([]); return }
    const t = setTimeout(async () => {
      try {
        const data = await apiList<any>('/knowledge?search=' + encodeURIComponent(form.title))
        setKnowledgeRecs((data || []).slice(0, 3))
      } catch { setKnowledgeRecs([]) }
    }, 300)
    return () => clearTimeout(t)
  }, [form.title])

  // 动态选项加载器：带 source 的字段（cmdb:<type> / iam:user|role|group）用它拉取可选项。
  // parent 用于级联字段逐级取子节点（空串=根层级）。
  // useCallback 保持引用稳定，避免 DynamicForm 内部 effect 反复触发请求。
  const loadOptions = useCallback(
    (source: string, keyword?: string, parent?: string) => getFormOptions(source, keyword, parent),
    [getFormOptions],
  )

  const loadCreateSchema = async (scope: string) => {
    try {
      const schema = await getFormSchema(scope)
      setCreateSchema(schema)
    } catch { setCreateSchema(null) }
  }

  const openCreateDialog = async (scope = 'ticket:default') => {
    initialScopeRef.current = scope
    setCreateScope(scope)
    setForm({ title: '', description: '', risk_level: 'low', change_plan: '', rollback_plan: '', created_by: me?.username || 'anonymous', process_id: '' })
    setCustomFields({})
    setCiIds([])
    setDialogOpen(true)
    await loadCreateSchema(scope)
  }

  const handleCreate = async () => {
    const ok = await create({ ...form, ci_ids: ciIds, custom_fields: customFields, form_scope: createScope })
    if (!ok) return
    setDialogOpen(false)
    setCiIds([])
    setCustomFields({})
    setCreateSchema(null)
    setForm({ title: '', description: '', risk_level: 'low', change_plan: '', rollback_plan: '', created_by: me?.username || 'anonymous', process_id: '' })
    fetchStats()
  }

  const handleSelectTicket = async (ticket: Ticket) => {
    setSelectedTicket(ticket)
    setDetailTab('detail')
    setStepScope(''); setStepName(''); setStepEditable(false); setStepFields({}); setStepSaving(false)
    try {
      const events = await getEvents(ticket.id)
      setTicketEvents(events || [])
    } catch { setTicketEvents([]) }
    fetchComments(ticket.id)
    fetchAttachments(ticket.id)
    fetchSubtasks(ticket.id)

    // 表单解析：流程主表单（流程设计第一步产出）为唯一数据源，
    // 当前节点按 form_fields 权限投影出「本节点可见/可编辑」的字段子集。
    // 未绑定流程或流程没主表单时，回退到 ticket.form_scope 对应的作用域表单（兼容存量）。
    let scope = ticket.form_scope || 'ticket:default'
    let schema: FormSchema | null = null
    setCurrentStepActions(undefined) // 默认全部显示，绑定流程且节点配置 actions 时再收窄
    if (ticket.process_id) {
      // 直接读 store 快照：深链 ?id= 场景下 render 闭包里的 processes 可能还没拉回来
      const procList = useProcessStore.getState().processes
      const proc = (procList.length ? procList : processes).find(p => p.id === ticket.process_id)
      const main = parseFormSchema(proc?.form)
      if (main.fields.length) schema = main
      try {
        const inst = await getTicketProcess(ticket.id)
        setProcessInst(inst || null)
        const step = inst?.current_step ? proc?.steps?.find(s => s.id === inst.current_step) : undefined
        if (step) {
          setStepName(step.name || '')
          setCurrentStepActions(step.actions) // 节点配置了 actions 则按配置收窄按钮
          if (schema) {
            const projected = applyStepFieldPerms(schema, step.form_fields)
            schema = projected
            const editable = (projected?.fields || []).some(f => !f.readonly)
            setStepEditable(step.type === 'user' && editable)
            setStepScope(ticket.form_scope || '')
          } else if (step.form_scope) {
            // 存量流程：节点仍挂着独立表单作用域
            scope = step.form_scope
            setStepScope(step.form_scope)
            setStepEditable(true)
          }
        }
      } catch { /* 流程实例不可用时回退默认作用域 */ }
    }
    try {
      if (!schema) schema = await getFormSchema(scope)
      setSelectedSchema(schema)
      setStepFields({ ...(ticket.custom_fields || {}) })
    } catch { setSelectedSchema(null) }
  }

  const handleSaveStepForm = async () => {
    // 主表单模式下 stepScope 允许为空（服务端按空作用域跳过二次校验，仍会推进流程）
    if (!selectedTicket || !stepEditable) return
    setStepSaving(true)
    const ok = await updateTicketCustomFields(selectedTicket.id, stepScope, stepFields)
    setStepSaving(false)
    if (ok) {
      try {
        const t = await getTicket(selectedTicket.id)
        setSelectedTicket(t)
        setStepFields({ ...(t.custom_fields || {}) })
      } catch { /* 忽略刷新失败 */ }
    }
  }

  const resolveConfigScope = () => {
    if (configScopeType === 'catalog') return `catalog:${configScopeId.trim()}`
    if (configScopeType === 'process') return `process:${configScopeId.trim()}`
    if (configScopeType === 'process_step') return `process:${configScopeId.trim()}:step:${configStepId.trim()}`
    return 'ticket:default'
  }

  const openConfigDialog = async () => {
    const scope = resolveConfigScope()
    let seed: FormSchema = { version: 1, scope, fields: [] }
    try {
      const schema = await getFormSchema(scope)
      if (schema) seed = { ...schema, scope }
    } catch { /* 取不到就用空 schema 起步 */ }
    setConfigSchema(seed)
    setConfigJson(JSON.stringify(seed, null, 2))
    setConfigDialog(true)
    // 拉取可用的动态选项来源，供字段属性面板下拉选择
    getFormOptionSources().then(setOptionSources).catch(() => setOptionSources([]))
  }

  // 可视化 ↔ JSON 双向同步：切到 JSON 时序列化当前 schema，切回时解析回结构
  const switchConfigMode = (mode: 'visual' | 'json') => {
    if (mode === configMode) return
    if (mode === 'json') {
      setConfigJson(JSON.stringify(configSchema, null, 2))
    } else {
      try {
        const parsed = JSON.parse(configJson) as FormSchema
        setConfigSchema({ version: parsed.version || 1, scope: resolveConfigScope(), fields: parsed.fields || [] })
      } catch {
        message.error('JSON 格式错误，无法切回可视化模式')
        return
      }
    }
    setConfigMode(mode)
  }

  const handleSaveConfig = async () => {
    const scope = resolveConfigScope()
    let next: FormSchema = configSchema
    if (configMode === 'json') {
      try {
        const parsed = JSON.parse(configJson) as FormSchema
        next = { version: parsed.version || 1, scope, fields: parsed.fields || [] }
      } catch {
        message.error('JSON 格式错误，请检查')
        return
      }
    }
    const keys = (next.fields || []).map(f => (f.key || '').trim())
    if (keys.some(k => !k)) {
      message.error('存在未填写字段标识 key 的字段')
      return
    }
    if (new Set(keys).size !== keys.length) {
      message.error('字段标识 key 存在重复，请修正')
      return
    }
    setConfigSaving(true)
    const ok = await upsertFormSchema(scope, { version: next.version || 1, scope, fields: next.fields || [] })
    setConfigSaving(false)
    if (ok) setConfigDialog(false)
  }

  const handleApprove = async () => {
    if (!selectedTicket) return
    await approve(selectedTicket.id, approveComment)
    setApproveDialog(false); setApproveComment('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleReject = async () => {
    if (!selectedTicket) return
    await reject(selectedTicket.id, rejectReason)
    setRejectDialog(false); setRejectReason('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleExecute = async () => {
    if (!selectedTicket) return
    await execute(selectedTicket.id)
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleComplete = async () => {
    if (!selectedTicket) return
    await complete(selectedTicket.id, completeComment, completeAssessment)
    setCompleteDialog(false); setCompleteComment(''); setCompleteAssessment(undefined)
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleRollback = async () => {
    if (!selectedTicket) return
    await rollback(selectedTicket.id, rollbackReason)
    setRollbackDialog(false); setRollbackReason('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  // SN 补全操作: 撤回/重开/挂起恢复/催办/关联(无对话框, 直接调用 + toast 由 store 处理)
  const [confirmDialog, setConfirmDialog] = useState<{ title: string; action: () => Promise<void> } | null>(null)
  const [remindDialog, setRemindDialog] = useState(false)
  const [remindMsg, setRemindMsg] = useState('')
  const [relateDialog, setRelateDialog] = useState(false)
  const [relateId, setRelateId] = useState('')

  const handleWithdraw = async () => {
    if (!selectedTicket) return
    await withdraw(selectedTicket.id, '操作人撤回')
    await handleSelectTicket(await getTicket(selectedTicket.id)); fetchStats()
  }
  const handleReopen = async () => {
    if (!selectedTicket) return
    await reopen(selectedTicket.id, '重新激活')
    await handleSelectTicket(await getTicket(selectedTicket.id)); fetchStats()
  }
  const handleHold = async () => {
    if (!selectedTicket) return
    await hold(selectedTicket.id, '人工挂起')
    await handleSelectTicket(await getTicket(selectedTicket.id)); fetchStats()
  }
  const handleResume = async () => {
    if (!selectedTicket) return
    await resume(selectedTicket.id)
    await handleSelectTicket(await getTicket(selectedTicket.id)); fetchStats()
  }
  const handleRemind = async () => {
    if (!selectedTicket) return
    await remind(selectedTicket.id, remindMsg)
    setRemindDialog(false); setRemindMsg('')
  }
  const handleRelate = async () => {
    if (!selectedTicket || !relateId) return
    await relate(selectedTicket.id, relateId.trim())
    setRelateDialog(false); setRelateId('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
  }

  // 闭环: 草稿送审(低/中风险直通执行, 高风险进审批)
  const handleSubmitTicket = async () => {
    if (!selectedTicket) return
    await submit(selectedTicket.id)
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  // 闭环: 指派负责人(不改变状态)
  const handleAssignTicket = async () => {
    if (!selectedTicket || !assigneeInput.trim()) return
    await assign(selectedTicket.id, assigneeInput.trim())
    setAssignDialog(false); setAssigneeInput('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  // 闭环: 关单(succeeded → closed, 终态)
  const handleCloseTicket = async () => {
    if (!selectedTicket) return
    await close(selectedTicket.id, closeComment, closeSatisfaction)
    setCloseDialog(false); setCloseComment(''); setCloseSatisfaction(0)
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleConvertToKnowledge = async (ticket: Ticket) => {
    const content = [
      '## 问题描述', ticket.description || '',
      ticket.change_plan ? '## 变更方案\n' + ticket.change_plan : '',
      ticket.rollback_plan ? '## 回滚方案\n' + ticket.rollback_plan : '',
      '## 处理结果',
      '- 风险等级: ' + ticket.risk_level,
      '- 处理人: ' + (ticket.completed_by || ticket.created_by),
      ticket.completed_at ? '- 完成时间: ' + formatTime(ticket.completed_at) : '',
    ].filter(Boolean).join('\n\n')
    try {
      await apiFetch('/knowledge', { method: 'POST', body: JSON.stringify({ title: ticket.title + ' - 处理总结', content, category: '故障处理', tags: [ticket.risk_level, '工单转换'], author: me?.username || 'anonymous' }) })
      navigate('/knowledge')
    } catch (e: any) { message.error('转换失败: ' + e.message) }
  }

  const handleAddComment = async () => {
    if (!selectedTicket || !commentContent.trim()) return
    setCommentLoading(true)
    try {
      await apiFetch<Comment>(`/itsm/tickets/${selectedTicket.id}/comments`, {
        method: 'POST',
        body: JSON.stringify({ author: me?.username || 'anonymous', content: commentContent }),
      })
      setCommentContent('')
      setMentionOpen(false)
      fetchComments(selectedTicket.id)
    } catch { message.error('添加评论失败') }
    setCommentLoading(false)
  }

  // 评论 @ 用户：输入 @ 后弹出用户列表，选中后插入 @用户名。
  const [mentionOpen, setMentionOpen] = useState(false)
  const [mentionQuery, setMentionQuery] = useState('')
  const commentRef = useRef<any>(null)
  const mentionMatches = useMemo(() => {
    const q = mentionQuery.trim().toLowerCase()
    return (users || []).filter(u => !q || u.username.toLowerCase().includes(q) || (u.email || '').toLowerCase().includes(q)).slice(0, 8)
  }, [users, mentionQuery])

  const onCommentChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value
    setCommentContent(val)
    const pos = e.target.selectionStart ?? val.length
    const m = val.slice(0, pos).match(/@([\w\u4e00-\u9fa5]*)$/)
    if (m) { setMentionQuery(m[1]); setMentionOpen(true) }
    else setMentionOpen(false)
  }
  const pickMention = (username: string) => {
    const ta = commentRef.current
    const val = commentContent
    const pos = ta ? ta.selectionStart ?? val.length : val.length
    const before = val.slice(0, pos).replace(/@([\w\u4e00-\u9fa5]*)$/, '@' + username + ' ')
    const after = val.slice(pos)
    const next = before + after
    setCommentContent(next)
    setMentionOpen(false)
    if (ta) {
      requestAnimationFrame(() => {
        ta.focus()
        const p = before.length
        ta.setSelectionRange(p, p)
      })
    }
  }

  const handleDeleteComment = async (cid: number) => {
    if (!selectedTicket) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/comments/${cid}`, { method: 'DELETE' })
      fetchComments(selectedTicket.id)
    } catch { message.error('删除评论失败') }
  }

  // --- Subtasks ---
  const handleCreateSubtask = async () => {
    if (!selectedTicket || !subtaskTitle.trim()) return
    setSubtaskSaving(true)
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/subtasks`, {
        method: 'POST',
        body: JSON.stringify({
          title: subtaskTitle.trim(),
          description: subtaskDesc.trim(),
          assignee: subtaskAssignee.trim(),
          due_at: subtaskDue ? new Date(subtaskDue).toISOString() : null,
          created_by: me?.username || 'anonymous',
          sync_fields: subtaskSync,
          close_with_parent: subtaskCloseWithParent,
        }),
      })
      setSubtaskDialog(false); setSubtaskTitle(''); setSubtaskDesc(''); setSubtaskAssignee(''); setSubtaskDue('')
      setSubtaskSync([]); setSubtaskCloseWithParent(true)
      fetchSubtasks(selectedTicket.id)
    } catch { message.error('创建子任务失败') }
    setSubtaskSaving(false)
  }

  // 打开子任务处理详情：首次查看即认领(pending→in_progress)，可回填处理情况/更新字段
  const openSubtaskDetail = async (st: Subtask) => {
    if (!selectedTicket) return
    try {
      const data = await apiFetch<Subtask>(`/itsm/tickets/${selectedTicket.id}/subtasks/${st.id}`)
      setSubtaskDetail(data || st)
    } catch { setSubtaskDetail(st) }
    setSubtaskDetailOpen(true)
  }

  const handleSaveSubtaskDetail = async () => {
    if (!selectedTicket || !subtaskDetail) return
    setSubtaskDetailSaving(true)
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/subtasks/${subtaskDetail.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          detail: subtaskDetail.detail ?? '',
          close_with_parent: subtaskDetail.close_with_parent,
          updated_by: me?.username || 'anonymous',
        }),
      })
      message.success('已保存处理情况')
      setSubtaskDetailOpen(false)
      fetchSubtasks(selectedTicket.id)
    } catch { message.error('保存处理情况失败') }
    setSubtaskDetailSaving(false)
  }

  // 可同步到子任务的主单字段（系统字段 + 自定义字段），供新建时勾选映射
  const subtaskSyncOptions = useMemo<{ key: string; label: string }[]>(() => {
    if (!selectedTicket) return []
    const opts: { key: string; label: string }[] = [
      { key: 'risk_level', label: '风险等级' },
      { key: 'title', label: '标题' },
      { key: 'description', label: '描述' },
    ]
    const cf = (selectedTicket as any).custom_fields || {}
    for (const k of Object.keys(cf)) opts.push({ key: `custom_fields.${k}`, label: `自定义字段: ${k}` })
    return opts
  }, [selectedTicket])

  const handleToggleSubtask = async (st: Subtask) => {
    if (!selectedTicket) return
    const next = st.status === 'done' ? 'pending' : 'done'
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/subtasks/${st.id}`, {
        method: 'PUT',
        body: JSON.stringify({ status: next }),
      })
      fetchSubtasks(selectedTicket.id)
    } catch { message.error('更新子任务失败') }
  }

  const handleSetSubtaskStatus = async (st: Subtask, status: string) => {
    if (!selectedTicket || status === st.status) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/subtasks/${st.id}`, {
        method: 'PUT',
        body: JSON.stringify({ status }),
      })
      fetchSubtasks(selectedTicket.id)
    } catch { message.error('更新子任务失败') }
  }

  const handleDeleteSubtask = async (sid: number) => {
    if (!selectedTicket) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/subtasks/${sid}`, { method: 'DELETE' })
      fetchSubtasks(selectedTicket.id)
    } catch { message.error('删除子任务失败') }
  }

  const handleUploadFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file || !selectedTicket) return
    setUploading(true)
    try {
      const formData = new FormData()
      formData.append('file', file)
      formData.append('uploaded_by', me?.username || 'anonymous')
      await apiFetch<Attachment>(`/itsm/tickets/${selectedTicket.id}/attachments`, {
        method: 'POST',
        headers: {},
        body: formData,
      })
      fetchAttachments(selectedTicket.id)
    } catch { message.error('上传附件失败') }
    setUploading(false)
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const handleDeleteAttachment = async (aid: number) => {
    if (!selectedTicket) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/attachments/${aid}`, { method: 'DELETE' })
      fetchAttachments(selectedTicket.id)
    } catch { message.error('删除附件失败') }
  }

  const addCiId = () => {
    if (ciIdInput.trim() && !ciIds.includes(ciIdInput.trim())) {
      setCiIds([...ciIds, ciIdInput.trim()]); setCiIdInput('')
    }
  }

  const handleImportCSV = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setImporting(true)
    try {
      const formData = new FormData()
      formData.append('file', file)
      const result = await apiFetch<{ total: number; success: number; errors: { row: number; message: string }[] }>('/itsm/tickets/import', {
        method: 'POST',
        headers: {},
        body: formData,
      })
      setImportResult(result)
      setImportDialog(true)
      fetch()
      fetchStats()
    } catch { message.error('CSV 导入失败') }
    setImporting(false)
    if (importFileRef.current) importFileRef.current.value = ''
  }

  const handleExportTickets = () => {
    window.open('/api/v1/itsm/tickets/export/csv', '_blank')
  }

  const handleExportStats = () => {
    window.open('/api/v1/itsm/stats/export/csv', '_blank')
  }

  const isOverdue = (ticket: Ticket) => {
    if (!ticket.deadline_at || ticket.status === 'succeeded' || ticket.status === 'rejected' || ticket.status === 'rolled_back' || ticket.status === 'closed') return false
    return new Date(ticket.deadline_at) < new Date()
  }

  // 「我的工单」筛选：仅看由我创建或指派给我的工单
  const ticketView = myOnly && me
    ? tickets.filter(t => t.created_by === me.username || (t.assignee || '') === me.username)
    : tickets

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">ITSM 工单</h1><p className="text-sm text-muted-foreground">变更审批与风险控制</p></div>
        <div className="flex gap-2">
          <input ref={importFileRef} type="file" accept=".csv" className="hidden" onChange={handleImportCSV} />
          <Button type="default" onClick={() => importFileRef.current?.click()} disabled={importing || !canWrite}><FileUp className="h-4 w-4" />{importing ? '导入中...' : '批量导入'}</Button>
          <Button type="default" onClick={handleExportTickets}><FileDown className="h-4 w-4" />导出工单</Button>
          <Button type="default" onClick={handleExportStats}><FileDown className="h-4 w-4" />导出统计</Button>
          <Button type="default" onClick={openConfigDialog} disabled={!canWrite}><FileText className="h-4 w-4" />表单配置</Button>
          <Button type="primary" onClick={() => openCreateDialog()} disabled={!canWrite}><Plus className="h-4 w-4" />新建工单</Button>
        </div>
      </div>

      {stats && (
        <div className="grid grid-cols-6 gap-3">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">总工单</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">审批通过率</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{(stats.approval_rate * 100).toFixed(1)}%</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">平均审批</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{stats.avg_approval_ms > 0 ? `${(stats.avg_approval_ms / 1000).toFixed(1)}秒` : '-'}</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">平均执行</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{stats.avg_execution_ms > 0 ? `${(stats.avg_execution_ms / 1000).toFixed(1)}秒` : '-'}</span></CardContent>
          </Card>
          <Card className="col-span-2">
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">状态分布</CardTitle></CardHeader>
            <CardContent className="pt-0">
              <div className="flex gap-2 flex-wrap">
                {Object.entries(stats.by_status).filter(([,v]) => v > 0).map(([k, v]) => (
                  <Tag key={k} color={statusVariant(k)} className="text-xs">{STATUS_LABELS[k] || k}: {v}</Tag>
                ))}
              </div>
            </CardContent>
          </Card>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <div className="lg:col-span-1">
          <Card>
            <CardHeader className="py-3 flex items-center justify-between">
              <CardTitle className="text-sm">工单列表{myOnly ? ' · 我的工单' : ''}（{ticketView.length}）</CardTitle>
              <button
                type="button"
                onClick={() => setMyOnly(v => !v)}
                className={`text-xs rounded-full px-2.5 py-1 border transition-colors ${myOnly ? 'bg-primary text-primary-foreground border-primary' : 'border-input text-muted-foreground hover:bg-accent'}`}
                title="仅显示由我创建或指派给我的工单"
              >
                我的工单{myOnly ? ' ✓' : ''}
              </button>
            </CardHeader>
            <CardContent className="p-0">
              {loading ? <p className="px-4 text-sm text-muted-foreground">加载中...</p> : ticketView.length === 0 ? <p className="px-4 text-sm text-muted-foreground">暂无工单。</p> : (
                <div className="flex flex-col">
                  {ticketView.map(t => (
                    <div key={t.id} onClick={() => handleSelectTicket(t)}
                      className={`px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors ${selectedTicket?.id === t.id ? 'bg-accent border-l-2 border-l-blue-500' : ''} ${isOverdue(t) ? 'border-l-2 border-l-destructive' : ''}`}>
                      <div className="flex items-center justify-between">
                        <span className="font-medium text-sm truncate">{t.title}</span>
                        <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
                      </div>
                      <div className="flex gap-1.5 mt-1.5 flex-wrap">
                        <Tag color={RISK_BADGE_VARIANT[t.risk_level] || 'secondary'} className="text-xs">{RISK_LABELS[t.risk_level] || t.risk_level}</Tag>
                        <Tag color={statusVariant(t.status)} className="text-xs">{STATUS_LABELS[t.status] || t.status}</Tag>
                        {isOverdue(t) && <Tag color="destructive" className="text-xs"><Clock className="h-3 w-3 mr-0.5" />SLA 逾期</Tag>}
                      </div>
                      <p className="text-xs text-muted-foreground mt-1">{t.created_by} · {formatTime(t.created_at)}</p>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>

        <div className="lg:col-span-2">
          {selectedTicket ? (
            <Card>
              <CardHeader className="py-3">
                <div className="flex items-center justify-between">
                  <div>
                    <CardTitle className="text-lg">{selectedTicket.title}</CardTitle>
                    <CardDescription className="text-xs">ID: {selectedTicket.id.slice(0, 12)}... · {selectedTicket.created_by} · {formatTime(selectedTicket.created_at)}</CardDescription>
                  </div>
                  <div className="flex gap-1">
                    {!['rejected', 'rolled_back', 'closed', 'withdrawn'].includes(selectedTicket.status) && stepAllows('assign') && (
                      <Button type="default" size="middle" onClick={() => { setAssignDialog(true); setAssigneeInput(selectedTicket.assignee || '') }} disabled={!canWrite}><UserPlus className="h-3 w-3" />指派</Button>
                    )}
                    {selectedTicket.status === 'draft' && stepAllows('submit') && (
                      <Button type="default" size="middle" onClick={handleSubmitTicket} disabled={!canWrite}><Send className="h-3 w-3" />提交</Button>
                    )}
                    {selectedTicket.status === 'draft' && stepAllows('withdraw') && (
                      <Button type="default" size="middle" onClick={handleWithdraw} disabled={!canWrite}><X className="h-3 w-3" />撤回</Button>
                    )}
                    {selectedTicket.status === 'closed' && stepAllows('reopen') && (
                      <Button type="default" size="middle" onClick={handleReopen} disabled={!canWrite}><RotateCcw className="h-3 w-3" />重开</Button>
                    )}
                    {selectedTicket.status === 'pending_review' && stepAllows('approve') && (
                      <Button type="default" size="middle" onClick={() => { setApproveDialog(true); setApproveComment('') }} disabled={!canApprove}><Check className="h-3 w-3" />通过</Button>
                    )}
                    {selectedTicket.status === 'pending_review' && stepAllows('reject') && (
                      <Button type="default" size="middle" onClick={() => { setRejectDialog(true); setRejectReason('') }} disabled={!canApprove}><X className="h-3 w-3" />驳回</Button>
                    )}
                    {selectedTicket.status === 'approved' && stepAllows('execute') && (
                      <Button type="default" size="middle" onClick={handleExecute} disabled={!canWrite}><Play className="h-3 w-3" />执行</Button>
                    )}
                    {selectedTicket.status === 'executing' && stepAllows('complete') && (
                      <Button type="default" size="middle" onClick={() => { setCompleteDialog(true); setCompleteComment('') }} disabled={!canWrite}>完成</Button>
                    )}
                    {(selectedTicket.status === 'failed' || selectedTicket.status === 'succeeded') && stepAllows('rollback') && (
                      <Button type="default" size="middle" onClick={() => { setRollbackDialog(true); setRollbackReason('') }} disabled={!canWrite}><RotateCcw className="h-3 w-3" />回滚</Button>
                    )}
                    {selectedTicket.status === 'succeeded' && stepAllows('close') && (
                      <Button type="default" size="middle" onClick={() => { setCloseDialog(true); setCloseComment(''); setCloseSatisfaction(0) }} disabled={!canWrite}><CheckCircle2 className="h-3 w-3" />关闭</Button>
                    )}
                    {selectedTicket.status === 'succeeded' && stepAllows('convert') && (
                      <Button type="default" size="middle" onClick={() => handleConvertToKnowledge(selectedTicket)} disabled={!canWrite}><BookOpen className="h-3 w-3 mr-1"/>转知识</Button>
                    )}
                    {!['rejected', 'rolled_back', 'closed', 'withdrawn'].includes(selectedTicket.status) && (
                      <>
                        {selectedTicket.on_hold ? (
                          stepAllows('resume') && <Button type="default" size="middle" onClick={handleResume} disabled={!canWrite}><Play className="h-3 w-3" />恢复</Button>
                        ) : (
                          stepAllows('hold') && <Button type="default" size="middle" onClick={handleHold} disabled={!canWrite}><Clock className="h-3 w-3" />挂起</Button>
                        )}
                        {stepAllows('remind') && <Button type="default" size="middle" onClick={() => setRemindDialog(true)} disabled={!canWrite}><Bell className="h-3 w-3" />催办</Button>}
                        {stepAllows('relate') && <Button type="default" size="middle" onClick={() => setRelateDialog(true)} disabled={!canWrite}><Link2 className="h-3 w-3" />关联</Button>}
                        {stepAllows('change') && <Button type="default" size="middle" onClick={() => navigate(`/itsm/change?ticket=${selectedTicket.id}`)} disabled={!canWrite}><FileText className="h-3 w-3" />变更</Button>}
                      </>
                    )}
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-3 gap-2 text-sm mb-3">
                  <div><span className="text-muted-foreground">风险:</span> <Tag color={RISK_BADGE_VARIANT[selectedTicket.risk_level]} className="text-xs">{RISK_LABELS[selectedTicket.risk_level]}</Tag></div>
                  <div><span className="text-muted-foreground">状态:</span> <Tag color={statusVariant(selectedTicket.status)} className="text-xs">{STATUS_LABELS[selectedTicket.status]}</Tag></div>
                  <div><span className="text-muted-foreground">流程:</span> {selectedTicket.process_id ? <Tag color="outline" className="text-xs">{processes.find(p => p.id === selectedTicket.process_id)?.name || selectedTicket.process_id.slice(0, 8)}</Tag> : '无'}</div>
                  <div><span className="text-muted-foreground">SLA:</span> <span className={isOverdue(selectedTicket) ? 'text-destructive font-medium' : ''}>{SLA_BY_RISK[selectedTicket.risk_level] || '72小时'}{isOverdue(selectedTicket) && ' 已逾期'}</span></div>
                  {selectedTicket.deadline_at && <div className="col-span-3"><span className="text-muted-foreground">截止:</span> <Clock className="h-3 w-3 inline mr-1" />{formatTime(selectedTicket.deadline_at)}</div>}
                  {selectedTicket.scheduled_at && <div className="col-span-3"><span className="text-muted-foreground">计划执行:</span> {formatTime(selectedTicket.scheduled_at)}</div>}
                  {selectedTicket.assignee && <div><span className="text-muted-foreground">负责人:</span> {selectedTicket.assignee}</div>}
                  {selectedTicket.approved_by && <div><span className="text-muted-foreground">审批人:</span> {selectedTicket.approved_by}</div>}
                  {selectedTicket.approved_at && <div><span className="text-muted-foreground">审批时间:</span> {formatTime(selectedTicket.approved_at)}</div>}
                  {selectedTicket.reject_reason && <div className="col-span-3"><span className="text-muted-foreground">驳回原因:</span> <span className="text-destructive">{selectedTicket.reject_reason}</span></div>}
                  {selectedTicket.completed_by && <div><span className="text-muted-foreground">完成人:</span> {selectedTicket.completed_by}</div>}
                  {selectedTicket.completed_at && <div><span className="text-muted-foreground">完成时间:</span> {formatTime(selectedTicket.completed_at)}</div>}
                  {selectedTicket.rolled_back_by && <div><span className="text-muted-foreground">回滚人:</span> {selectedTicket.rolled_back_by}</div>}
                  {selectedTicket.rollback_reason && <div className="col-span-3"><span className="text-muted-foreground">回滚原因:</span> <span className="text-amber-400">{selectedTicket.rollback_reason}</span></div>}
                  {selectedTicket.status === 'closed' && (selectedTicket.satisfaction ?? 0) > 0 && (
                    <div><span className="text-muted-foreground">满意度:</span> <span className="text-amber-400">{'★'.repeat(selectedTicket.satisfaction ?? 0)}<span className="text-muted-foreground/50">{'★'.repeat(5 - (selectedTicket.satisfaction ?? 0))}</span></span></div>
                  )}
                  {selectedTicket.labels?.source === 'ai_inspection' && (
                    <div className="col-span-3">
                      <span className="text-muted-foreground">AI 巡检:</span>{' '}
                      {selectedTicket.labels?.kb_archived === 'true' ? (
                        <Tag color="success" className="text-xs gap-1">
                          <BookOpenCheck className="h-3 w-3" />已沉淀知识库
                        </Tag>
                      ) : (
                        <Tag color="secondary" className="text-xs">巡检发现转单</Tag>
                      )}
                    </div>
                  )}
                </div>

                <Tabs activeKey={detailTab} onChange={setDetailTab} items={[
                  {
                    key: 'detail',
                    label: <><FileText className="w-3 h-3 mr-1" />详情</>,
                    children: <div className="mt-3 space-y-3">
                    {selectedTicket.description && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">描述</h4>
                        <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selectedTicket.description}</p>
                      </div>
                    )}
                    {selectedTicket.change_plan && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">变更计划</h4>
                        <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selectedTicket.change_plan}</p>
                      </div>
                    )}
                    {selectedTicket.rollback_plan && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">回滚计划</h4>
                        <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selectedTicket.rollback_plan}</p>
                      </div>
                    )}
                    {(selectedTicket.ci_ids || []).length > 0 && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">关联资产</h4>
                        <div className="flex gap-1 flex-wrap">
                          {(selectedTicket.ci_ids || []).map(ciId => (
                            <Tag key={ciId} color="default" className="text-xs">{ciId.slice(0, 12)}...</Tag>
                          ))}
                        </div>
                      </div>
                    )}
                    {(stepEditable || stepName) && selectedSchema && (selectedSchema.fields || []).length > 0 ? (
                      <div>
                        <h4 className="text-sm font-medium mb-1">
                          流程表单
                          {stepName && <span className="text-xs font-normal text-muted-foreground">（当前节点：{stepName}）</span>}
                        </h4>
                        <DynamicForm
                          schema={selectedSchema}
                          values={stepFields}
                          context={selectedTicket as unknown as Record<string, unknown>}
                          onChange={(k, v) => setStepFields(f => ({ ...f, [k]: v }))}
                          loadOptions={loadOptions}
                          readOnly={!stepEditable}
                          ticketId={selectedTicket.id}
                        />
                        {stepEditable && (
                          <div className="pt-1">
                            <Button size="middle" type="primary" onClick={handleSaveStepForm} disabled={stepSaving}>
                              {stepSaving ? '保存中…' : '保存并推进'}
                            </Button>
                          </div>
                        )}
                      </div>
                    ) : (selectedTicket.custom_fields && Object.keys(selectedTicket.custom_fields).length > 0) && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">自定义字段</h4>
                        <DynamicForm
                          schema={selectedSchema}
                          values={selectedTicket.custom_fields}
                          context={selectedTicket as unknown as Record<string, unknown>}
                          onChange={() => {}}
                          loadOptions={loadOptions}
                          readOnly
                          ticketId={selectedTicket.id}
                        />
                      </div>
                    )}
                    </div>,
                  },
                  {
                    key: 'sla',
                    label: <><Clock className="w-3 h-3 mr-1" />SLA</>,
                    children: <div className="mt-3 space-y-4">
                    <div className="flex items-center justify-between">
                      <h3 className="text-sm font-medium">SLA 概览</h3>
                      <Button type="default" size="middle" onClick={() => navigate(`/itsm/tickets/${selectedTicket.id}/sla`)}>
                        <Clock className="h-3 w-3 mr-1" />SLA 详情
                      </Button>
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                      <div className="border rounded-md p-3">
                        <p className="text-xs text-muted-foreground">风险等级</p>
                        <p className="text-lg font-medium">{RISK_LABELS[selectedTicket.risk_level] || selectedTicket.risk_level}</p>
                      </div>
                      <div className="border rounded-md p-3">
                        <p className="text-xs text-muted-foreground">SLA 目标</p>
                        <p className="text-lg font-medium">{SLA_BY_RISK[selectedTicket.risk_level] || '-'}</p>
                      </div>
                    </div>

                    {/* 时间线 */}
                    <div className="border rounded-md p-3 space-y-3">
                      <h4 className="text-sm font-medium">时间线</h4>
                      <div className="space-y-2 text-sm">
                        <div className="flex justify-between">
                          <span className="text-muted-foreground">创建时间</span>
                          <span>{formatTime(selectedTicket.created_at)}</span>
                        </div>
                        {selectedTicket.approved_at && (
                          <div className="flex justify-between">
                            <span className="text-muted-foreground">审批时间</span>
                            <span>{formatTime(selectedTicket.approved_at)}</span>
                          </div>
                        )}
                        {selectedTicket.executed_at && (
                          <div className="flex justify-between">
                            <span className="text-muted-foreground">执行时间</span>
                            <span>{formatTime(selectedTicket.executed_at)}</span>
                          </div>
                        )}
                        {selectedTicket.completed_at && (
                          <div className="flex justify-between">
                            <span className="text-muted-foreground">完成时间</span>
                            <span>{formatTime(selectedTicket.completed_at)}</span>
                          </div>
                        )}
                        {selectedTicket.deadline_at && (
                          <div className="flex justify-between">
                            <span className="text-muted-foreground">截止时间</span>
                            <span className={selectedTicket.sla_breached ? 'text-red-500 font-medium' : ''}>
                              {formatTime(selectedTicket.deadline_at)}
                            </span>
                          </div>
                        )}
                      </div>
                    </div>

                    {/* 挂起信息 */}
                    {selectedTicket.on_hold && (
                      <div className="border rounded-md p-3 bg-yellow-50 dark:bg-yellow-900/20">
                        <h4 className="text-sm font-medium text-yellow-700 dark:text-yellow-400">已挂起</h4>
                        <p className="text-sm text-muted-foreground mt-1">{selectedTicket.hold_reason || '无原因'}</p>
                        {selectedTicket.hold_until_at && (
                          <p className="text-xs text-muted-foreground mt-1">预计恢复: {formatTime(selectedTicket.hold_until_at)}</p>
                        )}
                      </div>
                    )}

                    {/* SLA 状态 */}
                    <div className="border rounded-md p-3">
                      <h4 className="text-sm font-medium mb-2">SLA 状态</h4>
                      {selectedTicket.sla_breached ? (
                        <Tag color="error">已违规</Tag>
                      ) : selectedTicket.sla_warning_notified ? (
                        <Tag color="warning">即将到期</Tag>
                      ) : (
                        <Tag color="success">正常</Tag>
                      )}
                      {(selectedTicket.hold_seconds || 0) > 0 && (
                        <p className="text-xs text-muted-foreground mt-2">
                          累计挂起时长: {Math.floor((selectedTicket.hold_seconds || 0) / 3600)}小时 {Math.floor(((selectedTicket.hold_seconds || 0) % 3600) / 60)}分钟
                        </p>
                      )}
                    </div>
                    </div>,
                  },
                  ...(selectedTicket.process_id ? [{
                    key: 'process',
                    label: <><Activity className="w-3 h-3 mr-1" />流程</>,
                    children: <div className="mt-3">
                      {(() => {
                        const proc = processes.find(p => p.id === selectedTicket.process_id)
                        if (!proc) return <p className="text-sm text-muted-foreground">未找到流程定义。</p>
                        return (
                          <ProcessFlowView
                            steps={proc.steps}
                            currentStep={processInst?.current_step}
                            stepResults={processInst?.step_results}
                            processName={proc.name}
                          />
                        )
                      })()}
                    </div>,
                  }] : []),
                  {
                    key: 'events',
                    label: <><Activity className="w-3 h-3 mr-1" />事件 ({ticketEvents.length})</>,
                    children: <div className="mt-3">
                    {ticketEvents.length === 0 ? <p className="text-sm text-muted-foreground">无变更事件。</p> : (
                      <div className="space-y-0">
                        {ticketEvents.map((ev, i) => (
                          <div key={ev.id || i} className="flex items-start gap-3 py-2 border-b last:border-0 text-sm">
                            <div className="flex flex-col items-center shrink-0">
                              <div className={`w-2 h-2 rounded-full ${i === ticketEvents.length - 1 ? 'bg-blue-500' : 'bg-muted-foreground/30'}`} />
                              {i < ticketEvents.length - 1 && <div className="w-px h-full bg-muted-foreground/20 mt-1" />}
                            </div>
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center gap-2">
                                <Tag color={statusVariant(ev.to_status)} className="text-xs">{STATUS_LABELS[ev.to_status] || ev.to_status}</Tag>
                                {ev.from_status && <span className="text-xs text-muted-foreground">从 {STATUS_LABELS[ev.from_status] || ev.from_status}</span>}
                              </div>
                              <p className="text-xs text-muted-foreground mt-0.5">{ev.changed_by} · {formatTime(ev.created_at)}</p>
                              {ev.comment && <p className="text-xs mt-1 text-muted-foreground italic">{ev.comment}</p>}
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                    </div>,
                  },
                  {
                    key: 'comments',
                    label: <><MessageSquare className="w-3 h-3 mr-1" />评论 ({comments.length})</>,
                    children: <div className="mt-3 space-y-3">
                    <div className="border rounded-md p-3 bg-muted/30 space-y-2">
                      <div className="flex gap-2 items-center">
                        <label className="text-xs text-muted-foreground shrink-0">评论人:</label>
                        <Tag color="default" className="text-xs">{me?.username || 'anonymous'}</Tag>
                        <span className="text-[10px] text-muted-foreground">（当前登录用户，不可修改）</span>
                      </div>
                      <div className="relative">
                        <Input.TextArea
                          ref={commentRef}
                          rows={2}
                          value={commentContent}
                          onChange={e => setCommentContent(e.target.value)}
                          placeholder="输入评论内容，输入 @ 可提及用户…"
                          onKeyDown={e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) handleAddComment() }}
                        />
                        {mentionOpen && mentionMatches.length > 0 && (
                          <div className="absolute z-20 left-0 right-0 mt-1 max-h-48 overflow-y-auto rounded-md border bg-popover shadow-md">
                            {mentionMatches.map(u => (
                              <button type="button" key={u.id} onClick={() => pickMention(u.username)}
                                className="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-left hover:bg-accent hover:text-accent-foreground">
                                <span className="font-medium">{u.username}</span>
                                {u.email && <span className="text-[10px] text-muted-foreground truncate">{u.email}</span>}
                              </button>
                            ))}
                          </div>
                        )}
                      </div>
                      <div className="flex justify-end">
                        <Button size="middle" type="primary" onClick={handleAddComment} disabled={!canWrite || !commentContent.trim() || commentLoading}>
                          {commentLoading ? '发送中...' : '发送评论'}
                        </Button>
                      </div>
                    </div>
                    {comments.length === 0 ? (
                      <p className="text-sm text-muted-foreground text-center py-4">暂无评论</p>
                    ) : (
                      <div className="space-y-2">
                        {comments.map(c => (
                          <div key={c.id} className="border rounded-md p-3 text-sm group">
                            <div className="flex items-center justify-between">
                              <div className="flex items-center gap-2">
                                <Tag color="default" className="text-xs">{c.author}</Tag>
                                <span className="text-xs text-muted-foreground">{formatTime(c.created_at)}</span>
                              </div>
                              <Button
                                type="text"
                                size="middle"
                                danger
                                className="h-6 w-6 p-0 opacity-0 group-hover:opacity-100 transition-opacity"
                                onClick={() => handleDeleteComment(c.id)}
                                disabled={!canWrite}
                              >
                                <Trash2 className="h-3 w-3" />
                              </Button>
                            </div>
                            <p className="mt-1 text-sm whitespace-pre-wrap">{c.content}</p>
                          </div>
                        ))}
                      </div>
                    )}
                    </div>,
                  },
                  {
                    key: 'subtasks',
                    label: <><ListChecks className="w-3 h-3 mr-1" />子任务 ({subtasks.length})</>,
                    children: <div className="mt-3 space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="text-xs text-muted-foreground">
                        {subtasks.filter(s => s.status === 'done').length} / {subtasks.length} 已完成
                      </span>
                      <Button type="default" size="middle" onClick={() => { setSubtaskDialog(true); setSubtaskTitle(''); setSubtaskDesc(''); setSubtaskAssignee(''); setSubtaskDue(''); setSubtaskSync([]); setSubtaskCloseWithParent(true) }} disabled={!canWrite}>
                        <Plus className="h-3 w-3 mr-1" />新增子任务
                      </Button>
                    </div>
                    {subtasks.length === 0 ? (
                      <p className="text-sm text-muted-foreground text-center py-4">暂无子任务</p>
                    ) : (
                      <div className="space-y-2">
                        {subtasks.map(st => (
                          <div key={st.id} className={`border rounded-md p-3 text-sm group ${st.status === 'done' ? 'opacity-60' : ''}`}>
                            <div className="flex items-start gap-2">
                              <button onClick={() => handleToggleSubtask(st)} disabled={!canWrite} className="mt-0.5 shrink-0" aria-label={st.status === 'done' ? '标记未完成' : '标记完成'}>
                                <CheckCircle2 className={`h-4 w-4 ${st.status === 'done' ? 'text-blue-600' : 'text-muted-foreground/40'}`} />
                              </button>
                              <div
                                className="flex-1 min-w-0 cursor-pointer"
                                onClick={() => openSubtaskDetail(st)}
                              >
                                <div className="flex items-center gap-2 flex-wrap">
                                  <span className={`text-sm font-medium ${st.status === 'done' ? 'line-through' : ''}`}>{st.title}</span>
                                  {st.status === 'pending' && !st.viewed_at && st.assignee && (
                                    <Tag color="error" className="text-[10px]">待认领</Tag>
                                  )}
                                  <select
                                    value={st.status}
                                    onClick={e => e.stopPropagation()}
                                    onChange={e => handleSetSubtaskStatus(st, e.target.value)}
                                    disabled={!canWrite}
                                    className="h-6 text-[11px] rounded border border-input bg-background px-1"
                                  >
                                    {Object.entries(subtaskLabels).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                                  </select>
                                  {st.due_at && <span className="text-[11px] text-muted-foreground">截止 {formatTime(st.due_at)}</span>}
                                </div>
                                {st.description && <p className="text-xs text-muted-foreground mt-1 whitespace-pre-wrap">{st.description}</p>}
                                {st.detail && <p className="text-xs mt-1 whitespace-pre-wrap text-foreground/80"><span className="text-muted-foreground">处理情况：</span>{st.detail}</p>}
                                <div className="flex items-center gap-2 mt-1 text-[11px] text-muted-foreground">
                                  {st.assignee && <Tag color="default" className="text-[10px]">{st.assignee}</Tag>}
                                  <span>{formatTime(st.created_at)}</span>
                                  {st.completed_at && <span className="text-blue-600">完成于 {formatTime(st.completed_at)}</span>}
                                  {st.close_with_parent === false && <span className="text-amber-600">不随主单关闭</span>}
                                </div>
                              </div>
                              <Button
                                type="text"
                                size="middle"
                                danger
                                className="h-6 w-6 p-0 opacity-0 group-hover:opacity-100 transition-opacity"
                                onClick={() => handleDeleteSubtask(st.id)}
                                disabled={!canWrite}
                              >
                                <Trash2 className="h-3 w-3" />
                              </Button>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                    </div>,
                  },
                  {
                    key: 'attachments',
                    label: <><Paperclip className="w-3 h-3 mr-1" />附件 ({attachments.length})</>,
                    children: <div className="mt-3 space-y-3">
                    <div className="flex items-center gap-2">
                      <input
                        ref={fileInputRef}
                        type="file"
                        className="hidden"
                        onChange={handleUploadFile}
                      />
                      <Button
                        type="default"
                        size="middle"
                        onClick={() => fileInputRef.current?.click()}
                        disabled={uploading || !canWrite}
                      >
                        <Upload className="h-3 w-3 mr-1" />
                        {uploading ? '上传中...' : '上传附件'}
                      </Button>
                    </div>
                    {attachments.length === 0 ? (
                      <p className="text-sm text-muted-foreground text-center py-4">暂无附件</p>
                    ) : (
                      <div className="space-y-1">
                        {attachments.map(a => (
                          <div key={a.id} className="flex items-center justify-between border rounded-md px-3 py-2 text-sm group">
                            <div className="flex items-center gap-2 min-w-0">
                              <Paperclip className="h-3 w-3 text-muted-foreground shrink-0" />
                              <span className="truncate">{a.file_name}</span>
                              <span className="text-xs text-muted-foreground shrink-0">({formatFileSize(a.file_size)})</span>
                              <span className="text-xs text-muted-foreground shrink-0">{a.uploaded_by}</span>
                            </div>
                            <div className="flex items-center gap-1 shrink-0">
                              <a
                                href={`/api/v1/itsm/tickets/${selectedTicket.id}/attachments/${a.id}`}
                                download={a.file_name}
                                className="inline-flex items-center justify-center h-7 w-7 rounded-md hover:bg-accent transition-colors"
                              >
                                <Download className="h-3 w-3" />
                              </a>
                              <Button
                                type="text"
                                size="middle"
                                danger
                                className="h-7 w-7 p-0 opacity-0 group-hover:opacity-100 transition-opacity"
                                onClick={() => handleDeleteAttachment(a.id)}
                                disabled={!canWrite}
                              >
                                <Trash2 className="h-3 w-3" />
                              </Button>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                    </div>,
                  },
                ]} />
              </CardContent>
            </Card>
          ) : (
            <Card className="h-full flex items-center justify-center min-h-[400px]">
              <CardContent className="text-center text-muted-foreground">
                <p>选择一个工单查看详情</p>
              </CardContent>
            </Card>
          )}
        </div>
      </div>

      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title={`新建工单${createScope !== 'ticket:default' ? ' · ' + createScope : ''}`} footer={null}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          <div className="grid grid-cols-2 gap-3">
            <div className="col-span-2"><label className="text-sm font-medium">流程模板</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.process_id} onChange={e => {
                const pid = e.target.value
                setForm(f => ({ ...f, process_id: pid }))
                // 选流程时作用域升级为 process:<pid>，否则回退到打开时的来源（catalog 或默认）
                const ns = pid ? `process:${pid}` : initialScopeRef.current
                setCreateScope(ns)
                loadCreateSchema(ns)
              }}>
                <option value="">无流程模板</option>
                {processes.filter(p => p.enabled).map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </div>
          </div>
          {/* 申请标题/说明/紧急程度 为内置字段，并入 DynamicForm 渲染（设计器中以锁定卡片呈现） */}
          <DynamicForm
            schema={withBuiltinFields(createSchema)}
            values={{ title: form.title, description: form.description, risk_level: form.risk_level, ...customFields }}
            context={form}
            onChange={(key, value) => {
              if (BUILTIN_KEYS.includes(key)) setForm(f => ({ ...f, [key]: value }))
              else setCustomFields(c => ({ ...c, [key]: value }))
            }}
            loadOptions={loadOptions}
          />
          <div><label className="text-sm font-medium">变更计划</label><Input.TextArea rows={3} value={form.change_plan} onChange={e => setForm(f => ({ ...f, change_plan: e.target.value }))} /></div>
          <div><label className="text-sm font-medium">回滚计划</label><Input.TextArea rows={3} value={form.rollback_plan} onChange={e => setForm(f => ({ ...f, rollback_plan: e.target.value }))} /></div>
          <div>
            <label className="text-sm font-medium">关联资产 CI</label>
            <div className="flex gap-1 flex-wrap mb-1">
              {ciIds.map(id => <Tag key={id} color="default" className="text-xs cursor-pointer" onClick={() => setCiIds(ciIds.filter(c => c !== id))}>{id.slice(0, 12)}... ×</Tag>)}
            </div>
            <div className="flex gap-2">
              <Input className="text-xs" value={ciIdInput} onChange={e => setCiIdInput(e.target.value)} placeholder="输入 CI ID" onKeyDown={e => e.key === 'Enter' && addCiId()} />
              <Button size="middle" onClick={addCiId}>添加</Button>
            </div>
          </div>
          {knowledgeRecs.length > 0 && <div className="mt-2 p-2 bg-amber-50 dark:bg-amber-950/20 rounded-md">
            <p className="text-xs font-medium text-amber-700 dark:text-amber-400 mb-1">相关文章推荐</p>
            {knowledgeRecs.map(a => <div key={a.id} className="text-[11px] text-muted-foreground hover:text-foreground cursor-pointer truncate" onClick={e => { e.stopPropagation(); window.open('/knowledge/' + a.id, '_blank') }}>· {a.title}</div>)}
          </div>}
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleCreate} disabled={!canWrite || !form.title}>提交</Button></div>
      </Modal>

      <Modal open={approveDialog} onCancel={() => setApproveDialog(false)} title="审批通过" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">审批意见</label><Input.TextArea value={approveComment} onChange={e => setApproveComment(e.target.value)} placeholder="审批意见（可选）" /></div>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setApproveDialog(false)}>取消</Button><Button onClick={handleApprove} disabled={!canApprove}>通过</Button></div>
      </Modal>

      <Modal open={rejectDialog} onCancel={() => setRejectDialog(false)} title="驳回工单" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">驳回原因 *</label><Input.TextArea value={rejectReason} onChange={e => setRejectReason(e.target.value)} placeholder="请填写驳回原因" /></div>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setRejectDialog(false)}>取消</Button><Button onClick={handleReject} disabled={!canApprove || !rejectReason}>驳回</Button></div>
      </Modal>

      <Modal open={completeDialog} onCancel={() => setCompleteDialog(false)} title="完成执行" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">完成备注</label><Input.TextArea rows={4} value={completeComment} onChange={e => setCompleteComment(e.target.value)} placeholder="完成备注（可选）" /></div>
          <div>
            <label className="text-sm font-medium">处置效果评估（AI 工单用于准确率统计）</label>
            <div className="flex gap-2 mt-1">
              <Button size="middle" type={completeAssessment === 'confirmed' ? 'primary' : 'default'} onClick={() => setCompleteAssessment(completeAssessment === 'confirmed' ? undefined : 'confirmed')}>确认有效</Button>
              <Button size="middle" type={completeAssessment === 'false_positive' ? 'primary' : 'default'} onClick={() => setCompleteAssessment(completeAssessment === 'false_positive' ? undefined : 'false_positive')}>误报</Button>
              {completeAssessment && <span className="text-xs text-muted-foreground self-center">将计入 AI 准确率统计</span>}
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setCompleteDialog(false)}>取消</Button><Button onClick={handleComplete} disabled={!canWrite}>确认完成</Button></div>
      </Modal>

      <Modal open={rollbackDialog} onCancel={() => setRollbackDialog(false)} title="回滚工单" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">回滚原因</label><Input.TextArea value={rollbackReason} onChange={e => setRollbackReason(e.target.value)} placeholder="请填写回滚原因（可选）" /></div>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setRollbackDialog(false)}>取消</Button><Button onClick={handleRollback} disabled={!canWrite}>确认回滚</Button></div>
      </Modal>

      <Modal open={assignDialog} onCancel={() => setAssignDialog(false)} title="指派负责人" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">负责人</label><Input className="mt-1" value={assigneeInput} onChange={e => setAssigneeInput(e.target.value)} placeholder="输入负责人用户名" onKeyDown={e => e.key === 'Enter' && handleAssignTicket()} /></div>
          <p className="text-xs text-muted-foreground">指派后 SLA 预警/违约通知会带上负责人信息。</p>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setAssignDialog(false)}>取消</Button><Button onClick={handleAssignTicket} disabled={!canWrite || !assigneeInput.trim()}>确认指派</Button></div>
      </Modal>

      <Modal open={remindDialog} onCancel={() => setRemindDialog(false)} title="催办工单" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">催办留言</label><Input.TextArea rows={3} className="mt-1" value={remindMsg} onChange={e => setRemindMsg(e.target.value)} placeholder="催办留言（可选）" /></div>
          <p className="text-xs text-muted-foreground">催办通知将发给负责人（{selectedTicket?.assignee || '未指派'}）。</p>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setRemindDialog(false)}>取消</Button><Button onClick={handleRemind} disabled={!canWrite}>发送催办</Button></div>
      </Modal>

      <Modal open={relateDialog} onCancel={() => setRelateDialog(false)} title="关联工单" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">目标工单 ID</label><Input className="mt-1" value={relateId} onChange={e => setRelateId(e.target.value)} placeholder="输入要关联的工单 ID" onKeyDown={e => e.key === 'Enter' && handleRelate()} /></div>
          <p className="text-xs text-muted-foreground">关联是双向的：目标工单也会显示与本工单的关联。</p>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setRelateDialog(false)}>取消</Button><Button onClick={handleRelate} disabled={!canWrite || !relateId.trim()}>确认关联</Button></div>
      </Modal>

      <Modal open={closeDialog} onCancel={() => setCloseDialog(false)} title="关闭工单" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">关单备注</label><Input.TextArea rows={3} className="mt-1" value={closeComment} onChange={e => setCloseComment(e.target.value)} placeholder="关单备注（可选）" /></div>
          <div>
            <label className="text-sm font-medium">满意度评分</label>
            <div className="flex gap-1 mt-1">
              {[1, 2, 3, 4, 5].map(n => (
                <Button key={n} size="middle" type={closeSatisfaction === n ? 'primary' : 'default'} className="px-2.5" onClick={() => setCloseSatisfaction(closeSatisfaction === n ? 0 : n)}>
                  {'★'.repeat(n)}
                </Button>
              ))}
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setCloseDialog(false)}>取消</Button><Button onClick={handleCloseTicket} disabled={!canWrite}>确认关闭</Button></div>
      </Modal>

      <Modal open={subtaskDialog} onCancel={() => setSubtaskDialog(false)} title="新增子任务" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">标题</label><Input className="mt-1" value={subtaskTitle} onChange={e => setSubtaskTitle(e.target.value)} placeholder="子任务标题" onKeyDown={e => e.key === 'Enter' && handleCreateSubtask()} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">负责人</label>
              <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-sm mt-1"
                value={subtaskAssignee} onChange={e => setSubtaskAssignee(e.target.value)}>
                <option value="">选择处理人</option>
                {(users || []).map(u => (
                  <option key={u.id} value={u.username}>{u.username}{u.email ? `（${u.email}）` : ''}</option>
                ))}
              </select>
            </div>
            <div><label className="text-sm font-medium">截止日期</label><Input type="date" className="mt-1" value={subtaskDue} onChange={e => setSubtaskDue(e.target.value)} /></div>
          </div>
          <div><label className="text-sm font-medium">描述</label><Input.TextArea rows={4} value={subtaskDesc} onChange={e => setSubtaskDesc(e.target.value)} placeholder="描述（可选）" /></div>
          <div>
            <label className="text-sm font-medium">字段映射（同步主单字段到子任务）</label>
            <div className="mt-1 max-h-32 overflow-y-auto rounded-md border border-input p-2 space-y-1">
              {subtaskSyncOptions.length === 0 && <p className="text-xs text-muted-foreground">主单暂无可同步字段</p>}
              {subtaskSyncOptions.map(o => (
                <label key={o.key} className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5"
                    checked={subtaskSync.includes(o.key)}
                    onChange={e => setSubtaskSync(prev => e.target.checked ? [...prev, o.key] : prev.filter(k => k !== o.key))}
                  />
                  {o.label}
                </label>
              ))}
            </div>
          </div>
          <SwitchField
            label="随主工单关闭而自动关闭"
            description="主工单关闭时，该子任务若仍未完成将一并关闭（默认开启）"
            checked={subtaskCloseWithParent}
            onCheckedChange={setSubtaskCloseWithParent}
          />
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setSubtaskDialog(false)}>取消</Button><Button onClick={handleCreateSubtask} disabled={!canWrite || !subtaskTitle.trim() || subtaskSaving}>{subtaskSaving ? '创建中...' : '创建'}</Button></div>
      </Modal>

      <Modal open={subtaskDetailOpen} onCancel={() => setSubtaskDetailOpen(false)} title="子任务处理详情" footer={null}>
        {subtaskDetail && (
          <div className="space-y-3">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-sm font-medium">{subtaskDetail.title}</span>
              <Tag color="secondary" className="text-[10px]">{subtaskLabels[subtaskDetail.status] || subtaskDetail.status}</Tag>
              {subtaskDetail.viewed_at && <span className="text-[11px] text-muted-foreground">已认领 {formatTime(subtaskDetail.viewed_at)}</span>}
            </div>
            {subtaskDetail.description && <p className="text-xs text-muted-foreground whitespace-pre-wrap">{subtaskDetail.description}</p>}
            <div>
              <label className="text-sm font-medium">处理情况</label>
              <Input.TextArea
                rows={6}
                value={subtaskDetail.detail ?? ''}
                onChange={e => setSubtaskDetail(d => d ? ({ ...d, detail: e.target.value }) : d)}
                placeholder="填写处理过程、结论或阻塞点"
                disabled={!canWrite}
              />
            </div>
            <SwitchField
              label="随主工单关闭而自动关闭"
              description="主工单关闭时，该子任务若仍未完成将一并关闭"
              checked={subtaskDetail.close_with_parent ?? true}
              onCheckedChange={checked => setSubtaskDetail(d => d ? ({ ...d, close_with_parent: checked }) : d)}
              disabled={!canWrite}
            />
            <p className="text-[11px] text-muted-foreground">
              处理人首次查看即进入「处理中」（认领）；保存后更新最后处理人。
            </p>
          </div>
        )}
        <div className="flex justify-end gap-2">
          <Button onClick={() => setSubtaskDetailOpen(false)}>关闭</Button>
          <Button onClick={handleSaveSubtaskDetail} disabled={!canWrite || subtaskDetailSaving}>{subtaskDetailSaving ? '保存中...' : '保存处理情况'}</Button>
        </div>
      </Modal>

      <Modal open={importDialog} onCancel={() => setImportDialog(false)} title="导入结果" footer={null}>
        {importResult && (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div><span className="text-muted-foreground">总行数:</span> {importResult.total}</div>
              <div><span className="text-muted-foreground">成功:</span> <span className="text-blue-600 font-medium">{importResult.success}</span></div>
            </div>
            {importResult.errors.length > 0 && (
              <div>
                <h4 className="text-sm font-medium text-destructive mb-1">失败记录 ({importResult.errors.length})</h4>
                <div className="max-h-40 overflow-y-auto space-y-1">
                  {importResult.errors.map((err, i) => (
                    <div key={i} className="text-xs border rounded px-2 py-1">
                      <span className="text-muted-foreground">行 {err.row}:</span> {err.message}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
        <div className="flex justify-end gap-2"><Button onClick={() => setImportDialog(false)}>关闭</Button></div>
      </Modal>

      <Modal open={configDialog} onCancel={() => setConfigDialog(false)} title={`表单配置（${resolveConfigScope()}）`} footer={null}>
        <div className="space-y-3">
          <div className="flex gap-2 flex-wrap">
            <select className="flex h-9 rounded-md border border-input bg-background px-2 text-xs" value={configScopeType} onChange={e => setConfigScopeType(e.target.value as 'ticket' | 'catalog' | 'process' | 'process_step')}>
              <option value="ticket">默认 (ticket:default)</option>
              <option value="catalog">{`服务目录项 (catalog:<id>)`}</option>
              <option value="process">{`流程 (process:<id>)`}</option>
              <option value="process_step">{`流程步骤 (process:<id>:step:<step>)`}</option>
            </select>
            {configScopeType === 'catalog' && (
              <Input className="text-xs" value={configScopeId} onChange={e => setConfigScopeId(e.target.value)} placeholder="目录项 ID" />
            )}
            {configScopeType === 'process' && (
              <Input className="text-xs" value={configScopeId} onChange={e => setConfigScopeId(e.target.value)} placeholder="流程 ID" />
            )}
            {configScopeType === 'process_step' && (
              <>
                <Input className="text-xs" value={configScopeId} onChange={e => setConfigScopeId(e.target.value)} placeholder="流程 ID" />
                <Input className="text-xs" value={configStepId} onChange={e => setConfigStepId(e.target.value)} placeholder="步骤 ID" />
              </>
            )}
            <Button size="middle" type="default" onClick={openConfigDialog}>载入</Button>
            <div className="ml-auto flex rounded-md border border-input overflow-hidden">
              <button
                type="button"
                className={`px-2.5 py-1 text-xs ${configMode === 'visual' ? 'bg-accent font-medium' : 'hover:bg-accent/50'}`}
                onClick={() => switchConfigMode('visual')}
              >
                可视化
              </button>
              <button
                type="button"
                className={`px-2.5 py-1 text-xs border-l border-input ${configMode === 'json' ? 'bg-accent font-medium' : 'hover:bg-accent/50'}`}
                onClick={() => switchConfigMode('json')}
              >
                JSON
              </button>
            </div>
          </div>
          {configMode === 'visual' ? (
            <>
              <p className="text-xs text-muted-foreground">
                左侧管理字段、右侧编辑属性，下方实时预览。条件显隐支持多规则 AND/OR 组合；
                选项来源可选 <code className="px-1 rounded bg-muted">cmdb:&lt;类型&gt;</code> 或{' '}
                <code className="px-1 rounded bg-muted">iam:user|role|group</code> 实时拉取。
              </p>
              <FormBuilder
                schema={configSchema}
                onChange={setConfigSchema}
                sources={optionSources}
                loadOptions={loadOptions}
                disabled={!canWrite}
              />
            </>
          ) : (
            <>
              <p className="text-xs text-muted-foreground">
                专家模式：直接编辑 Form Schema JSON。字段类型支持 text / textarea / select / multiselect / number / date / bool / user / ci / cascader；
                条件显隐用 <code className="px-1 rounded bg-muted">visible_when_rules</code> +{' '}
                <code className="px-1 rounded bg-muted">visible_logic</code>（旧版 <code className="px-1 rounded bg-muted">visible_when</code> 仍兼容）。
              </p>
              <Input.TextArea
                rows={10}
                value={configJson}
                onChange={e => setConfigJson(e.target.value)}
                spellCheck={false}
              />
            </>
          )}
        </div>
        <div className="flex justify-end gap-2">
          <Button onClick={() => setConfigDialog(false)}>取消</Button>
          <Button onClick={handleSaveConfig} disabled={configSaving || !canWrite}>{configSaving ? '保存中...' : '保存配置'}</Button>
        </div>
      </Modal>
    </div>
  )
}
