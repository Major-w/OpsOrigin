'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

// ============================================================================
// 多智能体团队（F1）类型定义
// 与后端 Go API 契约严格对齐：GET/POST/PUT/DELETE /api/v1/ai/teams
// 以及 POST /api/v1/ai/teams/{id}/chat 的 SSE 事件流。
// ============================================================================

/** 团队协作模式 */
export type TeamMode = 'supervisor' | 'parallel' | 'sequential' | 'debate'

/** 成员在团队中的角色 */
export type TeamRole = 'supervisor' | 'member' | 'finalizer'

/** 团队成员 */
export interface TeamMember {
  agent_id: string
  agent_name: string
  role: TeamRole
  can_finalize: boolean
  persona: string
  system_prompt: string
  /** 成员是否启用工具调用(Function Calling) */
  enable_tools: boolean
  /** per-member 模型覆盖(覆盖引擎/团队默认模型) */
  model_name: string
  /** 成员权重(预留, 目前未使用) */
  weight: number
  /** 可选：后端返回时可能携带的自身 ID */
  id?: string
}

/** 一个多智能体团队 */
export interface AgentTeam {
  id: string
  name: string
  description: string
  mode: TeamMode
  system_prompt: string
  members: TeamMember[]
  /** 团队级启用工具调用(所有成员继承) */
  enable_tools: boolean
  /** 单成员作答硬超时(秒, 默认90) */
  member_timeout_sec: number
  /** 单成员失败重试次数(默认1) */
  member_retries: number
  /** 辩论模式往返轮数(默认2) */
  debate_rounds: number
  /** 是否启用主管审核修订回路 */
  supervisor_verify: boolean
  created_at?: string
  updated_at?: string
}

/** 聊天历史消息（用于把上下文回传给后端） */
export interface ChatMessage {
  role: string
  content: string
}

/** 创建 / 更新团队的请求体（两者字段一致，均为全量） */
export interface CreateTeamPayload {
  name: string
  description: string
  mode: TeamMode
  system_prompt: string
  enable_tools: boolean
  member_timeout_sec: number
  member_retries: number
  debate_rounds: number
  supervisor_verify: boolean
  members: Array<{
    agent_id: string
    agent_name: string
    role: TeamRole
    can_finalize: boolean
    persona: string
    system_prompt: string
    enable_tools: boolean
    model_name: string
  }>
}

// ============================================================================
// 敏感操作审批策略 (PRD 5.2.5)
// ============================================================================

/** 审批策略模式 */
export type ApprovalMode = 'always_ask' | 'always_allow' | 'sensitive_only'

/** 审批策略配置 */
export interface ApprovalPolicy {
  mode: ApprovalMode
  sensitive_tools: string[]
  ask_timeout_sec: number
}

/** 挂起的审批请求(SSE ask 事件载荷) */
export interface PendingApproval {
  id: string
  tenant_id?: string
  agent_id?: string
  tool: string
  summary: string
  args: string
  created_at?: string
  deadline: string
}

export type UpdateTeamPayload = CreateTeamPayload

/**
 * SSE 团队流式事件。
 * 与服务端保持一致：
 * - agent_start / token / agent_end 携带 agent 维度信息
 * - final 为团队最终答复（done 表示流式是否结束）
 * - error 为异常事件（agent_id 等字段可选）
 */
export type TeamStreamEvent =
  | { type: 'agent_start'; agent_id: string; agent_name: string; role: string }
  | { type: 'token'; agent_id: string; agent_name: string; role: string; content: string }
  | { type: 'agent_end'; agent_id: string; agent_name: string; role: string; content: string }
  | { type: 'tool_call'; agent_id: string; agent_name: string; role: string; tool_name: string; tool_args: string }
  | { type: 'tool_result'; agent_id: string; agent_name: string; role: string; tool_name: string; tool_result: string }
  | { type: 'supervisor_review'; agent_id: string; agent_name: string; role: string; content: string }
  | { type: 'final'; content: string; done: boolean }
  | { type: 'error'; agent_id?: string; agent_name?: string; role?: string; error: string }

/** 类型守卫：对 SSE 解析出的 unknown 做安全收窄 */
export function isTeamStreamEvent(value: unknown): value is TeamStreamEvent {
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  if (typeof v.type !== 'string') return false
      switch (v.type) {
    case 'agent_start':
    case 'token':
    case 'agent_end': {
      const baseOk =
        typeof v.agent_id === 'string' &&
        typeof v.agent_name === 'string' &&
        typeof v.role === 'string'
      if (!baseOk) return false
      if (v.type === 'token' || v.type === 'agent_end') {
        return typeof v.content === 'string'
      }
      return true
    }
    case 'tool_call': {
      const baseOk =
        typeof v.agent_id === 'string' &&
        typeof v.agent_name === 'string' &&
        typeof v.role === 'string'
      if (!baseOk) return false
      return typeof v.tool_name === 'string' && typeof v.tool_args === 'string'
    }
    case 'tool_result': {
      const baseOk =
        typeof v.agent_id === 'string' &&
        typeof v.agent_name === 'string' &&
        typeof v.role === 'string'
      if (!baseOk) return false
      return typeof v.tool_name === 'string' && typeof v.tool_result === 'string'
    }
    case 'supervisor_review':
      return typeof v.agent_id === 'string' &&
        typeof v.agent_name === 'string' &&
        typeof v.role === 'string' &&
        typeof v.content === 'string'
    case 'final':
      return typeof v.content === 'string' && typeof v.done === 'boolean'
    case 'error':
      return typeof v.error === 'string'
    default:
      return false
  }
}

// ============================================================================
// AI Store（含既有 agents/tools/plans/chat 能力 + 新增 teams 能力）
// ============================================================================

interface AIState {
  // —— 既有 AI 能力（保留，供 /ai 普通·思维链 模式与 /ai/settings 使用） ——
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
  ) => Promise<void>
  loadConversations: (agentId?: string) => Promise<any[]>
  saveConversation: (conv: any) => Promise<any | null>
  submitFeedback: (params: { conversation_id?: string; message_id?: string; rating: 'up' | 'down'; comment?: string }) => Promise<boolean>

  // —— 多智能体团队（F1） ——
  teams: AgentTeam[]
  teamsLoading: boolean
  teamsError: string | null
  loadTeams: () => Promise<void>
  createTeam: (payload: CreateTeamPayload) => Promise<AgentTeam | null>
  updateTeam: (id: string, payload: UpdateTeamPayload) => Promise<AgentTeam | null>
  deleteTeam: (id: string) => Promise<boolean>
  streamTeamChat: (
    teamId: string,
    message: string,
    history: ChatMessage[],
    onEvent: (ev: TeamStreamEvent) => void,
  ) => Promise<void>

  // —— 敏感操作审批策略 ——
  getApprovalPolicy: (agentId?: string) => Promise<ApprovalPolicy | null>
  putApprovalPolicy: (data: { agent_id?: string; mode?: ApprovalMode; sensitive_tools?: string[]; ask_timeout_sec?: number }) => Promise<ApprovalPolicy | null>
  respondApproval: (id: string, action: 'approve' | 'reject', comment?: string) => Promise<boolean>
  listPendingApprovals: () => Promise<PendingApproval[]>

  // —— 全局悬浮助手 ——
  assistantOpen: boolean
  setAssistantOpen: (open: boolean) => void
  toggleAssistant: () => void
}

export const useAIStore = create<AIState>((set, get) => ({
  agents: [], tools: [], plans: [], loading: false, error: null,
  teams: [], teamsLoading: false, teamsError: null,
  assistantOpen: false,

  // ----------------------- 既有 AI 能力 -----------------------
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
  streamChat: async (prompt, onToken, onDone, history = [], agentId, reasoning, onThinking, explore, onAsk, serverConvId) => {
    try {
      // 为本次 AI 回复生成稳定的 message_id, 反馈时用于精确关联
      const messageId = `msg_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
      const body: any = { prompt, history, stream: true, message_id: messageId }
      if (agentId) body.agent_id = agentId
      if (reasoning) body.reasoning = true
      if (explore) body.explore = true
      // 显式携带 conversation_id 时, 后端按该 ID 续接/新建持久化会话(SSE 返回 event:conversation 确认)。
      if (serverConvId) body.conversation_id = serverConvId
      const resp = await fetch(`${window.location.origin}/api/v1/ai/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
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
          // SSE 命名事件
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
          // conversation / citations 命名事件
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

  submitFeedback: async (params) => {
    try {
      await apiFetch<void>('/ai/feedback', {
        method: 'POST',
        body: JSON.stringify(params),
      })
      toast.success(params.rating === 'up' ? '感谢你的认可！' : '感谢反馈，我们会持续改进')
      return true
    } catch (e: any) {
      toast.error(`反馈提交失败: ${e.message}`)
      return false
    }
  },

  // ----------------------- 多智能体团队（F1） -----------------------

  loadTeams: async () => {
    set({ teamsLoading: true, teamsError: null })
    try {
      const teams = await apiFetch<AgentTeam[]>('/ai/teams')
      set({ teams: teams || [], teamsLoading: false })
    } catch (e: any) {
      set({ teamsLoading: false, teamsError: e.message || '加载团队失败' })
      toast.error(`加载团队失败: ${e.message}`)
    }
  },

  createTeam: async (payload) => {
    try {
      const team = await apiFetch<AgentTeam>('/ai/teams', {
        method: 'POST',
        body: JSON.stringify(payload),
      })
      toast.success('创建团队成功')
      await get().loadTeams()
      return team
    } catch (e: any) {
      toast.error(`创建团队失败: ${e.message}`)
      return null
    }
  },

  updateTeam: async (id, payload) => {
    try {
      const team = await apiFetch<AgentTeam>(`/ai/teams/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body: JSON.stringify(payload),
      })
      toast.success('更新团队成功')
      await get().loadTeams()
      return team
    } catch (e: any) {
      toast.error(`更新团队失败: ${e.message}`)
      return null
    }
  },

  deleteTeam: async (id) => {
    try {
      await apiFetch<void>(`/ai/teams/${encodeURIComponent(id)}`, { method: 'DELETE' })
      toast.success('删除团队成功')
      return true
    } catch (e: any) {
      toast.error(`删除团队失败: ${e.message}`)
      return false
    }
  },

  /**
   * 调用团队流式聊天接口（SSE）。
   * 使用 fetch + ReadableStream 逐块读取，按 '\n' 切分，匹配 'data: ' 前缀的行，
   * JSON.parse 后（经过 isTeamStreamEvent 校验）回调 onEvent。
   * final 事件视为结束信号；连接异常时回调 error 事件。
   */
  streamTeamChat: async (teamId, message, history, onEvent) => {
    try {
      const resp = await fetch(
        `${window.location.origin}/api/v1/ai/teams/${encodeURIComponent(teamId)}/chat`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ message, history }),
        },
      )
      if (!resp.ok) {
        let errMsg = `HTTP ${resp.status}`
        try {
          const body = await resp.json()
          if (body && typeof body.error === 'string') errMsg = body.error
        } catch {
          /* 忽略解析失败，使用默认错误信息 */
        }
        onEvent({ type: 'error', error: errMsg })
        return
      }

      const reader = resp.body?.getReader()
      if (!reader) {
        onEvent({ type: 'error', error: '响应流不可用' })
        return
      }

      const decoder = new TextDecoder()
      let buf = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        // 保留未完成的最后一行（可能跨块）
        buf = lines.pop() || ''
        for (const line of lines) {
          const trimmed = line.trim()
          if (!trimmed.startsWith('data:')) continue
          const data = trimmed.slice(trimmed.indexOf('data:') + 5).trim()
          if (!data) continue
          try {
            const parsed: unknown = JSON.parse(data)
            if (isTeamStreamEvent(parsed)) {
              onEvent(parsed)
            }
          } catch {
            /* 跳过无法解析的行（如心跳注释） */
          }
        }
      }
    } catch (e: any) {
      onEvent({ type: 'error', error: e?.message || '连接异常' })
    }
  },

  // ----------------------- 敏感操作审批策略 -----------------------

  getApprovalPolicy: async (agentId) => {
    try {
      const q = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
      const d = await apiFetch<any>(`/ai/approval-policy${q}`)
      return d?.policy || null
    } catch (e: any) {
      toast.error(`加载审批策略失败: ${e.message}`)
      return null
    }
  },

  putApprovalPolicy: async (data) => {
    try {
      const d = await apiFetch<any>('/ai/approval-policy', {
        method: 'PUT',
        body: JSON.stringify(data),
      })
      toast.success('审批策略已保存，即时生效')
      return d?.policy || null
    } catch (e: any) {
      toast.error(`保存审批策略失败: ${e.message}`)
      return null
    }
  },

  respondApproval: async (id, action, comment) => {
    try {
      await apiFetch<void>(`/ai/approvals/${encodeURIComponent(id)}/respond`, {
        method: 'POST',
        body: JSON.stringify({ action, comment }),
      })
      return true
    } catch (e: any) {
      toast.error(`响应审批失败: ${e.message}`)
      return false
    }
  },

  listPendingApprovals: async () => {
    try {
      return await apiFetch<PendingApproval[]>('/ai/approvals/pending')
    } catch {
      return []
    }
  },

  // —— 全局悬浮助手 ——
  setAssistantOpen: (open) => set({ assistantOpen: open }),
  toggleAssistant: () => set(st => ({ assistantOpen: !st.assistantOpen })),
}))

/**
 * 团队能力专用 Hook：从 AIStore 中选取团队相关状态与方法。
 * 通过逐个 selector 订阅，避免返回新对象导致的无谓重渲染。
 */
export function useTeams() {
  const teams = useAIStore(s => s.teams)
  const loading = useAIStore(s => s.teamsLoading)
  const error = useAIStore(s => s.teamsError)
  const loadTeams = useAIStore(s => s.loadTeams)
  const createTeam = useAIStore(s => s.createTeam)
  const updateTeam = useAIStore(s => s.updateTeam)
  const deleteTeam = useAIStore(s => s.deleteTeam)
  const streamTeamChat = useAIStore(s => s.streamTeamChat)
  return { teams, loading, error, loadTeams, createTeam, updateTeam, deleteTeam, streamTeamChat }
}
