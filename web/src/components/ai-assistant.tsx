
import { useEffect, useRef, useCallback, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAIStore, useChatStore } from '@/stores/ai'
import { useAIChat, ChatMessageList, ChatInputBar, ApprovalBar, SHARED_SESSION_KEY, SHARED_CONV_ID_KEY, type ChatMessage } from '@/components/ai/chat-kernel'
import { Bot, X, Trash2, ExternalLink } from 'lucide-react'
import { Popconfirm } from 'antd'
import { getMessage } from '@/lib/message'
import { t, useLocale } from '@/lib/i18n'

// ============================================================================
// 全局悬浮 AI 助手
// 挂载于 Layout, 所有 (dashboard) 页面可见。收起态为右下角悬浮钮,
// 点击展开浮窗(桌面 400×600 / 移动端全屏), 复用 chat-kernel 全链路。
// ============================================================================

export default function AIAssistant() {
  const navigate = useNavigate()
  const assistantOpen = useAIStore(s => s.assistantOpen)
  const toggleAssistant = useAIStore(s => s.toggleAssistant)
  const setAssistantOpen = useAIStore(s => s.setAssistantOpen)
  const locale = useLocale()

  const WELCOME = useMemo(() => t('ai.welcome'), [locale])

  const chat = useAIChat({
    welcome: WELCOME,
    welcomeId: 'welcome',
    persistKey: SHARED_SESSION_KEY,
    convIdKey: SHARED_CONV_ID_KEY,
    enableTeam: false,
    captureToolCalls: true,
    enableAbort: true,
    resetConvOn403: true,
    defaultMode: 'normal',
  })

  const { messages, input, setInput, streaming, send, stopGeneration, pending, approving, respond, clearSession, regenerate, editAndResend } = chat

  const submitFeedback = useChatStore(s => s.submitFeedback)

  const retryMessage = useCallback((errMsg: ChatMessage, modelOverride?: string) => {
    const idx = messages.findIndex(m => m.id === errMsg.id)
    if (idx < 0) return
    for (let i = idx - 1; i >= 0; i--) {
      if (messages[i].role === 'user') { send(messages[i].content, undefined, modelOverride); return }
    }
  }, [messages, send])

  const handleFeedback = useCallback(async (msg: ChatMessage, rating: 'up' | 'down') => {
    try {
      await submitFeedback({ message_id: msg.messageId || msg.id, rating })
    } catch { /* ignore */ }
  }, [submitFeedback])

  const listEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (assistantOpen) setTimeout(() => inputRef.current?.focus(), 80)
  }, [assistantOpen])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // Ctrl/Cmd + Shift + A: 打开/关闭 AI 助手
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'A') {
        e.preventDefault()
        toggleAssistant()
        return
      }
      if (e.key === 'Escape' && assistantOpen && !streaming) setAssistantOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [assistantOpen, streaming, setAssistantOpen, toggleAssistant])

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
      className="fixed bottom-0 right-0 z-50 flex flex-col border border-border bg-background shadow-2xl
                   w-full h-[100dvh] sm:w-[400px] sm:h-[620px] sm:bottom-5 sm:right-5 sm:rounded-2xl overflow-hidden"
      role="dialog"
      aria-label="AI 助手"
    >
      {/* 头部 */}
      <div className="flex items-center justify-between px-4 py-2.5 border-b border-border bg-gradient-to-r from-blue-600 to-teal-600 text-white shrink-0">
        <div className="flex items-center gap-2">
          <Bot className="h-4 w-4" />
          <span className="text-sm font-medium">{t('ai.assistant')}</span>
          <span className="text-[10px] bg-white/20 rounded px-1.5 py-0.5">{t('ai.floating.anyPage')}</span>
        </div>
        <div className="flex items-center gap-1">
          <Popconfirm
            title={t('ai.floating.clearConfirm')}
            description={t('ai.floating.clearDescription')}
            onConfirm={clearSession}
            okText="确认"
            cancelText="取消"
          >
            <button className="p-1.5 rounded-md hover:bg-white/20 transition-colors" title={t('ai.floating.clear')} aria-label={t('ai.floating.clear')}>
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          </Popconfirm>
          <button onClick={() => navigate('/ai')} className="p-1.5 rounded-md hover:bg-white/20 transition-colors" title={t('ai.floating.openFullPage')} aria-label={t('ai.floating.openFullPage')}>
            <ExternalLink className="h-3.5 w-3.5" />
          </button>
          <button onClick={() => setAssistantOpen(false)} className="ml-1 p-1.5 rounded-md bg-white/15 hover:bg-white/30 transition-colors" title={t('ai.floating.close')} aria-label={t('ai.floating.close')}>
            <X className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* 消息区 */}
      <ChatMessageList
        messages={messages}
        welcomeId="welcome"
        variant="floating"
        enableCitations={false}
        enableFeedback
        enableTaskList
        loading={streaming}
        loadingText={t('ai.floating.thinking')}
        onSuggestion={send}
        onRetry={retryMessage}
        onRegenerate={regenerate}
        onEditResend={editAndResend}
        onFeedback={handleFeedback}
        endRef={listEndRef}
        className="px-3.5 py-3"
      />

      {/* 审批确认条 */}
      <ApprovalBar pending={pending} approving={approving} onRespond={respond} variant="floating" />

      {/* 输入区(浮窗恒为默认助手, 执行者/模型等选择在完整 AI 页) */}
      <ChatInputBar
        value={input}
        onChange={setInput}
        onSend={() => send(input)}
        onStop={stopGeneration}
        streaming={streaming}
        variant="floating"
      />
    </div>
  )
}
