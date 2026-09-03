
// ============================================================================
// AI 聊天内核（共享）
// 浮窗 AIAssistant / 纯净模式 PureMode / 全页 /ai 三入口共用同一套:
//   - useAIChat: 普通/自主/团队 三模式状态 + streamChat/streamTeamChat 编排 + 审批 + 会话持久化
//   - ChatMessageList: 消息渲染(思考折叠 / 任务列表 / 引用 / 反馈 / 重试)
//   - ChatInputBar: 模式切换 / 模型 / 思考开关 / Agent / 输入框 / 快捷操作
//   - TeamChatView: 团队模式(选队 + 多智能体流式 + 输入)
//   - ApprovalBar: 敏感操作审批条
// 通过 props/variant 适配三处差异, 消除三套重复 UI。
// ============================================================================

import { useEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { useAIStore, useTeams, type TeamStreamEvent, type AgentTeam } from '@/stores/ai'
import type { ModelInfo, PendingApproval } from '@/stores/ai/types'
import { renderMarkdown, renderExploreContent } from '@/lib/ai-render'
import { AiMarkdown } from '@/components/ai-markdown'
import { cn } from '@/lib/utils'
import {
  Bot, User, Send, Square, Loader2, Terminal, Brain, CheckCircle, AlertTriangle,
  ThumbsUp, ThumbsDown, Users, Sparkles, XCircle,
} from 'lucide-react'

// ---------------------------------------------------------------------------
// 共享类型
// ---------------------------------------------------------------------------

export type ChatMode = 'normal' | 'explore' | 'team'

export interface ToolCallLocal {
  id: string
  name: string
  args: string
  result: string
  status: 'running' | 'done'
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  thinking?: string
  citations?: string[]
  feedback_rating?: 'up' | 'down'
  status?: 'streaming' | 'done' | 'error'
  errorText?: string
  timestamp: number
  toolCalls?: ToolCallLocal[]
  messageId?: string
}

export interface AgentRunLocal {
  agent_id: string
  agent_name: string
  role: string
  status: 'streaming' | 'done' | 'error'
  text: string
  error?: string
  toolCalls: ToolCallLocal[]
  supervisorNote?: string
}

export interface TeamTurnLocal {
  id: string
  teamId: string
  user: string
  agents: AgentRunLocal[]
  final: string
  done: boolean
  error?: string
}

// 把思考过程文本解析成步骤(按序号/项目符号/换行), 用于折叠式"思考步骤"展示。
export function parseThinkingSteps(text: string): string[] {
  if (!text) return []
  const lines = text.split('\n').map(l => l.trim()).filter(Boolean)
  const steps: string[] = []
  for (const line of lines) {
    const m = line.match(/^(\d+[.、]|[-*•]|\s*-\s|步骤\s*\d+[.、:]?)\s*(.*)$/)
    if (m) {
      steps.push(m[2] || line)
    } else if (steps.length === 0) {
      steps.push(line)
    } else {
      steps[steps.length - 1] += ' ' + line
    }
  }
  return steps.length ? steps : [text]
}

const MODE_LABEL: Record<ChatMode, string> = { normal: '普通', explore: '自主', team: '团队' }
const MODE_DESC: Record<ChatMode, string> = {
  normal: '单轮问答 / 工具调用',
  explore: 'AI 自主规划、多步执行',
  team: '多智能体协同作答',
}
const TEAM_ROLE_LABEL: Record<string, string> = { supervisor: '主管', member: '成员', finalizer: '终结者' }
const TEAM_MODE_LABEL: Record<string, string> = {
  supervisor: '主管调度', parallel: '并行协作', sequential: '顺序流转', debate: '辩论裁决',
}

function newConvId(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

// ---------------------------------------------------------------------------
// useAIChat —— 统一聊天状态与编排
// ---------------------------------------------------------------------------

interface UseAIChatOptions {
  welcome: string
  welcomeId?: string
  // 会话持久化(localStorage): 传 key 则跨刷新续接
  persistKey?: string
  convIdKey?: string
  // 续接被拒(403)时是否清空本地会话 ID(浮窗/纯净=true; 全页=false, 由服务端历史驱动)
  resetConvOn403?: boolean
  // 自定义历史裁剪(全页带 HIST_BUDGET 压缩); 不传则取最近 20 条
  buildHistory?: (msgs: ChatMessage[]) => { role: string; content: string }[]
  // 是否把 tool_event 累积进助手消息的 toolCalls(全页=true 用于"任务列表")
  captureToolCalls?: boolean
  // 是否支持团队模式(纯净/全页=true; 浮窗=false)
  enableTeam?: boolean
  // 是否启用 abort(浮窗/纯净=true; 全页=false)
  enableAbort?: boolean
  // 思考开关持久化 key(全页=ai.showThinking)
  showThinkingKey?: string
  // 默认模式
  defaultMode?: ChatMode
}

export function useAIChat(opts: UseAIChatOptions) {
  const {
    welcome,
    welcomeId = 'welcome',
    persistKey,
    convIdKey,
    resetConvOn403 = true,
    buildHistory,
    captureToolCalls = false,
    enableTeam = false,
    enableAbort = true,
    showThinkingKey,
    defaultMode = 'normal',
  } = opts

  const { streamChat, respondApproval, models, agents, fetchAgents, fetchModels } = useAIStore()
  const teamApi = useTeams()

  const [mode, setMode] = useState<ChatMode>(defaultMode)
  const [showThinking, setShowThinking] = useState<boolean>(() => {
    if (showThinkingKey && typeof window !== 'undefined') {
      return localStorage.getItem(showThinkingKey) !== 'false'
    }
    return true
  })
  const [selectedModel, setSelectedModel] = useState('')
  const [selectedAgent, setSelectedAgent] = useState<any>(null)

  const [messages, setMessages] = useState<ChatMessage[]>(() => {
    const welcomeMsg: ChatMessage = { id: welcomeId, role: 'assistant', content: welcome, timestamp: Date.now() }
    if (persistKey && typeof window !== 'undefined') {
      try {
        const raw = localStorage.getItem(persistKey)
        if (raw) {
          const parsed = JSON.parse(raw) as { role: string; content: string }[]
          if (Array.isArray(parsed) && parsed.length > 0) {
            return parsed.map((m, i) => ({
              id: `m_${Date.now()}_${i}`,
              role: m.role === 'user' ? 'user' : 'assistant',
              content: m.content || '',
              timestamp: Date.now(),
            }))
          }
        }
      } catch { /* ignore */ }
    }
    return [welcomeMsg]
  })

  const [input, setInput] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [pending, setPending] = useState<PendingApproval | null>(null)
  const [approving, setApproving] = useState(false)
  const [convId, setConvId] = useState<string>(() => {
    if (convIdKey && typeof window !== 'undefined') {
      try { return localStorage.getItem(convIdKey) || '' } catch { return '' }
    }
    return ''
  })

  // 团队模式状态
  const [selectedTeamId, setSelectedTeamId] = useState<string | null>(null)
  const [teamTurns, setTeamTurns] = useState<TeamTurnLocal[]>([])
  const [teamInput, setTeamInput] = useState('')
  const [teamStreaming, setTeamStreaming] = useState(false)

  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => { fetchAgents(); fetchModels() }, [fetchAgents, fetchModels])
  useEffect(() => { if (enableTeam && mode === 'team') teamApi.loadTeams() }, [enableTeam, mode, teamApi])

  // 模型默认选中: 加载到模型池后, 若用户未手动选择, 默认选第一个健康模型
  useEffect(() => {
    if (!selectedModel && models.length > 0) {
      const healthy = models.find(m => m.healthy !== false) || models[0]
      setSelectedModel(healthy.model)
    }
  }, [models, selectedModel])

  // 会话持久化(localStorage, 仅存 role/content, 刷新不丢)
  useEffect(() => {
    if (!persistKey) return
    try {
      const slim = messages.filter(m => m.id !== welcomeId).map(m => ({ role: m.role, content: m.content }))
      localStorage.setItem(persistKey, JSON.stringify(slim))
    } catch { /* ignore */ }
  }, [messages, persistKey, welcomeId])

  const defaultHistory = (msgs: ChatMessage[]): { role: string; content: string }[] =>
    msgs
      .filter(m => m.id !== welcomeId && (m.role === 'user' || m.role === 'assistant'))
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))

  // ----------------------- 普通 / 自主模式 -----------------------

  const send = async (raw: string) => {
    const content = raw.trim()
    if (!content || streaming) return

    if (enableAbort) {
      abortRef.current?.abort()
      abortRef.current = new AbortController()
    }

    const userMsg: ChatMessage = { id: `u_${Date.now()}`, role: 'user', content, timestamp: Date.now() }
    const aiId = `a_${Date.now()}`
    const aiMsg: ChatMessage = { id: aiId, role: 'assistant', content: '', thinking: '', timestamp: Date.now() }
    setMessages(prev => [...prev, userMsg, aiMsg])
    setInput('')
    setStreaming(true)

    const history = buildHistory ? buildHistory([...messages, userMsg]) : defaultHistory([...messages, userMsg])

    let acc = ''
    let thinkingAcc = ''
    const onToken = (t: string) => {
      acc += t
      setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, content: acc } : m)))
    }
    const onThinking = (t: string) => {
      thinkingAcc += t
      setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, thinking: thinkingAcc } : m)))
    }
    const onAsk = (approval: PendingApproval) => setPending(approval)

    const onTool = captureToolCalls
      ? (tc: { name: string; args: any; status: 'running' | 'done'; result?: string }) => {
          setMessages(prev => prev.map(m => {
            if (m.id !== aiId) return m
            const list = m.toolCalls ? [...m.toolCalls] : []
            const argsStr = tc.args ? JSON.stringify(tc.args) : ''
            if (tc.status === 'done') {
              for (let i = list.length - 1; i >= 0; i--) {
                if (list[i].name === tc.name && list[i].status === 'running') {
                  list[i] = { ...list[i], status: 'done', result: tc.result || '', args: argsStr || list[i].args }
                  return { ...m, toolCalls: list }
                }
              }
              list.push({ id: `${tc.name}-${Date.now()}-${list.length}`, name: tc.name, args: argsStr, result: tc.result || '', status: 'done' })
            } else {
              list.push({ id: `${tc.name}-${Date.now()}-${list.length}`, name: tc.name, args: argsStr, result: '', status: 'running' })
            }
            return { ...m, toolCalls: list }
          }))
        }
      : undefined

    let cid = convId
    if (!cid) {
      cid = newConvId('chat')
      setConvId(cid)
      if (convIdKey) { try { localStorage.setItem(convIdKey, cid) } catch { /* ignore */ } }
    }

    await streamChat(
      content,
      onToken,
      (meta) => {
        setMessages(prev => prev.map(m =>
          m.id === aiId
            ? { ...m, status: 'done', citations: meta?.citations ?? m.citations, messageId: meta?.message_id ?? m.messageId }
            : m,
        ))
        if (meta?.conversation_id && meta.conversation_id !== cid) {
          setConvId(meta.conversation_id)
          if (convIdKey) { try { localStorage.setItem(convIdKey, meta.conversation_id) } catch { /* ignore */ } }
        }
        setStreaming(false)
      },
      history,
      selectedAgent?.id,
      showThinking,
      showThinking ? onThinking : undefined,
      mode === 'explore',
      onAsk,
      cid,
      enableAbort ? abortRef.current?.signal : undefined,
      selectedModel,
      (err?: string) => {
        setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, status: 'error', errorText: err || 'AI 响应失败' } : m)))
        setStreaming(false)
        if (resetConvOn403 && err && err.includes('403')) {
          setConvId('')
          if (convIdKey) { try { localStorage.removeItem(convIdKey) } catch { /* ignore */ } }
        }
      },
      onTool,
    )
  }

  const clearSession = () => {
    if (streaming) return
    if (enableAbort) { abortRef.current?.abort(); abortRef.current = null }
    if (persistKey) { try { localStorage.removeItem(persistKey) } catch { /* ignore */ } }
    if (convIdKey) { try { localStorage.removeItem(convIdKey) } catch { /* ignore */ } }
    setConvId('')
    setSelectedAgent(null)
    setTeamTurns([])
    setSelectedTeamId(null)
    setMessages([{ id: welcomeId, role: 'assistant', content: welcome, timestamp: Date.now() }])
  }

  const stopGeneration = () => {
    if (!enableAbort) return
    abortRef.current?.abort()
    abortRef.current = null
    setStreaming(false)
  }

  const respond = async (action: 'approve' | 'reject') => {
    if (!pending) return
    setApproving(true)
    const ok = await respondApproval(pending.id, action)
    setApproving(false)
    if (ok) setPending(null)
  }

  const dismissApproval = () => setPending(null)

  // ----------------------- 团队模式 -----------------------

  const handleTeamSend = async (text: string) => {
    const content = text.trim()
    if (!content || !selectedTeamId || teamStreaming) return

    const turnId = Date.now().toString()
    const newTurn: TeamTurnLocal = { id: turnId, teamId: selectedTeamId, user: content, agents: [], final: '', done: false }
    setTeamTurns(prev => [...prev, newTurn])
    setTeamInput('')
    setTeamStreaming(true)

    const history: { role: string; content: string }[] = []
    for (const t of teamTurns) {
      if (t.done && t.final) {
        history.push({ role: 'user', content: t.user })
        history.push({ role: 'assistant', content: t.final })
      }
    }

    const onEvent = (ev: TeamStreamEvent) => {
      setTeamTurns(prev => prev.map(t => {
        if (t.id !== turnId) return t
        switch (ev.type) {
          case 'agent_start': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) agents[idx] = { ...agents[idx], status: 'streaming' }
            else agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'streaming', text: '', toolCalls: [] })
            return { ...t, agents }
          }
          case 'token': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) agents[idx] = { ...agents[idx], text: agents[idx].text + ev.content, status: 'streaming' }
            else agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'streaming', text: ev.content, toolCalls: [] })
            return { ...t, agents }
          }
          case 'agent_end': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) agents[idx] = { ...agents[idx], text: ev.content, status: 'done' }
            else agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'done', text: ev.content, toolCalls: [] })
            return { ...t, agents }
          }
          case 'tool_call': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) {
              agents[idx] = {
                ...agents[idx],
                status: 'streaming',
                toolCalls: [...agents[idx].toolCalls, { id: ev.tool_name + '_' + Date.now(), name: ev.tool_name, args: ev.tool_args, result: '', status: 'running' }],
              }
            }
            return { ...t, agents }
          }
          case 'tool_result': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) {
              const toolCalls = [...agents[idx].toolCalls]
              for (let i = toolCalls.length - 1; i >= 0; i--) {
                if (toolCalls[i].name === ev.tool_name && toolCalls[i].status === 'running') {
                  toolCalls[i] = { ...toolCalls[i], result: ev.tool_result, status: 'done' }
                  break
                }
              }
              agents[idx] = { ...agents[idx], toolCalls }
            }
            return { ...t, agents }
          }
          case 'supervisor_review': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) agents[idx] = { ...agents[idx], supervisorNote: ev.content }
            else agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'done', text: '', toolCalls: [], supervisorNote: ev.content })
            return { ...t, agents }
          }
          case 'final':
            return { ...t, final: ev.content, done: ev.done }
          case 'error': {
            const agents = [...t.agents]
            if (ev.agent_id) {
              const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
              if (idx >= 0) agents[idx] = { ...agents[idx], status: 'error', error: ev.error }
            }
            return { ...t, error: ev.agent_id ? t.error : ev.error }
          }
          default:
            return t
        }
      }))
    }

    await teamApi.streamTeamChat(selectedTeamId, content, history, onEvent)
    setTeamStreaming(false)
  }

  const toggleThinking = () => {
    const v = !showThinking
    setShowThinking(v)
    if (showThinkingKey) { try { localStorage.setItem(showThinkingKey, String(v)) } catch { /* ignore */ } }
  }

  return {
    // 普通/自主
    mode, setMode,
    showThinking, toggleThinking,
    selectedModel, setSelectedModel,
    selectedAgent, setSelectedAgent,
    models, agents,
    messages, setMessages,
    input, setInput,
    streaming,
    send,
    clearSession,
    stopGeneration,
    // 审批
    pending, approving, respond, dismissApproval,
    // 会话
    convId, setConvId,
    // 团队
    selectedTeamId, setSelectedTeamId,
    teamTurns, setTeamTurns,
    teamInput, setTeamInput,
    teamStreaming,
    handleTeamSend,
    teams: teamApi.teams,
    teamsLoading: teamApi.loading,
  }
}

// ---------------------------------------------------------------------------
// ChatMessageList —— 消息渲染
// ---------------------------------------------------------------------------

interface ChatMessageListProps {
  messages: ChatMessage[]
  mode: ChatMode
  welcomeId?: string
  variant?: 'page' | 'panel' | 'floating'
  enableCitations?: boolean
  enableFeedback?: boolean
  enableTaskList?: boolean
  onFeedback?: (msg: ChatMessage, rating: 'up' | 'down') => void
  onRetry?: (msg: ChatMessage) => void
  extraActions?: (msg: ChatMessage) => ReactNode
  loading?: boolean
  loadingText?: string
  emptyQuickActions?: ReactNode
  endRef?: RefObject<HTMLDivElement>
  className?: string
}

export function ChatMessageList({
  messages, mode, welcomeId = 'welcome', variant = 'page',
  enableCitations = false, enableFeedback = false, enableTaskList = false,
  onFeedback, onRetry, extraActions, loading, loadingText, emptyQuickActions, endRef, className,
}: ChatMessageListProps) {
  const isPanel = variant === 'panel'
  const isFloating = variant === 'floating'

  return (
    <div className={cn('flex-1 overflow-y-auto space-y-3 min-h-0', className)}>
      {messages.map(msg => (
        <div key={msg.id} className={cn('flex gap-3', msg.role === 'user' ? 'justify-end' : 'justify-start')}>
          {msg.role === 'assistant' && (
            <div className={cn(
              'h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0',
              isFloating && 'h-7 w-7',
            )}>
              <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
            </div>
          )}
          <div className={cn(
            'rounded-lg px-4 py-2 text-sm',
            msg.role === 'user'
              ? isPanel || isFloating
                ? 'bg-blue-600 text-white rounded-br-sm whitespace-pre-wrap'
                : 'bg-blue-600 text-white whitespace-pre-wrap'
              : isPanel
                ? 'bg-white dark:bg-white/[0.04] text-gray-800 dark:text-gray-100 rounded-bl-sm border border-gray-200/60 dark:border-white/10'
                : isFloating
                  ? 'bg-gray-100 dark:bg-gray-800/80 text-gray-800 dark:text-gray-100 rounded-bl-sm border border-gray-200/60 dark:border-gray-700/50'
                  : 'bg-gray-100 dark:bg-gray-800 prose-sm max-w-none',
            isPanel ? 'max-w-[80%] rounded-2xl py-3' : isFloating ? 'max-w-[85%] rounded-2xl py-2.5 text-[13px] leading-relaxed' : 'max-w-[88%]',
          )}>
            {msg.role === 'assistant' ? (
              <>
                {/* 任务列表: 工具调用并发可视化(默认折叠) */}
                {enableTaskList && msg.toolCalls && msg.toolCalls.length > 0 && (
                  <details className="mb-2 group/task">
                    <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground flex items-center gap-1">
                      <Terminal className="h-3 w-3" />任务列表 ({msg.toolCalls.length})
                      {msg.toolCalls.some(t => t.status === 'running') && <Loader2 className="h-3 w-3 animate-spin text-blue-500" />}
                    </summary>
                    <div className="mt-1 space-y-1">
                      {msg.toolCalls.map(tc => (
                        <div key={tc.id} className={cn(
                          'flex items-center gap-2 text-xs rounded border px-2 py-1',
                          tc.status === 'running'
                            ? 'border-amber-300 bg-amber-50 dark:border-amber-500/30 dark:bg-amber-500/10'
                            : 'border-gray-200 dark:border-[rgba(255,255,255,0.06)]',
                        )}>
                          {tc.status === 'running'
                            ? <Loader2 className="h-3 w-3 animate-spin text-amber-500 shrink-0" />
                            : <CheckCircle className="h-3 w-3 text-blue-500 shrink-0" />}
                          <code className="font-mono font-semibold">{tc.name}</code>
                          <span className="text-[10px] text-muted-foreground truncate flex-1">{tc.status === 'done' ? (tc.result || '已完成') : '执行中…'}</span>
                        </div>
                      ))}
                    </div>
                  </details>
                )}

                {/* 思考过程: 默认折叠 */}
                {msg.thinking && (
                  <details className="mb-2">
                    <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground flex items-center gap-1">
                      <Brain className="h-3 w-3" />{variant === 'page' ? `思考过程（${parseThinkingSteps(msg.thinking).length} 步）` : '💭 推理过程'}
                    </summary>
                    <div className="mt-1 p-2 rounded bg-gray-200 dark:bg-gray-700 text-xs whitespace-pre-wrap text-muted-foreground">
                      {variant === 'page'
                        ? <ol className="list-decimal list-inside space-y-1">{parseThinkingSteps(msg.thinking).map((s, i) => <li key={i}>{s}</li>)}</ol>
                        : msg.thinking}
                    </div>
                  </details>
                )}

                {mode === 'explore'
                  ? <div dangerouslySetInnerHTML={{ __html: renderExploreContent(msg.content) }} />
                  : <AiMarkdown content={msg.content} />}

                {/* RAG 引用来源 */}
                {enableCitations && msg.citations && msg.citations.length > 0 && (
                  <details className="mt-2">
                    <summary className="text-[10px] text-muted-foreground cursor-pointer hover:text-foreground">📚 引用来源 ({msg.citations.length})</summary>
                    <div className="mt-1 space-y-0.5">
                      {msg.citations.map((c, i) => (
                        <div key={i} className="text-[10px] text-muted-foreground pl-2 border-l-2 border-blue-200 dark:border-blue-500/30">
                          {c.startsWith('[') ? c : `[${c}]`}
                        </div>
                      ))}
                    </div>
                  </details>
                )}

                {/* 失败态: 错误提示 + 重试 */}
                {msg.status === 'error' && (
                  <div className="mt-2 rounded-md border border-red-300 dark:border-red-500/40 bg-red-50 dark:bg-red-500/10 p-2">
                    <p className="text-[11px] text-red-600 dark:text-red-400 flex items-center gap-1">
                      <AlertTriangle className="h-3 w-3 shrink-0" />
                      {msg.errorText || 'AI 响应失败'}
                    </p>
                    {onRetry && (
                      <button onClick={() => onRetry(msg)} className="mt-1.5 text-[11px] text-red-600 dark:text-red-400 hover:underline font-medium">
                        ↻ 重试
                      </button>
                    )}
                  </div>
                )}

                {/* 反馈 + 额外操作 */}
                {(enableFeedback && onFeedback && msg.id !== welcomeId) || (extraActions && msg.id !== welcomeId) ? (
                  <div className="flex items-center gap-1 mt-2 pt-1.5 border-t border-gray-200 dark:border-[rgba(255,255,255,0.04)]">
                    {enableFeedback && onFeedback && msg.id !== welcomeId && (
                      <>
                        <button
                          onClick={() => { if (msg.feedback_rating !== 'up') onFeedback(msg, 'up') }}
                          disabled={!!msg.feedback_rating}
                          className={cn('p-1 rounded text-[10px] transition-colors', msg.feedback_rating === 'up' ? 'text-blue-500 bg-blue-50 dark:bg-blue-500/10' : 'text-muted-foreground hover:text-blue-500')}
                          title="有帮助"
                        >
                          <ThumbsUp className="h-3 w-3" />
                        </button>
                        <button
                          onClick={() => { if (msg.feedback_rating !== 'down') onFeedback(msg, 'down') }}
                          disabled={!!msg.feedback_rating}
                          className={cn('p-1 rounded text-[10px] transition-colors', msg.feedback_rating === 'down' ? 'text-red-500 bg-red-50 dark:bg-red-500/10' : 'text-muted-foreground hover:text-red-500')}
                          title="没帮助"
                        >
                          <ThumbsDown className="h-3 w-3" />
                        </button>
                        <span className="w-px h-3 bg-gray-200 dark:bg-gray-700 mx-0.5" />
                      </>
                    )}
                    {extraActions && msg.id !== welcomeId && extraActions(msg)}
                  </div>
                ) : null}
              </>
            ) : (
              <span className="whitespace-pre-wrap">{msg.content}</span>
            )}
          </div>
          {msg.role === 'user' && (
            <div className={cn('h-8 w-8 rounded-full bg-gray-200 dark:bg-gray-700 flex items-center justify-center shrink-0', isFloating && 'h-7 w-7')}>
              <User className="h-4 w-4 text-gray-600 dark:text-gray-300" />
            </div>
          )}
        </div>
      ))}

      {loading && (
        <div className="flex gap-3 justify-start">
          {!isFloating && (
            <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
              <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
            </div>
          )}
          <div className={cn('rounded-lg px-4 py-2 text-sm flex items-center gap-2', isPanel || isFloating ? 'bg-gray-100 dark:bg-gray-800/80' : 'bg-gray-100 dark:bg-gray-800')}>
            <Loader2 className="h-4 w-4 animate-spin" />
            <span>{loadingText || '思考中...'}</span>
          </div>
        </div>
      )}

      {/* 空态快捷操作 */}
      {emptyQuickActions && messages.length === 1 && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 mt-2">{emptyQuickActions}</div>
      )}

      <div ref={endRef} />
    </div>
  )
}

// ---------------------------------------------------------------------------
// ChatInputBar —— 输入区
// ---------------------------------------------------------------------------

interface QuickAction { label: string; prompt: string }

interface ChatInputBarProps {
  value: string
  onChange: (v: string) => void
  onSend: () => void
  onStop?: () => void
  streaming: boolean
  disabled?: boolean
  placeholder?: string
  mode: ChatMode
  onModeChange: (m: ChatMode) => void
  showThinking: boolean
  onToggleThinking: () => void
  modes?: ChatMode[]
  models?: ModelInfo[]
  selectedModel?: string
  onModelChange?: (v: string) => void
  agents?: any[]
  selectedAgent?: any
  onAgentChange?: (a: any) => void
  enableModel?: boolean
  enableAgent?: boolean
  enableThink?: boolean
  enableQuickActions?: boolean
  quickActions?: QuickAction[]
  onQuickSend?: (prompt: string) => void
  variant?: 'page' | 'panel' | 'floating'
  inputFooter?: ReactNode
  exploreHint?: boolean
  className?: string
}

export function ChatInputBar(props: ChatInputBarProps) {
  const {
    value, onChange, onSend, onStop, streaming, disabled, placeholder,
    mode, onModeChange, showThinking, onToggleThinking,
    modes = ['normal', 'explore', 'team'],
    models = [], selectedModel, onModelChange,
    agents = [], selectedAgent, onAgentChange,
    enableModel = false, enableAgent = false, enableThink = true,
    enableQuickActions = false, quickActions = [],
    onQuickSend,
    variant = 'page', inputFooter, exploreHint = false, className,
  } = props

  const isDark = variant === 'panel'
  const send = () => { if (value.trim()) onSend() }

  return (
    <div className={cn('border-t p-4 space-y-2 shrink-0', isDark && 'border-[rgba(255,255,255,0.04)]', variant === 'floating' && 'px-3 py-2.5', className)}>
      {/* 模式切换 */}
      <div className="flex gap-2">
        {modes.map(m => (
          <button
            key={m}
            onClick={() => onModeChange(m)}
            className={cn(
              'px-3 py-1 rounded-md text-xs border transition-colors flex items-center gap-1',
              mode === m
                ? 'bg-blue-50 border-blue-200 text-blue-700 dark:bg-blue-500/10 dark:border-blue-500/30 dark:text-blue-400'
                : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
            )}
            title={MODE_DESC[m]}
          >
            {MODE_LABEL[m]}模式
          </button>
        ))}
      </div>

      {/* 工具栏：模型 + 思考开关 + Agent */}
      {(enableModel || enableThink || enableAgent) && (
        <div className="flex flex-wrap items-center gap-2">
          {enableModel && models.length > 0 && (
            <select
              value={selectedModel || ''}
              onChange={e => onModelChange?.(e.target.value)}
              className={cn(
                'h-7 rounded-md border px-1.5 text-xs bg-transparent focus:outline-none focus:ring-1 focus:ring-blue-500/50',
                isDark ? 'border-white/10 text-gray-200' : 'border-gray-200 text-gray-700',
              )}
              title="运行级模型(空=按权重/健康度路由)"
            >
              <option value="">⚡ 自动（按权重/健康度路由）</option>
              {models.map(m => (
                <option key={`${m.name}-${m.model}`} value={m.model}>
                  {m.healthy === false ? '⚠️ ' : ''}{m.name !== 'default' ? `${m.name} · ${m.model}` : m.model}{m.healthy === false ? '（降级）' : ''}
                </option>
              ))}
            </select>
          )}
          {enableThink && (
            <button
              onClick={onToggleThinking}
              className={cn(
                'px-2 py-1 rounded text-xs border transition-colors flex items-center gap-1',
                showThinking
                  ? 'bg-purple-50 border-purple-200 text-purple-700 dark:bg-purple-500/10 dark:border-purple-500/30 dark:text-purple-400'
                  : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
              )}
              title="显示/隐藏 AI 思考过程（默认折叠展示，主动展开查看）"
            >
              💭 思考过程
            </button>
          )}
          {enableAgent && agents.length > 0 && (
            <select
              value={selectedAgent?.id || ''}
              onChange={e => onAgentChange?.(agents.find((a: any) => a.id === e.target.value) || null)}
              className={cn(
                'h-7 max-w-[180px] rounded-md border px-1.5 text-xs bg-transparent focus:outline-none focus:ring-1 focus:ring-blue-500/50',
                isDark ? 'border-white/10 text-gray-200' : 'border-gray-200 text-gray-700',
              )}
              title="选择专属 Agent"
            >
              <option value="">默认助手（无 Agent）</option>
              {agents.map((a: any) => <option key={a.id} value={a.id}>{a.name}</option>)}
            </select>
          )}
        </div>
      )}

      {/* 快捷操作（仅普通模式） */}
      {enableQuickActions && mode === 'normal' && !streaming && quickActions.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {quickActions.map(q => (
            <button
              key={q.label}
              onClick={() => onQuickSend?.(q.prompt)}
              className={cn(
                'px-2.5 py-1 rounded-full text-[11px] border transition-colors',
                isDark ? 'border-white/10 text-gray-300 hover:border-blue-500/40 hover:text-blue-400' : 'border-gray-200 text-gray-600 hover:border-blue-300 hover:text-blue-600',
              )}
            >
              {q.label}
            </button>
          ))}
        </div>
      )}

      <div className="flex gap-2 items-end">
        <textarea
          value={value}
          onChange={e => onChange(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() } }}
          placeholder={placeholder || (mode === 'explore' ? '给出目标，AI 自主规划执行…' : disabled ? '当前不可发送' : '输入你的问题...（Enter 发送，Shift+Enter 换行）')}
          disabled={disabled}
          rows={variant === 'page' ? 2 : 1}
          className={cn(
            'flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm resize-none placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:opacity-60',
            isDark && 'border-white/10 bg-white/[0.03] text-white focus-visible:ring-blue-500/50',
            variant === 'floating' && 'rounded-xl min-h-[38px] max-h-32',
          )}
        />
        <button
          onClick={streaming && onStop ? onStop : send}
          disabled={(!value.trim() && !streaming) || disabled}
          className={cn(
            'flex items-center justify-center rounded-md bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0',
            variant === 'page' ? 'h-9 w-9' : 'h-11 w-11 rounded-xl',
          )}
          title={streaming ? '停止生成' : '发送'}
          aria-label={streaming ? '停止生成' : '发送'}
        >
          {streaming ? <Square className="h-4 w-4" /> : <Send className="h-4 w-4" />}
        </button>
      </div>

      {exploreHint && mode === 'explore' && (
        <p className="text-[10px] text-muted-foreground flex items-center gap-1">
          <Sparkles className="h-3 w-3" /> 自主模式：AI 将规划步骤、调用工具并汇报结果
        </p>
      )}

      {inputFooter}
    </div>
  )
}

// ---------------------------------------------------------------------------
// TeamChatView —— 团队模式
// ---------------------------------------------------------------------------

interface TeamChatViewProps {
  teams: AgentTeam[]
  teamsLoading: boolean
  selectedTeamId: string | null
  onSelectTeam: (id: string) => void
  teamTurns: TeamTurnLocal[]
  teamInput: string
  onTeamInput: (v: string) => void
  teamStreaming: boolean
  onSend: (text: string) => void
  onManageTeams: () => void
  variant?: 'page' | 'panel'
  endRef?: RefObject<HTMLDivElement>
}

export function TeamChatView({
  teams, teamsLoading, selectedTeamId, onSelectTeam, teamTurns, teamInput, onTeamInput,
  teamStreaming, onSend, onManageTeams, variant = 'page', endRef,
}: TeamChatViewProps) {
  const isDark = variant === 'panel'
  return (
    <div className="flex-1 flex flex-col border rounded-lg overflow-hidden min-h-0">
      {/* 团队分段卡片选择 */}
      <div className={cn('border-b p-3', isDark ? 'border-white/10' : '')}>
        <div className="flex items-center gap-2 mb-2">
          <Users className="h-4 w-4 text-blue-400" />
          <span className="text-sm font-medium">选择团队</span>
          {teamsLoading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
        </div>
        {teams.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            暂无团队，<button className="text-blue-500 underline" onClick={onManageTeams}>前往创建</button>
          </p>
        ) : (
          <div className="flex gap-3 overflow-x-auto pb-1">
            {teams.map(team => (
              <button
                key={team.id}
                onClick={() => onSelectTeam(team.id)}
                className={cn(
                  'shrink-0 w-56 text-left rounded-xl border p-3 transition-colors',
                  selectedTeamId === team.id
                    ? isDark ? 'border-blue-400 bg-blue-500/10' : 'border-blue-400 bg-blue-50/60'
                    : isDark ? 'border-white/10 hover:border-white/20' : 'border-gray-200 hover:border-gray-300',
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm font-medium truncate">{team.name}</span>
                  <span className={cn('text-[10px] px-1.5 py-0.5 rounded shrink-0', isDark ? 'bg-white/10 text-gray-300' : 'bg-gray-100 text-gray-500')}>
                    {TEAM_MODE_LABEL[team.mode] || team.mode}
                  </span>
                </div>
                <p className="text-xs text-muted-foreground mt-1 line-clamp-2 h-8 overflow-hidden">{team.description || '无描述'}</p>
                <div className="flex items-center gap-1.5 mt-2 text-[11px] text-muted-foreground">
                  <Users className="h-3 w-3" /> {team.members.length} 名成员
                </div>
              </button>
            ))}
          </div>
        )}
      </div>

      {/* 对话流 */}
      <div className="flex-1 overflow-y-auto p-4 space-y-4">
        {teamTurns.length === 0 ? (
          <div className="h-full flex items-center justify-center">
            <p className="text-sm text-muted-foreground text-center max-w-xs">
              {selectedTeamId ? '向团队发送第一条消息，多智能体将协同作答（流式实时呈现）。' : '请先在上方选择一个团队。'}
            </p>
          </div>
        ) : (
          teamTurns.map(turn => (
            <div key={turn.id} className="space-y-3">
              <div className="flex justify-end">
                <div className="max-w-[70%] rounded-lg px-4 py-2 text-sm bg-blue-600 text-white whitespace-pre-wrap rounded-br-sm">{turn.user}</div>
              </div>

              {turn.agents.map(a => (
                <div
                  key={`${turn.id}-${a.agent_id}`}
                  className={cn(
                    'rounded-2xl px-4 py-3 text-sm border',
                    isDark ? 'border-white/10 bg-white/[0.04] text-gray-100' : 'border-gray-200/60 bg-white text-gray-800',
                  )}
                >
                  <div className="flex items-center gap-2 mb-1.5">
                    <span className={cn(
                      'text-[10px] px-1.5 py-0.5 rounded shrink-0',
                      a.status === 'error' ? 'bg-red-500/15 text-red-500'
                        : a.status === 'done' ? 'bg-blue-500/15 text-blue-500'
                        : 'bg-blue-500/15 text-blue-500',
                    )}>
                      {TEAM_ROLE_LABEL[a.role] || a.role} · {a.agent_name}
                    </span>
                    {a.status === 'streaming' && <Loader2 className="h-3 w-3 animate-spin text-blue-400" />}
                    {a.status === 'done' && <span className="text-[10px] text-blue-500">✓ 完成</span>}
                  </div>

                  {a.toolCalls.length > 0 && (
                    <details className="mb-1.5" open>
                      <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground">🔧 工具调用 ({a.toolCalls.length})</summary>
                      <div className="mt-1 space-y-1">
                        {a.toolCalls.map(tc => (
                          <div key={tc.id} className={cn(
                            'rounded border px-2 py-1 text-xs',
                            tc.status === 'running'
                              ? 'border-amber-300 bg-amber-50 dark:border-amber-500/30 dark:bg-amber-500/10'
                              : 'border-gray-200 dark:border-[rgba(255,255,255,0.06)]',
                          )}>
                            <div className="flex items-center justify-between">
                              <span className="font-mono text-[11px] font-semibold">{tc.name}</span>
                              {tc.status === 'running' && <Loader2 className="h-3 w-3 animate-spin text-amber-500" />}
                              {tc.status === 'done' && <span className="text-[10px] text-blue-500">✓</span>}
                            </div>
                            {tc.args && (
                              <details className="mt-0.5">
                                <summary className="text-[10px] text-muted-foreground cursor-pointer">参数</summary>
                                <pre className="mt-0.5 p-1 rounded bg-gray-100 dark:bg-gray-800 text-[10px] overflow-x-auto whitespace-pre-wrap max-h-20 overflow-y-auto">{tc.args}</pre>
                              </details>
                            )}
                            {tc.status === 'done' && tc.result && (
                              <details className="mt-0.5">
                                <summary className="text-[10px] text-muted-foreground cursor-pointer">结果</summary>
                                <pre className="mt-0.5 p-1 rounded bg-gray-100 dark:bg-gray-800 text-[10px] overflow-x-auto whitespace-pre-wrap max-h-24 overflow-y-auto">{tc.result}</pre>
                              </details>
                            )}
                          </div>
                        ))}
                      </div>
                    </details>
                  )}

                  {a.supervisorNote && (
                    <details className="mb-1.5" open>
                      <summary className="text-xs text-purple-600 dark:text-purple-400 cursor-pointer hover:underline">📋 主管审核</summary>
                      <div className="mt-1 p-2 rounded border border-purple-200 dark:border-purple-500/30 bg-purple-50 dark:bg-purple-500/10 text-xs whitespace-pre-wrap">{a.supervisorNote}</div>
                    </details>
                  )}

                  {a.status === 'error' ? (
                    <p className="text-xs text-red-500 whitespace-pre-wrap">{a.error}</p>
                  ) : (
                    <div className="text-xs text-gray-700 dark:text-[var(--foreground)] whitespace-pre-wrap" dangerouslySetInnerHTML={{ __html: renderMarkdown(a.text) }} />
                  )}
                </div>
              ))}

              {turn.final && (
                <div className="flex gap-3 justify-start">
                  <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
                    <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                  </div>
                  <div className={cn(
                    'max-w-[80%] rounded-lg px-4 py-2 text-sm border',
                    isDark ? 'border-white/10 bg-white/[0.04] text-gray-100' : 'border-gray-200/60 bg-white text-gray-800',
                  )}>
                    <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-500/15 text-blue-500 mb-1.5 inline-block">团队最终答复</span>
                    <div dangerouslySetInnerHTML={{ __html: renderMarkdown(turn.final) }} />
                  </div>
                </div>
              )}

              {turn.error && !turn.agents.some(a => a.status === 'error') && (
                <p className="text-xs text-red-500">⚠ {turn.error}</p>
              )}
            </div>
          ))
        )}
        <div ref={endRef} />
      </div>

      {/* 输入区 */}
      <div className={cn('border-t p-4', isDark && 'border-white/10')}>
        <div className="flex gap-2">
          <textarea
            value={teamInput}
            onChange={e => onTeamInput(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && !e.shiftKey && (e.preventDefault(), onSend(teamInput))}
            placeholder={selectedTeamId ? '向团队发送消息...' : '请先选择上方团队'}
            className={cn(
              'flex-1 resize-none rounded-xl border px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500/50 min-h-[44px] max-h-32',
              isDark ? 'border-white/10 bg-white/[0.03] text-white' : 'border-gray-200 bg-white text-gray-900',
            )}
            disabled={!selectedTeamId || teamStreaming}
          />
          <button
            onClick={() => onSend(teamInput)}
            disabled={!selectedTeamId || !teamInput.trim() || teamStreaming}
            className="flex h-11 w-11 items-center justify-center rounded-xl bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
            title="发送"
            aria-label="发送"
          >
            <Send className="h-4 w-4" />
          </button>
        </div>
        <div className="flex items-center justify-between mt-2">
          <button onClick={onManageTeams} className={cn(
            'flex items-center gap-1.5 py-1 rounded-md text-xs font-medium transition-colors',
            isDark ? 'text-gray-300 hover:bg-white/10' : 'text-gray-600 hover:bg-gray-100',
          )}>
            <Users className="h-3.5 w-3.5" />管理团队
          </button>
          {teamStreaming && (
            <span className="text-xs text-muted-foreground flex items-center gap-1">
              <Loader2 className="h-3 w-3 animate-spin" />团队响应中...
            </span>
          )}
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// ApprovalBar —— 敏感操作审批条(浮窗 / 纯净模式通用)
// ---------------------------------------------------------------------------

interface ApprovalBarProps {
  pending: PendingApproval | null
  approving: boolean
  onRespond: (action: 'approve' | 'reject') => void
  variant?: 'floating' | 'panel'
  className?: string
}

export function ApprovalBar({ pending, approving, onRespond, variant = 'panel', className }: ApprovalBarProps) {
  if (!pending) return null
  return (
    <div className={cn(
      'mx-3 mb-2 rounded-xl border border-amber-200 dark:border-amber-500/30 bg-amber-50 dark:bg-amber-500/10 px-3 py-2.5 shrink-0',
      variant === 'floating' && 'mx-3',
      className,
    )}>
      <div className="flex items-start gap-2">
        <AlertTriangle className="h-4 w-4 text-amber-500 mt-0.5 shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="text-xs font-medium text-amber-700 dark:text-amber-400">敏感操作需要确认</p>
          <p className="text-[11px] text-amber-600/90 dark:text-amber-500/80 mt-0.5">
            工具 <code className="font-mono">{pending.tool}</code>
            {pending.summary ? ` · ${pending.summary}` : ''}
          </p>
          {pending.args && pending.args !== '{}' && (
            <pre className="mt-1 max-h-16 overflow-auto rounded bg-amber-100/60 dark:bg-amber-500/10 px-2 py-1 text-[10px] whitespace-pre-wrap break-all">{pending.args}</pre>
          )}
          {pending.deadline && <p className="text-[10px] text-amber-500/70 mt-1">截止：{new Date(pending.deadline).toLocaleTimeString()}</p>}
        </div>
      </div>
      <div className="flex gap-2 mt-2 justify-end">
        <button onClick={() => onRespond('reject')} disabled={approving} className="flex items-center gap-1 px-3 py-1 rounded-md text-xs bg-gray-200/70 dark:bg-gray-700/60 text-gray-700 dark:text-gray-200 hover:bg-gray-300/70 dark:hover:bg-gray-600/60 disabled:opacity-50 transition-colors">
          <XCircle className="h-3 w-3" /> 拒绝
        </button>
        <button onClick={() => onRespond('approve')} disabled={approving} className="flex items-center gap-1 px-3 py-1 rounded-md text-xs bg-amber-500 text-white hover:bg-amber-600 disabled:opacity-50 transition-colors">
          {approving ? <Loader2 className="h-3 w-3 animate-spin" /> : <CheckCircle className="h-3 w-3" />} 批准执行
        </button>
      </div>
    </div>
  )
}
