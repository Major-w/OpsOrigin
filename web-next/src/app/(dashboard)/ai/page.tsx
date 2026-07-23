'use client'

import { useEffect, useState, useRef } from 'react'
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

const QUICK_ACTIONS = [
  { label: '查询主机状态', icon: Server, prompt: '帮我查询所有主机的运行状态' },
  { label: '执行巡检任务', icon: Terminal, prompt: '执行一次自动化巡检任务' },
  { label: '生成运维报告', icon: FileText, prompt: '生成本周的运维报告' },
  { label: '排查故障', icon: Sparkles, prompt: '帮我排查当前系统的异常情况' },
]

export default function AIPage() {
  const { agents, fetchAgents, chat } = useAIStore()
  const { theme } = useThemeStore()
  const [messages, setMessages] = useState<Message[]>([
    { id: '0', role: 'assistant', content: '你好！我是 OpsOrigin AI 助手，可以帮你完成运维任务。你可以直接告诉我需要什么，或者选择下方的快捷操作。', timestamp: new Date() }
  ])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => { fetchAgents() }, [fetchAgents])

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

    // 透传最近对话历史给后端,保持多轮连贯(最多最近 20 条)
    const history = next
      .filter(m => m.role === 'user' || m.role === 'assistant')
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))

    const reply = await chat(content, history)
    setLoading(false)
    if (reply == null) return // 错误已由 store 内 toast 提示

    const aiMsg: Message = {
      id: (Date.now() + 1).toString(),
      role: 'assistant',
      content: reply,
      timestamp: new Date()
    }
    setMessages(prev => [...prev, aiMsg])
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
          <Button variant="outline" size="sm"><History className="h-4 w-4" />历史</Button>
          <Button variant="outline" size="sm"><Settings className="h-4 w-4" />设置</Button>
        </div>
      </div>

      <div className="flex-1 flex gap-4 min-h-0">
        <div className="flex-1 flex flex-col border rounded-lg overflow-hidden">
          <div className="flex-1 overflow-y-auto p-4 space-y-4">
            {messages.map(msg => (
              <div key={msg.id} className={cn("flex gap-3", msg.role === 'user' ? "justify-end" : "justify-start")}>
                {msg.role === 'assistant' && (
                  <div className="h-8 w-8 rounded-full bg-emerald-100 dark:bg-emerald-500/20 flex items-center justify-center shrink-0">
                    <Bot className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
                  </div>
                )}
                <div className={cn(
                  "max-w-[70%] rounded-lg px-4 py-2 text-sm whitespace-pre-wrap",
                  msg.role === 'user'
                    ? "bg-emerald-600 text-white"
                    : "bg-gray-100 dark:bg-gray-800"
                )}>
                  {msg.content}
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
                <div className="space-y-2">
                  {agents.slice(0, 3).map((a: any) => (
                    <div key={a.id} className="flex items-center justify-between">
                      <span className="text-xs truncate">{a.name}</span>
                      <Badge variant={a.status === 'active' ? 'default' : 'secondary'} className="text-[10px]">{a.status}</Badge>
                    </div>
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
