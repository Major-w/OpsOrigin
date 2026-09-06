
import { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAIStore } from '@/stores/ai'
import { useAIChat, ChatMessageList, ChatInputBar, ApprovalBar, SHARED_SESSION_KEY, SHARED_CONV_ID_KEY } from '@/components/ai/chat-kernel'
import { Bot, X, Trash2, ExternalLink } from 'lucide-react'

// ============================================================================
// 全局悬浮 AI 助手
// 挂载于 Layout, 所有 (dashboard) 页面可见。收起态为右下角悬浮钮,
// 点击展开浮窗(桌面 400×600 / 移动端全屏), 复用 chat-kernel 全链路。
// ============================================================================

const WELCOME = '你好！我是 OpsOrigin AI 助手 👋\n\n在任意页面都可以随时找我：\n- **普通模式** — 快速问答、查询主机状态\n- **自主模式** — 给出目标，AI 自主规划并调用工具执行\n\n直接提问开始吧。'

export default function AIAssistant() {
  const navigate = useNavigate()
  const assistantOpen = useAIStore(s => s.assistantOpen)
  const toggleAssistant = useAIStore(s => s.toggleAssistant)
  const setAssistantOpen = useAIStore(s => s.setAssistantOpen)

  const chat = useAIChat({
    welcome: WELCOME,
    welcomeId: 'welcome',
    persistKey: SHARED_SESSION_KEY,
    convIdKey: SHARED_CONV_ID_KEY,
    enableTeam: false,
    captureToolCalls: false,
    enableAbort: true,
    resetConvOn403: true,
    defaultMode: 'normal',
  })

  const { messages, input, setInput, streaming, send, stopGeneration, pending, approving, respond, clearSession } = chat

  const listEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (assistantOpen) setTimeout(() => inputRef.current?.focus(), 80)
  }, [assistantOpen])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && assistantOpen && !streaming) setAssistantOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [assistantOpen, streaming, setAssistantOpen])

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

      {/* 消息区 */}
      <ChatMessageList
        messages={messages}
        welcomeId="welcome"
        variant="floating"
        enableCitations={false}
        enableFeedback={false}
        loading={streaming}
        loadingText="AI 正在思考…"
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
