'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { useAIStore, type PendingApproval } from '@/stores/ai'
import { renderMarkdown, renderExploreContent } from '@/lib/ai-render'
import {
  Bot, X, Send, Loader2, Trash2, AlertTriangle, CheckCircle2, XCircle, Sparkles, Square,
  PanelLeftClose,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'
import { usePathname } from 'next/navigation'
import { AI_ASSISTANT_NAV } from '@/components/ai-nav'

// ============================================================================
// 纯净模式（AI 工作界面）
// 独立全屏布局：左侧功能区(AI 助手菜单) + 中间全屏 AI 对话区。
// 聊天逻辑复用 ai-assistant 的 streamChat 全链路，但拥有独立会话与独立渲染，
// 不依赖悬浮助手状态，避免互相干扰。
// ============================================================================

interface PureMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  thinking?: string
  timestamp: number
}

type PureMode = 'normal' | 'explore'

const SESSION_KEY = 'ai.pure.session'
const CONV_ID_KEY = 'ai.pure.convId'
const DEFAULT_KEY = 'opsorigin_pure_default'

function newConvId(): string {
  return `pure_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

const WELCOME: PureMessage = {
  id: 'welcome',
  role: 'assistant',
  content: '欢迎进入 **纯净模式** 🤖\n\n这是一个以 AI 对话为核心的工作界面：\n- 左侧是「AI 助手」菜单，可一键进入场景包、数字员工、主动巡检、智能体团队等能力\n- 中间随时向我提问：查询主机、发起工单、分析告警、写知识库文章…\n- 右上角可切换「自主模式」，给出目标由我规划执行\n\n直接开始吧。',
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
  const router = useRouter()
  const pathname = usePathname()
  const { theme } = useThemeStore()
  const streamChat = useAIStore(s => s.streamChat)
  const respondApproval = useAIStore(s => s.respondApproval)

  const [mode, setMode] = useState<PureMode>('normal')
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

  const abortRef = useRef<AbortController | null>(null)
  const listEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    try {
      const slim = messages.filter(m => m.id !== 'welcome').map(m => ({ role: m.role, content: m.content }))
      localStorage.setItem(SESSION_KEY, JSON.stringify(slim))
    } catch { /* ignore */ }
  }, [messages])

  useEffect(() => {
    listEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, pending])

  useEffect(() => {
    setTimeout(() => inputRef.current?.focus(), 80)
  }, [])

  const persist = useCallback(() => setStreaming(false), [])

  const send = async () => {
    const content = input.trim()
    if (!content || streaming) return
    setInput('')

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
      () => {
        setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, content: acc || '（无应答）' } : m)))
        persist()
      },
      history,
      undefined,
      showThinking,
      onThinking,
      mode === 'explore',
      onAsk,
      cid,
      abortRef.current.signal,
    )
  }

  const clearSession = () => {
    if (streaming) return
    try {
      localStorage.removeItem(SESSION_KEY)
      localStorage.removeItem(CONV_ID_KEY)
    } catch { /* ignore */ }
    setMessages([WELCOME])
    setConvId('')
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
    router.push(to)
  }

  const isDark = theme === 'dark'

  return (
    <div className={cn('flex h-screen overflow-hidden', isDark ? 'bg-[#0B0F19] text-white' : 'bg-gray-50 text-gray-900')}>
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
            <Bot className="w-6 h-6 text-emerald-400" strokeWidth={1.5} />
          </div>
          <div className="flex-1 min-w-0">
            <span className="text-sm font-semibold tracking-tight">纯净模式</span>
            <p className="text-[10px] text-muted-foreground tracking-wider uppercase">AI Workspace</p>
          </div>
          <button onClick={onExitPure} className="p-1.5 rounded-md transition-colors text-muted-foreground hover:text-foreground hover:bg-black/5 dark:hover:bg-white/10" title="退出纯净模式">
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
                    ? isDark ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20' : 'bg-emerald-50 text-emerald-600 border border-emerald-200'
                    : isDark ? 'text-[#94A3B8] hover:text-white hover:bg-white/[0.03]' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100',
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
                pureDefault ? 'bg-emerald-500' : isDark ? 'bg-white/15' : 'bg-gray-300',
              )}
            >
              <span className={cn('absolute top-0.5 h-4 w-4 rounded-full bg-white transition-transform', pureDefault ? 'translate-x-4' : 'translate-x-0.5')} />
            </button>
          </label>
          <button onClick={onExitPure} className={cn(
            'w-full flex items-center justify-center gap-1.5 py-1.5 rounded-lg text-xs font-medium transition-colors',
            isDark ? 'bg-white/5 text-gray-300 hover:bg-white/10' : 'bg-gray-100 text-gray-600 hover:bg-gray-200',
          )}>
            <X className="h-3.5 w-3.5" /> 退出纯净模式
          </button>
        </div>
      </aside>

      {/* ============ 中间 AI 对话区 ============ */}
      <main className="relative z-10 flex-1 flex flex-col min-w-0">
        {/* 头部 */}
        <div className={cn(
          'h-14 flex items-center justify-between px-5 border-b shrink-0',
          isDark ? 'border-[rgba(255,255,255,0.04)] bg-[rgba(11,15,25,0.6)]' : 'border-gray-200 bg-white/70',
        )}>
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold">AI 工作区</span>
            <span className={cn('text-[10px] px-1.5 py-0.5 rounded', isDark ? 'bg-white/10 text-gray-300' : 'bg-gray-100 text-gray-500')}>OpsOrigin</span>
          </div>
          <div className="flex items-center gap-2">
            {/* 模式切换 */}
            <div className={cn('flex gap-1 rounded-lg p-0.5', isDark ? 'bg-white/5' : 'bg-gray-100')}>
              {(['normal', 'explore'] as PureMode[]).map(m => (
                <button
                  key={m}
                  onClick={() => setMode(m)}
                  className={cn(
                    'px-2.5 py-1 rounded-md text-xs font-medium transition-colors',
                    mode === m
                      ? 'bg-emerald-500 text-white shadow-sm'
                      : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {m === 'normal' ? '普通' : '自主'}
                </button>
              ))}
            </div>
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
            <button onClick={clearSession} className="p-1.5 rounded-md transition-colors text-muted-foreground hover:text-foreground hover:bg-black/5 dark:hover:bg-white/10" title="清空会话">
              <Trash2 className="h-3.5 w-3.5" />
            </button>
            <button onClick={onExitPure} className="p-1.5 rounded-md transition-colors text-muted-foreground hover:text-foreground hover:bg-black/5 dark:hover:bg-white/10 md:hidden" title="退出纯净模式">
              <X className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* 消息区 */}
        <div className="flex-1 overflow-y-auto px-5 py-5 space-y-3 min-h-0">
          {messages.map(m => (
            <div key={m.id} className={cn('flex', m.role === 'user' ? 'justify-end' : 'justify-start')}>
              <div
                className={cn(
                  'max-w-[80%] rounded-2xl px-4 py-3 text-sm leading-relaxed',
                  m.role === 'user'
                    ? 'bg-emerald-600 text-white rounded-br-sm'
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

        {/* 审批确认条 */}
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

        {/* 输入区 */}
        <div className={cn('px-5 py-3 border-t shrink-0', isDark ? 'border-[rgba(255,255,255,0.04)]' : 'border-gray-200')}>
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
                'flex-1 resize-none rounded-xl border px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-emerald-500/50 min-h-[44px] max-h-32',
                isDark ? 'border-white/10 bg-white/[0.03] text-white' : 'border-gray-200 bg-white text-gray-900',
              )}
            />
            <button
              onClick={streaming ? stopGeneration : send}
              disabled={!input.trim() && !streaming}
              className="flex h-11 w-11 items-center justify-center rounded-xl bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
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
      </main>
    </div>
  )
}
