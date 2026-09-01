
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAIStore } from '@/stores/ai'
import { useTeams } from '@/stores/ai/teams'
import type { PendingApproval, ChatMessage, TeamStreamEvent } from '@/stores/ai/types'
import { renderMarkdown, renderExploreContent } from '@/lib/ai-render'
import {
  Bot, X, Send, Loader2, Trash2, AlertTriangle, CheckCircle2, XCircle, Sparkles, Square,
  PanelLeftClose, ThumbsUp, ThumbsDown, History, BarChart3, Settings, Users, ChevronDown,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'
import usePathname from '@/lib/use-pathname'
import { AI_ASSISTANT_NAV } from '@/components/ai-nav'

// ============================================================================
// 纯净模式（AI 工作界面）
// 独立全屏布局：左侧功能区(AI 助手菜单) + 中间全屏 AI 对话区。
// 聊天逻辑复用 ai-assistant 的 streamChat / streamTeamChat 全链路，但拥有独立会话
// 与独立渲染，不依赖悬浮助手状态，避免互相干扰。
// 对话能力已对齐完整 AI 页(/ai)：普通 / 自主 / 团队 三模式齐全，Agent 选择、
// 快捷操作、会话历史、引用来源、反馈、分析/设置入口均为纯前端接线(后端能力早已齐备)。
// ============================================================================

interface PureMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  thinking?: string
  citations?: string[]
  messageId?: string
  feedback_rating?: 'up' | 'down'
  timestamp: number
}

type PureMode = 'normal' | 'explore' | 'team'

// —— 团队模式本地类型 ——
interface ToolCallLocal {
  id: string
  name: string
  args: string
  result: string
  status: 'running' | 'done'
}
interface AgentRunLocal {
  agent_id: string
  agent_name: string
  role: string
  status: 'streaming' | 'done' | 'error'
  text: string
  error?: string
  toolCalls: ToolCallLocal[]
  supervisorNote?: string
}
interface TeamTurnLocal {
  id: string
  teamId: string
  user: string
  agents: AgentRunLocal[]
  final: string
  done: boolean
  error?: string
}

const TEAM_MODE_LABEL: Record<string, string> = {
  supervisor: '主管调度', parallel: '并行协作', sequential: '顺序流转', debate: '辩论裁决',
}
const TEAM_ROLE_LABEL: Record<string, string> = {
  supervisor: '主管', member: '成员', finalizer: '终结者',
}

const SESSION_KEY = 'ai.pure.session'
const CONV_ID_KEY = 'ai.pure.convId'
const DEFAULT_KEY = 'opsorigin_pure_default'

const QUICK_ACTIONS = [
  { label: '查询主机状态', prompt: '帮我查询所有主机的运行状态' },
  { label: '执行巡检任务', prompt: '执行一次自动化巡检任务' },
  { label: '生成运维报告', prompt: '生成本周的运维报告' },
  { label: '排查故障', prompt: '帮我排查当前系统的异常情况' },
]

function newConvId(): string {
  return `pure_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

const WELCOME: PureMessage = {
  id: 'welcome',
  role: 'assistant',
  content: '欢迎进入 **纯净模式** 🤖\n\n这是一个以 AI 对话为核心的工作界面：\n- 左侧是「AI 助手」菜单，可一键进入场景包、数字员工、主动巡检、智能体团队等能力\n- 中间随时向我提问：查询主机、发起工单、分析告警、写知识库文章…\n- 右上角可切换「自主模式 / 团队模式」，给出目标由我规划执行或多人协作\n\n直接开始吧。',
  timestamp: Date.now(),
}

function loadSession(): PureMessage[] {
  try {
    const raw = localStorage.getItem(SESSION_KEY)
    if (!raw) return [WELCOME]
    const parsed = JSON.parse(raw) as { role: string; content: string }[]
    if (!Array.isArray(parsed) || parsed.length === 0) return [WELCOME]
    return parsed.map((m, i) => ({
      id: `m_${Date.now()}_${i}`,
      role: m.role === 'user' ? 'user' : 'assistant',
      content: m.content || '',
      timestamp: Date.now(),
    }))
  } catch {
    return [WELCOME]
  }
}

export default function PureMode({ onExitPure }: { onExitPure: () => void }) {
  const navigate = useNavigate()
  const pathname = usePathname()
  const { theme } = useThemeStore()
  const streamChat = useAIStore(s => s.streamChat)
  const respondApproval = useAIStore(s => s.respondApproval)
  const agents = useAIStore(s => s.agents)
  const fetchAgents = useAIStore(s => s.fetchAgents)
  const loadConversations = useAIStore(s => s.loadConversations)
  const submitFeedback = useAIStore(s => s.submitFeedback)
  const models = useAIStore(s => s.models)
  const fetchModels = useAIStore(s => s.fetchModels)
  // —— 团队模式数据流 ——
  const { teams, loading: teamsLoading, loadTeams, streamTeamChat } = useTeams()

  const [mode, setMode] = useState<PureMode>('normal')
  // —— 运行级模型选择(按会话/请求覆盖; 空=回退 agent/引擎默认) ——
  const [selectedModel, setSelectedModel] = useState('')
  const [showThinking, setShowThinking] = useState(true)
  const [messages, setMessages] = useState<PureMessage[]>(() =>
    typeof window !== 'undefined' ? loadSession() : [WELCOME],
  )
  const [input, setInput] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [pending, setPending] = useState<PendingApproval | null>(null)
  const [approving, setApproving] = useState(false)
  const [convId, setConvId] = useState<string>(() => {
    if (typeof window === 'undefined') return ''
    try { return localStorage.getItem(CONV_ID_KEY) || '' } catch { return '' }
  })
  // 登录后默认进入纯净模式偏好
  const [pureDefault, setPureDefault] = useState<boolean>(() => {
    try { return localStorage.getItem(DEFAULT_KEY) === '1' } catch { return false }
  })
  // —— 对齐 /ai 的对话能力 ——
  const [selectedAgent, setSelectedAgent] = useState<any>(null)
  const [showHistory, setShowHistory] = useState(false)
  const [conversations, setConversations] = useState<any[]>([])
  // —— 团队模式状态 ——
  const [selectedTeamId, setSelectedTeamId] = useState<string | null>(null)
  const [teamTurns, setTeamTurns] = useState<TeamTurnLocal[]>([])
  const [teamInput, setTeamInput] = useState('')
  const [teamStreaming, setTeamStreaming] = useState(false)

  const abortRef = useRef<AbortController | null>(null)
  const listEndRef = useRef<HTMLDivElement>(null)
  const teamMessagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    fetchAgents()
    fetchModels()
  }, [fetchAgents, fetchModels])

  useEffect(() => {
    if (mode === 'team') loadTeams()
  }, [mode, loadTeams])

  useEffect(() => {
    try {
      const slim = messages.filter(m => m.id !== 'welcome').map(m => ({ role: m.role, content: m.content }))
      localStorage.setItem(SESSION_KEY, JSON.stringify(slim))
    } catch { /* ignore */ }
  }, [messages])

  useEffect(() => {
    if (mode === 'team') teamMessagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
    else listEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, teamTurns, pending])

  useEffect(() => {
    setTimeout(() => inputRef.current?.focus(), 80)
  }, [])

  const persist = useCallback(() => setStreaming(false), [])

  // ----------------------- 普通 / 自主模式 -----------------------

  const doSend = async (raw: string) => {
    const content = raw.trim()
    if (!content || streaming) return

    abortRef.current?.abort()
    abortRef.current = new AbortController()

    const userMsg: PureMessage = { id: `u_${Date.now()}`, role: 'user', content, timestamp: Date.now() }
    const aiId = `a_${Date.now()}`
    const aiMsg: PureMessage = { id: aiId, role: 'assistant', content: '', thinking: '', timestamp: Date.now() }
    setMessages(prev => [...prev, userMsg, aiMsg])
    setStreaming(true)

    const history = [...messages, userMsg]
      .filter(m => m.id !== 'welcome' && (m.role === 'user' || m.role === 'assistant'))
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))

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

    let cid = convId
    if (!cid) {
      cid = newConvId()
      setConvId(cid)
      try { localStorage.setItem(CONV_ID_KEY, cid) } catch { /* ignore */ }
    }

    await streamChat(
      content,
      onToken,
      (meta) => {
        setMessages(prev => prev.map(m =>
          m.id === aiId ? { ...m, content: acc || '（无应答）', citations: meta?.citations, messageId: meta?.message_id } : m,
        ))
        persist()
      },
      history,
      selectedAgent?.id,
      showThinking,
      onThinking,
      mode === 'explore',
      onAsk,
      cid,
      abortRef.current.signal,
      selectedModel,
    )
  }

  const send = () => {
    const text = input
    setInput('')
    void doSend(text)
  }

  const clearSession = () => {
    if (streaming) return
    try {
      localStorage.removeItem(SESSION_KEY)
      localStorage.removeItem(CONV_ID_KEY)
    } catch { /* ignore */ }
    setMessages([WELCOME])
    setConvId('')
    setSelectedAgent(null)
  }

  const respond = async (action: 'approve' | 'reject') => {
    if (!pending) return
    setApproving(true)
    const ok = await respondApproval(pending.id, action)
    setApproving(false)
    if (ok) setPending(null)
  }

  const stopGeneration = () => {
    abortRef.current?.abort()
    setStreaming(false)
    abortRef.current = null
  }

  const togglePureDefault = () => {
    const v = !pureDefault
    setPureDefault(v)
    try { localStorage.setItem(DEFAULT_KEY, v ? '1' : '0') } catch { /* ignore */ }
  }

  // 点击功能区：先退出纯净模式(恢复普通布局)，再跳转对应模块
  const goto = (to: string) => {
    onExitPure()
    navigate(to)
  }

  // —— 会话历史：加载服务端会话列表 / 回载指定会话 ——
  const handleOpenHistory = async () => {
    if (!showHistory) {
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
    }
    setShowHistory(v => !v)
  }

  const handleLoadConversation = async (id: string) => {
    try {
      const resp = await fetch(`${window.location.origin}/api/v1/ai/conversations/${id}`)
      if (!resp.ok) return
      const conv = await resp.json()
      if (conv?.messages) {
        const msgs: PureMessage[] = conv.messages.map((m: any, i: number) => ({
          id: `h_${id}_${i}`,
          role: m.role === 'user' ? 'user' : 'assistant',
          content: m.content || '',
          timestamp: Date.now(),
        }))
        setMessages(msgs)
      }
      setConvId(id)
      setShowHistory(false)
    } catch { /* ignore */ }
  }

  // —— 反馈：点赞/点踩(精确关联后端 message_id) ——
  const submitFb = async (msg: PureMessage, rating: 'up' | 'down') => {
    if (!msg.messageId || msg.feedback_rating) return
    const ok = await submitFeedback({ message_id: msg.messageId, rating })
    if (ok) setMessages(prev => prev.map(m => (m.id === msg.id ? { ...m, feedback_rating: rating } : m)))
  }

  // ----------------------- 团队模式 -----------------------

  const handleTeamSend = async (text: string) => {
    const content = text.trim()
    if (!content || !selectedTeamId || teamStreaming) return

    const turnId = Date.now().toString()
    const newTurn: TeamTurnLocal = { id: turnId, teamId: selectedTeamId, user: content, agents: [], final: '', done: false }
    setTeamTurns(prev => [...prev, newTurn])
    setTeamInput('')
    setTeamStreaming(true)

    const history: ChatMessage[] = []
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
            if (idx >= 0) {
              agents[idx] = { ...agents[idx], supervisorNote: ev.content }
            } else {
              agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'done', text: '', toolCalls: [], supervisorNote: ev.content })
            }
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

    await streamTeamChat(selectedTeamId, content, history, onEvent)
    setTeamStreaming(false)
  }

  const isDark = theme === 'dark'

  return (
    <div className={cn('flex h-screen overflow-hidden', isDark ? 'bg-[var(--background)] text-white' : 'bg-gray-50 text-gray-900')}>
      {/* ============ 左侧功能区 ============ */}
      <aside className={cn(
        'relative z-10 w-60 flex flex-col border-r backdrop-blur-2xl shrink-0',
        isDark ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.8)]' : 'border-gray-200 bg-white/80',
      )}>
        <div className={cn(
          'h-14 flex items-center gap-2.5 px-4 border-b shrink-0',
          isDark ? 'border-[rgba(255,255,255,0.04)]' : 'border-gray-200',
        )}>
          <div className="relative flex items-center justify-center w-7 h-7">
            <Bot className="w-6 h-6 text-blue-400" strokeWidth={1.5} />
          </div>
          <div className="flex-1 min-w-0">
            <span className="text-sm font-semibold tracking-tight">纯净模式</span>
            <p className="text-[10px] text-muted-foreground tracking-wider uppercase">AI Workspace</p>
          </div>
          <button onClick={onExitPure} className="p-1.5 rounded-md transition-colors text-white bg-blue-500 hover:bg-blue-600" title="退出纯净模式">
            <PanelLeftClose className="h-4 w-4" />
          </button>
        </div>

        <nav className="flex-1 p-2 space-y-0.5 overflow-y-auto pt-3">
          <p className="px-3 pb-1 text-[10px] uppercase tracking-wider text-muted-foreground/70">AI 助手</p>
          {AI_ASSISTANT_NAV.map(item => {
            const Icon = item.icon
            const active = item.to === pathname || pathname.startsWith(item.to + '/')
            return (
              <button
                key={item.to}
                onClick={() => goto(item.to)}
                className={cn(
                  'group flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-all duration-200 w-full text-left',
                  active
                    ? isDark ? 'bg-blue-500/10 text-blue-400 border border-blue-500/20' : 'bg-blue-50 text-blue-600 border border-blue-200'
                    : isDark ? 'text-[var(--secondary-foreground)] hover:text-white hover:bg-white/[0.03]' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100',
                )}
              >
                <Icon className="h-4 w-4" strokeWidth={1.5} />
                <span className="text-xs font-medium tracking-wide">{item.label}</span>
              </button>
            )
          })}
        </nav>

        <div className={cn(
          'px-4 py-3 border-t space-y-3 shrink-0',
          isDark ? 'border-[rgba(255,255,255,0.04)]' : 'border-gray-200',
        )}>
          <label className="flex items-center justify-between gap-2 cursor-pointer select-none">
            <span className="text-[11px] text-muted-foreground leading-tight">登录后默认<br />进入纯净模式</span>
            <button
              type="button"
              role="switch"
              aria-checked={pureDefault}
              onClick={togglePureDefault}
              className={cn(
                'relative h-5 w-9 rounded-full transition-colors shrink-0',
                pureDefault ? 'bg-blue-500' : isDark ? 'bg-white/15' : 'bg-gray-300',
              )}
            >
              <span className={cn('absolute top-0.5 h-4 w-4 rounded-full bg-white transition-transform', pureDefault ? 'translate-x-4' : 'translate-x-0.5')} />
            </button>
          </label>
          <button onClick={onExitPure} className="w-full flex items-center justify-center gap-1.5 py-1.5 rounded-lg text-xs font-semibold text-white bg-blue-500 hover:bg-blue-600 transition-colors shadow-sm">
            <X className="h-3.5 w-3.5" /> 退出纯净模式
          </button>
        </div>
      </aside>

      {/* ============ 中间 AI 对话区 ============ */}
      <main className="relative z-10 flex-1 flex flex-col min-w-0">
        {/* 头部 */}
        <div className={cn(
          'h-14 flex items-center justify-between px-5 border-b shrink-0 gap-2',
          isDark ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.6)]' : 'border-gray-200 bg-white/70',
        )}>
          <div className="flex items-center gap-2 min-w-0">
            <span className="text-sm font-semibold shrink-0">AI 工作区</span>
            <span className={cn('text-[10px] px-1.5 py-0.5 rounded shrink-0', isDark ? 'bg-white/10 text-gray-300' : 'bg-gray-100 text-gray-500')}>OpsOrigin</span>
            {/* 模式切换：普通 / 自主 / 团队 */}
            <div className={cn('flex gap-1 rounded-lg p-0.5 ml-1', isDark ? 'bg-white/5' : 'bg-gray-100')}>
              {(['normal', 'explore', 'team'] as PureMode[]).map(m => (
                <button
                  key={m}
                  onClick={() => setMode(m)}
                  className={cn(
                    'px-2.5 py-1 rounded-md text-xs font-medium transition-colors',
                    mode === m
                      ? 'bg-blue-500 text-white shadow-sm'
                      : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {m === 'normal' ? '普通' : m === 'explore' ? '自主' : '团队'}
                </button>
              ))}
            </div>
            {/* Agent 选择（团队模式不展示） */}
            {mode !== 'team' && (
              <select
                value={selectedAgent?.id || ''}
                onChange={e => {
                  const a = agents.find((x: any) => x.id === e.target.value) || null
                  setSelectedAgent(a)
                }}
                className={cn(
                  'ml-1 h-7 max-w-[160px] rounded-md border px-1.5 text-xs bg-transparent focus:outline-none focus:ring-1 focus:ring-blue-500/50',
                  isDark ? 'border-white/10 text-gray-200' : 'border-gray-200 text-gray-700',
                )}
                title="选择专属 Agent"
              >
                <option value="">默认 Agent</option>
                {agents.map((a: any) => (
                  <option key={a.id} value={a.id}>{a.name}</option>
                ))}
              </select>
            )}
          </div>
          <div className="flex items-center gap-1.5 shrink-0">
            {mode === 'normal' && (
              <button onClick={handleOpenHistory} className={cn(
                'p-1.5 rounded-md transition-colors text-muted-foreground hover:text-foreground hover:bg-black/5 dark:hover:bg-white/10',
                showHistory && 'text-blue-500',
              )} title="会话历史">
                <History className="h-3.5 w-3.5" />
              </button>
            )}
            <button onClick={() => goto('/ai/analytics')} className="p-1.5 rounded-md transition-colors text-muted-foreground hover:text-foreground hover:bg-black/5 dark:hover:bg-white/10" title="AI 分析">
              <BarChart3 className="h-3.5 w-3.5" />
            </button>
            <button onClick={() => goto('/ai/settings')} className="p-1.5 rounded-md transition-colors text-muted-foreground hover:text-foreground hover:bg-black/5 dark:hover:bg-white/10" title="智能体工作台">
              <Settings className="h-3.5 w-3.5" />
            </button>
            {/* 思考过程开关（团队模式不展示） */}
            {mode !== 'team' && (
              <button
                onClick={() => setShowThinking(v => !v)}
                className={cn(
                  'px-2 py-1 rounded-md text-[11px] font-medium transition-colors',
                  showThinking
                    ? 'bg-purple-500/20 text-purple-600 dark:text-purple-300'
                    : 'text-muted-foreground hover:bg-black/5 dark:hover:bg-white/10',
                )}
                title="显示/隐藏 AI 思考过程"
              >
                💭 思考
              </button>
            )}
            <button onClick={clearSession} className="p-1.5 rounded-md transition-colors text-muted-foreground hover:text-foreground hover:bg-black/5 dark:hover:bg-white/10" title="清空会话">
              <Trash2 className="h-3.5 w-3.5" />
            </button>
            <button onClick={onExitPure} className="p-1.5 rounded-md transition-colors text-white bg-blue-500 hover:bg-blue-600 md:hidden" title="退出纯净模式">
              <X className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* ============ 普通 / 自主 模式主体 ============ */}
        {mode !== 'team' && (
          <>
            {/* 消息区 */}
            <div className="flex-1 overflow-y-auto px-5 py-5 space-y-3 min-h-0">
              {messages.map(m => (
                <div key={m.id} className={cn('flex', m.role === 'user' ? 'justify-end' : 'justify-start')}>
                  <div
                    className={cn(
                      'max-w-[80%] rounded-2xl px-4 py-3 text-sm leading-relaxed',
                      m.role === 'user'
                        ? 'bg-blue-600 text-white rounded-br-sm'
                        : cn('rounded-bl-sm border', isDark ? 'bg-white/[0.04] text-gray-100 border-white/10' : 'bg-white text-gray-800 border-gray-200/60'),
                    )}
                  >
                    {m.role === 'assistant' && m.thinking ? (
                      <details className="mb-2 text-[11px] text-muted-foreground">
                        <summary className="cursor-pointer select-none hover:text-foreground">💭 推理过程</summary>
                        <div className="mt-1 whitespace-pre-wrap border-t border-gray-200 dark:border-gray-700 pt-1.5 max-h-48 overflow-y-auto">
                          {m.thinking}
                        </div>
                      </details>
                    ) : null}
                    {m.role === 'assistant' ? (
                      <div dangerouslySetInnerHTML={{ __html: mode === 'explore' ? renderExploreContent(m.content) : renderMarkdown(m.content) }} />
                    ) : (
                      <span className="whitespace-pre-wrap">{m.content}</span>
                    )}

                    {/* 引用来源 (RAG) */}
                    {m.role === 'assistant' && m.citations && m.citations.length > 0 && (
                      <details className="mt-2">
                        <summary className="text-[10px] text-muted-foreground cursor-pointer hover:text-foreground">📚 引用来源 ({m.citations.length})</summary>
                        <div className="mt-1 space-y-0.5">
                          {m.citations.map((c, i) => (
                            <div key={i} className="text-[10px] text-muted-foreground pl-2 border-l-2 border-blue-200 dark:border-blue-500/30">
                              {c.startsWith('[') ? c : `[${c}]`}
                            </div>
                          ))}
                        </div>
                      </details>
                    )}

                    {/* 反馈按钮 */}
                    {m.role === 'assistant' && m.id !== 'welcome' && (
                      <div className="flex items-center gap-1 mt-2 pt-1.5 border-t border-gray-200 dark:border-[rgba(255,255,255,0.04)]">
                        <button
                          onClick={() => submitFb(m, 'up')}
                          disabled={!!m.feedback_rating}
                          className={cn('p-1 rounded text-[10px] transition-colors', m.feedback_rating === 'up' ? 'text-blue-500 bg-blue-50 dark:bg-blue-500/10' : 'text-muted-foreground hover:text-blue-500')}
                          title="有帮助"
                        >
                          <ThumbsUp className="h-3 w-3" />
                        </button>
                        <button
                          onClick={() => submitFb(m, 'down')}
                          disabled={!!m.feedback_rating}
                          className={cn('p-1 rounded text-[10px] transition-colors', m.feedback_rating === 'down' ? 'text-red-500 bg-red-50 dark:bg-red-500/10' : 'text-muted-foreground hover:text-red-500')}
                          title="没帮助"
                        >
                          <ThumbsDown className="h-3 w-3" />
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              ))}
              {streaming && (
                <div className="flex items-center gap-1.5 text-xs text-muted-foreground pl-1">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  <span>{mode === 'explore' ? 'AI 正在规划并执行…' : 'AI 正在思考…'}</span>
                </div>
              )}
              <div ref={listEndRef} />
            </div>

            {/* 会话历史抽屉 */}
            {showHistory && (
              <div className="absolute inset-0 z-20 flex">
                <div className="w-72 h-full overflow-y-auto p-3 space-y-2 shrink-0 backdrop-blur-2xl"
                  style={{ background: isDark ? 'rgba(11,15,25,0.96)' : 'rgba(255,255,255,0.97)', borderColor: isDark ? 'var(--border)' : 'var(--border)', borderRightWidth: 1, borderRightStyle: 'solid' }}>
                  <div className="flex items-center justify-between mb-2">
                    <span className="text-sm font-medium">会话历史</span>
                    <button onClick={() => setShowHistory(false)} className="p-1 rounded hover:bg-black/5 dark:hover:bg-white/10 text-muted-foreground" title="收起">
                      <X className="h-4 w-4" />
                    </button>
                  </div>
                  {conversations.length === 0 ? (
                    <p className="text-xs text-muted-foreground">暂无对话记录</p>
                  ) : (
                    conversations.map(c => (
                      <button
                        key={c.id}
                        onClick={() => handleLoadConversation(c.id)}
                        className="w-full text-left p-3 rounded-lg bg-gray-50 dark:bg-gray-800 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                      >
                        <p className="text-sm font-medium truncate">{c.title || '新对话'}</p>
                        <p className="text-xs text-muted-foreground mt-0.5">{new Date(c.updated_at || c.UpdatedAt).toLocaleString('zh-CN')}</p>
                      </button>
                    ))
                  )}
                </div>
                <button className="flex-1 h-full" onClick={() => setShowHistory(false)} aria-label="关闭历史" />
              </div>
            )}

            {/* 输入区 */}
            <div className={cn('px-5 py-3 border-t shrink-0', isDark ? 'border-[rgba(255,255,255,0.04)]' : 'border-gray-200')}>
              {/* 快捷操作（仅普通模式） */}
              {mode === 'normal' && !streaming && (
                <div className="flex flex-wrap gap-1.5 mb-2">
                  {QUICK_ACTIONS.map(q => (
                    <button
                      key={q.label}
                      onClick={() => doSend(q.prompt)}
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
              {models.length > 0 && (
                <div className="relative mb-2 inline-block">
                  <select
                    value={selectedModel}
                    onChange={e => setSelectedModel(e.target.value)}
                    className={cn(
                      'appearance-none h-7 pl-2.5 pr-7 rounded-full border text-xs cursor-pointer focus:outline-none focus:ring-1 focus:ring-blue-500',
                      isDark ? 'border-white/10 bg-white/[0.03] text-white' : 'border-gray-200 bg-white text-gray-900',
                    )}
                  >
                    <option value="">默认</option>
                    {models.map(m => (
                      <option key={`${m.name}-${m.model}`} value={m.model}>{m.name !== 'default' ? `${m.name} · ${m.model}` : m.model}</option>
                    ))}
                  </select>
                  <ChevronDown className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 h-3 w-3 text-muted-foreground" />
                </div>
              )}
              <div className="flex items-end gap-2">
                <textarea
                  ref={inputRef}
                  value={input}
                  onChange={e => setInput(e.target.value)}
                  onKeyDown={e => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      send()
                    }
                  }}
                  rows={1}
                  placeholder={mode === 'explore' ? '给出目标，AI 自主规划执行…' : '输入问题，Enter 发送（Shift+Enter 换行）…'}
                  className={cn(
                    'flex-1 resize-none rounded-xl border px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500/50 min-h-[44px] max-h-32',
                    isDark ? 'border-white/10 bg-white/[0.03] text-white' : 'border-gray-200 bg-white text-gray-900',
                  )}
                />
                <button
                  onClick={streaming ? stopGeneration : send}
                  disabled={!input.trim() && !streaming}
                  className="flex h-11 w-11 items-center justify-center rounded-xl bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
                  title={streaming ? '停止生成' : '发送'}
                  aria-label={streaming ? '停止生成' : '发送'}
                >
                  {streaming ? <Square className="h-4 w-4" /> : <Send className="h-4 w-4" />}
                </button>
              </div>
              {mode === 'explore' && (
                <p className="mt-1.5 text-[10px] text-muted-foreground flex items-center gap-1">
                  <Sparkles className="h-3 w-3" /> 自主模式：AI 将规划步骤、调用工具并汇报结果
                </p>
              )}
            </div>
          </>
        )}

        {/* ============ 团队模式主体 ============ */}
        {mode === 'team' && (
          <>
            {/* 团队选择卡 */}
            <div className={cn('border-b p-3 shrink-0', isDark ? 'border-white/10' : 'border-gray-200')}>
              <div className="flex items-center gap-2 mb-2">
                <Users className="h-4 w-4 text-blue-400" />
                <span className="text-sm font-medium">选择团队</span>
                {teamsLoading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
              </div>
              {teams.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  暂无团队，<button className="text-blue-500 underline" onClick={() => goto('/ai/teams')}>前往创建</button>
                </p>
              ) : (
                <div className="flex gap-3 overflow-x-auto pb-1">
                  {teams.map(team => (
                    <button
                      key={team.id}
                      onClick={() => setSelectedTeamId(team.id)}
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
            <div className="flex-1 overflow-y-auto px-5 py-5 space-y-3 min-h-0">
              {teamTurns.length === 0 ? (
                <div className="h-full flex items-center justify-center">
                  <p className="text-sm text-muted-foreground text-center max-w-xs">
                    {selectedTeamId ? '向团队发送第一条消息，多智能体将协同作答（流式实时呈现）。' : '请先在上方选择一个团队。'}
                  </p>
                </div>
              ) : (
                teamTurns.map(turn => (
                  <div key={turn.id} className="space-y-3">
                    {/* 用户消息 */}
                    <div className="flex justify-end">
                      <div className="max-w-[70%] rounded-2xl px-4 py-2 text-sm bg-blue-600 text-white whitespace-pre-wrap rounded-br-sm">
                        {turn.user}
                      </div>
                    </div>

                    {/* 各 agent 的流式输出 */}
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

                        {/* 工具调用过程（可观测面板） */}
                        {a.toolCalls.length > 0 && (
                          <details className="mb-1.5" open>
                            <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground">
                              🔧 工具调用 ({a.toolCalls.length})
                            </summary>
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

                        {/* 主管审核备注 */}
                        {a.supervisorNote && (
                          <details className="mb-1.5" open>
                            <summary className="text-xs text-purple-600 dark:text-purple-400 cursor-pointer hover:underline">📋 主管审核</summary>
                            <div className="mt-1 p-2 rounded border border-purple-200 dark:border-purple-500/30 bg-purple-50 dark:bg-purple-500/10 text-xs whitespace-pre-wrap">
                              {a.supervisorNote}
                            </div>
                          </details>
                        )}

                        {a.status === 'error' ? (
                          <p className="text-xs text-red-500 whitespace-pre-wrap">{a.error}</p>
                        ) : (
                          <div
                            className="text-xs text-gray-700 dark:text-[var(--foreground)] whitespace-pre-wrap"
                            dangerouslySetInnerHTML={{ __html: renderMarkdown(a.text) }}
                          />
                        )}
                      </div>
                    ))}

                    {/* 团队最终答复 */}
                    {turn.final && (
                      <div className="flex gap-3 justify-start">
                        <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
                          <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                        </div>
                        <div className={cn(
                          'max-w-[80%] rounded-2xl px-4 py-2 text-sm border',
                          isDark ? 'border-white/10 bg-white/[0.04] text-gray-100' : 'border-gray-200/60 bg-white text-gray-800',
                        )}>
                          <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-500/15 text-blue-500 mb-1.5 inline-block">团队最终答复</span>
                          <div dangerouslySetInnerHTML={{ __html: renderMarkdown(turn.final) }} />
                        </div>
                      </div>
                    )}

                    {/* 团队级错误（无 agent 维度） */}
                    {turn.error && !turn.agents.some(a => a.status === 'error') && (
                      <p className="text-xs text-red-500">⚠ {turn.error}</p>
                    )}
                  </div>
                ))
              )}
              <div ref={teamMessagesEndRef} />
            </div>

            {/* 输入区 */}
            <div className={cn('px-5 py-3 border-t shrink-0', isDark ? 'border-white/10' : 'border-gray-200')}>
              <div className="flex items-end gap-2">
                <textarea
                  value={teamInput}
                  onChange={e => setTeamInput(e.target.value)}
                  onKeyDown={e => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      void handleTeamSend(teamInput)
                    }
                  }}
                  rows={1}
                  placeholder={selectedTeamId ? '向团队发送消息...' : '请先选择上方团队'}
                  disabled={!selectedTeamId || teamStreaming}
                  className={cn(
                    'flex-1 resize-none rounded-xl border px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500/50 min-h-[44px] max-h-32',
                    isDark ? 'border-white/10 bg-white/[0.03] text-white' : 'border-gray-200 bg-white text-gray-900',
                  )}
                />
                <button
                  onClick={() => void handleTeamSend(teamInput)}
                  disabled={!selectedTeamId || !teamInput.trim() || teamStreaming}
                  className="flex h-11 w-11 items-center justify-center rounded-xl bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
                  title="发送"
                  aria-label="发送"
                >
                  <Send className="h-4 w-4" />
                </button>
              </div>
              <div className="flex items-center justify-between mt-2">
                <button onClick={() => goto('/ai/teams')} className={cn(
                  'flex items-center gap-1.5 py-1 rounded-md text-xs font-medium transition-colors',
                  isDark ? 'text-gray-300 hover:bg-white/10' : 'text-gray-600 hover:bg-gray-100',
                )}>
                  <Settings className="h-3.5 w-3.5" />管理团队
                </button>
                {teamStreaming && (
                  <span className="text-xs text-muted-foreground flex items-center gap-1">
                    <Loader2 className="h-3 w-3 animate-spin" />团队响应中...
                  </span>
                )}
              </div>
            </div>
          </>
        )}

        {/* 审批确认条（所有模式通用） */}
        {pending && (
          <div className="mx-5 mb-2 rounded-xl border border-amber-200 dark:border-amber-500/30 bg-amber-50 dark:bg-amber-500/10 px-3 py-2.5 shrink-0">
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
              <button onClick={() => respond('reject')} disabled={approving} className="flex items-center gap-1 px-3 py-1 rounded-md text-xs bg-gray-200/70 dark:bg-gray-700/60 text-gray-700 dark:text-gray-200 hover:bg-gray-300/70 dark:hover:bg-gray-600/60 disabled:opacity-50 transition-colors">
                <XCircle className="h-3 w-3" /> 拒绝
              </button>
              <button onClick={() => respond('approve')} disabled={approving} className="flex items-center gap-1 px-3 py-1 rounded-md text-xs bg-amber-500 text-white hover:bg-amber-600 disabled:opacity-50 transition-colors">
                {approving ? <Loader2 className="h-3 w-3 animate-spin" /> : <CheckCircle2 className="h-3 w-3" />} 批准执行
              </button>
            </div>
          </div>
        )}
      </main>
    </div>
  )
}
