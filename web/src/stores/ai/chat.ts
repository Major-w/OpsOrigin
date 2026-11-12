
import { create } from 'zustand'
import { apiFetch, apiList, apiFetchRaw } from '@/lib/utils'
import type { ModelInfo, PendingApproval, TeamStreamEvent } from './types'

/** 统一入口执行者(对应后端 chatRequest.executor)。团队流传 {type:'team',id} 即走 /ai/chat 团队分支(v1 帧)。 */
export interface ChatExecutorSpec {
  type: 'default' | 'agent' | 'team'
  id?: string
}
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
    /** 团队/智能体执行者(统一入口)。传 {type:'team',id} 即走 /ai/chat 团队流(v1 帧), 由 onTeamEvent 消费; 普通模式省略。 */
    executor?: ChatExecutorSpec,
    /** 团队流事件回调: 接收由 v1 帧(agent/agent_token/tool/final/error)翻译回的 TeamStreamEvent, 与旧 streamTeamChat 的 onEvent 契约一致。 */
    onTeamEvent?: (ev: TeamStreamEvent) => void,
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
  streamChat: async (prompt, onToken, onDone, history = [], agentId, reasoning, onThinking, explore, onAsk, serverConvId, signal, model, onError, onToolCall, knowledgeCategories, onTruncated?: (note: string) => void, attachments?: { id?: string; name: string; type: string }[], onClarify?: (question: string) => void, onSuggestions?: (items: string[]) => void, executor?: ChatExecutorSpec, onTeamEvent?: (ev: TeamStreamEvent) => void) => {
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
      if (executor) body.executor = executor
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
              const tc = JSON.parse(data) as { name: string; args: any; result?: string; agent_id?: string; status: 'running' | 'done' }
              // 团队流: tool 事件带 agent_id, 翻译成 TeamStreamEvent 走 onTeamEvent(路由到具体成员的工具链)。
              if (tc.agent_id && onTeamEvent) {
                if (tc.status === 'done') {
                  onTeamEvent({ type: 'tool_result', agent_id: tc.agent_id, agent_name: '', role: '', tool_name: tc.name, tool_result: tc.result ?? '' })
                } else {
                  onTeamEvent({ type: 'tool_call', agent_id: tc.agent_id, agent_name: '', role: '', tool_name: tc.name, tool_args: tc.args })
                }
              } else {
                onToolCall?.(tc)
              }
            } catch {}
            currentEvent = ''
            continue
          }
          // 流内错误事件(模型调用失败/限流/超时等): 必须转成 onError 展示, 不能吞掉或当普通 token 上屏
          if (currentEvent === 'error') {
            let m = data
            let member: { agent_id?: string; agent_name?: string; role?: string } = {}
            try {
              const j = JSON.parse(data)
              if (typeof j === 'string') m = j
              else if (j) {
                m = j.message || j.error || data
                // 团队 v1 帧的成员级 error 带身份, 用于标红具体成员
                if (j.agent_id) member = {
                  agent_id: String(j.agent_id),
                  agent_name: String(j.agent_name ?? ''),
                  role: String(j.role ?? ''),
                }
              }
            } catch { /* data 为纯文本 */ }
            currentEvent = ''
            const msg = `AI 调用失败：${String(m).replace(/^AI 调用失败[：:]\s*/, '')}`
            // 成员级错误只在团队卡片内标红: 不弹全局 toast, 也不升级成整轮失败。
            const memberError = !!member.agent_id
            if (!memberError) {
              getMessage().error(msg)
              onError?.(msg)
            }
            // 团队流: 同一错误帧也翻译给 onTeamEvent(顶层错误标记 turn, 成员错误标记具体成员)。
            if (onTeamEvent) onTeamEvent({ type: 'error', error: msg, ...member })
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
          // —— 团队流 v1 帧(由 /ai/chat + executor=team 产生) ——
          // 仅在 onTeamEvent 存在时消费, 翻译回 TeamStreamEvent 喂给既有团队状态机, 与普通模式正文 token 完全隔离。
          if (currentEvent === 'agent') {
            try {
              const a = JSON.parse(data) as { phase: 'start' | 'end'; agent_id: string; agent_name?: string; role?: string; content?: string }
              if (a.phase === 'start') onTeamEvent?.({ type: 'agent_start', agent_id: a.agent_id, agent_name: a.agent_name ?? '', role: a.role ?? '' })
              else onTeamEvent?.({ type: 'agent_end', agent_id: a.agent_id, agent_name: a.agent_name ?? '', role: a.role ?? '', content: a.content ?? '' })
            } catch {}
            currentEvent = ''
            continue
          }
          if (currentEvent === 'agent_token') {
            try {
              const a = JSON.parse(data) as { agent_id: string; content: string; agent_name?: string; role?: string; member_role?: string }
              // supervisor 帧的 role 是帧标记, 成员真实角色在 member_role; 普通 token 直接用 role。
              const identity = {
                agent_id: a.agent_id,
                agent_name: a.agent_name ?? '',
                role: a.member_role ?? a.role ?? '',
              }
              if (a.role === 'supervisor') onTeamEvent?.({ type: 'supervisor_review', ...identity, content: a.content })
              else onTeamEvent?.({ type: 'token', ...identity, content: a.content })
            } catch {}
            currentEvent = ''
            continue
          }
          if (currentEvent === 'final') {
            try {
              const f = JSON.parse(data) as { content: string; done: boolean }
              onTeamEvent?.({ type: 'final', content: f.content, done: f.done })
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
