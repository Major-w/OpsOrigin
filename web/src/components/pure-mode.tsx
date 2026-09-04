
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAIStore } from '@/stores/ai'
import { useKnowledgeStore } from '@/stores/knowledge'
import { useAIChat, ChatMessageList, ChatInputBar, TeamChatView, ApprovalBar, ChatContextBar, ConversationList, estimateContextTokens, SHARED_SESSION_KEY, SHARED_CONV_ID_KEY } from '@/components/ai/chat-kernel'
import { useThemeStore } from '@/stores/theme'
import usePathname from '@/lib/use-pathname'
import { apiFetchRaw } from '@/lib/utils'
import { AI_ASSISTANT_NAV } from '@/components/ai-nav'
import { cn } from '@/lib/utils'
import {
  Bot, X, Trash2, History, BarChart3, Settings, PanelLeftClose, Sparkles,
} from 'lucide-react'

// ============================================================================
// 纯净模式（AI 工作界面）
// 独立全屏布局：左侧功能区(AI 助手菜单) + 中间全屏 AI 对话区。
// 聊天逻辑复用 chat-kernel 的 useAIChat / ChatMessageList / ChatInputBar / TeamChatView,
// 普通 / 自主 / 团队 三模式齐全, 拥有独立会话与独立渲染, 不依赖悬浮助手状态。
// ============================================================================

const DEFAULT_KEY = 'opsorigin_pure_default'

const QUICK_ACTIONS = [
  { label: '查询主机状态', prompt: '帮我查询所有主机的运行状态' },
  { label: '执行巡检任务', prompt: '执行一次自动化巡检任务' },
  { label: '生成运维报告', prompt: '生成本周的运维报告' },
  { label: '排查故障', prompt: '帮我排查当前系统的异常情况' },
]

const WELCOME = '欢迎进入 **纯净模式** 🤖\n\n这是一个以 AI 对话为核心的工作界面：\n- 左侧是「AI 助手」菜单，可一键进入场景包、数字员工、主动巡检、智能体团队等能力\n- 中间随时向我提问：查询主机、发起工单、分析告警、写知识库文章…\n- 右上角可切换「自主模式 / 团队模式」，给出目标由我规划执行或多人协作\n\n直接开始吧。'

export default function PureMode({ onExitPure }: { onExitPure: () => void }) {
  const navigate = useNavigate()
  const pathname = usePathname()
  const { theme } = useThemeStore()
  const loadConversations = useAIStore(s => s.loadConversations)
  const submitFeedback = useAIStore(s => s.submitFeedback)
  const fetchAgents = useAIStore(s => s.fetchAgents)

  const chat = useAIChat({
    welcome: WELCOME,
    welcomeId: 'welcome',
    persistKey: SHARED_SESSION_KEY,
    convIdKey: SHARED_CONV_ID_KEY,
    enableTeam: true,
    captureToolCalls: false,
    enableAbort: true,
    resetConvOn403: true,
    defaultMode: 'normal',
    enableKnowledgeScope: true,
  })

  const {
    mode, setMode, showThinking, toggleThinking, selectedModel, setSelectedModel,
    selectedAgent, setSelectedAgent, models, agents, messages, setMessages, input, setInput,
    streaming, send, clearSession, pending, approving, respond, convId, setConvId,
    selectedTeamId, setSelectedTeamId, teamTurns, teamInput, setTeamInput, teamStreaming, handleTeamSend,
    teams, teamsLoading,
    knowledgeCategories, setKnowledgeCategories,
  } = chat

  // 知识范围选择器选项(挂载时加载分类列表)
  const knowledgeCategoryOptions = useKnowledgeStore(s => s.categories)
  useEffect(() => { useKnowledgeStore.getState().fetchCategories() }, [])
  // 数字员工(Agent)列表: 供对话页临时选择绑定(普通/探索模式)
  useEffect(() => { fetchAgents() }, [])

  const isDark = theme === 'dark'
  const [pureDefault, setPureDefault] = useState<boolean>(() => {
    try { return localStorage.getItem(DEFAULT_KEY) === '1' } catch { return false }
  })
  const [showHistory, setShowHistory] = useState(false)
  const [conversations, setConversations] = useState<any[]>([])
  const [artifacts, setArtifacts] = useState<{ id: string; file_name: string; file_size: number }[]>([])

  const listEndRef = useRef<HTMLDivElement>(null)
  const teamMessagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => { setTimeout(() => inputRef.current?.focus(), 80) }, [])

  const togglePureDefault = () => {
    const v = !pureDefault
    setPureDefault(v)
    try { localStorage.setItem(DEFAULT_KEY, v ? '1' : '0') } catch { /* ignore */ }
  }

  const goto = (to: string) => { onExitPure(); navigate(to) }

  const handleOpenHistory = async () => {
    if (!showHistory) {
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
      // 本会话产物(探索模式报告/文件等归档对象存储), 仅在已有关联会话时拉取
      if (convId) {
        try {
          const r = await apiFetchRaw(`/ai/conversations/${convId}/artifacts`)
          setArtifacts(r.ok ? await r.json() : [])
        } catch { /* ignore */ }
      } else {
        setArtifacts([])
      }
    }
    setShowHistory(v => !v)
  }

  const handleLoadConversation = async (id: string) => {
    try {
      const resp = await apiFetchRaw(`/ai/conversations/${id}`)
      if (!resp.ok) return
      const conv = await resp.json()
      if (conv?.messages) {
        const msgs = conv.messages.map((m: any, i: number) => ({
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

  // 产物下载: blob + content-disposition 文件名
  const downloadArtifact = async (aid: string) => {
    try {
      const r = await apiFetchRaw(`/ai/conversations/${convId}/artifacts/${aid}`)
      if (!r.ok) return
      const blob = await r.blob()
      const cd = r.headers.get('content-disposition') || ''
      const name = cd.match(/filename="?([^";]+)/)?.[1] || `artifact-${aid}.md`
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = name
      a.click()
      URL.revokeObjectURL(url)
    } catch { /* ignore */ }
  }

  const submitFb = async (msg: any, rating: 'up' | 'down') => {
    if (!msg.messageId || msg.feedback_rating) return
    const ok = await submitFeedback({ message_id: msg.messageId, rating })
    if (ok) setMessages(prev => prev.map(m => (m.id === msg.id ? { ...m, feedback_rating: rating } : m)))
  }

  const clear = () => { clearSession(); setShowHistory(false) }

  return (
    <div className={cn('flex h-screen overflow-hidden', isDark ? 'bg-[var(--background)] text-white' : 'bg-gray-50 text-gray-900')}>
      {/* ============ 左侧功能区 ============ */}
      <aside className={cn(
        'relative z-10 w-60 flex flex-col border-r backdrop-blur-2xl shrink-0',
        isDark ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.8)]' : 'border-gray-200 bg-white/80',
      )}>
        <div className={cn('h-14 flex items-center gap-2.5 px-4 border-b shrink-0', isDark ? 'border-[rgba(255,255,255,0.04)]' : 'border-gray-200')}>
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

        <div className={cn('px-4 py-3 border-t space-y-3 shrink-0', isDark ? 'border-[rgba(255,255,255,0.04)]' : 'border-gray-200')}>
          <label className="flex items-center justify-between gap-2 cursor-pointer select-none">
            <span className="text-[11px] text-muted-foreground leading-tight">登录后默认<br />进入纯净模式</span>
            <button
              type="button"
              role="switch"
              aria-checked={pureDefault}
              onClick={togglePureDefault}
              className={cn('relative h-5 w-9 rounded-full transition-colors shrink-0', pureDefault ? 'bg-blue-500' : isDark ? 'bg-white/15' : 'bg-gray-300')}
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
            <button onClick={clear} className="p-1.5 rounded-md transition-colors text-muted-foreground hover:text-foreground hover:bg-black/5 dark:hover:bg-white/10" title="清空会话">
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
            <ChatContextBar
              modelLabel={selectedModel ? (models.find(m => m.model === selectedModel)?.name || selectedModel) : undefined}
              agentName={selectedAgent?.name}
              knowledgeScope={knowledgeCategories}
              mode={mode}
              models={models}
              selectedModel={selectedModel}
              onModelChange={setSelectedModel}
              agents={agents}
              selectedAgent={selectedAgent}
              onAgentChange={setSelectedAgent}
              onOpenKnowledge={() => navigate(knowledgeCategories.length === 1 ? `/knowledge?cat=${encodeURIComponent(knowledgeCategories[0])}` : '/knowledge')}
              contextTokens={estimateContextTokens(messages)}
              variant="panel"
              onConfigure={() => navigate('/ai/settings')}
            />
            <ChatMessageList
              messages={messages}
              mode={mode}
              welcomeId="welcome"
              variant="panel"
              enableCitations
              enableFeedback
              onFeedback={submitFb}
              loading={streaming}
              loadingText={mode === 'explore' ? 'AI 正在规划并执行…' : 'AI 正在思考…'}
              emptyQuickActions={
                mode === 'normal'
                  ? QUICK_ACTIONS.map(q => (
                    <button key={q.label} onClick={() => { setInput(q.prompt); send(q.prompt) }} className="flex flex-col items-start gap-1.5 px-3 py-2.5 rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.08)] hover:border-blue-300 hover:bg-blue-50/50 dark:hover:border-blue-500/40 dark:hover:bg-blue-500/10 transition-colors text-left">
                      <Sparkles className="h-4 w-4 text-blue-500" />
                      <span className="text-xs">{q.label}</span>
                    </button>
                  ))
                  : null
              }
              endRef={listEndRef}
              className="px-5 py-5"
            />

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
                  <ConversationList
                    conversations={conversations}
                    activeId={convId}
                    onSelect={(id) => handleLoadConversation(id)}
                    onNew={() => clear()}
                    emptyText="暂无对话记录"
                  />
                  {/* 本会话产物(探索模式归档的报告/文件) */}
                  <div className="pt-3 mt-3" style={{ borderTop: '1px solid var(--border)' }}>
                    <p className="text-xs font-medium mb-1.5">本会话产物（{artifacts.length}）</p>
                    {artifacts.length === 0 ? (
                      <p className="text-xs text-muted-foreground">{convId ? '暂无产物' : '探索模式产出的报告/文件会归档到这里'}</p>
                    ) : (
                      artifacts.map(a => (
                        <div key={a.id} className="flex items-center justify-between gap-2 p-2 mb-1 rounded-lg bg-gray-50 dark:bg-gray-800">
                          <span className="text-xs truncate" title={a.file_name}>{a.file_name}</span>
                          <button onClick={() => downloadArtifact(a.id)} className="text-xs text-blue-500 hover:underline shrink-0">下载</button>
                        </div>
                      ))
                    )}
                  </div>
                </div>
                <button className="flex-1 h-full" onClick={() => setShowHistory(false)} aria-label="关闭历史" />
              </div>
            )}

            {/* 输入区 */}
            <ChatInputBar
              value={input}
              onChange={setInput}
              onSend={() => send(input)}
              onStop={chat.stopGeneration}
              streaming={streaming}
              mode={mode}
              onModeChange={setMode}
              showThinking={showThinking}
              onToggleThinking={toggleThinking}
              models={models}
              selectedModel={selectedModel}
              onModelChange={setSelectedModel}
              agents={agents}
              selectedAgent={selectedAgent}
              onAgentChange={setSelectedAgent}
              enableModel
              enableAgent
              enableQuickActions={mode === 'normal'}
              quickActions={QUICK_ACTIONS}
              onQuickSend={(p) => send(p)}
              exploreHint
              enableKnowledgeScope
              knowledgeCategories={knowledgeCategories}
              onKnowledgeCategoriesChange={setKnowledgeCategories}
              knowledgeCategoryOptions={knowledgeCategoryOptions}
              variant="panel"
              className="px-5 py-3"
            />
          </>
        )}

        {/* ============ 团队模式主体 ============ */}
        {mode === 'team' && (
          <>
            <TeamChatView
              teams={teams}
              teamsLoading={teamsLoading}
              selectedTeamId={selectedTeamId}
              onSelectTeam={setSelectedTeamId}
              teamTurns={teamTurns}
              teamInput={teamInput}
              onTeamInput={setTeamInput}
              teamStreaming={teamStreaming}
              onSend={(t) => handleTeamSend(t)}
              onManageTeams={() => goto('/ai/teams')}
              variant="panel"
              endRef={teamMessagesEndRef}
            />
          </>
        )}

        {/* 审批确认条（所有模式通用） */}
        <ApprovalBar pending={pending} approving={approving} onRespond={respond} variant="panel" />
      </main>
    </div>
  )
}
