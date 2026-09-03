
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAIStore, type PendingApproval } from '@/stores/ai'
import { renderMarkdown, renderExploreContent } from '@/lib/ai-render'
import { Bot, X, Send, Loader2, Trash2, ExternalLink, ChevronRight, AlertTriangle, CheckCircle2, XCircle, Sparkles, Square } from 'lucide-react'
import { cn } from '@/lib/utils'

// ============================================================================
// 全局悬浮 AI 助手
// 挂载于 Layout, 所有 (dashboard) 页面可见。收起态为右下角悬浮钮,
// 点击展开浮窗(桌面 400×600 / 移动端全屏), 复用 streamChat 全链路。
// ============================================================================

interface FabMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  thinking?: string
  timestamp: number
}

type FabMode = 'normal' | 'explore'

const SESSION_KEY = 'ai.assistant.session'
const CONV_ID_KEY = 'ai.assistant.convId'

// 本地生成持久化会话 ID(首次对话时), 后端按此 ID 自动创建/续接会话
function newConvId(): string {
  return `fab_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

const WELCOME: FabMessage = {
  id: 'welcome',
  role: 'assistant',
  content: '你好！我是 OpsOrigin AI 助手 👋\n\n在任意页面都可以随时找我：\n- **普通模式** — 快速问答、查询主机状态\n- **自主模式** — 给出目标，AI 自主规划并调用工具执行\n\n直接提问开始吧。',
  timestamp: Date.now(),
}

function loadSession(): FabMessage[] {
  try {
    const raw = localStorage.getItem(SESSION_KEY)
    if (!raw) return [WELCOME]
    const parsed = JSON.parse(raw) as { role: string; content: string }[]
    if (!Array.isArray(parsed) || parsed.length === 0) return [WELCOME]
    const msgs: FabMessage[] = parsed.map((m, i) => ({
      id: `m_${Date.now()}_${i}`,
      role: m.role === 'user' ? 'user' : 'assistant',
      content: m.content || '',
      timestamp: Date.now(),
    }))
    return msgs
  } catch {
    return [WELCOME]
  }
}

export default function AIAssistant() {
  const navigate = useNavigate()
  const assistantOpen = useAIStore(s => s.assistantOpen)
  const toggleAssistant = useAIStore(s => s.toggleAssistant)
  const setAssistantOpen = useAIStore(s => s.setAssistantOpen)
  const streamChat = useAIStore(s => s.streamChat)
  const respondApproval = useAIStore(s => s.respondApproval)

  const [mode, setMode] = useState<FabMode>('normal')
  const [showThinking, setShowThinking] = useState(true)
  const [messages, setMessages] = useState<FabMessage[]>(() =>
    typeof window !== 'undefined' ? loadSession() : [WELCOME],
  )
  const [input, setInput] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [pending, setPending] = useState<PendingApproval | null>(null)
  const [approving, setApproving] = useState(false)
  // 持久化会话 ID: 首次对话生成并存 localStorage, 跨页面/刷新续接同一会话
  const [convId, setConvId] = useState<string>(() => {
    if (typeof window === 'undefined') return ''
    try { return localStorage.getItem(CONV_ID_KEY) || '' } catch { return '' }
  })
  const abortRef = useRef<AbortController | null>(null)

  const listEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  // —— 会话持久化(localStorage, 仅存 role/content, 刷新不丢) ——
  useEffect(() => {
    try {
      const slim = messages
        .filter(m => m.id !== 'welcome')
        .map(m => ({ role: m.role, content: m.content }))
      localStorage.setItem(SESSION_KEY, JSON.stringify(slim))
    } catch {
      /* localStorage 不可用时静默降级为内存会话 */
    }
  }, [messages])

  // —— 自动滚动到底部 ——
  useEffect(() => {
    listEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, pending, assistantOpen])

  // —— 展开时聚焦输入框 ——
  useEffect(() => {
    if (assistantOpen) setTimeout(() => inputRef.current?.focus(), 80)
  }, [assistantOpen])

  // —— Esc 收起 ——
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && assistantOpen && !streaming) setAssistantOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [assistantOpen, streaming, setAssistantOpen])

  const persist = useCallback(() => {
    setStreaming(false)
  }, [])

  const send = async () => {
    const content = input.trim()
    if (!content || streaming) return
    setInput('')

    // 中止上一次流式请求(如果还在进行)
    abortRef.current?.abort()
    abortRef.current = new AbortController()

    const userMsg: FabMessage = { id: `u_${Date.now()}`, role: 'user', content, timestamp: Date.now() }
    const aiId = `a_${Date.now()}`
    const aiMsg: FabMessage = { id: aiId, role: 'assistant', content: '', thinking: '', timestamp: Date.now() }
    setMessages(prev => [...prev, userMsg, aiMsg])
    setStreaming(true)

    // 构造多轮历史: 最近 20 条 user/assistant
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

    // 会话续接: 已有持久化会话则复用; 首次对话生成本地 ID, 后端按 ID 自动创建持久化
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
        setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, content: acc || '（无应答）' } : m)))
        // 后端确认的会话 ID(与本地一致时无需处理; 若不同则采用后端值)
        if (meta?.conversation_id && meta.conversation_id !== cid) {
          setConvId(meta.conversation_id)
          try { localStorage.setItem(CONV_ID_KEY, meta.conversation_id) } catch { /* ignore */ }
        }
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
      undefined,
      // 续接被拒(如复用了已删除/无权限的 conversation_id → 后端 403)时,清空本地会话 ID,
      // 下一句自动开新会话,避免每次对话都卡在 403。
      (msg?: string) => {
        if (msg && msg.includes('403')) {
          setConvId('')
          try { localStorage.removeItem(CONV_ID_KEY) } catch { /* ignore */ }
        }
      },
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

  // ============ 渲染 ============
  if (!assistantOpen) {
    return (
      <button
        onClick={toggleAssistant}
        className="fixed bottom-5 right-5 z-50 group flex items-center justify-center rounded-full bg-blue-600 hover:bg-blue-500 text-white shadow-lg shadow-blue-900/20 transition-all duration-300 hover:shadow-blue-600/30 hover:scale-105 h-10 w-10"
        title="打开 AI 助手"
        aria-label="打开 AI 助手"
      >
        <Bot className="h-4.5 w-4.5" />
      </button>
    )
  }

  return (
      <div
        className="fixed bottom-0 right-0 z-50 flex flex-col border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-950 shadow-2xl
                   w-full h-[100dvh] sm:w-[400px] sm:h-[620px] sm:bottom-5 sm:right-5 sm:rounded-2xl overflow-hidden"
        role="dialog"
        aria-label="AI 助手"
      >
        {/* 头部 */}
        <div className="flex items-center justify-between px-4 py-2.5 border-b border-gray-200 dark:border-gray-800 bg-gradient-to-r from-blue-600 to-teal-600 text-white shrink-0">
          <div className="flex items-center gap-2">
            <Bot className="h-4 w-4" />
          <span className="text-sm font-medium">AI 助手</span>
          <span className="text-[10px] bg-white/20 rounded px-1.5 py-0.5">任意页面可用</span>
        </div>
        <div className="flex items-center gap-1">
          <button onClick={clearSession} className="p-1.5 rounded-md hover:bg-white/20 transition-colors" title="清空会话" aria-label="清空会话">
            <Trash2 className="h-3.5 w-3.5" />
          </button>
          <button onClick={() => navigate('/ai')} className="p-1.5 rounded-md hover:bg-white/20 transition-colors" title="打开完整 AI 页面" aria-label="打开完整 AI 页面">
            <ExternalLink className="h-3.5 w-3.5" />
          </button>
          <button onClick={() => setAssistantOpen(false)} className="ml-1 p-1.5 rounded-md bg-white/15 hover:bg-white/30 transition-colors" title="收起 AI 助手" aria-label="收起 AI 助手">
            <X className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* 模式切换 + 思考开关 */}
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900/60 shrink-0">
        <div className="flex gap-1">
          {(['normal', 'explore'] as FabMode[]).map(m => (
            <button
              key={m}
              onClick={() => setMode(m)}
              className={cn(
                'px-2.5 py-1 rounded-md text-xs font-medium transition-colors',
                mode === m
                  ? 'bg-blue-100 dark:bg-blue-500/20 text-blue-700 dark:text-blue-400'
                  : 'text-muted-foreground hover:bg-gray-200/60 dark:hover:bg-gray-800',
              )}
            >
              {m === 'normal' ? '普通' : '自主'}
            </button>
          ))}
        </div>
        <button
          onClick={() => setShowThinking(v => !v)}
          className={cn(
            'px-2 py-1 rounded-md text-[11px] font-medium transition-colors flex items-center gap-1',
            showThinking
              ? 'bg-purple-100 dark:bg-purple-500/20 text-purple-700 dark:text-purple-400'
              : 'text-muted-foreground hover:bg-gray-200/60 dark:hover:bg-gray-800',
          )}
          title="显示/隐藏 AI 思考过程"
        >
          💭 思考
        </button>
      </div>

      {/* 消息区 */}
      <div className="flex-1 overflow-y-auto px-3.5 py-3 space-y-3 min-h-0">
        {messages.map(m => (
          <div key={m.id} className={cn('flex', m.role === 'user' ? 'justify-end' : 'justify-start')}>
            <div
              className={cn(
                'max-w-[85%] rounded-2xl px-3.5 py-2.5 text-[13px] leading-relaxed',
                m.role === 'user'
                  ? 'bg-blue-600 text-white rounded-br-sm'
                  : 'bg-gray-100 dark:bg-gray-800/80 text-gray-800 dark:text-gray-100 rounded-bl-sm border border-gray-200/60 dark:border-gray-700/50',
              )}
            >
              {m.role === 'assistant' && m.thinking ? (
                <details className="mb-1.5 text-[11px] text-muted-foreground">
                  <summary className="cursor-pointer select-none hover:text-foreground">💭 推理过程</summary>
                  <div className="mt-1 whitespace-pre-wrap border-t border-gray-200 dark:border-gray-700 pt-1.5 max-h-40 overflow-y-auto">
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
        <div className="mx-3 mb-2 rounded-xl border border-amber-200 dark:border-amber-500/30 bg-amber-50 dark:bg-amber-500/10 px-3 py-2.5 shrink-0">
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
            <button
              onClick={() => respond('reject')}
              disabled={approving}
              className="flex items-center gap-1 px-3 py-1 rounded-md text-xs bg-gray-200/70 dark:bg-gray-700/60 text-gray-700 dark:text-gray-200 hover:bg-gray-300/70 dark:hover:bg-gray-600/60 disabled:opacity-50 transition-colors"
            >
              <XCircle className="h-3 w-3" /> 拒绝
            </button>
            <button
              onClick={() => respond('approve')}
              disabled={approving}
              className="flex items-center gap-1 px-3 py-1 rounded-md text-xs bg-amber-500 text-white hover:bg-amber-600 disabled:opacity-50 transition-colors"
            >
              {approving ? <Loader2 className="h-3 w-3 animate-spin" /> : <CheckCircle2 className="h-3 w-3" />} 批准执行
            </button>
          </div>
        </div>
      )}

      {/* 输入区 */}
      <div className="px-3 py-2.5 border-t border-gray-200 dark:border-gray-800 shrink-0">
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
            placeholder={mode === 'explore' ? '给出目标，AI 自主规划执行…' : '输入问题，Enter 发送…'}
            className="flex-1 resize-none rounded-xl border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 px-3 py-2 text-[13px] focus:outline-none focus:ring-2 focus:ring-blue-500/50 min-h-[38px] max-h-32"
          />
          <button
            onClick={streaming ? stopGeneration : send}
            disabled={!input.trim() && !streaming}
            className="flex h-9 w-9 items-center justify-center rounded-xl bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
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
    </div>
  )
}
