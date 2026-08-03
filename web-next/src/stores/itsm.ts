'use client'

import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'

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
}

type StepType = 'start' | 'end' | 'user' | 'service' | 'gateway' | 'timer' | 'notify' | 'parallel' | 'subflow'

export interface Process {
  id: string; name: string; description?: string; category?: string
  steps: ProcessStep[]; labels?: Record<string,string>
  enabled: boolean; version: number
  created_at: string; updated_at: string
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
  approve: (id: string, comment?: string) => Promise<boolean>
  reject: (id: string, reason: string) => Promise<boolean>
  execute: (id: string) => Promise<boolean>
  complete: (id: string, comment?: string) => Promise<boolean>
  rollback: (id: string, reason?: string) => Promise<boolean>
  fetchStats: () => Promise<void>
  // process
  fetchProcesses: () => Promise<void>
  getProcess: (id: string) => Promise<Process | null>
  createProcess: (data: Partial<Process>) => Promise<string | null>
  updateProcess: (id: string, data: Partial<Process>) => Promise<boolean>
  deleteProcess: (id: string) => Promise<boolean>
}

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
  complete: async (id, comment?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/complete`, { method: 'POST', body: JSON.stringify({ completed_by: 'admin', comment: comment || '' }) })
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
}))
