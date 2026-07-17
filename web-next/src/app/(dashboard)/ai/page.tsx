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
  const { agents, fetchAgents } = useAIStore()
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

  const sendMessage = (text: string) => {
    if (!text.trim()) return

    const userMsg: Message = {
      id: Date.now().toString(),
      role: 'user',
      content: text,
      timestamp: new Date()
    }
    setMessages(prev => [...prev, userMsg])
    setInput('')
    setLoading(true)

    setTimeout(() => {
      const aiMsg: Message = {
        id: (Date.now() + 1).toString(),
        role: 'assistant',
        content: getAIResponse(text),
        timestamp: new Date()
      }
      setMessages(prev => [...prev, aiMsg])
      setLoading(false)
    }, 1000 + Math.random() * 1500)
  }

  const getAIResponse = (input: string): string => {
    const lower = input.toLowerCase()
    if (lower.includes('主机') || lower.includes('状态')) {
      return '已为你查询主机状态：\n\n• web-server-01: 运行中 (CPU 23%, 内存 45%)\n• web-server-02: 运行中 (CPU 18%, 内存 38%)\n• db-master: 运行中 (CPU 67%, 内存 82%)\n• db-slave-01: 运行中 (CPU 12%, 内存 35%)\n\n所有主机运行正常，db-master 内存使用率较高，建议关注。'
    }
    if (lower.includes('巡检')) {
      return '正在执行自动化巡检任务...\n\n✓ 检查系统服务状态 - 正常\n✓ 检查磁盘使用率 - 正常\n✓ 检查网络连通性 - 正常\n✓ 检查安全更新 - 发现 3 个待更新\n\n巡检完成，未发现严重问题。建议尽快安装安全更新。'
    }
    if (lower.includes('报告') || lower.includes('运维')) {
      return '已为你生成本周运维报告，主要内容：\n\n📊 总览\n• 系统可用率: 99.95%\n• 处理工单: 47 个\n• 执行脚本: 128 次\n• 巡检次数: 7 次\n\n⚠️ 告警\n• 告警总数: 12 次\n• 已处理: 11 次\n• 待处理: 1 次\n\n报告已保存到「运维报告」目录。'
    }
    if (lower.includes('故障') || lower.includes('排查')) {
      return '正在排查系统异常...\n\n🔍 检查服务状态... 正常\n🔍 检查网络连接... 正常\n🔍 检查磁盘 I/O... 发现异常\n🔍 检查日志... 发现错误\n\n📋 排查结果：\n发现 db-master 磁盘 I/O 偏高，日志中出现多次慢查询告警。建议：\n1. 检查数据库慢查询日志\n2. 考虑优化索引\n3. 临时增加磁盘缓存'
    }
    return `收到你的请求：「${input}」\n\n我已记录并正在处理中。如果需要进一步操作，请告诉我。`
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
