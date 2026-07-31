'use client'

import { useEffect, useState, useRef } from 'react'
import { useRouter } from 'next/navigation'
import { useAIStore } from '@/stores/ai'
import { useTeams, type ChatMessage, type TeamStreamEvent } from '@/stores/ai'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Bot, Send, History, Settings, Sparkles, Terminal, FileText, Server, ChevronRight, Loader2, User, BarChart3, Users, ThumbsUp, ThumbsDown, Rocket, Target, AlertTriangle, CheckCircle, XCircle } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'
import { renderMarkdown, renderExploreContent } from '@/lib/ai-render'

// ============================================================================
// 类型与本地工具
// ============================================================================

interface Message {
  id: string
  role: 'user' | 'assistant'
  content: string
  thinking?: string
  citations?: string[]
  feedback_rating?: 'up' | 'down'
  timestamp: Date
}

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

type AIMode = 'normal' | 'explore' | 'team'

const QUICK_ACTIONS = [
  { label: '查询主机状态', icon: Server, prompt: '帮我查询所有主机的运行状态' },
  { label: '执行巡检任务', icon: Terminal, prompt: '执行一次自动化巡检任务' },
  { label: '生成运维报告', icon: FileText, prompt: '生成本周的运维报告' },
  { label: '排查故障', icon: Sparkles, prompt: '帮我排查当前系统的异常情况' },
]

const modeLabelMap: Record<AIMode, string> = { normal: '普通', explore: '自主', team: '团队' }
const teamModeBadge: Record<string, 'default' | 'info' | 'warning' | 'destructive'> = {
  supervisor: 'default', parallel: 'info', sequential: 'warning', debate: 'destructive',
}
const teamModeLabel: Record<string, string> = {
  supervisor: '主管调度', parallel: '并行协作', sequential: '顺序流转', debate: '辩论裁决',
}
const teamRoleLabel: Record<string, string> = {
  supervisor: '主管', member: '成员', finalizer: '终结者',
}

// ============================================================================
// 页面
// ============================================================================

export default function AIPage() {
  const { agents, fetchAgents, streamChat, loadConversations, submitFeedback: submitFB } = useAIStore()
  const { teams, loading: teamsLoading, loadTeams, streamTeamChat } = useTeams()
  const router = useRouter()
  const { theme } = useThemeStore()

  const [mode, setMode] = useState<AIMode>('normal')

  // —— 全局思考过程开关(localStorage 持久化) ——
  const [showThinking, setShowThinking] = useState<boolean>(() => {
    if (typeof window === 'undefined') return true
    return localStorage.getItem('ai.showThinking') !== 'false'
  })

  // —— 普通模式状态 ——
  const [messages, setMessages] = useState<Message[]>([
    { id: '0', role: 'assistant', content: '你好！我是 OpsOrigin AI 助手，可以帮你完成运维任务。\n\n👉 **快速开始：** 点击右上角「设置」→「模型」配置 LLM API Key 即可使用。\n\n配置完成后可直接提问，或选择右侧快捷操作。', timestamp: new Date() },
  ])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [selectedAgent, setSelectedAgent] = useState<any>(null)
  const [showHistory, setShowHistory] = useState(false)
  const [conversations, setConversations] = useState<any[]>([])
  // 当前会话 ID: 加载历史后指向该会话, 继续发送时携带以续接服务端持久历史
  const [convId, setConvId] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)

  // —— 团队模式状态 ——
  const [selectedTeamId, setSelectedTeamId] = useState<string | null>(null)
  const [teamTurns, setTeamTurns] = useState<TeamTurnLocal[]>([])
  const [teamInput, setTeamInput] = useState('')
  const [teamStreaming, setTeamStreaming] = useState(false)
  const teamMessagesEndRef = useRef<HTMLDivElement>(null)

  // —— 自主模式状态 ——
  const [exploreRuns, setExploreRuns] = useState<{ id: string; goal: string; content: string; status: 'running' | 'done' | 'error'; error?: string }[]>([])
  const [exploreInput, setExploreInput] = useState('')
  const [exploreStreaming, setExploreStreaming] = useState(false)
  const exploreEndRef = useRef<HTMLDivElement>(null)

  // —— 敏感操作审批弹窗 ——
  const [pendingApproval, setPendingApproval] = useState<any>(null)
  const [approvalBusy, setApprovalBusy] = useState(false)
  const { respondApproval } = useAIStore()

  useEffect(() => {
    fetchAgents()
    if (mode === 'team') loadTeams()
  }, [fetchAgents, mode, loadTeams])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])
  useEffect(() => {
    teamMessagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [teamTurns])
  useEffect(() => {
    exploreEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [exploreRuns])

  // ----------------------- 普通 / 思维链 模式 -----------------------

  const handleOpenHistory = async () => {
    if (!showHistory) {
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
    }
    setShowHistory(!showHistory)
  }

  const handleLoadConversation = async (id: string) => {
    try {
      const resp = await fetch(`${window.location.origin}/api/v1/ai/conversations/${id}`)
      if (!resp.ok) return
      const conv = await resp.json()
      if (conv?.messages) {
        const msgs: Message[] = conv.messages.map((m: any, i: number) => ({
          id: `${id}-${i}`,
          role: m.role,
          content: m.content,
          timestamp: new Date(),
        }))
        setMessages(msgs)
      }
      // 绑定当前会话: 后续发送将携带该 conversation_id, 续接服务端可信历史(含 tool 消息)
      setConvId(id)
      setShowHistory(false)
    } catch {}
  }

  // submitFeedback 上报点赞/点踩
  const submitFeedback = async (msg: Message, rating: 'up' | 'down' = 'up') => {
    await submitFB({ message_id: msg.id, rating })
  }

  const sendMessage = async (text: string) => {
    const content = text.trim()
    if (!content || loading) return

    const userMsg: Message = { id: Date.now().toString(), role: 'user', content, timestamp: new Date() }
    const next = [...messages, userMsg]
    setMessages(next)
    setInput('')
    setLoading(true)

    const aiId = (Date.now() + 1).toString()
    const aiMsg: Message = { id: aiId, role: 'assistant', content: '', timestamp: new Date() }
    setMessages(prev => [...prev, aiMsg])

    const history = next
      .filter(m => m.role === 'user' || m.role === 'assistant')
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))

    // 会话续接: 已有会话(点历史加载)则携带其 ID; 否则本地生成新会话 ID, 后端按 ID 自动创建持久化
    let cid = convId
    if (!cid) {
      cid = `ai_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
      setConvId(cid)
    }

    let full = ''
    let thinkingAcc = ''
    await streamChat(content, (token) => {
      full += token
      setMessages(prev => prev.map(m => m.id === aiId ? { ...m, content: full } : m))
    }, (meta) => {
      if (meta?.citations && meta.citations.length > 0) {
        setMessages(prev => prev.map(m => m.id === aiId ? { ...m, content: full, citations: meta.citations } : m))
      }
      setLoading(false)
    }, history, selectedAgent?.id, showThinking, showThinking ? (t) => {
      thinkingAcc += t
      setMessages(prev => prev.map(m => m.id === aiId ? { ...m, thinking: thinkingAcc } : m))
    } : undefined, undefined, (approval) => setPendingApproval(approval), cid)
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

    // 把历史上已完成轮次拼成历史上下文
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
              // 逆序找到最后一个 running 的同名工具调用, 标记完成
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
            }
            else {
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

  // ----------------------- 自主模式 -----------------------

  const handleExploreSend = async (text: string) => {
    const goal = text.trim()
    if (!goal || exploreStreaming) return

    const runId = Date.now().toString()
    const newRun = { id: runId, goal, content: '', status: 'running' as const }
    setExploreRuns(prev => [...prev, newRun])
    setExploreInput('')
    setExploreStreaming(true)

    let acc = ''
    let thinkingAcc = ''
    await streamChat(goal, (token) => {
      acc += token
      setExploreRuns(prev => prev.map(r => r.id === runId ? { ...r, content: acc } : r))
    }, (meta) => {
      // 结束时把思考过程以 <think> 标签前置展示(可折叠)
      const final = thinkingAcc ? `<think>${thinkingAcc}</think>\n\n${acc}` : acc
      setExploreRuns(prev => prev.map(r => r.id === runId ? { ...r, content: final, status: 'done' as const } : r))
      setExploreStreaming(false)
    }, [], undefined, showThinking, showThinking ? (t) => {
      thinkingAcc += t
    } : undefined, true, (approval) => setPendingApproval(approval))
  }

  const EXPLORE_EXAMPLES = [
    '检查所有主机的运行状态，找出异常主机并给出处理建议',
    '梳理当前 CMDB 中数据库资产的拓扑关系',
    '分析近期告警，归类并给出根因分析',
  ]

  // ----------------------- 审批响应 -----------------------

  const handleApprovalRespond = async (action: 'approve' | 'reject') => {
    if (!pendingApproval) return
    setApprovalBusy(true)
    const ok = await respondApproval(pendingApproval.id, action)
    setApprovalBusy(false)
    if (ok) setPendingApproval(null)
  }

  // ============================================================================
  // 渲染
  // ============================================================================

  return (
    <div className="p-6 h-[calc(100vh-120px)] flex flex-col">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Sparkles className="h-6 w-6 text-emerald-500" />
            AI 助手
          </h1>
          <p className="text-sm text-muted-foreground">智能运维助手，帮你高效完成运维任务</p>
        </div>
        <div className="flex items-center gap-2">
          {mode !== 'team' && (
            <>
              <button
                onClick={() => { const v = !showThinking; setShowThinking(v); localStorage.setItem('ai.showThinking', String(v)) }}
                className={cn(
                  'px-2 py-1 rounded text-xs border transition-colors flex items-center gap-1',
                  showThinking
                    ? 'bg-purple-50 border-purple-200 text-purple-700 dark:bg-purple-500/10 dark:border-purple-500/30 dark:text-purple-400'
                    : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
                )}
                title="显示/隐藏 AI 思考过程"
              >
                💭 思考过程
              </button>
              {mode !== 'explore' && (
                <>
                  <Button variant="outline" size="sm" onClick={() => router.push('/ai/analytics')}><BarChart3 className="h-4 w-4" />分析</Button>
                  <Button variant="outline" size="sm" onClick={handleOpenHistory}><History className="h-4 w-4" />{showHistory ? '返回' : '历史'}</Button>
                  <Button variant="outline" size="sm" onClick={() => router.push('/ai/settings')}><Settings className="h-4 w-4" />设置</Button>
                </>
              )}
            </>
          )}
        </div>
      </div>

      {/* 模式 Tab：普通 / 自主 / 团队 */}
      <div className="flex gap-2 mb-4">
        {(['normal', 'explore', 'team'] as AIMode[]).map(m => (
          <button
            key={m}
            onClick={() => setMode(m)}
            className={cn(
              'px-4 py-1.5 rounded-md text-sm border transition-colors',
              mode === m
                ? 'bg-emerald-50 border-emerald-200 text-emerald-700 dark:bg-emerald-500/10 dark:border-emerald-500/30 dark:text-emerald-400'
                : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
            )}
          >
            {modeLabelMap[m]}模式
          </button>
        ))}
      </div>

      {/* 普通模式 */}
      {mode === 'normal' && (
        <div className="flex-1 flex gap-4 min-h-0">
          <div className="flex-1 flex flex-col border rounded-lg overflow-hidden">
            <div className="flex-1 overflow-y-auto p-4 space-y-4">
              {showHistory ? (
                <div className="space-y-2">
                  <h3 className="font-medium text-sm mb-3">对话历史</h3>
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
              ) : (
                <>
                  {messages.map(msg => (
                    <div key={msg.id} className={cn('flex gap-3', msg.role === 'user' ? 'justify-end' : 'justify-start')}>
                      {msg.role === 'assistant' && (
                        <div className="h-8 w-8 rounded-full bg-emerald-100 dark:bg-emerald-500/20 flex items-center justify-center shrink-0">
                          <Bot className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
                        </div>
                      )}
                      <div className={cn(
                        'max-w-[70%] rounded-lg px-4 py-2 text-sm',
                        msg.role === 'user'
                          ? 'bg-emerald-600 text-white whitespace-pre-wrap'
                          : 'bg-gray-100 dark:bg-gray-800 prose-sm max-w-none',
                      )}>
                        {msg.role === 'user' ? msg.content : (
                          <>
                            {msg.thinking && (
                              <details className="mb-2">
                                <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground">💭 思维链 ({msg.thinking.slice(0, 30)}...)</summary>
                                <div className="mt-1 p-2 rounded bg-gray-200 dark:bg-gray-700 text-xs whitespace-pre-wrap text-muted-foreground">{msg.thinking}</div>
                              </details>
                            )}
                            <div dangerouslySetInnerHTML={{ __html: renderMarkdown(msg.content) }} />

                            {/* RAG 引用来源 */}
                            {msg.citations && msg.citations.length > 0 && (
                              <details className="mt-2">
                                <summary className="text-[10px] text-muted-foreground cursor-pointer hover:text-foreground">📚 引用来源 ({msg.citations.length})</summary>
                                <div className="mt-1 space-y-0.5">
                                  {msg.citations.map((c, i) => (
                                    <div key={i} className="text-[10px] text-muted-foreground pl-2 border-l-2 border-emerald-200 dark:border-emerald-500/30">
                                      {c.startsWith('[') ? c : `[${c}]`}
                                    </div>
                                  ))}
                                </div>
                              </details>
                            )}

                            {/* 反馈按钮 */}
                            {msg.id !== '0' && (
                              <div className="flex items-center gap-1 mt-2 pt-1.5 border-t border-gray-200 dark:border-[rgba(255,255,255,0.04)]">
                                <button
                                  onClick={() => {
                                    if (msg.feedback_rating === 'up') return
                                    submitFeedback(msg)
                                    setMessages(prev => prev.map(m => m.id === msg.id ? { ...m, feedback_rating: 'up' as const } : m))
                                  }}
                                  disabled={!!msg.feedback_rating}
                                  className={`p-1 rounded text-[10px] transition-colors ${msg.feedback_rating === 'up' ? 'text-emerald-500 bg-emerald-50 dark:bg-emerald-500/10' : 'text-muted-foreground hover:text-emerald-500'}`}
                                  title="有帮助"
                                >
                                  <ThumbsUp className="h-3 w-3" />
                                </button>
                                <button
                                  onClick={() => {
                                    if (msg.feedback_rating === 'down') return
                                    submitFeedback(msg, 'down')
                                    setMessages(prev => prev.map(m => m.id === msg.id ? { ...m, feedback_rating: 'down' as const } : m))
                                  }}
                                  disabled={!!msg.feedback_rating}
                                  className={`p-1 rounded text-[10px] transition-colors ${msg.feedback_rating === 'down' ? 'text-red-500 bg-red-50 dark:bg-red-500/10' : 'text-muted-foreground hover:text-red-500'}`}
                                  title="没帮助"
                                >
                                  <ThumbsDown className="h-3 w-3" />
                                </button>
                              </div>
                            )}
                          </>
                        )}
                      </div>
                      {msg.role === 'user' && (
                        <div className="h-8 w-8 rounded-full bg-gray-200 dark:bg-gray-700 flex items-center justify-center shrink-0">
                          <User className="h-4 w-4 text-gray-600 dark:text-gray-300" />
                        </div>
                      )}
                    </div>
                  ))}
                  {loading && (
                    <div className="flex gap-3 justify-start">
                      <div className="h-8 w-8 rounded-full bg-emerald-100 dark:bg-emerald-500/20 flex items-center justify-center shrink-0">
                        <Bot className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
                      </div>
                      <div className="bg-gray-100 dark:bg-gray-800 rounded-lg px-4 py-2 text-sm flex items-center gap-2">
                        <Loader2 className="h-4 w-4 animate-spin" />
                        <span>思考中...</span>
                      </div>
                    </div>
                  )}
                  <div ref={messagesEndRef} />
                </>
              )}
            </div>

            <div className="border-t p-4">
              <div className="flex gap-2">
                <Input
                  value={input}
                  onChange={e => setInput(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && !e.shiftKey && sendMessage(input)}
                  placeholder="输入你的问题..."
                  className="flex-1"
                />
                <Button onClick={() => sendMessage(input)} disabled={!input.trim() || loading}>
                  <Send className="h-4 w-4" />
                </Button>
              </div>

              <div className="flex gap-2 mt-2">
                <p className="text-[10px] text-muted-foreground">
                  {showThinking ? '💭 思考过程已启用 — 每条回复会展示推理链' : '点击右上角 💭 可开启思考过程'}
                </p>
              </div>
            </div>
          </div>

          <div className="w-64 space-y-4 shrink-0">
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-sm">快捷操作</CardTitle></CardHeader>
              <CardContent className="space-y-1">
                {QUICK_ACTIONS.map(action => {
                  const Icon = action.icon
                  return (
                    <button
                      key={action.label}
                      onClick={() => sendMessage(action.prompt)}
                      className="w-full flex items-center gap-2 px-2 py-1.5 rounded text-xs text-left hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
                    >
                      <Icon className="h-3.5 w-3.5 text-emerald-500" />
                      <span className="flex-1">{action.label}</span>
                      <ChevronRight className="h-3 w-3 text-muted-foreground" />
                    </button>
                  )
                })}
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-sm">当前 Agent</CardTitle></CardHeader>
              <CardContent>
                {agents.length === 0 ? (
                  <p className="text-xs text-muted-foreground">暂无可用 Agent</p>
                ) : (
                  <div className="space-y-1">
                    {agents.map((a: any) => (
                      <button
                        key={a.id}
                        onClick={() => setSelectedAgent(selectedAgent?.id === a.id ? null : a)}
                        className={cn(
                          'w-full flex items-center justify-between px-2 py-1.5 rounded text-xs text-left transition-colors',
                          selectedAgent?.id === a.id
                            ? 'bg-emerald-100 dark:bg-emerald-500/20 text-emerald-700 dark:text-emerald-300'
                            : 'hover:bg-gray-100 dark:hover:bg-gray-800',
                        )}
                      >
                        <span className="truncate flex-1">{a.name}</span>
                        <Badge variant={a.status === 'active' ? 'default' : 'secondary'} className="text-[10px] ml-1 shrink-0">{a.status}</Badge>
                      </button>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      )}

      {/* 自主模式 */}
      {mode === 'explore' && (
        <div className="flex-1 flex flex-col min-h-0">
          {/* 目标输入区 */}
          <div className="rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.06)] bg-gray-50/60 dark:bg-[rgba(255,255,255,0.02)] p-4 mb-4">
            <div className="flex items-center gap-2 mb-2">
              <Bot className="h-4 w-4 text-emerald-500" />
              <span className="text-sm font-medium">下达运维目标</span>
              <span className="text-[11px] text-muted-foreground">AI 将自主规划、调用工具、多步执行并汇报</span>
            </div>
            <div className="flex gap-2">
              <textarea
                value={exploreInput}
                onChange={e => setExploreInput(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleExploreSend(exploreInput) } }}
                placeholder="例如：检查所有主机的运行状态，找出异常主机并给出处理建议"
                rows={2}
                className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm resize-none placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              />
              <Button onClick={() => handleExploreSend(exploreInput)} disabled={!exploreInput.trim() || exploreStreaming} className="shrink-0 self-start">
                {exploreStreaming ? <Loader2 className="h-4 w-4 animate-spin" /> : <Rocket className="h-4 w-4" />}
                {exploreStreaming ? '执行中' : '自主执行'}
              </Button>
            </div>
            <div className="flex gap-2 mt-2 flex-wrap">
              {EXPLORE_EXAMPLES.map(ex => (
                <button
                  key={ex}
                  onClick={() => setExploreInput(ex)}
                  className="text-[11px] px-2 py-1 rounded border border-gray-200 dark:border-[rgba(255,255,255,0.08)] text-muted-foreground hover:border-emerald-300 hover:text-emerald-600 dark:hover:border-emerald-500/40 transition-colors"
                >
                  {ex}
                </button>
              ))}
            </div>
          </div>

          {/* 执行过程 */}
          <div className="flex-1 overflow-y-auto space-y-4 pr-1">
            {exploreRuns.length === 0 ? (
              <div className="h-full flex items-center justify-center">
                <div className="text-center max-w-sm">
                  <Bot className="h-10 w-10 mx-auto mb-3 text-emerald-500/40" />
                  <p className="text-sm text-muted-foreground">
                    输入一个目标，AI 将自动拆解计划、调用工具逐步执行，并在关键节点向你汇报。
                  </p>
                  <p className="text-xs text-muted-foreground/70 mt-2">提示：只读类任务（查询/分析/巡检）可放心交给自主模式；涉及执行类操作会触发安全审批。</p>
                </div>
              </div>
            ) : (
              exploreRuns.map(run => (
                <div key={run.id} className="rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.06)] bg-white dark:bg-transparent shadow-sm">
                  {/* 目标头 */}
                  <div className="flex items-start justify-between gap-3 px-4 py-3 border-b border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground mb-1">
                        <Target className="h-3 w-3 text-emerald-500" /> 目标
                      </div>
                      <p className="text-sm font-medium text-gray-800 dark:text-gray-100 whitespace-pre-wrap">{run.goal}</p>
                    </div>
                    <Badge variant={run.status === 'done' ? 'success' : run.status === 'error' ? 'destructive' : 'info'} pulse={run.status === 'running'} className="shrink-0 text-[10px]">
                      {run.status === 'running' ? '执行中' : run.status === 'done' ? '已完成' : '异常'}
                    </Badge>
                  </div>
                  {/* 内容 */}
                  <div className="px-4 py-3">
                    {run.status === 'running' && !run.content ? (
                      <div className="flex items-center gap-2 text-xs text-muted-foreground">
                        <Loader2 className="h-3.5 w-3.5 animate-spin text-emerald-500" />
                        正在规划执行方案...
                      </div>
                    ) : (
                      <div className="text-sm text-gray-700 dark:text-[#C7D2E0] leading-relaxed" dangerouslySetInnerHTML={{ __html: renderExploreContent(run.content) }} />
                    )}
                  </div>
                </div>
              ))
            )}
            <div ref={exploreEndRef} />
          </div>
        </div>
      )}

      {/* 团队模式 */}
      {mode === 'team' && (
        <div className="flex-1 flex flex-col border rounded-lg overflow-hidden min-h-0">
          {/* 团队分段卡片选择 */}
          <div className="border-b p-3">
            <div className="flex items-center gap-2 mb-2">
              <Users className="h-4 w-4 text-emerald-500" />
              <span className="text-sm font-medium">选择团队</span>
              {teamsLoading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
            </div>
            {teams.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                暂无团队，
                <button className="text-emerald-600 underline" onClick={() => router.push('/ai/teams')}>前往创建</button>
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
                        ? 'border-emerald-400 bg-emerald-50/60 dark:bg-emerald-500/10'
                        : 'border-gray-200 dark:border-[rgba(255,255,255,0.06)] hover:border-gray-300',
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-sm font-medium truncate">{team.name}</span>
                      <Badge variant={teamModeBadge[team.mode] || 'secondary'} className="text-[10px] shrink-0">
                        {teamModeLabel[team.mode] || team.mode}
                      </Badge>
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
                  {/* 用户消息 */}
                  <div className="flex justify-end">
                    <div className="max-w-[70%] rounded-lg px-4 py-2 text-sm bg-emerald-600 text-white whitespace-pre-wrap">
                      {turn.user}
                    </div>
                  </div>

                  {/* 各 agent 的流式输出 */}
                  {turn.agents.map(a => (
                    <div
                      key={`${turn.id}-${a.agent_id}`}
                      className="rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.04)] bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] p-3"
                    >
                      <div className="flex items-center gap-2 mb-1.5">
                        <Badge
                          variant={a.status === 'error' ? 'destructive' : a.status === 'done' ? 'success' : 'info'}
                          pulse={a.status === 'streaming'}
                        >
                          {teamRoleLabel[a.role] || a.role} · {a.agent_name}
                        </Badge>
                        {a.status === 'streaming' && <Loader2 className="h-3 w-3 animate-spin text-emerald-500" />}
                        {a.status === 'done' && <span className="text-[10px] text-emerald-500">✓ 完成</span>}
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
                                  {tc.status === 'done' && <span className="text-[10px] text-emerald-500">✓</span>}
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
                          className="text-xs text-gray-700 dark:text-[#C7D2E0] whitespace-pre-wrap"
                          dangerouslySetInnerHTML={{ __html: renderMarkdown(a.text) }}
                        />
                      )}
                    </div>
                  ))}

                  {/* 团队最终答复 */}
                  {turn.final && (
                    <div className="flex gap-3 justify-start">
                      <div className="h-8 w-8 rounded-full bg-emerald-100 dark:bg-emerald-500/20 flex items-center justify-center shrink-0">
                        <Bot className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
                      </div>
                      <div className="max-w-[80%] rounded-lg px-4 py-2 text-sm bg-gray-100 dark:bg-gray-800 prose-sm max-w-none">
                        <Badge variant="success" className="mb-1.5 text-[10px]">团队最终答复</Badge>
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
          <div className="border-t p-4">
            <div className="flex gap-2">
              <Input
                value={teamInput}
                onChange={e => setTeamInput(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && !e.shiftKey && handleTeamSend(teamInput)}
                placeholder={selectedTeamId ? '向团队发送消息...' : '请先选择上方团队'}
                className="flex-1"
                disabled={!selectedTeamId || teamStreaming}
              />
              <Button onClick={() => handleTeamSend(teamInput)} disabled={!selectedTeamId || !teamInput.trim() || teamStreaming}>
                <Send className="h-4 w-4" />
              </Button>
            </div>
            <div className="flex items-center justify-between mt-2">
              <Button variant="outline" size="sm" onClick={() => router.push('/ai/teams')}>
                <Settings className="h-4 w-4 mr-1" />管理团队
              </Button>
              {teamStreaming && (
                <span className="text-xs text-muted-foreground flex items-center gap-1">
                  <Loader2 className="h-3 w-3 animate-spin" />团队响应中...
                </span>
              )}
            </div>
          </div>
        </div>
      )}

      {/* 敏感操作审批弹窗 */}
      <Dialog open={!!pendingApproval} onClose={() => { if (!approvalBusy) setPendingApproval(null) }} title="🔐 敏感操作确认">
        {pendingApproval && (
          <div className="space-y-3">
            <div className={cn("flex items-center gap-2 text-xs p-3 rounded-lg", theme === 'dark' ? 'bg-amber-500/5 border border-amber-500/10' : 'bg-amber-50 border border-amber-200')}>
              <AlertTriangle className="h-4 w-4 text-amber-500 shrink-0" />
              <span>AI 请求执行敏感操作，请确认后放行。超时未响应将自动拒绝。</span>
            </div>
            <div className="grid gap-2 text-sm">
              <div><span className="text-muted-foreground text-xs">操作：</span>
                <code className="font-mono text-xs bg-gray-100 dark:bg-gray-800 rounded px-1.5 py-0.5">{pendingApproval.tool}</code>
              </div>
              <div><span className="text-muted-foreground text-xs">说明：</span><span className="text-sm">{pendingApproval.summary || '无'}</span></div>
              {pendingApproval.args && pendingApproval.args !== '{}' && (
                <div>
                  <span className="text-muted-foreground text-xs">参数：</span>
                  <pre className="mt-1 p-2 rounded bg-gray-100 dark:bg-gray-800 text-[11px] overflow-x-auto whitespace-pre-wrap max-h-28 overflow-y-auto">{pendingApproval.args}</pre>
                </div>
              )}
              <div className="text-[11px] text-muted-foreground">
                截止：{pendingApproval.deadline ? new Date(pendingApproval.deadline).toLocaleTimeString('zh-CN') : '-'}
              </div>
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => handleApprovalRespond('reject')} disabled={approvalBusy} className="border-red-200 text-red-600 hover:bg-red-50 dark:border-red-500/30 dark:text-red-400 dark:hover:bg-red-500/10">
            <XCircle className="h-4 w-4 mr-1" />拒绝
          </Button>
          <Button onClick={() => handleApprovalRespond('approve')} disabled={approvalBusy}>
            {approvalBusy ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <CheckCircle className="h-4 w-4 mr-1" />}
            批准执行
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
