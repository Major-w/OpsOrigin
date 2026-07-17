'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
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

interface ITSMState {
  tickets: Ticket[]
  stats: ITSMStats | null
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
}

export const useITSMStore = create<ITSMState>((set, get) => ({
  tickets: [],
  stats: null,
  loading: false,
  error: null,
  fetch: async () => {
    set({ loading: true })
    try {
      const tickets = await apiFetch<Ticket[]>('/itsm/tickets')
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
      return await apiFetch<TicketEvent[]>(`/itsm/tickets/${id}/events`)
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
}))
