
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { message as antdMessage } from 'antd'
import type { Ticket, TicketEvent, ITSMStats } from './types'

const getCurrentUser = () => useAuthStore.getState().user?.username || 'anonymous'

interface TicketState {
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
  complete: (id: string, comment?: string, assessment?: 'confirmed' | 'false_positive') => Promise<boolean>
  rollback: (id: string, reason?: string) => Promise<boolean>
  submit: (id: string) => Promise<boolean>
  assign: (id: string, assignee: string) => Promise<boolean>
  close: (id: string, comment?: string, satisfaction?: number) => Promise<boolean>
  withdraw: (id: string, reason?: string) => Promise<boolean>
  reopen: (id: string, reason?: string) => Promise<boolean>
  resubmit: (id: string, reason?: string) => Promise<boolean>
  hold: (id: string, reason?: string) => Promise<boolean>
  resume: (id: string) => Promise<boolean>
  remind: (id: string, message?: string) => Promise<boolean>
  relate: (id: string, relatedId: string) => Promise<boolean>
  fetchStats: () => Promise<void>
}

export const useTicketStore = create<TicketState>((set, get) => ({
  tickets: [],
  stats: null,
  loading: false,
  error: null,
  fetch: async () => {
    set({ loading: true })
    try {
      const tickets = await apiList<Ticket>('/itsm/tickets')
      set({ tickets, loading: false })
    } catch (e: any) {
      set({ loading: false })
      antdMessage.error(`加载工单列表失败: ${e.message}`)
    }
  },
  getTicket: async (id) => {
    try {
      return await apiFetch<Ticket>(`/itsm/tickets/${id}`)
    } catch (e: any) {
      antdMessage.error(`加载工单详情失败: ${e.message}`)
      throw e
    }
  },
  getEvents: async (id) => {
    try {
      return await apiList<TicketEvent>(`/itsm/tickets/${id}/events`)
    } catch (e: any) {
      antdMessage.error(`加载工单事件失败: ${e.message}`)
      throw e
    }
  },
  create: async (data) => {
    try {
      await apiFetch('/itsm/tickets', { method: 'POST', body: JSON.stringify(data) })
      antdMessage.success('创建工单成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`创建工单失败: ${e.message}`)
      return false
    }
  },
  approve: async (id, comment?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/approve`, { method: 'POST', body: JSON.stringify({ approved_by: getCurrentUser(), comment: comment || '' }) })
      antdMessage.success('审批成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`审批失败: ${e.message}`)
      return false
    }
  },
  reject: async (id, reason) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/reject`, { method: 'POST', body: JSON.stringify({ rejected_by: getCurrentUser(), reason }) })
      antdMessage.success('驳回成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`驳回失败: ${e.message}`)
      return false
    }
  },
  execute: async (id) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/execute`, { method: 'POST', body: JSON.stringify({ executed_by: getCurrentUser() }) })
      antdMessage.success('执行成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`执行失败: ${e.message}`)
      return false
    }
  },
  complete: async (id, comment?, assessment?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/complete`, { method: 'POST', body: JSON.stringify({ completed_by: getCurrentUser(), comment: comment || '', assessment: assessment || '' }) })
      antdMessage.success('完成成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`完成失败: ${e.message}`)
      return false
    }
  },
  rollback: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/rollback`, { method: 'POST', body: JSON.stringify({ rolled_back_by: getCurrentUser(), rollback_reason: reason || '' }) })
      antdMessage.success('回滚成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`回滚失败: ${e.message}`)
      return false
    }
  },
  submit: async (id) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/submit`, { method: 'POST', body: JSON.stringify({ submitted_by: getCurrentUser() }) })
      antdMessage.success('提交成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`提交失败: ${e.message}`)
      return false
    }
  },
  withdraw: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/withdraw`, { method: 'POST', body: JSON.stringify({ withdrawn_by: getCurrentUser(), reason: reason || '' }) })
      antdMessage.success('已撤回')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`撤回失败: ${e.message}`)
      return false
    }
  },
  reopen: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/reopen`, { method: 'POST', body: JSON.stringify({ reopened_by: getCurrentUser(), reason: reason || '' }) })
      antdMessage.success('已重开')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`重开失败: ${e.message}`)
      return false
    }
  },
  resubmit: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/resubmit`, { method: 'POST', body: JSON.stringify({ resubmitted_by: getCurrentUser(), reason: reason || '' }) })
      antdMessage.success('已重新提交')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`重新提交失败: ${e.message}`)
      return false
    }
  },
  hold: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/hold`, { method: 'POST', body: JSON.stringify({ hold_by: getCurrentUser(), reason: reason || '' }) })
      antdMessage.success('已挂起（SLA 计时暂停）')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`挂起失败: ${e.message}`)
      return false
    }
  },
  resume: async (id) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/resume`, { method: 'POST', body: JSON.stringify({ resumed_by: getCurrentUser() }) })
      antdMessage.success('已恢复')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`恢复失败: ${e.message}`)
      return false
    }
  },
  remind: async (id, msg?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/remind`, { method: 'POST', body: JSON.stringify({ reminded_by: getCurrentUser(), message: msg || '' }) })
      antdMessage.success('已催办')
      return true
    } catch (e: any) {
      antdMessage.error(`催办失败: ${e.message}`)
      return false
    }
  },
  relate: async (id, relatedId) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/relate`, { method: 'POST', body: JSON.stringify({ related_ticket_id: relatedId }) })
      antdMessage.success('已关联')
      return true
    } catch (e: any) {
      antdMessage.error(`关联失败: ${e.message}`)
      return false
    }
  },
  assign: async (id, assignee) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/assign`, { method: 'POST', body: JSON.stringify({ assignee, assigned_by: getCurrentUser() }) })
      antdMessage.success('指派成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`指派失败: ${e.message}`)
      return false
    }
  },
  close: async (id, comment?, satisfaction?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/close`, { method: 'POST', body: JSON.stringify({ closed_by: getCurrentUser(), comment: comment || '', satisfaction: satisfaction || 0 }) })
      antdMessage.success('关单成功')
      await get().fetch()
      return true
    } catch (e: any) {
      antdMessage.error(`关单失败: ${e.message}`)
      return false
    }
  },
  fetchStats: async () => {
    try {
      const stats = await apiFetch<ITSMStats>('/itsm/stats')
      set({ stats })
    } catch (e: any) {
      antdMessage.error(`加载统计失败: ${e.message}`)
    }
  },
}))
