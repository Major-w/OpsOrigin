
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import type { Process, ProcessVersion, ProcessInstance, FormSchema, FormFieldOption } from './types'
import { getMessage } from '@/lib/message'

const FORM_OPTIONS_TTL_MS = 30_000
const formOptionsCache = new Map<string, { options: FormFieldOption[]; expireAt: number }>()

interface ProcessState {
  processes: Process[]
  loading: boolean
  error: string | null
  fetchProcesses: () => Promise<void>
  getProcess: (id: string) => Promise<Process | null>
  createProcess: (data: Partial<Process>) => Promise<string | null>
  updateProcess: (id: string, data: Partial<Process>) => Promise<boolean>
  deleteProcess: (id: string) => Promise<boolean>
  listProcessVersions: (id: string) => Promise<ProcessVersion[]>
  getProcessVersion: (id: string, version: number) => Promise<ProcessVersion | null>
  getTicketProcess: (ticketId: string) => Promise<ProcessInstance | null>
  getFormSchema: (scope: string) => Promise<FormSchema | null>
  getFormScopes: () => Promise<string[]>
  upsertFormSchema: (scope: string, schema: FormSchema) => Promise<boolean>
  getFormOptions: (source: string, keyword?: string, parent?: string) => Promise<FormFieldOption[]>
  getFormOptionSources: () => Promise<string[]>
  updateTicketCustomFields: (ticketId: string, scope: string, fields: Record<string, any>) => Promise<boolean>
}

export const useProcessStore = create<ProcessState>((set, get) => ({
  processes: [],
  loading: false,
  error: null,
  fetchProcesses: async () => {
    set({ loading: true })
    try {
      const processes = await apiList<Process>('/itsm/processes')
      set({ processes, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载流程列表失败: ${e.message}`)
    }
  },
  getProcess: async (id) => {
    try {
      return await apiFetch<Process>(`/itsm/processes/${id}`)
    } catch (e: any) {
      getMessage().error(`加载流程详情失败: ${e.message}`)
      return null
    }
  },
  createProcess: async (data) => {
    try {
      const result = await apiFetch<{ id: string }>('/itsm/processes', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('创建流程成功')
      await get().fetchProcesses()
      return result.id
    } catch (e: any) {
      getMessage().error(`创建流程失败: ${e.message}`)
      return null
    }
  },
  updateProcess: async (id, data) => {
    try {
      await apiFetch(`/itsm/processes/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      getMessage().success('更新流程成功')
      await get().fetchProcesses()
      return true
    } catch (e: any) {
      getMessage().error(`更新流程失败: ${e.message}`)
      return false
    }
  },
  deleteProcess: async (id) => {
    try {
      await apiFetch(`/itsm/processes/${id}`, { method: 'DELETE' })
      getMessage().success('删除流程成功')
      await get().fetchProcesses()
      return true
    } catch (e: any) {
      getMessage().error(`删除流程失败: ${e.message}`)
      return false
    }
  },
  listProcessVersions: async (id) => {
    try {
      return await apiList<ProcessVersion>(`/itsm/processes/${id}/versions`)
    } catch (e: any) {
      getMessage().error(`加载版本历史失败: ${e.message}`)
      return []
    }
  },
  getProcessVersion: async (id, version) => {
    try {
      return await apiFetch<ProcessVersion>(`/itsm/processes/${id}/versions/${version}`)
    } catch (e: any) {
      getMessage().error(`加载版本详情失败: ${e.message}`)
      return null
    }
  },
  getTicketProcess: async (ticketId) => {
    try {
      return await apiFetch<ProcessInstance>(`/itsm/tickets/${ticketId}/process`)
    } catch (e: any) {
      return null
    }
  },
  getFormSchema: async (scope) => {
    try {
      return await apiFetch<FormSchema>(`/itsm/forms/${scope}`)
    } catch {
      return null
    }
  },
  getFormScopes: async () => {
    try {
      return await apiList<string>('/itsm/forms')
    } catch {
      return []
    }
  },
  upsertFormSchema: async (scope, schema) => {
    try {
      await apiFetch(`/itsm/forms/${scope}`, { method: 'PUT', body: JSON.stringify(schema) })
      getMessage().success('表单 schema 已保存')
      return true
    } catch (e: any) {
      getMessage().error(`保存表单失败: ${e.message}`)
      return false
    }
  },
  getFormOptions: async (source, keyword, parent) => {
    const cacheKey = `${source}:${keyword || ''}:${parent || ''}`
    const cached = formOptionsCache.get(cacheKey)
    if (cached && cached.expireAt > Date.now()) return cached.options
    try {
      const params = new URLSearchParams()
      if (keyword) params.set('keyword', keyword)
      if (parent) params.set('parent', parent)
      const qs = params.toString()
      const options = await apiList<FormFieldOption>(`/itsm/forms/options/${source}${qs ? `?${qs}` : ''}`)
      formOptionsCache.set(cacheKey, { options, expireAt: Date.now() + FORM_OPTIONS_TTL_MS })
      return options
    } catch {
      return []
    }
  },
  getFormOptionSources: async () => {
    try {
      return await apiList<string>('/itsm/forms/options')
    } catch {
      return []
    }
  },
  updateTicketCustomFields: async (ticketId, scope, fields) => {
    try {
      await apiFetch(`/itsm/tickets/${ticketId}/custom-fields`, { method: 'PUT', body: JSON.stringify({ scope, fields }) })
      return true
    } catch (e: any) {
      getMessage().error(`更新自定义字段失败: ${e.message}`)
      return false
    }
  },
}))
