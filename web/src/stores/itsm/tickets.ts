
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import type { Ticket, TicketEvent, ITSMStats, TicketTemplate, AutomationRule, TicketButton } from './types'
import { getMessage } from '@/lib/message'

const getCurrentUser = () => useAuthStore.getState().user?.username || 'anonymous'

interface TicketState {
  tickets: Ticket[]
  stats: ITSMStats | null
  loading: boolean
  error: string | null
  fetch: () => Promise<void>
  getTicket: (id: string) => Promise<Ticket>
  getEvents: (id: string) => Promise<TicketEvent[]>
  create: (data: Partial<Ticket>) => Promise<string | null>
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
  relate: (id: string, relatedId: string, relation?: string, reason?: string) => Promise<boolean>
  addApprover: (id: string, approver: string, position?: 'before' | 'after', comment?: string) => Promise<boolean>
  fetchStats: () => Promise<void>
  // 提单模板
  templates: TicketTemplate[]
  fetchTemplates: () => Promise<void>
  saveTemplate: (t: TicketTemplate) => Promise<boolean>
  deleteTemplate: (id: string) => Promise<boolean>
  // 自动化: 触发器 + 自定义按钮
  rules: AutomationRule[]
  fetchRules: () => Promise<void>
  saveRule: (r: AutomationRule) => Promise<boolean>
  deleteRule: (id: string) => Promise<boolean>
  buttons: TicketButton[]
  fetchButtons: () => Promise<void>
  saveButton: (b: TicketButton) => Promise<boolean>
  deleteButton: (id: string) => Promise<boolean>
}

export const useTicketStore = create<TicketState>((set, get) => ({
  tickets: [],
  stats: null,
  loading: false,
  error: null,
  fetch: async () => {
    set({ loading: true })
    try {
      const tickets = await apiList<Ticket>('/itsm/tickets?page_size=200')
      set({ tickets, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载工单列表失败: ${e.message}`)
    }
  },
  getTicket: async (id) => {
    try {
      return await apiFetch<Ticket>(`/itsm/tickets/${id}`)
    } catch (e: any) {
      getMessage().error(`加载工单详情失败: ${e.message}`)
      throw e
    }
  },
  getEvents: async (id) => {
    try {
      return await apiList<TicketEvent>(`/itsm/tickets/${id}/events`)
    } catch (e: any) {
      getMessage().error(`加载工单事件失败: ${e.message}`)
      throw e
    }
  },
  create: async (data) => {
    try {
      const created = await apiFetch<Ticket>('/itsm/tickets', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('创建工单成功')
      await get().fetch()
      return created?.id || null
    } catch (e: any) {
      getMessage().error(`创建工单失败: ${e.message}`)
      return null
    }
  },
  approve: async (id, comment?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/approve`, { method: 'POST', body: JSON.stringify({ approved_by: getCurrentUser(), comment: comment || '' }) })
      getMessage().success('审批成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`审批失败: ${e.message}`)
      return false
    }
  },
  reject: async (id, reason) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/reject`, { method: 'POST', body: JSON.stringify({ rejected_by: getCurrentUser(), reason }) })
      getMessage().success('驳回成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`驳回失败: ${e.message}`)
      return false
    }
  },
  execute: async (id) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/execute`, { method: 'POST', body: JSON.stringify({ executed_by: getCurrentUser() }) })
      getMessage().success('执行成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`执行失败: ${e.message}`)
      return false
    }
  },
  complete: async (id, comment?, assessment?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/complete`, { method: 'POST', body: JSON.stringify({ completed_by: getCurrentUser(), comment: comment || '', assessment: assessment || '' }) })
      getMessage().success('完成成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`完成失败: ${e.message}`)
      return false
    }
  },
  rollback: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/rollback`, { method: 'POST', body: JSON.stringify({ rolled_back_by: getCurrentUser(), rollback_reason: reason || '' }) })
      getMessage().success('回滚成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`回滚失败: ${e.message}`)
      return false
    }
  },
  submit: async (id) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/submit`, { method: 'POST', body: JSON.stringify({ submitted_by: getCurrentUser() }) })
      getMessage().success('提交成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`提交失败: ${e.message}`)
      return false
    }
  },
  withdraw: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/withdraw`, { method: 'POST', body: JSON.stringify({ withdrawn_by: getCurrentUser(), reason: reason || '' }) })
      getMessage().success('已撤回')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`撤回失败: ${e.message}`)
      return false
    }
  },
  reopen: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/reopen`, { method: 'POST', body: JSON.stringify({ reopened_by: getCurrentUser(), reason: reason || '' }) })
      getMessage().success('已重开')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`重开失败: ${e.message}`)
      return false
    }
  },
  resubmit: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/resubmit`, { method: 'POST', body: JSON.stringify({ resubmitted_by: getCurrentUser(), reason: reason || '' }) })
      getMessage().success('已重新提交')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`重新提交失败: ${e.message}`)
      return false
    }
  },
  hold: async (id, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/hold`, { method: 'POST', body: JSON.stringify({ hold_by: getCurrentUser(), reason: reason || '' }) })
      getMessage().success('已挂起（SLA 计时暂停）')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`挂起失败: ${e.message}`)
      return false
    }
  },
  resume: async (id) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/resume`, { method: 'POST', body: JSON.stringify({ resumed_by: getCurrentUser() }) })
      getMessage().success('已恢复')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`恢复失败: ${e.message}`)
      return false
    }
  },
  remind: async (id, msg?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/remind`, { method: 'POST', body: JSON.stringify({ reminded_by: getCurrentUser(), message: msg || '' }) })
      getMessage().success('已催办')
      return true
    } catch (e: any) {
      getMessage().error(`催办失败: ${e.message}`)
      return false
    }
  },
  relate: async (id, relatedId, relation?, reason?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/relate`, { method: 'POST', body: JSON.stringify({ related_id: relatedId, relation: relation || '', reason: reason || '' }) })
      getMessage().success('已关联')
      return true
    } catch (e: any) {
      getMessage().error(`关联失败: ${e.message}`)
      return false
    }
  },
  addApprover: async (id, approver, position?, comment?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/add-approver`, { method: 'POST', body: JSON.stringify({ approver, position: position || 'after', comment: comment || '' }) })
      getMessage().success('加签成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`加签失败: ${e.message}`)
      return false
    }
  },
  assign: async (id, assignee) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/assign`, { method: 'POST', body: JSON.stringify({ assignee, assigned_by: getCurrentUser() }) })
      getMessage().success('指派成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`指派失败: ${e.message}`)
      return false
    }
  },
  close: async (id, comment?, satisfaction?) => {
    try {
      await apiFetch(`/itsm/tickets/${id}/close`, { method: 'POST', body: JSON.stringify({ closed_by: getCurrentUser(), comment: comment || '', satisfaction: satisfaction || 0 }) })
      getMessage().success('关单成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`关单失败: ${e.message}`)
      return false
    }
  },
  fetchStats: async () => {
    try {
      const stats = await apiFetch<ITSMStats>('/itsm/stats')
      set({ stats })
    } catch (e: any) {
      getMessage().error(`加载统计失败: ${e.message}`)
    }
  },
  templates: [],
  fetchTemplates: async () => {
    try {
      const list = await apiList<TicketTemplate>('/itsm/ticket-templates')
      set({ templates: list || [] })
    } catch (e: any) {
      getMessage().error(`加载提单模板失败: ${e.message}`)
    }
  },
  saveTemplate: async (t) => {
    try {
      if (t.id) {
        await apiFetch(`/itsm/ticket-templates/${t.id}`, { method: 'PUT', body: JSON.stringify(t) })
      } else {
        await apiFetch('/itsm/ticket-templates', { method: 'POST', body: JSON.stringify(t) })
      }
      getMessage().success('模板已保存')
      await get().fetchTemplates()
      return true
    } catch (e: any) {
      getMessage().error(`保存模板失败: ${e.message}`)
      return false
    }
  },
  deleteTemplate: async (id) => {
    try {
      await apiFetch(`/itsm/ticket-templates/${id}`, { method: 'DELETE' })
      getMessage().success('模板已删除')
      await get().fetchTemplates()
      return true
    } catch (e: any) {
      getMessage().error(`删除模板失败: ${e.message}`)
      return false
    }
  },
  rules: [],
  fetchRules: async () => {
    try {
      const list = await apiList<AutomationRule>('/itsm/automation-rules')
      set({ rules: list || [] })
    } catch (e: any) {
      getMessage().error(`加载自动化规则失败: ${e.message}`)
    }
  },
  saveRule: async (r) => {
    try {
      if (r.id) {
        await apiFetch(`/itsm/automation-rules/${r.id}`, { method: 'PUT', body: JSON.stringify(r) })
      } else {
        await apiFetch('/itsm/automation-rules', { method: 'POST', body: JSON.stringify(r) })
      }
      getMessage().success('规则已保存')
      await get().fetchRules()
      return true
    } catch (e: any) {
      getMessage().error(`保存规则失败: ${e.message}`)
      return false
    }
  },
  deleteRule: async (id) => {
    try {
      await apiFetch(`/itsm/automation-rules/${id}`, { method: 'DELETE' })
      getMessage().success('规则已删除')
      await get().fetchRules()
      return true
    } catch (e: any) {
      getMessage().error(`删除规则失败: ${e.message}`)
      return false
    }
  },
  buttons: [],
  fetchButtons: async () => {
    try {
      const list = await apiList<TicketButton>('/itsm/ticket-buttons')
      set({ buttons: (list || []).filter(b => b.enabled) })
    } catch (e: any) {
      getMessage().error(`加载自定义按钮失败: ${e.message}`)
    }
  },
  saveButton: async (b) => {
    try {
      if (b.id) {
        await apiFetch(`/itsm/ticket-buttons/${b.id}`, { method: 'PUT', body: JSON.stringify(b) })
      } else {
        await apiFetch('/itsm/ticket-buttons', { method: 'POST', body: JSON.stringify(b) })
      }
      getMessage().success('按钮已保存')
      await get().fetchButtons()
      return true
    } catch (e: any) {
      getMessage().error(`保存按钮失败: ${e.message}`)
      return false
    }
  },
  deleteButton: async (id) => {
    try {
      await apiFetch(`/itsm/ticket-buttons/${id}`, { method: 'DELETE' })
      getMessage().success('按钮已删除')
      await get().fetchButtons()
      return true
    } catch (e: any) {
      getMessage().error(`删除按钮失败: ${e.message}`)
      return false
    }
  },
}))
