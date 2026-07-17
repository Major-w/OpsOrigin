'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

interface AIState {
  agents: any[]
  tools: any[]
  plans: any[]
  loading: boolean
  error: string | null
  fetchAgents: () => Promise<void>
  fetchTools: () => Promise<void>
  fetchPlans: () => Promise<void>
  createAgent: (data: any) => Promise<boolean>
  executePlan: (data: any) => Promise<boolean>
}

export const useAIStore = create<AIState>((set, get) => ({
  agents: [], tools: [], plans: [], loading: false, error: null,
  fetchAgents: async () => {
    set({ loading: true })
    try {
      const agents = await apiFetch<any[]>('/ai/agents')
      set({ agents, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载AI代理失败: ${e.message}`)
    }
  },
  fetchTools: async () => {
    set({ loading: true })
    try {
      const tools = await apiFetch<any[]>('/ai/tools')
      set({ tools, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载AI工具失败: ${e.message}`)
    }
  },
  fetchPlans: async () => {
    set({ loading: true })
    try {
      const plans = await apiFetch<any[]>('/ai/plans')
      set({ plans, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载执行计划失败: ${e.message}`)
    }
  },
  createAgent: async (data) => {
    try {
      await apiFetch('/ai/agents', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建AI代理成功')
      await get().fetchAgents()
      return true
    } catch (e: any) {
      toast.error(`创建AI代理失败: ${e.message}`)
      return false
    }
  },
  executePlan: async (data) => {
    try {
      await apiFetch('/ai/plans', { method: 'POST', body: JSON.stringify(data) })
      toast.success('执行计划成功')
      await get().fetchPlans()
      return true
    } catch (e: any) {
      toast.error(`执行计划失败: ${e.message}`)
      return false
    }
  },
}))
