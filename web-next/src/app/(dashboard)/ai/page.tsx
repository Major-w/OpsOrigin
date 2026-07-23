'use client'

import { useEffect, useState, useRef } from 'react'
import { useRouter } from 'next/navigation'
import { useAIStore } from '@/stores/ai'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Bot, Send, History, Settings, Sparkles, Terminal, FileText, Server, ChevronRight, Loader2, User } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'

interface Message {
  id: string
  role: 'user' | 'assistant'
  content: string
  timestamp: Date
}

// 轻量内联 Markdown→HTML。生产中可替换为 react-markdown+remark-gfm 获得完整 GFM 支持
// (cd web-next && npm install react-markdown remark-gfm)
function renderMarkdown(text: string): string {
  let html = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
  // 代码块 ```...```
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, '<pre class="rounded bg-gray-900 text-gray-100 p-3 my-2 overflow-x-auto text-xs"><code>$2</code></pre>')
  // 行内代码 `...`
  html = html.replace(/`([^`]+)`/g, '<code class="rounded bg-gray-200 dark:bg-gray-700 px-1 text-xs">$1</code>')
  // 加粗 **...**
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  // 斜体 *...*
  html = html.replace(/\*([^*\n]+?)\*/g, '<em>$1</em>')
  // 链接 [text](url)
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a class="text-blue-500 underline" href="$2" target="_blank">$1</a>')
  // 换行
  html = html.replace(/\n/g, '<br/>')
  return html
}

const QUICK_ACTIONS = [
  { label: '查询主机状态', icon: Server, prompt: '帮我查询所有主机的运行状态' },
  { label: '执行巡检任务', icon: Terminal, prompt: '执行一次自动化巡检任务' },
  { label: '生成运维报告', icon: FileText, prompt: '生成本周的运维报告' },
  { label: '排查故障', icon: Sparkles, prompt: '帮我排查当前系统的异常情况' },
]

export default function AIPage() {
  const { agents, fetchAgents, streamChat, loadConversations } = useAIStore()
  const router = useRouter()
  const { theme } = useThemeStore()
  const [messages, setMessages] = useState<Message[]>([
    { id: '0', role: 'assistant', content: '你好！我是 OpsOrigin AI 助手，可以帮你完成运维任务。你可以直接告诉我需要什么，或者选择下方的快捷操作。', timestamp: new Date() }
  ])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [selectedAgent, setSelectedAgent] = useState<any>(null)
  const [showHistory, setShowHistory] = useState(false)
  const [conversations, setConversations] = useState<any[]>([])
  const messagesEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => { fetchAgents() }, [fetchAgents])

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

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const sendMessage = async (text: string) => {
    const content = text.trim()
    if (!content) return

    const userMsg: Message = {
      id: Date.now().toString(),
      role: 'user',
      content,
      timestamp: new Date()
    }
    const next = [...messages, userMsg]
    setMessages(next)
    setInput('')
    setLoading(true)

    // 预留 AI 消息位(流式填充)
    const aiId = (Date.now() + 1).toString()
    const aiMsg: Message = { id: aiId, role: 'assistant', content: '', timestamp: new Date() }
    setMessages(prev => [...prev, aiMsg])

    const history = next
      .filter(m => m.role === 'user' || m.role === 'assistant')
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))

    // SSE 流式接收 token
    let full = ''
    await streamChat(content, (token) => {
      full += token
      setMessages(prev => prev.map(m => m.id === aiId ? { ...m, content: full } : m))
    }, () => {
      setLoading(false)
    }, history, selectedAgent?.id)
  }

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
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={handleOpenHistory}><History className="h-4 w-4" />{showHistory ? '返回' : '历史'}</Button>
          <Button variant="outline" size="sm" onClick={() => router.push('/ai/settings')}><Settings className="h-4 w-4" />设置</Button>
        </div>
      </div>

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
              <div key={msg.id} className={cn("flex gap-3", msg.role === 'user' ? "justify-end" : "justify-start")}>
                {msg.role === 'assistant' && (
                  <div className="h-8 w-8 rounded-full bg-emerald-100 dark:bg-emerald-500/20 flex items-center justify-center shrink-0">
                    <Bot className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
                  </div>
                )}
                <div className={cn(
                  "max-w-[70%] rounded-lg px-4 py-2 text-sm",
                  msg.role === 'user'
                    ? "bg-emerald-600 text-white whitespace-pre-wrap"
                    : "bg-gray-100 dark:bg-gray-800 prose-sm max-w-none"
                )}>
                  {msg.role === 'user' ? msg.content : (
                    <div dangerouslySetInnerHTML={{ __html: renderMarkdown(msg.content) }} />
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
          </div>
        </div>

        <div className="w-64 space-y-4 shrink-0">
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">快捷操作</CardTitle>
            </CardHeader>
            <CardContent className="space-y-1">
              {QUICK_ACTIONS.map((action) => {
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
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">当前 Agent</CardTitle>
            </CardHeader>
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
                        "w-full flex items-center justify-between px-2 py-1.5 rounded text-xs text-left transition-colors",
                        selectedAgent?.id === a.id
                          ? "bg-emerald-100 dark:bg-emerald-500/20 text-emerald-700 dark:text-emerald-300"
                          : "hover:bg-gray-100 dark:hover:bg-gray-800"
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
    </div>
  )
}
