'use client'

import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import type { FormSchema, FormFieldOption } from '@/components/itsm/DynamicForm'

export interface Ticket {
  id: string
  title: string
  description?: string
  risk_level: string
  status: string
  change_plan?: string
  rollback_plan?: string
  created_by: string
  created_by_type: string
  approved_by?: string
  reject_reason?: string
  completed_by?: string
  rolled_back_by?: string
  rollback_reason?: string
  ci_ids?: string[]
  process_id?: string
  created_at: string
  updated_at: string
  approved_at?: string
  executed_at?: string
  completed_at?: string
  rolled_back_at?: string
  scheduled_at?: string
  deadline_at?: string
  labels?: Record<string, string>
  webhook_url?: string
  // 可配置表单采集的业务自定义字段（由 form.FormSchema 驱动，无 schema 时为空）
  custom_fields?: Record<string, any>
  // 本工单套用的表单作用域（默认 ticket:default）；用于目录/流程按场景套不同字段
  form_scope?: string
  // 负责人与指派时间（闭环：指派端点）
  assignee?: string
  assigned_at?: string
  // 关单满意度评分（1-5，close 端点写入）
  satisfaction?: number
}

export interface TicketEvent {
  id: number
  ticket_id: string
  from_status: string
  to_status: string
  changed_by: string
  comment?: string
  created_at: string
}

export interface ITSMStats {
  total: number
  by_status: Record<string, number>
  by_risk: Record<string, number>
  approval_rate: number
  avg_approval_ms: number
  avg_execution_ms: number
}

export interface ProcessStep {
  id: string; name: string; type: StepType
  description?: string
  approvers?: string[]; approval_msg?: string
  script_id?: string; inputs?: Record<string,any>
  condition?: string
  duration?: string; deadline?: string
  notify_channel?: string; notify_title?: string; notify_body?: string
  subflow_id?: string
  depends_on?: { nodeId: string; condition: string }[]
  position_x?: number; position_y?: number
  // 表单作用域：工单流转到本步骤时渲染的自定义表单 scope（如 process:<pid>:step:<step_id>）
  form_scope?: string
}

type StepType = 'start' | 'end' | 'user' | 'service' | 'gateway' | 'timer' | 'notify' | 'parallel' | 'subflow'

export interface Process {
  id: string; name: string; description?: string; category?: string
  steps: ProcessStep[]; labels?: Record<string,string>
  enabled: boolean; version: number
  created_at: string; updated_at: string
}

export interface ProcessInstance {
  id: string
  process_id: string
  ticket_id: string
  current_step: string
  status: string
  step_results?: Record<string, any>
  started_at?: string
  finished_at?: string | null
  created_at?: string
  updated_at?: string
}

interface ITSMState {
  tickets: Ticket[]
  stats: ITSMStats | null
  processes: Process[]
  loading: boolean
  error: string | null
  fetch: () => Promise<void>
  getTicket: (id: string) => Promise<Ticket>
  getEvents: (id: string) => Promise<TicketEvent[]>
  create: (data: Partial<Ticket>) => Promise<boolean>
  // 可配置表单 schema
  getFormSchema: (scope: string) => Promise<FormSchema | null>
  getFormScopes: () => Promise<string[]>
  upsertFormSchema: (scope: string, schema: FormSchema) => Promise<boolean>
  // 动态选项来源（cmdb:<type> / iam:user|role|group）
  getFormOptions: (source: string, keyword?: string, parent?: string) => Promise<FormFieldOption[]>
  getFormOptionSources: () => Promise<string[]>
  approve: (id: string, comment?: string) => Promise<boolean>
  reject: (id: string, reason: string) => Promise<boolean>
  execute: (id: string) => Promise<boolean>
  complete: (id: string, comment?: string, assessment?: 'confirmed' | 'false_positive') => Promise<boolean>
  rollback: (id: string, reason?: string) => Promise<boolean>
  submit: (id: string) => Promise<boolean>
  assign: (id: string, assignee: string) => Promise<boolean>
  close: (id: string, comment?: string, satisfaction?: number) => Promise<boolean>
  fetchStats: () => Promise<void>
  // process
  fetchProcesses: () => Promise<void>
  getProcess: (id: string) => Promise<Process | null>
  createProcess: (data: Partial<Process>) => Promise<string | null>
  updateProcess: (id: string, data: Partial<Process>) => Promise<boolean>
  deleteProcess: (id: string) => Promise<boolean>
  // 流程实例与步骤级自定义字段
  getTicketProcess: (ticketId: string) => Promise<ProcessInstance | null>
  updateTicketCustomFields: (ticketId: string, scope: string, fields: Record<string, any>) => Promise<boolean>
}

// 动态选项进程内缓存：同一 source 在 TTL 内复用，避免每个字段挂载都打一次 CMDB/IAM。
// 后端 resolver 侧另有一层 TTL 缓存，这里主要省掉网络往返。
const FORM_OPTIONS_TTL_MS = 30_000
const formOptionsCache = new Map<string, { options: FormFieldOption[]; expireAt: number }>()

export const useITSMStore = create<ITSMState>((set, get) => ({
  tickets: [],
  stats: null,
  processes: [],
  loading: false,
  error: null,
  fetch: async () => {
    set({ loading: true })
    try {
      const tickets = await apiList<Ticket>('/itsm/tickets')
      set({ tickets, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载工单列表失败: ${e.message}`)
    }
  },
  getTicket: async (id) => {
    try {
      return await apiFetch<Ticket>(`/itsm/tickets/${id}`)
    } catch (e: any) {
      toast.error(`加载工单详情失败: ${e.message}`)
      throw e
    }
  },
  getEvents: async (id) => {
    try {
      return await apiList<TicketEvent>(`/itsm/tickets/${id}/events`)
    } catch (e: any) {
      toast.error(`加载工单事件失败: ${e.message}`)
      throw e
    }
  },
  create: async (data) => {
    try {
      await apiFetch('/itsm/tickets', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建工单成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`创建工单失败: ${e.message}`)
      return false
    }
  },
  approve: async (id, comment?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/approve`, { method: 'POST', body: JSON.stringify({ approved_by: 'admin', comment: comment || '' }) })
      toast.success('审批成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`审批失败: ${e.message}`)
      return false
    }
  },
  reject: async (id, reason) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/reject`, { method: 'POST', body: JSON.stringify({ rejected_by: 'admin', reason }) })
      toast.success('驳回成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`驳回失败: ${e.message}`)
      return false
    }
  },
  execute: async (id) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/execute`, { method: 'POST', body: JSON.stringify({ executed_by: 'admin' }) })
      toast.success('执行成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`执行失败: ${e.message}`)
      return false
    }
  },
  complete: async (id, comment?, assessment?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/complete`, { method: 'POST', body: JSON.stringify({ completed_by: 'admin', comment: comment || '', assessment: assessment || '' }) })
      toast.success('完成成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`完成失败: ${e.message}`)
      return false
    }
  },
  rollback: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/rollback`, { method: 'POST', body: JSON.stringify({ rolled_back_by: 'admin', rollback_reason: reason || '' }) })
      toast.success('回滚成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`回滚失败: ${e.message}`)
      return false
    }
  },
  submit: async (id) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/submit`, { method: 'POST', body: JSON.stringify({ submitted_by: 'admin' }) })
      toast.success('提交成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`提交失败: ${e.message}`)
      return false
    }
  },
  assign: async (id, assignee) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/assign`, { method: 'POST', body: JSON.stringify({ assignee, assigned_by: 'admin' }) })
      toast.success('指派成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`指派失败: ${e.message}`)
      return false
    }
  },
  close: async (id, comment?, satisfaction?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/close`, { method: 'POST', body: JSON.stringify({ closed_by: 'admin', comment: comment || '', satisfaction: satisfaction || 0 }) })
      toast.success('关闭成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`关闭失败: ${e.message}`)
      return false
    }
  },
  fetchStats: async () => {
    try {
      const stats = await apiFetch<ITSMStats>('/itsm/stats')
      set({ stats })
    } catch (e: any) {
      toast.error(`加载工单统计失败: ${e.message}`)
    }
  },
  fetchProcesses: async () => {
    try {
      const processes = await apiList<Process>('/itsm/processes')
      set({ processes })
    } catch (e: any) {
      toast.error(`加载流程列表失败: ${e.message}`)
    }
  },
  getFormSchema: async (scope) => {
    try {
      const data = await apiFetch<{ scope: string; schema: FormSchema | null }>(`/itsm/forms?scope=${encodeURIComponent(scope)}`)
      return data.schema || null
    } catch {
      return null
    }
  },
  getFormScopes: async () => {
    try {
      const data = await apiFetch<{ scopes: string[] }>('/itsm/forms')
      return data.scopes || []
    } catch {
      return []
    }
  },
  upsertFormSchema: async (scope, schema) => {
    try {
      await apiFetch('/itsm/forms', {
        method: 'PUT',
        body: JSON.stringify({ scope, schema: { ...schema, scope } }),
      })
      toast.success('表单配置已保存')
      return true
    } catch (e: any) {
      toast.error(`保存表单配置失败: ${e.message}`)
      return false
    }
  },
  getFormOptions: async (source, keyword, parent) => {
    // 命中进程内缓存则直接返回，避免同一张表单多个字段重复打后端
    // parent 为 undefined = 平铺模式；为字符串（含空串）= 级联层级模式，空串取根层级
    const cacheKey = `${source}|${keyword || ''}|${parent === undefined ? '' : `p:${parent}`}`
    const cached = formOptionsCache.get(cacheKey)
    if (cached && Date.now() < cached.expireAt) return cached.options
    const qs = new URLSearchParams({ source })
    if (keyword) qs.set('keyword', keyword)
    if (parent !== undefined) qs.set('parent', parent)
    const data = await apiFetch<{ source: string; options: FormFieldOption[] }>(`/itsm/form-options?${qs.toString()}`)
    const options = data.options || []
    formOptionsCache.set(cacheKey, { options, expireAt: Date.now() + FORM_OPTIONS_TTL_MS })
    return options
  },
  getFormOptionSources: async () => {
    try {
      const data = await apiFetch<{ sources: string[] }>('/itsm/form-options')
      return data.sources || []
    } catch {
      return []
    }
  },
  getProcess: async (id) => {
    try {
      return await apiFetch<Process>(`/itsm/processes/${id}`)
    } catch { return null }
  },
  createProcess: async (data) => {
    try {
      const p = await apiFetch<Process>('/itsm/processes', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建流程成功')
      await get().fetchProcesses()
      return p.id
    } catch (e: any) { toast.error(`创建失败: ${e.message}`); return null }
  },
  updateProcess: async (id, data) => {
    try {
      await apiFetch(`/itsm/processes/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      toast.success('保存流程成功')
      await get().fetchProcesses()
      return true
    } catch (e: any) { toast.error(`保存失败: ${e.message}`); return false }
  },
  deleteProcess: async (id) => {
    try {
      await apiFetch(`/itsm/processes/${id}`, { method: 'DELETE' })
      set(s => ({ processes: s.processes.filter(p => p.id !== id) }))
      toast.success('删除流程成功')
      return true
    } catch (e: any) { toast.error(`删除失败: ${e.message}`); return false }
  },
  getTicketProcess: async (ticketId) => {
    try {
      return await apiFetch<ProcessInstance>(`/itsm/tickets/${ticketId}/process`)
    } catch { return null }
  },
  updateTicketCustomFields: async (ticketId, scope, fields) => {
    try {
      await apiFetch(`/itsm/tickets/${ticketId}/custom-fields`, {
        method: 'PATCH',
        body: JSON.stringify({ scope, fields }),
      })
      toast.success('步骤表单已保存')
      return true
    } catch (e: any) { toast.error(`保存失败: ${e.message}`); return false }
  },
}))
