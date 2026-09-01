
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'

interface AgentState {
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

export const useAgentAIStore = create<AgentState>((set, get) => ({
  agents: [],
  tools: [],
  plans: [],
  loading: false,
  error: null,
  fetchAgents: async () => {
    set({ loading: true })
    try {
      const agents = await apiList<any>('/ai/agents')
      set({ agents, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载AI代理失败: ${e.message}`)
    }
  },
  fetchTools: async () => {
    set({ loading: true })
    try {
      const tools = await apiList<any>('/ai/tools')
      set({ tools, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载AI工具失败: ${e.message}`)
    }
  },
  fetchPlans: async () => {
    set({ loading: true })
    try {
      const plans = await apiList<any>('/ai/plans')
      set({ plans, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载执行计划失败: ${e.message}`)
    }
  },
  createAgent: async (data) => {
    try {
      await apiFetch('/ai/agents', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('创建AI代理成功')
      await get().fetchAgents()
      return true
    } catch (e: any) {
      getMessage().error(`创建AI代理失败: ${e.message}`)
      return false
    }
  },
  executePlan: async (data) => {
    try {
      await apiFetch('/ai/plans', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('执行计划成功')
      await get().fetchPlans()
      return true
    } catch (e: any) {
      getMessage().error(`执行计划失败: ${e.message}`)
      return false
    }
  },
}))
