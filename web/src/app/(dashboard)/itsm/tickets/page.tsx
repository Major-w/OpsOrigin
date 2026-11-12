
import { useEffect, useState, useRef, useCallback, useMemo, type ChangeEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useTicketStore } from '@/stores/itsm/tickets'
import { useProcessStore } from '@/stores/itsm/processes'
import { useNotificationStore } from '@/stores/notification'
import type { Ticket, TicketEvent, ITSMStats, ProcessInstance, TicketTemplate, AutomationRule, TicketButton } from '@/stores/itsm/types'
import { DynamicForm, withBuiltinFields, BUILTIN_KEYS, type FormSchema } from '@/components/itsm/DynamicForm'
import { FormBuilder } from '@/components/itsm/FormBuilder'
import ProcessFlowView from '@/components/itsm/ProcessFlowView'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Tabs, Select, Dropdown, message } from 'antd'
import { Switch, SwitchField } from '@/components/ui/switch'
import { formatTime, apiFetch, apiList } from '@/lib/utils'
import { useCan, useCanAny } from '@/stores/permissions'
import { useAuthStore } from '@/stores/auth'
import { useUserStore } from '@/stores/user'
import { useWebSocket } from '@/lib/useWebSocket'
import { Plus, Check, X, Play, RotateCcw, Clock, FileText, Activity, ChevronLeft, Paperclip, MessageSquare, Upload, Download, Trash2, FileUp, FileDown, BookOpen, Send, UserPlus, CheckCircle2, ListChecks, Bell, Link2, Zap } from 'lucide-react'
import { RISK_LABELS, STATUS_LABELS, RISK_BADGE_VARIANT, parseFormSchema, applyStepFieldPerms, stepPermCaps, validateComboRules, initStatusLabelOverrides, type StepFieldPerm } from '@/lib/itsm-constants'
import { getMessage } from '@/lib/message'
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
  // 双路由复用本组件：/itsm/tickets = 列表模式；/itsm/tickets/:id = 详情模式（全宽）。
  // 列表接口返回的是摘要（process_id/custom_fields 可能省略），详情模式必须 getTicket 拉全量，
  // 否则流程图 tab（依赖 process_id）和流程表单（依赖 custom_fields/节点权限）都渲染不出来。
  const { id: routeId } = useParams()
  const isDetail = !!routeId
  // 当前流程节点允许展示的操作按钮 key；undefined = 未绑定流程 / 节点未配置 → 沿用默认全部显示（向后兼容）。
  const [currentStepActions, setCurrentStepActions] = useState<string[] | undefined>(undefined)
  const { tickets, stats, loading, fetch, create, approve, reject, execute, complete, rollback, submit, assign, close, withdraw, reopen, hold, resume, remind, relate, addApprover, getTicket, getEvents, fetchStats, templates, fetchTemplates, saveTemplate, deleteTemplate, rules, fetchRules, saveRule, deleteRule, buttons, fetchButtons, saveButton, deleteButton } = useTicketStore()
  const { processes, fetchProcesses, getFormSchema, upsertFormSchema, getFormOptions, getFormOptionSources, getTicketProcess, updateTicketCustomFields } = useProcessStore()
  const notifChannels = useNotificationStore(s => s.channels)
  const fetchNotifChannels = useNotificationStore(s => s.fetchChannels)
  const canWrite = useCan('itsm:write')
  const canApprove = useCanAny(['itsm:approve', 'itsm:write'])
  const me = useAuthStore(s => s.user)
  const users = useUserStore(s => s.users)
  // 按钮可见性：未绑定流程或节点未配置 actions → 全部显示（向后兼容）；否则仅显示节点配置的按钮。
  const stepAllows = useCallback((key: string) => currentStepActions === undefined || currentStepActions.includes(key), [currentStepActions])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [myOnly, setMyOnly] = useState(false)
  // 列表查询：关键词（标题/创建人/负责人）+ 状态
  const [q, setQ] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [page, setPage] = useState(1)
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
  // 当前节点字段权限（step.form_fields）：供 permCaps 兜底——行为规则不得放宽节点权限
  const [stepPerms, setStepPerms] = useState<StepFieldPerm[]>([])
  const [stepFields, setStepFields] = useState<Record<string, any>>({})
  const [stepSaving, setStepSaving] = useState(false)
  const [ticketEvents, setTicketEvents] = useState<TicketEvent[]>([])
  const [processInst, setProcessInst] = useState<ProcessInstance | null>(null)
  const [detailTab, setDetailTab] = useState('sla')
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
  // 审批链配置(新建时可选: 顺序/并行 + 审批人列表)与运行时加签
  const [chainMode, setChainMode] = useState<'sequential' | 'parallel'>('sequential')
  const [chainApprovers, setChainApprovers] = useState<string[]>([])
  // 转建单：记录源工单 ID，新单创建成功后自动与源单双向关联
  const [convertSourceId, setConvertSourceId] = useState<string>('')
  const [addApproverDialog, setAddApproverDialog] = useState(false)
  const [addApproverName, setAddApproverName] = useState<string | undefined>(undefined)
  const [addApproverPosition, setAddApproverPosition] = useState<'before' | 'after'>('after')
  const [addApproverSaving, setAddApproverSaving] = useState(false)

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
        getMessage().error(`OLA 超时: 工单 ${data.ticket_id} — ${data.policy || ''}`)
        fetch()
      } else if (data.type === 'ola_warning') {
        getMessage().warning(`OLA 预警: 工单 ${data.ticket_id} — ${data.policy || ''}`)
      } else if (data.type === 'hold_resumed') {
        getMessage().success(`工单 ${data.ticket_id} 已自动恢复`)
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
    } catch { setComments([]); getMessage().error('加载评论失败') }
  }, [])

  const fetchAttachments = useCallback(async (ticketId: string) => {
    try {
      const data = await apiList<Attachment>(`/itsm/tickets/${ticketId}/attachments`)
      setAttachments(data || [])
    } catch { setAttachments([]); getMessage().error('加载附件失败') }
  }, [])

  const fetchSubtasks = useCallback(async (ticketId: string) => {
    try {
      const data = await apiList<Subtask>(`/itsm/tickets/${ticketId}/subtasks`)
      setSubtasks(data || [])
    } catch { setSubtasks([]); getMessage().error('加载子任务失败') }
  }, [])

  useEffect(() => { fetch(); fetchStats(); fetchProcesses(); fetchTemplates(); fetchRules(); fetchButtons(); fetchNotifChannels(); initStatusLabelOverrides() }, [])

  // 评论/子任务的「用户」下拉：确保用户列表已加载（评论 @ 与子任务处理人共用）。
  useEffect(() => {
    const st = useUserStore.getState()
    if (st.users.length === 0) st.fetch()
  }, [])

  // 兼容旧深链 ?id=<ticket>：服务门户「查看工单」等入口，重定向到独立详情路由。
  useEffect(() => {
    try {
      const tid = new URLSearchParams(window.location.search).get('id')
      if (tid) navigate(`/itsm/tickets/${tid}`, { replace: true })
    } catch { /* ignore */ }
  }, [])

  // 详情模式：按路由参数拉取工单全量数据（列表摘要不含 process_id/custom_fields 等）。
  useEffect(() => {
    if (!routeId) return
    ;(async () => {
      try {
        await fetchProcesses() // 先备好流程定义，详情才能按主表单+节点权限渲染
        const t = await getTicket(routeId)
        if (t) await handleSelectTicket(t)
        else getMessage().error('工单不存在或无权访问')
      } catch { getMessage().error('工单不存在或无权访问') }
    })()
  }, [routeId])

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
    setConvertSourceId('')
    setDialogOpen(true)
    await loadCreateSchema(scope)
  }

  // 转建为问题工单：打开新建弹窗并自动填入源工单字段（P1-⑩ 字段映射），
  // 创建成功后由 handleCreate 自动与源单双向关联。
  const openConvertTicket = async () => {
    const src = selectedTicket
    if (!src) return
    await openCreateDialog('ticket:default')
    setConvertSourceId(src.id)
    setForm(f => ({
      ...f,
      title: `[转建] ${src.title}`,
      description: `${src.description || ''}${src.description ? '\n\n' : ''}—— 由工单「${src.title}」(${src.id.slice(0, 8)}) 转建`,
      risk_level: src.risk_level || 'low',
    }))
    if (src.ci_ids?.length) setCiIds([...src.ci_ids])
  }

  const handleCreate = async () => {
    // 组合校验（前端预检，后端同样校验）：block 阻断提交，warn 仅提示
    if (createSchema) {
      const ctx = { ...form, ...customFields }
      const { blocks, warnings } = validateComboRules(createSchema, ctx)
      if (blocks.length) { getMessage().error(blocks.join('；')); return }
      if (warnings.length) getMessage().warning(warnings.join('；'))
    }
    const newId = await create({ ...form, ci_ids: ciIds, custom_fields: customFields, form_scope: createScope, approval_chain: chainApprovers.length > 0 ? { mode: chainMode, approvers: chainApprovers } : undefined })
    if (!newId) return
    // 转建闭环：新单与源工单自动双向关联
    if (convertSourceId) {
      await relate(newId, convertSourceId, 'related', '由工单转建')
      setConvertSourceId('')
    }
    setDialogOpen(false)
    setCiIds([])
    setCustomFields({})
    setChainApprovers([])
    setChainMode('sequential')
    setCreateSchema(null)
    setConvertSourceId('')
    setForm({ title: '', description: '', risk_level: 'low', change_plan: '', rollback_plan: '', created_by: me?.username || 'anonymous', process_id: '' })
    fetchStats()
  }

  // 提单模板：打开编辑器（新建或编辑已有）
  const openTemplateEditor = (t?: TicketTemplate) => {
    const base: TicketTemplate = t
      ? t
      : { id: '', name: '', remark: '', category: '', form_scope: 'ticket:default', process_id: '', content: {} }
    setEditingTemplate(t || null)
    setTemplateForm(base)
    setTemplateCustomFieldsJSON(JSON.stringify((t?.content?.custom_fields) || {}, null, 2))
  }

  // 应用模板到新建工单表单：预填标题/描述/风险/计划/回滚/自定义字段/关联资产/流程
  const applyTemplate = (t: TicketTemplate) => {
    const c = t.content || {}
    setForm(f => ({
      ...f,
      title: c.title || f.title,
      description: c.description || f.description,
      risk_level: c.risk_level || f.risk_level,
      change_plan: c.change_plan || f.change_plan,
      rollback_plan: c.rollback_plan || f.rollback_plan,
      process_id: t.process_id || f.process_id,
    }))
    setCustomFields(c.custom_fields || {})
    setCiIds(c.ci_ids || [])
    const scope = t.form_scope || 'ticket:default'
    setCreateScope(scope)
    loadCreateSchema(scope)
    getMessage().success(`已套用模板「${t.name}」`)
  }

  const handleSaveTemplate = async () => {
    if (!templateForm.name.trim()) { getMessage().error('请填写模板名称'); return }
    let customFields: Record<string, any> = {}
    try {
      customFields = JSON.parse(templateCustomFieldsJSON || '{}')
    } catch {
      getMessage().error('自定义字段 JSON 格式错误')
      return
    }
    const payload: TicketTemplate = { ...templateForm, content: { ...templateForm.content, custom_fields: customFields } }
    setTemplateSaving(true)
    const ok = await saveTemplate(payload)
    setTemplateSaving(false)
    if (ok) { setEditingTemplate(null); setTemplateForm({ id: '', name: '', content: {} }) }
  }

  const handleSelectTicket = async (ticket: Ticket) => {
    setSelectedTicket(ticket)
    setDetailTab('detail')
    // 装载关联工单摘要（双向关联，逐条拉取标题/状态用于面板展示）
    setRelatedTickets([])
    if (ticket.related_ticket_ids && ticket.related_ticket_ids.length) {
      loadRelatedTickets(ticket.related_ticket_ids)
    }
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
    setStepPerms([]) // 默认无节点权限 cap，绑定流程且节点配置 form_fields 时再设置
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
          const isUserStep = step.type === 'user'
          if (schema) {
            const projected = applyStepFieldPerms(schema, step.form_fields)
            schema = projected
            const editable = (projected?.fields || []).some(f => !f.readonly)
            setStepEditable(isUserStep && editable)
            setStepScope(ticket.form_scope || '')
            setStepPerms(step.form_fields || [])
          } else if (step.form_scope) {
            // 存量流程：节点仍挂着独立表单作用域
            scope = step.form_scope
            setStepScope(step.form_scope)
            setStepEditable(isUserStep)
          } else {
            // 流程无主表单、步骤也无独立表单作用域：回退工单默认表单（ticket.form_scope / ticket:default），
            // user 类型节点允许填写，保证流程进行中表单可编辑（修复"流程中表单只读"）。
            setStepScope(ticket.form_scope || 'ticket:default')
            setStepEditable(isUserStep)
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
    // 组合校验（前端预检，后端同样校验）：block 阻断保存，warn 仅提示
    if (selectedSchema) {
      const ctx = { risk_level: selectedTicket.risk_level, status: selectedTicket.status, ...stepFields }
      const { blocks, warnings } = validateComboRules(selectedSchema, ctx)
      if (blocks.length) { getMessage().error(blocks.join('；')); return }
      if (warnings.length) getMessage().warning(warnings.join('；'))
    }
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
        getMessage().error('JSON 格式错误，无法切回可视化模式')
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
        getMessage().error('JSON 格式错误，请检查')
        return
      }
    }
    const keys = (next.fields || []).map(f => (f.key || '').trim())
    if (keys.some(k => !k)) {
      getMessage().error('存在未填写字段标识 key 的字段')
      return
    }
    if (new Set(keys).size !== keys.length) {
      getMessage().error('字段标识 key 存在重复，请修正')
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

  // 加签: 审批中动态追加审批人(before=插在当前审批人之前 / after=之后)
  const handleAddApprover = async () => {
    if (!selectedTicket || !addApproverName) return
    setAddApproverSaving(true)
    const ok = await addApprover(selectedTicket.id, addApproverName, addApproverPosition)
    setAddApproverSaving(false)
    if (!ok) return
    setAddApproverDialog(false)
    setAddApproverName(undefined)
    await handleSelectTicket(await getTicket(selectedTicket.id))
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
  const [remindDialog, setRemindDialog] = useState(false)
  const [remindMsg, setRemindMsg] = useState('')
  const [relateDialog, setRelateDialog] = useState(false)
  const [relateId, setRelateId] = useState('')
  const [relateRelation, setRelateRelation] = useState('related')
  const [relateReason, setRelateReason] = useState('')
  const [relatedTickets, setRelatedTickets] = useState<Ticket[]>([])
  // 提单模板：管理弹窗 + 编辑态
  const [templateModalOpen, setTemplateModalOpen] = useState(false)
  const [editingTemplate, setEditingTemplate] = useState<TicketTemplate | null>(null)
  const [templateForm, setTemplateForm] = useState<TicketTemplate>({ id: '', name: '', content: {} })
  const [templateSaving, setTemplateSaving] = useState(false)
  const [templateCustomFieldsJSON, setTemplateCustomFieldsJSON] = useState('{}')
  // 自动化：管理弹窗(触发器/自定义按钮双 Tab) + 编辑态 + 按钮执行确认
  const [autoModalOpen, setAutoModalOpen] = useState(false)
  const [autoTab, setAutoTab] = useState<'rules' | 'buttons'>('rules')
  const [ruleForm, setRuleForm] = useState<AutomationRule | null>(null)
  const [ruleSaving, setRuleSaving] = useState(false)
  const [buttonForm, setButtonForm] = useState<TicketButton | null>(null)
  const [buttonSaving, setButtonSaving] = useState(false)
  const [pendingButton, setPendingButton] = useState<TicketButton | null>(null)

  const emptyRule = (): AutomationRule => ({ id: '', name: '', event: 'ticket.created', enabled: true, conditions: [], actions: [] })
  const emptyButton = (): TicketButton => ({ id: '', name: '', action_type: 'assign', action_params: {}, visible_statuses: [], roles: [], confirm_text: '', sort_order: 0, enabled: true })

  const handleSaveRule = async () => {
    if (!ruleForm) return
    if (!ruleForm.name || !ruleForm.event) { getMessage().warning('请填写规则名称与触发事件'); return }
    if (!ruleForm.actions?.length) { getMessage().warning('请至少配置一个动作'); return }
    if (ruleForm.event === 'schedule' && !(ruleForm.schedule?.cron || ruleForm.schedule?.interval_minutes)) {
      getMessage().warning('定时调度规则需填写 Cron 表达式或执行间隔'); return
    }
    setRuleSaving(true)
    const ok = await saveRule(ruleForm)
    setRuleSaving(false)
    if (ok) setRuleForm(null)
  }
  const handleSaveButtonCfg = async () => {
    if (!buttonForm) return
    if (!buttonForm.name) { getMessage().warning('请填写按钮名称'); return }
    if (buttonForm.action_type === 'assign' && !String(buttonForm.action_params?.assignee || '')) {
      getMessage().warning('指派按钮需填写目标受派人'); return
    }
    setButtonSaving(true)
    const ok = await saveButton(buttonForm)
    setButtonSaving(false)
    if (ok) setButtonForm(null)
  }

  const BUTTON_ACTION_LABELS: Record<string, string> = {
    assign: '指派', execute: '执行', complete: '完成', close: '关闭', reopen: '重开',
    hold: '挂起', resume: '恢复', rollback: '回滚', withdraw: '撤回',
  }

  // runButtonAction 把自定义按钮映射到既有工单操作端点执行(权限由端点把守)。
  const runButtonAction = async (b: TicketButton) => {
    if (!selectedTicket) return
    const t = selectedTicket
    const p = (b.action_params || {}) as Record<string, any>
    const str = (k: string) => String(p[k] ?? '')
    let ok = false
    switch (b.action_type) {
      case 'assign': ok = await assign(t.id, str('assignee')); break
      case 'execute': ok = await execute(t.id); break
      case 'complete': ok = await complete(t.id, str('comment')); break
      case 'close': ok = await close(t.id, str('comment')); break
      case 'reopen': ok = await reopen(t.id, str('reason')); break
      case 'hold': ok = await hold(t.id, str('reason')); break
      case 'resume': ok = await resume(t.id); break
      case 'rollback': ok = await rollback(t.id, str('reason')); break
      case 'withdraw': ok = await withdraw(t.id, str('reason')); break
      default: getMessage().error(`未知按钮动作: ${b.action_type}`)
    }
    setPendingButton(null)
    if (ok) {
      await handleSelectTicket(await getTicket(t.id)); fetchStats()
    }
  }

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
    const ok = await relate(selectedTicket.id, relateId.trim(), relateRelation, relateReason)
    if (!ok) return
    setRelateDialog(false); setRelateId(''); setRelateRelation('related'); setRelateReason('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
  }
  // 装载关联工单摘要：逐条 GET 目标工单，得到标题/状态供面板展示（与列表分页无关，确保命中）。
  const loadRelatedTickets = async (ids: string[]) => {
    const out: Ticket[] = []
    for (const id of ids) {
      try {
        const t = await apiFetch<Ticket>(`/itsm/tickets/${id}`)
        if (t) out.push(t)
      } catch { /* 关联工单不可见时跳过 */ }
    }
    setRelatedTickets(out)
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
      const created = await apiFetch<any>('/knowledge', { method: 'POST', body: JSON.stringify({ title: ticket.title + ' - 处理总结', content, category: '故障处理', tags: [ticket.risk_level, '工单转换'], author: me?.username || 'anonymous', status: 'draft' }) })
      getMessage().success('已创建知识草稿，可继续完善后发布')
      // 直达新建的文章详情页（草稿态可继续编辑/发布），而不是落在文章列表
      navigate(created?.id ? `/knowledge/${created.id}` : '/knowledge')
    } catch (e: any) { getMessage().error('转换失败: ' + e.message) }
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
    } catch { getMessage().error('添加评论失败') }
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
    } catch { getMessage().error('删除评论失败') }
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
    } catch { getMessage().error('创建子任务失败') }
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
      getMessage().success('已保存处理情况')
      setSubtaskDetailOpen(false)
      fetchSubtasks(selectedTicket.id)
    } catch { getMessage().error('保存处理情况失败') }
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
    } catch { getMessage().error('更新子任务失败') }
  }

  const handleSetSubtaskStatus = async (st: Subtask, status: string) => {
    if (!selectedTicket || status === st.status) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/subtasks/${st.id}`, {
        method: 'PUT',
        body: JSON.stringify({ status }),
      })
      fetchSubtasks(selectedTicket.id)
    } catch { getMessage().error('更新子任务失败') }
  }

  const handleDeleteSubtask = async (sid: number) => {
    if (!selectedTicket) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/subtasks/${sid}`, { method: 'DELETE' })
      fetchSubtasks(selectedTicket.id)
    } catch { getMessage().error('删除子任务失败') }
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
    } catch { getMessage().error('上传附件失败') }
    setUploading(false)
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const handleDeleteAttachment = async (aid: number) => {
    if (!selectedTicket) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/attachments/${aid}`, { method: 'DELETE' })
      fetchAttachments(selectedTicket.id)
    } catch { getMessage().error('删除附件失败') }
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
    } catch { getMessage().error('CSV 导入失败') }
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

  // 列表筛选：关键词（标题/创建人/负责人）+ 状态 + 「我的工单」
  const kw = q.trim().toLowerCase()
  const ticketView = tickets
    .filter(t => !kw || (t.title || '').toLowerCase().includes(kw) || (t.created_by || '').toLowerCase().includes(kw) || (t.assignee || '').toLowerCase().includes(kw))
    .filter(t => !statusFilter || t.status === statusFilter)
    .filter(t => !(myOnly && me) || t.created_by === me.username || (t.assignee || '') === me.username)

  const PAGE_SIZE = 20
  const totalPages = Math.max(1, Math.ceil(ticketView.length / PAGE_SIZE))
  const paged = ticketView.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE)

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          {isDetail && (
            <Button type="default" onClick={() => navigate('/itsm/tickets')}>
              <ChevronLeft className="h-4 w-4" />返回列表
            </Button>
          )}
          <div><h1 className="text-2xl font-bold">{isDetail ? '工单详情' : 'ITSM 工单'}</h1><p className="text-sm text-muted-foreground">{isDetail ? '变更审批与风险控制 · 全宽视图' : '变更审批与风险控制'}</p></div>
        </div>
        {!isDetail && (
        <div className="flex gap-2">
          <input ref={importFileRef} type="file" accept=".csv" className="hidden" onChange={handleImportCSV} />
          <Button type="default" onClick={() => importFileRef.current?.click()} disabled={importing || !canWrite}><FileUp className="h-4 w-4" />{importing ? '导入中...' : '批量导入'}</Button>
          <Button type="default" onClick={handleExportTickets}><FileDown className="h-4 w-4" />导出工单</Button>
          <Button type="default" onClick={handleExportStats}><FileDown className="h-4 w-4" />导出统计</Button>
          <Button type="default" onClick={openConfigDialog} disabled={!canWrite}><FileText className="h-4 w-4" />表单配置</Button>
          <Button type="default" onClick={() => setTemplateModalOpen(true)} disabled={!canWrite}><ListChecks className="h-4 w-4" />提单模板</Button>
          <Button type="default" onClick={() => setAutoModalOpen(true)} disabled={!canWrite}><Zap className="h-4 w-4" />自动化</Button>
          <Button type="primary" onClick={() => openCreateDialog()} disabled={!canWrite}><Plus className="h-4 w-4" />新建工单</Button>
        </div>
        )}
      </div>

      {stats && !isDetail && (
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

      {/* 列表模式：卡片网格铺满全宽（不再留右侧详情占位栏）；详情模式：全宽单卡 */}
      {!isDetail ? (
        <div>
          <Card>
            <CardHeader className="py-3 flex items-center justify-between gap-2 flex-wrap">
              <CardTitle className="text-sm">工单列表{myOnly ? ' · 我的工单' : ''}（{ticketView.length}）</CardTitle>
              <div className="flex items-center gap-2">
                <Input value={q} onChange={e => { setQ(e.target.value); setPage(1) }} placeholder="搜索标题 / 创建人 / 负责人" className="h-8 w-56 text-xs" allowClear />
                <select className="h-8 rounded-md border border-input bg-background px-2 text-xs" value={statusFilter} onChange={e => { setStatusFilter(e.target.value); setPage(1) }}>
                  <option value="">全部状态</option>
                  {Object.entries(STATUS_LABELS).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                </select>
                <button
                  type="button"
                  onClick={() => { setMyOnly(v => !v); setPage(1) }}
                  className={`text-xs rounded-full px-2.5 py-1 border transition-colors ${myOnly ? 'bg-primary text-primary-foreground border-primary' : 'border-input text-muted-foreground hover:bg-accent'}`}
                  title="仅显示由我创建或指派给我的工单"
                >
                  我的工单{myOnly ? ' ✓' : ''}
                </button>
              </div>
            </CardHeader>
            <CardContent className="p-0">
              {loading ? (
                <p className="px-4 py-6 text-sm text-muted-foreground">加载中...</p>
              ) : ticketView.length === 0 ? (
                <p className="px-4 py-6 text-sm text-muted-foreground">暂无工单。</p>
              ) : (
                <>
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b bg-muted/40 text-xs text-muted-foreground">
                          <th className="text-left font-medium px-4 py-2.5">标题</th>
                          <th className="text-left font-medium px-4 py-2.5 whitespace-nowrap">工单号</th>
                          <th className="text-left font-medium px-4 py-2.5">状态</th>
                          <th className="text-left font-medium px-4 py-2.5">风险</th>
                          <th className="text-left font-medium px-4 py-2.5">负责人</th>
                          <th className="text-left font-medium px-4 py-2.5">创建人</th>
                          <th className="text-left font-medium px-4 py-2.5">创建时间</th>
                          <th className="text-right font-medium px-4 py-2.5">操作</th>
                        </tr>
                      </thead>
                      <tbody>
                        {paged.map(t => (
                          <tr
                            key={t.id}
                            onClick={() => navigate(`/itsm/tickets/${t.id}`)}
                            className={`border-b last:border-0 cursor-pointer hover:bg-accent transition-colors ${selectedTicket?.id === t.id ? 'bg-accent' : ''} ${isOverdue(t) ? 'border-l-2 border-l-destructive' : ''}`}
                          >
                            <td className="px-4 py-2.5 max-w-[360px]">
                              <span className="font-medium truncate">{t.title}</span>
                            </td>
                            <td className="px-4 py-2.5 whitespace-nowrap">
                              <span className="font-mono text-xs text-muted-foreground">{t.number || '—'}</span>
                            </td>
                            <td className="px-4 py-2.5"><Tag color={statusVariant(t.status)} className="text-xs">{STATUS_LABELS[t.status] || t.status}</Tag></td>
                            <td className="px-4 py-2.5"><Tag color={RISK_BADGE_VARIANT[t.risk_level] || 'secondary'} className="text-xs">{RISK_LABELS[t.risk_level] || t.risk_level}</Tag></td>
                            <td className="px-4 py-2.5 text-muted-foreground">{t.assignee || '未指派'}</td>
                            <td className="px-4 py-2.5 text-muted-foreground">{t.created_by}</td>
                            <td className="px-4 py-2.5 text-muted-foreground whitespace-nowrap">{formatTime(t.created_at)}</td>
                            <td className="px-4 py-2.5 text-right">
                              <Button size="small" type="default" onClick={(e) => { e.stopPropagation(); navigate(`/itsm/tickets/${t.id}`) }}>查看</Button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                  {totalPages > 1 && (
                    <div className="flex items-center justify-between px-4 py-2.5 border-t text-xs text-muted-foreground">
                      <span>共 {ticketView.length} 条</span>
                      <div className="flex items-center gap-2">
                        <button type="button" disabled={page <= 1} onClick={() => setPage(p => Math.max(1, p - 1))} className="px-2 py-1 rounded border border-input disabled:opacity-40 hover:bg-accent">上一页</button>
                        <span>{page} / {totalPages}</span>
                        <button type="button" disabled={page >= totalPages} onClick={() => setPage(p => p + 1)} className="px-2 py-1 rounded border border-input disabled:opacity-40 hover:bg-accent">下一页</button>
                      </div>
                    </div>
                  )}
                </>
              )}
            </CardContent>
          </Card>
        </div>
      ) : (
        <div>
          {selectedTicket ? (
            <Card>
              <CardHeader className="py-3">
                <div>
                  <CardTitle className="text-lg">{selectedTicket.title}</CardTitle>
                  <CardDescription className="text-xs">工单号: <span className="font-mono">{selectedTicket.number || selectedTicket.id.slice(0, 12)}</span> · 提单人 {selectedTicket.created_by} · {formatTime(selectedTicket.created_at)}</CardDescription>
                </div>
              </CardHeader>
              <CardContent>
                {/* 顶部摘要条：状态 / 当前节点 / 处理人 / 逾期一眼可见 */}
                <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm mb-2 border-b pb-3">
                  <div className="flex items-center gap-1.5"><span className="text-muted-foreground">状态</span><Tag color={statusVariant(selectedTicket.status)} className="text-xs">{STATUS_LABELS[selectedTicket.status]}</Tag></div>
                  <div className="flex items-center gap-1.5"><span className="text-muted-foreground">风险</span><Tag color={RISK_BADGE_VARIANT[selectedTicket.risk_level]} className="text-xs">{RISK_LABELS[selectedTicket.risk_level]}</Tag></div>
                  <div><span className="text-muted-foreground">当前节点</span> <span className="font-medium">{(() => {
                    const proc = processes.find(p => p.id === selectedTicket.process_id)
                    return stepName || (processInst?.current_step ? proc?.steps.find(s => s.id === processInst.current_step)?.name || processInst.current_step : '—')
                  })()}</span></div>
                  <div><span className="text-muted-foreground">当前处理人</span> <span className="font-medium">{selectedTicket.assignee || '未指派'}</span></div>
                  <div>
                    <span className="text-muted-foreground">逾期</span>{' '}
                    {isOverdue(selectedTicket)
                      ? <span className="text-destructive font-medium">已逾期</span>
                      : <span>否</span>}
                    {selectedTicket.deadline_at && <span className="text-muted-foreground">（截止 {formatTime(selectedTicket.deadline_at)}）</span>}
                  </div>
                  <div><span className="text-muted-foreground">SLA 目标</span> {SLA_BY_RISK[selectedTicket.risk_level] || '72小时'}</div>
                  <div><span className="text-muted-foreground">流程</span> {selectedTicket.process_id ? (processes.find(p => p.id === selectedTicket.process_id)?.name || selectedTicket.process_id.slice(0, 8)) : '无'}</div>
                  {selectedTicket.scheduled_at && <div><span className="text-muted-foreground">计划执行</span> {formatTime(selectedTicket.scheduled_at)}</div>}
                </div>
                {/* 次要过程信息：审批 / 驳回 / 完成 / 回滚等 */}
                <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-muted-foreground mb-4">
                  {selectedTicket.approved_by && <span>审批人 {selectedTicket.approved_by}{selectedTicket.approved_at ? ` · ${formatTime(selectedTicket.approved_at)}` : ''}</span>}
                  {selectedTicket.reject_reason && <span className="text-destructive">驳回原因：{selectedTicket.reject_reason}</span>}
                  {selectedTicket.completed_by && <span>完成人 {selectedTicket.completed_by}{selectedTicket.completed_at ? ` · ${formatTime(selectedTicket.completed_at)}` : ''}</span>}
                  {selectedTicket.rolled_back_by && <span className="text-amber-600">回滚人 {selectedTicket.rolled_back_by}{selectedTicket.rollback_reason ? ` · ${selectedTicket.rollback_reason}` : ''}</span>}
                  {selectedTicket.status === 'closed' && (selectedTicket.satisfaction ?? 0) > 0 && <span className="text-amber-500">满意度 {'★'.repeat(selectedTicket.satisfaction ?? 0)}</span>}
                  {selectedTicket.labels?.source === 'ai_inspection' && <span>AI 巡检转单{selectedTicket.labels?.kb_archived === 'true' ? ' · 已沉淀知识库' : ''}</span>}
                </div>

                {/* 审批链进度: 顺序/并行审批的当前审批人与投票记录 */}
                {selectedTicket.approval_chain && (selectedTicket.approval_chain.approvers?.length || 0) > 0 && (() => {
                  const chain = selectedTicket.approval_chain!
                  const st = selectedTicket.approval_status
                  const votes = st?.votes || []
                  const curIdx = st?.current_index ?? 0
                  const voteOf = (name: string) => votes.find((v: any) => v.approver === name)
                  return (
                    <div className="border rounded-md p-3 mb-4 space-y-2">
                      <div className="flex items-center justify-between">
                        <h4 className="text-sm font-medium flex items-center gap-2">
                          审批进度
                          <Tag color={chain.mode === 'parallel' ? 'purple' : 'blue'}>{chain.mode === 'parallel' ? '并行会签' : '依次审批'}</Tag>
                        </h4>
                        {selectedTicket.status === 'pending_review' && (
                          <Button size="small" icon={<UserPlus className="h-3.5 w-3.5" />} onClick={() => { setAddApproverName(undefined); setAddApproverPosition('after'); setAddApproverDialog(true) }}>加签</Button>
                        )}
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        {chain.approvers.map((name, i) => {
                          const v = voteOf(name)
                          const isCurrent = chain.mode === 'sequential' && i === curIdx && !v && selectedTicket.status === 'pending_review'
                          return (
                            <span key={`${name}-${i}`} className={`inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs border ${isCurrent ? 'border-blue-500 text-blue-600 bg-blue-500/10 font-medium' : v?.approved ? 'border-emerald-500 text-emerald-600' : v && !v.approved ? 'border-red-500 text-red-600' : 'border-border text-muted-foreground'}`}>
                              <span className="opacity-60">{i + 1}.</span> {name}
                              {v?.approved && <Check className="h-3 w-3" />}
                              {v && !v.approved && <X className="h-3 w-3" />}
                              {isCurrent && <span>（当前）</span>}
                            </span>
                          )
                        })}
                      </div>
                      {selectedTicket.status === 'pending_review' && chain.mode === 'sequential' && chain.approvers[curIdx] && (
                        <p className="text-xs text-muted-foreground">当前待 {chain.approvers[curIdx]} 审批{chain.approvers.length > curIdx + 1 ? `，后续：${chain.approvers.slice(curIdx + 1).join(' → ')}` : ''}</p>
                      )}
                      {selectedTicket.status === 'pending_review' && chain.mode === 'parallel' && (
                        <p className="text-xs text-muted-foreground">会签进度 {votes.filter((v: any) => v.approved).length}/{chain.approvers.length}</p>
                      )}
                      {st?.final_result === 'rejected' && <p className="text-xs text-destructive">审批链已驳回</p>}
                    </div>
                  )
                })()}

                {/* 工单内容：表单直接平铺，用户打开即见 */}
                <div className="space-y-3">
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
                    {(selectedTicket.related_ticket_ids || []).length > 0 && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">关联工单</h4>
                        <div className="flex flex-col gap-1">
                          {(selectedTicket.related_ticket_ids || []).map(rid => {
                            const r = relatedTickets.find(t => t.id === rid)
                            return (
                              <button key={rid} type="button" onClick={() => navigate(`/itsm/tickets/${rid}`)}
                                className="text-left text-xs border rounded-md px-2 py-1.5 hover:bg-muted/50 transition-colors">
                                <span className="font-medium">{r ? r.title : rid.slice(0, 12) + '...'}</span>
                                {r && <span className="ml-2 text-muted-foreground">{STATUS_LABELS[r.status] || r.status}</span>}
                                <span className="ml-2 text-muted-foreground font-mono">{rid.slice(0, 8)}</span>
                              </button>
                            )
                          })}
                        </div>
                      </div>
                    )}
                    {/* 有 schema 字段即渲染表单：当前节点可编辑则开放填写，否则只读展示。
                        不再依赖 custom_fields 非空——否则建单时未填自定义字段的工单整块表单消失。 */}
                    {selectedSchema && (selectedSchema.fields || []).length > 0 ? (
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
                          permCaps={stepPermCaps(stepPerms)}
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
                </div>

                {/* 辅助信息 tabs：流程图 / SLA / 事件 / 评论 / 子任务 / 附件 */}
                <Tabs activeKey={detailTab} onChange={setDetailTab} items={[
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
                            stages={processInst?.stages}
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

                {/* 底部操作栏：按当前状态展示可用动作 */}
                <div className="mt-4 pt-3 border-t flex flex-wrap items-center gap-2">
                  <span className="text-xs text-muted-foreground mr-1">操作</span>
                  {!['rejected', 'rolled_back', 'closed', 'withdrawn'].includes(selectedTicket.status) && stepAllows('assign') && (
                    <Button type="default" size="middle" onClick={() => { setAssignDialog(true); setAssigneeInput(selectedTicket.assignee || '') }} disabled={!canWrite}><UserPlus className="h-3 w-3" />指派</Button>
                  )}
                  {selectedTicket.status === 'draft' && stepAllows('submit') && (
                    <Button type="primary" size="middle" onClick={handleSubmitTicket} disabled={!canWrite}><Send className="h-3 w-3" />提交</Button>
                  )}
                  {selectedTicket.status === 'draft' && stepAllows('withdraw') && (
                    <Button type="default" size="middle" onClick={handleWithdraw} disabled={!canWrite}><X className="h-3 w-3" />撤回</Button>
                  )}
                  {selectedTicket.status === 'closed' && stepAllows('reopen') && (
                    <Button type="default" size="middle" onClick={handleReopen} disabled={!canWrite}><RotateCcw className="h-3 w-3" />重开</Button>
                  )}
                  {selectedTicket.status === 'pending_review' && stepAllows('approve') && (
                    <Button type="primary" size="middle" onClick={() => { setApproveDialog(true); setApproveComment('') }} disabled={!canApprove}><Check className="h-3 w-3" />通过</Button>
                  )}
                  {selectedTicket.status === 'pending_review' && stepAllows('reject') && (
                    <Button type="default" danger size="middle" onClick={() => { setRejectDialog(true); setRejectReason('') }} disabled={!canApprove}><X className="h-3 w-3" />驳回</Button>
                  )}
                  {selectedTicket.status === 'approved' && stepAllows('execute') && (
                    <Button type="primary" size="middle" onClick={handleExecute} disabled={!canWrite}><Play className="h-3 w-3" />执行</Button>
                  )}
                  {/* 免审批直达处理：低/中风险工单在草稿/待审核态可直接开始处理，高风险仍须走审批 */}
                  {['draft', 'pending_review'].includes(selectedTicket.status) && !['high', 'critical'].includes(selectedTicket.risk_level) && stepAllows('execute') && (
                    <Button type="primary" size="middle" onClick={handleExecute} disabled={!canWrite}><Play className="h-3 w-3" />开始处理</Button>
                  )}
                  {selectedTicket.status === 'executing' && stepAllows('complete') && (
                    <Button type="primary" size="middle" onClick={() => { setCompleteDialog(true); setCompleteComment('') }} disabled={!canWrite}>完成</Button>
                  )}
                  {/* 处理中直接关闭：取消/重复单等场景，无需先走"完成" */}
                  {selectedTicket.status === 'executing' && stepAllows('close') && (
                    <Button type="default" size="middle" onClick={() => { setCloseDialog(true); setCloseComment(''); setCloseSatisfaction(0) }} disabled={!canWrite}><CheckCircle2 className="h-3 w-3" />关闭</Button>
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
                      <Button type="default" size="middle" onClick={() => setRelateDialog(true)} disabled={!canWrite}><Link2 className="h-3 w-3" />关联工单</Button>
                      {stepAllows('change') && <Dropdown menu={{ items: [
                        { key: 'to_change', label: '转为变更单' },
                        { key: 'to_problem', label: '转为问题工单' },
                      ], onClick: ({ key }) => {
                        if (key === 'to_change') navigate(`/itsm/change?ticket=${selectedTicket.id}`)
                        else openConvertTicket()
                      } }}>
                        <Button type="default" size="middle" disabled={!canWrite}><FileText className="h-3 w-3" />转建</Button>
                      </Dropdown>}
                      {/* 自定义按钮：visible_statuses 过滤 + roles 匹配(空=不限制)，confirm_text 需二次确认 */}
                      {buttons
                        .filter(b => !b.visible_statuses?.length || b.visible_statuses.includes(selectedTicket.status))
                        .filter(b => {
                          if (!b.roles?.length) return true
                          const myRoles: string[] = Array.isArray((me as any)?.role) ? (me as any).role : [(me as any)?.role || '']
                          return b.roles.some(r => myRoles.includes(r))
                        })
                        .map(b => (
                          <Button key={b.id} type="default" size="middle" className="border-dashed"
                            onClick={() => { if (b.confirm_text) { setPendingButton(b) } else { runButtonAction(b) } }}
                            disabled={!canWrite}>
                            <Zap className="h-3 w-3" />{b.name}
                          </Button>
                        ))}
                    </>
                  )}
                </div>
              </CardContent>
            </Card>
          ) : (
            <Card className="h-full flex items-center justify-center min-h-[400px]">
              <CardContent className="text-center text-muted-foreground">
                <p>工单加载中...</p>
              </CardContent>
            </Card>
          )}
        </div>
      )}

      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title={`新建工单${createScope !== 'ticket:default' ? ' · ' + createScope : ''}`} footer={null}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          <div className="col-span-2"><label className="text-sm font-medium">从模板新建</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value="" onChange={e => {
              const t = templates.find(x => x.id === e.target.value)
              if (t) applyTemplate(t)
            }}>
              <option value="">不使用模板</option>
              {templates.map(t => <option key={t.id} value={t.id}>{t.name}{t.category ? `（${t.category}）` : ''}</option>)}
            </select>
          </div>
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
          {/* 审批链(可选): sequential=依次逐级审批, parallel=并行会签; 不填走默认单人审批 */}
          <div className="border rounded-md p-3 space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium">审批链（可选）</label>
              {chainApprovers.length > 0 && <Button type="link" size="small" className="px-0" onClick={() => { setChainApprovers([]); setChainMode('sequential') }}>清除</Button>}
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div>
                <label className="text-xs text-muted-foreground">审批方式</label>
                <Select className="w-full mt-1" value={chainMode} onChange={(v: 'sequential' | 'parallel') => setChainMode(v)} disabled={chainApprovers.length === 0}>
                  <Select.Option value="sequential">依次审批（逐级）</Select.Option>
                  <Select.Option value="parallel">并行审批（会签）</Select.Option>
                </Select>
              </div>
              <div>
                <label className="text-xs text-muted-foreground">审批人</label>
                <Select className="w-full mt-1" mode="multiple" allowClear placeholder="选择审批人" value={chainApprovers} onChange={(v: string[]) => setChainApprovers(v)} optionFilterProp="label" showSearch
                  options={users.map(u => ({ label: u.username, value: u.username }))} />
              </div>
            </div>
            {chainApprovers.length > 1 && chainMode === 'sequential' && (
              <p className="text-xs text-muted-foreground">将按所选顺序依次审批：{chainApprovers.join(' → ')}</p>
            )}
            {chainApprovers.length > 1 && chainMode === 'parallel' && (
              <p className="text-xs text-muted-foreground">并行会签：{chainApprovers.length} 人全部通过才视为审批通过</p>
            )}
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

      <Modal open={addApproverDialog} onCancel={() => setAddApproverDialog(false)} title="加签审批人" footer={null}>
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">加签对象</label>
            <Select className="w-full mt-1" showSearch optionFilterProp="label" placeholder="选择要追加的审批人" value={addApproverName} onChange={(v: string) => setAddApproverName(v)}
              options={users.filter(u => !(selectedTicket?.approval_chain?.approvers || []).includes(u.username)).map(u => ({ label: u.username, value: u.username }))} />
          </div>
          <div>
            <label className="text-sm font-medium">加签位置</label>
            <Select className="w-full mt-1" value={addApproverPosition} onChange={(v: 'before' | 'after') => setAddApproverPosition(v)}>
              <Select.Option value="after">后加签（当前审批人之后）</Select.Option>
              <Select.Option value="before">前加签（当前审批人之前，先由其审批）</Select.Option>
            </Select>
          </div>
          <p className="text-xs text-muted-foreground">加签后审批链将按新顺序继续，已完成的审批不受影响。</p>
        </div>
        <div className="flex justify-end gap-2 mt-3"><Button onClick={() => setAddApproverDialog(false)}>取消</Button><Button onClick={handleAddApprover} disabled={!canApprove || !addApproverName || addApproverSaving}>{addApproverSaving ? '提交中...' : '确认加签'}</Button></div>
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
          <div><label className="text-sm font-medium">关联类型</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={relateRelation} onChange={e => setRelateRelation(e.target.value)}>
              <option value="related">关联</option>
              <option value="parent">父单</option>
              <option value="child">子单</option>
              <option value="depends_on">依赖</option>
              <option value="blocks">阻塞</option>
            </select>
          </div>
          <div><label className="text-sm font-medium">关联原因（可选）</label><Input className="mt-1" value={relateReason} onChange={e => setRelateReason(e.target.value)} placeholder="关联原因" /></div>
          <p className="text-xs text-muted-foreground">关联是双向的：目标工单也会显示与本工单的关联。</p>
        </div>
        <div className="flex justify-end gap-2"><Button onClick={() => setRelateDialog(false)}>取消</Button><Button onClick={handleRelate} disabled={!canWrite || !relateId.trim()}>确认关联</Button></div>
      </Modal>

      {/* 提单模板管理：列表 + 编辑器两视图 */}
      {/* 工单自动化：触发器 + 自定义按钮 */}
      <Modal open={autoModalOpen} onCancel={() => { setAutoModalOpen(false); setRuleForm(null); setButtonForm(null) }} title="工单自动化" footer={null} width={860}>
        <Tabs activeKey={autoTab} onChange={k => { setAutoTab(k as 'rules' | 'buttons'); setRuleForm(null); setButtonForm(null) }}
          items={[
            {
              key: 'rules', label: `触发器 (${rules.length})`,
              children: (
                <div className="space-y-3">
                  <div className="flex justify-between items-center">
                    <p className="text-xs text-muted-foreground">工单事件命中条件后自动执行动作（通知 / 自动指派 / Webhook）。</p>
                    <Button type="primary" size="small" onClick={() => setRuleForm(emptyRule())}><Plus className="h-3 w-3" />新建规则</Button>
                  </div>
                  {ruleForm ? (
                    <div className="space-y-3 border rounded-md p-3">
                      <div className="grid grid-cols-2 gap-3">
                        <div><label className="text-xs font-medium">规则名称 *</label>
                          <Input className="text-xs mt-1" value={ruleForm.name} onChange={e => setRuleForm({ ...ruleForm, name: e.target.value })} placeholder="如：P1 工单自动通知值班群" /></div>
                        <div><label className="text-xs font-medium">触发事件 *</label>
                          <Select className="w-full mt-1" size="small" value={ruleForm.event} onChange={v => setRuleForm({ ...ruleForm, event: v })}
                            options={[
                              { value: 'ticket.created', label: '工单创建' }, { value: 'ticket.submitted', label: '工单送审' },
                              { value: 'ticket.approved', label: '审批通过' }, { value: 'ticket.rejected', label: '审批驳回' },
                              { value: 'ticket.executing', label: '开始执行' }, { value: 'ticket.completed', label: '执行完成' },
                              { value: 'ticket.rolled_back', label: '已回滚' }, { value: 'ticket.closed', label: '工单关闭' },
                              { value: 'ticket.reopened', label: '工单重开' }, { value: 'ticket.withdrawn', label: '工单撤回' },
                              { value: 'schedule', label: '定时调度（按计划扫描工单）' },
                            ]} /></div>
                      </div>
                      {ruleForm.event === 'schedule' && (
                        <div className="grid grid-cols-2 gap-3 border rounded-md p-2 bg-muted/40">
                          <div><label className="text-xs font-medium">Cron 表达式（5 段：分 时 日 月 周，优先）</label>
                            <Input className="text-xs mt-1" value={ruleForm.schedule?.cron || ''} placeholder="0 9 * * 1-5 = 工作日每天 9 点"
                              onChange={e => setRuleForm({ ...ruleForm, schedule: { ...ruleForm.schedule, cron: e.target.value } })} /></div>
                          <div><label className="text-xs font-medium">或间隔（分钟，未填 Cron 时生效）</label>
                            <Input className="text-xs mt-1" type="number" value={ruleForm.schedule?.interval_minutes || ''} placeholder="60 = 每小时"
                              onChange={e => setRuleForm({ ...ruleForm, schedule: { ...ruleForm.schedule, interval_minutes: Number(e.target.value) || undefined } })} /></div>
                        </div>
                      )}
                      <SwitchField label="启用" checked={ruleForm.enabled} onCheckedChange={v => setRuleForm({ ...ruleForm, enabled: v })} />
                      <div>
                        <label className="text-xs font-medium">触发条件（全部满足才触发，留空=无条件）</label>
                        <div className="space-y-1.5 mt-1">
                          {(ruleForm.conditions || []).map((c, i) => (
                            <div key={i} className="flex gap-1.5 items-center">
                              <Input className="text-xs flex-1" value={c.field} placeholder="字段：risk_level / status / assignee / custom_fields.x"
                                onChange={e => { const cs = [...(ruleForm.conditions || [])]; cs[i] = { ...c, field: e.target.value }; setRuleForm({ ...ruleForm, conditions: cs }) }} />
                              <Select size="small" style={{ width: 110 }} value={c.op} onChange={v => { const cs = [...(ruleForm.conditions || [])]; cs[i] = { ...c, op: v }; setRuleForm({ ...ruleForm, conditions: cs }) }}
                                options={[{ value: 'equals', label: '等于' }, { value: 'not_equals', label: '不等于' }, { value: 'contains', label: '包含' }]} />
                              <Input className="text-xs flex-1" value={c.value} placeholder="值"
                                onChange={e => { const cs = [...(ruleForm.conditions || [])]; cs[i] = { ...c, value: e.target.value }; setRuleForm({ ...ruleForm, conditions: cs }) }} />
                              <Button type="text" size="small" onClick={() => setRuleForm({ ...ruleForm, conditions: (ruleForm.conditions || []).filter((_, j) => j !== i) })}><Trash2 className="h-3 w-3" /></Button>
                            </div>
                          ))}
                          <Button type="dashed" size="small" onClick={() => setRuleForm({ ...ruleForm, conditions: [...(ruleForm.conditions || []), { field: '', op: 'equals', value: '' }] })}><Plus className="h-3 w-3" />添加条件</Button>
                        </div>
                      </div>
                      <div>
                        <label className="text-xs font-medium">执行动作（依序执行）</label>
                        <div className="space-y-1.5 mt-1">
                          {ruleForm.actions.map((a, i) => (
                            <div key={i} className="flex gap-1.5 items-start">
                              <Select size="small" style={{ width: 110 }} value={a.type} onChange={v => { const as = [...ruleForm.actions]; as[i] = { ...a, type: v, target: '' }; setRuleForm({ ...ruleForm, actions: as }) }}
                                options={[{ value: 'notify', label: '通知' }, { value: 'assign', label: '自动指派' }, { value: 'webhook', label: 'Webhook' }, { value: 'start_process', label: '启动流程' }]} />
                              {a.type === 'notify'
                                ? <div className="flex-1 space-y-1">
                                    <Input className="text-xs w-full" value={a.message || ''} placeholder="通知内容，支持 ${ticket.title} / ${custom_fields.x}"
                                      onChange={e => { const as = [...ruleForm.actions]; as[i] = { ...a, message: e.target.value }; setRuleForm({ ...ruleForm, actions: as }) }} />
                                    <Select className="text-xs w-full" size="small" mode="multiple" allowClear value={a.channels || []} placeholder="通知渠道（留空=按通知策略分发）"
                                      onChange={v => { const as = [...ruleForm.actions]; as[i] = { ...a, channels: v }; setRuleForm({ ...ruleForm, actions: as }) }}
                                      options={notifChannels.map(c => ({ value: c.id, label: `${c.name} (${c.type})` }))} />
                                  </div>
                                : a.type === 'start_process'
                                ? <Select className="text-xs flex-1" size="small" value={a.target || undefined} placeholder="选择要启动的流程"
                                    onChange={v => { const as = [...ruleForm.actions]; as[i] = { ...a, target: v }; setRuleForm({ ...ruleForm, actions: as }) }}
                                    options={processes.filter(p => p.enabled).map(p => ({ value: p.id, label: p.name }))} />
                                : <Input className="text-xs flex-1" value={a.target} placeholder={a.type === 'assign' ? '受派人用户名' : '回调 URL'}
                                    onChange={e => { const as = [...ruleForm.actions]; as[i] = { ...a, target: e.target.value }; setRuleForm({ ...ruleForm, actions: as }) }} />}
                              <Button type="text" size="small" onClick={() => setRuleForm({ ...ruleForm, actions: ruleForm.actions.filter((_, j) => j !== i) })}><Trash2 className="h-3 w-3" /></Button>
                            </div>
                          ))}
                          <Button type="dashed" size="small" onClick={() => setRuleForm({ ...ruleForm, actions: [...ruleForm.actions, { type: 'notify', target: '', message: '' }] })}><Plus className="h-3 w-3" />添加动作</Button>
                        </div>
                      </div>
                      <div className="flex justify-end gap-2">
                        <Button size="small" onClick={() => setRuleForm(null)}>取消</Button>
                        <Button type="primary" size="small" loading={ruleSaving} onClick={handleSaveRule}>保存规则</Button>
                      </div>
                    </div>
                  ) : (
                    <div className="space-y-1.5 max-h-[55vh] overflow-y-auto">
                      {rules.length === 0 && <p className="text-xs text-muted-foreground text-center py-6">暂无触发器</p>}
                      {rules.map(r => (
                        <div key={r.id} className="flex items-center justify-between border rounded-md px-3 py-2">
                          <div className="min-w-0">
                            <p className="text-sm font-medium truncate">{r.name} {!r.enabled && <Tag className="text-[10px]">已停用</Tag>}</p>
                            <p className="text-[11px] text-muted-foreground truncate">
                              {r.event === 'schedule' ? `定时 ${r.schedule?.cron || `每 ${r.schedule?.interval_minutes} 分钟`} · 动作 ${r.actions.map(a => ({ notify: '通知', assign: '指派', webhook: 'Webhook', start_process: '启动流程' } as any)[a.type] || a.type).join('、')}`
                                : `${r.event} · 条件 ${r.conditions?.length || 0} 项 · 动作 ${r.actions.map(a => ({ notify: '通知', assign: '指派', webhook: 'Webhook', start_process: '启动流程' } as any)[a.type] || a.type).join('、')}`}
                            </p>
                          </div>
                          <div className="flex gap-1 shrink-0">
                            <Button type="text" size="small" onClick={() => setRuleForm({ ...r, conditions: r.conditions || [], actions: r.actions || [] })}>编辑</Button>
                            <Button type="text" size="small" danger onClick={() => deleteRule(r.id)}><Trash2 className="h-3 w-3" /></Button>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              ),
            },
            {
              key: 'buttons', label: `自定义按钮 (${buttons.length})`,
              children: (
                <div className="space-y-3">
                  <div className="flex justify-between items-center">
                    <p className="text-xs text-muted-foreground">工单详情页快捷操作按钮，点击后走既有工单操作接口（权限由接口把守）。</p>
                    <Button type="primary" size="small" onClick={() => setButtonForm(emptyButton())}><Plus className="h-3 w-3" />新建按钮</Button>
                  </div>
                  {buttonForm ? (
                    <div className="space-y-3 border rounded-md p-3">
                      <div className="grid grid-cols-2 gap-3">
                        <div><label className="text-xs font-medium">按钮名称 *</label>
                          <Input className="text-xs mt-1" value={buttonForm.name} onChange={e => setButtonForm({ ...buttonForm, name: e.target.value })} placeholder="如：一键升级网络组" /></div>
                        <div><label className="text-xs font-medium">动作类型 *</label>
                          <Select className="w-full mt-1" size="small" value={buttonForm.action_type} onChange={v => setButtonForm({ ...buttonForm, action_type: v })}
                            options={Object.entries(BUTTON_ACTION_LABELS).map(([value, label]) => ({ value, label }))} /></div>
                      </div>
                      {buttonForm.action_type === 'assign' && (
                        <div><label className="text-xs font-medium">目标受派人 *</label>
                          <Input className="text-xs mt-1" value={String(buttonForm.action_params?.assignee || '')}
                            onChange={e => setButtonForm({ ...buttonForm, action_params: { ...buttonForm.action_params, assignee: e.target.value } })} placeholder="用户名" /></div>
                      )}
                      {['complete', 'close', 'reopen', 'hold', 'rollback', 'withdraw'].includes(buttonForm.action_type) && (
                        <div><label className="text-xs font-medium">{['reopen', 'hold', 'rollback', 'withdraw'].includes(buttonForm.action_type) ? '原因' : '备注'}</label>
                          <Input className="text-xs mt-1" value={String(buttonForm.action_params?.[(['reopen', 'hold', 'rollback', 'withdraw'].includes(buttonForm.action_type)) ? 'reason' : 'comment'] || '')}
                            onChange={e => setButtonForm({ ...buttonForm, action_params: { ...buttonForm.action_params, [(['reopen', 'hold', 'rollback', 'withdraw'].includes(buttonForm.action_type)) ? 'reason' : 'comment']: e.target.value } })} /></div>
                      )}
                      <div className="grid grid-cols-2 gap-3">
                        <div><label className="text-xs font-medium">可见状态（逗号分隔，留空=全部）</label>
                          <Input className="text-xs mt-1" value={(buttonForm.visible_statuses || []).join(',')} placeholder="如 executing,approved"
                            onChange={e => setButtonForm({ ...buttonForm, visible_statuses: e.target.value.split(',').map(s => s.trim()).filter(Boolean) })} /></div>
                        <div><label className="text-xs font-medium">可见角色（逗号分隔，留空=全部）</label>
                          <Input className="text-xs mt-1" value={(buttonForm.roles || []).join(',')} placeholder="如 admin,operator"
                            onChange={e => setButtonForm({ ...buttonForm, roles: e.target.value.split(',').map(s => s.trim()).filter(Boolean) })} /></div>
                      </div>
                      <div className="grid grid-cols-2 gap-3">
                        <div><label className="text-xs font-medium">点击确认文案（留空=直接执行）</label>
                          <Input className="text-xs mt-1" value={buttonForm.confirm_text || ''} onChange={e => setButtonForm({ ...buttonForm, confirm_text: e.target.value })} placeholder="如：确认一键升级？" /></div>
                        <div><label className="text-xs font-medium">排序（小的在前）</label>
                          <Input className="text-xs mt-1" type="number" value={buttonForm.sort_order} onChange={e => setButtonForm({ ...buttonForm, sort_order: Number(e.target.value) || 0 })} /></div>
                      </div>
                      <SwitchField label="启用" checked={buttonForm.enabled} onCheckedChange={v => setButtonForm({ ...buttonForm, enabled: v })} />
                      <div className="flex justify-end gap-2">
                        <Button size="small" onClick={() => setButtonForm(null)}>取消</Button>
                        <Button type="primary" size="small" loading={buttonSaving} onClick={handleSaveButtonCfg}>保存按钮</Button>
                      </div>
                    </div>
                  ) : (
                    <div className="space-y-1.5 max-h-[55vh] overflow-y-auto">
                      {buttons.length === 0 && <p className="text-xs text-muted-foreground text-center py-6">暂无自定义按钮</p>}
                      {buttons.map(b => (
                        <div key={b.id} className="flex items-center justify-between border rounded-md px-3 py-2">
                          <div className="min-w-0">
                            <p className="text-sm font-medium truncate">{b.name} {!b.enabled && <Tag className="text-[10px]">已停用</Tag>}</p>
                            <p className="text-[11px] text-muted-foreground truncate">
                              {BUTTON_ACTION_LABELS[b.action_type] || b.action_type}
                              {b.action_params?.assignee ? ` → ${b.action_params.assignee}` : ''}
                              {b.visible_statuses?.length ? ` · 状态 ${b.visible_statuses.join('/')}` : ' · 全部状态'}
                            </p>
                          </div>
                          <div className="flex gap-1 shrink-0">
                            <Button type="text" size="small" onClick={() => setButtonForm({ ...b, action_params: b.action_params || {}, visible_statuses: b.visible_statuses || [], roles: b.roles || [] })}>编辑</Button>
                            <Button type="text" size="small" danger onClick={() => deleteButton(b.id)}><Trash2 className="h-3 w-3" /></Button>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              ),
            },
          ]} />
      </Modal>

      {/* 自定义按钮执行确认 */}
      <Modal open={!!pendingButton} onCancel={() => setPendingButton(null)} title={pendingButton?.name || '确认执行'} footer={null} width={380}>
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">{pendingButton?.confirm_text || '确认执行该操作？'}</p>
          <div className="flex justify-end gap-2">
            <Button size="middle" onClick={() => setPendingButton(null)}>取消</Button>
            <Button type="primary" size="middle" onClick={() => pendingButton && runButtonAction(pendingButton)}>确认</Button>
          </div>
        </div>
      </Modal>

      <Modal open={templateModalOpen} onCancel={() => { setTemplateModalOpen(false); setEditingTemplate(null); setTemplateForm({ id: '', name: '', content: {} }) }} title="提单模板" footer={null} width={760}>
        {editingTemplate === null ? (
          <div className="space-y-3">
            <div className="flex justify-end"><Button type="primary" onClick={() => openTemplateEditor()}>新建模板</Button></div>
            <div className="space-y-2 max-h-[55vh] overflow-y-auto">
              {templates.length === 0 && <p className="text-sm text-muted-foreground">暂无模板，点击「新建模板」创建预填工单字段的快捷入口。</p>}
              {templates.map(t => (
                <div key={t.id} className="flex items-center justify-between border rounded-md px-3 py-2">
                  <div className="min-w-0">
                    <div className="text-sm font-medium truncate">{t.name}</div>
                    <div className="text-xs text-muted-foreground truncate">{[t.category, t.form_scope, t.process_id].filter(Boolean).join(' · ')}</div>
                  </div>
                  <div className="flex gap-2 shrink-0">
                    <Button size="small" onClick={() => openTemplateEditor(t)}>编辑</Button>
                    <Button size="small" danger onClick={() => deleteTemplate(t.id)}>删除</Button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className="space-y-3 max-h-[60vh] overflow-y-auto">
            <div className="grid grid-cols-2 gap-3">
              <div><label className="text-sm font-medium">模板名称</label><Input className="mt-1" value={templateForm.name} onChange={e => setTemplateForm(f => ({ ...f, name: e.target.value }))} placeholder="如：数据库故障应急" /></div>
              <div><label className="text-sm font-medium">分类</label><Input className="mt-1" value={templateForm.category} onChange={e => setTemplateForm(f => ({ ...f, category: e.target.value }))} placeholder="变更/事件/问题/请求" /></div>
              <div><label className="text-sm font-medium">表单作用域</label><Input className="mt-1" value={templateForm.form_scope} onChange={e => setTemplateForm(f => ({ ...f, form_scope: e.target.value }))} placeholder="ticket:default" /></div>
              <div><label className="text-sm font-medium">关联流程 ID</label><Input className="mt-1" value={templateForm.process_id} onChange={e => setTemplateForm(f => ({ ...f, process_id: e.target.value }))} placeholder="可选" /></div>
            </div>
            <div><label className="text-sm font-medium">说明</label><Input.TextArea rows={2} className="mt-1" value={templateForm.remark} onChange={e => setTemplateForm(f => ({ ...f, remark: e.target.value }))} /></div>
            <div className="border-t pt-3">
              <p className="text-sm font-medium mb-2">预填内容</p>
              <div className="grid grid-cols-2 gap-3">
                <div><label className="text-sm font-medium">标题</label><Input className="mt-1" value={templateForm.content.title || ''} onChange={e => setTemplateForm(f => ({ ...f, content: { ...f.content, title: e.target.value } }))} /></div>
                <div><label className="text-sm font-medium">风险级别</label>
                  <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={templateForm.content.risk_level || ''} onChange={e => setTemplateForm(f => ({ ...f, content: { ...f.content, risk_level: e.target.value } }))}>
                    <option value="">不预设</option>
                    <option value="low">低</option>
                    <option value="medium">中</option>
                    <option value="high">高</option>
                    <option value="critical">紧急</option>
                  </select>
                </div>
                <div className="col-span-2"><label className="text-sm font-medium">描述</label><Input.TextArea rows={2} className="mt-1" value={templateForm.content.description || ''} onChange={e => setTemplateForm(f => ({ ...f, content: { ...f.content, description: e.target.value } }))} /></div>
                <div className="col-span-2"><label className="text-sm font-medium">变更计划</label><Input.TextArea rows={2} className="mt-1" value={templateForm.content.change_plan || ''} onChange={e => setTemplateForm(f => ({ ...f, content: { ...f.content, change_plan: e.target.value } }))} /></div>
                <div className="col-span-2"><label className="text-sm font-medium">回滚计划</label><Input.TextArea rows={2} className="mt-1" value={templateForm.content.rollback_plan || ''} onChange={e => setTemplateForm(f => ({ ...f, content: { ...f.content, rollback_plan: e.target.value } }))} /></div>
                <div className="col-span-2"><label className="text-sm font-medium">关联资产 CI（逗号分隔）</label><Input className="mt-1" value={(templateForm.content.ci_ids || []).join(',')} onChange={e => setTemplateForm(f => ({ ...f, content: { ...f.content, ci_ids: e.target.value.split(',').map(s => s.trim()).filter(Boolean) } }))} placeholder="CI ID 列表" /></div>
                <div className="col-span-2"><label className="text-sm font-medium">自定义字段（JSON）</label><Input.TextArea rows={4} className="mt-1 font-mono text-xs" value={templateCustomFieldsJSON} onChange={e => setTemplateCustomFieldsJSON(e.target.value)} placeholder='{"env":"prod"}' /></div>
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <Button onClick={() => { setEditingTemplate(null); setTemplateForm({ id: '', name: '', content: {} }) }}>返回</Button>
              <Button type="primary" loading={templateSaving} onClick={handleSaveTemplate}>保存</Button>
            </div>
          </div>
        )}
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
