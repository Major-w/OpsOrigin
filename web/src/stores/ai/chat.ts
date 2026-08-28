
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { message } from 'antd'
import type { ModelInfo, PendingApproval } from './types'

interface ChatState {
  models: ModelInfo[]
  assistantOpen: boolean
  setAssistantOpen: (open: boolean) => void
  toggleAssistant: () => void
  fetchModels: () => Promise<ModelInfo[]>
  chat: (prompt: string, history?: { role: string; content: string }[]) => Promise<string | null>
  streamChat: (
    prompt: string,
    onToken: (t: string) => void,
    onDone: (meta?: { message_id?: string; conversation_id?: string; citations?: string[] }) => void,
    history?: { role: string; content: string }[],
    agentId?: string,
    reasoning?: boolean,
    onThinking?: (t: string) => void,
    explore?: boolean,
    onAsk?: (approval: PendingApproval) => void,
    serverConvId?: string,
    signal?: AbortSignal,
    model?: string,
    onError?: (msg: string) => void,
  ) => Promise<void>
  loadConversations: (agentId?: string) => Promise<any[]>
  saveConversation: (conv: any) => Promise<any | null>
  submitFeedback: (params: { conversation_id?: string; message_id?: string; rating: 'up' | 'down'; comment?: string }) => Promise<boolean>
}

export const useChatStore = create<ChatState>((set) => ({
  models: [],
  assistantOpen: false,
  setAssistantOpen: (open) => set({ assistantOpen: open }),
  toggleAssistant: () => set(st => ({ assistantOpen: !st.assistantOpen })),
  fetchModels: async () => {
    try {
      const models = await apiList<ModelInfo>('/ai/models')
      set({ models: models || [] })
      return models || []
    } catch {
      return []
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
      message.error(`AI 响应失败: ${e.message}`)
      return null
    }
  },
  streamChat: async (prompt, onToken, onDone, history = [], agentId, reasoning, onThinking, explore, onAsk, serverConvId, signal, model, onError) => {
    try {
      const messageId = `msg_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
      const body: any = { prompt, history, stream: true, message_id: messageId }
      if (agentId) body.agent_id = agentId
      if (reasoning) body.reasoning = true
      if (explore) body.explore = true
      if (model) body.model = model
      if (serverConvId) body.conversation_id = serverConvId
      const resp = await fetch(`${window.location.origin}/api/v1/ai/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal,
      })
      if (!resp.ok) {
        const errMsg = `AI 响应失败: ${resp.status}`
        message.error(errMsg)
        onError?.(errMsg)
        onDone()
        return
      }
      const reader = resp.body?.getReader()
      if (!reader) { onDone(); return }
      const decoder = new TextDecoder()
      let buf = ''
      let currentEvent = ''
      let conversationId = ''
      let citations: string[] = []
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() || ''
        for (const line of lines) {
          if (line.startsWith('event: ')) {
            currentEvent = line.slice(7).trim()
            continue
          }
          if (!line.startsWith('data: ')) continue
          const data = line.slice(6)
          if (data === '[DONE]') {
            onDone({ message_id: messageId, conversation_id: conversationId, citations })
            return
          }
          if (currentEvent === 'conversation') {
            conversationId = data
            currentEvent = ''
            continue
          }
          if (currentEvent === 'citations') {
            try { citations = JSON.parse(data) } catch {}
            currentEvent = ''
            continue
          }
          if (currentEvent === 'thinking') {
            try { const t = JSON.parse(data) as string; onThinking?.(t) } catch {}
            currentEvent = ''
            continue
          }
          if (currentEvent === 'ask') {
            try {
              const approval = JSON.parse(data) as PendingApproval
              onAsk?.(approval)
            } catch {}
            currentEvent = ''
            continue
          }
          currentEvent = ''
          try {
            const token = JSON.parse(data) as string
            onToken(token)
          } catch {}
        }
      }
      onDone({ message_id: messageId, conversation_id: conversationId, citations })
    } catch (e: any) {
      if (e?.name === 'AbortError' || e?.name === 'TimeoutError') {
        onDone()
        return
      }
      message.error(`AI 流式响应失败: ${e.message}`)
      onError?.(`AI 流式响应失败: ${e.message}`)
      onDone()
    }
  },
  loadConversations: async (agentId) => {
    try {
      const params = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
      return await apiList<any>(`/ai/conversations${params}`)
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
  submitFeedback: async (params) => {
    try {
      await apiFetch<void>('/ai/feedback', {
        method: 'POST',
        body: JSON.stringify(params),
      })
      message.success(params.rating === 'up' ? '感谢你的认可！' : '感谢反馈，我们会持续改进')
      return true
    } catch (e: any) {
      message.error(`反馈提交失败: ${e.message}`)
      return false
    }
  },
}))
