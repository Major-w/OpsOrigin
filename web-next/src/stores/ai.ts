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
  chat: (prompt: string, history?: { role: string; content: string }[]) => Promise<string | null>
  streamChat: (prompt: string, onToken: (t: string) => void, onDone: () => void, history?: { role: string; content: string }[]) => Promise<void>
  loadConversations: (agentId?: string) => Promise<any[]>
  saveConversation: (conv: any) => Promise<any | null>
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
  chat: async (prompt, history = []) => {
    try {
      const res = await apiFetch<{ content: string }>('/ai/chat', {
        method: 'POST',
        body: JSON.stringify({ prompt, history }),
      })
      return res.content
    } catch (e: any) {
      toast.error(`AI 响应失败: ${e.message}`)
      return null
    }
  },
  streamChat: async (prompt, onToken, onDone, history = []) => {
    try {
      const resp = await fetch(`${window.location.origin}/api/v1/ai/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prompt, history, stream: true }),
      })
      if (!resp.ok) {
        toast.error(`AI 响应失败: ${resp.status}`)
        onDone()
        return
      }
      const reader = resp.body?.getReader()
      if (!reader) { onDone(); return }
      const decoder = new TextDecoder()
      let buf = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() || ''
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          const data = line.slice(6)
          if (data === '[DONE]') { onDone(); return }
          try {
            const token = JSON.parse(data) as string
            onToken(token)
          } catch {}
        }
      }
      onDone()
    } catch (e: any) {
      toast.error(`AI 流式响应失败: ${e.message}`)
      onDone()
    }
  },
  loadConversations: async (agentId) => {
    try {
      const params = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
      return await apiFetch<any[]>(`/ai/conversations${params}`)
    } catch {
      return []
    }
  },
  saveConversation: async (conv) => {
    try {
      return await apiFetch<any>('/ai/conversations', {
        method: 'POST',
        body: JSON.stringify(conv),
      })
    } catch {
      return null
    }
  },
}))
