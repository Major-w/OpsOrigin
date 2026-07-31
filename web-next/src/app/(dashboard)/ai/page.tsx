'use client'

import { useEffect, useState, useRef } from 'react'
import { useRouter } from 'next/navigation'
import { useAIStore } from '@/stores/ai'
import { useTeams, type ChatMessage, type TeamStreamEvent } from '@/stores/ai'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Bot, Send, History, Settings, Sparkles, Terminal, FileText, Server, ChevronRight, Loader2, User, BarChart3, Users } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'

// ============================================================================
// 类型与本地工具
// ============================================================================

interface Message {
  id: string
  role: 'user' | 'assistant'
  content: string
  thinking?: string
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

type AIMode = 'normal' | 'cot' | 'team'

// 轻量内联 Markdown→HTML。生产中可替换为 react-markdown+remark-gfm 获得完整 GFM 支持。
function renderMarkdown(text: string): string {
  let html = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, '<pre class="rounded bg-gray-900 text-gray-100 p-3 my-2 overflow-x-auto text-xs"><code>$2</code></pre>')
  html = html.replace(/`([^`]+)`/g, '<code class="rounded bg-gray-200 dark:bg-gray-700 px-1 text-xs">$1</code>')
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  html = html.replace(/\*([^*\n]+?)\*/g, '<em>$1</em>')
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a class="text-blue-500 underline" href="$2" target="_blank">$1</a>')
  html = html.replace(/\n/g, '<br/>')
  return html
}

const QUICK_ACTIONS = [
  { label: '查询主机状态', icon: Server, prompt: '帮我查询所有主机的运行状态' },
  { label: '执行巡检任务', icon: Terminal, prompt: '执行一次自动化巡检任务' },
  { label: '生成运维报告', icon: FileText, prompt: '生成本周的运维报告' },
  { label: '排查故障', icon: Sparkles, prompt: '帮我排查当前系统的异常情况' },
]

const modeLabelMap: Record<AIMode, string> = { normal: '普通', cot: '思维链', team: '团队' }
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
  const { agents, fetchAgents, streamChat, loadConversations } = useAIStore()
  const { teams, loading: teamsLoading, loadTeams, streamTeamChat } = useTeams()
  const router = useRouter()
  const { theme } = useThemeStore()

  const [mode, setMode] = useState<AIMode>('normal')

  // —— 普通 / 思维链 模式状态 ——
  const [messages, setMessages] = useState<Message[]>([
    { id: '0', role: 'assistant', content: '你好！我是 OpsOrigin AI 助手，可以帮你完成运维任务。\n\n👉 **快速开始：** 点击右上角「设置」→「模型」配置 LLM API Key 即可使用。\n\n配置完成后可直接提问，或选择右侧快捷操作。', timestamp: new Date() },
  ])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [selectedAgent, setSelectedAgent] = useState<any>(null)
  const [showHistory, setShowHistory] = useState(false)
  const [conversations, setConversations] = useState<any[]>([])
  const messagesEndRef = useRef<HTMLDivElement>(null)

  // —— 团队模式状态 ——
  const [selectedTeamId, setSelectedTeamId] = useState<string | null>(null)
  const [teamTurns, setTeamTurns] = useState<TeamTurnLocal[]>([])
  const [teamInput, setTeamInput] = useState('')
  const [teamStreaming, setTeamStreaming] = useState(false)
  const teamMessagesEndRef = useRef<HTMLDivElement>(null)

  const reasoning = mode === 'cot'

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
      setShowHistory(false)
    } catch {}
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

    if (reasoning) {
      try {
        const resp = await fetch('/api/v1/ai/chat', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ prompt: content, history, stream: false, reasoning: true, agent_id: selectedAgent?.id }),
        })
        const data = await resp.json()
        setMessages(prev => prev.map(m => m.id === aiId ? { ...m, content: data.content || data.error || '无应答', thinking: data.thinking } : m))
      } catch { setMessages(prev => prev.filter(m => m.id !== aiId)) }
      setLoading(false)
      return
    }

    let full = ''
    await streamChat(content, (token) => {
      full += token
      setMessages(prev => prev.map(m => m.id === aiId ? { ...m, content: full } : m))
    }, () => {
      setLoading(false)
    }, history, selectedAgent?.id)
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
        {mode !== 'team' && (
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => router.push('/ai/analytics')}><BarChart3 className="h-4 w-4" />分析</Button>
            <Button variant="outline" size="sm" onClick={handleOpenHistory}><History className="h-4 w-4" />{showHistory ? '返回' : '历史'}</Button>
            <Button variant="outline" size="sm" onClick={() => router.push('/ai/settings')}><Settings className="h-4 w-4" />设置</Button>
          </div>
        )}
      </div>

      {/* 模式 Tab：普通 / 思维链 / 团队 */}
      <div className="flex gap-2 mb-4">
        {(['normal', 'cot', 'team'] as AIMode[]).map(m => (
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

      {/* 普通 / 思维链 模式 */}
      {mode !== 'team' && (
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
                <button
                  onClick={() => setMode('normal')}
                  className={cn(
                    'flex-1 text-xs px-3 py-1.5 rounded-md border transition-colors text-left',
                    !reasoning
                      ? 'bg-emerald-50 border-emerald-200 text-emerald-700 dark:bg-emerald-500/10 dark:border-emerald-500/30 dark:text-emerald-400'
                      : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
                  )}
                >
                  <span className="font-medium">⚡ 普通模式</span>
                  <span className="block text-[10px] opacity-60 mt-0.5">快速回答，流式输出</span>
                </button>
                <button
                  onClick={() => setMode('cot')}
                  className={cn(
                    'flex-1 text-xs px-3 py-1.5 rounded-md border transition-colors text-left',
                    reasoning
                      ? 'bg-emerald-50 border-emerald-200 text-emerald-700 dark:bg-emerald-500/10 dark:border-emerald-500/30 dark:text-emerald-400'
                      : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
                  )}
                >
                  <span className="font-medium">💭 思维链模式</span>
                  <span className="block text-[10px] opacity-60 mt-0.5">展示推理过程，适合复杂问题</span>
                </button>
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
    </div>
  )
}
