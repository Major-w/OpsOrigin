'use client'

import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface ConfigItem {
  key: string
  category: string
  label: string
  description: string
  type: 'string' | 'int' | 'bool' | 'duration' | 'secret'
  current_value: string
  default_value: string
  requires_restart: boolean
  sensitive: boolean
}

export interface ConfigGroup {
  category: string
  label: string
  items: ConfigItem[]
}

interface SysConfigState {
  groups: ConfigGroup[]
  loading: boolean
  saving: boolean
  fetch: () => Promise<void>
  update: (updates: Record<string, string>) => Promise<{ updated: number; requires_restart: number; message: string } | null>
}

export const useSysConfigStore = create<SysConfigState>((set, get) => ({
  groups: [],
  loading: false,
  saving: false,
  fetch: async () => {
    set({ loading: true })
    try {
      const groups = await apiList<ConfigGroup>('/system/config')
      set({ groups, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载系统配置失败: ${e.message}`)
    }
  },
  update: async (updates) => {
    set({ saving: true })
    try {
      const result = await apiFetch<{ updated: number; requires_restart: number; message: string }>('/system/config', {
        method: 'PUT',
        body: JSON.stringify({ updates }),
      })
      set({ saving: false })
      toast.success(result.message || '配置已更新')
      // 重新加载配置
      await get().fetch()
      return result
    } catch (e: any) {
      set({ saving: false })
      toast.error(`更新配置失败: ${e.message}`)
      return null
    }
  },
}))
