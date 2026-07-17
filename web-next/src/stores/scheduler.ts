'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface ScheduledTask { id: string; name: string; cron_expr: string; task_type: string; target_id: string; agent_ids?: string[]; enabled: boolean; created_at: string; last_run_at?: string }

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
      const tasks = await apiFetch<ScheduledTask[]>('/schedules')
      set({ tasks, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载定时任务失败: ${e.message}`)
    }
  },
  create: async (data) => {
    try {
      await apiFetch('/schedules', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建定时任务成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`创建定时任务失败: ${e.message}`)
      return false
    }
  },
  update: async (id, data) => {
    try {
      await apiFetch(`/schedules/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      toast.success('更新定时任务成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`更新定时任务失败: ${e.message}`)
      return false
    }
  },
  remove: async (id) => {
    try {
      await apiFetch(`/schedules/${id}`, { method: 'DELETE' })
      set(s => ({ tasks: s.tasks.filter(t => t.id !== id) }))
      toast.success('删除定时任务成功')
      return true
    } catch (e: any) {
      toast.error(`删除定时任务失败: ${e.message}`)
      return false
    }
  },
}))
