// ============================================================================
// AI 代码助手（专属入口 /ai/coding）
// 复用 chat-kernel 的 useAIChat / ChatMessageList / ChatInputBar, 与 /ai 的差异:
//   - 挂载即预选 coding-assistant 预设智能体(若不存在则走默认助手)
//   - 右侧常驻「本次会话文件变更」树(SessionFileChanges), 点击定位到对话中的具体操作
//   - 顶部安全横幅: 沙箱隔离 + 写操作/危险命令需人工批准
//   - 工具调用卡片走 coding-tool-card 富视图(行内 diff / 终端 / 文件预览 / 友好拦截态)
// ============================================================================

import { useEffect, useMemo, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useChatStore, useAgentAIStore } from '@/stores/ai'
import { useCan } from '@/stores/permissions'
import { useThemeStore } from '@/stores/theme'
import {
  useAIChat, ChatMessageList, ChatInputBar, type ChatMessage,
} from '@/components/ai/chat-kernel'
import { SessionFileChanges, CodingToolCard } from '@/components/ai/coding-tool-card'
import { Modal, Button, Space } from 'antd'
import { Code2, ShieldCheck, Sparkles, Bot, Loader2, CheckCircle, XCircle, AlertTriangle } from 'lucide-react'
import { cn } from '@/lib/utils'
import { t, useLocale } from '@/lib/i18n'

export default function AICodingPage() {
  const fetchAgents = useAgentAIStore(s => s.fetchAgents)
  const agents = useAgentAIStore(s => s.agents)
  const canUseAI = useCan('ai:tool:exec')
  const { theme } = useThemeStore()
  const locale = useLocale()
  const navigate = useNavigate()

  const WELCOME = useMemo(() => t('ai.coding.welcome'), [locale])

  const chat = useAIChat({
    welcome: WELCOME,
    welcomeId: 'coding-0',
    // 跨路由切换续接: 消息与后端会话 ID 落 localStorage
    persistKey: 'ai.coding.messages',
    convIdKey: 'ai.coding.convId',
    captureToolCalls: true,
    enableAbort: true,
    enableTeam: false,
    defaultMode: 'normal',
    enableKnowledgeScope: false,
  })

  const {
    mode, selectedAgent, setSelectedAgent, messages, input, setInput,
    streaming, send, stopGeneration,
    models, selectedModel, setSelectedModel,
    pending, approving, respond, dismissApproval,
  } = chat

  // 挂载即预选 coding-assistant 预设智能体(存在时); 同时拉取智能体列表
  useEffect(() => { fetchAgents() }, [fetchAgents])
  useEffect(() => {
    if (agents.length && !selectedAgent) {
      const ca = agents.find(a => a.id === 'coding-assistant' || (a.name || '').toLowerCase().includes('coding'))
      if (ca) setSelectedAgent(ca)
    }
  }, [agents, selectedAgent, setSelectedAgent])

  // 聚合本会话所有 coding_* 工具调用 → 文件变更树
  const codingToolCalls = useMemo(
    () => messages.flatMap(m => m.toolCalls || []).filter(tc => (tc.name || '').startsWith('coding_')),
    [messages],
  )

  const messagesEndRef = useRef<HTMLDivElement>(null)

  const modelOptions = useMemo(
    () => models.filter(m => m.healthy !== false).map(m => ({ value: m.model, label: m.name || m.model })),
    [models],
  )

  return (
    <div className={cn('p-3 md:p-6 flex flex-col bg-background', 'h-[calc(100vh-45px)]')}>
      <div className="flex items-center justify-between mb-3 gap-2">
        <div className="min-w-0">
          <h1 className="text-xl font-bold flex items-center gap-2">
            <Code2 className="h-5 w-5 text-blue-500 shrink-0" />{t('ai.coding')}
          </h1>
          <p className="text-xs text-muted-foreground mt-0.5 flex items-center gap-1.5">
            <ShieldCheck className="h-3.5 w-3.5 text-green-500" />
            {selectedAgent ? `已接入智能体：${selectedAgent.name}` : '代码助手（沙箱隔离 + 写操作需人工批准）'}
          </p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <Button size="middle" onClick={() => navigate('/ai/settings')}><Bot className="h-4 w-4" /><span className="max-sm:hidden">智能体</span></Button>
          <Button size="middle" onClick={() => navigate('/ai/models')}><Sparkles className="h-4 w-4" /><span className="max-sm:hidden">模型</span></Button>
        </div>
      </div>

      {/* 安全横幅 */}
      <div className={cn(
        'flex items-start gap-2 text-[11px] rounded-lg border px-3 py-2 mb-3',
        theme === 'dark' ? 'bg-blue-500/5 border-blue-500/15 text-blue-200' : 'bg-blue-50 border-blue-200 text-blue-700',
      )}>
        <ShieldCheck className="h-4 w-4 text-blue-500 shrink-0 mt-0.5" />
        <span>
          代码助手在<strong>沙箱目录</strong>内读写文件、执行命令；<strong>写操作与危险命令需人工批准</strong>，
          越界或高危操作会被自动拦截。所有改动不会离开你的工作区。
        </span>
      </div>

      <div className="flex-1 flex gap-4 min-h-0">
        {/* 对话区 */}
        <div className="flex-1 flex flex-col border rounded-lg overflow-hidden min-w-0">
          <ChatMessageList
            messages={messages}
            mode={mode}
            welcomeId="coding-0"
            variant="page"
            enableTaskList
            modelOptions={modelOptions}
            onSuggestion={(tt) => { if (!streaming && canUseAI) send(tt) }}
            loading={streaming}
            loadingText="思考中..."
            endRef={messagesEndRef}
            className="p-4"
          />
          <ChatInputBar
            value={input}
            onChange={setInput}
            onSend={() => send(input)}
            onStop={stopGeneration}
            streaming={streaming}
            disabled={!canUseAI}
            variant="page"
          />
          {!canUseAI && (
            <p className="text-[10px] text-amber-600 dark:text-amber-400 flex items-center gap-1 px-4 pb-2">
              <AlertTriangle className="h-3 w-3" />无 AI 工具执行权限，请联系管理员在角色中授予 ai:tool:exec。
            </p>
          )}
        </div>

        {/* 变更文件树（窄屏隐藏） */}
        <div className="w-72 shrink-0 border rounded-lg overflow-hidden flex flex-col max-md:hidden">
          <div className="p-3 border-b flex items-center gap-1.5 text-sm font-medium">
            <Code2 className="h-4 w-4 text-blue-500" />本次会话文件变更
          </div>
          <div className="flex-1 overflow-y-auto p-2">
            <SessionFileChanges toolCalls={codingToolCalls} />
          </div>
        </div>
      </div>

      {/* 敏感操作审批弹窗（与全页一致） */}
      <Modal open={!!pending} onCancel={() => { if (!approving) dismissApproval() }} title="🔐 敏感操作确认" footer={
        <Space>
          <Button onClick={() => respond('reject')} disabled={approving} danger><XCircle className="h-4 w-4 mr-1" />拒绝</Button>
          <Button type="primary" onClick={() => respond('approve')} disabled={approving}>
            {approving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <CheckCircle className="h-4 w-4 mr-1" />}批准执行
          </Button>
        </Space>
      }>
        {pending && (
          <div className="space-y-3">
            <div className={cn("flex items-center gap-2 text-xs p-3 rounded-lg", theme === 'dark' ? 'bg-amber-500/5 border border-amber-500/10' : 'bg-amber-50 border border-amber-200')}>
              <AlertTriangle className="h-4 w-4 text-amber-500 shrink-0" />
              <span>AI 请求执行敏感操作，请确认后放行。超时未响应将自动拒绝。</span>
            </div>
            <div className="grid gap-2 text-sm">
              <div><span className="text-muted-foreground text-xs">操作：</span><code className="font-mono text-xs bg-gray-100 dark:bg-gray-800 rounded px-1.5 py-0.5">{pending.tool}</code></div>
              <div><span className="text-muted-foreground text-xs">说明：</span><span className="text-sm">{pending.summary || '无'}</span></div>
              {pending.tool.startsWith('coding_') ? (
                <div>
                  <span className="text-muted-foreground text-xs">改动预览：</span>
                  <div className="mt-1">
                    <CodingToolCard toolCall={{ id: 'pending', name: pending.tool, args: pending.args || '', result: '', status: 'running' }} />
                  </div>
                </div>
              ) : pending.args && pending.args !== '{}' ? (
                <div><span className="text-muted-foreground text-xs">参数：</span><pre className="mt-1 p-2 rounded bg-gray-100 dark:bg-gray-800 text-[11px] overflow-x-auto whitespace-pre-wrap max-h-28 overflow-y-auto">{pending.args}</pre></div>
              ) : null}
              <div className="text-[11px] text-muted-foreground">截止：{pending.deadline ? new Date(pending.deadline).toLocaleTimeString('zh-CN') : '-'}</div>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
