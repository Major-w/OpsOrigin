
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAIStore } from '@/stores/ai'
import { useKnowledgeStore } from '@/stores/knowledge'
import { useCan } from '@/stores/permissions'
import { useAIChat, ChatMessageList, ChatInputBar, TeamChatView, ChatContextBar, ConversationList, estimateContextTokens, type ChatMessage } from '@/components/ai/chat-kernel'
import { Button, Input, Space, Modal, Popconfirm } from 'antd'
import {
  Sparkles, History, Settings, BarChart3, Share2, Lock, PanelLeftClose, PanelLeftOpen,
  AlertTriangle, XCircle, CheckCircle, Loader2, Brain, Bookmark, Download, Save, Terminal, Paperclip,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { apiFetchRaw } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'
import { getMessage } from '@/lib/message'

// ============================================================================
// AI 助手（全页 /ai）
// 复用 chat-kernel 的 useAIChat / ChatMessageList / ChatInputBar / TeamChatView,
// 保留本页独有的: 左侧会话列表(可收起) / 分享 / 删除 / 会话产物(对象存储) /
// 工作区(工具产物) / 上下文用量条 / 只读共享权限门禁 / 记忆·知识库·下载·归档 消息操作。
// ============================================================================

const QUICK_ACTIONS = [
  { label: '查询主机状态', icon: Sparkles, prompt: '帮我查询所有主机的运行状态' },
  { label: '执行巡检任务', icon: Terminal, prompt: '执行一次自动化巡检任务' },
  { label: '生成运维报告', icon: Sparkles, prompt: '生成本周的运维报告' },
  { label: '排查故障', icon: Sparkles, prompt: '帮我排查当前系统的异常情况' },
]

const EXPLORE_EXAMPLES = [
  '检查所有主机的运行状态，找出异常主机并给出处理建议',
  '梳理当前 CMDB 中数据库资产的拓扑关系',
  '分析近期告警，归类并给出根因分析',
].map((p) => ({ label: p, prompt: p }))

// 欢迎词(改这里即可调整 AI 助手首屏文案)
const WELCOME_MESSAGE = '你好！我是 OpsOrigin AI 助手，可以帮你完成运维任务。\n\n👉 **快速开始：** 点击右上角「设置」→「模型」配置 LLM API Key 即可使用。\n\n配置完成后可直接在下方提问，或点击下方快捷操作开始。'

export default function AIPage() {
  const { loadConversations, submitFeedback: submitFB, fetchAgents } = useAIStore()
  const { theme } = useThemeStore()
  const navigate = useNavigate()
  const canUseAI = useCan('ai:tool:exec')

  // 历史压缩: 最近 20 条, 超预算从最新往前裁剪(与 chat-kernel 默认一致, 这里保留 HIST_BUDGET 行为)
  const buildHistory = (msgs: ChatMessage[]): { role: string; content: string }[] => {
    const base = msgs
      .filter(m => m.id !== '0' && (m.role === 'user' || m.role === 'assistant'))
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))
    const HIST_BUDGET = 24000
    let histTokens = base.reduce((n, m) => n + Math.ceil((m.content?.length || 0) / 2), 0)
    if (histTokens > HIST_BUDGET && base.length > 4) {
      const trimmed: typeof base = []
      let acc = 0
      for (let i = base.length - 1; i >= 0; i--) {
        const t = Math.ceil((base[i].content?.length || 0) / 2)
        if (acc + t > HIST_BUDGET && trimmed.length > 0) break
        trimmed.unshift(base[i])
        acc += t
      }
      if (base.length - trimmed.length > 0) {
        return [{ role: 'assistant', content: `⚠️ 上下文过长，更早的 ${base.length - trimmed.length} 条消息已自动省略。` }, ...trimmed]
      }
    }
    return base
  }

  const chat = useAIChat({
    welcome: WELCOME_MESSAGE,
    welcomeId: '0',
    enableTeam: true,
    captureToolCalls: true,
    enableAbort: false,
    resetConvOn403: false,
    showThinkingKey: 'ai.showThinking',
    buildHistory,
    defaultMode: 'normal',
    enableKnowledgeScope: true,
  })

  const {
    mode, setMode, showThinking, toggleThinking, selectedModel, setSelectedModel,
    selectedAgent, setSelectedAgent, models, agents, messages, setMessages, input, setInput,
    streaming, send, clearSession, pending, approving, respond, dismissApproval, convId, setConvId,
    selectedTeamId, setSelectedTeamId, teamTurns, teamInput, setTeamInput, teamStreaming, handleTeamSend,
    teams, teamsLoading,
    knowledgeCategories, setKnowledgeCategories,
  } = chat

  // 知识范围选择器选项(挂载时加载分类列表)
  const knowledgeCategoryOptions = useKnowledgeStore(s => s.categories)
  useEffect(() => { useKnowledgeStore.getState().fetchCategories() }, [])
  // 数字员工(Agent)列表: 供对话页临时选择绑定(普通/探索模式)
  useEffect(() => { fetchAgents() }, [])

  // —— 本页独有状态 ——
  const [showHistory, setShowHistory] = useState(false)
  const [conversations, setConversations] = useState<any[]>([])
  const [sidebarOpen, setSidebarOpen] = useState<boolean>(() => {
    if (typeof window === 'undefined') return true
    return localStorage.getItem('ai.sidebarOpen') !== 'false'
  })
  const [convPerm, setConvPerm] = useState<'owner' | 'edit' | 'view' | ''>('')
  const [shareConv, setShareConv] = useState<{ id: string; title: string } | null>(null)
  const [shareForm, setShareForm] = useState({ target_type: 'user', target_id: '', permission: 'view' })
  const [artifacts, setArtifacts] = useState<{ id: string; message_id: string; file_name: string; file_type: string; file_size: number; created_at: string }[]>([])
  const [workspaceFiles, setWorkspaceFiles] = useState<{ id: string; tool_name: string; file_name: string; file_size: number; created_at: string }[]>([])
  const [pendingConvId, setPendingConvId] = useState<string | null>(null)
  const [pendingArtifactId, setPendingArtifactId] = useState<string | null>(null)
  const [pendingWsFileId, setPendingWsFileId] = useState<string | null>(null)

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const teamMessagesEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (mode === 'normal') loadConversations(selectedAgent?.id).then(setConversations)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, selectedAgent])
  useEffect(() => { messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [messages])
  useEffect(() => { teamMessagesEndRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [teamTurns])
  useEffect(() => { loadArtifacts(convId); loadWorkspace(convId) }, [convId])

  // ----- 会话产物 / 工作区 -----
  const authHeaders = (): Record<string, string> => {
    const token = localStorage.getItem('opsorigin_token')
    return token ? { Authorization: `Bearer ${token}` } : {}
  }
  const loadArtifacts = async (id: string) => {
    if (!id) { setArtifacts([]); return }
    try {
      const r = await fetch(`/api/v1/ai/conversations/${id}/artifacts`, { headers: authHeaders() })
      if (r.ok) setArtifacts(await r.json())
    } catch {}
  }
  const loadWorkspace = async (id: string) => {
    if (!id) { setWorkspaceFiles([]); return }
    try {
      const r = await fetch(`/api/v1/ai/conversations/${id}/workspace`, { headers: authHeaders() })
      if (r.ok) setWorkspaceFiles(await r.json())
    } catch {}
  }

  // ----- 会话列表操作 -----
  const handleOpenHistory = async () => {
    if (!showHistory) {
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
    }
    setShowHistory(!showHistory)
  }
  const handleLoadConversation = async (id: string) => {
    try {
      const resp = await apiFetchRaw(`/ai/conversations/${id}`)
      if (!resp.ok) return
      const conv = await resp.json()
      setConvPerm(conv?.permission === 'view' ? 'view' : conv?.permission === 'edit' ? 'edit' : conv?.permission === 'owner' ? 'owner' : '')
      if (conv?.messages) {
        const msgs: ChatMessage[] = conv.messages.map((m: any, i: number) => ({
          id: `${id}-${i}`,
          role: m.role,
          content: m.content,
          timestamp: Date.now(),
        }))
        setMessages(msgs)
      }
      setConvId(id)
      setShowHistory(false)
    } catch {}
  }
  const handleNewConversation = () => {
    clearSession()
    setConvPerm('')
    setShowHistory(false)
  }
  const handleDeleteConversation = async () => {
    const id = pendingConvId
    if (!id) return
    try {
      const token = localStorage.getItem('opsorigin_token')
      const resp = await fetch(`${window.location.origin}/api/v1/ai/conversations/${id}`, {
        method: 'DELETE',
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
      if (!resp.ok) throw new Error(`删除失败(${resp.status})`)
      getMessage().success('会话已删除')
      if (id === convId) {
        clearSession()
        setConvPerm('')
      }
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
    } catch (e: any) {
      getMessage().error('删除失败: ' + e.message)
    }
    setPendingConvId(null)
  }

  // ----- 消息操作: 反馈 / 记忆 / 知识库 / 下载 / 归档 -----
  const submitFeedback = async (msg: ChatMessage, rating: 'up' | 'down' = 'up') => {
    await submitFB({ message_id: msg.messageId || msg.id, rating, conversation_id: convId || undefined })
  }
  const saveMsgToMemory = async (msg: ChatMessage) => {
    if (!msg.content.trim()) return
    const idx = messages.findIndex(m => m.id === msg.id)
    let problem = '对话要点'
    for (let i = idx - 1; i >= 0; i--) {
      if (messages[i].role === 'user') { problem = messages[i].content.replace(/\s+/g, ' ').slice(0, 40) || '对话要点'; break }
    }
    try {
      const r = await fetch('/api/v1/ai/memories', {
        method: 'POST', headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ agent_id: selectedAgent?.id || '', category: '对话要点', problem, resolution: msg.content.slice(0, 500) }),
      })
      if (!r.ok) throw new Error(`保存失败(${r.status})`)
      getMessage().success('已记住（长期记忆，重启不丢）')
    } catch (e: any) { getMessage().error('保存失败: ' + e.message) }
  }
  const saveMsgToKnowledge = async (msg: ChatMessage) => {
    if (!msg.content.trim()) return
    const title = msg.content.replace(/[#*`>\-\n\r]+/g, ' ').replace(/\s+/g, ' ').trim().slice(0, 40) || 'AI 对话沉淀'
    try {
      const r = await fetch('/api/v1/knowledge', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, content: msg.content, category: 'AI 对话沉淀', tags: ['ai', 'conversation'], status: 'published' }),
      })
      if (!r.ok) throw new Error(`保存失败(${r.status})`)
      getMessage().success('已保存到知识库（分类「AI 对话沉淀」）')
    } catch (e: any) { getMessage().error('保存失败: ' + e.message) }
  }
  const downloadMsgAsMd = (msg: ChatMessage) => {
    if (!msg.content.trim()) return
    const blob = new Blob([msg.content], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `ai-${msg.id}.md`
    a.click()
    URL.revokeObjectURL(url)
  }
  const saveMsgAsArtifact = async (msg: ChatMessage) => {
    if (!msg.content.trim()) return
    if (!convId) { getMessage().error('请先新建或选择对话会话'); return }
    try {
      const r = await fetch(`/api/v1/ai/conversations/${convId}/artifacts`, {
        method: 'POST', headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ message_id: msg.id, file_name: `ai-${msg.id}.md`, content: msg.content }),
      })
      if (!r.ok) throw new Error(`保存失败(${r.status})`)
      getMessage().success('已归档到对象存储（本会话产物区可下载）')
      loadArtifacts(convId)
    } catch (e: any) { getMessage().error('归档失败: ' + e.message) }
  }
  const downloadArtifact = async (aid: string) => {
    try {
      const r = await apiFetchRaw(`/ai/conversations/${convId}/artifacts/${aid}`)
      if (!r.ok) throw new Error(`下载失败(${r.status})`)
      const blob = await r.blob()
      const cd = r.headers.get('Content-Disposition') || ''
      const name = cd.match(/filename="?([^";]+)/)?.[1] || `artifact-${aid}.md`
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = decodeURIComponent(name)
      a.click()
      URL.revokeObjectURL(url)
    } catch (e: any) { getMessage().error('下载失败: ' + e.message) }
  }
  const deleteArtifact = async () => {
    const aid = pendingArtifactId
    if (!aid) return
    try {
      await fetch(`/api/v1/ai/conversations/${convId}/artifacts/${aid}`, { method: 'DELETE', headers: authHeaders() })
      getMessage().success('已删除')
      loadArtifacts(convId)
    } catch (e: any) { getMessage().error('删除失败: ' + e.message) }
    setPendingArtifactId(null)
  }
  const downloadWorkspaceFile = async (fid: string) => {
    try {
      const r = await apiFetchRaw(`/ai/conversations/${convId}/workspace/${fid}`)
      if (!r.ok) throw new Error(`下载失败(${r.status})`)
      const blob = await r.blob()
      const cd = r.headers.get('Content-Disposition') || ''
      const name = cd.match(/filename="?([^";]+)/)?.[1] || `workspace-${fid}.json`
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = decodeURIComponent(name)
      a.click()
      URL.revokeObjectURL(url)
    } catch (e: any) { getMessage().error('下载失败: ' + e.message) }
  }
  const deleteWorkspaceFile = async () => {
    const fid = pendingWsFileId
    if (!fid) return
    try {
      await fetch(`/api/v1/ai/conversations/${convId}/workspace/${fid}`, { method: 'DELETE', headers: authHeaders() })
      getMessage().success('已删除')
      loadWorkspace(convId)
    } catch (e: any) { getMessage().error('删除失败: ' + e.message) }
    setPendingWsFileId(null)
  }

  const retryMessage = (errMsg: ChatMessage) => {
    const idx = messages.findIndex(m => m.id === errMsg.id)
    if (idx < 0) return
    for (let i = idx - 1; i >= 0; i--) {
      if (messages[i].role === 'user') { send(messages[i].content); return }
    }
  }

  const saveShare = async () => {
    if (!shareConv || !shareForm.target_id.trim()) { getMessage().error('请填写目标用户/组 ID'); return }
    try {
      const r = await fetch(`/api/v1/ai/conversations/${shareConv.id}/shares`, {
        method: 'POST', headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify(shareForm),
      })
      if (!r.ok) throw new Error(`共享失败(${r.status})`)
      getMessage().success(shareForm.permission === 'edit' ? '已共享（可继续对话）' : '已共享（仅查看）')
      setShareConv(null)
      setShareForm({ target_type: 'user', target_id: '', permission: 'view' })
    } catch (e: any) { getMessage().error('共享失败: ' + e.message) }
  }

  // 上下文用量估算
  const estTokens = messages.reduce((n, m) => n + Math.ceil((m.content?.length || 0) / 2), 0)
  const ctxPct = Math.min(100, Math.round((estTokens / 32000) * 100))

  // 消息额外操作(记忆 / 知识库 / 下载 / 归档)
  const renderMsgActions = (msg: ChatMessage) => (
    <>
      <button onClick={() => saveMsgToMemory(msg)} className="p-1 rounded text-[10px] text-muted-foreground hover:text-violet-500 transition-colors" title="记住（存长期记忆，跨会话复用）">
        <Brain className="h-3 w-3" />
      </button>
      <button onClick={() => saveMsgToKnowledge(msg)} className="p-1 rounded text-[10px] text-muted-foreground hover:text-amber-500 transition-colors" title="保存到知识库">
        <Bookmark className="h-3 w-3" />
      </button>
      <button onClick={() => downloadMsgAsMd(msg)} className="p-1 rounded text-[10px] text-muted-foreground hover:text-blue-500 transition-colors" title="下载为 Markdown">
        <Download className="h-3 w-3" />
      </button>
      <button onClick={() => saveMsgAsArtifact(msg)} className="p-1 rounded text-[10px] text-muted-foreground hover:text-indigo-500 transition-colors" title="归档到对象存储">
        <Save className="h-3 w-3" />
      </button>
    </>
  )

  // 输入区底部: 上下文用量 + 工作区 + 产物
  const inputFooter = (
    <>
      <div className="flex items-center gap-2">
        <div className="flex-1 h-1 rounded-full bg-muted overflow-hidden">
          <div className={cn('h-full rounded-full transition-all', ctxPct > 85 ? 'bg-red-500' : ctxPct > 60 ? 'bg-amber-500' : 'bg-blue-500')} style={{ width: `${ctxPct}%` }} />
        </div>
        <p className="text-[10px] text-muted-foreground whitespace-nowrap">
          {estTokens >= 1000 ? `≈ ${(estTokens / 1000).toFixed(1)}k tokens` : `≈ ${estTokens} tokens`}
          {ctxPct >= 85 ? ' · 上下文偏满，建议新开对话' : ''}
        </p>
      </div>
      {workspaceFiles.length > 0 && (
        <div className="rounded-lg border border-violet-200 dark:border-violet-500/30 p-2 space-y-1">
          <p className="text-[10px] text-muted-foreground flex items-center gap-1"><Terminal className="h-3 w-3" />会话工作区（{workspaceFiles.length}）— 工具产物自动沉淀</p>
          {workspaceFiles.map(f => (
            <div key={f.id} className="flex items-center justify-between gap-2 text-xs">
              <span className="truncate font-mono text-[11px]">{f.file_name}</span>
              <span className="text-[10px] text-muted-foreground shrink-0">{f.tool_name} · {(f.file_size / 1024).toFixed(1)} KB</span>
              <span className="flex gap-2 shrink-0">
                <button onClick={() => downloadWorkspaceFile(f.id)} className="text-[10px] text-blue-500 hover:underline">下载</button>
                <button onClick={() => setPendingWsFileId(f.id)} className="text-[10px] text-red-400 hover:underline">删除</button>
              </span>
            </div>
          ))}
        </div>
      )}
      {artifacts.length > 0 && (
        <div className="rounded-lg border border-border p-2 space-y-1">
          <p className="text-[10px] text-muted-foreground flex items-center gap-1"><Paperclip className="h-3 w-3" />本会话产物（{artifacts.length}）— 已归档对象存储</p>
          {artifacts.map(a => (
            <div key={a.id} className="flex items-center justify-between gap-2 text-xs">
              <span className="truncate font-medium">{a.file_name}</span>
              <span className="text-[10px] text-muted-foreground shrink-0">{(a.file_size / 1024).toFixed(1)} KB</span>
              <span className="flex gap-2 shrink-0">
                <button onClick={() => downloadArtifact(a.id)} className="text-[10px] text-blue-500 hover:underline">下载</button>
                <button onClick={() => setPendingArtifactId(a.id)} className="text-[10px] text-red-400 hover:underline">删除</button>
              </span>
            </div>
          ))}
        </div>
      )}
    </>
  )

  return (
    <div className="p-6 h-[calc(100vh-120px)] flex flex-col">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2"><Sparkles className="h-6 w-6 text-blue-500" />AI 助手</h1>
          <p className="text-sm text-muted-foreground">智能运维助手，帮你高效完成运维任务</p>
        </div>
        <div className="flex items-center gap-2">
          {mode === 'normal' && (<Button size="middle" onClick={() => navigate('/ai/analytics')}><BarChart3 className="h-4 w-4" />分析</Button>)}
          <Button size="middle" onClick={() => navigate('/ai/settings')}><Settings className="h-4 w-4" />设置</Button>
        </div>
      </div>

      {/* 普通 / 自主 模式：左会话列表(可收起) + 全宽对话区 */}
      {mode !== 'team' && (
        <div className="flex-1 flex gap-4 min-h-0">
          {sidebarOpen ? (
            <div className="w-64 shrink-0 border rounded-lg overflow-hidden flex flex-col">
              <div className="p-3 border-b flex items-center justify-between">
                <span className="text-sm font-medium flex items-center gap-1.5"><History className="h-4 w-4 text-blue-500" />对话列表</span>
                <button onClick={() => { setSidebarOpen(false); localStorage.setItem('ai.sidebarOpen', 'false') }} className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors" title="收起会话列表">
                  <PanelLeftClose className="h-4 w-4" />
                </button>
              </div>
              <div className="flex-1 overflow-y-auto p-2 space-y-1">
                <ConversationList
                  conversations={conversations}
                  activeId={convId}
                  onSelect={handleLoadConversation}
                  onNew={handleNewConversation}
                  onDelete={(id) => setPendingConvId(id)}
                  onShare={(c) => { setShareConv({ id: c.id, title: c.title || '新对话' }); setShareForm({ target_type: 'user', target_id: '', permission: 'view' }) }}
                />
              </div>
            </div>
          ) : (
            <button onClick={() => { setSidebarOpen(true); localStorage.setItem('ai.sidebarOpen', 'true') }} className="w-9 shrink-0 border rounded-lg flex flex-col items-center justify-center gap-1 text-muted-foreground hover:text-foreground hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors" title="展开会话列表">
              <PanelLeftOpen className="h-4 w-4" />
              <span className="text-[10px] [writing-mode:vertical-rl]">对话</span>
            </button>
          )}

          <div className="flex-1 flex flex-col border rounded-lg overflow-hidden min-w-0">
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
              variant="page"
              onConfigure={() => navigate('/ai/settings')}
            />
            <ChatMessageList
              messages={messages}
              mode={mode}
              welcomeId="0"
              variant="page"
              enableCitations
              enableFeedback
              enableTaskList
              onFeedback={submitFeedback}
              onRetry={retryMessage}
              extraActions={renderMsgActions}
              loading={streaming}
              loadingText="思考中..."
              emptyQuickActions={
                (mode === 'normal' ? QUICK_ACTIONS : EXPLORE_EXAMPLES).map(q => (
                  <button key={q.label} onClick={() => send(q.prompt)} className="flex flex-col items-start gap-1.5 px-3 py-2.5 rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.08)] hover:border-blue-300 hover:bg-blue-50/50 dark:hover:border-blue-500/40 dark:hover:bg-blue-500/10 transition-colors text-left">
                    <Sparkles className="h-4 w-4 text-blue-500" />
                    <span className="text-xs">{q.label}</span>
                  </button>
                ))
              }
              endRef={messagesEndRef}
              className="p-4"
            />

            <ChatInputBar
              value={input}
              onChange={setInput}
              onSend={() => send(input)}
              streaming={streaming}
              disabled={!canUseAI || convPerm === 'view'}
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
              exploreHint
              enableKnowledgeScope
              knowledgeCategories={knowledgeCategories}
              onKnowledgeCategoriesChange={setKnowledgeCategories}
              knowledgeCategoryOptions={knowledgeCategoryOptions}
              variant="page"
              inputFooter={inputFooter}
            />

            {!canUseAI && (
              <p className="text-[10px] text-amber-600 dark:text-amber-400 flex items-center gap-1 px-4 pb-2">
                <AlertTriangle className="h-3 w-3" />无 AI 工具执行权限，仅可查看历史对话。请联系管理员在角色中授予 ai:tool:exec。
              </p>
            )}
            {canUseAI && convPerm === 'view' && (
              <p className="text-[10px] text-violet-600 dark:text-violet-400 flex items-center gap-1 px-4 pb-2">
                <Lock className="h-3 w-3" />此会话由他人共享（仅查看）。如需继续对话，请向属主申请「可编辑」权限。
              </p>
            )}
          </div>
        </div>
      )}

      {/* 团队模式 */}
      {mode === 'team' && (
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
          onManageTeams={() => navigate('/ai/teams')}
          variant="page"
          endRef={teamMessagesEndRef}
        />
      )}

      {/* 敏感操作审批弹窗 */}
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
              {pending.args && pending.args !== '{}' && (
                <div><span className="text-muted-foreground text-xs">参数：</span><pre className="mt-1 p-2 rounded bg-gray-100 dark:bg-gray-800 text-[11px] overflow-x-auto whitespace-pre-wrap max-h-28 overflow-y-auto">{pending.args}</pre></div>
              )}
              <div className="text-[11px] text-muted-foreground">截止：{pending.deadline ? new Date(pending.deadline).toLocaleTimeString('zh-CN') : '-'}</div>
            </div>
          </div>
        )}
      </Modal>

      {/* 会话共享弹窗 */}
      <Modal open={!!shareConv} onCancel={() => setShareConv(null)} title={`分享会话：${shareConv?.title || ''}`} footer={
        <Space>
          <Button onClick={() => setShareConv(null)}>取消</Button>
          <Button type="primary" onClick={saveShare}><Share2 className="h-4 w-4 mr-1" />共享</Button>
        </Space>
      }>
        <div className="space-y-3 py-1">
          <p className="text-xs text-muted-foreground">默认对话仅自己可见；共享后指定用户/组成员可见。目标 ID 为用户名或用户组 ID。</p>
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">目标类型</p>
              <select value={shareForm.target_type} onChange={e => setShareForm({ ...shareForm, target_type: e.target.value })} className="w-full h-9 rounded-md border border-input bg-background px-2 text-sm">
                <option value="user">用户</option>
                <option value="group">用户组</option>
              </select>
            </div>
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">权限</p>
              <select value={shareForm.permission} onChange={e => setShareForm({ ...shareForm, permission: e.target.value })} className="w-full h-9 rounded-md border border-input bg-background px-2 text-sm">
                <option value="view">仅查看</option>
                <option value="edit">可继续对话</option>
              </select>
            </div>
          </div>
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">目标 ID（用户名 / 用户组 ID）</p>
            <Input className="h-9 text-xs font-mono" placeholder="如: zhangsan 或 group-001" value={shareForm.target_id} onChange={e => setShareForm({ ...shareForm, target_id: e.target.value })} />
          </div>
        </div>
      </Modal>

      {/* 删除确认弹窗 */}
      <Popconfirm title="删除会话" description="删除该会话？此操作不可恢复。" onConfirm={handleDeleteConversation} okText="删除" cancelText="取消" open={!!pendingConvId} onCancel={() => setPendingConvId(null)}><span /></Popconfirm>
      <Popconfirm title="删除产物文件" description="删除该产物文件？" onConfirm={deleteArtifact} okText="删除" cancelText="取消" open={!!pendingArtifactId} onCancel={() => setPendingArtifactId(null)}><span /></Popconfirm>
      <Popconfirm title="删除工作区文件" description="删除该工作区文件？" onConfirm={deleteWorkspaceFile} okText="删除" cancelText="取消" open={!!pendingWsFileId} onCancel={() => setPendingWsFileId(null)}><span /></Popconfirm>
    </div>
  )
}
