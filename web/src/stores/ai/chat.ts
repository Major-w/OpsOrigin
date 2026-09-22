
import { create } from 'zustand'
import { apiFetch, apiList, apiFetchRaw } from '@/lib/utils'
import type { ModelInfo, PendingApproval } from './types'
import { getMessage } from '@/lib/message'

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
    onToolCall?: (tc: { name: string; args: any; status: 'running' | 'done'; result?: string }) => void,
    knowledgeCategories?: string[],
    /** SSE event:truncated 时回调(上游 finish_reason=length, 输出被 max_tokens 截断)。 */
    onTruncated?: (note: string) => void,
    /** 本轮随消息上传的附件(图片/文档)元数据, 后端转为多模态片段(attachment:// 引用)。 */
    attachments?: { id?: string; name: string; type: string }[],
    /** SSE event:clarify — 用户问题模糊时 AI 反问需要补充的信息。 */
    onClarify?: (question: string) => void,
    /** SSE event:suggestions — 可点击的继续追问建议。 */
    onSuggestions?: (items: string[]) => void,
  ) => Promise<void>
  loadConversations: (agentId?: string) => Promise<any[]>
  submitFeedback: (params: { conversation_id?: string; message_id?: string; rating: 'up' | 'down'; comment?: string; cited_articles?: string[] }) => Promise<boolean>
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
      getMessage().error(`AI 响应失败: ${e.message}`)
      return null
    }
  },
  streamChat: async (prompt, onToken, onDone, history = [], agentId, reasoning, onThinking, explore, onAsk, serverConvId, signal, model, onError, onToolCall, knowledgeCategories, onTruncated?: (note: string) => void, attachments?: { id?: string; name: string; type: string }[], onClarify?: (question: string) => void, onSuggestions?: (items: string[]) => void) => {
    try {
      const messageId = `msg_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
      const body: any = { prompt, history, stream: true, message_id: messageId }
      if (agentId) body.agent_id = agentId
      if (reasoning) body.reasoning = true
      if (explore) body.explore = true
      if (model) body.model = model
      if (knowledgeCategories?.length) body.knowledge_categories = knowledgeCategories
      if (attachments?.length) body.attachments = attachments.map(a => ({ id: a.id, name: a.name, type: a.type }))
      if (serverConvId) body.conversation_id = serverConvId
      const resp = await apiFetchRaw('/ai/chat', {
        method: 'POST',
        body: JSON.stringify(body),
        signal,
      })
      if (!resp.ok) {
        // 尽量读响应体的具体错误原因(如后端 WriteError 的 {error}), 不要只给状态码
        let detail = ''
        try {
          const raw = await resp.text()
          try {
            const j = JSON.parse(raw)
            detail = (j && (j.error || j.message)) || ''
          } catch { detail = raw.slice(0, 200) }
        } catch { /* body 不可读 */ }
        const errMsg = `AI 响应失败(${resp.status})${detail ? `：${String(detail).trim()}` : ''}`
        getMessage().error(errMsg)
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
          if (currentEvent === 'tool') {
            try {
              const tc = JSON.parse(data) as { name: string; args: any; status: 'running' | 'done'; result?: string }
              onToolCall?.(tc)
            } catch {}
            currentEvent = ''
            continue
          }
          // 流内错误事件(模型调用失败/限流/超时等): 必须转成 onError 展示, 不能吞掉或当普通 token 上屏
          if (currentEvent === 'error') {
            let m = data
            try {
              const j = JSON.parse(data)
              if (typeof j === 'string') m = j
              else if (j && (j.message || j.error)) m = j.message || j.error
            } catch { /* data 为纯文本 */ }
            currentEvent = ''
            const msg = `AI 调用失败：${String(m).replace(/^AI 调用失败[：:]\s*/, '')}`
            getMessage().error(msg)
            onError?.(msg)
            continue
          }
          // 输出被 max_tokens 截断(上游 finish_reason=length): 内容是真的, 不算错误——
          // 走 onTruncated 回调, 前端追加一个黄色「输出可能不完整」标签, 不覆盖流内已生成的正文。
          if (currentEvent === 'truncated') {
            let m = data
            try {
              const j = JSON.parse(data)
              if (typeof j === 'string') m = j
            } catch { /* data 为纯文本 */ }
            currentEvent = ''
            onTruncated?.(String(m))
            continue
          }
          // 用户问题模糊 → AI 反问需要补充哪些信息(结构化, 前端渲染为澄清卡片)。
          if (currentEvent === 'clarify') {
            try {
              const j = JSON.parse(data) as { question?: string }
              if (j?.question) onClarify?.(j.question)
            } catch {}
            currentEvent = ''
            continue
          }
          // 继续追问建议(可点击 chips, 点击即作为下一条消息发送)。
          if (currentEvent === 'suggestions') {
            try {
              const arr = JSON.parse(data) as string[]
              if (Array.isArray(arr) && arr.length) onSuggestions?.(arr)
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
      getMessage().error(`AI 流式响应失败: ${e.message}`)
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
  submitFeedback: async (params) => {
    try {
      await apiFetch<void>('/ai/feedback', {
        method: 'POST',
        body: JSON.stringify(params),
      })
      getMessage().success(params.rating === 'up' ? '感谢你的认可！' : '感谢反馈，我们会持续改进')
      return true
    } catch (e: any) {
      getMessage().error(`反馈提交失败: ${e.message}`)
      return false
    }
  },
}))
