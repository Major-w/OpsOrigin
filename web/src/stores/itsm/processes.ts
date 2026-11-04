
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import type { Process, ProcessVersion, ProcessInstance, FormSchema, FormFieldOption, DryRunResult } from './types'
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
  restoreProcessVersion: (id: string, version: number) => Promise<Process | null>
  dryRunProcess: (id: string, vars?: Record<string, any>) => Promise<DryRunResult | null>
  getTicketProcess: (ticketId: string) => Promise<ProcessInstance | null>
  getFormSchema: (scope: string) => Promise<FormSchema | null>
  getFormScopes: () => Promise<string[]>
  upsertFormSchema: (scope: string, schema: FormSchema) => Promise<boolean>
  getFormOptions: (source: string, keyword?: string, parent?: string, params?: Record<string, string>) => Promise<FormFieldOption[]>
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
  restoreProcessVersion: async (id, version) => {
    try {
      const p = await apiFetch<Process>(`/itsm/processes/${id}/versions/${version}/restore`, { method: 'POST' })
      getMessage().success(`已回滚到版本 v${version}（保存为新版本 v${p.version}）`)
      await get().fetchProcesses()
      return p
    } catch (e: any) {
      getMessage().error(`版本回滚失败: ${e.message}`)
      return null
    }
  },
  dryRunProcess: async (id, vars) => {
    try {
      return await apiFetch<DryRunResult>(`/itsm/processes/${id}/dry-run`, {
        method: 'POST',
        body: JSON.stringify({ vars: vars || null }),
      })
    } catch (e: any) {
      getMessage().error(`流程测试运行失败: ${e.message}`)
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
  // 后端契约：GET /itsm/forms?scope=X 返回 {scope, schema}（无配置时 schema 为 null）；
  // PUT /itsm/forms body 为 {scope, schema}。旧实现拼路径 /itsm/forms/{scope} 恒 404，
  // 导致表单 schema 永远加载不到（工单详情/创建/配置全部失效）。
  getFormSchema: async (scope) => {
    try {
      const res = await apiFetch<{ scope: string; schema: FormSchema | null }>(`/itsm/forms?scope=${encodeURIComponent(scope)}`)
      return res?.schema || null
    } catch {
      return null
    }
  },
  getFormScopes: async () => {
    try {
      const res = await apiFetch<{ scopes: string[] }>('/itsm/forms')
      return res?.scopes || []
    } catch {
      return []
    }
  },
  upsertFormSchema: async (scope, schema) => {
    try {
      await apiFetch('/itsm/forms', { method: 'PUT', body: JSON.stringify({ scope, schema: { ...schema, scope } }) })
      getMessage().success('表单 schema 已保存')
      return true
    } catch (e: any) {
      getMessage().error(`保存表单失败: ${e.message}`)
      return false
    }
  },
  getFormOptions: async (source, keyword, parent, cascadeParams) => {
    // 后端端点为 GET /itsm/form-options（query 传 source）; 端点此前误写为
    // /itsm/forms/options/<source>（后端从未注册该路由），导致动态选项一直落空。
    const cacheKey = `${source}:${keyword || ''}:${parent || ''}:${cascadeParams ? JSON.stringify(cascadeParams) : ''}`
    const cached = formOptionsCache.get(cacheKey)
    if (cached && cached.expireAt > Date.now()) return cached.options
    try {
      const qs = new URLSearchParams()
      qs.set('source', source)
      if (keyword) qs.set('keyword', keyword)
      if (parent !== undefined) qs.set('parent', parent)
      // 参数化级联: 表单当前值以 p_<key> 传给后端展开 source 里的 ${key} 占位符
      for (const [k, v] of Object.entries(cascadeParams || {})) {
        if (v !== '' && v !== undefined && v !== null) qs.set(`p_${k}`, String(v))
      }
      const options = await apiList<FormFieldOption>(`/itsm/form-options?${qs.toString()}`)
      formOptionsCache.set(cacheKey, { options, expireAt: Date.now() + FORM_OPTIONS_TTL_MS })
      return options
    } catch {
      return []
    }
  },
  getFormOptionSources: async () => {
    try {
      const res = await apiFetch<{ sources: string[] }>('/itsm/form-options')
      return res?.sources || []
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
