
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'
export interface ScheduledTask {
  id: string; name: string; cron_expr: string; task_type: string; target_id: string
  agent_ids?: string[]; enabled: boolean; created_at: string; last_run_at?: string
  max_retries?: number; retry_delay_secs?: number; retry_count?: number
  last_error?: string; alert_on_failure?: boolean
}

interface SchedulerState {
  tasks: ScheduledTask[]; loading: boolean
  fetch: () => Promise<void>
  create: (data: any) => Promise<boolean>
  update: (id: string, data: any) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
}

export const useSchedulerStore = create<SchedulerState>((set, get) => ({
  tasks: [], loading: false,
  fetch: async () => {
    set({ loading: true })
    try {
      const tasks = await apiList<ScheduledTask>('/schedules')
      set({ tasks, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载定时任务失败: ${e.message}`)
    }
  },
  create: async (data) => {
    try {
      await apiFetch('/schedules', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('创建定时任务成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`创建定时任务失败: ${e.message}`)
      return false
    }
  },
  update: async (id, data) => {
    try {
      await apiFetch(`/schedules/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      getMessage().success('更新定时任务成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`更新定时任务失败: ${e.message}`)
      return false
    }
  },
  remove: async (id) => {
    try {
      await apiFetch(`/schedules/${id}`, { method: 'DELETE' })
      set(s => ({ tasks: s.tasks.filter(t => t.id !== id) }))
      getMessage().success('删除定时任务成功')
      return true
    } catch (e: any) {
      getMessage().error(`删除定时任务失败: ${e.message}`)
      return false
    }
  },
}))
