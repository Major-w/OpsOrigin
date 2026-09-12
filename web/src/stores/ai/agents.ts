
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'

/** 数字员工(Agent)摘要。字段随后端版本演进, 用 unknown 索引签名吸纳新增字段(拒绝 any)。 */
export interface AgentLite {
  id: string
  name: string
  description?: string
  system_prompt?: string
  model_name?: string
  tools?: string[]
  enabled?: boolean
  created_at?: string
  updated_at?: string
  [key: string]: unknown
}

/** 新建数字员工的入参(createAgent)。 */
export interface CreateAgentPayload {
  name: string
  description?: string
  system_prompt?: string
  model_name?: string
  tools?: string[]
  [key: string]: unknown
}

interface AgentState {
  agents: AgentLite[]
  loading: boolean
  error: string | null
  fetchAgents: () => Promise<void>
  createAgent: (data: CreateAgentPayload) => Promise<boolean>
}

export const useAgentAIStore = create<AgentState>((set, get) => ({
  agents: [],
  loading: false,
  error: null,
  fetchAgents: async () => {
    set({ loading: true })
    try {
      const agents = await apiList<AgentLite>('/ai/agents')
      set({ agents: Array.isArray(agents) ? agents : [], loading: false, error: null })
    } catch (e: any) {
      set({ loading: false, error: e?.message || '加载 AI 代理失败' })
      getMessage().error(`加载AI代理失败: ${e.message}`)
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
}))
