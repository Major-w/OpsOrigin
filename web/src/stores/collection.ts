
import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import {
  DEFAULT_PLATFORM_POLICY,
  type CollectionTask,
  type CreateCollectionTaskRequest,
  type OnboardResourceRequest,
  type PlatformPolicy,
} from '@/lib/collection'
import { getMessage } from '@/lib/message'

interface CollectionState {
  policy: PlatformPolicy
  policyLoaded: boolean
  tasks: CollectionTask[]
  loading: boolean
  saving: boolean

  /** 拉取平台默认策略；force=false 时若已加载则复用缓存（供纳管表单预填）。 */
  fetchPolicy: (force?: boolean) => Promise<PlatformPolicy>
  savePolicy: (p: PlatformPolicy) => Promise<boolean>

  fetchTasks: () => Promise<void>
  createTask: (req: CreateCollectionTaskRequest) => Promise<boolean>
  updateTask: (id: string, patch: { enabled?: boolean; frequency?: string }) => Promise<boolean>
  deleteTask: (id: string) => Promise<boolean>
  runTask: (id: string) => Promise<boolean>

  onboardResource: (req: OnboardResourceRequest) => Promise<boolean>
}

export const useCollectionStore = create<CollectionState>((set, get) => ({
  policy: DEFAULT_PLATFORM_POLICY,
  policyLoaded: false,
  tasks: [],
  loading: false,
  saving: false,

  fetchPolicy: async (force = false) => {
    if (!force && get().policyLoaded) return get().policy
    try {
      const p = await apiFetch<PlatformPolicy>('/cmdb/collection-policy')
      const policy = { ...DEFAULT_PLATFORM_POLICY, ...(p || {}) }
      set({ policy, policyLoaded: true })
      return policy
    } catch (e: any) {
      // 策略读取失败不阻塞纳管流程，回落内置默认值
      set({ policyLoaded: true })
      getMessage().error(`加载采集策略失败: ${e.message}`)
      return get().policy
    }
  },

  savePolicy: async (p) => {
    set({ saving: true })
    try {
      const saved = await apiFetch<PlatformPolicy>('/cmdb/collection-policy', {
        method: 'PUT',
        body: JSON.stringify(p),
      })
      set({ policy: { ...DEFAULT_PLATFORM_POLICY, ...(saved || p) }, policyLoaded: true, saving: false })
      getMessage().success('采集策略已保存')
      return true
    } catch (e: any) {
      set({ saving: false })
      getMessage().error(`保存采集策略失败: ${e.message}`)
      return false
    }
  },

  fetchTasks: async () => {
    set({ loading: true })
    try {
      const res = await apiFetch<{ tasks: CollectionTask[] }>('/cmdb/collection-tasks')
      set({ tasks: res?.tasks || [], loading: false })
    } catch (e: any) {
      set({ tasks: [], loading: false })
      getMessage().error(`加载采集任务失败: ${e.message}`)
    }
  },

  createTask: async (req) => {
    try {
      await apiFetch('/cmdb/collection-tasks', { method: 'POST', body: JSON.stringify(req) })
      getMessage().success('采集任务已创建')
      await get().fetchTasks()
      return true
    } catch (e: any) {
      getMessage().error(`创建采集任务失败: ${e.message}`)
      return false
    }
  },

  updateTask: async (id, patch) => {
    // 乐观更新，失败时回滚
    const prev = get().tasks
    set({ tasks: prev.map(t => (t.id === id ? { ...t, ...patch } : t)) })
    try {
      const updated = await apiFetch<CollectionTask>(`/cmdb/collection-tasks/${id}`, {
        method: 'PUT',
        body: JSON.stringify(patch),
      })
      set(s => ({ tasks: s.tasks.map(t => (t.id === id ? { ...t, ...updated } : t)) }))
      return true
    } catch (e: any) {
      set({ tasks: prev })
      getMessage().error(`更新采集任务失败: ${e.message}`)
      return false
    }
  },

  deleteTask: async (id) => {
    try {
      await apiFetch(`/cmdb/collection-tasks/${id}`, { method: 'DELETE' })
      set(s => ({ tasks: s.tasks.filter(t => t.id !== id) }))
      getMessage().success('采集任务已删除')
      return true
    } catch (e: any) {
      getMessage().error(`删除采集任务失败: ${e.message}`)
      return false
    }
  },

  runTask: async (id) => {
    set(s => ({ tasks: s.tasks.map(t => (t.id === id ? { ...t, status: 'running' } : t)) }))
    try {
      const res = await apiFetch<{ status: string; cis: number; metrics: number }>(
        `/cmdb/collection-tasks/${id}/run`,
        { method: 'POST' }
      )
      getMessage().success(`采集完成：CI ${res?.cis ?? 0} 条，指标 ${res?.metrics ?? 0} 条`)
      await get().fetchTasks()
      return true
    } catch (e: any) {
      getMessage().error(`采集失败: ${e.message}`)
      await get().fetchTasks()
      return false
    }
  },

  onboardResource: async (req) => {
    try {
      await apiFetch('/cmdb/onboard', { method: 'POST', body: JSON.stringify(req) })
      return true
    } catch (e: any) {
      getMessage().error(`纳管失败: ${e.message}`)
      return false
    }
  },
}))
